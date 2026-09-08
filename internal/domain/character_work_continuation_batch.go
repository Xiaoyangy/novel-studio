package domain

import (
	"fmt"
	"math"
	"slices"
	"sort"
)

type CharacterWorkContinuationBatchItemV1 struct {
	Ledger  CharacterWorkContinuationLedgerV1  `json:"ledger"`
	Receipt CharacterWorkContinuationReceiptV1 `json:"receipt"`
}

// Peer proof references never embed the other owner's ledger recursively.
// Current creation verifies that ledger in the batch. Historical replay needs
// the complete global commit to authenticate every member of this source set.
type CharacterWorkContinuationPeerSourceV1 struct {
	Proposal CharacterDecisionProposal          `json:"proposal"`
	Receipt  CharacterWorkContinuationReceiptV1 `json:"receipt"`
}

type CharacterWorkContinuationBatchOwnerV1 struct {
	AgentID string                               `json:"agent_id"`
	Ledger  CharacterWorkContinuationLedgerV1    `json:"ledger"`
	Entry   CharacterWorkContinuationExecutionV1 `json:"entry"`
}

type CharacterWorkContinuationBatchResultV1 struct {
	Owners                  []CharacterWorkContinuationBatchOwnerV1 `json:"owners"`
	PhysicalState           WorldPhysicalStateV2                    `json:"physical_state"`
	AfterPhysicalRoot       string                                  `json:"after_physical_root"`
	ContinuationSourcesRoot string                                  `json:"continuation_sources_root"`
}

// AppendCharacterWorkContinuationBatchV1 authenticates every continuing source
// and every fresh proposal BEFORE validating one shared world arbitration. It
// never edits an original proposal, observation, activation, or receipt. No
// partial result is returned on failure. The resulting entries are not global
// commit authority: the host must commit and verify the full global cycle.
func AppendCharacterWorkContinuationBatchV1(input CharacterActivationInputSet, items []CharacterWorkContinuationBatchItemV1, fresh []CharacterDecisionProposal, arbitration WorldArbitrationReceipt, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationBatchResultV1, error) {
	var empty CharacterWorkContinuationBatchResultV1
	if len(items) == 0 {
		return empty, fmt.Errorf("continuation batch has no continuing owner")
	}
	if err := ValidateCharacterActivationInputSet(input); err != nil {
		return empty, err
	}
	states := map[string]continuationStateV1{}
	byOwner := map[string]CharacterWorkContinuationBatchItemV1{}
	var sources []CharacterWorkContinuationPeerSourceV1
	for _, item := range items {
		state, err := validateContinuationLedgerV1(item.Ledger, boundaries)
		if err != nil {
			return empty, err
		}
		owner := state.proposal.AgentID
		if _, exists := states[owner]; exists {
			return empty, fmt.Errorf("continuation batch duplicates an owner")
		}
		eligible, err := evaluateWorkContinuationV1(item.Ledger, state, input, boundaries)
		if err != nil {
			return empty, err
		}
		if !eligible.Eligible || !samePhysicalValueV2(eligible.Receipt, &item.Receipt) {
			return empty, fmt.Errorf("continuation batch has an ineligible or altered authorization")
		}
		states[owner], byOwner[owner] = state, item
		sources = append(sources, CharacterWorkContinuationPeerSourceV1{Proposal: state.proposal, Receipt: item.Receipt})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Proposal.AgentID < sources[j].Proposal.AgentID })
	fresh = continuationCloneV1(fresh)
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].AgentID < fresh[j].AgentID })
	proposals, sourceRoot, err := continuationBatchSourcesV1(input, sources, fresh)
	if err != nil {
		return empty, err
	}
	after, root, err := validateContinuationBatchArbitrationV1(input, sources, proposals, arbitration)
	if err != nil {
		return empty, err
	}
	result := CharacterWorkContinuationBatchResultV1{PhysicalState: after, AfterPhysicalRoot: root, ContinuationSourcesRoot: sourceRoot}
	for _, source := range sources {
		owner := source.Proposal.AgentID
		item := byOwner[owner]
		entry, _, err := deriveContinuationBatchEntryV1(item.Ledger, states[owner], input, source.Receipt, arbitration, fresh, sources, sourceRoot, after, root)
		if err != nil {
			return empty, err
		}
		ledger := continuationCloneV1(item.Ledger)
		ledger.Entries = append(ledger.Entries, entry)
		ledger.Digest, err = continuationLedgerDigestV1(ledger)
		if err != nil {
			return empty, err
		}
		result.Owners = append(result.Owners, CharacterWorkContinuationBatchOwnerV1{AgentID: owner, Ledger: ledger, Entry: entry})
	}
	return result, nil
}

// Source scope checks are mechanical. Original continuation observations are
// authenticated by the input ledgers (or the verified historical global root),
// while fresh proposals must bind the CURRENT owner's frozen observation.
func continuationBatchSourcesV1(input CharacterActivationInputSet, sources []CharacterWorkContinuationPeerSourceV1, fresh []CharacterDecisionProposal) ([]CharacterDecisionProposal, string, error) {
	beforeRoot, err := CharacterPhysicalRootForCycle(*input.Stimulus.PhysicalState)
	if err != nil {
		return nil, "", err
	}
	active := map[string]bool{}
	for _, actor := range input.Activation.Entries {
		if actor.State == CharacterAgentActive {
			active[actor.AgentID] = true
		}
	}
	observations := map[string]CharacterObservationPacket{}
	for _, o := range input.Observations {
		observations[o.AgentID] = o
	}
	var proposals []CharacterDecisionProposal
	type binding struct{ AgentID, Kind, Proposal, Continuation string }
	var bindings []binding
	for _, source := range sources {
		p, r := source.Proposal, source.Receipt
		if !active[p.AgentID] {
			return nil, "", fmt.Errorf("continuation/fresh source overlaps, duplicates, or executes an inactive owner")
		}
		delete(active, p.AgentID)
		digest, err := ComputeCharacterDecisionProposalDigest(p)
		if err != nil || digest != p.Digest || p.Digest != r.OriginProposalDigest || p.AgentID != r.AgentID || p.ObservationDigest != r.OriginObservationDigest {
			return nil, "", fmt.Errorf("continuation source changed original proposal identity/digest")
		}
		digest, err = ComputeCharacterWorkContinuationReceiptV1Digest(r)
		if err != nil || digest != r.Digest {
			return nil, "", fmt.Errorf("continuation source receipt digest mismatch")
		}
		o := observations[p.AgentID]
		if r.Version != CharacterWorkContinuationPolicyV1 || r.GenerationID != input.Stimulus.GenerationID || r.Chapter != input.Stimulus.Chapter || p.GenerationID != r.GenerationID || p.Chapter != r.Chapter || p.Character != o.Character || r.InputSetDigest != input.Digest || r.StimulusDigest != input.Stimulus.Digest || r.ObservationDigest != o.Digest || r.BeforePhysicalRoot != beforeRoot || o.CycleContext == nil || r.Cycle != o.CycleContext.Index || r.StartDay != input.Stimulus.StoryClock.CurrentDay {
			return nil, "", fmt.Errorf("continuation source is not bound to shared current input")
		}
		if len(p.SelfTasks) != 1 || p.SelfTasks[0].TaskID != r.TaskID || p.SelfTasks[0].Kind != "work" || len(p.SelfTasks[0].ObservationRequests) != 0 || p.Round != 1 || p.Location != o.Location || len(p.Communications)+len(p.ResourceReads)+len(p.ResourceMeasurements)+len(p.ResourceReports)+len(p.ResourceEstimates) != 0 {
			return nil, "", fmt.Errorf("continuation batch exceeded pure stationary work scope")
		}
		if len(p.WorkContinuations) != 1 || p.WorkContinuations[0].TaskID != r.TaskID {
			return nil, "", fmt.Errorf("continuation batch source lacks original grant")
		}
		grant := p.WorkContinuations[0]
		if !physicalAmountV2(p.SelfTasks[0].ProgressTarget) || *p.SelfTasks[0].ProgressTarget <= 0 || grant.UntilTarget == (grant.MaxEffectiveMinutes != nil) || (grant.MaxEffectiveMinutes != nil && (!physicalAmountV2(grant.MaxEffectiveMinutes) || *grant.MaxEffectiveMinutes <= 0)) || math.IsNaN(r.EffectiveCompleted) || math.IsInf(r.EffectiveCompleted, 0) || r.EffectiveCompleted < 0 || math.IsNaN(r.AuthorizedRemaining) || math.IsInf(r.AuthorizedRemaining, 0) || r.AuthorizedRemaining <= 0 {
			return nil, "", fmt.Errorf("continuation batch source has invalid finite authorization")
		}
		grantDigest, err := characterAgentDigest(p.WorkContinuations[0])
		if err != nil || grantDigest != r.AuthorizationDigest {
			return nil, "", fmt.Errorf("continuation batch grant identity mismatch")
		}
		proposals = append(proposals, p)
		bindings = append(bindings, binding{p.AgentID, "continuation", p.Digest, r.Digest})
	}
	for _, p := range fresh {
		if !active[p.AgentID] {
			return nil, "", fmt.Errorf("fresh/continuation source overlaps, duplicates, or executes an inactive owner")
		}
		delete(active, p.AgentID)
		valid, err := FinalizeCharacterDecisionProposal(p, observations[p.AgentID])
		if err != nil || !samePhysicalValueV2(valid, p) {
			return nil, "", fmt.Errorf("fresh batch proposal does not bind its current observation")
		}
		proposals = append(proposals, p)
		bindings = append(bindings, binding{p.AgentID, "fresh", p.Digest, ""})
	}
	if len(active) != 0 {
		return nil, "", fmt.Errorf("continuation batch does not cover the complete active set")
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].AgentID < bindings[j].AgentID })
	root, err := characterAgentDigest(bindings)
	return proposals, root, err
}

func validateContinuationBatchArbitrationV1(input CharacterActivationInputSet, sources []CharacterWorkContinuationPeerSourceV1, proposals []CharacterDecisionProposal, arbitration WorldArbitrationReceipt) (WorldPhysicalStateV2, string, error) {
	var empty WorldPhysicalStateV2
	if !arbitration.Finalized || arbitration.HardContractStatus != "feasible" {
		return empty, "", fmt.Errorf("continuation batch requires finalized feasible arbitration")
	}
	owners := map[string]bool{}
	for _, source := range sources {
		owners[source.Proposal.AgentID] = true
	}
	for _, delivery := range arbitration.ResourceDeliveries {
		if owners[delivery.FromAgentID] {
			return empty, "", fmt.Errorf("continuation cannot deliver resources")
		}
	}
	for _, reception := range arbitration.PassiveReceptions {
		if owners[reception.FromAgentID] {
			return empty, "", fmt.Errorf("continuation cannot repeat communication")
		}
	}
	valid, err := FinalizeWorldArbitrationReceipt(continuationCloneV1(arbitration), input.Stimulus, input.Activation, proposals, 1)
	if err != nil {
		return empty, "", err
	}
	if !samePhysicalValueV2(valid, arbitration) {
		return empty, "", fmt.Errorf("batch arbitration bytes/digest are not finalized")
	}
	// One shared post-state for the ENTIRE batch, never one resource ledger per
	// continuing owner. The existing finalizer also verifies this same kernel.
	after, err := ApplyArbitrationPhysicalStateV2(arbitration, input.Stimulus, proposals...)
	if err != nil {
		return empty, "", err
	}
	root, err := CharacterPhysicalRootForCycle(after)
	return after, root, err
}

func deriveContinuationBatchEntryV1(ledger CharacterWorkContinuationLedgerV1, state continuationStateV1, input CharacterActivationInputSet, receipt CharacterWorkContinuationReceiptV1, arbitration WorldArbitrationReceipt, fresh []CharacterDecisionProposal, sources []CharacterWorkContinuationPeerSourceV1, sourceRoot string, after WorldPhysicalStateV2, root string) (CharacterWorkContinuationExecutionV1, continuationStateV1, error) {
	var empty CharacterWorkContinuationExecutionV1
	resolution, err := continuationResolutionV1(arbitration, state.proposal)
	if err != nil {
		return empty, state, err
	}
	minutes, status, err := continuationSegmentV1(resolution, ledger.TaskID)
	if err != nil {
		return empty, state, err
	}
	if minutes > receipt.AuthorizedRemaining+1e-9 {
		return empty, state, fmt.Errorf("batch owner exceeded explicit continuation allowance")
	}
	for _, actor := range after.Actors {
		if actor.AgentID == state.proposal.AgentID && actor.Location != state.proposal.Location {
			return empty, state, fmt.Errorf("batch continuation cannot relocate owner")
		}
	}
	entry := CharacterWorkContinuationExecutionV1{Input: continuationCloneV1(input), Continuation: receipt, Arbitration: continuationCloneV1(arbitration), FreshPeerProposals: continuationCloneV1(fresh), Executions: continuationCloneV1(resolution.SelfExecutions), AfterPhysicalRoot: root}
	if len(sources) > 1 {
		entry.ContinuationSourcesRoot = sourceRoot
		for _, source := range sources {
			if source.Proposal.AgentID != state.proposal.AgentID {
				entry.PeerContinuations = append(entry.PeerContinuations, continuationCloneV1(source))
			}
		}
	}
	entry.EntryDigest, err = continuationEntryDigestV1(entry)
	if err != nil {
		return empty, state, err
	}
	state.completed += minutes
	state.grantedWork += minutes
	state.status, state.day, state.cycle, state.physical = status, arbitration.StoryTime.EndDay, receipt.Cycle, after
	state.feedback = continuationCloneV1(state.feedback)
	text, err := CharacterActivationPrivateOutcome(state.proposal, resolution, after, arbitration)
	if err != nil {
		return empty, state, err
	}
	state.feedback[arbitration.Digest] = text
	return entry, state, nil
}

func continuationEntryDigestV1(entry CharacterWorkContinuationExecutionV1) (string, error) {
	if entry.ContinuationSourcesRoot == "" {
		return characterAgentDigest(struct{ Input, Authorization, Arbitration, Before, After string }{entry.Input.Digest, entry.Continuation.Digest, entry.Arbitration.Digest, entry.Continuation.BeforePhysicalRoot, entry.AfterPhysicalRoot})
	}
	return characterAgentDigest(struct{ Policy, Input, Authorization, Arbitration, Before, After, Sources string }{"work-continuation-batch.v1", entry.Input.Digest, entry.Continuation.Digest, entry.Arbitration.Digest, entry.Continuation.BeforePhysicalRoot, entry.AfterPhysicalRoot, entry.ContinuationSourcesRoot})
}

func authenticateCommittedContinuationBatchEntryV1(ledger CharacterWorkContinuationLedgerV1, state continuationStateV1, entry CharacterWorkContinuationExecutionV1, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationExecutionV1, continuationStateV1, error) {
	var empty CharacterWorkContinuationExecutionV1
	eligible, err := evaluateWorkContinuationV1(ledger, state, entry.Input, boundaries)
	if err != nil {
		return empty, state, err
	}
	if !eligible.Eligible || !samePhysicalValueV2(eligible.Receipt, &entry.Continuation) {
		return empty, state, fmt.Errorf("historical batch owner authorization differs from original ledger")
	}
	sources := append([]CharacterWorkContinuationPeerSourceV1{{Proposal: state.proposal, Receipt: entry.Continuation}}, continuationCloneV1(entry.PeerContinuations)...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Proposal.AgentID < sources[j].Proposal.AgentID })
	proposals, sourceRoot, err := continuationBatchSourcesV1(entry.Input, sources, entry.FreshPeerProposals)
	if err != nil {
		return empty, state, err
	}
	if sourceRoot != entry.ContinuationSourcesRoot || len(sources) < 2 {
		return empty, state, fmt.Errorf("historical continuation batch source root mismatch")
	}
	// Peer ledgers are not recursively copied. Their complete validation was
	// committed by the HOST's global verifier, which must cover all source-set
	// members under this identical input, arbitration and after-state root.
	wanted := []string{}
	for _, source := range sources {
		candidate := entry
		candidate.Continuation = source.Receipt
		digest, err := continuationEntryDigestV1(candidate)
		if err != nil {
			return empty, state, err
		}
		wanted = append(wanted, digest)
	}
	if err := requireContinuationBatchMembershipV1(entry, wanted, boundaries); err != nil {
		return empty, state, err
	}
	after, root, err := validateContinuationBatchArbitrationV1(entry.Input, sources, proposals, entry.Arbitration)
	if err != nil {
		return empty, state, err
	}
	return deriveContinuationBatchEntryV1(ledger, state, entry.Input, entry.Continuation, entry.Arbitration, entry.FreshPeerProposals, sources, sourceRoot, after, root)
}

func requireContinuationBatchMembershipV1(entry CharacterWorkContinuationExecutionV1, members []string, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) error {
	root, err := verifiedContinuationGlobalRootV1(entry, boundaries)
	if err != nil {
		return err
	}
	for _, verified := range boundaries {
		b := verified.binding
		if !verified.verified || b.GlobalRoot != root {
			continue
		}
		if b.InputSetDigest != entry.Input.Digest || b.ArbitrationDigest != entry.Arbitration.Digest || b.AfterPhysicalRoot != entry.AfterPhysicalRoot {
			continue
		}
		all := true
		for _, member := range members {
			all = all && slices.Contains(b.ContinuationEntryDigests, member)
		}
		if all {
			return nil
		}
	}
	return fmt.Errorf("global commit does not authenticate every continuation peer")
}
