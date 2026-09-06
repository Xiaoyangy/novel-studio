package llmcodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func TestExactAgentWindowBudgetPreservesLargePacketAndItsCompleteFeedbackCall(t *testing.T) {
	payload := "BEGIN-EXACT\n" + strings.Repeat(`{"id":"src_0123456789abcdef0123456789abcdef","事实":"原始数据"}`, 1700) + "\nEND-EXACT"
	packet := exactAgentPacketTestMessage(t, payload)
	system := "SYSTEM-BEGIN" + strings.Repeat("system instruction;", 2700) + "SYSTEM-END"
	parameters := map[string]any{"description": "TOOL-BEGIN" + strings.Repeat("原始参数规则", 6000) + "TOOL-END"}
	specs := []agentcore.ToolSpec{{Name: "resolve_chapter_world", Description: "完整工具说明", Parameters: parameters}}
	callArgs := json.RawMessage(fmt.Sprintf(`{"action":%q}`, "CALL-BEGIN"+strings.Repeat("abcdef123456", 4000)+"CALL-END"))
	call := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "actual-latest-call", Name: specs[0].Name, Args: callArgs})}}
	feedback := "FEEDBACK-BEGIN" + strings.Repeat("校验错误", 3000) + "FEEDBACK-END"
	messages := []agentcore.Message{{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(system)}}, packet}
	for i := 0; i < 20; i++ {
		messages = append(messages, agentcore.UserMsg(fmt.Sprintf("OLD-%02d:", i)+strings.Repeat("history ", 900)))
	}
	messages = append(messages, call, agentcore.ToolResultMsg("actual-latest-call", json.RawMessage(fmt.Sprintf("%q", feedback)), true), agentcore.UserMsg("LATEST-REPAIR-INSTRUCTION"))
	if _, err := buildCodexPromptChecked(messages, specs); err == nil {
		t.Fatal("legacy no-window exact budget unexpectedly expanded")
	}
	budget := codexExactAgentBudget{contextWindow: 372_000}
	prompt, err := buildCodexPromptWithExactBudget(messages, specs, budget)
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(parameters)
	for _, exact := range []string{payload, system, string(params), string(callArgs), feedback, "LATEST-REPAIR-INSTRUCTION"} {
		if !strings.Contains(prompt, exact) {
			t.Fatal("windowed path cut a complete packet, system, schema, feedback or its original tool call")
		}
	}
	if utf8.RuneCountInString(prompt) <= codexPromptRuneBudget || strings.Contains(prompt, "Codex 入参压缩") || strings.Count(prompt, payload) != 1 {
		t.Fatal("large input was not preserved beyond the legacy rune cap exactly once")
	}
	oldTokens, oldCount := 0, 0
	for _, message := range messages {
		if strings.HasPrefix(message.TextContent(), "OLD-") && strings.Contains(prompt, message.TextContent()) {
			oldCount++
			oldTokens += corecontext.EstimateTokens(agentcore.UserMsg(codexExactHistoryMessage(message)))
		}
	}
	if oldCount == 0 || oldCount == 20 || oldTokens > codexExactOldHistoryTokens {
		t.Fatalf("large window filled with unnecessary history: %d messages/%d estimated tokens", oldCount, oldTokens)
	}
	budget.responseSchema, _ = json.Marshal(buildResponseSchema(specs))
	estimate, err := budget.check(prompt)
	if err != nil || estimate.inputTokens <= 0 || estimate.guardedTokens > estimate.inputLimit {
		t.Fatal("assembled exact prompt does not satisfy the full operating budget")
	}
	// Repeated exact input keeps its bytes once even during a tool retry.
	messages = append(messages, packet)
	retry, err := buildCodexPromptWithExactBudget(messages, specs, budget)
	if err != nil || strings.Count(retry, payload) != 1 || !strings.Contains(retry, string(callArgs)) || !strings.Contains(retry, feedback) {
		t.Fatal("retry duplicated or dropped complete source/call/feedback")
	}
}

func TestExactAgentWindowBudgetUsesEstimatedTokensNotRuneCounts(t *testing.T) {
	specs := []agentcore.ToolSpec{{Name: "resolve_chapter_world"}}
	budget := codexExactAgentBudget{contextWindow: 128_000}
	ascii, err := buildCodexPromptWithExactBudget([]agentcore.Message{exactAgentPacketTestMessage(t, strings.Repeat("a", 80_000))}, specs, budget)
	if err != nil || ascii == "" {
		t.Fatal("valid ASCII input was treated as 80k tokens")
	}
	if _, err := buildCodexPromptWithExactBudget([]agentcore.Message{exactAgentPacketTestMessage(t, strings.Repeat("汉", 80_000))}, specs, budget); err == nil || !strings.Contains(err.Error(), "estimated_input_tokens") {
		t.Fatal("same-rune CJK input ignored the existing CJK-aware estimator")
	}
	budget.responseSchema = []byte(`{"description":"output envelope"}`)
	estimate, err := budget.check("complete prompt")
	if err != nil || estimate.inputTokens != corecontext.EstimateTokens(agentcore.UserMsg("complete prompt"))+corecontext.EstimateTokens(agentcore.UserMsg(string(budget.responseSchema))) || estimate.outputReserve != 32768 || estimate.inputLimit != 87040 {
		t.Fatalf("budget formula or output-schema accounting changed: %+v %v", estimate, err)
	}
	budget.contextWindow = math.MaxInt
	if estimate, err := budget.check("small"); err != nil || estimate.inputLimit != codexExactAbsoluteInputTokens {
		t.Fatal("extreme configured window escaped the absolute estimated input ceiling")
	}
	budget.maxOutputTokens = math.MaxInt
	if estimate, err := budget.check("small"); err == nil || estimate.inputLimit != 0 {
		t.Fatal("huge requested output wrapped the input allowance")
	}
}

func TestExactAgentWindowBudgetRejectsBeforeAnyCLIOrReportedUsage(t *testing.T) {
	for _, mode := range []string{"packet", "system", "schema", "invalid_schema", "invalid_system_utf8", "feedback", "paired_call", "absolute_tokens", "absolute_bytes", "output_reserve", "invalid_window"} {
		t.Run(mode, func(t *testing.T) {
			called := filepath.Join(t.TempDir(), "cli-called")
			t.Setenv("EXACT_BUDGET_CLI_CALL_LOG", called)
			fake := usageFakeCLIWithMCP(t, `printf 'inventory' > "$EXACT_BUDGET_CLI_CALL_LOG"; printf '[]'; exit 0`, `printf 'exec' > "$EXACT_BUDGET_CLI_CALL_LOG"; exit 90`)
			window := 64_000
			messages := []agentcore.Message{exactAgentPacketTestMessage(t, "small exact packet")}
			specs := []agentcore.ToolSpec{{Name: "resolve_chapter_world"}}
			var options []agentcore.CallOption
			switch mode {
			case "packet":
				messages[0] = exactAgentPacketTestMessage(t, strings.Repeat("x", 160_000))
			case "system":
				messages = append(messages, agentcore.Message{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("s", 160_000))}})
			case "schema":
				specs[0].Parameters = map[string]any{"description": strings.Repeat("s", 160_000)}
			case "invalid_schema":
				specs[0].Parameters = map[string]any{"unserializable": make(chan int)}
			case "invalid_system_utf8":
				messages = append(messages, agentcore.Message{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(string([]byte{255}))}})
			case "feedback":
				messages = append(messages, agentcore.ToolResultMsg("feedback", json.RawMessage(fmt.Sprintf("%q", strings.Repeat("f", 160_000))), true))
			case "paired_call":
				messages = append(messages, agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "must-keep", Name: specs[0].Name, Args: json.RawMessage(fmt.Sprintf(`{"value":%q}`, strings.Repeat("a", 160_000)))})}}, agentcore.ToolResultMsg("must-keep", json.RawMessage(`"latest error"`), true))
			case "absolute_tokens":
				window = 10_000_000
				messages[0] = exactAgentPacketTestMessage(t, strings.Repeat("汉", 160_000))
			case "absolute_bytes":
				window = 10_000_000
				messages[0] = exactAgentPacketTestMessage(t, strings.Repeat("x", codexExactAbsoluteInputBytes))
			case "output_reserve":
				options = []agentcore.CallOption{agentcore.WithMaxTokens(64_000)}
			case "invalid_window":
				window = -1
			}
			model := New(fake.binary, fake.model, "", WithContextWindow(window))
			before := codexExecCallSeq.Load()
			response, err := model.Generate(context.Background(), messages, specs, options...)
			var billed *UsageError
			if err == nil || response != nil || errors.As(err, &billed) || !strings.Contains(err.Error(), "exact agent packet") || codexExecCallSeq.Load() != before {
				t.Fatalf("budget rejection became a provider call or reported usage: %v", err)
			}
			if _, err := os.Stat(called); !os.IsNotExist(err) {
				t.Fatal("budget failure launched even the CLI discovery process")
			}
			stream, streamErr := model.GenerateStream(context.Background(), messages, specs, options...)
			if streamErr == nil || stream != nil || errors.As(streamErr, &billed) || codexExecCallSeq.Load() != before {
				t.Fatalf("stream preflight was not a direct unexecuted rejection: %v", streamErr)
			}
			if _, err := os.Stat(called); !os.IsNotExist(err) {
				t.Fatal("stream budget failure created a CLI process")
			}
		})
	}
}

func TestExactAgentWindowBudgetDoesNotExpandOrdinaryInputs(t *testing.T) {
	messages := []agentcore.Message{agentcore.UserMsg(strings.Repeat("普通文本", 40_000))}
	specs := []agentcore.ToolSpec{{Name: "some_tool"}}
	legacy, err := buildCodexPromptChecked(messages, specs)
	if err != nil {
		t.Fatal(err)
	}
	windowed, err := buildCodexPromptWithExactBudget(messages, specs, codexExactAgentBudget{contextWindow: 1_000_000})
	if err != nil || windowed != legacy {
		t.Fatal("model window changed the ordinary compaction policy")
	}
	if New("/unused", "unknown", "").ExactAgentContextWindow() != 0 {
		t.Fatal("unspecified model acquired an assumed context window")
	}
}

func TestExactAgentWindowBudgetLeavesPlainCompletionUnchanged(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "plain")
	t.Setenv("EXACT_BUDGET_PLAIN_CAPTURE", capture)
	fake := usageFakeCLI(t, `printf '%s' "$prompt" > "$EXACT_BUDGET_PLAIN_CAPTURE"; printf 'plain answer' > "$out"`)
	model := New(fake.binary, fake.model, "", WithContextWindow(1))
	messages := []agentcore.Message{agentcore.UserMsg(strings.Repeat("普通摘要材料", 1000))}
	response, err := model.Generate(context.Background(), messages, nil)
	if err != nil || response.Message.TextContent() != "plain answer" {
		t.Fatalf("exact-only budget changed a plain completion: %v", err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil || string(raw) != buildPlainPrompt(messages) {
		t.Fatal("configured exact budget changed the plain prompt bytes")
	}
}

func TestExactAgentWindowBudgetSendsCompleteLargePacketButOnlyReportsActualUsage(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "prompt")
	t.Setenv("EXACT_BUDGET_PROMPT_CAPTURE", capture)
	fake := usageFakeCLI(t, `printf '%s' "$prompt" > "$EXACT_BUDGET_PROMPT_CAPTURE"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1234,"cached_input_tokens":0,"output_tokens":56}}'
printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"complete"}' > "$out"`)
	model := New(fake.binary, fake.model, "", WithContextWindow(200_000))
	payload := "EXACT-START" + strings.Repeat("data-value-0123456789 ", 6000) + "EXACT-END"
	response, err := model.Generate(context.Background(), []agentcore.Message{exactAgentPacketTestMessage(t, payload)}, []agentcore.ToolSpec{{Name: "resolve_chapter_world"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(raw), payload) || utf8.RuneCount(raw) <= codexPromptRuneBudget {
		t.Fatal("fake provider did not receive the complete large packet")
	}
	if response.Message.Usage == nil || response.Message.Usage.Input != 1234 || response.Message.Usage.Output != 56 || response.Message.Metadata["codex_usage_source"] != "reported" {
		t.Fatal("operating estimate replaced actual provider token accounting")
	}
}
