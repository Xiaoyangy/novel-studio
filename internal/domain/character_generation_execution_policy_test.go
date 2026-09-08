package domain

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Tests the generation-to-policy gate only. These compact source inventories
// are not claimed to be full arbitration receipts; existing bundle/cycle
// validators independently authenticate their complete immutable evidence.
func generationExecutionPolicySources(t *testing.T, newPolicy bool) ProjectedChapterBundle {
	t.Helper()
	sources := []string{"old-preserved-source"}
	if newPolicy {
		sources = append(sources, CharacterSelfChronologyPolicyV1, CharacterWorkContinuationPolicyV1)
	}
	copySources := func() []string { return append([]string(nil), sources...) }
	bundle := ProjectedChapterBundle{ChapterWorldSimulation: ChapterWorldSimulation{Sources: copySources()}, CharacterActivationEvidence: &CharacterActivationChapterEvidence{}}
	for i := range 2 {
		bundle.CharacterActivationEvidence.Cycles = append(bundle.CharacterActivationEvidence.Cycles, CharacterActivationCycle{Evidence: CharacterAgentEvidenceBundle{Stimulus: WorldStimulusPacket{Sources: copySources()}, Observations: []CharacterObservationPacket{{Sources: copySources()}}}})
		bundle.CharacterActivationEvidence.Inputs = append(bundle.CharacterActivationEvidence.Inputs, CharacterActivationInputSet{Stimulus: WorldStimulusPacket{Sources: copySources()}, Observations: []CharacterObservationPacket{{Sources: copySources()}, {Sources: copySources()}}})
		if newPolicy {
			bundle.CharacterActivationEvidence.Reviews = append(bundle.CharacterActivationEvidence.Reviews, generationExecutionPolicyModelView(t, i+1))
		}
	}
	return bundle
}

// This inventory fixture uses an actual canonical readiness codec binding,
// not a non-nil empty placeholder. Full cycle authentication is tested elsewhere.
func generationExecutionPolicyModelView(t *testing.T, count int) CharacterReadinessReviewAudit {
	t.Helper()
	digest := func(n int) string { return fmt.Sprintf("sha256:%064x", n) }
	context, err := FinalizeCharacterReadinessContext(CharacterReadinessContext{GenerationID: "pg2_policy_fixture", Chapter: 1, POVCharacter: "甲", ArcLastChapter: 3, BookLastChapter: 3,
		SoftOutline: OutlineEntry{Chapter: 1}, HardContracts: []string{"保留实际角色选择"}})
	if err != nil {
		t.Fatal(err)
	}
	input := CharacterReadinessReviewInput{Policy: CharacterReadinessReviewPolicy, ReviewProtocol: digest(100), SessionDigest: digest(101), Context: context, RemainingCycles: 8 - count,
		Trace: CharacterReadinessTrace{FinalPhysicalRoot: digest(102)}}
	input.Requirements, err = characterReadinessRequirements(context)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= count; index++ {
		input.Trace.Cycles = append(input.Trace.Cycles, CharacterReadinessCycleView{Index: index, CycleDigest: digest(index), ArbitrationDigest: digest(index + 10),
			StoryTime: &StoryTimeChapterSchedule{Chapter: 1, StartDay: float64(index - 1), EndDay: float64(index)}, HardContractStatus: "feasible"})
	}
	codec, err := NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{input.Trace.Cycles[count-1].CycleDigest}
	receipt, err := FinalizeCharacterReadinessReview(input, CharacterReadinessVerdict{Decision: "continue", Reason: "检验当前模型视图与规范输入绑定", EvidenceRefs: refs,
		ContractChecks: []CharacterReadinessContractCheck{{ContractID: input.Requirements[0].ID, Status: "preserved", EvidenceRefs: refs}}})
	if err != nil {
		t.Fatal(err)
	}
	binding := codec.Binding()
	audit := CharacterReadinessReviewAudit{Input: input, Receipt: receipt, ModelView: &binding}
	if err := ValidateCharacterReadinessReviewAudit(audit); err != nil {
		t.Fatal(err)
	}
	return audit
}

func TestGenerationExecutionPolicyV2RequiresBothMarkersAtEverySourceBoundary(t *testing.T) {
	for _, policy := range []string{CharacterActivationCyclePolicy, CharacterActivationCyclePolicyV2} {
		fresh := policy == CharacterActivationCyclePolicyV2
		valid := generationExecutionPolicySources(t, fresh)
		before, _ := json.Marshal(valid)
		if err := validateGenerationActivationPolicySources(policy, valid); err != nil {
			t.Fatal(err)
		}
		for _, location := range []string{"simulation", "cycle", "cycle-observation", "input", "sleeping-input-observation"} {
			for _, marker := range []string{CharacterSelfChronologyPolicyV1, CharacterWorkContinuationPolicyV1} {
				var changed ProjectedChapterBundle
				if err := json.Unmarshal(before, &changed); err != nil {
					t.Fatal(err)
				}
				var target *[]string
				switch location {
				case "simulation":
					target = &changed.ChapterWorldSimulation.Sources
				case "cycle":
					target = &changed.CharacterActivationEvidence.Cycles[1].Evidence.Stimulus.Sources
				case "cycle-observation":
					target = &changed.CharacterActivationEvidence.Cycles[1].Evidence.Observations[0].Sources
				case "input":
					target = &changed.CharacterActivationEvidence.Inputs[1].Stimulus.Sources
				case "sleeping-input-observation":
					target = &changed.CharacterActivationEvidence.Inputs[1].Observations[1].Sources
				}
				if fresh {
					var kept []string
					for _, source := range *target {
						if source != marker {
							kept = append(kept, source)
						}
					}
					*target = kept
				} else {
					*target = append(*target, marker)
				}
				if err := validateGenerationActivationPolicySources(policy, changed); err == nil {
					t.Fatalf("policy=%s location=%s marker=%s mixture passed", policy, location, marker)
				}
			}
		}
		var wrongVersion ProjectedChapterBundle
		_ = json.Unmarshal(before, &wrongVersion)
		otherPolicy := CharacterActivationCyclePolicyV2
		if fresh {
			otherPolicy = CharacterActivationCyclePolicy
		}
		wrongVersion.ChapterWorldSimulation.Sources = append(wrongVersion.ChapterWorldSimulation.Sources, otherPolicy)
		if err := validateGenerationActivationPolicySources(policy, wrongVersion); err == nil {
			t.Fatal("explicit contradictory execution version source was accepted")
		}
		after, _ := json.Marshal(valid)
		if string(before) != string(after) {
			t.Fatal("generation policy verification rewrote old source evidence")
		}
	}
}

func TestGenerationExecutionPolicyV2RejectsMissingReadinessModelView(t *testing.T) {
	valid := generationExecutionPolicySources(t, true)
	for index := range valid.CharacterActivationEvidence.Reviews {
		raw, _ := json.Marshal(valid)
		var changed ProjectedChapterBundle
		_ = json.Unmarshal(raw, &changed)
		changed.CharacterActivationEvidence.Reviews[index].ModelView = nil
		if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV2, changed); err == nil {
			t.Fatalf("new generation accepted missing model view at cycle %d", index+1)
		}
	}
	valid.CharacterActivationEvidence.Reviews = valid.CharacterActivationEvidence.Reviews[:1]
	if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV2, valid); err == nil {
		t.Fatal("new generation accepted a missing whole readiness audit")
	}
	if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicy, generationExecutionPolicySources(t, false)); err != nil {
		t.Fatalf("legacy generation was retroactively required to have model views: %v", err)
	}
}

func TestGenerationExecutionPolicyVersionAndLimitValidation(t *testing.T) {
	for _, policy := range []string{CharacterActivationCyclePolicy, CharacterActivationCyclePolicyV2} {
		generation, _, _ := planningV2TestChain(t, 1)
		generation.CharacterAgentProtocol = CharacterAgentDecisionProtocolV2Version
		generation.CharacterActivationPolicy, generation.MaxCharacterActivationCycles = policy, 8
		var err error
		generation.GenerationDigest, err = ComputePlanningGenerationV2Digest(generation)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePlanningGenerationV2(generation); err != nil {
			t.Fatalf("supported policy %s rejected: %v", policy, err)
		}
		for _, limit := range []int{0, 1, 65} {
			changed := generation
			changed.MaxCharacterActivationCycles = limit
			changed.GenerationDigest, _ = ComputePlanningGenerationV2Digest(changed)
			if err := ValidatePlanningGenerationV2(changed); err == nil {
				t.Fatalf("policy=%s invalid frozen limit=%d accepted", policy, limit)
			}
		}
		changed := generation
		changed.CharacterAgentProtocol = CharacterAgentDecisionProtocolVersion
		changed.GenerationDigest, _ = ComputePlanningGenerationV2Digest(changed)
		if err := ValidatePlanningGenerationV2(changed); err == nil {
			t.Fatal("activation execution policy was combined with old character wire v1")
		}
	}
	if CharacterActivationCyclePolicy != "chapter-activation-cycles.v1" {
		t.Fatal("historical v1 policy token changed")
	}
}
