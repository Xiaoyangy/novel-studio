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

func TestChapterAttractionContentRequiresPlannedMemeEvidence(t *testing.T) {
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
	if err := requireChapterAttractionContent(s, 1, "第一章 失业饭桌\n\n这里只有普通对白。"); err == nil || !strings.Contains(err.Error(), "未兑现 trend_language_plan") {
		t.Fatalf("expected missing trend evidence, got %v", err)
	}
	if err := requireChapterAttractionContent(s, 1, "第一章 失业饭桌\n\n赵航没憋住，呱了一声，又被舅舅瞪回碗里。"); err != nil {
		t.Fatalf("planned trend evidence should pass: %v", err)
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
	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
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
	if len(changes) != 2 {
		t.Fatalf("expected trend and system normalizations, got %v", changes)
	}
	if got := plan.CausalSimulation.TrendLanguage[0].Item; got != "呱" {
		t.Fatalf("expected anchored trend item, got %q", got)
	}
	if problems := domain.SystemCompanionPlanProblems(plan.CausalSimulation); len(problems) > 0 {
		t.Fatalf("normalized system plan still contradicts policy: %v", problems)
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
