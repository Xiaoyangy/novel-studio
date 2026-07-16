package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelinePlanningReceiptPath = "meta/planning/preplan_receipt.json"
	pipelineFrozenPlanPath      = "meta/planning/current_frozen_plan.json"
	pipelineRenderReceiptPath   = "meta/planning/current_render_receipt.json"
	pipelinePlanningSchema      = "pipeline-planning.v1"
	pipelineExecutionLease      = 6 * time.Hour
	pipelineProjectionExpanded  = "expanded_outline"
	pipelineProjectionCoarse    = "arc_slot_coarse"
)

// pipelineProjectedChapterPayload is deliberately not a ChapterPlan. It is a
// prose-free forecast derived from an already-expanded outline entry. Keeping a
// different schema and an explicit Formal=false marker prevents a skeleton arc
// or outline forecast from masquerading as drafts/NN.plan.json.
type pipelineProjectedChapterPayload struct {
	Version         string              `json:"version"`
	GenerationID    string              `json:"generation_id"`
	Chapter         int                 `json:"chapter"`
	Volume          int                 `json:"volume,omitempty"`
	Arc             int                 `json:"arc,omitempty"`
	Formal          bool                `json:"formal"`
	Authority       string              `json:"authority"`
	ProjectionLevel string              `json:"projection_level"`
	Outline         domain.OutlineEntry `json:"outline"`
	Notice          string              `json:"notice"`
}

type pipelinePreplanReceipt struct {
	Version                    string   `json:"version"`
	GenerationID               string   `json:"generation_id"`
	BaseCanonChapter           int      `json:"base_canon_chapter"`
	BaseCanonRoot              string   `json:"base_canon_root"`
	CurrentCanonRoot           string   `json:"current_canon_root"`
	DependencyRoot             string   `json:"dependency_root"`
	TotalChapters              int      `json:"total_chapters"`
	VolumeIndices              []int    `json:"volume_indices"`
	StagedChapters             []int    `json:"staged_chapters,omitempty"`
	DetailedChapters           []int    `json:"detailed_chapters,omitempty"`
	CoarseChapters             []int    `json:"coarse_chapters,omitempty"`
	RealizedChapters           []int    `json:"realized_chapters,omitempty"`
	RebaseRequiredBeforeFuture bool     `json:"rebase_required_before_future,omitempty"`
	BlockedAtChapter           int      `json:"blocked_at_chapter,omitempty"`
	BlockedReason              string   `json:"blocked_reason,omitempty"`
	CreatedAt                  string   `json:"created_at"`
	SourceArtifacts            []string `json:"source_artifacts"`
}

type pipelineFrozenPlan struct {
	Version                 string `json:"version"`
	Chapter                 int    `json:"chapter"`
	PlanPath                string `json:"plan_path"`
	PlanDigest              string `json:"plan_digest"`
	PlanCheckpointSeq       int64  `json:"plan_checkpoint_seq"`
	BaselineCommitSeq       int64  `json:"baseline_commit_seq"`
	BaselineCompletedDigest string `json:"baseline_completed_digest"`
	PlanningGenerationID    string `json:"planning_generation_id"`
	PlanningDependencyRoot  string `json:"planning_dependency_root"`
	ProjectionBinding       string `json:"projection_binding"`
	ProjectedPlanSHA256     string `json:"projected_plan_sha256,omitempty"`
	ProjectedPreStateRoot   string `json:"projected_pre_state_root,omitempty"`
	ProjectedPostStateRoot  string `json:"projected_post_state_root,omitempty"`
	FrozenAt                string `json:"frozen_at"`
}

type pipelineRenderReceipt struct {
	Version             string `json:"version"`
	Chapter             int    `json:"chapter"`
	PlanDigest          string `json:"plan_digest"`
	PlanCheckpointSeq   int64  `json:"plan_checkpoint_seq"`
	CommitDigest        string `json:"commit_digest"`
	CommitCheckpointSeq int64  `json:"commit_checkpoint_seq"`
	ChapterPath         string `json:"chapter_path"`
	ChapterBodySHA256   string `json:"chapter_body_sha256"`
	ActualCanonRoot     string `json:"actual_canon_root"`
	ProjectedStateRoot  string `json:"projected_state_root,omitempty"`
	ProjectionBound     bool   `json:"projection_receipt_bound"`
	DownstreamInvalid   bool   `json:"downstream_invalidated"`
	NextAction          string `json:"next_action,omitempty"`
	RenderedAt          string `json:"rendered_at"`
}

type pipelineProjectionEntry struct {
	Entry  domain.OutlineEntry
	Volume int
	Arc    int
	Level  string
}

type pipelinePlanProseSnapshot struct {
	CompletedDigest string
	TotalWordCount  int
	CommitSeq       int64
	DraftDigest     string
	ChapterDigest   string
}

func pipelinePreplan(opts cliOptions, flags pipelineFlags) (returnErr error) {
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := pipelineRequirePrewritingReady(cfg.OutputDir); err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("preplan 读取 progress: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("preplan 缺少 meta/progress.json")
	}
	baseChapter := progress.LatestCompleted()
	owner := pipelineExecutionOwner("preplan", baseChapter+1)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: baseChapter + 1,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("preplan 获取全书推演执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("preplan 释放全书推演执行锁: %w", err)
		}
	}()

	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("preplan 读取 layered outline: %w", err)
	}
	flat, err := st.Outline.LoadOutline()
	if err != nil {
		return fmt.Errorf("preplan 读取 outline: %w", err)
	}
	if len(volumes) > 0 {
		flat, err = pipelineSyncStableFlatOutline(st, volumes, flat)
		if err != nil {
			return err
		}
	}

	baseRoot, err := pipelineCanonRoot(cfg.OutputDir, progress)
	if err != nil {
		return err
	}
	generationID := strings.TrimSpace(progress.GenerationID)
	if generationID == "" {
		generationID = "planning-" + shortPipelineHash(baseRoot)
	}
	dependencies, sourceArtifacts, err := pipelinePlanningDependencies(cfg.OutputDir)
	if err != nil {
		return err
	}
	fingerprint, err := domain.NewDependencyFingerprint(generationID, baseRoot, dependencies)
	if err != nil {
		return fmt.Errorf("preplan 生成依赖指纹: %w", err)
	}

	bookNodes, volumeSkeletons, projections, total, err := pipelineBuildCausalSkeletons(volumes, flat)
	if err != nil {
		return err
	}
	next := baseChapter + 1
	if flags.Start > next {
		return fmt.Errorf("preplan 的 projected state 必须从正史下一章 %d 连续推演，不能用 --from=%d 跳过前驱", next, flags.Start)
	}
	if flags.End > 0 && flags.End != total {
		return fmt.Errorf("preplan 是全书阶段，禁止用 --to=%d 截断 projected chain；应省略 --to 或显式使用全书末章 --to=%d", flags.End, total)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	book := domain.BookCausalSkeleton{
		Version:               domain.PlanningStoreVersion,
		GenerationID:          generationID,
		BaseCanonChapter:      baseChapter,
		BaseCanonRoot:         baseRoot,
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		Nodes:                 bookNodes,
		CreatedAt:             now,
	}
	if err := st.Planning.SaveBookCausalSkeleton(book); err != nil {
		return fmt.Errorf("preplan 保存全书因果骨架: %w", err)
	}
	for i := range volumeSkeletons {
		volumeSkeletons[i].Version = domain.PlanningStoreVersion
		volumeSkeletons[i].GenerationID = generationID
		volumeSkeletons[i].BaseCanonChapter = baseChapter
		volumeSkeletons[i].BaseCanonRoot = baseRoot
		volumeSkeletons[i].DependencyFingerprint = fingerprint
		volumeSkeletons[i].Authority = domain.PlanningAuthoritySpeculative
		volumeSkeletons[i].Realization = domain.PlanningRealizationStaged
		volumeSkeletons[i].CreatedAt = now
		if err := st.Planning.SaveVolumeCausalSkeleton(volumeSkeletons[i]); err != nil {
			return fmt.Errorf("preplan 保存第 %d 卷因果骨架: %w", volumeSkeletons[i].Volume, err)
		}
	}

	limit := total
	manifests := make([]domain.StagedChapterPlanManifest, 0, max(0, limit-next+1))
	staged := make([]int, 0, cap(manifests))
	detailed := make([]int, 0, cap(manifests))
	coarse := make([]int, 0, cap(manifests))
	preStateRoot := baseRoot
	generationDir := shortPipelineHash(generationID + "\x00" + fingerprint.RootSHA256)
	for chapter := next; chapter <= limit; chapter++ {
		projected, ok := projections[chapter]
		if !ok {
			return fmt.Errorf("preplan 第 %d 章稳定章位不存在于 outline 或 layered outline；拒绝保存截断的全书 projected chain", chapter)
		}
		payload := pipelineProjectedChapterPayload{
			Version:         pipelinePlanningSchema,
			GenerationID:    generationID,
			Chapter:         chapter,
			Volume:          projected.Volume,
			Arc:             projected.Arc,
			Formal:          false,
			Authority:       "speculative",
			ProjectionLevel: projected.Level,
			Outline:         projected.Entry,
			Notice:          "非正史投影；只能用于后续当前章正式推演，不能直接渲染正文或推进 progress/world canon",
		}
		projectionRoot, err := domain.DeterministicPlanningHash(payload)
		if err != nil {
			return fmt.Errorf("preplan 第 %d 章投影摘要: %w", chapter, err)
		}
		planPath := filepath.ToSlash(filepath.Join(
			"meta", "planning", "generations", generationDir, "chapters", fmt.Sprintf("%06d.projected.json", chapter),
		))
		planSHA, err := writePipelinePlanningJSON(filepath.Join(cfg.OutputDir, filepath.FromSlash(planPath)), payload)
		if err != nil {
			return fmt.Errorf("preplan 保存第 %d 章非正史投影: %w", chapter, err)
		}
		postStateRoot, err := domain.DeriveProjectedStateRoot(
			chapter, generationID, baseRoot, fingerprint.RootSHA256, preStateRoot, projectionRoot,
		)
		if err != nil {
			return fmt.Errorf("preplan 第 %d 章 projected state: %w", chapter, err)
		}
		manifest := domain.StagedChapterPlanManifest{
			Version:               domain.PlanningStoreVersion,
			Chapter:               chapter,
			Volume:                projected.Volume,
			GenerationID:          generationID,
			BaseCanonChapter:      baseChapter,
			BaseCanonRoot:         baseRoot,
			DependencyFingerprint: fingerprint,
			Authority:             domain.PlanningAuthoritySpeculative,
			Realization:           domain.PlanningRealizationStaged,
			PlanPath:              planPath,
			PlanSHA256:            planSHA,
			ProjectedState: domain.ProjectedStateReceipt{
				Version:        domain.PlanningStoreVersion,
				Chapter:        chapter,
				GenerationID:   generationID,
				BaseCanonRoot:  baseRoot,
				DependencyRoot: fingerprint.RootSHA256,
				Authority:      domain.PlanningAuthorityProjected,
				Realization:    domain.PlanningRealizationStaged,
				PreStateRoot:   preStateRoot,
				ProjectionRoot: projectionRoot,
				PostStateRoot:  postStateRoot,
			},
			CreatedAt: now,
		}
		manifests = append(manifests, manifest)
		staged = append(staged, chapter)
		if projected.Level == pipelineProjectionExpanded {
			detailed = append(detailed, chapter)
		} else {
			coarse = append(coarse, chapter)
		}
		preStateRoot = postStateRoot
	}
	if err := st.Planning.ReplaceStagedChapterPlanManifests(manifests); err != nil {
		return fmt.Errorf("preplan 原子替换 projected plan chain: %w", err)
	}

	volumeIndices := make([]int, 0, len(volumeSkeletons))
	for _, skeleton := range volumeSkeletons {
		volumeIndices = append(volumeIndices, skeleton.Volume)
	}
	receipt := pipelinePreplanReceipt{
		Version:          pipelinePlanningSchema,
		GenerationID:     generationID,
		BaseCanonChapter: baseChapter,
		BaseCanonRoot:    baseRoot,
		CurrentCanonRoot: baseRoot,
		DependencyRoot:   fingerprint.RootSHA256,
		TotalChapters:    total,
		VolumeIndices:    volumeIndices,
		StagedChapters:   staged,
		DetailedChapters: detailed,
		CoarseChapters:   coarse,
		CreatedAt:        now,
		SourceArtifacts:  sourceArtifacts,
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath), receipt); err != nil {
		return fmt.Errorf("preplan 保存验收回执: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:preplan] 全书 %d 个稳定章位已建立非正史因果投影：详细投影 %d 章，骨架级投影 %d 章；正式 world simulation/POV plan 仍只在逐章 plan 阶段生成\n", total, len(detailed), len(coarse))
	return nil
}

func pipelinePlan(opts cliOptions, flags pipelineFlags) (returnErr error) {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		return fmt.Errorf("plan 阶段 RAG 就绪检查失败: %w", err)
	}
	if err := pipelineRequirePrewritingReady(cfg.OutputDir); err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if _, err := pipelineQueueCurrentExternalSamplingFailures(st, flags.Start, flags.End); err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	fresh, err := pipelineNeedsFreshWritingSession(cfg.OutputDir, progress)
	if err != nil {
		return err
	}
	if fresh {
		if err := pipelinePrepareFreshWritingSession(cfg.OutputDir, 1); err != nil {
			return err
		}
		st = store.NewStore(cfg.OutputDir)
	}
	chapter, progress, err := pipelineCurrentActionableChapter(st, flags)
	if err != nil {
		return err
	}
	if len(progress.PendingRewrites) == 0 && chapter > 1 {
		if err := requirePipelineAcceptedExactReview(cfg.OutputDir, chapter-1); err != nil {
			return fmt.Errorf("plan 第 %d 章前一章尚未完成 exact-body accept：%w", chapter, err)
		}
	}
	var preplanReceipt pipelinePreplanReceipt
	if err := readPipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath), &preplanReceipt); err != nil {
		return fmt.Errorf("plan 必须先有当前 preplan 回执: %w", err)
	}
	if err := validatePipelinePreplanFresh(st, preplanReceipt); err != nil {
		return err
	}
	if err := validatePipelineRebaseBoundary(preplanReceipt, chapter); err != nil {
		return err
	}
	projectedManifest, err := st.Planning.LoadStagedChapterPlanManifest(chapter)
	if err != nil {
		return fmt.Errorf("plan 读取第 %d 章 projected manifest: %w", chapter, err)
	}
	if projectedManifest == nil {
		switch {
		case chapter > preplanReceipt.BaseCanonChapter:
			return fmt.Errorf("plan 第 %d 章缺少全书 preplan manifest；禁止绕过 projected chain，必须重跑完整 preplan", chapter)
		case !slices.Contains(progress.PendingRewrites, chapter):
			return fmt.Errorf("plan 第 %d 章既无 preplan manifest，也不是当前正史返工章；禁止无投影正式规划", chapter)
		}
	}
	projectedPayload, err := loadAndVerifyPipelineProjectedPayload(cfg.OutputDir, projectedManifest)
	if err != nil {
		return fmt.Errorf("plan 验证第 %d 章 projected payload: %w", chapter, err)
	}
	if projectedPayload != nil {
		if projectedPayload.ProjectionLevel != pipelineProjectionExpanded {
			if _, err := st.Outline.GetChapterFromLayered(chapter); err != nil {
				return fmt.Errorf("plan 第 %d 章目前只是 %s 粗槽，禁止晋升为正式 plan；请先显式 expand_arc 展开该章所在弧: %w",
					chapter, projectedPayload.ProjectionLevel, err)
			}
			return fmt.Errorf("plan 第 %d 章 projected payload 仍标记为 %s；请先重跑 preplan 同步已展开弧", chapter, projectedPayload.ProjectionLevel)
		}
	}
	owner := pipelineExecutionOwner("plan", chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: chapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("plan 获取推演执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("plan 释放推演执行锁: %w", err)
		}
	}()

	before, err := capturePipelinePlanProseSnapshot(st, progress, chapter)
	if err != nil {
		return err
	}
	baselinePlanSeq := int64(0)
	if old := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "plan"); old != nil {
		baselinePlanSeq = old.Seq
	}
	cp, recovered, err := recoverPipelineUnfrozenFormalPlan(st, chapter, before.CommitSeq)
	if err != nil {
		return fmt.Errorf("plan 第 %d 章恢复未冻结正式计划: %w", chapter, err)
	}
	if !recovered {
		if err := retirePipelineFormalPlan(st, chapter, baselinePlanSeq); err != nil {
			return err
		}
		if err := headless.Run(cfg, bundle, headless.Options{
			StopAfterPlanChapter: chapter,
			PreserveUserRules:    true,
			SkipQueueReplay:      true,
		}); err != nil {
			return fmt.Errorf("plan 第 %d 章推演失败: %w", chapter, err)
		}
		// headless.Run owns a separate Store and appends the new plan checkpoint
		// there. CheckpointStore is an in-memory snapshot, so the Store captured
		// before the run cannot observe that append. Reload before freezing or a
		// valid new plan may be compared with the previous epoch's stale digest.
		st, cp, err = loadPipelineCurrentFormalPlanAfterHeadless(cfg.OutputDir, chapter)
		if err != nil {
			return fmt.Errorf("plan 第 %d 章未产出可冻结的正式计划: %w", chapter, err)
		}
		if cp.Seq <= baselinePlanSeq {
			return fmt.Errorf("plan 第 %d 章没有新增正式 plan epoch（baseline=%d current=%d）", chapter, baselinePlanSeq, cp.Seq)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[pipeline:plan] 恢复第 %d 章 checkpoint 后、freeze 前中断的正式计划 #%d\n", chapter, cp.Seq)
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return fmt.Errorf("plan 第 %d 章正式计划尚未满足 render freshness: %w", chapter, err)
	}
	if err := verifyPipelinePlanDidNotWriteProse(st, progress, chapter, before); err != nil {
		return err
	}
	frozen := pipelineFrozenPlan{
		Version:                 pipelinePlanningSchema,
		Chapter:                 chapter,
		PlanPath:                fmt.Sprintf("drafts/%02d.plan.json", chapter),
		PlanDigest:              cp.Digest,
		PlanCheckpointSeq:       cp.Seq,
		BaselineCommitSeq:       before.CommitSeq,
		BaselineCompletedDigest: before.CompletedDigest,
		PlanningGenerationID:    preplanReceipt.GenerationID,
		PlanningDependencyRoot:  preplanReceipt.DependencyRoot,
		ProjectionBinding:       "canonical_rewrite_rebase_required",
		FrozenAt:                time.Now().UTC().Format(time.RFC3339Nano),
	}
	if projectedManifest != nil {
		frozen.ProjectedPlanSHA256 = projectedManifest.PlanSHA256
		frozen.ProjectedPreStateRoot = projectedManifest.ProjectedState.PreStateRoot
		frozen.ProjectedPostStateRoot = projectedManifest.ProjectedState.PostStateRoot
		frozen.ProjectionBinding = "coarse_projection"
		if projectedPayload != nil && projectedPayload.ProjectionLevel == pipelineProjectionExpanded {
			frozen.ProjectionBinding = "detailed_projection"
		}
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelineFrozenPlanPath), frozen); err != nil {
		return fmt.Errorf("plan 保存冻结回执: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:plan] 第 %d 章正式计划已冻结：%s\n", chapter, cp.Digest)
	return nil
}

func loadPipelineCurrentFormalPlanAfterHeadless(
	outputDir string,
	chapter int,
) (*store.Store, *domain.Checkpoint, error) {
	fresh := store.NewStore(outputDir)
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(fresh, chapter)
	if err != nil {
		return fresh, nil, err
	}
	return fresh, cp, nil
}

// recoverPipelineUnfrozenFormalPlan closes the crash window between a durable
// formal-plan checkpoint and current_frozen_plan.json. It only adopts a plan
// newer than every causal/prose boundary and only when no still-valid frozen
// receipt already exists; ordinary --restart therefore continues to create a
// fresh epoch, while a freeze-step failure does not pay for the same LLM plan
// twice.
func recoverPipelineUnfrozenFormalPlan(
	st *store.Store,
	chapter int,
	baselineCommitSeq int64,
) (*domain.Checkpoint, bool, error) {
	if st == nil {
		return nil, false, fmt.Errorf("store is nil")
	}
	if frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir()); err == nil && frozen != nil {
		return nil, false, nil
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
	if err != nil {
		return nil, false, nil
	}
	if !pipelinePlanCheckpointAfterLatestBoundary(st.Checkpoints.All(), chapter, cp.Seq, baselineCommitSeq) {
		return nil, false, nil
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return nil, false, nil
	}
	return cp, true, nil
}

func pipelinePlanCheckpointAfterLatestBoundary(
	checkpoints []domain.Checkpoint,
	chapter int,
	planSeq int64,
	baselineCommitSeq int64,
) bool {
	boundary := baselineCommitSeq
	previousPlanSeq := int64(0)
	boundarySteps := map[string]struct{}{
		"causal-rewrite":           {},
		"rerender-request":         {},
		"chapter_world_simulation": {},
		"draft-structural-block":   {},
		"commit":                   {},
	}
	for _, item := range checkpoints {
		if item.Scope.Matches(domain.ChapterScope(chapter)) &&
			item.Step == "plan" &&
			item.Seq < planSeq &&
			item.Seq > previousPlanSeq {
			previousPlanSeq = item.Seq
		}
		if item.Scope.Matches(domain.ChapterScope(chapter)) {
			if _, ok := boundarySteps[item.Step]; ok && item.Seq > boundary {
				boundary = item.Seq
			}
		}
	}
	return boundary > previousPlanSeq && planSeq > boundary
}

func pipelineRender(opts cliOptions, flags pipelineFlags, state *domain.PipelineState) (returnErr error) {
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if _, err := pipelineQueueCurrentExternalSamplingFailures(st, flags.Start, flags.End); err != nil {
		return err
	}
	frozen, cp, err := loadAndVerifyPipelineFrozenPlan(cfg.OutputDir)
	if err != nil {
		return err
	}
	chapter := frozen.Chapter
	_, postCommitRecovery := pipelineCommittedAfterFrozenBaseline(st, frozen)
	if !postCommitRecovery {
		actionable, _, actionErr := pipelineCurrentActionableChapter(st, flags)
		if actionErr != nil {
			return actionErr
		}
		if frozen.Chapter != actionable {
			return fmt.Errorf("render 当前可行动章是第 %d 章，但冻结计划属于第 %d 章；必须先单独执行 plan", actionable, frozen.Chapter)
		}
		chapter = actionable
		if err := pipelineRequireRenderAttemptAvailable(st, chapter); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "[pipeline:render] 检测到第 %d 章 commit 已落盘但 render receipt 尚待收口；从冻结计划恢复 fresh review/receipt，不重复写正文\n", chapter)
	}
	var preplanReceipt pipelinePreplanReceipt
	if err := readPipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath), &preplanReceipt); err != nil {
		return fmt.Errorf("render 读取 preplan 回执: %w", err)
	}
	if frozen.PlanningGenerationID != preplanReceipt.GenerationID ||
		frozen.PlanningDependencyRoot != preplanReceipt.DependencyRoot {
		return fmt.Errorf("render 冻结计划未绑定当前 preplan generation；必须显式重跑 plan")
	}
	preplanForValidation := preplanReceipt
	if postCommitRecovery {
		progress, progressErr := st.Progress.Load()
		if progressErr != nil {
			return fmt.Errorf("render commit recovery 读取 progress: %w", progressErr)
		}
		if progress == nil {
			return fmt.Errorf("render commit recovery 缺少 progress")
		}
		if err := validatePipelinePostCommitProgressBoundary(progress, frozen); err != nil {
			return err
		}
		currentRoot, rootErr := pipelineCanonRoot(cfg.OutputDir, progress)
		if rootErr != nil {
			return rootErr
		}
		// The only canon drift allowed here is the durable commit bound to this
		// exact frozen plan. Dependency and invalidation checks still run.
		preplanForValidation.CurrentCanonRoot = currentRoot
	}
	if err := validatePipelinePreplanFresh(st, preplanForValidation); err != nil {
		return err
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return fmt.Errorf("render 正式计划 freshness 失败: %w", err)
	}
	projectedManifest, err := st.Planning.LoadStagedChapterPlanManifest(chapter)
	if err != nil {
		return fmt.Errorf("render 读取 projected manifest: %w", err)
	}
	if _, err := loadAndVerifyPipelineProjectedPayload(cfg.OutputDir, projectedManifest); err != nil {
		return fmt.Errorf("render projected payload 已漂移；必须显式重跑 preplan/plan: %w", err)
	}
	if err := validatePipelineFrozenProjectionBinding(frozen, projectedManifest); err != nil {
		return fmt.Errorf("render 冻结投影绑定失效；必须显式重跑 plan: %w", err)
	}
	owner := pipelineExecutionOwner("render", chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: chapter,
		PlanDigest:    frozen.PlanDigest,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("render 获取正文执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("render 释放正文执行锁: %w", err)
		}
	}()

	if !postCommitRecovery {
		renderFlags := flags
		renderFlags.WriteTo = chapter
		renderFlags.StopAfterCommit = chapter
		renderFlags.RenderOnly = true
		if err := pipelineWrite(opts, renderFlags, state); err != nil {
			return fmt.Errorf("render 第 %d 章失败（render lock 已禁止临时重规划）: %w", chapter, err)
		}
	}
	// headless writes checkpoints through another Store. CheckpointStore is a
	// point-in-time cache, so reusing the pre-render Store would miss the new
	// commit and falsely report that render produced nothing.
	st = reloadPipelineStore(cfg.OutputDir)
	currentPlan, err := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("render 后正式计划不可验证: %w", err)
	}
	if currentPlan.Digest != frozen.PlanDigest || currentPlan.Seq != cp.Seq {
		return fmt.Errorf("render 期间第 %d 章正式计划漂移（frozen=%s#%d current=%s#%d）",
			chapter, frozen.PlanDigest, cp.Seq, currentPlan.Digest, currentPlan.Seq)
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit")
	if commit == nil || commit.Seq <= frozen.BaselineCommitSeq {
		return fmt.Errorf("render 第 %d 章没有产生晚于冻结基线 #%d 的 commit checkpoint", chapter, frozen.BaselineCommitSeq)
	}
	chapterPath := fmt.Sprintf("chapters/%02d.md", chapter)
	bodySHA, err := pipelineRequiredFileSHA(cfg.OutputDir, chapterPath)
	if err != nil {
		return fmt.Errorf("render 验证正文: %w", err)
	}
	if commit.Artifact != chapterPath || commit.Digest != "sha256:"+bodySHA {
		return fmt.Errorf("render 第 %d 章 commit checkpoint 未绑定当前正文（artifact=%q digest=%s current=sha256:%s）",
			chapter, commit.Artifact, commit.Digest, bodySHA)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("render 读取提交后 progress: %w", err)
	}
	actualCanonRoot, err := pipelineCanonRoot(cfg.OutputDir, progress)
	if err != nil {
		return fmt.Errorf("render 计算提交后 canon root: %w", err)
	}
	// ProjectedState.PostStateRoot and the canonical artifact root are different
	// hash domains and must never be compared directly. Exact frozen-plan
	// identity plus a fresh exact-body accept realizes this chapter's projection;
	// semantic outcome drift needs a future homogeneous outcome-receipt schema.
	projectionBound := false
	downstreamInvalidated := false
	nextAction := ""
	reviewArgs := []string{"--from", fmt.Sprint(chapter), "--to", fmt.Sprint(chapter)}
	if flags.Budget > 0 {
		reviewArgs = append(reviewArgs, "--budget", flags.Budget.String())
	}
	if err := reviewExistingPipeline(opts, reviewArgs); err != nil {
		return fmt.Errorf("render 第 %d 章 fresh exact-body review 失败: %w", chapter, err)
	}
	if err := requirePipelineAcceptedExactReview(cfg.OutputDir, chapter); err != nil {
		if projectedManifest != nil {
			downstreamInvalidated, _ = appendPipelineProjectionInvalidation(
				st,
				*projectedManifest,
				chapter,
				fmt.Sprintf("chapter %d fresh exact-body review did not accept the realized projection: %v", chapter, err),
			)
		}
		return fmt.Errorf("render 第 %d 章未通过 fresh exact-body accept；下一步只能显式 preplan/plan 后再 render: %w", chapter, err)
	}
	progress, err = st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("render 读取复审后 progress: %w", err)
	}
	actualCanonRoot, err = pipelineCanonRoot(cfg.OutputDir, progress)
	if err != nil {
		return fmt.Errorf("render 计算复审后 canon root: %w", err)
	}
	projectionBound = projectedManifest != nil
	preplanReceipt.CurrentCanonRoot = actualCanonRoot
	if projectedManifest != nil {
		projectedManifest.Realization = domain.PlanningRealizationRendered
		projectedManifest.ProjectedState.Realization = domain.PlanningRealizationRendered
		if err := st.Planning.SaveStagedChapterPlanManifest(*projectedManifest); err != nil {
			return fmt.Errorf("render 标记第 %d 章 projected manifest 已实现: %w", chapter, err)
		}
		if !slices.Contains(preplanReceipt.RealizedChapters, chapter) {
			preplanReceipt.RealizedChapters = append(preplanReceipt.RealizedChapters, chapter)
			sort.Ints(preplanReceipt.RealizedChapters)
		}
	}
	// Until projected and actual state use one homogeneous outcome-receipt
	// schema, an exact frozen plan plus accepted exact-body review proves the
	// hand-off, not semantic root equality. Conservatively require a fresh
	// full-book projection before promoting any later future chapter.
	preplanReceipt.RebaseRequiredBeforeFuture = true
	if _, err := writePipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath), preplanReceipt); err != nil {
		return fmt.Errorf("render 推进 preplan realization 回执: %w", err)
	}
	nextAction = "render changed canonical state; refresh preplan before promoting any future chapter"
	receipt := pipelineRenderReceipt{
		Version:             pipelinePlanningSchema,
		Chapter:             chapter,
		PlanDigest:          frozen.PlanDigest,
		PlanCheckpointSeq:   frozen.PlanCheckpointSeq,
		CommitDigest:        commit.Digest,
		CommitCheckpointSeq: commit.Seq,
		ChapterPath:         chapterPath,
		ChapterBodySHA256:   bodySHA,
		ActualCanonRoot:     actualCanonRoot,
		ProjectedStateRoot:  frozen.ProjectedPostStateRoot,
		ProjectionBound:     projectionBound,
		DownstreamInvalid:   downstreamInvalidated,
		NextAction:          nextAction,
		RenderedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(cfg.OutputDir, pipelineRenderReceiptPath), receipt); err != nil {
		return fmt.Errorf("render 保存验收回执: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:render] 第 %d 章已按冻结计划渲染并提交\n", chapter)
	return nil
}

func reloadPipelineStore(outputDir string) *store.Store {
	return store.NewStore(outputDir)
}

func pipelineCommittedAfterFrozenBaseline(st *store.Store, frozen *pipelineFrozenPlan) (*domain.Checkpoint, bool) {
	if st == nil || frozen == nil || frozen.Chapter <= 0 {
		return nil, false
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(frozen.Chapter), "commit")
	return commit, commit != nil && commit.Seq > frozen.BaselineCommitSeq
}

func validatePipelinePostCommitProgressBoundary(progress *domain.Progress, frozen *pipelineFrozenPlan) error {
	if progress == nil || frozen == nil {
		return fmt.Errorf("render commit recovery progress/frozen plan is nil")
	}
	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)
	currentDigest, err := domain.DeterministicPlanningHash(completed)
	if err != nil {
		return err
	}
	if currentDigest == frozen.BaselineCompletedDigest {
		// Rewrite: the chapter was already part of completed canon.
		return nil
	}
	withoutChapter := slices.DeleteFunc(append([]int(nil), completed...), func(chapter int) bool {
		return chapter == frozen.Chapter
	})
	previousDigest, err := domain.DeterministicPlanningHash(withoutChapter)
	if err != nil {
		return err
	}
	if previousDigest != frozen.BaselineCompletedDigest {
		return fmt.Errorf("render commit recovery 检测到冻结章以外的 completed_chapters 漂移；拒绝把未知正史变化伪装成第 %d 章 commit", frozen.Chapter)
	}
	return nil
}

func pipelineCurrentActionableChapter(st *store.Store, flags pipelineFlags) (int, *domain.Progress, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return 0, nil, fmt.Errorf("读取 progress: %w", err)
	}
	if progress == nil {
		return 0, nil, fmt.Errorf("缺少 meta/progress.json")
	}
	chapter := 0
	if len(progress.PendingRewrites) > 0 {
		chapter = progress.PendingRewrites[0]
	} else if progress.Phase != domain.PhaseComplete {
		chapter = progress.NextChapter()
	}
	if chapter <= 0 {
		return 0, progress, fmt.Errorf("当前没有可 plan/render 的章节")
	}
	if flags.Start > 0 && chapter < flags.Start {
		return 0, progress, fmt.Errorf("第 %d 章仍是因果链最早可行动章，不能用 --from=%d 跳过", chapter, flags.Start)
	}
	if flags.End > 0 && chapter > flags.End {
		return 0, progress, fmt.Errorf("当前可行动章 %d 超出 --to=%d", chapter, flags.End)
	}
	return chapter, progress, nil
}

func validatePipelineRebaseBoundary(receipt pipelinePreplanReceipt, chapter int) error {
	if receipt.RebaseRequiredBeforeFuture && chapter > receipt.BaseCanonChapter {
		return fmt.Errorf(
			"plan 已渲染章节改变了 preplan 基准正史；进入第 %d 章前必须显式重跑 preplan 重新投影未来链",
			chapter,
		)
	}
	return nil
}

func retirePipelineFormalPlan(st *store.Store, chapter int, checkpointSeq int64) error {
	planRel := fmt.Sprintf("drafts/%02d.plan.json", chapter)
	planPath := filepath.Join(st.Dir(), filepath.FromSlash(planRel))
	raw, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("plan 读取旧正式计划: %w", err)
	}
	epoch := time.Now().UTC().Format("20060102T150405.000000000Z")
	archiveRel := filepath.ToSlash(filepath.Join(
		"meta", "planning", "retired_formal_plans",
		fmt.Sprintf("ch%06d", chapter),
		fmt.Sprintf("%s-plan-seq-%d.json", epoch, checkpointSeq),
	))
	if err := atomicWriteRewriteFile(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel)), raw, 0o644); err != nil {
		return fmt.Errorf("plan 归档旧正式计划: %w", err)
	}
	meta := map[string]any{
		"version":              pipelinePlanningSchema,
		"chapter":              chapter,
		"source":               planRel,
		"archive":              archiveRel,
		"plan_checkpoint_seq":  checkpointSeq,
		"retired_for":          "split plan stage requires a fresh formal plan epoch",
		"retired_at":           time.Now().UTC().Format(time.RFC3339Nano),
		"retired_plan_sha256":  pipelineBytesSHA(raw),
		"canonical_authority":  false,
		"may_render_from_copy": false,
	}
	if _, err := writePipelinePlanningJSON(strings.TrimSuffix(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel)), ".json")+".retirement.json", meta); err != nil {
		return fmt.Errorf("plan 保存旧计划归档回执: %w", err)
	}
	for _, rel := range []string{
		planRel,
		fmt.Sprintf("drafts/%02d.plan_consistency.json", chapter),
	} {
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("plan 退役旧计划 %s: %w", rel, err)
		}
	}
	return nil
}

func requirePipelineAcceptedExactReview(outputDir string, chapter int) error {
	inspection := inspectCurrentChapterReview(outputDir, chapter)
	if len(inspection.Issues) > 0 {
		return fmt.Errorf("第 %d 章 review 非当前 exact body：%s", chapter, strings.Join(inspection.Issues, "；"))
	}
	if !pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) {
		return fmt.Errorf("第 %d 章 review verdict=%q disposition=%q，要求 accept 且 disposition 为否/可选", chapter, inspection.Verdict, inspection.Disposition)
	}
	return nil
}

func pipelineReviewAcceptedForProjection(verdict, disposition string) bool {
	if strings.TrimSpace(verdict) != "accept" {
		return false
	}
	switch strings.TrimSpace(disposition) {
	case "否", "可选":
		return true
	default:
		return false
	}
}

func appendPipelineProjectionInvalidation(
	st *store.Store,
	current domain.StagedChapterPlanManifest,
	chapter int,
	reason string,
) (bool, error) {
	manifests, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil {
		return false, err
	}
	last := 0
	for _, manifest := range manifests {
		if manifest.Chapter > chapter && manifest.Chapter > last {
			last = manifest.Chapter
		}
	}
	if last == 0 {
		return false, nil
	}
	id := "render-drift-" + shortPipelineHash(strings.Join([]string{
		current.GenerationID,
		fmt.Sprint(chapter),
		current.ProjectedState.PostStateRoot,
		reason,
	}, "\x00"))
	existing, err := st.Planning.LoadInvalidations()
	if err != nil {
		return false, err
	}
	for _, record := range existing {
		if record.ID == id {
			return true, nil
		}
	}
	record := domain.PlanningInvalidationRecord{
		Version:               domain.PlanningStoreVersion,
		ID:                    id,
		GenerationID:          current.GenerationID,
		BaseCanonRoot:         current.BaseCanonRoot,
		DependencyFingerprint: current.DependencyFingerprint,
		TargetKind:            "staged_chapter_plan_chain",
		TargetID:              fmt.Sprintf("%d-%d", chapter+1, last),
		InvalidatedRoot:       current.ProjectedState.PostStateRoot,
		Reason:                reason,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := st.Planning.AppendInvalidation(record); err != nil {
		return false, fmt.Errorf("append planning invalidation: %w", err)
	}
	return true, nil
}

func capturePipelinePlanProseSnapshot(st *store.Store, progress *domain.Progress, chapter int) (pipelinePlanProseSnapshot, error) {
	if progress == nil {
		var err error
		progress, err = st.Progress.Load()
		if err != nil {
			return pipelinePlanProseSnapshot{}, err
		}
	}
	if progress == nil {
		return pipelinePlanProseSnapshot{}, fmt.Errorf("plan snapshot 缺少 progress")
	}
	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)
	completedDigest, err := domain.DeterministicPlanningHash(completed)
	if err != nil {
		return pipelinePlanProseSnapshot{}, err
	}
	draftDigest, err := pipelineOptionalFileSHA(st.Dir(), fmt.Sprintf("drafts/%02d.draft.md", chapter))
	if err != nil {
		return pipelinePlanProseSnapshot{}, err
	}
	chapterDigest, err := pipelineOptionalFileSHA(st.Dir(), fmt.Sprintf("chapters/%02d.md", chapter))
	if err != nil {
		return pipelinePlanProseSnapshot{}, err
	}
	return pipelinePlanProseSnapshot{
		CompletedDigest: completedDigest,
		TotalWordCount:  progress.TotalWordCount,
		CommitSeq:       latestPipelineChapterCommitSeq(st.Dir(), chapter),
		DraftDigest:     draftDigest,
		ChapterDigest:   chapterDigest,
	}, nil
}

func verifyPipelinePlanDidNotWriteProse(st *store.Store, beforeProgress *domain.Progress, chapter int, before pipelinePlanProseSnapshot) error {
	afterProgress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	after, err := capturePipelinePlanProseSnapshot(st, afterProgress, chapter)
	if err != nil {
		return err
	}
	if before.CompletedDigest != after.CompletedDigest || before.TotalWordCount != after.TotalWordCount {
		return fmt.Errorf("plan 阶段越权推进 progress 完成态（completed/word_count 已变化）")
	}
	if before.CommitSeq != after.CommitSeq || before.ChapterDigest != after.ChapterDigest {
		return fmt.Errorf("plan 阶段越权提交或改写第 %d 章终稿", chapter)
	}
	if before.DraftDigest != after.DraftDigest {
		return fmt.Errorf("plan 阶段越权生成或改写第 %d 章正文草稿", chapter)
	}
	return nil
}

func loadAndVerifyPipelineFrozenPlan(outputDir string) (*pipelineFrozenPlan, *domain.Checkpoint, error) {
	var frozen pipelineFrozenPlan
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelineFrozenPlanPath), &frozen); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("render 缺少冻结计划 %s；请先执行 --pipeline --stages plan", pipelineFrozenPlanPath)
		}
		return nil, nil, fmt.Errorf("render 读取冻结计划: %w", err)
	}
	if frozen.Version != pipelinePlanningSchema || frozen.Chapter <= 0 ||
		strings.TrimSpace(frozen.PlanDigest) == "" || frozen.PlanCheckpointSeq <= 0 {
		return nil, nil, fmt.Errorf("冻结计划回执无效")
	}
	wantPath := fmt.Sprintf("drafts/%02d.plan.json", frozen.Chapter)
	if frozen.PlanPath != wantPath {
		return nil, nil, fmt.Errorf("冻结计划路径 %q 不是正式计划 %q", frozen.PlanPath, wantPath)
	}
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(outputDir), frozen.Chapter)
	if err != nil {
		return nil, nil, fmt.Errorf("冻结计划已失效: %w", err)
	}
	if cp.Digest != frozen.PlanDigest || cp.Seq != frozen.PlanCheckpointSeq {
		return nil, nil, fmt.Errorf("冻结计划漂移（frozen=%s#%d current=%s#%d）；必须重新执行 plan",
			frozen.PlanDigest, frozen.PlanCheckpointSeq, cp.Digest, cp.Seq)
	}
	return &frozen, cp, nil
}

func loadAndVerifyPipelineProjectedPayload(
	outputDir string,
	manifest *domain.StagedChapterPlanManifest,
) (*pipelineProjectedChapterPayload, error) {
	if manifest == nil {
		return nil, nil
	}
	if err := domain.ValidateStagedChapterPlanManifest(*manifest); err != nil {
		return nil, err
	}
	gotSHA, err := pipelineRequiredFileSHA(outputDir, manifest.PlanPath)
	if err != nil {
		return nil, err
	}
	if gotSHA != manifest.PlanSHA256 {
		return nil, fmt.Errorf("第 %d 章 projected payload 文件 SHA 漂移", manifest.Chapter)
	}
	var payload pipelineProjectedChapterPayload
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, filepath.FromSlash(manifest.PlanPath)), &payload); err != nil {
		return nil, err
	}
	if payload.Version != pipelinePlanningSchema || payload.Formal ||
		payload.Chapter != manifest.Chapter ||
		payload.GenerationID != manifest.GenerationID ||
		payload.Authority != "speculative" ||
		payload.Outline.Chapter != manifest.Chapter ||
		(payload.ProjectionLevel != pipelineProjectionExpanded && payload.ProjectionLevel != pipelineProjectionCoarse) {
		return nil, fmt.Errorf("第 %d 章 projected payload identity/authority/level 无效", manifest.Chapter)
	}
	projectionRoot, err := domain.DeterministicPlanningHash(payload)
	if err != nil {
		return nil, err
	}
	if projectionRoot != manifest.ProjectedState.ProjectionRoot {
		return nil, fmt.Errorf(
			"第 %d 章 projected payload root 漂移（manifest=%s payload=%s）",
			manifest.Chapter,
			manifest.ProjectedState.ProjectionRoot,
			projectionRoot,
		)
	}
	return &payload, nil
}

func validatePipelineFrozenProjectionBinding(
	frozen *pipelineFrozenPlan,
	manifest *domain.StagedChapterPlanManifest,
) error {
	if frozen == nil {
		return fmt.Errorf("frozen plan is nil")
	}
	if manifest == nil {
		if frozen.ProjectionBinding != "canonical_rewrite_rebase_required" ||
			frozen.ProjectedPlanSHA256 != "" ||
			frozen.ProjectedPreStateRoot != "" ||
			frozen.ProjectedPostStateRoot != "" {
			return fmt.Errorf("canonical rewrite freeze contains an invalid projected manifest binding")
		}
		return nil
	}
	if frozen.ProjectionBinding != "detailed_projection" {
		return fmt.Errorf("future render requires a detailed projection, got %q", frozen.ProjectionBinding)
	}
	if frozen.ProjectedPlanSHA256 != manifest.PlanSHA256 ||
		frozen.ProjectedPreStateRoot != manifest.ProjectedState.PreStateRoot ||
		frozen.ProjectedPostStateRoot != manifest.ProjectedState.PostStateRoot {
		return fmt.Errorf("frozen plan SHA/pre-state/post-state roots do not match the current manifest")
	}
	return nil
}

func verifyPipelinePreplanStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	var receipt pipelinePreplanReceipt
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelinePlanningReceiptPath), &receipt); err != nil {
		evidence.Missing = append(evidence.Missing, pipelinePlanningReceiptPath)
		return evidence, fmt.Errorf("preplan 缺少验收回执: %w", err)
	}
	if receipt.Version != pipelinePlanningSchema || receipt.GenerationID == "" || receipt.BaseCanonRoot == "" ||
		receipt.CurrentCanonRoot == "" || receipt.DependencyRoot == "" {
		return evidence, fmt.Errorf("preplan 验收回执字段不完整")
	}
	st := store.NewStore(outputDir)
	if err := validatePipelinePreplanFresh(st, receipt); err != nil {
		return evidence, err
	}
	book, err := st.Planning.LoadBookCausalSkeleton()
	if err != nil || book == nil {
		return evidence, fmt.Errorf("preplan 全书因果骨架无效: %w", err)
	}
	if book.GenerationID != receipt.GenerationID || book.BaseCanonRoot != receipt.BaseCanonRoot ||
		book.DependencyFingerprint.RootSHA256 != receipt.DependencyRoot {
		return evidence, fmt.Errorf("preplan 回执与全书因果骨架指纹不一致")
	}
	manifests, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil {
		return evidence, fmt.Errorf("preplan projected plan chain 无效: %w", err)
	}
	expectedCount := receipt.TotalChapters - receipt.BaseCanonChapter
	if expectedCount < 0 || len(manifests) != expectedCount {
		return evidence, fmt.Errorf(
			"preplan projected plan chain 被截断：base=%d total=%d manifests=%d want=%d",
			receipt.BaseCanonChapter,
			receipt.TotalChapters,
			len(manifests),
			expectedCount,
		)
	}
	gotChapters := make([]int, 0, len(manifests))
	realized := make([]int, 0, len(receipt.RealizedChapters))
	sawStaged := false
	for i, manifest := range manifests {
		wantChapter := receipt.BaseCanonChapter + i + 1
		if manifest.Chapter != wantChapter {
			return evidence, fmt.Errorf("preplan projected plan chain 不连续：index=%d chapter=%d want=%d", i, manifest.Chapter, wantChapter)
		}
		gotChapters = append(gotChapters, manifest.Chapter)
		if manifest.GenerationID != receipt.GenerationID || manifest.BaseCanonRoot != receipt.BaseCanonRoot ||
			manifest.DependencyFingerprint.RootSHA256 != receipt.DependencyRoot {
			return evidence, fmt.Errorf("preplan 第 %d 章 manifest 不属于当前回执 generation", manifest.Chapter)
		}
		if _, err := loadAndVerifyPipelineProjectedPayload(outputDir, &manifest); err != nil {
			return evidence, fmt.Errorf("preplan 第 %d 章 projected payload 无效: %w", manifest.Chapter, err)
		}
		switch manifest.Realization {
		case domain.PlanningRealizationRendered:
			if sawStaged {
				return evidence, fmt.Errorf("preplan rendered manifests 不是连续前缀：第 %d 章越过 staged predecessor", manifest.Chapter)
			}
			if err := requirePipelineAcceptedExactReview(outputDir, manifest.Chapter); err != nil {
				return evidence, fmt.Errorf("preplan 第 %d 章标记 rendered 但 exact-body review 不成立: %w", manifest.Chapter, err)
			}
			realized = append(realized, manifest.Chapter)
		case domain.PlanningRealizationStaged:
			sawStaged = true
		default:
			return evidence, fmt.Errorf("preplan 第 %d 章 realization=%s 不可用于活动链", manifest.Chapter, manifest.Realization)
		}
		evidence.Artifacts = append(evidence.Artifacts,
			filepath.ToSlash(filepath.Join("meta", "planning", "chapters", fmt.Sprintf("%06d.json", manifest.Chapter))),
			manifest.PlanPath,
		)
	}
	if !slices.Equal(gotChapters, receipt.StagedChapters) {
		return evidence, fmt.Errorf("preplan 回执 staged_chapters=%v 与 manifests=%v 不一致", receipt.StagedChapters, gotChapters)
	}
	classified := append(append([]int(nil), receipt.DetailedChapters...), receipt.CoarseChapters...)
	sort.Ints(classified)
	if !slices.Equal(classified, receipt.StagedChapters) {
		return evidence, fmt.Errorf("preplan 回执 detailed/coarse 分类没有完整覆盖 staged chain")
	}
	if !slices.Equal(realized, receipt.RealizedChapters) {
		return evidence, fmt.Errorf("preplan 回执 realized_chapters=%v 与 rendered manifest prefix=%v 不一致", receipt.RealizedChapters, realized)
	}
	for _, volume := range receipt.VolumeIndices {
		skeleton, err := st.Planning.LoadVolumeCausalSkeleton(volume)
		if err != nil || skeleton == nil {
			return evidence, fmt.Errorf("preplan 第 %d 卷因果骨架无效: %w", volume, err)
		}
		if skeleton.GenerationID != receipt.GenerationID || skeleton.BaseCanonRoot != receipt.BaseCanonRoot {
			return evidence, fmt.Errorf("preplan 第 %d 卷因果骨架 generation/base 不一致", volume)
		}
		evidence.Artifacts = append(evidence.Artifacts,
			filepath.ToSlash(filepath.Join("meta", "planning", "volumes", fmt.Sprintf("%03d.json", volume))),
		)
	}
	evidence.Artifacts = append(evidence.Artifacts, pipelinePlanningReceiptPath, "meta/planning/book_causal_skeleton.json")
	evidence.Checkpoints = append(evidence.Checkpoints, "projected-state:"+receipt.DependencyRoot)
	evidence.Message = fmt.Sprintf("coarse skeleton ready; staged projected chapters=%d", len(manifests))
	if len(receipt.CoarseChapters) > 0 {
		evidence.Message += fmt.Sprintf("; coarse-only chapters=%d", len(receipt.CoarseChapters))
	}
	if receipt.BlockedAtChapter > 0 {
		evidence.Message += fmt.Sprintf("; detailed projection stops before unexpanded chapter %d", receipt.BlockedAtChapter)
	}
	return evidence, nil
}

func validatePipelinePreplanFresh(st *store.Store, receipt pipelinePreplanReceipt) error {
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("preplan freshness 读取 progress: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("preplan freshness 缺少 progress")
	}
	currentRoot, err := pipelineCanonRoot(st.Dir(), progress)
	if err != nil {
		return err
	}
	if currentRoot != receipt.CurrentCanonRoot {
		return fmt.Errorf("preplan 已因未登记的正史状态变化失效（expected current root=%s actual=%s）；必须显式重跑 preplan，再重跑 plan",
			receipt.CurrentCanonRoot, currentRoot)
	}
	dependencies, _, err := pipelinePlanningDependencies(st.Dir())
	if err != nil {
		return err
	}
	fingerprint, err := domain.NewDependencyFingerprint(receipt.GenerationID, receipt.BaseCanonRoot, dependencies)
	if err != nil {
		return err
	}
	if fingerprint.RootSHA256 != receipt.DependencyRoot {
		return fmt.Errorf("preplan 依赖指纹已漂移（planned=%s current=%s）；必须显式重跑 preplan，再重跑 plan",
			receipt.DependencyRoot, fingerprint.RootSHA256)
	}
	invalidations, err := st.Planning.LoadInvalidations()
	if err != nil {
		return fmt.Errorf("读取 planning invalidations: %w", err)
	}
	for i := len(invalidations) - 1; i >= 0; i-- {
		record := invalidations[i]
		if record.GenerationID == receipt.GenerationID &&
			record.BaseCanonRoot == receipt.BaseCanonRoot &&
			record.DependencyFingerprint.RootSHA256 == receipt.DependencyRoot {
			return fmt.Errorf("preplan projected chain 已失效（%s）；必须显式重跑 preplan，再重跑 plan", record.Reason)
		}
	}
	return nil
}

func verifyPipelinePlanStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	frozen, cp, err := loadAndVerifyPipelineFrozenPlan(outputDir)
	if err != nil {
		evidence.Missing = append(evidence.Missing, pipelineFrozenPlanPath)
		return evidence, err
	}
	var preplan pipelinePreplanReceipt
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelinePlanningReceiptPath), &preplan); err != nil {
		return evidence, fmt.Errorf("plan 缺少绑定的 preplan 回执: %w", err)
	}
	if frozen.PlanningGenerationID != preplan.GenerationID || frozen.PlanningDependencyRoot != preplan.DependencyRoot {
		return evidence, fmt.Errorf("plan 冻结回执未绑定当前 preplan generation")
	}
	if err := validatePipelinePreplanFresh(store.NewStore(outputDir), preplan); err != nil {
		return evidence, err
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(store.NewStore(outputDir), frozen.Chapter); err != nil {
		return evidence, fmt.Errorf("plan render freshness 已失效: %w", err)
	}
	st := store.NewStore(outputDir)
	manifest, err := st.Planning.LoadStagedChapterPlanManifest(frozen.Chapter)
	if err != nil {
		return evidence, fmt.Errorf("plan 读取 frozen projected manifest: %w", err)
	}
	if _, err := loadAndVerifyPipelineProjectedPayload(outputDir, manifest); err != nil {
		return evidence, fmt.Errorf("plan projected payload 已漂移: %w", err)
	}
	if err := validatePipelineFrozenProjectionBinding(frozen, manifest); err != nil {
		return evidence, fmt.Errorf("plan frozen projection binding 无效: %w", err)
	}
	evidence.Artifacts = append(evidence.Artifacts, pipelineFrozenPlanPath, frozen.PlanPath)
	evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:plan#%d:%s", frozen.Chapter, cp.Seq, cp.Digest))
	evidence.Message = fmt.Sprintf("chapter %d formal plan frozen", frozen.Chapter)
	return evidence, nil
}

func verifyPipelineRenderStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	var receipt pipelineRenderReceipt
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelineRenderReceiptPath), &receipt); err != nil {
		evidence.Missing = append(evidence.Missing, pipelineRenderReceiptPath)
		return evidence, fmt.Errorf("render 缺少验收回执: %w", err)
	}
	if receipt.Version != pipelinePlanningSchema || receipt.Chapter <= 0 || receipt.CommitCheckpointSeq <= 0 ||
		receipt.PlanDigest == "" || receipt.CommitDigest == "" || receipt.ChapterBodySHA256 == "" || receipt.ActualCanonRoot == "" {
		return evidence, fmt.Errorf("render 验收回执字段不完整")
	}
	frozen, cp, err := loadAndVerifyPipelineFrozenPlan(outputDir)
	if err != nil {
		return evidence, err
	}
	if frozen.Chapter != receipt.Chapter || frozen.PlanDigest != receipt.PlanDigest || cp.Seq != receipt.PlanCheckpointSeq {
		return evidence, fmt.Errorf("render 回执与冻结计划不一致")
	}
	st := store.NewStore(outputDir)
	manifest, err := st.Planning.LoadStagedChapterPlanManifest(receipt.Chapter)
	if err != nil {
		return evidence, fmt.Errorf("render 读取 projected manifest: %w", err)
	}
	if _, err := loadAndVerifyPipelineProjectedPayload(outputDir, manifest); err != nil {
		return evidence, fmt.Errorf("render projected payload 已漂移: %w", err)
	}
	if err := validatePipelineFrozenProjectionBinding(frozen, manifest); err != nil {
		return evidence, fmt.Errorf("render frozen projection binding 无效: %w", err)
	}
	if receipt.ProjectionBound != (manifest != nil) {
		return evidence, fmt.Errorf("render projection_receipt_bound 与 manifest 存在性不一致")
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(receipt.Chapter), "commit")
	if commit == nil || commit.Seq != receipt.CommitCheckpointSeq || commit.Digest != receipt.CommitDigest {
		return evidence, fmt.Errorf("render commit checkpoint 已缺失或漂移")
	}
	bodySHA, err := pipelineRequiredFileSHA(outputDir, receipt.ChapterPath)
	if err != nil {
		return evidence, err
	}
	if bodySHA != receipt.ChapterBodySHA256 {
		return evidence, fmt.Errorf("render 正文已偏离验收回执")
	}
	if err := requirePipelineAcceptedExactReview(outputDir, receipt.Chapter); err != nil {
		return evidence, fmt.Errorf("render fresh exact-body accept 已失效: %w", err)
	}
	progress, err := store.NewStore(outputDir).Progress.Load()
	if err != nil || progress == nil {
		return evidence, fmt.Errorf("render 读取 progress: %w", err)
	}
	currentRoot, err := pipelineCanonRoot(outputDir, progress)
	if err != nil {
		return evidence, err
	}
	if currentRoot != receipt.ActualCanonRoot {
		return evidence, fmt.Errorf("render 后 canon root 已漂移；需显式重跑 preplan/plan")
	}
	evidence.Artifacts = append(evidence.Artifacts, pipelineRenderReceiptPath, receipt.ChapterPath)
	evidence.Checkpoints = append(evidence.Checkpoints,
		fmt.Sprintf("chapter:%d:plan#%d:%s", receipt.Chapter, receipt.PlanCheckpointSeq, receipt.PlanDigest),
		fmt.Sprintf("chapter:%d:commit#%d:%s", receipt.Chapter, receipt.CommitCheckpointSeq, receipt.CommitDigest),
	)
	evidence.CompletedChapters = receipt.Chapter
	evidence.Message = fmt.Sprintf("chapter %d rendered from frozen formal plan", receipt.Chapter)
	if receipt.NextAction != "" {
		evidence.Message += "; " + receipt.NextAction
	}
	return evidence, nil
}

func pipelineBuildCausalSkeletons(volumes []domain.VolumeOutline, flat []domain.OutlineEntry) (
	[]domain.CausalSkeletonNode,
	[]domain.VolumeCausalSkeleton,
	map[int]pipelineProjectionEntry,
	int,
	error,
) {
	projections := make(map[int]pipelineProjectionEntry)
	if len(volumes) == 0 {
		if len(flat) == 0 {
			return nil, nil, nil, 0, fmt.Errorf("preplan 缺少可用 outline")
		}
		sort.Slice(flat, func(i, j int) bool { return flat[i].Chapter < flat[j].Chapter })
		for _, entry := range flat {
			if entry.Chapter > 0 {
				projections[entry.Chapter] = pipelineProjectionEntry{Entry: entry, Volume: 1, Arc: 1, Level: pipelineProjectionExpanded}
			}
		}
		first, last := flat[0], flat[len(flat)-1]
		book := []domain.CausalSkeletonNode{{
			ID:          "book-outline",
			Cause:       firstNonEmptyString(first.CoreEvent, first.Title, "故事开端"),
			Effect:      firstNonEmptyString(last.Hook, last.CoreEvent, last.Title, "完成现有大纲承诺"),
			ChapterFrom: first.Chapter,
			ChapterTo:   last.Chapter,
		}}
		volume := domain.VolumeCausalSkeleton{
			Volume: 1, ChapterFrom: first.Chapter, ChapterTo: last.Chapter, Nodes: append([]domain.CausalSkeletonNode(nil), book...),
		}
		return book, []domain.VolumeCausalSkeleton{volume}, projections, last.Chapter, nil
	}

	bookNodes := make([]domain.CausalSkeletonNode, 0, len(volumes))
	volumeSkeletons := make([]domain.VolumeCausalSkeleton, 0, len(volumes))
	cursor := 1
	previousVolumeID := ""
	for _, volume := range volumes {
		if len(volume.Arcs) == 0 && volume.Index <= 0 {
			// Historical layered outlines may carry a V0 compatibility shell.
			// It owns no chapter positions and is not a real planning volume.
			continue
		}
		volumeFrom := cursor
		arcNodes := make([]domain.CausalSkeletonNode, 0, len(volume.Arcs))
		previousArcID := ""
		for _, arc := range volume.Arcs {
			span := arc.ChapterSpan()
			if span <= 0 {
				return nil, nil, nil, 0, fmt.Errorf("preplan V%d A%d 缺少 chapters 和 estimated_chapters，无法稳定分配章号", volume.Index, arc.Index)
			}
			arcFrom, arcTo := cursor, cursor+span-1
			arcID := fmt.Sprintf("v%03d-a%03d", volume.Index, arc.Index)
			effect := firstNonEmptyString(arc.Title, arc.Goal)
			if len(arc.Chapters) > 0 {
				last := arc.Chapters[len(arc.Chapters)-1]
				effect = firstNonEmptyString(last.Hook, last.CoreEvent, effect)
				for i, entry := range arc.Chapters {
					entry.Chapter = cursor + i
					projections[entry.Chapter] = pipelineProjectionEntry{
						Entry: entry, Volume: volume.Index, Arc: arc.Index, Level: pipelineProjectionExpanded,
					}
				}
			} else {
				effect = "兑现骨架弧目标：" + effect
				for chapter := arcFrom; chapter <= arcTo; chapter++ {
					slot := chapter - arcFrom + 1
					projections[chapter] = pipelineProjectionEntry{
						Entry: domain.OutlineEntry{
							Chapter:   chapter,
							Title:     fmt.Sprintf("%s（骨架位 %d/%d）", firstNonEmptyString(arc.Title, "未展开弧"), slot, span),
							CoreEvent: firstNonEmptyString(arc.Goal, volume.Theme, "承接并推进当前弧目标"),
							Hook:      "待本弧展开时细化；当前只承诺因果方向，不承诺场景或正文",
						},
						Volume: volume.Index,
						Arc:    arc.Index,
						Level:  pipelineProjectionCoarse,
					}
				}
			}
			node := domain.CausalSkeletonNode{
				ID:          arcID,
				Cause:       firstNonEmptyString(arc.Goal, volume.Theme, arc.Title, "承接前序状态"),
				Effect:      effect,
				ChapterFrom: arcFrom,
				ChapterTo:   arcTo,
			}
			if previousArcID != "" {
				node.DependsOn = []string{previousArcID}
			}
			arcNodes = append(arcNodes, node)
			previousArcID = arcID
			cursor += span
		}
		if len(arcNodes) == 0 {
			return nil, nil, nil, 0, fmt.Errorf("preplan 第 %d 卷没有任何 arc", volume.Index)
		}
		volumeTo := cursor - 1
		volumeSkeletons = append(volumeSkeletons, domain.VolumeCausalSkeleton{
			Volume: volume.Index, ChapterFrom: volumeFrom, ChapterTo: volumeTo, Nodes: arcNodes,
		})
		volumeID := fmt.Sprintf("volume-%03d", volume.Index)
		bookNode := domain.CausalSkeletonNode{
			ID:          volumeID,
			Cause:       firstNonEmptyString(volume.Theme, volume.Title, arcNodes[0].Cause),
			Effect:      arcNodes[len(arcNodes)-1].Effect,
			ChapterFrom: volumeFrom,
			ChapterTo:   volumeTo,
		}
		if previousVolumeID != "" {
			bookNode.DependsOn = []string{previousVolumeID}
		}
		bookNodes = append(bookNodes, bookNode)
		previousVolumeID = volumeID
	}
	return bookNodes, volumeSkeletons, projections, cursor - 1, nil
}

func pipelineSyncStableFlatOutline(st *store.Store, volumes []domain.VolumeOutline, existing []domain.OutlineEntry) ([]domain.OutlineEntry, error) {
	stable := domain.FlattenOutline(volumes)
	if len(stable) == 0 {
		return nil, fmt.Errorf("preplan layered outline 没有任何已展开的详细章节，不能生成 stable flat outline")
	}
	if len(existing) > 0 {
		if len(existing) != len(stable) {
			return nil, fmt.Errorf("preplan 拒绝覆盖内容不对应的 flat outline：existing=%d stable=%d", len(existing), len(stable))
		}
		for i := range stable {
			if strings.TrimSpace(existing[i].Title) != strings.TrimSpace(stable[i].Title) {
				return nil, fmt.Errorf("preplan flat outline 第 %d 个详细条目标题不对应 layered outline（existing=%q layered=%q）",
					i+1, existing[i].Title, stable[i].Title)
			}
		}
	}
	if err := st.Outline.SaveOutline(stable); err != nil {
		return nil, fmt.Errorf("preplan 刷新稳定 flat outline: %w", err)
	}
	return stable, nil
}

func pipelinePlanningDependencies(outputDir string) ([]domain.PlanningDependency, []string, error) {
	outlinePath := "layered_outline.json"
	if _, err := os.Stat(filepath.Join(outputDir, outlinePath)); err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, err
		}
		outlinePath = "outline.json"
	}
	required := []struct {
		kind string
		path string
	}{
		{kind: "outline", path: outlinePath},
		{kind: "world_rules", path: "world_rules.json"},
		{kind: "compass", path: "meta/compass.json"},
	}
	if outlinePath == "layered_outline.json" {
		required = append(required, struct {
			kind string
			path string
		}{kind: "stable_flat_outline", path: "outline.json"})
	}
	dependencies := make([]domain.PlanningDependency, 0, len(required)+2)
	artifacts := make([]string, 0, len(required)+2)
	for _, item := range required {
		digest, err := pipelineRequiredFileSHA(outputDir, item.path)
		if err != nil {
			return nil, nil, fmt.Errorf("preplan 必需依赖 %s: %w", item.path, err)
		}
		dependencies = append(dependencies, domain.PlanningDependency{Kind: item.kind, ID: item.path, SHA256: digest})
		artifacts = append(artifacts, item.path)
	}
	for _, item := range []struct {
		kind string
		path string
	}{
		{kind: "premise", path: "premise.md"},
	} {
		digest, err := pipelineOptionalFileSHA(outputDir, item.path)
		if err != nil {
			return nil, nil, err
		}
		if digest == "" {
			continue
		}
		dependencies = append(dependencies, domain.PlanningDependency{Kind: item.kind, ID: item.path, SHA256: digest})
		artifacts = append(artifacts, item.path)
	}
	// The coarse skeleton does not consume arbitrary RAG chunks. Keep the index
	// in the audit list, but do not make its ever-growing file hash a hard
	// generation dependency. Formal plans bind selected retrieval receipts.
	if digest, err := pipelineOptionalFileSHA(outputDir, "meta/rag/index_state.json"); err != nil {
		return nil, nil, err
	} else if digest != "" {
		artifacts = append(artifacts, "meta/rag/index_state.json")
	}
	return dependencies, artifacts, nil
}

func pipelineCanonRoot(outputDir string, progress *domain.Progress) (string, error) {
	type canonArtifact struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	payload := struct {
		GenerationID string          `json:"generation_id,omitempty"`
		BaseChapter  int             `json:"base_chapter"`
		Artifacts    []canonArtifact `json:"artifacts"`
	}{
		BaseChapter:  progress.LatestCompleted(),
		GenerationID: strings.TrimSpace(progress.GenerationID),
	}
	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)
	for _, chapter := range completed {
		rel := fmt.Sprintf("chapters/%02d.md", chapter)
		digest, err := pipelineRequiredFileSHA(outputDir, rel)
		if err != nil {
			return "", fmt.Errorf("preplan 正史根读取第 %d 章: %w", chapter, err)
		}
		payload.Artifacts = append(payload.Artifacts, canonArtifact{Path: rel, SHA256: digest})
	}
	for _, rel := range []string{
		"meta/world_tick.json",
		"meta/resource_ledger.json",
		"meta/state_changes.json",
		"meta/cast_ledger.json",
		"meta/chapter_progress.json",
	} {
		digest, err := pipelineOptionalFileSHA(outputDir, rel)
		if err != nil {
			return "", err
		}
		if digest != "" {
			payload.Artifacts = append(payload.Artifacts, canonArtifact{Path: rel, SHA256: digest})
		}
	}
	return domain.DeterministicPlanningHash(payload)
}

func writePipelinePlanningJSON(path string, value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".planning-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return pipelineBytesSHA(raw), nil
}

func readPipelinePlanningJSON(path string, dst any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func pipelineRequiredFileSHA(outputDir, rel string) (string, error) {
	digest, err := pipelineOptionalFileSHA(outputDir, rel)
	if err != nil {
		return "", err
	}
	if digest == "" {
		return "", fmt.Errorf("缺少或为空：%s", rel)
	}
	return digest, nil
}

func pipelineOptionalFileSHA(outputDir, rel string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if len(raw) == 0 {
		return "", nil
	}
	return pipelineBytesSHA(raw), nil
}

func pipelineBytesSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func shortPipelineHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func pipelineExecutionOwner(stage string, chapter int) string {
	return fmt.Sprintf("pipeline-%s-ch%06d-pid%d-%d", stage, chapter, os.Getpid(), time.Now().UnixNano())
}
