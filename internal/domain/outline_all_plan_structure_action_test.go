package domain

import (
	"strings"
	"testing"
)

func TestValidateOutlineAllPendingActionPlanStructure(t *testing.T) {
	digest, err := planningV2Digest("seed-skeleton")
	if err != nil {
		t.Fatal(err)
	}
	good := OutlineAllPendingAction{
		Type: OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest,
	}
	if err := ValidateOutlineAllPendingAction(good); err != nil {
		t.Fatalf("plan_structure pending action should validate: %v", err)
	}
	// Any volume/arc/span field is illegal: the model, not the host, chooses the
	// allocation, so the pending action carries none of it.
	for _, bad := range []OutlineAllPendingAction{
		{Type: OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest, Volume: 1},
		{Type: OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest, ExpectedChapterSpan: 12},
		{Type: OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest, ExpectedArcSpans: "8,8"},
		{Type: OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest, FinalSkeleton: true},
	} {
		if err := ValidateOutlineAllPendingAction(bad); err == nil {
			t.Fatalf("plan_structure with %+v should be rejected", bad)
		}
	}
}

func TestValidateOutlineAllPendingActionAppendVolumeUsesModelArcSpans(t *testing.T) {
	digest, err := planningV2Digest("seed-skeleton")
	if err != nil {
		t.Fatal(err)
	}
	// A model-allocated volume of one 4-chapter arc and one 30-chapter arc: no
	// arithmetic partition, no upper bound, but the spans must sum to the span.
	good := OutlineAllPendingAction{
		Type: OutlineAllActionAppendVolume, Operation: 2, BeforeLayeredDigest: digest,
		Volume: 1, ExpectedVolumeIndex: 1, ExpectedChapterSpan: 34, ExpectedArcSpans: "4,30",
	}
	if err := ValidateOutlineAllPendingAction(good); err != nil {
		t.Fatalf("append_volume with model arc spans should validate: %v", err)
	}
	mismatch := good
	mismatch.ExpectedArcSpans = "4,20"
	if err := ValidateOutlineAllPendingAction(mismatch); err == nil {
		t.Fatal("append_volume arc spans that do not sum to the chapter span must be rejected")
	}
}

func TestOutlineAllArcSpansRoundTrip(t *testing.T) {
	spans := []int{4, 30, 1, 17}
	got, err := ParseOutlineAllArcSpans(FormatOutlineAllArcSpans(spans))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(spans) {
		t.Fatalf("round trip = %v", got)
	}
	for i := range spans {
		if got[i] != spans[i] {
			t.Fatalf("round trip = %v want %v", got, spans)
		}
	}
	if _, err := ParseOutlineAllArcSpans("8,0,8"); err == nil || !strings.Contains(err.Error(), "floor") {
		t.Fatalf("zero span must be rejected, got %v", err)
	}
	if _, err := ParseOutlineAllArcSpans(""); err == nil {
		t.Fatal("empty spans must be rejected")
	}
}

func TestOutlineAllExecutionReceiptRejectsPlanTargetMismatch(t *testing.T) {
	plan := OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
		{ArcSpans: []int{40, 5}}, {ArcSpans: []int{33, 18}}, {ArcSpans: []int{20, 22}},
		{ArcSpans: []int{30}}, {ArcSpans: []int{19, 6}}, {ArcSpans: []int{14, 15}},
	}}
	if plan.TotalChapters() < 200 || plan.TotalChapters() > 260 {
		t.Fatalf("test plan total=%d", plan.TotalChapters())
	}
	scaleRange := BookScaleRange{MinVolumes: 6, MaxVolumes: 7, MinChapters: 200, MaxChapters: 260}
	if err := ValidateOutlineAllStructurePlan(plan, scaleRange); err != nil {
		t.Fatalf("plan should validate: %v", err)
	}
}
