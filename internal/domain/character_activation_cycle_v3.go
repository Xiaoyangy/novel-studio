package domain

import (
	"encoding/json"
	"fmt"
	"sort"
)

const (
	CharacterActivationCycleV3Version         = "character-activation-cycle.v3"
	CharacterActivationRoundEvidenceV3Version = "character-agent-execution-evidence.v3"
	CharacterActivationCyclePolicyV3          = "chapter-activation-cycles.v3"
	CharacterWorkContinuationLedgerV2Version  = "character-work-continuation-ledger.v2"
)

// V3 binds the full flat source payload, including every original R1/R2 row and
// every derived round/entry root. Empty work-entry membership is legitimate: a
// complete all-fresh cycle or hard stop is still a real verified global step.
func CharacterActivationCycleRootV3(cycle CharacterActivationCycle) (string, error) {
	if cycle.Version != CharacterActivationCycleV3Version {
		return "", fmt.Errorf("v3 global root requires explicit v3 cycle")
	}
	cycle.Digest = ""
	raw, err := json.Marshal(cycle)
	if err != nil {
		return "", err
	}
	return ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
}

func validateActivationV3Input(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet) error {
	if err := validateVerifiedActivationInputSession(prefix, input); err != nil {
		return err
	}
	require := func(sources []string) error {
		for _, policy := range []string{CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2, CharacterSelfChronologyPolicyV1, CharacterWorkContinuationPolicyV1} {
			if !physicalContainsRefV2(sources, policy) {
				return fmt.Errorf("v3 cycle requires explicit source policy %s", policy)
			}
		}
		if physicalContainsRefV2(sources, CharacterActivationCyclePolicy) || physicalContainsRefV2(sources, CharacterActivationCyclePolicyV2) {
			return fmt.Errorf("v3 cannot reinterpret an old activation policy")
		}
		return nil
	}
	if err := require(input.Stimulus.Sources); err != nil {
		return err
	}
	for _, o := range input.Observations {
		if err := require(o.Sources); err != nil {
			return err
		}
	}
	for _, step := range prefix.steps {
		if !step.verified || step.cycle.Version != CharacterActivationCycleV3Version {
			return fmt.Errorf("v3 cannot upgrade an existing legacy cycle prefix")
		}
	}
	return nil
}

func FinalizeCharacterActivationCycleV3(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (CharacterActivationCycle, error) {
	value, _, _, err := finalizeActivationCycleV3(prefix, input, cycle)
	return value, err
}

func finalizeActivationCycleV3(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (CharacterActivationCycle, []VerifiedCharacterArbitrationRoundV1, map[string]CharacterWorkContinuationExecutionV1, error) {
	fail := func(err error) (CharacterActivationCycle, []VerifiedCharacterArbitrationRoundV1, map[string]CharacterWorkContinuationExecutionV1, error) {
		return CharacterActivationCycle{}, nil, nil, err
	}
	if err := validateActivationV3Input(prefix, input); err != nil {
		return fail(err)
	}
	owned, err := cloneVerifiedActivationValue(cycle)
	if err != nil {
		return fail(err)
	}
	cycle = owned
	session := prefix.Session()
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	if cycle.Version != CharacterActivationCycleV3Version || cycle.GenerationID != session.GenerationID || cycle.Chapter != session.Chapter || cycle.Index != len(prefix.steps)+1 || cycle.PreviousDigest != previous || cycle.ChapterContextDigest != session.ChapterContextDigest || cycle.InputSetDigest != input.Digest {
		return fail(fmt.Errorf("v3 cycle differs from its exact verified prefix/input identity"))
	}
	e := &cycle.Evidence
	if e.Version != CharacterActivationRoundEvidenceV3Version || e.GenerationID != cycle.GenerationID || e.Chapter != cycle.Chapter || !samePhysicalValueV2(e.Registry, input.Registry) || !samePhysicalValueV2(e.Stimulus, input.Stimulus) || !samePhysicalValueV2(e.Activation, input.Activation) || len(e.Arbitrations) < 1 || len(e.Arbitrations) > 2 {
		return fail(fmt.Errorf("v3 evidence must contain exact frozen inputs and one or two actual rounds"))
	}
	if err := validatePlanningV2Digest("v3 execution protocol", e.ProtocolDigest); err != nil {
		return fail(err)
	}
	if len(prefix.steps) > 0 && prefix.steps[0].cycle.Evidence.ProtocolDigest != e.ProtocolDigest {
		return fail(fmt.Errorf("v3 changed the chapter's frozen execution protocol"))
	}
	active := map[string]CharacterObservationPacket{}
	for _, entry := range input.Activation.Entries {
		if entry.State == CharacterAgentActive {
			for _, o := range input.Observations {
				if o.AgentID == entry.AgentID {
					active[o.AgentID] = o
				}
			}
		}
	}
	var roots []string
	for _, o := range active {
		roots = append(roots, o.MemoryRoot)
	}
	if !samePhysicalValueV2(normalizeV2Strings(e.MemoryRoots), normalizeV2Strings(roots)) {
		return fail(fmt.Errorf("v3 evidence must bind every active frozen memory root"))
	}
	observations := map[int][]CharacterObservationPacket{}
	proposals := map[int][]CharacterDecisionProposal{}
	seen := map[string]bool{}
	for _, o := range e.Observations {
		key := fmt.Sprintf("%s/%d", o.AgentID, o.Round)
		if o.Round < 1 || o.Round > len(e.Arbitrations) || seen[key] || active[o.AgentID].AgentID == "" {
			return fail(fmt.Errorf("v3 observation is duplicate, inactive or outside actual rounds"))
		}
		seen[key] = true
		if o.Round == 1 && !samePhysicalValueV2(o, active[o.AgentID]) {
			return fail(fmt.Errorf("v3 R1 observation differs from its frozen input"))
		}
		observations[o.Round] = append(observations[o.Round], o)
	}
	if len(observations[1]) != len(active) {
		return fail(fmt.Errorf("v3 lacks complete active R1 observations"))
	}
	seen = map[string]bool{}
	for _, p := range e.Proposals {
		key := fmt.Sprintf("%s/%d", p.AgentID, p.Round)
		if p.Round < 1 || p.Round > len(e.Arbitrations) || seen[key] || active[p.AgentID].AgentID == "" {
			return fail(fmt.Errorf("v3 submitted proposal is duplicate, inactive or outside actual rounds"))
		}
		seen[key] = true
		proposals[p.Round] = append(proposals[p.Round], p)
	}
	scope, err := ResolveCharacterArbitrationRoundV1(prefix, input, cycle.WorkContinuations, proposals[1])
	if err != nil {
		return fail(err)
	}
	var rounds []VerifiedCharacterArbitrationRoundV1
	for i, receipt := range e.Arbitrations {
		if receipt.Round != i+1 {
			return fail(fmt.Errorf("v3 arbitration rounds are not contiguous original R1/R2"))
		}
		if i == 1 {
			scope, err = ResolveCharacterArbitrationRevisionV1(rounds[0], observations[2], proposals[2])
			if err != nil {
				return fail(err)
			}
		}
		round, err := FinalizeCharacterArbitrationRoundV1(scope, receipt)
		if err != nil {
			return fail(err)
		}
		if !samePhysicalValueV2(round.Receipt(), receipt) {
			return fail(fmt.Errorf("v3 arbitration payload/digest differs from its source-aware finalized receipt"))
		}
		rounds = append(rounds, round)
	}
	last := rounds[len(rounds)-1]
	final := last.Receipt()
	if !final.Finalized && final.HardContractStatus != "infeasible" {
		return fail(fmt.Errorf("v3 cycle still requires its actual bounded revision"))
	}
	if err := validateActivationV3Usage(e.Usage, rounds); err != nil {
		return fail(err)
	}
	after := last.AfterState()
	cycle.BeforePhysicalRoot, err = CharacterPhysicalRootForCycle(*input.Stimulus.PhysicalState)
	if err != nil {
		return fail(err)
	}
	cycle.AfterPhysicalRoot, err = CharacterPhysicalRootForCycle(after)
	if err != nil {
		return fail(err)
	}
	cycle.StartDay, cycle.EndDay = final.StoryTime.StartDay, final.StoryTime.EndDay
	if cycle.BeforePhysicalRoot != session.CurrentPhysicalRoot || cycle.StartDay != session.CurrentDay {
		return fail(fmt.Errorf("v3 cycle changed its actual starting state/time"))
	}
	if final.HardContractStatus != "infeasible" && cycle.StartDay == cycle.EndDay && cycle.BeforePhysicalRoot == cycle.AfterPhysicalRoot {
		return fail(fmt.Errorf("v3 cycle made no actual progress"))
	}
	cycle.RoundSourceDigests = nil
	for _, round := range rounds {
		cycle.RoundSourceDigests = append(cycle.RoundSourceDigests, round.Sources().Digest())
	}
	entries := map[string]CharacterWorkContinuationExecutionV1{}
	cycle.ContinuationEntryDigests = nil
	if final.Finalized {
		entries, err = activationV3Entries(last, cycle.AfterPhysicalRoot)
		if err != nil {
			return fail(err)
		}
		for _, entry := range entries {
			cycle.ContinuationEntryDigests = append(cycle.ContinuationEntryDigests, entry.EntryDigest)
		}
	}
	sort.Strings(cycle.ContinuationEntryDigests)
	cycle.WorkContinuations = rounds[0].Sources().AdmissionContinuations()
	sort.Slice(e.Observations, func(i, j int) bool {
		if e.Observations[i].Round == e.Observations[j].Round {
			return e.Observations[i].AgentID < e.Observations[j].AgentID
		}
		return e.Observations[i].Round < e.Observations[j].Round
	})
	sort.Slice(e.Proposals, func(i, j int) bool {
		if e.Proposals[i].Round == e.Proposals[j].Round {
			return e.Proposals[i].AgentID < e.Proposals[j].AgentID
		}
		return e.Proposals[i].Round < e.Proposals[j].Round
	})
	sort.SliceStable(e.Usage, func(i, j int) bool {
		if e.Usage[i].Round == e.Usage[j].Round {
			return e.Usage[i].AgentID < e.Usage[j].AgentID
		}
		return e.Usage[i].Round < e.Usage[j].Round
	})
	e.MemoryRoots = normalizeV2Strings(roots)
	e.EvidenceRoot, err = ComputeCharacterAgentEvidenceRoot(*e)
	if err != nil {
		return fail(err)
	}
	cycle.Digest, err = CharacterActivationCycleRootV3(cycle)
	if err != nil {
		return fail(err)
	}
	return cycle, rounds, entries, nil
}

func validateActivationV3Usage(usage []CharacterAgentUsage, rounds []VerifiedCharacterArbitrationRoundV1) error {
	for _, u := range usage {
		if u.Round < 1 || u.Round > len(rounds) || u.AgentID == "" || u.Input < 0 || u.Output < 0 || u.CostUSD < 0 || u.Attempts < 0 || u.UnpricedCalls < 0 {
			return fmt.Errorf("v3 usage has invalid round or quantities")
		}
		s := rounds[u.Round-1].Sources()
		coordinate := s.Coordinate()
		if u.GenerationID != coordinate.GenerationID || u.Chapter != coordinate.Chapter || u.Cycle != coordinate.Cycle {
			return fmt.Errorf("v3 usage has a foreign generation/chapter/cycle")
		}
		if u.Role == "character" {
			called := false
			for _, p := range s.SubmittedProposals() {
				if p.AgentID == u.AgentID && p.Round == u.Round {
					called = true
				}
			}
			if !called {
				return fmt.Errorf("v3 usage invents a character call for an unchanged continuer or unaffected owner")
			}
		} else if u.Role != "world_arbiter" || u.AgentID != "world_arbiter" {
			return fmt.Errorf("v3 usage has an unsupported role")
		}
		if u.CostSource != "" && u.CostSource != "reported" && u.CostSource != "estimated" && u.CostSource != "unknown" {
			return fmt.Errorf("v3 usage cost source is invalid")
		}
		if u.Status != "" && u.Status != "success" && u.Status != "failed" && u.Status != "canceled" {
			return fmt.Errorf("v3 usage call status is invalid")
		}
	}
	return nil
}

func activationV3Entries(round VerifiedCharacterArbitrationRoundV1, afterRoot string) (map[string]CharacterWorkContinuationExecutionV1, error) {
	entries := map[string]CharacterWorkContinuationExecutionV1{}
	scope := round.Sources()
	receipt := round.Receipt()
	var fresh []CharacterDecisionProposal
	byOwner := map[string]CharacterDecisionProposal{}
	for _, p := range scope.EffectiveProposals() {
		byOwner[p.AgentID] = p
	}
	for _, s := range scope.Sources() {
		if s.Kind == "fresh" {
			fresh = append(fresh, byOwner[s.AgentID])
		}
	}
	continued := scope.Continuations()
	for _, admission := range continued {
		resolution, err := continuationResolutionV1(receipt, byOwner[admission.AgentID])
		if err != nil {
			return nil, err
		}
		entry := CharacterWorkContinuationExecutionV1{Input: scope.Input(), Continuation: admission, Arbitration: receipt, FreshPeerProposals: continuationCloneV1(fresh), ContinuationSourcesRoot: scope.Digest(), RoundSourceDigest: scope.Digest(), Executions: continuationCloneV1(resolution.SelfExecutions), AfterPhysicalRoot: afterRoot}
		for _, peer := range continued {
			if peer.AgentID != admission.AgentID {
				entry.PeerContinuations = append(entry.PeerContinuations, CharacterWorkContinuationPeerSourceV1{Proposal: byOwner[peer.AgentID], Receipt: peer})
			}
		}
		entry.EntryDigest, err = continuationEntryDigestV1(entry)
		if err != nil {
			return nil, err
		}
		entries[admission.AgentID] = entry
	}
	return entries, nil
}

// The only new authority is this verified full step. No old work-boundary
// badge with a synthetic member is used to certify all-fresh/hard cycles.
func VerifyCharacterActivationStepV3(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (VerifiedCharacterActivationStep, VerifiedCharacterActivationPrefix, error) {
	var empty VerifiedCharacterActivationStep
	verified, rounds, entries, err := finalizeActivationCycleV3(prefix, input, cycle)
	if err != nil {
		return empty, prefix, err
	}
	if !samePhysicalValueV2(verified, cycle) {
		return empty, prefix, fmt.Errorf("v3 cycle differs from its complete source-derived canonical payload")
	}
	session, err := appendValidatedCharacterActivationCycle(prefix.session, verified)
	if err != nil {
		return empty, prefix, err
	}
	last := rounds[len(rounds)-1]
	step := VerifiedCharacterActivationStep{input: continuationCloneV1(input), cycle: verified, effectiveProposals: last.Sources().EffectiveProposals(), afterState: last.AfterState(), rounds: rounds, verified: true}
	next := prefix
	next.session = session
	next.steps = append(append([]VerifiedCharacterActivationStep(nil), prefix.steps...), step)
	next.ledgers = continuationCloneV1(prefix.ledgers)
	if next.ledgers == nil {
		next.ledgers = map[string]CharacterWorkContinuationLedgerV1{}
	}
	if last.Receipt().Finalized {
		for owner, entry := range entries {
			ledger, ok := next.ledgers[owner]
			if !ok || ledger.Version != CharacterWorkContinuationLedgerV2Version {
				return empty, prefix, fmt.Errorf("v3 work execution lacks its v2 source ledger")
			}
			ledger.Entries = append(ledger.Entries, continuationCloneV1(entry))
			ledger.Digest, err = continuationLedgerDigestV1(ledger)
			if err != nil {
				return empty, prefix, err
			}
			next.ledgers[owner] = ledger
		}
		byOwner := map[string]CharacterDecisionProposal{}
		for _, p := range step.effectiveProposals {
			byOwner[p.AgentID] = p
		}
		for _, source := range last.Sources().Sources() {
			if source.Kind != "fresh" {
				continue
			}
			delete(next.ledgers, source.AgentID)
			p := byOwner[source.AgentID]
			if len(p.SelfTasks) != 1 || len(p.WorkContinuations) != 1 || p.SelfTasks[0].TaskID != p.WorkContinuations[0].TaskID {
				continue
			}
			// Complete source verification has already proved this exact final
			// submission and outcome; R1 proposals superseded by R2 never get here.
			ledger := CharacterWorkContinuationLedgerV1{Version: CharacterWorkContinuationLedgerV2Version, Origin: continuationCloneV1(verified), ProposalDigest: p.Digest, TaskID: p.SelfTasks[0].TaskID}
			ledger.Digest, err = continuationLedgerDigestV1(ledger)
			if err != nil {
				return empty, prefix, err
			}
			next.ledgers[p.AgentID] = ledger
		}
	}
	return step, next, nil
}

// Execution policy helpers retain the original v1/v2 boundaries.
func CharacterActivationUsesRoundSources(policy string) bool {
	return policy == CharacterActivationCyclePolicyV3
}

func CharacterActivationUsesVerifiedPrefix(policy string) bool {
	return policy == CharacterActivationCyclePolicyV2 || CharacterActivationUsesRoundSources(policy)
}

func IsCharacterActivationPolicy(policy string) bool {
	return policy == CharacterActivationCyclePolicy || CharacterActivationUsesVerifiedPrefix(policy)
}
