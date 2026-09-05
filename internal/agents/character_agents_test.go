package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type characterConcurrencyProbeModel struct {
	active atomic.Int32
	peak   atomic.Int32
}

func (m *characterConcurrencyProbeModel) response(messages []agentcore.Message) *agentcore.LLMResponse {
	for _, message := range messages {
		if message.Role == agentcore.RoleTool {
			return &agentcore.LLMResponse{Message: agentcore.Message{
				Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop,
				Content: []agentcore.ContentBlock{agentcore.TextBlock("决定已提交。")},
			}}
		}
	}
	active := m.active.Add(1)
	defer m.active.Add(-1)
	for {
		peak := m.peak.Load()
		if active <= peak || m.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID: "decision", Name: "submit_character_decision", Args: json.RawMessage(`{
				"location":"车站","current_goal":"赶车","pressure":"时间紧迫",
				"available_options":["登车","留下"],"decision":"登车",
				"decision_reason":"车票有效且时间紧迫","intended_action":"检票登车",
				"action_duration":"三分钟","knowledge_refs":["fact-1"]
			}`),
		})},
	}}
}

func (m *characterConcurrencyProbeModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.response(messages), nil
}

func (m *characterConcurrencyProbeModel) GenerateStream(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response := m.response(messages)
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}

func (*characterConcurrencyProbeModel) SupportsTools() bool { return true }

func TestCharacterActivationReasonsCoverEventDrivenTriggers(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 5, Title: "顾岚收到消息", CoreEvent: "顾岚赶往车站"}}); err != nil {
		t.Fatal(err)
	}
	profile := characterAgentProfile{
		Character: domain.Character{Name: "顾岚", Tier: "core"},
		Agenda:    &domain.CharacterAgenda{Name: "顾岚", CurrentGoal: "赶到车站", Status: "active", LastAdvancedChapter: 4},
		Continuity: &domain.CharacterContinuityEntry{
			Name:       "顾岚",
			ReturnPlan: domain.CharacterReturnPlan{SuggestedChapter: 5, ReturnPriority: "required"},
			FutureUses: []domain.CharacterFutureUse{{Chapter: 5, UsageType: "outline_return", Action: "兑现承诺"}},
			Dynamics:   domain.CharacterDynamicsProfile{RelationshipForces: []string{"欠周宁一次人情"}},
		},
	}
	mutation := func(field string) domain.StateMutationV2 {
		return domain.StateMutationV2{Subject: "顾岚", Field: field}
	}
	projected := domain.ProjectedPlanningContextV2{
		RecentTransitions: []domain.ProjectedPlanningTransitionV2{{Delta: domain.ProjectedDelta{
			Resources: []domain.StateMutationV2{mutation("车票")}, Relationships: []domain.StateMutationV2{mutation("信任")}, Knowledge: []domain.StateMutationV2{mutation("消息")},
		}}},
		OpenObligations: []domain.ProjectedPlanningObligationV2{{Contract: "顾岚必须兑现承诺", DueNow: true}},
	}
	reasons := characterActivationReasons(st, profile, "顾岚", 5, projected)
	for _, want := range []string{
		"protagonist", "scene_appearance", "due_action", "return_due", "relationship_or_promise_trigger",
		"resource_change", "relationship_change", "received_information", "commitment_trigger",
	} {
		if !containsAgentString(reasons, want) {
			t.Errorf("missing activation reason %q in %v", want, reasons)
		}
	}
}

func TestCharacterAgentRosterExcludesDecorativeAndCrowd(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "顾岚", Role: "主角", Tier: "core"},
		{Name: "周宁", Role: "盟友", Tier: "important"},
		{Name: "店员", Role: "路人", Tier: "decorative"},
		{Name: "围观群众", Role: "群众", Tier: "important"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 5, Title: "周宁登场", CoreEvent: "顾岚与周宁会合"}}); err != nil {
		t.Fatal(err)
	}
	profiles, protagonist, err := selectCharacterAgentRoster(st, 5, ProjectedArcBoundary{Volume: 1, Arc: 1, Title: "会合", Goal: "结盟", FirstChapter: 5, LastChapter: 5, BookLastChapter: 5})
	if err != nil {
		t.Fatal(err)
	}
	if protagonist != "顾岚" || len(profiles) != 2 {
		t.Fatalf("unexpected active-core roster protagonist=%q profiles=%+v", protagonist, profiles)
	}
	for _, profile := range profiles {
		if profile.Character.Name == "店员" || profile.Character.Name == "围观群众" {
			t.Fatalf("decorative/crowd character received an agent: %+v", profile.Character)
		}
	}
}

func TestCharacterProposalRoundBatchesMoreThanEightAtConcurrencyFour(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	observations := make(map[string]domain.CharacterObservationPacket)
	ids := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		agentID := fmt.Sprintf("ca_%02d", i)
		observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
			Version: domain.CharacterObservationVersion, GenerationID: "pg2_concurrency", Chapter: 1, Round: 1,
			AgentID: agentID, Character: fmt.Sprintf("角色%d", i), CurrentGoal: "赶车", Pressure: "时间紧迫",
			StimulusDigest: "sha256:stimulus", KnownFacts: []domain.CharacterAgentFact{{ID: "fact-1", Kind: "known", Text: "车票有效"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CharacterAgents.SaveObservation(observation); err != nil {
			t.Fatal(err)
		}
		observations[agentID] = observation
		ids = append(ids, agentID)
	}
	model := &characterConcurrencyProbeModel{}
	proposals, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{MaxConcurrency: 4}}, st, model, observations, ids, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 10 {
		t.Fatalf("cast was truncated at an old hard limit: got %d proposals", len(proposals))
	}
	if peak := model.peak.Load(); peak < 2 || peak > 4 {
		t.Fatalf("unexpected character-agent concurrency peak %d", peak)
	}
}

func TestWorldStimulusCarriesOperationalWorldAndRedactsSecretMechanisms(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorldCodex(domain.WorldCodex{
		Mechanisms: []domain.CodexMechanism{
			{ID: "public-route", Name: "公开通行", Visibility: "formal"},
			{ID: "secret-toll", Name: "隐秘追缴", Visibility: "secret"},
		},
		CounterfactualTests: []domain.CodexCounterfactualProbe{{ID: "no-pass", MechanismRefs: []string{"public-route"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{
		Version: 1, Name: "凭证城",
		Places:   []domain.WorldPlace{{ID: "gate", Name: "城门"}, {ID: "market", Name: "集市"}},
		Routes:   []domain.WorldRoute{{From: "gate", To: "market", TravelDays: 0.1, Risk: "夜间关闭"}},
		Factions: []domain.WorldFaction{{ID: "guards", Name: "守门人", Resources: []string{"登记簿"}}},
	}); err != nil {
		t.Fatal(err)
	}
	stimulus, err := buildWorldStimulus(st, "pg2_world", 1, ProjectedArcBoundary{Goal: "进城"}, domain.ProjectedPlanningContextV2{}, nil, "now")
	if err != nil {
		t.Fatal(err)
	}
	if stimulus.OperationalWorld == nil || len(stimulus.OperationalWorld.Routes) != 1 || len(stimulus.Mechanisms) != 2 || len(stimulus.CounterfactualTests) != 1 {
		t.Fatalf("stimulus lost operational contracts: %+v", stimulus)
	}
	observation, err := buildCharacterObservation(st, stimulus.GenerationID, 1, characterAgentProfile{
		Character: domain.Character{Name: "林默", Role: "主角", Tier: "core"},
		Record:    domain.CharacterAgentRecord{AgentID: "ca_linmo", Character: "林默"},
	}, stimulus, domain.ProjectedPlanningContextV2{}, "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.PublicMechanisms) != 1 || observation.PublicMechanisms[0].ID != "public-route" {
		t.Fatalf("secret mechanism visibility leak: %+v", observation.PublicMechanisms)
	}
}

func TestWorldStimulusV2RequiresCoherenceProof(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{Version: domain.CurrentBookWorldSchemaVersion, Name: "未审世界"}); err != nil {
		t.Fatal(err)
	}
	if _, err := buildWorldStimulus(st, "pg2_unverified", 1, ProjectedArcBoundary{}, domain.ProjectedPlanningContextV2{}, nil, "now"); err == nil {
		t.Fatal("v2 operational world entered arbitration without a coherence proof")
	}
}

func containsAgentString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
