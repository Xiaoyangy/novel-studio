package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func initialTickTransportFixture(t *testing.T) (*store.Store, []agentcore.Message, json.RawMessage) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("transport", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "first", CoreEvent: "FIRST-CHAPTER-EXACT-CORE", Hook: "FIRST-CHAPTER-EXACT-HOOK"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json", "meta/user_rules.json"} {
		body := `{"text":"` + path + `-BEGIN-` + strings.Repeat("完整作者资料-", 1600) + `-MIDDLE-REQUIRED-END"}`
		if err := os.WriteFile(filepath.Join(st.Dir(), path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionWorldTick, TargetChapter: 1, Owner: "exact-world-tick-test"}); err != nil {
		t.Fatal(err)
	}
	raw, err := tools.NewContextTool(st, tools.References{}, "default").Execute(t.Context(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	result := agentcore.ToolResultMsg("ctx", raw, false)
	result.Metadata["tool_name"] = "novel_context"
	messages := []agentcore.Message{agentcore.SystemMsg("author only, no character knowledge transfer"), agentcore.UserMsg(contract.Block), result}
	return st, messages, raw
}

func initialTickFakeCodex(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fake-codex")
	capture := filepath.Join(dir, "input")
	calls := filepath.Join(dir, "calls")
	t.Setenv("INITIAL_TICK_CAPTURE", capture)
	t.Setenv("INITIAL_TICK_CALLS", calls)
	script := `#!/bin/sh
set -eu
if [ "$1" = mcp ]; then
 printf 'inventory\n' >> "$INITIAL_TICK_CALLS"
 printf '[]'
 exit 0
fi
printf 'exec\n' >> "$INITIAL_TICK_CALLS"
out=''
while [ "$#" -gt 0 ]; do
 case "$1" in -o|--output-last-message) shift; out="$1" ;; esac
 shift
done
cat > "$INITIAL_TICK_CAPTURE"
printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"offline capture only"}' > "$out"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return binary, capture, calls
}

func TestInitialWorldTickExactTransportAndBudget(t *testing.T) {
	st, messages, raw := initialTickTransportFixture(t)
	save := tools.NewSaveWorldTickTool(st)
	specs := []agentcore.ToolSpec{{Name: save.Name(), Description: save.Description(), Parameters: save.Schema()}}
	for _, window := range []int{272000, 64000} {
		t.Run(strconv.Itoa(window), func(t *testing.T) {
			binary, capture, calls := initialTickFakeCodex(t)
			model := withInitialWorldTickTransport(llmcodex.New(binary, "fixture", "", llmcodex.WithContextWindow(window)), st)
			response, err := model.Generate(context.Background(), messages, specs)
			if window == 64000 {
				if err == nil || !strings.Contains(err.Error(), "budget") {
					t.Fatalf("not fail-closed: %v", err)
				}
				if _, err := os.Stat(calls); !os.IsNotExist(err) {
					t.Fatal("budget failure launched provider process")
				}
				return
			}
			if err != nil || response == nil {
				t.Fatalf("capture: %v", err)
			}
			captured, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(captured), string(raw)) || strings.Count(string(captured), string(raw)) != 1 || strings.Contains(string(captured), "Codex 入参压缩") {
				t.Fatal("source truncated or duplicated")
			}
			parameters, _ := json.Marshal(save.Schema())
			if !strings.Contains(string(captured), string(parameters)) || !strings.Contains(string(captured), "FIRST-CHAPTER-EXACT-CORE") || !strings.Contains(string(captured), "FIRST-CHAPTER-EXACT-HOOK") {
				t.Fatal("tool schema or chapter boundary lost")
			}
			t.Logf("complete_context_bytes=%d final_codex_prompt_bytes=%d", len(raw), len(captured))
		})
	}
}

func TestInitialWorldTickTransportRejectsFalseSuccessAndPreservesErrors(t *testing.T) {
	st, messages, _ := initialTickTransportFixture(t)
	model := &initialWorldTickTransport{store: st}
	repeated := append(append([]agentcore.Message(nil), messages...), messages[2])
	transformedRepeated, err := model.messages(repeated)
	if err != nil {
		t.Fatal(err)
	}
	exactCount := 0
	for _, message := range transformedRepeated {
		if message.Role == agentcore.RoleUser && message.TextContent() == messages[2].TextContent() {
			exactCount++
		}
	}
	if exactCount != 1 {
		t.Fatal("repeated successful reads duplicated complete source")
	}
	wrong := append([]agentcore.Message(nil), messages...)
	wrong[2].Content = []agentcore.ContentBlock{agentcore.TextBlock(`{"version":"partial"}`)}
	if _, err := model.messages(wrong); err == nil {
		t.Fatal("partial successful tool result accepted")
	}
	wrong[2].Metadata = map[string]any{"tool_name": "novel_context", "is_error": true}
	transformed, err := model.messages(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(transformed[len(transformed)-1], wrong[2]) {
		t.Fatal("tool error rewritten as success")
	}
	if err := st.Runtime.ReleasePipelineExecution("exact-world-tick-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := model.messages(messages); err == nil {
		t.Fatal("expired initial session fell back to ordinary transport")
	}
	base := llmcodex.New("/never-run", "fixture", "high")
	if withInitialWorldTickTransport(base, st) != base {
		t.Fatal("ordinary generation model changed")
	}
}

func TestInitialWorldTickRealBookExactTransport(t *testing.T) {
	dir := os.Getenv("NOVEL_TICK_CONTEXT_COPY")
	if dir == "" {
		t.Skip("requires disposable book copy")
	}
	st := store.NewStore(dir)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionWorldTick, TargetChapter: 1, Owner: "real-book-transport"}); err != nil {
		t.Fatal(err)
	}
	defer st.Runtime.ReleasePipelineExecution("real-book-transport")
	raw, err := tools.NewContextTool(st, tools.References{}, "default").Execute(t.Context(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	toolResult := agentcore.ToolResultMsg("exact-context", raw, false)
	toolResult.Metadata["tool_name"] = "novel_context"
	messages := []agentcore.Message{agentcore.SystemMsg("作者只设置章零条件；不产生角色知情或新的写入权限。"), agentcore.UserMsg(contract.Block), toolResult}
	binary, capture, _ := initialTickFakeCodex(t)
	model := withInitialWorldTickTransport(llmcodex.New(binary, "fixture", "", llmcodex.WithContextWindow(272000)), st)
	save := tools.NewSaveWorldTickTool(st)
	specs := []agentcore.ToolSpec{{Name: save.Name(), Description: save.Description(), Parameters: save.Schema()}}
	if _, err := model.Generate(t.Context(), messages, specs); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(captured), string(raw)) != 1 || !strings.Contains(string(captured), contract.Block) || strings.Contains(string(captured), "Codex 入参压缩") {
		t.Fatal("real book source or exact first-chapter boundary lost")
	}
	t.Logf("real_source_packet=%d final_codex_stdin=%d original_tools=%v", len(raw), len(captured), []string{specs[0].Name})
}

type initialTickUsageError struct{}

func (*initialTickUsageError) Error() string { return "typed provider failure" }
func (*initialTickUsageError) LLMUsage() (*agentcore.Usage, string) {
	return &agentcore.Usage{Input: 17, Output: 3}, "reported"
}

type initialTickFailingModel struct {
	agentcore.ChatModel
	calls    int
	thinking agentcore.ThinkingLevel
	failure  error
}

func (m *initialTickFailingModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	m.thinking = agentcore.ResolveCallConfig(opts).ThinkingLevel
	return nil, m.failure
}
func (m *initialTickFailingModel) GenerateStream(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.calls++
	m.thinking = agentcore.ResolveCallConfig(opts).ThinkingLevel
	return nil, m.failure
}
func (*initialTickFailingModel) UsageAccountingBound() bool { return true }

func TestInitialWorldTickTransportPreservesThinkingMetadataAndTypedUsage(t *testing.T) {
	st, messages, _ := initialTickTransportFixture(t)
	base := llmcodex.New("/never-run", "gpt-6-astra", "high", llmcodex.WithContextWindow(272000))
	wrapped := withInitialWorldTickTransport(base, st).(*initialWorldTickTransport)
	if wrapped.ProviderName() != base.ProviderName() || !reflect.DeepEqual(wrapped.Info(), base.Info()) || !reflect.DeepEqual(wrapped.Capabilities(), base.Capabilities()) || wrapped.ExactAgentContextWindow() != 272000 || wrapped.SupportsTools() != base.SupportsTools() {
		t.Fatal("model capabilities/identity/window changed")
	}
	if level, ok := ResolveThinkingForModel(wrapped, agentcore.ThinkingHigh); !ok || level != agentcore.ThinkingHigh {
		t.Fatal("high reasoning changed")
	}
	sentinel := &initialTickUsageError{}
	failing := &initialTickFailingModel{ChatModel: base, failure: sentinel}
	model := withInitialWorldTickTransport(failing, st).(*initialWorldTickTransport)
	if !model.UsageAccountingBound() {
		t.Fatal("accounting boundary hidden")
	}
	_, err := model.Generate(t.Context(), messages, []agentcore.ToolSpec{{Name: "save_world_tick"}}, agentcore.WithThinking(agentcore.ThinkingHigh))
	var typed interface {
		error
		LLMUsage() (*agentcore.Usage, string)
	}
	if !errors.As(err, &typed) || err != sentinel || failing.calls != 1 || failing.thinking != agentcore.ThinkingHigh {
		t.Fatal("typed usage/options or single-call delegation changed")
	}
	_, err = model.GenerateStream(t.Context(), messages, []agentcore.ToolSpec{{Name: "save_world_tick"}}, agentcore.WithThinking(agentcore.ThinkingHigh))
	if !errors.As(err, &typed) || err != sentinel || failing.calls != 2 || failing.thinking != agentcore.ThinkingHigh {
		t.Fatal("stream lost typed usage/options")
	}
}
