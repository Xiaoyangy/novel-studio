package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

// Begin with complete independent original choices and real chronological
// evaluations. Only later cycles reuse the unchanged authorized proposals.
func verifiedInputOrigin(t *testing.T) (domain.VerifiedCharacterActivationPrefix, domain.VerifiedCharacterActivationStep) {
	t.Helper()
	origin, _, _ := selectionFixture(t, 2, nil)
	e := origin.Evidence
	state, err := domain.PrepareCharacterSelfChronologyStateV1(*e.Stimulus.PhysicalState)
	selectionMust(t, err)
	session, err := domain.NewCharacterActivationSession(origin.GenerationID, 1, origin.ChapterContextDigest, state, 0, 8)
	selectionMust(t, err)
	e.Stimulus.PhysicalState = &state
	e.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	selectionMust(t, err)
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSourceRefPolicyV2, domain.CharacterSelfChronologyPolicyV1, "future_outline_window/作者未公开的未来情节")
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	selectionMust(t, err)
	var memories []domain.CharacterAgentMemory
	for i := range e.Observations {
		o := &e.Observations[i]
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, GenerationID: origin.GenerationID, State: "projected", Facts: []domain.CharacterAgentMemoryFact{
			newCharacterMemoryFact(1, "known", o.Character+"本人已知的日常经验", "sha256:"+strings.Repeat("c", 64), false),
			newCharacterMemoryFact(1, "known", "尚未向模型展示的私有历史"+o.Character, "sha256:"+strings.Repeat("d", 64), false),
		}})
		selectionMust(t, err)
		memories = append(memories, memory)
		o.MemoryRoot, o.StimulusDigest = memory.MemoryRoot, e.Stimulus.Digest
		for _, fact := range memory.Facts {
			if strings.Contains(fact.Text, "本人已知") {
				o.Memory = append(o.Memory, fact)
			}
		}
		o.Sources = append([]string(nil), e.Stimulus.Sources...)
		o.KnownFacts[0].Source = "作者来源含未读R02六十升"
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(state, o.AgentID)
		selectionMust(t, err)
		projectCharacterObservationSourcesV2(o)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		selectionMust(t, err)
		e.Proposals[i].ObservationDigest = o.Digest
		e.Proposals[i], err = domain.FinalizeCharacterDecisionProposal(e.Proposals[i], *o)
		selectionMust(t, err)
		e.Activation.Entries[i].ObservationDigest = o.Digest
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	selectionMust(t, err)
	r := e.Arbitrations[0]
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", e.Stimulus.Digest, e.Activation.Digest
	r.ProposalDigests = nil
	for i, proposal := range e.Proposals {
		r.ProposalDigests = append(r.ProposalDigests, proposal.Digest)
		r.Resolutions[i].ProposalDigest = proposal.Digest
		for _, actor := range state.Actors {
			if actor.AgentID == proposal.AgentID {
				post := actor
				r.Resolutions[i].PostState = &post
			}
		}
	}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, e.Proposals, 1)
	selectionMust(t, err)
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e.MemoryRoots = nil
	for _, o := range e.Observations {
		e.MemoryRoots = append(e.MemoryRoots, o.MemoryRoot)
	}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	selectionMust(t, err)
	input, err := domain.FinalizeCharacterActivationInputSet(domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus, Activation: e.Activation, Observations: e.Observations, Memories: memories})
	selectionMust(t, err)
	origin.Evidence, origin.InputSetDigest = e, input.Digest
	origin, err = domain.FinalizeCharacterActivationCycle(origin)
	selectionMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	selectionMust(t, err)
	step, prefix, err := domain.VerifyCharacterActivationStep(prefix, input, origin)
	selectionMust(t, err)
	prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, testutil.CycleReadiness(t, origin, "continue"))
	selectionMust(t, err)
	return prefix, step
}

func verifiedInputContinuation(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, step domain.VerifiedCharacterActivationStep) (domain.VerifiedCharacterActivationPrefix, domain.VerifiedCharacterActivationStep) {
	t.Helper()
	input, err := buildNextCharacterActivationInputsFromStep(step.Input(), step, prefix.Session())
	selectionMust(t, err)
	ledgers := map[string]domain.CharacterWorkContinuationLedgerV1{}
	for _, proposal := range step.EffectiveProposals() {
		ledger, ok := prefix.ContinuationLedger(proposal.AgentID)
		if !ok {
			t.Fatal("verified source lost its original grant")
		}
		ledgers[proposal.AgentID] = ledger
	}
	selection, err := SelectCharacterWorkContinuations(input, ledgers, prefix.ContinuationBoundaries()...)
	selectionMust(t, err)
	if len(selection.FreshAgentIDs) != 0 || len(selection.Continuations) != 2 {
		t.Fatalf("ordinary verified feedback caused fresh model calls: %+v", selection)
	}
	prior := step.Cycle()
	r := selectionCopy(t, prior.Evidence.Arbitrations[0])
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}
	for i := range r.Resolutions {
		for _, actor := range input.Stimulus.PhysicalState.Actors {
			if actor.AgentID == r.Resolutions[i].AgentID {
				post := actor
				r.Resolutions[i].PostState = &post
			}
		}
		r.Resolutions[i].SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: &start, EndDay: &end}}
	}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, step.EffectiveProposals(), 1)
	selectionMust(t, err)
	cycle := domain.CharacterActivationCycle{Version: domain.CharacterActivationCycleV2Version, GenerationID: prior.GenerationID, Chapter: 1, Index: prior.Index + 1, PreviousDigest: step.GlobalRoot(), ChapterContextDigest: prior.ChapterContextDigest, InputSetDigest: input.Digest, WorkContinuations: selection.Continuations,
		Evidence: domain.CharacterAgentEvidenceBundle{Version: domain.CharacterContinuationEvidenceV2Version, GenerationID: prior.GenerationID, Chapter: 1, Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation, Observations: input.Observations, Arbitrations: []domain.WorldArbitrationReceipt{r}, ProtocolDigest: prior.Evidence.ProtocolDigest}}
	for _, observation := range input.Observations {
		cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, observation.MemoryRoot)
	}
	cycle, err = domain.FinalizeCharacterContinuationCycleV2(prefix, input, cycle)
	selectionMust(t, err)
	nextStep, prefix, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	selectionMust(t, err)
	prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, testutil.CycleReadiness(t, cycle, "continue"))
	selectionMust(t, err)
	return prefix, nextStep
}

func TestVerifiedActivationNextInputRetainsMultiOwnerContinuationWithoutReplayingPrivateHistory(t *testing.T) {
	prefix, first := verifiedInputOrigin(t)
	// The source-aware adapter also preserves ordinary fully verified cycles.
	legacy, err := buildNextCharacterActivationInputs(first.Input(), first.Cycle(), prefix.Session())
	selectionMust(t, err)
	resolved, err := buildNextCharacterActivationInputsFromStep(first.Input(), first, prefix.Session())
	selectionMust(t, err)
	if !reflect.DeepEqual(legacy, resolved) {
		t.Fatal("source-aware path changed the historical fresh-cycle projection")
	}
	original, _ := json.Marshal(first.EffectiveProposals())
	prefix, second := verifiedInputContinuation(t, prefix, first)
	if len(second.Cycle().Evidence.Proposals) != 0 || len(second.EffectiveProposals()) != 2 {
		t.Fatal("fixture does not exercise source-only continued choices")
	}
	if _, err := buildNextCharacterActivationInputs(second.Input(), second.Cycle(), prefix.Session()); err == nil {
		t.Fatal("legacy source-less cycle validator was weakened")
	}
	before, _ := json.Marshal([]any{second.Input(), second.Cycle(), second.AfterState()})
	next, err := buildNextCharacterActivationInputsFromStep(second.Input(), second, prefix.Session())
	selectionMust(t, err)
	if next.Stimulus.StoryClock.CurrentDay != second.Cycle().EndDay || next.Stimulus.SelfEvaluationContext.Cycle != 3 {
		t.Fatal("verified next input lost the actual clock/evaluation")
	}
	for _, observation := range next.Observations {
		raw, _ := json.Marshal(observation)
		for _, secret := range []string{"future_outline_window", "未读R02六十升", "尚未向模型展示", "本章开局只发生一次"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("next input leaked or re-fired %s", secret)
			}
		}
		for _, other := range next.Observations {
			if other.AgentID != observation.AgentID && strings.Contains(string(raw), other.Character+"本人已知的日常经验") {
				t.Fatal("another owner's visible private memory crossed the actor boundary")
			}
		}
		if len(observation.TaskProgress) != 1 || observation.TaskProgress[0].Completed != 2 || len(observation.SelfExperiences) != 2 {
			t.Fatal("continuing owner's actual result was omitted")
		}
		if len(observation.Memory) != 3 { // Visible initial fact + one fact per real cycle.
			t.Fatalf("owner feedback missing or duplicated: %d", len(observation.Memory))
		}
		for _, fact := range observation.Memory {
			if fact.SourceDigest == second.Cycle().Evidence.Arbitrations[0].Digest && strings.Count(fact.Text, "本人经历（") != 1 {
				t.Fatal("current private feedback repeated a prior-cycle execution")
			}
		}
	}
	// A third verified continuation demonstrates that newly materialized normal
	// feedback does not force an otherwise unnecessary fresh actor request.
	_, third := verifiedInputContinuation(t, prefix, second)
	if len(third.EffectiveProposals()) != 2 || len(third.Cycle().Evidence.Proposals) != 0 {
		t.Fatal("source-only continued intent was replaced by synthetic fresh proposals")
	}
	after, _ := json.Marshal([]any{second.Input(), second.Cycle(), second.AfterState()})
	unchanged, _ := json.Marshal(first.EffectiveProposals())
	if string(before) != string(after) || string(original) != string(unchanged) {
		t.Fatal("next-input derivation mutated a verified authority/source")
	}
}

func TestVerifiedActivationNextInputRejectsBadgesForeignInputAndMemory(t *testing.T) {
	prefix, step := verifiedInputOrigin(t)
	for _, name := range []string{"zero", "json_badge", "input", "session", "memory"} {
		t.Run(name, func(t *testing.T) {
			current, source, session := step.Input(), step, prefix.Session()
			switch name {
			case "zero":
				source = domain.VerifiedCharacterActivationStep{}
			case "json_badge":
				raw, _ := json.Marshal(step)
				source = domain.VerifiedCharacterActivationStep{}
				selectionMust(t, json.Unmarshal(raw, &source))
			case "input":
				current.Observations[0].KnownFacts[0].Text = "另一个合法但不同的冻结来源"
				current = selectionFinalizeInput(t, current)
			case "session":
				session.CurrentDay++
			case "memory":
				memories := current.Memories
				memories[0].Facts[0].Text = "未授权替代私有记忆"
				if _, err := projectCharacterActivationMemoriesFromStep(memories, source); err == nil {
					t.Fatal("foreign memory entered a verified source projection")
				}
				return
			}
			if _, err := buildNextCharacterActivationInputsFromStep(current, source, session); err == nil {
				t.Fatal("foreign/serialized authority entered next-cycle projection")
			}
		})
	}
}
