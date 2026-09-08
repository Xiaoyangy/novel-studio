package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func completionViewState(t *testing.T, later int, distinct bool, actionLength int) WorldPhysicalStateV2 {
	t.Helper()
	f, _ := newSelfChronologyFixture(t)
	state := continuationCloneV1(*f.stimulus.PhysicalState)
	a := &state.Actors[0]
	first := selfChronologyTestFact(a.AgentID, 1, 1, "original-inspection", "completed", selfTestDay(0), selfTestDay(35))
	first.Action, first.ProgressTarget = "本班规定检查", physicalTestNumber(35)
	first.Evaluation.EvaluatedAtDay = *first.EndDay
	first.ID = CharacterSelfExperienceIDV2(a.AgentID, first)
	a.SelfExperiences = []CharacterSelfExperienceV2{first}
	for i := 1; i <= later; i++ {
		id, status := "later-work", "in_progress"
		if distinct {
			id, status = fmt.Sprintf("later-%02d", i), "completed"
		}
		e := selfChronologyTestFact(a.AgentID, i, 1, id, status, selfTestDay(float64(34+i)), selfTestDay(float64(35+i)))
		e.Chapter = 2 + (i-1)/64
		e.Evaluation.Cycle = (i-1)%64 + 1
		e.Action = "后续工作"
		if actionLength > 0 {
			e.Action = strings.Repeat("工", actionLength)
		}
		e.Evaluation.EvaluatedAtDay = *e.EndDay
		e.ID = CharacterSelfExperienceIDV2(a.AgentID, e)
		a.SelfExperiences = append(a.SelfExperiences, e)
	}
	var err error
	a.TaskProgress, err = deriveSelfChronologyProgressV1(*a)
	if err != nil {
		t.Fatal(err)
	}
	state, err = FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSelfCompletionViewRetainsFinishedTaskAfterSlidingWindowAndAcrossChapters(t *testing.T) {
	state := completionViewState(t, 20, false, 0)
	before, _ := json.Marshal(state)
	legacyE, legacyP, err := BuildCharacterSelfObservationV2(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range legacyE {
		if e.TaskID == "original-inspection" {
			t.Fatal("fixture did not evict the old completed experience")
		}
	}
	e, p, err := BuildCharacterSelfObservationForSourcesV2(state, "ca_a", []string{CharacterSelfCompletionViewPolicyV1})
	if err != nil {
		t.Fatal(err)
	}
	var completion CharacterTaskProgressV2
	for _, task := range p {
		if task.TaskID == "original-inspection" {
			completion = task
		}
	}
	if completion.State != "completed" || completion.Completed != 35 || completion.Target == nil || *completion.Target != 35 || len(e) >= len(state.Actors[0].SelfExperiences) {
		t.Fatal("bounded current task summaries lost completion or copied full history")
	}
	if completion.SourceExperienceID != state.Actors[0].SelfExperiences[0].ID {
		t.Fatal("cross-chapter completion lost its original full-state source")
	}
	for _, source := range e {
		if source.ID == completion.SourceExperienceID {
			t.Fatal("old complete source was needlessly repeated outside the recent window")
		}
	}
	for _, sources := range [][]string{nil, {CharacterWorkContinuationHistoryPolicyV1}} {
		oldE, oldP, err := BuildCharacterSelfObservationForSourcesV2(state, "ca_a", sources)
		if err != nil || !samePhysicalValueV2(oldE, legacyE) || !samePhysicalValueV2(oldP, legacyP) {
			t.Fatal("unmarked historical selection changed")
		}
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("selector mutated canonical owner state")
	}
	p[0].Action = "changed"
	e[0].Action = "changed"
	for _, task := range p {
		if task.Target != nil {
			*task.Target = 999
		}
	}
	after, _ = json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("returned view aliases mutable source history")
	}
}

func TestSelfCompletionViewBudgetPrioritizesAnchorsAndFailsWithoutPartialState(t *testing.T) {
	state := completionViewState(t, 20, false, 256)
	// Legal but verbose resource references make the optional recent tail
	// exceed 16KB while the two required task/source anchors still fit.
	var resourceIDs []string
	for i := 100; i < 120; i++ {
		id := fmt.Sprintf("res_%016x", i)
		resourceIDs = append(resourceIDs, id)
		state.Resources = append(state.Resources, WorldResourceBalanceV2{ResourceID: id, Name: "本人工作资源"})
	}
	for i := range state.Actors[0].SelfExperiences {
		e := &state.Actors[0].SelfExperiences[i]
		e.ResourceIDs = append([]string(nil), resourceIDs...)
		e.ID = CharacterSelfExperienceIDV2(state.Actors[0].AgentID, *e)
	}
	var err error
	state.Actors[0].TaskProgress, err = deriveSelfChronologyProgressV1(state.Actors[0])
	if err != nil {
		t.Fatal(err)
	}
	e, p, err := BuildCharacterSelfObservationForSourcesV2(state, "ca_a", []string{CharacterSelfCompletionViewPolicyV1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 || len(e) >= 10 {
		t.Fatal("optional recent history was not reduced for mandatory completion anchors")
	}
	if err := validateSelfObservationBudgetV2(e, p); err != nil {
		t.Fatal(err)
	}
	for _, state := range []WorldPhysicalStateV2{completionViewState(t, 80, true, 0), completionViewState(t, 30, true, 256)} {
		before, _ := json.Marshal(state)
		e, p, err := BuildCharacterSelfObservationForSourcesV2(state, "ca_a", []string{CharacterSelfCompletionViewPolicyV1})
		if err == nil || !strings.Contains(err.Error(), "no completed or unfinished task was dropped") || e != nil || p != nil {
			t.Fatalf("overflow returned a truncated mandatory view: %v", err)
		}
		after, _ := json.Marshal(state)
		if string(before) != string(after) {
			t.Fatal("budget rejection changed full source")
		}
	}
}

func TestSelfCompletionViewConcurrentCopiesRemainOwnerLocal(t *testing.T) {
	state := completionViewState(t, 12, false, 0)
	secret := selfChronologyTestFact(state.Actors[1].AgentID, 1, 1, "foreign-task", "completed", selfTestDay(0), selfTestDay(1))
	secret.Action = "FOREIGN_OWNER_PRIVATE_COMPLETION"
	secret.ID = CharacterSelfExperienceIDV2(state.Actors[1].AgentID, secret)
	state.Actors[1].SelfExperiences = []CharacterSelfExperienceV2{secret}
	var err error
	state.Actors[1].TaskProgress, err = deriveSelfChronologyProgressV1(state.Actors[1])
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, p, err := BuildCharacterSelfObservationForSourcesV2(state, "ca_a", []string{CharacterSelfCompletionViewPolicyV1})
			if err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal([]any{e, p})
			if strings.Contains(string(raw), secret.Action) {
				t.Error("foreign completion leaked")
			}
			e[0].Action, p[0].Action = "local edit", "local edit"
		}()
	}
	wg.Wait()
}

func TestSelfCompletionViewExactSourceBindingRejectsTampering(t *testing.T) {
	f, _ := newSelfChronologyFixture(t)
	state := completionViewState(t, 12, false, 0)
	f.stimulus.Chapter = 3
	f.stimulus.PhysicalState = &state
	f.stimulus.StoryClock.CurrentDay = *selfTestDay(47)
	clock, err := FinalizeStoryClockContext(*f.stimulus.StoryClock)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.StoryClock = &clock
	f.stimulus.Sources = []string{CharacterSelfExperiencePolicyV2, CharacterSelfChronologyPolicyV1, CharacterWorkContinuationPolicyV1, CharacterActivationCyclePolicyV3, CharacterWorkContinuationHistoryPolicyV1, CharacterSelfCompletionViewPolicyV1}
	session, err := NewCharacterActivationSession(f.stimulus.GenerationID, 3, "sha256:"+strings.Repeat("a", 64), state, f.stimulus.StoryClock.CurrentDay, 4)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.SelfEvaluationContext, err = NewCharacterSelfEvaluationContextV1(session)
	if err != nil {
		t.Fatal(err)
	}
	token, err := CharacterActivationCycleSourceToken(session.GenerationID, 3, 1, session.ChapterContextDigest, "")
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.Sources = append(f.stimulus.Sources, token)
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	o := f.observations[0]
	o.Chapter, o.StimulusDigest, o.Sources = 3, f.stimulus.Digest, append([]string(nil), f.stimulus.Sources...)
	o.CycleContext, err = NewCharacterObservationCycleContext(session)
	if err != nil {
		t.Fatal(err)
	}
	o.ResourceViews, err = BuildCharacterResourceViewsV2(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o.SelfExperiences, o.TaskProgress, err = BuildCharacterSelfObservationForSourcesV2(state, o.AgentID, o.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCharacterResourceViewsAgainstStimulusV2(f.stimulus, o); err != nil {
		t.Fatal(err)
	}
	completeIndex := -1
	for i, task := range o.TaskProgress {
		if task.TaskID == "original-inspection" {
			completeIndex = i
		}
	}
	if completeIndex < 0 {
		t.Fatal("missing completed row")
	}
	sourceID := o.TaskProgress[completeIndex].SourceExperienceID
	if _, ok := o.AllowedFactIDs()[sourceID]; !ok {
		t.Fatal("verified compact completion source is not referenceable")
	}
	for _, source := range o.SelfExperiences {
		if source.ID == sourceID {
			t.Fatal("fixture did not exercise host-bound non-inline completion")
		}
	}
	legacy := continuationCloneV1(o)
	var legacySources []string
	for _, s := range legacy.Sources {
		if s != CharacterSelfCompletionViewPolicyV1 {
			legacySources = append(legacySources, s)
		}
	}
	legacy.Sources = legacySources
	if _, ok := legacy.AllowedFactIDs()[sourceID]; ok {
		t.Fatal("new completion capability leaked into historical source policy")
	}
	for _, kind := range []string{"amount", "drop completion", "foreign source", "wrong existing source", "owner", "forged completion", "remove marker"} {
		t.Run(kind, func(t *testing.T) {
			bad := continuationCloneV1(o)
			switch kind {
			case "amount":
				bad.TaskProgress[completeIndex].Completed++
			case "drop completion":
				for i, p := range bad.TaskProgress {
					if p.TaskID == "original-inspection" {
						bad.TaskProgress = append(bad.TaskProgress[:i], bad.TaskProgress[i+1:]...)
						break
					}
				}
			case "foreign source":
				bad.TaskProgress[completeIndex].SourceExperienceID = "self_" + strings.Repeat("f", 64)
			case "wrong existing source":
				bad.TaskProgress[completeIndex].SourceExperienceID = bad.TaskProgress[0].SourceExperienceID
			case "owner":
				bad.AgentID, bad.Character = state.Actors[1].AgentID, state.Actors[1].Character
			case "forged completion":
				bad.TaskProgress[0].State = "completed"
			case "remove marker":
				var sources []string
				for _, s := range bad.Sources {
					if s != CharacterSelfCompletionViewPolicyV1 {
						sources = append(sources, s)
					}
				}
				bad.Sources = sources
			}
			if err := ValidateCharacterResourceViewsAgainstStimulusV2(f.stimulus, bad); err == nil {
				t.Fatal("changed completion view accepted")
			}
		})
	}
}
