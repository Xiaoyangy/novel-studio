package reviewreport

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

func TestBlockingMechanicalWarningsAndHighRiskDimensions(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{Dimensions: map[string]aigc.Dimension{
			"structure_fingerprint": {
				Name:  "结构指纹",
				Score: 42,
				Signals: []aigc.Signal{{
					Name:     "paragraph_start_repeat",
					Evidence: "段首重复偏多",
				}},
			},
		}},
		RuleViolations: []rules.Violation{{
			Rule:     "isolated_sentence_overuse",
			Severity: rules.SeverityWarning,
			Actual:   35,
			Limit:    "4",
		}},
	}

	if !HasBlockingMechanicalGate(payload) {
		t.Fatal("expected mechanical warning/high-risk dimension to block")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"## 是否需要改写：是", "机械门禁：未通过", "AI味/节奏机械门禁未通过", "机械门禁 error｜高风险维度 42.00%｜结构指纹"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "## 改写建议") {
		t.Fatalf("unified markdown should not emit rewrite-suggestion section:\n%s", md)
	}
}

func TestAcceptedWarningOnlyGateDowngradesToOptional(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        4.8, // 干净短章：raw 与 blended 一致，EffectiveGatePercent ≤5 才允许仅告警通过
			BlendedAIGCPercent: 4.8,
			Dimensions: map[string]aigc.Dimension{
				"perplexity_proxy": {
					Name:  "困惑度代理",
					Score: 18,
				},
			},
		},
		RuleViolations: []rules.Violation{{
			Rule:     "fatigue_words",
			Severity: rules.SeverityWarning,
			Actual:   2,
			Limit:    1,
		}},
	}
	voice := &domain.AIVoiceAnalysis{RedFlags: []domain.AIVoiceRedFlag{{
		Rule:     "supporting_dialogue_ratio",
		Severity: "warning",
	}}}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Issues: []domain.ConsistencyIssue{{
			Severity: "warning",
			Type:     "aesthetic",
		}},
		Dimensions: []domain.DimensionScore{{
			Dimension: "hook",
			Score:     100,
			Verdict:   "pass",
		}},
		Summary: "编辑通过，仅建议打磨",
	}
	if !AcceptedWarningOnlyGate(payload, voice, editor) {
		t.Fatal("expected accepted warning-only gate")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		AIVoice:    voice,
		Editor:     editor,
	})
	for _, want := range []string{"## 是否需要改写：可选", "机械门禁：有警告", "仅剩 warning"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "本章可进入下一章") {
		t.Fatalf("accepted warning-only gate must not be displayed as a complete pass:\n%s", md)
	}
	if strings.Contains(md, "进入返工队列") {
		t.Fatalf("accepted warning-only gate should not force rewrite:\n%s", md)
	}
}

func TestEditorAcceptWithOpenIssuesIsNotCompletePass(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        4.8,
			AIRatioPercent:     4.8,
			BlendedAIGCPercent: 4.8,
		},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Summary:        "编辑认为结构可用",
		Issues: []domain.ConsistencyIssue{{
			Type:        "aesthetic",
			Severity:    "error",
			Description: "第42段总结腔仍需改成动作后果",
		}},
	}

	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     editor,
	})
	for _, want := range []string{"## 是否需要改写：可选", "Editor 复审：通过（有主要问题）", "主要问题仍未清空"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "主要问题已清空") {
		t.Fatalf("open editor issues must not be displayed as cleared:\n%s", md)
	}
}

func TestEditorAcceptWithWarningIssuesIsCompletePass(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        4.8,
			AIRatioPercent:     4.8,
			BlendedAIGCPercent: 4.8,
		},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Summary:        "编辑认为结构可用，仅有后续建议",
		Issues: []domain.ConsistencyIssue{{
			Type:        "aesthetic",
			Severity:    "warning",
			Description: "后续可增加更私人、更具体的利益捆绑",
		}},
	}

	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     editor,
	})
	for _, want := range []string{"## 是否需要改写：否", "Editor 复审：通过", "主要问题已清空"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "后续可增加") || strings.Contains(md, "通过（有主要问题）") {
		t.Fatalf("accepted warning-only editor advice must not be displayed as main issues:\n%s", md)
	}
}

func TestBlockingExternalAIJudgeOverridesAcceptedReview(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        4.8,
			AIRatioPercent:     4.8,
			BlendedAIGCPercent: 4.8,
		},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Summary:        "编辑通过",
	}

	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     editor,
		ExternalAIJudge: &ExternalAIJudge{
			Name:                 "DeepSeek 裸正文",
			Verdict:              "mixed",
			RiskLevel:            "medium",
			AIProbabilityPercent: 40,
			Blocking:             true,
			Summary:              "角色互动过于同步，疑似 AI 润色。",
		},
	})
	for _, want := range []string{"## 是否需要改写：是", "DeepSeek 裸正文：阻断 mixed/medium/40%", "DeepSeek 裸正文 阻断重写", "DeepSeek 裸正文 阻断｜mixed/medium/40%"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "主要问题已清空，可进入下一章") || strings.Contains(md, "## 是否需要改写：否") {
		t.Fatalf("blocking external judge must not be displayed as complete pass:\n%s", md)
	}
}

func TestMechanicalGateRendersEffectiveGatePercent(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			Engine:             aigc.Engine,
			AIGCPercent:        80,
			AIRatioPercent:     80,
			BlendedAIGCPercent: 4.8,
			Stats:              aigc.Stats{Hanzi: 3000},
			SegmentRiskFloor:   80,
		},
	}

	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"AI 占比：80.00%", "门禁采用值：80.00%", "融合值：4.80%", "朱雀分片风险下限：80.00%"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBlockingAIGCDimensionsAreSuppressedByHumanAnchorCap(t *testing.T) {
	capValue := 4.8
	reasons := BlockingAIGCDimensionReasons(aigc.Report{
		AIGCPercent:         80,
		BlendedAIGCPercent:  4.8,
		SegmentRiskFloor:    80,
		HumanAnchorFinalCap: &capValue,
		Dimensions: map[string]aigc.Dimension{
			"perplexity_proxy": {
				Name:  "困惑度代理",
				Score: 48,
			},
		},
	})
	if len(reasons) != 0 {
		t.Fatalf("human-anchor capped report should not have blocking dimension reasons, got %+v", reasons)
	}
}

func TestChapterWordsBlockingIsNotReportedAsAIVoiceFailure(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		RuleViolations: []rules.Violation{{
			Rule:      "chapter_words",
			Severity:  rules.SeverityError,
			Actual:    4114,
			Limit:     "2800-3400",
			Deviation: 0.21,
		}},
	}

	if !HasBlockingMechanicalGate(payload) {
		t.Fatal("expected chapter_words error to block")
	}
	if HasBlockingAIMechanicalGate(payload) {
		t.Fatal("chapter_words should not be reported as an AI/voice mechanical block")
	}
	if !HasBlockingContractMechanicalGate(payload) {
		t.Fatal("expected chapter_words to be reported as a contract block")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"## 是否需要改写：是", "篇幅/硬性规则机械门禁未通过", "这不是 AI 味结论"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "AI味/节奏机械门禁未通过") {
		t.Fatalf("chapter_words should not use AI/voice failure copy:\n%s", md)
	}
}

func TestCombinedAIMechanicalAndHardRuleConclusion(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		RuleViolations: []rules.Violation{
			{Rule: "micro_action_overuse", Severity: rules.SeverityWarning, Actual: 16},
			{Rule: "deprecated_story_engine", Target: "日志窗口", Severity: rules.SeverityError, Actual: 1},
		},
	}

	if !HasBlockingAIMechanicalGate(payload) || !HasBlockingContractMechanicalGate(payload) {
		t.Fatalf("expected both AI mechanical and hard rule gates to block")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"AI味/节奏机械门禁与篇幅/硬性规则均未通过", "项目硬禁项"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBlockingAIVoiceWarning(t *testing.T) {
	analysis := &domain.AIVoiceAnalysis{RedFlags: []domain.AIVoiceRedFlag{{
		Rule:     "supporting_dialogue_ratio",
		Severity: "warning",
		Evidence: "对话占比偏低",
	}}}
	if !HasBlockingAIVoice(analysis) {
		t.Fatal("expected AI voice warning to block")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter: 1,
		AIVoice: analysis,
		Editor:  &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"## 是否需要改写：是", "AI味信号未通过", "AI味红/黄旗存在阻断项"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
}

func TestNearThresholdAIVoiceWarningDoesNotBlockAcceptedReview(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        4.8,
			AIRatioPercent:     4.8,
			BlendedAIGCPercent: 4.8,
		},
	}
	analysis := &domain.AIVoiceAnalysis{
		Metrics: domain.ChapterAIVoiceMetrics{
			DialogueRatio: 0.297,
			SentenceCount: 129,
		},
		RedFlags: []domain.AIVoiceRedFlag{{
			Rule:     "supporting_dialogue_ratio",
			Severity: "warning",
			Actual:   0.297,
			Limit:    0.30,
		}},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Summary:        "AI 腔 red flag 各项检查结果均为安全。",
		Dimensions: []domain.DimensionScore{{
			Dimension: "ai_voice_detection",
			Score:     100,
			Verdict:   "pass",
		}},
	}

	if HasBlockingAIVoice(analysis) {
		t.Fatal("near-threshold supporting dialogue warning should not block")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		AIVoice:    analysis,
		Editor:     editor,
	})
	for _, want := range []string{"## 是否需要改写：否", "Editor 复审：通过", "主要问题已清空"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "AI warning｜supporting_dialogue_ratio") || strings.Contains(md, "主要问题仍未清空") {
		t.Fatalf("near-threshold warning should not be rendered as a main issue:\n%s", md)
	}
}

func TestUnifiedMarkdownStripsEditorRewriteSuggestions(t *testing.T) {
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:        1,
		Editor:         &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
		EditorMarkdown: "# ch01 评审\n\n## 主要问题（按严重度排序）\n1. 钩子偏弱。\n\n## 改写建议（如需要）\n- 改成另一句。\n\n## 结论\n通过。",
	})
	if strings.Contains(md, "## 改写建议") || strings.Contains(md, "改成另一句") {
		t.Fatalf("unified markdown should strip editor rewrite suggestions:\n%s", md)
	}
	if strings.Contains(md, "钩子偏弱") || strings.Contains(md, "## 主要问题（按严重度排序）\n1.") {
		t.Fatalf("unified markdown should strip raw editor main-issue section when unified issues are empty:\n%s", md)
	}
	if !strings.Contains(md, "## 结论") {
		t.Fatalf("unified markdown stripped too much:\n%s", md)
	}
}

func TestBlockingPunctuationMechanicalWarnings(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		RuleViolations: []rules.Violation{{
			Rule:     "form_notice_semicolon_chain",
			Severity: rules.SeverityWarning,
			Actual:   4,
			Limit:    "条款/单据优先换行分项",
		}},
	}
	if !HasBlockingMechanicalGate(payload) {
		t.Fatal("expected punctuation warning to block rewrite")
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	if !strings.Contains(md, "## 是否需要改写：是") {
		t.Fatalf("expected unified markdown to require rewrite:\n%s", md)
	}
}
