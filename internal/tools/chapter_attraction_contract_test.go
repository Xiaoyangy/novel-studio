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
	if evidence := inspectChapterAttractionEvidence(plan, "第一章 失业饭桌\n\n赵航没憋住，呱了一声，又被舅舅瞪回碗里。"); evidence.TrendPassed {
		t.Fatal("呱了一声 must not be recorded as a correct 呱， discourse opener")
	}
	if err := requireChapterAttractionContent(s, 1, "第一章 失业饭桌\n\n赵航抬眼：‘呱，这也叫关心？’"); err != nil {
		t.Fatalf("proper 呱， dialogue opener should pass: %v", err)
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

func completeAttractionTestPlan() domain.ChapterPlan {
	return domain.ChapterPlan{Chapter: 1, Title: "失业饭桌", CausalSimulation: domain.ChapterCausalSimulation{
		TrendLanguage: []domain.TrendLanguagePlan{{
			Item: "呱", SourceContext: "项目联网简报", CharacterCarrier: "赵航",
			SceneFunction: "饭桌误会反应", UsageBudget: "1次", ForbiddenUsage: "旁白和章末不用",
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
