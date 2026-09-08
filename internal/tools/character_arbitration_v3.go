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
