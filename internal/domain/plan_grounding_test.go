package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func groundingTestInput(t *testing.T) PlanGroundingInput {
	t.Helper()
	sim := ChapterWorldSimulation{Version: 2, Chapter: 1, GenerationID: "pg2_grounding", SimulationID: "ch001-grounding", TimeWindow: "0-5 minutes", ProtagonistProjection: ProtagonistDecisionProjection{Protagonist: "林澄", ChosenDecision: "留在仓库核验"}, CharacterAgentProtocol: &CharacterAgentProtocolReceipt{ArbitrationDigest: "arb"}}
	plan := ChapterPlan{Chapter: 1, Goal: "T+17到达值班室", Contract: ChapterContract{RequiredBeats: []string{"读完未交付的R03"}}, CausalSimulation: ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	obs := CharacterObservationPacket{AgentID: "character_lin", Character: "林澄", Chapter: 1, GenerationID: sim.GenerationID}
	arb := WorldArbitrationReceipt{GenerationID: sim.GenerationID, Chapter: 1, Finalized: true, Digest: "arb", Resolutions: []CharacterDecisionResolution{{AgentID: obs.AgentID, Character: obs.Character, ImmediateResult: "留在仓库；只读R01，未取得R03"}}}
	input, err := NewPlanGroundingInput(plan, sim, obs, arb, "sha256:"+strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func groundingTestFinding() PlanGroundingFinding {
	return PlanGroundingFinding{Kind: "knowledge", PlanPath: "/plan/contract/required_beats/0", PlanQuote: "读完未交付的R03", SourcePath: "/arbitration/resolutions/0/immediate_result", SourceQuote: "只读R01，未取得R03", Explanation: "计划新增了未交付材料的阅读结果"}
}

func TestPlanGroundingReceiptRequiresCitableClassification(t *testing.T) {
	input := groundingTestInput(t)
	for name, edit := range map[string]func(*PlanGroundingVerdict){
		"pass_with_findings": func(v *PlanGroundingVerdict) { v.Pass = true },
		"empty_failure":      func(v *PlanGroundingVerdict) { v.Findings = nil },
		"invented_quote":     func(v *PlanGroundingVerdict) { v.Findings[0].SourceQuote = "已经取得R03" },
		"wrong_source":       func(v *PlanGroundingVerdict) { v.Findings[0].SourcePath = v.Findings[0].PlanPath },
		"wrong_pointer":      func(v *PlanGroundingVerdict) { v.Findings[0].PlanPath = "/plan/contract/required_beats/9" },
		"unknown_kind":       func(v *PlanGroundingVerdict) { v.Findings[0].Kind = "literary_taste" },
		"private_reasoning":  func(v *PlanGroundingVerdict) { v.Findings[0].Explanation = strings.Repeat("推理", 501) },
	} {
		t.Run(name, func(t *testing.T) {
			v := PlanGroundingVerdict{Findings: []PlanGroundingFinding{groundingTestFinding()}}
			edit(&v)
			if _, err := FinalizePlanGroundingReceipt(input, v); err == nil {
				t.Fatal("accepted invalid verdict")
			}
		})
	}
	for _, v := range []PlanGroundingVerdict{{Pass: true}, {Findings: []PlanGroundingFinding{groundingTestFinding()}}} {
		receipt, err := FinalizePlanGroundingReceipt(input, v)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlanGroundingAudit(PlanGroundingAudit{input, receipt}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanGroundingBindsEveryInputAndRemainsLegacyOptional(t *testing.T) {
	input := groundingTestInput(t)
	receipt, err := FinalizePlanGroundingReceipt(input, PlanGroundingVerdict{Pass: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*PlanGroundingInput){
		"plan":        func(v *PlanGroundingInput) { v.Plan.Hook = "新发生的泄密" },
		"simulation":  func(v *PlanGroundingInput) { v.Simulation.TimeWindow = "17分钟" },
		"observation": func(v *PlanGroundingInput) { v.POVObservation.Location = "值班室" },
		"arbitration": func(v *PlanGroundingInput) { v.Arbitration.Finalized = false },
		"protocol":    func(v *PlanGroundingInput) { v.ReviewProtocol = "sha256:" + strings.Repeat("2", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := input
			edit(&copy)
			if err := ValidatePlanGroundingAudit(PlanGroundingAudit{copy, receipt}); err == nil {
				t.Fatal("tampered evidence accepted")
			}
		})
	}
	if err := ValidatePlanGroundingBundle(ChapterPlan{}, ChapterWorldSimulation{Version: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanGroundingBundle(ChapterPlan{}, ChapterWorldSimulation{Version: 2, Sources: []string{PlanGroundingPolicyV1}}, nil); err == nil {
		t.Fatal("new policy bypassed")
	}
	if err := ValidateGenerationCharacterProtocolV2(PlanningGenerationV2{PlanGroundingPolicy: PlanGroundingPolicyV1}, ProjectedChapterBundle{}); err == nil {
		t.Fatal("stripped policy bypassed generation")
	}
	raw, _ := json.Marshal(ChapterPlan{Chapter: 1})
	if strings.Contains(string(raw), "grounding_review") {
		t.Fatal("legacy JSON changed")
	}
}

func TestPlanGroundingRenderContractCannotAddPostReviewEvents(t *testing.T) {
	base := ProjectedChapterBundle{ChapterWorldSimulation: ChapterWorldSimulation{Sources: []string{PlanGroundingPolicyV1}}, ChapterPlan: ChapterPlan{Contract: ChapterContract{RequiredBeats: []string{"只读取R01"}, ForbiddenMoves: []string{"不读取R03"}, ContinuityChecks: []string{"留在柜台"}}}, HardRenderContract: HardRenderContractV2{MustOccur: []string{"只读取R01"}, MustNotOccur: []string{"不读取R03"}, MustPreserve: []string{"留在柜台"}}}
	if err := ValidateGroundedRenderContract(base); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*ProjectedChapterBundle){
		"occur":    func(b *ProjectedChapterBundle) { b.HardRenderContract.MustOccur = []string{"读取R03"} },
		"forbid":   func(b *ProjectedChapterBundle) { b.HardRenderContract.MustNotOccur = nil },
		"preserve": func(b *ProjectedChapterBundle) { b.HardRenderContract.MustPreserve = []string{"到达值班室"} },
		"reveal": func(b *ProjectedChapterBundle) {
			b.HardRenderContract.RevealBudget = []RevealBudgetItemV2{{Limit: "可以揭露隐藏量"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := base
			edit(&b)
			if err := ValidateGroundedRenderContract(b); err == nil {
				t.Fatal("post-review contract mutation accepted")
			}
		})
	}
}
