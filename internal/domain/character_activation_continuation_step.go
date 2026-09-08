package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

const CharacterContinuationEvidenceV2Version = "character-agent-execution-evidence.v2"

// Unlike legacy struct-order hashes, a v2 global root has one canonical JSON
// map payload. Every member, the global predecessor, and the verified boundary
// use this same root; an owner's entry root is never substituted for it.
func CharacterContinuationCycleRootV2(cycle CharacterActivationCycle) (string, error) {
	if cycle.Version != CharacterActivationCycleV2Version {
		return "", fmt.Errorf("canonical continuation root requires a v2 cycle")
	}
	cycle.Digest = ""
	raw, err := json.Marshal(cycle)
	if err != nil {
		return "", err
	}
	return ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
}

// A mixed cycle can itself contain a NEW independently submitted grant. Its
// source is the complete globally verified cycle, not a standalone fresh-only
// evidence bundle or an owner-signed progress total.
func validateCharacterContinuationOriginV2(cycle CharacterActivationCycle, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (WorldPhysicalStateV2, error) {
	var empty WorldPhysicalStateV2
	root, err := CharacterContinuationCycleRootV2(cycle)
	if err != nil || root != cycle.Digest || len(cycle.Evidence.Arbitrations) != 1 {
		return empty, fmt.Errorf("continuation origin lacks its canonical complete cycle root")
	}
	for _, boundary := range boundaries {
		b := boundary.binding
		if !boundary.verified || b.GlobalRoot != root {
			continue
		}
		if b.GenerationID != cycle.GenerationID || b.Chapter != cycle.Chapter || b.Cycle != cycle.Index || b.InputSetDigest != cycle.InputSetDigest || b.ArbitrationDigest != cycle.Evidence.Arbitrations[0].Digest || b.AfterPhysicalRoot != cycle.AfterPhysicalRoot || !samePhysicalValueV2(b.ContinuationEntryDigests, cycle.ContinuationEntryDigests) || b.AfterPhysicalState == nil {
			return empty, fmt.Errorf("mixed-origin source differs from its verified global boundary")
		}
		afterRoot, err := CharacterPhysicalRootForCycle(*b.AfterPhysicalState)
		if err != nil || afterRoot != cycle.AfterPhysicalRoot {
			return empty, fmt.Errorf("mixed-origin after-state is not globally bound")
		}
		return continuationCloneV1(*b.AfterPhysicalState), nil
	}
	return empty, fmt.Errorf("mixed-origin grant lacks verified full-cycle authority")
}

// The model never calls this constructor. It operates on immutable host inputs
// and a verified prefix; repeated construction yields the same source roots.
func FinalizeCharacterContinuationCycleV2(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (CharacterActivationCycle, error) {
	value, _, _, err := finalizeCharacterContinuationPayloadV2(prefix, input, cycle)
	return value, err
}

func finalizeCharacterContinuationPayloadV2(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (CharacterActivationCycle, CharacterWorkContinuationBatchResultV1, []CharacterDecisionProposal, error) {
	var empty CharacterWorkContinuationBatchResultV1
	fail := func(err error) (CharacterActivationCycle, CharacterWorkContinuationBatchResultV1, []CharacterDecisionProposal, error) {
		return CharacterActivationCycle{}, empty, nil, err
	}
	if len(cycle.RoundSourceDigests) > 0 {
		return fail(fmt.Errorf("v2 cycle cannot carry v3 round source fields"))
	}
	if physicalContainsRefV2(input.Stimulus.Sources, CharacterActivationCyclePolicyV3) {
		return fail(fmt.Errorf("v2 cycle cannot claim v3 execution policy"))
	}
	if !prefix.verified || prefix.session.Phase != "collecting" || len(prefix.steps) == 0 || len(prefix.steps) >= prefix.session.MaxCycles || cycle.Version != CharacterActivationCycleV2Version || len(cycle.WorkContinuations) == 0 {
		return fail(fmt.Errorf("continuation requires a verified collecting prefix and explicit v2 members"))
	}
	if err := ValidateCharacterActivationInputSet(input); err != nil {
		return fail(err)
	}
	session := prefix.session
	if cycle.GenerationID != session.GenerationID || cycle.Chapter != session.Chapter || cycle.Index != len(session.CycleDigests)+1 || cycle.PreviousDigest != session.CycleDigests[len(session.CycleDigests)-1] || cycle.ChapterContextDigest != session.ChapterContextDigest || cycle.InputSetDigest != input.Digest || input.Stimulus.GenerationID != cycle.GenerationID || input.Stimulus.Chapter != cycle.Chapter || !HasCharacterWorkContinuationPolicyV1(input.Stimulus.Sources) {
		return fail(fmt.Errorf("continuation payload is not bound to the exact global predecessor/input"))
	}
	token, err := CharacterActivationCycleSourceToken(cycle.GenerationID, cycle.Chapter, cycle.Index, cycle.ChapterContextDigest, cycle.PreviousDigest)
	if err != nil || !planningV2ContainsExactString(input.Stimulus.Sources, token) {
		return fail(fmt.Errorf("continuation payload lacks its exact cycle source token"))
	}
	wantContext, err := NewCharacterObservationCycleContext(session)
	if err != nil {
		return fail(err)
	}
	for _, observation := range input.Observations {
		if !samePhysicalValueV2(observation.CycleContext, wantContext) {
			return fail(fmt.Errorf("continuation input has a foreign observation cycle context"))
		}
	}
	if HasCharacterSelfChronologyPolicyV1(input.Stimulus.Sources) {
		if input.Stimulus.SelfEvaluationContext == nil {
			return fail(fmt.Errorf("continuation input lacks its actual evaluation context"))
		}
		if err := ValidateCharacterSelfEvaluationContextAgainstSessionV1(*input.Stimulus.SelfEvaluationContext, session); err != nil {
			return fail(err)
		}
	}
	cycle = continuationCloneV1(cycle)
	e := &cycle.Evidence
	if e.Version != CharacterContinuationEvidenceV2Version || e.GenerationID != cycle.GenerationID || e.Chapter != cycle.Chapter || !samePhysicalValueV2(e.Registry, input.Registry) || !samePhysicalValueV2(e.Stimulus, input.Stimulus) || !samePhysicalValueV2(e.Activation, input.Activation) || len(e.Arbitrations) != 1 {
		return fail(fmt.Errorf("continuation evidence differs from its immutable complete input"))
	}
	if err := validatePlanningV2Digest("continuation protocol", e.ProtocolDigest); err != nil {
		return fail(err)
	}
	if e.ProtocolDigest != prefix.steps[0].cycle.Evidence.ProtocolDigest {
		return fail(fmt.Errorf("continuation changed the chapter's frozen decision protocol"))
	}
	active := map[string]string{}
	for _, entry := range input.Activation.Entries {
		if entry.State == CharacterAgentActive {
			active[entry.AgentID] = entry.ObservationDigest
		}
	}
	current := map[string]CharacterObservationPacket{}
	for _, observation := range input.Observations {
		current[observation.AgentID] = observation
	}
	if len(e.Observations) != len(active) {
		return fail(fmt.Errorf("continuation evidence must retain every current active observation"))
	}
	seen := map[string]bool{}
	var roots []string
	for _, observation := range e.Observations {
		if seen[observation.AgentID] || active[observation.AgentID] == "" || !samePhysicalValueV2(observation, current[observation.AgentID]) {
			return fail(fmt.Errorf("continuation evidence substitutes or duplicates a current observation"))
		}
		seen[observation.AgentID] = true
		roots = append(roots, observation.MemoryRoot)
	}
	if !samePhysicalValueV2(normalizeV2Strings(e.MemoryRoots), normalizeV2Strings(roots)) {
		return fail(fmt.Errorf("continuation evidence does not bind all current memory roots"))
	}
	var items []CharacterWorkContinuationBatchItemV1
	var effective []CharacterDecisionProposal
	continuing := map[string]bool{}
	for _, receipt := range cycle.WorkContinuations {
		ledger, ok := prefix.ledgers[receipt.AgentID]
		if !ok || continuing[receipt.AgentID] {
			return fail(fmt.Errorf("continuation does not name one current authorized ledger"))
		}
		continuing[receipt.AgentID] = true
		items = append(items, CharacterWorkContinuationBatchItemV1{Ledger: ledger, Receipt: receipt})
		for _, proposal := range ledger.Origin.Evidence.Proposals {
			if proposal.Digest == ledger.ProposalDigest {
				effective = append(effective, proposal)
			}
		}
	}
	fresh := map[string]bool{}
	for _, proposal := range e.Proposals {
		if proposal.Round != 1 || fresh[proposal.AgentID] || continuing[proposal.AgentID] {
			return fail(fmt.Errorf("fresh and continued decisions overlap or require unsupported revision"))
		}
		fresh[proposal.AgentID] = true
		effective = append(effective, proposal)
	}
	batch, err := AppendCharacterWorkContinuationBatchV1(input, items, e.Proposals, e.Arbitrations[0], prefix.boundaries)
	if err != nil {
		return fail(err)
	}
	for _, usage := range e.Usage {
		if usage.AgentID == "" || usage.GenerationID != cycle.GenerationID || usage.Chapter != cycle.Chapter || usage.Round != 1 || usage.Input < 0 || usage.Output < 0 || usage.CostUSD < 0 || usage.Attempts < 0 || usage.UnpricedCalls < 0 || (usage.Role != "world_arbiter" && (usage.Role != "character" || !fresh[usage.AgentID])) {
			return fail(fmt.Errorf("continuation evidence invents a model call or has invalid usage"))
		}
		if usage.CostSource != "" && usage.CostSource != "reported" && usage.CostSource != "estimated" && usage.CostSource != "unknown" {
			return fail(fmt.Errorf("continuation evidence has an invalid cost source"))
		}
		if usage.Status != "" && usage.Status != "success" && usage.Status != "failed" && usage.Status != "canceled" {
			return fail(fmt.Errorf("continuation evidence has an invalid call status"))
		}
	}
	cycle.BeforePhysicalRoot, err = CharacterPhysicalRootForCycle(*input.Stimulus.PhysicalState)
	if err != nil {
		return fail(err)
	}
	cycle.AfterPhysicalRoot = batch.AfterPhysicalRoot
	r := e.Arbitrations[0]
	cycle.StartDay, cycle.EndDay = r.StoryTime.StartDay, r.StoryTime.EndDay
	if cycle.BeforePhysicalRoot != session.CurrentPhysicalRoot || cycle.StartDay != session.CurrentDay || (cycle.StartDay == cycle.EndDay && cycle.BeforePhysicalRoot == cycle.AfterPhysicalRoot) {
		return fail(fmt.Errorf("continuation changed the prestate or made no actual progress"))
	}
	cycle.ContinuationEntryDigests = nil
	for _, owner := range batch.Owners {
		cycle.ContinuationEntryDigests = append(cycle.ContinuationEntryDigests, owner.Entry.EntryDigest)
	}
	sort.Strings(cycle.ContinuationEntryDigests)
	sort.Slice(cycle.WorkContinuations, func(i, j int) bool { return cycle.WorkContinuations[i].AgentID < cycle.WorkContinuations[j].AgentID })
	sort.Slice(e.Proposals, func(i, j int) bool { return e.Proposals[i].AgentID < e.Proposals[j].AgentID })
	sort.Slice(e.Observations, func(i, j int) bool { return e.Observations[i].AgentID < e.Observations[j].AgentID })
	e.MemoryRoots = normalizeV2Strings(roots)
	e.EvidenceRoot, err = ComputeCharacterAgentEvidenceRoot(*e)
	if err != nil {
		return fail(err)
	}
	cycle.Digest, err = CharacterContinuationCycleRootV2(cycle)
	if err != nil {
		return fail(err)
	}
	sort.Slice(effective, func(i, j int) bool { return effective[i].AgentID < effective[j].AgentID })
	return cycle, batch, effective, nil
}

// This is the production global verifier. The closure is chosen by the Host,
// binds an immutable input and a verified prefix, and rechecks the full mixed
// cycle. It is not a model-supplied membership certificate.
func VerifyCharacterContinuationCycleBoundaryV2(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, raw json.RawMessage) (VerifiedCharacterWorkContinuationBoundaryV1, error) {
	return VerifyCharacterWorkContinuationBoundaryV1(raw, func(payload json.RawMessage) (CharacterWorkContinuationGlobalBindingV1, error) {
		var binding CharacterWorkContinuationGlobalBindingV1
		var candidate CharacterActivationCycle
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&candidate); err != nil {
			return binding, err
		}
		if candidate.Digest != "" {
			return binding, fmt.Errorf("global root payload cannot include its own digest")
		}
		verified, batch, _, err := finalizeCharacterContinuationPayloadV2(prefix, input, candidate)
		if err != nil {
			return binding, err
		}
		root := verified.Digest
		verified.Digest = ""
		if !samePhysicalValueV2(candidate, verified) {
			return binding, fmt.Errorf("global payload changed source-derived membership/state")
		}
		return CharacterWorkContinuationGlobalBindingV1{
			GlobalRoot: root, GenerationID: verified.GenerationID, Chapter: verified.Chapter, Cycle: verified.Index,
			InputSetDigest: input.Digest, ArbitrationDigest: verified.Evidence.Arbitrations[0].Digest,
			AfterPhysicalRoot: verified.AfterPhysicalRoot, ContinuationEntryDigests: verified.ContinuationEntryDigests,
			AfterPhysicalState: &batch.PhysicalState,
		}, nil
	})
}

func executeCharacterContinuationStepV2(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (VerifiedCharacterActivationStep, VerifiedCharacterActivationPrefix, error) {
	var emptyStep VerifiedCharacterActivationStep
	verified, batch, effective, err := finalizeCharacterContinuationPayloadV2(prefix, input, cycle)
	if err != nil {
		return emptyStep, prefix, err
	}
	if !samePhysicalValueV2(verified, cycle) {
		return emptyStep, prefix, fmt.Errorf("continuation cycle differs from its derived immutable payload")
	}
	payload := continuationCloneV1(verified)
	payload.Digest = ""
	raw, err := json.Marshal(payload)
	if err != nil {
		return emptyStep, prefix, err
	}
	// This lexical Host verifier can authenticate only the exact payload just
	// derived above in this invocation. It is not an external JSON capability
	// or a cross-call cache. The public boundary verifier still performs full
	// source verification for independently supplied payloads. Reusing this
	// already-verified result avoids executing the same physical batch twice;
	// the outer verifier still checks canonical root, membership and after-state.
	boundary, err := VerifyCharacterWorkContinuationBoundaryV1(raw, func(candidate json.RawMessage) (CharacterWorkContinuationGlobalBindingV1, error) {
		if !bytes.Equal(candidate, raw) {
			return CharacterWorkContinuationGlobalBindingV1{}, fmt.Errorf("continuation boundary changed this invocation's exact verified payload")
		}
		return CharacterWorkContinuationGlobalBindingV1{
			GlobalRoot: verified.Digest, GenerationID: verified.GenerationID, Chapter: verified.Chapter, Cycle: verified.Index,
			InputSetDigest: input.Digest, ArbitrationDigest: verified.Evidence.Arbitrations[0].Digest,
			AfterPhysicalRoot: verified.AfterPhysicalRoot, ContinuationEntryDigests: verified.ContinuationEntryDigests,
			AfterPhysicalState: &batch.PhysicalState,
		}, nil
	})
	if err != nil {
		return emptyStep, prefix, err
	}
	session, err := appendValidatedCharacterActivationCycle(prefix.session, verified)
	if err != nil {
		return emptyStep, prefix, err
	}
	step := VerifiedCharacterActivationStep{
		input: continuationCloneV1(input), cycle: verified, effectiveProposals: continuationCloneV1(effective),
		afterState: continuationCloneV1(batch.PhysicalState), boundary: boundary, verified: true,
	}
	next := prefix
	next.session = session
	next.steps = append(append([]VerifiedCharacterActivationStep(nil), prefix.steps...), step)
	next.boundaries = append(append([]VerifiedCharacterWorkContinuationBoundaryV1(nil), prefix.boundaries...), boundary)
	next.ledgers = continuationCloneV1(prefix.ledgers)
	for _, owner := range batch.Owners {
		next.ledgers[owner.AgentID] = owner.Ledger
	}
	// A new independent choice revokes its owner's old continuation grant.
	// If the new mixed-cycle choice grants finite work, bind that grant to THIS
	// full global boundary, not to somebody else's owner entry.
	for _, proposal := range verified.Evidence.Proposals {
		delete(next.ledgers, proposal.AgentID)
		if len(proposal.SelfTasks) != 1 || len(proposal.WorkContinuations) != 1 || proposal.WorkContinuations[0].TaskID != proposal.SelfTasks[0].TaskID {
			continue
		}
		ledger, err := NewCharacterWorkContinuationLedgerV1(verified, proposal.Digest, proposal.WorkContinuations[0].TaskID, next.boundaries...)
		if err != nil {
			return emptyStep, prefix, err
		}
		next.ledgers[proposal.AgentID] = ledger
	}
	return step, next, nil
}
