package reviewreport

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

const blockingAIGCDimensionThreshold = 40.0

var blockingMechanicalRules = map[string]bool{
	"aigc_ratio":                   true,
	"micro_action_overuse":         true,
	"dramatic_negation_overuse":    true,
	"paragraph_start_repetition":   true,
	"isolated_sentence_overuse":    true,
	"object_response_overuse":      true,
	"object_response_rhythm_flat":  true,
	"dialogue_semicolon_formality": true,
	"form_notice_semicolon_chain":  true,
	"semicolon_overuse":            true,
	"stiff_trade_dialogue":         true,
	"structured_note_triplet":      true,
	"card_tos_block":               true,
	"empty_parallel_chant":         true,
	"de_fa_adjective_repetition":   true,
	"duplicate_dialogue_point":     true,
	"impossible_body_geometry":     true,
	"impossible_line_of_sight":     true,
	"causal_evidence_order":        true,
	"identity_effect_delayed":      true,
	"building_floor_mismatch":      true,
	"anomalous_phone_unverified":   true,
	"form_image_mismatch":          true,
	"card_core_rule_overblurred":   true,
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
		strings.TrimSpace(flag.Severity) == "warning" &&
		analysis.Metrics.SentenceCount > 0 &&
		analysis.Metrics.SentenceCount < 20 {
		return false
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
	if mechanical == nil || mechanical.AIGCReport.BlendedAIGCPercent <= 0 || mechanical.AIGCReport.BlendedAIGCPercent > 5 {
		return false
	}
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
	return true
}

func BlockingAIGCDimensionReasons(report aigc.Report) []string {
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
