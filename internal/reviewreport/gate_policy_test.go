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

func TestRendererReadabilityViolationsAlwaysBlockUnifiedGate(t *testing.T) {
	for _, rule := range []string{
		"abstract_system_reassurance",
		"system_message_inline",
		"opaque_procedure_jargon",
		"dialogue_action_lead_repetition",
	} {
		payload := &MechanicalGatePayload{RuleViolations: []rules.Violation{{Rule: rule, Severity: rules.SeverityWarning}}}
		if !HasBlockingMechanicalGate(payload) {
			t.Fatalf("%s should remain blocking even at warning severity", rule)
		}
		if AcceptedWarningOnlyGate(payload, nil, &domain.ReviewEntry{Verdict: "accept", ContractStatus: "met"}) {
			t.Fatalf("%s must not downgrade to optional after editor accept", rule)
		}
	}
}

func TestAcceptedWarningOnlyGateDowngradesToOptional(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        3.8, // 干净短章：raw 与 blended 一致，EffectiveGatePercent <4 才允许仅告警通过
			BlendedAIGCPercent: 3.8,
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
	for _, want := range []string{"## 是否需要改写：可选", "机械门禁：有警告", "仅剩风格 warning"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "可继续下一章") {
		t.Fatalf("accepted warning-only gate should allow the pipeline to continue:\n%s", md)
	}
	if strings.Contains(md, "进入返工队列") {
		t.Fatalf("accepted warning-only gate should not force rewrite:\n%s", md)
	}
}

func TestAcceptedCleanChapterTreatsFlatObjectRhythmAsAdvisory(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        3.8,
			BlendedAIGCPercent: 3.8,
		},
		RuleViolations: []rules.Violation{{
			Rule:     "object_response_rhythm_flat",
			Severity: rules.SeverityWarning,
			Actual:   4,
			Limit:    "物件回应至少一次延迟、一次缺席",
		}},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Dimensions: []domain.DimensionScore{{
			Dimension: "aesthetic",
			Score:     88,
			Verdict:   "pass",
		}},
	}

	if IsBlockingMechanicalViolation(payload.RuleViolations[0]) {
		t.Fatal("warning-only flat object rhythm should remain advisory")
	}
	if !AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("expected low-AIGC editor accept with advisory rhythm warning to pass")
	}
	if got := RewriteDisposition(payload, nil, nil, editor); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestAcceptedLowAIGCChapterTreatsStyleWarningsAsAdvisory(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        3.8,
			BlendedAIGCPercent: 3.8,
		},
		RuleViolations: []rules.Violation{
			{Rule: "isolated_sentence_overuse", Severity: rules.SeverityWarning, Actual: 28},
			{Rule: "dramatic_negation_overuse", Severity: rules.SeverityWarning, Actual: 3},
		},
	}
	editor := &domain.ReviewEntry{
		Chapter:        1,
		Verdict:        "accept",
		ContractStatus: "met",
		Dimensions: []domain.DimensionScore{{
			Dimension: "ai_voice_detection",
			Score:     80,
			Verdict:   "pass",
		}},
	}

	if !AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("low-AIGC accepted chapter must not loop on style-only warnings")
	}
	if got := RewriteDisposition(payload, nil, nil, editor); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestCurrentHashExternalPassCalibratesStrongNarrativeLocalDiagnostic(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n林澈回到青山县，饭桌上的话还没说完。")
	payload := &MechanicalGatePayload{
		Chapter:    1,
		BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent:            11.19,
			AIRatioPercent:         11.19,
			BlendedAIGCPercent:     11.19,
			ZhuqueCompositePercent: 3.36,
			LegacyHeuristicPercent: 4.30,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_overuse", Severity: rules.SeverityWarning, Actual: 8},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 8},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 11.19},
		},
	}
	external := &ExternalAIJudge{
		Name: "DeepSeek 裸正文", BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}
	editor := &domain.ReviewEntry{
		Chapter: 1, Verdict: "accept", ContractStatus: "met",
		Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: 90, Verdict: "pass"}},
	}

	if !ApplyExternalCorroboration(payload, external) {
		t.Fatal("same-hash external pass should calibrate a strong narrative local diagnostic")
	}
	if got := aigc.EffectiveGatePercent(payload.AIGCReport); got != 3.36 {
		t.Fatalf("calibrated gate = %.2f, want 3.36", got)
	}
	if got := RewriteDisposition(payload, nil, external, editor); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{Chapter: 1, Mechanical: payload, ExternalAIJudge: external, Editor: editor})
	for _, want := range []string{"机械门禁：有警告", "同哈希外判校准：已采用", "本地原始值保留为诊断", "无需返工"} {
		if !strings.Contains(md, want) {
			t.Fatalf("calibrated report missing %q:\n%s", want, md)
		}
	}
}

func TestCurrentHashExternalPassDemotesHighAggregateProxiesWhenSegmentsPass(t *testing.T) {
	bodyHash := BodySHA256("第三章\n\n人群堵在桥头，林澈先让开取餐口，沈知遥守住另一边。")
	payload := &MechanicalGatePayload{
		Chapter: 3, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 9.18, ZhuqueCompositePercent: 8.39, SegmentRiskFloor: 2.7,
			LegacyHeuristicPercent: 9.18, WholeTextSegmentGate: 2.7,
			Stats: aigc.Stats{Hanzi: 2200, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 9.18}},
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}
	if !ApplyExternalCorroboration(payload, external) {
		t.Fatal("safe current-hash external and segment evidence should calibrate aggregate proxies")
	}
	if got := aigc.EffectiveGatePercent(payload.AIGCReport); got != 2.7 {
		t.Fatalf("calibrated gate = %.2f, want 2.70", got)
	}
}

func TestExternalPassCannotOverrideDeterministicReadabilityFailure(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n【系统提示】他接着说话。")
	payload := &MechanicalGatePayload{
		Chapter:    1,
		BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 11.19, ZhuqueCompositePercent: 3.0, LegacyHeuristicPercent: 4.3,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "system_message_inline", Severity: rules.SeverityWarning}},
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}

	if ApplyExternalCorroboration(payload, external) {
		t.Fatal("deterministic readability failure must block external corroboration")
	}
	if len(payload.CorroborationBlockedBy) != 1 || payload.CorroborationBlockedBy[0] != "system_message_inline" {
		t.Fatalf("corroboration blockers = %#v", payload.CorroborationBlockedBy)
	}
}

func TestEditorAcceptWithOpenIssuesIsNotCompletePass(t *testing.T) {
	payload := &MechanicalGatePayload{
		Chapter: 1,
		AIGCReport: aigc.Report{
			AIGCPercent:        3.8,
			AIRatioPercent:     3.8,
			BlendedAIGCPercent: 3.8,
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
			AIGCPercent:        3.8,
			AIRatioPercent:     3.8,
			BlendedAIGCPercent: 3.8,
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
			AIGCPercent:        3.8,
			AIRatioPercent:     3.8,
			BlendedAIGCPercent: 3.8,
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
			BlendedAIGCPercent: 3.8,
			Stats:              aigc.Stats{Hanzi: 3000},
			SegmentRiskFloor:   80,
		},
	}

	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter:    1,
		Mechanical: payload,
		Editor:     &domain.ReviewEntry{Chapter: 1, Verdict: "accept", Summary: "编辑通过"},
	})
	for _, want := range []string{"AI 占比：80.00%", "门禁采用值：80.00%", "融合值：3.80%", "朱雀分片风险下限：80.00%"} {
		if !strings.Contains(md, want) {
			t.Fatalf("unified markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBlockingAIGCDimensionsAreSuppressedByHumanAnchorCap(t *testing.T) {
	capValue := 3.8
	reasons := BlockingAIGCDimensionReasons(aigc.Report{
		AIGCPercent:         80,
		BlendedAIGCPercent:  3.8,
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
			AIGCPercent:        3.8,
			AIRatioPercent:     3.8,
			BlendedAIGCPercent: 3.8,
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

func TestUnifiedMarkdownDoesNotResurrectAcceptedEditorIssuesBecauseOfMechanicalWarnings(t *testing.T) {
	md := RenderUnifiedMarkdown(UnifiedMarkdownInput{
		Chapter: 1,
		Mechanical: &MechanicalGatePayload{
			Chapter:    1,
			AIGCReport: aigc.Report{AIGCPercent: 3.8, BlendedAIGCPercent: 3.8},
			RuleViolations: []rules.Violation{{
				Rule: "isolated_sentence_overuse", Severity: rules.SeverityWarning, Actual: 28,
			}},
		},
		Editor: &domain.ReviewEntry{
			Chapter: 1, Verdict: "accept", ContractStatus: "met",
			Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: 90, Verdict: "pass"}},
			Issues: []domain.ConsistencyIssue{{
				Type: "aesthetic", Severity: "warning", Description: "已裁定为可选的建议。",
			}},
		},
		EditorMarkdown: "# ch01 评审\n\n## 主要问题（按严重度排序）\n1. 不应复活的原始假问题。\n\n## 结论\n通过。",
	})

	if strings.Contains(md, "Editor 原始报告摘录") || strings.Contains(md, "不应复活") {
		t.Fatalf("accepted editor advice must stay out of the unified report:\n%s", md)
	}
	if !strings.Contains(md, "机械门禁 warning｜isolated_sentence_overuse") {
		t.Fatalf("mechanical observation should remain visible:\n%s", md)
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
