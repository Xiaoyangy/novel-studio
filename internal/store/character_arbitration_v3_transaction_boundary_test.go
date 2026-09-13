package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func transactionProofPath(t *testing.T, v *CharacterArbitrationV3, legacy string) string {
	t.Helper()
	rel, err := v.proofs.proofPath(legacy)
	verifiedStoreMust(t, err)
	return v.proofs.io.path(rel)
}

func transactionWriteProof(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, os.MkdirAll(filepath.Dir(path), 0700))
	verifiedStoreMust(t, os.WriteFile(path, raw, 0600))
}

func transactionRejectWithoutWrites(t *testing.T, st *Store, action func() error) {
	t.Helper()
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if err := action(); err == nil {
		t.Fatal("a public operation trusted an earlier transaction's proof")
	}
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("rejected transaction changed evidence or session state")
	}
}

func TestArbitrationV3TransactionBoundaryRechecksEveryPublicCallAfterTampering(t *testing.T) {
	for _, mutation := range []string{"resigned-proposal", "receipt", "registry", "foreign-owner", "extra-round"} {
		t.Run(mutation, func(t *testing.T) {
			st, _, prefix, input := arbitrationV3Setup(t)
			v := arbitrationV3Prepare(t, st, prefix, input)
			scope, err := v.Sources(1)
			verifiedStoreMust(t, err)
			r := arbitrationV3Receipt(t, scope, nil, false, false)
			verifiedStoreMust(t, v.SaveArbitration(r))
			// Warm every read path on the SAME captured public view first.
			_, err = v.LoadArbitration(1)
			verifiedStoreMust(t, err)
			_, err = v.FinalizeCycle(nil)
			verifiedStoreMust(t, err)
			p := scope.EffectiveProposals()[0]
			var o domain.CharacterObservationPacket
			for _, original := range input.Observations {
				if original.AgentID == p.AgentID {
					o = original
				}
			}
			gen, ch := input.Stimulus.GenerationID, input.Stimulus.Chapter
			switch mutation {
			case "resigned-proposal":
				changed := verifiedStoreCopy(p)
				changed.DecisionReason = "a different independently signed source"
				changed, err = domain.FinalizeCharacterDecisionProposal(changed, o)
				verifiedStoreMust(t, err)
				transactionWriteProof(t, transactionProofPath(t, v, characterAgentProposalPath(gen, ch, 1, p.AgentID)), changed)
			case "receipt":
				changed := verifiedStoreCopy(r)
				changed.Resolutions[0].StateAfter = "changed after a successful public load"
				transactionWriteProof(t, transactionProofPath(t, v, characterAgentArbitrationPath(gen, ch, 1)), changed)
			case "registry":
				changed := verifiedStoreCopy(input.Registry)
				changed.Entries[0].Character = "different source owner"
				transactionWriteProof(t, transactionProofPath(t, v, characterAgentRegistrySnapshotPath(gen, ch)), changed)
			case "foreign-owner":
				transactionWriteProof(t, transactionProofPath(t, v, characterAgentProposalPath(gen, ch, 1, "ca_foreign")), p)
			case "extra-round":
				transactionWriteProof(t, transactionProofPath(t, v, characterAgentArbitrationPath(gen, ch, 3)), r)
			}
			calls := []struct {
				name string
				run  func() error
			}{
				{"sources", func() error { _, err := v.Sources(1); return err }},
				{"arbitration", func() error { _, err := v.LoadArbitration(1); return err }},
				{"finalize", func() error { _, err := v.FinalizeCycle(nil); return err }},
				{"save-observation", func() error { return v.SaveObservation(o) }},
				{"save-proposal", func() error { return v.SaveProposal(p) }},
				{"save-arbitration", func() error { return v.SaveArbitration(r) }},
				{"reopen-store", func() error { _, err := NewStore(st.Dir()).LoadCharacterArbitrationV3(gen, ch); return err }},
			}
			for _, call := range calls {
				t.Run(call.name, func(t *testing.T) { transactionRejectWithoutWrites(t, st, call.run) })
			}
		})
	}
}

func TestArbitrationV3TransactionBoundaryPartialR2AndLocalRoundOutputs(t *testing.T) {
	st, _, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	scope, err := v.Sources(1)
	verifiedStoreMust(t, err)
	proposals := scope.EffectiveProposals()
	if len(proposals) < 2 {
		t.Fatal("fixture needs two affected owners")
	}
	ids := []string{proposals[0].AgentID, proposals[1].AgentID}
	r1 := arbitrationV3Receipt(t, scope, ids, false, false)
	verifiedStoreMust(t, v.SaveArbitration(r1))
	obs, revised := arbitrationV3Revisions(t, scope, r1, ids)
	verifiedStoreMust(t, v.SaveObservation(obs[0]))
	verifiedStoreMust(t, v.SaveProposal(revised[0]))
	gen, ch := input.Stimulus.GenerationID, input.Stimulus.Chapter
	reopened, err := NewStore(st.Dir()).LoadCharacterArbitrationV3(gen, ch)
	verifiedStoreMust(t, err)
	if reopened == nil {
		t.Fatal("a valid partial R2 became unrecoverable")
	}
	transactionRejectWithoutWrites(t, st, func() error { _, err := reopened.Sources(2); return err })
	transactionRejectWithoutWrites(t, st, func() error { _, err := reopened.FinalizeCycle(nil); return err })

	// An orphan P2 must remain visible even though the legacy proposal loader
	// returns nil without its O2. This is corruption only in a temporary fixture.
	orphan := transactionProofPath(t, v, characterAgentProposalPath(gen, ch, 2, ids[1]))
	transactionWriteProof(t, orphan, revised[1])
	transactionRejectWithoutWrites(t, st, func() error { _, err := v.LoadArbitration(1); return err })
	transactionRejectWithoutWrites(t, st, func() error { return v.SaveObservation(obs[1]) })
	transactionRejectWithoutWrites(t, st, func() error {
		return st.ProjectedV2().withProjectedReadLock(func() error {
			tx, err := st.newCharacterArbitrationV3(gen, ch)
			if err != nil {
				return err
			}
			if err := tx.loadAdmission(); err != nil {
				return err
			}
			var rounds [2]*domain.VerifiedCharacterArbitrationRoundV1
			err = tx.validatePartialProofsWithRounds(&rounds)
			if err == nil || !strings.Contains(err.Error(), "path-bound observation") {
				t.Fatalf("expected orphan after R1 verification, got %v", err)
			}
			if rounds[0] != nil || rounds[1] != nil {
				t.Fatal("failed complete walk leaked a successful partial round output")
			}
			return err
		})
	})
	verifiedStoreMust(t, os.Remove(orphan)) // Remove only our injected fixture corruption.
	// The same captured public view must observe newly saved O2/P2, never an
	// earlier partial result or cached absence of the second owner's proposal.
	verifiedStoreMust(t, v.SaveObservation(obs[1]))
	verifiedStoreMust(t, v.SaveProposal(revised[1]))
	s2, err := v.Sources(2)
	verifiedStoreMust(t, err)
	r2 := arbitrationV3Receipt(t, s2, nil, false, false)
	verifiedStoreMust(t, v.SaveArbitration(r2))
	want, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	if len(want.Evidence.Arbitrations) != 2 {
		t.Fatal("complete R2 lost an actual round")
	}
	verifiedStoreMust(t, st.ProjectedV2().withProjectedReadLock(func() error {
		tx, err := st.newCharacterArbitrationV3(gen, ch)
		if err != nil {
			return err
		}
		if err := tx.loadAdmission(); err != nil {
			return err
		}
		// A caller-supplied non-authority seed is ignored, never reused as a cache.
		fake := domain.VerifiedCharacterArbitrationRoundV1{}
		rounds := [2]*domain.VerifiedCharacterArbitrationRoundV1{&fake, &fake}
		if err := tx.validatePartialProofsWithRounds(&rounds); err != nil {
			return err
		}
		if rounds[0] == nil || rounds[1] == nil || !jsonValuesEqual(rounds[0].Receipt(), r1) || !jsonValuesEqual(rounds[1].Receipt(), r2) {
			t.Fatal("successful transaction did not return its exact current source rounds")
		}
		got, err := tx.finalizeCycleWithRounds(nil, rounds)
		if err != nil {
			return err
		}
		if !jsonValuesEqual(got, want) || got.Digest != want.Digest {
			t.Fatal("local round reuse changed the complete canonical cycle")
		}
		copyReceipt := rounds[1].Receipt()
		copyReceipt.Resolutions[0].StateAfter = "mutated detached output"
		originalInput := rounds[1].Sources().Input()
		copyInput := rounds[1].Sources().Input()
		if len(copyInput.Observations) == 0 || len(copyInput.Observations[0].KnownFacts) == 0 {
			t.Fatal("fixture needs a mutable nested source fact")
		}
		copyInput.Observations[0].KnownFacts[0].Text = "mutated detached source"
		if !jsonValuesEqual(rounds[1].Receipt(), r2) {
			t.Fatal("returned round receipt is a mutable alias")
		}
		if !jsonValuesEqual(rounds[1].Sources().Input(), originalInput) {
			t.Fatal("returned round input is a mutable source alias")
		}
		return nil
	}))
	loaded, err := v.LoadArbitration(2)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(loaded, &r2) {
		t.Fatal("transaction-local getter mutation polluted a later public operation")
	}
	_, err = st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, want)
	verifiedStoreMust(t, err)
	loadedPrefix, err := NewStore(st.Dir()).LoadVerifiedCharacterActivationPrefix(gen, ch)
	verifiedStoreMust(t, err)
	if loadedPrefix == nil || len(loadedPrefix.Session().CycleDigests) != 1 || loadedPrefix.Session().CycleDigests[0] != want.Digest {
		t.Fatal("actual append/reopen lost the locally verified complete cycle")
	}
}
