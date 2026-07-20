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
	voice := &domain.AIVoiceAnalysis{
		Metrics:  domain.ChapterAIVoiceMetrics{SentenceCount: 40, DialogueRatio: 0.299},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "supporting_dialogue_ratio", Severity: "warning", Actual: 0.299, Limit: 0.30}},
	}
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

func TestAcceptedCleanChapterBlocksFlatObjectRhythmUntilRewritten(t *testing.T) {
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
		t.Fatal("flat object rhythm is a structural blocker, not a deterministic mechanical error")
	}
	if !IsStructuralProseViolation(payload.RuleViolations[0]) {
		t.Fatal("flat object rhythm must be classified as a structural prose violation")
	}
	if AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("editor accept and low AIGC must not excuse a flat object-response chain")
	}
	if got := RewriteDisposition(payload, nil, nil, editor); got != "是" {
		t.Fatalf("RewriteDisposition = %q, want 是", got)
	}
}

func TestDialogueMicroPeriodChainIsStructuralAndBlocksExternalCalibration(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n沈知遥说：断电，先把线断了。")
	violation := rules.Violation{
		Rule:     "dialogue_micro_period_chain",
		Severity: rules.SeverityWarning,
		Actual:   3,
		Limit:    "同章不同话轮中二至四字句号短句 < 3",
	}
	payload := &MechanicalGatePayload{
		Chapter: 1, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 2.3, BlendedAIGCPercent: 2.3,
			Stats: aigc.Stats{Hanzi: 2300, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(96), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{violation},
	}
	editor := &domain.ReviewEntry{Chapter: 1, BodySHA256: bodyHash, Verdict: "accept", ContractStatus: "met"}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if IsBlockingMechanicalViolation(violation) {
		t.Fatal("micro-period cadence is a structural warning, not a deterministic mechanical error")
	}
	if !IsStructuralProseViolation(violation) {
		t.Fatal("dialogue_micro_period_chain must be classified as a structural prose warning")
	}
	if !HasBlockingMechanicalGate(payload) || !HasBlockingAIMechanicalGate(payload) {
		t.Fatal("uncleared micro-period dialogue must block both mechanical and AI-mechanical gates")
	}
	if AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("an accepting Editor without exact clearance evidence must not demote this warning")
	}
	if ApplyExternalCorroboration(payload, external) {
		t.Fatal("a low external score must not calibrate away the structural dialogue warning")
	}
	if !containsString(payload.CorroborationBlockedBy, "dialogue_micro_period_chain") {
		t.Fatalf("external corroboration audit missing micro-period blocker: %#v", payload.CorroborationBlockedBy)
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

func TestAcceptedWarningOnlyGateAllowsExplicitlyClearedStructuralWarning(t *testing.T) {
	bodyHash := BodySHA256("第三章\n\n林澈把手机塞回口袋，先看了一眼桥口的人群。")
	payload := &MechanicalGatePayload{
		Chapter: 3, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent:        2.57,
			BlendedAIGCPercent: 2.57,
		},
		RuleViolations: []rules.Violation{{
			Rule:     "dialogue_conveyor_overuse",
			Severity: rules.SeverityWarning,
			Actual:   1,
		}},
	}
	editor := &domain.ReviewEntry{
		Chapter:        3,
		BodySHA256:     bodyHash,
		Verdict:        "accept",
		ContractStatus: "met",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass", Comment: "合同一致。"},
			{Dimension: "character", Score: 100, Verdict: "pass", Comment: "人物一致。"},
			{Dimension: "pacing", Score: 100, Verdict: "pass", Comment: "节奏通过。"},
			{Dimension: "continuity", Score: 100, Verdict: "pass", Comment: "连续性通过。"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass", Comment: "伏笔通过。"},
			{Dimension: "hook", Score: 100, Verdict: "pass", Comment: "钩子通过。"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass", Comment: "审美通过。"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "dialogue_conveyor_overuse 提示为 warning，原文已通过主视角停留有效打断，无需改写。"},
		},
	}

	if !EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, payload.RuleViolations[0]) ||
		!EditorExplicitlyClearsStructuralProseWarning(editor, payload, payload.RuleViolations[0]) {
		t.Fatal("same-comment rule name and effective-break evidence should explicitly clear this warning")
	}
	if !AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("explicitly inspected structural warning should remain advisory after Editor accept")
	}
	if got := RewriteDisposition(payload, nil, nil, editor); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
	partialClearance := *payload
	partialClearance.RuleViolations = append(append([]rules.Violation(nil), payload.RuleViolations...), rules.Violation{
		Rule: "pov_interiority_thin", Severity: rules.SeverityWarning,
	})
	if AcceptedWarningOnlyGate(&partialClearance, nil, editor) {
		t.Fatal("clearing one structural warning must not clear a second unreviewed warning")
	}

	unreviewed := *editor
	unreviewed.Dimensions = append([]domain.DimensionScore(nil), editor.Dimensions...)
	unreviewed.Dimensions[7].Comment = "dialogue_conveyor_overuse 提示为 warning，建议后续关注。"
	if EditorExplicitlyClearsStructuralProseWarning(&unreviewed, payload, payload.RuleViolations[0]) ||
		AcceptedWarningOnlyGate(payload, nil, &unreviewed) {
		t.Fatal("an unqualified warning mention must remain blocking")
	}
	conclusionOnly := *editor
	conclusionOnly.Dimensions = append([]domain.DimensionScore(nil), editor.Dimensions...)
	conclusionOnly.Dimensions[7].Comment = "dialogue_conveyor_overuse 提示为 warning，本章无需改写。"
	if EditorExplicitlySupportsStructuralProseWarningClearance(&conclusionOnly, payload, payload.RuleViolations[0]) {
		t.Fatal("a rule name plus no-rewrite conclusion without concrete prose evidence must remain blocking")
	}
	negated := *editor
	negated.Dimensions = append([]domain.DimensionScore(nil), editor.Dimensions...)
	negated.Dimensions[7].Comment = "dialogue_conveyor_overuse 提示为 warning，但现有停顿不足以有效打断，建议改写。"
	if EditorExplicitlySupportsStructuralProseWarningClearance(&negated, payload, payload.RuleViolations[0]) {
		t.Fatal("negated effective-break wording must remain blocking")
	}
	wrongHash := *editor
	wrongHash.BodySHA256 = BodySHA256("另一份正文")
	if EditorExplicitlySupportsStructuralProseWarningClearance(&wrongHash, payload, payload.RuleViolations[0]) {
		t.Fatal("Editor evidence from another body hash must never clear the warning")
	}

	errorPayload := *payload
	errorPayload.RuleViolations = append([]rules.Violation(nil), payload.RuleViolations...)
	errorPayload.RuleViolations[0].Severity = rules.SeverityError
	if EditorExplicitlyClearsStructuralProseWarning(editor, &errorPayload, errorPayload.RuleViolations[0]) ||
		AcceptedWarningOnlyGate(&errorPayload, nil, editor) {
		t.Fatal("Editor commentary must never clear an error-level structural violation")
	}
}

func TestEditorChineseAliasesCanExplicitlyClearReviewedStructuralWarnings(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n饭桌上的快接话停在父亲推开的鱼盘旁。")
	payload := &MechanicalGatePayload{
		Chapter: 1, BodySHA256: bodyHash,
		RuleViolations: []rules.Violation{
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 19},
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 3},
		},
	}
	dimensions := []domain.DimensionScore{
		{Dimension: "consistency", Score: 100, Verdict: "pass"},
		{Dimension: "character", Score: 100, Verdict: "pass"},
		{Dimension: "pacing", Score: 100, Verdict: "pass"},
		{Dimension: "continuity", Score: 90, Verdict: "pass"},
		{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
		{Dimension: "hook", Score: 100, Verdict: "pass"},
		{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
		{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "物件回应延迟命中3次、对白传送带19个重叠窗口，均为警示级别；正文已有动作与沉默作为有效打断；无需改写，只作后续章优化。"},
	}
	editor := &domain.ReviewEntry{
		Chapter: 1, BodySHA256: bodyHash, Verdict: "rewrite", ContractStatus: "met", Dimensions: dimensions,
	}
	for _, violation := range payload.RuleViolations {
		if !EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, violation) {
			t.Fatalf("Chinese exact-body clearance did not cover %s", violation.Rule)
		}
	}
	preReconciled := *editor
	preReconciled.Dimensions = append([]domain.DimensionScore(nil), dimensions...)
	preReconciled.Dimensions[7].Score = 70
	preReconciled.Dimensions[7].Verdict = "warning"
	for _, violation := range payload.RuleViolations {
		if !EditorExplicitlySupportsStructuralProseWarningClearance(&preReconciled, payload, violation) {
			t.Fatalf("explicit AI-dimension warning clearance did not cover %s before reconciliation", violation.Rule)
		}
	}
	otherDimensionWarning := preReconciled
	otherDimensionWarning.Dimensions = append([]domain.DimensionScore(nil), preReconciled.Dimensions...)
	otherDimensionWarning.Dimensions[2].Score = 70
	otherDimensionWarning.Dimensions[2].Verdict = "warning"
	if EditorExplicitlySupportsStructuralProseWarningClearance(&otherDimensionWarning, payload, payload.RuleViolations[0]) {
		t.Fatal("a warning in a non-AI dimension must not enter the narrow pre-reconciliation clearance path")
	}
	blocked := *editor
	blocked.Dimensions = append([]domain.DimensionScore(nil), dimensions...)
	blocked.Dimensions[7].Comment = "物件回应延迟与对白传送带均为警示级别，但仍需改写。"
	for _, violation := range payload.RuleViolations {
		if EditorExplicitlySupportsStructuralProseWarningClearance(&blocked, payload, violation) {
			t.Fatalf("explicit rewrite wording incorrectly cleared %s", violation.Rule)
		}
	}
}

func TestEditorRuleIDClearanceCanExplicitlyClearReviewedStructuralWarnings(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n壶面先映出画外的手，程野压住猜测，等姜岚追问后才回答。")
	payload := &MechanicalGatePayload{
		Chapter: 1, BodySHA256: bodyHash,
		RuleViolations: []rules.Violation{
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 4},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 2},
			{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning, Actual: 3},
		},
	}
	editor := &domain.ReviewEntry{
		Chapter: 1, BodySHA256: bodyHash, Verdict: "rewrite", ContractStatus: "met",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 100, Verdict: "pass", Comment: "object_response_rhythm_flat（延迟）：壶面倒影先出现画外手势，数段后再独立确认，延迟与缺席均已发生，rule ID清除。dialogue_conveyor_overuse：对白中有姜岚追问、程野应声与保留的沉默，后续未连续滚九段以上，不触发。pov_interiority_thin：这个念头压住了她报出猜测的冲动，已超过单纯情绪标签，rule ID清除。无 structural warning 阻断本章。"},
		},
	}
	for _, violation := range payload.RuleViolations {
		if !EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, violation) {
			t.Fatalf("rule-ID exact-body clearance did not cover %s", violation.Rule)
		}
	}

	missingEvidence := *editor
	missingEvidence.Dimensions = append([]domain.DimensionScore(nil), editor.Dimensions...)
	missingEvidence.Dimensions[7].Comment = "dialogue_conveyor_overuse 是 warning，rule ID清除。"
	if EditorExplicitlySupportsStructuralProseWarningClearance(&missingEvidence, payload, payload.RuleViolations[1]) {
		t.Fatal("rule-ID clearance without concrete prose evidence must remain blocking")
	}
}

func TestEditorExactBodyAIVoiceWarningClearanceIsRuleSpecific(t *testing.T) {
	bodyHash := BodySHA256("第二章\n\n程野只把四项弱信息压入待核。")
	catalogEvidence := "镜头稍稍偏过，餐盒侧面的封签露出半截分店字样"
	flags := []domain.AIVoiceRedFlag{
		{Rule: "supporting_dialogue_ratio", Severity: "warning", Actual: 0.07, Limit: 0.12},
		{Rule: "catalog_stuffing", Severity: "warning", Paragraph: 44, Evidence: catalogEvidence, Actual: 8, Limit: 7},
	}
	analysis := &domain.AIVoiceAnalysis{Chapter: 2, BodySHA256: bodyHash, Metrics: domain.ChapterAIVoiceMetrics{SentenceCount: 108}, RedFlags: flags}
	mechanical := &MechanicalGatePayload{Chapter: 2, BodySHA256: bodyHash, AIGCReport: aigc.Report{AIGCPercent: 2}}
	dimensions := []domain.DimensionScore{
		{Dimension: "consistency", Score: 100, Verdict: "pass"}, {Dimension: "character", Score: 100, Verdict: "pass"},
		{Dimension: "pacing", Score: 100, Verdict: "pass"}, {Dimension: "continuity", Score: 100, Verdict: "pass"},
		{Dimension: "foreshadow", Score: 100, Verdict: "pass"}, {Dimension: "hook", Score: 90, Verdict: "pass"},
		{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
		{Dimension: "ai_voice_detection", Score: 70, Verdict: "warning", Comment: "red flag 1：supporting_dialogue_ratio 实际 0.07，rule ID supporting_dialogue_ratio，severity warning；本章场景为程野独处停车场、仅接收姜岚转达结论，叙事限定程野视角，姜岚的转述构成有效打断，无需改写。red flag 2：catalog_stuffing 第44段「" + catalogEvidence + "」，rule ID catalog_stuffing，severity warning；该段随即接程野将四项并栏、压入待核的后续动作，不触发返工。"},
	}
	editor := &domain.ReviewEntry{Chapter: 2, BodySHA256: bodyHash, Verdict: "accept", ContractStatus: "met", Dimensions: dimensions}

	for _, flag := range flags {
		if !EditorExplicitlySupportsAIVoiceWarningClearance(editor, mechanical, analysis, flag) {
			t.Fatalf("exact-body evidence did not clear %s", flag.Rule)
		}
	}
	if !AcceptedWarningOnlyGate(mechanical, analysis, editor) {
		t.Fatal("all exact-body AI voice warnings were explicitly cleared")
	}

	generic := *editor
	generic.Dimensions = append([]domain.DimensionScore(nil), dimensions...)
	generic.Dimensions[7].Comment = "supporting_dialogue_ratio 是 severity warning，符合本章场景，无需改写。"
	if EditorExplicitlySupportsAIVoiceWarningClearance(&generic, mechanical, analysis, flags[0]) {
		t.Fatal("generic reassurance without scene evidence must remain blocking")
	}
	splitSentence := *editor
	splitSentence.Dimensions = append([]domain.DimensionScore(nil), dimensions...)
	splitSentence.Dimensions[7].Comment = "supporting_dialogue_ratio 是 severity warning。程野独处停车场，仅接收姜岚转达结论，转述构成有效打断，无需改写。"
	if EditorExplicitlySupportsAIVoiceWarningClearance(&splitSentence, mechanical, analysis, flags[0]) {
		t.Fatal("rule and evidence in different sentences must remain blocking")
	}
	suggested := *editor
	suggested.Dimensions = append([]domain.DimensionScore(nil), dimensions...)
	suggested.Dimensions[7].Comment = dimensions[7].Comment + "supporting_dialogue_ratio 建议改写。"
	if EditorExplicitlySupportsAIVoiceWarningClearance(&suggested, mechanical, analysis, flags[0]) {
		t.Fatal("a same-sentence rewrite suggestion must remain blocking")
	}
	wrongHash := *editor
	wrongHash.BodySHA256 = BodySHA256("另一份正文")
	if EditorExplicitlySupportsAIVoiceWarningClearance(&wrongHash, mechanical, analysis, flags[0]) {
		t.Fatal("another body hash must never clear the warning")
	}
	errorFlag := flags[0]
	errorFlag.Severity = "error"
	if EditorExplicitlySupportsAIVoiceWarningClearance(editor, mechanical, analysis, errorFlag) {
		t.Fatal("error-level flags must never be cleared")
	}
	errorEditor := *editor
	errorEditor.Issues = []domain.ConsistencyIssue{{Severity: "error", Description: "当前章仍有硬错"}}
	if EditorExplicitlySupportsAIVoiceWarningClearance(&errorEditor, mechanical, analysis, flags[0]) {
		t.Fatal("an Editor error must never be cleared by warning commentary")
	}
}

func TestAcceptedWarningOnlyGateAllowsOnlyCorroboratedContextualDialogueRatioError(t *testing.T) {
	bodyHash := BodySHA256("第四章\n\n程野盯着七条配送轨迹，姜岚问她漏了什么。")
	flag := domain.AIVoiceRedFlag{
		Rule: "supporting_dialogue_ratio", Severity: "error", Actual: 0.0349, Limit: 0.12,
	}
	analysis := &domain.AIVoiceAnalysis{
		Chapter: 4, BodySHA256: bodyHash,
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 4, ParagraphCount: 37, SentenceCount: 101, DialogueRatio: 0.0349},
		RedFlags: []domain.AIVoiceRedFlag{flag},
	}
	mechanical := &MechanicalGatePayload{
		Chapter: 4, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 7.2, ZhuqueCompositePercent: 0.65, LegacyHeuristicPercent: 2.75,
			Stats: aigc.Stats{Hanzi: 2300, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 7.2}},
	}
	editor := &domain.ReviewEntry{
		Chapter: 4, BodySHA256: bodyHash, Verdict: "accept", ContractStatus: "met",
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "一段自省可再压缩，不影响整体。"}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "rule ID: supporting_dialogue_ratio, actual 0.0349 < 0.12, severity: error —— 本章仅二人同室、骑手静默、许知遥受控无声，姜岚两次主动提问已构成必要冲突，无需改写，不触发返工。aigc_ratio 是本地整章概率代理，同哈希 DeepSeek 2% 后仅作诊断项，无需改写。"},
		},
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if !ApplyExternalCorroborationWithEditor(mechanical, external, editor) {
		t.Fatal("same-hash 2% external judge should calibrate the clean mechanical gate")
	}
	if !EditorExplicitlySupportsContextualDialogueRatioErrorClearance(editor, mechanical, analysis, flag) {
		t.Fatal("strict scene evidence should clear this one ratio-threshold false positive")
	}
	if !AcceptedWarningOnlyGate(mechanical, analysis, editor) {
		t.Fatal("persisted accepted gate should retain the reconciled exact-body verdict")
	}
	if got := RewriteDisposition(mechanical, analysis, external, editor); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}

	otherError := *analysis
	otherError.RedFlags = append([]domain.AIVoiceRedFlag(nil), analysis.RedFlags...)
	otherError.RedFlags = append(otherError.RedFlags, domain.AIVoiceRedFlag{Rule: "figurative_density", Severity: "error", Actual: 0.5, Limit: 0.3})
	if AcceptedWarningOnlyGate(mechanical, &otherError, editor) {
		t.Fatal("an unrelated error-level AI-voice flag must remain blocking")
	}
	failedDimension := *editor
	failedDimension.Dimensions = append([]domain.DimensionScore(nil), editor.Dimensions...)
	failedDimension.Dimensions[2].Score = 70
	failedDimension.Dimensions[2].Verdict = "warning"
	if AcceptedWarningOnlyGate(mechanical, analysis, &failedDimension) {
		t.Fatal("a non-passing Editor dimension must remain blocking")
	}
	contractMiss := *editor
	contractMiss.ContractStatus = "partial"
	if AcceptedWarningOnlyGate(mechanical, analysis, &contractMiss) {
		t.Fatal("a contract miss must remain blocking")
	}
	structuralIssue := *editor
	structuralIssue.Issues = []domain.ConsistencyIssue{{Type: "pacing", Severity: "warning", Description: "当前节奏仍需修复"}}
	if AcceptedWarningOnlyGate(mechanical, analysis, &structuralIssue) {
		t.Fatal("the exceptional path must not erase a non-aesthetic Editor issue")
	}
	uncorroborated := *mechanical
	uncorroborated.ExternalCorroborated = false
	uncorroborated.ExternalAIProbabilityPercent = nil
	uncorroborated.AIGCCalibration = ""
	uncorroborated.AIGCReport = mechanical.AIGCReport
	uncorroborated.AIGCReport.HumanAnchorFinalCap = nil
	if AcceptedWarningOnlyGate(&uncorroborated, analysis, editor) {
		t.Fatal("Editor prose alone must not clear an error without same-hash external <4% corroboration")
	}
}

func TestStructuralClearanceAcceptsConcreteNonblockingSynonym(t *testing.T) {
	bodyHash := BodySHA256("第二章\n\n程野险些把熟悉当成答案。")
	violation := rules.Violation{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning}
	payload := &MechanicalGatePayload{Chapter: 2, BodySHA256: bodyHash, RuleViolations: []rules.Violation{violation}}
	editor := &domain.ReviewEntry{Chapter: 2, BodySHA256: bodyHash, ContractStatus: "met", Dimensions: []domain.DimensionScore{
		{Dimension: "consistency", Score: 100, Verdict: "pass"}, {Dimension: "character", Score: 100, Verdict: "pass"},
		{Dimension: "pacing", Score: 100, Verdict: "pass"}, {Dimension: "continuity", Score: 100, Verdict: "pass"},
		{Dimension: "foreshadow", Score: 100, Verdict: "pass"}, {Dimension: "hook", Score: 90, Verdict: "pass"},
		{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
		{Dimension: "ai_voice_detection", Score: 70, Verdict: "warning", Comment: "pov_interiority_thin 仅命中1处，本章已有两处主观体验改变判断，不构成阻断。"},
	}}
	if !EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, violation) {
		t.Fatal("“不构成阻断” with same-sentence rule and judgment evidence should clear the warning")
	}
}

func TestCurrentHashExternalPassCalibratesExplicitlyClearedStructuralWarning(t *testing.T) {
	bodyHash := BodySHA256("第三章\n\n林澈把手机塞回口袋，先看了一眼桥口的人群。")
	payload := &MechanicalGatePayload{
		Chapter: 3, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 2.57, ZhuqueCompositePercent: 2.09, LegacyHeuristicPercent: 2.9,
			Dimensions: map[string]aigc.Dimension{"perplexity_proxy": {Score: 48}},
			Stats: aigc.Stats{Hanzi: 2495, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(98), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning}},
	}
	editor := &domain.ReviewEntry{
		Chapter: 3, BodySHA256: bodyHash, Verdict: "accept", ContractStatus: "met",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "dialogue_conveyor_overuse 提示为 warning，原文已有主视角停顿有效打断，无需改写。"},
		},
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if ApplyExternalCorroboration(payload, external) {
		t.Fatal("plain external corroboration must still reject a structural warning")
	}
	highLocal := *payload
	highLocal.AIGCReport = payload.AIGCReport
	highLocal.AIGCReport.AIGCPercent = 9
	highLocal.AIGCReport.BlendedAIGCPercent = 9
	if !ApplyExternalCorroborationWithEditor(&highLocal, external, editor) {
		t.Fatal("exact-body Editor clearance should calibrate a high aggregate when segment, integrity and deterministic gates are clean")
	}
	if got := aigc.EffectiveGatePercent(highLocal.AIGCReport); got >= aigc.PassExclusivePercent {
		t.Fatalf("calibrated high aggregate = %.2f, want < %.0f", got, aigc.PassExclusivePercent)
	}
	nonHuman := *external
	nonHuman.Verdict = "mixed"
	nonHuman.RiskLevel = "medium"
	if ApplyExternalCorroborationWithEditor(payload, &nonHuman, editor) {
		t.Fatal("non-human or non-low-risk external judgment must not calibrate the gate")
	}
	if !ApplyExternalCorroborationWithEditor(payload, external, editor) {
		t.Fatal("exact-body accepting Editor evidence should calibrate the explicitly cleared warning")
	}
	if got := aigc.EffectiveGatePercent(payload.AIGCReport); got != 2.57 {
		t.Fatalf("calibrated effective gate = %.2f, want 2.57", got)
	}
	if !AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("calibrated warning-only gate should be accepted as advisory")
	}
}

func newC10AggregateDiagnosticFixture() (*MechanicalGatePayload, *domain.ReviewEntry, *ExternalAIJudge) {
	bodyHash := BodySHA256("第十章\n\n程野把两路音轨重新对齐，仍没有替任何一列下结论。")
	payload := &MechanicalGatePayload{
		Chapter: 10, BodySHA256: bodyHash,
		AIGCReport: aigc.Report{
			AIGCPercent: 1.96, BlendedAIGCPercent: 1.96,
			Dimensions: map[string]aigc.Dimension{
				"structure_fingerprint": {
					Name: "结构指纹", Score: 48,
					Signals: []aigc.Signal{{Name: "transition_density", Evidence: "转折密度与段落重复聚合值"}},
				},
			},
			Stats: aigc.Stats{Hanzi: 2500, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "not_but_overuse", Severity: rules.SeverityWarning, Actual: 2, Limit: 1},
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 4},
		},
	}
	dimensions := []domain.DimensionScore{
		{Dimension: "consistency", Score: 100, Verdict: "pass"},
		{Dimension: "character", Score: 100, Verdict: "pass"},
		{Dimension: "pacing", Score: 100, Verdict: "pass"},
		{Dimension: "continuity", Score: 100, Verdict: "pass"},
		{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
		{Dimension: "hook", Score: 90, Verdict: "pass"},
		{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
		{Dimension: "ai_voice_detection", Score: 100, Verdict: "pass", Comment: "机械门禁项：not_but_overuse 2 次，主体两处间隔超过15段，语境不同且无模板感，判通过；object_response_rhythm_flat 显示4处延迟，但原文中的没有追问、没有立即圈它、没有偏向任何一列全部嵌入合理人物决策步骤，并非单调重复节奏，不影响叙事推进，不触发返工。"},
	}
	editor := &domain.ReviewEntry{
		Chapter: 10, BodySHA256: bodyHash, Verdict: "rewrite", ContractStatus: "met", Dimensions: dimensions,
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}
	return payload, editor, external
}

func TestC10EditorClearanceCalibratesAggregateDimensionToDiagnostic(t *testing.T) {
	payload, editor, external := newC10AggregateDiagnosticFixture()
	if got := BlockingAIGCDimensionReasons(payload.AIGCReport); len(got) != 1 {
		t.Fatalf("pre-calibration aggregate reasons = %v, want one visible diagnostic", got)
	}
	for _, violation := range payload.RuleViolations {
		if !EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, violation) {
			t.Fatalf("C10 exact-body Editor clearance did not cover %s", violation.Rule)
		}
	}
	if !ApplyExternalCorroborationWithEditor(payload, external, editor) {
		t.Fatalf("C10 exact-body Editor and external 2%% pass should calibrate aggregate diagnostics; blockers=%v", payload.CorroborationBlockedBy)
	}
	if got := BlockingAIGCDimensionReasons(payload.AIGCReport); len(got) != 0 {
		t.Fatalf("calibrated 48%% aggregate remained blocking: %v", got)
	}
	editor.Verdict = "accept"
	if !AcceptedWarningOnlyGate(payload, nil, editor) {
		t.Fatal("C10 calibrated warning-only review should be accepted")
	}
	if got := RewriteDisposition(payload, nil, external, editor); got != "可选" {
		t.Fatalf("C10 RewriteDisposition = %q, want 可选", got)
	}
}

func TestC10EditorClearanceRemainsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MechanicalGatePayload, *domain.ReviewEntry, *ExternalAIJudge)
	}{
		{name: "actual rewrite request", mutate: func(_ *MechanicalGatePayload, editor *domain.ReviewEntry, _ *ExternalAIJudge) {
			editor.Dimensions[7].Comment += "建议改写后再审。"
		}},
		{name: "deterministic error", mutate: func(payload *MechanicalGatePayload, _ *domain.ReviewEntry, _ *ExternalAIJudge) {
			payload.RuleViolations = append(payload.RuleViolations, rules.Violation{Rule: "impossible_line_of_sight", Severity: rules.SeverityError})
		}},
		{name: "failed dimension", mutate: func(_ *MechanicalGatePayload, editor *domain.ReviewEntry, _ *ExternalAIJudge) {
			editor.Dimensions[2].Score = 50
			editor.Dimensions[2].Verdict = "fail"
		}},
		{name: "external four percent", mutate: func(_ *MechanicalGatePayload, _ *domain.ReviewEntry, external *ExternalAIJudge) {
			external.AIProbabilityPercent = 4
		}},
		{name: "external wrong body hash", mutate: func(_ *MechanicalGatePayload, _ *domain.ReviewEntry, external *ExternalAIJudge) {
			external.BodySHA256 = BodySHA256("另一份正文")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, editor, external := newC10AggregateDiagnosticFixture()
			tt.mutate(payload, editor, external)
			if ApplyExternalCorroborationWithEditor(payload, external, editor) {
				t.Fatal("unsafe C10 case was calibrated")
			}
			if payload.AIGCReport.HumanAnchorFinalCap != nil {
				t.Fatal("failed C10 calibration persisted a human-anchor cap")
			}
		})
	}
}

func TestCurrentHashExternalPassCannotCalibrateStructuralDialogueBlocker(t *testing.T) {
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

	if ApplyExternalCorroboration(payload, external) {
		t.Fatal("same-hash external pass must not calibrate away a structural dialogue blocker")
	}
	if got := aigc.EffectiveGatePercent(payload.AIGCReport); got != 11.19 {
		t.Fatalf("uncalibrated gate = %.2f, want 11.19", got)
	}
	if got := RewriteDisposition(payload, nil, external, editor); got != "是" {
		t.Fatalf("RewriteDisposition = %q, want 是", got)
	}
	if !containsString(payload.CorroborationBlockedBy, "dialogue_conveyor_overuse") {
		t.Fatalf("structural blocker missing from corroboration audit: %#v", payload.CorroborationBlockedBy)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestCurrentHashExternalPassClearsWholeTextProbabilityProxyWithoutEditor(t *testing.T) {
	bodyHash := BodySHA256("第一章\n\n整章作为一个检测片段，三条原始分布曲线同时高危。")
	newPayload := func() *MechanicalGatePayload {
		return &MechanicalGatePayload{
			Chapter: 1, BodySHA256: bodyHash,
			AIGCReport: aigc.Report{
				AIGCPercent:          79.07,
				BlendedAIGCPercent:   7.25,
				WholeTextSegmentGate: 79.07,
				SegmentRiskFloor:     79.07,
				Stats: aigc.Stats{Hanzi: 2856, HumanAnchor: map[string]any{
					"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(90), "blockers": []string{},
				}},
			},
			RuleViolations: []rules.Violation{{Rule: "aigc_ratio", Severity: rules.SeverityError, Actual: 79.07}},
		}
	}
	external := &ExternalAIJudge{
		BodySHA256: bodyHash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	payload := newPayload()
	if !ApplyExternalCorroboration(payload, external) {
		t.Fatalf("same-hash provider pass should adjudicate probability proxies: %#v", payload.CorroborationBlockedBy)
	}
	if got := aigc.EffectiveGatePercent(payload.AIGCReport); got != 2 {
		t.Fatalf("calibrated whole-text gate = %.2f, want 2", got)
	}
	if payload.AIGCReport.AIGCPercent != 79.07 || payload.AIGCReport.SegmentRiskFloor != 79.07 {
		t.Fatalf("raw probability diagnostics were erased: %+v", payload.AIGCReport)
	}

	wrongHash := *external
	wrongHash.BodySHA256 = BodySHA256("另一份正文")
	if ApplyExternalCorroboration(newPayload(), &wrongHash) {
		t.Fatal("provider result from another body hash must not demote probability proxies")
	}
	atThreshold := *external
	atThreshold.AIProbabilityPercent = 4
	if ApplyExternalCorroboration(newPayload(), &atThreshold) {
		t.Fatal("an external score exactly at 4% must fail the strict threshold")
	}
	incomplete := *external
	incomplete.AdviceComplete = false
	if ApplyExternalCorroboration(newPayload(), &incomplete) {
		t.Fatal("an external result with incomplete advice must not calibrate")
	}
	hardPayload := newPayload()
	hardPayload.RuleViolations = append(hardPayload.RuleViolations, rules.Violation{
		Rule: "system_message_inline", Severity: rules.SeverityWarning,
	})
	if ApplyExternalCorroboration(hardPayload, external) {
		t.Fatal("exact external pass must never override a deterministic hard blocker")
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
