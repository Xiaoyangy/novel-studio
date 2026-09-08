package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

const operationalPowerTestID = "res_0000000000000005"

func newOperationalObservationFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f := newSelfExperienceFixture(t)
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterOperationalAvailabilityPolicyV1)
	f.stimulus.PhysicalState.Resources = append(f.stimulus.PhysicalState.Resources, WorldResourceBalanceV2{ResourceID: operationalPowerTestID, Name: "作者秘密：电源容量与故障原因不可见"})
	f.stimulus.PhysicalState.Actors[0].Resources = append(f.stimulus.PhysicalState.Actors[0].Resources, CharacterResourceHoldingV2{ResourceID: operationalPowerTestID, PerceivedName: "船用电源", PerceivedLabel: "独立船用电源（限本岗使用）", Access: "shared", Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"power-owner-source"}})
	f.stimulus.Mechanisms = append(f.stimulus.Mechanisms, CodexMechanism{ID: "operational_check", Name: "本岗通电检查", Visibility: "formal"})
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterOperationalAvailabilityPolicyV1)
		f.observations[i].PublicMechanisms = f.stimulus.Mechanisms
	}
	f.proposals[0].MechanismRefs = []string{"operational_check"}
	f.proposals[0].SelfTasks[2].ObservationRequests = []CharacterOperationalObservationRequestV1{{RequestID: "check_power", ResourceID: operationalPowerTestID, Purpose: "为本班机务检查提供操作电源", MechanismRef: "operational_check", KnowledgeRefs: []string{"known-ca_a"}}}
	rebindPhysicalTestStimulus(t, &f)
	copy := f.stimulus.PhysicalState.Actors[0]
	copy.Location = "停泊的渡船"
	f.receipt.Resolutions[0].PostState = &copy
	f.receipt.Resolutions[0].MechanismRefs = []string{"operational_check"}
	f.receipt.Resolutions[0].SelfExecutions[2].ObservationResults = []CharacterOperationalObservationResultV1{{RequestID: "check_power", Result: "available", ObservedAtDay: selfTestDay(4.5)}}
	f.receipt.Resolutions[0].StateAfter = "旁人秘密与无限供电容量9999Wh，不得复制进个人知识。"
	f.receipt.Resolutions[0].ImmediateResult = "世界侧说明不是任何观测的新事实来源。"
	settlePhysicalFuel(&f)
	return f
}

func TestOperationalObservationV1DerivesOnlyOwnerResultAndPreservesPhysicalState(t *testing.T) {
	f := newOperationalObservationFixture(t)
	before, _ := json.Marshal(f.stimulus)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	actor := state.Actors[0]
	if len(actor.OperationalObservations) != 1 || len(state.Actors[1].OperationalObservations) != 0 {
		t.Fatal("operational result lost or leaked across owners")
	}
	got := actor.OperationalObservations[0]
	if got.AgentID != "ca_a" || got.GenerationID != f.stimulus.GenerationID || got.Chapter != 1 || got.TaskID != "inspection" || got.RequestID != "check_power" || got.Result != "available" || *got.ObservedAtDay != *selfTestDay(4.5) || got.SourceProposalDigest != f.proposals[0].Digest || got.ID != CharacterOperationalObservationIDV1(got) {
		t.Fatalf("result identity/source not bound: %+v", got)
	}
	for _, resource := range state.Resources {
		if resource.ResourceID == operationalPowerTestID && (resource.ActualAmount != nil || resource.Unit != "") {
			t.Fatal("qualitative result invented a world quantity")
		}
	}
	for _, view := range actor.Resources {
		if view.ResourceID == operationalPowerTestID && (view.Access != "shared" || view.Perception.Kind != "unknown") {
			t.Fatal("operational result rewrote permission/numeric perception")
		}
	}
	if actor.TaskProgress[0].State == "completed" || *actor.TaskProgress[0].Target != 35 {
		t.Fatal("local availability completed the whole inspection")
	}
	private, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(private, "本人操作性观察") || !strings.Contains(private, "T+4.5分钟") || !strings.Contains(private, "局部可操作") || !strings.Contains(private, "不证明持续可用、容量或整项检查合格") {
		t.Fatalf("private result lost scope/time: %s", private)
	}
	for _, secret := range []string{"作者秘密", "9999Wh", "世界侧说明"} {
		if strings.Contains(private, secret) {
			t.Fatalf("unstructured world text leaked: %s", secret)
		}
	}
	other, _ := CharacterPrivateOutcomeV2(f.proposals[1], receipt.Resolutions[1], state)
	if strings.Contains(other, "操作性观察") {
		t.Fatal("another actor learned the result")
	}
	after, _ := json.Marshal(f.stimulus)
	if !bytes.Equal(before, after) {
		t.Fatal("derivation mutated stimulus")
	}
	raw, _ := json.Marshal(receipt)
	var replay WorldArbitrationReceipt
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	verified, err := FinalizeWorldArbitrationReceipt(replay, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || verified.Digest != receipt.Digest {
		t.Fatalf("receipt recovery changed its result: %v", err)
	}
}

func TestOperationalObservationV1NextObservationBindsExactResultAndOriginalAge(t *testing.T) {
	f := newOperationalObservationFixture(t)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	next := f.stimulus
	next.Chapter = 2
	next.PhysicalState = &state
	next, err = FinalizeWorldStimulusPacket(next)
	if err != nil {
		t.Fatal(err)
	}
	o := f.observations[0]
	o.Chapter = 2
	o.StimulusDigest = next.Digest
	o.Location = state.Actors[0].Location
	o.ResourceViews, err = BuildCharacterResourceViewsV2(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o.SelfExperiences, o.TaskProgress, err = BuildCharacterSelfObservationV2(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o.OperationalObservations, err = BuildCharacterOperationalObservationsV1(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o, err = FinalizeCharacterObservationPacket(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCharacterResourceViewsAgainstStimulusV2(next, o); err != nil {
		t.Fatal(err)
	}
	if len(o.OperationalObservations) != 1 || o.OperationalObservations[0].Chapter != 1 || *o.OperationalObservations[0].ObservedAtDay != *selfTestDay(4.5) {
		t.Fatal("past observation was refreshed into current availability")
	}
	if _, allowed := o.AllowedFactIDs()[o.OperationalObservations[0].ID]; !allowed {
		t.Fatal("owner cannot cite its validated result")
	}
	for _, mode := range []string{"result", "omission", "policy", "foreign-owner", "future"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(o)
			var changed CharacterObservationPacket
			_ = json.Unmarshal(raw, &changed)
			switch mode {
			case "result":
				changed.OperationalObservations[0].Result = "unavailable"
				changed.OperationalObservations[0].ID = CharacterOperationalObservationIDV1(changed.OperationalObservations[0])
			case "omission":
				changed.OperationalObservations = nil
			case "policy":
				changed.Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2}
			case "foreign-owner":
				changed.AgentID = "ca_b"
			case "future":
				changed.Chapter = 1
			}
			finalized, err := FinalizeCharacterObservationPacket(changed)
			if err == nil && ValidateCharacterResourceViewsAgainstStimulusV2(next, finalized) == nil {
				t.Fatal("re-signed altered result escaped source binding")
			}
		})
	}
	other, err := BuildCharacterOperationalObservationsV1(state, "ca_b")
	if err != nil || len(other) != 0 {
		t.Fatal("other owner sees private result")
	}
}

func TestOperationalObservationV1RejectsUnauthorizedOrNonOperationalRequests(t *testing.T) {
	for _, mode := range []string{"no-policy", "unknown-resource", "no-access", "numeric-view", "unknown-mechanism", "uninvoked-mechanism", "foreign-knowledge", "carry", "duplicate-id", "too-many"} {
		t.Run(mode, func(t *testing.T) {
			f := newOperationalObservationFixture(t)
			p, o := f.proposals[0], f.observations[0]
			r := &p.SelfTasks[2].ObservationRequests[0]
			switch mode {
			case "no-policy":
				o.Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2}
			case "unknown-resource":
				r.ResourceID = "res_9999999999999999"
			case "no-access":
				for i := range o.ResourceViews {
					if o.ResourceViews[i].ResourceID == r.ResourceID {
						o.ResourceViews[i].Access = "none"
					}
				}
			case "numeric-view":
				r.ResourceID = physicalFuelTestID
			case "unknown-mechanism":
				r.MechanismRef = "secret_mechanism"
			case "uninvoked-mechanism":
				p.MechanismRefs = nil
			case "foreign-knowledge":
				r.KnowledgeRefs = []string{"known-ca_b"}
			case "carry":
				p.SelfTasks[2].Kind = "carry"
			case "duplicate-id":
				p.SelfTasks[0].Kind = "work"
				p.SelfTasks[0].ObservationRequests = append([]CharacterOperationalObservationRequestV1(nil), p.SelfTasks[2].ObservationRequests...)
			case "too-many":
				for i := range 8 {
					next := *r
					next.RequestID = fmt.Sprintf("request_%d", i)
					p.SelfTasks[2].ObservationRequests = append(p.SelfTasks[2].ObservationRequests, next)
				}
			}
			if err := ValidateCharacterSelfTaskIntentV2(p, o); err == nil {
				t.Fatal("invalid operational request was accepted")
			}
		})
	}
	for _, mode := range []string{"actual-amount", "world-unit", "document"} {
		t.Run(mode, func(t *testing.T) {
			f := newOperationalObservationFixture(t)
			for i := range f.stimulus.PhysicalState.Resources {
				r := &f.stimulus.PhysicalState.Resources[i]
				if r.ResourceID != operationalPowerTestID {
					continue
				}
				switch mode {
				case "actual-amount":
					r.Unit = "Wh"
					r.ActualAmount = physicalTestNumber(200)
				case "world-unit":
					r.Unit = "Wh"
				case "document":
					r.ReadableFacts = []ResourceReadableFactV2{{ID: "secret", Text: "文书载明的隐藏事实"}}
				}
			}
			rebindPhysicalTestStimulus(t, &f)
			settlePhysicalFuel(&f)
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("operational path bypassed quantity or document protocol")
			}
		})
	}
}

func TestOperationalObservationV1RejectsUnexecutedUntimedOrForgedResults(t *testing.T) {
	for _, mode := range []string{"blocked", "not-started", "before", "after", "nan", "missing-time", "unknown-request", "wrong-task", "wrong-result", "no-invocation", "duplicate", "provisional"} {
		t.Run(mode, func(t *testing.T) {
			f := newOperationalObservationFixture(t)
			execution := &f.receipt.Resolutions[0].SelfExecutions[2]
			result := &execution.ObservationResults[0]
			switch mode {
			case "blocked":
				execution.Status = "blocked"
				execution.StartDay = nil
				execution.EndDay = nil
			case "not-started":
				execution.Status = "not_started"
				execution.StartDay = nil
				execution.EndDay = nil
			case "before":
				result.ObservedAtDay = selfTestDay(3)
			case "after":
				result.ObservedAtDay = selfTestDay(6)
			case "nan":
				result.ObservedAtDay = physicalTestNumber(math.NaN())
			case "missing-time":
				result.ObservedAtDay = nil
			case "unknown-request":
				result.RequestID = "foreign_request"
			case "wrong-task":
				execution.TaskID = "carry-tools"
			case "wrong-result":
				result.Result = "available_200Wh"
			case "no-invocation":
				f.receipt.Resolutions[0].MechanismRefs = nil
			case "duplicate":
				execution.ObservationResults = append(execution.ObservationResults, *result)
			case "provisional":
				f.receipt.Finalized = false
			}
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("unbound observation result accepted")
			}
		})
	}
	f := newOperationalObservationFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	r.Resolutions[0].PostState.OperationalObservations[0].Purpose = "自称已经确认200Wh无限容量"
	r.Resolutions[0].PostState.OperationalObservations[0].ID = CharacterOperationalObservationIDV1(r.Resolutions[0].PostState.OperationalObservations[0])
	r.Digest = ""
	if _, err := FinalizeWorldArbitrationReceipt(r, f.stimulus, f.activation, f.proposals, 1); err == nil {
		t.Fatal("re-signed free post-state observation bypassed deterministic derivation")
	}
}

func TestOperationalObservationV1RetainsOldProofAndDoesNotUseFreeWorldText(t *testing.T) {
	f := newOperationalObservationFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	actor := &state.Actors[0]
	for chapter := 2; chapter < 20; chapter++ {
		experience := CharacterSelfExperienceV2{Chapter: chapter, TaskID: fmt.Sprintf("unrelated_%d", chapter), Kind: "work", Action: "已完成独立例行工作", Status: "completed", StartDay: selfTestDay(float64(chapter * 10)), EndDay: selfTestDay(float64(chapter*10 + 1)), ProgressUnit: "minute", SourceProposalDigest: f.proposals[0].Digest}
		experience.ID = CharacterSelfExperienceIDV2(actor.AgentID, experience)
		actor.SelfExperiences = append(actor.SelfExperiences, experience)
	}
	actor.TaskProgress, err = deriveCharacterTaskProgressV2(actor.SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	// Mark the original ongoing work completed using a lawful later interval,
	// so only the operational-observation proof retention keeps its old source.
	extra := CharacterSelfExperienceV2{Chapter: 20, TaskID: "inspection", Kind: "work", Action: "执行本班机务安全检查", Status: "completed", StartDay: selfTestDay(200), EndDay: selfTestDay(234), ProgressUnit: "minute", ProgressTarget: physicalTestNumber(35), SourceProposalDigest: f.proposals[0].Digest}
	extra.ID = CharacterSelfExperienceIDV2(actor.AgentID, extra)
	actor.SelfExperiences = append(actor.SelfExperiences, extra)
	actor.TaskProgress, err = deriveCharacterTaskProgressV2(actor.SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	experiences, _, err := BuildCharacterSelfObservationV2(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, experience := range experiences {
		found = found || experience.ID == actor.OperationalObservations[0].SourceExperienceID
	}
	if !found {
		t.Fatal("recent history evicted the last operational result's execution proof")
	}
	observations, err := BuildCharacterOperationalObservationsV1(state, "ca_a")
	if err != nil || len(observations) != 1 || observations[0].Chapter != 1 {
		t.Fatal("old result age was reset or forgotten")
	}
	// Free-world prose by itself cannot create an observation under old policy.
	legacy := newSelfExperienceFixture(t)
	legacy.receipt.Resolutions[0].StateAfter = "电源已经确认可用，容量无限"
	old, err := finalizePhysicalFixture(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(old)
	if bytes.Contains(raw, []byte("operational_observations")) || bytes.Contains(raw, []byte("observation_results")) || bytes.Contains(raw, []byte("observation_requests")) {
		t.Fatal("legacy receipt acquired new fields")
	}
}

func TestOperationalObservationV1LatestSummaryReactivationAndPlannerView(t *testing.T) {
	f := newOperationalObservationFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := BuildCharacterOperationalObservationsV1(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	previous := CharacterObservationPacket{AgentID: "ca_a", Character: "甲", GenerationID: f.stimulus.GenerationID, Chapter: 2}
	current := previous
	current.OperationalObservations = operations
	reasons, err := CharacterReactivationReasons(previous, current)
	if err != nil || !physicalContainsRefV2(reasons, "self_execution_feedback") {
		t.Fatalf("new owner result did not trigger feedback: %v %v", reasons, err)
	}
	repeated := current
	repeated.GeneratedAt = "a later envelope timestamp"
	if reasons, err := CharacterReactivationReasons(current, repeated); err != nil || len(reasons) != 0 {
		t.Fatalf("unchanged observation woke owner again: %v %v", reasons, err)
	}
	newer := operations[0]
	newer.ObservedAtDay = selfTestDay(4.75)
	newer.Result = "unavailable"
	newer.ID = CharacterOperationalObservationIDV1(newer)
	history := append(append([]CharacterOperationalObservationV1(nil), operations...), newer)
	summary, err := selectCharacterOperationalObservationsV1(history)
	if err != nil || len(summary) != 1 || summary[0].ID != newer.ID || *operations[0].ObservedAtDay != *selfTestDay(4.5) {
		t.Fatalf("summary repeated history or changed original age: %+v %v", summary, err)
	}
	sim := ChapterWorldSimulation{Version: 2, GenerationID: f.stimulus.GenerationID, Chapter: 1, PhysicalState: &state, CharacterActivation: &CharacterActivationSimulationBinding{}, CharacterDecisionTrace: []CharacterActivationDecisionTrace{{Cycle: 1}}}
	view, err := CharacterActivationPlannerView(sim)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	if !bytes.Contains(raw, []byte(operations[0].ID)) || !bytes.Contains(raw, []byte("operational_observations")) || bytes.Contains(raw, []byte("\"self_experiences\"")) {
		t.Fatal("Planner final view dropped operational result or repeated full self history")
	}
}

func TestOperationalObservationV1UnavailableAndInconclusiveAreNotInventedCapacity(t *testing.T) {
	for _, result := range []string{"unavailable", "inconclusive"} {
		t.Run(result, func(t *testing.T) {
			f := newOperationalObservationFixture(t)
			f.receipt.Resolutions[0].SelfExecutions[2].ObservationResults[0].Result = result
			r, err := finalizePhysicalFixture(f)
			if err != nil {
				t.Fatal(err)
			}
			state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
			if err != nil {
				t.Fatal(err)
			}
			if state.Actors[0].OperationalObservations[0].Result != result {
				t.Fatal("restricted result was changed to availability")
			}
			for _, resource := range state.Resources {
				if resource.ResourceID == operationalPowerTestID && resource.ActualAmount != nil {
					t.Fatal("qualitative observation created a quantity")
				}
			}
		})
	}
}

func TestOperationalObservationV1SummaryBudgetFailsWithoutDroppingLatest(t *testing.T) {
	f := newOperationalObservationFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	base := state.Actors[0].OperationalObservations[0]
	var history []CharacterOperationalObservationV1
	for i := range CharacterOperationalObservationLimitV1 + 1 {
		item := base
		item.Purpose = fmt.Sprintf("本人申报用途%d", i)
		item.ID = CharacterOperationalObservationIDV1(item)
		history = append(history, item)
	}
	if _, err := selectCharacterOperationalObservationsV1(history); err == nil {
		t.Fatal("summary silently dropped latest owner observations to fit budget")
	}
	if len(history) != CharacterOperationalObservationLimitV1+1 {
		t.Fatal("budget check modified private history")
	}
}
