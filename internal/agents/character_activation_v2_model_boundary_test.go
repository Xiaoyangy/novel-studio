package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// Uses only the actual model-visible views. It does not load a canonical input,
// build a codec, or replace short references itself. Every cycle is a new actor
// choice; no WorkContinuations authorization is submitted or claimed tested.
type activationV2ViewModel struct {
	actors, arbiters, readiness atomic.Int32
	actorViews                  []domain.CharacterObservationPacket
	actorBodies                 []json.RawMessage
	arbiterBodies               []json.RawMessage
	readinessViews              []domain.CharacterReadinessModelViewV1
	actorBindings               []modelinput.ScopedReferenceBinding
	arbiterBindings             []modelinput.ScopedReferenceBinding
	actorRefs                   []string
	proposalRefs                []string
	readinessRefs               []string
}

func (*activationV2ViewModel) SupportsTools() bool { return true }

func (m *activationV2ViewModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("v2 boundary expects one original terminal tool")
	}
	decode := func(tag string, kind modelinput.ExactAgentPacketKind, target any) error {
		for _, message := range messages {
			if message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true {
				return fmt.Errorf("v2 production tool rejected model-view reply: %s", message.TextContent())
			}
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, raw, found := strings.Cut(message.TextContent(), "<"+tag+">\n")
			if !found {
				continue
			}
			descriptor, marked, err := modelinput.ParseExactAgentPacketMessage(message)
			if err != nil || !marked || descriptor.Kind != kind {
				return fmt.Errorf("v2 boundary lost exact transport metadata: %v", err)
			}
			raw, _, closed := strings.Cut(raw, "\n</"+tag+">")
			if !closed {
				return fmt.Errorf("v2 view is incomplete")
			}
			return json.Unmarshal([]byte(raw), target)
		}
		return fmt.Errorf("v2 view %s absent", tag)
	}
	seenPrompt := func(prompt string) bool {
		for _, message := range messages {
			if message.Role == agentcore.RoleSystem && strings.Contains(message.TextContent(), prompt) {
				return true
			}
		}
		return false
	}
	var args any
	switch specs[0].Name {
	case "submit_character_decision":
		cycle := int(m.actors.Add(1))
		var view modelinput.ScopedReferenceModelView
		if err := decode("character_observation_packet", modelinput.KindCharacterObservation, &view); err != nil {
			return nil, err
		}
		if view.Binding.Policy != modelinput.ScopedReferenceViewPolicy || view.Binding.Kind != modelinput.KindCharacterObservation || !seenPrompt(scopedReferencePrompt) {
			return nil, fmt.Errorf("actor did not receive scoped-reference-model-view")
		}
		var observation domain.CharacterObservationPacket
		if err := json.Unmarshal(view.Body, &observation); err != nil {
			return nil, err
		}
		if observation.Character != "甲" || observation.Location != "船上" || observation.CycleContext == nil || observation.CycleContext.Index != cycle || len(observation.KnownFacts) == 0 || !strings.HasPrefix(observation.KnownFacts[0].ID, "@ref") || !strings.HasPrefix(observation.AgentID, "@ref") {
			return nil, fmt.Errorf("actor body lost original facts/current cycle or did not contain short typed references")
		}
		m.actorViews = append(m.actorViews, observation)
		m.actorBodies = append(m.actorBodies, append(json.RawMessage(nil), view.Body...))
		m.actorBindings = append(m.actorBindings, view.Binding)
		m.actorRefs = append(m.actorRefs, observation.KnownFacts[0].ID)
		refs := []string{observation.KnownFacts[0].ID}
		args = map[string]any{"location": observation.Location, "current_goal": "确认眼前状态", "pressure": "时间有限", "available_options": []string{"观察", "等候"}, "decision": "观察现场", "decision_reason": "先确认自身处境", "intended_action": "观察当前现场", "action_duration": "一分钟", "knowledge_refs": refs,
			"self_tasks": []domain.CharacterSelfTaskV2{{TaskID: "observe_local", Kind: "work", Action: "观察当前现场", ProgressUnit: "minute", KnowledgeRefs: refs}}}
	case "resolve_chapter_world":
		cycle := int(m.arbiters.Add(1))
		var view modelinput.ScopedReferenceModelView
		if err := decode("world_arbitration_input", modelinput.KindWorldArbitration, &view); err != nil {
			return nil, err
		}
		if view.Binding.Policy != modelinput.ScopedReferenceViewPolicy || view.Binding.Kind != modelinput.KindWorldArbitration || !seenPrompt(scopedReferencePrompt) {
			return nil, fmt.Errorf("arbiter did not receive scoped-reference-model-view")
		}
		var body struct {
			Stimulus struct {
				StoryClock domain.StoryClockContext                 `json:"story_clock"`
				Evaluation *domain.CharacterSelfEvaluationContextV1 `json:"self_evaluation_context"`
			} `json:"world_stimulus"`
			Proposals []domain.CharacterDecisionProposal `json:"proposals"`
		}
		if err := json.Unmarshal(view.Body, &body); err != nil {
			return nil, err
		}
		if len(body.Proposals) != 1 || body.Stimulus.Evaluation == nil || body.Stimulus.Evaluation.Cycle != cycle {
			return nil, fmt.Errorf("arbiter body did not bind the current self evaluation")
		}
		proposal := body.Proposals[0]
		if !strings.HasPrefix(proposal.AgentID, "@ref") || !strings.HasPrefix(proposal.Digest, "@ref") || proposal.Decision != "观察现场" || proposal.IntendedAction != "观察当前现场" || len(proposal.WorkContinuations) != 0 {
			return nil, fmt.Errorf("arbiter saw rewritten choices, original long IDs, or unrequested continuation")
		}
		m.arbiterBindings = append(m.arbiterBindings, view.Binding)
		m.arbiterBodies = append(m.arbiterBodies, append(json.RawMessage(nil), view.Body...))
		m.proposalRefs = append(m.proposalRefs, proposal.Digest)
		start, end := body.Stimulus.StoryClock.CurrentDay, body.Stimulus.StoryClock.CurrentDay+1.0/1440
		args = map[string]any{"time_window": "本轮一分钟", "story_time": domain.StoryTimeChapterSchedule{Chapter: proposal.Chapter, StartDay: start, EndDay: end}, "hard_contract_status": "feasible", "finalized": true, "resource_settlements": []any{},
			"resolutions": []map[string]any{{"agent_id": proposal.AgentID, "character": proposal.Character, "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "本轮观察完成", "state_after": "位置与资源保持不变", "visible_to_pov": true,
				"self_executions": []domain.CharacterSelfExecutionV2{{TaskID: proposal.SelfTasks[0].TaskID, Status: "completed", StartDay: &start, EndDay: &end}}, "post_state": map[string]any{"location": proposal.Location, "resource_updates": []any{}},
				"butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "观察消耗一分钟", TransmissionPath: "实际时间推进", ArrivalChapter: proposal.Chapter, Visibility: "visible", ProtagonistImpact: "剩余时间减少"}}}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: proposal.Character, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, AvailableOptions: proposal.AvailableOptions, PlanConstraints: []string{"只写已发生的观察"}, CausalChain: []string{"自主选择转为实际观察"}}}
	case "submit_chapter_readiness":
		cycle := int(m.readiness.Add(1))
		var view domain.CharacterReadinessModelViewV1
		if err := decode("chapter_readiness_input", modelinput.KindChapterReadiness, &view); err != nil {
			return nil, err
		}
		if view.ViewPolicy != domain.CharacterReadinessModelViewPolicyV1 || view.SchemaPolicy != domain.CharacterReadinessGroupedSchemaPolicyV1 || !seenPrompt(characterGroupedReadinessPrompt) || len(view.Requirements) == 0 {
			return nil, fmt.Errorf("readiness did not receive the actual grouped model view")
		}
		if _, duplicate := view.Context["hard_contracts"]; duplicate {
			return nil, fmt.Errorf("readiness duplicated hard contracts")
		}
		m.readinessViews = append(m.readinessViews, view)
		properties, ok := specs[0].Parameters.(map[string]any)["properties"].(map[string]any)
		if !ok || properties["contract_groups"] == nil || properties["contract_checks"] != nil || properties["input_digest"] != nil || properties["view_digest"] != nil {
			return nil, fmt.Errorf("readiness still asks the model for old checks or Host digest binding")
		}
		var ref string
		if err := json.Unmarshal(view.Trace["final_state_ref"], &ref); err != nil || !strings.HasPrefix(ref, "e") {
			return nil, fmt.Errorf("readiness has no short actual-state evidence reference")
		}
		m.readinessRefs = append(m.readinessRefs, ref)
		groups := []domain.CharacterReadinessContractGroupV1{}
		byStatus := map[string]int{}
		for _, requirement := range view.Requirements {
			if !strings.HasPrefix(requirement.ContractAlias, "c") {
				return nil, fmt.Errorf("readiness requirement is not a local short alias")
			}
			status := "pending"
			if requirement.DueNow {
				status = "satisfied"
			}
			index, exists := byStatus[status]
			if !exists {
				index = len(groups)
				byStatus[status] = index
				groups = append(groups, domain.CharacterReadinessContractGroupV1{Status: status, EvidenceRefs: []string{ref}, ContractAliases: []string{}})
			}
			groups[index].ContractAliases = append(groups[index].ContractAliases, requirement.ContractAlias)
		}
		decision, reason := "continue", "本地回归要求再发生一轮独立观察"
		if cycle == 2 {
			decision, reason = "ready_for_plan", "两轮实际观察已完成，未来硬约束仍保留"
		}
		args = domain.CharacterReadinessGroupedVerdictV1{Decision: decision, Reason: reason, EvidenceRefs: []string{ref}, ContractGroups: groups}
	default:
		return nil, fmt.Errorf("unexpected v2 terminal tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("v2-%s-%d-%d-%d", specs[0].Name, m.actors.Load(), m.arbiters.Load(), m.readiness.Load()), Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *activationV2ViewModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(stream)
	return stream, nil
}

func TestActivationV2WholeChapterUsesShortReferencesGroupedReadinessAndActualEvaluations(t *testing.T) {
	st := chapterActivationStore(t)
	if err := st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "在既有事实范围内完成最后的判断", NonNegotiables: []string{"人物的自主选择不得被改写", "观察结果只代表实际发生的观察"}}); err != nil {
		t.Fatal(err)
	}
	const generation = "pg2_v2_model_boundary"
	model := &activationV2ViewModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v2-model-boundary", model)}
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v2", MaxActivationCycles: 4, MaxRevisionRounds: 1}}
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV2, MaxCharacterActivationCycles: 4}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil {
		t.Fatalf("v2 whole-chapter production path: %v", err)
	}
	if proof.Session.Phase != "ready" || len(proof.Cycles) != 2 || len(proof.Inputs) != 2 || len(proof.Reviews) != 2 || model.actors.Load() != 2 || model.arbiters.Load() != 2 || model.readiness.Load() != 2 {
		t.Fatal("v2 did not complete exactly two fresh actor/arbitration/readiness cycles")
	}
	if err := domain.ValidateCharacterActivationChapterEvidence(*proof); err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewCharacterActivationSession(generation, 1, proof.Context.Digest, *proof.Inputs[0].Stimulus.PhysicalState, proof.Cycles[0].StartDay, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i, cycle := range proof.Cycles {
		input := proof.Inputs[i]
		if input.Stimulus.SelfEvaluationContext == nil {
			t.Fatal("cycle lacks actual Host evaluation context")
		}
		if err := domain.ValidateCharacterSelfEvaluationContextAgainstSessionV1(*input.Stimulus.SelfEvaluationContext, session); err != nil {
			t.Fatalf("cycle %d did not bind its real pre-cycle session: %v", i+1, err)
		}
		proposal := cycle.Evidence.Proposals[0]
		observation := cycle.Evidence.Observations[0]
		if proposal.KnowledgeRefs[0] != observation.KnownFacts[0].ID || proposal.SelfTasks[0].KnowledgeRefs[0] != observation.KnownFacts[0].ID || proposal.KnowledgeRefs[0] == model.actorRefs[i] || strings.HasPrefix(proposal.AgentID, "@ref") || strings.HasPrefix(proposal.Digest, "@ref") || len(proposal.WorkContinuations) != 0 {
			t.Fatal("short references were not expanded into original canonical IDs, or continuation was granted")
		}
		if proposal.Decision != "观察现场" || proposal.IntendedAction != "观察当前现场" || observation.KnownFacts[0].Text != model.actorViews[i].KnownFacts[0].Text || model.actorViews[i].Location != observation.Location {
			t.Fatal("reference projection changed original facts or choices")
		}
		actorCodec, err := modelinput.NewScopedReferenceCodec(modelinput.KindCharacterObservation, observation)
		if err != nil || actorCodec.Binding() != model.actorBindings[i] || string(actorCodec.ModelView().Body) != string(model.actorBodies[i]) {
			t.Fatalf("actual actor model view did not preserve its complete canonical source: %v", err)
		}
		stimulusView, err := characterArbiterStimulusModelView(input.Stimulus)
		if err != nil {
			t.Fatal(err)
		}
		arbiterCodec, err := modelinput.NewScopedReferenceCodec(modelinput.KindWorldArbitration, struct {
			Stimulus   any                                `json:"world_stimulus"`
			Activation domain.CharacterAgentActivation    `json:"activation"`
			Proposals  []domain.CharacterDecisionProposal `json:"proposals"`
		}{stimulusView, cycle.Evidence.Activation, cycle.Evidence.Proposals})
		if err != nil || arbiterCodec.Binding() != model.arbiterBindings[i] || string(arbiterCodec.ModelView().Body) != string(model.arbiterBodies[i]) || arbiterCodec.Alias(proposal.Digest) != model.proposalRefs[i] {
			t.Fatalf("actual arbiter model view did not preserve its complete canonical source: %v", err)
		}
		receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
		if receipt.Resolutions[0].ProposalDigest != proposal.Digest || receipt.Resolutions[0].AgentID != proposal.AgentID {
			t.Fatal("arbiter short references were not expanded for the original tool")
		}
		found := false
		for _, fact := range receipt.Resolutions[0].PostState.SelfExperiences {
			if fact.SourceProposalDigest != proposal.Digest {
				continue
			}
			found = true
			if fact.Evaluation == nil || fact.Evaluation.Cycle != i+1 || fact.Evaluation.ContextDigest != input.Stimulus.SelfEvaluationContext.Digest || fact.Evaluation.StimulusDigest != input.Stimulus.Digest || fact.Evaluation.EvaluatedAtDay != receipt.StoryTime.EndDay || fact.Evaluation.Ordinal != 1 {
				t.Fatal("self evaluation was fabricated or did not advance with its actual cycle")
			}
		}
		if !found {
			t.Fatal("actual self execution did not materialize an evaluated record")
		}
		audit := proof.Reviews[i]
		if audit.ModelView == nil || audit.Receipt.EvidenceRefs[0] != audit.Input.Trace.FinalPhysicalRoot || len(audit.Receipt.ContractChecks) != len(audit.Input.Requirements) {
			t.Fatal("grouped readiness did not preserve canonical evidence and complete checks")
		}
		if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
			t.Fatal(err)
		}
		codec, err := domain.NewCharacterReadinessModelCodecV1(audit.Input)
		if err != nil || !reflect.DeepEqual(*audit.ModelView, codec.Binding()) || !reflect.DeepEqual(codec.ModelView(), model.readinessViews[i]) {
			t.Fatal("readiness audit did not authenticate the actual model view")
		}
		session, err = domain.AppendCharacterActivationCycle(session, cycle)
		if err != nil {
			t.Fatal(err)
		}
		session, err = domain.ApplyCharacterChapterReadiness(session, audit.Receipt)
		if err != nil {
			t.Fatal(err)
		}
	}
	simulation, checkpoint, err := tools.PublishCharacterActivationSimulation(context.Background(), st, generation, 1, nil)
	if err != nil || simulation == nil || checkpoint == nil {
		t.Fatalf("v2 complete publication: %v", err)
	}
	if err := domain.ValidateCharacterActivationSimulation(*simulation, *proof); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateGenerationCharacterProtocolV2(domain.PlanningGenerationV2{GenerationID: generation, CharacterAgentProtocol: domain.CharacterAgentDecisionProtocolV2Version, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV2, MaxCharacterActivationCycles: 4}, domain.ProjectedChapterBundle{GenerationID: generation, Chapter: 1, ChapterWorldSimulation: *simulation, CharacterActivationEvidence: proof}); err != nil {
		t.Fatalf("published v2 simulation lost generation execution policy: %v", err)
	}
	if len(model.actorBindings) != 2 || model.actorBindings[0] == model.actorBindings[1] || len(model.arbiterBindings) != 2 || model.arbiterBindings[0] == model.arbiterBindings[1] {
		t.Fatal("view bindings were reused across different cycle inputs")
	}
	replayed, err := runCharacterActivationChapter(context.Background(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil || replayed.Digest != proof.Digest || model.actors.Load() != 2 || model.arbiters.Load() != 2 || model.readiness.Load() != 2 {
		t.Fatalf("completed v2 chapter re-called a model or changed source: %v", err)
	}
	usage, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(usage) != 6 {
		t.Fatalf("v2 two-cycle boundary lost or duplicated its six model receipts: %d %v", len(usage), err)
	}
}
