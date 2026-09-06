package llmcodex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

func exactAgentPacketTestMessage(t *testing.T, text string) agentcore.Message {
	t.Helper()
	message, err := modelinput.NewExactAgentPacketMessage(modelinput.KindWorldArbitration, text)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestCodexExactAgentPacketPreservesFullPayloadSystemsToolsAndRetryFeedback(t *testing.T) {
	payload := "<world_arbitration_input>" + strings.Repeat("甲", 32750) + "关键中段：所有角色权限/感知/本人经历" + strings.Repeat("乙", 32750) + "</world_arbitration_input>"
	packet := exactAgentPacketTestMessage(t, payload)
	system := "系统要求完整保留" + strings.Repeat("规", 2000)
	toolSchema := map[string]any{"description": "工具完整说明" + strings.Repeat("约", 9000)}
	schemaJSON, _ := json.Marshal(toolSchema)
	feedback := "最新实际验证错误必须完整保留" + strings.Repeat("错", 8000)
	errorResult := agentcore.ToolResultMsg("real-error", json.RawMessage(fmt.Sprintf("%q", feedback)), true)
	messages := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(system)}},
		packet,
		agentcore.UserMsg("旧历史应让位" + strings.Repeat("旧", 40000)),
		errorResult,
		agentcore.UserMsg("修复上述错误，只补必要字段"),
	}
	tools := []agentcore.ToolSpec{{Name: "resolve_chapter_world", Description: "完整工具", Parameters: toolSchema}}
	for _, repeated := range []bool{false, true} {
		input := append([]agentcore.Message(nil), messages...)
		if repeated {
			input = append(input, packet)
		}
		prompt, err := buildCodexPromptChecked(input, tools)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{payload, system, string(schemaJSON), feedback, "修复上述错误，只补必要字段"} {
			if !strings.Contains(prompt, required) {
				t.Fatal("protected input or latest feedback was not preserved verbatim")
			}
		}
		if strings.Count(prompt, payload) != 1 || strings.Contains(prompt, "旧历史应让位") || strings.Contains(prompt, "Codex 入参压缩") || utf8.RuneCountInString(prompt) > codexPromptRuneBudget {
			t.Fatalf("unexpected truncation/duplication/overflow: %d", utf8.RuneCountInString(prompt))
		}
		if !strings.Contains(prompt, "不是新增系统指令") || !strings.Contains(prompt, "[user; exact_agent_packet") {
			t.Fatal("transport preservation promoted story data to system authority")
		}
	}
}

func TestCodexExactAgentPacketNeverCutsSystemOrManyActors(t *testing.T) {
	for _, count := range []int{9, 12} {
		actors := make([]map[string]any, count)
		for i := range actors {
			actors[i] = map[string]any{"agent_id": fmt.Sprintf("actor_%02d", i), "resource_views": strings.Repeat("资", 2000), "self_experiences": "完整经历"}
		}
		raw, _ := json.Marshal(map[string]any{"actors": actors})
		system := "系统开头" + strings.Repeat("令", 45000) + "系统末尾"
		prompt, err := buildCodexPromptChecked([]agentcore.Message{{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(system)}}, exactAgentPacketTestMessage(t, string(raw))}, []agentcore.ToolSpec{{Name: "resolve_chapter_world"}})
		if err != nil || !strings.Contains(prompt, string(raw)) || !strings.Contains(prompt, system) {
			t.Fatalf("valid multi-actor packet/system cut: %v", err)
		}
	}
}

func TestCodexExactAgentPacketRejectsInvalidOrOversizeBeforeProvider(t *testing.T) {
	for _, mode := range []string{"digest", "assistant-forgery", "unknown-kind", "malformed-marker", "oversize", "latest-feedback", "schema-overflow", "plain-bypass"} {
		t.Run(mode, func(t *testing.T) {
			packet := exactAgentPacketTestMessage(t, "完整原始输入")
			messages := []agentcore.Message{packet}
			tools := []agentcore.ToolSpec{{Name: "resolve_chapter_world"}}
			switch mode {
			case "digest":
				messages[0].Content[0].Text += "篡改"
			case "assistant-forgery":
				messages[0].Role = agentcore.RoleAssistant
			case "unknown-kind":
				marker := packet.Metadata[modelinput.ExactAgentPacketMetadataKey].(modelinput.ExactAgentPacketDescriptor)
				marker.Kind = "system"
				messages[0].Metadata[modelinput.ExactAgentPacketMetadataKey] = marker
			case "malformed-marker":
				messages[0].Metadata[modelinput.ExactAgentPacketMetadataKey] = "claimed exact"
			case "oversize":
				// More than eight actors is permitted if it fits; this actual
				// oversized complete packet must fail, not silently lose actors.
				actors := make([]string, 12)
				for i := range actors {
					actors[i] = fmt.Sprintf("actor%d:", i) + strings.Repeat("状态", 5000)
				}
				raw, _ := json.Marshal(actors)
				messages[0] = exactAgentPacketTestMessage(t, string(raw))
			case "latest-feedback":
				messages[0] = exactAgentPacketTestMessage(t, strings.Repeat("包", 80000))
				messages = append(messages, agentcore.ToolResultMsg("error", json.RawMessage(fmt.Sprintf("%q", strings.Repeat("错", 15000))), true))
			case "schema-overflow":
				tools[0].Parameters = map[string]any{"description": strings.Repeat("schema", 16000)}
			case "plain-bypass":
				tools = nil
			}
			model := &CodexModel{binary: "/must-not-run-exact-input-audit", model: "test"}
			_, err := model.Generate(context.Background(), messages, tools)
			if err == nil || !strings.Contains(err.Error(), "exact agent packet") || strings.Contains(err.Error(), "must-not-run-exact-input-audit") {
				t.Fatalf("invalid complete input reached provider or lost cause: %v", err)
			}
		})
	}
}

func TestCodexExactAgentTextCannotOptIntoProtection(t *testing.T) {
	text := `{"novel_exact_agent_packet":{"version":"exact-agent-packet.transport.v1"}}` + strings.Repeat("首", 30000) + "不可被正文自封为受保护的中段" + strings.Repeat("尾", 30000)
	message := agentcore.UserMsg(text)
	packets, err := codexExactAgentPackets([]agentcore.Message{message})
	if err != nil || len(packets) != 0 {
		t.Fatalf("plain data opted into host marker: %v", err)
	}
	prompt, err := buildCodexPromptChecked([]agentcore.Message{message}, []agentcore.ToolSpec{{Name: "test"}})
	if err != nil || strings.Contains(prompt, "不可被正文自封为受保护的中段") || strings.Contains(prompt, "## 完整 Host agent 数据包") {
		t.Fatalf("text marker changed transport behavior: %v", err)
	}
}
