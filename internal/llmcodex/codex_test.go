package llmcodex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBuildProseResponseSchemaStrictCompliant(t *testing.T) {
	schema := buildProseResponseSchema()
	if schema["additionalProperties"] != false {
		t.Fatalf("prose schema must reject extra properties: %+v", schema)
	}
	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "prose" {
		t.Fatalf("prose schema required = %v", required)
	}
	properties := schema["properties"].(map[string]any)
	if properties["prose"].(map[string]any)["type"] != "string" {
		t.Fatalf("prose field must be string: %+v", properties)
	}
}

func TestRunCodexProseUnwrapsSingleFieldSchema(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	argsLog := filepath.Join(dir, "args.log")
	t.Setenv("CODEX_ARGS_LOG", argsLog)
	body := `#!/bin/sh
out=""
printf '%s\n' "$@" > "$CODEX_ARGS_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
printf '%s' '{"prose":"第二章 测试\n\n这才是正文。"}' > "$out"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "ultra")
	prose, err := model.runCodexProse(context.Background(), "直接写正文", "ultra")
	if err != nil {
		t.Fatal(err)
	}
	if prose != "第二章 测试\n\n这才是正文。" {
		t.Fatalf("prose = %q", prose)
	}
	rawArgs, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	args := string(rawArgs)
	for _, want := range []string{"--ephemeral", "--ignore-rules", "multi_agent", "plugins", "apps", "browser_use", "computer_use", "-C"} {
		if !strings.Contains(args, want) {
			t.Fatalf("isolated prose call missing %q in args:\n%s", want, args)
		}
	}
}

func TestBuildCodexPromptSerializes(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "写第1章"}}},
	}
	tools := []agentcore.ToolSpec{{Name: "plan_chapter", Description: "保存计划", Parameters: map[string]any{"type": "object"}}}
	p := buildCodexPrompt(msgs, tools)
	for _, want := range []string{"plan_chapter", "保存计划", "写第1章", "output schema", "不要执行任何 shell", "--sandbox read-only", "外层运行时"} {
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

func TestBuildProsePromptStructurallyPrunesNovelContext(t *testing.T) {
	payload := map[string]any{
		"content":           "OLD_DRAFT_CONTENT_MUST_NOT_LEAK",
		"chapter_contract":  map[string]any{"marker": "FULL_CONTRACT_MUST_NOT_LEAK"},
		"causal_simulation": map[string]any{"marker": "FULL_SIMULATION_MUST_NOT_LEAK"},
		"rewrite_brief":     map[string]any{"marker": "REWRITE_BRIEF_MUST_NOT_LEAK"},
		"chapter_draft":     map[string]any{"marker": "CHAPTER_DRAFT_STATE_MUST_NOT_LEAK"},
		"characters":        map[string]any{"marker": "FULL_CHARACTER_LIST_MUST_NOT_LEAK"},
		"working_memory": map[string]any{
			"render_packet":            map[string]any{"goal": "保留本章五摊试点"},
			"draft_external_ai_review": map[string]any{"blocking": false, "evidence": []string{"已通过外审仍不应干扰正文"}},
			"world_codex":              strings.Repeat("无关世界法典", 20_000),
		},
		"reference_pack": map[string]any{
			"writing_engine":  map[string]any{"voice": "林澈冷幽默"},
			"retrieval_trace": strings.Repeat("无关检索轨迹", 20_000),
		},
	}
	raw, _ := json.Marshal(payload)
	msgs := []agentcore.Message{{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}}
	prompt := buildProsePrompt(msgs)
	for _, want := range []string{"保留本章五摊试点"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("pruned prose prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"无关世界法典", "无关检索轨迹", "OLD_DRAFT_CONTENT_MUST_NOT_LEAK",
		"FULL_CONTRACT_MUST_NOT_LEAK", "FULL_SIMULATION_MUST_NOT_LEAK", "REWRITE_BRIEF_MUST_NOT_LEAK",
		"CHAPTER_DRAFT_STATE_MUST_NOT_LEAK", "FULL_CHARACTER_LIST_MUST_NOT_LEAK",
		"已通过外审仍不应干扰正文", "林澈冷幽默",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("pruned prose prompt kept %q", forbidden)
		}
	}
	if got := len([]rune(prompt)); got > codexProsePromptRuneBudget+2000 {
		t.Fatalf("pruned prose prompt too large: %d", got)
	}
}

func TestBuildProsePromptKeepsOnlyBlockingExternalAdvice(t *testing.T) {
	payload := map[string]any{"draft_external_ai_review": map[string]any{
		"blocking":          true,
		"evidence":          []string{"角色轮流解释步骤"},
		"revision_plan":     []string{"例如让老丁忘带护套，再让邻摊抱怨"},
		"dialogue_fix_plan": []string{"照抄这一句示例台词"},
	}}
	raw, _ := json.Marshal(payload)
	prompt := buildProsePrompt([]agentcore.Message{{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}})
	if !strings.Contains(prompt, "角色轮流解释步骤") {
		t.Fatalf("blocking review evidence should reach prose renderer: %s", prompt)
	}
	for _, forbidden := range []string{"老丁忘带护套", "邻摊抱怨", "照抄这一句示例台词"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("external review prescription leaked into prose prompt: %q in %s", forbidden, prompt)
		}
	}
}

func TestProseCacheRoundTripIsBoundToPromptModelAndEffort(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", t.TempDir())
	contract := proseWordContract{Min: 4, Max: 20}
	prompt := "精确渲染包"
	if _, hit := loadCachedProse(prompt, "gpt-5.6-sol", "ultra", contract); hit {
		t.Fatal("empty cache unexpectedly hit")
	}
	if err := saveCachedProse(prompt, "gpt-5.6-sol", "ultra", "第一章\n\n正文内容"); err != nil {
		t.Fatal(err)
	}
	got, hit := loadCachedProse(prompt, "gpt-5.6-sol", "ultra", contract)
	if !hit || got != "第一章\n\n正文内容" {
		t.Fatalf("cache miss or mismatch: hit=%v prose=%q", hit, got)
	}
	if _, hit := loadCachedProse(prompt+"已修改", "gpt-5.6-sol", "ultra", contract); hit {
		t.Fatal("changed prompt reused stale prose")
	}
	if _, hit := loadCachedProse(prompt, "gpt-5.6-sol", "high", contract); hit {
		t.Fatal("changed effort reused stale prose")
	}
}

func TestCompactProseMessageKeepsSanitizedRewriteBrief(t *testing.T) {
	payload := map[string]any{
		"working_memory": map[string]any{
			"render_packet": map[string]any{"chapter": 2, "title": "想买辆皮卡"},
			"rewrite_brief": map[string]any{
				"human_acceptance_supplements": []string{"采购只留一个票据异常，禁止逐项报账。"},
				"render_policy":                "示例动作不是剧情指令。",
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: string(raw)}}}
	got := compactProseMessageText(msg, string(raw))
	for _, want := range []string{"rewrite_brief", "采购只留一个票据异常", "示例动作不是剧情指令"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prose context dropped %q: %s", want, got)
		}
	}
}

func TestBuildProsePromptDoesNotForceGenericSystemRefusal(t *testing.T) {
	prompt := buildProsePrompt([]agentcore.Message{{
		Role:    agentcore.RoleUser,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("渲染第二章，系统要像熟人一样接话。")},
	}})
	if strings.Contains(prompt, "只允许【不是，哥们，这笔不能这么花。】") || strings.Contains(prompt, "这笔不能这么花") {
		t.Fatalf("prose prompt still primes the rejected generic system line: %s", prompt)
	}
	for _, want := range []string{"接住主角刚才具体的误判或问题", "不带具体对象的客服话", "通常只写 2-4 个页面场景", "不得一项建一场", "普通连接句", "不要为了“像人”强加", "刚重逢或刚合作", "visible_to_pov=false", "申请加入的摊主", "真实交付、参与范围、统一条件", "今晚就这五家，明天再排"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prose prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{"对白要断续、有隐瞒、被追问才挤出下一条信息", "若没有拒绝、反转、笑点、代价或关系位移就一句带过", "每 2-3 句至少换一次承载方式"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prose prompt still forces short-drama mechanics %q", forbidden)
		}
	}
}

func TestBuildProsePromptCompactsOversizedContext(t *testing.T) {
	huge := "计划开头" + strings.Repeat("正文计划", 40_000) + "计划尾部"
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: huge}}},
	}
	p := buildProsePrompt(msgs)
	if got := len([]rune(p)); got > codexProsePromptRuneBudget+2000 {
		t.Fatalf("prose prompt should be compacted near budget, got %d", got)
	}
	for _, want := range []string{"计划开头", "计划尾部", "现在写正文", "你面对的是连载读者", "Codex 入参压缩", "不要 JSON", "运行环境诊断"} {
		if !strings.Contains(p, want) {
			t.Fatalf("compacted prose prompt 缺 %q", want)
		}
	}
}

func TestBoundedCodexExecContextAddsHardDeadlineAndRespectsEarlierParent(t *testing.T) {
	ctx, cancel := boundedCodexExecContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("codex exec context must have a hard deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > codexExecHardTimeout {
		t.Fatalf("unexpected hard timeout remaining: %s", remaining)
	}

	parent, parentCancel := context.WithTimeout(context.Background(), time.Minute)
	defer parentCancel()
	child, childCancel := boundedCodexExecContext(parent)
	defer childCancel()
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Equal(parentDeadline) {
		t.Fatalf("earlier parent deadline changed: parent=%v child=%v", parentDeadline, childDeadline)
	}
}

func TestBuildProsePromptPinsChapterWordContract(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"user_rules":{"structured":{"chapter_words":{"min":2100,"max":3000}}}}`)}},
	}
	prompt := buildProsePrompt(msgs)
	for _, want := range []string{"本章字数硬合同", "2100-3000", "工具按完整输出", "覆盖终稿前被拒绝"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prose prompt missing %q:\n%s", want, prompt)
		}
	}
	contract := inferProseWordContract(msgs)
	if contract.Min != 2100 || contract.Max != 3000 || !contract.accepts(2500) || contract.accepts(3900) {
		t.Fatalf("unexpected inferred contract: %+v", contract)
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
