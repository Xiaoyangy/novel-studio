package testutil

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// CharacterActivationInputs includes a real memory root and the current
// opaque-source boundary, unlike older minimal protocol-only cycle fixtures.
func CharacterActivationInputs(t *testing.T, contextDigests ...string) domain.CharacterActivationInputSet {
	t.Helper()
	cycle := CharacterCycle(t, 1, "", nil, 0, contextDigests...)
	e := cycle.Evidence
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSourceRefPolicyV2)
	var err error
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: e.Observations[0].AgentID, Character: e.Observations[0].Character, State: "projected", GenerationID: e.GenerationID})
	if err != nil {
		t.Fatal(err)
	}
	observation := e.Observations[0]
	observation.MemoryRoot, observation.StimulusDigest = memory.MemoryRoot, e.Stimulus.Digest
	observation.Sources = []string{domain.CharacterSourceRefPolicyV2}
	observation, err = domain.FinalizeCharacterObservationPacket(observation)
	if err != nil {
		t.Fatal(err)
	}
	e.Activation.Entries[0].ObservationDigest = observation.Digest
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.FinalizeCharacterActivationInputSet(domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus, Activation: e.Activation, Observations: []domain.CharacterObservationPacket{observation}, Memories: []domain.CharacterAgentMemory{memory}})
	if err != nil {
		t.Fatal(err)
	}
	return input
}
