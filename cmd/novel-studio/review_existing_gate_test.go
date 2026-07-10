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

func TestParseReviewIssuesSkipsNonActionablePraiseAndOptionalAdvice(t *testing.T) {
	md := `# ch01 评审

## 主要问题（按严重度排序）
1. **无严重问题。** 本章各项 red flag 检测的"警告"实为优秀写作的表现。
2. **次要优化建议（审美，非必要）：** 某句可以更含混，但非必需。

## 结论
通过，不建议改写。`

	if issues := parseReviewIssues(md); len(issues) != 0 {
		t.Fatalf("expected non-actionable lines to be skipped, got %+v", issues)
	}
}

func TestParseReviewIssuesKeepsActionableIssue(t *testing.T) {
	md := `# ch01 评审

## 主要问题（按严重度排序）
1. 第12段总结腔仍需改成动作后果。

## 结论
建议打磨。`

	issues := parseReviewIssues(md)
	if len(issues) != 1 {
		t.Fatalf("expected one actionable issue, got %+v", issues)
	}
	if issues[0].Description != "第12段总结腔仍需改成动作后果。" {
		t.Fatalf("unexpected issue: %+v", issues[0])
	}
}
