package llmcodex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/voocel/agentcore"
)

func TestDetectCodexBinaryHonorsExplicitPipelineOverride(t *testing.T) {
	const want = "/opt/novel-studio/codex-cli"
	t.Setenv("NOVEL_STUDIO_CODEX_BINARY", "  "+want+"  ")
	if got := detectCodexBinary(); got != want {
		t.Fatalf("detectCodexBinary()=%q want explicit override %q", got, want)
	}
}

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

func TestGenerateAuthenticatedRenderUsesOneDirectProseCall(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	callLog := filepath.Join(dir, "calls.log")
	t.Setenv("CODEX_DIRECT_CALL_LOG", callLog)
	body := `#!/bin/sh
out=""
printf 'call\n' >> "$CODEX_DIRECT_CALL_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
printf '%s' '{"prose":"第二章 只走一次\n\n她推门进去。"}' > "$out"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "high")
	response, err := model.Generate(
		context.Background(),
		directRenderPrimingMessages(t, 2, 1, 100),
		directRenderToolSpecs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := directRenderProviderCalls(t, callLog); got != 1 {
		t.Fatalf("authenticated render provider calls=%d want=1", got)
	}
	calls := response.Message.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "draft_chapter" {
		t.Fatalf("direct render tool calls=%+v", calls)
	}
	var args struct {
		Chapter int    `json:"chapter"`
		Mode    string `json:"mode"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(calls[0].Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.Chapter != 2 || args.Mode != "write" || args.Content != "第二章 只走一次\n\n她推门进去。" {
		t.Fatalf("direct render synthesized args=%+v", args)
	}
}

func TestGenerateStreamAuthenticatedRenderUsesOneDirectProseCall(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	callLog := filepath.Join(dir, "calls.log")
	t.Setenv("CODEX_DIRECT_CALL_LOG", callLog)
	body := `#!/bin/sh
out=""
printf 'call\n' >> "$CODEX_DIRECT_CALL_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
printf '%s' '{"prose":"第二章 流式\n\n只调用一次。"}' > "$out"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "high")
	events, err := model.GenerateStream(context.Background(), directRenderPrimingMessages(t, 2, 1, 100), directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Type == agentcore.StreamEventDone {
			done++
			if calls := event.Message.ToolCalls(); len(calls) != 1 || calls[0].Name != "draft_chapter" {
				t.Fatalf("stream direct render calls=%+v", calls)
			}
		}
	}
	providerCalls := directRenderProviderCalls(t, callLog)
	if done != 1 || providerCalls != 1 {
		t.Fatalf("stream direct render done=%d provider calls=%d", done, providerCalls)
	}
}

func TestGenerateAuthenticatedRenderNeverRepairsEmptyOrOutOfRangeBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prose    string
		min, max int
	}{
		{name: "empty", prose: "", min: 1, max: 100},
		{name: "out-of-range", prose: "太短", min: 50, max: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "fake-codex")
			callLog := filepath.Join(dir, "calls.log")
			t.Setenv("CODEX_DIRECT_CALL_LOG", callLog)
			encoded, _ := json.Marshal(map[string]string{"prose": tc.prose})
			body := `#!/bin/sh
out=""
printf 'call\n' >> "$CODEX_DIRECT_CALL_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
printf '%s' "$CODEX_DIRECT_RESPONSE" > "$out"
`
			t.Setenv("CODEX_DIRECT_RESPONSE", string(encoded))
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			model := New(script, "gpt-5.6-sol", "high")
			if _, err := model.Generate(context.Background(), directRenderPrimingMessages(t, 2, tc.min, tc.max), directRenderToolSpecs()); err == nil {
				t.Fatal("invalid direct prose body did not fail closed")
			}
			if got := directRenderProviderCalls(t, callLog); got != 1 {
				t.Fatalf("invalid direct body triggered repair/retry: calls=%d", got)
			}
		})
	}
}

func TestAuthenticatedRenderToolAmbiguityFailsBeforeProvider(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	callLog := filepath.Join(dir, "calls.log")
	t.Setenv("CODEX_DIRECT_CALL_LOG", callLog)
	body := `#!/bin/sh
printf 'call\n' >> "$CODEX_DIRECT_CALL_LOG"
exit 99
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "high")
	for name, tools := range map[string][]agentcore.ToolSpec{
		"missing":       {{Name: "read_chapter", Parameters: map[string]any{"type": "object"}}},
		"duplicate":     append(directRenderToolSpecs(), directRenderToolSpecs()[0]),
		"bad-schema":    {{Name: "draft_chapter", Parameters: map[string]any{"type": "object"}}},
		"extra-read":    append(directRenderToolSpecs(), agentcore.ToolSpec{Name: "read_chapter", Parameters: map[string]any{"type": "object"}}),
		"novel-context": append(directRenderToolSpecs(), agentcore.ToolSpec{Name: "novel_context", Parameters: map[string]any{"type": "object"}}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := model.Generate(context.Background(), directRenderPrimingMessages(t, 2, 1, 100), tools); err == nil {
				t.Fatal("ambiguous authenticated render tools did not fail closed")
			}
		})
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("ambiguous authenticated render crossed provider boundary: %v", err)
	}
}

func TestUntrustedRenderEnvelopeDoesNotActivateDirectPath(t *testing.T) {
	messages := directRenderPrimingMessages(t, 2, 1, 100)
	messages[len(messages)-1].Metadata = nil
	authorization, err := authenticatedDirectRenderProse(messages, directRenderToolSpecs())
	if err != nil || authorization != nil {
		t.Fatalf("user-controlled envelope activated privileged path: authorization=%+v err=%v", authorization, err)
	}
}

func TestTrustedRenderEnvelopeDriftFailsBeforeProvider(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	callLog := filepath.Join(dir, "calls.log")
	t.Setenv("CODEX_DIRECT_CALL_LOG", callLog)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'call\\n' >> \"$CODEX_DIRECT_CALL_LOG\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "high")
	for _, tc := range []struct {
		name   string
		tamper func(*agentcore.Message)
	}{
		{"metadata-chapter", func(message *agentcore.Message) { message.Metadata["chapter"] = 3 }},
		{"metadata-payload-sha", func(message *agentcore.Message) {
			message.Metadata["payload_sha256"] = "sha256:" + strings.Repeat("f", 64)
		}},
		{"malformed-envelope", func(message *agentcore.Message) {
			message.Content = []agentcore.ContentBlock{agentcore.TextBlock(`{"_server_owned_frozen_render_context":`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := directRenderPrimingMessages(t, 2, 1, 100)
			tc.tamper(&messages[len(messages)-1])
			if _, err := model.Generate(context.Background(), messages, directRenderToolSpecs()); err == nil {
				t.Fatal("trusted render envelope drift did not fail closed")
			}
		})
	}
	if _, err := os.Stat(callLog); !os.IsNotExist(err) {
		t.Fatalf("trusted envelope drift crossed provider boundary: %v", err)
	}
}

func TestTypedDirectRenderWordContractFallsBackToFrozenUserRules(t *testing.T) {
	root := map[string]any{"user_rules": map[string]any{"structured": map[string]any{
		"chapter_words": map[string]any{"min": float64(2100), "max": float64(3000)},
	}}}
	contract, err := typedDirectRenderWordContract(root, map[string]any{})
	if err != nil || contract.Min != 2100 || contract.Max != 3000 {
		t.Fatalf("typed user-rules fallback=%+v err=%v", contract, err)
	}
}

func TestDirectRenderPromptUsesTypedPacketContractNotDecoyText(t *testing.T) {
	messages := directRenderPrimingMessages(t, 2, 1, 100)
	authorization, err := authenticatedDirectRenderProse(messages, directRenderToolSpecs())
	if err != nil || authorization == nil {
		t.Fatalf("direct authorization=%+v err=%v", authorization, err)
	}
	prompt := buildProsePromptWithContract(messages, &authorization.WordContract)
	if !strings.Contains(prompt, "硬边界仍是 1-100 字") {
		t.Fatalf("provider prompt lost typed packet range:\n%s", prompt)
	}
	if strings.Contains(prompt, "硬边界仍是 500-600 字") {
		t.Fatalf("decoy message range replaced typed packet contract:\n%s", prompt)
	}
}

func TestOrdinaryDraftGenerateKeepsPlaceholderThenProseCalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(dir, "prose-cache"))
	script := filepath.Join(dir, "fake-codex")
	countPath := filepath.Join(dir, "count")
	t.Setenv("CODEX_ORDINARY_COUNT", countPath)
	body := `#!/bin/sh
out=""
n=0
if [ -f "$CODEX_ORDINARY_COUNT" ]; then n=$(sed -n '1p' "$CODEX_ORDINARY_COUNT"); fi
n=$((n + 1))
printf '%s' "$n" > "$CODEX_ORDINARY_COUNT"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
if [ "$n" -eq 1 ]; then
  printf '%s' '{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"content\":\"占位\",\"mode\":\"write\"}","text":""}' > "$out"
else
  printf '%s' '{"prose":"第一章 普通路径\n\n仍按旧协议。"}' > "$out"
fi
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	model := New(script, "gpt-5.6-sol", "high")
	response, err := model.Generate(context.Background(), []agentcore.Message{{
		Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("调用 draft_chapter 写第一章")},
	}}, directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	rawCount, err := os.ReadFile(countPath)
	if err != nil || strings.TrimSpace(string(rawCount)) != "2" {
		t.Fatalf("ordinary path call count=%q err=%v, want 2", rawCount, err)
	}
	if calls := response.Message.ToolCalls(); len(calls) != 1 || !strings.Contains(string(calls[0].Args), "仍按旧协议") {
		t.Fatalf("ordinary prose replacement changed: %+v", calls)
	}
}

func directRenderPrimingMessages(t *testing.T, chapter, minWords, maxWords int) []agentcore.Message {
	t.Helper()
	payload := map[string]any{
		"_context_profile": "draft",
		"working_memory": map[string]any{"render_packet": map[string]any{
			"version": 11, "chapter": chapter, "title": "一次调用",
			"word_budget": map[string]any{"hard_min": minWords, "hard_max": maxWords},
		}},
		// A regex over the whole message would see this later key and replace the
		// sealed packet contract. The direct path must ignore it.
		"zzz_decoy": map[string]any{"word_budget": map[string]any{"hard_min": 500, "hard_max": 600}},
	}
	aigc.ApplyProseRenderCompatibilityContracts(payload)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope, identity, err := aigc.BuildProseRenderPrimingEnvelope(chapter, "sha256:"+strings.Repeat("a", 64), raw)
	if err != nil {
		t.Fatal(err)
	}
	return []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("直接 draft_chapter")}},
		{
			Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(envelope)},
			Metadata: map[string]any{
				"server_owned": true, "protocol_version": identity.ProtocolVersion,
				"chapter": identity.Chapter, "plan_digest": identity.PlanDigest,
				"payload_sha256": identity.PayloadSHA256,
			},
		},
	}
}

func directRenderToolSpecs() []agentcore.ToolSpec {
	return []agentcore.ToolSpec{
		{
			Name: "draft_chapter",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"chapter": map[string]any{"type": "integer"},
					"content": map[string]any{"type": "string"},
					"mode":    map[string]any{"type": "string", "enum": []any{"write", "append"}},
				},
				"required": []any{"chapter", "content", "mode"},
			},
		},
	}
}

func directRenderProviderCalls(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "call\n")
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
	renderPacket := map[string]any{
		"version": 11,
		"goal":    "保留本章关键选择",
		"fact_anchors": []map[string]any{{
			"fact":           "TRANSFORMED_FACT_ANCHOR_MUST_SURVIVE",
			"transformed_as": "让角色从现场价签读出差异",
			"source_ref":     "rag:fact-receipt:17",
			"authority":      "receipt_bound",
		}},
		"craft_methods": []map[string]any{{
			"receipt_id":          "CRAFT_RECEIPT_MUST_SURVIVE",
			"candidate_moves":     []string{"TRANSFORMED_CRAFT_MOVE_MUST_SURVIVE"},
			"transformation_rule": "改成当前人物的临场选择",
			"hard_avoid":          []string{"不得复原样例桥段"},
		}},
		"rag_recall": []string{"RAW_RAG_INSIDE_PACKET_MUST_NOT_LEAK"},
		"style_contract": map[string]any{
			"profile_id":             "county-light-comedy-system-single-romance",
			"dialogue_breath_policy": "PINNED_STYLE_FULL_BREATH_GROUP",
			"romance_policy":         "PINNED_STYLE_SINGLE_HEROINE_BOUNDARY",
		},
		"literary_render_contract": map[string]any{
			"focalizer":          "许栀",
			"perceptual_bias":    "先留意对方避开的称呼",
			"knowledge_boundary": "只写许栀能够感知和推断的信息",
		},
		"craft_cards": []map[string]any{{
			"card_id":     "dialogue-subtext",
			"move":        "让答非所问暴露隐瞒",
			"source_text": strings.Repeat("FULL_CRAFT_SOURCE_MUST_NOT_LEAK", 100),
		}},
		"source_refs": []string{
			"literary-rendering#dialogue-subtext",
			"rag:chunk:scene-17",
		},
	}
	payload := map[string]any{
		"content":           "OLD_DRAFT_CONTENT_MUST_NOT_LEAK",
		"chapter_contract":  map[string]any{"marker": "FULL_CONTRACT_MUST_NOT_LEAK"},
		"causal_simulation": map[string]any{"marker": "FULL_SIMULATION_MUST_NOT_LEAK"},
		"rewrite_brief":     map[string]any{"marker": "REWRITE_BRIEF_MUST_NOT_LEAK"},
		"chapter_draft":     map[string]any{"marker": "CHAPTER_DRAFT_STATE_MUST_NOT_LEAK"},
		"characters":        map[string]any{"marker": "FULL_CHARACTER_LIST_MUST_NOT_LEAK"},
		"rag_recall":        []string{"RAW_ROOT_RAG_RECALL_MUST_NOT_LEAK"},
		"render_packet":     renderPacket,
		"literary_render_contract": map[string]any{
			"marker": "FALLBACK_LITERARY_CONTRACT_MUST_NOT_WIN",
		},
		"style_contract": map[string]any{"marker": "FALLBACK_STYLE_CONTRACT_MUST_NOT_WIN"},
		"craft_cards":    []map[string]any{{"card_id": "FALLBACK_CRAFT_CARD_MUST_NOT_WIN"}},
		"source_refs":    []string{"fallback:source-ref-must-not-win"},
		"working_memory": map[string]any{
			"render_packet":            renderPacket,
			"draft_external_ai_review": map[string]any{"blocking": false, "evidence": []string{"已通过外审仍不应干扰正文"}},
			"world_codex":              strings.Repeat("无关世界法典", 20_000),
		},
		"reference_pack": map[string]any{
			"writing_engine":  map[string]any{"voice": "林澈冷幽默"},
			"retrieval_trace": strings.Repeat("无关检索轨迹", 20_000),
		},
		"selected_memory": map[string]any{
			"rag_recall": []string{"RAW_SELECTED_MEMORY_RAG_MUST_NOT_LEAK"},
		},
	}
	raw, _ := json.Marshal(payload)
	msgs := []agentcore.Message{{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}}
	prompt := buildProsePrompt(msgs)
	for _, want := range []string{
		"保留本章关键选择", "literary_render_contract", "许栀", "先留意对方避开的称呼",
		"dialogue-subtext", "让答非所问暴露隐瞒", "literary-rendering#dialogue-subtext", "rag:chunk:scene-17",
		"style_contract", "PINNED_STYLE_FULL_BREATH_GROUP", "PINNED_STYLE_SINGLE_HEROINE_BOUNDARY",
		"TRANSFORMED_FACT_ANCHOR_MUST_SURVIVE", "CRAFT_RECEIPT_MUST_SURVIVE", "TRANSFORMED_CRAFT_MOVE_MUST_SURVIVE",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("pruned prose prompt missing %q", want)
		}
	}
	if got := strings.Count(prompt, `"literary_render_contract":`); got != 1 {
		t.Fatalf("prose payload should carry one literary_render_contract, got %d in %s", got, prompt)
	}
	if got := strings.Count(prompt, `"style_contract":`); got != 1 {
		t.Fatalf("prose payload should carry one style_contract, got %d in %s", got, prompt)
	}
	for _, key := range []string{`"craft_cards":`, `"source_refs":`} {
		if got := strings.Count(prompt, key); got != 1 {
			t.Fatalf("prose payload should carry one %s key, got %d in %s", key, got, prompt)
		}
	}
	for _, forbidden := range []string{
		"无关世界法典", "无关检索轨迹", "OLD_DRAFT_CONTENT_MUST_NOT_LEAK",
		"FULL_CONTRACT_MUST_NOT_LEAK", "FULL_SIMULATION_MUST_NOT_LEAK", "REWRITE_BRIEF_MUST_NOT_LEAK",
		"CHAPTER_DRAFT_STATE_MUST_NOT_LEAK", "FULL_CHARACTER_LIST_MUST_NOT_LEAK",
		"已通过外审仍不应干扰正文", "林澈冷幽默", "FULL_CRAFT_SOURCE_MUST_NOT_LEAK",
		"FALLBACK_LITERARY_CONTRACT_MUST_NOT_WIN", "FALLBACK_CRAFT_CARD_MUST_NOT_WIN", "fallback:source-ref-must-not-win",
		"FALLBACK_STYLE_CONTRACT_MUST_NOT_WIN",
		"RAW_ROOT_RAG_RECALL_MUST_NOT_LEAK", "RAW_SELECTED_MEMORY_RAG_MUST_NOT_LEAK", "RAW_RAG_INSIDE_PACKET_MUST_NOT_LEAK",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("pruned prose prompt kept %q", forbidden)
		}
	}
	if got := len([]rune(prompt)); got > codexProsePromptRuneBudget+2000 {
		t.Fatalf("pruned prose prompt too large: %d", got)
	}
}

func TestBuildProsePromptRequiresSceneFirstCompleteChapter(t *testing.T) {
	prompt := buildProsePrompt([]agentcore.Message{{
		Role: agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(
			`{"render_packet":{"version":11,"chapter":2,"title":"门外的两声轻响"}}`,
		)},
	}})
	for _, want := range []string{
		"不要以章纲、事实边界、时间窗、结论或自检摘要起笔",
		"标题后必须直接进入带有人物动作或现场感知的具体场景",
		"连续写成完整章节正文",
		"只能确认什么、不能确认什么",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scene-first prose guard missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildProsePromptPassesAphoristicSummaryGateBeforeGeneration(t *testing.T) {
	msgs := []agentcore.Message{{
		Role: agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(
			`{"render_packet":{"version":11,"chapter":4,"title":"三层时间"},"user_rules":{"structured":{"chapter_words":{"min":2200,"max":2600}}}}`,
		)},
	}}

	for name, prompt := range map[string]string{
		"initial": buildProsePrompt(msgs),
		"repair":  buildProseRepairPrompt(msgs, "第四章 三层时间\n\n待修正文。", 2190, proseWordContract{Min: 2200, Max: 2600}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range []string{
				"写前硬门禁：aphoristic_narrative_summary",
				"无对白的旁白段不得把人物当下判断提炼成对称判词",
				"理由一条比一条……只有……",
				"任何一段……只能……不能……",
				"接单相近不等于……也不等于……",
				"命中任一例型会整章拒绝",
				"具体误判、动作、对话或后果",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s prose prompt missing pre-generation aphoristic gate %q:\n%s", name, want, prompt)
				}
			}
		})
	}
}

func TestProseContextWhitelistExcludesRawRAGRecall(t *testing.T) {
	if slices.Contains(proseContextKeys, "rag_recall") {
		t.Fatal("raw rag_recall must remain planning-only")
	}
	raw := `{"render_packet":{"version":11,"fact_anchors":[{"fact":"SAFE_FACT"}],"craft_methods":[{"receipt_id":"SAFE_RECEIPT"}]},"rag_recall":["RAW_ROOT"],"selected_memory":{"rag_recall":["RAW_NESTED"]}}`
	selected, ok := selectProseToolContext(raw)
	if !ok {
		t.Fatal("valid context JSON was not recognized")
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{"SAFE_FACT", "SAFE_RECEIPT"} {
		if !strings.Contains(text, want) {
			t.Fatalf("v11 converted RAG field %q was dropped: %s", want, text)
		}
	}
	for _, forbidden := range []string{"RAW_ROOT", "RAW_NESTED", `"rag_recall"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("raw RAG leaked through prose whitelist: %q in %s", forbidden, text)
		}
	}
}

func TestCompactProseMessagePinsLiteraryBridgeWhenAllowedContextIsOversized(t *testing.T) {
	renderPacket := map[string]any{
		"chapter": 6,
		"title":   "桥下的回声",
		"literary_render_contract": map[string]any{
			"focalizer":          "沈岚",
			"knowledge_boundary": "只写沈岚在桥下能感知和推断的信息",
		},
		"craft_cards": []map[string]any{{
			"card_id": "psychic-distance",
			"move":    "决定发生时拉近人物经验",
		}},
		"source_refs": []string{"literary-rendering#psychic-distance", "rag:bridge:42"},
	}
	payload := map[string]any{
		"render_packet":      renderPacket,
		"previous_tail":      strings.Repeat("巨大前章尾声", 10_000),
		"rag_recall":         []string{strings.Repeat("巨大召回材料", 10_000)},
		"relationship_state": strings.Repeat("巨大关系台账", 10_000),
		"user_rules": map[string]any{
			"preferences": strings.Repeat("巨大用户规则", 10_000),
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}
	got := compactProseMessageText(msg, string(raw))
	if size := len([]rune(got)); size > codexProsePerMessageRuneBudget {
		t.Fatalf("priority prose context exceeds 24K cap: %d", size)
	}
	for _, want := range []string{
		"prose_priority_context", `"render_packet":`, `"literary_render_contract":`,
		`"craft_cards":`, `"source_refs":`, "沈岚", "决定发生时拉近人物经验",
		"literary-rendering#psychic-distance", "rag:bridge:42",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("oversized prose context dropped priority bridge %q: %s", want, got)
		}
	}
	if !strings.Contains(got, "Codex 入参压缩") {
		t.Fatalf("oversized supplemental context was not visibly compacted: %s", got)
	}
}

func TestBuildProsePromptPinsLiteraryBridgeAcrossTotalPromptBudget(t *testing.T) {
	renderPacket := map[string]any{
		"chapter": 7,
		"title":   "潮声没说完",
		"literary_render_contract": map[string]any{
			"focalizer":          "沈岚",
			"knowledge_boundary": "PINNED_CONTRACT_ONLY_WRITES_SHENLAN_KNOWLEDGE",
		},
		"craft_cards": []map[string]any{{
			"card_id": "free-indirect-discourse",
			"move":    "PINNED_CRAFT_DYES_NARRATION_WITH_SHENLAN_VOICE",
		}},
		"source_refs": []string{"literary-rendering#free-indirect-discourse", "rag:tide:07"},
	}
	payload := map[string]any{
		"render_packet":      renderPacket,
		"previous_tail":      "TOOL_HEAD_" + strings.Repeat("前章巨大补充", 12_000) + "_TOOL_TAIL",
		"rag_recall":         []string{strings.Repeat("召回巨大补充", 12_000)},
		"relationship_state": strings.Repeat("关系巨大补充", 12_000),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	largeUser := "USER_HEAD_" + strings.Repeat("用户前置要求", 12_000) + "_USER_TAIL"
	prompt := buildProsePrompt([]agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(largeUser)}},
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}},
	})
	if size := len([]rune(prompt)); size > codexProsePromptRuneBudget {
		t.Fatalf("total prose prompt exceeds hard budget: %d", size)
	}
	for _, want := range []string{
		"prose_priority_context", `"render_packet":`, `"literary_render_contract":`,
		`"craft_cards":`, `"source_refs":`, "潮声没说完",
		"PINNED_CONTRACT_ONLY_WRITES_SHENLAN_KNOWLEDGE",
		"PINNED_CRAFT_DYES_NARRATION_WITH_SHENLAN_VOICE",
		"literary-rendering#free-indirect-discourse", "rag:tide:07",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("total prompt compaction dropped pinned literary bridge %q", want)
		}
	}
	if got := strings.Count(prompt, `"literary_render_contract":`); got != 1 {
		t.Fatalf("pinned literary contract should appear once, got %d", got)
	}
	if pinnedAt, suffixAt := strings.Index(prompt, "prose_priority_context"), strings.Index(prompt, "## 现在写正文"); pinnedAt < 0 || suffixAt < 0 || pinnedAt > suffixAt {
		t.Fatalf("pinned literary context must sit outside the compactible dialog middle")
	}
}

func TestBuildProsePromptReplacesStalePrioritySnapshotWithLatestToolPacket(t *testing.T) {
	oldPayload := map[string]any{
		"render_packet": map[string]any{
			"chapter":                   8,
			"title":                     "旧标题",
			"excluded_named_characters": []string{"OLD_EXCLUDED_CHARACTER_MUST_DISAPPEAR"},
			"literary_render_contract": map[string]any{
				"focalizer": "OLD_FOCALIZER_MUST_DISAPPEAR",
				"active_lenses": []map[string]any{{
					"kind": "OLD_ACTIVE_LENS_MUST_DISAPPEAR",
				}},
			},
			"craft_cards": []map[string]any{{"card_id": "OLD_CRAFT_CARD_MUST_DISAPPEAR"}},
			"source_refs": []string{"old:source-ref-must-disappear"},
		},
	}
	newPayload := map[string]any{
		"working_memory": map[string]any{
			"render_packet": map[string]any{
				"chapter": 8,
				"title":   "新标题",
				"literary_render_contract": map[string]any{
					"focalizer":          "NEW_FOCALIZER_MUST_SURVIVE",
					"knowledge_boundary": "NEW_KNOWLEDGE_BOUNDARY_MUST_SURVIVE",
				},
				"source_refs": []string{"new:source-ref-must-survive"},
			},
		},
	}
	oldRaw, _ := json.Marshal(oldPayload)
	newRaw, _ := json.Marshal(newPayload)
	prompt := buildProsePrompt([]agentcore.Message{
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(oldRaw))}},
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(newRaw))}},
	})
	for _, want := range []string{
		"新标题", "NEW_FOCALIZER_MUST_SURVIVE", "NEW_KNOWLEDGE_BOUNDARY_MUST_SURVIVE", "new:source-ref-must-survive",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("latest priority snapshot missing %q", want)
		}
	}
	for _, stale := range []string{
		"旧标题", "OLD_EXCLUDED_CHARACTER_MUST_DISAPPEAR", "OLD_FOCALIZER_MUST_DISAPPEAR",
		"OLD_ACTIVE_LENS_MUST_DISAPPEAR", "OLD_CRAFT_CARD_MUST_DISAPPEAR", "old:source-ref-must-disappear",
	} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("stale priority snapshot leaked %q", stale)
		}
	}
}

func TestBuildProsePromptExcludesFullLiteraryReferencePack(t *testing.T) {
	fullReference := "FULL_LITERARY_RENDERING_RESEARCH_MUST_NOT_REACH_PROSE"
	catalogMarker := "FULL_LITERARY_CARD_CATALOG_MUST_NOT_REACH_PROSE"
	references := map[string]any{
		"literary_rendering": strings.Repeat(fullReference, 500),
	}
	catalog := map[string]any{
		"version": 1,
		"cards": []map[string]any{{
			"id":       "focalization-boundary",
			"decision": catalogMarker,
		}},
	}
	payload := map[string]any{
		"references":               references,
		"literary_rendering_cards": catalog,
		"render_packet": map[string]any{
			"chapter": 1,
			"title":   "雨停以前",
			"literary_render_contract": map[string]any{
				"focalizer":   "陆真",
				"source_refs": []string{"literary-rendering#focalization-boundary"},
			},
		},
		"reference_pack": map[string]any{
			"references":               references,
			"literary_rendering_cards": catalog,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildProsePrompt([]agentcore.Message{{
		Role:    agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))},
	}})
	for _, want := range []string{"雨停以前", "陆真", "literary-rendering#focalization-boundary"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prose prompt dropped selected literary contract %q", want)
		}
	}
	for _, forbidden := range []string{
		fullReference, catalogMarker, `"literary_rendering":`, `"literary_rendering_cards":`,
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("full literary reference pack leaked into prose prompt: %q", forbidden)
		}
	}
}

func TestBuildProsePromptDropsLiveExternalAdviceAfterPlanFreeze(t *testing.T) {
	payload := map[string]any{"draft_external_ai_review": map[string]any{
		"blocking":          true,
		"evidence":          []string{"角色轮流解释步骤"},
		"revision_plan":     []string{"例如让老丁忘带护套，再让邻摊抱怨"},
		"dialogue_fix_plan": []string{"照抄这一句示例台词"},
	}}
	raw, _ := json.Marshal(payload)
	prompt := buildProsePrompt([]agentcore.Message{{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}})
	for _, forbidden := range []string{"角色轮流解释步骤", "老丁忘带护套", "邻摊抱怨", "照抄这一句示例台词"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("live external review leaked past the frozen render packet: %q in %s", forbidden, prompt)
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

func TestCompactProseMessageDropsLiveRewriteBrief(t *testing.T) {
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
	for _, forbidden := range []string{"rewrite_brief", "采购只留一个票据异常", "示例动作不是剧情指令"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("live rewrite brief leaked past the frozen render packet %q: %s", forbidden, got)
		}
	}
}

func TestCompactProseMessageKeepsSealedRerenderFeedback(t *testing.T) {
	payload := map[string]any{
		"render_packet": map[string]any{"chapter": 3, "title": "七个地址都只试门口"},
		"sealed_rerender_feedback": map[string]any{
			"plan_digest": "sha256:frozen-plan",
			"body_sha256": "2958deadbeef",
			"summary":     "删掉七址清单感，补一处主角真实误判与代价。",
			"issues":      []any{map[string]any{"type": "aesthetic", "problem": "catalog stuffing"}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}
	got := compactProseMessageText(msg, string(raw))
	for _, want := range []string{"sealed_rerender_feedback", "sha256:frozen-plan", "删掉七址清单感"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sealed semantic feedback was dropped from prose context %q: %s", want, got)
		}
	}
}

func TestCompactProseMessageKeepsLiteraryBridgeFallback(t *testing.T) {
	payload := map[string]any{
		"render_packet": map[string]any{"chapter": 4, "title": "门后的雨"},
		"literary_render_contract": map[string]any{
			"focalizer":        "周遥",
			"narrative_access": "internal",
		},
		"craft_cards": []map[string]any{{
			"card_id": "psychic-distance",
			"move":    "在决定发生时拉近",
		}},
		"source_refs": []string{"literary-rendering#psychic-distance"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := agentcore.Message{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}
	got := compactProseMessageText(msg, string(raw))
	for _, want := range []string{
		"literary_render_contract", "周遥", "psychic-distance", "在决定发生时拉近",
		"literary-rendering#psychic-distance",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prose context dropped literary bridge %q: %s", want, got)
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
	for _, want := range []string{
		"literary_render_contract 是本章唯一的文学渲染合同", "source_refs 只用于追溯",
		"不是必须逐项拍出来的镜头", "不要把结果写成验收录像", "普通连接句",
		"不要为了“像人”强加", "非人物媒介、界面或传话声音", "visible_to_pov=false",
		"可发言的功能角色", "计划、审核和流程术语", "没有术语也可能像报告",
		"render_packet v11", "fact_anchors", "craft_methods", "raw rag_recall",
		"不得调用 craft_recall", "退回 plan",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prose prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"对白要断续、有隐瞒、被追问才挤出下一条信息", "若没有拒绝、反转、笑点、代价或关系位移就一句带过",
		"每 2-3 句至少换一次承载方式", "成果兑现只跟一组普通顾客", "今晚就这五家，明天再排",
		"接住主角刚才具体的误判或问题", "不带具体对象的客服话", "刚重逢或刚合作",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prose prompt still forces short-drama mechanics %q", forbidden)
		}
	}
}

func TestBuildProsePromptKeepsOneLeanHumanFacingBrief(t *testing.T) {
	prompt := buildProsePrompt(nil)
	if got := len([]rune(prompt)); got > 2600 {
		t.Fatalf("static prose brief regrew into a second planning dossier: %d runes", got)
	}
	for _, forbidden := range []string{
		"P1", "P2", "P3", "相邻三段轮换", "固定句长", "固定周期",
		"每 2-3 句", "逐段功能", "句长 CV", "TTR",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("static prose brief contains algorithmic writing recipe %q: %s", forbidden, prompt)
		}
	}
	for _, want := range []string{
		"先写人，再写事", "不是必须逐项拍出来的镜头", "不要把结果写成验收录像",
		"允许感受多停一会儿", "不要为了“像人”强加",
		"render_packet v11", "fact_anchors", "craft_methods", "退回 plan",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("lean prose brief lost human-facing boundary %q", want)
		}
	}
}

func TestBuildProsePromptHasNoProjectGenreHardcoding(t *testing.T) {
	prompt := buildProsePrompt([]agentcore.Message{{
		Role:    agentcore.RoleUser,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("渲染当前章节，沿用项目已有设定。")},
	}})
	for _, forbidden := range []string{"青山县", "县城", "经营", "摊主", "顾客", "系统", "男女主", "林澈"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("genre-neutral prose prompt contains project hardcoding %q", forbidden)
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

func TestConfiguredCodexExecHardTimeoutSupportsBoundedOverride(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_CODEX_EXEC_HARD_TIMEOUT", "25m")
	if got := configuredCodexExecHardTimeout(); got != 25*time.Minute {
		t.Fatalf("override = %s, want 25m", got)
	}
	for _, invalid := range []string{"bad", "30s", "2h"} {
		t.Setenv("NOVEL_STUDIO_CODEX_EXEC_HARD_TIMEOUT", invalid)
		if got := configuredCodexExecHardTimeout(); got != defaultCodexExecHardTimeout {
			t.Fatalf("invalid override %q = %s, want default %s", invalid, got, defaultCodexExecHardTimeout)
		}
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

func TestBuildProsePromptPrefersEffectiveRenderWordBudget(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"render_packet":{"version":11,"chapter":5,"word_budget":{
				"unit":"unicode_characters_including_title",
				"hard_min":2444,"hard_max":2600,
				"submission_target_min":2522,"submission_target_max":2561,
				"exact_boundary":true
			}},
			"user_rules":{"structured":{"chapter_words":{"min":2200,"max":2600}}}
		}`)}},
	}

	contract := inferProseWordContract(msgs)
	if contract.Min != 2444 || contract.Max != 2600 ||
		contract.TargetMin != 2522 || contract.TargetMax != 2561 {
		t.Fatalf("effective render contract lost to project fallback: %+v", contract)
	}
	if contract.accepts(2351) || !contract.accepts(2444) || !contract.accepts(2600) {
		t.Fatalf("effective render contract accepted an under-length C5 body: %+v", contract)
	}
	if min, max := contract.targetRange(); min != 2522 || max != 2561 {
		t.Fatalf("effective submission target = %d-%d, want 2522-2561", min, max)
	}
	prompt := buildProsePrompt(msgs)
	for _, want := range []string{
		"硬边界仍是 2444-2600", "安全写作目标", "2522-2561", "达到 2522 字前不得提前收束",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("effective render prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildProseRepairPromptCarriesRejectedBodyAndExactDelta(t *testing.T) {
	msgs := []agentcore.Message{{
		Role:    agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"render_packet":{"version":11,"chapter":5,"word_budget":{"hard_min":2444,"hard_max":2600,"submission_target_min":2522,"submission_target_max":2561}}}`)},
	}}
	previous := "第五章 两下，停一停，再两下\n\n这是一版需要定向补足的完整正文。"
	prompt := buildProseRepairPrompt(msgs, previous, 2351, inferProseWordContract(msgs))
	for _, want := range []string{previous, "净增约 190 字", "2444-2600", "2522-2561", "只输出修复后的完整正文"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildProsePromptInjectsAntiAIRulesBeforeRenderingOldV11Packet(t *testing.T) {
	messages := []agentcore.Message{{
		Role: agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"render_packet":{
				"version":11,
				"chapter":9,
				"title":"画面黑了，许知遥还在行动",
				"mandatory_beats":["按冻结事实完成本章"]
			}
		}`)},
	}}
	prompt := buildProsePrompt(messages)
	for _, want := range []string{
		`"anti_ai_render_contract"`,
		`"event_timing_safeguards"`,
		"证据、规则或流程按计划顺序逐项播报成台账",
		"刺激先改变POV的注意、判断或误判",
		"句段随观察、犹疑、冲突、决断和余波自然换挡",
		"首稿前执行；章级优先。",
		"首次落笔前必须执行",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("old v11 prose prompt did not receive render-time anti-AI rule %q:\n%s", want, prompt)
		}
	}
}

func TestBuildProsePromptPreservesChapterSpecificAntiAIRenderContract(t *testing.T) {
	messages := []agentcore.Message{{
		Role: agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"render_packet":{
				"version":11,
				"chapter":10,
				"anti_ai_render_contract":{
					"risk_signals":["本章专属：分钟窗被逐格复述"],
					"counter_moves":["本章专属：让等待改变程野的判断"],
					"sentence_rhythm_policy":"本章专属节奏",
					"object_response_budget":"本章专属物件预算",
					"dialogue_function_plan":"本章专属对白功能",
					"review_checks":["本章专属复核"],
					"usage_policy":"首稿前执行；章级优先。"
				}
			}
		}`)},
	}}
	prompt := buildProsePrompt(messages)
	for _, want := range []string{
		"本章专属：分钟窗被逐格复述",
		"本章专属：让等待改变程野的判断",
		"本章专属节奏",
		"本章专属物件预算",
		"本章专属对白功能",
		"本章专属复核",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("chapter-specific anti-AI contract was replaced or dropped: missing %q\n%s", want, prompt)
		}
	}
}

func TestBuildProsePromptPinsServerPrimedChapterContractFromUserEnvelope(t *testing.T) {
	payload := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{
			"version":11,
			"chapter":10,
			"title":"首 token 前可见",
			"anti_ai_render_contract":{
				"risk_signals":["本章专属：首个 token 前风险"],
				"counter_moves":["本章专属：先改判断再行动"],
				"sentence_rhythm_policy":"本章专属首 token 节奏",
				"object_response_budget":"本章专属首 token 物件预算",
				"dialogue_function_plan":"本章专属首 token 对白计划",
				"review_checks":["本章专属首 token 复核"],
				"usage_policy":"首稿前执行；章级优先。"
			}
		}}
	}`)
	envelope, _, err := aigc.BuildProseRenderPrimingEnvelope(10, "sha256:plan", payload)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildProsePrompt([]agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("直接调用 draft_chapter")}},
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(envelope)}},
	})
	for _, want := range []string{
		"正文必保留结构化合同",
		"本章专属：首个 token 前风险",
		"本章专属：先改判断再行动",
		"本章专属首 token 节奏",
		"本章专属首 token 物件预算",
		"本章专属首 token 对白计划",
		"本章专属首 token 复核",
		"直接调用 draft_chapter",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("server-primed Codex prose prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildProsePromptDoesNotInventStorySpecificSurfaceProjection(t *testing.T) {
	withContract := []agentcore.Message{{
		Role: agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"render_packet":{
				"version":11,
				"chapter":4,
				"title":"批量履约",
				"heading":"第4章 批量履约",
				"visible_characters":["甲","乙"],
				"continuity_checks":[
					"三项任务的精确状态必须保持",
					"章末两项完成、一项仍在途"
				],
				"mandatory_beats":["甲决定启动三项任务"],
				"render_capacity":{"scene_spine":[{"beats":["启动","误判","收束"]}]},
				"literary_render_contract":{"focalizer":"甲","narrative_access":"limited","knowledge_boundary":"只写甲可见事实"}
			},
			"sealed_rerender_feedback":{"surface_allocation_contract":{
				"scope":"expression_only",
				"detailed_node_limit":3,
				"policy":"页面只详写3处，其余台账离屏"
			},"rewrite_brief":"压缩重复流程，但不得更改冻结事实。"}
		}`)},
	}}
	prompt := buildProsePrompt(withContract)
	for _, want := range []string{
		"冻结事实的页面分配优先级",
		"只覆盖正文的表面篇幅分配",
		"不覆盖或改写 render_packet、frozen plan 的事实",
		"批量事实仍由冻结 plan 锁定",
		"不得为证明完整性把台账逐行逐值转写",
		"凡正文显写的事实仍必须与冻结合同一致",
		"三项任务的精确状态必须保持",
		"章末两项完成、一项仍在途",
		"甲决定启动三项任务",
		"只写甲可见事实",
		"页面只详写3处，其余台账离屏",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("surface-allocation prose priority missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"程野", "许知遥", "姜岚", "南栈", "桥湾", "七单", "公共呼叫板",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("unrelated story-specific value was injected %q:\n%s", forbidden, prompt)
		}
	}

	withoutContract := []agentcore.Message{{
		Role:    agentcore.RoleTool,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"render_packet":{"version":11,"chapter":4},"sealed_rerender_feedback":{"summary":"普通表达返工"}}`)},
	}}
	plainPrompt := buildProsePrompt(withoutContract)
	if strings.Contains(plainPrompt, "冻结事实的页面分配优先级") {
		t.Fatalf("generic prose render unexpectedly inherited story-specific priority:\n%s", plainPrompt)
	}
}

func TestBuildProsePromptUsesBufferedTargetForInitialAndWholeRerender(t *testing.T) {
	initial := []agentcore.Message{
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"user_rules":{"structured":{"chapter_words":{"min":2200,"max":2600}}},"render_packet":{"chapter":3,"title":"门外来单"}}`)}},
	}
	rerender := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("整章重渲染第 3 章，调用 draft_chapter(mode=write)")}},
		{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"user_rules":{"structured":{"chapter_words":{"min":2200,"max":2600}}},"render_packet":{"chapter":3,"title":"门外来单"},"sealed_rerender_feedback":{"body_sha256":"sha256:rejected","summary":"保留冻结计划做整章表达返工"}}`)}},
	}

	for name, messages := range map[string][]agentcore.Message{"initial": initial, "whole-rerender": rerender} {
		t.Run(name, func(t *testing.T) {
			prompt := buildProsePrompt(messages)
			for _, want := range []string{
				"本章字数硬合同", "硬边界仍是 2200-2600", "安全写作目标", "2300-2450", "靶心约 2375", "达到 2300 字前不得提前收束",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("%s prose prompt missing %q:\n%s", name, want, prompt)
				}
			}
		})
	}

	contract := proseWordContract{Min: 2200, Max: 2600}
	if min, max := contract.targetRange(); min != 2300 || max != 2450 {
		t.Fatalf("buffered target = %d-%d, want 2300-2450", min, max)
	}
	if !contract.accepts(2200) || !contract.accepts(2600) || contract.accepts(2199) || contract.accepts(2601) {
		t.Fatalf("buffer changed the hard 2200-2600 contract")
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

func TestCappedCodexReasoningDoesNotChangeRequestedIdentity(t *testing.T) {
	t.Setenv(codexReasoningCapEnv, "high")
	if got := cappedCodexReasoning("ultra"); got != "high" {
		t.Fatalf("capped reasoning = %q, want high", got)
	}
	if got := cappedCodexReasoning("medium"); got != "medium" {
		t.Fatalf("lower requested reasoning must remain unchanged, got %q", got)
	}
	t.Setenv(codexReasoningCapEnv, "invalid")
	if got := cappedCodexReasoning("ultra"); got != "ultra" {
		t.Fatalf("invalid cap must preserve requested reasoning, got %q", got)
	}
}
