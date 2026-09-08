package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// A promoted live plan intentionally has no projected character sidecars.
// Its evidence is the exact current promotion and immutable sealed bundle,
// not mere presence in a sealed directory and never accepted-canon authority.
func validatePromotedCharacterSimulation(st *store.Store, simulation domain.ChapterWorldSimulation) (bool, error) {
	projected := st.ProjectedV2()
	cursor, err := projected.LoadRealizationCursor()
	if err != nil {
		return false, err
	}
	if cursor == nil || cursor.ActiveGenerationID != simulation.GenerationID || cursor.ActivePromotedChapter != simulation.Chapter || cursor.ActivePromotionReceiptDigest == "" {
		return false, nil
	}
	active, err := projected.LoadActiveGeneration()
	if err != nil {
		return false, err
	}
	if active == nil || active.GenerationID != simulation.GenerationID {
		return false, fmt.Errorf("promoted character simulation does not bind the active generation")
	}
	promotion, err := projected.LoadPromotionReceipt(simulation.GenerationID, simulation.Chapter, cursor.ActivePromotionReceiptDigest)
	if err != nil {
		return false, err
	}
	if promotion == nil {
		return false, fmt.Errorf("promoted character simulation has no exact current promotion receipt")
	}
	bundles, err := projected.LoadProjectedChapterBundles(simulation.GenerationID)
	if err != nil {
		return false, err
	}
	var matched *domain.ProjectedChapterBundle
	for i := range bundles {
		if bundles[i].Chapter == simulation.Chapter {
			if matched != nil {
				return false, fmt.Errorf("promoted character simulation has ambiguous chapter evidence")
			}
			matched = &bundles[i]
		}
	}
	if matched == nil || matched.BundleDigest != promotion.BundleDigest || !matched.HasCharacterEvidence() {
		return false, fmt.Errorf("promoted character simulation lacks its exact sealed character evidence")
	}
	want, err := domain.DeterministicPlanningHash(matched.ChapterWorldSimulation)
	if err != nil {
		return false, err
	}
	got, err := domain.DeterministicPlanningHash(simulation)
	if err != nil || got != want {
		return false, fmt.Errorf("live character simulation differs from its currently promoted sealed bundle")
	}
	after, err := projected.LoadRealizationCursor()
	if err != nil {
		return false, err
	}
	if after == nil || after.CursorDigest != cursor.CursorDigest {
		return false, fmt.Errorf("promotion cursor changed while verifying character simulation")
	}
	return true, nil
}
