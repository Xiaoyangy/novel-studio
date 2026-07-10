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

func IsBlockingMechanicalViolation(v rules.Violation) bool {
	if v.Severity == rules.SeverityError {
		return true
	}
	return blockingMechanicalRules[strings.TrimSpace(v.Rule)]
}

func IsAIMechanicalViolation(v rules.Violation) bool {
	return blockingMechanicalRules[strings.TrimSpace(v.Rule)]
}

func IsBlockingAIVoiceFlag(flag domain.AIVoiceRedFlag) bool {
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
		if IsBlockingMechanicalViolation(v) {
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
		if IsBlockingMechanicalViolation(v) && IsAIMechanicalViolation(v) {
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
	// 读者会把整章 ~3000 字丢进检测器，看到的就是这个分——高于 5% 不许"仅告警通过"。
	if mechanical == nil {
		return false
	}
	if gate := aigc.EffectiveGatePercent(mechanical.AIGCReport); gate <= 0 || gate > 5 {
		return false
	}
	if len(BlockingAIGCDimensionReasons(mechanical.AIGCReport)) > 0 {
		return false
	}
	// Editor 已确认合同与八维均通过、整章 AIGC 采用值也低于阈值时，
	// 风格统计 warning 只作为下一章观察项。只有 error 级机械事实仍能推翻 accept；
	// 否则 isolated_sentence 等启发式指标会让同一章在不同建议间无限返工。
	for _, violation := range mechanical.RuleViolations {
		if violation.Severity == rules.SeverityError {
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
	if gate := aigc.EffectiveGatePercent(report); gate > 0 && gate <= 5 {
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
