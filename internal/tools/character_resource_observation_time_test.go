package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestResourceObservationTimeSchemaIsExplicitAndKeepsLegacySnapshots(t *testing.T) {
	for _, tc := range []struct {
		name, submitHash, resolveHash string
		sources                       []string
	}{
		{"v2", "d6258b930faa876d7ee9ec13b4831002a941135b2b7165933ef5713d94d39cd6", "8f495a5f1cc71f8c2242b96695114db23f304e8a9cc6726e971426832c3a5c0a", nil},
		{"self", "14004f0257ce8b4f3e770f5d6f282732340e5e46194ca948e003cc80a1776c48", "5210097c2775a20b8ace10236989d5fd87c134317315012e1ca502f00859b6d7", []string{domain.CharacterSelfExperiencePolicyV2}},
	} {
		submit := NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: tc.sources}).Schema()
		resolve := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: tc.sources}, domain.CharacterAgentActivation{}, nil, "", nil, 1).Schema()
		for _, schema := range []struct {
			name, want string
			value      map[string]any
		}{{"submit", tc.submitHash, submit}, {"resolve", tc.resolveHash, resolve}} {
			digest, err := domain.DeterministicPlanningHash(schema.value)
			if err != nil {
				t.Fatal(err)
			}
			if digest != schema.want {
				t.Fatalf("unmarked %s %s schema changed: got %s want %s", tc.name, schema.name, digest, schema.want)
			}
		}
	}
	sources := []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterResourceObservationTimePolicyV1}
	submit := NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: sources}).Schema()
	measurement := submit["properties"].(map[string]any)["resource_measurements"].(map[string]any)["items"].(map[string]any)
	if measurement["properties"].(map[string]any)["task_id"] == nil || !containsString(measurement["required"].([]string), "task_id") {
		t.Fatal("timed measurement does not require its own work task")
	}
	resolve := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: sources}, domain.CharacterAgentActivation{}, nil, "", nil, 1).Schema()
	properties := resolve["properties"].(map[string]any)
	settlement := properties["resource_settlements"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if settlement["start_day"] == nil || settlement["end_day"] == nil {
		t.Fatal("timed settlement lost its explicit interval")
	}
	post := properties["resolutions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["post_state"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"resource_updates", "resources"} {
		perception := post[field].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["perception"].(map[string]any)["properties"].(map[string]any)
		if perception["observed_at_day"] == nil {
			t.Fatalf("timed %s perception lost its observation timestamp", field)
		}
	}
}

const observationTimeToolFuel = "res_2222222222222222"

func resourceObservationTimeToolFixture(t *testing.T, timed bool) (*store.Store, domain.CharacterObservationPacket, domain.WorldStimulusPacket, domain.CharacterAgentActivation, map[string]any) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	st := store.NewStore(t.TempDir())
	must(st.Init())
	must(st.Progress.Init("测量时点", 1))
	registry, actor, err := (domain.CharacterAgentRegistry{}).UpsertCharacter("周砚", nil, "core", 1, "")
	must(err)
	registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	must(err)
	four := 4.0
	physical, err := domain.PrepareCharacterSelfExperienceStateV2(domain.WorldPhysicalStateV2{
		Version:   domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{{ResourceID: observationTimeToolFuel, Name: "作者燃油余额", Unit: "L", ActualAmount: &four}},
		Actors: []domain.CharacterPhysicalStateV2{{AgentID: actor.AgentID, Character: actor.Character, Location: "船上", Resources: []domain.CharacterResourceHoldingV2{{
			ResourceID: observationTimeToolFuel, PerceivedName: "燃油", PerceivedLabel: "船用燃油", PerceivedUnit: "L", Access: "shared",
			Perception:   domain.ResourcePerceptionV2{Kind: "estimated", EstimateMin: &four, EstimateMax: &four, AsOfChapter: 0, EvidenceRefs: []string{"opening-fuel-estimate"}},
			EvidenceRefs: []string{"opening-fuel-access"},
		}}}},
	})
	must(err)
	sources := []string{domain.CharacterSelfExperiencePolicyV2}
	if timed {
		sources = append(sources, domain.CharacterResourceObservationTimePolicyV1)
	}
	clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{CurrentDay: 0, DurationDaysMin: 1, DurationDaysMax: 1, TimeContractCoreDigest: "sha256:" + strings.Repeat("b", 64)})
	must(err)
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{
		Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_observation_time_tool", Chapter: 1, TimeWindow: "实际五分钟", StoryClock: &clock, PhysicalState: &physical, Sources: sources,
		Mechanisms: []domain.CodexMechanism{{ID: "gauge", Name: "现场读表", Visibility: "formal"}, {ID: "burn", Name: "正常耗油", Visibility: "formal"}},
	})
	must(err)
	views, err := domain.BuildCharacterResourceViewsV2(physical, actor.AgentID)
	must(err)
	experiences, progress, err := domain.BuildCharacterSelfObservationV2(physical, actor.AgentID)
	must(err)
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		Version: domain.CharacterObservationV2Version, GenerationID: stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character,
		Location: "船上", CurrentGoal: "先测量，再按需运行设备", Pressure: "准确区分读数与后续耗用", StimulusDigest: stimulus.Digest,
		Sources: sources, ResourceViews: views, SelfExperiences: experiences, TaskProgress: progress, PublicMechanisms: stimulus.Mechanisms,
		KnownFacts: []domain.CharacterAgentFact{{ID: "known-work", Kind: "known", Text: "本人可读燃油表并运行现有设备"}},
	})
	must(err)
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: stimulus.GenerationID, Chapter: 1, RegistryRoot: registry.RegistryRoot,
		Entries: []domain.CharacterAgentActivationEntry{{AgentID: actor.AgentID, Character: actor.Character, State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	must(err)
	must(st.CharacterAgents.SaveRegistrySnapshot(stimulus.GenerationID, 1, registry))
	must(st.CharacterAgents.SaveStimulus(stimulus))
	must(st.CharacterAgents.SaveObservation(observation))
	must(st.CharacterAgents.SaveActivation(activation))
	measurement := map[string]any{"resource_id": observationTimeToolFuel, "mechanism_ref": "gauge"}
	if timed {
		measurement["task_id"] = "read_gauge"
	}
	proposalArgs := map[string]any{
		"location": observation.Location, "current_goal": observation.CurrentGoal, "pressure": observation.Pressure,
		"available_options": []string{"先读表后运行", "暂停运行"}, "decision": "先读表后运行", "decision_reason": "记录当时读数后按需运行",
		"intended_action": "先完成燃油表核读，再运行设备四分钟", "action_duration": "实际五分钟", "knowledge_refs": []string{"known-work"}, "mechanism_refs": []string{"gauge", "burn"},
		"resource_measurements": []map[string]any{measurement},
		"self_tasks": []map[string]any{
			{"task_id": "read_gauge", "kind": "work", "action": "核读燃油表", "resource_ids": []string{observationTimeToolFuel}, "progress_target": 1, "progress_unit": "minute", "knowledge_refs": []string{"known-work"}},
			{"task_id": "run_equipment", "kind": "work", "action": "运行现有设备", "resource_ids": []string{observationTimeToolFuel}, "progress_target": 4, "progress_unit": "minute", "knowledge_refs": []string{"known-work"}},
		},
	}
	return st, observation, stimulus, activation, proposalArgs
}

func submitResourceObservationTimeToolFixture(t *testing.T, timed bool) (*store.Store, *ResolveChapterWorldTool, map[string]any, domain.CharacterDecisionProposal) {
	t.Helper()
	st, observation, stimulus, activation, proposalArgs := resourceObservationTimeToolFixture(t, timed)
	raw, _ := json.Marshal(proposalArgs)
	if _, err := NewSubmitCharacterDecisionTool(st, observation).Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	proposal, err := st.CharacterAgents.LoadProposal(stimulus.GenerationID, 1, 1, observation.AgentID)
	if err != nil || proposal == nil {
		t.Fatalf("submitted measurement was not persisted: %v", err)
	}
	zero, one, five := 0.0, 1.0/1440, 5.0/1440
	perception := map[string]any{"kind": "last_observed", "amount": 4, "as_of_chapter": 1, "evidence_refs": []string{proposal.Digest}}
	settlement := map[string]any{"resource_id": observationTimeToolFuel, "before": 4, "delta": -4, "after": 0, "evidence_refs": []string{proposal.Digest}}
	if timed {
		perception["observed_at_day"] = one
		settlement["start_day"], settlement["end_day"] = one, five
	} else {
		perception["amount"] = 0 // Preserve the historical end-balance-only contract when unmarked.
	}
	args := map[string]any{
		"time_window": stimulus.TimeWindow, "story_time": domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: zero, EndDay: five}, "hard_contract_status": "feasible", "finalized": true,
		"resource_settlements": []map[string]any{settlement},
		"resolutions": []map[string]any{{
			"agent_id": proposal.AgentID, "character": proposal.Character, "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction,
			"action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "先实际核读，再完成设备运行", "state_after": "按实际时点分别保存读数和余额", "mechanism_refs": []string{"gauge", "burn"},
			"self_executions":   []map[string]any{{"task_id": "read_gauge", "status": "completed", "start_day": zero, "end_day": one}, {"task_id": "run_equipment", "status": "completed", "start_day": one, "end_day": five}},
			"post_state":        map[string]any{"location": proposal.Location, "resource_updates": []map[string]any{{"resource_id": observationTimeToolFuel, "perception": perception}}},
			"butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "运行消耗燃油", TransmissionPath: "实际设备运行", ArrivalChapter: 1, ProtagonistImpact: "后续依据真实资源与原时点读数行动"}},
		}},
		"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: proposal.Character, AvailableOptions: proposal.AvailableOptions, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, PlanConstraints: []string{"保持读表先于运行的真实顺序"}, CausalChain: []string{"实际读表之后才耗用燃油"}},
	}
	return st, NewResolveChapterWorldTool(st, stimulus, activation, []domain.CharacterDecisionProposal{*proposal}, "sha256:"+strings.Repeat("a", 64), nil, 1), args, *proposal
}

func TestResourceObservationTimeToolsPersistReadingBeforeLaterConsumption(t *testing.T) {
	for _, timed := range []bool{false, true} {
		name := "legacy_unmarked"
		if timed {
			name = "timed_reading"
		}
		t.Run(name, func(t *testing.T) {
			st, tool, args, proposal := submitResourceObservationTimeToolFixture(t, timed)
			raw, _ := json.Marshal(args)
			if _, err := tool.Execute(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			receipt, err := st.CharacterAgents.LoadArbitration(proposal.GenerationID, 1, 1)
			if err != nil || receipt == nil {
				t.Fatalf("timed arbitration missing: %v", err)
			}
			sim, err := st.LoadChapterWorldSimulation(1)
			if err != nil || sim == nil || sim.PhysicalState == nil {
				t.Fatalf("timed simulation missing: %v", err)
			}
			if *sim.PhysicalState.Resources[0].ActualAmount != 0 {
				t.Fatal("world failed to retain the later consumption")
			}
			perception := receipt.Resolutions[0].PostState.Resources[0].Perception
			if timed {
				if proposal.ResourceMeasurements[0].TaskID != "read_gauge" || perception.Amount == nil || *perception.Amount != 4 || perception.ObservedAtDay == nil || *perception.ObservedAtDay != 1.0/1440 {
					t.Fatalf("earlier measurement was replaced with the cycle-end balance: %+v", perception)
				}
				segment := receipt.ResourceSettlements[0]
				if segment.StartDay == nil || segment.EndDay == nil || *segment.StartDay != 1.0/1440 || *segment.EndDay != 5.0/1440 {
					t.Fatalf("settlement time interval did not survive storage: %+v", segment)
				}
			} else if proposal.ResourceMeasurements[0].TaskID != "" || perception.ObservedAtDay != nil || perception.Amount == nil || *perception.Amount != 0 {
				t.Fatalf("unmarked legacy behavior changed: %+v", perception)
			}
			views, err := domain.BuildCharacterResourceViewsV2(*sim.PhysicalState, proposal.AgentID)
			ownerPerception := perception
			ownerPerception.EvidenceRefs = domain.CharacterSourceRefsV2(proposal.AgentID, ownerPerception.EvidenceRefs)
			if err != nil || len(views) != 1 || !reflect.DeepEqual(views[0].Perception, ownerPerception) {
				t.Fatalf("reading timestamp changed in the later owner view: %v", err)
			}
		})
	}
}

func TestResourceObservationTimeToolsRejectUnboundOrInterpolatedReadings(t *testing.T) {
	for _, mode := range []string{"measurement_missing_task", "measurement_unmarked_task"} {
		t.Run(mode, func(t *testing.T) {
			st, observation, _, _, args := resourceObservationTimeToolFixture(t, mode == "measurement_missing_task")
			measurement := args["resource_measurements"].([]map[string]any)[0]
			if mode == "measurement_missing_task" {
				delete(measurement, "task_id")
			} else {
				measurement["task_id"] = "read_gauge"
			}
			before := arbitrationReferenceFiles(t, st.Dir())
			raw, _ := json.Marshal(args)
			if _, err := NewSubmitCharacterDecisionTool(st, observation).Execute(context.Background(), raw); !errors.Is(err, errs.ErrToolPrecondition) {
				t.Fatalf("measurement task did not obey its explicit policy: %v", err)
			}
			if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
				t.Fatal("rejected measurement request wrote a proposal")
			}
		})
	}
	for _, mode := range []string{"future_balance", "missing_observation_time", "wrong_task_end", "interval_interior", "missing_settlement_time", "legacy_timed_fields"} {
		t.Run(mode, func(t *testing.T) {
			st, tool, args, _ := submitResourceObservationTimeToolFixture(t, mode != "legacy_timed_fields")
			resolution := args["resolutions"].([]map[string]any)[0]
			perception := resolution["post_state"].(map[string]any)["resource_updates"].([]map[string]any)[0]["perception"].(map[string]any)
			settlement := args["resource_settlements"].([]map[string]any)[0]
			switch mode {
			case "future_balance":
				perception["amount"] = 0
			case "missing_observation_time":
				delete(perception, "observed_at_day")
			case "wrong_task_end":
				perception["observed_at_day"] = 5.0 / 1440
			case "interval_interior":
				perception["observed_at_day"] = 3.0 / 1440
				resolution["self_executions"].([]map[string]any)[0]["end_day"] = 3.0 / 1440
				resolution["self_executions"].([]map[string]any)[1]["start_day"] = 3.0 / 1440
			case "missing_settlement_time":
				delete(settlement, "start_day")
			case "legacy_timed_fields":
				perception["observed_at_day"] = 1.0 / 1440
				settlement["start_day"], settlement["end_day"] = 1.0/1440, 5.0/1440
			}
			before := arbitrationReferenceFiles(t, st.Dir())
			raw, _ := json.Marshal(args)
			if _, err := tool.Execute(context.Background(), raw); !errors.Is(err, errs.ErrToolPrecondition) {
				t.Fatalf("invalid measurement chronology was accepted: %v", err)
			}
			if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
				t.Fatal("rejected time candidate changed files")
			}
		})
	}
}
