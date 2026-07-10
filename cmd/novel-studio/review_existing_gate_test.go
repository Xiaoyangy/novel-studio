package main

import (
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
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

func TestCallEditorOnChapterIncludesProjectRules(t *testing.T) {
	model := &deepseekJudgeCaptureModel{}
	rules := `{"preferences":"系统会接话吐槽并始终支持林澈，不能写成纯任务机器人。"}`
	plan := `{"trend_language_plan":[{"item":"呱，","character_carrier":"赵航本人"}]}`
	if _, err := callEditorOnChapter(model, "故事前提", rules, plan, 1, "第一章\n\n林澈看向手机。", domain.AIVoiceAnalysis{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(model.messages))
	}
	if got := model.messages[1].TextContent(); !strings.Contains(got, "项目用户规则（最高优先级）") || !strings.Contains(got, "系统会接话吐槽") {
		t.Fatalf("project rules missing from editor input: %s", got)
	}
	if got := model.messages[1].TextContent(); !strings.Contains(got, "本章已批准写前 plan") || !strings.Contains(got, "赵航本人") {
		t.Fatalf("approved chapter plan missing from editor input: %s", got)
	}
	if got := model.messages[0].TextContent(); !strings.Contains(got, "禁止建议把系统改成冷硬") {
		t.Fatalf("editor system guard missing: %s", got)
	}
}

func TestSanitizeEditorReviewDropsClaimsContradictedByBodyAndApprovedPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{
				Item: "呱，", CharacterCarrier: "赵航本人；由他在饭桌说出口。",
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := `赵航把碗一放：“呱，这也叫关心？”

【真实改善核验完成。】

【解锁小额改善额度五万元。】

【这一笔，算你自己挣的。^_^】

付款页面已经打开，林澈的拇指却迟迟没落下。`
	entry := domain.ReviewEntry{
		Chapter: 1,
		Summary: "主角动摇缺失，缺少一次真实迟疑。",
		Dimensions: []domain.DimensionScore{{
			Dimension: "ai_voice_detection", Score: 80, Verdict: "pass",
			Comment: "命中 protagonist_waver_missing。",
		}},
		Issues: []domain.ConsistencyIssue{
			{Severity: "warning", Description: "主角动摇缺失，缺少一次真实迟疑。"},
			{Severity: "warning", Description: "系统消息一次塞入过多功能，同时承担奖励和任务。"},
			{Severity: "warning", Description: "热梗‘呱’落地方位错误，放在赵航这个配角口中会削弱记忆点。"},
			{Severity: "warning", Description: "章末颜文字位置风险，容易被当成正式系统条款。"},
			{Severity: "warning", Description: "老丁的一句报价仍可再口语化。"},
		},
	}
	analysis := domain.AIVoiceAnalysis{Metrics: domain.ChapterAIVoiceMetrics{ProtagonistWaver: true}}
	removed := sanitizeEditorReviewForProject(s, 1, body, analysis, &entry)
	if len(removed) != 4 {
		t.Fatalf("removed=%v, want four deterministic contradictions", removed)
	}
	if len(entry.Issues) != 1 || !strings.Contains(entry.Issues[0].Description, "老丁") {
		t.Fatalf("unexpected surviving issues: %+v", entry.Issues)
	}
	if strings.Contains(entry.Summary, "动摇缺失") || strings.Contains(entry.Dimensions[0].Comment, "waver_missing") {
		t.Fatalf("stale contradicted diagnosis survived: %+v", entry)
	}
}

func TestReviewVerdictAllPassingDimensionsDoNotForcePolish(t *testing.T) {
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dimensions = append(dimensions, domain.DimensionScore{
			Dimension: name,
			Score:     80,
			Verdict:   "pass",
		})
	}
	md := "## 是否需要改写：是"
	if got := reviewVerdictFromMarkdown(md, dimensions); got != "accept" {
		t.Fatalf("all-pass review verdict = %q, want accept", got)
	}
	dimensions[2].Score = 70
	dimensions[2].Verdict = "warning"
	if got := reviewVerdictFromMarkdown(md, dimensions); got != "polish" {
		t.Fatalf("warning-dimension review verdict = %q, want polish", got)
	}
}
