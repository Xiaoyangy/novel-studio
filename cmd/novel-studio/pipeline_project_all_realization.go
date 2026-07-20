package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

// pipelineSeal validates every formal projected bundle in the current arc,
// atomically publishes that immutable arc generation, and jointly activates
// its per-chapter realization cursor.
func pipelineSeal(opts cliOptions, flags pipelineFlags) (returnErr error) {
	_, releaseControl, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return fmt.Errorf("seal requires published outline-all: %w", err)
	}
	defer releasePublishedOutlineAllStage(releaseControl, "seal", &returnErr)
	cfg, promptBundle, st, identity, err := pipelineProjectAllGenerationForCurrentInputs(opts)
	if err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("seal 读取 progress: %w", err)
	}
	if len(progress.PendingRewrites) > 0 {
		return fmt.Errorf("seal 禁止从 pending_rewrites=%v 的正史封版", progress.PendingRewrites)
	}
	if flags.Start > 0 && flags.Start != identity.Generation.FirstProjectedChapter {
		return fmt.Errorf("seal 不接受局部 --from；当前 V%dA%d 从第 %d 章开始", identity.Arc.Volume, identity.Arc.Arc, identity.Generation.FirstProjectedChapter)
	}
	if flags.End > 0 && flags.End != identity.Generation.LastProjectedChapter {
		return fmt.Errorf("seal 不接受局部 --to；当前 V%dA%d 到第 %d 章结束", identity.Arc.Volume, identity.Arc.Arc, identity.Generation.LastProjectedChapter)
	}

	owner := pipelineExecutionOwner("seal", identity.Generation.FirstProjectedChapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: identity.Generation.FirstProjectedChapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("seal 获取执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("seal 释放执行锁: %w", err)
		}
	}()
	lockedProgress, err := st.Progress.Load()
	if err != nil || lockedProgress == nil {
		return fmt.Errorf("seal 锁内读取 progress: %w", err)
	}
	if len(lockedProgress.PendingRewrites) > 0 {
		return fmt.Errorf("seal 锁内发现 pending_rewrites=%v", lockedProgress.PendingRewrites)
	}
	lockedIdentity, err := buildPipelineProjectAllIdentity(cfg, promptBundle, st, lockedProgress)
	if err != nil {
		return err
	}
	if lockedIdentity.Generation.GenerationID != identity.Generation.GenerationID {
		return fmt.Errorf("seal 获取锁期间 generation 输入漂移")
	}
	identity = lockedIdentity
	projected := st.ProjectedV2()
	if err := validatePipelineProjectAllComplete(projected, identity.Generation); err != nil {
		return fmt.Errorf("seal 前 project-all 未完整: %w", err)
	}
	building, err := projected.LoadBuildingGeneration(identity.Generation.GenerationID)
	if err != nil {
		return fmt.Errorf("seal 锁内读取 building generation: %w", err)
	}
	source, err := projected.LoadPlanningSourceSnapshot(identity.Generation.GenerationID)
	if err != nil || source == nil {
		return fmt.Errorf("seal 锁内 source snapshot 不可验证: %w", err)
	}
	if source.GenerationID != identity.Source.GenerationID ||
		source.BaseCanonRoot != identity.Source.BaseCanonRoot ||
		source.BaseStateRoot != identity.Source.BaseStateRoot ||
		source.StableOutlineRoot != identity.Source.StableOutlineRoot ||
		source.PlanningDependencyRoot != identity.Source.PlanningDependencyRoot ||
		source.RandomSeedContractRoot != identity.Source.RandomSeedContractRoot ||
		source.FoundationSnapshotRoot != identity.Source.FoundationSnapshotRoot ||
		source.RAGSnapshotRoot != identity.Source.RAGSnapshotRoot {
		return fmt.Errorf("seal 锁内 source snapshot identity 漂移")
	}
	preflightBundles, err := projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
	if err != nil {
		return fmt.Errorf("seal 锁内读取 projected bundles 预检: %w", err)
	}
	preflightInputs := make([]pipelinePreflightInput, 0, len(preflightBundles))
	for i := range preflightBundles {
		bundle := preflightBundles[i]
		preflightInputs = append(preflightInputs, pipelinePreflightInput{
			Stage:  pipelinePreflightStageSeal,
			Bundle: bundle,
			Expected: &pipelinePreflightSealedIdentity{
				GenerationID:           identity.Generation.GenerationID,
				Chapter:                bundle.Chapter,
				BundleDigest:           bundle.BundleDigest,
				PlanningContextDigest:  bundle.PlanningContextDigest,
				RenderContextSHA256:    bundle.RenderContextSHA256,
				ProjectedPreStateRoot:  bundle.ProjectedPreStateRoot,
				ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
			},
		})
	}
	if err := persistAndRequirePipelinePreflight(
		cfg.OutputDir,
		compilePipelinePreflightBatch(
			pipelinePreflightStageSeal,
			identity.Generation.GenerationID,
			preflightInputs,
		),
	); err != nil {
		return fmt.Errorf("seal 锁内 typed preflight: %w", err)
	}
	var arcManifest *domain.ArcPlanningManifest
	var seal *domain.SealReceiptV2
	if building != nil {
		if err := validatePipelineProjectAllGenerationIdentity(*building, identity.Generation); err != nil {
			return fmt.Errorf("seal 锁内 generation identity 漂移: %w", err)
		}
		arcManifest, err = savePipelineArcPlanningManifest(st, identity, *building)
		if err != nil {
			return fmt.Errorf("seal 当前弧计划清单: %w", err)
		}
		seal, err = projected.SealGenerationExpected(
			building.GenerationID,
			building.GenerationDigest,
			source.SnapshotDigest,
		)
		if err != nil {
			return fmt.Errorf("seal generation: %w", err)
		}
	} else {
		// Crash recovery: sealing is an atomic publication and may have
		// completed before activation/state evidence was written.
		existingSealed, loadErr := projected.LoadSealedGeneration(identity.Generation.GenerationID)
		if loadErr != nil || existingSealed == nil {
			return fmt.Errorf("seal 锁内 generation 既非 building 也非 sealed: %w", loadErr)
		}
		if err := validatePipelineProjectAllGenerationIdentity(*existingSealed, identity.Generation); err != nil {
			return fmt.Errorf("seal recovery generation identity 漂移: %w", err)
		}
		arcManifest, err = requirePipelineArcPlanningManifest(st, existingSealed)
		if err != nil {
			return fmt.Errorf("seal recovery 当前弧计划清单: %w", err)
		}
		seal, err = projected.LoadSealReceipt(existingSealed.GenerationID)
		if err != nil || seal == nil {
			return fmt.Errorf("seal recovery seal receipt 不可验证: %w", err)
		}
	}
	sealed, err := projected.LoadSealedGeneration(identity.Generation.GenerationID)
	if err != nil || sealed == nil {
		return fmt.Errorf("seal 后 generation 不可验证: %w", err)
	}
	if err := validatePipelineProjectAllGenerationIdentity(*sealed, identity.Generation); err != nil {
		return err
	}
	sealedRegistry, err := projected.LoadObligationRegistry(sealed.GenerationID)
	if err != nil || sealedRegistry == nil {
		return fmt.Errorf("seal 后 obligation registry 不可验证: %w", err)
	}
	if err := domain.ValidateArcObligationCarryBoundaryV2(*sealed, *sealedRegistry); err != nil {
		return fmt.Errorf("seal 当前弧跨弧义务边界: %w", err)
	}
	if currentManifest, err := requirePipelineArcPlanningManifest(st, sealed); err != nil {
		return err
	} else if currentManifest.ManifestDigest != arcManifest.ManifestDigest {
		return fmt.Errorf("seal 当前弧计划清单 digest 漂移")
	}

	activated, realization, err := projected.ActivateSealedGeneration(sealed.GenerationID, nil)
	if err != nil {
		return fmt.Errorf("seal 激活 generation/cursor: %w", err)
	}
	if activated == nil || realization == nil ||
		activated.GenerationID != sealed.GenerationID ||
		activated.SealReceiptDigest != seal.ReceiptDigest ||
		realization.ActiveGenerationID != sealed.GenerationID {
		return fmt.Errorf("seal 高层激活事务返回了不完整 control state")
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:seal] V%dA%d generation %s 已封版：第%d-%d章计划不可变；下一阶段逐章 promote/render，并保持逐章审核\n",
		identity.Arc.Volume,
		identity.Arc.Arc,
		sealed.GenerationID,
		sealed.FirstProjectedChapter,
		sealed.LastProjectedChapter,
	)
	return nil
}

// pipelinePromote installs the next sealed simulation/plan/render packet into
// live chapter slots. No model, planner, retrieval or context builder runs.
func pipelinePromote(opts cliOptions, flags pipelineFlags) (returnErr error) {
	_, releaseControl, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return fmt.Errorf("promote requires published outline-all: %w", err)
	}
	defer releasePublishedOutlineAllStage(releaseControl, "promote", &returnErr)
	cfg, promptBundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("promote 读取 progress: %w", err)
	}
	if len(progress.PendingRewrites) > 0 {
		return fmt.Errorf("promote 被 pending_rewrites=%v 阻塞；必须先完成当前正史返工", progress.PendingRewrites)
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return fmt.Errorf("promote 缺少 active sealed generation: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return fmt.Errorf("promote 缺少 realization cursor: %w", err)
	}
	sealed, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || sealed == nil {
		return fmt.Errorf("promote active generation 不可验证: %w", err)
	}
	if cursor.ActiveGenerationID != active.GenerationID {
		return fmt.Errorf("promote active generation 与 realization cursor 不一致")
	}
	if _, err := requirePipelineArcPlanningManifest(st, sealed); err != nil {
		return fmt.Errorf("promote 缺少当前弧不可变计划清单: %w", err)
	}
	if err := requirePreviousPipelineSealedRenderClosed(cfg.OutputDir, cursor, sealed); err != nil {
		return err
	}
	if cursor.NextPromoteChapter > sealed.LastProjectedChapter {
		return fmt.Errorf("promote generation %s 已全部实现", sealed.GenerationID)
	}
	chapter := cursor.NextPromoteChapter
	if flags.Start > 0 && flags.Start != chapter {
		return fmt.Errorf("promote 当前只能提升第 %d 章，不能用 --from=%d 跳章", chapter, flags.Start)
	}
	if flags.End > 0 && flags.End != chapter {
		return fmt.Errorf("promote 每次只提升一章；当前 --to 必须为 %d", chapter)
	}
	if err := validatePipelineSealedGenerationDependencies(cfg, promptBundle, *sealed); err != nil {
		return err
	}
	actionable, _, err := pipelineCurrentActionableChapter(st, pipelineFlags{Start: chapter, End: chapter})
	if err != nil {
		return err
	}
	if actionable != chapter {
		return fmt.Errorf("promote 正史当前可行动章=%d，sealed cursor 下一章=%d", actionable, chapter)
	}
	bundle, err := pipelineProjectAllBundleForChapter(projected, sealed.GenerationID, chapter)
	if err != nil {
		return err
	}
	actualPreStateRoot, err := pipelineProjectAllCurrentActualPreState(projected, cursor, sealed)
	if err != nil {
		return err
	}
	if actualPreStateRoot != bundle.ProjectedPreStateRoot {
		return fmt.Errorf(
			"promote 第 %d 章实际前态与 sealed projected 前态不一致；必须失效并重推演后缀",
			chapter,
		)
	}

	owner := pipelineExecutionOwner("promote", chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPromote,
		TargetChapter: chapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("promote 获取执行锁: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("promote 释放执行锁: %w", err)
		}
	}()
	progress, err = st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("promote 锁内读取 progress: %w", err)
	}
	if len(progress.PendingRewrites) > 0 {
		return fmt.Errorf("promote 锁内发现 pending_rewrites=%v", progress.PendingRewrites)
	}
	// Everything read before acquiring the live execution lease was only useful
	// for selecting the lock target. Re-read and re-validate the complete
	// control plane under the lease so a concurrent seal/invalidation/promotion
	// cannot be installed from stale pointers.
	active, err = projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return fmt.Errorf("promote 锁内缺少 active sealed generation: %w", err)
	}
	cursor, err = projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return fmt.Errorf("promote 锁内缺少 realization cursor: %w", err)
	}
	sealed, err = projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || sealed == nil {
		return fmt.Errorf("promote 锁内 active generation 不可验证: %w", err)
	}
	if active.GenerationID != cursor.ActiveGenerationID ||
		chapter != cursor.NextPromoteChapter ||
		chapter > sealed.LastProjectedChapter {
		return fmt.Errorf("promote 锁内 active generation/cursor 已漂移")
	}
	if err := requirePreviousPipelineSealedRenderClosed(cfg.OutputDir, cursor, sealed); err != nil {
		return err
	}
	if err := validatePipelineSealedGenerationDependencies(cfg, promptBundle, *sealed); err != nil {
		return fmt.Errorf("promote 锁内 sealed dependencies 漂移: %w", err)
	}
	actionable, _, err = pipelineCurrentActionableChapter(st, pipelineFlags{Start: chapter, End: chapter})
	if err != nil {
		return err
	}
	if actionable != chapter {
		return fmt.Errorf("promote 锁内正史当前可行动章=%d，sealed cursor 下一章=%d", actionable, chapter)
	}
	bundle, err = pipelineProjectAllBundleForChapter(projected, sealed.GenerationID, chapter)
	if err != nil {
		return err
	}
	actualPreStateRoot, err = pipelineProjectAllCurrentActualPreState(projected, cursor, sealed)
	if err != nil {
		return err
	}
	if actualPreStateRoot != bundle.ProjectedPreStateRoot {
		return fmt.Errorf("promote 锁内第 %d 章实际前态与 sealed projected 前态不一致", chapter)
	}
	if err := validatePipelineProjectAllLiveCanonForPromotion(
		cfg.OutputDir,
		progress,
		projected,
		cursor,
		sealed,
	); err != nil {
		return fmt.Errorf("promote 锁内第 %d 章正史边界失效: %w", chapter, err)
	}
	promotePreflight := compilePipelinePreflight(pipelinePreflightInput{
		Stage:  pipelinePreflightStagePromote,
		Bundle: *bundle,
		Expected: &pipelinePreflightSealedIdentity{
			GenerationID:           sealed.GenerationID,
			Chapter:                chapter,
			BundleDigest:           bundle.BundleDigest,
			PlanningContextDigest:  bundle.PlanningContextDigest,
			RenderContextSHA256:    bundle.RenderContextSHA256,
			ProjectedPreStateRoot:  bundle.ProjectedPreStateRoot,
			ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
		},
	})
	if err := persistAndRequirePipelinePreflight(cfg.OutputDir, promotePreflight); err != nil {
		return fmt.Errorf("promote 锁内第 %d 章 typed preflight: %w", chapter, err)
	}
	if err := retirePipelinePromotedProseInputs(
		cfg.OutputDir,
		sealed.GenerationID,
		chapter,
	); err != nil {
		return fmt.Errorf("promote 隔离第 %d 章旧正文表面: %w", chapter, err)
	}

	before, err := capturePipelinePlanProseSnapshot(st, progress, chapter)
	if err != nil {
		return err
	}
	baselineChapterSHA256, err := pipelineCompletedChapterSHA256(cfg.OutputDir, progress)
	if err != nil {
		return err
	}
	baselineCanonRoot, err := pipelineCanonRoot(cfg.OutputDir, progress)
	if err != nil {
		return err
	}
	renderDependencies, err := capturePipelineFrozenRenderDependencies(cfg.OutputDir)
	if err != nil {
		return err
	}
	renderDependencyRoot := pipelineProjectAllDigest(renderDependencies)
	planSemanticDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return err
	}

	var promotion domain.PromotionReceiptV2
	alreadyPromoted := cursor.ActivePromotedChapter == chapter
	if alreadyPromoted {
		if strings.TrimSpace(cursor.ActivePromotionReceiptDigest) == "" {
			return fmt.Errorf("promote cursor 已提升第 %d 章但缺 receipt digest", chapter)
		}
		existing, err := projected.LoadPromotionReceipt(
			sealed.GenerationID,
			chapter,
			cursor.ActivePromotionReceiptDigest,
		)
		if err != nil || existing == nil {
			return fmt.Errorf("promote 恢复 receipt: %w", err)
		}
		promotion = *existing
		if promotion.RenderDependencyRoot != renderDependencyRoot {
			return fmt.Errorf("promote 恢复时渲染依赖已漂移")
		}
	} else {
		promotion = domain.PromotionReceiptV2{
			Version:               domain.PromotionReceiptV2Version,
			GenerationID:          sealed.GenerationID,
			Chapter:               chapter,
			BundleDigest:          bundle.BundleDigest,
			ActualPreStateRoot:    actualPreStateRoot,
			ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
			RenderDependencyRoot:  renderDependencyRoot,
			FrozenPlanDigest:      planSemanticDigest,
			Mode:                  domain.ExactPromotionModeV2,
			PromotedAt:            time.Now().UTC().Format(time.RFC3339Nano),
		}
		promotion.ReceiptDigest, err = domain.ComputePromotionReceiptV2Digest(promotion)
		if err != nil {
			return err
		}
	}

	// Publish the immutable promotion receipt/cursor first. If live installation
	// crashes, a retry observes alreadyPromoted and idempotently finishes the
	// same receipt instead of minting another promotion after partially writing
	// chapter planning artifacts.
	if !alreadyPromoted {
		if _, err := projected.Promote(*cursor, promotion); err != nil {
			return fmt.Errorf("promote 发布权威 receipt/cursor: %w", err)
		}
	}
	cp, contextEnvelope, err := installPipelineProjectedChapter(
		st,
		bundle,
		promotion,
		before,
		baselineCanonRoot,
		baselineChapterSHA256,
		renderDependencies,
		pipelineRenderInputDigest(cfg, promptBundle),
		sealed.PlanningDependencyRoot,
	)
	if err != nil {
		return err
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return fmt.Errorf("promote 第 %d 章 live render freshness: %w", chapter, err)
	}
	if contextEnvelope.PayloadSHA256 != bundle.RenderContextSHA256 {
		return fmt.Errorf("promote 第 %d 章 render payload SHA 漂移", chapter)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:promote] 第 %d 章已从 sealed bundle %s 机械提升；plan checkpoint #%d，未调用 Planner\n",
		chapter,
		bundle.BundleDigest,
		cp.Seq,
	)
	return nil
}

// A mechanically promoted chapter must start with a clean prose surface.
// Legacy or interrupted drafts are retained for audit under quarantine, but
// they are removed from every path that read_chapter/novel_context can expose
// during the sealed render lease.
func retirePipelinePromotedProseInputs(
	outputDir string,
	generationID string,
	chapter int,
) error {
	if chapter <= 0 || strings.TrimSpace(generationID) == "" {
		return fmt.Errorf("sealed promotion prose retirement requires generation and chapter")
	}
	draftsDir := filepath.Join(outputDir, "drafts")
	prefix := fmt.Sprintf("%02d", chapter)
	candidates := []string{
		filepath.Join(draftsDir, prefix+".draft.md"),
		filepath.Join(draftsDir, prefix+".parts"),
		filepath.Join(draftsDir, prefix+".manual_candidate.md"),
		filepath.Join(draftsDir, prefix+".hard_consistency.json"),
		filepath.Join(draftsDir, prefix+".rerender_request.json"),
	}
	for _, pattern := range []string{
		filepath.Join(draftsDir, prefix+".candidate_*.md"),
		filepath.Join(draftsDir, prefix+".candidate-*.md"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		candidates = append(candidates, matches...)
	}
	seen := make(map[string]struct{}, len(candidates))
	active := make([]string, 0, len(candidates))
	for _, path := range candidates {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if _, err := os.Lstat(path); err == nil {
			active = append(active, path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(active) == 0 {
		return nil
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	archiveRoot := filepath.Join(
		outputDir,
		"meta", "quarantine", "sealed_promotion",
		filepath.Base(generationID),
		fmt.Sprintf("ch%04d", chapter),
		stamp,
	)
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		return err
	}
	type archivedSurface struct {
		Source  string `json:"source"`
		Archive string `json:"archive"`
	}
	archived := make([]archivedSurface, 0, len(active))
	for _, source := range active {
		target := filepath.Join(archiveRoot, filepath.Base(source))
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("archive %s: %w", source, err)
		}
		sourceRel, _ := filepath.Rel(outputDir, source)
		targetRel, _ := filepath.Rel(outputDir, target)
		archived = append(archived, archivedSurface{
			Source:  filepath.ToSlash(sourceRel),
			Archive: filepath.ToSlash(targetRel),
		})
	}
	_, err := writePipelinePlanningJSON(filepath.Join(archiveRoot, "manifest.json"), map[string]any{
		"version":       "sealed-promotion-prose-retirement.v1",
		"generation_id": generationID,
		"chapter":       chapter,
		"archived_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"surfaces":      archived,
	})
	return err
}

func validatePipelineProjectAllLiveCanonForPromotion(
	outputDir string,
	progress *domain.Progress,
	projected *store.ProjectedStoreV2,
	cursor *domain.RealizationCursorV2,
	generation *domain.PlanningGenerationV2,
) error {
	if progress == nil {
		return fmt.Errorf("live canon root validation requires progress")
	}
	expectedCanonRoot, err := pipelineProjectAllExpectedCanonRoot(projected, cursor, generation)
	if err != nil {
		return fmt.Errorf("read expected canon root: %w", err)
	}
	actualCanonRoot, err := pipelineProjectAllLiveCanonRoot(outputDir, progress)
	if err != nil {
		return fmt.Errorf("compute live canon root: %w", err)
	}
	if actualCanonRoot != expectedCanonRoot {
		return fmt.Errorf(
			"live canon root drift（actual=%s expected=%s）；must invalidate and reproject suffix",
			actualCanonRoot,
			expectedCanonRoot,
		)
	}
	return nil
}

func pipelineProjectAllExpectedCanonRoot(
	projected *store.ProjectedStoreV2,
	cursor *domain.RealizationCursorV2,
	generation *domain.PlanningGenerationV2,
) (string, error) {
	if projected == nil || cursor == nil || generation == nil {
		return "", fmt.Errorf("expected canon root requires projected store, cursor and generation")
	}
	if cursor.ActiveGenerationID != generation.GenerationID ||
		cursor.LastAcceptedChapter < generation.BaseCanonChapter {
		return "", fmt.Errorf("realization cursor does not belong to generation canon boundary")
	}
	if cursor.LastAcceptedChapter == generation.BaseCanonChapter {
		carriedOutcomeDigest := strings.TrimSpace(cursor.LastOutcomeReceiptDigest)
		if generation.ParentGenerationID == "" {
			if carriedOutcomeDigest != "" {
				return "", fmt.Errorf("first arc base cursor unexpectedly binds an outcome receipt")
			}
			return generation.BaseCanonRoot, nil
		}
		if carriedOutcomeDigest == "" {
			return "", fmt.Errorf("successor arc base cursor lost predecessor outcome receipt")
		}
		predecessor, err := projected.LoadSealedGeneration(generation.ParentGenerationID)
		if err != nil || predecessor == nil {
			return "", fmt.Errorf("load predecessor generation %s: %w", generation.ParentGenerationID, err)
		}
		if predecessor.LastProjectedChapter != generation.BaseCanonChapter {
			return "", fmt.Errorf("predecessor arc does not end at successor base chapter")
		}
		outcome, err := projected.LoadActualOutcomeReceipt(
			predecessor.GenerationID,
			generation.BaseCanonChapter,
			carriedOutcomeDigest,
		)
		if err != nil {
			return "", err
		}
		if outcome == nil ||
			outcome.ReceiptDigest != carriedOutcomeDigest ||
			outcome.Chapter != generation.BaseCanonChapter ||
			outcome.ActualCanonRoot != generation.BaseCanonRoot {
			return "", fmt.Errorf("successor arc base cursor does not bind the predecessor's exact accepted canon root")
		}
		return generation.BaseCanonRoot, nil
	}
	if strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return "", fmt.Errorf("last accepted chapter %d has no durable actual outcome", cursor.LastAcceptedChapter)
	}
	outcome, err := projected.LoadActualOutcomeReceipt(
		generation.GenerationID,
		cursor.LastAcceptedChapter,
		cursor.LastOutcomeReceiptDigest,
	)
	if err != nil {
		return "", err
	}
	if outcome == nil ||
		outcome.Chapter != cursor.LastAcceptedChapter ||
		outcome.ReceiptDigest != cursor.LastOutcomeReceiptDigest ||
		strings.TrimSpace(outcome.ActualCanonRoot) == "" {
		return "", fmt.Errorf("last accepted outcome does not bind an actual canon root")
	}
	return outcome.ActualCanonRoot, nil
}

func requirePreviousPipelineSealedRenderClosed(
	outputDir string,
	cursor *domain.RealizationCursorV2,
	generation *domain.PlanningGenerationV2,
) error {
	if cursor == nil || generation == nil {
		return fmt.Errorf("promote previous render closure requires cursor and generation")
	}
	if cursor.LastAcceptedChapter <= generation.BaseCanonChapter {
		return nil
	}
	// Promote publishes the immutable receipt/cursor before installing the live
	// plan.  A crash in that gap leaves the *next* chapter active while the
	// previous accepted chapter is still the render receipt being verified.
	// That exact state is an idempotent recovery, not an attempt to jump over an
	// unclosed chapter.  The caller subsequently reloads the content-addressed
	// active receipt and verifies it against the sealed bundle before install.
	activeReceiptRecovery := pipelinePromoteIsActiveReceiptRecovery(cursor)
	if (cursor.ActivePromotedChapter != 0 && !activeReceiptRecovery) ||
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return fmt.Errorf("promote 上一章 actual outcome 尚未收口")
	}
	if _, err := verifyPipelineRenderStage(
		outputDir,
		domain.PipelineStageEvidence{Stage: "render"},
	); err != nil {
		return fmt.Errorf(
			"promote 禁止越过尚未完成 render receipt/directory finalize 的第 %d 章: %w",
			cursor.LastAcceptedChapter,
			err,
		)
	}
	if err := requirePipelineChapterAcceptance(
		store.NewStore(outputDir),
		generation,
		cursor.LastAcceptedChapter,
		cursor.LastOutcomeReceiptDigest,
	); err != nil {
		return fmt.Errorf(
			"promote 禁止越过尚未形成不可变逐章审核回执的第 %d 章: %w",
			cursor.LastAcceptedChapter,
			err,
		)
	}
	return nil
}

func pipelinePromoteIsActiveReceiptRecovery(cursor *domain.RealizationCursorV2) bool {
	return cursor != nil &&
		cursor.ActivePromotedChapter > 0 &&
		cursor.ActivePromotedChapter == cursor.NextPromoteChapter &&
		strings.TrimSpace(cursor.ActivePromotionReceiptDigest) != ""
}

func installPipelineProjectedChapter(
	st *store.Store,
	bundle *domain.ProjectedChapterBundle,
	promotion domain.PromotionReceiptV2,
	before pipelinePlanProseSnapshot,
	baselineCanonRoot string,
	baselineChapterSHA256 map[string]string,
	renderDependencies map[string]string,
	runInputDigest string,
	planningDependencyRoot string,
) (*domain.Checkpoint, *tools.FrozenDraftRenderContext, error) {
	if st == nil || bundle == nil {
		return nil, nil, fmt.Errorf("install projected chapter requires store and bundle")
	}
	chapter := bundle.Chapter
	if bundle.RAGFactReceipt == nil {
		return nil, nil, fmt.Errorf("promote 第 %d 章缺少 RAG fact receipt", chapter)
	}
	factDigest, err := domain.RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
	if err != nil {
		return nil, nil, fmt.Errorf("promote 第 %d 章 RAG fact receipt 非法: %w", chapter, err)
	}
	if bundle.RAGFactReceiptDigest != factDigest {
		return nil, nil, fmt.Errorf("promote 第 %d 章 RAG fact receipt digest 不匹配", chapter)
	}
	if err := st.RAG.SaveRAGFactReceipt(*bundle.RAGFactReceipt); err != nil {
		return nil, nil, fmt.Errorf("promote 安装第 %d 章 RAG fact receipt: %w", chapter, err)
	}
	if bundle.CraftRecallReceipt == nil {
		return nil, nil, fmt.Errorf("promote 第 %d 章缺少 project-all craft receipt", chapter)
	}
	if err := domain.ValidateProjectAllCraftRecallReceipt(*bundle.CraftRecallReceipt); err != nil {
		return nil, nil, fmt.Errorf("promote 第 %d 章 craft receipt 非法: %w", chapter, err)
	}
	craftDigest, err := domain.CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
	if err != nil {
		return nil, nil, fmt.Errorf("promote 第 %d 章 craft receipt digest 非法: %w", chapter, err)
	}
	if bundle.CraftRecallReceiptDigest != craftDigest {
		return nil, nil, fmt.Errorf("promote 第 %d 章 craft receipt digest 不匹配", chapter)
	}
	if err := st.RAG.SaveCraftRecallReceipt(*bundle.CraftRecallReceipt); err != nil {
		return nil, nil, fmt.Errorf("promote 安装第 %d 章 craft receipt: %w", chapter, err)
	}
	_ = st.DeleteChapterWorldSimulationPartial(chapter)
	_ = st.Drafts.DeleteChapterPlanPartial(chapter)
	if err := st.SaveChapterWorldSimulation(bundle.ChapterWorldSimulation); err != nil {
		return nil, nil, fmt.Errorf("promote 安装 world simulation: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"chapter_world_simulation",
		fmt.Sprintf("meta/chapter_simulations/%03d.json", chapter),
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		return nil, nil, fmt.Errorf("promote checkpoint world simulation: %w", err)
	}
	if err := st.Drafts.SaveChapterPlan(bundle.ChapterPlan); err != nil {
		return nil, nil, fmt.Errorf("promote 安装 chapter plan: %w", err)
	}
	if !st.Progress.IsChapterCompleted(chapter) {
		if err := st.Progress.StartChapter(chapter); err != nil {
			return nil, nil, fmt.Errorf("promote 启动第 %d 章 progress: %w", chapter, err)
		}
	}
	cp, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"plan",
		fmt.Sprintf("drafts/%02d.plan.json", chapter),
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		return nil, nil, fmt.Errorf("promote checkpoint plan: %w", err)
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, chapter); err != nil {
		return nil, nil, err
	}
	contextEnvelope, err := tools.PublishFrozenDraftRenderContext(
		st,
		chapter,
		cp.Digest,
		bundle.RenderContext,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("promote 发布冻结正文上下文: %w", err)
	}
	frozen := pipelineFrozenPlan{
		Version:                 pipelinePlanningSchema,
		Chapter:                 chapter,
		PlanPath:                fmt.Sprintf("drafts/%02d.plan.json", chapter),
		PlanDigest:              cp.Digest,
		PlanCheckpointSeq:       cp.Seq,
		BaselineCommitSeq:       before.CommitSeq,
		BaselineCompletedDigest: before.CompletedDigest,
		BaselineCanonRoot:       baselineCanonRoot,
		BaselineChapterSHA256:   maps.Clone(baselineChapterSHA256),
		RenderDependencySHA256:  maps.Clone(renderDependencies),
		PipelineRunInputDigest:  runInputDigest,
		RenderContextPath:       tools.FrozenDraftRenderContextPath,
		RenderContextSHA256:     contextEnvelope.PayloadSHA256,
		PlanningGenerationID:    bundle.GenerationID,
		PlanningDependencyRoot:  planningDependencyRoot,
		ProjectionBinding:       "sealed_v2",
		ProjectedPlanSHA256:     promotion.FrozenPlanDigest,
		ProjectedPreStateRoot:   bundle.ProjectedPreStateRoot,
		ProjectedPostStateRoot:  bundle.ProjectedPostStateRoot,
		ProjectedBundleDigest:   bundle.BundleDigest,
		PromotionReceiptDigest:  promotion.ReceiptDigest,
		FrozenAt:                time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineFrozenPlanPath), frozen); err != nil {
		return nil, nil, fmt.Errorf("promote 保存冻结计划回执: %w", err)
	}
	if err := verifyPipelinePlanDidNotWriteProse(
		st,
		mustLoadPipelineProgress(st),
		chapter,
		before,
	); err != nil {
		return nil, nil, err
	}
	return cp, contextEnvelope, nil
}

func mustLoadPipelineProgress(st *store.Store) *domain.Progress {
	progress, _ := st.Progress.Load()
	return progress
}

func validatePipelineSealedGenerationDependencies(
	cfg bootstrap.Config,
	promptBundle assets.Bundle,
	generation domain.PlanningGenerationV2,
) error {
	var receipt pipelinePreplanReceipt
	if err := readPipelinePlanningJSON(
		filepath.Join(cfg.OutputDir, pipelinePlanningReceiptPath),
		&receipt,
	); err != nil {
		return fmt.Errorf("promote 读取 generation 来源 preplan: %w", err)
	}
	stableRoot, err := pipelineProjectAllStableOutlineRoot(cfg.OutputDir, receipt)
	if err != nil {
		return err
	}
	if stableRoot != generation.StableOutlineRoot {
		return fmt.Errorf("sealed generation stable outline 已漂移；禁止继续提升")
	}
	source, err := store.NewStore(cfg.OutputDir).ProjectedV2().
		LoadPlanningSourceSnapshot(generation.GenerationID)
	if err != nil || source == nil {
		return fmt.Errorf("promote 读取 generation captured source snapshot: %w", err)
	}
	dependencyRoot, err := pipelineProjectAllDependencyRootWithSourceRoots(
		cfg,
		promptBundle,
		receipt,
		source.FoundationSnapshotRoot,
		source.RAGSnapshotRoot,
	)
	if err != nil {
		return err
	}
	if dependencyRoot != generation.PlanningDependencyRoot {
		return fmt.Errorf("sealed generation 模型/provider/prompt/资料依赖已漂移；禁止继续提升")
	}
	return nil
}

func verifyPipelineProjectAllStage(
	outputDir string,
	evidence domain.PipelineStageEvidence,
) (domain.PipelineStageEvidence, error) {
	projected := store.NewStore(outputDir).ProjectedV2()
	cursor, err := projected.LoadProjectionCursor()
	if err != nil || cursor == nil {
		return evidence, fmt.Errorf("project-all 缺少 projection cursor: %w", err)
	}
	generation, err := projected.LoadBuildingGeneration(cursor.GenerationID)
	baseDir := filepath.Join("meta", "planning", "v2", ".building", cursor.GenerationID)
	if err != nil {
		return evidence, err
	}
	if generation == nil {
		generation, err = projected.LoadSealedGeneration(cursor.GenerationID)
		baseDir = filepath.Join("meta", "planning", "v2", "generations", cursor.GenerationID)
		if err != nil || generation == nil {
			return evidence, fmt.Errorf("project-all cursor generation 不存在或不可验证: %w", err)
		}
	}
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return evidence, err
	}
	registry, err := projected.LoadObligationRegistry(generation.GenerationID)
	if err != nil || registry == nil {
		return evidence, fmt.Errorf("project-all obligation registry 不可验证: %w", err)
	}
	if len(bundles) != generation.ExpectedChapterCount {
		return evidence, fmt.Errorf("project-all formal bundle 只完成 %d/%d", len(bundles), generation.ExpectedChapterCount)
	}
	if err := domain.ValidateProjectedChapterBundleChain(*generation, bundles, *registry); err != nil {
		return evidence, err
	}
	if cursor.LastProjectedChapter != generation.LastProjectedChapter ||
		cursor.NextProjectChapter != generation.LastProjectedChapter+1 ||
		cursor.LastBundleDigest != generation.ChainTailRoot {
		return evidence, fmt.Errorf("project-all projection cursor 未抵达完整 chain tail")
	}
	evidence.Artifacts = append(evidence.Artifacts,
		filepath.ToSlash(filepath.Join(baseDir, "generation.json")),
		filepath.ToSlash(filepath.Join(baseDir, "source_snapshot.json")),
		filepath.ToSlash(filepath.Join(baseDir, "obligation_registry.json")),
		"meta/planning/v2/projection_cursor.json",
	)
	evidence.Message = fmt.Sprintf(
		"generation %s has %d/%d formal projected bundles and no prose",
		generation.GenerationID,
		len(bundles),
		generation.ExpectedChapterCount,
	)
	return evidence, nil
}

func verifyPipelineSealStage(
	outputDir string,
	evidence domain.PipelineStageEvidence,
) (domain.PipelineStageEvidence, error) {
	projected := store.NewStore(outputDir).ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return evidence, fmt.Errorf("seal 缺少 active generation: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return evidence, fmt.Errorf("seal 缺少 realization cursor: %w", err)
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return evidence, fmt.Errorf("seal generation 不可验证: %w", err)
	}
	seal, err := projected.LoadSealReceipt(active.GenerationID)
	if err != nil || seal == nil {
		return evidence, fmt.Errorf("seal receipt 不可验证: %w", err)
	}
	if active.SealReceiptDigest != seal.ReceiptDigest ||
		cursor.ActiveGenerationID != active.GenerationID {
		return evidence, fmt.Errorf("seal active pointer/cursor/receipt 未绑定同一 generation")
	}
	manifest, err := requirePipelineArcPlanningManifest(store.NewStore(outputDir), generation)
	if err != nil {
		return evidence, fmt.Errorf("seal arc planning manifest 不可验证: %w", err)
	}
	baseDir := filepath.Join("meta", "planning", "v2", "generations", active.GenerationID)
	manifestPath := filepath.Join(
		"meta", "planning", "v3", "arc_cycle", "manifests",
		active.GenerationID,
		manifest.ManifestDigest+".json",
	)
	evidence.Artifacts = append(evidence.Artifacts,
		"meta/planning/v2/active_generation.json",
		filepath.ToSlash(filepath.Join(baseDir, "generation.json")),
		filepath.ToSlash(filepath.Join(baseDir, "manifests", "chain.json")),
		filepath.ToSlash(filepath.Join(baseDir, "seal_receipt.json")),
		filepath.ToSlash(manifestPath),
	)
	evidence.Message = fmt.Sprintf(
		"generation %s sealed with %d immutable formal bundles",
		generation.GenerationID,
		generation.ExpectedChapterCount,
	)
	return evidence, nil
}

func verifyPipelinePromoteStage(
	outputDir string,
	evidence domain.PipelineStageEvidence,
) (domain.PipelineStageEvidence, error) {
	st := store.NewStore(outputDir)
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return evidence, fmt.Errorf("promote 缺少 active generation: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return evidence, fmt.Errorf("promote 缺少 realization cursor: %w", err)
	}
	frozen, cp, err := loadAndVerifyPipelineFrozenPlan(outputDir)
	if err != nil {
		return evidence, err
	}
	expectedPromotionDigest := cursor.ActivePromotionReceiptDigest
	switch {
	case cursor.ActivePromotedChapter == frozen.Chapter &&
		strings.TrimSpace(expectedPromotionDigest) != "":
	case cursor.ActivePromotedChapter == 0 &&
		cursor.LastAcceptedChapter == frozen.Chapter &&
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) != "":
		outcome, loadErr := projected.LoadActualOutcomeReceipt(
			active.GenerationID,
			frozen.Chapter,
			cursor.LastOutcomeReceiptDigest,
		)
		if loadErr != nil || outcome == nil {
			return evidence, fmt.Errorf("promote 已验收 outcome 不可验证: %w", loadErr)
		}
		expectedPromotionDigest = outcome.PromotionReceiptDigest
	default:
		return evidence, fmt.Errorf("promote 尚未提升或验收 frozen 第 %d 章", frozen.Chapter)
	}
	if frozen.ProjectionBinding != "sealed_v2" ||
		frozen.PlanningGenerationID != active.GenerationID ||
		frozen.PromotionReceiptDigest != expectedPromotionDigest {
		return evidence, fmt.Errorf("promote frozen plan 未绑定 active sealed generation/cursor")
	}
	bundle, err := pipelineProjectAllBundleForChapter(projected, active.GenerationID, frozen.Chapter)
	if err != nil {
		return evidence, err
	}
	if frozen.ProjectedBundleDigest != bundle.BundleDigest ||
		frozen.ProjectedPreStateRoot != bundle.ProjectedPreStateRoot ||
		frozen.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
		return evidence, fmt.Errorf("promote frozen plan 与 sealed bundle 不一致")
	}
	promotion, err := projected.LoadPromotionReceipt(
		active.GenerationID,
		frozen.Chapter,
		frozen.PromotionReceiptDigest,
	)
	if err != nil || promotion == nil {
		return evidence, fmt.Errorf("promote receipt 不可验证: %w", err)
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, frozen.Chapter); err != nil {
		return evidence, err
	}
	receiptPath := filepath.Join(
		"meta", "planning", "v2", "promotion_receipts",
		active.GenerationID,
		fmt.Sprintf("%04d", frozen.Chapter),
		frozen.PromotionReceiptDigest+".json",
	)
	evidence.Artifacts = append(evidence.Artifacts,
		pipelineFrozenPlanPath,
		frozen.PlanPath,
		frozen.RenderContextPath,
		filepath.ToSlash(receiptPath),
	)
	evidence.Checkpoints = append(
		evidence.Checkpoints,
		fmt.Sprintf("chapter:%d:plan#%d:%s", frozen.Chapter, cp.Seq, cp.Digest),
	)
	evidence.Message = fmt.Sprintf(
		"chapter %d mechanically promoted from sealed bundle %s",
		frozen.Chapter,
		bundle.BundleDigest,
	)
	return evidence, nil
}

type pipelineSealedRenderBinding struct {
	Active                   domain.ActivePlanningGenerationV2
	Cursor                   domain.RealizationCursorV2
	Generation               domain.PlanningGenerationV2
	Bundle                   domain.ProjectedChapterBundle
	Promotion                domain.PromotionReceiptV2
	ConvergenceReplanReceipt *domain.SealedConvergenceReplanReceipt
	Outcome                  *domain.ActualOutcomeReceiptV2
}

func validatePipelineSealedRenderBinding(
	st *store.Store,
	frozen *pipelineFrozenPlan,
	postCommitRecovery bool,
) (*pipelineSealedRenderBinding, error) {
	if st == nil || frozen == nil || frozen.ProjectionBinding != "sealed_v2" {
		return nil, fmt.Errorf("sealed render binding requires store and sealed_v2 frozen plan")
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return nil, fmt.Errorf("render sealed_v2 active generation 不可读: %w", err)
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return nil, fmt.Errorf("render sealed_v2 generation 不可验证: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return nil, fmt.Errorf("render sealed_v2 realization cursor 不可读: %w", err)
	}
	if frozen.PlanningGenerationID != active.GenerationID ||
		cursor.ActiveGenerationID != active.GenerationID ||
		frozen.PlanningDependencyRoot != generation.PlanningDependencyRoot {
		return nil, fmt.Errorf("render sealed_v2 frozen generation/dependency 与 active control state 不一致")
	}
	bundle, err := pipelineProjectAllBundleForChapter(projected, active.GenerationID, frozen.Chapter)
	if err != nil {
		return nil, err
	}
	if frozen.ProjectedBundleDigest != bundle.BundleDigest ||
		frozen.ProjectedPreStateRoot != bundle.ProjectedPreStateRoot ||
		frozen.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
		return nil, fmt.Errorf("render sealed_v2 frozen plan/context 未绑定 exact bundle")
	}
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return nil, err
	}
	if frozen.ProjectedPlanSHA256 != planDigest {
		return nil, fmt.Errorf("render sealed_v2 formal plan semantic digest 漂移")
	}
	livePlan, err := st.Drafts.LoadChapterPlan(frozen.Chapter)
	if err != nil || livePlan == nil {
		return nil, fmt.Errorf("render sealed_v2 live formal plan 不可读: %w", err)
	}
	livePlanDigest, err := domain.ComputeChapterPlanV2Digest(*livePlan)
	if err != nil {
		return nil, err
	}
	var convergenceReceipt *domain.SealedConvergenceReplanReceipt
	if strings.TrimSpace(frozen.ConvergenceReplanReceiptDigest) == "" {
		if frozen.RenderContextSHA256 != bundle.RenderContextSHA256 || livePlanDigest != planDigest {
			return nil, fmt.Errorf("render sealed_v2 frozen plan/context 未绑定 exact bundle")
		}
	} else {
		convergenceReceipt, err = loadAndVerifyPipelineSealedConvergenceReplanReceipt(
			st.Dir(), frozen, bundle, livePlanDigest,
		)
		if err != nil {
			return nil, fmt.Errorf("render sealed_v2 convergence successor binding 不可验证: %w", err)
		}
	}
	promotion, err := projected.LoadPromotionReceipt(
		active.GenerationID,
		frozen.Chapter,
		frozen.PromotionReceiptDigest,
	)
	if err != nil || promotion == nil {
		return nil, fmt.Errorf("render sealed_v2 promotion receipt 不可验证: %w", err)
	}
	if promotion.RenderDependencyRoot != pipelineProjectAllDigest(frozen.RenderDependencySHA256) {
		return nil, fmt.Errorf("render sealed_v2 promotion receipt 未绑定 frozen render dependencies")
	}
	binding := &pipelineSealedRenderBinding{
		Active:                   *active,
		Cursor:                   *cursor,
		Generation:               *generation,
		Bundle:                   *bundle,
		Promotion:                *promotion,
		ConvergenceReplanReceipt: convergenceReceipt,
	}
	switch {
	case cursor.ActivePromotedChapter == frozen.Chapter &&
		cursor.ActivePromotionReceiptDigest == promotion.ReceiptDigest:
		return binding, nil
	case postCommitRecovery &&
		(cursor.ActivePromotedChapter == 0 ||
			(pipelinePromoteIsActiveReceiptRecovery(cursor) &&
				cursor.NextPromoteChapter == frozen.Chapter+1)) &&
		cursor.LastAcceptedChapter == frozen.Chapter &&
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) != "":
		outcome, err := projected.LoadActualOutcomeReceipt(
			active.GenerationID,
			frozen.Chapter,
			cursor.LastOutcomeReceiptDigest,
		)
		if err != nil || outcome == nil {
			return nil, fmt.Errorf("render sealed_v2 recovery outcome receipt 不可验证: %w", err)
		}
		binding.Outcome = outcome
		return binding, nil
	default:
		return nil, fmt.Errorf(
			"render sealed_v2 cursor 不允许第 %d 章执行或恢复（active_promoted=%d last_accepted=%d）",
			frozen.Chapter,
			cursor.ActivePromotedChapter,
			cursor.LastAcceptedChapter,
		)
	}
}

func acceptPipelineSealedRenderOutcome(
	st *store.Store,
	binding *pipelineSealedRenderBinding,
	commit *domain.Checkpoint,
	bodySHA string,
	actualCanonRoot string,
	actualMatch *pipelineSealedActualDeltaMatch,
) (*domain.ActualOutcomeReceiptV2, error) {
	if st == nil || binding == nil || commit == nil {
		return nil, fmt.Errorf("sealed render outcome requires store, binding and commit")
	}
	if actualMatch == nil || !actualMatch.ProjectionMatch || !actualMatch.Complete {
		return nil, fmt.Errorf("sealed render outcome requires complete independent actual-delta evidence")
	}
	if strings.TrimSpace(actualCanonRoot) == "" {
		return nil, fmt.Errorf("sealed render outcome requires durable actual canon root")
	}
	projectedDigest, err := domain.ComputeProjectedDeltaV2Digest(binding.Bundle.ProjectedDelta)
	if err != nil {
		return nil, err
	}
	actualDigest, err := domain.ComputeProjectedDeltaV2Digest(actualMatch.ActualDelta)
	if err != nil {
		return nil, err
	}
	if actualDigest != projectedDigest {
		return nil, fmt.Errorf("sealed render independently reconstructed actual delta differs from projected delta")
	}
	if binding.Outcome != nil {
		if binding.Outcome.ChapterBodySHA256 != bodySHA ||
			binding.Outcome.CommitCheckpointSeq != commit.Seq ||
			binding.Outcome.ActualCanonRoot != actualCanonRoot {
			return nil, fmt.Errorf("sealed render recovery outcome 未绑定当前 exact body/commit")
		}
		recoveredDigest, digestErr := domain.ComputeProjectedDeltaV2Digest(binding.Outcome.ActualDelta)
		if digestErr != nil || recoveredDigest != actualDigest {
			return nil, fmt.Errorf("sealed render recovery outcome actual delta 与当前独立证据不一致")
		}
		return binding.Outcome, nil
	}
	actualPostStateRoot, err := domain.DeriveProjectedPostStateRootV2(
		binding.Promotion.ActualPreStateRoot,
		actualMatch.ActualDelta,
	)
	if err != nil {
		return nil, err
	}
	if actualPostStateRoot != binding.Bundle.ProjectedPostStateRoot {
		return nil, fmt.Errorf("sealed render actual post-state root differs from projected post-state root")
	}
	if !samePipelineStringSet(
		actualMatch.ObligationsSatisfied,
		binding.Bundle.ObligationsConsumed,
	) {
		return nil, fmt.Errorf("sealed render consumed obligations lack exact independent realization evidence")
	}
	outcome := domain.ActualOutcomeReceiptV2{
		Version:                     domain.ActualOutcomeReceiptV2Version,
		GenerationID:                binding.Generation.GenerationID,
		Chapter:                     binding.Bundle.Chapter,
		PromotionReceiptDigest:      binding.Promotion.ReceiptDigest,
		ChapterBodySHA256:           bodySHA,
		CommitCheckpointSeq:         commit.Seq,
		ActualDelta:                 actualMatch.ActualDelta,
		ActualPreStateRoot:          binding.Promotion.ActualPreStateRoot,
		ActualPostStateRoot:         actualPostStateRoot,
		ActualCanonRoot:             actualCanonRoot,
		ProjectedPostStateRoot:      binding.Bundle.ProjectedPostStateRoot,
		ObligationsSatisfied:        append([]string(nil), actualMatch.ObligationsSatisfied...),
		ObligationsCreatedUnplanned: []string{},
		ProjectionMatch:             true,
		AcceptedAt:                  time.Now().UTC().Format(time.RFC3339Nano),
	}
	outcome.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
	if err != nil {
		return nil, err
	}
	cursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil || cursor == nil {
		return nil, fmt.Errorf("sealed render accept 读取 realization cursor: %w", err)
	}
	if _, err := st.ProjectedV2().AcceptOutcome(*cursor, outcome); err != nil {
		return nil, fmt.Errorf("sealed render 发布 actual outcome/cursor: %w", err)
	}
	return &outcome, nil
}

func samePipelineStringSet(left, right []string) bool {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range left {
		if value = strings.TrimSpace(value); value != "" {
			leftSet[value] = struct{}{}
		}
	}
	for _, value := range right {
		if value = strings.TrimSpace(value); value != "" {
			rightSet[value] = struct{}{}
		}
	}
	return maps.Equal(leftSet, rightSet)
}
