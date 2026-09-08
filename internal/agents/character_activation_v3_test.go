package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// Emits only references present in the actual protected model view. It never
// reads Store files, reconstructs canonical aliases, or supplies fake receipts.
type activationV3RuntimeModel struct {
	mu                                      sync.Mutex
	actorCalls, arbiterCalls, revisionCalls int
	readiness                               activationV2ViewModel
	reviseAll, failSecondRevision           bool
	hardRound                               int
	produceArtifact                         bool
	seenActors                              []domain.CharacterObservationPacket
	seenCoordinates                         []domain.CharacterArbitrationCoordinateV1
	continuationCounts                      []int
}

func (*activationV3RuntimeModel) SupportsTools() bool { return true }
func (m *activationV3RuntimeModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(specs) != 1 {
		return nil, fmt.Errorf("one terminal tool required")
	}
	for _, msg := range messages {
		if msg.Role == agentcore.RoleTool && msg.Metadata["is_error"] == true {
			return nil, fmt.Errorf("actual v3 tool rejected response: %s", msg.TextContent())
		}
	}
	if specs[0].Name == "submit_chapter_readiness" {
		r, err := m.readiness.Generate(ctx, messages, specs, opts...)
		if err != nil {
			return nil, err
		}
		call := r.Message.ToolCalls()[0]
		var verdict domain.CharacterReadinessGroupedVerdictV1
		if err := json.Unmarshal(call.Args, &verdict); err != nil {
			return nil, err
		}
		verdict.Decision, verdict.Reason = "continue", "继续本人尚未完成的有限检查"
		if m.readiness.readiness.Load() == 3 {
			verdict.Decision, verdict.Reason = "ready_for_plan", "实际三段检查已完成"
		}
		if m.hardRound > 0 && m.readiness.readiness.Load() == 2 {
			verdict.Decision, verdict.Reason = "hard_conflict", "当前明确硬约束不可同时满足"
			for i := range verdict.ContractGroups {
				verdict.ContractGroups[i].Status = "impossible"
			}
		}
		call.Args, err = json.Marshal(verdict)
		if err != nil {
			return nil, err
		}
		r.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
		return r, nil
	}
	decode := func(tag string, kind modelinput.ExactAgentPacketKind) (modelinput.ScopedReferenceModelView, error) {
		var view modelinput.ScopedReferenceModelView
		for _, msg := range messages {
			if msg.Role != agentcore.RoleUser {
				continue
			}
			_, raw, ok := strings.Cut(msg.TextContent(), "<"+tag+">\n")
			if !ok {
				continue
			}
			d, marked, err := modelinput.ParseExactAgentPacketMessage(msg)
			if err != nil || !marked || d.Kind != kind {
				return view, fmt.Errorf("missing exact string transport binding")
			}
			raw, _, ok = strings.Cut(raw, "\n</"+tag+">")
			if !ok {
				return view, fmt.Errorf("truncated view")
			}
			if err := json.Unmarshal([]byte(raw), &view); err != nil {
				return view, err
			}
			return view, nil
		}
		return view, fmt.Errorf("missing model view")
	}
	var args map[string]any
	switch specs[0].Name {
	case "submit_character_decision":
		view, err := decode("character_observation_packet", modelinput.KindCharacterObservation)
		if err != nil {
			return nil, err
		}
		if strings.Contains(string(view.Body), "UNEXECUTED_PEER_SECRET") || strings.Contains(string(view.Body), "AUTHOR_PRIVATE_SECRET") {
			return nil, fmt.Errorf("actor received arbiter private text")
		}
		var o domain.CharacterObservationPacket
		if err := json.Unmarshal(view.Body, &o); err != nil {
			return nil, err
		}
		if !hasCharacterActivationPolicyV3(o.Sources) || !domain.HasCharacterWorkArtifactPolicyV1(o.Sources) || o.CycleContext == nil || len(o.KnownFacts) == 0 {
			return nil, fmt.Errorf("actor lacks explicit v3/actual facts")
		}
		m.actorCalls++
		if view.Binding.Policy != modelinput.ScopedArtifactReferenceViewPolicyV1 {
			return nil, fmt.Errorf("v3 artifact view did not select explicit reference codec")
		}
		m.seenActors = append(m.seenActors, o)
		if o.Round == 2 {
			m.revisionCalls++
			if len(o.ConflictFeedback) == 0 {
				return nil, fmt.Errorf("revision lost safe constraint")
			}
			if m.failSecondRevision && m.revisionCalls == 2 {
				m.failSecondRevision = false
				return nil, context.Canceled
			}
		}
		refs := []string{o.KnownFacts[0].ID}
		target := 3.0
		task := domain.CharacterSelfTaskV2{TaskID: "inspection", Kind: "work", Action: "本人原地检查", ProgressTarget: &target, ProgressUnit: "minute", KnowledgeRefs: refs}
		if o.Character == "丙" {
			one := 1.0
			task.TaskID = fmt.Sprintf("fresh%d", o.CycleContext.Index)
			task.ProgressTarget = &one
		}
		if m.produceArtifact && o.Character == "甲" {
			if len(o.ResourceViews) != 1 {
				return nil, fmt.Errorf("artifact writer lacks actual material view")
			}
			material := o.ResourceViews[0].ResourceID
			task.ResourceIDs = []string{material}
			task.OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "inspection_note", Label: "本人检查记录", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: material, Amount: 1}}, Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "note_claim", Text: "这是本人当场撰写的检查声明，尚不代替独立核验。", EpistemicKind: "self_statement", SourceRefs: refs}}}}
		}
		args = map[string]any{"location": o.Location, "current_goal": o.CurrentGoal, "pressure": o.Pressure, "available_options": []string{"检查", "等待"}, "decision": "本人检查", "decision_reason": "本人根据已有观察选择", "intended_action": "原地完成本人有限检查", "action_duration": "按实际有效分钟累计", "knowledge_refs": refs, "self_tasks": []domain.CharacterSelfTaskV2{task}}
		if o.Character != "丙" {
			args["work_continuations"] = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: task.TaskID, UntilTarget: true}}
		}
	case "resolve_chapter_world":
		view, err := decode("world_arbitration_input", modelinput.KindWorldArbitration)
		if err != nil {
			return nil, err
		}
		var body struct {
			Stimulus struct {
				Clock    domain.StoryClockContext    `json:"story_clock"`
				Physical domain.WorldPhysicalStateV2 `json:"physical_state"`
			} `json:"world_stimulus"`
			Proposals     []domain.CharacterDecisionProposal          `json:"proposals"`
			Continuations []domain.CharacterWorkContinuationReceiptV1 `json:"continuations"`
			Current       domain.CharacterArbitrationCoordinateV1     `json:"current_arbitration"`
		}
		if err := json.Unmarshal(view.Body, &body); err != nil {
			return nil, err
		}
		if len(body.Proposals) != 3 || body.Current.Cycle < 1 || body.Current.Round < 1 {
			return nil, fmt.Errorf("arbiter lacks complete owner/current coordinate")
		}
		m.arbiterCalls++
		m.seenCoordinates = append(m.seenCoordinates, body.Current)
		m.continuationCounts = append(m.continuationCounts, len(body.Continuations))
		provisional := body.Current.Cycle == 2 && body.Current.Round == 1 && m.hardRound != 1 && !m.produceArtifact
		hard := body.Current.Cycle == 2 && body.Current.Round == m.hardRound
		start, end := body.Stimulus.Clock.CurrentDay, body.Stimulus.Clock.CurrentDay+1.0/1440
		if provisional || hard {
			end = start
		}
		var resolutions []map[string]any
		var affected []string
		var settlements []domain.ResourceSettlementV2
		for i, p := range body.Proposals {
			status := "in_progress"
			if p.Character == "丙" || body.Current.Cycle == 3 {
				status = "completed"
			}
			if p.Character == "甲" || (m.reviseAll && p.Character == "乙") {
				affected = append(affected, p.AgentID)
			}
			resolution := map[string]any{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction, "action_order": i + 1, "outcome": "success", "completion_state": status, "immediate_result": "本轮明确结果", "state_after": "AUTHOR_PRIVATE_SECRET", "visible_to_pov": p.Character == "甲", "post_state": map[string]any{"location": p.Location, "resource_updates": []any{}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "本段结束", TransmissionPath: "本人实际行动", ArrivalChapter: 1, Visibility: "visible", ProtagonistImpact: "余下任务有据"}}}
			if !provisional && !hard {
				execution := domain.CharacterSelfExecutionV2{TaskID: p.SelfTasks[0].TaskID, Status: status, StartDay: &start, EndDay: &end}
				if m.produceArtifact && p.Character == "甲" {
					if len(p.SelfTasks[0].OutputRequests) != 1 {
						return nil, fmt.Errorf("arbiter lost original output request")
					}
					request := p.SelfTasks[0].OutputRequests[0]
					if request.OutputKey != "inspection_note" || request.Claims[0].ClaimID != "note_claim" || !strings.HasPrefix(request.Claims[0].SourceRefs[0], "@ref") {
						return nil, fmt.Errorf("artifact reference/declaration view changed")
					}
					resultStatus := "updated"
					if body.Current.Cycle == 1 {
						resultStatus = "created"
					}
					execution.OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: request.OutputKey, Status: resultStatus, AtDay: end, ClaimIDs: []string{request.Claims[0].ClaimID}, Complete: body.Current.Cycle == 3}}
					if body.Current.Cycle == 1 {
						for _, resource := range body.Stimulus.Physical.Resources {
							if resource.ResourceID == request.MaterialInputs[0].ResourceID {
								before := *resource.ActualAmount
								delta := -1.0
								after := before - 1
								settlements = append(settlements, domain.ResourceSettlementV2{ResourceID: resource.ResourceID, Before: &before, Delta: &delta, After: &after, StartDay: &start, EndDay: &start, EvidenceRefs: []string{p.Digest}})
							}
						}
					}
				}
				resolution["self_executions"] = []domain.CharacterSelfExecutionV2{execution}
			}
			resolutions = append(resolutions, resolution)
		}
		args = map[string]any{"story_time": domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}, "time_window": "实际本段", "hard_contract_status": "feasible", "finalized": !provisional && !hard, "resolutions": resolutions, "resource_settlements": []any{}}
		if len(settlements) > 0 {
			args["resource_settlements"] = settlements
		}
		if provisional {
			args["conflicts"] = []domain.WorldArbitrationConflict{{ID: "private-conflict", Kind: "time", AffectedAgentIDs: affected, Feedback: "UNEXECUTED_PEER_SECRET：乙打算私下藏匿，不曾实际发生；秘密余额11.8"}}
		}
		if hard {
			args["hard_contract_status"] = "infeasible"
			args["hard_contract_conflicts"] = []string{"明确硬约束冲突"}
		}
	default:
		return nil, fmt.Errorf("unexpected terminal tool")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("v3-%d-%d", m.actorCalls, m.arbiterCalls), Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}
func (m *activationV3RuntimeModel) GenerateStream(ctx context.Context, msg []agentcore.Message, spec []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, msg, spec, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func activationV3RuntimeFixture(t *testing.T) (*store.Store, bootstrap.Config, ProjectedArcBoundary) {
	t.Helper()
	st := chapterActivationStore(t)
	var chars []domain.Character
	for _, name := range []string{"甲", "乙", "丙"} {
		role, tier := "配角", "important"
		if name == "甲" {
			role, tier = "主角", "core"
		}
		chars = append(chars, domain.Character{Name: name, Role: role, Tier: tier, InitialState: &domain.CharacterInitialState{Location: "船上", CurrentGoal: "完成本人检查", Pressure: "时间有限", KnownFacts: []string{name + "知道自己的检查需求"}}})
	}
	selectionMust(t, st.Characters.Save(chars))
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "各自检查", CoreEvent: "甲、乙、丙各自决定本人检查"}}))
	selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "保留实际选择", NonNegotiables: []string{"不改角色原选择"}}))
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v3", MaxActivationCycles: 4, MaxRevisionRounds: 1, MaxConcurrency: 1}}
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV3, MaxCharacterActivationCycles: 4}
	return st, cfg, boundary
}

func TestActivationV3ExplicitSelectionCannotFallBackToLegacyEntry(t *testing.T) {
	st, cfg, _ := activationV3RuntimeFixture(t)
	before, err := store.DirectoryContentRoot(st.Dir())
	selectionMust(t, err)
	if _, err := characterActivationExecutionLimit(st, cfg, "live_generation", ProjectedArcBoundary{}); err == nil {
		t.Fatal("explicit v3 fell back to one-shot")
	}
	if _, err := characterActivationExecutionLimit(st, cfg, "pg2_frozen", ProjectedArcBoundary{CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV2, MaxCharacterActivationCycles: 4}); err == nil {
		t.Fatal("explicit v3 reused old pinned generation")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	selectionMust(t, err)
	if before != after {
		t.Fatal("selection rejection wrote source files")
	}
}

func TestActivationV3RuntimeMixedRevisionOriginalP2AndOwnerPrivacy(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	model := &activationV3RuntimeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3", model)}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_runtime_v3_mixed", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if len(proof.Cycles) != 3 || model.actorCalls != 6 || model.arbiterCalls != 4 || model.revisionCalls != 1 {
		t.Fatalf("wrong actual calls: actors=%d arbiter=%d revisions=%d", model.actorCalls, model.arbiterCalls, model.revisionCalls)
	}
	if fmt.Sprint(model.continuationCounts) != "[0 2 1 2]" {
		t.Fatalf("mixed source calls %v", model.continuationCounts)
	}
	selectionMust(t, domain.ValidateCharacterActivationChapterEvidence(*proof))
	var p2 domain.CharacterDecisionProposal
	for _, p := range proof.Cycles[1].Evidence.Proposals {
		if p.Round == 2 {
			p2 = p
		}
	}
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(proof.Session.GenerationID, 1)
	selectionMust(t, err)
	matched := false
	for _, p := range prefix.Steps()[2].EffectiveProposals() {
		if p.AgentID == p2.AgentID {
			matched = sameCharacterCycleValue(p, p2)
		}
	}
	if !matched || p2.Round != 2 || model.seenCoordinates[3].Round != 1 {
		t.Fatal("original P2 changed or current round inferred from origin")
	}
	simulation, _, err := tools.PublishCharacterActivationSimulation(context.Background(), st, proof.Session.GenerationID, 1, nil)
	selectionMust(t, err)
	selectionMust(t, domain.ValidateGenerationCharacterProtocolV2(domain.PlanningGenerationV2{GenerationID: proof.Session.GenerationID, CharacterAgentProtocol: domain.CharacterAgentDecisionProtocolV2Version, CharacterActivationPolicy: domain.CharacterActivationCyclePolicyV3, MaxCharacterActivationCycles: 4}, domain.ProjectedChapterBundle{GenerationID: proof.Session.GenerationID, Chapter: 1, ChapterWorldSimulation: *simulation, CharacterActivationEvidence: proof}))
	beforeCalls := model.actorCalls + model.arbiterCalls + int(model.readiness.readiness.Load())
	reloaded, err := runCharacterActivationChapter(context.Background(), cfg, store.NewStore(st.Dir()), models, proof.Session.GenerationID, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if reloaded.Digest != proof.Digest || beforeCalls != model.actorCalls+model.arbiterCalls+int(model.readiness.readiness.Load()) {
		t.Fatal("completed restart recalled a model")
	}
}

func TestActivationV3RuntimePartialP2RecoveryDoesNotRequeryOtherOwners(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	model := &activationV3RuntimeModel{reviseAll: true, failSecondRevision: true}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3", model)}
	_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_runtime_v3_resume", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected exact injected interruption: %v", err)
	}
	prefix, err := st.LoadVerifiedCharacterActivationPrefix("pg2_runtime_v3_resume", 1)
	selectionMust(t, err)
	if len(prefix.Steps()) != 1 || prefix.Session().Phase != "collecting" {
		t.Fatal("R1 provisional advanced cycle")
	}
	view, err := st.LoadCharacterArbitrationV3("pg2_runtime_v3_resume", 1)
	selectionMust(t, err)
	beforeCalls := model.actorCalls
	currentSession := prefix.Session()
	originalInputs := activationInputsForExecution(view.Input(), currentSession)
	_, dispatchErr := runCharacterProposalRoundWithModel(context.Background(), cfg, st, model, originalInputs.Observations, []string{view.Continuations()[0].AgentID}, 1, &currentSession)
	if dispatchErr == nil || beforeCalls != model.actorCalls {
		t.Fatal("an admitted continuer was redispatched to the model")
	}
	r1, err := view.LoadArbitration(1)
	selectionMust(t, err)
	if r1 == nil || r1.Finalized {
		t.Fatal("R1 not durable")
	}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, store.NewStore(st.Dir()), models, "pg2_runtime_v3_resume", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if model.actorCalls != 8 || model.revisionCalls != 3 || model.arbiterCalls != 4 || len(proof.Cycles[1].ContinuationEntryDigests) != 0 {
		t.Fatalf("recovery duplicated paid work actors=%d revisions=%d arbiters=%d", model.actorCalls, model.revisionCalls, model.arbiterCalls)
	}
}

func TestActivationV3RuntimeHardRoundNeverPublishesPhysicalProgress(t *testing.T) {
	for _, round := range []int{1, 2} {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			model := &activationV3RuntimeModel{hardRound: round}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3", model)}
			_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_runtime_v3_hard", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			var conflict *CharacterActivationChapterConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("expected audited hard conflict: %v", err)
			}
			prefix, err := st.LoadVerifiedCharacterActivationPrefix("pg2_runtime_v3_hard", 1)
			selectionMust(t, err)
			last := prefix.Steps()[1].Cycle()
			if last.StartDay != last.EndDay || last.BeforePhysicalRoot != last.AfterPhysicalRoot || len(last.ContinuationEntryDigests) != 0 {
				t.Fatal("hard result advanced real state")
			}
		})
	}
}

func TestActivationV3RuntimeArtifactPartialContinuationUsesOriginalRequestAndOneMaterialDebit(t *testing.T) {
	st, cfg, boundary := activationV3RuntimeFixture(t)
	characters, err := st.Characters.Load()
	selectionMust(t, err)
	stock := 10.0
	for i := range characters {
		if characters[i].Name == "甲" {
			characters[i].InitialState.ResourceBalances = []domain.InitialCharacterResourceV2{{ResourceID: "res_0123456789abcdef", Name: "空白纸张", PerceivedName: "空白纸张", PerceivedLabel: "空白纸张", Unit: "张", PerceivedUnit: "张", ActualAmount: &stock, Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: &stock, AsOfChapter: 0, EvidenceRefs: []string{"本人初始清点"}}, EvidenceRefs: []string{"本人持有"}}}
		}
	}
	selectionMust(t, st.Characters.Save(characters))
	model := &activationV3RuntimeModel{produceArtifact: true}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3-artifact", model)}
	proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_v3_artifact_actual", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	selectionMust(t, err)
	if model.actorCalls != 5 || model.arbiterCalls != 3 || fmt.Sprint(model.continuationCounts) != "[0 2 2]" {
		t.Fatalf("normal own output work woke original choices: actors=%d arbiters=%d continuations=%v", model.actorCalls, model.arbiterCalls, model.continuationCounts)
	}
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(proof.Session.GenerationID, 1)
	selectionMust(t, err)
	var original domain.CharacterDecisionProposal
	for _, p := range prefix.Steps()[0].EffectiveProposals() {
		if p.Character == "甲" {
			original = p
		}
	}
	if original.SelfTasks[0].OutputRequests[0].ResourceID != "" || original.SelfTasks[0].OutputRequests[0].ExpectedVersionDigest != "" {
		t.Fatal("original create intent had fake prior version")
	}
	for i, step := range prefix.Steps() {
		for _, p := range step.EffectiveProposals() {
			if p.AgentID == original.AgentID && !sameCharacterCycleValue(p, original) {
				t.Fatal("continuation rewrote original request")
			}
		}
		state := step.AfterState()
		artifacts := 0
		for _, r := range state.Resources {
			if r.ResourceID == "res_0123456789abcdef" && *r.ActualAmount != 9 {
				t.Fatal("material was lost or repeatedly debited")
			}
			if r.Artifact != nil {
				artifacts++
				if r.Artifact.Revision != i+1 || r.Artifact.OriginProposalDigest != original.Digest {
					t.Fatal("artifact lost source/version lineage")
				}
				if i == 2 && r.Artifact.Status != "complete" {
					t.Fatal("actual final output was not materialized")
				}
			}
		}
		if artifacts != 1 {
			t.Fatal("one work output cloned multiple world resources")
		}
	}
	for _, o := range proof.Inputs[2].Observations {
		if o.Character == "甲" && len(o.ArtifactViews) == 0 {
			t.Fatal("owner lost actual artifact views")
		}
		if o.Character != "甲" && len(o.ArtifactViews) > 0 {
			t.Fatal("artifact content leaked to another owner")
		}
	}
	_, _, err = tools.PublishCharacterActivationSimulation(context.Background(), st, proof.Session.GenerationID, 1, nil)
	selectionMust(t, err)
}
