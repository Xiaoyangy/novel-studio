package llmcodex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func TestBuildResponseSchemaStrictCompliant(t *testing.T) {
	schema := buildResponseSchema(nil)
	// OpenAI 严格结构化输出：additionalProperties:false + 所有 property required。
	if schema["additionalProperties"] != false {
		t.Fatalf("顶层 additionalProperties 应为 false: %+v", schema)
	}
	req := schema["required"].([]string)
	if len(req) != 4 {
		t.Fatalf("严格模式要求所有字段 required（action/tool_name/arguments_json/text）: %+v", req)
	}
	props := schema["properties"].(map[string]any)
	// arguments_json 是 nullable 字符串（承载工具参数 JSON），不是自由 object。
	aj := props["arguments_json"].(map[string]any)
	if _, ok := aj["type"].([]string); !ok {
		t.Fatalf("arguments_json 应是 nullable 字符串: %+v", aj)
	}
}

func TestBuildCodexPromptSerializes(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "写第1章"}}},
	}
	tools := []agentcore.ToolSpec{{Name: "plan_chapter", Description: "保存计划", Parameters: map[string]any{"type": "object"}}}
	p := buildCodexPrompt(msgs, tools)
	for _, want := range []string{"plan_chapter", "保存计划", "写第1章", "output schema", "不要执行任何 shell"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt 缺 %q:\n%s", want, p)
		}
	}
}

func TestParseCodexResponseToolCall(t *testing.T) {
	// arguments_json 是 JSON 字符串（严格结构化输出无法用自由 object）。
	raw := `{"action":"tool_call","tool_name":"plan_chapter","arguments_json":"{\"chapter\":1,\"title\":\"开局\"}","text":null}`
	msg, err := parseCodexResponse(raw, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	calls := msg.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "plan_chapter" {
		t.Fatalf("应解析出 plan_chapter 工具调用: %+v", calls)
	}
	var args map[string]any
	json.Unmarshal(calls[0].Args, &args)
	if args["chapter"].(float64) != 1 {
		t.Fatalf("参数丢失: %v", args)
	}
	if msg.StopReason != agentcore.StopReasonToolUse {
		t.Fatalf("stop reason 应为 toolUse")
	}
}

func TestParseCodexResponseFinalText(t *testing.T) {
	raw := "```json\n{\"action\":\"final\",\"text\":\"完成了\"}\n```"
	msg, err := parseCodexResponse(raw, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msg.ToolCalls()) != 0 {
		t.Fatal("final 不应有工具调用")
	}
	if strings.TrimSpace(msg.TextContent()) != "完成了" {
		t.Fatalf("文本解析错: %q", msg.TextContent())
	}
}

func TestParseCodexResponseFallbackToText(t *testing.T) {
	// 非结构化输出兜底为文本，不崩。
	msg, err := parseCodexResponse("就是一段自由文本", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(msg.TextContent(), "自由文本") {
		t.Fatalf("兜底文本错: %q", msg.TextContent())
	}
}

func TestSupportsTools(t *testing.T) {
	if !New("", "gpt-5.5", "high").SupportsTools() {
		t.Fatal("应支持工具")
	}
}
