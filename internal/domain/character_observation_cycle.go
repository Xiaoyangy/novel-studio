package domain

import (
	"fmt"
	"strings"
)

const CharacterObservationCyclePolicy = "character-observation-cycle.prior-self.v1"

// Only opaque source bindings and current time are exposed, not the readiness
// context's narrative goals or future outline. The tool cannot edit these.
type CharacterObservationCycleContext struct {
	Version              string  `json:"version"`
	Index                int     `json:"index"`
	ChapterContextDigest string  `json:"chapter_context_digest"`
	PreviousCycleDigest  string  `json:"previous_cycle_digest,omitempty"`
	CurrentDay           float64 `json:"current_day"`
}

func NewCharacterObservationCycleContext(session CharacterActivationSession) (*CharacterObservationCycleContext, error) {
	if err := ValidateCharacterActivationSession(session); err != nil {
		return nil, err
	}
	if session.Phase != "collecting" {
		return nil, fmt.Errorf("observation cycle context requires a collecting session")
	}
	value := &CharacterObservationCycleContext{Version: CharacterObservationCyclePolicy, Index: len(session.CycleDigests) + 1, ChapterContextDigest: session.ChapterContextDigest, CurrentDay: session.CurrentDay}
	if len(session.CycleDigests) > 0 {
		value.PreviousCycleDigest = session.CycleDigests[len(session.CycleDigests)-1]
	}
	return value, nil
}

func validateCharacterObservationCycleContext(observation CharacterObservationPacket) error {
	value := observation.CycleContext
	if value == nil {
		return nil
	}
	if value.Version != CharacterObservationCyclePolicy || value.Index > 64 || !finiteStoryDay(value.CurrentDay) || value.CurrentDay < 0 {
		return fmt.Errorf("invalid character observation cycle context")
	}
	_, err := CharacterActivationCycleSourceToken(observation.GenerationID, observation.Chapter, value.Index, value.ChapterContextDigest, value.PreviousCycleDigest)
	return err
}

func validateCharacterObservationCycleBinding(stimulus WorldStimulusPacket, observation CharacterObservationPacket) error {
	if err := validateCharacterObservationCycleContext(observation); err != nil {
		return err
	}
	tokens := []string{}
	for _, source := range stimulus.Sources {
		if strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			tokens = append(tokens, source)
		}
	}
	if observation.CycleContext == nil {
		if len(tokens) > 0 && HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
			return fmt.Errorf("cycle self observation lacks its exact cycle/time context")
		}
		return nil
	}
	value := observation.CycleContext
	token, err := CharacterActivationCycleSourceToken(observation.GenerationID, observation.Chapter, value.Index, value.ChapterContextDigest, value.PreviousCycleDigest)
	if err != nil {
		return err
	}
	if len(tokens) != 1 || tokens[0] != token || stimulus.StoryClock == nil || stimulus.StoryClock.CurrentDay != value.CurrentDay {
		return fmt.Errorf("character observation cycle/time differs from its actual stimulus")
	}
	return nil
}
