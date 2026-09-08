package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type activationChapterProbeModel struct {
	characterCalls, arbiterCalls atomic.Int32
	readiness                    readinessProbeModel
}

func (*activationChapterProbeModel) SupportsTools() bool { return true }
func (m *activationChapterProbeModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	for _, message := range messages {
		if message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true {
			return nil, fmt.Errorf("chapter fixture tool error: %s", message.TextContent())
		}
	}
	if len(specs) != 1 {
		return nil, fmt.Errorf("unexpected chapter tool capabilities")
	}
	if specs[0].Name == "submit_chapter_readiness" {
		response, err := m.readiness.Generate(ctx, messages, specs, opts...)
		if err != nil {
			return nil, err
		}
		if m.readiness.calls.Load() == 1 {
			call := response.Message.ToolCalls()[0]
			var verdict domain.CharacterReadinessVerdict
			if err := json.Unmarshal(call.Args, &verdict); err != nil {
				return nil, err
			}
			verdict.Decision, verdict.Reason = "continue", "本地回归要求再发生一轮真实执行"
			call.Args, err = json.Marshal(verdict)
			if err != nil {
				return nil, err
			}
			response.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
		}
		return response, nil
	}
	decode := func(tag string, out any) error {
		for _, message := range messages {
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, raw, ok := strings.Cut(message.TextContent(), "<"+tag+">\n")
			if !ok {
				continue
			}
			raw, _, ok = strings.Cut(raw, "\n</"+tag+">")
			if !ok {
				return fmt.Errorf("incomplete chapter execution input")
			}
			return json.Unmarshal([]byte(raw), out)
		}
		return fmt.Errorf("chapter input %s missing", tag)
	}
	var args map[string]any
	switch specs[0].Name {
	case "submit_character_decision":
		m.characterCalls.Add(1)
		var observation domain.CharacterObservationPacket
		if err := decode("character_observation_packet", &observation); err != nil {
			return nil, err
		}
		if len(observation.KnownFacts) == 0 {
			return nil, fmt.Errorf("actor lacks known identity")
		}
		refs := []string{observation.KnownFacts[0].ID}
		args = map[string]any{"location": observation.Location, "current_goal": "确认眼前状态", "pressure": "时间有限", "available_options": []string{"观察", "等候"}, "decision": "观察现场", "decision_reason": "先确认自身处境", "intended_action": "观察当前现场", "action_duration": "一分钟", "knowledge_refs": refs,
			"self_tasks": []domain.CharacterSelfTaskV2{{TaskID: fmt.Sprintf("observe_%d", len(observation.TaskProgress)+1), Kind: "work", Action: "观察当前现场", KnowledgeRefs: refs}}}
	case "resolve_chapter_world":
		m.arbiterCalls.Add(1)
		var payload struct {
			Stimulus struct {
				Chapter    int                      `json:"chapter"`
				StoryClock domain.StoryClockContext `json:"story_clock"`
			} `json:"world_stimulus"`
			Proposals []domain.CharacterDecisionProposal `json:"proposals"`
		}
		if err := decode("world_arbitration_input", &payload); err != nil {
			return nil, err
		}
		if len(payload.Proposals) != 1 {
			return nil, fmt.Errorf("fixture expects a single active protagonist")
		}
		p := payload.Proposals[0]
		start, end := payload.Stimulus.StoryClock.CurrentDay, payload.Stimulus.StoryClock.CurrentDay+1.0/1440
		executions := []domain.CharacterSelfExecutionV2{}
		for _, task := range p.SelfTasks {
			executions = append(executions, domain.CharacterSelfExecutionV2{TaskID: task.TaskID, Status: "completed", StartDay: &start, EndDay: &end})
		}
		args = map[string]any{"time_window": "本轮一分钟", "story_time": domain.StoryTimeChapterSchedule{Chapter: p.Chapter, StartDay: start, EndDay: end}, "hard_contract_status": "feasible", "finalized": true,
			"resource_settlements": []domain.ResourceSettlementV2{},
			"resolutions": []map[string]any{{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "本轮观察完成", "state_after": "位置与资源保持不变", "visible_to_pov": true,
				"self_executions": executions, "post_state": map[string]any{"location": p.Location, "resource_updates": []any{}},
				"butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "观察消耗一分钟", TransmissionPath: "实际时间推进", ArrivalChapter: p.Chapter, Visibility: "visible", ProtagonistImpact: "剩余时间减少"}}}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: p.Character, ChosenDecision: p.Decision, DecisionReason: p.DecisionReason, AvailableOptions: p.AvailableOptions, PlanConstraints: []string{"只写已发生的观察"}, CausalChain: []string{"自主选择转为实际观察"}},
		}
	default:
		return nil, fmt.Errorf("unexpected chapter tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "chapter-cycle", Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *activationChapterProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(ch)
	return ch, nil
}

func chapterActivationStore(t *testing.T) *store.Store {
	t.Helper()
	st := storyClockStore(t)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "甲", Role: "主角", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "船上", CurrentGoal: "观察现场", Pressure: "时间有限", CurrentAction: "准备观察", KnownFacts: []string{"身在船上"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "现场", CoreEvent: "角色自主观察"}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWholeChapterActivationConnectsInputsModelsReadinessAndImmutableProof(t *testing.T) {
	st := chapterActivationStore(t)
	model := &activationChapterProbeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "chapter-activation", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_whole_chapter", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.Cycles) != 2 || len(proof.Inputs) != 2 || len(proof.Reviews) != 2 || proof.Session.Phase != "ready" {
		t.Fatal("whole chapter lost a cycle/input/review")
	}
	if model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 || model.readiness.calls.Load() != 2 {
		t.Fatal("whole chapter did not use two bounded independent cycles")
	}
	if err := domain.ValidateCharacterActivationChapterEvidence(*proof); err != nil {
		t.Fatal(err)
	}
	// Same-chapter history is legal only through the exact earlier-cycle
	// context, never by removing the temporal boundary or inventing a later day.
	for _, mode := range []string{"missing-cycle", "wrong-predecessor", "earlier-clock"} {
		raw, _ := json.Marshal(proof.Inputs[1].Observations[0])
		var observation domain.CharacterObservationPacket
		if err := json.Unmarshal(raw, &observation); err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "missing-cycle":
			observation.CycleContext = nil
		case "wrong-predecessor":
			observation.CycleContext.PreviousCycleDigest = "sha256:" + strings.Repeat("f", 64)
		case "earlier-clock":
			observation.CycleContext.CurrentDay = proof.Cycles[0].StartDay
		}
		checked, err := domain.FinalizeCharacterObservationPacket(observation)
		if err == nil {
			err = domain.ValidateCharacterResourceViewsAgainstStimulusV2(proof.Inputs[1].Stimulus, checked)
		}
		if err == nil {
			t.Fatalf("%s allowed current/future self knowledge without its real predecessor", mode)
		}
	}
	simulation, err := domain.BuildCharacterActivationSimulation(*proof, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(simulation.CharacterDecisions) != 1 || len(simulation.CharacterDecisionTrace) != 2 || simulation.StoryTime.StartDay != proof.Cycles[0].StartDay || simulation.StoryTime.EndDay != proof.Cycles[1].EndDay {
		t.Fatal("aggregate collapsed earlier decisions or used only the last cycle's time")
	}
	if err := domain.ValidateCharacterActivationSimulation(simulation, *proof); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Goal: "保留两轮观察", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: simulation.SimulationID, ProtagonistDecision: simulation.ProtagonistProjection.ChosenDecision}}
	grounding, err := domain.NewActivationPlanGroundingInput(plan, simulation, *proof, proof.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(grounding.Activation.Cycles) != 2 || grounding.Activation.Cycles[0].CycleDigest != proof.Cycles[0].Digest || grounding.Activation.Cycles[1].CycleDigest != proof.Cycles[1].Digest {
		t.Fatal("grounding omitted an earlier cycle")
	}
	grounded, err := domain.FinalizePlanGroundingReceipt(grounding, domain.PlanGroundingVerdict{Pass: true})
	if err != nil {
		t.Fatal(err)
	}
	plan.GroundingReview = &grounded
	if err := domain.ValidatePlanGroundingBundle(plan, simulation, nil, proof); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidatePlanGroundingBundle(plan, simulation, &proof.Cycles[1].Evidence); err == nil {
		t.Fatal("last-cycle-only grounding authorized the whole chapter")
	}
	probe := &groundingProbeModel{args: `{"pass":true,"findings":[]}`}
	if _, err := runPlanGroundingReview(context.Background(), probe, agentcore.ThinkingHigh, grounding); err != nil {
		t.Fatal(err)
	}
	if len(probe.messages) != 2 || !strings.Contains(probe.messages[1].TextContent(), proof.Cycles[0].Digest) || !strings.Contains(probe.messages[1].TextContent(), proof.Cycles[1].Digest) {
		t.Fatal("grounding model did not receive the complete activation trace")
	}
	plan.Goal = "篡改已审计划"
	if err := domain.ValidatePlanGroundingBundle(plan, simulation, nil, proof); err == nil {
		t.Fatal("changed plan retained a paid grounding pass")
	}
	for i, trace := range simulation.CharacterDecisionTrace {
		original := domain.LatestCharacterCycleProposals(proof.Cycles[i].Evidence)[0]
		if trace.Cycle != i+1 || trace.ProposalDigest != original.Digest || trace.Decision.Decision != original.Decision || trace.Decision.Action != original.IntendedAction || trace.Decision.PostState != nil {
			t.Fatal("aggregate changed an original choice or duplicated full physical history")
		}
	}
	badSimulation := simulation
	badSimulation.CharacterDecisionTrace = append([]domain.CharacterActivationDecisionTrace(nil), simulation.CharacterDecisionTrace...)
	badSimulation.CharacterDecisionTrace[0].Decision.Decision = "篡改早期选择"
	badSimulation.SimulationID = domain.ComputeChapterWorldSimulationID(badSimulation)
	if err := domain.ValidateCharacterActivationSimulation(badSimulation, *proof); err == nil {
		t.Fatal("re-signed aggregate could rewrite an earlier choice")
	}
	loaded, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_whole_chapter", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil || loaded.Digest != proof.Digest || model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 || model.readiness.calls.Load() != 2 {
		t.Fatal("completed chapter recovery repeated model calls")
	}
	changedBoundary := boundary
	changedBoundary.BookLastChapter++
	if _, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_whole_chapter", 1, changedBoundary, domain.ProjectedPlanningContextV2{}, nil, 4); err == nil {
		t.Fatal("completed chapter was silently reused for different book bounds")
	}
	for _, mutate := range []func(*domain.CharacterActivationChapterEvidence){
		func(p *domain.CharacterActivationChapterEvidence) { p.Cycles = p.Cycles[1:] },
		func(p *domain.CharacterActivationChapterEvidence) {
			p.Inputs[0].Observations[0].CurrentGoal = "篡改本人目标"
		},
		func(p *domain.CharacterActivationChapterEvidence) {
			p.Reviews[0].Input.Trace.Cycles[0].Actions[0].ImmediateResult = "伪造完成"
		},
		func(p *domain.CharacterActivationChapterEvidence) { p.Session.CurrentDay += 1 },
	} {
		raw, _ := json.Marshal(proof)
		var bad domain.CharacterActivationChapterEvidence
		if err := json.Unmarshal(raw, &bad); err != nil {
			t.Fatal(err)
		}
		mutate(&bad)
		if err := domain.ValidateCharacterActivationChapterEvidence(bad); err == nil {
			t.Fatal("incomplete/tampered chapter proof passed")
		}
	}
	if published, err := st.LoadChapterWorldSimulation(1); err != nil || published != nil {
		t.Fatal("execution silently bypassed the not-yet-connected chapter aggregation boundary")
	}
	stale := store.NewStore(st.Dir()) // Empty checkpoint cache before another Store publishes.
	published, checkpoint, err := tools.PublishCharacterActivationSimulation(context.Background(), st, proof.Session.GenerationID, 1, nil)
	if err != nil || published == nil || checkpoint == nil {
		t.Fatalf("complete chapter publication failed: %v", err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{"keep": "new planner work"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "plan", "", ""); err != nil {
		t.Fatal(err)
	}
	retry, retryCP, err := tools.PublishCharacterActivationSimulation(context.Background(), stale, proof.Session.GenerationID, 1, nil)
	if err != nil || retry.SimulationID != published.SimulationID || retryCP.Seq != checkpoint.Seq {
		t.Fatal("publication retry replaced simulation or advanced checkpoint again")
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || partial["keep"] != "new planner work" {
		t.Fatal("publication retry erased later planner work")
	}
	journal, err := st.Checkpoints.AllStrict()
	if err != nil || len(journal) != 2 || journal[1].Step != "plan" {
		t.Fatal("stale-store retry appended a new simulation epoch after planning")
	}
}
