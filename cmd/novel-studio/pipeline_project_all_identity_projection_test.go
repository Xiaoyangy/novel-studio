package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func identityProjectionFixture() (domain.ChapterWorldSimulation, domain.ChapterPlan, *domain.WorldPhysicalStateV2) {
	actors := []domain.CharacterPhysicalStateV2{
		{AgentID: "ca_lin", Character: "林澄", Location: "油料仓库", Resources: []domain.CharacterResourceHoldingV2{}},
		{AgentID: "ca_xu", Character: "许岚", Location: "油料仓库", Resources: []domain.CharacterResourceHoldingV2{}},
		{AgentID: "ca_zhou", Character: "周砚", Location: "机修棚", Resources: []domain.CharacterResourceHoldingV2{}},
	}
	before := &domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Actors: append([]domain.CharacterPhysicalStateV2(nil), actors...)}
	actors[2].Location = "停泊的渡船"
	sim := domain.ChapterWorldSimulation{Version: 2, Chapter: 1, SimulationID: "identity-projection", TimeWindow: "T+0—5",
		PhysicalState:         &domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Actors: actors},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{Protagonist: "林澄", ChosenDecision: "柜台限时核对，不签未核数量", AvailableOptions: []string{"柜台核对", "回值班室"}, HiddenPressures: []string{"许岚自己的既往行为尚未告知林澄"}},
	}
	// The actual incident had Zhou first and the POV character last.
	for _, index := range []int{2, 1, 0} {
		actor := actors[index]
		choice := map[string]string{"林澄": sim.ProtagonistProjection.ChosenDecision, "周砚": "先安全登船完成机务检查", "许岚": "先护货且不主动披露R04"}[actor.Character]
		sim.CharacterDecisions = append(sim.CharacterDecisions, domain.CharacterWorldDecision{Character: actor.Character, Location: actor.Location, PostState: &actor, Decision: choice, Action: choice, CurrentGoal: actor.Character + "自己的目标", Pressure: actor.Character + "自己的压力", AvailableOptions: []string{choice, "本人的另一选择"}, StateAfter: actor.Location + "中的本人后态", ImmediateResult: "本人的实际后果"})
	}
	plan := domain.ChapterPlan{Chapter: 1, Goal: "当前柜台结果", CausalSimulation: domain.ChapterCausalSimulation{
		ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision,
		InitialState:        []domain.CharacterSimulationState{{Character: "周砚"}, {Character: "林澄", LikelyAction: "旧软计划：去机修棚读R03"}},
		ReaderRetentionPlan: domain.ReaderRetentionPlan{SurfaceBeats: []domain.RetentionSurfaceBeat{{MustShow: "柜台争议"}, {MustShow: "请求尚待回应"}}},
	}}
	return sim, plan, before
}

func TestProjectAllIdentityProjectionDoesNotBorrowAnotherActorsLocationOrDecision(t *testing.T) {
	sim, plan, before := identityProjectionFixture()
	var causalIDs map[string]string
	for _, order := range [][]int{{0, 1, 2}, {2, 0, 1}, {1, 2, 0}} {
		copy := sim
		copy.CharacterDecisions = nil
		for _, index := range order {
			copy.CharacterDecisions = append(copy.CharacterDecisions, sim.CharacterDecisions[index])
		}
		pov := pipelinePOVPlanV2(copy, plan)
		choices := map[string]string{}
		for _, item := range pov.Motivations {
			choices[item.CharacterID] = item.Choice
		}
		for _, decision := range copy.CharacterDecisions {
			if choices[decision.Character] != decision.Decision {
				t.Fatalf("decision reassigned by order or stale initial plan: %v", choices)
			}
		}
		for _, scene := range pov.Scenes {
			if scene.Location != "油料仓库" || !reflect.DeepEqual(scene.PresentActors, []string{"林澄"}) {
				t.Fatalf("POV borrowed another actor location: %+v", scene)
			}
		}
		foundZhou := false
		for _, state := range pov.OffscreenStates {
			if state.CharacterID == "周砚" {
				foundZhou = state.State == "停泊的渡船中的本人后态"
			}
		}
		if !foundZhou {
			t.Fatal("Zhou's own offscreen ship state was lost")
		}
		formal := pipelineFormalWorldSimulationV2(copy, before)
		initialLocations := map[string]string{}
		initialIDs := map[string]string{}
		for _, fact := range formal.InitialConditions {
			if fact.Field == "location" {
				initialLocations[fact.Subject] = fact.Value
				initialIDs[fact.Subject] = fact.ID
			}
		}
		if initialLocations["林澄"] != "油料仓库" || initialLocations["周砚"] != "机修棚" {
			t.Fatalf("formal origins mixed with order/post state: %v", initialLocations)
		}
		currentIDs := map[string]string{}
		for _, step := range formal.CausalSteps {
			if len(step.CauseIDs) != 1 || step.CauseIDs[0] != initialIDs[step.ActorID] || step.Decision != choices[step.ActorID] {
				t.Fatalf("formal cause/choice belongs to another actor: %+v", step)
			}
			currentIDs[step.ActorID] = step.ID
		}
		if causalIDs != nil && !reflect.DeepEqual(causalIDs, currentIDs) {
			t.Fatal("formal causal identity depends on actor array position")
		}
		causalIDs = currentIDs
		for _, actor := range formal.Actors {
			if actor.CharacterID != "林澄" && len(actor.UnknownFacts) > 0 {
				t.Fatal("POV unknowns copied into another actor's ignorance")
			}
		}
	}
	sim.ProtagonistProjection.Protagonist = "不在裁决中的人"
	if firstProjectAllLocation(sim) != "" {
		t.Fatal("missing protagonist borrowed an unrelated location")
	}
}

func TestProjectAllActualC1IdentityProjectionReadonlyAudit(t *testing.T) {
	path := os.Getenv("NOVEL_STUDIO_POV_IDENTITY_AUDIT_BUNDLE")
	if path == "" {
		t.Skip("set the explicit historical C1 bundle for read-only audit")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ProjectedChapterBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	pov := pipelinePOVPlanV2(bundle.ChapterWorldSimulation, bundle.ChapterPlan)
	for _, scene := range pov.Scenes {
		if scene.Location != "油料仓库" {
			t.Fatal("real C1 still gives Zhou's ship to Lin")
		}
	}
	for _, motivation := range pov.Motivations {
		decision, ok := projectAllDecisionForCharacter(bundle.ChapterWorldSimulation, motivation.CharacterID)
		if !ok || motivation.Choice != decision.Decision {
			t.Fatal("real C1 has crossed/stale choices")
		}
	}
	formal := pipelineFormalWorldSimulationV2(bundle.ChapterWorldSimulation, bundle.CharacterAgentEvidence.Stimulus.PhysicalState)
	for _, fact := range formal.InitialConditions {
		if fact.Field == "location" && fact.Subject == "周砚" && fact.Value != "机修棚" {
			t.Fatal("real C1 Zhou origin became terminal location")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(raw) != string(after) {
		t.Fatal("identity audit changed historical bundle")
	}
}

func TestProjectAllTypedIdentityValidationIsGroundingPolicyBound(t *testing.T) {
	sim, plan, before := identityProjectionFixture()
	sim.Sources = []string{domain.PlanGroundingPolicyV1}
	valid := domain.ProjectedChapterBundle{Chapter: 1, ChapterWorldSimulation: sim, ChapterPlan: plan,
		CharacterAgentEvidence: &domain.CharacterAgentEvidenceBundle{Stimulus: domain.WorldStimulusPacket{PhysicalState: before}},
		FormalWorldSimulation:  pipelineFormalWorldSimulationV2(sim, before), POVPlan: pipelinePOVPlanV2(sim, plan)}
	if err := domain.ValidateProjectedIdentityBindingsV2(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*domain.ProjectedChapterBundle)
	}{
		{"wrong_pov", func(b *domain.ProjectedChapterBundle) { b.POVPlan.POVCharacterID = "周砚" }},
		{"crossed_choice", func(b *domain.ProjectedChapterBundle) {
			b.POVPlan.Motivations[0].Choice = sim.ProtagonistProjection.ChosenDecision
		}},
		{"borrowed_location", func(b *domain.ProjectedChapterBundle) { b.POVPlan.Scenes[0].Location = "停泊的渡船" }},
		{"crossed_causal_origin", func(b *domain.ProjectedChapterBundle) {
			b.FormalWorldSimulation.CausalSteps[0].CauseIDs = []string{b.FormalWorldSimulation.InitialConditions[2].ID}
		}},
		{"terminal_as_initial", func(b *domain.ProjectedChapterBundle) {
			for i := range b.FormalWorldSimulation.InitialConditions {
				if b.FormalWorldSimulation.InitialConditions[i].Subject == "周砚" && b.FormalWorldSimulation.InitialConditions[i].Field == "location" {
					b.FormalWorldSimulation.InitialConditions[i].Value = "停泊的渡船"
				}
			}
		}},
		{"wrong_time", func(b *domain.ProjectedChapterBundle) { b.POVPlan.TimeAdvance = "T+0—17" }},
		{"wrong_scene_time", func(b *domain.ProjectedChapterBundle) { b.POVPlan.Scenes[0].Time = "T+8—13" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var bad domain.ProjectedChapterBundle
			if err := json.Unmarshal(raw, &bad); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&bad)
			if err := domain.ValidateProjectedIdentityBindingsV2(bad); err == nil || !strings.Contains(err.Error(), "projection identity") {
				t.Fatalf("typed contradiction accepted: %v", err)
			}
			bad.ChapterWorldSimulation.Sources = nil
			if err := domain.ValidateProjectedIdentityBindingsV2(bad); err != nil {
				t.Fatal("new policy reinterpreted historical unmarked bundle")
			}
		})
	}
}
