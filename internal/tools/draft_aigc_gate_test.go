package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestDraftAIGCGateUsesStrictExclusiveBoundary(t *testing.T) {
	report := aigc.Report{
		Engine:             aigc.Engine,
		AIGCPercent:        3.99,
		BlendedAIGCPercent: 3.99,
		Stats:              aigc.Stats{Hanzi: draftAIGCMinHanzi},
	}
	gate := draftAIGCGateResultFromReport(report)
	if !gate.Enforced || !gate.Passed {
		t.Fatalf("3.99%% should pass strict draft gate: %+v", gate)
	}

	report.AIGCPercent = 4
	report.BlendedAIGCPercent = 4
	gate = draftAIGCGateResultFromReport(report)
	if !gate.Enforced || gate.Passed {
		t.Fatalf("4%% must fail strict draft gate: %+v", gate)
	}
	if len(gate.RewriteFocus) == 0 {
		t.Fatalf("failed draft gate must return rewrite focus: %+v", gate)
	}
}

func TestAIGCDetectorRewriteFocusUsesNarrativeSignals(t *testing.T) {
	report := aigc.Report{
		Stats: aigc.Stats{SentenceCV: 0.42, ParagraphCV: 0.36, ShortSentenceRatio: 0.06},
		LatestDetectorProxy: aigc.DetectorProxy{Components: map[string]aigc.Dimension{
			"narrative_dynamics": {
				Stats: map[string]any{
					"dense_dialogue_windows":     3,
					"max_dialogue_paragraph_run": 7,
					"action_dialogue_lead_ratio": 0.52,
					"interiority_density_per_k":  1.1,
					"logistics_density_per_k":    6.4,
				},
				Signals: []aigc.Signal{
					{Name: "dialogue_conveyor_windows"},
					{Name: "action_dialogue_lead_uniform"},
					{Name: "pov_interiority_thin"},
				},
			},
		}},
	}
	focus := strings.Join(aigcDetectorRewriteFocus(report), "\n")
	for _, want := range []string{"对白传送带", "动作报幕式对白", "主视角仍被经营流程压住", "按焦点真实换段"} {
		if !strings.Contains(focus, want) {
			t.Fatalf("focus missing %q:\n%s", want, focus)
		}
	}
	for _, stale := range []string{"事故触发", "保全/导出", "拒签"} {
		if strings.Contains(focus, stale) {
			t.Fatalf("focus retained genre-specific stale advice %q:\n%s", stale, focus)
		}
	}
}

func TestDraftAIGCGateDoesNotEnforceOnFixtureSizedText(t *testing.T) {
	report := aigc.Report{
		Engine:      aigc.Engine,
		AIGCPercent: 90,
		Stats:       aigc.Stats{Hanzi: draftAIGCMinHanzi - 1},
	}
	gate := draftAIGCGateResultFromReport(report)
	if gate.Enforced || !gate.Passed || len(gate.RewriteFocus) != 0 {
		t.Fatalf("sub-chapter text should report but not enforce: %+v", gate)
	}
}

func TestDraftAIGCGateUsesCurrentHashExternalCorroborationWithoutHidingRawScore(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("林澈把价牌搬到摊前，沈知遥看过通道后点头。", 90)
	writeDraftExternalJudgeStatus(t, st.Dir(), 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	report := aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            11.69,
		ZhuqueCompositePercent: 3.42,
		LegacyHeuristicPercent: 2.71,
		Stats:                  aigc.Stats{Hanzi: 2200},
	}
	gate := corroborateDraftAIGCGate(st, 2, content, report, draftAIGCGateResultFromReport(report))
	if !gate.Passed || !gate.ExternalCorroborated || gate.EffectiveGatePercent != 3.42 {
		t.Fatalf("current-hash external consensus should pass at conservative max: %+v", gate)
	}
	if gate.RawLocalGatePercent != 11.69 || gate.ExternalAIProbabilityPercent == nil || *gate.ExternalAIProbabilityPercent != 3 {
		t.Fatalf("raw and external scores must remain visible: %+v", gate)
	}
	if len(gate.RewriteFocus) != 0 || len(gate.DiagnosticFocus) == 0 {
		t.Fatalf("passing corroboration should demote, not erase, raw diagnostics: %+v", gate)
	}
}

func TestDraftAIGCGateDoesNotCorroborateContentIntegrityRisk(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("林澈把事情办完。", 120)
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	report := aigc.Report{
		Engine: aigc.Engine, AIGCPercent: 12, ContentIntegrityFloor: 70,
		ZhuqueCompositePercent: 3, LegacyHeuristicPercent: 3, Stats: aigc.Stats{Hanzi: 2000},
	}
	gate := corroborateDraftAIGCGate(st, 1, content, report, draftAIGCGateResultFromReport(report))
	if gate.Passed || gate.ExternalCorroborated || !strings.Contains(strings.Join(gate.CorroborationBlockedBy, "\n"), "content_integrity_floor") {
		t.Fatalf("external score must not override deterministic integrity risk: %+v", gate)
	}
}

func TestDraftAIGCGateTreatsMarginalLegacyAsDiagnosticUnderStrongHumanConsensus(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("林澈把价牌收进箱里，沈知遥站在旁边等他。", 90)
	writeDraftExternalJudgeStatus(t, st.Dir(), 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	report := aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            4.8,
		WholeTextSegmentGate:   4,
		ZhuqueCompositePercent: 3.14,
		LegacyHeuristicPercent: 4.8,
		SegmentRiskFloor:       0,
		Stats: aigc.Stats{Hanzi: 2200, HumanAnchor: map[string]any{
			"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
			"score": float64(88), "blockers": []string{},
		}},
		ZhuqueSegmentProxy: aigc.ZhuqueSegmentProxy{
			Enabled: true, HumanRatioPercent: 100, MaxSegmentPercent: 4,
			Segments: []aigc.ZhuqueSegment{{Index: 1, Proportion: 1, AIGCPercent: 4, Category: "人工特征"}},
		},
	}
	gate := corroborateDraftAIGCGate(st, 3, content, report, draftAIGCGateResultFromReport(report))
	if !gate.Passed || !gate.ExternalCorroborated || gate.EffectiveGatePercent != 3.14 {
		t.Fatalf("strong current-hash human consensus should demote marginal legacy score: %+v", gate)
	}
	if gate.RawLocalGatePercent != 4.8 || !strings.Contains(gate.Calibration, "high_local_proxies_diagnostic_only") ||
		!strings.Contains(strings.Join(gate.DiagnosticFocus, "\n"), "旧启发式") {
		t.Fatalf("raw marginal legacy diagnostic must remain visible: %+v", gate)
	}
}

func TestDraftAIGCGateDemotesHighLocalProxiesWhenCurrentHashExternalAndSegmentsPass(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("人群堵在桥头，林澈先把取餐口让出来，沈知遥替他守住另一边。", 70)
	writeDraftExternalJudgeStatus(t, st.Dir(), 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	report := aigc.Report{
		Engine: aigc.Engine, AIGCPercent: 9.18, WholeTextSegmentGate: 2.7,
		ZhuqueCompositePercent: 8.39, SegmentRiskFloor: 2.7, LegacyHeuristicPercent: 9.18,
		Stats: aigc.Stats{Hanzi: 2200, HumanAnchor: map[string]any{
			"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(88), "blockers": []string{},
		}},
		ZhuqueSegmentProxy: aigc.ZhuqueSegmentProxy{
			Enabled: true, HumanRatioPercent: 100, MaxSegmentPercent: 2.7,
			Segments: []aigc.ZhuqueSegment{{Index: 1, Proportion: 1, AIGCPercent: 2.7, Category: "人工特征"}},
		},
	}
	gate := corroborateDraftAIGCGate(st, 3, content, report, draftAIGCGateResultFromReport(report))
	if !gate.Passed || !gate.ExternalCorroborated || gate.EffectiveGatePercent != 2.7 {
		t.Fatalf("safe current-hash external and segment evidence should demote high local proxies: %+v", gate)
	}
	if gate.RawLocalGatePercent != 9.18 || len(gate.DiagnosticFocus) < 2 {
		t.Fatalf("high local proxy diagnostics must remain visible: %+v", gate)
	}
}

func TestDraftAIGCGateKeepsRealWholeSegmentRiskBlocking(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("林澈把事情办完。", 180)
	writeDraftExternalJudgeStatus(t, st.Dir(), 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	report := aigc.Report{
		Engine: aigc.Engine, AIGCPercent: 12, WholeTextSegmentGate: 18,
		ZhuqueCompositePercent: 3, LegacyHeuristicPercent: 5, Stats: aigc.Stats{Hanzi: 2200, HumanAnchor: map[string]any{
			"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
		}},
		ZhuqueSegmentProxy: aigc.ZhuqueSegmentProxy{
			Enabled: true, MaxSegmentPercent: 18,
			Segments: []aigc.ZhuqueSegment{{Index: 1, Proportion: 1, AIGCPercent: 18, Category: "疑似 AI"}},
		},
	}
	gate := corroborateDraftAIGCGate(st, 3, content, report, draftAIGCGateResultFromReport(report))
	if gate.Passed || gate.ExternalCorroborated || !strings.Contains(strings.Join(gate.CorroborationBlockedBy, "\n"), "whole_text_or_segment_risk") {
		t.Fatalf("real whole-segment risk must remain blocking: %+v", gate)
	}
}

func writeDraftExternalJudgeStatus(t *testing.T, projectDir string, chapter int, status draftExternalJudgeStatus) {
	t.Helper()
	dir := filepath.Join(projectDir, "reviews", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter)), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
