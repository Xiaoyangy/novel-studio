package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func cycleProofFixture(t *testing.T, st *Store) (domain.CharacterActivationSession, domain.CharacterActivationCycle, *CharacterAgentStore) {
	t.Helper()
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	return session, cycle, proof
}

func saveCycleProofInputs(t *testing.T, proof *CharacterAgentStore, cycle domain.CharacterActivationCycle) {
	t.Helper()
	e := cycle.Evidence
	if err := proof.SaveRegistrySnapshot(e.GenerationID, e.Chapter, e.Registry); err != nil {
		t.Fatal(err)
	}
	if err := proof.SaveStimulus(e.Stimulus); err != nil {
		t.Fatal(err)
	}
	for _, observation := range e.Observations {
		if err := proof.SaveObservation(observation); err != nil {
			t.Fatal(err)
		}
	}
	if err := proof.SaveActivation(e.Activation); err != nil {
		t.Fatal(err)
	}
	for i, proposal := range e.Proposals {
		if err := proof.SaveProposal(proposal, e.Observations[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, receipt := range e.Arbitrations {
		if err := proof.SaveArbitration(receipt, e.Stimulus, e.Activation, e.Proposals, 1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCharacterActivationProofCyclesAreIndependentAndLegacyUnchanged(t *testing.T) {
	st := NewStore(t.TempDir())
	session, first, proof := cycleProofFixture(t, st)
	// Existing one-chapter evidence must not count as a completed cycle.
	if err := st.CharacterAgents.SaveStimulus(first.Evidence.Stimulus); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(characterAgentChapterDir(first.GenerationID, first.Chapter), "stimulus.json")
	legacyBefore, err := st.CharacterAgents.io.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if missing, err := proof.LoadStimulus(first.GenerationID, first.Chapter); err != nil || missing != nil {
		t.Fatalf("cycle fell back to legacy proof: %+v %v", missing, err)
	}
	saveCycleProofInputs(t, proof, first)
	session, err = domain.AppendCharacterActivationCycle(session, first)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, first, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(first.Evidence.Arbitrations[0], first.Evidence.Stimulus, first.Evidence.Proposals...)
	if err != nil {
		t.Fatal(err)
	}
	second := testutil.CharacterCycle(t, 2, first.Digest, &after, first.EndDay)
	secondProof, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if missing, err := secondProof.LoadProposal(first.GenerationID, 1, 1, first.Evidence.Proposals[0].AgentID); err != nil || missing != nil {
		t.Fatalf("second cycle reused first cycle proposal: %+v %v", missing, err)
	}
	if err := secondProof.SaveStimulus(first.Evidence.Stimulus); err == nil {
		t.Fatal("previous cycle stimulus was accepted")
	}
	saveCycleProofInputs(t, secondProof, second)
	if err := secondProof.SaveObservation(first.Evidence.Observations[0]); err == nil {
		t.Fatal("previous cycle observation was accepted")
	}
	if err := secondProof.SaveProposal(first.Evidence.Proposals[0], first.Evidence.Observations[0]); err == nil {
		t.Fatal("previous cycle proposal was accepted")
	}
	for i, view := range []*CharacterAgentStore{proof, secondProof} {
		receipt, err := view.LoadArbitration(first.GenerationID, 1, 1)
		want := []domain.CharacterActivationCycle{first, second}[i].Evidence.Arbitrations[0].Digest
		if err != nil || receipt == nil || receipt.Digest != want {
			t.Fatalf("cycle %d receipt: %+v %v", i+1, receipt, err)
		}
		if _, err := view.LoadActivation(first.GenerationID, 2); err == nil {
			t.Fatal("foreign chapter read was accepted")
		}
		if _, err := view.LoadRegistrySnapshot("pg2_foreign", 1); err == nil {
			t.Fatal("foreign generation read was accepted")
		}
	}
	legacyAfter, err := st.CharacterAgents.io.ReadFile(legacyPath)
	if err != nil || string(legacyAfter) != string(legacyBefore) {
		t.Fatal("cycle writes changed legacy bytes")
	}
	if registry, err := st.CharacterAgents.LoadRegistrySnapshot(first.GenerationID, 1); err != nil || registry != nil {
		t.Fatal("cycle snapshot leaked into legacy namespace")
	}
	if st.CharacterAgents.ActivationCycleIndex() != 0 || proof.ActivationCycleIndex() != 1 || secondProof.ActivationCycleIndex() != 2 {
		t.Fatal("views mutated the shared store")
	}
}

func TestCharacterActivationProofRejectsUnboundStateAndInputs(t *testing.T) {
	st := NewStore(t.TempDir())
	_, cycle, proof := cycleProofFixture(t, st)
	if err := proof.SaveObservation(cycle.Evidence.Observations[0]); err == nil {
		t.Fatal("observation accepted without persisted stimulus")
	}
	bad := cycle.Evidence.Stimulus
	bad.Sources = []string{proof.ActivationCycleSourceToken(), domain.CharacterActivationCycleSourcePrefix + "foreign"}
	if err := proof.SaveStimulus(bad); err == nil {
		t.Fatal("ambiguous cycle source accepted")
	}
	bad = cycle.Evidence.Stimulus
	clock := *bad.StoryClock
	clock.CurrentDay = 0.1
	clock, err := domain.FinalizeStoryClockContext(clock)
	if err != nil {
		t.Fatal(err)
	}
	bad.StoryClock = &clock
	if err := proof.SaveStimulus(bad); err == nil {
		t.Fatal("different initial time accepted")
	}
	if err := proof.SaveStimulus(cycle.Evidence.Stimulus); err != nil {
		t.Fatal(err)
	}
	if err := proof.SaveProposal(cycle.Evidence.Proposals[0], cycle.Evidence.Observations[0]); err == nil {
		t.Fatal("proposal accepted without persisted observation")
	}
	if err := proof.SaveActivation(cycle.Evidence.Activation); err == nil {
		t.Fatal("activation accepted without registry/observations")
	}
	if err := proof.SaveArbitration(cycle.Evidence.Arbitrations[0], cycle.Evidence.Stimulus, cycle.Evidence.Activation, cycle.Evidence.Proposals, 1); err == nil {
		t.Fatal("arbitration accepted without persisted inputs")
	}
}

func TestCharacterActivationProofConcurrentResumeAndSymlinkRejection(t *testing.T) {
	st := NewStore(t.TempDir())
	session, cycle, proof := cycleProofFixture(t, st)
	saveCycleProofInputs(t, proof, cycle)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(st.Dir()).CharacterAgents.ForActivationCycle(session)
			if err != nil {
				t.Error(err)
				return
			}
			// Each instance reads its own value before idempotent publication.
			proposal, err := other.LoadProposal(cycle.GenerationID, 1, 1, cycle.Evidence.Proposals[0].AgentID)
			if err != nil || proposal == nil {
				t.Errorf("resume lost proposal: %v", err)
				return
			}
			observation, err := other.LoadObservation(cycle.GenerationID, 1, 1, proposal.AgentID)
			if err != nil || observation == nil {
				t.Errorf("resume lost observation: %v", err)
				return
			}
			if err := other.SaveProposal(*proposal, *observation); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	unsafeStore := NewStore(t.TempDir())
	unsafeProof, err := unsafeStore.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	root, err := characterActivationSessionDir(cycle.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unsafeStore.CharacterAgents.io.path(root), 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, unsafeStore.CharacterAgents.io.path(filepath.Join(root, "work"))); err != nil {
		t.Fatal(err)
	}
	if err := unsafeProof.SaveStimulus(cycle.Evidence.Stimulus); err == nil {
		t.Fatalf("symlink write not rejected: %v", err)
	}
	if _, err := unsafeProof.LoadStimulus(cycle.GenerationID, 1); err == nil {
		t.Fatal("symlink read not rejected")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("cycle wrote outside the store")
	}
}
