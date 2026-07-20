package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const pipelineProjectAllPlanningContextPath = "meta/project_all_state.json"
const pipelineProjectAllWorkspaceManifestPath = "meta/project_all_workspace_manifest.json"

type pipelineProjectAllWorkspaceManifest struct {
	Version                string `json:"version"`
	GenerationID           string `json:"generation_id"`
	SourceOutput           string `json:"source_output"`
	BaseChapter            int    `json:"base_chapter"`
	Workspace              string `json:"workspace"`
	InitializedAt          string `json:"initialized_at"`
	IsolatedWrites         bool   `json:"isolated_writes"`
	FoundationSnapshotRoot string `json:"foundation_snapshot_root"`
	RAGSnapshotRoot        string `json:"rag_snapshot_root"`
}

func pipelineProjectAllWorkspacePath(outputDir, generationID string) string {
	runDir := filepath.Dir(filepath.Dir(filepath.Clean(outputDir)))
	return filepath.Join(runDir, ".project-all", generationID, "output", "novel")
}

func preparePipelineProjectAllWorkspace(
	liveOutputDir string,
	generationID string,
	baseChapter int,
	restart bool,
) (string, error) {
	workspace := pipelineProjectAllWorkspacePath(liveOutputDir, generationID)
	if restart {
		if _, err := os.Stat(workspace); err == nil {
			generationRoot := filepath.Dir(filepath.Dir(workspace))
			retired := filepath.Join(
				filepath.Dir(generationRoot),
				"retired-"+generationID+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"),
			)
			if err := os.Rename(generationRoot, retired); err != nil {
				return "", fmt.Errorf("retire project-all workspace: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if _, err := os.Stat(workspace); err == nil {
		if err := validatePipelineProjectAllWorkspaceManifest(
			workspace,
			liveOutputDir,
			generationID,
			baseChapter,
		); err != nil {
			return "", fmt.Errorf("project-all workspace identity invalid; rerun with --restart: %w", err)
		}
		st := store.NewStore(workspace)
		if err := repairPipelineProjectAllWorldDeltaVisibility(
			st,
			generationID,
			baseChapter,
		); err != nil {
			return "", fmt.Errorf("repair project-all world-delta visibility: %w", err)
		}
		return workspace, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := copyProjectAllWorkspace(liveOutputDir, workspace); err != nil {
		return "", err
	}
	if err := sanitizePipelineProjectAllWorkspace(workspace); err != nil {
		return "", err
	}
	if err := materializeProjectAllOutline(liveOutputDir, workspace); err != nil {
		return "", err
	}
	st := store.NewStore(workspace)
	if err := st.Init(); err != nil {
		return "", err
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return "", fmt.Errorf("project-all workspace missing progress: %w", err)
	}
	actual := make([]int, 0, len(progress.CompletedChapters))
	for _, chapter := range progress.CompletedChapters {
		if chapter <= baseChapter {
			actual = append(actual, chapter)
		}
	}
	sort.Ints(actual)
	progress.CompletedChapters = actual
	progress.PendingRewrites = nil
	progress.RewriteReason = ""
	progress.ReopenedFromComplete = false
	progress.Phase = domain.PhaseWriting
	progress.Flow = domain.FlowWriting
	progress.CurrentChapter = baseChapter + 1
	progress.InProgressChapter = 0
	progress.CompletedScenes = nil
	if err := st.Progress.Save(progress); err != nil {
		return "", fmt.Errorf("initialize project-all shadow progress: %w", err)
	}
	if err := savePipelineProjectAllWorkspaceManifest(
		workspace,
		liveOutputDir,
		generationID,
		baseChapter,
	); err != nil {
		return "", err
	}
	return workspace, nil
}

func savePipelineProjectAllWorkspaceManifest(
	workspace, liveOutputDir, generationID string,
	baseChapter int,
) error {
	foundationSnapshotRoot, err := pipelineProjectAllFoundationSnapshotRoot(liveOutputDir)
	if err != nil {
		return fmt.Errorf("hash project-all foundation snapshot: %w", err)
	}
	workspaceFoundationRoot, err := pipelineProjectAllFoundationSnapshotRoot(workspace)
	if err != nil {
		return fmt.Errorf("hash copied project-all foundation snapshot: %w", err)
	}
	if workspaceFoundationRoot != foundationSnapshotRoot {
		return fmt.Errorf("project-all foundation changed while workspace was copied")
	}
	ragSnapshotRoot, err := pipelineProjectAllRAGSnapshotRoot(workspace)
	if err != nil {
		return fmt.Errorf("hash project-all RAG snapshot: %w", err)
	}
	manifest := pipelineProjectAllWorkspaceManifest{
		Version:                "project-all-workspace.v3",
		GenerationID:           strings.TrimSpace(generationID),
		SourceOutput:           filepath.Clean(liveOutputDir),
		BaseChapter:            baseChapter,
		Workspace:              filepath.Clean(workspace),
		InitializedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		IsolatedWrites:         true,
		FoundationSnapshotRoot: foundationSnapshotRoot,
		RAGSnapshotRoot:        ragSnapshotRoot,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := atomicWriteRewriteFile(
		filepath.Join(workspace, filepath.FromSlash(pipelineProjectAllWorkspaceManifestPath)),
		raw,
		0o644,
	); err != nil {
		return fmt.Errorf("save project-all workspace manifest: %w", err)
	}
	return nil
}

func validatePipelineProjectAllWorkspaceManifest(
	workspace, liveOutputDir, generationID string,
	baseChapter int,
) error {
	manifest, err := loadPipelineProjectAllWorkspaceManifest(workspace)
	if err != nil {
		return err
	}
	foundationSnapshotRoot, err := pipelineProjectAllFoundationSnapshotRoot(liveOutputDir)
	if err != nil {
		return err
	}
	ragSnapshotRoot, err := pipelineProjectAllRAGSnapshotRoot(workspace)
	if err != nil {
		return err
	}
	if manifest.Version != "project-all-workspace.v3" ||
		manifest.GenerationID != strings.TrimSpace(generationID) ||
		manifest.SourceOutput != filepath.Clean(liveOutputDir) ||
		manifest.BaseChapter != baseChapter ||
		manifest.Workspace != filepath.Clean(workspace) ||
		!manifest.IsolatedWrites ||
		manifest.FoundationSnapshotRoot != foundationSnapshotRoot ||
		manifest.RAGSnapshotRoot != ragSnapshotRoot {
		return fmt.Errorf("workspace manifest does not match generation/source/base")
	}
	return nil
}

func loadPipelineProjectAllWorkspaceManifest(
	workspace string,
) (pipelineProjectAllWorkspaceManifest, error) {
	var manifest pipelineProjectAllWorkspaceManifest
	raw, err := os.ReadFile(
		filepath.Join(workspace, filepath.FromSlash(pipelineProjectAllWorkspaceManifestPath)),
	)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func pipelineProjectAllRAGSnapshotRoot(outputDir string) (string, error) {
	artifacts := make(map[string]string)
	for _, rel := range []string{
		"meta/rag/index_state.json",
		"meta/rag/vector_store.json",
	} {
		digest, err := pipelineOptionalFileSHA(outputDir, rel)
		if err != nil {
			return "", err
		}
		if digest != "" {
			artifacts[rel] = digest
		}
	}
	return pipelineProjectAllDigest(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{
		Version:   "project-all-rag-snapshot.v2",
		Artifacts: artifacts,
	}), nil
}

// pipelineProjectAllFoundationSnapshotRoot binds every live baseline artifact
// that the project-all ContextTool or its server-side validators can consume.
// It is captured before the shadow starts advancing. Mutable shadow outputs are
// never folded back into this root; later promotion uses the root persisted in
// PlanningSourceSnapshotV2 rather than rehashing live-growing ledgers.
func pipelineProjectAllFoundationSnapshotRoot(outputDir string) (string, error) {
	artifacts := make(map[string]string)
	add := func(rel string) error {
		rel = filepath.ToSlash(filepath.Clean(rel))
		digest, err := pipelineOptionalFileSHA(outputDir, rel)
		if err != nil {
			return err
		}
		if digest != "" {
			artifacts[rel] = digest
		}
		return nil
	}
	for _, rel := range []string{
		"premise.md",
		"characters.json",
		"book_world.json",
		"world_codex.json",
		"world_rules.json",
		"relationship_state.initial.json",
		"relationship_state.json",
		"foreshadow_ledger.initial.json",
		"foreshadow_ledger.json",
		"timeline.json",
		"meta/compass.json",
		"meta/run.json",
		"meta/world_foundation.json",
		"meta/initial_character_dynamics.json",
		"meta/initial_resource_ledger.json",
		"meta/simulation_restart_policy.json",
		"meta/simulation_profile.json",
		"meta/crowd_role_policy.json",
		"meta/prewrite_storycraft_plan.json",
		"meta/prewrite_storycraft_plan.md",
		"references/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json",
		"meta/world_background_plan.md",
		"references/world_background_plan.md",
		"meta/zero_chapter_context_manifest.json",
		"meta/resource_ledger.json",
		"meta/cast_ledger.json",
		"meta/state_changes.json",
		"meta/chapter_progress.json",
		"meta/project_progress.json",
		"meta/character_continuity.json",
		"meta/evolution_report.json",
		"meta/world_events.jsonl",
		"meta/world_tick.json",
		"meta/offscreen_agenda.json",
		"meta/story_time_contract.json",
		"meta/story_calendar.json",
		"meta/simulation_tiers.json",
		"meta/event_weave.json",
		"meta/moral_ceiling.json",
		"meta/physics_axioms.json",
		"meta/pacing_contract.json",
		"meta/social_mood.json",
		"meta/info_graph.json",
		"meta/ritual_calendar.json",
		"meta/crowd_life.json",
		"meta/ecological_map.json",
		"meta/cosmology.json",
		"meta/cultural_footnotes.json",
		"meta/user_rules.json",
		"meta/style_rules.json",
		"meta/writing_assets.json",
		"meta/web_reference_brief.json",
		"meta/web_reference_brief.md",
		"references/web_reference_brief.md",
	} {
		if err := add(rel); err != nil {
			return "", err
		}
	}
	for _, sourceRoot := range []struct {
		path       string
		extensions map[string]bool
	}{
		{path: "meta/characters", extensions: map[string]bool{".json": true}},
		{path: "meta/volume_codex", extensions: map[string]bool{".json": true}},
		{path: "meta/snapshots", extensions: map[string]bool{".json": true}},
		{path: "meta/character_stage", extensions: map[string]bool{".json": true}},
		{path: "meta/side_character_journeys", extensions: map[string]bool{".json": true}},
		{path: "meta/chapter_world_deltas", extensions: map[string]bool{".json": true}},
		{path: "reviews", extensions: map[string]bool{
			".json": true, ".jsonl": true, ".md": true,
		}},
	} {
		base := filepath.Join(outputDir, filepath.FromSlash(sourceRoot.path))
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		if err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(outputDir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				if rel == "reviews/drafts" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("project-all foundation snapshot refuses symlink %s", rel)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("project-all foundation snapshot refuses non-regular file %s", rel)
			}
			if !sourceRoot.extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
				return nil
			}
			return add(rel)
		}); err != nil {
			return "", err
		}
	}
	return pipelineProjectAllDigest(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{
		Version:   "project-all-foundation-snapshot.v1",
		Artifacts: artifacts,
	}), nil
}

// sanitizePipelineProjectAllWorkspace removes every inference artifact that
// could make the isolated runner mistake an old live/future plan for a chapter
// produced by this pg2 generation. Canon chapters, summaries, world ledgers,
// foundation and the retrieval corpus remain; draft/simulation/checkpoint
// epochs are rebuilt from scratch inside the shadow.
func sanitizePipelineProjectAllWorkspace(workspace string) error {
	for _, rel := range []string{
		"drafts",
		filepath.Join("reviews", "drafts"),
		filepath.Join("meta", "chapter_simulations"),
		filepath.Join("meta", "checkpoints.jsonl"),
		filepath.Join("meta", "pending_commit.json"),
		filepath.Join("meta", "rag", "fact_receipts"),
		filepath.Join("meta", "planning"),
		filepath.Join("meta", "runtime"),
		filepath.Join("meta", "sessions"),
		filepath.Join("meta", "chapter_metrics"),
		filepath.Join("meta", "sampling"),
		filepath.Join("meta", "scene_dynamics"),
		filepath.Join("meta", "delivery_snapshots"),
		filepath.Join("meta", "rewrite_recovery"),
	} {
		if err := os.RemoveAll(filepath.Join(workspace, rel)); err != nil {
			return fmt.Errorf("sanitize project-all workspace %s: %w", rel, err)
		}
	}
	for _, rel := range []string{"drafts", filepath.Join("meta", "chapter_simulations")} {
		if err := os.MkdirAll(filepath.Join(workspace, rel), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func copyProjectAllWorkspace(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0o755)
		}
		slashRel := filepath.ToSlash(rel)
		if projectAllWorkspaceExcluded(slashRel, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyProjectAllFile(path, dst, info.Mode().Perm())
	})
}

func projectAllWorkspaceExcluded(rel string, isDir bool) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	for _, prefix := range []string{
		"meta/planning",
		"meta/runtime",
		"meta/quarantine",
		"meta/sessions",
		"meta/chapter_metrics",
		"meta/sampling",
		"meta/scene_dynamics",
		"meta/delivery_snapshots",
		"meta/rewrite_recovery",
		"sessions",
	} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	if !isDir {
		switch rel {
		case "meta/pipeline.json", "meta/usage.json", "meta/diag-export.md":
			return true
		}
	}
	return false
}

func copyProjectAllFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	sourceInfo, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Freshness receipts compare foundation mtimes with their generated_at
	// boundary. A byte-exact isolated copy must retain that temporal evidence;
	// otherwise every copied foundation looks newly edited and rebase/render
	// recovery fails closed for the wrong reason.
	if err := os.Chtimes(target, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		return err
	}
	ok = true
	return nil
}

// materializeProjectAllOutline expands every reserved coarse slot only inside
// the isolated workspace. The source is the stable v1 preplan payload for that
// exact global chapter number; project-all still has to turn every slot into a
// full world simulation + POV plan before seal.
func materializeProjectAllOutline(liveOutputDir, workspace string) error {
	live := store.NewStore(liveOutputDir)
	shadow := store.NewStore(workspace)
	volumes, err := live.Outline.LoadLayeredOutline()
	if err != nil {
		return err
	}
	manifests, err := live.Planning.LoadStagedChapterPlanManifests()
	if err != nil {
		return err
	}
	projected := make(map[int]domain.OutlineEntry, len(manifests))
	for i := range manifests {
		payload, err := loadAndVerifyPipelineProjectedPayload(liveOutputDir, &manifests[i])
		if err != nil {
			return err
		}
		if payload != nil {
			projected[payload.Chapter] = payload.Outline
		}
	}
	cursor := 1
	for vi := range volumes {
		for ai := range volumes[vi].Arcs {
			arc := &volumes[vi].Arcs[ai]
			span := arc.ChapterSpan()
			chapters := make([]domain.OutlineEntry, 0, span)
			for offset := 0; offset < span; offset++ {
				chapter := cursor + offset
				if entry, ok := projected[chapter]; ok {
					entry.Chapter = chapter
					chapters = append(chapters, entry)
					continue
				}
				if arc.IsExpanded() && offset < len(arc.Chapters) {
					entry := arc.Chapters[offset]
					entry.Chapter = chapter
					chapters = append(chapters, entry)
					continue
				}
				return fmt.Errorf(
					"project-all cannot materialize volume=%d arc=%d chapter=%d: stable preplan slot is missing",
					volumes[vi].Index,
					arc.Index,
					chapter,
				)
			}
			arc.Chapters = chapters
			arc.EstimatedChapters = 0
			cursor += span
		}
	}
	if err := shadow.Outline.SaveLayeredOutline(volumes); err != nil {
		return fmt.Errorf("save project-all expanded outline: %w", err)
	}
	return nil
}

// applyPipelineProjectAllObligationsToOutline makes cross-chapter promises
// visible to the production simulator/planner through the ordinary current
// chapter outline. It only mutates the isolated shadow outline. Exact tags make
// crash replay idempotent and stop a future plan from silently losing a prior
// chapter's delayed consequence.
func applyPipelineProjectAllObligationsToOutline(
	st *store.Store,
	registry domain.ObligationRegistryV2,
	predecessor *domain.ProjectedPlanningPredecessorContractV2,
	chapter int,
) (*domain.OutlineEntry, error) {
	if st == nil || chapter <= 0 {
		return nil, fmt.Errorf("project-all obligation outline requires store and chapter")
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	var additions []string
	for _, obligation := range registry.Obligations {
		if obligation.State == domain.ObligationSupersededV2 ||
			obligation.State == domain.ObligationSatisfiedV2 ||
			!containsProjectAllChapter(obligation.ConsumerChapters, chapter) {
			continue
		}
		tag := "project-all simulation-obligation"
		mode := "本章世界推演必须完成"
		if obligation.Hardness == domain.ObligationHardV2 {
			tag = "project-all hard-obligation"
			mode = "本章正式 POV 计划必须兑现"
		}
		additions = append(additions, fmt.Sprintf(
			"[%s:%s] %s：%s",
			tag,
			obligation.ID,
			mode,
			strings.TrimSpace(obligation.Contract),
		))
	}
	if predecessor != nil && predecessor.Chapter == chapter-1 &&
		strings.TrimSpace(predecessor.OutgoingConsequenceID) != "" &&
		strings.TrimSpace(predecessor.OutgoingConsequenceText) != "" {
		additions = append(additions, fmt.Sprintf(
			"[project-all predecessor-state:%s] 第%d章不可逆前态已完成：%s；本章只能推进由此前态产生的新后果、证据回看或人物反应，不得把同一状态转移重新安排为当前章现场。",
			strings.TrimSpace(predecessor.OutgoingConsequenceID),
			predecessor.Chapter,
			strings.TrimSpace(predecessor.OutgoingConsequenceText),
		))
	}
	sort.Strings(additions)
	cursor := 1
	for vi := range volumes {
		for ai := range volumes[vi].Arcs {
			arc := &volumes[vi].Arcs[ai]
			for ci := range arc.Chapters {
				globalChapter := cursor + ci
				if globalChapter != chapter {
					continue
				}
				for _, addition := range additions {
					arc.Chapters[ci].Scenes = appendUniqueProjectAllString(arc.Chapters[ci].Scenes, addition)
				}
				if len(additions) > 0 {
					if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
						return nil, err
					}
				}
				entry := arc.Chapters[ci]
				entry.Chapter = chapter
				return &entry, nil
			}
			cursor += arc.ChapterSpan()
		}
	}
	return nil, fmt.Errorf("chapter %d not found in project-all layered outline", chapter)
}

func savePipelineProjectAllPlanningContext(
	st *store.Store,
	context domain.ProjectedPlanningContextV2,
) error {
	if st == nil {
		return fmt.Errorf("project-all planning context store is nil")
	}
	if err := domain.ValidateProjectedPlanningContextV2(context); err != nil {
		return err
	}
	_, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), filepath.FromSlash(pipelineProjectAllPlanningContextPath)),
		context,
	)
	return err
}

func advancePipelineProjectAllWorkspace(
	st *store.Store,
	generationID string,
	chapter int,
	simulation *domain.ChapterWorldSimulation,
	plan *domain.ChapterPlan,
	projectedDeltas ...domain.ProjectedDelta,
) error {
	if st == nil || simulation == nil || plan == nil {
		return fmt.Errorf("advance project-all workspace requires store, simulation and plan")
	}
	characters := make([]string, 0, len(simulation.CharacterDecisions))
	keyEvents := append([]string(nil), plan.Contract.RequiredBeats...)
	if len(keyEvents) == 0 {
		keyEvents = append(keyEvents, plan.CausalSimulation.OutcomeShift...)
	}
	var stateChanges []domain.StateChange
	var timeline []domain.TimelineEvent
	var worldEvents []domain.WorldEvent
	var projectedDelta domain.ProjectedDelta
	if len(projectedDeltas) > 0 {
		projectedDelta = projectedDeltas[0]
	}
	for _, decision := range simulation.CharacterDecisions {
		if pipelineProjectAllAuthorityNoOp(decision) {
			continue
		}
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		characters = appendUniqueProjectAllString(characters, name)
		event := strings.TrimSpace(decision.Action)
		if result := strings.TrimSpace(decision.ImmediateResult); result != "" {
			event = strings.TrimSpace(event + "；" + result)
		}
		if event != "" {
			timeline = append(timeline, domain.TimelineEvent{
				Chapter: chapter, Time: decision.Time, Event: event, Characters: []string{name},
			})
		}
		for _, fact := range []struct {
			field string
			value string
		}{
			{field: "location", value: decision.Location},
			{field: "knowledge", value: decision.KnowledgeBoundary},
			{field: "decision", value: decision.Decision},
			{field: "status", value: decision.StateAfter},
			{field: "completion_state", value: decision.CompletionState},
			{field: "decision_rationale", value: decision.DecisionReason},
		} {
			value := strings.TrimSpace(fact.value)
			if value == "" {
				continue
			}
			stateChanges = append(stateChanges, domain.StateChange{
				Chapter:   chapter,
				Entity:    name,
				Field:     fact.field,
				NewValue:  value,
				Reason:    strings.TrimSpace(decision.ImmediateResult),
				FactKey:   name + ":" + fact.field,
				ValidFrom: chapter,
			})
		}
		for _, effect := range decision.ButterflyEffects {
			if strings.TrimSpace(effect.Effect) == "" {
				continue
			}
			arrival := effect.ArrivalChapter
			if arrival < chapter {
				arrival = chapter
			}
			worldEvents = append(worldEvents, domain.WorldEvent{
				TickID:              fmt.Sprintf("projected:%s:%06d", generationID, chapter),
				Chapter:             chapter,
				Location:            decision.Location,
				Actors:              append([]string{name}, effect.Targets...),
				Summary:             effect.Effect,
				Consequence:         effect.ProtagonistImpact,
				VisibilityChapter:   arrival,
				VisibilityPath:      effect.TransmissionPath,
				ForeshadowCandidate: effect.Visibility != "visible",
				Tier:                "supporting",
			})
		}
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:       chapter,
		Summary:       strings.TrimSpace(strings.Join(plan.CausalSimulation.OutcomeShift, "；")),
		Characters:    characters,
		KeyEvents:     keyEvents,
		OpeningDevice: plan.Goal,
		EndingDevice:  plan.Hook,
	}); err != nil {
		return err
	}
	if len(timeline) > 0 {
		if err := st.World.AppendTimelineEvents(timeline); err != nil {
			return err
		}
	}
	if len(stateChanges) > 0 {
		if err := st.World.AppendStateChanges(stateChanges); err != nil {
			return err
		}
	}
	if len(plan.CausalSimulation.OffscreenStage) > 0 {
		if err := st.SaveCharacterStageRecords(chapter, plan.CausalSimulation.OffscreenStage); err != nil {
			return err
		}
		if err := st.SaveSideCharacterJourneys(chapter, plan.CausalSimulation.OffscreenStage); err != nil {
			return err
		}
	}
	for _, arc := range plan.CausalSimulation.RelationshipArcs {
		if len(arc.Pair) != 2 {
			continue
		}
		relation := strings.TrimSpace(strings.Join([]string{
			arc.RelationshipType,
			arc.CurrentBond,
			arc.IntimacyStage,
			arc.TrustDebt,
			arc.NextEmotionalBeat,
		}, "；"))
		if relation == "" {
			continue
		}
		if err := st.World.UpdateRelationships([]domain.RelationshipEntry{{
			CharacterA: arc.Pair[0],
			CharacterB: arc.Pair[1],
			Relation:   relation,
			Chapter:    chapter,
		}}); err != nil {
			return err
		}
	}
	if len(worldEvents) > 0 {
		if _, err := st.WorldSim.AppendWorldEvents(worldEvents); err != nil {
			return err
		}
	}
	if err := applyPipelineProjectAllResourceDelta(st, chapter, projectedDelta); err != nil {
		return err
	}
	if err := applyPipelineProjectAllForeshadowDelta(st, chapter, projectedDelta); err != nil {
		return err
	}
	if err := savePipelineProjectAllWorldDelta(
		st,
		generationID,
		chapter,
		simulation,
		plan,
		projectedDelta,
	); err != nil {
		return err
	}
	volume, arc, _ := st.Outline.LocateChapter(chapter)
	if err := st.WorldSim.SaveTick(domain.WorldTick{
		TickID:         fmt.Sprintf("projected:%s:%06d", generationID, chapter),
		Volume:         volume,
		Arc:            arc,
		ThroughChapter: chapter,
		EventCount:     len(worldEvents),
	}); err != nil {
		return err
	}
	if err := st.Progress.MarkChapterComplete(chapter, 0, "", "projected_non_canon"); err != nil {
		return err
	}
	return nil
}

func applyPipelineProjectAllResourceDelta(
	st *store.Store,
	chapter int,
	delta domain.ProjectedDelta,
) error {
	if st == nil || len(delta.Resources) == 0 {
		return nil
	}
	pending := make([]domain.ResourceClaim, 0, len(delta.Resources))
	for _, mutation := range delta.Resources {
		name := strings.TrimSpace(mutation.Object)
		if name == "" {
			name = strings.TrimSpace(mutation.Field)
		}
		pending = append(pending, domain.ResourceClaim{
			ID:           mutation.StableID,
			Name:         name,
			Owner:        mutation.Subject,
			Kind:         "projected_pressure",
			Status:       "pending",
			Risk:         mutation.After,
			Evidence:     mutation.Cause,
			Participants: compactProjectAllStrings([]string{mutation.Subject, mutation.Object}),
		})
	}
	return st.ResourceLedger.MergeClaims(chapter, nil, pending)
}

func applyPipelineProjectAllForeshadowDelta(
	st *store.Store,
	chapter int,
	delta domain.ProjectedDelta,
) error {
	if st == nil || len(delta.Foreshadows) == 0 {
		return nil
	}
	existing, err := st.World.LoadForeshadowLedger()
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(existing))
	for _, entry := range existing {
		known[entry.ID] = true
	}
	updates := make([]domain.ForeshadowUpdate, 0, len(delta.Foreshadows))
	for _, mutation := range delta.Foreshadows {
		action := "plant"
		if known[mutation.StableID] {
			action = "advance"
		}
		updates = append(updates, domain.ForeshadowUpdate{
			ID:          mutation.StableID,
			Action:      action,
			Description: fallbackProjectAllText(mutation.After, mutation.Cause),
		})
		known[mutation.StableID] = true
	}
	return st.World.UpdateForeshadow(chapter, updates)
}

func savePipelineProjectAllWorldDelta(
	st *store.Store,
	generationID string,
	chapter int,
	simulation *domain.ChapterWorldSimulation,
	plan *domain.ChapterPlan,
	projected domain.ProjectedDelta,
) error {
	delta := domain.ChapterWorldDelta{
		Version:      1,
		Chapter:      chapter,
		GenerationID: generationID,
		Summary:      strings.Join(plan.CausalSimulation.OutcomeShift, "；"),
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Sources:      []string{"project-all sealed projection", simulation.SimulationID},
	}
	for _, decision := range simulation.CharacterDecisions {
		if pipelineProjectAllAuthorityNoOp(decision) {
			continue
		}
		currentAction := strings.TrimSpace(decision.Action)
		if decision.CompletionState == "completed" || decision.CompletionState == "instant" {
			currentAction = ""
		}
		delta.CharacterDeltas = append(delta.CharacterDeltas, domain.CharacterChapterDelta{
			Character:         decision.Character,
			Location:          decision.Location,
			Status:            decision.StateAfter,
			VisibleInChapter:  decision.VisibleToPOV,
			CurrentAction:     currentAction,
			Decision:          decision.Decision,
			DecisionReason:    decision.DecisionReason,
			KnowledgeBoundary: decision.KnowledgeBoundary,
			ButterflyEffects:  projectAllButterflyEffectTexts(decision.ButterflyEffects),
			WorldImpact:       decision.ImmediateResult,
			NextPotential:     decision.ImmediateResult,
			TimelineConsistency: fallbackProjectAllText(
				decision.Time,
				simulation.TimeWindow,
			),
		})
	}
	appendWorld := func(kind string, mutations []domain.StateMutationV2) {
		for _, mutation := range mutations {
			delta.WorldDeltas = append(delta.WorldDeltas, domain.WorldChapterDelta{
				Kind:     kind,
				Entity:   fallbackProjectAllText(mutation.Object, mutation.Subject),
				Change:   mutation.After,
				Evidence: mutation.Cause,
				VisibleToProtagonist: pipelineProjectAllWorldMutationVisibleToProtagonist(
					kind,
					mutation,
					simulation,
				),
			})
		}
	}
	appendWorld("timeline", projected.Timeline)
	appendWorld("state", projected.CharacterState)
	appendWorld("relationship", projected.Relationships)
	appendWorld("resource_pending", projected.Resources)
	appendWorld("knowledge", projected.Knowledge)
	appendWorld("location", projected.Locations)
	appendWorld("foreshadow", projected.Foreshadows)
	appendWorld("obligation", projected.Obligations)
	return st.SaveChapterWorldDelta(delta)
}

// pipelineProjectAllWorldMutationVisibleToProtagonist is deliberately
// conservative. ProjectedDelta contains the full simulated world, including
// off-screen antagonist state. A character decision being visible in the
// chapter does not imply that every field on that decision (especially its
// real location or private state_after) is known to the POV protagonist.
// Only the protagonist's own mutations and values explicitly present in the
// simulation's observable projection are marked known.
func pipelineProjectAllWorldMutationVisibleToProtagonist(
	kind string,
	mutation domain.StateMutationV2,
	simulation *domain.ChapterWorldSimulation,
) bool {
	if strings.TrimSpace(kind) == "obligation" || simulation == nil {
		return false
	}
	protagonist := strings.TrimSpace(simulation.ProtagonistProjection.Protagonist)
	if protagonist != "" && strings.TrimSpace(mutation.Subject) == protagonist {
		return true
	}
	value := compactPipelineProjectAllVisibilityText(mutation.After)
	if value == "" {
		return false
	}
	for _, effect := range simulation.ProtagonistProjection.ObservableEffects {
		if strings.Contains(compactPipelineProjectAllVisibilityText(effect), value) {
			return true
		}
	}
	return false
}

func compactPipelineProjectAllVisibilityText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}

// repairPipelineProjectAllWorldDeltaVisibility upgrades resumable shadow
// workspaces produced before visibility became projection-aware. It touches
// only non-canon chapters from the same project-all generation; sealed canon
// and the immutable planning bundles remain unchanged.
func repairPipelineProjectAllWorldDeltaVisibility(
	st *store.Store,
	generationID string,
	baseChapter int,
) error {
	if st == nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil {
		return nil
	}
	for _, chapter := range progress.CompletedChapters {
		if chapter <= baseChapter {
			continue
		}
		delta, err := st.LoadChapterWorldDelta(chapter)
		if err != nil {
			return err
		}
		if delta == nil || strings.TrimSpace(delta.GenerationID) != strings.TrimSpace(generationID) {
			continue
		}
		simulation, err := st.LoadChapterWorldSimulation(chapter)
		if err != nil {
			return err
		}
		if simulation == nil {
			continue
		}
		changed := false
		for i := range delta.WorldDeltas {
			item := &delta.WorldDeltas[i]
			visible := pipelineProjectAllWorldMutationVisibleToProtagonist(
				item.Kind,
				domain.StateMutationV2{
					Subject: item.Entity,
					After:   item.Change,
				},
				simulation,
			)
			if item.VisibleToProtagonist != visible {
				item.VisibleToProtagonist = visible
				changed = true
			}
		}
		if changed {
			if err := st.SaveChapterWorldDelta(*delta); err != nil {
				return err
			}
		}
	}
	return nil
}

func projectAllButterflyEffectTexts(values []domain.DecisionButterflyEffect) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueProjectAllString(out, value.Effect)
	}
	return out
}

func appendUniqueProjectAllString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
