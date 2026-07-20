package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type seededCompactCaptureModel struct {
	mu          sync.Mutex
	responses   []agentcore.Message
	errs        []error
	calls       int
	requests    [][]agentcore.Message
	tools       [][]agentcore.ToolSpec
	callConfigs []agentcore.CallConfig
}

func (m *seededCompactCaptureModel) next(
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts []agentcore.CallOption,
) (agentcore.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	m.requests = append(m.requests, append([]agentcore.Message(nil), messages...))
	m.tools = append(m.tools, append([]agentcore.ToolSpec(nil), tools...))
	m.callConfigs = append(m.callConfigs, agentcore.ResolveCallConfig(opts))
	var response agentcore.Message
	if idx < len(m.responses) {
		response = m.responses[idx]
	}
	if idx < len(m.errs) && m.errs[idx] != nil {
		return agentcore.Message{}, m.errs[idx]
	}
	return response, nil
}

func (m *seededCompactCaptureModel) Generate(
	_ context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	response, err := m.next(messages, tools, opts)
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: response}, nil
}

func (m *seededCompactCaptureModel) GenerateStream(
	_ context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	response, err := m.next(messages, tools, opts)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{
		Type:       agentcore.StreamEventDone,
		Message:    response,
		StopReason: response.StopReason,
	}
	close(events)
	return events, nil
}

func (*seededCompactCaptureModel) SupportsTools() bool { return true }

func seededCompactToolCall(id string) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID: id, Name: "plan_details", Args: json.RawMessage(`{"chapter":5,"finalize":true}`),
		})},
	}
}

func TestSeededCompactFinalizeUsesMinimalPromptSingleToolLowAndTightBounds(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &seededCompactCaptureModel{responses: []agentcore.Message{
		seededCompactToolCall("patch"),
		seededCompactToolCall("finalize"),
	}}
	executions := 0
	tool := agentcore.NewFuncTool(
		"plan_details",
		"allowlisted mutable patch only",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			if executions == 1 {
				return json.RawMessage(`{"staged":"mutable_patch"}`), nil
			}
			return json.RawMessage(`{"planned":true}`), nil
		},
	)
	const userPrompt = "HOST SEEDED PARTIAL + ALLOWED MUTABLE KEYS"
	if err := runSealedConvergencePlannerSeededCompactFinalizeWithModel(
		context.Background(), st, 5, userPrompt, model, "codex", "gpt-test", tool,
	); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || executions != 2 {
		t.Fatalf("tight two-turn finalize drifted: model=%d tools=%d", model.calls, executions)
	}
	for i := range model.callConfigs {
		if model.callConfigs[i].ThinkingLevel != agentcore.ThinkingLow {
			t.Fatalf("call %d thinking=%q, want low", i+1, model.callConfigs[i].ThinkingLevel)
		}
		if len(model.tools[i]) != 1 || model.tools[i][0].Name != "plan_details" {
			t.Fatalf("call %d tools=%v, want plan_details only", i+1, model.tools[i])
		}
	}
	if got := model.requests[0][0].TextContent(); got != sealedConvergenceSeededCompactFinalizeSystemPrompt {
		t.Fatalf("system prompt drifted:\n%s", got)
	}
	joined, _ := json.Marshal(model.requests[0])
	if strings.Contains(string(joined), "PLANNER_BUNDLE_POISON") || !strings.Contains(string(joined), userPrompt) {
		t.Fatalf("request loaded an unexpected bundle prompt or lost host prompt: %s", joined)
	}
	if strings.Contains(sealedConvergenceSeededCompactFinalizeSystemPrompt, "source_token 原样放入") ||
		!strings.Contains(sealedConvergenceSeededCompactFinalizeSystemPrompt, "context_sources 与其余 seeded fields") ||
		!strings.Contains(sealedConvergenceSeededCompactFinalizeSystemPrompt, "禁止提交、覆盖或重写") {
		t.Fatal("seeded authority boundary allows the compact model to overwrite host-seeded sources")
	}
	if sealedConvergenceSeededCompactMaxTurns != 2 ||
		sealedConvergenceSeededCompactMaxRetries != 0 ||
		sealedConvergenceSeededCompactMaxToolErrors != 2 ||
		sealedConvergenceSeededCompactCallTimeout != 4*time.Minute ||
		sealedConvergenceSeededCompactTimeout != 5*time.Minute {
		t.Fatal("seeded compact liveness limits drifted")
	}
	sessionPath := filepath.Join(
		st.Dir(), "meta", "sessions", "agents",
		"convergence_planner_seeded_compact_finalize-ch05.jsonl",
	)
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("independent seeded compact session was not recorded: %v", err)
	}
	if !validSealedConvergencePlannerSessionName(sealedConvergenceSeededCompactFinalizeAgentName) ||
		validSealedConvergencePlannerSessionName("convergence_planner_seeded_compact_finalize-ch05") {
		t.Fatal("seeded compact session-name validator drifted")
	}
}

func TestSeededCompactFinalizeExecutesOneSemanticRepairAfterFirstToolError(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &seededCompactCaptureModel{responses: []agentcore.Message{
		seededCompactToolCall("first-rejected"),
		seededCompactToolCall("second-repair"),
	}}
	executions := 0
	tool := agentcore.NewFuncTool(
		"plan_details", "allowlisted mutable patch only", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			if executions == 1 {
				return nil, errors.New("story-specific semantic gate rejected first patch")
			}
			return json.RawMessage(`{"planned":true}`), nil
		},
	)
	if err := runSealedConvergencePlannerSeededCompactFinalizeWithModel(
		context.Background(), st, 5, "seeded", model, "codex", "gpt-test", tool,
	); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || executions != 2 {
		t.Fatalf("semantic repair was disabled before execution: model=%d tools=%d", model.calls, executions)
	}
}

func TestExhaustedCompactFinalizeUsesDistinctSessionIdentity(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &seededCompactCaptureModel{responses: []agentcore.Message{
		seededCompactToolCall("finalize"),
	}}
	tool := agentcore.NewFuncTool(
		"plan_details", "allowlisted mutable patch only", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"planned":true}`), nil
		},
	)
	if err := runSealedConvergencePlannerCompactFinalizeWithModel(
		context.Background(), st, 5, "exhausted", model, "codex", "gpt-test", tool,
		sealedConvergenceExhaustedCompactFinalizeAgentName,
	); err != nil {
		t.Fatal(err)
	}
	exhaustedPath := filepath.Join(
		st.Dir(), "meta", "sessions", "agents",
		"convergence_planner_exhausted_compact_finalize-ch05.jsonl",
	)
	if _, err := os.Stat(exhaustedPath); err != nil {
		t.Fatalf("exhausted compact session was not recorded independently: %v", err)
	}
	seededPath := filepath.Join(
		st.Dir(), "meta", "sessions", "agents",
		"convergence_planner_seeded_compact_finalize-ch05.jsonl",
	)
	if _, err := os.Stat(seededPath); !os.IsNotExist(err) {
		t.Fatalf("exhausted compact run reused seeded compact session: %v", err)
	}
	if !validSealedConvergencePlannerSessionName(sealedConvergenceExhaustedCompactFinalizeAgentName) {
		t.Fatal("exhausted compact session-name validator rejected the fixed identity")
	}
}

func TestSeededCompactFinalizeBindsOnlyWriterToExplicitExecutable(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binaryPath, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{
		Provider: "default-codex", ModelName: "default-model",
		Providers: map[string]bootstrap.ProviderConfig{
			"default-codex": {Type: "codex-cli", BaseURL: "/old/default"},
			"writer-codex":  {Type: "codex-cli", BaseURL: "/old/writer"},
		},
		Roles: map[string]bootstrap.RoleConfig{
			"writer": {Provider: "writer-codex", Model: "writer-model"},
		},
	}
	bound, err := bindSealedConvergenceSeededCompactWriter(cfg, binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bound.Providers["writer-codex"].BaseURL; got != binaryPath {
		t.Fatalf("writer binary=%q, want %q", got, binaryPath)
	}
	if got := bound.Providers["default-codex"].BaseURL; got != "/old/default" {
		t.Fatalf("unrelated provider was rebound: %q", got)
	}
	if got := cfg.Providers["writer-codex"].BaseURL; got != "/old/writer" {
		t.Fatalf("input config was mutated: %q", got)
	}
	if _, err := bindSealedConvergenceSeededCompactWriter(cfg, "relative/codex"); err == nil {
		t.Fatal("relative codex path was accepted")
	}
	nonExecutable := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(nonExecutable, []byte("no execute bit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bindSealedConvergenceSeededCompactWriter(cfg, nonExecutable); err == nil {
		t.Fatal("non-executable codex path was accepted")
	}
}

func TestSeededCompactFinalizeExposesRunActivityOnError(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	model := &seededCompactCaptureModel{responses: []agentcore.Message{seededCompactToolCall("bad-binding")}}
	tool := agentcore.NewFuncTool(
		"plan_details", "allowlist", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("sealed convergence authority overlay rejected plan_details project-all-state injection")
		},
	)
	err := runSealedConvergencePlannerSeededCompactFinalizeWithModel(
		context.Background(), st, 5, "seeded", model, "codex", "gpt-test", tool,
	)
	if err == nil {
		t.Fatal("deterministic tool failure unexpectedly succeeded")
	}
	activity, ok := SealedConvergencePlannerSeededCompactActivity(err)
	if !ok || activity.AssistantMessageStarts != 1 || activity.ToolExecutions != 1 {
		t.Fatalf("run activity unavailable or incorrect: ok=%v activity=%+v err=%v", ok, activity, err)
	}
	var typed *SealedConvergencePlannerSeededCompactRunError
	if !errors.As(err, &typed) || typed.Cause == nil {
		t.Fatalf("exported seeded compact run error unavailable: %T %v", err, err)
	}
}
