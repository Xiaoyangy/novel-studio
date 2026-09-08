package domain

import (
	"encoding/json"
	"fmt"
)

// AppendProjectedCharacterMemoryFact is pure. An exact retry preserves the
// original timestamp/root; an ID collision with different content fails. It
// never promotes a projected fact to canon and never mutates shared slices.
func AppendProjectedCharacterMemoryFact(memory CharacterAgentMemory, fact CharacterAgentMemoryFact, updatedAt string) (CharacterAgentMemory, bool, error) {
	if memory.State != "projected" || fact.Accepted {
		return memory, false, fmt.Errorf("projected memory append cannot publish accepted facts")
	}
	raw, err := json.Marshal(memory)
	if err != nil {
		return memory, false, err
	}
	var copy CharacterAgentMemory
	if err := json.Unmarshal(raw, &copy); err != nil {
		return memory, false, err
	}
	checked, err := FinalizeCharacterAgentMemory(copy)
	if err != nil {
		return memory, false, err
	}
	if checked.MemoryRoot != memory.MemoryRoot {
		return memory, false, fmt.Errorf("projected memory source root mismatch")
	}
	// Normalize the incoming fact through the same normal memory validator.
	single := CharacterAgentMemory{AgentID: memory.AgentID, Character: memory.Character, State: "projected", GenerationID: memory.GenerationID, Facts: []CharacterAgentMemoryFact{fact}}
	single, err = FinalizeCharacterAgentMemory(single)
	if err != nil {
		return memory, false, err
	}
	fact = single.Facts[0]
	for _, old := range checked.Facts {
		if old.ID != fact.ID {
			continue
		}
		left, _ := json.Marshal(old)
		right, _ := json.Marshal(fact)
		if string(left) != string(right) {
			return memory, false, fmt.Errorf("projected memory fact ID has different content")
		}
		return checked, false, nil
	}
	checked.Facts = append(checked.Facts, fact)
	checked.UpdatedAt = updatedAt
	result, err := FinalizeCharacterAgentMemory(checked)
	return result, err == nil, err
}
