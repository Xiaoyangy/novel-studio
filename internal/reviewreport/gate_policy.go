package reviewreport

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

const blockingAIGCDimensionThreshold = 40.0
const supportingDialogueRatioNearMissTolerance = 0.005

var blockingMechanicalRules = map[string]bool{
	"aigc_ratio":                         true,
	"external_aigc_ratio":                true,
	"micro_action_overuse":               true,
	"dramatic_negation_overuse":          true,
	"paragraph_start_repetition":         true,
	"isolated_sentence_overuse":          true,
	"object_response_overuse":            true,
	"dialogue_semicolon_formality":       true,
	"templated_dialogue_chain":           true,
	"abstract_system_reassurance":        true,
	"system_message_inline":              true,
	"opaque_procedure_jargon":            true,
	"dialogue_action_lead_repetition":    true,
	"trend_language_sound_effect_misuse": true,
	"system_procedure_narration":         true,
	"form_notice_semicolon_chain":        true,
	"semicolon_overuse":                  true,
	"stiff_trade_dialogue":               true,
	"structured_note_triplet":            true,
	"card_tos_block":                     true,
	"empty_parallel_chant":               true,
	"de_fa_adjective_repetition":         true,
	"duplicate_dialogue_point":           true,
	"impossible_body_geometry":           true,
	"impossible_line_of_sight":           true,
	"causal_evidence_order":              true,
	"identity_effect_delayed":            true,
	"building_floor_mismatch":            true,
	"anomalous_phone_unverified":         true,
	"form_image_mismatch":                true,
	"card_core_rule_overblurred":         true,
}

var blockingAIVoiceWarningRules = map[string]bool{
	"catalog_stuffing":                    true,
	"catalog_stuffing_run":                true,
	"ending_aphorism_question":            true,
	"ending_hook_uniformity":              true,
	"instant_purpose_answer_without_beat": true,
	"numbered_ladder_statement":           true,
	"opening_single_sentence_aphorism":    true,
	"supporting_dialogue_ratio":           true,
}

var structuralProseWarningRules = map[string]bool{
	"dialogue_conveyor_overuse":   true,
	"dialogue_micro_period_chain": true,
	"pov_interiority_thin":        true,
	"state_clause_pile":           true,
	"procedure_stage_pile":        true,
	"not_but_overuse":             true,
	"object_response_rhythm_flat": true,
}

// deterministicMechanicalRules describe concrete readability, factual or
// project-contract failures. Statistical style signals are intentionally not
// included: a current-hash external pass and an accepting Editor may demote
// those signals to diagnostics, but cannot excuse malformed prose or facts.
var deterministicMechanicalRules = map[string]bool{
	"external_aigc_ratio":                true,
	"abstract_system_reassurance":        true,
	"system_message_inline":              true,
	"opaque_procedure_jargon":            true,
	"trend_language_sound_effect_misuse": true,
	"system_procedure_narration":         true,
	"form_notice_semicolon_chain":        true,
	"stiff_trade_dialogue":               true,
	"structured_note_triplet":            true,
	"card_tos_block":                     true,
	"empty_parallel_chant":               true,
	"duplicate_dialogue_point":           true,
	"impossible_body_geometry":           true,
	"impossible_line_of_sight":           true,
	"causal_evidence_order":              true,
	"identity_effect_delayed":            true,
	"building_floor_mismatch":            true,
	"anomalous_phone_unverified":         true,
	"form_image_mismatch":                true,
	"card_core_rule_overblurred":         true,
}

func IsDeterministicMechanicalViolation(v rules.Violation) bool {
	if v.Severity == rules.SeverityError {
		return true
	}
	return deterministicMechanicalRules[strings.TrimSpace(v.Rule)]
}

func IsBlockingMechanicalViolation(v rules.Violation) bool {
	if v.Severity == rules.SeverityError {
		return true
	}
	return blockingMechanicalRules[strings.TrimSpace(v.Rule)]
}

func IsStructuralProseViolation(v rules.Violation) bool {
	return structuralProseWarningRules[strings.TrimSpace(v.Rule)]
}

var editorReviewDimensionNames = map[string]bool{
	"consistency":        true,
	"character":          true,
	"pacing":             true,
	"continuity":         true,
	"foreshadow":         true,
	"hook":               true,
	"aesthetic":          true,
	"ai_voice_detection": true,
}

// EditorExplicitlySupportsStructuralProseWarningClearance recognizes the
// narrow pre-reconciliation case where a statistical structural warning was
// inspected against this exact chapter body and the Editor recorded, in one
// clause, the rule name plus concrete evidence that the prose already contains
// an effective break. It intentionally does not require verdict=accept because
// it is used to reconcile a contradictory rewrite verdict after all eight
// dimensions passed. Errors and unmentioned/ambiguous warnings remain blocking.
func EditorExplicitlySupportsStructuralProseWarningClearance(editor *domain.ReviewEntry, mechanical *MechanicalGatePayload, violation rules.Violation) bool {
	if editor == nil || mechanical == nil || violation.Severity != rules.SeverityWarning || !IsStructuralProseViolation(violation) ||
		editor.Chapter <= 0 || editor.Chapter != mechanical.Chapter ||
		strings.TrimSpace(editor.BodySHA256) == "" || strings.TrimSpace(editor.BodySHA256) != strings.TrimSpace(mechanical.BodySHA256) ||
		strings.TrimSpace(editor.ContractStatus) != "met" || len(editor.ContractMisses) > 0 ||
		len(editor.Dimensions) != len(editorReviewDimensionNames) {
		return false
	}
	seenDimensions := make(map[string]bool, len(editorReviewDimensionNames))
	for _, dimension := range editor.Dimensions {
		name := strings.TrimSpace(dimension.Dimension)
		if !editorReviewDimensionNames[name] || seenDimensions[name] {
			return false
		}
		minimumScore := 80
		allowedVerdict := strings.TrimSpace(dimension.Verdict) == "pass"
		// 预协调时，第八维可能因为同时记录多个 statistical warning 被
		// Editor 记为 3/5（结构化后 70/warning），即使每条 warning 都在
		// comment 中明确写了正文证据和“无需改写”。允许这一维进入窄
		// clearance 路径；其余七维仍必须通过。真正放行还要经过同哈希
		// external <4%、机械门禁和 AI voice 的后续三方校准。
		if name == "ai_voice_detection" {
			minimumScore = 70
			allowedVerdict = allowedVerdict || strings.TrimSpace(dimension.Verdict) == "warning"
		}
		if dimension.Score < minimumScore || !allowedVerdict {
			return false
		}
		seenDimensions[name] = true
	}
	rule := strings.ToLower(strings.TrimSpace(violation.Rule))
	if rule == "" {
		return false
	}
	comments := make([]string, 0, len(editor.Dimensions))
	for _, dimension := range editor.Dimensions {
		comments = append(comments, dimension.Comment)
	}
	for _, comment := range comments {
		for _, clause := range strings.FieldsFunc(comment, func(r rune) bool {
			// 中文编辑常用分号串联“rule ID → 多处正文证据 → 无需改写”。
			// 分号仍属于同一句证据链；在这里切开会让 rule ID 留在前半句、
			// clearance 留在后半句，制造假阻断。真正的否定/返工词仍会在
			// 整句 blocked markers 中被优先识别。
			return r == '。' || r == '\n' || r == '\r'
		}) {
			normalized := strings.ToLower(strings.Join(strings.Fields(clause), ""))
			if !editorClauseMentionsStructuralRule(normalized, rule) ||
				(!strings.Contains(normalized, "warning") && !strings.Contains(normalized, "警示级") && !strings.Contains(normalized, "警告级")) {
				continue
			}
			blocked := false
			for _, marker := range []string{
				"未有效打断", "尚未有效打断", "并未有效打断", "没有有效打断", "没能有效打断",
				"无法有效打断", "不能有效打断", "不算有效打断", "不足以有效打断",
				"仍需修改", "需要修改", "必须修改", "建议修改", "应修改", "待修改",
				"仍需改写", "需要改写", "必须改写", "建议改写",
				"仍需返工", "需要返工", "必须返工", "建议返工", "不能放行", "不应放行",
			} {
				if strings.Contains(normalized, marker) {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			evidence := false
			for _, marker := range []string{
				"有效打断", "动作打断", "非对白动作", "动作与沉默", "沉默换挡", "后果换挡",
				"主视角停顿", "主视角停留", "主视角体验", "人物反应", "关系动作", "场景合理",
				"延迟出现", "延迟回应", "延迟与缺席", "延迟和缺席", "先行后开口",
			} {
				if strings.Contains(normalized, marker) {
					evidence = true
					break
				}
			}
			if !evidence {
				continue
			}
			for _, marker := range []string{
				"不构成改写门槛", "不触发返工", "仅记录不返工",
				"非阻断", "不损害阅读体验", "不伤害阅读体验", "不影响本单章阅读",
				"无需改写", "无需返工", "不构成强制改写", "仅作后续章优化",
			} {
				if strings.Contains(normalized, marker) {
					return true
				}
			}
		}
	}
	return false
}

func editorClauseMentionsStructuralRule(normalized, rule string) bool {
	if strings.Contains(normalized, rule) {
		return true
	}
	aliases := map[string][]string{
		"dialogue_conveyor_overuse":   {"对白传送带", "对话传送带"},
		"dialogue_micro_period_chain": {"微句号", "句号碎句", "短句号链"},
		"pov_interiority_thin":        {"主视角主观体验", "主视角内心", "pov内省", "pov主观"},
		"state_clause_pile":           {"状态从句堆叠", "状态分句堆叠"},
		"procedure_stage_pile":        {"流程阶段堆叠", "程序阶段堆叠"},
		"not_but_overuse":             {"不是而是句式", "不是a而是b"},
		"object_response_rhythm_flat": {"物件回应延迟", "物件回应节奏", "物件响应节奏"},
	}
	for _, alias := range aliases[rule] {
		if strings.Contains(normalized, alias) {
			return true
		}
	}
	return false
}

// EditorExplicitlyClearsStructuralProseWarning is the persisted/shared-gate
// form: the same strict evidence is required after reconciliation has produced
// an accepting Editor verdict.
func EditorExplicitlyClearsStructuralProseWarning(editor *domain.ReviewEntry, mechanical *MechanicalGatePayload, violation rules.Violation) bool {
	return editor != nil && strings.TrimSpace(editor.Verdict) == "accept" &&
		EditorExplicitlySupportsStructuralProseWarningClearance(editor, mechanical, violation)
}

func IsAIMechanicalViolation(v rules.Violation) bool {
	return blockingMechanicalRules[strings.TrimSpace(v.Rule)]
}

func IsBlockingAIVoiceFlag(flag domain.AIVoiceRedFlag) bool {
	if domain.IsAdvisoryAIVoiceFlag(flag) {
		return false
	}
	rule := strings.TrimSpace(flag.Rule)
	if rule == "" {
		return false
	}
	switch strings.TrimSpace(flag.Severity) {
	case "error", "critical":
		return true
	case "warning":
		return blockingAIVoiceWarningRules[rule]
	default:
		return false
	}
}

func IsBlockingAIVoiceFlagInAnalysis(flag domain.AIVoiceRedFlag, analysis domain.AIVoiceAnalysis) bool {
	if !IsBlockingAIVoiceFlag(flag) {
		return false
	}
	if strings.TrimSpace(flag.Rule) == "supporting_dialogue_ratio" &&
		strings.TrimSpace(flag.Severity) == "warning" {
		if analysis.Metrics.SentenceCount > 0 && analysis.Metrics.SentenceCount < 20 {
			return false
		}
		actual, limit := flag.Actual, flag.Limit
		if actual <= 0 {
			actual = analysis.Metrics.DialogueRatio
		}
		if limit <= 0 && analysis.Metrics.DialogueRatio > 0 {
			limit = 0.30
		}
		if limit > 0 && actual > 0 && actual >= limit-supportingDialogueRatioNearMissTolerance {
			return false
		}
	}
	return true
}

func HasBlockingMechanicalGate(payload *MechanicalGatePayload) bool {
	if payload == nil {
		return false
	}
	for _, v := range payload.RuleViolations {
		if IsBlockingMechanicalViolation(v) || IsStructuralProseViolation(v) {
			return true
		}
	}
	return len(BlockingAIGCDimensionReasons(payload.AIGCReport)) > 0
}

func HasBlockingAIMechanicalGate(payload *MechanicalGatePayload) bool {
	if payload == nil {
		return false
	}
	for _, v := range payload.RuleViolations {
		if (IsBlockingMechanicalViolation(v) && IsAIMechanicalViolation(v)) || IsStructuralProseViolation(v) {
			return true
		}
	}
	return len(BlockingAIGCDimensionReasons(payload.AIGCReport)) > 0
}

func HasBlockingContractMechanicalGate(payload *MechanicalGatePayload) bool {
	if payload == nil {
		return false
	}
	for _, v := range payload.RuleViolations {
		if IsBlockingMechanicalViolation(v) && !IsAIMechanicalViolation(v) {
			return true
		}
	}
	return false
}

func HasBlockingAIVoice(analysis *domain.AIVoiceAnalysis) bool {
	if analysis == nil {
		return false
	}
	for _, flag := range analysis.RedFlags {
		if IsBlockingAIVoiceFlagInAnalysis(flag, *analysis) {
			return true
		}
	}
	return false
}

func AcceptedWarningOnlyGate(mechanical *MechanicalGatePayload, aiVoice *domain.AIVoiceAnalysis, editor *domain.ReviewEntry) bool {
	if editor == nil || strings.TrimSpace(editor.Verdict) != "accept" {
		return false
	}
	if editor.ContractStatus != "" && editor.ContractStatus != "met" {
		return false
	}
	for _, issue := range editor.Issues {
		switch strings.TrimSpace(issue.Severity) {
		case "critical", "error":
			return false
		}
	}
	for _, dim := range editor.Dimensions {
		if strings.TrimSpace(dim.Verdict) == "fail" {
			return false
		}
	}
	// 与 commit 机械门禁共用口径：短章按 segment floor 真高判，不被 blended 稀释。
	// 读者会把整章 ~3000 字丢进检测器；当前门禁严格小于 4%。
	if mechanical == nil {
		return false
	}
	if gate := aigc.EffectiveGatePercent(mechanical.AIGCReport); gate <= 0 || gate >= aigc.PassExclusivePercent {
		return false
	}
	if len(BlockingAIGCDimensionReasons(mechanical.AIGCReport)) > 0 {
		return false
	}
	// Editor 已确认合同与八维均通过、整章 AIGC 采用值也低于阈值时，
	// 风格统计 warning 只作为下一章观察项。只有 error 级机械事实仍能推翻 accept；
	// 否则 isolated_sentence 等启发式指标会让同一章在不同建议间无限返工。
	for _, violation := range mechanical.RuleViolations {
		if IsDeterministicMechanicalViolation(violation) ||
			(IsStructuralProseViolation(violation) && !EditorExplicitlyClearsStructuralProseWarning(editor, mechanical, violation)) {
			return false
		}
	}
	if aiVoice != nil {
		for _, flag := range aiVoice.RedFlags {
			switch strings.TrimSpace(flag.Severity) {
			case "critical", "error":
				return false
			}
		}
	}
	return len(mechanical.RuleViolations) > 0 || hasBlockingAIVoiceWarnings(aiVoice)
}

// ApplyExternalCorroboration records a current-hash external pass as a
// calibrated cap only when the local report has a strong narrative anchor and
// no independent segment, integrity or deterministic prose blocker. The raw
// local score and every warning remain in the artifact for diagnosis.
func ApplyExternalCorroboration(payload *MechanicalGatePayload, external *ExternalAIJudge) bool {
	return applyExternalCorroboration(payload, external, nil)
}

// ApplyExternalCorroborationWithEditor permits one additional calibration path:
// an accepting, exact-body Editor review may clear an explicitly inspected
// statistical structural warning. Whole-text risk, integrity failures,
// deterministic violations and every unreviewed structural warning still block.
func ApplyExternalCorroborationWithEditor(payload *MechanicalGatePayload, external *ExternalAIJudge, editor *domain.ReviewEntry) bool {
	if payload == nil {
		return false
	}
	// Exact-body Editor evidence may clear only explicitly inspected statistical
	// warnings. Segment floors, integrity failures, deterministic violations and
	// every unreviewed warning remain blockers inside applyExternalCorroboration.
	return applyExternalCorroboration(payload, external, editor)
}

func applyExternalCorroboration(payload *MechanicalGatePayload, external *ExternalAIJudge, editor *domain.ReviewEntry) bool {
	if payload == nil || external == nil || external.Blocking || !external.AdviceComplete ||
		external.AIProbabilityPercent <= 0 || float64(external.AIProbabilityPercent) >= aigc.PassExclusivePercent ||
		!strings.Contains(strings.ToLower(strings.TrimSpace(external.Verdict)), "human") ||
		(strings.TrimSpace(strings.ToLower(external.RiskLevel)) != "low" && strings.TrimSpace(external.RiskLevel) != "低") {
		return false
	}
	if strings.TrimSpace(payload.BodySHA256) == "" || strings.TrimSpace(external.BodySHA256) == "" ||
		strings.TrimSpace(payload.BodySHA256) != strings.TrimSpace(external.BodySHA256) {
		return false
	}
	report := payload.AIGCReport
	if !strongNarrativeHumanAnchor(report) {
		return false
	}
	blockers := externalCorroborationBlockers(payload, editor)
	payload.CorroborationBlockedBy = blockers
	if len(blockers) > 0 {
		return false
	}

	calibrated := float64(external.AIProbabilityPercent)
	for _, value := range []float64{report.ZhuqueCompositePercent, report.SegmentRiskFloor, report.LegacyHeuristicPercent} {
		if value > 0 && value < aigc.PassExclusivePercent && value > calibrated {
			calibrated = value
		}
	}
	payload.ExternalCorroborated = true
	externalPercent := float64(external.AIProbabilityPercent)
	payload.ExternalAIProbabilityPercent = &externalPercent
	payload.AIGCCalibration = "current_hash_strong_narrative_consensus_max(external,passing_local_proxies);high_local_proxies_diagnostic_only"
	payload.AIGCReport.HumanAnchorFinalCap = &calibrated
	return true
}

func strongNarrativeHumanAnchor(report aigc.Report) bool {
	anchor := report.Stats.HumanAnchor
	if anchor == nil || !boolValue(anchor["eligible"]) || stringValue(anchor["strength"]) != "strong" ||
		stringValue(anchor["anchor_type"]) != "narrative_scene" || numberValue(anchor["score"]) < 80 {
		return false
	}
	switch blockers := anchor["blockers"].(type) {
	case []string:
		return len(blockers) == 0
	case []any:
		return len(blockers) == 0
	case nil:
		return true
	default:
		return false
	}
}

func externalCorroborationBlockers(payload *MechanicalGatePayload, editor *domain.ReviewEntry) []string {
	report := payload.AIGCReport
	var blockers []string
	if report.ContentIntegrityFloor > 0 {
		blockers = append(blockers, "content_integrity_floor")
	}
	if report.ZhuqueSegmentProxy.SuspectedAIRatioPercent > 0 || report.WholeTextSegmentGate >= aigc.PassExclusivePercent {
		blockers = append(blockers, "whole_text_or_segment_risk")
	}
	if report.LegacyHeuristicPercent >= 10 {
		blockers = append(blockers, "legacy_consensus_high")
	}
	for _, violation := range payload.RuleViolations {
		editorCleared := EditorExplicitlyClearsStructuralProseWarning(editor, payload, violation) ||
			EditorExplicitlySupportsStructuralProseWarningClearance(editor, payload, violation)
		if IsDeterministicMechanicalViolation(violation) ||
			(IsStructuralProseViolation(violation) && !editorCleared) {
			blockers = append(blockers, strings.TrimSpace(violation.Rule))
		}
	}
	return uniqueNonEmpty(blockers)
}

func boolValue(value any) bool {
	v, _ := value.(bool)
	return v
}

func stringValue(value any) string {
	v, _ := value.(string)
	return strings.TrimSpace(v)
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func hasBlockingAIVoiceWarnings(analysis *domain.AIVoiceAnalysis) bool {
	if analysis == nil {
		return false
	}
	for _, flag := range analysis.RedFlags {
		if strings.TrimSpace(flag.Rule) == "" && strings.TrimSpace(flag.Evidence) == "" {
			continue
		}
		switch strings.TrimSpace(flag.Severity) {
		case "", "info", "note":
			continue
		default:
			if IsBlockingAIVoiceFlagInAnalysis(flag, *analysis) {
				return true
			}
		}
	}
	return false
}

func BlockingAIGCDimensionReasons(report aigc.Report) []string {
	if gate := aigc.EffectiveGatePercent(report); gate > 0 && gate < aigc.PassExclusivePercent {
		if _, ok := aigc.HumanAnchorFinalCap(report); ok {
			return nil
		}
	}
	var reasons []string
	for _, dim := range sortedDimensions(report.Dimensions) {
		if dim.Score < blockingAIGCDimensionThreshold {
			continue
		}
		reason := fmt.Sprintf("高风险维度 %.2f%%｜%s", dim.Score, dim.Name)
		if len(dim.Signals) > 0 {
			reason += "｜" + dim.Signals[0].Name + "：" + dim.Signals[0].Evidence
		}
		reasons = append(reasons, reason)
	}
	return reasons
}
