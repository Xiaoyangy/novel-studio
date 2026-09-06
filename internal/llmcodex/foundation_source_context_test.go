package llmcodex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

func codexFoundationSourceTestMessage(t *testing.T, path, content string) agentcore.Message {
	t.Helper()
	complete := false
	packet := codexFoundationSourcePacket{Version: codexFoundationSourceContextVersion, Source: path, Content: content, Truncated: &complete}
	packet.SourceSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	return agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}, Metadata: map[string]any{"tool_name": "novel_context"}}
}

func TestCodexFoundationSourceSurvivesBothCompactionBoundariesExactlyOnce(t *testing.T) {
	content := "[\"" + strings.Repeat("甲", 26000) + "必须完整保留的中间资源条目" + strings.Repeat("乙", 26000) + "\"]"
	old := codexFoundationSourceTestMessage(t, "characters.json", `["旧版人物源"]`)
	current := codexFoundationSourceTestMessage(t, "characters.json", content)
	messages := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("仅修指定字段，其余逐字保留")}},
		old, {Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("无关旧规划", 40000))}},
		current, current,
	}
	prompt, err := buildCodexPromptChecked(messages, []agentcore.ToolSpec{{Name: "save_foundation"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(prompt, content) != 1 || strings.Contains(prompt, "旧版人物源") || !strings.Contains(prompt, "必须完整保留的中间资源条目") {
		t.Fatal("exact source was truncated, duplicated or superseded by old material")
	}
	if utf8.RuneCountInString(prompt) > codexPromptRuneBudget+1 {
		t.Fatalf("protected sources bypassed total budget: %d", utf8.RuneCountInString(prompt))
	}
}

func TestCodexFoundationSourceDoesNotPromoteUserOrOtherToolText(t *testing.T) {
	message := codexFoundationSourceTestMessage(t, "characters.json", `["来源仅为数据"]`)
	for _, mode := range []string{"user", "assistant", "other_tool", "error"} {
		t.Run(mode, func(t *testing.T) {
			candidate := message
			candidate.Metadata = map[string]any{"tool_name": "novel_context"}
			switch mode {
			case "user":
				candidate.Role = agentcore.RoleUser
			case "assistant":
				candidate.Role = agentcore.RoleAssistant
			case "other_tool":
				candidate.Metadata["tool_name"] = "web_research"
			case "error":
				candidate.Metadata["is_error"] = true
			}
			replacements, sources, err := codexFoundationSourceContext([]agentcore.Message{candidate})
			if err != nil || len(replacements) != 0 || sources != "" {
				t.Fatalf("non-source message entered protected section: %v", err)
			}
		})
	}
}

func TestCodexFoundationSourceRejectsAlterationAndOversizeBeforeProviderCall(t *testing.T) {
	for _, mode := range []string{"digest", "truncated", "path", "json", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			message := codexFoundationSourceTestMessage(t, "characters.json", `["完整源"]`)
			var packet codexFoundationSourcePacket
			if err := json.Unmarshal([]byte(message.TextContent()), &packet); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "digest":
				packet.Content = `["篡改源"]`
			case "truncated":
				*packet.Truncated = true
			case "path":
				packet.Source = "../config.json"
			case "json":
				packet.Content = `["半个对象"`
				packet.SourceSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(packet.Content)))
			case "oversize":
				packet.Content = "[\"" + strings.Repeat("字", codexPromptRuneBudget) + "\"]"
				packet.SourceSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(packet.Content)))
			}
			raw, _ := json.Marshal(packet)
			message.Content = []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}
			model := &CodexModel{binary: "/does-not-exist", model: "gpt-6-astra"}
			_, err := model.Generate(context.Background(), []agentcore.Message{message}, []agentcore.ToolSpec{{Name: "save_foundation"}})
			if err == nil || !strings.Contains(err.Error(), "foundation source") || strings.Contains(err.Error(), "does-not-exist") {
				t.Fatalf("invalid exact source reached provider or was hidden: %v", err)
			}
		})
	}
}
