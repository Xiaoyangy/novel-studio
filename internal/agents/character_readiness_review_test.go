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
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/voocel/agentcore"
)

type readinessProbeModel struct {
	calls atomic.Int32
	input domain.CharacterReadinessReviewInput
}

func (*readinessProbeModel) SupportsTools() bool { return true }
func (m *readinessProbeModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	if len(specs) != 1 || specs[0].Name != "submit_chapter_readiness" {
		return nil, fmt.Errorf("readiness received extra model capabilities")
	}
	found := false
	for _, message := range messages {
		if message.Role != agentcore.RoleUser {
			continue
		}
		descriptor, marked, err := modelinput.ParseExactAgentPacketMessage(message)
		if err != nil || !marked || descriptor.Kind != modelinput.KindChapterReadiness {
			return nil, fmt.Errorf("readiness input lacks exact packet protection")
		}
		_, text, ok := strings.Cut(message.TextContent(), "<chapter_readiness_input>\n")
		if !ok {
			continue
		}
		text, _, ok = strings.Cut(text, "\n</chapter_readiness_input>")
		if !ok {
			return nil, fmt.Errorf("truncated readiness input")
		}
		if err := json.Unmarshal([]byte(text), &m.input); err != nil {
			return nil, err
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("readiness input not found")
	}
	raw, err := json.Marshal(testutil.ReadyVerdict(m.input))
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "readiness", Name: specs[0].Name, Args: raw})}, Usage: &agentcore.Usage{Input: 100, Output: 20}}}, nil
}

func (m *readinessProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(ch)
	return ch, nil
}

func readinessExecutionFixture(t *testing.T) (*store.Store, domain.CharacterActivationSession, domain.CharacterActivationCycle, *bootstrap.ModelSet, *readinessProbeModel) {
	t.Helper()
	contextValue, session, cycle, _ := testutil.CharacterReadiness(t, false)
	st := store.NewStore(t.TempDir())
	if err := st.SaveCharacterReadinessContext(contextValue); err != nil {
		t.Fatal(err)
	}
	baseline, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, contextValue.Digest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendCharacterActivationCycle(baseline.Digest, cycle); err != nil {
		t.Fatal(err)
	}
	model := &readinessProbeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "readiness-probe", model)}
	return st, session, cycle, models, model
}

func TestChapterReadinessExecutesOnceWithExactInputAndSeparateUsage(t *testing.T) {
	st, session, cycle, models, model := readinessExecutionFixture(t)
	result, err := runCharacterChapterReadiness(context.Background(), bootstrap.Config{}, st, models, session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "ready_for_plan" || model.calls.Load() != 1 || result.Version != domain.CharacterReadinessReviewedVersion {
		t.Fatal("readiness did not finish one structured call")
	}
	if len(model.input.Trace.Cycles) != 1 || model.input.Trace.FinalPhysicalRoot != cycle.AfterPhysicalRoot {
		t.Fatal("readiness did not see the full actual cycle chain")
	}
	replayed, err := runCharacterChapterReadiness(context.Background(), bootstrap.Config{}, st, models, session, cycle)
	if err != nil || replayed.Digest != result.Digest || model.calls.Load() != 1 {
		t.Fatalf("paid readiness was repeated: %v", err)
	}
	usage, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(usage) != 1 || usage[0].Role != "chapter_readiness" || usage[0].Cycle != 1 || usage[0].Chapter != 1 || usage[0].Status != "success" {
		t.Fatalf("readiness usage was not separate/unique: %+v %v", usage, err)
	}
	if sim, err := st.LoadChapterWorldSimulation(1); err != nil || sim != nil {
		t.Fatal("readiness prematurely created a chapter simulation")
	}
	changed := &readinessProbeModel{}
	models.Default = bootstrap.NewSwappableModel("test", "different-readiness-model", changed)
	if _, err := runCharacterChapterReadiness(context.Background(), bootstrap.Config{}, st, models, session, cycle); err == nil || changed.calls.Load() != 0 {
		t.Fatal("model change silently rejudged/relabeled immutable readiness")
	}
}

func TestChapterReadinessResumesFromPersistedToolBoundaryWithoutModel(t *testing.T) {
	st, session, cycle, models, model := readinessExecutionFixture(t)
	value, err := st.LoadCharacterReadinessContext(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := models.SnapshotForRole("writer")
	if err != nil {
		t.Fatal(err)
	}
	thinking, _ := ResolveThinkingForModel(snapshot.Model, roleThinking(bootstrap.Config{}, "writer"))
	protocol, err := characterReadinessReviewProtocol(snapshot, thinking)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.NewCharacterReadinessReviewInput(*value, session, []domain.CharacterActivationCycle{cycle}, protocol)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(testutil.ReadyVerdict(input))
	tool := &submitCharacterReadinessTool{store: st, input: input}
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runCharacterChapterReadiness(canceled, bootstrap.Config{}, st, models, session, cycle); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled readiness run continued")
	}
	result, err := runCharacterChapterReadiness(context.Background(), bootstrap.Config{}, st, models, session, cycle)
	if err != nil || result.Decision != "ready_for_plan" || model.calls.Load() != 0 {
		t.Fatalf("completed tool result was not recovered: %v", err)
	}
	if _, err := st.ApplyCharacterChapterReadiness(session.Digest, result); err != nil {
		t.Fatal(err)
	}
}

func TestChapterReadinessBudgetFailureNeverStartsModel(t *testing.T) {
	st, session, cycle, models, model := readinessExecutionFixture(t)
	limit := errors.New("fixture budget exhausted")
	ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{BeforeAgent: func() error { return limit }})
	if _, err := runCharacterChapterReadiness(ctx, bootstrap.Config{}, st, models, session, cycle); !errors.Is(err, limit) || model.calls.Load() != 0 {
		t.Fatalf("readiness ignored its host budget: %v", err)
	}
}
