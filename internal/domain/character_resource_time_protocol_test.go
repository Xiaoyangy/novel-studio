package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// This tests policy consistency, not receipt authenticity. The shared inventory
// fixture uses a real readiness codec binding; full tool tests cover execution.
func resourceTimeGenerationInventory(t *testing.T, timed bool) ProjectedChapterBundle {
	t.Helper()
	bundle := generationExecutionPolicySources(t, true)
	visitResourceTimeInventorySources(&bundle, func(s *[]string) {
		*s = append(*s, CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterWorkArtifactPolicyV1, CharacterRevisionFeedbackPolicyV1)
		if timed {
			*s = append(*s, CharacterResourceObservationTimePolicyV1)
		}
	})
	for i := range bundle.CharacterActivationEvidence.Cycles {
		bundle.CharacterActivationEvidence.Cycles[i].Version = CharacterActivationCycleV3Version
	}
	return bundle
}

func visitResourceTimeInventorySources(bundle *ProjectedChapterBundle, visit func(*[]string)) {
	visit(&bundle.ChapterWorldSimulation.Sources)
	for i := range bundle.CharacterActivationEvidence.Cycles {
		cycle := &bundle.CharacterActivationEvidence.Cycles[i]
		visit(&cycle.Evidence.Stimulus.Sources)
		for j := range cycle.Evidence.Observations {
			visit(&cycle.Evidence.Observations[j].Sources)
		}
		input := &bundle.CharacterActivationEvidence.Inputs[i]
		visit(&input.Stimulus.Sources)
		for j := range input.Observations {
			visit(&input.Observations[j].Sources)
		}
	}
}

func TestResourceObservationTimePolicyCannotMixWithinGeneration(t *testing.T) {
	for _, timed := range []bool{false, true} {
		valid := resourceTimeGenerationInventory(t, timed)
		before, _ := json.Marshal(valid)
		if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV3, valid); err != nil {
			t.Fatalf("uniform policy rejected, timed=%v: %v", timed, err)
		}
		count := 0
		visitResourceTimeInventorySources(&valid, func(*[]string) { count++ })
		for target := range count {
			var changed ProjectedChapterBundle
			if err := json.Unmarshal(before, &changed); err != nil {
				t.Fatal(err)
			}
			index := 0
			visitResourceTimeInventorySources(&changed, func(s *[]string) {
				if index == target {
					if timed {
						var kept []string
						for _, source := range *s {
							if source != CharacterResourceObservationTimePolicyV1 {
								kept = append(kept, source)
							}
						}
						*s = kept
					} else {
						*s = append(*s, CharacterResourceObservationTimePolicyV1)
					}
				}
				index++
			})
			if err := validateGenerationActivationPolicySources(CharacterActivationCyclePolicyV3, changed); err == nil || !strings.Contains(err.Error(), "resource observation time policy") {
				t.Fatalf("mixed policy at source %d accepted or lost diagnostic, timed=%v: %v", target, timed, err)
			}
		}
		after, _ := json.Marshal(valid)
		if string(after) != string(before) {
			t.Fatal("policy validation rewrote source evidence")
		}
	}
}
