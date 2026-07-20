package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type pipelineAllChapterRebaseReceipt struct {
	Version                          string `json:"version"`
	SourceOutput                     string `json:"source_output"`
	SourceRoot                       string `json:"source_root"`
	SourceRAGAuthorityRoot           string `json:"source_rag_authority_root"`
	ArchiveOutput                    string `json:"archive_output"`
	ArchiveRoot                      string `json:"archive_root"`
	ArchiveRAGAuthorityRoot          string `json:"archive_rag_authority_root"`
	ArchivedProjectAllWorkspaces     string `json:"archived_project_all_workspaces,omitempty"`
	PreviousProgress                 string `json:"previous_progress"`
	ArchivedPlanningGenerationID     string `json:"archived_planning_generation_id,omitempty"`
	PlanningGenerationArchiveReceipt string `json:"planning_generation_archive_receipt,omitempty"`
	NewGenerationID                  string `json:"new_generation_id"`
	RebasedRAGAuthorityRoot          string `json:"rebased_rag_authority_root"`
	RebasedAt                        string `json:"rebased_at"`
}

// pipelineRebaseAllChapters is the explicit bridge for an existing book whose
// already-committed opening must participate in the same all-book projection
// as every future chapter. It snapshots the entire old output outside the live
// canon, prepares a chapter-zero candidate, then swaps directories with the
// same recoverable publisher used by sealed render.
func pipelineRebaseAllChapters(opts cliOptions) (returnErr error) {
	live, err := pipelineRebaseOutputDirBeforeLoad(opts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(live) == "" {
		return fmt.Errorf("全书 rebase 需要在启动前解析出 output directory")
	}
	releaseControl, err := acquirePipelineOutlineAllControl(live, true)
	if err != nil {
		return fmt.Errorf("全书 rebase 获取 run-root 独占控制: %w", err)
	}
	defer func() {
		if releaseErr := releaseControl(); releaseErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("全书 rebase 释放 run-root 独占控制: %w", releaseErr)
		}
	}()
	// Recovery, config loading, candidate construction, and the final live swap
	// are one EX critical section. Neither published readers nor outline-all may
	// observe or replace the live path in the middle of rebase.
	if err := recoverAllDirectoryPublishesWithControlHeld(live); err != nil {
		return err
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if filepath.Clean(cfg.OutputDir) != live {
		return fmt.Errorf("全书 rebase output directory 在独占控制后发生变化")
	}
	st := store.NewStore(live)
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf("全书 rebase 前仍有 execution lock：mode=%s owner=%s", lock.Mode, lock.Owner)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("全书 rebase 读取 progress: %w", err)
	}
	chapterZero := len(progress.CompletedChapters) == 0 &&
		len(progress.PendingRewrites) == 0
	chapterZeroDirty := progress.CurrentChapter != 0 ||
		progress.InProgressChapter != 0 ||
		progress.TotalWordCount != 0 ||
		len(progress.ChapterWordCounts) != 0 ||
		len(progress.CompletedScenes) != 0 ||
		progress.Flow != ""
	if chapterZero {
		hasRestartState, restartErr := pipelineChapterZeroHasRestartState(live)
		err = restartErr
		if err != nil {
			return fmt.Errorf("全书 rebase 检查 chapter-zero 失败代次: %w", err)
		}
		chapterZeroDirty = chapterZeroDirty || hasRestartState
	}
	if chapterZero && !chapterZeroDirty {
		fmt.Fprintln(os.Stderr, "[pipeline:rebase-all] 正史已经位于第0章，无需重复 rebase")
		return nil
	}
	archivedPlanningGenerationID, planningArchiveReceipt, err :=
		archivePipelineActivePlanningGenerationForRebase(st.ProjectedV2())
	if err != nil {
		return fmt.Errorf("全书 rebase 归档 active sealed generation: %w", err)
	}
	sourceRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		return fmt.Errorf("全书 rebase 计算 live root: %w", err)
	}
	sourceRAGAuthorityRoot, err := pipelineRebaseRAGAuthorityRoot(live)
	if err != nil {
		return fmt.Errorf("全书 rebase 计算 live RAG authority root: %w", err)
	}
	runRoot := pipelineRebaseRunRoot(live)
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	archiveOutput := filepath.Join(runRoot, "archives", "sealed-rebase-"+stamp, "output", "novel")
	if err := copyPipelineRenderCandidateTree(live, archiveOutput); err != nil {
		return fmt.Errorf("全书 rebase 归档旧正史: %w", err)
	}
	archiveRoot, err := store.DirectoryContentRoot(archiveOutput)
	if err != nil || archiveRoot != sourceRoot {
		return fmt.Errorf(
			"全书 rebase 归档校验失败：source=%s archive=%s err=%v",
			sourceRoot,
			archiveRoot,
			err,
		)
	}
	archiveRAGAuthorityRoot, err := pipelineRebaseRAGAuthorityRoot(archiveOutput)
	if err != nil || archiveRAGAuthorityRoot != sourceRAGAuthorityRoot {
		return fmt.Errorf(
			"全书 rebase 归档 RAG authority 校验失败：source=%s archive=%s err=%v",
			sourceRAGAuthorityRoot,
			archiveRAGAuthorityRoot,
			err,
		)
	}
	archivedWorkspaces, err := archivePipelineProjectAllWorkspacesForRebase(
		live,
		filepath.Join(filepath.Dir(filepath.Dir(archiveOutput)), "project-all-workspaces"),
	)
	if err != nil {
		return fmt.Errorf("全书 rebase 归档 project-all 隔离工作区: %w", err)
	}

	candidateRoot := filepath.Join(pipelineRebaseCandidateRoot(live), "rebase-"+stamp)
	candidate := filepath.Join(candidateRoot, "output")
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		return fmt.Errorf("全书 rebase 准备候选正史: %w", err)
	}
	if err := preservePipelineRebaseFoundationModTimes(live, candidate); err != nil {
		return fmt.Errorf("全书 rebase 保留 Architect foundation freshness: %w", err)
	}
	project, err := loadZeroInitProjectForExplicitRebaseCandidate(
		store.NewStore(candidate),
		candidate,
	)
	if err != nil {
		return fmt.Errorf("全书 rebase 读取 chapter-zero foundation: %w", err)
	}
	project.GenerationID = zeroSimulationGenerationID(time.Now().UTC().Format(time.RFC3339Nano))
	if err := resetPipelineAllChapterCandidate(candidate, &project); err != nil {
		return err
	}
	rebasedRAGAuthorityRoot, err := pipelineRebaseRAGAuthorityRoot(candidate)
	if err != nil || rebasedRAGAuthorityRoot != sourceRAGAuthorityRoot {
		return fmt.Errorf(
			"全书 rebase 候选 RAG authority 漂移：source=%s candidate=%s err=%v",
			sourceRAGAuthorityRoot,
			rebasedRAGAuthorityRoot,
			err,
		)
	}
	if err := applyPipelineAllChapterZeroProgressReset(candidate, &project); err != nil {
		return fmt.Errorf("全书 rebase 重置活动推演状态: %w", err)
	}
	if err := resetPipelineChapterZeroTimeline(candidate); err != nil {
		return err
	}
	// The chapter-zero candidate itself carries the irreversible route intent.
	// If the process exits after the directory swap but before project-all
	// starts, direct plan/write/rewrite and prose tools still remain blocked.
	if err := activatePipelineSealedTwoPassModeAtOutput(candidate); err != nil {
		return fmt.Errorf("全书 rebase 锁定 sealed_two_pass_v2: %w", err)
	}
	candidateStore := store.NewStore(candidate)
	candidateProgress, err := candidateStore.Progress.Load()
	if err != nil || candidateProgress == nil ||
		len(candidateProgress.CompletedChapters) != 0 ||
		len(candidateProgress.PendingRewrites) != 0 ||
		candidateProgress.LatestCompleted() != 0 {
		return fmt.Errorf("全书 rebase 候选 progress 未回到 chapter zero: %+v err=%v", candidateProgress, err)
	}
	receipt := pipelineAllChapterRebaseReceipt{
		Version:                          "pipeline-all-chapter-rebase.v1",
		SourceOutput:                     live,
		SourceRoot:                       sourceRoot,
		SourceRAGAuthorityRoot:           sourceRAGAuthorityRoot,
		ArchiveOutput:                    archiveOutput,
		ArchiveRoot:                      archiveRoot,
		ArchiveRAGAuthorityRoot:          archiveRAGAuthorityRoot,
		ArchivedProjectAllWorkspaces:     archivedWorkspaces,
		PreviousProgress:                 filepath.Join(archiveOutput, "meta", "progress.json"),
		ArchivedPlanningGenerationID:     archivedPlanningGenerationID,
		PlanningGenerationArchiveReceipt: planningArchiveReceipt,
		NewGenerationID:                  project.GenerationID,
		RebasedRAGAuthorityRoot:          rebasedRAGAuthorityRoot,
		RebasedAt:                        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(candidate, "meta", "all_chapter_rebase.json"),
		receipt,
	); err != nil {
		return err
	}
	transactionID := "canon-rebase-" + strings.TrimPrefix(sourceRoot, "sha256:")[:24]
	publisher := store.NewDirectoryPublishStore(pipelineRebaseTransactionRoot(live))
	var publishReceipt *store.DirectoryPublishReceipt
	err = withPipelineWatchdogPaused(func() error {
		var publishErr error
		publishReceipt, publishErr = publisher.PublishDirectory(store.PublishDirectoryRequest{
			TransactionID:    transactionID,
			LiveDir:          live,
			CandidateDir:     candidate,
			ExpectedLiveRoot: sourceRoot,
		})
		return publishErr
	})
	if err != nil {
		return fmt.Errorf("全书 rebase 发布 chapter-zero 候选: %w", err)
	}
	if publishReceipt == nil || publishReceipt.CandidateRoot != publishReceipt.CommittedLiveRoot {
		return fmt.Errorf("全书 rebase directory publish receipt 不完整")
	}
	if err := publisher.FinalizeDirectoryPublish(transactionID); err != nil {
		return fmt.Errorf("全书 rebase finalize directory publish: %w", err)
	}
	if chapterZero {
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:rebase-all] chapter-zero 失败代次已完整归档到 %s；活动正史保持第0章，将创建全新 generation 并统一重推第1-%d章\n",
			archiveOutput,
			progress.TotalChapters,
		)
	} else {
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:rebase-all] 旧第1-%d章已完整归档到 %s；活动正史已回到第0章，将统一重推第1-%d章\n",
			progress.LatestCompleted(),
			archiveOutput,
			progress.TotalChapters,
		)
	}
	return nil
}

// pipelineChapterZeroHasRestartState distinguishes a genuinely clean chapter
// zero from a failed planning epoch. Store.Init creates several empty
// directories, so those only count when they contain files; planning/v2 itself
// is different because even an empty envelope or write lock belongs to an
// already-started generation protocol.
func pipelineChapterZeroHasRestartState(outputDir string) (bool, error) {
	for _, rel := range []string{
		"meta/planning/v2",
		store.OutlineAllExecutionReceiptPath,
		pipelineOutlineRepairReceiptPath,
		"meta/planning/preplan_receipt.json",
		"meta/planning/book_causal_skeleton.json",
		"meta/pipeline.json",
		"meta/first_chapter_generation_readiness.json",
		"meta/first_chapter_generation_readiness.md",
		"meta/pending_commit.json",
	} {
		if _, err := os.Lstat(filepath.Join(outputDir, filepath.FromSlash(rel))); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	for _, rel := range []string{
		"meta/sessions",
		"meta/chapter_simulations",
		"meta/chapter_metrics",
		"meta/sampling",
		"meta/scene_dynamics",
		"meta/delivery_snapshots",
		"meta/rewrite_recovery",
	} {
		hasFiles, err := pipelineRebaseTreeHasFiles(
			filepath.Join(outputDir, filepath.FromSlash(rel)),
		)
		if err != nil {
			return false, err
		}
		if hasFiles {
			return true, nil
		}
	}
	workspaceRoot := filepath.Join(pipelineRebaseRunRoot(outputDir), ".project-all")
	entries, err := os.ReadDir(workspaceRoot)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func pipelineRebaseTreeHasFiles(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !entry.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	return found, err
}

// archivePipelineActivePlanningGenerationForRebase makes deletion of the live
// control pointers an explicit lifecycle transition. The immutable sealed
// generation and this receipt are retained in the exact output archive before
// the chapter-zero candidate removes planning/v2 from the new live tree.
func archivePipelineActivePlanningGenerationForRebase(
	projected *store.ProjectedStoreV2,
) (string, string, error) {
	if projected == nil {
		return "", "", nil
	}
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return "", "", err
	}
	records, err := projected.LoadGenerationArchiveReceipts(active.GenerationID)
	if err != nil {
		return "", "", err
	}
	if len(records) > 0 {
		tail, err := pipelineGenerationArchiveReceiptTail(records)
		return active.GenerationID, tail, err
	}
	receipt := domain.GenerationArchiveReceiptV2{
		Version:           domain.GenerationArchiveV2Version,
		GenerationID:      active.GenerationID,
		SealReceiptDigest: active.SealReceiptDigest,
		Reason:            "explicit --rebase-all-chapters retires the active sealed generation before a fresh chapter-zero projection epoch",
		ArchivedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest, err = domain.ComputeGenerationArchiveReceiptV2Digest(receipt)
	if err != nil {
		return "", "", err
	}
	digest, err := projected.SaveGenerationArchiveReceipt(receipt)
	if err != nil {
		return "", "", err
	}
	return active.GenerationID, digest, nil
}

func pipelineGenerationArchiveReceiptTail(
	records []domain.GenerationArchiveReceiptV2,
) (string, error) {
	known := make(map[string]struct{}, len(records))
	referenced := make(map[string]struct{}, len(records))
	for _, record := range records {
		known[record.ReceiptDigest] = struct{}{}
		if record.PreviousReceiptDigest != "" {
			referenced[record.PreviousReceiptDigest] = struct{}{}
		}
	}
	tail := ""
	for digest := range known {
		if _, used := referenced[digest]; used {
			continue
		}
		if tail != "" {
			return "", fmt.Errorf("generation archive history has multiple tails")
		}
		tail = digest
	}
	if tail == "" {
		return "", fmt.Errorf("generation archive history contains a cycle")
	}
	return tail, nil
}

func archivePipelineProjectAllWorkspacesForRebase(
	outputDir, archiveDir string,
) (string, error) {
	source := filepath.Join(pipelineRebaseRunRoot(outputDir), ".project-all")
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	if _, err := os.Lstat(archiveDir); err == nil {
		return "", fmt.Errorf("project-all workspace archive already exists: %s", archiveDir)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(source, archiveDir); err != nil {
		return "", err
	}
	return archiveDir, nil
}

// copyPipelineRenderCandidateTree intentionally creates fresh inodes. Rebase
// also copies the Architect readiness receipt, whose freshness check compares
// its generated_at against foundation mtimes. Preserve those source mtimes so
// a slow copy cannot make unchanged foundation look newly edited.
func preservePipelineRebaseFoundationModTimes(sourceDir, candidateDir string) error {
	for _, rel := range architectFoundationFreshnessFiles {
		source := filepath.Join(sourceDir, rel)
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		target := filepath.Join(candidateDir, rel)
		if _, err := os.Stat(target); err != nil {
			return err
		}
		if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func pipelineRebaseRunRoot(outputDir string) string {
	return filepath.Dir(filepath.Dir(filepath.Clean(outputDir)))
}

func pipelineRebaseCandidateRoot(outputDir string) string {
	return filepath.Join(pipelineRebaseRunRoot(outputDir), ".canon-rebase")
}

func pipelineRebaseTransactionRoot(outputDir string) string {
	return filepath.Join(pipelineRebaseRunRoot(outputDir), ".canon-rebase-publish")
}

// recoverPipelineRebasePublishesBeforeLoad completes and finalizes every
// unfinished rebase directory publish before any caller may write OutputDir.
// The transaction root is project-local, but the intent is still checked
// against the exact live directory and candidate namespace so a misplaced or
// copied journal can never recover another project's canon.
func recoverPipelineRebasePublishesBeforeLoad(opts cliOptions) error {
	live, err := pipelineRebaseOutputDirBeforeLoad(opts)
	if err != nil || live == "" {
		return err
	}
	releaseControl, err := acquirePipelineOutlineAllControl(live, true)
	if err != nil {
		return fmt.Errorf("rebase recovery acquire run-root exclusive control: %w", err)
	}
	defer func() { _ = releaseControl() }()
	return recoverAllDirectoryPublishesWithControlHeld(live)
}

func pipelineRebaseOutputDirBeforeLoad(opts cliOptions) (string, error) {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return "", nil
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("rebase recovery load config: %w", err)
	}
	if err := normalizeOutputAndRAGForInvocation(
		&cfg,
		opts.Dir,
		hasConfiguredRAGQdrantCollection(opts),
	); err != nil {
		return "", err
	}
	return filepath.Clean(cfg.OutputDir), nil
}

func recoverPipelineRebasePublishesWithControlHeld(live string) error {
	live = filepath.Clean(live)
	candidateRoot := pipelineRebaseCandidateRoot(live)
	transactionRoot := pipelineRebaseTransactionRoot(live)
	entries, err := os.ReadDir(transactionRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("rebase recovery list directory publishes: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	publisher := store.NewDirectoryPublishStore(transactionRoot)
	for _, id := range ids {
		state, stateErr := publisher.LoadDirectoryPublishState(id)
		if stateErr != nil {
			return fmt.Errorf("rebase recovery inspect transaction %s: %w", id, stateErr)
		}
		if state == nil {
			continue
		}
		if state.Phase == store.DirectoryPublishFinalized {
			if state.Receipt == nil ||
				filepath.Clean(state.Receipt.LiveDir) != live {
				return fmt.Errorf(
					"rebase recovery finalized transaction %s targets unexpected live dir",
					id,
				)
			}
			continue
		}
		if state.Phase == store.DirectoryPublishAborted {
			continue
		}
		if state.Intent == nil ||
			filepath.Clean(state.Intent.LiveDir) != live ||
			!pathContainsPipelineRenderCandidate(candidateRoot, state.Intent.CandidateDir) {
			return fmt.Errorf(
				"rebase recovery transaction %s is not bound to this live/candidate root",
				id,
			)
		}
		var receipt *store.DirectoryPublishReceipt
		recoverErr := withPipelineWatchdogPaused(func() error {
			var err error
			receipt, err = publisher.RecoverDirectoryPublish(id)
			return err
		})
		if recoverErr != nil {
			return fmt.Errorf("rebase recovery pending directory publish %s: %w", id, recoverErr)
		}
		if receipt == nil ||
			filepath.Clean(receipt.LiveDir) != live ||
			receipt.CandidateRoot != receipt.CommittedLiveRoot {
			return fmt.Errorf("rebase recovery transaction %s returned an incomplete receipt", id)
		}
		if err := publisher.FinalizeDirectoryPublish(id); err != nil {
			return fmt.Errorf("rebase recovery finalize directory publish %s: %w", id, err)
		}
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:rebase-all] 已恢复并封存目录发布事务 %s（live=%s）\n",
			receipt.TransactionID,
			receipt.CommittedLiveRoot,
		)
	}
	return nil
}

func resetPipelineAllChapterCandidate(
	outputDir string,
	project *zeroInitProject,
) error {
	ragAuthorityRoot, err := pipelineRebaseRAGAuthorityRoot(outputDir)
	if err != nil {
		return fmt.Errorf("全书 rebase 读取冻结 RAG authority: %w", err)
	}
	for _, rel := range []string{
		"chapters",
		"drafts",
		"summaries",
		"reviews",
		"reviews_ai",
		"meta/planning",
		"meta/runtime",
		"meta/quarantine",
		"meta/chapter_world_deltas",
		"meta/character_stage",
		"meta/side_character_journeys",
		"meta/characters",
		"meta/scene_inventory",
		"meta/snapshots",
		"meta/writing_trials",
	} {
		if err := os.RemoveAll(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("全书 rebase 清理 %s: %w", rel, err)
		}
	}
	if err := resetPipelineRebaseRAGGenerationState(outputDir); err != nil {
		return err
	}
	currentRAGAuthorityRoot, err := pipelineRebaseRAGAuthorityRoot(outputDir)
	if err != nil {
		return fmt.Errorf("全书 rebase 复核冻结 RAG authority: %w", err)
	}
	if currentRAGAuthorityRoot != ragAuthorityRoot {
		return fmt.Errorf(
			"全书 rebase 不得修改冻结 RAG authority：before=%s after=%s",
			ragAuthorityRoot,
			currentRAGAuthorityRoot,
		)
	}
	if err := sanitizePipelineAllChapterLegacyInference(outputDir); err != nil {
		return err
	}
	for _, rel := range []string{
		"正文.md",
		"relationship_state.json",
		"relationship_state.md",
		"relationship_state.initial.json",
		"relationship_state.initial.md",
		"foreshadow_ledger.json",
		"foreshadow_ledger.md",
		"foreshadow_ledger.initial.json",
		"foreshadow_ledger.initial.md",
		"meta/checkpoints.jsonl",
		"meta/state_changes.json",
		"meta/pipeline.json",
		"meta/pending_commit.json",
		"meta/last_commit.json",
		"meta/last_review.json",
		"meta/review-summary.md",
		"meta/external_detection_log.jsonl",
		"meta/delivery_log.jsonl",
		"meta/delivery_log.md",
		"meta/chapter_progress.json",
		"meta/chapter_progress.md",
		"meta/project_progress.json",
		"meta/project_progress.md",
		"meta/evolution_report.json",
		"meta/evolution_report.md",
		"meta/cast_ledger.json",
		"meta/character_continuity.json",
		"meta/character_continuity.md",
		"meta/resource_ledger.json",
		"meta/resource_ledger.md",
		"meta/initial_resource_ledger.json",
		"meta/initial_resource_ledger.md",
		"meta/simulation_restart_policy.json",
		"meta/simulation_restart_policy.md",
		"meta/simulation_restart_state.json",
		"meta/simulation_restart_state.md",
		"meta/world_foundation.json",
		"meta/world_foundation.md",
		"meta/initial_character_dynamics.json",
		"meta/initial_character_dynamics.md",
		"meta/character_return_plan.json",
		"meta/character_return_plan.md",
		"meta/crowd_role_policy.json",
		"meta/crowd_role_policy.md",
		"meta/prewrite_storycraft_plan.json",
		"meta/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json",
		"meta/world_background_plan.md",
		"meta/ch01_zero_init_plan.md",
		"meta/zero_chapter_context_manifest.json",
		"meta/zero_chapter_context_manifest.md",
		"meta/story_time_contract.json",
		"meta/story_time_contract.md",
		"meta/story_calendar.json",
		"meta/world_events.jsonl",
		"meta/world_tick.json",
		"meta/simulation_tiers.json",
		"meta/offscreen_agenda.json",
		"meta/event_weave.json",
		"meta/info_graph.json",
		"meta/social_mood.json",
		"meta/ritual_calendar.json",
		"meta/physics_axioms.json",
		"meta/moral_ceiling.json",
		"meta/cosmology.json",
		"meta/crowd_life.json",
		"meta/ecological_map.json",
		"meta/cultural_footnotes.json",
		"meta/pacing_contract.json",
		"meta/book_stylestat.json",
		"meta/first_chapter_generation_readiness.json",
		"meta/first_chapter_generation_readiness.md",
	} {
		if err := os.Remove(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil &&
			!os.IsNotExist(err) {
			return fmt.Errorf("全书 rebase 清理 %s: %w", rel, err)
		}
	}
	return nil
}

// pipelineRebaseRAGAuthorityRoot binds the immutable retrieval authority that
// project-all will copy into the next generation. Rebase may discard receipts
// produced by the old generation, but rebuilding or deleting this index would
// silently replace the user's curated 15k-chunk/490-vector evidence base.
func pipelineRebaseRAGAuthorityRoot(outputDir string) (string, error) {
	artifacts := make(map[string]string)
	for _, rel := range []string{
		"meta/rag/index_state.json",
		"meta/rag/index_state.md",
		"meta/rag/vector_store.json",
		"meta/rag/vector_store.md",
	} {
		digest, err := pipelineOptionalFileSHA(outputDir, rel)
		if err != nil {
			return "", err
		}
		if digest != "" {
			artifacts[rel] = digest
		}
	}
	return pipelineProjectAllDigestE(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{
		Version:   "pipeline-rebase-rag-authority.v1",
		Artifacts: artifacts,
	})
}

// resetPipelineRebaseRAGGenerationState removes only evidence emitted by the
// previous planning/writing generation. The immutable index and local vector
// fallback remain byte-identical and are covered by the rebase receipt roots.
func resetPipelineRebaseRAGGenerationState(outputDir string) error {
	for _, rel := range []string{
		"meta/rag/receipts",
		"meta/rag/fact_receipts",
		"meta/rag/craft_receipts",
		"meta/rag/traces",
		"meta/rag/logs",
	} {
		if err := os.RemoveAll(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("全书 rebase 清理旧 generation RAG 目录 %s: %w", rel, err)
		}
	}
	for _, rel := range []string{
		"meta/rag/craft_recall_log.jsonl",
		"meta/rag/retrieval_trace.jsonl",
		"meta/rag/pending_upserts.json",
	} {
		if err := os.Remove(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil &&
			!os.IsNotExist(err) {
			return fmt.Errorf("全书 rebase 清理旧 generation RAG 文件 %s: %w", rel, err)
		}
	}
	return nil
}

// sanitizePipelineAllChapterLegacyInference removes chapter-run state that is
// never a chapter-zero seed. The exact pre-rebase tree already lives in the
// verified archive; retaining these resumable sessions or per-chapter metrics
// in the new live tree can make project-all continue an old Writer/Simulator
// conversation even though drafts and chapters were reset.
func sanitizePipelineAllChapterLegacyInference(outputDir string) error {
	for _, rel := range []string{
		"meta/sessions",
		"meta/chapter_simulations",
		"meta/chapter_metrics",
		"meta/sampling",
		"meta/scene_dynamics",
		"meta/delivery_snapshots",
		"meta/rewrite_recovery",
	} {
		if err := os.RemoveAll(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("清理旧章节推理状态 %s: %w", rel, err)
		}
	}
	return nil
}

// applyPipelineAllChapterZeroProgressReset is deliberately smaller than
// applyZeroInitSimulationRestartState. Rebase owns only the canon boundary: it
// publishes a fresh generation at chapter zero and clears activity cursors.
// Zero-init assets (policy, foundation derivatives, story clock and world tick)
// must be derived later from the fully published outline-all result.
func applyPipelineAllChapterZeroProgressReset(
	outputDir string,
	project *zeroInitProject,
) error {
	if project == nil {
		return fmt.Errorf("全书 rebase chapter-zero progress reset 缺少 project")
	}
	st := store.NewStore(outputDir)
	total, layered := zeroInitRestartChapterPlan(st, project)
	if total <= 0 {
		return fmt.Errorf("全书 rebase chapter-zero progress reset 缺少当前大纲章数")
	}
	if strings.TrimSpace(project.GenerationID) == "" {
		return fmt.Errorf("全书 rebase chapter-zero progress reset 缺少 generation_id")
	}
	if err := st.Progress.ResetForSimulationRestart(
		project.Name,
		total,
		project.GenerationID,
	); err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil {
		return fmt.Errorf("全书 rebase chapter-zero progress reset 未生成 progress")
	}
	progress.Layered = layered
	if layered {
		progress.CurrentVolume = 1
		progress.CurrentArc = 1
	} else {
		progress.CurrentVolume = 0
		progress.CurrentArc = 0
	}
	if err := st.Progress.Save(progress); err != nil {
		return err
	}
	// ResetActivityState only removes the previous world event stream and tick.
	// In particular, do not seed v0-a0 here: outline-all has not run yet.
	return st.WorldSim.ResetActivityState()
}

func resetPipelineChapterZeroTimeline(outputDir string) error {
	timeline, _ := json.MarshalIndent([]domain.TimelineEvent{}, "", "  ")
	timeline = append(timeline, '\n')
	if err := atomicWriteRewriteFile(filepath.Join(outputDir, "timeline.json"), timeline, 0o644); err != nil {
		return err
	}
	return atomicWriteRewriteFile(
		filepath.Join(outputDir, "timeline.md"),
		[]byte("# 时间线\n\n尚未开始正文。\n"),
		0o644,
	)
}
