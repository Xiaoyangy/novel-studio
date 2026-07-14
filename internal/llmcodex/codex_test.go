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
	renderPacket := map[string]any{
		"goal": "保留本章关键选择",
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
	}
	raw, _ := json.Marshal(payload)
	msgs := []agentcore.Message{{Role: agentcore.RoleTool, Content: []agentcore.ContentBlock{agentcore.TextBlock(string(raw))}}}
	prompt := buildProsePrompt(msgs)
	for _, want := range []string{
		"保留本章关键选择", "literary_render_contract", "许栀", "先留意对方避开的称呼",
		"dialogue-subtext", "让答非所问暴露隐瞒", "literary-rendering#dialogue-subtext", "rag:chunk:scene-17",
		"style_contract", "PINNED_STYLE_FULL_BREATH_GROUP", "PINNED_STYLE_SINGLE_HEROINE_BOUNDARY",
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
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("pruned prose prompt kept %q", forbidden)
		}
	}
	if got := len([]rune(prompt)); got > codexProsePromptRuneBudget+2000 {
		t.Fatalf("pruned prose prompt too large: %d", got)
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
