package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContinuationHistoryRootPreservesKnowledgeAndLegacyFormula(t *testing.T) {
	old := CharacterSelfExperienceV2{ID: "old-result", TaskID: "setup", Action: "actual setup", Status: "completed"}
	current := CharacterSelfExperienceV2{ID: "work-result", TaskID: "inspection", Status: "in_progress"}
	o := CharacterObservationPacket{AgentID: "owner", Character: "甲", Location: "现场", CurrentGoal: "检查", SelfExperiences: []CharacterSelfExperienceV2{old, current}}
	physical := WorldPhysicalStateV2{Actors: []CharacterPhysicalStateV2{{AgentID: o.AgentID, SelfExperiences: []CharacterSelfExperienceV2{old, current}}}}
	state := continuationStateV1{observation: o, task: CharacterSelfTaskV2{TaskID: current.TaskID}}
	legacy, err := continuationInformationRootV1(o, state, &physical)
	if err != nil {
		t.Fatal(err)
	}
	// Frozen pre-policy formula: display history excluding only the current task.
	want, err := characterAgentDigest(struct {
		Agent, Character, Location, Goal, Pressure string
		Facts, Events, Rules                       []CharacterAgentFact
		Mechanisms                                 []CodexMechanism
		Resources                                  []CharacterResourceViewV2
		Relations, Commitments                     []string
		Memory                                     []CharacterAgentMemoryFact
		Experiences                                []CharacterSelfExperienceV2
		Tasks                                      []CharacterTaskProgressV2
		Operational                                []CharacterOperationalObservationV1
		Received                                   []CharacterReceivedFactV2
		ActualOwnerObservations                    []CharacterOperationalObservationV1
	}{o.AgentID, o.Character, o.Location, o.CurrentGoal, "", nil, nil, nil, nil, nil, nil, nil, nil, []CharacterSelfExperienceV2{old}, nil, nil, nil, nil})
	if err != nil || legacy != want {
		t.Fatalf("legacy information root changed: %s != %s (%v)", legacy, want, err)
	}
	clipped := continuationCloneV1(o)
	clipped.SelfExperiences = []CharacterSelfExperienceV2{current}
	if root, err := continuationInformationRootV1(clipped, state, &physical); err != nil || root == legacy {
		t.Fatalf("old grant silently acquired the new eviction behavior: %s %v", root, err)
	}
	o.Sources = []string{CharacterWorkContinuationHistoryPolicyV1}
	state.observation = o
	clipped.Sources = o.Sources
	before, _ := json.Marshal(struct {
		O CharacterObservationPacket
		P WorldPhysicalStateV2
	}{o, physical})
	root, err := continuationInformationRootV1(o, state, &physical)
	if err != nil || root == legacy {
		t.Fatalf("new policy was not independently identified: %s %v", root, err)
	}
	if got, err := continuationInformationRootV1(clipped, state, &physical); err != nil || got != root {
		t.Fatalf("display eviction changed full-history root: %s %v", got, err)
	}
	for _, tc := range []struct {
		name string
		edit func(*CharacterObservationPacket, *WorldPhysicalStateV2)
	}{
		{"new unrelated experience", func(_ *CharacterObservationPacket, p *WorldPhysicalStateV2) {
			p.Actors[0].SelfExperiences = append(p.Actors[0].SelfExperiences, CharacterSelfExperienceV2{ID: "new-result", TaskID: "other", Status: "completed"})
		}},
		{"changed undisplayed experience", func(_ *CharacterObservationPacket, p *WorldPhysicalStateV2) {
			p.Actors[0].SelfExperiences[0].Status = "blocked"
		}},
		{"new known fact", func(o *CharacterObservationPacket, _ *WorldPhysicalStateV2) {
			o.KnownFacts = append(o.KnownFacts, CharacterAgentFact{ID: "report", Text: "本人刚收到的新事实"})
		}},
		{"changed known fact", func(o *CharacterObservationPacket, _ *WorldPhysicalStateV2) {
			o.CurrentGoal = "因新情况暂停检查"
		}},
		{"unverified work feedback", func(o *CharacterObservationPacket, _ *WorldPhysicalStateV2) {
			o.Memory = append(o.Memory, CharacterAgentMemoryFact{Kind: "projected_decision", Text: "并非已验真的正常工作反馈", SourceDigest: "foreign"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oo, pp := continuationCloneV1(clipped), continuationCloneV1(physical)
			tc.edit(&oo, &pp)
			got, err := continuationInformationRootV1(oo, state, &pp)
			if err != nil || got == root {
				t.Fatalf("genuine information change was suppressed: %s %v", got, err)
			}
		})
	}
	for _, missing := range []*WorldPhysicalStateV2{nil, {}} {
		if _, err := continuationInformationRootV1(o, state, missing); err == nil {
			t.Fatal("display list substituted for missing verified owner history")
		}
	}
	clipped.Sources = nil
	if _, err := continuationInformationRootV1(clipped, state, &physical); err == nil {
		t.Fatal("current observation removed the origin's history policy")
	}
	after, _ := json.Marshal(struct {
		O CharacterObservationPacket
		P WorldPhysicalStateV2
	}{o, physical})
	if string(before) != string(after) {
		t.Fatal("information hashing changed observation or physical history")
	}
}

func TestContinuationHistoryPolicyCannotMixWithinGeneration(t *testing.T) {
	for _, full := range []bool{false, true} {
		valid := resourceTimeGenerationInventory(t, false)
		if full {
			visitResourceTimeInventorySources(&valid, func(s *[]string) { *s = append(*s, CharacterWorkContinuationHistoryPolicyV1) })
		}
		if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV3, valid); err != nil {
			t.Fatal(err)
		}
		count := 0
		visitResourceTimeInventorySources(&valid, func(*[]string) { count++ })
		for target := range count {
			changed := continuationCloneV1(valid)
			index := 0
			visitResourceTimeInventorySources(&changed, func(s *[]string) {
				if index == target {
					if full {
						var kept []string
						for _, v := range *s {
							if v != CharacterWorkContinuationHistoryPolicyV1 {
								kept = append(kept, v)
							}
						}
						*s = kept
					} else {
						*s = append(*s, CharacterWorkContinuationHistoryPolicyV1)
					}
				}
				index++
			})
			if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV3, changed); err == nil || !strings.Contains(err.Error(), "continuation history policy") {
				t.Fatalf("mixed generation marker accepted at %d, full=%v: %v", target, full, err)
			}
		}
	}
	legacy := generationExecutionPolicySources(t, true)
	visitResourceTimeInventorySources(&legacy, func(s *[]string) { *s = append(*s, CharacterWorkContinuationHistoryPolicyV1) })
	if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV2, legacy); err == nil {
		t.Fatal("new history policy entered a legacy generation")
	}
}

func TestContinuationHistoryGenerationCannotSwitchOnNextChapter(t *testing.T) {
	for _, full := range []bool{false, true} {
		previous := resourceTimeGenerationInventory(t, false)
		if full {
			visitResourceTimeInventorySources(&previous, func(s *[]string) { *s = append(*s, CharacterWorkContinuationHistoryPolicyV1) })
		}
		current := continuationCloneV1(previous)
		current.Chapter++
		if err := validateGenerationContinuationHistoryPredecessor(previous, current); err != nil {
			t.Fatal(err)
		}
		if full {
			current.CharacterActivationEvidence.ProtocolDigest = "different-producer"
			if err := validateGenerationContinuationHistoryPredecessor(previous, current); err == nil {
				t.Fatal("new generation mixed producer digests across chapters")
			}
			current.ChapterWorldSimulation.Sources = nil
		} else {
			current.ChapterWorldSimulation.Sources = append(current.ChapterWorldSimulation.Sources, CharacterWorkContinuationHistoryPolicyV1)
		}
		if err := validateGenerationContinuationHistoryPredecessor(previous, current); err == nil {
			t.Fatal("generation switched history strategy at next chapter")
		}
	}
}
