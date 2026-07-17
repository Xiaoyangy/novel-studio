package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
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
