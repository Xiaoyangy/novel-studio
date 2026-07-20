package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	currentFrozenPlanMarkerPath = "meta/planning/current_frozen_plan.json"
	sealedV2ProjectionBinding   = "sealed_v2"
)

type sealedV2FrozenPlanMarker struct {
	Version                        string `json:"version"`
	Chapter                        int    `json:"chapter"`
	PlanDigest                     string `json:"plan_digest"`
	PlanCheckpointSeq              int64  `json:"plan_checkpoint_seq"`
	RenderContextSHA256            string `json:"render_context_sha256"`
	PlanningGenerationID           string `json:"planning_generation_id"`
	ProjectionBinding              string `json:"projection_binding"`
	ProjectedPlanSHA256            string `json:"projected_plan_sha256"`
	ProjectedBundleDigest          string `json:"projected_bundle_digest"`
	PromotionReceiptDigest         string `json:"promotion_receipt_digest"`
	ConvergenceReplanReceiptDigest string `json:"convergence_replan_receipt_digest,omitempty"`
}

// validateRAGFactPlanForChapterRender selects the sealed verifier only after
// proving that the render lease, live formal-plan checkpoint, immutable bundle,
// active promotion and frozen marker all identify the same chapter and plan.
// Any incomplete or stale marker falls back to the ordinary live-index check.
func validateRAGFactPlanForChapterRender(
	st *store.Store,
	chapter int,
	plan domain.ChapterPlan,
) error {
	receipt, sealed := exactSealedV2RAGReceiptForRender(st, chapter, plan)
	if !sealed {
		return ValidateRAGFactPlanCurrent(st, plan)
	}
	return validateRAGFactPlan(st, plan, ragFactPlanValidation{
		skipLiveIndexMembership: true,
		sealedReceipt:           receipt,
	})
}

func exactSealedV2RAGReceiptForRender(
	st *store.Store,
	chapter int,
	plan domain.ChapterPlan,
) (*domain.RAGFactReceipt, bool) {
	if st == nil || chapter <= 0 || plan.Chapter != chapter {
		return nil, false
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil ||
		lock.Mode != domain.PipelineExecutionRender ||
		lock.TargetChapter != chapter ||
		strings.TrimSpace(lock.PlanDigest) == "" {
		return nil, false
	}
	checkpoint, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil || checkpoint == nil || checkpoint.Digest != lock.PlanDigest {
		return nil, false
	}
	bundle, sealed := exactSealedV2FrozenBundleForPlan(st, chapter, plan)
	if !sealed {
		return nil, false
	}

	receiptID, factsSHA, count, err := ragFactReceiptIdentityFromSources(plan.CausalSimulation.ContextSources)
	if err != nil {
		return nil, false
	}
	if count == 0 {
		if bundle.RAGFactReceipt != nil {
			return nil, false
		}
		return nil, true
	}
	if bundle.RAGFactReceipt == nil ||
		bundle.RAGFactReceipt.ID != receiptID ||
		bundle.RAGFactReceipt.Chapter != chapter ||
		bundle.RAGFactReceipt.SelectedFactsSHA256 != factsSHA {
		return nil, false
	}
	exact := *bundle.RAGFactReceipt
	return &exact, true
}

// exactSealedV2FrozenBundleForPlan proves the immutable planning identity
// without depending on a currently-held render lease. Pipeline stage
// verification runs immediately before that lease is acquired, while prose
// tools separately enforce the same-process render lock through
// guardPipelineProseExecution.
func exactSealedV2FrozenBundleForPlan(
	st *store.Store,
	chapter int,
	plan domain.ChapterPlan,
) (*domain.ProjectedChapterBundle, bool) {
	if st == nil || chapter <= 0 || plan.Chapter != chapter {
		return nil, false
	}
	checkpoint, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil || checkpoint == nil {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath)))
	if err != nil {
		return nil, false
	}
	var marker sealedV2FrozenPlanMarker
	if json.Unmarshal(raw, &marker) != nil ||
		marker.Version != "pipeline-planning.v1" ||
		marker.ProjectionBinding != sealedV2ProjectionBinding ||
		marker.Chapter != chapter ||
		marker.PlanDigest != checkpoint.Digest ||
		strings.TrimSpace(marker.PlanningGenerationID) == "" ||
		strings.TrimSpace(marker.ProjectedPlanSHA256) == "" ||
		strings.TrimSpace(marker.ProjectedBundleDigest) == "" ||
		strings.TrimSpace(marker.PromotionReceiptDigest) == "" {
		return nil, false
	}

	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil || active.GenerationID != marker.PlanningGenerationID {
		return nil, false
	}
	generation, err := projected.LoadSealedGeneration(marker.PlanningGenerationID)
	if err != nil || generation == nil {
		return nil, false
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil ||
		cursor.ActiveGenerationID != marker.PlanningGenerationID ||
		cursor.ActivePromotedChapter != chapter ||
		cursor.ActivePromotionReceiptDigest != marker.PromotionReceiptDigest {
		return nil, false
	}
	bundles, err := projected.LoadProjectedChapterBundles(marker.PlanningGenerationID)
	if err != nil {
		return nil, false
	}
	var bundle *domain.ProjectedChapterBundle
	for i := range bundles {
		if bundles[i].Chapter != chapter {
			continue
		}
		if bundle != nil {
			return nil, false
		}
		bundle = &bundles[i]
	}
	if bundle == nil ||
		bundle.BundleDigest != marker.ProjectedBundleDigest ||
		bundle.GenerationID != generation.GenerationID {
		return nil, false
	}
	bundlePlanDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil || bundlePlanDigest != marker.ProjectedPlanSHA256 {
		return nil, false
	}
	currentPlanDigest, err := domain.ComputeChapterPlanV2Digest(plan)
	if err != nil {
		return nil, false
	}
	if strings.TrimSpace(marker.ConvergenceReplanReceiptDigest) == "" {
		if currentPlanDigest != bundlePlanDigest {
			return nil, false
		}
	} else if !sealedConvergenceReplanMarkerCurrent(
		st,
		marker,
		checkpoint,
		bundlePlanDigest,
		currentPlanDigest,
	) {
		return nil, false
	}
	promotion, err := projected.LoadPromotionReceipt(
		marker.PlanningGenerationID,
		chapter,
		marker.PromotionReceiptDigest,
	)
	if err != nil || promotion == nil ||
		promotion.ReceiptDigest != marker.PromotionReceiptDigest ||
		promotion.BundleDigest != bundle.BundleDigest ||
		promotion.FrozenPlanDigest != bundlePlanDigest {
		return nil, false
	}
	return bundle, true
}
