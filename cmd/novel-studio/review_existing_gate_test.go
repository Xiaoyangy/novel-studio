package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
)

func TestReviewExistingAIGCGatePercentUsesRawForShortChapter(t *testing.T) {
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 3000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
	}

	if got := reviewExistingAIGCGatePercent(report); got != 80 {
		t.Fatalf("short chapter gate percent = %.2f, want raw 80.00", got)
	}
}

func TestReviewExistingAIGCGatePercentUsesHumanAnchorCap(t *testing.T) {
	capValue := 4.8
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 3000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
		HumanAnchorFinalCap:    &capValue,
	}

	if got := reviewExistingAIGCGatePercent(report); got != 4.8 {
		t.Fatalf("human-anchor capped chapter gate percent = %.2f, want 4.80", got)
	}
}

func TestReviewExistingAIGCGatePercentAllowsBlendedForLongChapter(t *testing.T) {
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 6000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
	}

	if got := reviewExistingAIGCGatePercent(report); got != 4.8 {
		t.Fatalf("long chapter gate percent = %.2f, want blended 4.80", got)
	}
}
