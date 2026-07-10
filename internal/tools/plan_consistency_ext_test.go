package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCheckChapterPlanConsistencyForbiddenRequiredContradiction(t *testing.T) {
	st := store.NewStore(t.TempDir())
	plan := domain.ChapterPlan{
		Chapter: 1,
		Contract: domain.ChapterContract{
			RequiredBeats:  []string{"主角揭穿骗局"},
			ForbiddenMoves: []string{"主角揭穿骗局"}, // 自我矛盾
		},
	}
	hard, _ := checkChapterPlanConsistency(st, plan)
	if len(hard) == 0 {
		t.Fatal("契约自我矛盾应产生 hard 错误")
	}
	if !strings.Contains(hard[0], "自我矛盾") {
		t.Fatalf("hard 错误措辞不对: %v", hard)
	}
}

func TestCheckChapterPlanConsistencyRequiresOutlineTitle(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1,
		Title:   "失业饭桌",
	}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "只许把钱花在青山县",
	}
	hard, _ := checkChapterPlanConsistency(st, plan)
	if len(hard) == 0 {
		t.Fatal("plan.title 偏离 outline.title 应产生 hard 错误")
	}
	if !strings.Contains(hard[0], "章节标题与大纲不一致") {
		t.Fatalf("hard 错误措辞不对: %v", hard)
	}
}

func TestCheckChapterPlanConsistencyRejectsHiddenCharacterInRequiredBeat(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	sim := domain.ChapterWorldSimulation{
		Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "承认失业", true),
			simulatedDecision("沈知遥", "留在办公室", false),
		},
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Contract: domain.ChapterContract{
		RequiredBeats: []string{"让沈知遥在林家饭桌追问林澈"},
	}}
	hard, _ := checkChapterPlanConsistency(st, plan)
	if len(hard) == 0 || !strings.Contains(strings.Join(hard, "\n"), "visible_to_pov=false") {
		t.Fatalf("hidden character entering a visible beat must fail: %v", hard)
	}
}

func TestChapterPlanScopeCheckFlagsForbiddenHit(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter:  1,
		Goal:     "抵达北境",
		Contract: domain.ChapterContract{ForbiddenMoves: []string{"主角提前觉醒神力"}},
	}
	// 正文命中禁止项关键词
	scope, flags := chapterPlanScopeCheck(plan, "他在危急关头竟然提前觉醒神力，一掌拍碎巨石。")
	if scope["goal"] != "抵达北境" {
		t.Fatalf("scope 应含 goal: %v", scope)
	}
	if len(flags) == 0 {
		t.Fatalf("正文触犯 forbidden_move 应被 flag: %v", flags)
	}
	// 未触犯时无 flag
	_, flags2 := chapterPlanScopeCheck(plan, "他一路北行，风雪渐大。")
	if len(flags2) != 0 {
		t.Fatalf("未触犯不应 flag: %v", flags2)
	}
}

func TestChapterPlanScopeCheckFlagsTitleMismatch(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "失业饭桌",
	}

	_, flags := chapterPlanScopeCheck(plan, "第一章 欠费单\n\n林澈放下筷子。")
	if len(flags) == 0 {
		t.Fatal("正文标题偏离 plan.title 应被 flag")
	}
	if !strings.Contains(flags[0], "正文标题与计划标题不一致") {
		t.Fatalf("flag 措辞不对: %v", flags)
	}

	_, flags = chapterPlanScopeCheck(plan, "# 第一章 失业饭桌\n\n林澈放下筷子。")
	if len(flags) != 0 {
		t.Fatalf("等价标题不应 flag: %v", flags)
	}
}
