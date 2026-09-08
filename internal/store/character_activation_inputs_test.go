package store

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestCharacterActivationInputSnapshotRecoversPartialMaterialization(t *testing.T) {
	st := NewStore(t.TempDir())
	input := testutil.CharacterActivationInputs(t)
	fixture := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, fixture.ChapterContextDigest, *input.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	path, err := proofs.activationInputsPath()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate death after the transaction input was frozen, before its
	// individual observation/activation files had been materialized.
	if err := proofs.writeProof(path, input); err != nil {
		t.Fatal(err)
	}
	if observation, err := proofs.LoadObservation(input.Stimulus.GenerationID, 1, 1, input.Observations[0].AgentID); err != nil || observation != nil {
		t.Fatal("fixture unexpectedly contains materialized observation")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(st.Dir()).CharacterAgents.ForActivationCycle(session)
			if err != nil {
				t.Error(err)
				return
			}
			recovered, err := other.RecoverActivationInputs()
			if err != nil || recovered == nil || recovered.Digest != input.Digest {
				t.Errorf("snapshot recovery changed input: %v", err)
			}
		}()
	}
	wg.Wait()
	observation, err := proofs.LoadObservation(input.Stimulus.GenerationID, 1, 1, input.Observations[0].AgentID)
	if err != nil || observation == nil || observation.Digest != input.Observations[0].Digest {
		t.Fatalf("missing recovered observation: %v", err)
	}
	actualPath, err := proofs.proofPath(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(proofs.io.path(actualPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(input); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(proofs.io.path(actualPath))
	if err != nil || string(before) != string(after) {
		t.Fatal("retry rewrote frozen input bytes")
	}
	if legacy, err := st.CharacterAgents.LoadStimulus(input.Stimulus.GenerationID, 1); err != nil || legacy != nil {
		t.Fatal("input transaction leaked into legacy namespace")
	}
}

func TestProjectedCharacterMemoryAppendIsAtomicAndIdempotent(t *testing.T) {
	st := NewStore(t.TempDir())
	input := testutil.CharacterActivationInputs(t)
	memory := input.Memories[0]
	if err := st.CharacterAgents.SaveProjectedMemory(memory); err != nil {
		t.Fatal(err)
	}
	canonical := memory
	canonical.State, canonical.GenerationID = "canonical", ""
	if err := st.CharacterAgents.SaveCanonicalMemory(canonical); err != nil {
		t.Fatal(err)
	}
	before, err := st.CharacterAgents.LoadCanonicalMemory(memory.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	fact := func(i int) domain.CharacterAgentMemoryFact {
		return domain.CharacterAgentMemoryFact{ID: fmt.Sprintf("fact-%d", i), Chapter: 1, Kind: "projected_decision", Text: fmt.Sprintf("本人的实际观察%d", i), SourceDigest: fmt.Sprintf("source-%d", i)}
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := NewStore(st.Dir())
			if err := other.AppendProjectedCharacterMemoryFact(memory.GenerationID, memory.AgentID, fact(i), "original-time"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	complete, err := st.CharacterAgents.LoadProjectedMemory(memory.GenerationID, memory.AgentID)
	if err != nil || complete == nil || len(complete.Facts) != 12 {
		t.Fatalf("lost concurrent memory append: %+v %v", complete, err)
	}
	for i := range 12 {
		if err := st.AppendProjectedCharacterMemoryFact(memory.GenerationID, memory.AgentID, fact(i), "retry-time"); err != nil {
			t.Fatal(err)
		}
	}
	retried, err := st.CharacterAgents.LoadProjectedMemory(memory.GenerationID, memory.AgentID)
	if err != nil || retried.MemoryRoot != complete.MemoryRoot || retried.UpdatedAt != complete.UpdatedAt {
		t.Fatal("exact retry changed projected memory identity")
	}
	bad := fact(0)
	bad.Text = "相同ID的新解释"
	if err := st.AppendProjectedCharacterMemoryFact(memory.GenerationID, memory.AgentID, bad, "retry-time"); err == nil {
		t.Fatal("fact ID collision was silently overwritten")
	}
	after, err := st.CharacterAgents.LoadCanonicalMemory(memory.AgentID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("projected append polluted canonical memory")
	}
}
