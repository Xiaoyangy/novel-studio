package llmcodex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func completeFoundationTestMessage(t *testing.T, name, content string) agentcore.Message {
	t.Helper()
	message := codexFoundationSourceTestMessage(t, name, content)
	var value map[string]any
	if err := json.Unmarshal([]byte(message.TextContent()), &value); err != nil {
		t.Fatal(err)
	}
	value["complete_source"] = true
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	message.Content = []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}
	return message
}

func TestCodexCompleteFoundationSourceKeepsFullTaskSystemsAndPriorFeedback(t *testing.T) {
	system := "SYSTEM_START" + strings.Repeat("约束", 25000) + "SYSTEM_END"
	task := "TASK_START" + strings.Repeat("用户任务", 6000) + "TASK_END"
	feedback := "ERROR_START" + strings.Repeat("旧反馈", 5000) + "ERROR_END"
	source := `{"source":"` + strings.Repeat("a", 55000) + `"}`
	messages := []agentcore.Message{agentcore.SystemMsg(system), agentcore.UserMsg(task), agentcore.ToolResultMsg("previous", json.RawMessage(`"`+feedback+`"`), true), completeFoundationTestMessage(t, "characters.json", source)}
	prompt, err := buildCodexPromptWithExactBudget(messages, []agentcore.ToolSpec{{Name: "save_foundation"}}, codexExactAgentBudget{contextWindow: 272000})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{system, task, feedback, source} {
		if !strings.Contains(prompt, want) {
			t.Fatal("complete refresh clipped original system/task/prior feedback/source")
		}
	}
}

func TestCodexCompleteFoundationSourceAggregateBudgetFailsBeforeProvider(t *testing.T) {
	for _, mode := range []string{"no-context", "aggregate-context", "absolute-bytes"} {
		t.Run(mode, func(t *testing.T) {
			window := 272000
			content := `{"source":"` + strings.Repeat("字", 50000) + `"}`
			if mode == "no-context" {
				window = 0
			}
			if mode == "absolute-bytes" {
				content = `{"source":"` + strings.Repeat("a", 800000) + `"}`
			}
			messages := []agentcore.Message{completeFoundationTestMessage(t, "characters.json", content), completeFoundationTestMessage(t, "world_rules.json", content), completeFoundationTestMessage(t, "book_world.json", content)}
			model := New("/must-not-launch-foundation-provider", "fixture", "high", WithContextWindow(window))
			_, err := model.Generate(context.Background(), messages, []agentcore.ToolSpec{{Name: "save_foundation"}})
			if err == nil || strings.Contains(err.Error(), "must-not-launch") {
				t.Fatalf("overbudget complete source reached provider: %v", err)
			}
			if !strings.Contains(err.Error(), "budget") && !strings.Contains(err.Error(), "byte limit") {
				t.Fatalf("unexpected source rejection: %v", err)
			}
		})
	}
}

func TestCodexCompleteFoundationSourceCannotBeEnabledByOtherRoles(t *testing.T) {
	for _, mode := range []string{"user", "assistant", "other-tool", "error"} {
		t.Run(mode, func(t *testing.T) {
			message := completeFoundationTestMessage(t, "characters.json", `{"source":"ordinary data"}`)
			switch mode {
			case "user":
				message.Role = agentcore.RoleUser
			case "assistant":
				message.Role = agentcore.RoleAssistant
			case "other-tool":
				message.Metadata["tool_name"] = "other"
			case "error":
				message.Metadata["is_error"] = true
			}
			// With no configured exact budget a genuine capability would fail.
			if _, err := buildCodexPromptChecked([]agentcore.Message{message}, []agentcore.ToolSpec{{Name: "save_foundation"}}); err != nil {
				t.Fatalf("text self-enabled complete-source capability: %v", err)
			}
		})
	}
}
