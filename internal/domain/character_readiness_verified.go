package domain

import "fmt"

// This consumer accepts only runtime-verified source steps. In particular it
// does not reinterpret an old original proposal as a fresh current proposal,
// and it never routes a mixed cycle through the legacy evidence validator.
func BuildCharacterReadinessTraceFromSteps(steps []VerifiedCharacterActivationStep) (CharacterReadinessTrace, error) {
	var trace CharacterReadinessTrace
	if len(steps) == 0 || len(steps) > 64 {
		return trace, fmt.Errorf("readiness requires a bounded verified cycle chain")
	}
	for i, step := range steps {
		if !step.verified || step.GlobalRoot() == "" {
			return trace, fmt.Errorf("readiness cannot consume an unverified or JSON-created step")
		}
		// Read private immutable headers, not a deep copy of every old input.
		// Only the compact trace fields below cross the consumer boundary.
		cycle := step.cycle
		if cycle.Index != i+1 || cycle.ChapterContextDigest != steps[0].cycle.ChapterContextDigest ||
			cycle.GenerationID != steps[0].cycle.GenerationID || cycle.Chapter != steps[0].cycle.Chapter ||
			(i == 0 && cycle.PreviousDigest != "") ||
			(i > 0 && (cycle.PreviousDigest != steps[i-1].GlobalRoot() || cycle.BeforePhysicalRoot != steps[i-1].cycle.AfterPhysicalRoot || cycle.StartDay != steps[i-1].cycle.EndDay)) {
			return trace, fmt.Errorf("readiness verified steps are not one contiguous global source chain")
		}
		if len(cycle.Evidence.Arbitrations) == 0 {
			return trace, fmt.Errorf("readiness verified step lacks its actual arbitration")
		}
		receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
		clock, err := cloneVerifiedActivationValue(receipt.StoryTime)
		if err != nil || clock == nil || clock.Chapter != cycle.Chapter || clock.StartDay != cycle.StartDay || clock.EndDay != cycle.EndDay {
			return trace, fmt.Errorf("readiness verified step has inconsistent actual time")
		}
		view := CharacterReadinessCycleView{Index: cycle.Index, CycleDigest: step.GlobalRoot(), ArbitrationDigest: receipt.Digest,
			StoryTime: clock, HardContractStatus: receipt.HardContractStatus, HardContractConflicts: append([]string(nil), receipt.HardContractConflicts...)}
		proposals := map[string]CharacterDecisionProposal{}
		for _, proposal := range step.EffectiveProposals() {
			if proposals[proposal.AgentID].AgentID != "" {
				return trace, fmt.Errorf("readiness verified step has duplicate intent sources")
			}
			proposals[proposal.AgentID] = proposal
		}
		for _, resolution := range receipt.Resolutions {
			proposal, exists := proposals[resolution.AgentID]
			if !exists || proposal.Digest != resolution.ProposalDigest || proposal.Character != resolution.Character || proposal.Decision != resolution.Decision || proposal.IntendedAction != resolution.IntendedAction {
				return trace, fmt.Errorf("readiness action is not bound to its verified fresh or original continued intent")
			}
			delete(proposals, resolution.AgentID)
			view.Actions = append(view.Actions, CharacterReadinessAction{resolution.AgentID, resolution.Character, proposal.Digest, proposal.Decision, proposal.IntendedAction,
				resolution.Outcome, resolution.CompletionState, resolution.ImmediateResult, resolution.StateAfter})
		}
		if len(proposals) != 0 {
			return trace, fmt.Errorf("readiness omits a verified intent's actual outcome")
		}
		trace.Cycles = append(trace.Cycles, view)
	}
	last := steps[len(steps)-1]
	state := last.AfterState()
	root, err := CharacterPhysicalRootForCycle(state)
	if err != nil || root != last.cycle.AfterPhysicalRoot {
		return trace, fmt.Errorf("readiness final state does not match its verified global cycle")
	}
	trace.FinalPhysicalRoot, trace.Resources = root, state.Resources
	for _, actor := range state.Actors {
		resources, err := BuildCharacterResourceViewsV2(state, actor.AgentID)
		if err != nil {
			return trace, err
		}
		operations, err := selectCharacterOperationalObservationsV1(actor.OperationalObservations)
		if err != nil {
			return trace, err
		}
		trace.Actors = append(trace.Actors, CharacterReadinessActorView{AgentID: actor.AgentID, Character: actor.Character, Location: actor.Location,
			Resources: resources, TaskProgress: actor.TaskProgress, ReceivedFacts: actor.ReceivedFacts, OperationalObservations: operations})
	}
	return trace, nil
}

func NewCharacterReadinessReviewInputFromSteps(context CharacterReadinessContext, session CharacterActivationSession, steps []VerifiedCharacterActivationStep, reviewProtocol string) (CharacterReadinessReviewInput, error) {
	var input CharacterReadinessReviewInput
	checked, err := FinalizeCharacterReadinessContext(context)
	if err != nil {
		return input, err
	}
	if checked.Digest != context.Digest || context.Digest != session.ChapterContextDigest || context.GenerationID != session.GenerationID || context.Chapter != session.Chapter {
		return input, fmt.Errorf("readiness context is not the session's frozen chapter context")
	}
	if err := ValidateCharacterActivationSession(session); err != nil {
		return input, err
	}
	if session.Phase != "assessing" || len(steps) == 0 || len(steps) != len(session.CycleDigests) {
		return input, fmt.Errorf("readiness must assess the exact pending verified cycle chain")
	}
	for i, step := range steps {
		if !step.verified || step.GlobalRoot() != session.CycleDigests[i] || step.cycle.GenerationID != session.GenerationID || step.cycle.Chapter != session.Chapter || step.cycle.ChapterContextDigest != context.Digest {
			return input, fmt.Errorf("readiness step differs from its committed session/context")
		}
	}
	first, last := steps[0].cycle, steps[len(steps)-1].cycle
	if session.InitialPhysicalRoot != first.BeforePhysicalRoot || session.InitialDay != first.StartDay || session.CurrentPhysicalRoot != last.AfterPhysicalRoot || session.CurrentDay != last.EndDay {
		return input, fmt.Errorf("readiness session state/time differs from its verified source chain")
	}
	trace, err := BuildCharacterReadinessTraceFromSteps(steps)
	if err != nil {
		return input, err
	}
	if err := validatePlanningV2Digest("readiness protocol", reviewProtocol); err != nil {
		return input, err
	}
	context, err = cloneVerifiedActivationValue(context)
	if err != nil {
		return input, err
	}
	input = CharacterReadinessReviewInput{Policy: CharacterReadinessReviewPolicy, ReviewProtocol: reviewProtocol, SessionDigest: session.Digest,
		Context: context, Trace: trace, RemainingCycles: session.MaxCycles - len(steps)}
	input.Requirements, err = characterReadinessRequirements(context)
	return input, err
}
