package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

const testGenreStyleCatalog = `{
  "version": 1,
  "profiles": [{
    "id": "county-light-comedy-system-single-romance",
    "name": "轻松县城经营·系统·单女主",
    "match": {
      "require_any": ["县城", "种田", "经营"],
      "score_any": ["轻松", "搞笑", "系统", "单女主"],
      "minimum_score": 2
    },
    "tone": "明亮、松弛、有人情账。",
    "dialogue_breath_policy": "普通人口述按完整气口说完，禁止连续2—4汉字句号短句。",
    "scene_flow_policy": "快节奏来自选择与后果，不来自碎句。",
    "humor_policy": "笑点由人物错位和实际后果生成。",
    "growth_policy": "用物件前后变化和普通人的新选择兑现成长。",
    "romance_policy": "唯一恋爱线服从项目指定CP，其他异性不使用暧昧镜头。",
    "system_policy": "系统不抢人物余波，一次只回应一件事。",
    "source_refs": ["genre-style-craft#spoken-breath-group"],
    "cards": [{
      "id": "spoken-breath-group",
      "move": "把平静口述写成一个完整气口。",
      "avoid": "两个字。两个字。再解释。"
    }]
  }]
}`

func TestSelectGenreStyleProfileRequiresGenreAndEnoughSignals(t *testing.T) {
	selected, err := selectGenreStyleProfile(testGenreStyleCatalog, "default", &rules.Snapshot{
		Structured:  rules.Structured{Genre: "返乡县城经营系统文"},
		Preferences: "轻松搞笑，单女主。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.ID != "county-light-comedy-system-single-romance" {
		t.Fatalf("county profile was not selected: %#v", selected)
	}

	unrelated, err := selectGenreStyleProfile(testGenreStyleCatalog, "default", &rules.Snapshot{
		Structured:  rules.Structured{Genre: "悬疑刑侦"},
		Preferences: "冷峻克制，多线调查。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unrelated != nil {
		t.Fatalf("genre assumptions leaked into unrelated project: %#v", unrelated)
	}
}

func TestDraftProfileProjectsCompactStyleContractAndDropsRawAssets(t *testing.T) {
	var catalog genreStyleCatalog
	if err := json.Unmarshal([]byte(testGenreStyleCatalog), &catalog); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "回来第一天"}
	engine := domain.WritingCompiled{
		EnabledFeatures: []domain.WritingFeature{{ID: "qingshan:dialogue:oral-breath-group"}},
		ActiveRules:     []string{"普通人把对象、原因和请求放进一个完整气口。"},
		AntiAIRules:     []string{"禁止同一意思连续切成两至四字句号短句。"},
		Taboos:          []string{"其他异性不得承接恋爱镜头语法。"},
	}
	working := map[string]any{
		"chapter_plan":        &plan,
		"genre_style_profile": catalog.Profiles[0],
		"writing_engine":      &engine,
		"ai_voice_redflags": map[string]any{
			"red_flags": []map[string]any{{"rule": "chapter_function_repetition", "severity": "info"}},
		},
		"draft_external_ai_review": map[string]any{
			"blocking": false, "summary": "低风险", "revision_plan": []string{"照例句改"},
		},
	}
	result := map[string]any{
		"chapter_plan":        &plan,
		"working_memory":      working,
		"genre_style_profile": catalog.Profiles[0],
		"writing_engine":      &engine,
		"ai_voice_redflags":   working["ai_voice_redflags"],
	}

	applyChapterContextProfile(result, "draft")

	packet, ok := working["render_packet"].(draftRenderPacket)
	if !ok || packet.Version != 11 || packet.StyleContract == nil {
		t.Fatalf("style contract was not projected into v11 packet: %#v", working["render_packet"])
	}
	if _, mirrored := result["render_packet"]; mirrored {
		t.Fatal("draft profile duplicated render_packet outside canonical working_memory")
	}
	raw, err := json.Marshal(packet.StyleContract)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, want := range []string{"完整气口", "唯一恋爱线", "系统不抢人物余波", "qingshan:dialogue:oral-breath-group"} {
		if !strings.Contains(serialized, want) {
			t.Fatalf("style contract missing %q: %s", want, serialized)
		}
	}
	for _, key := range []string{"genre_style_profile", "writing_engine", "ai_voice_redflags", "draft_external_ai_review"} {
		if _, exists := result[key]; exists {
			t.Fatalf("raw draft asset %q leaked at top level", key)
		}
		if _, exists := working[key]; exists {
			t.Fatalf("raw draft asset %q leaked in working memory", key)
		}
	}
	if strings.Contains(serialized, "revision_plan") || strings.Contains(serialized, "chapter_function_repetition") {
		t.Fatalf("advisory or external rewrite choreography leaked into style contract: %s", serialized)
	}
}

func TestDraftStyleContractSoftensPersistedSceneAnchorQuota(t *testing.T) {
	result := map[string]any{"writing_engine": &domain.WritingCompiled{
		ActiveRules: []string{
			"每章至少让 2 个现场物件或痕迹承担新信息、关系位移、规则代价或章末钩子。",
			"规划时优先把这些物件写入 scene_anchors，正文中不能只重复名字，至少一次改变读者知道的信息或角色选择。",
			"让人物选择产生现场后果。",
		},
		AntiAIRules: []string{"禁止对白传送带。"},
		Taboos:      []string{"不得泄露人物知识边界。"},
	}}
	contract := newDraftStyleContract(result)
	if contract == nil {
		t.Fatal("style contract missing")
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "每章至少让 2 个") || strings.Contains(text, "至少一次改变") {
		t.Fatalf("persisted soft quota leaked into current render packet: %s", text)
	}
	for _, want := range []string{"soft_craft_rules", "择取0—2个", "anti_ai_rules", "taboos"} {
		if !strings.Contains(text, want) {
			t.Fatalf("style hard/soft role %q missing: %s", want, text)
		}
	}
	if strings.Contains(text, `"active_rules"`) || strings.Contains(text, `"cards"`) {
		t.Fatalf("ambiguous style fields survived: %s", text)
	}
}

func TestDraftStyleContractCarriesConfiguredStyleAndBoundedSerialMemory(t *testing.T) {
	configured := configuredStyleProfileFromMarkdown("romance", `## 言情风格

- **内心描写**：不靠旁白宣布情绪，让感受附着于正在发生的细节。
- **对话节奏**：冲突时双方的句子都保留自己的声音。`)
	if configured == nil || configured.ID != "romance" || configured.Name != "言情风格" {
		t.Fatalf("configured style parse failed: %#v", configured)
	}
	stats := &stylestat.Stats{
		Chapters: 8,
		Patterns: []stylestat.PatternStat{{
			Name: "矫正句『不是…(而)是…』", Total: 16, PerChapter: 2,
		}},
		TopPhrases: []stylestat.PhraseStat{
			{Text: "他没有回答", Count: 12},
			{Text: "屏幕亮了", Count: 10},
			{Text: "第三条应被截断", Count: 9},
			{Text: "第四条保留", Count: 8},
			{Text: "第五条不能进入", Count: 8},
		},
		RepeatedSentences: []stylestat.SentenceStat{{
			Text: "门外的脚步声又一次停在同一个位置", Chapters: 4, Count: 5,
		}},
		Ending:          stylestat.EndingStat{ShortRatio: 0.88, MedianRunes: 12},
		OpeningTimeRate: 0.75,
	}
	contract := newDraftStyleContract(map[string]any{
		"configured_style": configured,
		"style_stats":      stats,
	})
	if contract == nil || contract.Version != 3 || contract.ConfiguredStyleID != "romance" {
		t.Fatalf("configured style was not compiled: %#v", contract)
	}
	if len(contract.ConfiguredStyleRules) != 2 || contract.SerialMemory == nil {
		t.Fatalf("configured/serial contract incomplete: %#v", contract)
	}
	if got := len(contract.SerialMemory.AvoidPhrases); got != 4 {
		t.Fatalf("serial phrase memory len=%d, want bounded 4", got)
	}
	if len(contract.SerialMemory.AvoidExactLines) != 1 || len(contract.SerialMemory.PatternAlerts) != 1 ||
		contract.SerialMemory.OpeningGuard == "" || contract.SerialMemory.EndingGuard == "" {
		t.Fatalf("serial repetition evidence incomplete: %#v", contract.SerialMemory)
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"configured_style_rules", "serial_style_memory", "assets/styles/romance.md",
		"不靠旁白宣布情绪", "他没有回答", "门外的脚步声",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compiled style contract missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "第五条不能进入") {
		t.Fatalf("serial memory exceeded bounded phrase budget: %s", text)
	}
}

func TestConfiguredStyleProfileExcludesSemanticPlanningRules(t *testing.T) {
	configured := configuredStyleProfileFromMarkdown("suspense", `## 悬疑推理风格

- **叙事结构**：多线叙事交织，逐步揭示真相。
- **线索管理**：关键线索必须在揭示前至少出现两次。
- **节奏控制**：每章末留悬念钩子。
- **氛围营造**：用光影和声音形成不安，但不替人物宣布情绪。
- **对话风格**：对峙时让言外之意多于字面说明。`)
	if configured == nil {
		t.Fatal("surface-only configured style unexpectedly empty")
	}
	if len(configured.Rules) != 2 {
		t.Fatalf("surface-only rules=%#v, want exactly atmosphere/dialogue", configured.Rules)
	}
	joined := strings.Join(configured.Rules, "\n")
	for _, forbidden := range []string{"多线叙事", "至少出现两次", "每章末留悬念"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("semantic planning rule leaked into render style: %s", joined)
		}
	}
	for _, want := range []string{"光影和声音", "言外之意"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("surface rule %q missing: %s", want, joined)
		}
	}
}

func TestConfiguredStyleProfileRejectsSemanticPayloadBehindSurfaceLabel(t *testing.T) {
	configured := configuredStyleProfileFromMarkdown("custom", `## 自定义表层风格

- **叙述声音**：用近距离自由间接话语贴近焦点人物。
- **叙述声音**：新增一次背叛并改变主角决定。
- **语域**：让主角在本章杀死反派。
- **节奏**：This chapter must reveal a new clue.
- **句法**：调整句子长短，让动作段更紧凑。`)
	if configured == nil {
		t.Fatal("valid surface rules were unexpectedly removed")
	}
	joined := strings.Join(configured.Rules, "\n")
	for _, forbidden := range []string{"新增一次背叛", "杀死反派", "must reveal"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("semantic mutation survived surface label guard: %s", joined)
		}
	}
	for _, want := range []string{"自由间接话语", "调整句子长短"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("legitimate presentation rule %q was removed: %s", want, joined)
		}
	}
}

func TestConfiguredStyleOverlayUpgradesHistoricalPacketWithoutChangingPlanFields(t *testing.T) {
	payload := map[string]any{
		"_context_profile": "draft",
		"working_memory": map[string]any{
			"render_packet": map[string]any{
				"version":         11,
				"chapter":         9,
				"mandatory_beats": []any{"人物仍作出冻结选择"},
				"style_contract": map[string]any{
					"version":     1,
					"profile_id":  "existing-profile",
					"source_refs": []any{"existing#source"},
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	overlaid, err := applyConfiguredStyleOverlay(raw, "suspense", `## 悬疑推理风格
- **氛围营造**：只从焦点人物能感知的光影和声音建立不安。`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(overlaid, &got); err != nil {
		t.Fatal(err)
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(got)
	if err != nil {
		t.Fatal(err)
	}
	contract := packet["style_contract"].(map[string]any)
	if contract["profile_id"] != "existing-profile" || contract["configured_style_id"] != "suspense" {
		t.Fatalf("overlay replaced existing profile or missed configured style: %#v", contract)
	}
	beats := packet["mandatory_beats"].([]any)
	if len(beats) != 1 || beats[0] != "人物仍作出冻结选择" {
		t.Fatalf("style overlay changed frozen content contract: %#v", packet)
	}
	refs := styleContractStringSlice(contract["source_refs"])
	if !slicesContainsString(refs, "existing#source") || !slicesContainsString(refs, "assets/styles/suspense.md") {
		t.Fatalf("style overlay lost provenance: %#v", refs)
	}
}

func TestConfiguredStyleOverlayReplacesOrDeletesPriorConfiguredProjection(t *testing.T) {
	base := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{
			"version":11,
			"chapter":9,
			"mandatory_beats":["冻结事件"],
			"style_contract":{
				"version":3,
				"profile_id":"keep-profile",
				"configured_style":{"id":"nested-a","rules":["嵌套旧规则"]},
				"configured_style_id":"a",
				"configured_style_name":"旧 A",
				"configured_style_rules":["旧规则 A"],
				"source_refs":["genre-style#keep","assets/styles/a.md","assets/styles/nested-a.md"]
			}
		}}
	}`)

	extract := func(t *testing.T, raw json.RawMessage) map[string]any {
		t.Helper()
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
		if err != nil {
			t.Fatal(err)
		}
		return packet["style_contract"].(map[string]any)
	}

	t.Run("A to B", func(t *testing.T) {
		overlaid, err := applyConfiguredStyleOverlay(base, "b", `## 新 B
- **叙述声音**：只陈述人物当下能感知的具体变化。`)
		if err != nil {
			t.Fatal(err)
		}
		contract := extract(t, overlaid)
		if contract["configured_style_id"] != "b" || contract["configured_style_name"] != "新 B" {
			t.Fatalf("configured style was not replaced: %#v", contract)
		}
		if _, exists := contract["configured_style"]; exists {
			t.Fatalf("legacy nested configured style survived replacement: %#v", contract)
		}
		text, _ := json.Marshal(contract)
		if strings.Contains(string(text), "旧规则") || strings.Contains(string(text), "assets/styles/a.md") ||
			strings.Contains(string(text), "assets/styles/nested-a.md") {
			t.Fatalf("prior configured fields/provenance survived A to B: %s", text)
		}
		refs := styleContractStringSlice(contract["source_refs"])
		if !slicesContainsString(refs, "genre-style#keep") || !slicesContainsString(refs, "assets/styles/b.md") {
			t.Fatalf("replacement lost unrelated provenance or new source: %#v", refs)
		}
	})

	t.Run("A to empty", func(t *testing.T) {
		overlaid, err := applyConfiguredStyleOverlay(base, "", "")
		if err != nil {
			t.Fatal(err)
		}
		contract := extract(t, overlaid)
		for _, key := range []string{
			"configured_style", "configured_style_id", "configured_style_name", "configured_style_rules",
		} {
			if _, exists := contract[key]; exists {
				t.Fatalf("empty effective style retained %s: %#v", key, contract)
			}
		}
		refs := styleContractStringSlice(contract["source_refs"])
		if len(refs) != 1 || refs[0] != "genre-style#keep" {
			t.Fatalf("empty effective style did not delete only configured provenance: %#v", refs)
		}
		if contract["profile_id"] != "keep-profile" {
			t.Fatalf("empty configured overlay damaged unrelated style contract: %#v", contract)
		}
	})
}

func TestDraftProfileDropsBlockingExternalReviewAfterPlanFreeze(t *testing.T) {
	plan := domain.ChapterPlan{Chapter: 2, Title: "返工"}
	working := map[string]any{
		"chapter_plan": &plan,
		"draft_external_ai_review": map[string]any{
			"blocking":          true,
			"summary":           "对白像流程清单",
			"reasons":           []string{"话轮只负责交接步骤"},
			"evidence":          []string{"先付款，再登记"},
			"revision_plan":     []string{"照着三轮台词改"},
			"dialogue_fix_plan": []string{"示例台词"},
		},
	}
	result := map[string]any{"chapter_plan": &plan, "working_memory": working}

	applyChapterContextProfile(result, "draft")

	if _, exists := working["draft_external_ai_review"]; exists {
		t.Fatalf("post-freeze blocking review remained a live prose overlay: %#v", working["draft_external_ai_review"])
	}
	if _, exists := working["draft_external_ai_review_policy"]; exists {
		t.Fatal("post-freeze external review policy remained in draft context")
	}
}
