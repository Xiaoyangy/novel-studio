package tools

import (
	"fmt"
	"math"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
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
		if draftAIGCExternalProbabilityOnlySatisfied(content, report, gate) {
			calibrated := *gate.ExternalAIProbabilityPercent
			for _, value := range passingDraftAIGCProxyValues(
				gate.PassExclusivePercent,
				report.ZhuqueCompositePercent,
				report.SegmentRiskFloor,
				report.LegacyHeuristicPercent,
			) {
				calibrated = math.Max(calibrated, value)
			}
			gate.ExternalCorroborated = true
			gate.EffectiveGatePercent = math.Round(calibrated*100) / 100
			gate.Passed = !gate.Enforced || gate.EffectiveGatePercent < gate.PassExclusivePercent
			gate.Calibration = "current_hash_external_probability_only;raw_local_diagnostic;mechanical_structural_gates_preserved"
			gate.CorroborationBlockedBy = nil
			gate.DiagnosticFocus = append(gate.DiagnosticFocus, gate.RewriteFocus...)
			gate.RewriteFocus = nil
		}
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

// draftAIGCRawLocalGateResult preserves the raw local decision for audit and
// diagnostics. A same-body DeepSeek pass may clear only a probability-only
// disagreement through draftAIGCExternalProbabilityOnlySatisfied below; true
// whole-text, integrity, mechanical and structural failures still use this raw
// result and remain blocking.
func draftAIGCRawLocalGateResult(report aigc.Report, gate draftAIGCGateResult) draftAIGCGateResult {
	raw := gate
	if raw.PassExclusivePercent <= 0 {
		raw.PassExclusivePercent = aigc.PassExclusivePercent
	}
	raw.EffectiveGatePercent = raw.RawLocalGatePercent
	raw.Passed = !raw.Enforced || raw.RawLocalGatePercent < raw.PassExclusivePercent
	if raw.Enforced && !raw.Passed && len(raw.RewriteFocus) == 0 {
		raw.RewriteFocus = mechanicalGateRewriteFocus(aigcViolation(report), report)
		if len(raw.RewriteFocus) == 0 {
			raw.RewriteFocus = []string{"整章重排段落功能、人物主观因果和对白换挡；不要随机换词、补微动作或制造病句。"}
		}
	}
	return raw
}

func draftAIGCRawLocalPassed(report aigc.Report, gate draftAIGCGateResult) bool {
	return draftAIGCRawLocalGateResult(report, gate).Passed
}

// draftAIGCExternalProbabilityOnlySatisfied recognizes the narrow case where
// the local detector and the independent DeepSeek judge disagree only about a
// probability estimate for these exact bytes. corroborateDraftAIGCGate records
// ExternalAIProbabilityPercent only after checking advice completeness,
// blocking state, the strict <4% boundary and exact body SHA, so the pointer is
// also the durable same-body identity proof here.
//
// This is deliberately not a general external override. The whole-text,
// segment and legacy values are probability proxies, so an exact-body provider
// pass may demote them to diagnostics. Content-integrity evidence and every
// non-AIGC mechanical or structural prose violation remain hard. Raw local
// scores and rewrite diagnostics stay visible in the returned artifact.
func draftAIGCExternalProbabilityOnlySatisfied(content string, report aigc.Report, gate draftAIGCGateResult) bool {
	if !draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		return false
	}
	return len(draftAIGCExternalCurrentBodyBlockers(content)) == 0
}

// draftAIGCExternalProbabilityComponentSatisfied resolves only the stochastic
// authorship-probability component of the local gate.  Concrete mechanical
// warnings are deliberately not part of this predicate: after an exact-body
// provider pass they remain eligible for the single bounded local-soft edit.
// Integrity damage, extreme exact repetition and deterministic prose/fact
// failures are not calibratable and therefore remain fail-closed.
func draftAIGCExternalProbabilityComponentSatisfied(content string, report aigc.Report, gate draftAIGCGateResult) bool {
	raw := draftAIGCRawLocalGateResult(report, gate)
	if raw.Passed || !raw.Enforced || gate.ExternalAIProbabilityPercent == nil ||
		*gate.ExternalAIProbabilityPercent < 0 || *gate.ExternalAIProbabilityPercent >= raw.PassExclusivePercent {
		return false
	}
	return draftAIGCLocalProbabilityComponentCalibratable(content, report)
}

func draftAIGCLocalProbabilityOnly(content string, report aigc.Report) bool {
	if !draftAIGCLocalProbabilityComponentCalibratable(content, report) ||
		len(draftAIGCExternalCurrentBodyBlockers(content)) > 0 {
		return false
	}
	return true
}

// draftAIGCLocalProbabilityComponentCalibratable recognizes probability
// proxies that an independent exact-body provider may adjudicate. Statistical
// structural warnings (dialogue conveyor, thin POV, rhythm, etc.) are handled
// separately as mechanical local-soft work and must not turn the probability
// proxy into a whole-draft rerender capability.
func draftAIGCLocalProbabilityComponentCalibratable(content string, report aigc.Report) bool {
	if report.ContentIntegrityFloor > 0 || draftAIGCHasExtremeLongNgramRepetition(report) {
		return false
	}
	for _, violation := range qualityrules.Lint(content) {
		if strings.TrimSpace(violation.Rule) == "aigc_ratio" {
			continue
		}
		if reviewreport.IsDeterministicMechanicalViolation(violation) {
			return false
		}
	}
	for _, blocker := range draftAIGCCorroborationBlockers(content, report) {
		switch blocker {
		case "whole_text_or_segment_risk", "legacy_consensus_high":
			// These are probability estimates over the same exact body.
		default:
			return false
		}
	}
	return true
}

// draftAIGCManagedProviderPendingWholeText reports the narrow managed-writing
// state in which a current, checkpoint-bound body has only a local
// whole-text/segment probability signal and still lacks a complete exact-body
// DeepSeek conclusion. Such a signal remains visible but may not mint a
// rerender capability or consume the sealed convergence budget before the
// provider adjudicates these bytes.
func draftAIGCManagedProviderPendingWholeText(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) (bool, error) {
	if !draftAIGCHasWholeTextStructuralBlock(content, report, gate) ||
		!draftAIGCLocalProbabilityComponentCalibratable(content, report) {
		return false, nil
	}
	bodySHA := reviewreport.BodySHA256(content)
	tracked, err := pipelineManagedCurrentDraftTracked(st, chapter, bodySHA)
	if err != nil || !tracked {
		return false, err
	}
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil {
		return false, err
	}
	return status == nil || strings.TrimSpace(status.BodySHA256) != bodySHA || !status.AdviceComplete, nil
}

// draftAIGCWholeDraftRerenderRequired is the managed-writing routing decision.
// The raw whole-text signal stays visible in diagnostics, but a calibratable
// probability proxy must first reach an exact-body provider conclusion and
// cannot itself consume a whole-render attempt. A provider rejection, true
// non-calibratable risk, or an unmanaged legacy flow retains the old bounded
// whole-rerender behavior.
func draftAIGCWholeDraftRerenderRequired(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) (bool, error) {
	if !draftAIGCHasWholeTextStructuralBlock(content, report, gate) {
		return false, nil
	}
	if draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		return false, nil
	}
	pending, err := draftAIGCManagedProviderPendingWholeText(st, chapter, content, report, gate)
	if err != nil || pending {
		return false, err
	}
	return true, nil
}

func draftAIGCExternalCurrentBodyBlockers(content string) []qualityrules.Violation {
	var blockers []qualityrules.Violation
	for _, violation := range qualityrules.Lint(content) {
		if strings.TrimSpace(violation.Rule) == "aigc_ratio" {
			continue
		}
		if reviewreport.IsBlockingMechanicalViolation(violation) ||
			reviewreport.IsStructuralProseViolation(violation) {
			blockers = append(blockers, violation)
		}
	}
	return blockers
}

func corroborateDraftAIGCGate(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) draftAIGCGateResult {
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil || status == nil || !status.AdviceComplete || status.Blocking ||
		float64(status.AIProbabilityPercent) >= aigc.PassExclusivePercent ||
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
	// Repeated12Extra is an exact count over the current bytes, not a model
	// probability.  Only treat a large duplicate footprint as hard evidence so
	// ordinary refrains and recurring names remain eligible for provider
	// calibration, while pasted/template paragraphs cannot pass on a low
	// external percentage alone.
	if draftAIGCHasExtremeLongNgramRepetition(report) {
		blockers = append(blockers, "deterministic_long_ngram_repeat")
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

func draftAIGCHasExtremeLongNgramRepetition(report aigc.Report) bool {
	repeated := report.Stats.Repeated12Extra
	hanzi := report.Stats.Hanzi
	return repeated >= 40 && hanzi > 0 && repeated*10 >= hanzi
}

// draftAIGCHasWholeTextStructuralBlock distinguishes a deterministic whole-text
// failure from a soft local score. A fresh structural failure is useful before
// paying the latency/cost of a named platform retest: the draft is not yet a
// viable final candidate, so the pipeline may spend another bounded full-render
// attempt while keeping every registered detector obligation for the eventual
// locally clean hash.
func draftAIGCHasWholeTextStructuralBlock(content string, report aigc.Report, gate draftAIGCGateResult) bool {
	if draftAIGCRawLocalPassed(report, gate) {
		return false
	}
	for _, blocker := range draftAIGCCorroborationBlockers(content, report) {
		if blocker == "whole_text_or_segment_risk" {
			return true
		}
	}
	return false
}

// CurrentDraftHasLocalStructuralBlock independently reproduces the current
// draft's deterministic whole-text/segment failure. Markers alone never grant
// another write: the bytes on disk must still fail the current local engine.
func CurrentDraftHasLocalStructuralBlock(st *store.Store, chapter int) bool {
	_, ok := currentDraftLocalStructuralRerenderRequirement(st, chapter, nil)
	return ok
}

// currentDraftLocalStructuralRerenderRequirement synthesizes an exact-hash
// local marker from the current bytes. Only an explicitly configured
// automated_hard detector contract is carried forward. User sampling identity
// remains in the append-only log and never becomes a replacement-body duty.
func currentDraftLocalStructuralRerenderRequirement(st *store.Store, chapter int, base *DraftExternalRerenderRequirement) (*DraftExternalRerenderRequirement, bool) {
	if st == nil || chapter <= 0 {
		return nil, false
	}
	content, err := st.Drafts.LoadDraft(chapter)
	if err != nil || strings.TrimSpace(content) == "" {
		return nil, false
	}
	report, gate := inspectDraftAIGCGate(st, chapter, content)
	if base == nil || !RequiresRegisteredExternalRetest(base) {
		required, routeErr := draftAIGCWholeDraftRerenderRequired(st, chapter, content, report, gate)
		if routeErr != nil || !required {
			return nil, false
		}
	}
	if !draftAIGCHasWholeTextStructuralBlock(content, report, gate) {
		return nil, false
	}
	requirement := draftAIGCRerenderRequirement(chapter, content, draftAIGCRawLocalGateResult(report, gate))
	if base != nil {
		if RequiresRegisteredExternalRetest(base) {
			for _, identity := range registeredExternalRetestIdentities(base) {
				requirement.RequiredExternalRetests = appendRegisteredExternalRetestIdentity(requirement.RequiredExternalRetests, identity)
			}
			requirement.RequiredDetector = strings.TrimSpace(base.RequiredDetector)
			requirement.RequiredMode = strings.TrimSpace(base.RequiredMode)
			requirement.ExternalRetestPolicy = DraftExternalRetestPolicyAutomatedHard
			requirement.BlockUntilExternalRetest = true
		}
		requirement.InitialDraftBodySHA256 = strings.TrimSpace(base.InitialDraftBodySHA256)
	}
	return &requirement, true
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
	report, gate := inspectDraftAIGCGate(st, chapter, content)
	rawGate := draftAIGCRawLocalGateResult(report, gate)
	if rawGate.Passed {
		return nil
	}
	if draftAIGCExternalProbabilityOnlySatisfied(content, report, gate) {
		return nil
	}
	if draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, chapter, content, report, gate) {
		return nil
	}
	if err := persistDraftAIGCRerenderRequirement(st, chapter, content, report, gate); err != nil {
		return fmt.Errorf("第 %d 章 AIGC 阻断已确认，但整章重渲染标记写入失败: %v: %w", chapter, err, errs.ErrStoreWrite)
	}
	if err := checkpointDraftStructuralBlock(st, chapter, content, report, gate); err != nil {
		return fmt.Errorf("第 %d 章 AIGC 结构阻断已确认，但迭代 checkpoint 写入失败: %v: %w", chapter, err, errs.ErrStoreWrite)
	}
	focus := strings.Join(rawGate.RewriteFocus, "；")
	external := "none"
	if gate.ExternalAIProbabilityPercent != nil {
		external = fmt.Sprintf("%.2f%%", *gate.ExternalAIProbabilityPercent)
	}
	return fmt.Errorf("%s: %w", draftAIGCLocalGateFailureMessage(chapter, gate, rawGate, external, focus), errs.ErrToolPrecondition)
}

func draftAIGCLocalGateFailureMessage(chapter int, gate, rawGate draftAIGCGateResult, external, focus string) string {
	return fmt.Sprintf(
		"第 %d 章草稿本地 AIGC raw 门禁 %.2f%% 未达到严格 <%.0f%%；禁止覆盖终稿。raw_local=%.2f%% external_calibrated_effective=%.2f%% external=%s corroboration_blockers=%v calibration=%q。请保持当前 world simulation 与 plan，先按 rewrite_focus 使用 edit_chapter 重排整章，再 read_chapter + check_consistency：%s",
		chapter, rawGate.EffectiveGatePercent, rawGate.PassExclusivePercent, rawGate.RawLocalGatePercent,
		gate.EffectiveGatePercent, external, gate.CorroborationBlockedBy, gate.Calibration, focus,
	)
}

// checkpointDraftStructuralBlock records one distinct whole-draft structural
// failure per causal epoch. The digest binds body hash + current plan/simulation
// epoch: repeated checks in one cycle remain idempotent, while the same prose
// reappearing after a genuinely new causal plan is counted in that new budget.
func checkpointDraftStructuralBlock(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(content) == "" {
		return nil
	}
	if !draftAIGCHasWholeTextStructuralBlock(content, report, gate) {
		return nil
	}
	if draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		return nil
	}
	if pending, err := draftAIGCManagedProviderPendingWholeText(st, chapter, content, report, gate); err != nil {
		return err
	} else if pending {
		return nil
	}
	// Persist the exact failed body into the plan-owned convergence ledger before
	// returning control to the Host. This must precede checkpoint idempotence: a
	// process crash may leave the checkpoint durable while the older build never
	// wrote the outer ledger, and a retry must repair that missing record.
	if err := recordRenderConvergenceStructuralBlock(st, chapter, content); err != nil {
		return fmt.Errorf("record plan-owned structural convergence failure: %w", err)
	}
	bodyHash := reviewreport.BodySHA256(content)
	epochKey := renderOnlyCausalEpochKey(st, chapter)
	structuralDigest := "sha256:" + reviewreport.BodySHA256(bodyHash+"\n"+epochKey)
	legacyDigest := "sha256:" + bodyHash
	boundary := renderOnlyCausalBoundary(st, chapter)
	for _, cp := range st.Checkpoints.All() {
		if cp.Seq <= boundary || !cp.Scope.Matches(domain.ChapterScope(chapter)) || cp.Step != "draft-structural-block" {
			continue
		}
		// Builds predating causal-epoch digests stored the raw body digest. Treat
		// it as the same attempt during in-place upgrades instead of consuming a
		// second slot for unchanged prose.
		if cp.Digest == legacyDigest || cp.Digest == structuralDigest {
			return nil
		}
	}
	_, err := st.Checkpoints.Append(
		domain.ChapterScope(chapter),
		"draft-structural-block",
		fmt.Sprintf("drafts/%02d.draft.md", chapter),
		structuralDigest,
	)
	return err
}

func persistDraftAIGCRerenderRequirement(st *store.Store, chapter int, content string, report aigc.Report, gate draftAIGCGateResult) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(content) == "" {
		return nil
	}
	rawGate := draftAIGCRawLocalGateResult(report, gate)
	// The durable marker grants a one-shot *whole chapter* replacement. Soft
	// local proxies, legacy cadence scores and content-integrity diagnostics are
	// still commit blockers, but they remain repairable through edit_chapter.
	// Persisting a full-render marker for those cases makes the returned edit
	// instruction impossible to follow and can create an unbounded rerender loop.
	if len(rawGate.RewriteFocus) == 0 || !draftAIGCHasWholeTextStructuralBlock(content, report, rawGate) {
		return nil
	}
	if draftAIGCExternalProbabilityComponentSatisfied(content, report, gate) {
		return nil
	}
	if pending, err := draftAIGCManagedProviderPendingWholeText(st, chapter, content, report, rawGate); err != nil {
		return err
	} else if pending {
		return nil
	}
	return SetDraftExternalRerenderRequirement(st.Dir(), draftAIGCRerenderRequirement(chapter, content, rawGate))
}

func draftAIGCRerenderRequirement(chapter int, content string, gate draftAIGCGateResult) DraftExternalRerenderRequirement {
	evidence := []string{
		fmt.Sprintf("raw_local=%.2f%% effective=%.2f%%", gate.RawLocalGatePercent, gate.EffectiveGatePercent),
	}
	if len(gate.CorroborationBlockedBy) > 0 {
		evidence = append(evidence, "corroboration_blockers="+strings.Join(gate.CorroborationBlockedBy, ","))
	}
	if gate.ExternalAIProbabilityPercent != nil {
		evidence = append(evidence, fmt.Sprintf("current_hash_external=%.2f%%，但整章或确定性结构证据仍阻断校准", *gate.ExternalAIProbabilityPercent))
	}
	return DraftExternalRerenderRequirement{
		Chapter:              chapter,
		EvaluatedBodySHA256:  reviewreport.BodySHA256(content),
		Source:               "local_mechanical_gate",
		AIProbabilityPercent: int(math.Round(gate.RawLocalGatePercent)),
		PassExclusivePercent: int(gate.PassExclusivePercent),
		Summary:              "当前草稿的整章单段或确定性结构风险未被同哈希外审消解，必须复用既有世界模拟与 plan 做完整重渲染，不能对原哈希重复提交。",
		Evidence:             evidence,
		RevisionPlan:         append([]string(nil), gate.RewriteFocus...),
		AdviceComplete:       true,
	}
}

func draftQualityGateNextStep(wordContract chapterWordContractResult, gate draftAIGCGateResult) string {
	if !wordContract.Passed {
		return draftWordContractNextStep(wordContract)
	}
	if gate.ExternalAIProbabilityPercent != nil && gate.Passed {
		return draftWordContractNextStep(wordContract)
	}
	if gate.Enforced && gate.RawLocalGatePercent >= gate.PassExclusivePercent {
		return "先 read_chapter(source=draft)，按 aigc_gate.rewrite_focus 用 edit_chapter 重排正文，再调用 check_consistency；本地 AIGC 严格 <4% 前禁止 commit_chapter。"
	}
	return draftWordContractNextStep(wordContract)
}
