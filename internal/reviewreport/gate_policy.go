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
		if IsDeterministicMechanicalViolation(violation) {
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
	if payload == nil || external == nil || external.Blocking || !external.AdviceComplete ||
		external.AIProbabilityPercent <= 0 || float64(external.AIProbabilityPercent) >= aigc.PassExclusivePercent {
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
	blockers := externalCorroborationBlockers(payload)
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

func externalCorroborationBlockers(payload *MechanicalGatePayload) []string {
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
		if IsDeterministicMechanicalViolation(violation) {
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
