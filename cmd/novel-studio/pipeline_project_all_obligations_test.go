package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPipelineProjectAllCharacterObligationsPreserveIndependentSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.CharacterWorldDecision)
	}{
		{"actor", func(d *domain.CharacterWorldDecision) { d.Character = "乙" }},
		{"intent", func(d *domain.CharacterWorldDecision) { d.Decision = "拒绝交付" }},
		{"action", func(d *domain.CharacterWorldDecision) { d.Action = "寄出副本" }},
		{"arrival", func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].ArrivalChapter = 3 }},
		{"targets", func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].Targets = []string{"丙"} }},
		{"delivery", func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].TransmissionPath = "电报" }},
		{"visibility", func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].Visibility = "hidden" }},
		{"impact", func(d *domain.CharacterWorldDecision) {
			d.ButterflyEffects[0].ProtagonistImpact = "迫使主角暂缓出发"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
			registry.Obligations = nil
			first := projectAllCharacterObligationTestDecision()
			second := projectAllCharacterObligationTestDecision()
			tc.change(&second)
			sim := domain.ChapterWorldSimulation{
				CharacterAgentProtocol: &domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolV2Version},
				CharacterDecisions:     []domain.CharacterWorldDecision{first, second, first},
			}
			plan := domain.ChapterPlan{Chapter: 1, Hook: "这是章末钩子，不是角色后效的来源"}
			created, updated, err := pipelineProjectAllCreateObligations(generation, sim, plan, registry)
			if err != nil {
				t.Fatal(err)
			}
			if len(created) != 2 || len(updated.Obligations) != 2 || created[0] == created[1] {
				t.Fatalf("independent sources collapsed or exact replay duplicated: ids=%v obligations=%+v", created, updated.Obligations)
			}
			for i, decision := range []domain.CharacterWorldDecision{first, second} {
				obligation := updated.Obligations[i]
				if !strings.HasPrefix(obligation.Contract, decision.ButterflyEffects[0].Effect) ||
					obligation.DueWindow.FromChapter != decision.ButterflyEffects[0].ArrivalChapter ||
					obligation.DueWindow.ToChapter != decision.ButterflyEffects[0].ArrivalChapter ||
					obligation.Origin.SourceDigest == pipelineProjectAllDigest(plan.Hook) ||
					obligation.Evidence[0].SourceDigest != obligation.Origin.SourceDigest {
					t.Fatalf("lost exact source or consumer: %+v", obligation)
				}
			}
			updated.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(updated)
			if err != nil {
				t.Fatal(err)
			}
			if err := domain.ValidateObligationRegistryV2(updated); err != nil {
				t.Fatal(err)
			}
			replayedIDs, replayed, err := pipelineProjectAllCreateObligations(generation, sim, plan, updated)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(created, replayedIDs) || !reflect.DeepEqual(updated, replayed) {
				t.Fatalf("registry-before-bundle recovery changed the result: ids=%v/%v", created, replayedIDs)
			}
		})
	}
}

func TestPipelineProjectAllCharacterObligationTargetsAreASet(t *testing.T) {
	sim := domain.ChapterWorldSimulation{CharacterAgentProtocol: &domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolV2Version}}
	decision := projectAllCharacterObligationTestDecision()
	first := decision.ButterflyEffects[0]
	first.Targets = []string{"甲", "乙", "甲"}
	second := first
	second.Targets = []string{"乙", "甲"}
	contractA, sourceA := pipelineProjectAllCharacterObligationSource(sim, decision, first)
	contractB, sourceB := pipelineProjectAllCharacterObligationSource(sim, decision, second)
	if contractA != contractB || sourceA != sourceB {
		t.Fatal("target order or duplicate reference changed consequence identity")
	}
}

func TestPipelineProjectAllLegacyCharacterObligationIdentityAndReplay(t *testing.T) {
	for _, version := range []string{"", domain.CharacterAgentDecisionProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
			registry.Obligations = nil
			decision := projectAllCharacterObligationTestDecision()
			sim := domain.ChapterWorldSimulation{CharacterDecisions: []domain.CharacterWorldDecision{decision, decision}}
			if version != "" {
				sim.CharacterAgentProtocol = &domain.CharacterAgentProtocolReceipt{Version: version}
			}
			plan := domain.ChapterPlan{Chapter: 1, Hook: "保持旧摘要"}
			created, updated, err := pipelineProjectAllCreateObligations(generation, sim, plan, registry)
			if err != nil {
				t.Fatal(err)
			}
			wantID, err := domain.DeriveObligationIDV2(domain.ObligationCharacterV2, plan.Chapter, decision.ButterflyEffects[0].Effect)
			if err != nil {
				t.Fatal(err)
			}
			if len(created) != 1 || created[0] != wantID || len(updated.Obligations) != 1 || updated.Obligations[0].Contract != decision.ButterflyEffects[0].Effect {
				t.Fatalf("legacy identity changed or duplicate ID retained: %+v", updated)
			}
			replayedIDs, replayed, err := pipelineProjectAllCreateObligations(generation, sim, plan, updated)
			if err != nil || !reflect.DeepEqual(created, replayedIDs) || !reflect.DeepEqual(updated, replayed) {
				t.Fatalf("legacy recovery is not exact: ids=%v err=%v", replayedIDs, err)
			}
			sim.CharacterDecisions = []domain.CharacterWorldDecision{projectAllCharacterObligationTestDecision()}
			sim.CharacterDecisions[0].ButterflyEffects[0].ArrivalChapter = 3
			if _, _, err := pipelineProjectAllCreateObligations(generation, sim, plan, updated); err == nil || !strings.Contains(err.Error(), "conflicting") {
				t.Fatalf("silently discarded a changed consumer: %v", err)
			}
		})
	}
}

func projectAllCharacterObligationTestDecision() domain.CharacterWorldDecision {
	return domain.CharacterWorldDecision{
		Character: "甲", Decision: "交付原件", Action: "托人送出原件",
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect: "原件到达后改变主角的选择", Targets: []string{"主角"}, TransmissionPath: "当面送达",
			ArrivalChapter: 2, Visibility: "visible", ProtagonistImpact: "迫使主角公开旧规则",
		}},
	}
}
