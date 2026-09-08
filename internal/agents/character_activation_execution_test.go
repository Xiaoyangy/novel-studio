package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/voocel/agentcore"
)

// A deterministic model at the real AgentLoop boundary: it submits the real
// tools, writes normal proof files and uses normal usage accounting. This is
// not a provider or prose-quality test.
type activationExecutionModel struct {
	cycle                        domain.CharacterActivationCycle
	characterCalls, arbiterCalls atomic.Int32
	revise                       bool
	hardConflict                 bool
	failRevision                 atomic.Bool
}

func (*activationExecutionModel) SupportsTools() bool { return true }
func (m *activationExecutionModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("test requires one terminal tool")
	}
	var text strings.Builder
	for _, msg := range messages {
		if msg.Role == agentcore.RoleUser {
			for _, block := range msg.Content {
				text.WriteString(block.Text)
			}
		}
	}
	packet := func(tag string, out any) error {
		_, body, ok := strings.Cut(text.String(), "<"+tag+">\n")
		if !ok {
			return fmt.Errorf("missing %s", tag)
		}
		body, _, ok = strings.Cut(body, "\n</"+tag+">")
		if !ok {
			return fmt.Errorf("unclosed %s", tag)
		}
		return json.Unmarshal([]byte(body), out)
	}
	var args map[string]any
	if specs[0].Name == "submit_character_decision" {
		m.characterCalls.Add(1)
		var observation domain.CharacterObservationPacket
		if err := packet("character_observation_packet", &observation); err != nil {
			return nil, err
		}
		if observation.Round == 2 && m.failRevision.CompareAndSwap(true, false) {
			return nil, context.Canceled
		}
		p := m.cycle.Evidence.Proposals[0]
		args = map[string]any{"location": p.Location, "current_goal": p.CurrentGoal, "pressure": p.Pressure,
			"available_options": p.AvailableOptions, "decision": p.Decision, "decision_reason": p.DecisionReason,
			"intended_action": p.IntendedAction, "action_duration": p.ActionDuration, "knowledge_refs": p.KnowledgeRefs}
		if len(p.Communications) > 0 {
			args["communications"] = p.Communications
		}
	} else if specs[0].Name == "resolve_chapter_world" {
		m.arbiterCalls.Add(1)
		var payload struct {
			Proposals []domain.CharacterDecisionProposal `json:"proposals"`
		}
		if err := packet("world_arbitration_input", &payload); err != nil {
			return nil, err
		}
		if len(payload.Proposals) != 1 {
			return nil, fmt.Errorf("test expects one actor")
		}
		p := payload.Proposals[0]
		raw, _ := json.Marshal(m.cycle.Evidence.Arbitrations[0])
		var receipt domain.WorldArbitrationReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return nil, err
		}
		receipt.Resolutions[0].ProposalDigest = p.Digest
		receipt.ResourceSettlements[0].EvidenceRefs = []string{p.Digest}
		for i := range receipt.PassiveReceptions {
			receipt.PassiveReceptions[i].SourceProposalDigest = p.Digest
		}
		if m.revise && p.Round == 1 {
			receipt.Finalized = false
			receipt.Conflicts = []domain.WorldArbitrationConflict{{ID: "timing", Kind: "time", AffectedAgentIDs: []string{p.AgentID}, Feedback: "先确认本轮可用操作时间", Resolved: false}}
		}
		args = map[string]any{"time_window": m.cycle.Evidence.Stimulus.TimeWindow, "story_time": receipt.StoryTime,
			"resolutions": receipt.Resolutions, "resource_settlements": receipt.ResourceSettlements,
			"protagonist_projection": receipt.ProtagonistProjection, "hard_contract_status": "feasible",
			"finalized": receipt.Finalized, "conflicts": receipt.Conflicts}
		if len(receipt.PassiveReceptions) > 0 {
			args["passive_receptions"] = receipt.PassiveReceptions
		}
		if m.hardConflict {
			args["finalized"] = false
			args["hard_contract_status"] = "infeasible"
			args["hard_contract_conflicts"] = []string{"既定硬义务已无法共同兑现"}
		}
	} else {
		return nil, fmt.Errorf("unexpected tool %s", specs[0].Name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "activation", Name: specs[0].Name, Args: raw})},
		Usage:   &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *activationExecutionModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(ch)
	return ch, nil
}

func seedActivationExecutionInputs(t *testing.T, st *store.Store, session domain.CharacterActivationSession, cycle domain.CharacterActivationCycle) characterAgentChapterInputs {
	t.Helper()
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	e := cycle.Evidence
	for _, save := range []func() error{
		func() error { return proofs.SaveRegistrySnapshot(e.GenerationID, e.Chapter, e.Registry) },
		func() error { return proofs.SaveStimulus(e.Stimulus) },
		func() error { return proofs.SaveObservation(e.Observations[0]) },
		func() error { return proofs.SaveActivation(e.Activation) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	return characterAgentChapterInputs{Stimulus: e.Stimulus, Activation: e.Activation, Observations: map[string]domain.CharacterObservationPacket{e.Observations[0].AgentID: e.Observations[0]}}
}

func TestCharacterActivationRealExecutorUsesDistinctCyclesAndResumesWithoutCalls(t *testing.T) {
	st := store.NewStore(t.TempDir())
	first := testutil.CharacterCycle(t, 1, "", nil, 0)
	baseline, err := domain.NewCharacterActivationSession(first.GenerationID, 1, first.ChapterContextDigest, *first.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	model := &activationExecutionModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "activation-executor", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	driver := ChapterActivationDriver{
		ExecuteCycle: func(ctx context.Context, session domain.CharacterActivationSession) (domain.CharacterActivationCycle, error) {
			fixture := first
			if len(session.CycleDigests) > 0 {
				previous, err := st.LoadCharacterActivationCycle(session.GenerationID, 1, len(session.CycleDigests))
				if err != nil || previous == nil {
					return domain.CharacterActivationCycle{}, fmt.Errorf("missing prior cycle: %w", err)
				}
				after, err := domain.ApplyArbitrationPhysicalStateV2(previous.Evidence.Arbitrations[0], previous.Evidence.Stimulus, previous.Evidence.Proposals...)
				if err != nil {
					return domain.CharacterActivationCycle{}, err
				}
				fixture = testutil.CharacterCycle(t, len(session.CycleDigests)+1, previous.Digest, &after, session.CurrentDay)
			}
			model.cycle = fixture
			inputs := seedActivationExecutionInputs(t, st, session, fixture)
			cycle, err := runCharacterActivationCycle(ctx, cfg, st, models, session, inputs)
			if err != nil {
				return cycle, err
			}
			// A completed protocol before the session append must be recovered
			// using stored proposals/receipts, not another provider invocation.
			calls := model.characterCalls.Load() + model.arbiterCalls.Load()
			replayed, err := runCharacterActivationCycle(ctx, cfg, st, models, session, inputs)
			if err != nil || replayed.Digest != cycle.Digest || calls != model.characterCalls.Load()+model.arbiterCalls.Load() {
				t.Fatalf("paid cycle was rerun: %v", err)
			}
			return cycle, nil
		},
		AssessCycle: func(_ context.Context, _ domain.CharacterActivationSession, cycle domain.CharacterActivationCycle) (domain.CharacterChapterReadiness, error) {
			decision := "continue"
			if cycle.Index == 2 {
				decision = "ready_for_plan"
			}
			return testutil.CycleReadiness(t, cycle, decision), nil
		},
	}
	result, err := RunChapterActivationLoop(context.Background(), st, baseline, driver)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != "ready" || len(result.CycleDigests) != 2 || model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 {
		t.Fatalf("executor did not use two real cycles: %+v", result)
	}
	if _, err := RunChapterActivationLoop(context.Background(), st, baseline, driver); err != nil {
		t.Fatal(err)
	}
	if model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 {
		t.Fatal("ready session reran models")
	}
	usage, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(usage) != 4 {
		t.Fatalf("missing per-cycle usage: %+v %v", usage, err)
	}
	counts := map[int]int{}
	for _, row := range usage {
		counts[row.Cycle]++
		if row.Chapter != 1 || row.Status != "success" {
			t.Fatalf("usage lost chapter/status: %+v", row)
		}
	}
	if counts[1] != 2 || counts[2] != 2 {
		t.Fatalf("usage merged cycles: %+v", counts)
	}
	if sim, err := st.LoadChapterWorldSimulation(1); err != nil || sim != nil {
		t.Fatal("cycle execution published the chapter before aggregation")
	}
}

func TestCharacterActivationRevisionResumeKeepsPaidInitialRound(t *testing.T) {
	st := store.NewStore(t.TempDir())
	fixture := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(fixture.GenerationID, 1, fixture.ChapterContextDigest, *fixture.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	inputs := seedActivationExecutionInputs(t, st, session, fixture)
	model := &activationExecutionModel{cycle: fixture, revise: true}
	model.failRevision.Store(true)
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "activation-executor", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	if _, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, inputs); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation at revision: %v", err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	agentID := fixture.Evidence.Proposals[0].AgentID
	original, err := proofs.LoadProposal(fixture.GenerationID, 1, 1, agentID)
	if err != nil || original == nil {
		t.Fatalf("lost first paid decision: %v", err)
	}
	revision, err := proofs.LoadObservation(fixture.GenerationID, 1, 2, agentID)
	if err != nil || revision == nil {
		t.Fatalf("lost revision observation: %v", err)
	}
	result, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence.Arbitrations) != 2 || len(result.Evidence.Proposals) != 2 || model.characterCalls.Load() != 3 || model.arbiterCalls.Load() != 2 {
		t.Fatal("resume reran initial round or failed to collect both rounds")
	}
	after, err := proofs.LoadObservation(fixture.GenerationID, 1, 2, agentID)
	if err != nil || after.Digest != revision.Digest || after.GeneratedAt != revision.GeneratedAt {
		t.Fatal("resume re-signed revision observation")
	}
	if result.Evidence.Proposals[0].Digest != original.Digest {
		t.Fatal("resume replaced initial paid decision")
	}
}

func TestCharacterActivationHardConflictCannotBecomeNormalChapterEvidence(t *testing.T) {
	st := store.NewStore(t.TempDir())
	fixture := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(fixture.GenerationID, 1, fixture.ChapterContextDigest, *fixture.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	inputs := seedActivationExecutionInputs(t, st, session, fixture)
	model := &activationExecutionModel{cycle: fixture, hardConflict: true}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "activation-executor", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 2
	if _, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, inputs); err == nil {
		t.Fatal("accepted more than one conflict revision")
	}
	if model.characterCalls.Load() != 0 || model.arbiterCalls.Load() != 0 {
		t.Fatal("invalid cycle config dispatched models")
	}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	cycle, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.Evidence.Version != domain.CharacterHardConflictEvidenceVersion {
		t.Fatal("hard conflict lost its distinct evidence type")
	}
	if err := domain.ValidateCharacterAgentEvidenceBundle(cycle.Evidence); err == nil {
		t.Fatal("hard conflict passed ordinary evidence verification")
	}
	// The failed projection keeps its candidate outcomes for diagnosis, but
	// neither readiness nor another cycle may treat them as a closed chapter.
	pending, err := domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.PendingHardConflict || pending.Phase != "assessing" {
		t.Fatal("hard conflict was not retained by the session")
	}
	if _, err := domain.ApplyCharacterChapterReadiness(pending, testutil.CycleReadiness(t, cycle, "ready_for_plan")); err == nil {
		t.Fatal("hard conflict became ready for planning")
	}
	if _, err := st.CharacterAgents.ForActivationCycle(pending); err == nil {
		t.Fatal("hard conflict can dispatch another cycle")
	}
	if plan, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil || plan != nil {
		t.Fatal("cycle tool prematurely activated an Architect successor")
	}
	if sim, err := st.LoadChapterWorldSimulation(1); err != nil || sim != nil {
		t.Fatal("hard conflict published a chapter")
	}
}
