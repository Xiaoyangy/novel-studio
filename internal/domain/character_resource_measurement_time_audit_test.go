package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const measurementTimeLightID = "res_00000000000000f1"

// This fixture records a real measurement task before a separate consumption
// task, without opting into the new timing policy. It preserves the old numeric
// measurement contract as a compatibility fixture for both policy branches.
func measurementBeforeConsumptionFixture(t *testing.T, reading float64, consume bool) physicalProtocolFixture {
	t.Helper()
	f := newSelfExperienceFixture(t)
	f.stimulus.TimeWindow = "五分钟；先测量余量，再执行后续动作"
	f.stimulus.PhysicalState.Resources = append(f.stimulus.PhysicalState.Resources, WorldResourceBalanceV2{
		ResourceID: measurementTimeLightID, Name: "检修灯剩余点亮时间", Unit: "minute", ActualAmount: physicalTestNumber(4),
	})
	f.stimulus.PhysicalState.Actors[0].Resources = append(f.stimulus.PhysicalState.Actors[0].Resources, CharacterResourceHoldingV2{
		ResourceID: measurementTimeLightID, PerceivedName: "检修灯余量", PerceivedUnit: "minute", Access: "exclusive",
		Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"seed-a"},
	})
	laterAction := "连续点亮检修灯直至耗尽"
	remaining := 0.0
	if !consume {
		laterAction, remaining = "保持检修灯关闭并保存余量", 4
	}
	f.proposals[0].IntendedAction = "先读取检修灯余量，再" + laterAction
	f.proposals[0].ActionDuration = "五分钟"
	f.proposals[0].MechanismRefs = []string{"gauge"}
	f.proposals[0].ResourceMeasurements = []ResourceMeasurementV2{{ResourceID: measurementTimeLightID, MechanismRef: "gauge"}}
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{
		{TaskID: "measure-light", Kind: "work", Action: "读取检修灯剩余点亮分钟数", ProgressTarget: physicalTestNumber(1), ResourceIDs: []string{measurementTimeLightID}, KnowledgeRefs: []string{"known-ca_a"}},
		{TaskID: "after-measurement", Kind: "work", Action: laterAction, ProgressTarget: physicalTestNumber(4), ResourceIDs: []string{measurementTimeLightID}, KnowledgeRefs: []string{"known-ca_a"}},
	}
	rebindPhysicalTestStimulus(t, &f)
	for i, actor := range f.stimulus.PhysicalState.Actors {
		post := actor
		post.Resources = append([]CharacterResourceHoldingV2(nil), actor.Resources...)
		f.receipt.Resolutions[i].PostState = &post
	}
	r := &f.receipt.Resolutions[0]
	r.IntendedAction = f.proposals[0].IntendedAction
	r.Outcome, r.CompletionState = "success", "completed"
	r.MechanismRefs = []string{"gauge"}
	r.ImmediateResult = "第1分钟完成读表，读数为剩余4分钟；此后执行后续动作，没有再次测量"
	r.SelfExecutions = []CharacterSelfExecutionV2{
		{TaskID: "measure-light", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(1)},
		{TaskID: "after-measurement", Status: "completed", StartDay: selfTestDay(1), EndDay: selfTestDay(5)},
	}
	for i := range r.PostState.Resources {
		if r.PostState.Resources[i].ResourceID == measurementTimeLightID {
			r.PostState.Resources[i].Perception = ResourcePerceptionV2{
				Kind: "last_observed", Amount: physicalTestNumber(reading), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[0].Digest},
			}
		}
	}
	f.receipt.ResourceSettlements = []ResourceSettlementV2{{
		ResourceID: measurementTimeLightID, Before: physicalTestNumber(4), Delta: physicalTestNumber(remaining - 4), After: physicalTestNumber(remaining), EvidenceRefs: []string{f.proposals[0].Digest},
	}}
	return f
}

// Compatibility characterization for receipts without the new policy marker:
// a correct earlier reading is rejected; substituting the terminal balance is
// accepted without a second measurement. Changing this test requires an actual
// temporal authority fix, not relaxing numeric provenance validation.
func TestResourceMeasurementTimeAuditComparesEarlierReadingToCycleEndBalance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reading float64
		consume bool
		wantErr bool
	}{
		{"measured_four_then_exhausted", 4, true, true},
		{"terminal_zero_without_remeasurement", 0, true, false},
		{"measured_four_and_not_consumed", 4, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := measurementBeforeConsumptionFixture(t, tc.reading, tc.consume)
			before, err := json.Marshal([]any{f.stimulus, f.proposals})
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := finalizePhysicalFixture(f)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "numeric perception lacks an independent estimate, measurement or received report") {
					t.Fatalf("expected exact cycle-end perception rejection, got: %v", err)
				}
				t.Logf("legacy behavior retained: earlier reading 4 differs from final balance 0: %v", err)
			} else {
				if err != nil {
					t.Fatalf("control failed before isolating measurement timing: %v", err)
				}
				actor := receipt.Resolutions[0].PostState
				if len(actor.SelfExperiences) != 2 || *receipt.Resolutions[0].SelfExecutions[0].EndDay != *selfTestDay(1) || *receipt.Resolutions[0].SelfExecutions[1].EndDay != *selfTestDay(5) {
					t.Fatal("fixture lost the two distinct, ordered actual execution intervals")
				}
				if tc.consume {
					t.Log("legacy cycle-end comparison retained only without the new policy")
				}
			}
			after, marshalErr := json.Marshal([]any{f.stimulus, f.proposals})
			if marshalErr != nil || !bytes.Equal(before, after) {
				t.Fatal("validation mutated the original world or proposal")
			}
		})
	}
}

func TestResourceObservationTimeLegacyJSONGolden(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{ResourceMeasurementV2{ResourceID: measurementTimeLightID, MechanismRef: "gauge"}, `{"resource_id":"res_00000000000000f1","mechanism_ref":"gauge"}`},
		{ResourcePerceptionV2{Kind: "last_observed", Amount: physicalTestNumber(4), AsOfChapter: 1, EvidenceRefs: []string{"source"}}, `{"kind":"last_observed","amount":4,"as_of_chapter":1,"evidence_refs":["source"]}`},
		{ResourceSettlementV2{ResourceID: measurementTimeLightID, Before: physicalTestNumber(4), Delta: physicalTestNumber(-4), After: physicalTestNumber(0), EvidenceRefs: []string{"source"}}, `{"resource_id":"res_00000000000000f1","before":4,"delta":-4,"after":0,"evidence_refs":["source"]}`},
	} {
		raw, err := json.Marshal(tc.value)
		if err != nil || string(raw) != tc.want {
			t.Fatalf("legacy JSON bytes changed: got %s want %s err=%v", raw, tc.want, err)
		}
	}
	if operationalResourceV1(WorldResourceBalanceV2{ResourceID: measurementTimeLightID, Name: "检修灯余量", Unit: "minute", ActualAmount: physicalTestNumber(4)}) {
		t.Fatal("qualitative availability unexpectedly authorized a numeric reading")
	}
}
