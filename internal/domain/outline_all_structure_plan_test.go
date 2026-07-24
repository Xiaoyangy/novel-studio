package domain

import (
	"strings"
	"testing"
)

func TestOutlineAllStructurePlanTotals(t *testing.T) {
	plan := OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
		{ArcSpans: []int{12, 20}},
		{ArcSpans: []int{9}},
		{ArcSpans: []int{30, 7, 15}},
	}}
	if got := plan.TotalVolumes(); got != 3 {
		t.Fatalf("TotalVolumes=%d want 3", got)
	}
	if got := plan.TotalChapters(); got != 12+20+9+30+7+15 {
		t.Fatalf("TotalChapters=%d", got)
	}
	if got := plan.ArcSpansForVolume(3); len(got) != 3 || got[0] != 30 || got[2] != 15 {
		t.Fatalf("ArcSpansForVolume(3)=%v", got)
	}
	if got := plan.ArcSpansForVolume(0); got != nil {
		t.Fatalf("ArcSpansForVolume(0)=%v want nil", got)
	}
	if got := plan.ArcSpansForVolume(4); got != nil {
		t.Fatalf("ArcSpansForVolume(4)=%v want nil", got)
	}
	// Returned slice must be a copy, never the plan's backing array.
	got := plan.ArcSpansForVolume(1)
	got[0] = 999
	if plan.Volumes[0].ArcSpans[0] != 12 {
		t.Fatalf("ArcSpansForVolume aliased the plan: %v", plan.Volumes[0].ArcSpans)
	}
}

func TestValidateOutlineAllStructurePlanAcceptsModelAllocationWithNoCeiling(t *testing.T) {
	scale := BookScaleRange{MinVolumes: 6, MaxVolumes: 7, MinChapters: 200, MaxChapters: 260}
	// A 40-chapter arc and a 5-chapter arc coexist: no upper bound, model-chosen.
	plan := OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
		{ArcSpans: []int{40, 5}},
		{ArcSpans: []int{33, 18}},
		{ArcSpans: []int{20, 22}},
		{ArcSpans: []int{30}},
		{ArcSpans: []int{19, 6}},
		{ArcSpans: []int{14, 15}},
	}}
	if total := plan.TotalChapters(); total < 200 || total > 260 {
		t.Fatalf("test plan total=%d not in range", total)
	}
	if err := ValidateOutlineAllStructurePlan(plan, scale); err != nil {
		t.Fatalf("ValidateOutlineAllStructurePlan: %v", err)
	}
}

func TestValidateOutlineAllStructurePlanRejectsOutOfRangeAndInvalidSpans(t *testing.T) {
	scale := BookScaleRange{MinVolumes: 6, MaxVolumes: 7, MinChapters: 200, MaxChapters: 260}
	for name, tc := range map[string]struct {
		plan OutlineAllStructurePlan
		want string
	}{
		"too_few_volumes": {
			plan: OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{{ArcSpans: []int{200}}}},
			want: "volume count 1 is outside",
		},
		"chapters_below_range": {
			plan: OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
				{ArcSpans: []int{10}}, {ArcSpans: []int{10}}, {ArcSpans: []int{10}},
				{ArcSpans: []int{10}}, {ArcSpans: []int{10}}, {ArcSpans: []int{10}},
			}},
			want: "chapter total 60 is outside",
		},
		"zero_span_arc": {
			plan: OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
				{ArcSpans: []int{40, 0}}, {ArcSpans: []int{33, 18}}, {ArcSpans: []int{20, 22}},
				{ArcSpans: []int{30}}, {ArcSpans: []int{19, 6}}, {ArcSpans: []int{14, 15}},
			}},
			want: "below the 1-chapter floor",
		},
		"empty_volume": {
			plan: OutlineAllStructurePlan{Volumes: []OutlineAllVolumePlan{
				{ArcSpans: []int{}}, {ArcSpans: []int{33}}, {ArcSpans: []int{20}},
				{ArcSpans: []int{30}}, {ArcSpans: []int{19}}, {ArcSpans: []int{14}},
			}},
			want: "requires at least one arc",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateOutlineAllStructurePlan(tc.plan, scale)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
