package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	pipelineProjectAllSeedContract = "project-arc-v3; exactly one frozen outline arc; sequential world simulation then full POV plan; exact configured writer model; no prose; no live retrieval; chapter review remains per-body; sealed render payload"
	pipelineProjectAllLease        = 7 * 24 * time.Hour
	pipelineProjectAllAttemptPath  = "meta/planning/v2/project_all_attempt.json"
)

type pipelineProjectAllAttempt struct {
	Version        string `json:"version"`
	Nonce          string `json:"nonce"`
	RotatedAt      string `json:"rotated_at"`
	RestartPending bool   `json:"restart_pending,omitempty"`
}

type pipelineProjectAllIdentity struct {
	Generation             domain.PlanningGenerationV2
	Source                 domain.PlanningSourceSnapshotV2
	Registry               domain.ObligationRegistryV2
	Preplan                pipelinePreplanReceipt
	Arc                    pipelineArcScope
	FoundationSnapshotRoot string
	RAGSnapshotRoot        string
}

// pipelineProjectAll keeps its historical stage name for CLI/state migration,
// but its transaction boundary is one arc. It plans every chapter of the
// current arc in a shadow workspace and publishes only projected,
// non-canonical bundles. It never writes a chapter body and never crosses into
// the next arc before every chapter in this one has been rendered and accepted.
func pipelineProjectAll(opts cliOptions, flags pipelineFlags) (returnErr error) {
	_, releaseControl, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return fmt.Errorf("project-all requires published outline-all: %w", err)
	}
	defer releasePublishedOutlineAllStage(releaseControl, "project-all", &returnErr)
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := pipelineRequirePrewritingReady(cfg.OutputDir); err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if err := requirePipelineProjectAllRAGSnapshot(st); err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("project-all 读取 progress: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("project-all 缺少 meta/progress.json")
	}
	if len(progress.PendingRewrites) > 0 {
		return fmt.Errorf("project-all 必须从稳定正史起点推演；请先清空 pending_rewrites=%v", progress.PendingRewrites)
	}
	if err := requireNoPendingSealedSteer(st, "project-all"); err != nil {
		return err
	}
	identity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	if err != nil {
		return err
	}
	first := identity.Generation.FirstProjectedChapter
	last := identity.Generation.LastProjectedChapter
	if flags.Start > 0 && flags.Start != first {
		return fmt.Errorf("project-all 必须从正史下一章 %d 连续推演，不能用 --from=%d 跳过前驱", first, flags.Start)
	}
	if flags.End > 0 && flags.End != last {
		return fmt.Errorf(
			"project-all 当前只推演 V%dA%d，--to 必须省略或等于本弧末章 %d",
			identity.Arc.Volume,
			identity.Arc.Arc,
			last,
		)
	}

	owner := pipelineExecutionOwner("project-all", first)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: first,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineProjectAllLease),
	}); err != nil {
		return fmt.Errorf("project-all 获取全书推演执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("project-all 释放全书推演执行锁: %w", err)
		}
	}()

	// Recompute under the live execution lock. A project-all generation may
	// never start from a canon/dependency snapshot observed before the lock.
	if flags.Restart {
		if err := rotatePipelineProjectAllAttempt(cfg.OutputDir); err != nil {
			return fmt.Errorf("project-all 创建全新规划 attempt: %w", err)
		}
	}
	progress, err = st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("project-all 锁内重读 progress: %w", err)
	}
	if len(progress.PendingRewrites) > 0 {
		return fmt.Errorf("project-all 锁内发现 pending_rewrites=%v；拒绝从不稳定正史推演", progress.PendingRewrites)
	}
	if err := requireNoPendingSealedSteer(st, "project-all"); err != nil {
		return err
	}
	if err := activatePipelineSealedTwoPassMode(opts); err != nil {
		return err
	}
	lockedIdentity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	if err != nil {
		return err
	}
	if !flags.Restart &&
		lockedIdentity.Generation.GenerationID != identity.Generation.GenerationID {
		return fmt.Errorf("project-all 获取执行锁期间规划输入发生漂移；请重跑本阶段")
	}
	identity = lockedIdentity

	projected := st.ProjectedV2()
	restartPending, err := pipelineProjectAllRestartPending(cfg.OutputDir)
	if err != nil {
		return err
	}
	if flags.Restart || restartPending {
		if err := abandonPipelineActivePromotionForRestart(
			projected,
			identity.Generation.GenerationID,
		); err != nil {
			return err
		}
		if err := projected.ResetProjectionCursorForRestart(
			identity.Generation.GenerationID,
		); err != nil {
			return fmt.Errorf("project-all reset predecessor projection cursor: %w", err)
		}
		if err := completePipelineProjectAllRestart(cfg.OutputDir); err != nil {
			return fmt.Errorf("project-all finalize restart intent: %w", err)
		}
	}
	if sealed, err := projected.LoadSealedGeneration(identity.Generation.GenerationID); err != nil {
		return err
	} else if sealed != nil {
		if err := validatePipelineProjectAllGenerationIdentity(*sealed, identity.Generation); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[pipeline:project-all] generation %s 已封版，跳过重复推演\n", sealed.GenerationID)
		return nil
	}
	building, err := projected.LoadBuildingGeneration(identity.Generation.GenerationID)
	if err != nil {
		return err
	}
	if building == nil {
		if err := projected.CreateBuildingGeneration(identity.Generation, identity.Source, identity.Registry); err != nil {
			return fmt.Errorf("project-all 创建 generation: %w", err)
		}
		building = &identity.Generation
	} else if err := validatePipelineProjectAllGenerationIdentity(*building, identity.Generation); err != nil {
		return err
	}
	// Projection cursor is a derived pointer for the currently building arc,
	// not a lifetime-wide book cursor. After the previous arc is fully
	// realized, the successor generation must start at its own first chapter.
	if err := projected.ResetProjectionCursorForRestart(identity.Generation.GenerationID); err != nil {
		return fmt.Errorf("project-all 切换当前弧 projection cursor: %w", err)
	}
	if _, _, err := projected.RecoverBuildingProjection(identity.Generation.GenerationID); err != nil {
		return fmt.Errorf("project-all 恢复 generation/registry/bundle/cursor: %w", err)
	}

	workspace, err := preparePipelineProjectAllWorkspace(
		cfg.OutputDir,
		identity.Generation.GenerationID,
		identity.Generation.BaseCanonChapter,
		flags.Restart,
	)
	if err != nil {
		return fmt.Errorf("project-all 准备隔离工作区: %w", err)
	}
	workspaceManifest, err := loadPipelineProjectAllWorkspaceManifest(workspace)
	if err != nil {
		return fmt.Errorf("project-all 读取隔离工作区来源快照: %w", err)
	}
	if workspaceManifest.FoundationSnapshotRoot != identity.FoundationSnapshotRoot ||
		workspaceManifest.RAGSnapshotRoot != identity.RAGSnapshotRoot {
		return fmt.Errorf("project-all 隔离工作区实际复制的 foundation/RAG 快照与 generation identity 不一致；拒绝从混合输入推演")
	}
	shadow := store.NewStore(workspace)
	if err := requirePipelineProjectAllRAGSnapshot(shadow); err != nil {
		return err
	}
	if err := reconcilePipelineProjectAllWorkspace(shadow, projected, identity.Generation); err != nil {
		return err
	}

	for chapter := first; chapter <= last; chapter++ {
		bundles, err := projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
		if err != nil {
			return err
		}
		if len(bundles) > 0 && bundles[len(bundles)-1].Chapter >= chapter {
			continue
		}
		currentGeneration, err := projected.LoadBuildingGeneration(identity.Generation.GenerationID)
		if err != nil || currentGeneration == nil {
			return fmt.Errorf("project-all 第 %d 章 generation 不可读: %w", chapter, err)
		}
		registry, err := projected.LoadObligationRegistry(identity.Generation.GenerationID)
		if err != nil || registry == nil {
			return fmt.Errorf("project-all 第 %d 章 obligation registry 不可读: %w", chapter, err)
		}
		planningContext, err := domain.DeriveProjectedPlanningContextV2(
			*currentGeneration,
			bundles,
			*registry,
			chapter,
		)
		if err != nil {
			return fmt.Errorf("project-all 第 %d 章构造 authoritative projected context: %w", chapter, err)
		}
		if err := savePipelineProjectAllPlanningContext(shadow, planningContext); err != nil {
			return fmt.Errorf("project-all 第 %d 章发布 authoritative projected context: %w", chapter, err)
		}
		outline, err := applyPipelineProjectAllObligationsToOutline(shadow, *registry, chapter)
		if err != nil || outline == nil {
			return fmt.Errorf("project-all 第 %d 章缺少物化后的稳定章位/跨章义务: %w", chapter, err)
		}
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:project-all] V%dA%d 第 %d/%d 章：世界推演 → POV 正式计划 → 冻结渲染包\n",
			identity.Arc.Volume,
			identity.Arc.Arc,
			chapter,
			last,
		)
		artifacts, err := agents.RunProjectedChapterPlanning(
			context.Background(),
			cfg,
			promptBundle,
			workspace,
			chapter,
			planningContext.ContextDigest,
			agents.ProjectedArcBoundary{
				Volume:          identity.Arc.Volume,
				Arc:             identity.Arc.Arc,
				Title:           identity.Arc.Title,
				Goal:            identity.Arc.Goal,
				FirstChapter:    identity.Arc.FirstChapter,
				LastChapter:     identity.Arc.LastChapter,
				BookLastChapter: identity.Arc.BookLastChapter,
			},
		)
		if err != nil {
			return fmt.Errorf("project-all 第 %d 章失败: %w", chapter, err)
		}
		currentGeneration, err = projected.LoadBuildingGeneration(identity.Generation.GenerationID)
		if err != nil || currentGeneration == nil {
			return fmt.Errorf("project-all 第 %d 章 generation 不可读: %w", chapter, err)
		}
		registry, err = projected.LoadObligationRegistry(identity.Generation.GenerationID)
		if err != nil || registry == nil {
			return fmt.Errorf("project-all 第 %d 章 obligation registry 不可读: %w", chapter, err)
		}
		bundles, err = projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
		if err != nil {
			return err
		}
		previousDigest, preStateRoot, err := pipelineProjectAllTail(*currentGeneration, bundles)
		if err != nil {
			return err
		}
		nextBundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
			*currentGeneration,
			*outline,
			previousDigest,
			preStateRoot,
			artifacts,
			*registry,
		)
		if err != nil {
			return fmt.Errorf("project-all 第 %d 章封装失败: %w", chapter, err)
		}
		projectionCursor, err := projected.LoadProjectionCursor()
		if err != nil || projectionCursor == nil {
			return fmt.Errorf("project-all 第 %d 章 projection cursor 不可读: %w", chapter, err)
		}
		if _, err := projected.ProjectChapterAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			currentGeneration.ObligationRegistryRoot,
			*projectionCursor,
			nextBundle,
			nextRegistry,
		); err != nil {
			return fmt.Errorf("project-all 第 %d 章 registry/bundle/cursor 原子推进: %w", chapter, err)
		}
		if err := advancePipelineProjectAllWorkspace(
			shadow,
			identity.Generation.GenerationID,
			chapter,
			artifacts.WorldSimulation,
			artifacts.Plan,
			nextBundle.ProjectedDelta,
		); err != nil {
			return fmt.Errorf("project-all 第 %d 章推进影子前态: %w", chapter, err)
		}
	}
	if err := validatePipelineProjectAllComplete(projected, identity.Generation); err != nil {
		return err
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:project-all] V%dA%d（第 %d-%d 章）已完成整弧正式推演；尚未写正文，下一阶段只封存本弧\n",
		identity.Arc.Volume,
		identity.Arc.Arc,
		identity.Arc.FirstChapter,
		identity.Arc.LastChapter,
	)
	return nil
}

func requirePipelineProjectAllRAGSnapshot(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("project-all 要求隔离 RAG store")
	}
	ragState, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		return fmt.Errorf("project-all 读取 RAG index_state: %w", err)
	}
	if ragState == nil || len(ragState.Chunks) == 0 {
		return fmt.Errorf("project-all 要求 meta/rag/index_state.json 存在且 chunks>0；请保留/恢复现有 RAG 快照，或在首次运行前 build-rag")
	}
	return nil
}

func buildPipelineProjectAllIdentity(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	st *store.Store,
	progress *domain.Progress,
) (pipelineProjectAllIdentity, error) {
	var identity pipelineProjectAllIdentity
	if st == nil || progress == nil {
		return identity, fmt.Errorf("project-all identity requires store and progress")
	}
	if err := readPipelinePlanningJSON(
		filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath),
		&identity.Preplan,
	); err != nil {
		return identity, fmt.Errorf("project-all 必须先有完整 preplan 回执: %w", err)
	}
	if err := validatePipelinePreplanFresh(st, identity.Preplan); err != nil {
		return identity, err
	}
	if identity.Preplan.RebaseRequiredBeforeFuture {
		return identity, fmt.Errorf("project-all 当前 preplan 已要求正史 rebase；请先重跑 preplan")
	}
	baseChapter := progress.LatestCompleted()
	if identity.Preplan.BaseCanonChapter != baseChapter {
		return identity, fmt.Errorf(
			"project-all preplan base=%d 与当前正史 base=%d 不一致",
			identity.Preplan.BaseCanonChapter,
			baseChapter,
		)
	}
	first := baseChapter + 1
	arcScope, err := requirePipelineArcStart(st, baseChapter)
	if err != nil {
		return identity, fmt.Errorf("project-all 当前弧边界: %w", err)
	}
	last := arcScope.LastChapter
	bookLast := identity.Preplan.TotalChapters
	if bookLast < first || arcScope.BookLastChapter != bookLast {
		return identity, fmt.Errorf(
			"project-all 冻结章纲总章数漂移：preplan=%d layered=%d",
			bookLast,
			arcScope.BookLastChapter,
		)
	}
	if len(identity.Preplan.StagedChapters) != bookLast-first+1 {
		return identity, fmt.Errorf(
			"project-all preplan 全书剩余 staged chain 不完整：got=%d want=%d",
			len(identity.Preplan.StagedChapters),
			bookLast-first+1,
		)
	}
	for i, chapter := range identity.Preplan.StagedChapters {
		if chapter != first+i {
			return identity, fmt.Errorf("project-all preplan staged chain 在第 %d 项不连续", i)
		}
	}
	baseCanonRoot, err := pipelineProjectAllLiveCanonRoot(cfg.OutputDir, progress)
	if err != nil {
		return identity, err
	}
	baseStateRoot := pipelineProjectAllDigest(struct {
		Version       string `json:"version"`
		BaseCanonRoot string `json:"base_canon_root"`
		BaseChapter   int    `json:"base_chapter"`
	}{"project-all-structured-state.v2", baseCanonRoot, baseChapter})
	stableOutlineRoot, err := pipelineProjectAllStableOutlineRoot(cfg.OutputDir, identity.Preplan)
	if err != nil {
		return identity, err
	}
	foundationSnapshotRoot, err := pipelineProjectAllFoundationSnapshotRoot(cfg.OutputDir)
	if err != nil {
		return identity, err
	}
	ragSnapshotRoot, err := pipelineProjectAllRAGSnapshotRoot(cfg.OutputDir)
	if err != nil {
		return identity, err
	}
	dependencyRoot, err := pipelineProjectAllDependencyRootWithSourceRoots(
		cfg,
		promptBundle,
		identity.Preplan,
		foundationSnapshotRoot,
		ragSnapshotRoot,
	)
	if err != nil {
		return identity, err
	}
	seedRoot, err := domain.ComputePlanningSeedContractRootV2(pipelineProjectAllSeedContract)
	if err != nil {
		return identity, err
	}
	attemptNonce, err := loadPipelineProjectAllAttemptNonce(cfg.OutputDir)
	if err != nil {
		return identity, err
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		stableOutlineRoot,
		dependencyRoot,
		seedRoot,
		attemptNonce,
	)
	if err != nil {
		return identity, err
	}
	projected := st.ProjectedV2()
	parent := ""
	active, loadErr := projected.LoadActiveGeneration()
	if loadErr != nil {
		return identity, fmt.Errorf("project-all 读取上一弧 active generation: %w", loadErr)
	}
	predecessorGenerationID := ""
	if active != nil {
		if active.GenerationID == "" {
			return identity, fmt.Errorf("project-all active generation id 为空")
		}
		if active.GenerationID == generationID {
			// Idempotent replay after this arc was sealed but before its first
			// render: reconstruct the same parent and predecessor state.
			parent = active.PreviousGenerationID
			predecessorGenerationID = active.PreviousGenerationID
		} else {
			parent = active.GenerationID
			predecessorGenerationID = active.GenerationID
		}
	}
	if baseChapter > 0 {
		if predecessorGenerationID == "" {
			return identity, fmt.Errorf("project-all 第 %d 章弧边界缺少上一弧 generation", baseChapter)
		}
		previous, previousErr := projected.LoadSealedGeneration(predecessorGenerationID)
		if previousErr != nil || previous == nil {
			return identity, fmt.Errorf("project-all 读取上一弧 sealed generation: %w", previousErr)
		}
		completion, completionErr := requirePipelineArcCompletion(st, previous)
		if completionErr != nil {
			return identity, fmt.Errorf("project-all 上一弧完成证明无效: %w", completionErr)
		}
		cursor, cursorErr := projected.LoadRealizationCursor()
		if cursorErr != nil || cursor == nil {
			return identity, fmt.Errorf("project-all 读取上一弧 realization cursor: %w", cursorErr)
		}
		if cursor.ActiveGenerationID != active.GenerationID ||
			previous.LastProjectedChapter != baseChapter ||
			cursor.LastAcceptedChapter != baseChapter ||
			strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
			return identity, fmt.Errorf("project-all 上一弧未在第 %d 章形成完整 actual outcome 边界", baseChapter)
		}
		outcome, outcomeErr := projected.LoadActualOutcomeReceipt(
			previous.GenerationID,
			baseChapter,
			cursor.LastOutcomeReceiptDigest,
		)
		if outcomeErr != nil || outcome == nil {
			return identity, fmt.Errorf("project-all 读取上一弧末章 actual outcome: %w", outcomeErr)
		}
		previousBundles, bundlesErr := projected.LoadProjectedChapterBundles(previous.GenerationID)
		if bundlesErr != nil || len(previousBundles) == 0 {
			return identity, fmt.Errorf("project-all 读取上一弧 bundle chain: %w", bundlesErr)
		}
		lastBundle := previousBundles[len(previousBundles)-1]
		if lastBundle.Chapter != baseChapter ||
			outcome.ActualPostStateRoot != lastBundle.ProjectedPostStateRoot ||
			completion.FinalActualPostStateRoot != outcome.ActualPostStateRoot ||
			completion.FinalOutcomeReceiptDigest != outcome.ReceiptDigest {
			return identity, fmt.Errorf("project-all 上一弧 actual post-state 与末章 sealed projected state 不一致")
		}
		baseStateRoot = outcome.ActualPostStateRoot
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	scopeID := domain.DeriveArcCycleID(
		arcScope.Volume,
		arcScope.Arc,
		arcScope.FirstChapter,
		arcScope.LastChapter,
	)
	registry := domain.ObligationRegistryV2{
		Version:            domain.ObligationRegistryV2Version,
		GenerationID:       generationID,
		ProjectionScope:    domain.PlanningProjectionScopeArcV2,
		ScopeID:            scopeID,
		BookHorizonChapter: bookLast,
		FirstChapter:       first,
		LastChapter:        last,
		Obligations:        []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		return identity, err
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		ParentGenerationID:     parent,
		ProjectionScope:        domain.PlanningProjectionScopeArcV2,
		ScopeID:                scopeID,
		BookHorizonChapter:     bookLast,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       baseChapter,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      stableOutlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		AttemptID:              attemptNonce,
		FirstProjectedChapter:  first,
		LastProjectedChapter:   last,
		ExpectedChapterCount:   last - first + 1,
		ProjectedChapterCount:  0,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              now,
	}
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		return identity, err
	}
	if parent != "" {
		generation, registry, err = projected.PrepareCarriedArcGeneration(parent, generation)
		if err != nil {
			return identity, fmt.Errorf("project-all 继承上一弧未决义务: %w", err)
		}
	}
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generationID,
		BaseCanonChapter:       baseChapter,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      stableOutlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FoundationSnapshotRoot: foundationSnapshotRoot,
		RAGSnapshotRoot:        ragSnapshotRoot,
		CapturedAt:             now,
	}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		return identity, err
	}
	if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
		return identity, err
	}
	if err := domain.ValidatePlanningSourceSnapshotV2(source); err != nil {
		return identity, err
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return identity, err
	}
	identity.Generation = generation
	identity.Source = source
	identity.Registry = registry
	identity.Arc = arcScope
	identity.FoundationSnapshotRoot = foundationSnapshotRoot
	identity.RAGSnapshotRoot = ragSnapshotRoot
	return identity, nil
}

func abandonPipelineActivePromotionForRestart(
	projected *store.ProjectedStoreV2,
	successorGenerationID string,
) error {
	if projected == nil || strings.TrimSpace(successorGenerationID) == "" {
		return nil
	}
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return err
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return err
	}
	if cursor.ActivePromotedChapter == 0 ||
		active.GenerationID == successorGenerationID {
		return nil
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return fmt.Errorf("project-all restart load active sealed generation: %w", err)
	}
	records, err := projected.LoadSuffixInvalidationReceipts(active.GenerationID)
	if err != nil {
		return err
	}
	previous, err := pipelineSuffixInvalidationTail(records)
	if err != nil {
		return err
	}
	invalidation := domain.SuffixInvalidationReceiptV2{
		Version:                 domain.SuffixInvalidationV2Version,
		GenerationID:            active.GenerationID,
		FromChapter:             cursor.ActivePromotedChapter,
		ThroughChapter:          generation.LastProjectedChapter,
		CauseReceiptDigest:      cursor.ActivePromotionReceiptDigest,
		Reason:                  "explicit --restart abandons an unaccepted sealed render and reprojects the suffix",
		ReplacementGenerationID: successorGenerationID,
		InvalidatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		PreviousReceiptDigest:   previous,
	}
	invalidation.ReceiptDigest, err =
		domain.ComputeSuffixInvalidationReceiptV2Digest(invalidation)
	if err != nil {
		return err
	}
	if _, err := projected.SaveSuffixInvalidationReceipt(invalidation); err != nil {
		return fmt.Errorf("project-all restart persist suffix invalidation: %w", err)
	}
	if _, err := projected.AbandonInvalidatedPromotion(*cursor, invalidation); err != nil {
		return fmt.Errorf("project-all restart abandon active promotion: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:project-all] 已失效旧 generation %s 的第 %d-%d 章并切换 successor %s\n",
		active.GenerationID,
		invalidation.FromChapter,
		invalidation.ThroughChapter,
		successorGenerationID,
	)
	return nil
}

func pipelineSuffixInvalidationTail(
	records []domain.SuffixInvalidationReceiptV2,
) (string, error) {
	if len(records) == 0 {
		return "", nil
	}
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
			return "", fmt.Errorf("suffix invalidation history has multiple tails")
		}
		tail = digest
	}
	if tail == "" {
		return "", fmt.Errorf("suffix invalidation history contains a cycle")
	}
	return tail, nil
}

func loadPipelineProjectAllAttemptNonce(outputDir string) (string, error) {
	attempt, err := loadPipelineProjectAllAttempt(outputDir)
	if err != nil || attempt == nil {
		return "", err
	}
	return strings.TrimSpace(attempt.Nonce), nil
}

func loadPipelineProjectAllAttempt(
	outputDir string,
) (*pipelineProjectAllAttempt, error) {
	var attempt pipelineProjectAllAttempt
	err := readPipelinePlanningJSON(
		filepath.Join(outputDir, filepath.FromSlash(pipelineProjectAllAttemptPath)),
		&attempt,
	)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project-all attempt: %w", err)
	}
	if attempt.Version != "project-all-attempt.v2" ||
		strings.TrimSpace(attempt.Nonce) == "" {
		return nil, fmt.Errorf("invalid project-all attempt receipt")
	}
	return &attempt, nil
}

func pipelineProjectAllRestartPending(outputDir string) (bool, error) {
	attempt, err := loadPipelineProjectAllAttempt(outputDir)
	if err != nil || attempt == nil {
		return false, err
	}
	return attempt.RestartPending, nil
}

func completePipelineProjectAllRestart(outputDir string) error {
	attempt, err := loadPipelineProjectAllAttempt(outputDir)
	if err != nil || attempt == nil || !attempt.RestartPending {
		return err
	}
	attempt.RestartPending = false
	_, err = writePipelinePlanningJSON(
		filepath.Join(outputDir, filepath.FromSlash(pipelineProjectAllAttemptPath)),
		*attempt,
	)
	return err
}

func rotatePipelineProjectAllAttempt(outputDir string) error {
	now := time.Now().UTC()
	nonce := pipelineProjectAllDigest(struct {
		Version string `json:"version"`
		Time    string `json:"time"`
		PID     int    `json:"pid"`
	}{
		Version: "project-all-attempt-nonce.v2",
		Time:    now.Format(time.RFC3339Nano),
		PID:     os.Getpid(),
	})
	_, err := writePipelinePlanningJSON(
		filepath.Join(outputDir, filepath.FromSlash(pipelineProjectAllAttemptPath)),
		pipelineProjectAllAttempt{
			Version:        "project-all-attempt.v2",
			Nonce:          nonce,
			RotatedAt:      now.Format(time.RFC3339Nano),
			RestartPending: true,
		},
	)
	return err
}

func pipelineProjectAllLiveCanonRoot(
	outputDir string,
	progress *domain.Progress,
) (string, error) {
	rawCanonRoot, err := pipelineCanonRoot(outputDir, progress)
	if err != nil {
		return "", err
	}
	return pipelineProjectAllCanonRootFromSnapshot(rawCanonRoot), nil
}

func pipelineProjectAllCanonRootFromSnapshot(rawCanonRoot string) string {
	return pipelineProjectAllDigest(struct {
		Version string `json:"version"`
		Root    string `json:"root"`
	}{"project-all-canon-root.v2", rawCanonRoot})
}

func pipelineProjectAllStableOutlineRoot(outputDir string, receipt pipelinePreplanReceipt) (string, error) {
	artifacts := make(map[string]string)
	for _, rel := range []string{"layered_outline.json", "outline.json"} {
		digest, err := pipelineRequiredFileSHA(outputDir, rel)
		if err != nil {
			return "", err
		}
		artifacts[rel] = digest
	}
	manifests, err := store.NewStore(outputDir).Planning.LoadStagedChapterPlanManifests()
	if err != nil {
		return "", err
	}
	manifestDigests := make([]string, 0, len(manifests))
	for i := range manifests {
		if _, err := loadAndVerifyPipelineProjectedPayload(outputDir, &manifests[i]); err != nil {
			return "", err
		}
		manifestDigests = append(manifestDigests, manifests[i].PlanSHA256)
	}
	sort.Strings(manifestDigests)
	return pipelineProjectAllDigest(struct {
		Version         string            `json:"version"`
		Artifacts       map[string]string `json:"artifacts"`
		ManifestDigests []string          `json:"manifest_digests"`
		BaseChapter     int               `json:"base_chapter"`
		TotalChapters   int               `json:"total_chapters"`
	}{
		Version:         "project-all-stable-outline-root.v2",
		Artifacts:       artifacts,
		ManifestDigests: manifestDigests,
		BaseChapter:     receipt.BaseCanonChapter,
		TotalChapters:   receipt.TotalChapters,
	}), nil
}

func pipelineProjectAllDependencyRoot(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	receipt pipelinePreplanReceipt,
) (string, error) {
	foundationSnapshotRoot, err := pipelineProjectAllFoundationSnapshotRoot(cfg.OutputDir)
	if err != nil {
		return "", err
	}
	ragSnapshotRoot, err := pipelineProjectAllRAGSnapshotRoot(cfg.OutputDir)
	if err != nil {
		return "", err
	}
	return pipelineProjectAllDependencyRootWithSourceRoots(
		cfg,
		promptBundle,
		receipt,
		foundationSnapshotRoot,
		ragSnapshotRoot,
	)
}

func pipelineProjectAllDependencyRootWithSourceRoots(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	receipt pipelinePreplanReceipt,
	foundationSnapshotRoot string,
	ragSnapshotRoot string,
) (string, error) {
	if strings.TrimSpace(foundationSnapshotRoot) == "" ||
		strings.TrimSpace(ragSnapshotRoot) == "" {
		return "", fmt.Errorf("project-all dependency root requires captured foundation and RAG roots")
	}
	// receipt.DependencyRoot already captures the exact preplan artifacts, and
	// FoundationSnapshotRoot/RAGSnapshotRoot capture every additional file the
	// isolated full-book planner can consume. Do not rehash SourceArtifacts
	// here: after sealing, sequential render legitimately advances live ledgers,
	// dossiers and RAG while the remaining projected bundles stay immutable.
	// Before project-all and seal, validatePipelinePreplanFresh plus freshly
	// computed source roots still fail closed on any input drift.
	return pipelineProjectAllDigest(struct {
		Version                string            `json:"version"`
		PreplanDependency      string            `json:"preplan_dependency"`
		PipelineRunInput       string            `json:"pipeline_run_input"`
		SourceArtifacts        map[string]string `json:"source_artifacts"`
		FoundationSnapshotRoot string            `json:"foundation_snapshot_root"`
		RAGSnapshotRoot        string            `json:"rag_snapshot_root"`
		LiveRAGDisabled        bool              `json:"live_rag_disabled"`
		SeedContract           string            `json:"seed_contract"`
	}{
		Version:                "project-all-dependency-root.v3",
		PreplanDependency:      receipt.DependencyRoot,
		PipelineRunInput:       pipelineProjectAllInputDigest(cfg, promptBundle),
		SourceArtifacts:        map[string]string{},
		FoundationSnapshotRoot: foundationSnapshotRoot,
		RAGSnapshotRoot:        ragSnapshotRoot,
		LiveRAGDisabled:        true,
		SeedContract:           pipelineProjectAllSeedContract,
	}), nil
}

func validatePipelineProjectAllGenerationIdentity(
	got domain.PlanningGenerationV2,
	want domain.PlanningGenerationV2,
) error {
	if got.GenerationID != want.GenerationID ||
		got.ParentGenerationID != want.ParentGenerationID ||
		got.ProjectionScope != want.ProjectionScope ||
		got.ScopeID != want.ScopeID ||
		got.BookHorizonChapter != want.BookHorizonChapter ||
		got.BaseCanonChapter != want.BaseCanonChapter ||
		got.BaseCanonRoot != want.BaseCanonRoot ||
		got.BaseStateRoot != want.BaseStateRoot ||
		got.StableOutlineRoot != want.StableOutlineRoot ||
		got.PlanningDependencyRoot != want.PlanningDependencyRoot ||
		got.RandomSeedContractRoot != want.RandomSeedContractRoot ||
		got.FirstProjectedChapter != want.FirstProjectedChapter ||
		got.LastProjectedChapter != want.LastProjectedChapter {
		return fmt.Errorf("project-all generation %s 与当前正史/大纲/模型输入不一致", got.GenerationID)
	}
	return nil
}

func pipelineProjectAllTail(
	generation domain.PlanningGenerationV2,
	bundles []domain.ProjectedChapterBundle,
) (previousDigest string, preStateRoot string, err error) {
	if len(bundles) == 0 {
		genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
		if err != nil {
			return "", "", err
		}
		return genesis, generation.BaseStateRoot, nil
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Chapter < bundles[j].Chapter })
	last := bundles[len(bundles)-1]
	return last.BundleDigest, last.ProjectedPostStateRoot, nil
}

func reconcilePipelineProjectAllWorkspace(
	shadow *store.Store,
	projected *store.ProjectedStoreV2,
	generation domain.PlanningGenerationV2,
) error {
	if shadow == nil || projected == nil {
		return fmt.Errorf("project-all workspace reconciliation requires stores")
	}
	progress, err := shadow.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("project-all 影子 progress 不可读: %w", err)
	}
	if progress.LatestCompleted() < generation.BaseCanonChapter {
		return fmt.Errorf("project-all 影子正史基线落后于 generation base")
	}
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return err
	}
	for i := range bundles {
		chapter := bundles[i].Chapter
		if shadow.Progress.IsChapterCompleted(chapter) {
			continue
		}
		if err := advancePipelineProjectAllWorkspace(
			shadow,
			generation.GenerationID,
			chapter,
			&bundles[i].ChapterWorldSimulation,
			&bundles[i].ChapterPlan,
			bundles[i].ProjectedDelta,
		); err != nil {
			return fmt.Errorf("project-all 恢复第 %d 章影子前态: %w", chapter, err)
		}
	}
	progress, err = shadow.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("project-all 重读影子 progress: %w", err)
	}
	wantLatest := generation.BaseCanonChapter + len(bundles)
	if progress.LatestCompleted() != wantLatest {
		return fmt.Errorf(
			"project-all 影子进度与 durable bundle chain 不一致：latest=%d want=%d",
			progress.LatestCompleted(),
			wantLatest,
		)
	}
	return nil
}

func reconcilePipelineProjectionCursor(
	projected *store.ProjectedStoreV2,
	generation domain.PlanningGenerationV2,
) error {
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return err
	}
	lastChapter := generation.BaseCanonChapter
	lastDigest := ""
	if len(bundles) > 0 {
		sort.Slice(bundles, func(i, j int) bool { return bundles[i].Chapter < bundles[j].Chapter })
		lastChapter = bundles[len(bundles)-1].Chapter
		lastDigest = bundles[len(bundles)-1].BundleDigest
	}
	next := domain.ProjectionCursorV2{
		GenerationID:         generation.GenerationID,
		NextProjectChapter:   lastChapter + 1,
		LastProjectedChapter: lastChapter,
		LastBundleDigest:     lastDigest,
		UpdatedAt:            time.Now().UTC().Format(time.RFC3339Nano),
	}
	next.CursorDigest, err = domain.ComputeProjectionCursorV2Digest(next)
	if err != nil {
		return err
	}
	current, err := projected.LoadProjectionCursor()
	if err != nil {
		return err
	}
	if current != nil &&
		current.GenerationID == next.GenerationID &&
		current.NextProjectChapter == next.NextProjectChapter &&
		current.LastProjectedChapter == next.LastProjectedChapter &&
		current.LastBundleDigest == next.LastBundleDigest {
		return nil
	}
	return projected.CompareAndSwapProjectionCursor(current, next)
}

func validatePipelineProjectAllComplete(
	projected *store.ProjectedStoreV2,
	generation domain.PlanningGenerationV2,
) error {
	building, err := projected.LoadBuildingGeneration(generation.GenerationID)
	if err != nil {
		return err
	}
	if building == nil {
		if sealed, err := projected.LoadSealedGeneration(generation.GenerationID); err != nil {
			return err
		} else if sealed != nil {
			return nil
		}
		return fmt.Errorf("project-all generation %s 不存在", generation.GenerationID)
	}
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return err
	}
	registry, err := projected.LoadObligationRegistry(generation.GenerationID)
	if err != nil || registry == nil {
		return fmt.Errorf("project-all obligation registry 不可读: %w", err)
	}
	if len(bundles) != generation.ExpectedChapterCount {
		return fmt.Errorf("project-all 只完成 %d/%d 章", len(bundles), generation.ExpectedChapterCount)
	}
	if err := domain.ValidateProjectedChapterBundleChain(*building, bundles, *registry); err != nil {
		return err
	}
	return nil
}

func pipelineProjectAllGenerationForCurrentInputs(
	opts cliOptions,
) (bootstrap.Config, assets.Bundle, *store.Store, pipelineProjectAllIdentity, error) {
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		return cfg, promptBundle, nil, pipelineProjectAllIdentity{}, err
	}
	st := store.NewStore(cfg.OutputDir)
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return cfg, promptBundle, st, pipelineProjectAllIdentity{}, fmt.Errorf("读取 progress: %w", err)
	}
	identity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, progress)
	return cfg, promptBundle, st, identity, err
}

func pipelineProjectAllBundleForChapter(
	projected *store.ProjectedStoreV2,
	generationID string,
	chapter int,
) (*domain.ProjectedChapterBundle, error) {
	bundles, err := projected.LoadProjectedChapterBundles(generationID)
	if err != nil {
		return nil, err
	}
	for i := range bundles {
		if bundles[i].Chapter == chapter {
			return &bundles[i], nil
		}
	}
	return nil, fmt.Errorf("sealed generation %s 缺少第 %d 章 bundle", generationID, chapter)
}

func pipelineProjectAllCurrentActualPreState(
	projected *store.ProjectedStoreV2,
	cursor *domain.RealizationCursorV2,
	generation *domain.PlanningGenerationV2,
) (string, error) {
	if projected == nil || cursor == nil || generation == nil {
		return "", fmt.Errorf("actual pre-state requires projected store, cursor and generation")
	}
	if cursor.LastAcceptedChapter == generation.BaseCanonChapter {
		return generation.BaseStateRoot, nil
	}
	if strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return "", fmt.Errorf("realization cursor 缺少上一章 outcome receipt digest")
	}
	outcome, err := projected.LoadActualOutcomeReceipt(
		generation.GenerationID,
		cursor.LastAcceptedChapter,
		cursor.LastOutcomeReceiptDigest,
	)
	if err != nil {
		return "", err
	}
	if outcome == nil {
		return "", fmt.Errorf("realization cursor 指向的上一章 outcome receipt 不存在")
	}
	return outcome.ActualPostStateRoot, nil
}
