package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

var pipelineFoundationRepairHost = headless.Run

type pipelineFoundationRepairPlan struct {
	Manifest       pipelineFoundationRepairManifest
	Digest         string
	ExplicitPrompt string
	State          domain.PipelineState
	Source         []byte
}

func pipelineFoundationRepairConfig(opts cliOptions) (bootstrap.Config, error) {
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return cfg, err
	}
	err = normalizeOutputAndRAGForInvocation(&cfg, opts.Dir, hasConfiguredRAGQdrantCollection(opts))
	return cfg, err
}

func runPipelineFoundationRepair(opts cliOptions, flags pipelineFlags, prompt string) (returnErr error) {
	if !flags.RefreshArchitect || flags.ArchitectTarget == "" || flags.Stages != "architect" || flags.InitializeOnly || flags.NewNovel || flags.OutlineRepairFile != "" {
		return fmt.Errorf("Architect repair requires only the explicit targeted architect stage")
	}
	m, digest, err := loadPipelineFoundationRepairManifest(flags.ArchitectRepairFile)
	if err != nil {
		return err
	}
	if m.Target != flags.ArchitectTarget {
		return fmt.Errorf("Architect repair manifest target differs from --architect-target")
	}
	plan := &pipelineFoundationRepairPlan{Manifest: m, Digest: digest, ExplicitPrompt: prompt}
	if flags.RebaseAllChapters {
		return pipelineRebaseAllChaptersWithFoundationRepair(opts, plan)
	}
	cfg, err := pipelineFoundationRepairConfig(opts)
	if err != nil {
		return err
	}
	live := cfg.OutputDir
	release, err := acquirePipelineOutlineAllControl(live, true)
	if err != nil {
		return err
	}
	defer func() {
		if err := release(); returnErr == nil {
			returnErr = err
		}
	}()
	if err := recoverAllDirectoryPublishesWithControlHeld(live); err != nil {
		return err
	}
	if err := plan.preflight(live); err != nil {
		return err
	}
	if err := tools.RequireChapterZeroFoundationRefreshState(store.NewStore(live)); err != nil {
		return err
	}
	sourceRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	candidate := filepath.Join(pipelineRebaseCandidateRoot(live), "foundation-repair-"+stamp, "output")
	if err := validatePipelineFoundationRepairCopyTarget(live, candidate); err != nil {
		return err
	}
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		return err
	}
	if err := preservePipelineRebaseFoundationModTimes(live, candidate); err != nil {
		return err
	}
	if err := plan.prepare(cfg, live, candidate); err != nil {
		return fmt.Errorf("Architect repair rejected; live unchanged, candidate retained at %s: %w", candidate, err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineRebaseTransactionRoot(live))
	id := "foundation-repair-" + strings.TrimPrefix(digest, "sha256:")[:24] + "-" + stamp
	var receipt *store.DirectoryPublishReceipt
	err = withPipelineWatchdogPaused(func() error {
		var err error
		receipt, err = publisher.PublishDirectory(store.PublishDirectoryRequest{TransactionID: id, LiveDir: live, CandidateDir: candidate, ExpectedLiveRoot: sourceRoot})
		return err
	})
	if err != nil {
		return err
	}
	if receipt == nil || receipt.CandidateRoot != receipt.CommittedLiveRoot {
		return fmt.Errorf("Architect repair publish receipt is incomplete")
	}
	return publisher.FinalizeDirectoryPublish(id)
}

// The existing copier rejects source symlinks; this new COW entry also checks
// its destination before the first mkdir/write, not merely at publish time.
func validatePipelineFoundationRepairCopyTarget(live, target string) error {
	var root string
	for _, candidateRoot := range []string{pipelineRebaseCandidateRoot(live), filepath.Join(pipelineRebaseRunRoot(live), "archives")} {
		if pathContainsPipelineRenderCandidate(candidateRoot, target) {
			root = candidateRoot
			break
		}
	}
	if root == "" {
		return fmt.Errorf("Architect repair candidate is outside its existing rebase namespace")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Architect repair candidate namespace must contain only real directories")
		}
	}
	return fmt.Errorf("Architect repair refuses an already existing candidate destination")
}

func pipelineFoundationRepairSource(target string) string {
	if target == "update_compass" {
		return "meta/compass.json"
	}
	return "characters.json"
}

func pipelineFoundationRepairSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (p *pipelineFoundationRepairPlan) preflight(live string) error {
	st := store.NewStore(live)
	if lock, err := st.Runtime.InspectPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf("Architect repair refuses an active execution lock")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil || progress.LatestCompleted() != 0 || len(progress.CompletedChapters) != 0 || progress.TotalWordCount != 0 || len(progress.PendingRewrites) != 0 || progress.TotalChapters < 1 || progress.TotalChapters > 16 {
		return fmt.Errorf("Architect repair requires an unaccepted chapter-zero short book")
	}
	// A timer, even an armed-only one, belongs to a detailed generation. This
	// source repair cannot retire it or manufacture a new original start.
	for _, rel := range []string{"meta/runtime/chapter_delivery/ledger.json", "meta/planning/v2"} {
		if _, err := os.Lstat(filepath.Join(live, rel)); err == nil {
			return fmt.Errorf("Architect repair refuses any detailed generation or chapter delivery ledger")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if entries, err := os.ReadDir(filepath.Join(pipelineRebaseRunRoot(live), ".project-all")); err != nil && !os.IsNotExist(err) {
		return err
	} else if len(entries) > 0 {
		return fmt.Errorf("Architect repair refuses existing detailed workspaces")
	}
	if err := readPipelinePlanningJSON(filepath.Join(live, "meta/pipeline.json"), &p.State); err != nil {
		return err
	}
	if strings.TrimSpace(p.State.Prompt) == "" {
		return fmt.Errorf("Architect repair requires the original captured author prompt")
	}
	if p.ExplicitPrompt != "" && p.ExplicitPrompt != p.State.Prompt {
		return fmt.Errorf("Architect repair may not replace the original pipeline Prompt; put temporary instructions only in its manifest")
	}
	p.Source, err = os.ReadFile(filepath.Join(live, pipelineFoundationRepairSource(p.Manifest.Target)))
	if err != nil {
		return err
	}
	if pipelineFoundationRepairSHA(p.Source) != p.Manifest.ExpectedSourceDigest {
		return fmt.Errorf("Architect repair source digest changed")
	}
	report, input, err := st.LoadVerifiedArcRehearsal(p.Manifest.ReportDigest)
	if err != nil {
		return err
	}
	if report == nil {
		// After the first repair, the exact old report lives only in the
		// authenticated explicit rebase archive. Never search arbitrary paths.
		if err := st.ValidateRebasedChapterZeroFoundationRefresh(); err != nil {
			return fmt.Errorf("Architect repair report absent and no verified rebase archive: %w", err)
		}
		var receipt pipelineAllChapterRebaseReceipt
		if err := readPipelinePlanningJSON(filepath.Join(live, "meta/all_chapter_rebase.json"), &receipt); err != nil {
			return err
		}
		report, input, err = store.NewStore(receipt.ArchiveOutput).LoadVerifiedArcRehearsal(p.Manifest.ReportDigest)
		if err != nil {
			return err
		}
	}
	if report == nil || input == nil || report.ReadyForDetail || report.BaseCanonChapter != 0 || report.ArcFirstChapter != 1 || report.ArcLastChapter != progress.TotalChapters || input.SourceFiles[pipelineFoundationRepairSource(p.Manifest.Target)] != p.Manifest.ExpectedSourceDigest {
		return fmt.Errorf("Architect repair requires a verified non-ready whole-book report bound to this exact source")
	}
	for _, added := range p.Manifest.NewResources {
		for _, existing := range input.WorldState.Resources {
			if existing.ResourceID == added.ResourceID {
				return fmt.Errorf("Architect repair new resource ID already exists in the verified world source")
			}
		}
	}
	return nil
}

var pipelineFoundationRepairProtected = []string{
	"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json",
	"outline.json", "layered_outline.json", "meta/compass.json", "meta/user_rules.json", "meta/author_sources.json",
	"meta/pipeline_timings.jsonl", "meta/progress.json",
}

func (p *pipelineFoundationRepairPlan) prepare(cfg bootstrap.Config, live, candidate string) (returnErr error) {
	started := time.Now()
	defer func() {
		status := "ok"
		if returnErr != nil {
			status = "error"
		}
		recordPipelineStageTiming(candidate, p.Digest, "foundation-repair", "architect-repair-"+p.Manifest.Target, started, status, returnErr)
	}()
	target := pipelineFoundationRepairSource(p.Manifest.Target)
	ragBefore, err := pipelineRebaseRAGAuthorityRoot(candidate)
	if err != nil {
		return err
	}
	before := map[string][]byte{}
	for _, rel := range pipelineFoundationRepairProtected {
		raw, err := os.ReadFile(filepath.Join(candidate, rel))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		before[rel] = raw
	}
	if !bytes.Equal(before[target], p.Source) {
		return fmt.Errorf("Architect repair candidate source differs from its original binding")
	}
	st := store.NewStore(candidate)
	var authorCatalog *domain.AuthorSourcesV1
	oldJournal, err := os.ReadFile(filepath.Join(candidate, store.UsageAuditPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.Manifest.Target == "update_compass" {
		catalog, err := st.LoadAuthorSources()
		if err != nil {
			return err
		}
		if catalog == nil {
			value, err := domain.FinalizeAuthorSourcesV1(domain.AuthorSourcesV1{Policy: domain.AuthorSourcesPolicyV1, Sources: []domain.AuthorSourceV1{{ID: "startup_prompt", Text: p.State.Prompt}}})
			if err != nil {
				return err
			}
			if err := st.SaveAuthorSources(value); err != nil {
				return err
			}
			catalog = &value
		}
		authorCatalog = catalog
		if _, err := domain.MaterializeCompassAuthorContractsV1(domain.StoryCompass{AuthorContracts: &domain.CompassAuthorContractsV1{Policy: catalog.Policy, SourcesDigest: catalog.Digest, Refs: p.Manifest.RequiredAuthorRefs}}, *catalog); err != nil {
			return fmt.Errorf("Architect repair required original author references: %w", err)
		}
		// Exact new catalog bytes are frozen before the model, and remain
		// protected from the temporary repair instruction.
		before[store.AuthorSourcesPath], _ = os.ReadFile(filepath.Join(candidate, store.AuthorSourcesPath))
	}
	cfg.OutputDir = candidate
	cfg.DisableLiveRAG = true
	bundle, _ := assets.LoadWithOverrides(cfg.Style, assets.DefaultPromptOverrideDirs()...)
	base := "[Host-only one-off source repair; not an author requirement]\n" + p.Manifest.Instruction + "\n[Exact allowed JSON leaves]\n" + strings.Join(p.Manifest.AllowedJSONPointers, "\n") + "\n[Original author contract, read-only]\n" + p.State.Prompt
	if authorCatalog != nil {
		raw, err := json.Marshal(authorCatalog)
		if err != nil {
			return err
		}
		base += "\n[Verified original author source catalog; cite whole paragraphs only, never the host instruction]\n" + string(raw)
		required, err := json.Marshal(p.Manifest.RequiredAuthorRefs)
		if err != nil {
			return err
		}
		base += "\n[Operator-required original author paragraph references; every one must remain in author_contracts.refs]\n" + string(required)
	}
	if len(p.Manifest.NewResources) > 0 {
		raw, err := json.Marshal(p.Manifest.NewResources)
		if err != nil {
			return err
		}
		base += "\n[Only these new initial material/tool resources are authorized; Architect must define finite physical stock and units from the creative setting, no new historical records]\n" + string(raw)
	}
	targets, err := pipelineArchitectShortSelectedTargets(p.Manifest.Target)
	if err != nil {
		return err
	}
	opts := pipelineArchitectRefreshHeadlessOptions(pipelineArchitectShortRefreshTargetPrompt(base, targets[0]), targets[0].Artifacts, p.Manifest.Target, false)
	opts.SkipQueueReplay = true
	opts.DisableLiveRAG = true
	opts.DeferFoundationFinalization = true
	invocationID := newPipelineTimingInvocationID(time.Now())
	if err := withPipelineStageWatchdog(pipelineWatchdogConfig{
		OutputDir: candidate, InvocationID: pipelineWatchdogStageInvocationID(p.Digest, invocationID, "architect"),
		RunIdentity: p.Digest, Stage: "architect", Chapter: 1,
	}, func() error {
		return runPipelineFoundationStageAtOutput(candidate, "architect", func() error {
			if err := pipelineWatchdogProgress(pipelineWatchdogEventStageDispatched); err != nil {
				return err
			}
			if err := pipelineFoundationRepairHost(cfg, bundle, opts); err != nil {
				return err
			}
			return pipelineWatchdogProgress(pipelineWatchdogEventStageExecutionCompleted)
		})
	}); err != nil {
		return err
	}
	st = store.NewStore(candidate)
	ragAfter, err := pipelineRebaseRAGAuthorityRoot(candidate)
	if err != nil {
		return err
	}
	if ragBefore != ragAfter {
		return fmt.Errorf("Architect repair changed frozen retrieval authority before the formal rebuild")
	}
	after, err := os.ReadFile(filepath.Join(candidate, target))
	if err != nil {
		return err
	}
	if err := validatePipelineFoundationRepairChange(p.Manifest, p.Source, after); err != nil {
		return err
	}
	for _, rel := range pipelineFoundationRepairProtected {
		if rel == target {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(candidate, rel))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !bytes.Equal(raw, before[rel]) {
			return fmt.Errorf("Architect repair changed protected source %s", rel)
		}
	}
	journal, err := os.ReadFile(filepath.Join(candidate, store.UsageAuditPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !bytes.HasPrefix(journal, oldJournal) {
		return fmt.Errorf("Architect repair lost existing usage audit history")
	}
	// Validate complete typed source and resulting source-bound compass before
	// publishing; a valid field mask alone does not prove a valid foundation.
	if p.Manifest.Target == "update_compass" {
		compass, err := st.Outline.LoadCompass()
		if err != nil {
			return err
		}
		if err := validatePipelineRepairRequiredAuthorRefs(p.Manifest.RequiredAuthorRefs, compass); err != nil {
			return err
		}
	} else {
		chars, err := st.Characters.Load()
		if err != nil {
			return err
		}
		for _, ch := range chars {
			if err := domain.ValidateCharacterInitialState(ch); err != nil {
				return err
			}
		}
		registry, err := st.CharacterAgents.LoadRegistry()
		if err != nil {
			return err
		}
		if registry == nil {
			registry = &domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
			for _, ch := range chars {
				updated, _, err := registry.UpsertCharacter(ch.Name, ch.Aliases, ch.Tier, 0, "")
				if err != nil {
					return err
				}
				*registry = updated
			}
		}
		if _, err := domain.BuildWorldPhysicalStateFromInitialV2(chars, *registry); err != nil {
			return fmt.Errorf("Architect repair shared initial resources: %w", err)
		}
	}
	checkpoint := st.Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep(p.Manifest.Target))
	want, err := tools.FoundationRefreshArtifactsDigest(candidate, p.Manifest.Target)
	if err != nil {
		return err
	}
	if checkpoint == nil || checkpoint.Digest != want {
		return fmt.Errorf("Architect repair lacks its exact successful foundation refresh epoch")
	}
	// Do not claim downstream success. Only the original creative text survives
	// in the fresh graph; the one-off instruction remains host audit evidence.
	state := domain.PipelineState{Stages: append([]string(nil), p.State.Stages...), Prompt: p.State.Prompt, InputDigest: p.State.InputDigest}
	if err := savePipelineState(filepath.Join(candidate, "meta/pipeline.json"), &state); err != nil {
		return err
	}
	record := struct {
		Version            string                           `json:"version"`
		Manifest           pipelineFoundationRepairManifest `json:"manifest"`
		ManifestDigest     string                           `json:"manifest_digest"`
		SourceBefore       string                           `json:"source_before"`
		SourceAfter        string                           `json:"source_after"`
		AuthorPromptDigest string                           `json:"author_prompt_digest"`
	}{pipelineFoundationRepairVersion, p.Manifest, p.Digest, p.Manifest.ExpectedSourceDigest, pipelineFoundationRepairSHA(after), pipelineFoundationRepairSHA([]byte(p.State.Prompt))}
	_, err = writePipelinePlanningJSON(filepath.Join(candidate, "meta/foundation_repairs", strings.TrimPrefix(p.Digest, "sha256:")+".json"), record)
	return err
}

func validatePipelineRepairRequiredAuthorRefs(required []domain.AuthorSourceParagraphRefV1, compass *domain.StoryCompass) error {
	if compass == nil || compass.AuthorContracts == nil {
		return fmt.Errorf("Architect repair requires a verified author contract binding")
	}
	actual := map[domain.AuthorSourceParagraphRefV1]bool{}
	for _, ref := range compass.AuthorContracts.Refs {
		actual[ref] = true
	}
	for _, ref := range required {
		if !actual[ref] {
			return fmt.Errorf("Architect repair omitted required original author paragraph %s[%d]", ref.SourceID, ref.Paragraph)
		}
	}
	return nil
}
