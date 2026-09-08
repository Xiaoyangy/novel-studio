package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

const CharacterWorkContinuationPolicyV1 = "character-work-continuation:explicit.v1"

// This grant is part of the ORIGINAL independently submitted proposal. A task
// requirement alone is never permission to keep executing it. MaxEffectiveMinutes
// counts work after that proposal, including work in its original cycle.
type CharacterWorkContinuationAuthorizationV1 struct {
	TaskID              string   `json:"task_id"`
	UntilTarget         bool     `json:"until_target,omitempty"`
	MaxEffectiveMinutes *float64 `json:"max_effective_minutes,omitempty"`
}

// Host-only authorization to resume one stationary work task, not a new choice
// or a predicted outcome. Original proposal/observation bytes are never changed.
type CharacterWorkContinuationReceiptV1 struct {
	Version                 string  `json:"version"`
	GenerationID            string  `json:"generation_id"`
	Chapter                 int     `json:"chapter"`
	Cycle                   int     `json:"cycle"`
	AgentID                 string  `json:"agent_id"`
	TaskID                  string  `json:"task_id"`
	OriginCycleDigest       string  `json:"origin_cycle_digest"`
	OriginProposalDigest    string  `json:"origin_proposal_digest"`
	OriginObservationDigest string  `json:"origin_observation_digest"`
	AuthorizationDigest     string  `json:"authorization_digest"`
	LedgerDigest            string  `json:"ledger_digest"`
	InputSetDigest          string  `json:"input_set_digest"`
	StimulusDigest          string  `json:"stimulus_digest"`
	ObservationDigest       string  `json:"observation_digest"`
	BeforePhysicalRoot      string  `json:"before_physical_root"`
	InformationRoot         string  `json:"information_root"`
	StartDay                float64 `json:"start_day"`
	EffectiveCompleted      float64 `json:"effective_completed"`
	AuthorizedRemaining     float64 `json:"authorized_remaining"`
	Digest                  string  `json:"digest"`
}

type CharacterWorkContinuationEligibilityV1 struct {
	Eligible    bool                                `json:"eligible"`
	WakeReasons []string                            `json:"wake_reasons,omitempty"`
	Receipt     *CharacterWorkContinuationReceiptV1 `json:"receipt,omitempty"`
}

// Complete, bounded host proof for a segment. There is deliberately no writable
// cumulative-progress field. EntryDigest is this segment's independently
// derived root, NOT a claim to be an old CharacterActivationCycle.Digest. A future
// cycle adapter must bind this root explicitly into its new evidence contract.
type CharacterWorkContinuationExecutionV1 struct {
	Input                   CharacterActivationInputSet             `json:"input"`
	Continuation            CharacterWorkContinuationReceiptV1      `json:"continuation"`
	Arbitration             WorldArbitrationReceipt                 `json:"arbitration"`
	FreshPeerProposals      []CharacterDecisionProposal             `json:"fresh_peer_proposals,omitempty"`
	PeerContinuations       []CharacterWorkContinuationPeerSourceV1 `json:"peer_continuations,omitempty"`
	ContinuationSourcesRoot string                                  `json:"continuation_sources_root,omitempty"`
	// Source-aware ledger v2 only; old ledger/cycle bytes omit this field.
	RoundSourceDigest string                     `json:"round_source_digest,omitempty"`
	Executions        []CharacterSelfExecutionV2 `json:"executions"`
	AfterPhysicalRoot string                     `json:"after_physical_root"`
	EntryDigest       string                     `json:"entry_digest"`
}

// The source cycle is verified with its own observations. Continuation entries
// use their explicit authorization path, never the old fresh-proposal bundle
// validator. Scope is one chapter and one owner, with at most 64 execution slots.
type CharacterWorkContinuationLedgerV1 struct {
	Version        string                                 `json:"version"`
	Origin         CharacterActivationCycle               `json:"origin"`
	ProposalDigest string                                 `json:"proposal_digest"`
	TaskID         string                                 `json:"task_id"`
	Entries        []CharacterWorkContinuationExecutionV1 `json:"entries,omitempty"`
	Digest         string                                 `json:"digest"`
}

func HasCharacterWorkContinuationPolicyV1(sources []string) bool {
	return planningV2ContainsExactString(sources, CharacterWorkContinuationPolicyV1)
}

func ValidateCharacterWorkContinuationIntentV1(p CharacterDecisionProposal, o CharacterObservationPacket) error {
	if len(p.WorkContinuations) == 0 {
		return nil
	}
	if o.Version != CharacterObservationV2Version || !HasCharacterWorkContinuationPolicyV1(o.Sources) || !HasCharacterSelfExperiencePolicyV2(o.Sources) {
		return fmt.Errorf("work continuation requires explicit owner policy")
	}
	if len(p.WorkContinuations) > CharacterSelfTaskLimitV2 {
		return fmt.Errorf("too many work continuation grants")
	}
	seen := map[string]bool{}
	for _, grant := range p.WorkContinuations {
		var task CharacterSelfTaskV2
		for _, candidate := range p.SelfTasks {
			if candidate.TaskID == grant.TaskID {
				task = candidate
			}
		}
		if task.Kind != "work" || seen[grant.TaskID] || !physicalAmountV2(task.ProgressTarget) || *task.ProgressTarget <= 0 {
			return fmt.Errorf("work continuation requires a unique finite work target")
		}
		seen[grant.TaskID] = true
		if grant.UntilTarget == (grant.MaxEffectiveMinutes != nil) || (grant.MaxEffectiveMinutes != nil && (!physicalAmountV2(grant.MaxEffectiveMinutes) || *grant.MaxEffectiveMinutes <= 0)) {
			return fmt.Errorf("work continuation must explicitly choose until_target or positive max_effective_minutes")
		}
	}
	return nil
}

type continuationStateV1 struct {
	proposal                         CharacterDecisionProposal
	observation                      CharacterObservationPacket
	task                             CharacterSelfTaskV2
	grant                            CharacterWorkContinuationAuthorizationV1
	baseline, completed, grantedWork float64
	status                           string
	physical                         WorldPhysicalStateV2
	day                              float64
	cycle                            int
	feedback                         map[string]string
	unsupported                      bool
}

func NewCharacterWorkContinuationLedgerV1(origin CharacterActivationCycle, proposalDigest, taskID string, boundaries ...VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationLedgerV1, error) {
	ledger := CharacterWorkContinuationLedgerV1{Version: CharacterWorkContinuationPolicyV1, Origin: origin, ProposalDigest: proposalDigest, TaskID: taskID}
	if _, err := continuationOriginV1(ledger, boundaries); err != nil {
		return ledger, err
	}
	var err error
	ledger.Digest, err = continuationLedgerDigestV1(ledger)
	return continuationCloneV1(ledger), err
}

func continuationOriginV1(ledger CharacterWorkContinuationLedgerV1, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (continuationStateV1, error) {
	var state continuationStateV1
	if ledger.Version != CharacterWorkContinuationPolicyV1 {
		return state, fmt.Errorf("unsupported work continuation ledger")
	}
	var verifiedAfter *WorldPhysicalStateV2
	if ledger.Origin.Version == CharacterActivationCycleV2Version {
		after, err := validateCharacterContinuationOriginV2(ledger.Origin, boundaries)
		if err != nil {
			return state, err
		}
		verifiedAfter = &after
	} else if err := ValidateCharacterActivationCycle(ledger.Origin); err != nil {
		return state, err
	}
	e := ledger.Origin.Evidence
	if !HasCharacterWorkContinuationPolicyV1(e.Stimulus.Sources) {
		return state, fmt.Errorf("origin cycle has no continuation policy")
	}
	for _, p := range LatestCharacterCycleProposals(e) {
		if p.Digest == ledger.ProposalDigest {
			state.proposal = p
		}
	}
	for _, o := range e.Observations {
		if o.Digest == state.proposal.ObservationDigest {
			state.observation = o
		}
	}
	if state.proposal.Digest == "" || state.observation.Digest == "" {
		return state, fmt.Errorf("continuation origin is not an executed original proposal/observation")
	}
	if err := ValidateCharacterWorkContinuationIntentV1(state.proposal, state.observation); err != nil {
		return state, err
	}
	for _, grant := range state.proposal.WorkContinuations {
		if grant.TaskID == ledger.TaskID {
			state.grant = grant
		}
	}
	for _, task := range state.proposal.SelfTasks {
		if task.TaskID == ledger.TaskID {
			state.task = task
		}
	}
	if state.grant.TaskID == "" {
		return state, fmt.Errorf("task target is not explicit continuation authority")
	}
	// Complex original choices remain legal. They need fresh decisions until
	// the cycle protocol gains an explicit multi-task/auxiliary-action resolver.
	state.unsupported = len(state.proposal.SelfTasks) != 1 || len(state.task.ObservationRequests) != 0 || len(state.proposal.Communications)+len(state.proposal.ResourceReads)+len(state.proposal.ResourceMeasurements)+len(state.proposal.ResourceReports)+len(state.proposal.ResourceEstimates) != 0 || state.proposal.Round != 1
	for _, actor := range e.Stimulus.PhysicalState.Actors {
		if actor.AgentID != state.proposal.AgentID {
			continue
		}
		for _, exp := range actor.SelfExperiences {
			if exp.TaskID != ledger.TaskID || exp.Kind != "work" {
				continue
			}
			if exp.Action != state.task.Action || exp.ProgressUnit != selfTaskUnitV2(state.task) || !samePhysicalNumberV2(exp.ProgressTarget, state.task.ProgressTarget) {
				return state, fmt.Errorf("origin task baseline changed definition")
			}
			if selfExecutionActiveV2(exp.Status) {
				state.baseline += (*exp.EndDay - *exp.StartDay) * 1440
			}
		}
	}
	r := e.Arbitrations[len(e.Arbitrations)-1]
	if !r.Finalized || r.HardContractStatus != "feasible" {
		return state, fmt.Errorf("unclosed origin cannot authorize continuation")
	}
	resolution, err := continuationResolutionV1(r, state.proposal)
	if err != nil {
		return state, err
	}
	scopedExecutions := resolution
	scopedExecutions.SelfExecutions = nil
	for _, execution := range resolution.SelfExecutions {
		if execution.TaskID == ledger.TaskID {
			scopedExecutions.SelfExecutions = append(scopedExecutions.SelfExecutions, execution)
		}
	}
	state.grantedWork, state.status, err = continuationSegmentV1(scopedExecutions, ledger.TaskID)
	if err != nil {
		return state, err
	}
	state.completed = state.baseline + state.grantedWork
	if math.IsNaN(state.completed) || math.IsInf(state.completed, 0) {
		return state, fmt.Errorf("nonfinite work execution ledger")
	}
	if verifiedAfter != nil {
		state.physical = continuationCloneV1(*verifiedAfter)
	} else {
		state.physical, err = ApplyArbitrationPhysicalStateV2(r, e.Stimulus, LatestCharacterCycleProposals(e)...)
		if err != nil {
			return state, err
		}
	}
	state.day, state.cycle = ledger.Origin.EndDay, ledger.Origin.Index
	state.feedback = map[string]string{}
	text, err := CharacterActivationPrivateOutcome(state.proposal, resolution, state.physical, r)
	if err != nil {
		return state, err
	}
	state.feedback[r.Digest] = text
	return state, nil
}

func continuationLedgerDigestV1(value CharacterWorkContinuationLedgerV1) (string, error) {
	value.Digest = ""
	return characterAgentDigest(value)
}

func ComputeCharacterWorkContinuationLedgerV1Digest(value CharacterWorkContinuationLedgerV1) (string, error) {
	return continuationLedgerDigestV1(value)
}

func ValidateCharacterWorkContinuationLedgerV1(ledger CharacterWorkContinuationLedgerV1, boundaries ...VerifiedCharacterWorkContinuationBoundaryV1) error {
	_, err := validateContinuationLedgerV1(ledger, boundaries)
	return err
}

func validateContinuationLedgerV1(ledger CharacterWorkContinuationLedgerV1, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (continuationStateV1, error) {
	state, err := continuationOriginV1(ledger, boundaries)
	if err != nil {
		return state, err
	}
	if len(ledger.Entries)+ledger.Origin.Index > 64 {
		return state, fmt.Errorf("continuation ledger exceeds chapter bound")
	}
	want, err := continuationLedgerDigestV1(ledger)
	if err != nil || want != ledger.Digest {
		return state, fmt.Errorf("continuation ledger digest mismatch")
	}
	prefix, err := NewCharacterWorkContinuationLedgerV1(ledger.Origin, ledger.ProposalDigest, ledger.TaskID, boundaries...)
	if err != nil {
		return state, err
	}
	for _, entry := range ledger.Entries {
		var derived CharacterWorkContinuationExecutionV1
		var next continuationStateV1
		if len(entry.PeerContinuations) > 0 || entry.ContinuationSourcesRoot != "" {
			derived, next, err = authenticateCommittedContinuationBatchEntryV1(prefix, state, entry, boundaries)
		} else {
			derived, next, err = authenticateContinuationExecutionV1(prefix, state, entry.Input, entry.Continuation, entry.Arbitration, entry.FreshPeerProposals, boundaries)
		}
		if err != nil {
			return state, err
		}
		if !samePhysicalValueV2(derived, entry) {
			return state, fmt.Errorf("continuation execution entry is not source-derived")
		}
		prefix.Entries = append(prefix.Entries, derived)
		prefix.Digest, err = continuationLedgerDigestV1(prefix)
		if err != nil {
			return state, err
		}
		state = next
	}
	return state, nil
}

func EvaluateCharacterWorkContinuationV1(ledger CharacterWorkContinuationLedgerV1, current CharacterActivationInputSet, boundaries ...VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationEligibilityV1, error) {
	state, err := validateContinuationLedgerV1(ledger, boundaries)
	if err != nil {
		return CharacterWorkContinuationEligibilityV1{}, err
	}
	return evaluateWorkContinuationV1(ledger, state, current, boundaries)
}

func ValidateCharacterWorkContinuationReceiptV1(receipt CharacterWorkContinuationReceiptV1, ledger CharacterWorkContinuationLedgerV1, current CharacterActivationInputSet, boundaries ...VerifiedCharacterWorkContinuationBoundaryV1) error {
	result, err := EvaluateCharacterWorkContinuationV1(ledger, current, boundaries...)
	if err != nil {
		return err
	}
	if !result.Eligible || !samePhysicalValueV2(result.Receipt, &receipt) {
		return fmt.Errorf("continuation receipt is not the exact eligible host derivation")
	}
	return nil
}

func ComputeCharacterWorkContinuationReceiptV1Digest(receipt CharacterWorkContinuationReceiptV1) (string, error) {
	receipt.Digest = ""
	return characterAgentDigest(receipt)
}

func evaluateWorkContinuationV1(ledger CharacterWorkContinuationLedgerV1, state continuationStateV1, current CharacterActivationInputSet, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationEligibilityV1, error) {
	var result CharacterWorkContinuationEligibilityV1
	if err := ValidateCharacterActivationInputSet(current); err != nil {
		return result, err
	}
	if current.Stimulus.GenerationID != ledger.Origin.GenerationID || current.Stimulus.Chapter != ledger.Origin.Chapter || !HasCharacterWorkContinuationPolicyV1(current.Stimulus.Sources) {
		return result, fmt.Errorf("continuation has a foreign chapter/generation/policy")
	}
	var owner CharacterObservationPacket
	for _, o := range current.Observations {
		if o.AgentID == state.proposal.AgentID {
			owner = o
		}
	}
	if owner.Digest == "" || !HasCharacterWorkContinuationPolicyV1(owner.Sources) || owner.CycleContext == nil || owner.CycleContext.Index != state.cycle+1 || owner.CycleContext.ChapterContextDigest != ledger.Origin.ChapterContextDigest || owner.CycleContext.CurrentDay != state.day || current.Stimulus.StoryClock.CurrentDay != state.day {
		return result, fmt.Errorf("continuation input does not follow its ordered execution ledger")
	}
	previousRoot := ledger.Origin.Digest
	if len(ledger.Entries) > 0 {
		var boundaryErr error
		previousRoot, boundaryErr = verifiedContinuationGlobalRootV1(ledger.Entries[len(ledger.Entries)-1], boundaries)
		if boundaryErr != nil {
			return result, boundaryErr
		}
	}
	if owner.CycleContext.PreviousCycleDigest != previousRoot {
		return result, fmt.Errorf("continuation input is not bound to the exact preceding execution root")
	}
	before, err := CharacterPhysicalRootForCycle(*current.Stimulus.PhysicalState)
	if err != nil {
		return result, err
	}
	expected, err := CharacterPhysicalRootForCycle(state.physical)
	if err != nil || expected != before {
		result.WakeReasons = []string{"continuation_prestate_changed"}
		return result, nil
	}
	wake := func(reason string) { result.WakeReasons = append(result.WakeReasons, reason) }
	executable := false
	for _, entry := range current.Activation.Entries {
		if entry.AgentID == state.proposal.AgentID {
			executable = entry.State == CharacterAgentActive
		}
	}
	if !executable {
		wake("owner_not_executable")
	}
	if state.unsupported {
		wake("requires_fresh_complex_intent")
	}
	originClock := ledger.Origin.Evidence.Stimulus.StoryClock
	if current.Stimulus.StoryClock.TimeContractCoreDigest != originClock.TimeContractCoreDigest || current.Stimulus.StoryClock.NominalBudget != originClock.NominalBudget || current.Stimulus.StoryClock.DurationDaysMin != originClock.DurationDaysMin || current.Stimulus.StoryClock.DurationDaysMax != originClock.DurationDaysMax {
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
	if len(owner.ConflictFeedback) != 0 {
		wake("conflict_feedback")
	}
	baseline := state.observation
	baseline.CurrentGoal, baseline.Pressure = state.proposal.CurrentGoal, state.proposal.Pressure
	oldRoot, err := continuationInformationRootV1(baseline, state, ledger.Origin.Evidence.Stimulus.PhysicalState)
	if err != nil {
		return result, err
	}
	newRoot, err := continuationInformationRootV1(owner, state, current.Stimulus.PhysicalState)
	if err != nil {
		return result, err
	}
	if oldRoot != newRoot {
		wake("owner_information_changed")
	}
	clock := current.Stimulus.StoryClock
	if !clock.NominalBudget && clock.CurrentDay >= clock.DurationDaysMax-1e-12 {
		wake("known_time_limit")
	}
	if len(result.WakeReasons) > 0 {
		return result, nil
	}
	grantDigest, err := characterAgentDigest(state.grant)
	if err != nil {
		return result, err
	}
	r := CharacterWorkContinuationReceiptV1{Version: CharacterWorkContinuationPolicyV1, GenerationID: ledger.Origin.GenerationID, Chapter: ledger.Origin.Chapter, Cycle: owner.CycleContext.Index, AgentID: state.proposal.AgentID, TaskID: ledger.TaskID, OriginCycleDigest: ledger.Origin.Digest, OriginProposalDigest: state.proposal.Digest, OriginObservationDigest: state.observation.Digest, AuthorizationDigest: grantDigest, LedgerDigest: ledger.Digest, InputSetDigest: current.Digest, StimulusDigest: current.Stimulus.Digest, ObservationDigest: owner.Digest, BeforePhysicalRoot: before, InformationRoot: newRoot, StartDay: state.day, EffectiveCompleted: state.completed, AuthorizedRemaining: remaining}
	r.Digest, err = ComputeCharacterWorkContinuationReceiptV1Digest(r)
	if err != nil {
		return result, err
	}
	result.Eligible, result.Receipt = true, &r
	return result, nil
}

func continuationInformationRootV1(o CharacterObservationPacket, state continuationStateV1, physical *WorldPhysicalStateV2) (string, error) {
	o = continuationCloneV1(o)
	// Envelope/time changes and verified normal work feedback are not new
	// knowledge. Never suppress arbitrary memory merely because its kind says
	// projected_decision: the exact owner text AND arbitration digest must match.
	var memories []CharacterAgentMemoryFact
	for _, m := range o.Memory {
		if text, ok := state.feedback[m.SourceDigest]; !ok || m.Kind != "projected_decision" || m.Text != text {
			memories = append(memories, m)
		}
	}
	var otherExperiences []CharacterSelfExperienceV2
	for _, exp := range o.SelfExperiences {
		if exp.TaskID != state.task.TaskID {
			otherExperiences = append(otherExperiences, exp)
		}
	}
	var otherTasks []CharacterTaskProgressV2
	for _, task := range o.TaskProgress {
		if task.TaskID != state.task.TaskID {
			otherTasks = append(otherTasks, task)
		}
	}
	var received []CharacterReceivedFactV2
	var actualOwnerObservations []CharacterOperationalObservationV1
	for _, actor := range physical.Actors {
		if actor.AgentID == o.AgentID {
			received = actor.ReceivedFacts
			actualOwnerObservations = actor.OperationalObservations
		}
	}
	return characterAgentDigest(struct {
		Agent, Character, Location, Goal, Pressure string
		Facts, Events, Rules                       []CharacterAgentFact
		Mechanisms                                 []CodexMechanism
		Resources                                  []CharacterResourceViewV2
		Relations, Commitments                     []string
		Memory                                     []CharacterAgentMemoryFact
		Experiences                                []CharacterSelfExperienceV2
		Tasks                                      []CharacterTaskProgressV2
		Operational                                []CharacterOperationalObservationV1
		Received                                   []CharacterReceivedFactV2
		ActualOwnerObservations                    []CharacterOperationalObservationV1
	}{o.AgentID, o.Character, o.Location, o.CurrentGoal, o.Pressure, o.KnownFacts, o.PerceivedEvents, o.PublicRules, o.PublicMechanisms, o.ResourceViews, o.Relationships, o.Commitments, memories, otherExperiences, otherTasks, o.OperationalObservations, received, actualOwnerObservations})
}

func AppendCharacterWorkContinuationExecutionV1(ledger CharacterWorkContinuationLedgerV1, current CharacterActivationInputSet, receipt CharacterWorkContinuationReceiptV1, arbitration WorldArbitrationReceipt, boundaries []VerifiedCharacterWorkContinuationBoundaryV1, freshPeerProposals ...CharacterDecisionProposal) (CharacterWorkContinuationLedgerV1, error) {
	state, err := validateContinuationLedgerV1(ledger, boundaries)
	if err != nil {
		return ledger, err
	}
	entry, _, err := authenticateContinuationExecutionV1(ledger, state, current, receipt, arbitration, freshPeerProposals, boundaries)
	if err != nil {
		return ledger, err
	}
	ledger = continuationCloneV1(ledger)
	ledger.Entries = append(ledger.Entries, entry)
	ledger.Digest, err = continuationLedgerDigestV1(ledger)
	return ledger, err
}

func authenticateContinuationExecutionV1(ledger CharacterWorkContinuationLedgerV1, state continuationStateV1, input CharacterActivationInputSet, receipt CharacterWorkContinuationReceiptV1, arbitration WorldArbitrationReceipt, peers []CharacterDecisionProposal, boundaries []VerifiedCharacterWorkContinuationBoundaryV1) (CharacterWorkContinuationExecutionV1, continuationStateV1, error) {
	var entry CharacterWorkContinuationExecutionV1
	eligible, err := evaluateWorkContinuationV1(ledger, state, input, boundaries)
	if err != nil {
		return entry, state, err
	}
	if !eligible.Eligible || !samePhysicalValueV2(eligible.Receipt, &receipt) {
		return entry, state, fmt.Errorf("continuation execution lacks exact eligible authorization")
	}
	if !arbitration.Finalized || arbitration.HardContractStatus != "feasible" {
		return entry, state, fmt.Errorf("continuation segment requires finalized work-only arbitration")
	}
	for _, delivery := range arbitration.ResourceDeliveries {
		if delivery.FromAgentID == state.proposal.AgentID {
			return entry, state, fmt.Errorf("continuation cannot perform resource delivery")
		}
	}
	for _, reception := range arbitration.PassiveReceptions {
		if reception.FromAgentID == state.proposal.AgentID {
			return entry, state, fmt.Errorf("continuation cannot repeat communication")
		}
	}
	active := map[string]bool{}
	for _, actor := range input.Activation.Entries {
		if actor.State == CharacterAgentActive {
			active[actor.AgentID] = true
		}
	}
	if !active[state.proposal.AgentID] {
		return entry, state, fmt.Errorf("continuation cannot execute a sleeping actor")
	}
	delete(active, state.proposal.AgentID)
	proposals := []CharacterDecisionProposal{state.proposal} // Original bytes, never a synthetic current proposal.
	for _, peer := range peers {
		if !active[peer.AgentID] {
			return entry, state, fmt.Errorf("fresh proposal supersedes continuation authority")
		}
		delete(active, peer.AgentID)
		var observation CharacterObservationPacket
		for _, o := range input.Observations {
			if o.AgentID == peer.AgentID {
				observation = o
			}
		}
		valid, err := FinalizeCharacterDecisionProposal(peer, observation)
		if err != nil || valid.Digest != peer.Digest {
			return entry, state, fmt.Errorf("peer is not a fresh current-observation proposal")
		}
		proposals = append(proposals, peer)
	}
	if len(active) != 0 {
		return entry, state, fmt.Errorf("continuation arbitration omits a fresh active peer")
	}
	// Validate the unchanged original intent through the existing physical
	// constraint kernel. This is NOT the old fresh-proposal EvidenceBundle path.
	valid, err := FinalizeWorldArbitrationReceipt(continuationCloneV1(arbitration), input.Stimulus, input.Activation, proposals, 1)
	if err != nil {
		return entry, state, err
	}
	if !samePhysicalValueV2(valid, arbitration) {
		return entry, state, fmt.Errorf("arbitration bytes/digest are not finalized")
	}
	resolution, err := continuationResolutionV1(arbitration, state.proposal)
	if err != nil {
		return entry, state, err
	}
	minutes, status, err := continuationSegmentV1(resolution, ledger.TaskID)
	if err != nil {
		return entry, state, err
	}
	if minutes > receipt.AuthorizedRemaining+1e-9 {
		return entry, state, fmt.Errorf("continuation execution exceeds explicit authorization")
	}
	after, err := ApplyArbitrationPhysicalStateV2(arbitration, input.Stimulus, proposals...)
	if err != nil {
		return entry, state, err
	}
	for _, actor := range after.Actors {
		if actor.AgentID == state.proposal.AgentID && actor.Location != state.proposal.Location {
			return entry, state, fmt.Errorf("stationary work continuation cannot authorize relocation")
		}
	}
	root, err := CharacterPhysicalRootForCycle(after)
	if err != nil {
		return entry, state, err
	}
	entry = CharacterWorkContinuationExecutionV1{Input: continuationCloneV1(input), Continuation: receipt, Arbitration: continuationCloneV1(arbitration), FreshPeerProposals: continuationCloneV1(peers), Executions: continuationCloneV1(resolution.SelfExecutions), AfterPhysicalRoot: root}
	entry.EntryDigest, err = characterAgentDigest(struct{ Input, Authorization, Arbitration, Before, After string }{input.Digest, receipt.Digest, arbitration.Digest, receipt.BeforePhysicalRoot, root})
	if err != nil {
		return entry, state, err
	}
	state.completed += minutes
	state.grantedWork += minutes
	state.status, state.day, state.cycle, state.physical = status, arbitration.StoryTime.EndDay, receipt.Cycle, after
	state.feedback = continuationCloneV1(state.feedback)
	text, err := CharacterActivationPrivateOutcome(state.proposal, resolution, after, arbitration)
	if err != nil {
		return entry, state, err
	}
	state.feedback[arbitration.Digest] = text
	return entry, state, nil
}

func continuationResolutionV1(receipt WorldArbitrationReceipt, proposal CharacterDecisionProposal) (CharacterDecisionResolution, error) {
	for _, r := range receipt.Resolutions {
		if r.AgentID == proposal.AgentID && r.ProposalDigest == proposal.Digest && r.Decision == proposal.Decision && r.IntendedAction == proposal.IntendedAction {
			return r, nil
		}
	}
	return CharacterDecisionResolution{}, fmt.Errorf("continuation lacks the original owner's actual resolution")
}

func continuationSegmentV1(resolution CharacterDecisionResolution, taskID string) (float64, string, error) {
	executions := append([]CharacterSelfExecutionV2(nil), resolution.SelfExecutions...)
	if len(executions) == 0 {
		return 0, "", fmt.Errorf("continuation has no actual self execution")
	}
	for _, execution := range executions {
		if err := validateCharacterSelfExecutionShapeV2(execution); err != nil {
			return 0, "", err
		}
		if !selfExecutionActiveV2(execution.Status) && len(executions) != 1 {
			return 0, "", fmt.Errorf("continuation mixes execution and interruption")
		}
	}
	sort.SliceStable(executions, func(i, j int) bool {
		if executions[i].StartDay == nil {
			return executions[j].StartDay != nil
		}
		if executions[j].StartDay == nil {
			return false
		}
		return *executions[i].StartDay < *executions[j].StartDay
	})
	minutes, end, status := 0.0, -1.0, ""
	for _, execution := range executions {
		if execution.TaskID != taskID || len(execution.ObservationResults) != 0 {
			return 0, "", fmt.Errorf("continuation exceeded the granted pure work scope")
		}
		if err := validateCharacterSelfExecutionShapeV2(execution); err != nil {
			return 0, "", err
		}
		if !selfExecutionActiveV2(execution.Status) {
			if len(executions) != 1 {
				return 0, "", fmt.Errorf("continuation mixes execution and interruption")
			}
			return 0, execution.Status, nil
		}
		if *execution.StartDay < end-1e-12 || status == "completed" {
			return 0, "", fmt.Errorf("continuation execution is overlapping or follows completion")
		}
		minutes += (*execution.EndDay - *execution.StartDay) * 1440
		end, status = *execution.EndDay, execution.Status
	}
	if math.IsNaN(minutes) || math.IsInf(minutes, 0) {
		return 0, "", fmt.Errorf("nonfinite continuation duration")
	}
	if resolution.Outcome == "blocked" || resolution.CompletionState == "blocked" {
		status = "blocked"
	}
	return minutes, status, nil
}

func continuationCloneV1[T any](value T) T {
	raw, _ := json.Marshal(value)
	var clone T
	_ = json.Unmarshal(raw, &clone)
	return clone
}
