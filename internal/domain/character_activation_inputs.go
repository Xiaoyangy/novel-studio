package domain

import (
	"encoding/json"
	"fmt"
	"sort"
)

const CharacterActivationInputSetVersion = "character-activation-inputs.v1"

// InputSet freezes all private observation baselines, including sleeping
// actors, before any model runs. Memories are host-only raw projected memory;
// models receive only their own observation. This is not accepted canon.
type CharacterActivationInputSet struct {
	Version      string                       `json:"version"`
	Registry     CharacterAgentRegistry       `json:"registry"`
	Stimulus     WorldStimulusPacket          `json:"stimulus"`
	Activation   CharacterAgentActivation     `json:"activation"`
	Observations []CharacterObservationPacket `json:"observations"`
	Memories     []CharacterAgentMemory       `json:"memories"`
	Digest       string                       `json:"digest"`
}

func FinalizeCharacterActivationInputSet(input CharacterActivationInputSet) (CharacterActivationInputSet, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return input, err
	}
	var copy CharacterActivationInputSet
	if err := json.Unmarshal(raw, &copy); err != nil {
		return input, err
	}
	if copy.Version == "" {
		copy.Version = CharacterActivationInputSetVersion
	}
	if copy.Version != CharacterActivationInputSetVersion {
		return input, fmt.Errorf("unsupported activation input set")
	}
	registry, err := FinalizeCharacterAgentRegistry(copy.Registry)
	if err != nil {
		return input, err
	}
	stimulus, err := FinalizeWorldStimulusPacket(copy.Stimulus)
	if err != nil {
		return input, err
	}
	activation, err := FinalizeCharacterAgentActivation(copy.Activation)
	if err != nil {
		return input, err
	}
	if registry.RegistryRoot != copy.Registry.RegistryRoot || stimulus.Digest != copy.Stimulus.Digest || activation.Digest != copy.Activation.Digest || activation.RegistryRoot != registry.RegistryRoot || activation.GenerationID != stimulus.GenerationID || activation.Chapter != stimulus.Chapter {
		return input, fmt.Errorf("activation input set has inconsistent identities/digests")
	}
	if stimulus.Version != WorldStimulusPacketV2Version || stimulus.PhysicalState == nil || stimulus.StoryClock == nil {
		return input, fmt.Errorf("activation inputs require physical state and actual clock")
	}
	observations := map[string]CharacterObservationPacket{}
	for _, observation := range copy.Observations {
		if _, duplicate := observations[observation.AgentID]; duplicate {
			return input, fmt.Errorf("duplicate activation observation")
		}
		verified, err := FinalizeCharacterObservationPacket(observation)
		if err != nil {
			return input, err
		}
		if verified.Digest != observation.Digest || observation.Round != 1 {
			return input, fmt.Errorf("activation input observation must be an exact initial-round packet")
		}
		if err := ValidateCharacterResourceViewsAgainstStimulusV2(stimulus, observation); err != nil {
			return input, err
		}
		observations[observation.AgentID] = observation
	}
	memories := map[string]CharacterAgentMemory{}
	for _, memory := range copy.Memories {
		if _, duplicate := memories[memory.AgentID]; duplicate {
			return input, fmt.Errorf("duplicate activation private memory")
		}
		verified, err := FinalizeCharacterAgentMemory(memory)
		if err != nil {
			return input, err
		}
		if verified.MemoryRoot != memory.MemoryRoot || memory.State != "projected" || memory.GenerationID != stimulus.GenerationID {
			return input, fmt.Errorf("activation private memory has foreign identity/root")
		}
		memories[memory.AgentID] = memory
	}
	if len(observations) != len(activation.Entries) || len(memories) != len(activation.Entries) {
		return input, fmt.Errorf("activation requires private baselines for every active/sleeping actor")
	}
	for _, entry := range activation.Entries {
		observation, observed := observations[entry.AgentID]
		memory, remembered := memories[entry.AgentID]
		record, registered := registry.Resolve(entry.Character)
		if !observed || !remembered || !registered || record.AgentID != entry.AgentID || observation.Character != entry.Character || memory.Character != entry.Character || observation.MemoryRoot != memory.MemoryRoot {
			return input, fmt.Errorf("activation actor is not bound to its registry/observation/memory")
		}
		if entry.State == CharacterAgentActive && observation.Digest != entry.ObservationDigest {
			return input, fmt.Errorf("active observation digest differs from activation")
		}
		facts := map[string]CharacterAgentMemoryFact{}
		for _, fact := range memory.Facts {
			facts[fact.ID] = fact
		}
		for _, fact := range observation.Memory {
			original, ok := facts[fact.ID]
			if !ok || original.Chapter != fact.Chapter || original.Kind != fact.Kind || original.Text != fact.Text || original.Accepted != fact.Accepted || (fact.SourceDigest != original.SourceDigest && fact.SourceDigest != CharacterSourceRefV2(entry.AgentID, original.SourceDigest)) || !samePhysicalValueV2(CharacterSourceRefsV2(entry.AgentID, original.KnowledgeRefs), CharacterSourceRefsV2(entry.AgentID, fact.KnowledgeRefs)) {
				return input, fmt.Errorf("observation memory does not originate in the actor's frozen private memory")
			}
		}
	}
	sort.Slice(copy.Observations, func(i, j int) bool { return copy.Observations[i].AgentID < copy.Observations[j].AgentID })
	sort.Slice(copy.Memories, func(i, j int) bool { return copy.Memories[i].AgentID < copy.Memories[j].AgentID })
	copy.Digest = ""
	copy.Digest, err = characterAgentDigest(copy)
	return copy, err
}

func ValidateCharacterActivationInputSet(input CharacterActivationInputSet) error {
	verified, err := FinalizeCharacterActivationInputSet(input)
	if err != nil {
		return err
	}
	if verified.Digest != input.Digest {
		return fmt.Errorf("activation input set digest mismatch")
	}
	return nil
}

func ValidateCharacterActivationInputsForCycle(input CharacterActivationInputSet, cycle CharacterActivationCycle) error {
	if err := ValidateCharacterActivationInputSet(input); err != nil {
		return err
	}
	if err := ValidateCharacterActivationCycle(cycle); err != nil {
		return err
	}
	if cycle.InputSetDigest == "" || cycle.InputSetDigest != input.Digest || cycle.Evidence.Stimulus.Digest != input.Stimulus.Digest || cycle.Evidence.Activation.Digest != input.Activation.Digest || cycle.Evidence.Registry.RegistryRoot != input.Registry.RegistryRoot {
		return fmt.Errorf("cycle does not bind the exact activation input snapshot")
	}
	byAgent := map[string]string{}
	for _, observation := range input.Observations {
		byAgent[observation.AgentID] = observation.Digest
	}
	for _, observation := range cycle.Evidence.Observations {
		if observation.Round == 1 && byAgent[observation.AgentID] != observation.Digest {
			return fmt.Errorf("cycle initial observation differs from its input snapshot")
		}
	}
	return nil
}
