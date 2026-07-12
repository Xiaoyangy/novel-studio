package tools

import (
	"fmt"
	"math"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type draftAIGCGateResult struct {
	Engine                       string   `json:"engine"`
	RawLocalGatePercent          float64  `json:"raw_local_gate_percent"`
	EffectiveGatePercent         float64  `json:"effective_gate_percent"`
	PassExclusivePercent         float64  `json:"pass_exclusive_percent"`
	Enforced                     bool     `json:"enforced"`
	Passed                       bool     `json:"passed"`
	ExternalCorroborated         bool     `json:"external_corroborated,omitempty"`
	ExternalAIProbabilityPercent *float64 `json:"external_ai_probability_percent,omitempty"`
	Calibration                  string   `json:"calibration,omitempty"`
	CorroborationBlockedBy       []string `json:"corroboration_blocked_by,omitempty"`
	RewriteFocus                 []string `json:"rewrite_focus,omitempty"`
	DiagnosticFocus              []string `json:"diagnostic_focus,omitempty"`
}

const draftAIGCMinHanzi = 800

func inspectDraftAIGCGate(st *store.Store, chapter int, content string) (aigc.Report, draftAIGCGateResult) {
	if st != nil {
		aigc.LoadProjectLexicon(st.Dir())
	}
	report := aigc.Analyze(content)
	gate := draftAIGCGateResultFromReport(report)
	if st != nil && chapter > 0 {
		gate = corroborateDraftAIGCGate(st, chapter, content, report, gate)
	}
	return report, gate
}

func draftAIGCGateResultFromReport(report aigc.Report) draftAIGCGateResult {
	percent := aigc.EffectiveGatePercent(report)
	enforced := report.Stats.Hanzi >= draftAIGCMinHanzi
	passed := !enforced || percent < aigc.PassExclusivePercent
	focus := mechanicalGateRewriteFocus(aigcViolation(report), report)
	if !enforced {
		focus = nil
	} else if !passed && len(focus) == 0 {
		focus = []string{"整章重排段落功能、人物主观因果和对白换挡；不要随机换词、补微动作或制造病句。"}
	}
	return draftAIGCGateResult{
		Engine:               report.Engine,
		RawLocalGatePercent:  percent,
		EffectiveGatePercent: percent,
		PassExclusivePercent: aigc.PassExclusivePercent,
		Enforced:             enforced,
		Passed:               passed,
		RewriteFocus:         focus,
	}
}

func corroborateDraftAIGCGate(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) draftAIGCGateResult {
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil || status == nil || !status.AdviceComplete || status.Blocking ||
		status.AIProbabilityPercent >= status.PassExclusivePercent ||
		strings.TrimSpace(status.BodySHA256) != reviewreport.BodySHA256(content) {
		return gate
	}
	externalPercent := float64(status.AIProbabilityPercent)
	gate.ExternalAIProbabilityPercent = &externalPercent

	if blockers := draftAIGCCorroborationBlockers(content, report); len(blockers) > 0 {
		gate.CorroborationBlockedBy = blockers
		return gate
	}
	calibrated := externalPercent
	calibration := "current_hash_external_consensus_max(deepseek,zhuque_adopted,legacy_adopted,segment_floor)"
	values := []float64{report.ZhuqueCompositePercent, report.SegmentRiskFloor, report.LegacyHeuristicPercent}
	if draftAIGCStrongNarrativeHumanAnchor(report) {
		calibration = "current_hash_strong_narrative_consensus_max(external,passing_local_proxies);high_local_proxies_diagnostic_only"
		values = passingDraftAIGCProxyValues(gate.PassExclusivePercent, report.ZhuqueCompositePercent, report.SegmentRiskFloor, report.LegacyHeuristicPercent)
		for name, value := range map[string]float64{
			"朱雀整章代理": report.ZhuqueCompositePercent,
			"分片风险代理": report.SegmentRiskFloor,
			"旧启发式":   report.LegacyHeuristicPercent,
		} {
			if value >= gate.PassExclusivePercent {
				gate.DiagnosticFocus = append(gate.DiagnosticFocus,
					fmt.Sprintf("%s %.2f%% 仅作诊断：同哈希外判通过，且没有独立分片、内容完整性或确定性文本硬伤。", name, value),
				)
			}
		}
	} else {
		gate.DiagnosticFocus = append(gate.DiagnosticFocus, "缺少强叙事人工锚点，本地代理继续参与保守上限。")
	}
	for _, value := range values {
		calibrated = math.Max(calibrated, value)
	}
	calibrated = math.Round(calibrated*100) / 100
	if calibrated >= gate.EffectiveGatePercent {
		return gate
	}
	gate.ExternalCorroborated = true
	gate.Calibration = calibration
	gate.EffectiveGatePercent = calibrated
	gate.Passed = !gate.Enforced || calibrated < gate.PassExclusivePercent
	if gate.Passed {
		gate.DiagnosticFocus = append(gate.DiagnosticFocus, gate.RewriteFocus...)
		gate.RewriteFocus = nil
	}
	return gate
}

func passingDraftAIGCProxyValues(passExclusive float64, values ...float64) []float64 {
	passing := make([]float64, 0, len(values))
	for _, value := range values {
		if value > 0 && value < passExclusive {
			passing = append(passing, value)
		}
	}
	return passing
}

func draftAIGCCorroborationBlockers(content string, report aigc.Report) []string {
	var blockers []string
	if report.ContentIntegrityFloor > 0 {
		blockers = append(blockers, "content_integrity_floor")
	}
	if (report.WholeTextSegmentGate >= aigc.PassExclusivePercent && !draftAIGCMarginalHumanWholeSegment(report)) ||
		report.ZhuqueSegmentProxy.SuspectedAIRatioPercent > 0 {
		blockers = append(blockers, "whole_text_or_segment_risk")
	}
	if report.LegacyHeuristicPercent >= 10 {
		blockers = append(blockers, "legacy_consensus_high")
	}
	for _, violation := range qualityrules.Lint(content) {
		if violation.Rule == "abstract_system_reassurance" || violation.Rule == "aphoristic_narrative_summary" {
			blockers = append(blockers, violation.Rule)
		}
	}
	return blockers
}

func draftAIGCMarginalHumanWholeSegment(report aigc.Report) bool {
	proxy := report.ZhuqueSegmentProxy
	if !draftAIGCStrongNarrativeHumanAnchor(report) || report.WholeTextSegmentGate > aigc.PassExclusivePercent ||
		!proxy.Enabled || len(proxy.Segments) != 1 || proxy.SuspectedAIRatioPercent > 0 ||
		proxy.RiskFloorPercent >= aigc.PassExclusivePercent {
		return false
	}
	segment := proxy.Segments[0]
	return segment.Proportion >= 0.95 && strings.Contains(segment.Category, "人工") &&
		segment.AIGCPercent <= aigc.PassExclusivePercent
}

func draftAIGCStrongNarrativeHumanAnchor(report aigc.Report) bool {
	anchor := report.Stats.HumanAnchor
	if anchor == nil {
		return false
	}
	eligible, _ := anchor["eligible"].(bool)
	strength, _ := anchor["strength"].(string)
	anchorType, _ := anchor["anchor_type"].(string)
	score, _ := anchor["score"].(float64)
	blockers, _ := anchor["blockers"].([]string)
	if raw, ok := anchor["blockers"].([]any); ok && len(raw) > 0 {
		return false
	}
	// The anchor classifier already labels the sample as strong. Requiring 90
	// again made 80s-scoring, scene-dense chapters ignore a current-hash external
	// pass because of a marginal legacy proxy. Keep true segment/integrity risks
	// as blockers above, but treat 80+ strong narrative anchors as corroboratable.
	return eligible && strength == "strong" && anchorType == "narrative_scene" && score >= 80 && len(blockers) == 0
}

func requireDraftAIGCGate(st *store.Store, chapter int, content string) error {
	_, gate := inspectDraftAIGCGate(st, chapter, content)
	if gate.Passed {
		return nil
	}
	focus := strings.Join(gate.RewriteFocus, "；")
	external := "none"
	if gate.ExternalAIProbabilityPercent != nil {
		external = fmt.Sprintf("%.2f%%", *gate.ExternalAIProbabilityPercent)
	}
	return fmt.Errorf(
		"第 %d 章草稿本地 AIGC 门禁 %.2f%% 未达到严格 <%.0f%%；禁止覆盖终稿。raw_local=%.2f%% external=%s corroboration_blockers=%v calibration=%q。请保持当前 world simulation 与 plan，先按 rewrite_focus 使用 edit_chapter 重排整章，再 read_chapter + check_consistency：%s: %w",
		chapter, gate.EffectiveGatePercent, gate.PassExclusivePercent, gate.RawLocalGatePercent, external,
		gate.CorroborationBlockedBy, gate.Calibration, focus, errs.ErrToolPrecondition,
	)
}

func draftQualityGateNextStep(wordContract chapterWordContractResult, gate draftAIGCGateResult) string {
	if !wordContract.Passed {
		return draftWordContractNextStep(wordContract)
	}
	if !gate.Passed {
		return "先 read_chapter(source=draft)，按 aigc_gate.rewrite_focus 用 edit_chapter 重排正文，再调用 check_consistency；本地 AIGC 严格 <4% 前禁止 commit_chapter。"
	}
	return draftWordContractNextStep(wordContract)
}
