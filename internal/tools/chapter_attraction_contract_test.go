package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestChapterAttractionContentTreatsPlannedMemeAsOptional(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 128); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{
		Structured:  rules.Structured{Genre: "都市脑洞轻松搞笑爽文"},
		Preferences: "长篇连载，热梗可少量点缀",
	}); err != nil {
		t.Fatal(err)
	}

	plan := completeAttractionTestPlan()
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := requireChapterAttractionContent(s, 1, "第一章 失业饭桌\n\n这里只有普通对白。"); err != nil {
		t.Fatalf("missing optional trend phrase must not block prose: %v", err)
	}
	if evidence := inspectChapterAttractionEvidence(plan, "第一章 失业饭桌\n\n赵航没憋住，呱了一声，又被舅舅瞪回碗里。"); len(evidence.TrendMatches) != 0 || len(evidence.TrendMisuses) != 1 {
		t.Fatalf("呱了一声 must be recorded as an attempted misuse, not a missing required beat: %+v", evidence)
	}
	if err := requireChapterAttractionContent(s, 1, "第一章 失业饭桌\n\n赵航抬眼：‘呱，这也叫关心？’"); err != nil {
		t.Fatalf("proper 呱， dialogue opener should pass: %v", err)
	}
}

func TestAttractionEvidenceTreatsHumorPayoffAndUnusedTrendAsCandidates(t *testing.T) {
	plan := completeAttractionTestPlan()
	evidence := inspectChapterAttractionEvidence(plan, "第一章 失业饭桌\n\n林澈没有使用计划里的热梗，先替父亲把话接了过去。")
	if evidence.OpeningCandidate == "" || len(evidence.HumorCandidates) != 2 || len(evidence.PayoffCandidates) != 2 {
		t.Fatalf("planning candidates were lost: %+v", evidence)
	}
	if len(evidence.TrendMatches) != 0 || len(evidence.TrendMisuses) != 0 {
		t.Fatalf("unused optional trend must not become a pass/fail item: %+v", evidence)
	}
	if !strings.Contains(evidence.SelectionPolicy, "不逐项") || !strings.Contains(evidence.SelectionPolicy, "省略") {
		t.Fatalf("candidate selection policy is ambiguous: %+v", evidence)
	}
}

func TestRewriteBriefStyleLiteralsAreOptionalButPlainWordingContractRemains(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, s,
		"第一章 失业饭桌\n\n旧正文。",
		"# brief\n\n## 用户本轮要求\n\n- 开头用‘回来也别闲着’；赵航必须说成‘呱，’起头；自然落地一次原句‘那还说啥了，给你了呗’。\n")
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "失业饭桌"}); err != nil {
		t.Fatal(err)
	}
	missingPlainWording := "第一章 失业饭桌\n\n赵航替他挡了一句，林澈趁机离席。"
	if err := requireChapterAttractionContent(s, 1, missingPlainWording); err == nil || !strings.Contains(err.Error(), "回来也别闲着") {
		t.Fatalf("missing non-style wording contract must block commit, got %v", err)
	}
	withoutMemes := "第一章 失业饭桌\n\n回来也别闲着。赵航替他挡了一句，林澈趁机离席。"
	if err := requireChapterAttractionContent(s, 1, withoutMemes); err != nil {
		t.Fatalf("optional trend literals must not be forced into the rewrite: %v", err)
	}
}

func TestTrendLanguagePlanMustUseChapterBriefItem(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# brief\n\n## 第一章热梗落点\n\n- `呱`\n- `那还说啥了，给你了呗`\n\n## 禁用\n\n- 其他"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong := []domain.TrendLanguagePlan{{
		Item: "不是，哥们", SourceContext: "短视频评论区", CharacterCarrier: "林澈",
		SceneFunction: "自嘲", UsageBudget: "1次", ForbiddenUsage: "章末不用",
	}}
	if trendLanguagePlanGroundedInChapterBrief(s, 1, wrong) {
		t.Fatal("unlisted meme must not be accepted for chapter one")
	}
	allowed := []domain.TrendLanguagePlan{{
		Item: "呱", SourceContext: "meta/web_reference_brief.md 第一章热梗落点", CharacterCarrier: "赵航",
		SceneFunction: "饭桌反应", UsageBudget: "1次", ForbiddenUsage: "章末不用",
	}}
	if !trendLanguagePlanGroundedInChapterBrief(s, 1, allowed) {
		t.Fatal("chapter brief item with explicit source should pass")
	}
}

func TestStructuredTrendPolicyIsScopedToCurrentChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 本书简报\n\n## 第一章热梗落点\n\n- `呱，`\n\n## 全书限制\n\n标题不堆网络梗。\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "version": 1,
  "chapter_trend_language": {
    "1": [{
      "item": "呱，",
      "source_context": "meta/web_reference_brief.md#第一章热梗落点",
      "character_carrier": "赵航用网络语气词起手",
      "scene_function": "饭桌反应",
      "usage_budget": "全章一次并后接完整吐槽",
      "forbidden_usage": "旁白与章末禁用"
    }]
  }
}`
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if !attractionRequirementsForChapter(s, 1).Trend {
		t.Fatal("chapter one mapped trend item must be required")
	}
	if attractionRequirementsForChapter(s, 2).Trend {
		t.Fatal("chapter-one trend policy leaked into chapter two")
	}

	chapterOne := domain.ChapterPlan{Chapter: 1, Title: "第一章"}
	chapterOne.CausalSimulation.TrendLanguage = []domain.TrendLanguagePlan{{
		Item: "呱，", SourceContext: "meta/web_reference_brief.md#第一章热梗落点",
		CharacterCarrier: "赵航用网络语气词起手", SceneFunction: "饭桌反应",
		UsageBudget: "全章一次并后接完整吐槽", ForbiddenUsage: "旁白与章末禁用",
	}}
	if !ChapterAttractionPlanReadyForProject(s, chapterOne) {
		t.Fatal("grounded chapter-one policy should be ready")
	}

	chapterTwo := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	chapterTwo.CausalSimulation.TrendLanguage = []domain.TrendLanguagePlan{{
		Item: "先借一辆", SourceContext: "meta/web_reference_brief.md 第2章 active project item",
		CharacterCarrier: "系统", SceneFunction: "借车反转", UsageBudget: "一次", ForbiddenUsage: "章末禁用",
	}}
	if trendLanguagePlanGroundedInChapterBrief(s, 2, chapterTwo.CausalSimulation.TrendLanguage) || ChapterAttractionPlanReadyForProject(s, chapterTwo) {
		t.Fatal("an invented chapter-two web-brief item must not pass grounding or routing")
	}
	chapterTwo.CausalSimulation.TrendLanguage = []domain.TrendLanguagePlan{{
		Item: "none", SourceContext: "本章无结构化热梗落点", CharacterCarrier: "不适用",
		SceneFunction: "保持现场口语", UsageBudget: "0次", ForbiddenUsage: "禁止临时伪造联网来源",
	}}
	if !ChapterAttractionPlanReadyForProject(s, chapterTwo) {
		t.Fatal("a complete none sentinel should pass when this chapter has no mapped trend item")
	}

	duplicate := append([]domain.TrendLanguagePlan(nil), chapterOne.CausalSimulation.TrendLanguage...)
	duplicate = append(duplicate, chapterOne.CausalSimulation.TrendLanguage[0])
	if trendLanguagePlanGroundedInChapterBrief(s, 1, duplicate) {
		t.Fatal("duplicating one allowed item must not masquerade as complete mapped coverage")
	}

	if err := s.UserRules.Save(&rules.Snapshot{Preferences: "每章最多用一个热梗，但不堆网络梗"}); err != nil {
		t.Fatal(err)
	}
	if !attractionRequirementsForChapter(s, 2).Trend {
		t.Fatal("latest explicit user rule must take priority over an older chapter map")
	}
	if ChapterAttractionPlanReadyForProject(s, chapterTwo) {
		t.Fatal("missing chapter-two mapping must surface as a contract gap, not silently downgrade the explicit user rule")
	}
}

func TestLegacyChapterTrendSectionsDoNotLeakAcrossChapters(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 旧简报\n\n## 第一章热梗落点\n\n- `呱，`\n\n## 禁用\n\n其他章节不堆网络梗。\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if !attractionRequirementsForChapter(s, 1).Trend || attractionRequirementsForChapter(s, 2).Trend {
		t.Fatalf("legacy chapter headings were not scoped: ch1=%+v ch2=%+v", attractionRequirementsForChapter(s, 1), attractionRequirementsForChapter(s, 2))
	}
}

func TestChapterAttractionPlanReadyForProjectIncludesWebBrief(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 项目文风\n\n都市脑洞轻松搞笑爽文；系统会接话并始终支持主角。\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	plan.CausalSimulation.EntertainmentPlan.CompanionVoiceBeat = "系统用短促吐槽接话并支持主角。"
	plan.CausalSimulation.EntertainmentPlan.ForbiddenComedy = []string{"不连续抛梗"}
	if ChapterAttractionPlanReadyForProject(s, plan) {
		t.Fatal("routing must not ignore entertainment fields required only by meta/web_reference_brief.md")
	}
	plan.CausalSimulation.EntertainmentPlan.OpeningBeat = "买车失败立刻兑现边界笑点"
	plan.CausalSimulation.EntertainmentPlan.HumorBeats = []string{"系统短促吐槽", "旧车与新车反差"}
	plan.CausalSimulation.EntertainmentPlan.ImmediatePayoffs = []string{"借到有限运力", "一笔真实成交"}
	plan.CausalSimulation.EntertainmentPlan.ProcedureCompression = "运输和付款只留结果证据"
	if !ChapterAttractionPlanReadyForProject(s, plan) {
		t.Fatal("complete project-level attraction plan should be ready")
	}
}

func TestJSONOnlySystemCompanionPolicyIsRequired(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "version": 1,
  "system_companion": {
    "required": true,
    "companion_voice_beat": "系统短促接话并支持主角",
    "forbidden_comedy": ["不连续抛梗"]
  }
}`
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ProjectRequiresSystemCompanion(s) {
		t.Fatal("shared project contract must honor JSON-only required=true")
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	if ChapterAttractionPlanReadyForProject(s, plan) {
		t.Fatal("JSON required=true must not be ignored when Markdown is absent")
	}
	plan.CausalSimulation.EntertainmentPlan.CompanionVoiceBeat = "系统用短句接话并始终支持主角"
	if !ChapterAttractionPlanReadyForProject(s, plan) {
		t.Fatal("a compatible system companion beat should satisfy the JSON-only policy")
	}
}

func TestProjectSystemCompanionSourcePrecedence(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(s.Dir(), "meta", "web_reference_brief.md")
	if err := os.WriteFile(briefPath, []byte("旧简报：系统不能聊天，纯任务机器人即可。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Preferences: "最新要求：系统会接话并始终支持主角。"}); err != nil {
		t.Fatal(err)
	}
	if !ProjectRequiresSystemCompanion(s) {
		t.Fatal("newer positive user rules must override a stale negative Markdown brief")
	}
	if err := os.WriteFile(briefPath, []byte("旧简报：系统会接话并始终支持主角。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Preferences: "最新要求：系统不能聊天，纯任务机器人即可。"}); err != nil {
		t.Fatal(err)
	}
	if ProjectRequiresSystemCompanion(s) {
		t.Fatal("an explicit user prohibition must override a stale positive Markdown brief")
	}
	policy := `{"version":1,"system_companion":{"required":true}}`
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ProjectRequiresSystemCompanion(s) {
		t.Fatal("structured required=true must remain the highest-precedence contract")
	}
}

func TestLegacyH3TrendSectionsStopAtSiblingHeading(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 旧简报\n\n### 第一章热梗落点补充\n\n- `伪梗`\n\n### 第一章热梗落点 ###\n\n- `甲梗`\n\n### 第二章热梗落点\n\n- `乙梗`\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	section := chapterTrendLanguageBriefSection(s, 1)
	if !strings.Contains(section, "甲梗") || strings.Contains(section, "乙梗") || strings.Contains(section, "伪梗") {
		t.Fatalf("h3 chapter section crossed into its sibling: %q", section)
	}
	wrong := []domain.TrendLanguagePlan{{
		Item: "乙梗", SourceContext: "meta/web_reference_brief.md 第一章热梗落点",
		CharacterCarrier: "角色", SceneFunction: "反应", UsageBudget: "一次", ForbiddenUsage: "章末禁用",
	}}
	if trendLanguagePlanGroundedInChapterBrief(s, 1, wrong) {
		t.Fatal("a sibling h3 chapter item must not ground the current chapter")
	}
}

func TestLegacyH2TrendSectionStopsAtMixedLevelChapterHeading(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 旧简报\n\n## 第一章热梗落点\n\n- `甲梗`\n\n### 使用说明\n\n只进角色对白。\n\n### 第二章热梗落点\n\n- `乙梗`\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	section := chapterTrendLanguageBriefSection(s, 1)
	if !strings.Contains(section, "甲梗") || !strings.Contains(section, "使用说明") || strings.Contains(section, "乙梗") {
		t.Fatalf("mixed-level chapter scope was parsed incorrectly: %q", section)
	}
}

func TestNormalizeChapterAttractionPlanAnchorsExplicitPolicy(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 128); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{
		Structured:  rules.Structured{Genre: "都市轻松搞笑爽文"},
		Preferences: "热梗少量点缀。系统会与主角交流解闷，系统始终支持主角。长篇连载。",
	}); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "version": 1,
  "chapter_trend_language": {"1": [{
    "item": "呱", "source_context": "meta/web_reference_brief.md#第一章热梗落点",
    "character_carrier": "赵航饭桌短促反应", "scene_function": "承载旁观者反应",
    "usage_budget": "1次", "forbidden_usage": "旁白和章末不用"
  }]},
  "system_companion": {
    "required": true,
    "companion_voice_beat": "系统会接话吐槽并始终支持林澈，但不替他做决定。",
    "forbidden_comedy": ["不让系统连续抛梗或主动剧透"]
  }
}`
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Contract: domain.ChapterContract{RequiredBeats: []string{
		"赵航必须说成“呱，”再替林澈解围",
		"林澈完成第一笔真实消费",
	}}, CausalSimulation: domain.ChapterCausalSimulation{
		TrendLanguage: []domain.TrendLanguagePlan{{
			Item: "不是，哥们", SourceContext: "评论区", CharacterCarrier: "林澈",
			SceneFunction: "自嘲", UsageBudget: "1次", ForbiddenUsage: "章末不用",
		}},
		EntertainmentPlan: domain.ReaderEntertainmentPlan{
			OpeningBeat: "当众受辱", HumorBeats: []string{"笑点一", "笑点二"}, ImmediatePayoffs: []string{"兑现一", "兑现二"},
			ProcedureCompression: "压缩流程", CompanionVoiceBeat: "系统不接话", ForbiddenComedy: []string{"把系统写成会吐槽的聊天伙伴"},
		},
		AntiAIPlan: domain.AntiAIExecutionPlan{
			ObjectResponseBudget: "系统不回应情绪吐槽", DialogueFunctionPlan: "短对话",
			CounterMoves: []string{"系统保持冷硬"}, ReviewChecks: []string{"未变成陪聊"},
		},
		DialogueBlueprints: []domain.DialogueSceneBlueprint{{
			SceneID: "system-binding", ScenePressure: "不拟人闲聊", ExitBeat: "系统界面保持静默",
			TurnProgression: []domain.DialogueTurnDesign{{Speaker: "手机界面", HiddenSubtext: "不暗示系统人格"}},
		}},
	}}
	changes := normalizeChapterAttractionPlan(s, &plan)
	if len(changes) != 3 {
		t.Fatalf("expected trend anchoring, style demotion and system normalization, got %v", changes)
	}
	if got := plan.CausalSimulation.TrendLanguage[0].Item; got != "呱" {
		t.Fatalf("expected anchored trend item, got %q", got)
	}
	if problems := domain.SystemCompanionPlanProblems(plan.CausalSimulation); len(problems) > 0 {
		t.Fatalf("normalized system plan still contradicts policy: %v", problems)
	}
	if len(plan.Contract.RequiredBeats) != 1 || !strings.Contains(plan.Contract.RequiredBeats[0], "第一笔真实消费") {
		t.Fatalf("style literal should be demoted from required beats: %v", plan.Contract.RequiredBeats)
	}
	if !contextSourcesContain(plan.CausalSimulation.ContextSources, "style_policy:meta/web_reference_brief.json") {
		t.Fatalf("normalization provenance missing: %v", plan.CausalSimulation.ContextSources)
	}
}

func TestSystemCompanionNormalizationDoesNotLeakAnotherProjectsProtagonist(t *testing.T) {
	got := normalizeSystemCompanionText("系统不接话，系统保持静默")
	if strings.Contains(got, "林澈") || !strings.Contains(got, "支持主角") {
		t.Fatalf("generic system companion normalization leaked a project-specific protagonist: %q", got)
	}
	blueprints := []domain.DialogueSceneBlueprint{{
		SceneID: "system", RelationshipFrame: "规则边界明确",
		TurnProgression: []domain.DialogueTurnDesign{{Speaker: "系统界面"}},
	}}
	normalizeSystemCompanionDialogues(blueprints)
	if strings.Contains(blueprints[0].RelationshipFrame, "林澈") || !strings.Contains(blueprints[0].RelationshipFrame, "支持主角") {
		t.Fatalf("dialogue normalization leaked a project-specific protagonist: %q", blueprints[0].RelationshipFrame)
	}
}

func completeAttractionTestPlan() domain.ChapterPlan {
	return domain.ChapterPlan{Chapter: 1, Title: "失业饭桌", CausalSimulation: domain.ChapterCausalSimulation{
		TrendLanguage: []domain.TrendLanguagePlan{{
			Item: "呱，", SourceContext: "项目联网简报", CharacterCarrier: "赵航以网络语气词起手",
			SceneFunction: "后接完整吐槽承载饭桌误会反应，禁止写成拟声", UsageBudget: "1次并后接完整台词", ForbiddenUsage: "旁白和章末不用",
		}},
		EntertainmentPlan: domain.ReaderEntertainmentPlan{
			OpeningBeat:          "前200字当众点破失业",
			HumorBeats:           []string{"赵航误会后被瞪", "系统接住林澈自嘲"},
			ImmediatePayoffs:     []string{"系统验证", "夜市灯亮"},
			ProcedureCompression: "询价与安装只留冲突节点",
			CompanionVoiceBeat:   "系统短促吐槽并支持林澈",
			ForbiddenComedy:      []string{"不串梗"},
		},
		LongformOpening: domain.LongformOpeningDesign{
			TargetReader: "大众男频读者", OpeningHook: "失业当场绑定系统", SerialEngine: "县城项目逐级扩张",
			ReaderRewardLoop:  []string{"小项目即时见效"},
			LongRangePromises: []domain.LongRangePromise{{Promise: "县城翻新", FirstChapterSeed: "夜市灯", PayoffHorizon: "全书"}},
			RevealBudget:      []string{"不解释系统来源"}, FirstChapterProof: []string{"第一笔消费见效"}, RetentionRisks: []string{"流程过长"},
		},
	}}
}

func TestLongformScaleDeclaredIgnoresNeutralMentions(t *testing.T) {
	// A short-story contract whose POV rule says "无论长篇还是短篇" must NOT be read
	// as a longform declaration (that wrongly required a chapter-one longform
	// opening and blocked project-all for the whole book).
	shortContracts := []string{
		"无论长篇还是短篇，统一采用第三人称视角；总字数2.8万—3万中文字",
		"当代职业爱情短篇，不论长篇短篇都用近距离限知",
	}
	for _, c := range shortContracts {
		if longformScaleDeclared(c) {
			t.Fatalf("neutral 长篇/短篇 mention must not declare longform: %q", c)
		}
	}
	for _, c := range []string{"这是一部百万字长篇连载", "本书为长篇都市小说", "预计三十万字"} {
		if !longformScaleDeclared(c) {
			t.Fatalf("explicit large-scale declaration must register: %q", c)
		}
	}
}
