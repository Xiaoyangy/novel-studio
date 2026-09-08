package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A runtime-only, immutable-by-API source resolver. No field is JSON-visible;
// a restart must rebuild authority from the ordered flat input/cycle chain.
// Steps never contain their prefix or recursively embed predecessor histories.
type VerifiedCharacterActivationPrefix struct {
	session    CharacterActivationSession
	steps      []VerifiedCharacterActivationStep
	ledgers    map[string]CharacterWorkContinuationLedgerV1
	boundaries []VerifiedCharacterWorkContinuationBoundaryV1
	verified   bool
}

type VerifiedCharacterActivationStep struct {
	input              CharacterActivationInputSet
	cycle              CharacterActivationCycle
	effectiveProposals []CharacterDecisionProposal
	afterState         WorldPhysicalStateV2
	boundary           VerifiedCharacterWorkContinuationBoundaryV1
	rounds             []VerifiedCharacterArbitrationRoundV1
	verified           bool
}

func cloneVerifiedActivationBoundary(boundary VerifiedCharacterWorkContinuationBoundaryV1) VerifiedCharacterWorkContinuationBoundaryV1 {
	if !boundary.verified {
		return VerifiedCharacterWorkContinuationBoundaryV1{}
	}
	binding := boundary.binding
	binding.ContinuationEntryDigests = append([]string(nil), boundary.binding.ContinuationEntryDigests...)
	if boundary.binding.AfterPhysicalState != nil {
		state, _ := cloneVerifiedActivationValue(*boundary.binding.AfterPhysicalState)
		binding.AfterPhysicalState = &state
	}
	return VerifiedCharacterWorkContinuationBoundaryV1{binding: binding, verified: true}
}

func cloneVerifiedActivationValue[T any](value T) (T, error) {
	var result T
	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// Getters only copy previously validated, JSON-serializable values. A zero
// wrapper (including one unmarshaled from JSON) never yields source authority.
func (p VerifiedCharacterActivationPrefix) Session() CharacterActivationSession {
	if !p.verified {
		return CharacterActivationSession{}
	}
	value, _ := cloneVerifiedActivationValue(p.session)
	return value
}

func (p VerifiedCharacterActivationPrefix) Steps() []VerifiedCharacterActivationStep {
	if !p.verified {
		return nil
	}
	result := make([]VerifiedCharacterActivationStep, len(p.steps))
	for i, step := range p.steps {
		result[i] = cloneVerifiedActivationStep(step)
	}
	return result
}

// Step returns one detached verified step (zero-based), avoiding copies of the
// entire history for an indexed lookup. It does not skip source verification:
// callers must already have obtained this runtime-only verified prefix.
func (p VerifiedCharacterActivationPrefix) Step(index int) (VerifiedCharacterActivationStep, bool) {
	if !p.verified || index < 0 || index >= len(p.steps) {
		return VerifiedCharacterActivationStep{}, false
	}
	return cloneVerifiedActivationStep(p.steps[index]), true
}

func cloneVerifiedActivationStep(step VerifiedCharacterActivationStep) VerifiedCharacterActivationStep {
	return VerifiedCharacterActivationStep{input: step.Input(), cycle: step.Cycle(), effectiveProposals: step.EffectiveProposals(), afterState: step.AfterState(), boundary: cloneVerifiedActivationBoundary(step.boundary), rounds: step.ArbitrationRounds(), verified: step.verified}
}

func (p VerifiedCharacterActivationPrefix) ContinuationBoundaries() []VerifiedCharacterWorkContinuationBoundaryV1 {
	if !p.verified {
		return nil
	}
	result := make([]VerifiedCharacterWorkContinuationBoundaryV1, len(p.boundaries))
	for i, boundary := range p.boundaries {
		result[i] = cloneVerifiedActivationBoundary(boundary)
	}
	return result
}

func (p VerifiedCharacterActivationPrefix) ContinuationLedger(agentID string) (CharacterWorkContinuationLedgerV1, bool) {
	if !p.verified {
		return CharacterWorkContinuationLedgerV1{}, false
	}
	ledger, exists := p.ledgers[agentID]
	if !exists {
		return CharacterWorkContinuationLedgerV1{}, false
	}
	value, err := cloneVerifiedActivationValue(ledger)
	return value, err == nil
}

func (s VerifiedCharacterActivationStep) Input() CharacterActivationInputSet {
	if !s.verified {
		return CharacterActivationInputSet{}
	}
	value, _ := cloneVerifiedActivationValue(s.input)
	return value
}

func (s VerifiedCharacterActivationStep) Cycle() CharacterActivationCycle {
	if !s.verified {
		return CharacterActivationCycle{}
	}
	value, _ := cloneVerifiedActivationValue(s.cycle)
	return value
}

func (s VerifiedCharacterActivationStep) EffectiveProposals() []CharacterDecisionProposal {
	if !s.verified {
		return nil
	}
	value, _ := cloneVerifiedActivationValue(s.effectiveProposals)
	return value
}

// ArbitrationRounds exposes detached runtime source capabilities for V3.
// Legacy steps keep returning nil, and JSON cannot restore these capabilities.
func (s VerifiedCharacterActivationStep) ArbitrationRounds() []VerifiedCharacterArbitrationRoundV1 {
	if !s.verified {
		return nil
	}
	var out []VerifiedCharacterArbitrationRoundV1
	for _, round := range s.rounds {
		out = append(out, VerifiedCharacterArbitrationRoundV1{sources: round.Sources(), receipt: round.Receipt(), after: round.AfterState(), verified: round.verified})
	}
	return out
}

func (s VerifiedCharacterActivationStep) AfterState() WorldPhysicalStateV2 {
	if !s.verified {
		return WorldPhysicalStateV2{}
	}
	value, _ := cloneVerifiedActivationValue(s.afterState)
	return value
}

func (s VerifiedCharacterActivationStep) GlobalRoot() string {
	if !s.verified {
		return ""
	}
	return s.cycle.Digest
}

func NewVerifiedCharacterActivationPrefix(session CharacterActivationSession) (VerifiedCharacterActivationPrefix, error) {
	var result VerifiedCharacterActivationPrefix
	owned, err := cloneVerifiedActivationValue(session)
	if err != nil {
		return result, err
	}
	if err := ValidateCharacterActivationSession(owned); err != nil {
		return result, err
	}
	if owned.Phase != "collecting" || len(owned.CycleDigests)+len(owned.ReadinessDigests) != 0 || owned.CurrentPhysicalRoot != owned.InitialPhysicalRoot || owned.CurrentDay != owned.InitialDay {
		return result, fmt.Errorf("verified activation prefix must begin with an exact empty session; rebuild advanced sessions from their flat source chain")
	}
	return VerifiedCharacterActivationPrefix{session: owned, ledgers: map[string]CharacterWorkContinuationLedgerV1{}, verified: true}, nil
}

func validateVerifiedActivationInputSession(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet) error {
	if !prefix.verified {
		return fmt.Errorf("activation step requires a verified predecessor prefix")
	}
	session := prefix.session
	if err := ValidateCharacterActivationSession(session); err != nil {
		return err
	}
	if session.Phase != "collecting" || len(prefix.steps) != len(session.CycleDigests) || len(prefix.steps) >= session.MaxCycles {
		return fmt.Errorf("activation prefix is not collecting its next permitted step")
	}
	if err := ValidateCharacterActivationInputSet(input); err != nil {
		return err
	}
	if len(prefix.steps) > 0 && HasCharacterSurfaceInspectionPolicyV1(input.Stimulus.Sources) != HasCharacterSurfaceInspectionPolicyV1(prefix.steps[0].input.Stimulus.Sources) {
		return fmt.Errorf("activation input surface inspection policy differs from the verified session origin")
	}
	if len(prefix.steps) > 0 && HasCharacterWorkContinuationHistoryPolicyV1(input.Stimulus.Sources) != HasCharacterWorkContinuationHistoryPolicyV1(prefix.steps[0].input.Stimulus.Sources) {
		return fmt.Errorf("activation input continuation history policy differs from the verified session origin")
	}
	if len(prefix.steps) > 0 && HasCharacterSelfCompletionViewPolicyV1(input.Stimulus.Sources) != HasCharacterSelfCompletionViewPolicyV1(prefix.steps[0].input.Stimulus.Sources) {
		return fmt.Errorf("activation input self completion view policy differs from the verified session origin")
	}
	if input.Stimulus.Version != WorldStimulusPacketV2Version || input.Stimulus.PhysicalState == nil || input.Stimulus.StoryClock == nil {
		return fmt.Errorf("verified activation step requires typed physical state and actual story clock")
	}
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	token, err := CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(prefix.steps)+1, session.ChapterContextDigest, previous)
	if err != nil {
		return err
	}
	sources := 0
	for _, source := range input.Stimulus.Sources {
		if strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			sources++
			if source != token {
				return fmt.Errorf("activation input does not follow the exact preceding global cycle root")
			}
		}
	}
	root, err := CharacterPhysicalRootForCycle(*input.Stimulus.PhysicalState)
	if err != nil {
		return err
	}
	if sources != 1 || input.Stimulus.GenerationID != session.GenerationID || input.Stimulus.Chapter != session.Chapter || root != session.CurrentPhysicalRoot || input.Stimulus.StoryClock.CurrentDay != session.CurrentDay {
		return fmt.Errorf("activation input differs from the verified session identity, pre-state or actual clock")
	}
	if HasCharacterSelfChronologyPolicyV1(input.Stimulus.Sources) {
		if input.Stimulus.SelfEvaluationContext == nil {
			return fmt.Errorf("activation input lacks its host self-evaluation context")
		}
		if err := ValidateCharacterSelfEvaluationContextAgainstSessionV1(*input.Stimulus.SelfEvaluationContext, session); err != nil {
			return err
		}
	}
	return nil
}

func VerifyCharacterActivationStep(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (VerifiedCharacterActivationStep, VerifiedCharacterActivationPrefix, error) {
	var step VerifiedCharacterActivationStep
	ownedInput, err := cloneVerifiedActivationValue(input)
	if err != nil {
		return step, prefix, err
	}
	ownedCycle, err := cloneVerifiedActivationValue(cycle)
	if err != nil {
		return step, prefix, err
	}
	if err := validateVerifiedActivationInputSession(prefix, ownedInput); err != nil {
		return step, prefix, err
	}
	if len(prefix.steps) > 0 && prefix.steps[len(prefix.steps)-1].cycle.Version == CharacterActivationCycleV3Version && ownedCycle.Version != CharacterActivationCycleV3Version {
		return step, prefix, fmt.Errorf("v3 execution prefix cannot downgrade to a legacy cycle")
	}
	if ownedCycle.Version == CharacterActivationCycleV3Version {
		return VerifyCharacterActivationStepV3(prefix, ownedInput, ownedCycle)
	}
	if ownedCycle.Version == CharacterActivationCycleV2Version {
		return verifyCharacterContinuationStepV2(prefix, ownedInput, ownedCycle)
	}
	if ownedCycle.Version != CharacterActivationCycleVersion {
		return step, prefix, fmt.Errorf("unsupported activation step version")
	}
	if err := ValidateCharacterActivationInputsForCycle(ownedInput, ownedCycle); err != nil {
		return step, prefix, err
	}
	nextSession, err := AppendCharacterActivationCycle(prefix.session, ownedCycle)
	if err != nil {
		return step, prefix, err
	}
	proposals := LatestCharacterCycleProposals(ownedCycle.Evidence)
	receipt := ownedCycle.Evidence.Arbitrations[len(ownedCycle.Evidence.Arbitrations)-1]
	after, err := ApplyArbitrationPhysicalStateV2(receipt, ownedCycle.Evidence.Stimulus, proposals...)
	if err != nil {
		return step, prefix, err
	}
	ledgers := make(map[string]CharacterWorkContinuationLedgerV1, len(prefix.ledgers))
	for owner, ledger := range prefix.ledgers {
		ledgers[owner] = ledger // Private immutable values; updates replace, never mutate.
	}
	for _, proposal := range proposals {
		delete(ledgers, proposal.AgentID) // A fresh choice supersedes previous standing authority.
		if len(proposal.SelfTasks) != 1 || len(proposal.WorkContinuations) != 1 || proposal.WorkContinuations[0].TaskID != proposal.SelfTasks[0].TaskID {
			continue
		}
		ledger, err := NewCharacterWorkContinuationLedgerV1(ownedCycle, proposal.Digest, proposal.SelfTasks[0].TaskID)
		if err != nil {
			return step, prefix, err
		}
		ledgers[proposal.AgentID] = ledger
	}
	step = VerifiedCharacterActivationStep{input: ownedInput, cycle: ownedCycle, effectiveProposals: proposals, afterState: after, verified: true}
	next := VerifiedCharacterActivationPrefix{session: nextSession, steps: append(append([]VerifiedCharacterActivationStep(nil), prefix.steps...), step),
		ledgers: ledgers, boundaries: append([]VerifiedCharacterWorkContinuationBoundaryV1(nil), prefix.boundaries...), verified: true}
	return step, next, nil
}

// This validates the receipt/session transition, not the paid review audit.
// The existing store/chapter layer must still bind the full audited input.
func ApplyVerifiedCharacterActivationReadiness(prefix VerifiedCharacterActivationPrefix, receipt CharacterChapterReadiness) (VerifiedCharacterActivationPrefix, error) {
	if !prefix.verified || len(prefix.steps) != len(prefix.session.CycleDigests) {
		return prefix, fmt.Errorf("readiness requires a verified activation prefix")
	}
	owned, err := cloneVerifiedActivationValue(receipt)
	if err != nil {
		return prefix, err
	}
	if _, err := FinalizeCharacterChapterReadiness(owned); err != nil {
		return prefix, err
	}
	nextSession, err := ApplyCharacterChapterReadiness(prefix.session, owned)
	if err != nil {
		return prefix, err
	}
	next := prefix
	next.session = nextSession
	return next, nil
}

// Extension point for the explicit mixed fresh/continuation global contract.
// Common input/session/chronology checks have already passed. The old fresh
// evidence validator must NOT be relaxed to make this version acceptable.
func verifyCharacterContinuationStepV2(prefix VerifiedCharacterActivationPrefix, input CharacterActivationInputSet, cycle CharacterActivationCycle) (VerifiedCharacterActivationStep, VerifiedCharacterActivationPrefix, error) {
	return executeCharacterContinuationStepV2(prefix, input, cycle)
}
