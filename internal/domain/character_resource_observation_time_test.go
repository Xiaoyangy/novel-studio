package domain

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func timedResourceMeasurementFixture(t *testing.T, reading float64) physicalProtocolFixture {
	t.Helper()
	f := measurementBeforeConsumptionFixture(t, reading, true)
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterResourceObservationTimePolicyV1)
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterResourceObservationTimePolicyV1)
	}
	f.proposals[0].ResourceMeasurements[0].TaskID = "measure-light"
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{f.proposals[0].Digest}
	f.receipt.ResourceSettlements[0].StartDay, f.receipt.ResourceSettlements[0].EndDay = selfTestDay(1), selfTestDay(5)
	p := timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState)
	p.ObservedAtDay = selfTestDay(1)
	p.EvidenceRefs = []string{f.proposals[0].Digest}
	return f
}

func timedResourcePerceptionForTest(t *testing.T, actor *CharacterPhysicalStateV2) *ResourcePerceptionV2 {
	t.Helper()
	for i := range actor.Resources {
		if actor.Resources[i].ResourceID == measurementTimeLightID {
			return &actor.Resources[i].Perception
		}
	}
	t.Fatal("fixture is missing the light")
	return nil
}

func TestResourceObservationTimeReadsBeforeLaterConsumption(t *testing.T) {
	f := timedResourceMeasurementFixture(t, 4)
	before, _ := json.Marshal([]any{f.stimulus, f.proposals})
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatalf("actual minute-1 reading was rejected after minute-1..5 consumption: %v", err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	p := timedResourcePerceptionForTest(t, &after.Actors[0])
	if *p.Amount != 4 || *p.ObservedAtDay != *selfTestDay(1) {
		t.Fatalf("measurement did not retain its actual value/time: %+v", p)
	}
	for _, resource := range after.Resources {
		if resource.ResourceID == measurementTimeLightID && (resource.ActualAmount == nil || *resource.ActualAmount != 0) {
			t.Fatal("earlier reading changed the actual terminal balance")
		}
	}
	replayed, err := FinalizeWorldArbitrationReceipt(r, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || replayed.Digest != r.Digest {
		t.Fatalf("timed receipt did not replay deterministically: %v", err)
	}
	unchanged, _ := json.Marshal([]any{f.stimulus, f.proposals})
	if !bytes.Equal(before, unchanged) {
		t.Fatal("measurement validation changed its frozen source")
	}
}

func TestResourceObservationTimeRejectsInventedReadingsAndIntervals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*physicalProtocolFixture)
	}{
		{"terminal balance is not the earlier reading", func(f *physicalProtocolFixture) {
			timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).Amount = physicalTestNumber(0)
		}},
		{"missing observation time", func(f *physicalProtocolFixture) {
			timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).ObservedAtDay = nil
		}},
		{"different task end", func(f *physicalProtocolFixture) {
			timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).ObservedAtDay = selfTestDay(2)
		}},
		{"future observation", func(f *physicalProtocolFixture) {
			timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).ObservedAtDay = selfTestDay(6)
		}},
		{"unexecuted measurement", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[0] = CharacterSelfExecutionV2{TaskID: "measure-light", Status: "blocked"}
		}},
		{"other task cannot authorize reading", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].SelfExecutions[0].TaskID = "unrelated-task" }},
		{"unapplied mechanism", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].MechanismRefs = nil }},
		{"mechanism is not observation evidence", func(f *physicalProtocolFixture) {
			timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).EvidenceRefs = []string{"gauge"}
		}},
		{"missing settlement start", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].StartDay = nil }},
		{"missing settlement end", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].EndDay = nil }},
		{"reversed settlement", func(f *physicalProtocolFixture) {
			f.receipt.ResourceSettlements[0].StartDay, f.receipt.ResourceSettlements[0].EndDay = selfTestDay(5), selfTestDay(1)
		}},
		{"settlement outside cycle", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].EndDay = selfTestDay(6) }},
		{"nonfinite settlement", func(f *physicalProtocolFixture) {
			f.receipt.ResourceSettlements[0].StartDay = physicalTestNumber(math.NaN())
		}},
		{"inside interval cannot interpolate", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].StartDay = selfTestDay(0) }},
		{"nonconserving settlement", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].After = physicalTestNumber(1) }},
		{"duplicate resource segment remains unsupported", func(f *physicalProtocolFixture) {
			f.receipt.ResourceSettlements = append(f.receipt.ResourceSettlements, f.receipt.ResourceSettlements[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := timedResourceMeasurementFixture(t, 4)
			tc.edit(&f)
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("invalid temporal evidence was accepted")
			}
		})
	}
}

func TestResourceObservationTimeMeasurementMustBindOriginalOwnerWork(t *testing.T) {
	for _, taskID := range []string{"", "missing-task", "wait"} {
		f := timedResourceMeasurementFixture(t, 4)
		f.proposals[0].ResourceMeasurements[0].TaskID = taskID
		if _, err := FinalizeCharacterDecisionProposal(f.proposals[0], f.observations[0]); err == nil {
			t.Fatalf("measurement bound no actual owner work: %q", taskID)
		}
	}
	f := timedResourceMeasurementFixture(t, 4)
	f.proposals[0].SelfTasks[0].ResourceIDs = []string{physicalPaperTestID}
	if _, err := FinalizeCharacterDecisionProposal(f.proposals[0], f.observations[0]); err == nil {
		t.Fatal("work on another resource authorized this measurement")
	}
}

func TestResourceObservationTimeBoundaryAndInstantSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		start, end, at, want float64
		exact                bool
	}{
		{"before", 1, 5, .5, 4, true},
		{"positive start", 1, 5, 1, 4, true},
		{"interior", 1, 5, 3, 0, false},
		{"positive end", 1, 5, 5, 0, true},
		{"before instant", 1, 1, .5, 4, true},
		{"at instant", 1, 1, 1, 0, true},
		{"after instant", 1, 1, 2, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := timedResourceMeasurementFixture(t, 4)
			f.receipt.ResourceSettlements[0].StartDay, f.receipt.ResourceSettlements[0].EndDay = selfTestDay(tc.start), selfTestDay(tc.end)
			amount, unit, exact := resourceAmountAtObservationTimeV1(f.receipt, *f.stimulus.PhysicalState, measurementTimeLightID, *selfTestDay(tc.at))
			if exact != tc.exact || (exact && (amount == nil || *amount != tc.want || unit != "minute")) {
				t.Fatalf("snapshot=%v %s exact=%t, want %g exact=%t", amount, unit, exact, tc.want, tc.exact)
			}
		})
	}
	f := timedResourceMeasurementFixture(t, 0)
	f.receipt.ResourceSettlements[0].StartDay, f.receipt.ResourceSettlements[0].EndDay = selfTestDay(1), selfTestDay(1)
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatalf("actual instantaneous change at the measurement end was rejected: %v", err)
	}
}

func TestResourceObservationTimeKeepsOldObservationAgeAndRejectsFuturePrior(t *testing.T) {
	for _, tc := range []struct {
		name             string
		oldTime, newTime *float64
		kind             string
		wantErr          bool
	}{
		{"old untimed observation", nil, nil, "last_observed", false},
		{"timed old observation", selfTestDay(0), selfTestDay(0), "last_observed", false},
		{"estimate downgrade preserves age", selfTestDay(0), selfTestDay(0), "estimated", false},
		{"estimate cannot drop age", selfTestDay(0), nil, "estimated", true},
		{"estimate cannot refresh age", selfTestDay(0), selfTestDay(1), "estimated", true},
		{"future prior cannot use unchanged exemption", selfTestDay(1), selfTestDay(1), "last_observed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := timedResourceMeasurementFixture(t, 4)
			old := timedResourcePerceptionForTest(t, &f.stimulus.PhysicalState.Actors[0])
			*old = ResourcePerceptionV2{Kind: "last_observed", Amount: physicalTestNumber(4), AsOfChapter: 0, ObservedAtDay: tc.oldTime, EvidenceRefs: []string{"seed-a"}}
			rebindPhysicalTestStimulus(t, &f)
			f.receipt.ResourceSettlements[0].EvidenceRefs = []string{f.proposals[0].Digest}
			p := timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState)
			*p = *old
			p.Kind, p.ObservedAtDay = tc.kind, tc.newTime
			r, err := finalizePhysicalFixture(f)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected old observation result: %v", err)
			}
			if err == nil && !samePhysicalNumberV2(timedResourcePerceptionForTest(t, r.Resolutions[0].PostState).ObservedAtDay, tc.oldTime) {
				t.Fatal("old observation time was silently rewritten")
			}
		})
	}
}

func TestResourceObservationTimeLegacyRejectsNewFields(t *testing.T) {
	for _, field := range []string{"task_id", "observed_at_day", "start_day", "end_day"} {
		t.Run(field, func(t *testing.T) {
			f := measurementBeforeConsumptionFixture(t, 0, true)
			switch field {
			case "task_id":
				f.proposals[0].ResourceMeasurements[0].TaskID = "measure-light"
				if _, err := FinalizeCharacterDecisionProposal(f.proposals[0], f.observations[0]); err == nil || !strings.Contains(err.Error(), CharacterResourceObservationTimePolicyV1) {
					t.Fatalf("legacy task_id field accepted: %v", err)
				}
				return
			case "observed_at_day":
				timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).ObservedAtDay = selfTestDay(1)
			case "start_day":
				f.receipt.ResourceSettlements[0].StartDay = selfTestDay(1)
			case "end_day":
				f.receipt.ResourceSettlements[0].EndDay = selfTestDay(5)
			}
			if _, err := finalizePhysicalFixture(f); err == nil || !strings.Contains(err.Error(), CharacterResourceObservationTimePolicyV1) {
				t.Fatalf("legacy field accepted: %v", err)
			}
		})
	}
}

func TestResourceObservationTimeCanonicalSettlementOrderIsPolicyGated(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		f := measurementBeforeConsumptionFixture(t, 0, true)
		if enabled {
			f = timedResourceMeasurementFixture(t, 4)
		}
		f.receipt.ResourceSettlements = append(f.receipt.ResourceSettlements, ResourceSettlementV2{
			ResourceID: physicalFuelTestID, Before: physicalTestNumber(12), Delta: physicalTestNumber(0), After: physicalTestNumber(12), EvidenceRefs: []string{f.proposals[0].Digest},
		})
		first, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatal(err)
		}
		f.receipt.ResourceSettlements[0], f.receipt.ResourceSettlements[1] = f.receipt.ResourceSettlements[1], f.receipt.ResourceSettlements[0]
		second, err := finalizePhysicalFixture(f)
		if err != nil {
			t.Fatal(err)
		}
		if enabled && (first.Digest != second.Digest || first.ResourceSettlements[0].ResourceID != physicalFuelTestID) {
			t.Fatal("new policy did not canonicalize independent resource settlements")
		}
		if !enabled && first.Digest == second.Digest {
			t.Fatal("legacy receipt ordering/digest was silently changed")
		}
	}
}

func TestResourceObservationTimeUnknownBalanceCannotBecomeExact(t *testing.T) {
	f := timedResourceMeasurementFixture(t, 4)
	for i := range f.stimulus.PhysicalState.Resources {
		if f.stimulus.PhysicalState.Resources[i].ResourceID == measurementTimeLightID {
			f.stimulus.PhysicalState.Resources[i].ActualAmount = nil
		}
	}
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.ResourceSettlements[0].Before, f.receipt.ResourceSettlements[0].After = nil, nil
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{f.proposals[0].Digest}
	timedResourcePerceptionForTest(t, f.receipt.Resolutions[0].PostState).EvidenceRefs = []string{f.proposals[0].Digest}
	if _, err := finalizePhysicalFixture(f); err == nil || !strings.Contains(err.Error(), "numeric perception") {
		t.Fatalf("unknown world balance supplied a precise measurement: %v", err)
	}
}

func TestResourceObservationTimeNewReportDoesNotInheritMeasurementAge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		amount     float64
		reportedAt *float64
		unchanged  bool
		wantErr    bool
	}{
		{"new report clears old measurement time", 0, nil, false, false},
		{"new report cannot reuse old measurement time", 0, physicalTestNumber(1), false, true},
		{"report receipt time is not measurement time", 0, physicalTestNumber(5), false, true},
		{"same amount from new source clears old time", 4, nil, false, false},
		{"same amount from new source cannot reuse old time", 4, physicalTestNumber(1), false, true},
		{"fully unchanged reported perception keeps validated form", 4, physicalTestNumber(1), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPhysicalProtocolFixture(t)
			f.stimulus.Sources = append(f.stimulus.Sources, CharacterResourceObservationTimePolicyV1)
			for i := range f.observations {
				f.observations[i].Sources = append(f.observations[i].Sources, CharacterResourceObservationTimePolicyV1)
			}
			old := ResourcePerceptionV2{Kind: "last_observed", Amount: physicalTestNumber(4), AsOfChapter: 0, ObservedAtDay: physicalTestNumber(1), EvidenceRefs: []string{"seed-b"}}
			if tc.unchanged {
				old.Kind = "reported"
			}
			f.stimulus.PhysicalState.Actors[1].Resources[0].Perception = old
			f.receipt.StoryTime = &StoryTimeChapterSchedule{Chapter: 1, StartDay: 4, EndDay: 5}
			if !tc.unchanged {
				f.proposals[0].ResourceReports = []ResourceReportV2{{ResourceID: physicalFuelTestID, ToCharacter: "乙", PerceivedUnit: "L", Amount: physicalTestNumber(tc.amount), EvidenceRefs: CharacterSourceRefsV2("ca_a", []string{"seed-a"})}}
			}
			rebindPhysicalTestStimulus(t, &f)
			post := f.stimulus.PhysicalState.Actors[1]
			post.Resources = append([]CharacterResourceHoldingV2(nil), post.Resources...)
			f.receipt.Resolutions[1].PostState = &post
			if !tc.unchanged {
				post.Resources[0].Perception = ResourcePerceptionV2{Kind: "reported", Amount: physicalTestNumber(tc.amount), AsOfChapter: 1, ObservedAtDay: tc.reportedAt, EvidenceRefs: []string{f.proposals[0].Digest}}
				f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalFuelTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, ReceivedFields: []string{"amount"}, Access: "none", EvidenceRefs: []string{f.proposals[0].Digest, CharacterSourceRefV2("ca_a", "seed-a")}}}
			}
			before, _ := json.Marshal(f.stimulus)
			r, err := finalizePhysicalFixture(f)
			if (err != nil) != tc.wantErr {
				t.Fatalf("report time authorization changed: %v", err)
			}
			if err == nil {
				got := r.Resolutions[1].PostState.Resources[0].Perception
				if got.Kind != "reported" || *got.Amount != tc.amount || !samePhysicalNumberV2(got.ObservedAtDay, tc.reportedAt) {
					t.Fatalf("report acquired a false measurement time: %+v", got)
				}
			}
			after, _ := json.Marshal(f.stimulus)
			if !bytes.Equal(before, after) {
				t.Fatal("new report rewrote the immutable prior measurement")
			}
		})
	}
}
