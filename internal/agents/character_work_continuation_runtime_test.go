package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type continuationRuntimeModel struct {
	actorCalls, arbiterCalls atomic.Int32
	readiness                activationV2ViewModel
	mu                       sync.Mutex
	actorViews               map[string][]domain.CharacterObservationPacket
	actorBindings            map[string][]modelinput.ScopedReferenceBinding
	continuationCounts       []int
}

func (*continuationRuntimeModel) SupportsTools() bool { return true }

func (m *continuationRuntimeModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("continuation runtime expected one original terminal tool")
	}
	for _, message := range messages {
		if message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true {
			return nil, fmt.Errorf("continuation production tool rejected the actual view reply: %s", message.TextContent())
		}
	}
	if specs[0].Name == "submit_chapter_readiness" {
		// Reuse the existing boundary fake, which actually reads the grouped
		// model view and emits its current short aliases (not canonical IDs).
		response, err := m.readiness.Generate(ctx, messages, specs, opts...)
		if err != nil {
			return nil, err
		}
		call := response.Message.ToolCalls()[0]
		var verdict domain.CharacterReadinessGroupedVerdictV1
		if err := json.Unmarshal(call.Args, &verdict); err != nil {
			return nil, err
		}
		verdict.Decision, verdict.Reason = "continue", "实际检查尚未完成三分钟"
		if m.readiness.readiness.Load() == 3 {
			verdict.Decision, verdict.Reason = "ready_for_plan", "两人的实际检查均已达到本人明确目标"
		}
		call.Args, err = json.Marshal(verdict)
		if err != nil {
			return nil, err
		}
		response.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
		return response, nil
	}
	decodeView := func(tag string, kind modelinput.ExactAgentPacketKind) (modelinput.ScopedReferenceModelView, error) {
		var view modelinput.ScopedReferenceModelView
		for _, message := range messages {
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, raw, found := strings.Cut(message.TextContent(), "<"+tag+">\n")
			if !found {
				continue
			}
			descriptor, marked, err := modelinput.ParseExactAgentPacketMessage(message)
			if err != nil || !marked || descriptor.Kind != kind {
				return view, fmt.Errorf("continuation input lost exact packet binding: %v", err)
			}
			raw, _, found = strings.Cut(raw, "\n</"+tag+">")
			if !found {
				return view, fmt.Errorf("continuation input was cut")
			}
			if err := json.Unmarshal([]byte(raw), &view); err != nil {
				return view, err
			}
			if view.Binding.Kind != kind || view.Binding.Policy != modelinput.ScopedReferenceViewPolicy {
				return view, fmt.Errorf("continuation input lacks scoped reference model view")
			}
			return view, nil
		}
		return view, fmt.Errorf("missing actual model input %s", tag)
	}
	var args any
	switch specs[0].Name {
	case "submit_character_decision":
		m.actorCalls.Add(1)
		view, err := decodeView("character_observation_packet", modelinput.KindCharacterObservation)
		if err != nil {
			return nil, err
		}
		var observation domain.CharacterObservationPacket
		if err := json.Unmarshal(view.Body, &observation); err != nil {
			return nil, err
		}
		if observation.CycleContext == nil || len(observation.KnownFacts) == 0 || !strings.HasPrefix(observation.KnownFacts[0].ID, "@ref") || !domain.HasCharacterWorkContinuationPolicyV1(observation.Sources) || !domain.HasCharacterSelfChronologyPolicyV1(observation.Sources) {
			return nil, fmt.Errorf("actor did not receive its real authorized work/chronology/short-ref view")
		}
		m.mu.Lock()
		if m.actorViews == nil {
			m.actorViews = map[string][]domain.CharacterObservationPacket{}
			m.actorBindings = map[string][]modelinput.ScopedReferenceBinding{}
		}
		m.actorViews[observation.Character] = append(m.actorViews[observation.Character], observation)
		m.actorBindings[observation.Character] = append(m.actorBindings[observation.Character], view.Binding)
		m.mu.Unlock()
		refs := []string{observation.KnownFacts[0].ID}
		target := 3.0
		args = map[string]any{"location": observation.Location, "current_goal": observation.CurrentGoal, "pressure": observation.Pressure, "available_options": []string{"进行本人检查", "暂不检查"}, "decision": "进行本人检查", "decision_reason": "本人选择按已知要求执行", "intended_action": "完成本人三分钟例行检查", "action_duration": "本人累计三分钟有效工时", "knowledge_refs": refs,
			"self_tasks":         []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: observation.Character + "的本人例行检查", ProgressTarget: &target, ProgressUnit: "minute", KnowledgeRefs: refs}},
			"work_continuations": []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: true}}}
	case "resolve_chapter_world":
		cycle := int(m.arbiterCalls.Add(1))
		view, err := decodeView("world_arbitration_input", modelinput.KindWorldArbitration)
		if err != nil {
			return nil, err
		}
		var body struct {
			Stimulus struct {
				Clock      domain.StoryClockContext                 `json:"story_clock"`
				Evaluation *domain.CharacterSelfEvaluationContextV1 `json:"self_evaluation_context"`
			} `json:"world_stimulus"`
			Proposals     []domain.CharacterDecisionProposal          `json:"proposals"`
			Continuations []domain.CharacterWorkContinuationReceiptV1 `json:"continuations"`
		}
		if err := json.Unmarshal(view.Body, &body); err != nil {
			return nil, err
		}
		if len(body.Proposals) != 2 || body.Stimulus.Evaluation == nil || body.Stimulus.Evaluation.Cycle != cycle {
			return nil, fmt.Errorf("arbiter omitted an owner or current evaluation")
		}
		m.continuationCounts = append(m.continuationCounts, len(body.Continuations))
		if cycle > 1 && len(body.Continuations) != 2 {
			return nil, fmt.Errorf("cycle %d dispatched fresh character calls instead of two explicit continuations (actor calls=%d)", cycle, m.actorCalls.Load())
		}
		start, end := body.Stimulus.Clock.CurrentDay, body.Stimulus.Clock.CurrentDay+1.0/1440
		status := "in_progress"
		if cycle == 3 {
			status = "completed"
		}
		var resolutions []map[string]any
		var pov domain.ProtagonistDecisionProjection
		for i, proposal := range body.Proposals {
			if !strings.HasPrefix(proposal.Digest, "@ref") || len(proposal.WorkContinuations) != 1 || !proposal.WorkContinuations[0].UntilTarget || len(proposal.SelfTasks) != 1 || proposal.SelfTasks[0].ProgressTarget == nil || *proposal.SelfTasks[0].ProgressTarget != 3 {
				return nil, fmt.Errorf("arbiter lost the independently granted finite work intent")
			}
			resolutions = append(resolutions, map[string]any{"agent_id": proposal.AgentID, "character": proposal.Character, "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction, "action_order": i + 1, "outcome": "success", "completion_state": status, "immediate_result": "本人本周期实际检查一分钟", "state_after": "作者秘密不应进入本人记忆", "visible_to_pov": proposal.Character == "甲",
				"self_executions": []domain.CharacterSelfExecutionV2{{TaskID: proposal.SelfTasks[0].TaskID, Status: status, StartDay: &start, EndDay: &end}}, "post_state": map[string]any{"location": proposal.Location, "resource_updates": []any{}},
				"butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "本周期检查耗用一分钟", TransmissionPath: "本人实际作业", ArrivalChapter: 1, Visibility: "visible", ProtagonistImpact: "剩余任务时间减少"}}})
			if proposal.Character == "甲" {
				pov = domain.ProtagonistDecisionProjection{Protagonist: proposal.Character, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, AvailableOptions: proposal.AvailableOptions, PlanConstraints: []string{"保留本人原选择，只写实际检查段"}, CausalChain: []string{"原授权经本轮实际执行推进"}}
			}
		}
		args = map[string]any{"time_window": "本轮一分钟", "story_time": domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}, "hard_contract_status": "feasible", "finalized": true, "resource_settlements": []any{}, "resolutions": resolutions, "protagonist_projection": pov}
	default:
		return nil, fmt.Errorf("unexpected continuation tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("continuation-%s-%d-%d", specs[0].Name, m.actorCalls.Load(), m.arbiterCalls.Load()), Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *continuationRuntimeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(stream)
	return stream, nil
}

func reportContinuationRuntimeSeam(t *testing.T, st *store.Store, generation string) {
	t.Helper()
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
	if err != nil || prefix == nil {
		t.Logf("runtime prefix diagnostic: %v", err)
		return
	}
	session := prefix.Session()
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Logf("runtime scope diagnostic: %v", err)
		return
	}
	input, err := proofs.LoadActivationInputs()
	if err != nil || input == nil {
		t.Logf("runtime input diagnostic: %v", err)
		return
	}
	for _, observation := range input.Observations {
		if ledger, found := prefix.ContinuationLedger(observation.AgentID); found {
			result, err := domain.EvaluateCharacterWorkContinuationV1(ledger, *input, prefix.ContinuationBoundaries()...)
			t.Logf("frozen cycle=%d owner=%s eligible=%v reasons=%v err=%v", observation.CycleContext.Index, observation.Character, result.Eligible, result.WakeReasons, err)
		}
	}
}

func TestCharacterWorkContinuationRuntimeThreeCyclesOnlyCallsEachOwnerOnce(t *testing.T) {
	st := chapterActivationStore(t)
	var characters []domain.Character
	for _, name := range []string{"甲", "乙"} {
		role, tier := "配角", "important"
		if name == "甲" {
			role, tier = "主角", "core"
		}
		characters = append(characters, domain.Character{Name: name, Role: role, Tier: tier, InitialState: &domain.CharacterInitialState{Location: "船上", CurrentGoal: "完成本人例行检查", Pressure: "时间有限", KnownFacts: []string{name + "知道自己的检查需要三分钟有效工时"}}})
	}
	selectionMust(t, st.Characters.Save(characters))
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "各自检查", CoreEvent: "甲与乙各自选择是否进行本人检查"}}))
	selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "保留各自选择及实际结果", NonNegotiables: []string{"不替角色选择或宣称未执行的检查已完成"}}))
	const generation = "pg2_continuation_runtime"
	model := &continuationRuntimeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "continuation-runtime", model)}
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v2", MaxActivationCycles: 4, MaxRevisionRounds: 1}}
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV2, MaxCharacterActivationCycles: 4}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil {
		reportContinuationRuntimeSeam(t, st, generation)
		t.Fatalf("actual three-cycle continuation runtime: %v", err)
	}
	if model.actorCalls.Load() != 2 || model.arbiterCalls.Load() != 3 || model.readiness.readiness.Load() != 3 || !reflect.DeepEqual(model.continuationCounts, []int{0, 2, 2}) || len(proof.Cycles) != 3 || proof.Session.Phase != "ready" {
		t.Fatalf("paid-call/actual-cycle accounting wrong: actors=%d arbiter=%d readiness=%d continuation=%v", model.actorCalls.Load(), model.arbiterCalls.Load(), model.readiness.readiness.Load(), model.continuationCounts)
	}
	selectionMust(t, domain.ValidateCharacterActivationChapterEvidence(*proof))
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
	selectionMust(t, err)
	if prefix == nil || len(prefix.Steps()) != 3 {
		t.Fatal("published source chain cannot rebuild its real steps")
	}
	original := map[string]domain.CharacterDecisionProposal{}
	for _, proposal := range proof.Cycles[0].Evidence.Proposals {
		original[proposal.AgentID] = proposal
		if len(model.actorViews[proposal.Character]) != 1 || model.actorViews[proposal.Character][0].CycleContext.Index != 1 {
			t.Fatal("an owner received a redundant fresh model input")
		}
		for _, observation := range proof.Inputs[0].Observations {
			if observation.AgentID == proposal.AgentID {
				codec, err := modelinput.NewScopedReferenceCodec(modelinput.KindCharacterObservation, observation)
				selectionMust(t, err)
				if codec.Binding() != model.actorBindings[proposal.Character][0] {
					t.Fatal("initial model view did not bind the real original observation")
				}
			}
		}
	}
	initial, err := domain.NewCharacterActivationSession(generation, 1, proof.Context.Digest, *proof.Inputs[0].Stimulus.PhysicalState, proof.Cycles[0].StartDay, 4)
	selectionMust(t, err)
	verification, err := domain.NewVerifiedCharacterActivationPrefix(initial)
	selectionMust(t, err)
	for i, step := range prefix.Steps() {
		cycle := proof.Cycles[i]
		if cycle.InputSetDigest != proof.Inputs[i].Digest || step.Input().Digest != cycle.InputSetDigest {
			t.Fatal("actual frozen input did not bind its global cycle")
		}
		selectionMust(t, domain.ValidateCharacterReadinessReviewAudit(proof.Reviews[i]))
		codec, err := domain.NewCharacterReadinessModelCodecV1(proof.Reviews[i].Input)
		selectionMust(t, err)
		if proof.Reviews[i].ModelView == nil || *proof.Reviews[i].ModelView != codec.Binding() || !reflect.DeepEqual(codec.ModelView(), model.readiness.readinessViews[i]) {
			t.Fatal("grouped readiness audit/model-view inventory missing")
		}
		for _, observation := range proof.Inputs[i].Observations {
			if !domain.HasCharacterSourceRefPolicyV2(observation.Sources) || !domain.HasCharacterSelfChronologyPolicyV1(observation.Sources) {
				t.Fatal("real frozen observation lost the opaque-source/chronology boundary")
			}
			raw, err := json.Marshal(observation)
			selectionMust(t, err)
			other := "甲知道自己的检查需要三分钟有效工时"
			if observation.Character == "甲" {
				other = "乙知道自己的检查需要三分钟有效工时"
			}
			if strings.Contains(string(raw), "作者秘密") || strings.Contains(string(raw), other) {
				t.Fatal("actual next-cycle observation leaked author text or another owner's private fact")
			}
			if i == 0 {
				continue
			}
			ledger, ok := verification.ContinuationLedger(observation.AgentID)
			if !ok {
				t.Fatal("real flat prefix lost prior authority")
			}
			eligible, err := domain.EvaluateCharacterWorkContinuationV1(ledger, proof.Inputs[i], verification.ContinuationBoundaries()...)
			selectionMust(t, err)
			if !eligible.Eligible {
				t.Fatalf("real opaque feedback was misclassified as new knowledge: %v", eligible.WakeReasons)
			}
			// Inspect the actual persisted observation and its own frozen memory,
			// not a hand-built packet: projected source handles may attest normal
			// feedback but must neither replace its exact receipt nor wake an owner.
			priorReceipt := proof.Cycles[i-1].Evidence.Arbitrations[0].Digest
			feedbackVisible := false
			for _, memory := range proof.Inputs[i].Memories {
				if memory.AgentID != observation.AgentID {
					continue
				}
				for _, fact := range memory.Facts {
					if fact.SourceDigest != priorReceipt || fact.Kind != "projected_decision" {
						continue
					}
					for _, visible := range observation.Memory {
						if visible.ID == fact.ID {
							feedbackVisible = visible.SourceDigest == priorReceipt || visible.SourceDigest == domain.CharacterSourceRefV2(observation.AgentID, priorReceipt)
						}
					}
				}
			}
			if !feedbackVisible {
				t.Fatal("normal paid feedback lacks its original receipt or safe owner-bound alias")
			}
		}
		_, verification, err = domain.VerifyCharacterActivationStep(verification, proof.Inputs[i], cycle)
		selectionMust(t, err)
		verification, err = domain.ApplyVerifiedCharacterActivationReadiness(verification, proof.Reviews[i].Receipt)
		selectionMust(t, err)
		for _, proposal := range step.EffectiveProposals() {
			if !reflect.DeepEqual(proposal, original[proposal.AgentID]) {
				t.Fatal("continuation copied/re-signed/replaced the original proposal")
			}
		}
		proofPath := filepath.Join(st.Dir(), "meta", "character_agents", "activation_sessions", generation, "000001", "work", fmt.Sprintf("%06d", i+1), "proof")
		if _, err := os.Stat(filepath.Join(proofPath, "inputs.json")); err != nil {
			t.Fatalf("actual cycle proof directory is absent: %v", err)
		}
		proposalFiles, err := filepath.Glob(filepath.Join(proofPath, "proposals", "*", "*.json"))
		selectionMust(t, err)
		if i == 0 && len(proposalFiles) != 2 {
			t.Fatalf("original proposal artifacts missing: %v", proposalFiles)
		}
		if i > 0 {
			if cycle.Version != domain.CharacterActivationCycleV2Version || len(cycle.Evidence.Proposals) != 0 || len(cycle.WorkContinuations) != 2 || len(cycle.ContinuationEntryDigests) != 2 {
				t.Fatal("continued owners were stored as synthetic fresh proposals")
			}
			if len(proposalFiles) != 0 {
				t.Fatalf("a continuation has new per-cycle proposal files: %v", proposalFiles)
			}
		}
	}
	if verification.Session().Digest != proof.Session.Digest {
		t.Fatal("complete flat input/cycle/audit replay disagrees with the published session")
	}
	last := prefix.Steps()[2].AfterState()
	for _, actor := range last.Actors {
		if len(actor.TaskProgress) != 1 || math.Abs(actor.TaskProgress[0].Completed-3) > 1e-9 || actor.TaskProgress[0].State != "completed" || len(actor.SelfExperiences) != 3 {
			t.Fatal("actual three-minute work did not materialize exactly once per cycle")
		}
	}
	simulation, checkpoint, err := tools.PublishCharacterActivationSimulation(context.Background(), st, generation, 1, nil)
	selectionMust(t, err)
	if simulation == nil || checkpoint == nil {
		t.Fatal("complete runtime did not publish its simulation/checkpoint")
	}
	selectionMust(t, domain.ValidateCharacterActivationSimulation(*simulation, *proof))
	selectionMust(t, domain.ValidateGenerationCharacterProtocolV2(domain.PlanningGenerationV2{GenerationID: generation, CharacterAgentProtocol: domain.CharacterAgentDecisionProtocolV2Version, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV2, MaxCharacterActivationCycles: 4}, domain.ProjectedChapterBundle{GenerationID: generation, Chapter: 1, ChapterWorldSimulation: *simulation, CharacterActivationEvidence: proof}))
	usage, err := st.CharacterAgents.LoadUsage()
	selectionMust(t, err)
	if len(usage) != 8 {
		t.Fatalf("expected 2 actor + 3 arbiter + 3 readiness receipts, got %d", len(usage))
	}
	before, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "chapter_simulations", "001.json"))
	selectionMust(t, err)
	reopened := store.NewStore(st.Dir())
	replayed, err := runCharacterActivationChapter(context.Background(), cfg, reopened, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	republished, repeatedCP, err := tools.PublishCharacterActivationSimulation(context.Background(), reopened, generation, 1, nil)
	selectionMust(t, err)
	if replayed.Digest != proof.Digest || republished.SimulationID != simulation.SimulationID || repeatedCP.Seq != checkpoint.Seq || model.actorCalls.Load() != 2 || model.arbiterCalls.Load() != 3 || model.readiness.readiness.Load() != 3 {
		t.Fatal("restart reran paid work or advanced publication twice")
	}
	recoveredUsage, err := reopened.CharacterAgents.LoadUsage()
	selectionMust(t, err)
	if len(recoveredUsage) != 8 {
		t.Fatal("restart duplicated or lost an actual provider receipt")
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "chapter_simulations", "001.json"))
	selectionMust(t, err)
	if string(before) != string(after) {
		t.Fatal("republication changed original simulation bytes")
	}
}
