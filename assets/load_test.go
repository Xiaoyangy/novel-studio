package assets

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadReferencesIncludesProductionPlaybook(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.ProductionPlaybook, "章节任务单") {
		t.Fatalf("expected production playbook to be loaded")
	}
	if !strings.Contains(bundle.References.ProductionPlaybook, "质量债务") {
		t.Fatalf("expected production playbook to include quality debt rules")
	}
}

func TestBundleSelectedStyleDefaultsAndFallsBack(t *testing.T) {
	bundle := Load("")
	defaultStyle := bundle.Styles["default"]
	if strings.TrimSpace(defaultStyle) == "" {
		t.Fatal("embedded default style is empty")
	}
	if got := bundle.ResolveStyle(""); got.ID != "default" || got.Body != defaultStyle {
		t.Fatalf("empty configured style did not resolve default identity/body: %#v", got)
	}
	if got := bundle.ResolveStyle("missing-style"); got.ID != "default" || got.Body != defaultStyle {
		t.Fatalf("missing configured style split fallback identity/body: %#v", got)
	}
	if got := bundle.ResolveStyle("romance"); got.ID != "romance" || !strings.Contains(got.Body, "言情风格") {
		t.Fatalf("explicit style selection failed: %#v", got)
	}
	if got := bundle.SelectedStyle("missing-style"); got != defaultStyle {
		t.Fatal("body-only compatibility selector diverged from resolved fallback")
	}
}

func TestLoadUsesTheSameEffectiveStyleForReferencesAndBody(t *testing.T) {
	canonical := Load("fantasy")
	spaced := Load("  fantasy  ")
	if strings.TrimSpace(canonical.References.StyleReference) == "" ||
		strings.TrimSpace(canonical.References.ArcTemplates) == "" {
		t.Fatal("fantasy genre references are unexpectedly empty")
	}
	if spaced.References.StyleReference != canonical.References.StyleReference ||
		spaced.References.ArcTemplates != canonical.References.ArcTemplates {
		t.Fatal("whitespace-normalized style body and genre references diverged")
	}
	if got := spaced.ResolveStyle("  fantasy  "); got.ID != "fantasy" || got.Body != canonical.Styles["fantasy"] {
		t.Fatalf("whitespace-normalized effective style diverged: %#v", got)
	}

	defaultBundle := Load("default")
	missing := Load("missing-style")
	if missing.References.StyleReference != defaultBundle.References.StyleReference ||
		missing.References.ArcTemplates != defaultBundle.References.ArcTemplates {
		t.Fatal("missing style did not atomically fall back to default references")
	}
	if got := missing.ResolveStyle("missing-style"); got.ID != "default" || got.Body != defaultBundle.Styles["default"] {
		t.Fatalf("missing style did not atomically fall back to default body: %#v", got)
	}
}

func TestLoadReferencesIncludesHumanFeelCraft(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.HumanFeelCraft, "同桌是只假装高冷的猫") {
		t.Fatalf("expected human feel craft source to be loaded")
	}
	if !strings.Contains(bundle.References.HumanFeelCraft, "物件回扣") {
		t.Fatalf("expected human feel craft to include object callback rules")
	}
}

func TestLoadReferencesIncludesCharacterAndEmotionalCraft(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.CharacterBuilding, "人物塑造原则") {
		t.Fatalf("expected character building reference to be loaded")
	}
	if !strings.Contains(bundle.References.EmotionalNarrativeCraft, "情感叙事与人物推进写法") {
		t.Fatalf("expected emotional narrative craft reference to be loaded")
	}
	if !strings.Contains(bundle.References.EmotionalNarrativeCraft, "长循环处理规则") {
		t.Fatalf("expected emotional narrative craft to define loop handling")
	}
}

func TestLoadReferencesIncludesFictionParagraphing(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.FictionParagraphing, "小说正文分段规范") {
		t.Fatalf("expected fiction paragraphing reference to be loaded")
	}
	if !strings.Contains(bundle.References.FictionParagraphing, "文字墙候选") {
		t.Fatalf("expected fiction paragraphing reference to include long paragraph handling")
	}
	if !strings.Contains(bundle.References.FictionParagraphing, "一条消息独立一个自然段") {
		t.Fatalf("expected fiction paragraphing reference to isolate system messages")
	}
}

func TestLoadPromptsIncludeGoldenThreeAndWholeTextAIGCContracts(t *testing.T) {
	bundle := Load("default")
	for name, prompt := range map[string]string{
		"writer":  bundle.Prompts.Writer,
		"drafter": bundle.Prompts.Drafter,
		"planner": bundle.Prompts.Planner,
		"editor":  bundle.Prompts.Editor,
	} {
		if !strings.Contains(prompt, "黄金三章") {
			t.Fatalf("%s prompt missing golden-three contract", name)
		}
		if name != "planner" && !strings.Contains(prompt, "系统消息") {
			t.Fatalf("%s prompt missing system-message layout contract", name)
		}
	}
	if !strings.Contains(bundle.Prompts.Writer, "三条原始曲线") {
		t.Fatalf("writer prompt missing whole-text raw-curve contract")
	}
	for _, want := range []string{"跨章功能建议边界", "不得降低当前章任何维度评分", "不能把面向下一章的优化意见倒签成当前章返工理由"} {
		if !strings.Contains(bundle.Prompts.Editor, want) {
			t.Fatalf("editor prompt missing next-chapter advice boundary %q", want)
		}
	}
}

func TestDrafterPromptKeepsExactRenderOnlySubmissionContracts(t *testing.T) {
	prompt := Load("default").Prompts.Drafter
	for _, want := range []string{
		"少 1 字或多 1 字都会拒绝",
		"首行必须逐字使用 `render_packet.heading`",
		"submission_target_min-max",
		"章级契约要求仅一条短提示时只能写一条",
		"禁止为了说明完整把金额、禁项、地点、时限和验货要求塞进同一个",
		"不得套用固定的 2-3 条配额",
		"显式整章重渲染、AIGC 整章换稿",
		"不得读取旧 draft/final",
		"先删掉或合并整轮功能问答",
		"无论 `anti_ai_render_contract` 字段是否存在",
		"旧 sealed render packet 的兼容合同",
		"不等待检测、审稿或失败返工后再补",
		"让刺激先改变主视角人物的注意、判断或误判",
		"物件、屏幕和消息只在改变判断、选择、关系或安全后果时回应",
		"可能是非空章级 `anti_ai_execution_plan` 的定性投影，也可能是与上述要求同义的通用基线",
		"以更具体的章级规则优先细化上述基线",
		"不得假定上游曾提供完整专项计划",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("drafter prompt missing exact render-only contract %q", want)
		}
	}
	for _, stale := range []string{
		"高于上限 20% 以上",
		"首次绑定也不例外",
		"返工读取本章旧终稿",
		"先 `read_chapter(source=\"final\")` 读原文",
		"上游完整 `anti_ai_execution_plan` 已净化投影",
	} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("drafter prompt retained contradictory rule %q", stale)
		}
	}
}

func TestLoadReferencesIncludesWritingTechniquesDigest(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "19 篇写作技巧文章") {
		t.Fatalf("expected writing techniques digest source count to be loaded")
	}
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "中文标点") {
		t.Fatalf("expected writing techniques digest to include punctuation rules")
	}
	if !strings.Contains(bundle.References.WritingTechniquesDigest, "过渡章") {
		t.Fatalf("expected writing techniques digest to include article-level extraction")
	}
}

func TestLoadReferencesIncludesRAGWritingGuidelines(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.RAGWritingGuidelines, "retrieval_trace") {
		t.Fatalf("expected RAG writing guidelines to mention retrieval trace")
	}
	if !strings.Contains(bundle.References.RAGWritingGuidelines, "弱召回") {
		t.Fatalf("expected RAG writing guidelines to define weak recall handling")
	}
}

func TestLoadReferencesIncludesWebReferenceGuidelines(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.References.WebReferenceGuidelines, "web_reference_brief") {
		t.Fatalf("expected web reference guidelines to mention web_reference_brief")
	}
	if !strings.Contains(bundle.References.WebReferenceGuidelines, "热梗") {
		t.Fatalf("expected web reference guidelines to define trend language handling")
	}
}

func TestLoadReferencesIncludesLiteraryRendering(t *testing.T) {
	bundle := Load("default")
	for _, want := range []string{
		"文学渲染协议",
		"焦点化",
		"自由间接话语",
		"不得设置固定次数、固定比例或统一句长",
		"中文转译边界",
		"话题持续、零回指、承前连接和上下文可恢复性",
		"https://www-archiv.fdm.uni-hamburg.de/lhn/node/26.html",
		"https://www.qk.sjtu.edu.cn/cfls/CN/10.3969/j.issn.1674-8921.2015.12.014",
		"card_id: focalization-boundary",
		"card_id: psychic-distance",
		"card_id: scene-summary",
		"card_id: goal-causality",
		"card_id: emotion-appraisal",
		"card_id: motif-return",
		"card_id: syntax-rhythm",
		"card_id: free-indirect-discourse",
		"card_id: dialogue-subtext",
	} {
		if !strings.Contains(bundle.References.LiteraryRendering, want) {
			t.Fatalf("expected literary rendering reference to contain %q", want)
		}
	}
	for _, want := range []string{
		`"version": 1`,
		`"id": "focalization-boundary"`,
		`"id": "dialogue-subtext"`,
		`"hard_boundary": true`,
		"不做九项清单",
	} {
		if !strings.Contains(bundle.References.LiteraryRenderingCards, want) {
			t.Fatalf("expected compact literary rendering cards to contain %q", want)
		}
	}
	var catalog struct {
		Version int `json:"version"`
		Cards   []struct {
			ID           string `json:"id"`
			Decision     string `json:"decision"`
			Move         string `json:"move"`
			Avoid        string `json:"avoid"`
			HardBoundary bool   `json:"hard_boundary"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(bundle.References.LiteraryRenderingCards), &catalog); err != nil {
		t.Fatalf("decode compact literary rendering cards: %v", err)
	}
	if catalog.Version != 1 || len(catalog.Cards) != 9 {
		t.Fatalf("unexpected compact literary rendering catalog: version=%d cards=%d", catalog.Version, len(catalog.Cards))
	}
	for _, card := range catalog.Cards {
		if card.ID == "" || card.Decision == "" || card.Move == "" || card.Avoid == "" {
			t.Fatalf("compact literary rendering card is incomplete: %#v", card)
		}
	}
}

func TestLoadReferencesIncludesGenreStyleProfiles(t *testing.T) {
	bundle := Load("default")
	for _, want := range []string{
		"题材专项写法：轻松县城经营、系统、单女主",
		"card_id: spoken-breath-group",
		"一个“气口”",
		"唯一恋爱指向",
	} {
		if !strings.Contains(bundle.References.GenreStyleCraft, want) {
			t.Fatalf("expected genre style craft to contain %q", want)
		}
	}
	var catalog struct {
		Version  int `json:"version"`
		Profiles []struct {
			ID                   string `json:"id"`
			DialogueBreathPolicy string `json:"dialogue_breath_policy"`
			RomancePolicy        string `json:"romance_policy"`
			Cards                []struct {
				ID string `json:"id"`
			} `json:"cards"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(bundle.References.GenreStyleProfiles), &catalog); err != nil {
		t.Fatalf("decode genre style profiles: %v", err)
	}
	if catalog.Version != 1 || len(catalog.Profiles) != 1 {
		t.Fatalf("unexpected genre style catalog: version=%d profiles=%d", catalog.Version, len(catalog.Profiles))
	}
	profile := catalog.Profiles[0]
	if profile.ID != "county-light-comedy-system-single-romance" || len(profile.Cards) != 8 {
		t.Fatalf("unexpected genre style profile: %#v", profile)
	}
	if !strings.Contains(profile.DialogueBreathPolicy, "完整气口") || !strings.Contains(profile.RomancePolicy, "唯一恋爱线") {
		t.Fatalf("genre style profile lost hard boundaries: %#v", profile)
	}
}

func TestWritingPromptsCarryLiteraryRenderingContractWithoutQuotas(t *testing.T) {
	bundle := Load("default")
	for name, prompt := range map[string]string{
		"writer":  bundle.Prompts.Writer,
		"drafter": bundle.Prompts.Drafter,
		"editor":  bundle.Prompts.Editor,
	} {
		if !strings.Contains(prompt, "literary_render") {
			t.Fatalf("%s prompt does not connect the literary rendering contract", name)
		}
	}
	if !strings.Contains(bundle.Prompts.Writer, "literary-rendering#<card_id>") {
		t.Fatal("writer prompt must preserve stable literary card provenance")
	}
	if !strings.Contains(bundle.Prompts.Drafter, "文学合同硬软分层") ||
		!strings.Contains(bundle.Prompts.Drafter, "soft_scene_choices") ||
		!strings.Contains(bundle.Prompts.Drafter, "可重排、替换或全部省略") {
		t.Fatal("drafter prompt must keep literary POV boundaries hard while treating shots as optional candidates")
	}
	if !strings.Contains(bundle.Prompts.Editor, "才是硬问题") || !strings.Contains(bundle.Prompts.Editor, "软诊断") {
		t.Fatal("editor prompt must separate hard evidence boundaries from aesthetic diagnostics")
	}
}

func TestWritingPromptsRemainProjectNeutral(t *testing.T) {
	bundle := Load("default")
	if !strings.Contains(bundle.Prompts.Writer, "章节目标以本轮 task 为最高优先级") {
		t.Fatal("writer prompt must pin every planning tool to the task chapter")
	}
	if !strings.Contains(bundle.Prompts.Planner, "章节号以本轮 task 为最高优先级") {
		t.Fatal("planner prompt must pin every planning tool to the task chapter")
	}

	combined := strings.Join([]string{
		bundle.Prompts.Planner,
		bundle.Prompts.Writer,
		bundle.Prompts.Drafter,
		bundle.Prompts.Editor,
	}, "\n")
	for _, leaked := range []string{
		"许闻溪", "梁渡", "夏岚", "傅行简", "程棠",
		"澄光生活", "溪流助手", "江烬", "阴司银行", "黑伞先生",
	} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("builtin writing prompts leaked project-specific term %q", leaked)
		}
	}
}

func TestLightheartedTitleAndToneContractIsLoaded(t *testing.T) {
	bundle := Load("default")
	for name, prompt := range map[string]string{
		"architect": bundle.Prompts.ArchitectLong,
		"writer":    bundle.Prompts.Writer,
		"editor":    bundle.Prompts.Editor,
	} {
		if !strings.Contains(prompt, "轻松") {
			t.Fatalf("%s prompt must carry the lighthearted tone contract", name)
		}
	}
	if !strings.Contains(bundle.Prompts.ArchitectLong, "流程标签") {
		t.Fatal("architect prompt must reject report-like chapter titles")
	}
	if !strings.Contains(bundle.Prompts.Writer, "全章余味合同") {
		t.Fatal("writer prompt must preserve the lighthearted whole-chapter aftertaste")
	}
	if !strings.Contains(bundle.Prompts.Editor, "标题与总体基调") {
		t.Fatal("editor prompt must review title appeal and overall tone together")
	}
}
