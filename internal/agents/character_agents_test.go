package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	calls  atomic.Int32
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
	m.calls.Add(1)
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

func seedCharacterRound(t *testing.T, count int) (*store.Store, map[string]domain.CharacterObservationPacket, []string) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	observations := make(map[string]domain.CharacterObservationPacket, count)
	ids := make([]string, 0, count)
	for i := range count {
		id := fmt.Sprintf("ca_resume_%02d", i)
		observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
			Version: domain.CharacterObservationVersion, GenerationID: "pg2_resume", Chapter: 1, Round: 1,
			AgentID: id, Character: fmt.Sprintf("角色%d", i), CurrentGoal: "赶车", Pressure: "时间紧迫",
			StimulusDigest: "sha256:stimulus", KnownFacts: []domain.CharacterAgentFact{{ID: "fact-1", Kind: "known", Text: "车票有效"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CharacterAgents.SaveObservation(observation); err != nil {
			t.Fatal(err)
		}
		observations[id] = observation
		ids = append(ids, id)
	}
	return st, observations, ids
}

func TestCharacterProposalRoundDeduplicatesAndResumesOnlyMissing(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 3)
	model := &characterConcurrencyProbeModel{}
	if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, observations, ids[:1], 1); err != nil {
		t.Fatal(err)
	}
	requested := []string{ids[0], ids[1], ids[1], ids[2], ids[0]}
	proposals, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, observations, requested, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 3 || model.calls.Load() != 3 {
		t.Fatalf("expected one provider call and proposal per unique character, proposals=%d calls=%d", len(proposals), model.calls.Load())
	}
	if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, observations, requested, 1); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 3 {
		t.Fatal("completed proposal resume called the model again")
	}
}

func TestCharacterProposalRoundRejectsInvalidBatchBeforeModelCalls(t *testing.T) {
	for _, invalid := range []string{"missing", "identity", "generation", "round", "tampered", "unpersisted"} {
		t.Run(invalid, func(t *testing.T) {
			st, observations, ids := seedCharacterRound(t, 2)
			last := observations[ids[1]]
			switch invalid {
			case "missing":
				delete(observations, ids[1])
			case "identity":
				last.AgentID = ids[0]
				observations[ids[1]] = last
			case "generation":
				last.GenerationID = "pg2_other"
				observations[ids[1]] = last
			case "round":
				last.Round = 2
				observations[ids[1]] = last
			case "tampered":
				last.CurrentGoal = "未落盘的新目标"
				observations[ids[1]] = last
			case "unpersisted":
				last.CurrentGoal = "未落盘的新目标"
				last, err := domain.FinalizeCharacterObservationPacket(last)
				if err != nil {
					t.Fatal(err)
				}
				observations[ids[1]] = last
			}
			model := &characterConcurrencyProbeModel{}
			if _, err := runCharacterProposalRoundWithModel(context.Background(), bootstrap.Config{}, st, model, observations, ids, 1); err == nil {
				t.Fatal("invalid batch was accepted")
			}
			if model.calls.Load() != 0 || model.active.Load() != 0 {
				t.Fatal("invalid evidence batch started model work")
			}
		})
	}
}

type characterCancellationProbeModel struct {
	entered chan struct{}
	active  atomic.Int32
}

func (m *characterCancellationProbeModel) Generate(ctx context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.active.Add(1)
	defer m.active.Add(-1)
	select {
	case m.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *characterCancellationProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	_, err := m.Generate(ctx, messages, specs, opts...)
	return nil, err
}

func (*characterCancellationProbeModel) SupportsTools() bool { return true }

func TestCharacterProposalRoundCancellationDrainsWorkers(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 12)
	model := &characterCancellationProbeModel{entered: make(chan struct{}, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runCharacterProposalRoundWithModel(ctx, bootstrap.Config{}, st, model, observations, ids, 1)
		done <- err
	}()
	select {
	case <-model.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model worker never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled round did not drain its workers")
	}
	if model.active.Load() != 0 {
		t.Fatal("round returned with model work still active")
	}
	if proposals, err := st.CharacterAgents.LoadProposals("pg2_resume", 1, 1, ids); err != nil || len(proposals) != 0 {
		t.Fatalf("canceled round persisted decisions: proposals=%d err=%v", len(proposals), err)
	}
}

func TestWorldStimulusCarriesOperationalWorldAndRedactsSecretMechanisms(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorldCodex(domain.WorldCodex{
		Mechanisms: []domain.CodexMechanism{
			{ID: "public-route", Name: "公开通行", Visibility: "formal", CharacterView: &domain.CharacterMechanismView{Name: "公开通行"}},
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

func TestCharacterObservationDoesNotExposeAuthoredCharacterArc(t *testing.T) {
	const characterArc = "第二章拆封失去资格，第三章证明父亲只领六十升并原谅许岚。"
	const dossierArc = "第三章证实许岚挪用三十升，完成全部人物成长。"
	for _, currentAction := range []string{"", "核对眼前档案袋封条"} {
		st := store.NewStore(t.TempDir())
		profile := characterAgentProfile{
			Character: domain.Character{Name: "林澄", Role: "值班员", Arc: characterArc, Traits: []string{"谨慎"}},
			Record:    domain.CharacterAgentRecord{AgentID: "ca_lin", Character: "林澄"},
			Dossier: &domain.CharacterDossier{
				Profile:             domain.CharacterDossierProfile{Arc: dossierArc},
				CurrentAtStoryStart: domain.CharacterStartState{CurrentAction: currentAction},
			},
			Continuity: &domain.CharacterContinuityEntry{},
		}
		observation, err := buildCharacterObservation(st, "pg2_no_future_arc", 1, profile, domain.WorldStimulusPacket{}, domain.ProjectedPlanningContextV2{}, "now")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{characterArc, dossierArc, "六十升", "三十升", "第三章"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("authored future arc entered character observation: %s", raw)
			}
		}
		if currentAction != "" && observation.CurrentGoal != currentAction {
			t.Fatalf("current authored action was lost: %q", observation.CurrentGoal)
		}
		if observation.CurrentGoal == "" || !strings.Contains(string(raw), "谨慎") {
			t.Fatal("safe current goal or established character traits were lost")
		}
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
