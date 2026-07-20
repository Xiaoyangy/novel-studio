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

// aigc_ratio is a probability estimate even when a very high local score made
// the producer serialize it at error severity. It may be adjudicated only by
// the exact-body external+Editor path below; it is never treated as a factual
// or formatting error.
func isAIGCProbabilityViolation(v rules.Violation) bool {
	return strings.TrimSpace(v.Rule) == "aigc_ratio"
}

func IsDeterministicMechanicalViolation(v rules.Violation) bool {
	if isAIGCProbabilityViolation(v) {
		return false
	}
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
		normalizedComment := strings.ToLower(strings.Join(strings.Fields(comment), ""))
		// A positive evidence clause followed by a separate current-chapter
		// rewrite request is still a rewrite request. Inspect the whole comment
		// before accepting any one clause so punctuation cannot hide it.
		if editorClauseMentionsStructuralRule(normalizedComment, rule) && editorClauseRequestsStructuralEdit(normalizedComment) {
			return false
		}
		for _, clause := range strings.FieldsFunc(comment, func(r rune) bool {
			// 中文编辑常用分号串联“rule ID → 多处正文证据 → 无需改写”。
			// 分号仍属于同一句证据链；在这里切开会让 rule ID 留在前半句、
			// clearance 留在后半句，制造假阻断。真正的否定/返工词仍会在
			// 整句 blocked markers 中被优先识别。
			return r == '。' || r == '\n' || r == '\r'
		}) {
			normalized := strings.ToLower(strings.Join(strings.Fields(clause), ""))
			explicitRuleIDClearance := strings.Contains(normalized, "ruleid清除")
			explicitDialogueNonTrigger := rule == "dialogue_conveyor_overuse" && strings.Contains(normalized, "不触发")
			explicitNonblockingClearance := strings.Contains(normalized, "不构成阻断")
			explicitPassClearance := strings.Contains(normalized, "判通过")
			explicitNoReworkClearance := strings.Contains(normalized, "不触发返工")
			if !editorClauseMentionsStructuralRule(normalized, rule) ||
				(!strings.Contains(normalized, "warning") && !strings.Contains(normalized, "警示级") &&
					!strings.Contains(normalized, "警告级") && !explicitRuleIDClearance && !explicitDialogueNonTrigger &&
					!explicitNonblockingClearance && !explicitPassClearance && !explicitNoReworkClearance) {
				continue
			}
			if editorClauseRequestsStructuralEdit(normalized) {
				continue
			}
			if !editorClauseHasStructuralClearanceEvidence(normalized, rule) {
				continue
			}
			for _, marker := range []string{
				"不构成改写门槛", "不触发返工", "仅记录不返工",
				"非阻断", "不构成阻断", "不损害阅读体验", "不伤害阅读体验", "不影响本单章阅读",
				"无需改写", "无需返工", "不构成强制改写", "仅作后续章优化",
				// Editor 有时会逐条写“rule ID 清除”，再以“无 structural
				// warning 阻断本章”收束。两种写法都明确表示当前 exact-body
				// warning 已核验为非阻断；仍须同时满足上面的 rule、warning、
				// 正文打断证据和八维/合同约束，不能单凭一句泛化结论放行。
				"ruleid清除", "warning清除", "判通过", "不触发", "无structuralwarning阻断",
			} {
				if strings.Contains(normalized, marker) {
					return true
				}
			}
		}
	}
	return false
}

func editorClauseRequestsStructuralEdit(normalized string) bool {
	return reviewPolicyContainsAny(normalized,
		"未有效打断", "尚未有效打断", "并未有效打断", "没有有效打断", "没能有效打断",
		"无法有效打断", "不能有效打断", "不算有效打断", "不足以有效打断",
		"仍需修改", "需要修改", "必须修改", "建议修改", "应修改", "待修改",
		"仍需改写", "需要改写", "必须改写", "建议改写",
		"仍需返工", "需要返工", "必须返工", "建议返工", "不能放行", "不应放行",
	)
}

func editorClauseHasStructuralClearanceEvidence(normalized, rule string) bool {
	for _, marker := range []string{
		"有效打断", "动作打断", "非对白动作", "动作与沉默", "沉默换挡", "后果换挡",
		"主视角停顿", "主视角停留", "主视角体验", "人物反应", "关系动作", "场景合理",
		"延迟出现", "延迟回应", "延迟与缺席", "延迟和缺席", "先行后开口",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	switch rule {
	case "dialogue_conveyor_overuse", "dialogue_micro_period_chain":
		return (strings.Contains(normalized, "追问") && strings.Contains(normalized, "沉默")) ||
			strings.Contains(normalized, "未连续") || strings.Contains(normalized, "没有连续")
	case "pov_interiority_thin":
		return (strings.Contains(normalized, "压住") && strings.Contains(normalized, "冲动")) ||
			(strings.Contains(normalized, "改变") && reviewPolicyContainsAny(normalized, "选择", "判断", "注意力", "关系")) ||
			strings.Contains(normalized, "判断管控")
	case "not_but_overuse":
		return strings.Contains(normalized, "间隔") &&
			reviewPolicyContainsAny(normalized, "语境不同", "语义不同", "功能不同") &&
			reviewPolicyContainsAny(normalized, "无模板感", "不构成模板", "非模板")
	case "object_response_rhythm_flat":
		return (strings.Contains(normalized, "延迟") && strings.Contains(normalized, "缺席")) ||
			(reviewPolicyContainsAny(normalized, "嵌入合理人物决策步骤", "合理人物决策步骤", "人物决策步骤") &&
				reviewPolicyContainsAny(normalized, "并非单调重复节奏", "不构成单调重复节奏", "不是单调重复节奏"))
	default:
		return false
	}
}

func reviewPolicyContainsAny(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
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

// EditorExplicitlySupportsAIVoiceWarningClearance recognizes only an exact-body
// Editor audit of one warning-level AI-voice rule. The rule, its concrete scene
// evidence and an explicit non-blocking conclusion must occur in the same
// sentence; generic reassurance or any requested edit remains blocking.
func EditorExplicitlySupportsAIVoiceWarningClearance(editor *domain.ReviewEntry, mechanical *MechanicalGatePayload, analysis *domain.AIVoiceAnalysis, flag domain.AIVoiceRedFlag) bool {
	if editor == nil || mechanical == nil || analysis == nil ||
		strings.TrimSpace(flag.Severity) != "warning" || !blockingAIVoiceWarningRules[strings.TrimSpace(flag.Rule)] ||
		editor.Chapter <= 0 || editor.Chapter != mechanical.Chapter || editor.Chapter != analysis.Chapter ||
		strings.TrimSpace(editor.BodySHA256) == "" || editor.BodySHA256 != mechanical.BodySHA256 || editor.BodySHA256 != analysis.BodySHA256 ||
		strings.TrimSpace(editor.ContractStatus) != "met" || len(editor.ContractMisses) > 0 ||
		!editorDimensionsSupportWarningClearance(editor.Dimensions) || !aiVoiceAnalysisContainsFlag(*analysis, flag) {
		return false
	}
	for _, issue := range editor.Issues {
		switch strings.TrimSpace(issue.Severity) {
		case "critical", "error":
			return false
		}
	}
	rule := strings.ToLower(strings.TrimSpace(flag.Rule))
	for _, dimension := range editor.Dimensions {
		for _, normalized := range editorAIVoiceRuleClauses(dimension.Comment, rule) {
			if strings.Contains(normalized, rule) && editorClauseRequestsAIVoiceEdit(normalized) {
				return false
			}
		}
	}
	for _, dimension := range editor.Dimensions {
		for _, normalized := range editorAIVoiceRuleClauses(dimension.Comment, rule) {
			if !strings.Contains(normalized, rule) ||
				!reviewPolicyContainsAny(normalized, "severitywarning", "warning级", "警示级", "警告级") ||
				!editorClauseHasAIVoiceClearanceEvidence(normalized, flag) ||
				!reviewPolicyContainsAny(normalized, "不触发返工", "无需改写", "无需返工", "不构成改写门槛", "不构成阻断", "非阻断") ||
				editorClauseRequestsAIVoiceEdit(normalized) {
				continue
			}
			return true
		}
	}
	return false
}

// EditorExplicitlySupportsContextualDialogueRatioErrorClearance handles one
// deliberately narrow false-positive class. A very low dialogue ratio is
// promoted to error by a numeric threshold, but an exact-body Editor can prove
// that adding dialogue would violate the scene: only two people share the
// monitoring room, off-screen riders are silent, the controlled participant
// cannot speak, and the existing question already creates the necessary
// conflict. Unlike warning clearance, this path additionally requires a
// persisted same-hash external <4% corroboration and eight clean Editor
// dimensions. No other error-level AI-voice rule is eligible.
func EditorExplicitlySupportsContextualDialogueRatioErrorClearance(editor *domain.ReviewEntry, mechanical *MechanicalGatePayload, analysis *domain.AIVoiceAnalysis, flag domain.AIVoiceRedFlag) bool {
	if editor == nil || mechanical == nil || analysis == nil ||
		strings.TrimSpace(flag.Rule) != "supporting_dialogue_ratio" || strings.TrimSpace(flag.Severity) != "error" ||
		strings.TrimSpace(editor.Verdict) != "rewrite" && strings.TrimSpace(editor.Verdict) != "accept" ||
		editor.Chapter <= 0 || editor.Chapter != mechanical.Chapter || editor.Chapter != analysis.Chapter ||
		strings.TrimSpace(editor.BodySHA256) == "" || editor.BodySHA256 != mechanical.BodySHA256 || editor.BodySHA256 != analysis.BodySHA256 ||
		strings.TrimSpace(editor.ContractStatus) != "met" || len(editor.ContractMisses) > 0 ||
		!mechanical.ExternalCorroborated || mechanical.ExternalAIProbabilityPercent == nil ||
		*mechanical.ExternalAIProbabilityPercent <= 0 || *mechanical.ExternalAIProbabilityPercent >= aigc.PassExclusivePercent ||
		aigc.EffectiveGatePercent(mechanical.AIGCReport) <= 0 || aigc.EffectiveGatePercent(mechanical.AIGCReport) >= aigc.PassExclusivePercent ||
		!strings.Contains(strings.TrimSpace(mechanical.AIGCCalibration), "current_hash_") ||
		!editorDimensionsStrictlyPass(editor.Dimensions) || !aiVoiceAnalysisContainsFlag(*analysis, flag) {
		return false
	}
	for _, issue := range editor.Issues {
		if strings.TrimSpace(issue.Severity) != "warning" || strings.TrimSpace(issue.Type) != "aesthetic" {
			return false
		}
	}
	for _, dimension := range editor.Dimensions {
		if strings.TrimSpace(dimension.Dimension) != "ai_voice_detection" {
			continue
		}
		for _, normalized := range editorAIVoiceRuleClauses(dimension.Comment, "supporting_dialogue_ratio") {
			if !strings.Contains(normalized, "supporting_dialogue_ratio") ||
				!reviewPolicyContainsAny(normalized, "severity:error", "severityerror", "error级", "错误级") ||
				!editorClauseHasContextualDialogueRatioErrorEvidence(normalized) ||
				!strings.Contains(normalized, "无需改写") || !strings.Contains(normalized, "不触发返工") ||
				editorClauseRequestsAIVoiceEdit(normalized) {
				continue
			}
			return true
		}
	}
	return false
}

func editorDimensionsStrictlyPass(dimensions []domain.DimensionScore) bool {
	if len(dimensions) != len(editorReviewDimensionNames) {
		return false
	}
	seen := make(map[string]bool, len(editorReviewDimensionNames))
	for _, dimension := range dimensions {
		name := strings.TrimSpace(dimension.Dimension)
		if !editorReviewDimensionNames[name] || seen[name] || dimension.Score < 80 || strings.TrimSpace(dimension.Verdict) != "pass" {
			return false
		}
		seen[name] = true
	}
	return true
}

func editorClauseHasContextualDialogueRatioErrorEvidence(normalized string) bool {
	return reviewPolicyContainsAny(normalized, "仅两名人物同处一室", "仅二人同室") &&
		strings.Contains(normalized, "骑手静默") &&
		reviewPolicyContainsAny(normalized, "受控无声", "无对话机会") &&
		reviewPolicyContainsAny(normalized, "必要冲突", "信息碰撞")
}

// editorAIVoiceRuleClauses treats one numbered "red flag N" entry as the
// evidence clause. Editor commonly puts the rule header and its scene audit in
// two grammatical sentences; the next numbered red flag is the hard boundary,
// so evidence or clearance can never bleed between rules.
func editorAIVoiceRuleClauses(comment, rule string) []string {
	normalized := strings.ToLower(strings.Join(strings.Fields(comment), ""))
	if normalized == "" {
		return nil
	}
	var starts []int
	for offset := 0; ; {
		idx := strings.Index(normalized[offset:], "redflag")
		if idx < 0 {
			break
		}
		starts = append(starts, offset+idx)
		offset += idx + len("redflag")
	}
	var out []string
	for i, start := range starts {
		end := len(normalized)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		segment := normalized[start:end]
		if strings.Contains(segment, rule) {
			out = append(out, segment)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, clause := range strings.FieldsFunc(comment, func(r rune) bool {
		return r == '。' || r == '！' || r == '？' || r == '\n' || r == '\r'
	}) {
		clause = strings.ToLower(strings.Join(strings.Fields(clause), ""))
		if strings.Contains(clause, rule) {
			out = append(out, clause)
		}
	}
	return out
}

func editorClauseRequestsAIVoiceEdit(normalized string) bool {
	return reviewPolicyContainsAny(normalized,
		"建议修改", "建议改写", "建议返工", "建议调整", "建议删", "建议拆", "建议补",
		"仍需修改", "仍需改写", "仍需返工", "需要修改", "需要改写", "需要返工", "待修改", "待改写", "待返工")
}

func editorDimensionsSupportWarningClearance(dimensions []domain.DimensionScore) bool {
	if len(dimensions) != len(editorReviewDimensionNames) {
		return false
	}
	seen := make(map[string]bool, len(editorReviewDimensionNames))
	for _, dimension := range dimensions {
		name := strings.TrimSpace(dimension.Dimension)
		if !editorReviewDimensionNames[name] || seen[name] {
			return false
		}
		minimum, allowed := 80, strings.TrimSpace(dimension.Verdict) == "pass"
		if name == "ai_voice_detection" {
			minimum = 70
			allowed = allowed || strings.TrimSpace(dimension.Verdict) == "warning"
		}
		if dimension.Score < minimum || !allowed {
			return false
		}
		seen[name] = true
	}
	return true
}

func aiVoiceAnalysisContainsFlag(analysis domain.AIVoiceAnalysis, want domain.AIVoiceRedFlag) bool {
	for _, got := range analysis.RedFlags {
		if got.Rule == want.Rule && got.Severity == want.Severity && got.Paragraph == want.Paragraph && got.Sentence == want.Sentence &&
			got.Evidence == want.Evidence && got.Actual == want.Actual && got.Limit == want.Limit {
			return true
		}
	}
	return false
}

func editorClauseHasAIVoiceClearanceEvidence(normalized string, flag domain.AIVoiceRedFlag) bool {
	if evidence := strings.ToLower(strings.Join(strings.Fields(flag.Evidence), "")); len([]rune(evidence)) >= 8 && strings.Contains(normalized, evidence) {
		return true
	}
	if strings.TrimSpace(flag.Rule) != "supporting_dialogue_ratio" {
		return false
	}
	return reviewPolicyContainsAny(normalized, "独处", "仅接收", "角色位置", "叙事限定", "现场无其他配角") &&
		reviewPolicyContainsAny(normalized, "转达", "转述", "不直接通话", "只在通道里给结论", "主动误解", "打断或拒绝") &&
		reviewPolicyContainsAny(normalized, "有效打断", "不直接通话", "限定视角", "限知视角", "单一视角", "叙事限定")
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
			case "critical":
				return false
			case "error":
				if !EditorExplicitlySupportsContextualDialogueRatioErrorClearance(editor, mechanical, aiVoice, flag) {
					return false
				}
				continue
			}
			if IsBlockingAIVoiceFlagInAnalysis(flag, *aiVoice) &&
				!EditorExplicitlySupportsAIVoiceWarningClearance(editor, mechanical, aiVoice, flag) {
				return false
			}
		}
	}
	// Reaching here proves every blocking warning was explicitly cleared. Keep
	// those warnings visible as optional diagnostics; near-threshold signals that
	// IsBlockingAIVoiceFlagInAnalysis already demoted remain a clean pass.
	return len(mechanical.RuleViolations) > 0 || hasBlockingAIVoiceWarnings(aiVoice)
}

// ApplyExternalCorroboration records a current-hash external pass as a
// calibrated cap only when the local report has a strong narrative anchor and
// no integrity, deterministic or structural prose blocker. Whole-text,
// segment, legacy and aigc_ratio values are probability estimates over those
// exact bytes; the independent provider adjudicates that component. The raw
// local score remains in the artifact for diagnosis.
func ApplyExternalCorroboration(payload *MechanicalGatePayload, external *ExternalAIJudge) bool {
	return applyExternalCorroboration(payload, external, nil)
}

// ApplyExternalCorroborationWithEditor permits one additional calibration path:
// an accepting, exact-body Editor review may clear explicitly inspected
// structural warnings. Probability proxies remain diagnostics; integrity
// failures, deterministic violations and every unreviewed structural warning
// still block.
func ApplyExternalCorroborationWithEditor(payload *MechanicalGatePayload, external *ExternalAIJudge, editor *domain.ReviewEntry) bool {
	if payload == nil {
		return false
	}
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
	// Retire only the routing bits. Raw whole-text, segment and legacy values stay
	// in the report for audit, while EffectiveGatePercent can honor the exact-body
	// calibrated cap.
	payload.AIGCReport.WholeTextSegmentGate = 0
	for i := range payload.AIGCReport.ZhuqueSegmentProxy.Segments {
		payload.AIGCReport.ZhuqueSegmentProxy.Segments[i].WholeTextHardGate = false
	}
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
	if report.Stats.Repeated12Extra >= 40 && report.Stats.Hanzi > 0 && report.Stats.Repeated12Extra*10 >= report.Stats.Hanzi {
		blockers = append(blockers, "deterministic_long_ngram_repeat")
	}
	for _, violation := range payload.RuleViolations {
		if isAIGCProbabilityViolation(violation) {
			// The exact-body provider result is the authority for authorship
			// probability. Keep the local value visible, but do not reclassify it
			// as a factual or formatting defect merely because it was serialized
			// at error severity.
			continue
		}
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
