package domain

import "testing"

func TestWorldEventValidate(t *testing.T) {
	ok := WorldEvent{Chapter: 10, Actors: []string{"沈青"}, Summary: "沈青抵达北境", VisibilityChapter: 13}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法事件不应报错: %v", err)
	}
	for _, bad := range []WorldEvent{
		{Chapter: 10, Actors: []string{"x"}, VisibilityChapter: 10},              // 缺 summary
		{Chapter: 10, Summary: "s", VisibilityChapter: 10},                       // 缺 actors
		{Chapter: 0, Actors: []string{"x"}, Summary: "s", VisibilityChapter: 1},  // chapter<=0
		{Chapter: 10, Actors: []string{"x"}, Summary: "s", VisibilityChapter: 8}, // 信息早于事件
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("非法事件应报错: %+v", bad)
		}
	}
}

func TestNominateForeshadowCandidates(t *testing.T) {
	events := []WorldEvent{
		{Summary: "已浮出的近事", Chapter: 5, VisibilityChapter: 6},                          // 不提名
		{Summary: "显式标记", Chapter: 5, VisibilityChapter: 6, ForeshadowCandidate: true}, // 提名
		{Summary: "远期事件", Chapter: 5, VisibilityChapter: 30},                           // 提名（current=10, lead=3）
	}
	got := NominateForeshadowCandidates(events, 10, 3)
	if len(got) != 2 {
		t.Fatalf("期望提名 2 条，实际 %d: %+v", len(got), got)
	}
}

func TestCharacterAgendaValidateUpsertStale(t *testing.T) {
	ok := CharacterAgenda{Name: "沈青", CurrentGoal: "查清商队失踪", Status: "active"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法日程不应报错: %v", err)
	}
	if err := (CharacterAgenda{Name: "x", CurrentGoal: "y", Status: "procrastinating"}).Validate(); err == nil {
		t.Fatal("非法状态应报错")
	}
	if err := (CharacterAgenda{Name: "x"}).Validate(); err == nil {
		t.Fatal("缺 goal 应报错")
	}

	var l OffscreenAgendaLedger
	l = l.Upsert(CharacterAgenda{Name: "沈青", CurrentGoal: "A", LastAdvancedChapter: 2})
	l = l.Upsert(CharacterAgenda{Name: "老周", CurrentGoal: "B", LastAdvancedChapter: 18})
	l = l.Upsert(CharacterAgenda{Name: "沈青", CurrentGoal: "A2", LastAdvancedChapter: 5})
	if len(l.Agendas) != 2 || l.Agendas[0].CurrentGoal != "A2" {
		t.Fatalf("upsert 应按 name 覆盖: %+v", l.Agendas)
	}

	stale := l.Stale(20, 10)
	if len(stale) != 1 || stale[0].Name != "沈青" {
		t.Fatalf("停滞检测错误: %+v", stale)
	}
	// dormant 不算停滞
	l = l.Upsert(CharacterAgenda{Name: "沈青", CurrentGoal: "A2", Status: "dormant", LastAdvancedChapter: 5})
	if len(l.Stale(20, 10)) != 0 {
		t.Fatal("dormant 日程不应计入停滞")
	}
}

func TestSuggestSimulationTier(t *testing.T) {
	cases := []struct {
		char   Character
		recent int
		want   SimulationTier
	}{
		{Character{Tier: "core"}, 0, TierProtagonistCircle},
		{Character{Tier: "important"}, 0, TierSupporting},
		{Character{}, 0, TierSupporting}, // 未标注默认 important
		{Character{Tier: "secondary"}, 0, TierBackground},
		{Character{Tier: "secondary"}, 3, TierSupporting}, // 高频上调一层
		{Character{Tier: "important"}, 4, TierProtagonistCircle},
		{Character{Tier: "core"}, 5, TierProtagonistCircle}, // 已到顶
	}
	for _, c := range cases {
		if got := SuggestSimulationTier(c.char, c.recent); got != c.want {
			t.Fatalf("tier(%q, recent=%d) 期望 %s 实际 %s", c.char.Tier, c.recent, c.want, got)
		}
	}

	var cast SimulationCast
	cast = cast.Upsert(TierAssignment{Name: "沈青", Tier: TierSupporting})
	cast = cast.Upsert(TierAssignment{Name: "沈青", Tier: TierProtagonistCircle, Reason: "升格"})
	if tier, ok := cast.TierOf("沈青"); !ok || tier != TierProtagonistCircle {
		t.Fatalf("名单 upsert/查询错误: %v %v", tier, ok)
	}
	if _, ok := cast.TierOf("不存在"); ok {
		t.Fatal("未指派角色不应命中")
	}
}
