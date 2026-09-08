package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

const sharedCarryLightID = "res_0000000000000005"

func sharedCarryFixture(t *testing.T, enabled, chronology bool) physicalProtocolFixture {
	t.Helper()
	f := newSelfExperienceFixture(t)
	f.stimulus.PhysicalState.Resources = append(f.stimulus.PhysicalState.Resources, WorldResourceBalanceV2{ResourceID: sharedCarryLightID, Name: "作者侧检修灯目录", ActualAmount: nil})
	f.stimulus.PhysicalState.Actors[0].Resources = append(f.stimulus.PhysicalState.Actors[0].Resources, CharacterResourceHoldingV2{ResourceID: sharedCarryLightID, PerceivedName: "检修灯", PerceivedLabel: "检修灯（仅照明）", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"seed-a"}})
	lightTask := CharacterSelfTaskV2{TaskID: "carry-light", Kind: "carry", Action: "与手工具同程携带检修灯至实际作业终点", ResourceIDs: []string{sharedCarryLightID}, KnowledgeRefs: []string{"known-ca_a"}}
	f.proposals[0].SelfTasks = append(f.proposals[0].SelfTasks[:2:2], append([]CharacterSelfTaskV2{lightTask}, f.proposals[0].SelfTasks[2:]...)...)
	lightExecution := CharacterSelfExecutionV2{TaskID: "carry-light", Status: "completed", StartDay: selfTestDay(1), EndDay: selfTestDay(4)}
	f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions[:2:2], append([]CharacterSelfExecutionV2{lightExecution}, f.receipt.Resolutions[0].SelfExecutions[2:]...)...)
	var err error
	state, err := FinalizeWorldPhysicalStateV2(*f.stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		f.stimulus.Sources = append(f.stimulus.Sources, CharacterSharedIntervalCarryPolicyV1)
	}
	if chronology {
		state, err = PrepareCharacterSelfChronologyStateV1(state)
		if err != nil {
			t.Fatal(err)
		}
		session, err := NewCharacterActivationSession(f.stimulus.GenerationID, 1, "sha256:"+strings.Repeat("a", 64), state, 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		f.stimulus.SelfEvaluationContext, err = NewCharacterSelfEvaluationContextV1(session)
		if err != nil {
			t.Fatal(err)
		}
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
			f.observations[i].CycleContext = &CharacterObservationCycleContext{Version: CharacterObservationCyclePolicy, Index: 1, ChapterContextDigest: session.ChapterContextDigest}
		}
	}
	f.stimulus.PhysicalState = &state
	for i, actor := range state.Actors {
		post := actor
		post.Location = f.receipt.Resolutions[i].PostState.Location
		f.receipt.Resolutions[i].PostState = &post
	}
	rebindPhysicalTestStimulus(t, &f)
	settlePhysicalFuel(&f)
	return f
}

func TestSharedIntervalCarryTwoTasksKeepOneRouteAndDistinctOwnerEvidence(t *testing.T) {
	for _, chronology := range []bool{false, true} {
		f := sharedCarryFixture(t, true, chronology)
		before, _ := json.Marshal([]any{f.stimulus, f.proposals})
		receipt, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatalf("two independently declared same-trip items were rejected: %v", err)
		}
		state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]string{}
		for _, holding := range state.Actors[0].Resources {
			if holding.ResourceID != physicalPaperTestID && holding.ResourceID != sharedCarryLightID {
				continue
			}
			if holding.KnownPlacement == nil || holding.KnownPlacement.Kind != "with_actor" || holding.KnownPlacement.Location != "停泊的渡船" {
				t.Fatal("same-trip item did not retain the one actually reached endpoint")
			}
			ids[holding.ResourceID] = holding.KnownPlacement.SourceExperienceID
		}
		if len(ids) != 2 || ids[physicalPaperTestID] == ids[sharedCarryLightID] {
			t.Fatal("distinct original carry tasks were merged or lost their own receipts")
		}
		if len(state.Actors[0].TaskProgress) != 1 || math.Abs(state.Actors[0].TaskProgress[0].Completed-1) > 1e-9 || receipt.StoryTime.EndDay != *selfTestDay(5) || *state.Resources[0].ActualAmount != 11.8 {
			t.Fatal("sharing a carry interval double-counted work, elapsed time or a physical settlement")
		}
		views, err := BuildCharacterResourceViewsV2(state, "ca_a")
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, view := range views {
			if ids[view.ResourceID] != "" {
				seen++
				if view.KnownPlacement == nil || view.KnownPlacement.SourceExperienceID != ids[view.ResourceID] {
					t.Fatal("next owner projection lost an actual carried item")
				}
			}
		}
		if seen != 2 {
			t.Fatal("next observation omitted a carried resource")
		}
		raw, _ := json.Marshal(receipt)
		var loaded WorldArbitrationReceipt
		if err := json.Unmarshal(raw, &loaded); err != nil {
			t.Fatal(err)
		}
		replayed, err := FinalizeWorldArbitrationReceipt(loaded, f.stimulus, f.activation, f.proposals, 1)
		if err != nil || replayed.Digest != receipt.Digest {
			t.Fatalf("new-policy receipt failed immutable reload: %v", err)
		}
		reencoded, _ := json.Marshal(replayed)
		after, _ := json.Marshal([]any{f.stimulus, f.proposals})
		if string(raw) != string(reencoded) || string(before) != string(after) {
			t.Fatal("same-trip interpretation rewrote the original choices or receipt")
		}
	}
}

func TestSharedIntervalCarryLegacyStillRejectsTwoTasks(t *testing.T) {
	f := sharedCarryFixture(t, false, true)
	if _, err := finalizePhysicalFixture(f); err == nil || !strings.Contains(err.Error(), "one explicit endpoint carry") {
		t.Fatalf("a newer binary silently changed an unmarked generation: %v", err)
	}
}

func sharedCarryResetSource(t *testing.T, f *physicalProtocolFixture) {
	t.Helper()
	// Tests that change initial access must update the proposed unchanged
	// holdings too, so failure comes from real transport authority, not an
	// unrelated attempted access upgrade in model-supplied post-state.
	for i, actor := range f.stimulus.PhysicalState.Actors {
		post := actor
		post.Location = f.receipt.Resolutions[i].PostState.Location
		f.receipt.Resolutions[i].PostState = &post
	}
	rebindPhysicalTestStimulus(t, f)
	settlePhysicalFuel(f)
}

func TestSharedIntervalCarryRejectsDifferentTripsDuplicateResourcesAndParallelWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *physicalProtocolFixture)
		want string
	}{
		{"different_start", func(_ *testing.T, f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[2].StartDay = selfTestDay(2)
		}, "one exact actual interval"},
		{"different_end", func(_ *testing.T, f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[2].EndDay = selfTestDay(5)
		}, "one exact actual interval"},
		{"separate_trip", func(_ *testing.T, f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[2].StartDay, f.receipt.Resolutions[0].SelfExecutions[2].EndDay = selfTestDay(4), selfTestDay(5)
		}, "one exact actual interval"},
		{"duplicate_resource", func(t *testing.T, f *physicalProtocolFixture) {
			f.proposals[0].SelfTasks[2].ResourceIDs = []string{physicalPaperTestID}
			rebindPhysicalTestProposals(t, f)
			settlePhysicalFuel(f)
		}, "same resource"},
		{"duplicate_completed_task", func(_ *testing.T, f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions, f.receipt.Resolutions[0].SelfExecutions[1])
		}, "completed task"},
		{"shared_is_not_possession", func(t *testing.T, f *physicalProtocolFixture) {
			for i := range f.stimulus.PhysicalState.Actors[0].Resources {
				if f.stimulus.PhysicalState.Actors[0].Resources[i].ResourceID == sharedCarryLightID {
					f.stimulus.PhysicalState.Actors[0].Resources[i].Access = "shared"
				}
			}
			sharedCarryResetSource(t, f)
		}, "possession authority"},
		{"inaccessible_resource", func(t *testing.T, f *physicalProtocolFixture) {
			for i := range f.stimulus.PhysicalState.Actors[0].Resources {
				if f.stimulus.PhysicalState.Actors[0].Resources[i].ResourceID == sharedCarryLightID {
					f.stimulus.PhysicalState.Actors[0].Resources[i].Access = "none"
				}
			}
			sharedCarryResetSource(t, f)
		}, "unseen or inaccessible"},
		{"ordinary_parallel_work", func(t *testing.T, f *physicalProtocolFixture) {
			f.proposals[0].SelfTasks = append(f.proposals[0].SelfTasks, CharacterSelfTaskV2{TaskID: "second-work", Kind: "work", Action: "同时做第二项实际作业", KnowledgeRefs: []string{"known-ca_a"}})
			rebindPhysicalTestProposals(t, f)
			settlePhysicalFuel(f)
			f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions, CharacterSelfExecutionV2{TaskID: "second-work", Status: "in_progress", StartDay: selfTestDay(4), EndDay: selfTestDay(5)})
		}, "overlapping"},
		{"intermediate_place", func(_ *testing.T, f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[0].StartDay, f.receipt.Resolutions[0].SelfExecutions[0].EndDay = selfTestDay(3), selfTestDay(4)
		}, "place must be explicitly completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sharedCarryFixture(t, true, false)
			tc.edit(t, &f)
			before, _ := json.Marshal([]any{f.stimulus, f.proposals})
			if _, err := finalizePhysicalFixture(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid transport/work did not fail at its intended boundary: %v", err)
			}
			after, _ := json.Marshal([]any{f.stimulus, f.proposals})
			if string(before) != string(after) {
				t.Fatal("rejected execution modified source state or original proposals")
			}
		})
	}
	// Canonical evaluation sorts carry tasks by actual interval/task identity;
	// identical carry intervals must not disable its independent work-overlap
	// check or its rejection of duplicate segments of the SAME task.
	for _, duplicate := range []bool{false, true} {
		f := sharedCarryFixture(t, true, true)
		if duplicate {
			f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions, f.receipt.Resolutions[0].SelfExecutions[1])
		} else {
			f.proposals[0].SelfTasks = append(f.proposals[0].SelfTasks, CharacterSelfTaskV2{TaskID: "second-work", Kind: "work", Action: "第二项作业", KnowledgeRefs: []string{"known-ca_a"}})
			rebindPhysicalTestProposals(t, &f)
			settlePhysicalFuel(&f)
			f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions, CharacterSelfExecutionV2{TaskID: "second-work", Status: "in_progress", StartDay: selfTestDay(4), EndDay: selfTestDay(5)})
		}
		if _, err := finalizePhysicalFixture(f); err == nil {
			t.Fatal("shared carry interval disabled chronology execution overlap guards")
		}
	}
}

func TestSharedIntervalCarryUnfinishedItemDoesNotFollowActorAutomatically(t *testing.T) {
	for _, status := range []string{"not_started", "in_progress", "blocked"} {
		f := sharedCarryFixture(t, true, true)
		f.receipt.Resolutions[0].SelfExecutions[2].Status = status
		if status != "in_progress" {
			f.receipt.Resolutions[0].SelfExecutions[2].StartDay, f.receipt.Resolutions[0].SelfExecutions[2].EndDay = nil, nil
		}
		receipt, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatal(err)
		}
		state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
		if err != nil {
			t.Fatal(err)
		}
		for _, holding := range state.Actors[0].Resources {
			if holding.ResourceID == sharedCarryLightID && holding.KnownPlacement != nil {
				t.Fatal("unfinished light transport was inferred from the actor's arrival")
			}
			if holding.ResourceID == physicalPaperTestID && (holding.KnownPlacement == nil || holding.KnownPlacement.Location != "停泊的渡船") {
				t.Fatal("another unfinished item erased a genuinely completed carry")
			}
		}
	}
}

func TestSharedIntervalCarrySingleTaskMultipleResourcesStillWorksWithoutMarker(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		f := sharedCarryFixture(t, enabled, true)
		f.proposals[0].SelfTasks[1].ResourceIDs = append(f.proposals[0].SelfTasks[1].ResourceIDs, sharedCarryLightID)
		f.proposals[0].SelfTasks = append(f.proposals[0].SelfTasks[:2], f.proposals[0].SelfTasks[3:]...)
		f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions[:2], f.receipt.Resolutions[0].SelfExecutions[3:]...)
		rebindPhysicalTestProposals(t, &f)
		settlePhysicalFuel(&f)
		receipt, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatalf("single explicitly declared multi-item carry regressed: %v", err)
		}
		state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
		if err != nil {
			t.Fatal(err)
		}
		var source string
		for _, holding := range state.Actors[0].Resources {
			if holding.ResourceID != physicalPaperTestID && holding.ResourceID != sharedCarryLightID {
				continue
			}
			if holding.KnownPlacement == nil || holding.KnownPlacement.Location != "停泊的渡船" {
				t.Fatal("single-task multi-item source lost a confirmed placement")
			}
			if source != "" && source != holding.KnownPlacement.SourceExperienceID {
				t.Fatal("one original task was silently split or re-signed")
			}
			source = holding.KnownPlacement.SourceExperienceID
		}
	}
}
