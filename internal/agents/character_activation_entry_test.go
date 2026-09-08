package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func formalActivationStore(t *testing.T) (*store.Store, domain.ProjectedPlanningContextV2) {
	t.Helper()
	st := chapterActivationStore(t)
	if err := st.UserRules.Save(&rules.Snapshot{Version: rules.SnapshotVersion, Status: rules.StatusReady, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2000, Max: 3300}}}); err != nil {
		t.Fatal(err)
	}
	state := domain.ProjectedPlanningContextV2{Version: domain.ProjectedPlanningContextV2Version, GenerationID: "pg2_formal_activation", NextChapter: 1, StateRoot: "sha256:" + strings.Repeat("a", 64)}
	var err error
	state.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta/project_all_state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"version": "project-all-workspace.v3", "generation_id": state.GenerationID, "source_output": st.Dir(), "base_chapter": 0, "workspace": st.Dir(), "isolated_writes": true, "foundation_snapshot_root": state.StateRoot, "rag_snapshot_root": state.StateRoot})
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta/project_all_workspace_manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 1, Owner: "formal-activation-test", ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return st, state
}

func TestFormalCharacterSimulationEntryPublishesWholeChapterAndConsumesAccess(t *testing.T) {
	st, state := formalActivationStore(t)
	model := &activationChapterProbeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "formal-activation", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicy, MaxCharacterActivationCycles: 4}
	simulation, checkpoint, err := runCharacterAgentWorldSimulation(context.Background(), cfg, st, models, tools.NewContextTool(st, tools.References{}, ""), 1, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint == nil || simulation.CharacterActivation == nil || len(simulation.CharacterDecisionTrace) != 2 {
		t.Fatal("formal entry did not publish the complete multi-cycle chapter")
	}
	access, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessSimulate)
	if err != nil || access == nil || access.ConsumedAt.IsZero() {
		t.Fatalf("whole chapter publication did not consume its real access receipt: %v", err)
	}
	if model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 || model.readiness.calls.Load() != 2 {
		t.Fatal("formal entry did not follow the bounded cycle protocol")
	}
	view, err := domain.CharacterActivationPlannerView(*simulation)
	if err != nil {
		t.Fatal(err)
	}
	viewRaw, _ := json.Marshal(view)
	if strings.Contains(string(viewRaw), `"post_state"`) || strings.Contains(string(viewRaw), `"self_experiences"`) || !strings.Contains(string(viewRaw), `"character_decision_trace"`) {
		t.Fatal("planner view duplicated full physical histories or lost its trace")
	}
	// Resume prepares a fresh ephemeral receipt. The original durable consumed
	// authorization, not that unconsumed replacement, proves publication.
	if _, err := tools.NewContextTool(st, tools.References{}, "").PrepareCharacterAgentExecutionContext(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	retry, cp, err := tools.PublishCharacterActivationSimulation(context.Background(), store.NewStore(st.Dir()), state.GenerationID, 1, nil)
	if err != nil || retry.SimulationID != simulation.SimulationID || cp.Seq != checkpoint.Seq {
		t.Fatalf("durably authorized recovery changed publication: %v", err)
	}
	// Simulate an interruption after durable intent but before file/checkpoint
	// publication. Recovery reconstructs the original exact bytes and identity.
	for _, name := range []string{"meta/chapter_simulations/001.json", "meta/checkpoints.jsonl"} {
		if err := os.Remove(filepath.Join(st.Dir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	recovered, _, err := tools.PublishCharacterActivationSimulation(context.Background(), store.NewStore(st.Dir()), state.GenerationID, 1, nil)
	if err != nil || recovered.SimulationID != simulation.SimulationID {
		t.Fatalf("interrupted publication lost the original consumed authorization: %v", err)
	}
}

func TestActivationPublicationRejectsSavedSimulationWithoutConsumedAuthorization(t *testing.T) {
	st, state := formalActivationStore(t)
	execution, err := tools.NewContextTool(st, tools.References{}, "").PrepareCharacterAgentExecutionContext(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{execution.ProjectAllSourceToken, execution.AccessSourceToken}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "publication-auth", &activationChapterProbeModel{})}
	proof, err := runCharacterActivationChapter(context.Background(), bootstrap.Config{}, st, models, state.GenerationID, 1, ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}, state, sources, 4)
	if err != nil {
		t.Fatal(err)
	}
	simulation, err := domain.BuildCharacterActivationSimulation(*proof, "", sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulation(simulation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tools.PublishCharacterActivationSimulation(context.Background(), st, state.GenerationID, 1, sources); err == nil {
		t.Fatal("a reconstructed simulation file bypassed publication authorization")
	}
	access, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessSimulate)
	if err != nil || access == nil || !access.ConsumedAt.IsZero() {
		t.Fatal("rejected publication modified its unconsumed access receipt")
	}
	journal, err := st.Checkpoints.AllStrict()
	if err != nil || len(journal) != 0 {
		t.Fatal("unauthorized recovery created a simulation checkpoint")
	}
}

type activationSuccessorProbe struct {
	calls     int
	receipt   domain.WorldArbitrationReceipt
	readiness domain.CharacterChapterReadiness
}

func (*activationSuccessorProbe) SupportsTools() bool { return true }
func (m *activationSuccessorProbe) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_character_agent_successor_plan" {
		return nil, fmt.Errorf("unexpected Architect capability")
	}
	for _, message := range messages {
		if message.Role != agentcore.RoleUser {
			continue
		}
		descriptor, marked, err := modelinput.ParseExactAgentPacketMessage(message)
		if err != nil || !marked || descriptor.Kind != modelinput.KindCharacterSuccessor {
			return nil, fmt.Errorf("successor lost exact input protection")
		}
		_, raw, ok := strings.Cut(message.TextContent(), "<successor_input>\n")
		if !ok {
			continue
		}
		raw, _, _ = strings.Cut(raw, "\n</successor_input>")
		var payload struct {
			Arbitration domain.WorldArbitrationReceipt   `json:"world_arbitration"`
			Readiness   domain.CharacterChapterReadiness `json:"readiness"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, err
		}
		m.receipt, m.readiness = payload.Arbitration, payload.Readiness
	}
	args := json.RawMessage(`{"architect_summary":"保留硬合同，调整软章位","revised_chapters":[{"chapter":1,"title":"新的软安排","core_event":"保留角色原选择寻找后续路径","hook":"仍有待回应的实际问题","scenes":["原现场"]}]}`)
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "successor", Name: specs[0].Name, Args: args})}}}, nil
}
func (m *activationSuccessorProbe) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestReadinessConflictCreatesSuccessorWithoutRewritingFeasibleArbitration(t *testing.T) {
	st, session, cycle, _, _ := readinessExecutionFixture(t)
	contextValue, err := st.LoadCharacterReadinessContext(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.NewCharacterReadinessReviewInput(*contextValue, session, []domain.CharacterActivationCycle{cycle}, cycle.Evidence.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	verdict := testutil.ReadyVerdict(input)
	verdict.Decision = "hard_conflict"
	verdict.ContractChecks[0].Status = "impossible"
	readiness, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: input, Receipt: readiness}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyCharacterChapterReadiness(session.Digest, readiness); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "旧软章位", CoreEvent: "旧软方向"}}); err != nil {
		t.Fatal(err)
	}
	model := &activationSuccessorProbe{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "successor", model)}
	failure := CharacterActivationChapterConflictError{GenerationID: session.GenerationID, Chapter: 1, ReadinessDigest: readiness.Digest}
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
	_, _, err = characterActivationHardContractFailure(context.Background(), bootstrap.Config{}, st, models, boundary, failure)
	var conflict *CharacterAgentHardContractConflictError
	if !errors.As(err, &conflict) || conflict.SuccessorPlan.ReadinessDigest != readiness.Digest {
		t.Fatalf("missing bound Architect successor: %v", err)
	}
	if model.receipt.Digest != cycle.Evidence.Arbitrations[0].Digest || model.receipt.HardContractStatus != "feasible" || model.readiness.Digest != readiness.Digest {
		t.Fatal("readiness conflict was laundered by rewriting the original world receipt")
	}
	_, _, err = characterActivationHardContractFailure(context.Background(), bootstrap.Config{}, st, models, boundary, failure)
	if !errors.As(err, &conflict) || model.calls != 1 {
		t.Fatal("successor recovery repeated a paid Architect call")
	}
}
