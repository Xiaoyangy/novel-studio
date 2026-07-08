package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

type deepseekJudgeCaptureModel struct {
	messages []agentcore.Message
	opts     []agentcore.CallOption
}

func (m *deepseekJudgeCaptureModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = append([]agentcore.Message(nil), messages...)
	m.opts = append([]agentcore.CallOption(nil), opts...)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"verdict":"ai_like",
			"risk_level":"high",
			"ai_probability_percent":70,
			"confidence":"high",
			"summary":"对白和段落过于规整。",
			"reasons":["连续对话像流程节点"],
			"revision_plan":["打散连续说明段"],
			"dialogue_fix_plan":["省略可辨识说话标签"],
			"author_voice_plan":["保留程序员判断但落到现场证据"],
			"rag_rules":["后续避免每行都写说问答"]
		}`)},
	}}, nil
}

func (m *deepseekJudgeCaptureModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *deepseekJudgeCaptureModel) SupportsTools() bool { return false }

func TestRunDeepSeekAIJudgeUsesRawChapterBodyAndMaxThinking(t *testing.T) {
	model := &deepseekJudgeCaptureModel{}
	chapter := "第一章 样本M17\n\n许闻溪把审计盒接上。\n\n“先别解释。”"

	artifact, err := runDeepSeekAIJudge(model, deepseekAIJudgeModelSelection{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		Explicit: true,
	}, 1, chapter, time.Second)
	if err != nil {
		t.Fatalf("runDeepSeekAIJudge: %v", err)
	}
	if artifact == nil || !artifact.RawBodyOnly || artifact.BodySHA256 == "" {
		t.Fatalf("artifact raw-body metadata missing: %+v", artifact)
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(model.messages))
	}
	if got := model.messages[1].TextContent(); got != chapter {
		t.Fatalf("user message must be raw chapter body only\ngot: %q\nwant:%q", got, chapter)
	}
	if strings.Contains(model.messages[1].TextContent(), "判断是否为ai") {
		t.Fatalf("user message leaked task description: %q", model.messages[1].TextContent())
	}
	cfg := agentcore.ResolveCallConfig(model.opts)
	if cfg.ThinkingLevel != agentcore.ThinkingMax {
		t.Fatalf("thinking level = %q, want max", cfg.ThinkingLevel)
	}
	if !artifact.Blocking || artifact.Verdict != "ai_like" || artifact.AIProbabilityPercent != 70 {
		t.Fatalf("artifact verdict/blocking = %+v", artifact)
	}
}

func TestBuildRevisionPlanIncludesDeepSeekBlockingJudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reviews"), 0o755); err != nil {
		t.Fatal(err)
	}
	json := `{
		"chapter":1,
		"provider":"deepseek",
		"model":"deepseek-v4-pro",
		"reasoning_effort":"max",
		"raw_body_only":true,
		"verdict":"ai_like",
		"risk_level":"high",
		"ai_probability_percent":70,
		"blocking":true,
		"reasons":["对白像流程节点"],
		"revision_plan":["把说明藏进动作和误判"],
		"dialogue_fix_plan":["去掉不必要的说话标签"],
		"author_voice_plan":["保留程序员判断但别写成说明书"],
		"rag_rules":["连续双人对白可省标签"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "reviews", "01_deepseek_ai_judge.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := buildRevisionPlan(dir, 1, "第一章\n\n许闻溪把线接好。\n\n“先别解释。”", "")
	if !plan.HasRed {
		t.Fatalf("expected DeepSeek blocking judge to set red, got %+v", plan)
	}
	if !containsString(plan.Sources, "reviews/01_deepseek_ai_judge.json") {
		t.Fatalf("expected deepseek source, got %+v", plan.Sources)
	}
	if !strings.Contains(plan.Brief, "DeepSeek 裸正文 AI 判定") || !strings.Contains(plan.Brief, "去掉不必要的说话标签") {
		t.Fatalf("brief missing DeepSeek guidance:\n%s", plan.Brief)
	}
}

func TestExtractJSONObjectFromFencedResponse(t *testing.T) {
	got := extractJSONObject("```json\n{\"verdict\":\"mixed\"}\n```")
	if got != `{"verdict":"mixed"}` {
		t.Fatalf("extractJSONObject = %q", got)
	}
}
