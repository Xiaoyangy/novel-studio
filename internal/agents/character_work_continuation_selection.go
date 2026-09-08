package agents

import (
	"fmt"
	"sort"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// CharacterWorkContinuationSelection partitions the frozen active set. Only
// FreshAgentIDs may be dispatched to a character model. Continuations are host
// authorizations, never fresh proposals or evidence that work has occurred.
type CharacterWorkContinuationSelection struct {
	FreshAgentIDs []string
	Continuations []domain.CharacterWorkContinuationReceiptV1
	WakeReasons   map[string][]string
}

// SelectCharacterWorkContinuations is read-only and deterministic. ledgers is
// the host-selected current candidate for each owner, not a history from which
// this function guesses the newest grant. Callers must supply authenticated
// global boundaries for prior continuation entries. Invalid proof is an error,
// not permission to silently replace an already-paid result with a fresh call.
func SelectCharacterWorkContinuations(input domain.CharacterActivationInputSet, ledgers map[string]domain.CharacterWorkContinuationLedgerV1, boundaries ...domain.VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationSelection, error) {
	empty := CharacterWorkContinuationSelection{}
	if err := domain.ValidateCharacterActivationInputSet(input); err != nil {
		return empty, fmt.Errorf("continuation selection input: %w", err)
	}
	for _, boundary := range boundaries {
		if boundary.GlobalRoot() == "" {
			return empty, fmt.Errorf("continuation selection has an unverified global boundary")
		}
	}
	actors := make(map[string]domain.CharacterAgentActivationEntry, len(input.Activation.Entries))
	activeIDs := make([]string, 0, len(input.Activation.Entries))
	for _, entry := range input.Activation.Entries {
		actors[entry.AgentID] = entry
		if entry.State == domain.CharacterAgentActive {
			activeIDs = append(activeIDs, entry.AgentID)
		}
	}
	sort.Strings(activeIDs)
	ledgerIDs := make([]string, 0, len(ledgers))
	for id := range ledgers {
		ledgerIDs = append(ledgerIDs, id)
	}
	sort.Strings(ledgerIDs)
	eligible := make(map[string]domain.CharacterWorkContinuationEligibilityV1, len(ledgers))
	for _, id := range ledgerIDs {
		actor, exists := actors[id]
		if !exists {
			return empty, fmt.Errorf("continuation ledger has unknown frozen owner %q", id)
		}
		ledger := ledgers[id]
		if ledger.Origin.GenerationID != input.Stimulus.GenerationID || ledger.Origin.Chapter != input.Stimulus.Chapter || !domain.HasCharacterWorkContinuationPolicyV1(input.Stimulus.Sources) {
			return empty, fmt.Errorf("continuation ledger has foreign generation/chapter/policy")
		}
		ownerMatches := false
		for _, proposal := range domain.LatestCharacterCycleProposals(ledger.Origin.Evidence) {
			if proposal.Digest == ledger.ProposalDigest && proposal.AgentID == id && proposal.Character == actor.Character {
				ownerMatches = true
			}
		}
		if !ownerMatches {
			return empty, fmt.Errorf("continuation ledger does not belong to its frozen owner %q", id)
		}
		if actor.State != domain.CharacterAgentActive {
			// A sleeping owner is never dispatched. Its supplied source must
			// nevertheless be genuine; being asleep cannot hide a broken ledger.
			if err := domain.ValidateCharacterWorkContinuationLedgerV1(ledger, boundaries...); err != nil {
				return empty, fmt.Errorf("sleeping continuation ledger: %w", err)
			}
			continue
		}
		result, err := domain.EvaluateCharacterWorkContinuationV1(ledger, input, boundaries...)
		if err != nil {
			return empty, fmt.Errorf("evaluate continuation owner %q: %w", id, err)
		}
		if result.Eligible && (result.Receipt == nil || result.Receipt.AgentID != id) {
			return empty, fmt.Errorf("continuation evaluator returned an unbound authorization")
		}
		eligible[id] = result
	}
	selection := CharacterWorkContinuationSelection{WakeReasons: make(map[string][]string)}
	for _, id := range activeIDs {
		result, exists := eligible[id]
		if result.Eligible {
			selection.Continuations = append(selection.Continuations, *result.Receipt)
			continue
		}
		selection.FreshAgentIDs = append(selection.FreshAgentIDs, id)
		if !exists {
			selection.WakeReasons[id] = []string{"no_continuation_authorization"}
		} else {
			selection.WakeReasons[id] = append([]string(nil), result.WakeReasons...)
		}
	}
	return selection, nil
}
