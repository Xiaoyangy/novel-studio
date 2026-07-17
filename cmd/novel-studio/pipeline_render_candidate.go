package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineRenderCandidateManifestVersion = "pipeline-render-candidate.v1"

type pipelineRenderCandidate struct {
	ID              string
	ContainerDir    string
	OutputDir       string
	TransactionRoot string
	SourceLiveRoot  string
}

type pipelineRenderCandidateManifest struct {
	Version                string `json:"version"`
	CandidateID            string `json:"candidate_id"`
	GenerationID           string `json:"generation_id"`
	Chapter                int    `json:"chapter"`
	PlanDigest             string `json:"plan_digest"`
	ProjectedBundleDigest  string `json:"projected_bundle_digest"`
	PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	SourceOutputDir        string `json:"source_output_dir"`
	SourceLiveRoot         string `json:"source_live_root"`
	PreparedAt             string `json:"prepared_at"`
}

type pipelineRenderedChapterSnapshot struct {
	Store           *store.Store
	Commit          *domain.Checkpoint
	ChapterPath     string
	Body            string
	BodySHA256      string
	ActualCanonRoot string
}

func pipelineRenderTransactionRoot(outputDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(outputDir)), ".render-publish")
}

// recoverPipelineRenderPublishesBeforeLoad runs before loadCfgBundle because
// that loader writes the prompt manifest under OutputDir. If a prior process
// crashed after live→archive, touching OutputDir first could recreate an empty
// live directory and make recovery ambiguous.
func recoverPipelineRenderPublishesBeforeLoad(opts cliOptions) error {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return nil
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("render recovery load config: %w", err)
	}
	if err := normalizeOutputAndRAGForInvocation(
		&cfg,
		opts.Dir,
		hasConfiguredRAGQdrantCollection(opts),
	); err != nil {
		return err
	}
	releaseControl, err := acquirePipelineOutlineAllControl(cfg.OutputDir, true)
	if err != nil {
		return err
	}
	defer func() { _ = releaseControl() }()
	return recoverAllDirectoryPublishesWithControlHeld(cfg.OutputDir)
}

func recoverPipelineRenderPublishesWithControlHeld(outputDir string) error {
	publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(outputDir))
	transactionRoot := pipelineRenderTransactionRoot(outputDir)
	entries, err := os.ReadDir(transactionRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("render recovery list directory publishes: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		state, stateErr := publisher.LoadDirectoryPublishState(id)
		if stateErr != nil {
			return fmt.Errorf("render recovery inspect transaction %s: %w", id, stateErr)
		}
		if state == nil || state.Phase == store.DirectoryPublishFinalized ||
			state.Phase == store.DirectoryPublishAborted {
			if state != nil && state.Receipt != nil &&
				filepath.Clean(state.Receipt.LiveDir) != filepath.Clean(outputDir) {
				return fmt.Errorf(
					"render recovery transaction %s targets unexpected live dir %s",
					id,
					state.Receipt.LiveDir,
				)
			}
			continue
		}
		if state.Intent == nil ||
			filepath.Clean(state.Intent.LiveDir) != filepath.Clean(outputDir) ||
			!pathContainsPipelineRenderCandidate(
				pipelineRenderCandidateRoot(outputDir),
				state.Intent.CandidateDir,
			) {
			return fmt.Errorf(
				"render recovery transaction %s is not bound to this live/candidate root",
				id,
			)
		}
		receipt, recoverErr := publisher.RecoverDirectoryPublish(id)
		if recoverErr != nil {
			return fmt.Errorf("render recovery pending directory publish %s: %w", id, recoverErr)
		}
		if err := publisher.FinalizeDirectoryPublish(id); err != nil {
			return fmt.Errorf("render recovery finalize directory publish %s: %w", id, err)
		}
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:render] 已恢复并封存目录发布事务 %s（live=%s）\n",
			receipt.TransactionID,
			receipt.CommittedLiveRoot,
		)
	}
	return nil
}

func pipelineRenderCandidateRoot(outputDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(outputDir)), ".render-candidates")
}

func pipelineRenderTransactionID(frozen *pipelineFrozenPlan) (string, error) {
	if frozen == nil ||
		frozen.ProjectionBinding != "sealed_v2" ||
		frozen.Chapter <= 0 ||
		strings.TrimSpace(frozen.PlanningGenerationID) == "" ||
		strings.TrimSpace(frozen.ProjectedBundleDigest) == "" ||
		strings.TrimSpace(frozen.PromotionReceiptDigest) == "" {
		return "", fmt.Errorf("sealed render transaction requires exact generation/chapter/bundle/promotion")
	}
	digest, err := domain.DeterministicPlanningHash(struct {
		Version     string `json:"version"`
		Generation  string `json:"generation"`
		Chapter     int    `json:"chapter"`
		Plan        string `json:"plan"`
		Bundle      string `json:"bundle"`
		Promotion   string `json:"promotion"`
		RenderInput string `json:"render_input"`
	}{
		Version:     "sealed-render-directory-publish.v1",
		Generation:  frozen.PlanningGenerationID,
		Chapter:     frozen.Chapter,
		Plan:        frozen.PlanDigest,
		Bundle:      frozen.ProjectedBundleDigest,
		Promotion:   frozen.PromotionReceiptDigest,
		RenderInput: frozen.PipelineRunInputDigest,
	})
	if err != nil {
		return "", err
	}
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) < 24 {
		return "", fmt.Errorf("sealed render transaction digest is malformed")
	}
	return fmt.Sprintf("render-ch%04d-%s", frozen.Chapter, digest[:24]), nil
}

func preparePipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
) (*pipelineRenderCandidate, error) {
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return nil, err
	}
	root := pipelineRenderCandidateRoot(liveOutputDir)
	container := filepath.Join(root, id)
	output := filepath.Join(container, "output")
	if _, err := os.Stat(container); err == nil {
		if err := retirePipelineRenderCandidate(container, "stale"); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	sourceLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot sealed render live root: %w", err)
	}
	if err := copyPipelineRenderCandidateTree(liveOutputDir, output); err != nil {
		_ = retirePipelineRenderCandidate(container, "copy-failed")
		return nil, fmt.Errorf("prepare sealed render candidate: %w", err)
	}
	afterCopyLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		_ = retirePipelineRenderCandidate(container, "source-recheck-failed")
		return nil, fmt.Errorf("recheck sealed render live root: %w", err)
	}
	if afterCopyLiveRoot != sourceLiveRoot {
		_ = retirePipelineRenderCandidate(container, "source-drift")
		return nil, fmt.Errorf("live canon changed while preparing sealed render candidate")
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidateManifestVersion,
		CandidateID:            id,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		SourceOutputDir:        filepath.Clean(liveOutputDir),
		SourceLiveRoot:         sourceLiveRoot,
		PreparedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		raw,
		0o644,
	); err != nil {
		_ = retirePipelineRenderCandidate(container, "manifest-failed")
		return nil, fmt.Errorf("save sealed render candidate manifest: %w", err)
	}
	return &pipelineRenderCandidate{
		ID:              id,
		ContainerDir:    container,
		OutputDir:       output,
		TransactionRoot: pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:  sourceLiveRoot,
	}, nil
}

func copyPipelineRenderCandidateTree(source, target string) error {
	source = filepath.Clean(source)
	target = filepath.Clean(target)
	if source == target || pathContainsPipelineRenderCandidate(source, target) {
		return fmt.Errorf("render candidate target must be outside live output: live=%s target=%s", source, target)
	}
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
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("render candidate refuses symlink %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("render candidate refuses non-regular file %s", path)
		}
		// Never hard-link a candidate: the writer and review stages update many
		// append-only ledgers, and a shared inode would violate canon isolation.
		return copyProjectAllFile(path, dst, info.Mode().Perm())
	})
}

func pathContainsPipelineRenderCandidate(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func retirePipelineRenderCandidate(container, reason string) error {
	if _, err := os.Stat(container); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := filepath.Dir(container)
	retiredRoot := filepath.Join(root, "retired")
	if err := os.MkdirAll(retiredRoot, 0o755); err != nil {
		return err
	}
	base := filepath.Base(container)
	reason = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.TrimSpace(reason))
	if reason == "" {
		reason = "retired"
	}
	target := filepath.Join(
		retiredRoot,
		fmt.Sprintf("%s-%s-%s", base, reason, time.Now().UTC().Format("20060102T150405.000000000Z")),
	)
	if err := os.Rename(container, target); err != nil {
		return fmt.Errorf("retire render candidate: %w", err)
	}
	return nil
}

func loadPipelineRenderedChapterSnapshot(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (*pipelineRenderedChapterSnapshot, error) {
	if frozen == nil || planCheckpoint == nil {
		return nil, fmt.Errorf("rendered chapter snapshot requires frozen plan and plan checkpoint")
	}
	st := store.NewStore(outputDir)
	currentPlan, err := tools.CurrentChapterPlanCausalCheckpoint(st, frozen.Chapter)
	if err != nil {
		return nil, fmt.Errorf("render 后正式计划不可验证: %w", err)
	}
	if currentPlan.Digest != frozen.PlanDigest || currentPlan.Seq != planCheckpoint.Seq {
		return nil, fmt.Errorf(
			"render 期间第 %d 章正式计划漂移（frozen=%s#%d current=%s#%d）",
			frozen.Chapter,
			frozen.PlanDigest,
			planCheckpoint.Seq,
			currentPlan.Digest,
			currentPlan.Seq,
		)
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(frozen.Chapter), "commit")
	if commit == nil || commit.Seq <= frozen.BaselineCommitSeq {
		return nil, fmt.Errorf(
			"render 第 %d 章没有产生晚于冻结基线 #%d 的 commit checkpoint",
			frozen.Chapter,
			frozen.BaselineCommitSeq,
		)
	}
	chapterPath := fmt.Sprintf("chapters/%02d.md", frozen.Chapter)
	bodyPath := filepath.Join(outputDir, filepath.FromSlash(chapterPath))
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return nil, fmt.Errorf("render 读取正文: %w", err)
	}
	bodySHA, err := pipelineRequiredFileSHA(outputDir, chapterPath)
	if err != nil {
		return nil, fmt.Errorf("render 验证正文: %w", err)
	}
	if commit.Artifact != chapterPath || commit.Digest != bodySHA {
		return nil, fmt.Errorf(
			"render 第 %d 章 commit checkpoint 未绑定当前正文（artifact=%q digest=%s current=%s）",
			frozen.Chapter,
			commit.Artifact,
			commit.Digest,
			bodySHA,
		)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, fmt.Errorf("render 读取提交后 progress: %w", err)
	}
	actualCanonRoot, err := pipelineCanonRoot(outputDir, progress)
	if err != nil {
		return nil, fmt.Errorf("render 计算提交后 canon root: %w", err)
	}
	return &pipelineRenderedChapterSnapshot{
		Store:           st,
		Commit:          commit,
		ChapterPath:     chapterPath,
		Body:            string(body),
		BodySHA256:      bodySHA,
		ActualCanonRoot: actualCanonRoot,
	}, nil
}

func runPipelineSealedRenderCandidate(
	opts cliOptions,
	flags pipelineFlags,
	state *domain.PipelineState,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (_ *pipelineRenderCandidate, _ *pipelineRenderedChapterSnapshot, returnErr error) {
	candidate, err := preparePipelineRenderCandidate(cfg.OutputDir, frozen)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if returnErr != nil {
			_ = retirePipelineRenderCandidate(candidate.ContainerDir, "rejected")
		}
	}()
	candidateCfg := cfg
	candidateCfg.OutputDir = candidate.OutputDir
	candidateCfg.DisableLiveRAG = true
	renderFlags := flags
	renderFlags.WriteTo = frozen.Chapter
	renderFlags.StopAfterCommit = frozen.Chapter
	renderFlags.RenderOnly = true
	if err := pipelineWriteConfigured(opts, renderFlags, state, candidateCfg, bundle); err != nil {
		return nil, nil, fmt.Errorf(
			"render 第 %d 章候选失败（live canon 未变；render lock 已禁止临时重规划）: %w",
			frozen.Chapter,
			err,
		)
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
	)
	if err != nil {
		return nil, nil, err
	}
	reviewArgs := []string{"--from", fmt.Sprint(frozen.Chapter), "--to", fmt.Sprint(frozen.Chapter)}
	if flags.Budget > 0 {
		reviewArgs = append(reviewArgs, "--budget", flags.Budget.String())
	}
	if err := reviewExistingPipelineAtOutput(
		opts,
		reviewArgs,
		candidate.OutputDir,
		true,
	); err != nil {
		return nil, nil, fmt.Errorf(
			"render 第 %d 章候选 fresh exact-body review 失败（live canon 未变）: %w",
			frozen.Chapter,
			err,
		)
	}
	if err := requirePipelineAcceptedExactReview(candidate.OutputDir, frozen.Chapter); err != nil {
		return nil, nil, fmt.Errorf(
			"render 第 %d 章候选未通过 fresh exact-body accept；sealed generation 保持在本章，候选已隔离，必须只重渲染当前冻结计划: %w",
			frozen.Chapter,
			err,
		)
	}
	// Reload after review because the reviewer writes exact-body checkpoints and
	// quality artifacts into the candidate tree that will be promoted together.
	snapshot, err = loadPipelineRenderedChapterSnapshot(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
	)
	if err != nil {
		return nil, nil, err
	}
	return candidate, snapshot, nil
}

func publishPipelineRenderCandidate(
	liveOutputDir string,
	candidate *pipelineRenderCandidate,
) (*store.DirectoryPublishReceipt, error) {
	if candidate == nil {
		return nil, fmt.Errorf("publish sealed render candidate is nil")
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	receipt, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID:    candidate.ID,
		LiveDir:          liveOutputDir,
		CandidateDir:     candidate.OutputDir,
		ExpectedLiveRoot: candidate.SourceLiveRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("publish sealed render candidate: %w", err)
	}
	if receipt == nil ||
		receipt.TransactionID != candidate.ID ||
		filepath.Clean(receipt.LiveDir) != filepath.Clean(liveOutputDir) ||
		receipt.CandidateRoot != receipt.CommittedLiveRoot ||
		strings.TrimSpace(receipt.ReceiptDigest) == "" {
		return nil, fmt.Errorf("sealed render directory publish returned incomplete receipt")
	}
	return receipt, nil
}

func finalizePipelineRenderCandidate(
	outputDir string,
	transactionID string,
) error {
	if strings.TrimSpace(transactionID) == "" {
		return nil
	}
	publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(outputDir))
	if err := publisher.FinalizeDirectoryPublish(transactionID); err != nil {
		return fmt.Errorf("finalize sealed render directory publish: %w", err)
	}
	// CandidateDir itself has been renamed into live; remove only its now-empty
	// container. A non-empty container is retained for diagnosis.
	_ = os.Remove(filepath.Join(pipelineRenderCandidateRoot(outputDir), transactionID))
	return nil
}

func savePipelineSealedActualMatch(
	outputDir string,
	match pipelineSealedActualDeltaMatch,
) error {
	raw, err := json.MarshalIndent(match, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicWriteRewriteFile(
		filepath.Join(outputDir, "meta", "planning", "sealed_actual_match.json"),
		raw,
		0o644,
	)
}
