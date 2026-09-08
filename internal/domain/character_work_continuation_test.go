package domain_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func continuationCopy[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}
func continuationNumber(v float64) *float64 { return &v }
func continuationMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func continuationOriginFixture(t *testing.T, maxMinutes *float64, edit func(*domain.CharacterDecisionProposal)) domain.CharacterActivationCycle {
	t.Helper()
	c := testutil.CharacterCycle(t, 1, "", nil, 0)
	e := c.Evidence
	physical, err := domain.PrepareCharacterSelfExperienceStateV2(*e.Stimulus.PhysicalState)
	continuationMust(t, err)
	e.Stimulus.PhysicalState = &physical
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1)
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	continuationMust(t, err)
	o := e.Observations[0]
	o.StimulusDigest = e.Stimulus.Digest
	o.Sources = []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: 1, ChapterContextDigest: c.ChapterContextDigest, CurrentDay: 0}
	o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
	continuationMust(t, err)
	memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, State: "projected", GenerationID: o.GenerationID})
	continuationMust(t, err)
	o.MemoryRoot = memory.MemoryRoot
	o, err = domain.FinalizeCharacterObservationPacket(o)
	continuationMust(t, err)
	p := e.Proposals[0]
	p.ObservationDigest = o.Digest
	p.SelfTasks = []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "常规检查", ProgressTarget: continuationNumber(5), ProgressUnit: "minute", KnowledgeRefs: p.KnowledgeRefs}}
	p.WorkContinuations = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: maxMinutes == nil, MaxEffectiveMinutes: maxMinutes}}
	if edit != nil {
		edit(&p)
	}
	p, err = domain.FinalizeCharacterDecisionProposal(p, o)
	continuationMust(t, err)
	e.Activation.Entries[0].ObservationDigest = o.Digest
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	continuationMust(t, err)
	r := e.Arbitrations[0]
	r.Digest = ""
	r.StimulusDigest = e.Stimulus.Digest
	r.ActivationDigest = e.Activation.Digest
	r.ProposalDigests = []string{p.Digest}
	r.ProtagonistProjection = domain.ProtagonistDecisionProjection{}
	r.ResourceSettlements = nil
	r.Resolutions[0].ProposalDigest = p.Digest
	r.Resolutions[0].Decision = p.Decision
	r.Resolutions[0].IntendedAction = p.IntendedAction
	r.Resolutions[0].CompletionState = "in_progress"
	r.Resolutions[0].PostState = &physical.Actors[0]
	r.Resolutions[0].SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: continuationNumber(0), EndDay: continuationNumber(1.0 / 1440)}}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, []domain.CharacterDecisionProposal{p}, 1)
	continuationMust(t, err)
	e.Observations = []domain.CharacterObservationPacket{o}
	e.Proposals = []domain.CharacterDecisionProposal{p}
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e.MemoryRoots = []string{o.MemoryRoot}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	continuationMust(t, err)
	c.Evidence = e
	c, err = domain.FinalizeCharacterActivationCycle(c)
	continuationMust(t, err)
	return c
}

func continuationFixture(t *testing.T, maxMinutes *float64, edit func(*domain.CharacterDecisionProposal)) (domain.CharacterWorkContinuationLedgerV1, domain.CharacterActivationInputSet) {
	t.Helper()
	c := continuationOriginFixture(t, maxMinutes, edit)
	ledger, err := domain.NewCharacterWorkContinuationLedgerV1(c, c.Evidence.Proposals[0].Digest, "inspection")
	continuationMust(t, err)
	return ledger, continuationNextInput(t, ledger)
}

func continuationNextInput(t *testing.T, l domain.CharacterWorkContinuationLedgerV1) domain.CharacterActivationInputSet {
	t.Helper()
	e := continuationCopy(l.Origin.Evidence)
	p := e.Proposals[len(e.Proposals)-1]
	o := e.Observations[len(e.Observations)-1]
	stimulus, r := e.Stimulus, e.Arbitrations[len(e.Arbitrations)-1]
	proposals := e.Proposals
	previous := l.Origin.Digest
	index := l.Origin.Index + 1
	if len(l.Entries) > 0 {
		last := l.Entries[len(l.Entries)-1]
		stimulus, r = last.Input.Stimulus, last.Arbitration
		proposals = append([]domain.CharacterDecisionProposal{p}, last.FreshPeerProposals...)
		bindings := continuationBoundaries(t, l)
		previous = bindings[len(bindings)-1].GlobalRoot()
		index = last.Continuation.Cycle + 1
	}
	physical, err := domain.ApplyArbitrationPhysicalStateV2(r, stimulus, proposals...)
	continuationMust(t, err)
	stimulus.PhysicalState = &physical
	clock := *stimulus.StoryClock
	clock.CurrentDay = r.StoryTime.EndDay
	clock, err = domain.FinalizeStoryClockContext(clock)
	continuationMust(t, err)
	stimulus.StoryClock = &clock
	token, err := domain.CharacterActivationCycleSourceToken(l.Origin.GenerationID, 1, index, l.Origin.ChapterContextDigest, previous)
	continuationMust(t, err)
	stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	stimulus, err = domain.FinalizeWorldStimulusPacket(stimulus)
	continuationMust(t, err)
	o.StimulusDigest = stimulus.Digest
	o.CurrentGoal = p.CurrentGoal
	o.Pressure = p.Pressure
	o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: index, ChapterContextDigest: l.Origin.ChapterContextDigest, PreviousCycleDigest: previous, CurrentDay: clock.CurrentDay}
	o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
	continuationMust(t, err)
	o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, o.AgentID)
	continuationMust(t, err)
	memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, State: "projected", GenerationID: o.GenerationID})
	continuationMust(t, err)
	o.MemoryRoot = memory.MemoryRoot
	o.Memory = nil
	o, err = domain.FinalizeCharacterObservationPacket(o)
	continuationMust(t, err)
	a := e.Activation
	a.Entries[0].ObservationDigest = o.Digest
	a, err = domain.FinalizeCharacterAgentActivation(a)
	continuationMust(t, err)
	input, err := domain.FinalizeCharacterActivationInputSet(domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: stimulus, Activation: a, Observations: []domain.CharacterObservationPacket{o}, Memories: []domain.CharacterAgentMemory{memory}})
	continuationMust(t, err)
	return input
}

func continuationArbitration(t *testing.T, l domain.CharacterWorkContinuationLedgerV1, in domain.CharacterActivationInputSet, minutes float64, status string) domain.WorldArbitrationReceipt {
	t.Helper()
	p := l.Origin.Evidence.Proposals[0]
	r := continuationCopy(l.Origin.Evidence.Arbitrations[0])
	r.Digest = ""
	r.StimulusDigest = in.Stimulus.Digest
	r.ActivationDigest = in.Activation.Digest
	start := in.Stimulus.StoryClock.CurrentDay
	end := start + minutes/1440
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}
	r.Resolutions[0].PostState = &in.Stimulus.PhysicalState.Actors[0]
	x := domain.CharacterSelfExecutionV2{TaskID: l.TaskID, Status: status, StartDay: &start, EndDay: &end}
	r.Resolutions[0].Outcome = "success"
	r.Resolutions[0].CompletionState = "in_progress"
	if status == "blocked" {
		x.StartDay = nil
		x.EndDay = nil
		r.Resolutions[0].Outcome = "blocked"
		r.Resolutions[0].CompletionState = "blocked"
	}
	if status == "completed" {
		r.Resolutions[0].CompletionState = "completed"
	}
	r.Resolutions[0].SelfExecutions = []domain.CharacterSelfExecutionV2{x}
	final, err := domain.FinalizeWorldArbitrationReceipt(r, in.Stimulus, in.Activation, []domain.CharacterDecisionProposal{p}, 1)
	continuationMust(t, err)
	return final
}

func TestWorkContinuationAuthenticatesMultipleSegmentsWithoutResigningOrigin(t *testing.T) {
	l, in := continuationFixture(t, nil, nil)
	original, _ := json.Marshal(l.Origin)
	for _, minutes := range []float64{1, 2} {
		eligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in, continuationBoundaries(t, l)...)
		continuationMust(t, err)
		if !eligible.Eligible {
			t.Fatalf("normal accumulated work woke actor: %+v", eligible)
		}
		r := continuationArbitration(t, l, in, minutes, "in_progress")
		old := l
		l, err = domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, continuationBoundaries(t, l))
		continuationMust(t, err)
		if len(old.Entries)+1 != len(l.Entries) {
			t.Fatal("append mutated/replaced old ledger")
		}
		continuationMust(t, domain.ValidateCharacterWorkContinuationLedgerV1(l, continuationBoundaries(t, l)...))
		in = continuationNextInput(t, l)
	}
	eligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in, continuationBoundaries(t, l)...)
	continuationMust(t, err)
	if !eligible.Eligible || math.Abs(eligible.Receipt.EffectiveCompleted-4) > 1e-9 || math.Abs(eligible.Receipt.AuthorizedRemaining-1) > 1e-9 {
		t.Fatalf("free caller totals or lost accumulation: %+v", eligible)
	}
	after, _ := json.Marshal(l.Origin)
	if string(original) != string(after) {
		t.Fatal("original proposal/observation/cycle was re-signed")
	}
	r := continuationArbitration(t, l, in, 1, "completed")
	l, err = domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, continuationBoundaries(t, l))
	continuationMust(t, err)
	result, err := domain.EvaluateCharacterWorkContinuationV1(l, continuationNextInput(t, l), continuationBoundaries(t, l)...)
	continuationMust(t, err)
	if result.Eligible || result.Receipt != nil {
		t.Fatal("completed work received new authority")
	}
	for _, reason := range result.WakeReasons {
		if reason == "task_completed" {
			return
		}
	}
	t.Fatalf("completion did not wake owner: %+v", result)
}

func TestWorkContinuationBudgetAndInterruptionWakeOwner(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		l, in := continuationFixture(t, continuationNumber(2), nil)
		eligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
		continuationMust(t, err)
		if !eligible.Eligible || math.Abs(eligible.Receipt.AuthorizedRemaining-1) > 1e-9 {
			t.Fatal("original cycle did not consume its authorization budget")
		}
		status := "in_progress"
		if blocked {
			status = "blocked"
		}
		r := continuationArbitration(t, l, in, 1, status)
		l, err = domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, continuationBoundaries(t, l))
		continuationMust(t, err)
		result, err := domain.EvaluateCharacterWorkContinuationV1(l, continuationNextInput(t, l), continuationBoundaries(t, l)...)
		continuationMust(t, err)
		if result.Eligible {
			t.Fatal("exhausted/interrupted authorization continued")
		}
	}
}

func TestWorkContinuationRejectsUnauthorizedOrTamperedExecution(t *testing.T) {
	l, in := continuationFixture(t, continuationNumber(2), nil)
	eligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	r := continuationArbitration(t, l, in, 2, "in_progress")
	if _, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, nil); err == nil {
		t.Fatal("overspent continuation authority")
	}
	r = continuationArbitration(t, l, in, 1, "in_progress")
	for _, edit := range []func(*domain.CharacterWorkContinuationReceiptV1){func(r *domain.CharacterWorkContinuationReceiptV1) { r.AuthorizedRemaining = 100 }, func(r *domain.CharacterWorkContinuationReceiptV1) {
		r.OriginProposalDigest = "sha256:" + strings.Repeat("f", 64)
	}, func(r *domain.CharacterWorkContinuationReceiptV1) {
		r.InformationRoot = "sha256:" + strings.Repeat("f", 64)
	}} {
		bad := *eligible.Receipt
		edit(&bad)
		if _, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, bad, r, nil); err == nil {
			t.Fatal("tampered host authorization accepted")
		}
	}
	withEntry, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, nil)
	continuationMust(t, err)
	if _, err := domain.AppendCharacterWorkContinuationExecutionV1(withEntry, in, *eligible.Receipt, r, continuationBoundaries(t, withEntry)); err == nil {
		t.Fatal("same execution credited twice")
	}
	bad := continuationCopy(withEntry)
	bad.Entries[0].Executions[0].EndDay = continuationNumber(100)
	bad.Digest, err = domain.ComputeCharacterWorkContinuationLedgerV1Digest(bad)
	continuationMust(t, err)
	if domain.ValidateCharacterWorkContinuationLedgerV1(bad) == nil {
		t.Fatal("caller supplied execution total was trusted")
	}
	if !reflect.DeepEqual(l.Origin, withEntry.Origin) {
		t.Fatal("append changed original artifacts")
	}
	foreign := continuationCopy(in)
	foreign.Observations[0].CycleContext.PreviousCycleDigest = "sha256:" + strings.Repeat("f", 64)
	token, err := domain.CharacterActivationCycleSourceToken(foreign.Stimulus.GenerationID, 1, foreign.Observations[0].CycleContext.Index, l.Origin.ChapterContextDigest, foreign.Observations[0].CycleContext.PreviousCycleDigest)
	continuationMust(t, err)
	for i, s := range foreign.Stimulus.Sources {
		if strings.HasPrefix(s, domain.CharacterActivationCycleSourcePrefix) {
			foreign.Stimulus.Sources[i] = token
		}
	}
	foreign = continuationRefinalizeInput(t, foreign)
	if _, err := domain.EvaluateCharacterWorkContinuationV1(l, foreign); err == nil {
		t.Fatal("re-signed foreign predecessor substituted for ordered ledger")
	}
}

func TestWorkContinuationAuthorizationIsExplicitAndComplexChoicesRemainLegal(t *testing.T) {
	l, _ := continuationFixture(t, nil, nil)
	p := l.Origin.Evidence.Proposals[0]
	o := l.Origin.Evidence.Observations[0]
	for name, edit := range map[string]func(*domain.CharacterDecisionProposal, *domain.CharacterObservationPacket){
		"missing mode": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.WorkContinuations[0].UntilTarget = false
		},
		"both modes": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.WorkContinuations[0].MaxEffectiveMinutes = continuationNumber(1)
		},
		"nan": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.WorkContinuations[0].UntilTarget = false
			p.WorkContinuations[0].MaxEffectiveMinutes = continuationNumber(math.NaN())
		},
		"negative": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.WorkContinuations[0].UntilTarget = false
			p.WorkContinuations[0].MaxEffectiveMinutes = continuationNumber(-1)
		},
		"unbounded": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.SelfTasks[0].ProgressTarget = nil
		},
		"carry": func(p *domain.CharacterDecisionProposal, _ *domain.CharacterObservationPacket) {
			p.SelfTasks[0].Kind = "carry"
		},
		"no policy": func(_ *domain.CharacterDecisionProposal, o *domain.CharacterObservationPacket) { o.Sources = nil },
	} {
		t.Run(name, func(t *testing.T) {
			a, b := continuationCopy(p), continuationCopy(o)
			edit(&a, &b)
			if domain.ValidateCharacterWorkContinuationIntentV1(a, b) == nil {
				t.Fatal("unauthorized continuation grant accepted")
			}
		})
	}
	// A finite target without a grant remains a valid ordinary task, never an
	// inferred permission. Rebuild its ordinary evidence so rejection is not a
	// stale digest failure.
	old := testutil.CharacterCycle(t, 1, "", nil, 0)
	raw, _ := json.Marshal(old.Evidence.Proposals[0])
	if strings.Contains(string(raw), "work_continuations") {
		t.Fatal("legacy JSON changed")
	}
	if _, err := domain.NewCharacterWorkContinuationLedgerV1(old, old.Evidence.Proposals[0].Digest, "inspection"); err == nil {
		t.Fatal("legacy task got implicit continuation")
	}
	noGrant := continuationOriginFixture(t, nil, func(p *domain.CharacterDecisionProposal) { p.WorkContinuations = nil })
	if _, err := domain.NewCharacterWorkContinuationLedgerV1(noGrant, noGrant.Evidence.Proposals[0].Digest, "inspection"); err == nil || !strings.Contains(err.Error(), "explicit continuation authority") {
		t.Fatalf("finite total requirement became permission: %v", err)
	}
	complex, input := continuationFixture(t, nil, func(p *domain.CharacterDecisionProposal) {
		p.ResourceReads = []domain.ResourceReadRequestV2{{ResourceID: "res_0000000000000001"}}
	})
	result, err := domain.EvaluateCharacterWorkContinuationV1(complex, input)
	continuationMust(t, err)
	if result.Eligible || len(result.WakeReasons) == 0 {
		t.Fatal("complex proposal was promoted to autonomous continuation")
	}
}

func continuationRefinalizeInput(t *testing.T, input domain.CharacterActivationInputSet) domain.CharacterActivationInputSet {
	t.Helper()
	var err error
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.StimulusDigest = input.Stimulus.Digest
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(*input.Stimulus.PhysicalState, o.AgentID)
		continuationMust(t, err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		continuationMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID && input.Activation.Entries[j].State == domain.CharacterAgentActive {
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func TestWorkContinuationWakesOnNewKnowledgePermissionsAndAbnormality(t *testing.T) {
	l, input := continuationFixture(t, nil, nil)
	for name, edit := range map[string]func(*domain.CharacterActivationInputSet){
		"information": func(in *domain.CharacterActivationInputSet) {
			in.Observations[0].KnownFacts = append(in.Observations[0].KnownFacts, domain.CharacterAgentFact{ID: "new-report", Kind: "known", Text: "本人新收到的情况"})
		},
		"abnormality": func(in *domain.CharacterActivationInputSet) {
			in.Observations[0].PerceivedEvents = append(in.Observations[0].PerceivedEvents, domain.CharacterAgentFact{ID: "new-abnormality", Kind: "perceived", Text: "本人察觉异常声音"})
		},
		"permission": func(in *domain.CharacterActivationInputSet) {
			in.Stimulus.PhysicalState.Actors[0].Resources[0].Access = "none"
		},
		"perceived amount": func(in *domain.CharacterActivationInputSet) {
			in.Stimulus.PhysicalState.Actors[0].Resources[0].Perception.Amount = continuationNumber(11)
		},
		"location": func(in *domain.CharacterActivationInputSet) {
			in.Stimulus.PhysicalState.Actors[0].Location = "岸边"
			in.Observations[0].Location = "岸边"
		},
		"commitment": func(in *domain.CharacterActivationInputSet) {
			in.Observations[0].Commitments = append(in.Observations[0].Commitments, "新到期的本人承诺")
		},
		"deadline": func(in *domain.CharacterActivationInputSet) {
			clock := *in.Stimulus.StoryClock
			clock.TimeContractCoreDigest = "sha256:" + strings.Repeat("f", 64)
			var err error
			clock, err = domain.FinalizeStoryClockContext(clock)
			continuationMust(t, err)
			in.Stimulus.StoryClock = &clock
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := continuationCopy(input)
			edit(&changed)
			changed = continuationRefinalizeInput(t, changed)
			result, err := domain.EvaluateCharacterWorkContinuationV1(l, changed)
			continuationMust(t, err)
			if result.Eligible || len(result.WakeReasons) == 0 || result.Receipt != nil {
				t.Fatalf("new decision event did not wake: %+v", result)
			}
		})
	}
}

func TestWorkContinuationDoesNotExecuteSleepingOwnerOrChangedSource(t *testing.T) {
	l, in := continuationFixture(t, nil, nil)
	originalEligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	in.Activation.Entries[0].State = domain.CharacterAgentSleeping
	in.Activation.Entries[0].ObservationDigest = ""
	in = continuationRefinalizeInput(t, in)
	eligible, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	if eligible.Eligible || eligible.Receipt != nil {
		t.Fatal("sleeping owner was granted execution authority")
	}
	r := continuationArbitration(t, l, in, 1, "in_progress")
	if _, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, *originalEligible.Receipt, r, nil); err == nil {
		t.Fatal("sleeping actor executed under a fake active identity")
	}
	l, in = continuationFixture(t, nil, nil)
	eligible, err = domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	r = continuationArbitration(t, l, in, 1, "in_progress")
	r.Resolutions[0].IntendedAction = "篡改原意图"
	r.Digest, err = domain.ComputeWorldArbitrationReceiptDigest(r)
	continuationMust(t, err)
	if _, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, r, nil); err == nil {
		t.Fatal("re-signed intent rewrite accepted")
	}
	bad := *eligible.Receipt
	bad.AuthorizedRemaining = 100
	bad.Digest, err = domain.ComputeCharacterWorkContinuationReceiptV1Digest(bad)
	continuationMust(t, err)
	if domain.ValidateCharacterWorkContinuationReceiptV1(bad, l, in) == nil {
		t.Fatal("self-signed allowance inflated authority")
	}
	moved := continuationArbitration(t, l, in, 1, "in_progress")
	moved.Resolutions[0].PostState.Location = "岸边"
	moved, err = domain.FinalizeWorldArbitrationReceipt(moved, in.Stimulus, in.Activation, l.Origin.Evidence.Proposals, 1)
	continuationMust(t, err)
	if _, err := domain.AppendCharacterWorkContinuationExecutionV1(l, in, *eligible.Receipt, moved, nil); err == nil {
		t.Fatal("continuation authorized a new relocation")
	}
}

func TestWorkContinuationSuppressesOnlyVerifiedNormalFeedback(t *testing.T) {
	l, in := continuationFixture(t, nil, nil)
	e := l.Origin.Evidence
	r := e.Arbitrations[0]
	p := e.Proposals[0]
	physical, err := domain.ApplyArbitrationPhysicalStateV2(r, e.Stimulus, e.Proposals...)
	continuationMust(t, err)
	text, err := domain.CharacterActivationPrivateOutcome(p, r.Resolutions[0], physical, r)
	continuationMust(t, err)
	in.Memories[0].Facts = []domain.CharacterAgentMemoryFact{{ID: "mem_0000000000000001", Chapter: 1, Kind: "projected_decision", Text: text, SourceDigest: r.Digest}}
	in.Memories[0], err = domain.FinalizeCharacterAgentMemory(in.Memories[0])
	continuationMust(t, err)
	in.Observations[0].Memory = append([]domain.CharacterAgentMemoryFact(nil), in.Memories[0].Facts...)
	in.Observations[0].MemoryRoot = in.Memories[0].MemoryRoot
	in = continuationRefinalizeInput(t, in)
	result, err := domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	if !result.Eligible {
		t.Fatalf("verified normal own work caused fresh decision: %+v", result)
	}
	in.Memories[0].Facts[0].Text = "不同的新信息，不能凭同kind或旧source跳过"
	in.Memories[0], err = domain.FinalizeCharacterAgentMemory(in.Memories[0])
	continuationMust(t, err)
	in.Observations[0].Memory = append([]domain.CharacterAgentMemoryFact(nil), in.Memories[0].Facts...)
	in.Observations[0].MemoryRoot = in.Memories[0].MemoryRoot
	in = continuationRefinalizeInput(t, in)
	result, err = domain.EvaluateCharacterWorkContinuationV1(l, in)
	continuationMust(t, err)
	if result.Eligible {
		t.Fatal("arbitrary projected memory was dismissed as routine progress")
	}
}
