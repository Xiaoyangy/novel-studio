package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Only a real Store view can bind this tool. Neither output JSON nor the
// greatest origin proposal round selects the current arbitration scope.
func NewResolveCharacterArbitrationV3Tool(st *store.Store, session domain.CharacterActivationSession, view *store.CharacterArbitrationV3, round int, protocol string) (*ResolveChapterWorldTool, error) {
	if st == nil || view == nil || !view.BelongsTo(st) || round < 1 || round > 2 {
		return nil, fmt.Errorf("v3 arbitration requires its persisted source view and current round")
	}
	sources, err := view.Sources(round)
	if err != nil {
		return nil, err
	}
	coordinate := sources.Coordinate()
	if sources.SessionDigest() != session.Digest || coordinate.GenerationID != session.GenerationID || coordinate.Chapter != session.Chapter || coordinate.Cycle != len(session.CycleDigests)+1 || sources.CurrentRound() != round {
		return nil, fmt.Errorf("v3 arbitration source does not bind the exact current host session")
	}
	input := sources.Input()
	if !hasToolSourcePolicy(input.Stimulus.Sources, domain.CharacterActivationCyclePolicyV3) {
		return nil, fmt.Errorf("v3 arbitration cannot upgrade a legacy input")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return nil, err
	}
	stored, err := proofs.LoadActivationInputs()
	if err != nil {
		return nil, fmt.Errorf("read v3 arbitration frozen input: %w", err)
	}
	if stored == nil || !sameActivationToolValue(stored, input) {
		return nil, fmt.Errorf("v3 arbitration differs from this Store's frozen input")
	}
	t := NewResolveChapterWorldTool(st, input.Stimulus, input.Activation, sources.EffectiveProposals(), protocol, input.Stimulus.Sources, 1)
	t.cycleProofs, t.arbitrationV3, t.roundSourcesV3 = proofs, view, &sources
	return t, nil
}

func NewSubmitCharacterActivationV3DecisionTool(st *store.Store, session domain.CharacterActivationSession, observation domain.CharacterObservationPacket, view *store.CharacterArbitrationV3) (*SubmitCharacterDecisionTool, error) {
	if st == nil || view == nil || !view.BelongsTo(st) {
		return nil, fmt.Errorf("v3 character decision requires its own persisted Store view")
	}
	input := view.Input()
	if input.Stimulus.GenerationID != session.GenerationID || input.Stimulus.Chapter != session.Chapter || !hasToolSourcePolicy(input.Stimulus.Sources, domain.CharacterActivationCyclePolicyV3) {
		return nil, fmt.Errorf("v3 character decision cannot replace its generation or input policy")
	}
	t, err := NewSubmitCharacterActivationDecisionTool(st, session, observation)
	if err != nil {
		return nil, err
	}
	// SaveProposal refreshes the real session and exact R1/R2 sources while
	// holding the same publication lock, including affected-owner admission.
	t.arbitrationV3 = view
	return t, nil
}

func hasToolSourcePolicy(sources []string, policy string) bool {
	for _, source := range sources {
		if source == policy {
			return true
		}
	}
	return false
}

// A current verified continuation already binds its original proposal. Accept
// its digest only as a settlement-input alias, then retain the canonical source
// in the receipt. Admission history and every non-settlement field stay intact.
// SaveArbitration refreshes this Store's session/sources under the publication
// lock before accepting the finalized result; a captured stale view cannot write.
func (t *ResolveChapterWorldTool) normalizeContinuationSettlementEvidence(receipt domain.WorldArbitrationReceipt) domain.WorldArbitrationReceipt {
	if t.roundSourcesV3 == nil || t.arbitrationV3 == nil || receipt.Digest != "" {
		return receipt
	}
	aliases := map[string]string{}
	for _, grant := range t.roundSourcesV3.Continuations() {
		aliases[grant.Digest] = grant.OriginProposalDigest
	}
	copied := false
	for i, settlement := range receipt.ResourceSettlements {
		var refs []string
		for j, ref := range settlement.EvidenceRefs {
			canonical, exists := aliases[ref]
			if !exists || canonical == ref {
				continue
			}
			if refs == nil {
				refs = append([]string(nil), settlement.EvidenceRefs...)
			}
			refs[j] = canonical
		}
		if refs != nil {
			if !copied {
				receipt.ResourceSettlements = append([]domain.ResourceSettlementV2(nil), receipt.ResourceSettlements...)
				copied = true
			}
			receipt.ResourceSettlements[i].EvidenceRefs = refs
		}
	}
	return receipt
}

func (t *ResolveChapterWorldTool) finalizeBoundArbitration(receipt domain.WorldArbitrationReceipt) (domain.WorldArbitrationReceipt, error) {
	if t.roundSourcesV3 == nil {
		return domain.FinalizeWorldArbitrationReceipt(receipt, t.stimulus, t.activation, t.proposals, t.maxRevisionRounds)
	}
	verified, err := domain.FinalizeCharacterArbitrationRoundV1(*t.roundSourcesV3, receipt)
	if err != nil {
		return domain.WorldArbitrationReceipt{}, err
	}
	return verified.Receipt(), nil
}
