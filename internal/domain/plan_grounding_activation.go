package domain

import "fmt"

const PlanGroundingActivationPolicy = "plan-grounding:activation-trace.v1"

type PlanGroundingPOVState struct {
	ArtifactViews           []CharacterArtifactViewV1           `json:"artifact_views,omitempty"`
	OperationalObservations []CharacterOperationalObservationV1 `json:"operational_observations,omitempty"`
	Location                string                              `json:"location"`
	Resources               []CharacterResourceViewV2           `json:"resource_views"`
	TaskProgress            []CharacterTaskProgressV2           `json:"task_progress"`
}

type PlanGroundingActivationCycle struct {
	Cycle             int                                `json:"cycle"`
	CycleDigest       string                             `json:"cycle_digest"`
	ArbitrationDigest string                             `json:"arbitration_digest"`
	StoryTime         *StoryTimeChapterSchedule          `json:"story_time"`
	Decisions         []CharacterActivationDecisionTrace `json:"decisions"`
	POVBefore         PlanGroundingPOVState              `json:"pov_before"`
	POVAfter          PlanGroundingPOVState              `json:"pov_after"`
	NewPOVKnowledge   []CharacterReceivedFactV2          `json:"new_pov_knowledge"`
}

type PlanGroundingActivationSource struct {
	EvidenceDigest string                         `json:"evidence_digest"`
	Cycles         []PlanGroundingActivationCycle `json:"cycles"`
}

func NewActivationPlanGroundingInput(plan ChapterPlan, simulation ChapterWorldSimulation, evidence CharacterActivationChapterEvidence, protocol string) (PlanGroundingInput, error) {
	verified, err := VerifyCharacterActivationChapter(evidence)
	if err != nil {
		return PlanGroundingInput{}, err
	}
	return verified.NewPlanGroundingInput(plan, simulation, protocol)
}

func newActivationPlanGroundingInput(plan ChapterPlan, simulation ChapterWorldSimulation, evidence CharacterActivationChapterEvidence, protocol string, steps []VerifiedCharacterActivationStep) (PlanGroundingInput, error) {
	var input PlanGroundingInput
	if err := validatePlanningV2Digest("grounding review protocol", protocol); err != nil {
		return input, err
	}
	if plan.Chapter != simulation.Chapter || plan.CausalSimulation.WorldSimulationID != simulation.SimulationID || plan.CausalSimulation.ProtagonistDecision != simulation.ProtagonistProjection.ChosenDecision {
		return input, fmt.Errorf("activation grounding plan is not bound to the exact whole-chapter simulation")
	}
	plan.GroundingReview = nil
	digest, err := characterAgentDigest(simulation)
	if err != nil {
		return input, err
	}
	input = PlanGroundingInput{Policy: PlanGroundingActivationPolicy, ReviewProtocol: protocol, Plan: plan, Simulation: PlanGroundingSimulation{simulation.SimulationID, simulation.GenerationID, simulation.Chapter, simulation.TimeWindow, simulation.StoryTime, simulation.ProtagonistProjection, digest}, Activation: &PlanGroundingActivationSource{EvidenceDigest: evidence.Digest}}
	var povID string
	for _, observation := range evidence.Inputs[0].Observations {
		if observation.Character == evidence.Context.POVCharacter {
			input.POVObservation, povID = observation, observation.AgentID
			break
		}
	}
	if povID == "" {
		return input, fmt.Errorf("activation grounding lacks its original POV observation")
	}
	for i, cycle := range evidence.Cycles {
		receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
		var after WorldPhysicalStateV2
		if len(steps) > 0 {
			after = steps[i].AfterState()
		} else {
			after, err = ApplyArbitrationPhysicalStateV2(receipt, cycle.Evidence.Stimulus, LatestCharacterCycleProposals(cycle.Evidence)...)
		}
		if err != nil {
			return input, err
		}
		view := PlanGroundingActivationCycle{Cycle: cycle.Index, CycleDigest: cycle.Digest, ArbitrationDigest: receipt.Digest, StoryTime: receipt.StoryTime}
		known := map[string]bool{}
		stateView := func(state WorldPhysicalStateV2, before bool) (PlanGroundingPOVState, error) {
			for _, actor := range state.Actors {
				if actor.AgentID != povID {
					continue
				}
				resources, err := BuildCharacterResourceViewsV2(state, povID)
				if err != nil {
					return PlanGroundingPOVState{}, err
				}
				for _, fact := range actor.ReceivedFacts {
					if before {
						known[fact.ID] = true
					} else if !known[fact.ID] {
						view.NewPOVKnowledge = append(view.NewPOVKnowledge, fact)
					}
				}
				operations, err := selectCharacterOperationalObservationsV1(actor.OperationalObservations)
				if err != nil {
					return PlanGroundingPOVState{}, err
				}
				var artifacts []CharacterArtifactViewV1
				if HasCharacterWorkArtifactPolicyV1(cycle.Evidence.Stimulus.Sources) {
					artifacts, err = BuildCharacterArtifactViewsV1(state, povID)
					if err != nil {
						return PlanGroundingPOVState{}, err
					}
				}
				return PlanGroundingPOVState{Location: actor.Location, Resources: resources, TaskProgress: actor.TaskProgress, OperationalObservations: operations, ArtifactViews: artifacts}, nil
			}
			return PlanGroundingPOVState{}, fmt.Errorf("activation grounding lost its exact POV actor")
		}
		view.POVBefore, err = stateView(*cycle.Evidence.Stimulus.PhysicalState, true)
		if err != nil {
			return input, err
		}
		view.POVAfter, err = stateView(after, false)
		if err != nil {
			return input, err
		}
		for _, decision := range simulation.CharacterDecisionTrace {
			if decision.Cycle == cycle.Index {
				view.Decisions = append(view.Decisions, decision)
			}
		}
		input.Activation.Cycles = append(input.Activation.Cycles, view)
	}
	return input, nil
}
