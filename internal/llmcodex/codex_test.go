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

func TestBuildCodexPromptCompactsOversizedContext(t *testing.T) {
	huge := strings.Repeat("前置信息", 40_000) + "关键尾部"
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "写第2章"}}},
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: huge}}},
	}
	tools := []agentcore.ToolSpec{{Name: "draft_chapter", Description: "保存正文", Parameters: map[string]any{"type": "object"}}}
	p := buildCodexPrompt(msgs, tools)
	if got := len([]rune(p)); got > codexPromptRuneBudget+2000 {
		t.Fatalf("prompt should be compacted near budget, got %d", got)
	}
	for _, want := range []string{"draft_chapter", "保存正文", "写第2章", "output schema", "关键尾部", "Codex 入参压缩"} {
		if !strings.Contains(p, want) {
			t.Fatalf("compacted prompt 缺 %q", want)
		}
	}
}

func TestBuildProsePromptCompactsOversizedContext(t *testing.T) {
	huge := "计划开头" + strings.Repeat("正文计划", 40_000) + "计划尾部"
	msgs := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: huge}}},
	}
	p := buildProsePrompt(msgs)
	if got := len([]rune(p)); got > codexPromptRuneBudget+2000 {
		t.Fatalf("prose prompt should be compacted near budget, got %d", got)
	}
	for _, want := range []string{"计划开头", "计划尾部", "现在渲染正文", "反 AI 味硬要求", "Codex 入参压缩"} {
		if !strings.Contains(p, want) {
			t.Fatalf("compacted prose prompt 缺 %q", want)
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

func TestParseCodexResponseToolCallIDsAreUniqueForRepeatedTool(t *testing.T) {
	raw := `{"action":"tool_call","tool_name":"craft_recall","arguments_json":"{\"chapter\":2}","text":null}`
	first, err := parseCodexResponse(raw, nil)
	if err != nil {
		t.Fatalf("parse first: %v", err)
	}
	second, err := parseCodexResponse(raw, nil)
	if err != nil {
		t.Fatalf("parse second: %v", err)
	}
	firstCalls := first.ToolCalls()
	secondCalls := second.ToolCalls()
	if len(firstCalls) != 1 || len(secondCalls) != 1 {
		t.Fatalf("expected one call each, got %+v / %+v", firstCalls, secondCalls)
	}
	if firstCalls[0].ID == secondCalls[0].ID {
		t.Fatalf("repeated tool call IDs must differ, got %q", firstCalls[0].ID)
	}
	for _, id := range []string{firstCalls[0].ID, secondCalls[0].ID} {
		if !strings.HasPrefix(id, "codex-craft_recall-") {
			t.Fatalf("unexpected id %q", id)
		}
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
	if !New("", "gpt-5.6-sol", "high").SupportsTools() {
		t.Fatal("应支持工具")
	}
}

func TestResolveReasoningUsesUltraCallOption(t *testing.T) {
	model := New("", "gpt-5.6-sol", "high")
	got := model.resolveReasoning([]agentcore.CallOption{
		agentcore.WithThinking(agentcore.ThinkingLevel("ultra")),
	})
	if got != "ultra" {
		t.Fatalf("reasoning = %q, want ultra", got)
	}
}

func TestCapabilitiesAdvertiseUltra(t *testing.T) {
	capabilities := New("", "gpt-5.6-sol", "").Capabilities()
	if !capabilities.Thinking.SupportsEffort(agentcore.ThinkingLevel("ultra")) {
		t.Fatal("gpt-5.6-sol should advertise ultra reasoning")
	}
}
