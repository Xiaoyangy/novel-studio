package tools

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const selfToolResourceID = "res_1111111111111111"

func selfToolInitialState(t *testing.T) domain.WorldPhysicalStateV2 {
	t.Helper()
	state, err := domain.PrepareCharacterSelfExperienceStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{{ResourceID: selfToolResourceID, Name: "工具作者目录"}},
		Actors:    []domain.CharacterPhysicalStateV2{{AgentID: "ca_zhou", Character: "周砚", Location: "机修棚", Resources: []domain.CharacterResourceHoldingV2{{ResourceID: selfToolResourceID, PerceivedName: "机修棚内正在整理的手工具", PerceivedLabel: "常规手工具", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func selfToolRound(t *testing.T, st *store.Store, before domain.WorldPhysicalStateV2, chapter int, start, end float64, tasks []domain.CharacterSelfTaskV2, executions []domain.CharacterSelfExecutionV2) (*ResolveChapterWorldTool, map[string]any, domain.CharacterObservationPacket) {
	t.Helper()
	registry, err := domain.FinalizeCharacterAgentRegistry(domain.CharacterAgentRegistry{Entries: []domain.CharacterAgentRecord{{AgentID: "ca_zhou", Character: "周砚", Tier: "core", Status: domain.CharacterAgentActive, MemoryVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_self_tool_test", Chapter: chapter, TimeWindow: "本轮实际时段", PhysicalState: &before, Sources: []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2}})
	if err != nil {
		t.Fatal(err)
	}
	views, err := domain.BuildCharacterResourceViewsV2(*stimulus.PhysicalState, "ca_zhou")
	if err != nil {
		t.Fatal(err)
	}
	experiences, progress, err := domain.BuildCharacterSelfObservationV2(*stimulus.PhysicalState, "ca_zhou")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: stimulus.GenerationID, Chapter: chapter, Round: 1, AgentID: "ca_zhou", Character: "周砚", Location: before.Actors[0].Location, CurrentGoal: "完成本班机务检查", Pressure: "离泊窗口有限", StimulusDigest: stimulus.Digest, ResourceViews: views, SelfExperiences: experiences, TaskProgress: progress, Sources: stimulus.Sources, KnownFacts: []domain.CharacterAgentFact{{ID: "known-work", Kind: "known", Text: "本班需要三十五分钟有效检查，工具属于本人"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveObservation(observation); err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for _, task := range tasks {
		actions = append(actions, task.Action)
	}
	args, _ := json.Marshal(map[string]any{"location": observation.Location, "current_goal": observation.CurrentGoal, "pressure": observation.Pressure, "available_options": []string{"按本人任务执行", "留在原处等待"}, "decision": "按本人任务执行", "decision_reason": "履行本岗职责且不跳过实际步骤", "intended_action": strings.Join(actions, "；"), "action_duration": "按本轮实际时段执行", "knowledge_refs": []string{"known-work"}, "self_tasks": tasks})
	if _, err := NewSubmitCharacterDecisionTool(st, observation).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	proposal, err := st.CharacterAgents.LoadProposal(stimulus.GenerationID, chapter, 1, "ca_zhou")
	if err != nil || proposal == nil {
		t.Fatalf("missing self task proposal: %v", err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: stimulus.GenerationID, Chapter: chapter, RegistryRoot: registry.RegistryRoot, Entries: []domain.CharacterAgentActivationEntry{{AgentID: "ca_zhou", Character: "周砚", State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{func() error { return st.CharacterAgents.SaveRegistrySnapshot(stimulus.GenerationID, chapter, registry) }, func() error { return st.CharacterAgents.SaveStimulus(stimulus) }, func() error { return st.CharacterAgents.SaveActivation(activation) }} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	input := map[string]any{"time_window": "本轮实际时段", "story_time": domain.StoryTimeChapterSchedule{Chapter: chapter, StartDay: start, EndDay: end}, "resource_settlements": []any{}, "hard_contract_status": "feasible", "finalized": true,
		"resolutions":            []map[string]any{{"agent_id": "ca_zhou", "character": "周砚", "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction, "action_order": 1, "outcome": "partial", "completion_state": "in_progress", "immediate_result": "按原提案实际执行，不预认合格", "state_after": "世界侧叙述不作为本人进度来源", "self_executions": executions, "post_state": map[string]any{"location": "停泊的渡船", "resource_updates": []any{}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "已做工序改变后续工时安排", TransmissionPath: "本岗实际作业", ArrivalChapter: chapter, Visibility: "visible", ProtagonistImpact: "必须继续剩余工序"}}}},
		"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "周砚", AvailableOptions: proposal.AvailableOptions, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, PlanConstraints: []string{"尚未机务合格"}, CausalChain: []string{"实际执行本人任务改变进度"}}}
	return NewResolveChapterWorldTool(st, stimulus, activation, []domain.CharacterDecisionProposal{*proposal}, "sha256:"+strings.Repeat("a", 64), nil, 1), input, observation
}

func TestSelfExecutionToolsCarryAndWorkReachNextObservationWithoutInventedTotals(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("本人经历", 2); err != nil {
		t.Fatal(err)
	}
	target := 35.0
	zero, four, five, nine := 0.0, 4.0/1440, 5.0/1440, 9.0/1440
	work := domain.CharacterSelfTaskV2{TaskID: "boat_check", Kind: "work", Action: "执行本班机务检查", ProgressTarget: &target, ProgressUnit: "minute", ResourceIDs: []string{selfToolResourceID}, KnowledgeRefs: []string{"known-work"}}
	carry := domain.CharacterSelfTaskV2{TaskID: "carry_tools", Kind: "carry", Action: "明确携手工具到渡船", ResourceIDs: []string{selfToolResourceID}, KnowledgeRefs: []string{"known-work"}}
	tool, input, _ := selfToolRound(t, st, selfToolInitialState(t), 1, zero, five, []domain.CharacterSelfTaskV2{carry, work}, []domain.CharacterSelfExecutionV2{{TaskID: carry.TaskID, Status: "completed", StartDay: &zero, EndDay: &four}, {TaskID: work.TaskID, Status: "in_progress", StartDay: &four, EndDay: &five}})
	badRaw, _ := json.Marshal(input)
	var bad map[string]any
	_ = json.Unmarshal(badRaw, &bad)
	bad["resolutions"].([]any)[0].(map[string]any)["post_state"].(map[string]any)["task_progress"] = []any{map[string]any{"completed": 35}}
	badRaw, _ = json.Marshal(bad)
	beforeRoot, _ := store.DirectoryContentRoot(st.Dir())
	if _, err := tool.Execute(context.Background(), badRaw); err == nil {
		t.Fatal("model could directly author cumulative progress")
	}
	afterRoot, _ := store.DirectoryContentRoot(st.Dir())
	if beforeRoot != afterRoot {
		t.Fatal("forged derived state wrote receipts")
	}
	raw, _ := json.Marshal(input)
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	sim, err := st.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil || sim.PhysicalState == nil {
		t.Fatalf("self simulation missing: %v", err)
	}
	receipt, err := st.CharacterAgents.LoadArbitration(tool.stimulus.GenerationID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Resolutions[0].PostState.TaskProgress) != 1 || math.Abs(receipt.Resolutions[0].PostState.TaskProgress[0].Completed-1) > 1e-8 {
		t.Fatal("receipt did not retain derived one minute progress")
	}
	priorProgress := sim.PhysicalState.Actors[0].TaskProgress[0]
	work.TaskID, work.Action, work.ProgressUnit, work.ProgressTarget = priorProgress.TaskID, priorProgress.Action, priorProgress.Unit, priorProgress.Target
	work.KnowledgeRefs = []string{priorProgress.SourceExperienceID}
	second, input2, observation := selfToolRound(t, st, *sim.PhysicalState, 2, five, nine, []domain.CharacterSelfTaskV2{work}, []domain.CharacterSelfExecutionV2{{TaskID: work.TaskID, Status: "in_progress", StartDay: &five, EndDay: &nine}})
	if len(observation.TaskProgress) != 1 || math.Abs(observation.TaskProgress[0].Completed-1) > 1e-8 || *observation.TaskProgress[0].Target != 35 {
		t.Fatal("next observation reset work progress or replaced target with remaining time")
	}
	view := observation.ResourceViews[0]
	if view.Name != "常规手工具" || view.KnownPlacement == nil || view.KnownPlacement.Kind != "with_actor" || view.KnownPlacement.Location != "停泊的渡船" {
		t.Fatalf("next observation lost actual carry: %+v", view)
	}
	encoded, _ := json.Marshal(observation)
	if strings.Contains(string(encoded), "机修棚内正在整理") {
		t.Fatal("stale dynamic resource name survived in owner observation")
	}
	raw, _ = json.Marshal(input2)
	if _, err := second.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	last, err := st.LoadChapterWorldSimulation(2)
	if err != nil {
		t.Fatal(err)
	}
	if got := last.PhysicalState.Actors[0].TaskProgress[0].Completed; math.Abs(got-5) > 1e-8 {
		t.Fatalf("continued work should total five actual minutes, got %g", got)
	}
}

func TestSelfExecutionSchemaRequiresExplicitIntentAndDoesNotExposeDerivedInputs(t *testing.T) {
	obs := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: []string{domain.CharacterSelfExperiencePolicyV2}}
	submit := NewSubmitCharacterDecisionTool(nil, obs).Schema()
	if !containsString(submit["required"].([]string), "self_tasks") {
		t.Fatal("self tasks missing from actual input schema")
	}
	resolve := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: obs.Sources}, domain.CharacterAgentActivation{}, nil, "", nil, 1).Schema()
	if !containsString(resolve["required"].([]string), "story_time") {
		t.Fatal("self execution schema permits absent actual time")
	}
	raw, _ := json.Marshal(resolve)
	for _, field := range []string{`"known_placement":`, `"task_progress":`, `"self_experiences":`} {
		if strings.Contains(string(raw), field) {
			t.Fatal("derived self state became arbiter input")
		}
	}
}
