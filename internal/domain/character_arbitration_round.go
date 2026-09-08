package domain

import (
	"fmt"
	"math"
	"sort"
)

// This policy is an explicit, standalone API. No generation or runtime opts in
// automatically. Origin coordinates never replace the current arbitration round.
const CharacterArbitrationRoundSourcesPolicyV1 = "character-arbitration:verified-round-sources.v1"

type CharacterArbitrationCoordinateV1 struct {
	GenerationID string `json:"generation_id"`
	Chapter      int    `json:"chapter"`
	Cycle        int    `json:"cycle"`
	Round        int    `json:"round"`
}

type CharacterArbitrationIntentSourceV1 struct {
	AgentID            string                           `json:"agent_id"`
	Kind               string                           `json:"kind"`
	Origin             CharacterArbitrationCoordinateV1 `json:"origin"`
	ProposalDigest     string                           `json:"proposal_digest"`
	ObservationDigest  string                           `json:"observation_digest"`
	OriginCycleDigest  string                           `json:"origin_cycle_digest,omitempty"`
	ContinuationDigest string                           `json:"continuation_digest,omitempty"`
}

type arbitrationRoundOwnerV1 struct {
	source       CharacterArbitrationIntentSourceV1
	proposal     CharacterDecisionProposal
	observation  CharacterObservationPacket
	continuation *CharacterWorkContinuationReceiptV1
}

// Private fields intentionally cannot be unmarshaled into source authority.
// All getters return detached snapshots, never writable proof aliases.
type VerifiedCharacterArbitrationSourcesV1 struct {
	coordinate           CharacterArbitrationCoordinateV1
	sessionDigest        string
	input                CharacterActivationInputSet
	owners               map[string]arbitrationRoundOwnerV1
	observations         []CharacterObservationPacket
	submitted            []CharacterDecisionProposal
	admissions           []CharacterWorkContinuationReceiptV1
	priorReceipts        []WorldArbitrationReceipt
	previousSourceDigest string
	digest               string
	verified             bool
}

type VerifiedCharacterArbitrationRoundV1 struct {
	sources  VerifiedCharacterArbitrationSourcesV1
	receipt  WorldArbitrationReceipt
	after    WorldPhysicalStateV2
	verified bool
}

func (s VerifiedCharacterArbitrationSourcesV1) CurrentRound() int {
	if !s.verified {
		return 0
	}
	return s.coordinate.Round
}
func (s VerifiedCharacterArbitrationSourcesV1) Coordinate() CharacterArbitrationCoordinateV1 {
	if !s.verified {
		return CharacterArbitrationCoordinateV1{}
	}
	return s.coordinate
}
func (s VerifiedCharacterArbitrationSourcesV1) Digest() string {
	if !s.verified {
		return ""
	}
	return s.digest
}
func (s VerifiedCharacterArbitrationSourcesV1) Input() CharacterActivationInputSet {
	if !s.verified {
		return CharacterActivationInputSet{}
	}
	return continuationCloneV1(s.input)
}
func (s VerifiedCharacterArbitrationSourcesV1) SessionDigest() string {
	if !s.verified {
		return ""
	}
	return s.sessionDigest
}
func (s VerifiedCharacterArbitrationSourcesV1) Observations() []CharacterObservationPacket {
	if !s.verified {
		return nil
	}
	return continuationCloneV1(s.observations)
}
func (s VerifiedCharacterArbitrationSourcesV1) SubmittedProposals() []CharacterDecisionProposal {
	if !s.verified {
		return nil
	}
	return continuationCloneV1(s.submitted)
}
func (s VerifiedCharacterArbitrationSourcesV1) AdmissionContinuations() []CharacterWorkContinuationReceiptV1 {
	if !s.verified {
		return nil
	}
	return continuationCloneV1(s.admissions)
}
func (s VerifiedCharacterArbitrationSourcesV1) PreviousArbitrations() []WorldArbitrationReceipt {
	if !s.verified {
		return nil
	}
	return continuationCloneV1(s.priorReceipts)
}
func (s VerifiedCharacterArbitrationSourcesV1) Sources() []CharacterArbitrationIntentSourceV1 {
	if !s.verified {
		return nil
	}
	var out []CharacterArbitrationIntentSourceV1
	for _, o := range s.owners {
		out = append(out, o.source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}
func (s VerifiedCharacterArbitrationSourcesV1) EffectiveProposals() []CharacterDecisionProposal {
	if !s.verified {
		return nil
	}
	var out []CharacterDecisionProposal
	for _, o := range s.owners {
		out = append(out, continuationCloneV1(o.proposal))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}
func (s VerifiedCharacterArbitrationSourcesV1) Continuations() []CharacterWorkContinuationReceiptV1 {
	if !s.verified {
		return nil
	}
	var out []CharacterWorkContinuationReceiptV1
	for _, o := range s.owners {
		if o.continuation != nil {
			out = append(out, *o.continuation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}
func cloneArbitrationSourcesV1(s VerifiedCharacterArbitrationSourcesV1) VerifiedCharacterArbitrationSourcesV1 {
	if !s.verified {
		return VerifiedCharacterArbitrationSourcesV1{}
	}
	copy := s
	copy.input = s.Input()
	copy.observations = s.Observations()
	copy.submitted = s.SubmittedProposals()
	copy.admissions = s.AdmissionContinuations()
	copy.priorReceipts = s.PreviousArbitrations()
	copy.owners = make(map[string]arbitrationRoundOwnerV1, len(s.owners))
	for id, o := range s.owners {
		copy.owners[id] = arbitrationRoundOwnerV1{source: o.source, proposal: continuationCloneV1(o.proposal), observation: continuationCloneV1(o.observation), continuation: continuationCloneV1(o.continuation)}
	}
	return copy
}
func (r VerifiedCharacterArbitrationRoundV1) Sources() VerifiedCharacterArbitrationSourcesV1 {
	if !r.verified {
		return VerifiedCharacterArbitrationSourcesV1{}
	}
	return cloneArbitrationSourcesV1(r.sources)
}
func (r VerifiedCharacterArbitrationRoundV1) Receipt() WorldArbitrationReceipt {
	if !r.verified {
		return WorldArbitrationReceipt{}
	}
	return continuationCloneV1(r.receipt)
}
func (r VerifiedCharacterArbitrationRoundV1) AfterState() WorldPhysicalStateV2 {
	if !r.verified {
		return WorldPhysicalStateV2{}
	}
	return continuationCloneV1(r.after)
}

func signArbitrationSourcesV1(s VerifiedCharacterArbitrationSourcesV1) (VerifiedCharacterArbitrationSourcesV1, error) {
	// Only identities are needed here: the constructors verify the exact bytes
	// behind each digest before minting the runtime capability.
	s.verified = true
	var observationDigests, proposalDigests, receiptDigests []string
	for _, o := range s.observations {
		observationDigests = append(observationDigests, o.Digest)
	}
	for _, p := range s.submitted {
		proposalDigests = append(proposalDigests, p.Digest)
	}
	for _, r := range s.priorReceipts {
		receiptDigests = append(receiptDigests, r.Digest)
	}
	var err error
	s.digest, err = characterAgentDigest(struct {
		Policy                            string
		Coordinate                        CharacterArbitrationCoordinateV1
		Input, Session, Previous          string
		Sources                           []CharacterArbitrationIntentSourceV1
		Observations, Proposals, Receipts []string
		Admissions                        []CharacterWorkContinuationReceiptV1
	}{CharacterArbitrationRoundSourcesPolicyV1, s.coordinate, s.input.Digest, s.sessionDigest, s.previousSourceDigest, s.Sources(), normalizeV2Strings(observationDigests), normalizeV2Strings(proposalDigests), receiptDigests, s.admissions})
	if err != nil {
		return VerifiedCharacterArbitrationSourcesV1{}, err
	}
	return s, nil
}

// A P2 grant is reconstructed from real verified steps, not by relabeling it P1
// or by trusting legacy TaskProgress.State/caller-provided cumulative totals.
func continuationStateForRoundV1(prefix VerifiedCharacterActivationPrefix, owner string) (CharacterWorkContinuationLedgerV1, continuationStateV1, string, error) {
	var state continuationStateV1
	ledger, ok := prefix.ledgers[owner]
	if !prefix.verified || !ok {
		return ledger, state, "no_verified_authorization", nil
	}
	if ledger.Version != CharacterWorkContinuationPolicyV1 && ledger.Version != CharacterWorkContinuationLedgerV2Version {
		return ledger, state, "", fmt.Errorf("unsupported verified continuation ledger version")
	}
	index := ledger.Origin.Index
	if index < 1 || index > len(prefix.steps) || !prefix.steps[index-1].verified || prefix.steps[index-1].GlobalRoot() != ledger.Origin.Digest || !samePhysicalValueV2(prefix.steps[index-1].cycle, ledger.Origin) {
		return ledger, state, "", fmt.Errorf("continuation origin differs from its actual verified source step")
	}
	if digest, err := continuationLedgerDigestV1(ledger); err != nil || digest != ledger.Digest {
		return ledger, state, "", fmt.Errorf("verified continuation ledger digest mismatch")
	}
	origin := prefix.steps[index-1]
	for _, p := range origin.effectiveProposals {
		if p.AgentID == owner && p.Digest == ledger.ProposalDigest {
			state.proposal = continuationCloneV1(p)
		}
	}
	for _, o := range origin.cycle.Evidence.Observations {
		if o.AgentID == owner && o.Digest == state.proposal.ObservationDigest {
			state.observation = continuationCloneV1(o)
		}
	}
	if state.proposal.Digest == "" || state.observation.Digest == "" {
		return ledger, state, "", fmt.Errorf("continuation origin lacks exact owner proposal/observation")
	}
	p := state.proposal
	valid, err := FinalizeCharacterDecisionProposal(continuationCloneV1(p), continuationCloneV1(state.observation))
	if err != nil || !samePhysicalValueV2(valid, p) {
		return ledger, state, "", fmt.Errorf("continuation source proposal is not original source-bound bytes")
	}
	if len(p.SelfTasks) != 1 || len(p.WorkContinuations) != 1 || len(p.SelfTasks[0].ObservationRequests) > 0 || len(p.Communications)+len(p.ResourceReads)+len(p.ResourceMeasurements)+len(p.ResourceReports)+len(p.ResourceEstimates)+len(p.ArtifactReads)+len(p.ArtifactSigns)+len(p.ArtifactAccess) > 0 {
		return ledger, state, "requires_fresh_complex_intent", nil
	}
	state.task, state.grant = p.SelfTasks[0], p.WorkContinuations[0]
	if state.task.TaskID != ledger.TaskID || state.grant.TaskID != ledger.TaskID || state.task.Kind != "work" || p.Round < 1 || p.Round > 2 {
		return ledger, state, "requires_fresh_complex_intent", nil
	}
	if err := ValidateCharacterWorkContinuationIntentV1(p, state.observation); err != nil {
		return ledger, state, "", err
	}
	for _, a := range origin.input.Stimulus.PhysicalState.Actors {
		if a.AgentID == owner {
			for _, exp := range a.SelfExperiences {
				if exp.TaskID != ledger.TaskID {
					continue
				}
				if exp.Kind != "work" || exp.Action != state.task.Action || exp.ProgressUnit != selfTaskUnitV2(state.task) || !samePhysicalNumberV2(exp.ProgressTarget, state.task.ProgressTarget) {
					return ledger, state, "", fmt.Errorf("continuation source task changed its actual baseline definition")
				}
				if selfExecutionActiveV2(exp.Status) {
					state.baseline += (*exp.EndDay - *exp.StartDay) * 1440
				}
			}
		}
	}
	state.feedback = map[string]string{}
	for i := index - 1; i < len(prefix.steps); i++ {
		step := prefix.steps[i]
		if !step.verified || len(step.cycle.Evidence.Arbitrations) == 0 {
			return ledger, state, "", fmt.Errorf("continuation history has an unverified step")
		}
		matched := false
		for _, effective := range step.effectiveProposals {
			if effective.AgentID == owner {
				if !samePhysicalValueV2(effective, p) {
					return ledger, state, "authorization_superseded", nil
				}
				matched = true
			}
		}
		if !matched {
			return ledger, state, "owner_execution_gap", nil
		}
		r := step.cycle.Evidence.Arbitrations[len(step.cycle.Evidence.Arbitrations)-1]
		if !r.Finalized || r.HardContractStatus != "feasible" {
			return ledger, state, "execution_interrupted", nil
		}
		resolution, err := continuationResolutionV1(r, p)
		if err != nil {
			return ledger, state, "", err
		}
		minutes, status, err := continuationSegmentV1(resolution, ledger.TaskID)
		if err != nil {
			return ledger, state, "", err
		}
		state.grantedWork += minutes
		state.status = status
		state.physical = step.AfterState()
		state.day, state.cycle = r.StoryTime.EndDay, step.cycle.Index
		for _, a := range state.physical.Actors {
			if a.AgentID == owner && a.Location != p.Location {
				return ledger, state, "location_changed", nil
			}
		}
		text, err := CharacterActivationPrivateOutcome(p, resolution, state.physical, r)
		if err != nil {
			return ledger, state, "", err
		}
		state.feedback[r.Digest] = text
	}
	state.completed = state.baseline + state.grantedWork
	if math.IsNaN(state.completed) || math.IsInf(state.completed, 0) {
		return ledger, state, "", fmt.Errorf("nonfinite verified continuation progress")
	}
	return continuationCloneV1(ledger), state, "", nil
}

func EvaluateCharacterWorkContinuationForRoundV1(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, agentID string) (CharacterWorkContinuationEligibilityV1, error) {
	var out CharacterWorkContinuationEligibilityV1
	if err := validateVerifiedActivationInputSession(prefix, input); err != nil {
		return out, err
	}
	ledger, state, reason, err := continuationStateForRoundV1(prefix, agentID)
	if err != nil {
		return out, err
	}
	if reason != "" {
		out.WakeReasons = []string{reason}
		return out, nil
	}
	var owner CharacterObservationPacket
	executable := false
	for _, o := range input.Observations {
		if o.AgentID == agentID {
			owner = o
		}
	}
	for _, entry := range input.Activation.Entries {
		if entry.AgentID == agentID {
			executable = entry.State == CharacterAgentActive
		}
	}
	if owner.Digest == "" || owner.CycleContext == nil || !HasCharacterWorkContinuationPolicyV1(input.Stimulus.Sources) || !HasCharacterWorkContinuationPolicyV1(owner.Sources) {
		return out, fmt.Errorf("continuation round lacks exact current owner/policy")
	}
	wake := func(reason string) { out.WakeReasons = append(out.WakeReasons, reason) }
	if !executable {
		wake("owner_not_executable")
	}
	if state.cycle+1 != owner.CycleContext.Index || state.day != input.Stimulus.StoryClock.CurrentDay {
		wake("owner_execution_gap")
	}
	actual, err := CharacterPhysicalRootForCycle(*input.Stimulus.PhysicalState)
	if err != nil {
		return out, err
	}
	expected, err := CharacterPhysicalRootForCycle(state.physical)
	if err != nil {
		return out, err
	}
	if actual != expected {
		wake("continuation_prestate_changed")
	}
	clock, oldClock := input.Stimulus.StoryClock, ledger.Origin.Evidence.Stimulus.StoryClock
	if oldClock == nil || clock.TimeContractCoreDigest != oldClock.TimeContractCoreDigest || clock.NominalBudget != oldClock.NominalBudget || clock.DurationDaysMin != oldClock.DurationDaysMin || clock.DurationDaysMax != oldClock.DurationDaysMax {
		wake("time_contract_changed")
	}
	if state.status == "blocked" {
		wake("execution_interrupted")
	} else if state.status != "in_progress" {
		wake("work_not_in_progress")
	}
	remaining := *state.task.ProgressTarget - state.completed
	if remaining <= 1e-9 {
		wake("task_completed")
	}
	if state.grant.MaxEffectiveMinutes != nil {
		remaining = math.Min(remaining, *state.grant.MaxEffectiveMinutes-state.grantedWork)
	}
	if remaining <= 1e-9 {
		wake("authorization_exhausted")
	}
	if owner.Location != state.proposal.Location {
		wake("location_changed")
	}
	if len(owner.ConflictFeedback) > 0 {
		wake("conflict_feedback")
	}
	baseline := continuationCloneV1(state.observation)
	baseline.CurrentGoal, baseline.Pressure = state.proposal.CurrentGoal, state.proposal.Pressure
	informationRoot := func(o CharacterObservationPacket, physical *WorldPhysicalStateV2) (string, error) {
		if !HasCharacterWorkArtifactPolicyV1(input.Stimulus.Sources) {
			return continuationInformationRootV1(o, state, physical)
		}
		if physical == nil || ledger.Origin.Evidence.Stimulus.PhysicalState == nil {
			return "", fmt.Errorf("artifact continuation lacks verified physical source")
		}
		projected, views, err := continuationArtifactInformationViewV1(o, state.proposal, *ledger.Origin.Evidence.Stimulus.PhysicalState, *physical)
		if err != nil {
			return "", err
		}
		base, err := continuationInformationRootV1(projected, state, physical)
		if err != nil {
			return "", err
		}
		return characterAgentDigest(struct {
			Policy, Base string
			Artifacts    []CharacterArtifactViewV1
		}{CharacterWorkArtifactPolicyV1, base, views})
	}
	oldInfo, err := informationRoot(baseline, ledger.Origin.Evidence.Stimulus.PhysicalState)
	if err != nil {
		return out, err
	}
	newInfo, err := informationRoot(owner, input.Stimulus.PhysicalState)
	if err != nil {
		return out, err
	}
	if oldInfo != newInfo {
		wake("owner_information_changed")
	}
	if !clock.NominalBudget && clock.CurrentDay >= clock.DurationDaysMax-1e-12 {
		wake("known_time_limit")
	}
	if len(out.WakeReasons) > 0 {
		return out, nil
	}
	grant, err := characterAgentDigest(state.grant)
	if err != nil {
		return out, err
	}
	r := CharacterWorkContinuationReceiptV1{Version: CharacterWorkContinuationPolicyV1, GenerationID: input.Stimulus.GenerationID, Chapter: input.Stimulus.Chapter, Cycle: owner.CycleContext.Index, AgentID: agentID, TaskID: ledger.TaskID, OriginCycleDigest: ledger.Origin.Digest, OriginProposalDigest: state.proposal.Digest, OriginObservationDigest: state.observation.Digest, AuthorizationDigest: grant, LedgerDigest: ledger.Digest, InputSetDigest: input.Digest, StimulusDigest: input.Stimulus.Digest, ObservationDigest: owner.Digest, BeforePhysicalRoot: actual, InformationRoot: newInfo, StartDay: clock.CurrentDay, EffectiveCompleted: state.completed, AuthorizedRemaining: remaining}
	r.Digest, err = ComputeCharacterWorkContinuationReceiptV1Digest(r)
	if err != nil {
		return out, err
	}
	out.Eligible, out.Receipt = true, &r
	return out, nil
}

func ResolveCharacterArbitrationRoundV1(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, continuations []CharacterWorkContinuationReceiptV1, fresh []CharacterDecisionProposal) (VerifiedCharacterArbitrationSourcesV1, error) {
	var empty VerifiedCharacterArbitrationSourcesV1
	if err := validateVerifiedActivationInputSession(prefix, input); err != nil {
		return empty, err
	}
	owned, err := cloneVerifiedActivationValue(input)
	if err != nil {
		return empty, err
	}
	session := prefix.Session()
	s := VerifiedCharacterArbitrationSourcesV1{coordinate: CharacterArbitrationCoordinateV1{session.GenerationID, session.Chapter, len(session.CycleDigests) + 1, 1}, sessionDigest: session.Digest, input: owned, owners: map[string]arbitrationRoundOwnerV1{}}
	active := map[string]bool{}
	observations := map[string]CharacterObservationPacket{}
	for _, entry := range owned.Activation.Entries {
		if entry.State == CharacterAgentActive {
			active[entry.AgentID] = true
		}
	}
	if len(active) == 0 {
		return empty, fmt.Errorf("arbitration round has no executable owner")
	}
	for _, o := range owned.Observations {
		observations[o.AgentID] = o
		if active[o.AgentID] {
			s.observations = append(s.observations, o)
		}
	}
	for _, r := range continuations {
		if !active[r.AgentID] {
			return empty, fmt.Errorf("arbitration continuation source overlaps or executes an inactive owner")
		}
		delete(active, r.AgentID)
		eligible, err := EvaluateCharacterWorkContinuationForRoundV1(prefix, owned, r.AgentID)
		if err != nil {
			return empty, err
		}
		if !eligible.Eligible || !samePhysicalValueV2(eligible.Receipt, &r) {
			return empty, fmt.Errorf("arbitration continuation differs from its verified source/allowance")
		}
		ledger, state, _, err := continuationStateForRoundV1(prefix, r.AgentID)
		if err != nil {
			return empty, err
		}
		coord := CharacterArbitrationCoordinateV1{ledger.Origin.GenerationID, ledger.Origin.Chapter, ledger.Origin.Index, state.proposal.Round}
		if coord.Cycle >= s.coordinate.Cycle {
			return empty, fmt.Errorf("continuation origin is not an earlier actual cycle")
		}
		copy := r
		s.owners[r.AgentID] = arbitrationRoundOwnerV1{source: CharacterArbitrationIntentSourceV1{r.AgentID, "continuation", coord, state.proposal.Digest, state.observation.Digest, ledger.Origin.Digest, r.Digest}, proposal: state.proposal, observation: state.observation, continuation: &copy}
		s.admissions = append(s.admissions, r)
	}
	for _, proposal := range fresh {
		if !active[proposal.AgentID] || proposal.Round != 1 {
			return empty, fmt.Errorf("fresh arbitration source overlaps or has a foreign current round")
		}
		delete(active, proposal.AgentID)
		p, err := FinalizeCharacterDecisionProposal(continuationCloneV1(proposal), observations[proposal.AgentID])
		if err != nil || !samePhysicalValueV2(p, proposal) {
			return empty, fmt.Errorf("fresh arbitration source is not its exact frozen observation proposal")
		}
		s.owners[p.AgentID] = arbitrationRoundOwnerV1{source: CharacterArbitrationIntentSourceV1{AgentID: p.AgentID, Kind: "fresh", Origin: s.coordinate, ProposalDigest: p.Digest, ObservationDigest: p.ObservationDigest}, proposal: p, observation: observations[p.AgentID]}
		s.submitted = append(s.submitted, p)
	}
	if len(active) > 0 {
		return empty, fmt.Errorf("arbitration sources do not cover every active owner")
	}
	sort.Slice(s.admissions, func(i, j int) bool { return s.admissions[i].AgentID < s.admissions[j].AgentID })
	return signArbitrationSourcesV1(s)
}

func ResolveCharacterArbitrationRevisionV1(previous VerifiedCharacterArbitrationRoundV1, observations []CharacterObservationPacket, proposals []CharacterDecisionProposal) (VerifiedCharacterArbitrationSourcesV1, error) {
	var empty VerifiedCharacterArbitrationSourcesV1
	if !previous.verified || previous.sources.CurrentRound() != 1 || previous.receipt.Finalized || previous.receipt.HardContractStatus != "feasible" {
		return empty, fmt.Errorf("revision requires the exact verified nonfinal feasible R1")
	}
	s := previous.Sources()
	s.coordinate.Round = 2
	s.previousSourceDigest = previous.sources.digest
	s.priorReceipts = append(s.priorReceipts, previous.Receipt())
	feedback := map[string][]string{}
	for _, c := range previous.receipt.Conflicts {
		if !c.Resolved {
			for _, owner := range c.AffectedAgentIDs {
				feedback[owner] = CharacterArbitrationFeedbackForOwnerV1(previous.receipt, owner, s.input.Stimulus.Sources)
			}
		}
	}
	if len(feedback) == 0 || len(observations) != len(feedback) || len(proposals) != len(feedback) {
		return empty, fmt.Errorf("R2 must cover exactly the affected owners, not requery all actors")
	}
	baseline := map[string]CharacterObservationPacket{}
	for _, o := range s.input.Observations {
		baseline[o.AgentID] = o
	}
	byOwner := map[string]CharacterObservationPacket{}
	for _, o := range observations {
		old, exists := baseline[o.AgentID]
		if !exists || len(feedback[o.AgentID]) == 0 || byOwner[o.AgentID].AgentID != "" || o.Round != 2 {
			return empty, fmt.Errorf("revision observation belongs to an unaffected/duplicate/foreign owner")
		}
		checked, err := FinalizeCharacterObservationPacket(continuationCloneV1(o))
		if err != nil || !samePhysicalValueV2(checked, o) {
			return empty, fmt.Errorf("revision observation digest is not exact")
		}
		if !samePhysicalValueV2(normalizeV2Strings(o.ConflictFeedback), normalizeV2Strings(feedback[o.AgentID])) {
			return empty, fmt.Errorf("revision feedback is not from the owner's actual unresolved R1 conflict")
		}
		copy := continuationCloneV1(o)
		copy.Round, copy.GeneratedAt, copy.Digest, copy.ConflictFeedback = old.Round, old.GeneratedAt, old.Digest, old.ConflictFeedback
		if !samePhysicalValueV2(copy, old) {
			return empty, fmt.Errorf("revision changed frozen knowledge, location, resources or source identity")
		}
		byOwner[o.AgentID] = continuationCloneV1(o)
		s.observations = append(s.observations, continuationCloneV1(o))
	}
	for _, p := range proposals {
		o, ok := byOwner[p.AgentID]
		if !ok {
			return empty, fmt.Errorf("revision proposal is missing, repeated or from an unaffected owner")
		}
		delete(byOwner, p.AgentID)
		checked, err := FinalizeCharacterDecisionProposal(continuationCloneV1(p), o)
		if err != nil || !samePhysicalValueV2(checked, p) {
			return empty, fmt.Errorf("revision proposal is not its exact actual O2 submission")
		}
		s.owners[p.AgentID] = arbitrationRoundOwnerV1{source: CharacterArbitrationIntentSourceV1{AgentID: p.AgentID, Kind: "fresh", Origin: s.coordinate, ProposalDigest: p.Digest, ObservationDigest: p.ObservationDigest}, proposal: continuationCloneV1(p), observation: o}
		s.submitted = append(s.submitted, continuationCloneV1(p))
	}
	return signArbitrationSourcesV1(s)
}

func FinalizeCharacterArbitrationRoundV1(sources VerifiedCharacterArbitrationSourcesV1, receipt WorldArbitrationReceipt) (VerifiedCharacterArbitrationRoundV1, error) {
	var empty VerifiedCharacterArbitrationRoundV1
	if !sources.verified || sources.digest == "" || receipt.Round != sources.CurrentRound() {
		return empty, fmt.Errorf("arbitration requires its verified current-round source scope")
	}
	s := cloneArbitrationSourcesV1(sources)
	r, err := cloneVerifiedActivationValue(receipt)
	if err != nil {
		return empty, err
	}
	if !r.Finalized {
		clock := s.input.Stimulus.StoryClock
		if r.StoryTime == nil || r.StoryTime.StartDay != clock.CurrentDay || r.StoryTime.EndDay != clock.CurrentDay || len(r.ResourceSettlements)+len(r.ResourceDeliveries)+len(r.PassiveReceptions) != 0 {
			return empty, fmt.Errorf("nonfinal/hard arbitration cannot advance clock, resources or deliveries")
		}
		for _, resolution := range r.Resolutions {
			if len(resolution.SelfExecutions) > 0 {
				return empty, fmt.Errorf("nonfinal/hard arbitration cannot publish self execution")
			}
		}
	}
	prior := map[string]string{}
	for owner, source := range s.owners {
		if source.source.Kind == "continuation" {
			origin := source.source.Origin
			if origin.GenerationID != s.coordinate.GenerationID || origin.Chapter != s.coordinate.Chapter || origin.Cycle >= s.coordinate.Cycle || origin.Round != source.proposal.Round || source.source.ProposalDigest != source.proposal.Digest || source.continuation == nil {
				return empty, fmt.Errorf("continuation source coordinates were not verified")
			}
			prior[owner] = source.proposal.Digest
			for _, delivery := range r.ResourceDeliveries {
				if delivery.FromAgentID == owner {
					return empty, fmt.Errorf("continuation cannot initiate a resource delivery")
				}
			}
			for _, reception := range r.PassiveReceptions {
				if reception.FromAgentID == owner {
					return empty, fmt.Errorf("continuation cannot initiate communication")
				}
			}
		}
	}
	r, err = finalizeWorldArbitrationReceiptWithPriorSources(r, s.input.Stimulus, s.input.Activation, s.EffectiveProposals(), 1, prior)
	if err != nil {
		return empty, err
	}
	after, err := ApplyArbitrationPhysicalStateWithVerifiedSourcesV1(s, r)
	if err != nil {
		return empty, err
	}
	if !r.Finalized {
		if !samePhysicalValueV2(after, *s.input.Stimulus.PhysicalState) {
			return empty, fmt.Errorf("nonfinal/hard arbitration changed physical state, knowledge or perception")
		}
	} else {
		for owner, source := range s.owners {
			if source.continuation == nil {
				continue
			}
			resolution, err := continuationResolutionV1(r, source.proposal)
			if err != nil {
				return empty, err
			}
			minutes, _, err := continuationSegmentV1(resolution, source.continuation.TaskID)
			if err != nil {
				return empty, err
			}
			if minutes > source.continuation.AuthorizedRemaining+1e-9 {
				return empty, fmt.Errorf("continuation exceeded its actual remaining authorization")
			}
			for _, a := range after.Actors {
				if a.AgentID == owner && a.Location != source.proposal.Location {
					return empty, fmt.Errorf("continuation relocated its owner")
				}
			}
		}
	}
	return VerifiedCharacterArbitrationRoundV1{sources: s, receipt: r, after: after, verified: true}, nil
}
