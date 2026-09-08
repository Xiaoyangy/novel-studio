package agents

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Cycle memory is derived from a validated closed cycle and kept inside its
// projected input snapshot. No canonical or generation-wide memory is written.
func projectCharacterActivationMemories(memories []domain.CharacterAgentMemory, cycle domain.CharacterActivationCycle) ([]domain.CharacterAgentMemory, error) {
	if err := domain.ValidateCharacterActivationCycle(cycle); err != nil {
		return nil, err
	}
	receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
	if !receipt.Finalized {
		return nil, fmt.Errorf("unclosed/hard-conflict cycle cannot publish next-cycle memory")
	}
	proposals := domain.LatestCharacterCycleProposals(cycle.Evidence)
	physical, err := domain.ApplyArbitrationPhysicalStateV2(receipt, cycle.Evidence.Stimulus, proposals...)
	if err != nil {
		return nil, err
	}
	return projectCharacterActivationMemoriesResolved(memories, cycle, receipt, proposals, physical)
}

func projectCharacterActivationMemoriesResolved(memories []domain.CharacterAgentMemory, cycle domain.CharacterActivationCycle, receipt domain.WorldArbitrationReceipt, proposals []domain.CharacterDecisionProposal, physical domain.WorldPhysicalStateV2) ([]domain.CharacterAgentMemory, error) {
	byAgent := map[string]domain.CharacterDecisionProposal{}
	for _, proposal := range proposals {
		byAgent[proposal.AgentID] = proposal
	}
	byResolution := map[string]domain.CharacterDecisionResolution{}
	for _, resolution := range receipt.Resolutions {
		byResolution[resolution.AgentID] = resolution
	}
	seen := map[string]bool{}
	result := make([]domain.CharacterAgentMemory, 0, len(memories))
	for _, memory := range memories {
		if seen[memory.AgentID] || memory.State != "projected" || memory.GenerationID != cycle.GenerationID {
			return nil, fmt.Errorf("cycle memory has duplicate/foreign identity")
		}
		seen[memory.AgentID] = true
		resolution, active := byResolution[memory.AgentID]
		if !active {
			result = append(result, memory)
			continue
		}
		proposal := byAgent[memory.AgentID]
		if memory.Character != proposal.Character {
			return nil, fmt.Errorf("cycle memory character differs from its proposal")
		}
		text, err := domain.CharacterActivationPrivateOutcome(proposal, resolution, physical, receipt)
		if err != nil {
			return nil, err
		}
		fact := newCharacterMemoryFact(cycle.Chapter, "projected_decision", text, receipt.Digest, false)
		// Identical owner-visible feedback under a new receipt is not new
		// information. Full occurrences remain in immutable cycle evidence.
		duplicateText := false
		for _, old := range memory.Facts {
			if old.Kind == fact.Kind && old.Text == fact.Text {
				duplicateText = true
				break
			}
		}
		if duplicateText {
			result = append(result, memory)
			continue
		}
		updated, _, err := domain.AppendProjectedCharacterMemoryFact(memory, fact, receipt.GeneratedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, updated)
	}
	for agentID := range byResolution {
		if !seen[agentID] {
			return nil, fmt.Errorf("cycle lacks the active actor's previous private memory")
		}
	}
	return result, nil
}
