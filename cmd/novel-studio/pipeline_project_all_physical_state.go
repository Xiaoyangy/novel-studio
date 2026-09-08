package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const pipelineProjectedPhysicalStatePath = "meta/projected_physical_state.json"

type pipelineProjectedPhysicalStateReceipt struct {
	Version           string                      `json:"version"`
	GenerationID      string                      `json:"generation_id"`
	Chapter           int                         `json:"chapter"`
	SimulationID      string                      `json:"simulation_id"`
	ArbitrationDigest string                      `json:"arbitration_digest"`
	StateDigest       string                      `json:"state_digest"`
	PhysicalState     domain.WorldPhysicalStateV2 `json:"physical_state"`
}

func pipelineProjectAllPhysicalState(sim domain.ChapterWorldSimulation) (*domain.WorldPhysicalStateV2, error) {
	if sim.PhysicalState == nil {
		return nil, nil
	}
	state, err := domain.FinalizeWorldPhysicalStateV2(*sim.PhysicalState)
	if err != nil {
		return nil, fmt.Errorf("project-all physical post-state: %w", err)
	}
	expected, err := domain.ComputeWorldPhysicalStateV2Digest(state)
	if err != nil {
		return nil, err
	}
	for _, decision := range sim.CharacterDecisions {
		if pipelineProjectAllAuthorityNoOp(decision) {
			continue
		}
		if decision.PostState == nil {
			return nil, fmt.Errorf("project-all physical simulation lacks post_state for %s", decision.Character)
		}
		index := -1
		for i, actor := range state.Actors {
			if actor.AgentID == decision.PostState.AgentID && actor.Character == decision.Character {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("project-all post_state actor %s is missing from the physical world", decision.Character)
		}
		probe := state
		probe.Actors = append([]domain.CharacterPhysicalStateV2(nil), state.Actors...)
		probe.Actors[index] = *decision.PostState
		probe, err = domain.FinalizeWorldPhysicalStateV2(probe)
		if err != nil {
			return nil, fmt.Errorf("project-all actor post_state: %w", err)
		}
		actual, err := domain.ComputeWorldPhysicalStateV2Digest(probe)
		if err != nil || actual != expected {
			return nil, fmt.Errorf("project-all actor %s post_state differs from the arbitrated physical world", decision.Character)
		}
	}
	return &state, nil
}

func pipelineProjectAllPostLocation(sim domain.ChapterWorldSimulation, decision domain.CharacterWorldDecision) string {
	if sim.PhysicalState == nil {
		return decision.Location
	}
	if decision.PostState == nil {
		return ""
	}
	return decision.PostState.Location
}

func appendPipelineProjectAllPhysicalDelta(delta *domain.ProjectedDelta, chapter int, sim domain.ChapterWorldSimulation, state domain.WorldPhysicalStateV2) error {
	encoded, err := domain.EncodeWorldPhysicalStateV2(state)
	if err != nil {
		return err
	}
	source := "world arbitration:" + sim.SimulationID
	if sim.CharacterAgentProtocol != nil && sim.CharacterAgentProtocol.ArbitrationDigest != "" {
		source = "world arbitration:" + sim.CharacterAgentProtocol.ArbitrationDigest
	}
	delta.CharacterState = append(delta.CharacterState, pipelineProjectAllMutation(
		"physical", chapter, "world", "world", domain.WorldPhysicalStateV2Field, "set", encoded, source,
	))
	for _, resource := range state.Resources {
		// Null is significant: an unknown or qualitative balance is never zero.
		amount, err := json.Marshal(resource.ActualAmount)
		if err != nil {
			return fmt.Errorf("encode physical resource %s: %w", resource.ResourceID, err)
		}
		mutation := pipelineProjectAllMutation("physical-resource", chapter, resource.ResourceID, "world",
			domain.WorldResourceActualAmountV2Field, "set", string(amount), source)
		mutation.Object = resource.ResourceID
		delta.Resources = append(delta.Resources, mutation)
	}
	return nil
}

func pipelineProjectedPhysicalReceipt(generationID string, chapter int, sim domain.ChapterWorldSimulation, state domain.WorldPhysicalStateV2) (pipelineProjectedPhysicalStateReceipt, error) {
	digest, err := domain.ComputeWorldPhysicalStateV2Digest(state)
	if err != nil {
		return pipelineProjectedPhysicalStateReceipt{}, err
	}
	arbitrationDigest := ""
	if sim.CharacterAgentProtocol != nil {
		arbitrationDigest = strings.TrimSpace(sim.CharacterAgentProtocol.ArbitrationDigest)
	}
	if generationID == "" || generationID != sim.GenerationID || chapter != sim.Chapter || sim.SimulationID == "" || arbitrationDigest == "" {
		return pipelineProjectedPhysicalStateReceipt{}, fmt.Errorf("project-all physical shadow state requires exact generation/chapter/simulation/arbitration identity")
	}
	return pipelineProjectedPhysicalStateReceipt{
		Version: "projected-physical-state.v2", GenerationID: generationID, Chapter: chapter,
		SimulationID: sim.SimulationID, ArbitrationDigest: arbitrationDigest, StateDigest: digest, PhysicalState: state,
	}, nil
}

func savePipelineProjectedPhysicalState(st *store.Store, receipt pipelineProjectedPhysicalStateReceipt) error {
	_, err := writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineProjectedPhysicalStatePath), receipt)
	return err
}
