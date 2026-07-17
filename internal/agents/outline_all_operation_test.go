package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type outlineAllOperationCaptureModel struct {
	mu        sync.Mutex
	calls     int
	messages  []agentcore.Message
	requests  [][]agentcore.Message
	tools     []agentcore.ToolSpec
	response  agentcore.Message
	responses []agentcore.Message
}

func (m *outlineAllOperationCaptureModel) take(
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
) *agentcore.LLMResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	callIndex := m.calls
	m.calls++
	m.messages = append([]agentcore.Message(nil), messages...)
	m.requests = append(m.requests, append([]agentcore.Message(nil), messages...))
	m.tools = append([]agentcore.ToolSpec(nil), tools...)
	response := m.response
	if callIndex < len(m.responses) {
		response = m.responses[callIndex]
	}
	return &agentcore.LLMResponse{Message: response}
}

func (m *outlineAllOperationCaptureModel) Generate(
	_ context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	return m.take(messages, tools), nil
}

func (m *outlineAllOperationCaptureModel) GenerateStream(
	_ context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	response := m.take(messages, tools)
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{
		Type:       agentcore.StreamEventDone,
		Message:    response.Message,
		StopReason: response.Message.StopReason,
	}
	close(events)
	return events, nil
}

func (*outlineAllOperationCaptureModel) SupportsTools() bool { return true }

func outlineAllOperationToolUseResponse(id string) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID:   id,
			Name: "save_foundation",
			Args: json.RawMessage(`{"type":"expand_arc","volume":3,"arc":3,"content":"[]"}`),
		})},
	}
}

func outlineAllOperationTask(t *testing.T, operation, volume, arc, span int) string {
	t.Helper()
	marker, err := domain.FormatOutlineAllIntent(domain.OutlineAllPendingAction{
		Type:                domain.OutlineAllActionExpandArc,
		Operation:           operation,
		Volume:              volume,
		Arc:                 arc,
		ExpectedChapterSpan: span,
		BeforeLayeredDigest: domain.PlanningV2DigestPrefix + strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return "[PIPELINE OUTLINE-ALL / SINGLE MUTATION]\n" + marker
}

func TestRunOutlineAllOperationWithModelDirectPromptAndCapability(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &outlineAllOperationCaptureModel{response: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID:   "save-1",
			Name: "save_foundation",
			Args: json.RawMessage(`{"type":"expand_arc"}`),
		})},
	}}
	var executed int
	saveTool := agentcore.NewFuncTool(
		"save_foundation",
		"test save",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executed++
			return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
		},
	)
	action := domain.OutlineAllPendingAction{
		Type:                domain.OutlineAllActionExpandArc,
		Operation:           25,
		Volume:              6,
		Arc:                 1,
		ExpectedChapterSpan: 14,
		BeforeLayeredDigest: domain.PlanningV2DigestPrefix + strings.Repeat("a", 64),
	}
	marker, err := domain.FormatOutlineAllIntent(action)
	if err != nil {
		t.Fatal(err)
	}
	task := "[PIPELINE OUTLINE-ALL / SINGLE MUTATION]\n" + marker
	err = runOutlineAllOperationWithModel(
		context.Background(),
		bootstrap.Config{},
		assets.Bundle{Prompts: assets.Prompts{ArchitectLong: "ARCHITECT-LONG-SYSTEM"}},
		st,
		task,
		outlineAllOperationModel{ChatModel: model, Provider: "architect-provider", Name: "architect-main"},
		saveTool,
	)
	if err != nil {
		t.Fatalf("run direct operation: %v", err)
	}
	if executed != 1 {
		t.Fatalf("save_foundation executions = %d, want 1", executed)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want immediate stop after successful save", model.calls)
	}
	if len(model.tools) != 1 || model.tools[0].Name != "save_foundation" {
		t.Fatalf("direct capabilities = %+v, want only save_foundation", model.tools)
	}
	if len(model.messages) < 3 {
		t.Fatalf("model messages = %d, want system + complete task + final authorization", len(model.messages))
	}
	if got := model.messages[len(model.messages)-2].TextContent(); got != task {
		t.Fatalf("direct task changed:\n got: %q\nwant: %q", got, task)
	}
	finalAuthorization := model.messages[len(model.messages)-1].TextContent()
	for _, exact := range []string{
		"operation=25 type=expand_arc volume=6 arc=1 expected_chapter_span=14",
		`save_foundation(type="expand_arc", volume=6, arc=1, content=<恰好14个OutlineEntry>)`,
		"其他卷、弧和历史 operation 全部只读",
	} {
		if !strings.Contains(finalAuthorization, exact) {
			t.Fatalf("final authorization missing %q: %q", exact, finalAuthorization)
		}
	}
	system := model.messages[0].TextContent()
	if !strings.Contains(system, "ARCHITECT-LONG-SYSTEM") || !strings.Contains(system, "唯一拥有的工具是 save_foundation") {
		t.Fatalf("direct Architect system boundary missing: %q", system)
	}
	if strings.Contains(strings.ToLower(system), "coordinator") {
		t.Fatalf("direct Architect system still claims Coordinator dependency: %q", system)
	}
}

func TestRunOutlineAllOperationWithModelReanchorsExactFailureOnRetry(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{
		outlineAllOperationToolUseResponse("save-rejected"),
		outlineAllOperationToolUseResponse("save-success"),
	}}
	const rejection = "outline_all V3A3 chapter 167 rejected: core_event matched placeholder fragment 继续推进"
	var executions int
	saveTool := agentcore.NewFuncTool(
		"save_foundation",
		"test save",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			if executions == 1 {
				return nil, errors.New(rejection)
			}
			return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
		},
	)
	task := outlineAllOperationTask(t, 18, 3, 3, 14)
	protocolBefore, err := OutlineAllOperationProtocolDigest("ARCHITECT-LONG-SYSTEM")
	if err != nil {
		t.Fatal(err)
	}
	err = runOutlineAllOperationWithModel(
		context.Background(),
		bootstrap.Config{},
		assets.Bundle{Prompts: assets.Prompts{ArchitectLong: "ARCHITECT-LONG-SYSTEM"}},
		st,
		task,
		outlineAllOperationModel{ChatModel: model, Provider: "architect-provider", Name: "architect-main"},
		saveTool,
	)
	if err != nil {
		t.Fatalf("run direct operation with one correction: %v", err)
	}
	if executions != 2 || model.calls != 2 {
		t.Fatalf("retry counts: executions=%d model_calls=%d, want 2/2", executions, model.calls)
	}
	if len(model.requests) != 2 {
		t.Fatalf("captured requests = %d, want 2", len(model.requests))
	}
	second := model.requests[1]
	if len(second) < 2 {
		t.Fatalf("second request too short: %+v", second)
	}
	retryReminder := second[len(second)-1]
	if retryReminder.Role != agentcore.RoleUser {
		t.Fatalf("retry tail role = %s, want user", retryReminder.Role)
	}
	for _, exact := range []string{
		"operation=18 type=expand_arc volume=3 arc=3 expected_chapter_span=14",
		`save_foundation(type="expand_arc", volume=3, arc=3, content=<恰好14个OutlineEntry>)`,
		rejection,
	} {
		if !strings.Contains(retryReminder.TextContent(), exact) {
			t.Fatalf("retry reminder missing %q: %q", exact, retryReminder.TextContent())
		}
	}
	toolResult := second[len(second)-2]
	if toolResult.Role != agentcore.RoleTool || !strings.Contains(toolResult.TextContent(), rejection) {
		t.Fatalf("exact failed tool result was not preserved before retry reminder: role=%s text=%q", toolResult.Role, toolResult.TextContent())
	}
	protocolAfter, err := OutlineAllOperationProtocolDigest("ARCHITECT-LONG-SYSTEM")
	if err != nil {
		t.Fatal(err)
	}
	if protocolAfter != protocolBefore {
		t.Fatalf("runtime retry transport changed protocol digest: before=%s after=%s", protocolBefore, protocolAfter)
	}
}

func TestRunOutlineAllOperationWithModelStopsAfterFourRejectedTurns(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	responses := make([]agentcore.Message, outlineAllOperationMaxTurns+1)
	for i := range responses {
		responses[i] = outlineAllOperationToolUseResponse("save-rejected-" + string(rune('a'+i)))
	}
	model := &outlineAllOperationCaptureModel{responses: responses}
	var executions int
	saveTool := agentcore.NewFuncTool(
		"save_foundation",
		"test save",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			return nil, errors.New("host rejected outline mutation")
		},
	)
	cfg := bootstrap.Config{Roles: map[string]bootstrap.RoleConfig{
		"architect": {MaxTurns: 20},
	}}
	err := runOutlineAllOperationWithModel(
		context.Background(),
		cfg,
		assets.Bundle{},
		st,
		outlineAllOperationTask(t, 18, 3, 3, 14),
		outlineAllOperationModel{ChatModel: model, Provider: "architect-provider", Name: "architect-main"},
		saveTool,
	)
	if !errors.Is(err, agentcore.ErrMaxTurns) {
		t.Fatalf("four rejected turns error = %v, want ErrMaxTurns", err)
	}
	if model.calls != outlineAllOperationMaxTurns || executions != outlineAllOperationMaxTurns {
		t.Fatalf(
			"bounded retries: model_calls=%d executions=%d, want %d/%d",
			model.calls, executions, outlineAllOperationMaxTurns, outlineAllOperationMaxTurns,
		)
	}
}

func TestOutlineAllFinalAuthorizationPinsTargetAfterHistoricalArcs(t *testing.T) {
	action := domain.OutlineAllPendingAction{
		Type:                domain.OutlineAllActionExpandArc,
		Operation:           25,
		Volume:              6,
		Arc:                 1,
		ExpectedChapterSpan: 14,
		BeforeLayeredDigest: domain.PlanningV2DigestPrefix + strings.Repeat("b", 64),
	}
	marker, err := domain.FormatOutlineAllIntent(action)
	if err != nil {
		t.Fatal(err)
	}
	prompt := marker + `
MODEL_VISIBLE_CONTEXT:
历史目标弧 V1A3，全局章号 29-44，固定 span=16。
错误示例 save_foundation(type="expand_arc", volume=1, arc=3, content=<16个 OutlineEntry>)。
更早的 operation=9 只读。`

	got, err := outlineAllFinalAuthorization(prompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []string{
		"operation=25 type=expand_arc volume=6 arc=1 expected_chapter_span=14",
		`save_foundation(type="expand_arc", volume=6, arc=1, content=<恰好14个OutlineEntry>)`,
	} {
		if !strings.Contains(got, exact) {
			t.Fatalf("final authorization missing %q: %q", exact, got)
		}
	}
	for _, historical := range []string{"V1A3", "volume=1", "arc=3", "span=16", "operation=9"} {
		if strings.Contains(got, historical) {
			t.Fatalf("final authorization leaked historical target %q: %q", historical, got)
		}
	}
}

func TestRunOutlineAllOperationWithModelFailsClosedWithoutIntent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &outlineAllOperationCaptureModel{}
	saveTool := agentcore.NewFuncTool(
		"save_foundation",
		"test save",
		map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("save_foundation must not run without a valid intent")
			return nil, nil
		},
	)

	err := runOutlineAllOperationWithModel(
		context.Background(),
		bootstrap.Config{},
		assets.Bundle{},
		st,
		"MODEL_VISIBLE_CONTEXT without a host-issued marker",
		outlineAllOperationModel{ChatModel: model, Provider: "architect-provider", Name: "architect-main"},
		saveTool,
	)
	if err == nil || !strings.Contains(err.Error(), "missing its intent marker") {
		t.Fatalf("missing intent error = %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want fail-closed before model execution", model.calls)
	}
}

func TestRunOutlineAllOperationWithModelRejectsNonSaveCapability(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	wrongTool := agentcore.NewFuncTool(
		"novel_context",
		"forbidden",
		map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil },
	)
	err := runOutlineAllOperationWithModel(
		context.Background(), bootstrap.Config{}, assets.Bundle{}, st, "task",
		outlineAllOperationModel{
			ChatModel: &outlineAllOperationCaptureModel{}, Provider: "architect-provider", Name: "architect-main",
		},
		wrongTool,
	)
	if err == nil || !strings.Contains(err.Error(), `rejects capability "novel_context"`) {
		t.Fatalf("wrong capability error = %v", err)
	}
}
