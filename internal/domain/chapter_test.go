package domain

import "testing"

func TestRequiresFinalGlobalReviewAllowsExplicitLayeredShortOnly(t *testing.T) {
	progress := &Progress{Layered: true, TotalWordCount: 29000}
	if !RequiresFinalGlobalReview(progress, &RunMeta{PlanningTier: PlanningTierShort}) {
		t.Fatal("an explicit short book must receive a global review even when one arc is stored as layered")
	}
	for _, tier := range []PlanningTier{"", PlanningTierMid, PlanningTierLong} {
		if RequiresFinalGlobalReview(progress, &RunMeta{PlanningTier: tier}) {
			t.Fatalf("layered project with planning tier %q was misclassified as a short book", tier)
		}
	}
	progress.Layered = false
	if !RequiresFinalGlobalReview(progress, nil) {
		t.Fatal("legacy non-layered sub-30k project should retain the word-count fallback")
	}
}
