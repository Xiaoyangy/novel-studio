package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func (v *CharacterArbitrationV3) active(agentID string) bool {
	for _, e := range v.input.Activation.Entries {
		if e.AgentID == agentID {
			return e.State == domain.CharacterAgentActive
		}
	}
	return false
}

// The selection must precede paid choices. Missing admission metadata after a
// paid proof is corruption, not permission to choose a new source assignment.
func (v *CharacterArbitrationV3) requireNoPaidProofs() error {
	gen, ch := v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter
	var paths []string
	for round := 1; round <= 2; round++ {
		paths = append(paths, characterAgentArbitrationPath(gen, ch, round))
		for _, e := range v.input.Registry.Entries {
			paths = append(paths, characterAgentProposalPath(gen, ch, round, e.AgentID))
		}
	}
	for _, path := range paths {
		path, err := v.proofs.proofPath(path)
		if err != nil {
			return err
		}
		_, err = os.Lstat(v.proofs.io.path(path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("v3 paid proof has lost its preceding immutable admission")
	}
	return nil
}

func (v *CharacterArbitrationV3) continuing(agentID string) bool {
	for _, c := range v.admission.Continuations {
		if c.AgentID == agentID {
			return true
		}
	}
	return false
}

// Validate one revision independently so a crash after any subset of affected
// owners is recoverable. Full exact affected-set coverage remains the domain
// source resolver's responsibility before the R2 arbiter can run.
func (v *CharacterArbitrationV3) validateObservation(o domain.CharacterObservationPacket, first *domain.VerifiedCharacterArbitrationRoundV1) error {
	var baseline *domain.CharacterObservationPacket
	for _, frozen := range v.input.Observations {
		if frozen.AgentID == o.AgentID {
			copy := frozen
			baseline = &copy
		}
	}
	if baseline == nil {
		return fmt.Errorf("v3 observation owner is not in frozen input")
	}
	if o.Round == 1 {
		if !jsonValuesEqual(*baseline, o) {
			return fmt.Errorf("v3 R1 observation changed frozen input")
		}
		return nil
	}
	if o.Round != 2 || first == nil || !v.active(o.AgentID) {
		return fmt.Errorf("v3 revision lacks an active owner and durable R1")
	}
	r1 := first.Receipt()
	if r1.Finalized || r1.HardContractStatus != "feasible" {
		return fmt.Errorf("v3 terminal R1 does not authorize a revision")
	}
	feedback := domain.CharacterArbitrationFeedbackForOwnerV1(r1, o.AgentID, v.input.Stimulus.Sources)
	if len(feedback) == 0 {
		return fmt.Errorf("v3 revision owner is not affected by the actual R1")
	}
	expected := copyContinuationStoreValue(*baseline)
	expected.Round, expected.GeneratedAt, expected.ConflictFeedback = 2, o.GeneratedAt, feedback
	expected, err := domain.FinalizeCharacterObservationPacket(expected)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(expected, o) {
		return fmt.Errorf("v3 revision changed frozen knowledge or actual owner feedback")
	}
	return nil
}

func (v *CharacterArbitrationV3) validateProposal(p domain.CharacterDecisionProposal, o domain.CharacterObservationPacket) error {
	if !v.active(p.AgentID) || (p.Round == 1 && v.continuing(p.AgentID)) {
		return fmt.Errorf("v3 proposal conflicts with frozen active/continuing selection")
	}
	checked, err := domain.FinalizeCharacterDecisionProposal(copyContinuationStoreValue(p), o)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(checked, p) {
		return fmt.Errorf("v3 proposal is not its exact signed observation choice")
	}
	return nil
}

// New V3 callers use these locked writers for both first and revised choices.
// Existing proof writers and their v1/v2 behavior are unchanged.
func (v *CharacterArbitrationV3) SaveObservation(o domain.CharacterObservationPacket) error {
	return v.store.withCharacterActivationWrite(func() error {
		next, err := v.refresh()
		if err != nil {
			return err
		}
		var first *domain.VerifiedCharacterArbitrationRoundV1
		if o.Round == 2 {
			first, err = next.loadRound(1)
			if err != nil {
				return err
			}
		}
		if err := next.validateObservation(o, first); err != nil {
			return err
		}
		return next.proofs.writeProof(characterAgentObservationPath(o.GenerationID, o.Chapter, o.Round, o.AgentID), o)
	})
}

func (v *CharacterArbitrationV3) SaveProposal(p domain.CharacterDecisionProposal) error {
	return v.store.withCharacterActivationWrite(func() error {
		next, err := v.refresh()
		if err != nil {
			return err
		}
		if p.GenerationID != next.input.Stimulus.GenerationID || p.Chapter != next.input.Stimulus.Chapter || (p.Round != 1 && p.Round != 2) {
			return fmt.Errorf("v3 proposal has foreign source identity")
		}
		o, err := next.proofs.LoadObservation(p.GenerationID, p.Chapter, p.Round, p.AgentID)
		if err != nil {
			return err
		}
		if o == nil {
			return fmt.Errorf("v3 proposal lacks its exact durable observation")
		}
		var first *domain.VerifiedCharacterArbitrationRoundV1
		if p.Round == 2 {
			first, err = next.loadRound(1)
			if err != nil {
				return err
			}
		}
		if err := next.validateObservation(*o, first); err != nil {
			return err
		}
		if err := next.validateProposal(p, *o); err != nil {
			return err
		}
		return next.proofs.writeProof(characterAgentProposalPath(p.GenerationID, p.Chapter, p.Round, p.AgentID), p)
	})
}

// Reject hidden/foreign/extra-round artifacts, not just the subset named by an
// incoming cycle. Walk only the bounded current proof namespace, never history.
func (v *CharacterArbitrationV3) validateProofInventory() error {
	base, err := v.proofs.proofPath(filepath.Join(characterAgentChapterDir(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter), "inputs.json"))
	if err != nil {
		return err
	}
	root := filepath.Dir(base)
	owners := map[string]bool{}
	for _, e := range v.input.Registry.Entries {
		owners[e.AgentID+".json"] = true
	}
	for _, kind := range []string{"observations", "proposals"} {
		path := filepath.Join(root, kind)
		if err := validateCharacterMemoryPublicationPath(v.proofs.io, filepath.Join(path, ".inventory-check")); err != nil {
			return err
		}
		rounds, err := os.ReadDir(v.proofs.io.path(path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, round := range rounds {
			if !round.IsDir() || (round.Name() != "round-01" && round.Name() != "round-02") {
				return fmt.Errorf("v3 proof contains a foreign round namespace")
			}
			files, err := os.ReadDir(v.proofs.io.path(filepath.Join(path, round.Name())))
			if err != nil {
				return err
			}
			for _, file := range files {
				if !owners[file.Name()] || file.IsDir() || file.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("v3 proof contains a foreign owner or unsafe file")
				}
			}
		}
	}
	files, err := os.ReadDir(v.proofs.io.path(root))
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "arbitration-") && (file.Name() != "arbitration-round-01.json" && file.Name() != "arbitration-round-02.json") {
			return fmt.Errorf("v3 proof contains an extra arbitration round")
		}
	}
	return nil
}

func (v *CharacterArbitrationV3) validatePartialProofs() error {
	return v.validatePartialProofsWithRounds(nil)
}

// Any returned rounds belong only to this lock-held read. Inventory and all
// frozen/partial owner artifacts are always checked; no prior result is read
// from out, and nothing is published there until the entire walk succeeds.
func (v *CharacterArbitrationV3) validatePartialProofsWithRounds(out *[2]*domain.VerifiedCharacterArbitrationRoundV1) error {
	if err := v.validateProofInventory(); err != nil {
		return err
	}
	gen, chapter := v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter
	if err := exactArbitrationV3Proof(v.proofs, characterAgentRegistrySnapshotPath(gen, chapter), v.input.Registry); err != nil {
		return err
	}
	if err := exactArbitrationV3Proof(v.proofs, filepath.Join(characterAgentChapterDir(gen, chapter), "stimulus.json"), v.input.Stimulus); err != nil {
		return err
	}
	if err := exactArbitrationV3Proof(v.proofs, filepath.Join(characterAgentChapterDir(gen, chapter), "activation.json"), v.input.Activation); err != nil {
		return err
	}
	for _, o := range v.input.Observations {
		if err := exactArbitrationV3Proof(v.proofs, characterAgentObservationPath(gen, chapter, 1, o.AgentID), o); err != nil {
			return err
		}
	}
	// Read proposals directly as well as through their observation. The legacy
	// loader returns nil when O is absent, which must not hide an orphan P here.
	var first *domain.VerifiedCharacterArbitrationRoundV1
	var rounds [2]*domain.VerifiedCharacterArbitrationRoundV1
	for round := 1; round <= 2; round++ {
		for _, entry := range v.input.Registry.Entries {
			var o domain.CharacterObservationPacket
			err := v.proofs.readProof(characterAgentObservationPath(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, round, entry.AgentID), &o)
			hasObservation := err == nil
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if hasObservation {
				if o.AgentID != entry.AgentID || o.Round != round {
					return fmt.Errorf("v3 observation path identity mismatch")
				}
				if err := v.validateObservation(o, first); err != nil {
					return err
				}
			}
			var p domain.CharacterDecisionProposal
			err = v.proofs.readProof(characterAgentProposalPath(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, round, entry.AgentID), &p)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !hasObservation || p.AgentID != entry.AgentID || p.Round != round {
				return fmt.Errorf("v3 proposal lacks its exact path-bound observation")
			}
			if err := v.validateProposal(p, o); err != nil {
				return err
			}
		}
		verified, err := v.loadRoundAfterFirst(round, first)
		if err != nil {
			return err
		}
		if round == 1 {
			first = verified
		}
		rounds[round-1] = verified
	}
	if out != nil {
		*out = rounds
	}
	return nil
}

func exactArbitrationV3Proof[T any](proofs *CharacterAgentStore, path string, expected T) error {
	var stored T
	if err := proofs.readProof(path, &stored); err != nil {
		return fmt.Errorf("v3 frozen source artifact missing or unreadable: %w", err)
	}
	if !jsonValuesEqual(stored, expected) {
		return fmt.Errorf("v3 frozen source artifact differs from frozen input")
	}
	return nil
}

func (s *Store) validateArbitrationV3CycleProofs(prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet, cycle domain.CharacterActivationCycle) error {
	v, err := s.characterArbitrationV3ForPrefix(prefix)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(v.input, input) {
		return fmt.Errorf("v3 cycle changed exact frozen input")
	}
	if err := v.loadAdmission(); err != nil {
		return err
	}
	var rounds [2]*domain.VerifiedCharacterArbitrationRoundV1
	if err := v.validatePartialProofsWithRounds(&rounds); err != nil {
		return err
	}
	expected, err := v.finalizeCycleWithRounds(cycle.Evidence.Usage, rounds)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(expected, cycle) {
		return fmt.Errorf("v3 cycle differs from actual complete round artifacts/admission")
	}
	return nil
}

func (s *Store) validateStoredActivationChapterV3(root, generation string, chapter int, evidence domain.CharacterActivationChapterEvidence, verified ...domain.VerifiedCharacterActivationPrefix) error {
	var prefix *domain.VerifiedCharacterActivationPrefix
	if len(verified) == 1 {
		prefix = &verified[0]
	} else if len(verified) == 0 {
		var err error
		prefix, err = s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("chapter validation requires one exact source prefix")
	}
	if prefix == nil || prefix.Session().Phase != "ready" || !jsonValuesEqual(prefix.Session(), evidence.Session) {
		return fmt.Errorf("v3 chapter is not its actual ready committed prefix")
	}
	steps := prefix.Steps()
	if len(steps) != len(evidence.Cycles) || len(steps) != len(evidence.Inputs) || len(steps) != len(evidence.Reviews) {
		return fmt.Errorf("v3 chapter lost source steps")
	}
	var context domain.CharacterReadinessContext
	if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), &context); err != nil {
		return err
	}
	if !jsonValuesEqual(context, evidence.Context) {
		return fmt.Errorf("v3 chapter changed frozen context")
	}
	for i, step := range steps {
		if !jsonValuesEqual(step.Cycle(), evidence.Cycles[i]) || !jsonValuesEqual(step.Input(), evidence.Inputs[i]) {
			return fmt.Errorf("v3 chapter differs from actual cycle/frozen input")
		}
		var audit domain.CharacterReadinessReviewAudit
		if err := s.readCharacterActivationJSON(characterReadinessAuditPath(root, i+1), &audit); err != nil {
			return err
		}
		if !jsonValuesEqual(audit, evidence.Reviews[i]) {
			return fmt.Errorf("v3 chapter differs from durable review audit")
		}
	}
	return nil
}
