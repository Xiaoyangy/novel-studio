package domain

import (
	"fmt"
	"strings"
)

// CharacterInitialState describes only the character's actual opening state.
// Unknown optional values stay empty; callers must not fill them from a future
// chapter's core_event, another character's secrets, or the author's Arc.
type CharacterInitialState struct {
	Time             string                       `json:"time,omitempty"`
	Location         string                       `json:"location"`
	CurrentGoal      string                       `json:"current_goal"`
	CurrentAction    string                       `json:"current_action,omitempty"`
	Pressure         string                       `json:"pressure"`
	KnownFacts       []string                     `json:"known_facts"`
	Resources        []string                     `json:"resources"`
	ResourceBalances []InitialCharacterResourceV2 `json:"resource_balances,omitempty"`
	Relationships    []string                     `json:"relationships"`
	Commitments      []string                     `json:"commitments"`
}

// ValidateCharacterInitialState validates an explicitly supplied baseline,
// without inventing one for legacy characters. New-protocol initialization
// separately enforces that important characters provide this state at all.
func ValidateCharacterInitialState(character Character) error {
	state := character.InitialState
	if state == nil {
		return nil
	}
	for _, required := range []struct{ field, value string }{
		{"location", state.Location}, {"current_goal", state.CurrentGoal}, {"pressure", state.Pressure},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("character %q initial_state.%s must not be empty; provide the character's actual opening state, not a future outline", character.Name, required.field)
		}
	}
	if len(state.KnownFacts) == 0 {
		return fmt.Errorf("character %q initial_state.known_facts requires at least one explicitly known opening fact", character.Name)
	}
	seen := make(map[string]int, len(state.KnownFacts))
	for index, fact := range state.KnownFacts {
		key := strings.ToLower(strings.Join(strings.Fields(fact), " "))
		if key == "" {
			return fmt.Errorf("character %q initial_state.known_facts[%d] must not be blank", character.Name, index)
		}
		if previous, duplicate := seen[key]; duplicate {
			return fmt.Errorf("character %q initial_state.known_facts[%d] duplicates known_facts[%d]", character.Name, index, previous)
		}
		seen[key] = index
	}
	resourceIDs := map[string]bool{}
	for _, initial := range state.ResourceBalances {
		if resourceIDs[initial.ResourceID] {
			return fmt.Errorf("character %q has duplicate initial resource %q", character.Name, initial.ResourceID)
		}
		resourceIDs[initial.ResourceID] = true
		if err := validateWorldResourceBalanceV2(WorldResourceBalanceV2{ResourceID: initial.ResourceID, Name: initial.Name, Unit: initial.Unit, ActualAmount: initial.ActualAmount, ReadableFacts: initial.ReadableFacts, InspectableSurfaces: initial.InspectableSurfaces}); err != nil {
			return err
		}
		if err := validateResourceHoldingV2(CharacterResourceHoldingV2{ResourceID: initial.ResourceID, PerceivedName: initial.PerceivedName, PerceivedUnit: initial.PerceivedUnit, Access: initial.Access, Perception: initial.Perception}); err != nil {
			return err
		}
	}
	return nil
}
