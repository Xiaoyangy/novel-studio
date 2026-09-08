package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func (s *CharacterAgentStore) activationInputsPath() (string, error) {
	if s == nil || s.cycle == nil {
		return "", fmt.Errorf("activation input snapshot requires a cycle-bound store")
	}
	return filepath.Join(characterAgentChapterDir(s.cycle.generation, s.cycle.chapter), "inputs.json"), nil
}

func (s *CharacterAgentStore) LoadActivationInputs() (*domain.CharacterActivationInputSet, error) {
	path, err := s.activationInputsPath()
	if err != nil {
		return nil, err
	}
	var input domain.CharacterActivationInputSet
	if err := s.readProof(path, &input); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateCharacterActivationInputSet(input); err != nil {
		return nil, err
	}
	if err := s.validateCycleStimulus(input.Stimulus); err != nil {
		return nil, err
	}
	return &input, nil
}

// History is resolved only from an already validated cycle, not a fabricated
// collecting session or an unverified arbitrary cycle index.
func (s *CharacterAgentStore) LoadActivationInputsForCycle(cycle domain.CharacterActivationCycle) (*domain.CharacterActivationInputSet, error) {
	if s == nil || s.cycle != nil {
		return nil, fmt.Errorf("cycle history requires an unscoped store")
	}
	if cycle.Version == domain.CharacterActivationCycleV2Version || cycle.Version == domain.CharacterActivationCycleV3Version {
		prefix, err := NewStore(s.io.dir).LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, cycle.Chapter)
		if err != nil {
			return nil, err
		}
		if prefix == nil {
			return nil, fmt.Errorf("v2 cycle inputs require their verified committed source prefix")
		}
		step, exists := prefix.Step(cycle.Index - 1)
		if !exists {
			return nil, fmt.Errorf("v2 cycle inputs require their verified committed source prefix")
		}
		if !jsonValuesEqual(step.Cycle(), cycle) {
			return nil, fmt.Errorf("v2 cycle differs from its committed verified step")
		}
		input := step.Input()
		return &input, nil
	}
	if err := domain.ValidateCharacterActivationCycle(cycle); err != nil {
		return nil, err
	}
	source, err := domain.CharacterActivationCycleSourceToken(cycle.GenerationID, cycle.Chapter, cycle.Index, cycle.ChapterContextDigest, cycle.PreviousDigest)
	if err != nil {
		return nil, err
	}
	view := &CharacterAgentStore{io: s.io, cycle: &characterActivationProofScope{generation: cycle.GenerationID, chapter: cycle.Chapter, index: cycle.Index, source: source, physicalRoot: cycle.BeforePhysicalRoot, day: cycle.StartDay}}
	input, err := view.LoadActivationInputs()
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("cycle is missing its frozen input snapshot")
	}
	if err := domain.ValidateCharacterActivationInputsForCycle(*input, cycle); err != nil {
		return nil, err
	}
	return input, nil
}

// Freeze the complete input before materializing individual tool proof paths.
// A crash anywhere after that immutable publication is recovered without
// rebuilding observations from newer mutable state or invoking a model.
func (s *CharacterAgentStore) PublishActivationInputs(input domain.CharacterActivationInputSet) error {
	path, err := s.activationInputsPath()
	if err != nil {
		return err
	}
	if err := domain.ValidateCharacterActivationInputSet(input); err != nil {
		return err
	}
	if err := s.validateCycleStimulus(input.Stimulus); err != nil {
		return err
	}
	if err := s.writeProof(path, input); err != nil {
		return err
	}
	return s.materializeActivationInputs(input)
}

func (s *CharacterAgentStore) RecoverActivationInputs() (*domain.CharacterActivationInputSet, error) {
	input, err := s.LoadActivationInputs()
	if err != nil || input == nil {
		return input, err
	}
	if err := s.materializeActivationInputs(*input); err != nil {
		return nil, err
	}
	return input, nil
}

func (s *CharacterAgentStore) materializeActivationInputs(input domain.CharacterActivationInputSet) error {
	if err := s.SaveRegistrySnapshot(input.Stimulus.GenerationID, input.Stimulus.Chapter, input.Registry); err != nil {
		return err
	}
	if err := s.SaveStimulus(input.Stimulus); err != nil {
		return err
	}
	for _, observation := range input.Observations {
		if err := s.SaveObservation(observation); err != nil {
			return err
		}
	}
	return s.SaveActivation(input.Activation)
}
