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
