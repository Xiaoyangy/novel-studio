package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func newSelfChronologyFixture(t *testing.T) (physicalProtocolFixture, CharacterActivationSession) {
	t.Helper()
	f := newSelfExperienceFixture(t)
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{
		{TaskID: "inspection", Kind: "work", Action: "完成本人规定检查", ProgressTarget: physicalTestNumber(3), ProgressUnit: "minute", KnowledgeRefs: []string{"known-ca_a"}},
		{TaskID: "later-note", Kind: "work", Action: "另记本人检查经过", ProgressUnit: "minute", KnowledgeRefs: []string{"known-ca_a"}},
	}
	f.observations[0].KnownFacts[0].Text = "本项规定检查需要三分钟有效工时，另记经过是另一项工作。"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{
		{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1)},
		{TaskID: "inspection", Status: "completed", StartDay: selfTestDay(2), EndDay: selfTestDay(4)},
		{TaskID: "later-note", Status: "not_started"},
	}
	f.receipt.ResourceSettlements = nil
	prepared, err := PrepareCharacterSelfChronologyStateV1(*f.stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.PhysicalState = &prepared
	session, err := NewCharacterActivationSession(f.stimulus.GenerationID, 1, "sha256:"+strings.Repeat("a", 64), prepared, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	context, err := NewCharacterSelfEvaluationContextV1(session)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.SelfEvaluationContext = context
	token, err := CharacterActivationCycleSourceToken(session.GenerationID, 1, 1, session.ChapterContextDigest, "")
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterSelfChronologyPolicyV1, token)
	clock, err := FinalizeStoryClockContext(StoryClockContext{CurrentDay: 0, DurationDaysMin: 1, DurationDaysMax: 1, TimeContractCoreDigest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.StoryClock = &clock
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterSelfChronologyPolicyV1)
		f.observations[i].CycleContext = &CharacterObservationCycleContext{Version: CharacterObservationCyclePolicy, Index: 1, ChapterContextDigest: session.ChapterContextDigest, CurrentDay: 0}
		actor := prepared.Actors[i]
		f.receipt.Resolutions[i].PostState = &actor
	}
	rebindPhysicalTestStimulus(t, &f)
	return f, session
}

func TestSelfChronologyV1ActualSegmentsAllPermutationsHaveOneReceiptAndProgress(t *testing.T) {
	var first []byte
	for _, permutation := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		f, _ := newSelfChronologyFixture(t)
		original := f.receipt.Resolutions[0].SelfExecutions
		f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{original[permutation[0]], original[permutation[1]], original[permutation[2]]}
		receipt, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatal(err)
		}
		state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
		if err != nil {
			t.Fatal(err)
		}
		actor := state.Actors[0]
		if actor.TaskProgress[0].Completed != 3 || actor.TaskProgress[0].State != "completed" || actor.TaskProgress[0].LatestAttemptStatus != "completed" {
			t.Fatalf("segmented effective work was lost: %+v", actor.TaskProgress)
		}
		for i, fact := range actor.SelfExperiences {
			if fact.Evaluation.Ordinal != i+1 || fact.Evaluation.EvaluatedAtDay != f.receipt.StoryTime.EndDay || fact.Evaluation.ContextDigest != f.stimulus.SelfEvaluationContext.Digest || fact.Evaluation.StimulusDigest != f.stimulus.Digest {
				t.Fatal("host evaluation is not bound to the exact actual input/clock/ordinal")
			}
		}
		last := actor.SelfExperiences[2]
		if last.Status != "not_started" || last.StartDay != nil || last.EndDay != nil {
			t.Fatal("evaluation fabricated actual work time for an unstarted task")
		}
		raw, _ := json.Marshal(receipt)
		if first == nil {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatal("input execution permutation changed the canonical receipt")
		}
		var restored WorldArbitrationReceipt
		_ = json.Unmarshal(raw, &restored)
		replayed, err := FinalizeWorldArbitrationReceipt(restored, f.stimulus, f.activation, f.proposals, 1)
		if err != nil || replayed.Digest != receipt.Digest {
			t.Fatalf("new evaluation changed during reload: %v", err)
		}
	}
}

func TestSelfChronologyV1ContextAndBaselineCannotBeForged(t *testing.T) {
	f, session := newSelfChronologyFixture(t)
	if err := ValidateCharacterSelfEvaluationContextAgainstSessionV1(*f.stimulus.SelfEvaluationContext, session); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CharacterSelfEvaluationContextV1){
		"generation": func(c *CharacterSelfEvaluationContextV1) { c.GenerationID = "pg2_foreign" },
		"chapter":    func(c *CharacterSelfEvaluationContextV1) { c.Chapter++ },
		"cycle":      func(c *CharacterSelfEvaluationContextV1) { c.Cycle++ },
		"clock":      func(c *CharacterSelfEvaluationContextV1) { c.CurrentDay++ },
		"physical":   func(c *CharacterSelfEvaluationContextV1) { c.BeforePhysicalRoot = "sha256:" + strings.Repeat("f", 64) },
		"session":    func(c *CharacterSelfEvaluationContextV1) { c.SessionDigest = "sha256:" + strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *f.stimulus.SelfEvaluationContext
			mutate(&changed)
			changed.Digest, _ = selfEvaluationContextDigestV1(changed)
			if ValidateCharacterSelfEvaluationContextAgainstSessionV1(changed, session) == nil {
				t.Fatal("self-signed forged context bypassed the actual host session")
			}
			if name != "session" { // Session binding requires the separately loaded host session.
				stimulus := f.stimulus
				stimulus.SelfEvaluationContext = &changed
				if _, err := FinalizeWorldStimulusPacket(stimulus); err == nil {
					t.Fatal("forged context bypassed its frozen physical/cycle/clock binding")
				}
			}
		})
	}
	bad := f.receipt
	bad.Resolutions = append([]CharacterDecisionResolution(nil), f.receipt.Resolutions...)
	actor := *bad.Resolutions[0].PostState
	baseline := *actor.SelfChronologyBaseline
	baseline.SourcePhysicalRoot = "sha256:" + strings.Repeat("f", 64)
	baseline.Digest, _ = baselineDigestV1(baseline)
	actor.SelfChronologyBaseline = &baseline
	bad.Resolutions[0].PostState = &actor
	if _, err := ApplyArbitrationPhysicalStateV2(bad, f.stimulus, f.proposals...); err == nil {
		t.Fatal("arbiter changed the immutable host baseline")
	}
	badStimulus := f.stimulus
	badStimulus.Sources = []string{CharacterSelfExperiencePolicyV2}
	if _, err := FinalizeWorldStimulusPacket(badStimulus); err == nil {
		t.Fatal("new chronology was silently consumed under a legacy source policy")
	}
	oldView := f.observations[0]
	oldView.Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2}
	oldView, err := FinalizeCharacterObservationPacket(oldView)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateCharacterResourceViewsAgainstStimulusV2(f.stimulus, oldView) == nil {
		t.Fatal("re-signed observation silently dropped the stimulus chronology policy")
	}
}

func TestSelfChronologyV1RejectsUnexecutedMixPrematureCompletionAndOverlap(t *testing.T) {
	for name, executions := range map[string][]CharacterSelfExecutionV2{
		"nil mixed":       {{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1)}, {TaskID: "inspection", Status: "not_started"}},
		"blocked mixed":   {{TaskID: "inspection", Status: "blocked"}, {TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1)}},
		"early completed": {{TaskID: "inspection", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(1)}, {TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(2), EndDay: selfTestDay(4)}},
		"overlap":         {{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(3)}, {TaskID: "inspection", Status: "completed", StartDay: selfTestDay(2), EndDay: selfTestDay(4)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := canonicalSelfExecutionsV1(executions); err == nil {
				t.Fatal("invalid segmented evaluation accepted")
			}
		})
	}
}

func selfChronologyTestFact(actor string, cycle, ordinal int, task, status string, start, end *float64) CharacterSelfExperienceV2 {
	fact := CharacterSelfExperienceV2{Chapter: 1, TaskID: task, Kind: "work", Action: "检查" + task, Status: status,
		StartDay: start, EndDay: end, ProgressUnit: "minute", SourceProposalDigest: fmt.Sprintf("sha256:%064x", cycle),
		Evaluation: &CharacterSelfEvaluationV1{Version: CharacterSelfChronologyPolicyV1, GenerationID: "pg2_chronology", Cycle: cycle, Ordinal: ordinal,
			ContextDigest: fmt.Sprintf("sha256:%064x", cycle+100), StimulusDigest: fmt.Sprintf("sha256:%064x", cycle+200), EvaluatedAtDay: float64(cycle) / 1440}}
	fact.ID = CharacterSelfExperienceIDV2(actor, fact)
	return fact
}

func TestSelfChronologyV1CumulativeStateAndRecentEightUseActualEvaluations(t *testing.T) {
	f, _ := newSelfChronologyFixture(t)
	state := *f.stimulus.PhysicalState
	actor := &state.Actors[0]
	for cycle := 1; cycle <= 12; cycle++ {
		fact := selfChronologyTestFact(actor.AgentID, cycle, 1, fmt.Sprintf("task-%02d", cycle), "completed", selfTestDay(float64(cycle-1)), selfTestDay(float64(cycle)))
		actor.SelfExperiences = append(actor.SelfExperiences, fact)
	}
	var err error
	actor.TaskProgress, err = deriveSelfChronologyProgressV1(*actor)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately reverse storage order. Neither IDs nor input order are time.
	sort.Slice(actor.SelfExperiences, func(i, j int) bool { return actor.SelfExperiences[i].ID > actor.SelfExperiences[j].ID })
	recent, progress, err := BuildCharacterSelfObservationV2(state, actor.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 8 || len(progress) != 0 {
		t.Fatalf("unexpected recent window: %d/%d", len(recent), len(progress))
	}
	for i, fact := range recent {
		if fact.Evaluation.Cycle != i+5 {
			t.Fatal("recent-eight selected by opaque ID instead of verified chronology")
		}
	}
	actor.SelfExperiences = []CharacterSelfExperienceV2{
		selfChronologyTestFact(actor.AgentID, 1, 1, "work", "not_started", nil, nil),
		selfChronologyTestFact(actor.AgentID, 2, 1, "work", "in_progress", selfTestDay(1), selfTestDay(2)),
		selfChronologyTestFact(actor.AgentID, 3, 1, "work", "blocked", nil, nil),
	}
	for _, permutation := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		copy := *actor
		copy.SelfExperiences = []CharacterSelfExperienceV2{actor.SelfExperiences[permutation[0]], actor.SelfExperiences[permutation[1]], actor.SelfExperiences[permutation[2]]}
		got, err := deriveSelfChronologyProgressV1(copy)
		if err != nil || len(got) != 1 || got[0].Completed != 1 || got[0].State != "in_progress" || got[0].LatestAttemptStatus != "blocked" || got[0].SourceExperienceID != actor.SelfExperiences[2].ID {
			t.Fatalf("current blocked attempt erased or reordered actual work: %+v (%v)", got, err)
		}
	}
}

func TestSelfChronologyV1ExplicitLegacyBaselinePreservesFactsAndOldProgress(t *testing.T) {
	f := newSelfExperienceFixture(t)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(legacy)
	oldReceipt, _ := json.Marshal(receipt)
	prepared, err := PrepareCharacterSelfChronologyStateV1(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCharacterSelfChronologyBaselineTransitionV1(legacy, prepared); err != nil {
		t.Fatal(err)
	}
	for i, actor := range prepared.Actors {
		if actor.SelfChronologyBaseline == nil || !samePhysicalValueV2(actor.SelfExperiences, legacy.Actors[i].SelfExperiences) || !samePhysicalValueV2(actor.TaskProgress, legacy.Actors[i].TaskProgress) {
			t.Fatal("baseline invented evaluations or recomputed legacy progress")
		}
		for _, fact := range actor.SelfExperiences {
			if fact.Evaluation != nil {
				t.Fatal("legacy experience was assigned a fabricated evaluation time")
			}
		}
	}
	again, err := PrepareCharacterSelfChronologyStateV1(prepared)
	if err != nil || !samePhysicalValueV2(again, prepared) {
		t.Fatalf("baseline preparation is not idempotent: %v", err)
	}
	unchanged, _ := json.Marshal(legacy)
	if !bytes.Equal(before, unchanged) {
		t.Fatal("baseline mutated its accepted source state")
	}
	revalidated, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	legacyAfter, _ := json.Marshal(revalidated)
	if !bytes.Equal(oldReceipt, legacyAfter) || revalidated.Digest != receipt.Digest {
		t.Fatal("opt-in chronology changed an existing legacy receipt or its digest")
	}
	for name, corrupt := range map[string]func(*WorldPhysicalStateV2){
		"delete old fact": func(s *WorldPhysicalStateV2) { s.Actors[0].SelfExperiences = s.Actors[0].SelfExperiences[1:] },
		"inflate old total": func(s *WorldPhysicalStateV2) {
			b := s.Actors[0].SelfChronologyBaseline
			b.TaskProgress[0].Completed += 10
			b.Digest, _ = baselineDigestV1(*b)
		},
		"replace original root": func(s *WorldPhysicalStateV2) {
			b := s.Actors[0].SelfChronologyBaseline
			b.SourcePhysicalRoot = "sha256:" + strings.Repeat("f", 64)
			b.Digest, _ = baselineDigestV1(*b)
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(prepared)
			var changed WorldPhysicalStateV2
			_ = json.Unmarshal(raw, &changed)
			corrupt(&changed)
			if ValidateCharacterSelfChronologyBaselineTransitionV1(legacy, changed) == nil {
				t.Fatal("self-signed baseline bypassed the exact accepted source")
			}
			if name != "replace original root" && ValidateWorldPhysicalStateV2(changed) == nil {
				t.Fatal("baseline changed its authenticated old history or progress")
			}
		})
	}
}

func TestSelfChronologyV1ResignedEvaluationCannotBypassActualReceipt(t *testing.T) {
	f, _ := newSelfChronologyFixture(t)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*CharacterSelfEvaluationV1){
		"context":         func(e *CharacterSelfEvaluationV1) { e.ContextDigest = "sha256:" + strings.Repeat("f", 64) },
		"stimulus":        func(e *CharacterSelfEvaluationV1) { e.StimulusDigest = "sha256:" + strings.Repeat("f", 64) },
		"generation":      func(e *CharacterSelfEvaluationV1) { e.GenerationID = "pg2_other" },
		"cycle":           func(e *CharacterSelfEvaluationV1) { e.Cycle++ },
		"evaluation time": func(e *CharacterSelfEvaluationV1) { e.EvaluatedAtDay++ },
		"ordinal":         func(e *CharacterSelfEvaluationV1) { e.Ordinal++ },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(receipt)
			var changed WorldArbitrationReceipt
			_ = json.Unmarshal(raw, &changed)
			actor := changed.Resolutions[0].PostState
			for i := range actor.SelfExperiences {
				change(actor.SelfExperiences[i].Evaluation)
				actor.SelfExperiences[i].ID = CharacterSelfExperienceIDV2(actor.AgentID, actor.SelfExperiences[i])
			}
			if derived, err := deriveSelfChronologyProgressV1(*actor); err == nil {
				actor.TaskProgress = derived // Even an internally consistent self-sign is not authorization.
			}
			changed.Digest, _ = ComputeWorldArbitrationReceiptDigest(changed)
			if _, err := FinalizeWorldArbitrationReceipt(changed, f.stimulus, f.activation, f.proposals, 1); err == nil {
				t.Fatal("self-signed evaluation replaced the host-derived actual receipt")
			}
		})
	}
}

func TestSelfChronologyV1SameTimeNilAndStableOrdinalFormStrictTotalOrder(t *testing.T) {
	executions := []CharacterSelfExecutionV2{
		{TaskID: "z-wait", Status: "blocked"},
		{TaskID: "a-work", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1)},
		{TaskID: "b-carry", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(1)},
		{TaskID: "c-wait", Status: "not_started"},
	}
	ordered, err := canonicalSelfExecutionsV1(executions)
	if err != nil {
		t.Fatal(err)
	}
	var facts []CharacterSelfExperienceV2
	for i, e := range ordered {
		fact := selfChronologyTestFact("ca_a", 1, i+1, e.TaskID, e.Status, e.StartDay, e.EndDay)
		facts = append(facts, fact)
	}
	if ordered[0].TaskID != "a-work" || ordered[1].TaskID != "b-carry" || ordered[2].TaskID != "c-wait" || ordered[3].TaskID != "z-wait" {
		t.Fatal("same-time execution and nil buckets did not use canonical host order")
	}
	for _, a := range facts {
		if selfChronologyLessV1(a, a) {
			t.Fatal("ordering is not irreflexive")
		}
		for _, b := range facts {
			for _, c := range facts {
				if selfChronologyLessV1(a, b) && selfChronologyLessV1(b, c) && !selfChronologyLessV1(a, c) {
					t.Fatal("new policy recreated the legacy non-transitive comparator")
				}
			}
		}
	}
}
