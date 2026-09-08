package domain

import "fmt"

const CharacterActivationPlannerViewPolicy = "activation-planner-view.trace-final-state.v1"

// This view is not persisted as a replacement for the simulation. Full source
// identity is explicit; the host continues to validate the complete evidence.
func CharacterActivationPlannerView(simulation ChapterWorldSimulation) (map[string]any, error) {
	if simulation.CharacterActivation == nil || simulation.PhysicalState == nil || len(simulation.CharacterDecisionTrace) == 0 {
		return nil, fmt.Errorf("activation planner view lacks complete simulation identity/state/trace")
	}
	digest, err := DeterministicPlanningHash(simulation)
	if err != nil {
		return nil, err
	}
	stateRoot, err := CharacterPhysicalRootForCycle(*simulation.PhysicalState)
	if err != nil {
		return nil, err
	}
	decisions := append([]CharacterWorldDecision(nil), simulation.CharacterDecisions...)
	for i := range decisions {
		decisions[i].PostState = nil
	}
	actors := []CharacterReadinessActorView{}
	for _, actor := range simulation.PhysicalState.Actors {
		resources, err := BuildCharacterResourceViewsV2(*simulation.PhysicalState, actor.AgentID)
		if err != nil {
			return nil, err
		}
		operations, err := selectCharacterOperationalObservationsV1(actor.OperationalObservations)
		if err != nil {
			return nil, err
		}
		actors = append(actors, CharacterReadinessActorView{AgentID: actor.AgentID, Character: actor.Character, Location: actor.Location, Resources: resources, TaskProgress: actor.TaskProgress, ReceivedFacts: actor.ReceivedFacts, OperationalObservations: operations})
	}
	return map[string]any{
		"view_policy":              CharacterActivationPlannerViewPolicy,
		"source_simulation_digest": "sha256:" + digest,
		"character_activation":     simulation.CharacterActivation,
		"character_decision_trace": simulation.CharacterDecisionTrace,
		"character_decisions":      decisions,
		"story_time":               simulation.StoryTime,
		"final_physical_state":     map[string]any{"source_root": stateRoot, "world_resources": simulation.PhysicalState.Resources, "actors": actors},
		"render_policy":            "这是完整源的只读展示，不是替代源：character_decision_trace保留所有周期原始选择/意图与实际顺序；character_decisions只表示最近一次选择。完整旧self_experiences不重复展示，不等于未发生；task_progress与实际知识保留。不能从末态倒推早期知识或把离屏秘密写成POV亲见。",
	}, nil
}
