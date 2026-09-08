package agents

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Never marshal the VerifiedStep itself: its private runtime authority must
// come from the complete prefix verifier, not a JSON badge or a raw v2 cycle.
func buildNextCharacterActivationInputsFromStep(previous domain.CharacterActivationInputSet, step domain.VerifiedCharacterActivationStep, session domain.CharacterActivationSession) (domain.CharacterActivationInputSet, error) {
	var empty domain.CharacterActivationInputSet
	if step.GlobalRoot() == "" {
		return empty, fmt.Errorf("next activation requires a verified source step")
	}
	if err := domain.ValidateCharacterActivationInputSet(previous); err != nil {
		return empty, err
	}
	if !sameCharacterCycleValue(previous, step.Input()) {
		return empty, fmt.Errorf("next activation previous input differs from the verified step's exact input")
	}
	cycle := step.Cycle()
	if cycle.Digest != step.GlobalRoot() || cycle.InputSetDigest != previous.Digest {
		return empty, fmt.Errorf("next activation lost its verified source identity")
	}
	if err := validateNextCharacterActivationBoundary(previous, cycle, session); err != nil {
		return empty, err
	}
	memories, err := projectCharacterActivationMemoriesFromStep(previous.Memories, step)
	if err != nil {
		return empty, err
	}
	return buildNextCharacterActivationInputsResolved(previous, cycle, session, step.EffectiveProposals(), step.AfterState(), memories)
}

func projectCharacterActivationMemoriesFromStep(memories []domain.CharacterAgentMemory, step domain.VerifiedCharacterActivationStep) ([]domain.CharacterAgentMemory, error) {
	if step.GlobalRoot() == "" {
		return nil, fmt.Errorf("cycle memory requires a verified source step")
	}
	if !sameCharacterCycleValue(memories, step.Input().Memories) {
		return nil, fmt.Errorf("cycle memory differs from the verified frozen owner baselines")
	}
	cycle := step.Cycle()
	if len(cycle.Evidence.Arbitrations) == 0 {
		return nil, fmt.Errorf("verified cycle has no closed arbitration")
	}
	receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
	if !receipt.Finalized {
		return nil, fmt.Errorf("unclosed/hard-conflict cycle cannot publish next-cycle memory")
	}
	return projectCharacterActivationMemoriesResolved(memories, cycle, receipt, step.EffectiveProposals(), step.AfterState())
}
