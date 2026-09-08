package domain

import "fmt"

func validateGenerationContinuationHistoryPredecessor(previous, current ProjectedChapterBundle) error {
	old := HasCharacterWorkContinuationHistoryPolicyV1(previous.ChapterWorldSimulation.Sources)
	new := HasCharacterWorkContinuationHistoryPolicyV1(current.ChapterWorldSimulation.Sources)
	if old != new {
		return fmt.Errorf("generation continuation history policy cannot change between chapters")
	}
	if new && (previous.CharacterActivationEvidence == nil || current.CharacterActivationEvidence == nil || previous.CharacterActivationEvidence.ProtocolDigest != current.CharacterActivationEvidence.ProtocolDigest) {
		return fmt.Errorf("generation continuation history producer cannot change between chapters")
	}
	return nil
}

// ValidateGenerationCharacterProtocolV2 binds an explicitly selected generation
// protocol to every complete simulation. Missing metadata is historical, not a
// request to reinterpret or upgrade its already valid evidence.
func ValidateGenerationCharacterProtocolV2(generation PlanningGenerationV2, bundle ProjectedChapterBundle) error {
	if generation.CharacterActivationPolicy != "" || bundle.ChapterWorldSimulation.CharacterActivation != nil || bundle.CharacterActivationEvidence != nil {
		if !IsCharacterActivationPolicy(generation.CharacterActivationPolicy) || generation.CharacterAgentProtocol != CharacterAgentDecisionProtocolV2Version || bundle.CharacterActivationEvidence == nil || bundle.ChapterWorldSimulation.CharacterActivation == nil {
			return fmt.Errorf("generation activation policy does not match the complete chapter evidence")
		}
		if generation.MaxCharacterActivationCycles != bundle.CharacterActivationEvidence.Session.MaxCycles {
			return fmt.Errorf("generation cycle limit differs from frozen chapter session")
		}
		if err := validateGenerationActivationPolicySources(generation.CharacterActivationPolicy, bundle); err != nil {
			return err
		}
	}
	if generation.PlanGroundingPolicy != "" {
		if generation.PlanGroundingPolicy != PlanGroundingPolicyV1 || !HasPlanGroundingPolicy(bundle.ChapterWorldSimulation) {
			return fmt.Errorf("generation plan grounding policy does not match simulation")
		}
		if err := ValidatePlanGroundingBundle(bundle.ChapterPlan, bundle.ChapterWorldSimulation, bundle.CharacterAgentEvidence, bundle.CharacterActivationEvidence); err != nil {
			return err
		}
	}
	protocol := generation.CharacterAgentProtocol
	if protocol == "" {
		return nil
	}
	simulation := bundle.ChapterWorldSimulation
	fail := func() error {
		reported := "none"
		if simulation.CharacterAgentProtocol != nil {
			reported = simulation.CharacterAgentProtocol.Version
		}
		return fmt.Errorf("generation character protocol %s does not match chapter %d simulation version %d/receipt %s", protocol, bundle.Chapter, simulation.Version, reported)
	}
	if bundle.GenerationID != generation.GenerationID || simulation.GenerationID != generation.GenerationID {
		return fail()
	}
	switch protocol {
	case "legacy":
		if simulation.Version != 1 || simulation.CharacterAgentProtocol != nil || bundle.HasCharacterEvidence() || simulation.PhysicalState != nil {
			return fail()
		}
	case CharacterAgentDecisionProtocolVersion, CharacterAgentDecisionProtocolV2Version:
		if simulation.Version != 2 || simulation.CharacterAgentProtocol == nil || simulation.CharacterAgentProtocol.Version != protocol || !bundle.HasCharacterEvidence() {
			return fail()
		}
	default:
		return fail()
	}
	return nil
}

func validateGenerationActivationPolicySources(policy string, bundle ProjectedChapterBundle) error {
	wantV3 := CharacterActivationUsesRoundSources(policy)
	wantNew := policy == CharacterActivationCyclePolicyV2 || wantV3
	wantTimedResources := HasCharacterResourceObservationTimePolicyV1(bundle.ChapterWorldSimulation.Sources)
	wantFullHistory := HasCharacterWorkContinuationHistoryPolicyV1(bundle.ChapterWorldSimulation.Sources)
	if wantFullHistory && !wantV3 {
		return fmt.Errorf("full-owner continuation history requires a v3 generation")
	}
	if wantTimedResources && !wantV3 {
		return fmt.Errorf("timed resource observations require a v3 generation")
	}
	check := func(label string, sources []string) error {
		if HasCharacterWorkContinuationHistoryPolicyV1(sources) != wantFullHistory {
			return fmt.Errorf("generation continuation history policy differs from %s", label)
		}
		if HasCharacterResourceObservationTimePolicyV1(sources) != wantTimedResources {
			return fmt.Errorf("generation resource observation time policy differs from %s", label)
		}
		chronology := HasCharacterSelfChronologyPolicyV1(sources)
		continuation := HasCharacterWorkContinuationPolicyV1(sources)
		wrongVersion := (wantNew && planningV2ContainsExactString(sources, CharacterActivationCyclePolicy)) || (!wantNew && planningV2ContainsExactString(sources, CharacterActivationCyclePolicyV2)) || (!wantV3 && planningV2ContainsExactString(sources, CharacterActivationCyclePolicyV3)) || (wantV3 && planningV2ContainsExactString(sources, CharacterActivationCyclePolicyV2))
		if wantV3 {
			for _, required := range []string{CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterWorkArtifactPolicyV1, CharacterRevisionFeedbackPolicyV1} {
				if !planningV2ContainsExactString(sources, required) {
					return fmt.Errorf("v3 generation lacks %s in %s", required, label)
				}
			}
		}
		if wrongVersion || (wantNew && (!chronology || !continuation)) || (!wantNew && (chronology || continuation)) {
			return fmt.Errorf("generation activation policy %s differs from %s chronology/continuation policy", policy, label)
		}
		return nil
	}
	if err := check("simulation", bundle.ChapterWorldSimulation.Sources); err != nil {
		return err
	}
	evidence := bundle.CharacterActivationEvidence
	if evidence == nil || len(evidence.Cycles) == 0 || len(evidence.Inputs) != len(evidence.Cycles) {
		return fmt.Errorf("generation activation policy requires complete cycle input evidence")
	}
	for i, cycle := range evidence.Cycles {
		if (wantV3 && cycle.Version != CharacterActivationCycleV3Version) || (!wantV3 && cycle.Version == CharacterActivationCycleV3Version) {
			return fmt.Errorf("generation policy differs from actual cycle wire version")
		}
		if wantNew && (i >= len(evidence.Reviews) || evidence.Reviews[i].ModelView == nil) {
			return fmt.Errorf("new activation generation requires its source-bound readiness model view")
		}
		if err := check(fmt.Sprintf("cycle %d stimulus", i+1), cycle.Evidence.Stimulus.Sources); err != nil {
			return err
		}
		for _, observation := range cycle.Evidence.Observations {
			if err := check(fmt.Sprintf("cycle %d observation", i+1), observation.Sources); err != nil {
				return err
			}
		}
		input := evidence.Inputs[i]
		if err := check(fmt.Sprintf("cycle %d frozen input", i+1), input.Stimulus.Sources); err != nil {
			return err
		}
		for _, observation := range input.Observations {
			if err := check(fmt.Sprintf("cycle %d frozen observation", i+1), observation.Sources); err != nil {
				return err
			}
		}
	}
	return nil
}
