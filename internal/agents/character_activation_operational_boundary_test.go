package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/voocel/agentcore"
)

const operationalBoundaryPower = "res_1234567890abcdef"
const operationalBoundaryMechanism = "M_POWER_CHECK"

// This model only submits the real tools. It never fills a next-cycle
// observation, derives an operational receipt, or fabricates a memory row.
type operationalBoundaryModel struct {
	actorCalls, arbiterCalls int
	firstProposal            string
	secondObservation        domain.CharacterObservationPacket
}

func (*operationalBoundaryModel) SupportsTools() bool { return true }

func (m *operationalBoundaryModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("operational test expects one terminal tool")
	}
	decode := func(tag string, kind modelinput.ExactAgentPacketKind, target any) error {
		for _, message := range messages {
			if message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true {
				return fmt.Errorf("operational boundary tool rejected: %s", message.TextContent())
			}
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, body, found := strings.Cut(message.TextContent(), "<"+tag+">\n")
			if !found {
				continue
			}
			descriptor, marked, err := modelinput.ParseExactAgentPacketMessage(message)
			if err != nil || !marked || descriptor.Kind != kind {
				return fmt.Errorf("operational model lost exact input protection: %v", err)
			}
			body, _, closed := strings.Cut(body, "\n</"+tag+">")
			if !closed {
				return fmt.Errorf("operational input tag is incomplete")
			}
			return json.Unmarshal([]byte(body), target)
		}
		return fmt.Errorf("missing operational input %s", tag)
	}
	systemContains := func(text string) bool {
		for _, message := range messages {
			if message.Role == agentcore.RoleSystem && strings.Contains(message.TextContent(), text) {
				return true
			}
		}
		return false
	}
	schemaAt := func(keys ...string) any {
		var current any = specs[0].Parameters
		for _, key := range keys {
			object, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = object[key]
		}
		return current
	}
	var args map[string]any
	switch specs[0].Name {
	case "submit_character_decision":
		m.actorCalls++
		var observation domain.CharacterObservationPacket
		if err := decode("character_observation_packet", modelinput.KindCharacterObservation, &observation); err != nil {
			return nil, err
		}
		if observation.Character != "甲" || !domain.HasCharacterOperationalAvailabilityPolicyV1(observation.Sources) || !systemContains(characterOperationalAvailabilityPromptV1) || schemaAt("properties", "self_tasks", "items", "properties", "observation_requests") == nil {
			return nil, fmt.Errorf("operational actor policy/prompt/schema or owner boundary is missing")
		}
		if observation.Chapter != 1 || observation.Round != 1 || observation.CycleContext == nil || observation.CycleContext.Index != m.actorCalls {
			return nil, fmt.Errorf("operational result was moved to a new chapter or conflict revision instead of the next cycle")
		}
		observationJSON, _ := json.Marshal(observation)
		if strings.Contains(string(observationJSON), "PRIVATE_AUTHOR_DIAGNOSTIC") || strings.Contains(string(observationJSON), "乙的私密消息") {
			return nil, fmt.Errorf("operational owner input leaked another actor or author text")
		}
		refs := []string{"known-power"}
		task := domain.CharacterSelfTaskV2{TaskID: "check_local_power", Kind: "work", Action: "检查本地非计量电源对本岗操作的可用性", ResourceIDs: []string{operationalBoundaryPower}, ProgressUnit: "minute", KnowledgeRefs: refs}
		mechanisms := []string{operationalBoundaryMechanism}
		if m.actorCalls == 1 {
			if len(observation.OperationalObservations) != 0 {
				return nil, fmt.Errorf("first observation invented an executed result")
			}
			task.ObservationRequests = []domain.CharacterOperationalObservationRequestV1{{RequestID: "power_switch_test", ResourceID: operationalBoundaryPower, Purpose: "支持当前本岗局部操作", MechanismRef: operationalBoundaryMechanism, KnowledgeRefs: refs}}
		} else {
			if len(observation.OperationalObservations) != 1 {
				return nil, fmt.Errorf("second actor input lost its real operational result")
			}
			result := observation.OperationalObservations[0]
			if result.Result != "available" || result.SourceProposalDigest != m.firstProposal || result.ObservedAtDay == nil || *result.ObservedAtDay != .5/1440 || result.ResourceID != operationalBoundaryPower || result.TaskID != "check_local_power" || result.RequestID != "power_switch_test" || result.SourceExperienceID == "" {
				return nil, fmt.Errorf("operational result lost original intent or actual observation time")
			}
			if _, allowed := observation.AllowedFactIDs()[result.ID]; !allowed {
				return nil, fmt.Errorf("owner cannot cite the actual operational observation")
			}
			m.secondObservation = observation
			refs = []string{result.ID}
			task = domain.CharacterSelfTaskV2{TaskID: "continue_local_work", Kind: "work", Action: "依据上轮局部观察继续本岗操作但不宣称整体验收通过", ProgressUnit: "minute", KnowledgeRefs: refs}
			mechanisms = nil
		}
		args = map[string]any{"location": observation.Location, "current_goal": "完成本岗有限操作", "pressure": "时间有限且不能夸大检查范围", "available_options": []string{"实际检查后有限推进", "等候"}, "decision": task.Action, "decision_reason": "仅依本人已知输入选择", "intended_action": task.Action, "action_duration": "一分钟", "knowledge_refs": refs, "mechanism_refs": mechanisms, "self_tasks": []domain.CharacterSelfTaskV2{task}}
	case "resolve_chapter_world":
		m.arbiterCalls++
		var payload struct {
			Stimulus struct {
				Sources    []string                 `json:"sources"`
				StoryClock domain.StoryClockContext `json:"story_clock"`
			} `json:"world_stimulus"`
			Proposals []domain.CharacterDecisionProposal `json:"proposals"`
		}
		if err := decode("world_arbitration_input", modelinput.KindWorldArbitration, &payload); err != nil {
			return nil, err
		}
		if !domain.HasCharacterOperationalAvailabilityPolicyV1(payload.Stimulus.Sources) || !systemContains(worldArbiterOperationalAvailabilityPromptV1) || schemaAt("properties", "resolutions", "items", "properties", "self_executions", "items", "properties", "observation_results") == nil || len(payload.Proposals) != 1 {
			return nil, fmt.Errorf("arbiter operational marker/prompt/schema or active-owner binding missing")
		}
		proposal := payload.Proposals[0]
		start, end := payload.Stimulus.StoryClock.CurrentDay, payload.Stimulus.StoryClock.CurrentDay+1.0/1440
		execution := domain.CharacterSelfExecutionV2{TaskID: proposal.SelfTasks[0].TaskID, Status: "completed", StartDay: &start, EndDay: &end}
		mechanisms := []string{}
		if m.arbiterCalls == 1 {
			m.firstProposal = proposal.Digest
			observed := start + .5/1440
			execution.ObservationResults = []domain.CharacterOperationalObservationResultV1{{RequestID: "power_switch_test", Result: "available", ObservedAtDay: &observed}}
			mechanisms = []string{operationalBoundaryMechanism}
		}
		args = map[string]any{"time_window": "本轮一分钟", "story_time": domain.StoryTimeChapterSchedule{Chapter: proposal.Chapter, StartDay: start, EndDay: end}, "hard_contract_status": "feasible", "finalized": true, "resource_settlements": []any{},
			"resolutions":            []map[string]any{{"agent_id": proposal.AgentID, "character": proposal.Character, "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "完成该轮有限操作", "state_after": "PRIVATE_AUTHOR_DIAGNOSTIC不可进入角色", "mechanism_refs": mechanisms, "self_executions": []domain.CharacterSelfExecutionV2{execution}, "post_state": map[string]any{"location": proposal.Location, "resource_updates": []any{}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "留下本人有限检查记录", TransmissionPath: "本人实际操作", ArrivalChapter: 1, ProtagonistImpact: "支持后续有限决定"}}}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: proposal.Character, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, AvailableOptions: proposal.AvailableOptions, PlanConstraints: []string{"局部观察不是整体合格或永久可用"}, CausalChain: []string{"完成实际检查后得到局部结果"}}}
	default:
		return nil, fmt.Errorf("unexpected operational test tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("operational-%d-%d", m.actorCalls, m.arbiterCalls), Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *operationalBoundaryModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(stream)
	return stream, nil
}

func TestActivationOperationalAvailabilityReachesOnlyOwnerThroughRealModelsAndTools(t *testing.T) {
	st := store.NewStore(t.TempDir())
	input := activationInputWithSleepingActor(t)
	ownerRecord, found := input.Registry.Resolve("甲")
	if !found {
		t.Fatal("fixture lacks its active owner")
	}
	owner := ownerRecord.AgentID
	input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
	input.Stimulus.PhysicalState.Resources = append(input.Stimulus.PhysicalState.Resources, domain.WorldResourceBalanceV2{ResourceID: operationalBoundaryPower, Name: "作者目录中的本地电源"})
	for i := range input.Stimulus.PhysicalState.Actors {
		actor := &input.Stimulus.PhysicalState.Actors[i]
		if actor.AgentID == owner {
			actor.Resources = append(actor.Resources, domain.CharacterResourceHoldingV2{ResourceID: operationalBoundaryPower, PerceivedName: "本岗电源", PerceivedLabel: "本岗非计量电源", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"known-power"}})
		}
	}
	mechanism := domain.CodexMechanism{ID: operationalBoundaryMechanism, Name: "现场开关及状态检查", Visibility: "formal", ActorScope: []string{"本岗操作者"}, Trigger: "实际检查本岗电源", Preconditions: []string{"本人在操作位置且有访问权"}, Inputs: []string{"现有开关与状态信号"}, Costs: []string{"实际检查时间"}, Effects: []string{"仅确认当时局部用途的可用性"}, Observability: []string{"检查者亲见的状态信号"}}
	input.Stimulus.Mechanisms = append(input.Stimulus.Mechanisms, mechanism)
	var err error
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	fixture := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, fixture.ChapterContextDigest, *input.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input.Observations {
		observation := &input.Observations[i]
		observation.StimulusDigest = input.Stimulus.Digest
		observation.CycleContext, err = domain.NewCharacterObservationCycleContext(session)
		if err != nil {
			t.Fatal(err)
		}
		if observation.AgentID == owner {
			observation.PublicMechanisms = append(observation.PublicMechanisms, mechanism)
			observation.KnownFacts = append(observation.KnownFacts, domain.CharacterAgentFact{ID: "known-power", Kind: "known", Text: "本人可检查本岗非计量电源", Source: domain.CharacterSourceRefV2(owner, "known-power"), Visibility: "private"})
		}
		if err := applyCharacterPhysicalObservation(observation, characterAgentProfile{Record: domain.CharacterAgentRecord{AgentID: observation.AgentID}, Character: domain.Character{Name: observation.Character}}, input.Stimulus); err != nil {
			t.Fatal(err)
		}
		*observation, err = domain.FinalizeCharacterObservationPacket(*observation)
		if err != nil {
			t.Fatal(err)
		}
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == observation.AgentID && input.Activation.Entries[j].State == domain.CharacterAgentActive {
				input.Activation.Entries[j].ObservationDigest = observation.Digest
			}
		}
	}
	input, err = finalizeCharacterActivationInputs(input, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(session); err != nil {
		t.Fatal(err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(input); err != nil {
		t.Fatal(err)
	}
	model := &operationalBoundaryModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "operational-boundary", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	first, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, activationInputsForExecution(input, session))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.AppendCharacterActivationCycle(session.Digest, first)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := st.ApplyCharacterChapterReadiness(saved.Digest, testutil.CycleReadiness(t, first, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := buildNextCharacterActivationInputs(input, first, *continued)
	if err != nil {
		t.Fatal(err)
	}
	ownerOperations, err := domain.BuildCharacterOperationalObservationsV1(*next.Stimulus.PhysicalState, owner)
	if err != nil || len(ownerOperations) != 1 {
		t.Fatalf("actual next state lost owner operation: %v", err)
	}
	for _, observation := range next.Observations {
		if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(next.Stimulus, observation); err != nil {
			t.Fatal(err)
		}
		if observation.AgentID != owner {
			raw, _ := json.Marshal(observation)
			if len(observation.OperationalObservations) != 0 || strings.Contains(string(raw), model.firstProposal) || strings.Contains(string(raw), "power_switch_test") || strings.Contains(string(raw), "PRIVATE_AUTHOR_DIAGNOSTIC") {
				t.Fatal("another actor received the owner's operational result or author text")
			}
			if _, allowed := observation.AllowedFactIDs()[ownerOperations[0].ID]; allowed {
				t.Fatal("another actor can cite the owner's operational observation")
			}
		}
	}
	proofs, err = st.CharacterAgents.ForActivationCycle(*continued)
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(next); err != nil {
		t.Fatal(err)
	}
	second, err := runCharacterActivationCycle(context.Background(), cfg, st, models, *continued, activationInputsForExecution(next, *continued))
	if err != nil {
		t.Fatal(err)
	}
	if model.actorCalls != 2 || model.arbiterCalls != 2 || len(model.secondObservation.OperationalObservations) != 1 || second.Evidence.Proposals[0].KnowledgeRefs[0] != model.secondObservation.OperationalObservations[0].ID {
		t.Fatal("real next-cycle model did not consume and cite its original operational receipt")
	}
	if _, err := st.AppendCharacterActivationCycle(continued.Digest, second); err != nil {
		t.Fatal(err)
	}
	usage, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(usage) != 4 {
		t.Fatalf("unexpected hidden retries/calls: usage=%d err=%v", len(usage), err)
	}
}
