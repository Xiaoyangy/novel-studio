package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// A source-aware view is constructed from the real store, never JSON. Original
// continued proposals remain at their original cycle paths; only fresh choices
// may exist in this cycle's proposal namespace.
type CharacterContinuationArbitration struct {
	store         *Store
	proofs        *CharacterAgentStore
	prefix        domain.VerifiedCharacterActivationPrefix
	input         domain.CharacterActivationInputSet
	continuations []domain.CharacterWorkContinuationReceiptV1
	fresh         []domain.CharacterDecisionProposal
	effective     []domain.CharacterDecisionProposal
	protocol      string
}

func (s *Store) NewCharacterContinuationArbitration(session domain.CharacterActivationSession, continuations []domain.CharacterWorkContinuationReceiptV1, protocol string) (*CharacterContinuationArbitration, error) {
	if len(continuations) == 0 {
		return nil, fmt.Errorf("continuation arbitration requires explicit continuing sources")
	}
	prefix, err := s.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
	if err != nil {
		return nil, err
	}
	if prefix == nil || prefix.Session().Digest != session.Digest || session.Phase != "collecting" {
		return nil, fmt.Errorf("continuation arbitration has a stale or unverified session")
	}
	proofs, err := s.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return nil, err
	}
	input, err := proofs.LoadActivationInputs()
	if err != nil || input == nil {
		return nil, fmt.Errorf("continuation arbitration lacks its frozen input: %w", err)
	}
	view := &CharacterContinuationArbitration{store: s, proofs: proofs, prefix: *prefix, input: *input, protocol: protocol}
	byOwner := map[string]domain.CharacterWorkContinuationReceiptV1{}
	for _, receipt := range continuations {
		if _, duplicate := byOwner[receipt.AgentID]; duplicate {
			return nil, fmt.Errorf("continuation arbitration repeats an owner")
		}
		ledger, ok := prefix.ContinuationLedger(receipt.AgentID)
		if !ok {
			return nil, fmt.Errorf("continuation arbitration lacks an owner's original authorization")
		}
		if err := domain.ValidateCharacterWorkContinuationReceiptV1(receipt, ledger, *input, prefix.ContinuationBoundaries()...); err != nil {
			return nil, err
		}
		byOwner[receipt.AgentID] = receipt
		for _, proposal := range ledger.Origin.Evidence.Proposals {
			if proposal.Digest == ledger.ProposalDigest {
				view.effective = append(view.effective, proposal)
			}
		}
	}
	for _, entry := range input.Activation.Entries {
		if entry.State != domain.CharacterAgentActive {
			if _, exists := byOwner[entry.AgentID]; exists {
				return nil, fmt.Errorf("continuation cannot execute a sleeping owner")
			}
			continue
		}
		proposal, err := proofs.LoadProposal(session.GenerationID, session.Chapter, 1, entry.AgentID)
		if err != nil {
			return nil, err
		}
		if receipt, continuing := byOwner[entry.AgentID]; continuing {
			if proposal != nil {
				return nil, fmt.Errorf("continued owner also has a fresh proposal in this cycle")
			}
			view.continuations = append(view.continuations, receipt)
			delete(byOwner, entry.AgentID)
		} else {
			if proposal == nil {
				return nil, fmt.Errorf("continuation arbitration lacks a fresh active proposal")
			}
			view.fresh = append(view.fresh, *proposal)
			view.effective = append(view.effective, *proposal)
		}
	}
	if len(byOwner) != 0 || len(view.effective) == 0 {
		return nil, fmt.Errorf("continuation arbitration does not exactly cover current active sources")
	}
	sort.Slice(view.effective, func(i, j int) bool { return view.effective[i].AgentID < view.effective[j].AgentID })
	return view, nil
}

func copyContinuationStoreValue[T any](value T) T {
	raw, _ := json.Marshal(value)
	var copy T
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func (v *CharacterContinuationArbitration) Input() domain.CharacterActivationInputSet {
	return copyContinuationStoreValue(v.input)
}
func (v *CharacterContinuationArbitration) EffectiveProposals() []domain.CharacterDecisionProposal {
	return copyContinuationStoreValue(v.effective)
}
func (v *CharacterContinuationArbitration) Continuations() []domain.CharacterWorkContinuationReceiptV1 {
	return copyContinuationStoreValue(v.continuations)
}

func (v *CharacterContinuationArbitration) FinalizeCycle(receipt domain.WorldArbitrationReceipt) (domain.CharacterActivationCycle, error) {
	session := v.prefix.Session()
	cycle := domain.CharacterActivationCycle{
		Version: domain.CharacterActivationCycleV2Version, GenerationID: session.GenerationID, Chapter: session.Chapter,
		Index: len(session.CycleDigests) + 1, PreviousDigest: session.CycleDigests[len(session.CycleDigests)-1],
		ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: v.input.Digest, WorkContinuations: v.continuations,
		Evidence: domain.CharacterAgentEvidenceBundle{
			Version: domain.CharacterContinuationEvidenceV2Version, GenerationID: session.GenerationID, Chapter: session.Chapter,
			Registry: v.input.Registry, Stimulus: v.input.Stimulus, Activation: v.input.Activation,
			Proposals: v.fresh, Arbitrations: []domain.WorldArbitrationReceipt{receipt}, ProtocolDigest: v.protocol,
		},
	}
	active := map[string]bool{}
	for _, entry := range v.input.Activation.Entries {
		active[entry.AgentID] = entry.State == domain.CharacterAgentActive
	}
	for _, observation := range v.input.Observations {
		if active[observation.AgentID] {
			cycle.Evidence.Observations = append(cycle.Evidence.Observations, observation)
			cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, observation.MemoryRoot)
		}
	}
	return domain.FinalizeCharacterContinuationCycleV2(v.prefix, v.input, cycle)
}

func (v *CharacterContinuationArbitration) LoadArbitration() (*domain.WorldArbitrationReceipt, error) {
	var receipt domain.WorldArbitrationReceipt
	if err := v.proofs.readProof(characterAgentArbitrationPath(v.input.Stimulus.GenerationID, v.input.Stimulus.Chapter, 1), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := v.FinalizeCycle(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (v *CharacterContinuationArbitration) SaveArbitration(receipt domain.WorldArbitrationReceipt) error {
	if _, err := v.FinalizeCycle(receipt); err != nil {
		return err
	}
	session := v.prefix.Session()
	root, err := characterActivationSessionDir(session.GenerationID, session.Chapter)
	if err != nil {
		return err
	}
	return v.store.withCharacterActivationWrite(func() error {
		current, err := v.store.loadVerifiedCharacterActivationPrefix(root, session.GenerationID, session.Chapter)
		if err != nil {
			return err
		}
		if current == nil || current.Session().Digest != session.Digest {
			return fmt.Errorf("continuation arbitration cannot write across a changed session")
		}
		return v.proofs.writeProof(characterAgentArbitrationPath(receipt.GenerationID, receipt.Chapter, receipt.Round), receipt)
	})
}

// Called under the Store's projected read/write lock. Both commit and restart
// authenticate the actual tool artifacts, not just a caller-created envelope.
func (s *Store) validateVerifiedActivationCycleProofs(prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet, cycle domain.CharacterActivationCycle) error {
	if cycle.Version == domain.CharacterActivationCycleV3Version {
		return s.validateArbitrationV3CycleProofs(prefix, input, cycle)
	}
	proofs, err := s.CharacterAgents.ForActivationCycle(prefix.Session())
	if err != nil {
		return err
	}
	same := func(a, b any) bool {
		left, le := json.Marshal(a)
		right, re := json.Marshal(b)
		return le == nil && re == nil && string(left) == string(right)
	}
	frozen, err := proofs.LoadActivationInputs()
	if err != nil || frozen == nil || !same(*frozen, input) {
		return fmt.Errorf("verified cycle lacks its exact frozen input artifact: %w", err)
	}
	for _, observation := range cycle.Evidence.Observations {
		stored, err := proofs.LoadObservation(cycle.GenerationID, cycle.Chapter, observation.Round, observation.AgentID)
		if err != nil || stored == nil || !same(*stored, observation) {
			return fmt.Errorf("verified cycle observation differs from its tool artifact: %w", err)
		}
	}
	for _, proposal := range cycle.Evidence.Proposals {
		stored, err := proofs.LoadProposal(cycle.GenerationID, cycle.Chapter, proposal.Round, proposal.AgentID)
		if err != nil || stored == nil || !same(*stored, proposal) {
			return fmt.Errorf("verified cycle fresh proposal differs from its tool artifact: %w", err)
		}
	}
	for _, continuation := range cycle.WorkContinuations {
		stored, err := proofs.LoadProposal(cycle.GenerationID, cycle.Chapter, 1, continuation.AgentID)
		if err != nil || stored != nil {
			return fmt.Errorf("verified continuer has an unexpected current proposal artifact: %w", err)
		}
	}
	for _, arbitration := range cycle.Evidence.Arbitrations {
		var stored domain.WorldArbitrationReceipt
		if err := proofs.readProof(characterAgentArbitrationPath(cycle.GenerationID, cycle.Chapter, arbitration.Round), &stored); err != nil {
			return err
		}
		if !same(stored, arbitration) {
			return fmt.Errorf("verified cycle arbitration differs from its actual tool receipt")
		}
	}
	return nil
}
