package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newWorldTickTool(t *testing.T) (*SaveWorldTickTool, *store.Store) {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Progress.Init("测试书", 100); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "沈青", Role: "配角"}, {Name: "林昭", Role: "主角"}}); err != nil {
		t.Fatalf("save chars: %v", err)
	}
	return NewSaveWorldTickTool(s), s
}

func execWorldTick(t *testing.T, tool *SaveWorldTickTool, args string) map[string]any {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	return result
}

func TestSaveWorldTickHappyPath(t *testing.T) {
	tool, s := newWorldTickTool(t)
	result := execWorldTick(t, tool, `{
		"volume": 1, "arc": 2, "through_chapter": 24,
		"events": [
			{"chapter": 20, "actors": ["沈青"], "summary": "沈青查到商队线索", "visibility_chapter": 26, "visibility_path": "信使", "foreshadow_candidate": true, "tier": "supporting"}
		],
		"agenda_updates": [
			{"name": "沈青", "current_goal": "追查商队失踪", "status": "active", "steps": [{"description": "北上取证", "done": true}, {"description": "对质盐商"}]}
		],
		"social_mood": {"mood": "边境不安", "intensity": 0.6, "rumors": [{"text": "北境要打仗了", "credibility": 0.3, "spread_rate": 0.8}]}
	}`)
	if result["saved"] != true || result["tick_id"] != "v1-a2" || result["saved_events"] != float64(1) {
		t.Fatalf("返回事实错误: %v", result)
	}
	if _, hasWarn := result["warnings"]; hasWarn {
		t.Fatalf("合法输入不应有警告: %v", result["warnings"])
	}

	// 事实层落盘验证
	tick, _ := s.WorldSim.LoadTick()
	if tick == nil || tick.ThroughChapter != 24 {
		t.Fatalf("游标未落盘: %+v", tick)
	}
	events, _ := s.WorldSim.LoadWorldEvents()
	if len(events) != 1 || events[0].TickID != "v1-a2" || events[0].ID == "" {
		t.Fatalf("事件未正确落盘: %+v", events)
	}
	ledger, _ := s.WorldSim.LoadAgendaLedger()
	if len(ledger.Agendas) != 1 || ledger.Agendas[0].LastAdvancedChapter != 24 {
		t.Fatalf("日程未落盘或未自动填推进章: %+v", ledger.Agendas)
	}
	mood, _ := s.Methodology.LoadSocialMood()
	if mood == nil || mood.Chapter != 24 {
		t.Fatalf("社会情绪未落盘: %+v", mood)
	}
}

func TestSaveWorldTickGuards(t *testing.T) {
	tool, _ := newWorldTickTool(t)
	// 先落一次游标到 24
	execWorldTick(t, tool, `{"volume":1,"arc":2,"through_chapter":24}`)

	result := execWorldTick(t, tool, `{
		"volume": 1, "arc": 3, "through_chapter": 12,
		"events": [
			{"chapter": 10, "actors": ["不在册的人"], "summary": "神秘人出没", "visibility_chapter": 8}
		]
	}`)
	warnings, ok := result["warnings"].([]any)
	if !ok || len(warnings) < 3 {
		t.Fatalf("应有三条警告（倒退/未知actor/可见性修正）: %v", result["warnings"])
	}
	joined := ""
	for _, w := range warnings {
		joined += w.(string) + "|"
	}
	for _, want := range []string{"倒退", "不在角色册", "自动修正"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("警告缺少 %q: %s", want, joined)
		}
	}
	// 可见性被修正为不早于发生章
	if events := mustLoadEvents(t, tool); events[len(events)-1].VisibilityChapter != 10 {
		t.Fatalf("可见性未修正: %+v", events[len(events)-1])
	}
}

func mustLoadEvents(t *testing.T, tool *SaveWorldTickTool) []domain.WorldEvent {
	t.Helper()
	events, err := tool.store.WorldSim.LoadWorldEvents()
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	return events
}

func TestSaveWorldTickRejectsBadArgs(t *testing.T) {
	tool, _ := newWorldTickTool(t)
	// 负 through_chapter 非法。
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"volume":1,"through_chapter":-1}`)); err == nil {
		t.Fatal("负 through_chapter 应报错")
	}
	// through_chapter=0（缺省）是合法的开局前初始 tick。
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"volume":1}`)); err != nil {
		t.Fatalf("through_chapter=0 初始 tick 应允许: %v", err)
	}
}

func TestHorizonEventsInjection(t *testing.T) {
	tool, s := newWorldTickTool(t)
	execWorldTick(t, tool, `{
		"volume":1, "arc":1, "through_chapter": 10,
		"events": [
			{"chapter": 8, "actors": ["沈青"], "summary": "已浮出", "visibility_chapter": 11, "visibility_path": "谣言"},
			{"chapter": 9, "actors": ["沈青"], "summary": "远未浮出", "visibility_chapter": 40}
		]
	}`)

	ctxTool := NewContextTool(s, References{}, "default")
	working := map[string]any{}
	ctxTool.buildHorizonEvents(working, 12, func(string, error) {})
	events, ok := working["horizon_events"].([]domain.WorldEvent)
	if !ok || len(events) != 1 || events[0].Summary != "已浮出" {
		t.Fatalf("地平线注入错误: %v", working["horizon_events"])
	}
	if _, ok := working["horizon_events_usage"]; !ok {
		t.Fatal("缺使用指引")
	}

	// 规划路径注入
	planning := map[string]any{}
	ctxTool.buildWorldSimulationPlanning(planning, func(string, error) {})
	sim, ok := planning["world_simulation"].(map[string]any)
	if !ok {
		t.Fatalf("world_simulation 未注入: %v", planning)
	}
	if sim["tick"] == nil {
		t.Fatal("tick 游标缺失")
	}
	if _, ok := sim["foreshadow_candidates_from_world"]; !ok {
		t.Fatal("远期事件应被提名为伏笔素材")
	}

	// 老项目（无工件）零注入
	empty := NewContextTool(store.NewStore(t.TempDir()), References{}, "default")
	w2, p2 := map[string]any{}, map[string]any{}
	empty.buildHorizonEvents(w2, 5, func(string, error) {})
	empty.buildWorldSimulationPlanning(p2, func(string, error) {})
	if len(w2) != 0 || len(p2) != 0 {
		t.Fatalf("无工件时应零注入: %v %v", w2, p2)
	}
}

func TestSaveWorldTickFactionClocks(t *testing.T) {
	tool, s := newWorldTickTool(t)
	world := domain.BookWorld{Version: 1, Factions: []domain.WorldFaction{
		{ID: "salt", Name: "盐帮", Goal: "垄断火器", Clock: &domain.FactionClock{Segments: 4, Progress: 3, Consequence: "火器入城"}},
		{ID: "court", Name: "朝廷"}, // 无钟
	}}
	if err := s.World.SaveBookWorld(world); err != nil {
		t.Fatalf("save world: %v", err)
	}
	result := execWorldTick(t, tool, `{
		"volume":1, "arc":2, "through_chapter": 24,
		"faction_clock_updates": [
			{"target": "盐帮", "ticks": 2, "note": "主角忙于北境"},
			{"target": "朝廷", "ticks": 1},
			{"target": "不存在的势力", "ticks": 1}
		]
	}`)
	completed, ok := result["clocks_completed"].([]any)
	if !ok || len(completed) != 1 || !strings.Contains(completed[0].(string), "火器入城") {
		t.Fatalf("盐帮钟应走满并透出后果: %v", result["clocks_completed"])
	}
	warnings, _ := result["warnings"].([]any)
	joined := ""
	for _, w := range warnings {
		joined += w.(string) + "|"
	}
	if !strings.Contains(joined, "未设进度钟") || !strings.Contains(joined, "不在 book_world") {
		t.Fatalf("应有无钟与未知势力警告: %s", joined)
	}
	back, _ := s.World.LoadBookWorld()
	if back.Factions[0].Clock.Progress != 4 {
		t.Fatalf("钟进度应持久化为 4，实际 %d", back.Factions[0].Clock.Progress)
	}
}

func TestStoryCalendarRoundtripAndInjection(t *testing.T) {
	tool, s := newWorldTickTool(t)
	_ = tool
	if err := s.WorldSim.SaveStoryCalendar(domain.StoryCalendar{Era: "架空", DaysPerChapter: 2}); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	ctxTool := NewContextTool(s, References{}, "default")
	working := map[string]any{}
	ctxTool.buildMethodologyContext(working)
	if _, ok := working["story_calendar"]; !ok {
		t.Fatalf("story_calendar 未注入: %v", working)
	}
}

func TestSaveWorldTickStoryDayAnchoring(t *testing.T) {
	tool, s := newWorldTickTool(t)
	if err := s.WorldSim.SaveStoryCalendar(domain.StoryCalendar{DaysPerChapter: 2}); err != nil {
		t.Fatalf("save calendar: %v", err)
	}
	execWorldTick(t, tool, `{
		"volume":1, "arc":1, "through_chapter": 5,
		"events":[{"chapter":4,"actors":["势力X"],"summary":"离屏推进","visibility_chapter":6}]
	}`)
	events, err := s.WorldSim.LoadWorldEvents()
	if err != nil || len(events) != 1 {
		t.Fatalf("load events: %v n=%d", err, len(events))
	}
	// chapter=4 × 2 天/章 = 8 天，回填到 story_day。
	if events[0].StoryDay != 8 {
		t.Fatalf("story_day 应按 days_per_chapter 回填为 8，got %v", events[0].StoryDay)
	}
}
