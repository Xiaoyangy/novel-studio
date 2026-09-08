package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/voocel/agentcore"
)

// Exercises the real bounded loop and tools, not a network provider. The POV
// has already acted in cycle 1; this cycle has only its counterpart active.
type sleepingPOVCycleModel struct {
	owner, recipient, recipientID string
	actorCalls, arbiterCalls      int
}

func (*sleepingPOVCycleModel) SupportsTools() bool { return true }
func (m *sleepingPOVCycleModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("expected one cycle terminal tool")
	}
	decode := func(tag string, out any) error {
		for _, msg := range messages {
			if msg.Role == agentcore.RoleTool && msg.Metadata["is_error"] == true {
				return fmt.Errorf("cycle test tool rejected: %s", msg.TextContent())
			}
			if msg.Role != agentcore.RoleUser {
				continue
			}
			_, raw, found := strings.Cut(msg.TextContent(), "<"+tag+">\n")
			if found {
				raw, _, closed := strings.Cut(raw, "\n</"+tag+">")
				if !closed {
					return fmt.Errorf("unclosed cycle input")
				}
				return json.Unmarshal([]byte(raw), out)
			}
		}
		return fmt.Errorf("missing cycle input %s", tag)
	}
	var args map[string]any
	switch specs[0].Name {
	case "submit_character_decision":
		m.actorCalls++
		var obs domain.CharacterObservationPacket
		if err := decode("character_observation_packet", &obs); err != nil {
			return nil, err
		}
		if obs.Character != m.owner || len(obs.KnownFacts) == 0 {
			return nil, fmt.Errorf("sleeping POV was called or counterpart forgot delivered information")
		}
		refs := []string{obs.KnownFacts[0].ID}
		args = map[string]any{"location": obs.Location, "current_goal": "说明接收情况", "pressure": "报告尚未核验", "available_options": []string{"说明接收情况", "静候"}, "decision": "说明接收情况", "decision_reason": "区分收到报告与核实事实", "intended_action": "当面说明报告已收到但尚未核验", "action_duration": "一分钟", "knowledge_refs": refs,
			"communications": []domain.CharacterCommunicationV2{{ID: "acknowledge_report", ToCharacter: m.recipient, Kind: "statement", Text: "已收到你的报告，但尚未独立核验。", KnowledgeRefs: refs}}}
	case "resolve_chapter_world":
		m.arbiterCalls++
		promptSeen := false
		for _, msg := range messages {
			promptSeen = promptSeen || (msg.Role == agentcore.RoleSystem && strings.Contains(msg.TextContent(), worldArbiterActivationProjectionPromptV1))
		}
		if !promptSeen {
			return nil, fmt.Errorf("arbiter did not receive cycle-only POV instruction")
		}
		for _, field := range specs[0].Parameters.(map[string]any)["required"].([]string) {
			if field == "protagonist_projection" {
				return nil, fmt.Errorf("actual model schema still requires sleeping POV")
			}
		}
		var payload struct {
			Stimulus struct {
				StoryClock domain.StoryClockContext `json:"story_clock"`
			} `json:"world_stimulus"`
			Proposals []domain.CharacterDecisionProposal `json:"proposals"`
		}
		if err := decode("world_arbitration_input", &payload); err != nil {
			return nil, err
		}
		if len(payload.Proposals) != 1 || payload.Proposals[0].Character != m.owner {
			return nil, fmt.Errorf("cycle arbiter received a fabricated sleeping POV proposal")
		}
		p := payload.Proposals[0]
		start, end := payload.Stimulus.StoryClock.CurrentDay, payload.Stimulus.StoryClock.CurrentDay+1.0/1440
		args = map[string]any{"time_window": "本轮一分钟", "story_time": domain.StoryTimeChapterSchedule{Chapter: p.Chapter, StartDay: start, EndDay: end}, "hard_contract_status": "feasible", "finalized": true, "resource_settlements": []any{},
			"resolutions":        []map[string]any{{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "发送原通信", "state_after": "世界侧秘密不能变成主角选择", "post_state": map[string]any{"location": p.Location, "resources": []any{}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "传达接收状态", TransmissionPath: "当面陈述", ArrivalChapter: 1, ProtagonistImpact: "收到对方陈述，不自动确认报告真实"}}}},
			"passive_receptions": []domain.CharacterPassiveReceptionV2{{ToAgentID: m.recipientID, FromAgentID: p.AgentID, SourceProposalDigest: p.Digest, CommunicationID: "acknowledge_report", DeliveredAtDay: &end, Channel: "in_person"}}}
	default:
		return nil, fmt.Errorf("unexpected cycle tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "sleeping-pov", Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 50, Output: 10}}}, nil
}

func (m *sleepingPOVCycleModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestActivationCycleSleepingPOVKeepsEarlierChoiceAndReceivesWithoutResponding(t *testing.T) {
	chapter := testutil.CharacterActivationChapterWithPassive(t)
	first, previous := chapter.Cycles[0], chapter.Inputs[0]
	// Prepare the deterministic first-cycle fixture with the same protocol as
	// the real loop below before binding any session/predecessor to its digest.
	chapter.ProtocolDigest = characterActivationProtocolDigest()
	first.Evidence.ProtocolDigest = chapter.ProtocolDigest
	var err error
	first.Evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(first.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	first, err = domain.FinalizeCharacterActivationCycle(first)
	if err != nil {
		t.Fatal(err)
	}
	chapter.Cycles = []domain.CharacterActivationCycle{first}
	firstChoice := first.Evidence.Proposals[0]
	session, err := domain.NewCharacterActivationSession(first.GenerationID, 1, first.ChapterContextDigest, *previous.Stimulus.PhysicalState, first.StartDay, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, first)
	if err != nil {
		t.Fatal(err)
	}
	reviewInput, err := domain.NewCharacterReadinessReviewInput(chapter.Context, session, []domain.CharacterActivationCycle{first}, chapter.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	verdict := testutil.ReadyVerdict(reviewInput)
	verdict.Decision = "continue"
	review, err := domain.FinalizeCharacterReadinessReview(reviewInput, verdict)
	if err != nil {
		t.Fatal(err)
	}
	chapter.Reviews = []domain.CharacterReadinessReviewAudit{{Input: reviewInput, Receipt: review}}
	session, err = domain.ApplyCharacterChapterReadiness(session, review)
	if err != nil {
		t.Fatal(err)
	}
	next, err := buildNextCharacterActivationInputs(previous, first, session)
	if err != nil {
		t.Fatal(err)
	}
	// This test exercises an explicitly bound active-set input, not the separate
	// event-selection policy: only B acts while the frozen chapter POV A sleeps.
	for i := range next.Activation.Entries {
		e := &next.Activation.Entries[i]
		if e.AgentID == firstChoice.AgentID {
			e.State, e.Reasons, e.ObservationDigest = domain.CharacterAgentSleeping, nil, ""
		}
	}
	next, err = finalizeCharacterActivationInputs(next, "")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(t.TempDir())
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(next); err != nil {
		t.Fatal(err)
	}
	model := &sleepingPOVCycleModel{owner: "乙", recipient: firstChoice.Character, recipientID: firstChoice.AgentID}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "sleeping-pov", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	second, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, activationInputsForExecution(next, session))
	if err != nil {
		t.Fatal(err)
	}
	if model.actorCalls != 1 || model.arbiterCalls != 1 || len(second.Evidence.Proposals) != 1 || second.Evidence.Proposals[0].Character != "乙" || len(second.Evidence.Arbitrations[0].Resolutions) != 1 || second.Evidence.Arbitrations[0].ProtagonistProjection.Protagonist != "" {
		t.Fatal("sleeping POV was made to choose or counterpart relabelled as protagonist")
	}
	chapter.Cycles = append(chapter.Cycles, second)
	chapter.Inputs = append(chapter.Inputs, next)
	session, err = domain.AppendCharacterActivationCycle(session, second)
	if err != nil {
		t.Fatal(err)
	}
	reviewInput, err = domain.NewCharacterReadinessReviewInput(chapter.Context, session, chapter.Cycles, chapter.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	review, err = domain.FinalizeCharacterReadinessReview(reviewInput, testutil.ReadyVerdict(reviewInput))
	if err != nil {
		t.Fatal(err)
	}
	chapter.Reviews = append(chapter.Reviews, domain.CharacterReadinessReviewAudit{Input: reviewInput, Receipt: review})
	chapter.Session, err = domain.ApplyCharacterChapterReadiness(session, review)
	if err != nil {
		t.Fatal(err)
	}
	chapter, err = domain.FinalizeCharacterActivationChapterEvidence(chapter)
	if err != nil {
		t.Fatal(err)
	}
	simulation, err := domain.BuildCharacterActivationSimulation(chapter, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if simulation.ProtagonistProjection.Protagonist != firstChoice.Character || simulation.ProtagonistProjection.ChosenDecision != firstChoice.Decision || len(simulation.CharacterDecisionTrace) != 2 || simulation.StoryTime.StartDay != first.StartDay || simulation.StoryTime.EndDay != second.EndDay {
		t.Fatal("whole chapter lost earlier POV choice or real cycle clock/trace")
	}
	visible := strings.Join(simulation.ProtagonistProjection.ObservableEffects, "\n")
	if !strings.Contains(visible, "已收到你的报告，但尚未独立核验") || !strings.Contains(visible, "不自动确认内容真实") || strings.Contains(visible, "世界侧秘密") {
		t.Fatal("passive statement was lost, confirmed as truth or mixed with world secrets")
	}
	if err := domain.ValidateCharacterActivationSimulation(simulation, chapter); err != nil {
		t.Fatal(err)
	}
}
