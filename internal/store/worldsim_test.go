package store

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestWorldSimStoreEventsRoundtrip(t *testing.T) {
	s := NewStore(t.TempDir())

	// 缺文件返回空
	if events, err := s.WorldSim.LoadWorldEvents(); err != nil || len(events) != 0 {
		t.Fatalf("缺文件应返回空: %v %v", events, err)
	}

	saved, err := s.WorldSim.AppendWorldEvents([]domain.WorldEvent{
		{Chapter: 10, Actors: []string{"沈青"}, Summary: "沈青抵北境", VisibilityChapter: 13},
		{Chapter: 11, Actors: []string{"朝廷"}, Summary: "盐税加征", VisibilityChapter: 11},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if saved[0].ID != "we-000001" || saved[1].ID != "we-000002" {
		t.Fatalf("自动编号错误: %+v", saved)
	}
	// 二次追加接续编号
	more, err := s.WorldSim.AppendWorldEvents([]domain.WorldEvent{
		{Chapter: 12, Actors: []string{"老周"}, Summary: "老周收到密信", VisibilityChapter: 20},
	})
	if err != nil || more[0].ID != "we-000003" {
		t.Fatalf("接续编号错误: %+v %v", more, err)
	}

	all, err := s.WorldSim.LoadWorldEvents()
	if err != nil || len(all) != 3 {
		t.Fatalf("往返数量错误: %d %v", len(all), err)
	}

	// 非法事件整体拒绝
	if _, err := s.WorldSim.AppendWorldEvents([]domain.WorldEvent{{Chapter: 1, Summary: "缺 actors", VisibilityChapter: 1}}); err == nil {
		t.Fatal("非法事件应被拒绝")
	}
}

func TestWorldSimStoreHorizonEvents(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.WorldSim.AppendWorldEvents([]domain.WorldEvent{
		{Chapter: 5, Actors: []string{"a"}, Summary: "早已浮出且过期", VisibilityChapter: 5},
		{Chapter: 10, Actors: []string{"b"}, Summary: "窗口内", VisibilityChapter: 14},
		{Chapter: 10, Actors: []string{"c"}, Summary: "还没浮出", VisibilityChapter: 30},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.WorldSim.HorizonEvents(15, 5)
	if err != nil {
		t.Fatalf("horizon: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "窗口内" {
		t.Fatalf("窗口过滤错误（chapter=15, window=5）: %+v", got)
	}
}

func TestWorldSimStoreTickAgendaCast(t *testing.T) {
	s := NewStore(t.TempDir())
	if tick, err := s.WorldSim.LoadTick(); err != nil || tick != nil {
		t.Fatalf("缺游标应返回 nil,nil: %v %v", tick, err)
	}
	if err := s.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a2", Volume: 1, Arc: 2, ThroughChapter: 24, EventCount: 5}); err != nil {
		t.Fatalf("save tick: %v", err)
	}
	tick, err := s.WorldSim.LoadTick()
	if err != nil || tick == nil || tick.ThroughChapter != 24 || tick.UpdatedAt == "" {
		t.Fatalf("游标往返错误: %+v %v", tick, err)
	}

	ledger := domain.OffscreenAgendaLedger{}.Upsert(domain.CharacterAgenda{Name: "沈青", CurrentGoal: "查商队", Status: "active", LastAdvancedChapter: 24})
	if err := s.WorldSim.SaveAgendaLedger(ledger); err != nil {
		t.Fatalf("save agenda: %v", err)
	}
	back, err := s.WorldSim.LoadAgendaLedger()
	if err != nil || len(back.Agendas) != 1 || back.Agendas[0].Name != "沈青" {
		t.Fatalf("日程往返错误: %+v %v", back, err)
	}
	// 非法日程拒绝保存
	if err := s.WorldSim.SaveAgendaLedger(domain.OffscreenAgendaLedger{Agendas: []domain.CharacterAgenda{{Name: "x"}}}); err == nil {
		t.Fatal("非法日程应被拒绝")
	}

	cast := domain.SimulationCast{}.Upsert(domain.TierAssignment{Name: "老周", Tier: domain.TierSupporting})
	if err := s.WorldSim.SaveSimulationCast(cast); err != nil {
		t.Fatalf("save cast: %v", err)
	}
	castBack, err := s.WorldSim.LoadSimulationCast()
	if err != nil || len(castBack.Assignments) != 1 {
		t.Fatalf("名单往返错误: %+v %v", castBack, err)
	}
}
