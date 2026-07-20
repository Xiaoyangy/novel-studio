package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestInitialWorldTickQualityIssuesEnforcesCharacterFirstVisibility(t *testing.T) {
	tests := []struct {
		name              string
		actor             string
		visibilityChapter int
		wantIssue         string
	}{
		{
			name:              "delayed character cannot surface early",
			actor:             "沈知遥",
			visibilityChapter: 1,
			wantIssue:         "早于大纲首次可见第7章",
		},
		{
			name:              "delayed character may surface on planned chapter",
			actor:             "沈知遥",
			visibilityChapter: 7,
		},
		{
			name:              "unplanned character cannot surface",
			actor:             "周顾问",
			visibilityChapter: 4,
			wantIssue:         "尚未安排其首次可见章节",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatalf("init store: %v", err)
			}
			if err := st.Characters.Save([]domain.Character{
				{Name: "林澈", Role: "主角"},
				{Name: "沈知遥", Role: "女主"},
				{Name: "周顾问", Role: "配角"},
			}); err != nil {
				t.Fatalf("save characters: %v", err)
			}
			if err := st.Outline.SaveOutline([]domain.OutlineEntry{
				{Chapter: 1, Title: "返乡", CoreEvent: "林澈回到青山县；后续沈知遥入场复核项目"},
				{Chapter: 7, Title: "现场重逢", CoreEvent: "沈知遥进入项目现场"},
			}); err != nil {
				t.Fatalf("save outline: %v", err)
			}
			if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
				TickID:            "v1-a1",
				Chapter:           0,
				Actors:            []string{tc.actor},
				Summary:           tc.actor + "推进手头事务",
				Consequence:       "消息将在约定章节进入主角视野",
				VisibilityChapter: tc.visibilityChapter,
			}}); err != nil {
				t.Fatalf("append world event: %v", err)
			}
			if err := st.WorldSim.SaveTick(domain.WorldTick{
				TickID:         "v1-a1",
				Volume:         1,
				Arc:            1,
				ThroughChapter: 0,
				EventCount:     1,
			}); err != nil {
				t.Fatalf("save tick: %v", err)
			}

			issues := InitialWorldTickQualityIssues(st)
			joined := strings.Join(issues, "；")
			if tc.wantIssue == "" {
				if len(issues) != 0 {
					t.Fatalf("planned visibility should pass, got: %s", joined)
				}
				return
			}
			if !strings.Contains(joined, tc.wantIssue) {
				t.Fatalf("issues=%q, want substring %q", joined, tc.wantIssue)
			}
		})
	}
}

func TestInitialWorldTickQualityIssuesUsesLayeredOutlineOverStaleFlat(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角"},
		{Name: "沈知遥", Role: "女主"},
	}); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	// The flat outline is an intentionally stale compatibility artifact. If the
	// gate consumed it, it would incorrectly allow 沈知遥 to surface in ch1.
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 1, Title: "旧扁平口径", CoreEvent: "沈知遥在第一章出现",
	}}); err != nil {
		t.Fatalf("save stale flat outline: %v", err)
	}
	chapters := make([]domain.OutlineEntry, 7)
	for i := range chapters {
		chapters[i] = domain.OutlineEntry{Title: "章节", CoreEvent: "林澈推进项目"}
	}
	chapters[6].CoreEvent = "沈知遥进入项目现场"
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs:  []domain.ArcOutline{{Index: 1, Chapters: chapters}},
	}}); err != nil {
		t.Fatalf("save layered outline: %v", err)
	}
	saveInitialWorldTickGateFixture(t, st, domain.WorldEvent{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"沈知遥"},
		Summary:           "沈知遥推进手头事务",
		VisibilityChapter: 1,
	}, domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1})

	joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
	if !strings.Contains(joined, "早于大纲首次可见第7章") {
		t.Fatalf("layered outline must win over stale flat outline, issues=%q", joined)
	}
}

func TestInitialWorldTickQualityIssuesScansEveryVisibleEventTextField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.WorldEvent)
	}{
		{name: "summary", mutate: func(event *domain.WorldEvent) { event.Summary = "沈知遥已开始复核" }},
		{name: "consequence", mutate: func(event *domain.WorldEvent) { event.Consequence = "材料将交给沈知遥" }},
		{name: "location", mutate: func(event *domain.WorldEvent) { event.Location = "沈知遥办公室" }},
		{name: "visibility path", mutate: func(event *domain.WorldEvent) { event.VisibilityPath = "经沈知遥转交" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newWorldTickBoundaryStore(t)
			event := domain.WorldEvent{
				TickID:            "v1-a1",
				Chapter:           0,
				Actors:            []string{"林澈"},
				Summary:           "县城项目发生变化",
				VisibilityChapter: 1,
			}
			tc.mutate(&event)
			saveInitialWorldTickGateFixture(t, st, event, domain.WorldTick{
				TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1,
			})
			joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
			if !strings.Contains(joined, "早于大纲首次可见第7章") {
				t.Fatalf("field %s escaped first-visibility scan: %q", tc.name, joined)
			}
		})
	}
}

func TestInitialWorldTickQualityIssuesFailsClosedForUnknownActor(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	saveInitialWorldTickGateFixture(t, st, domain.WorldEvent{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"凭空出现的人"},
		Summary:           "凭空出现的人推动事件",
		VisibilityChapter: 1,
	}, domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1})

	joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
	if !strings.Contains(joined, `actor "凭空出现的人" 不在角色册/势力册/别名中`) {
		t.Fatalf("empty actor registries must fail closed, issues=%q", joined)
	}
}

func TestInitialWorldTickQualityIssuesRejectsTickLedgerDrift(t *testing.T) {
	tests := []struct {
		name      string
		event     domain.WorldEvent
		tick      domain.WorldTick
		wantIssue string
	}{
		{
			name:      "through chapter must remain zero",
			event:     domain.WorldEvent{TickID: "v1-a1", Chapter: 0, Actors: []string{"林澈"}, Summary: "开篇前事件", VisibilityChapter: 1},
			tick:      domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 1, EventCount: 1},
			wantIssue: "初始推演必须严格停在第0章",
		},
		{
			name:      "event count must equal current tick events",
			event:     domain.WorldEvent{TickID: "v1-a1", Chapter: 0, Actors: []string{"林澈"}, Summary: "开篇前事件", VisibilityChapter: 1},
			tick:      domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 2},
			wantIssue: "属于当前 tick \"v1-a1\" 的事件为 1 条",
		},
		{
			name:      "event tick must match cursor",
			event:     domain.WorldEvent{TickID: "v1-a2", Chapter: 0, Actors: []string{"林澈"}, Summary: "错误弧事件", VisibilityChapter: 1},
			tick:      domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1},
			wantIssue: "与当前 tick \"v1-a1\" 不一致",
		},
		{
			name:      "initial tick requires chapter zero event",
			event:     domain.WorldEvent{TickID: "v1-a1", Chapter: 1, Actors: []string{"林澈"}, Summary: "正文开始后的事件", VisibilityChapter: 1},
			tick:      domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1},
			wantIssue: "至少需要一条 chapter=0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newWorldTickBoundaryStore(t)
			saveInitialWorldTickGateFixture(t, st, tc.event, tc.tick)
			joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
			if !strings.Contains(joined, tc.wantIssue) {
				t.Fatalf("issues=%q, want %q", joined, tc.wantIssue)
			}
		})
	}
}

func TestInitialWorldTickQualityIssuesBindsGenerationAndRejectsWarnings(t *testing.T) {
	st := newWorldTickBoundaryStore(t)
	const generationID = "simulation-opening-v2"
	if err := st.Progress.ResetForSimulationRestart("测试书", 7, generationID); err != nil {
		t.Fatalf("reset progress generation: %v", err)
	}
	if err := st.SaveSimulationRestartPolicy(domain.SimulationRestartPolicy{
		Version:      1,
		Active:       true,
		Mode:         "simulation_restart_from_seed",
		GenerationID: generationID,
		GeneratedAt:  time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("save restart policy: %v", err)
	}
	event := domain.WorldEvent{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"林澈"},
		Summary:           "开篇前项目发生变化",
		VisibilityChapter: 1,
	}
	saveInitialWorldTickGateFixture(t, st, event, domain.WorldTick{
		TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1, GenerationID: generationID,
	})
	if joined := strings.Join(InitialWorldTickQualityIssues(st), "；"); strings.Contains(joined, "generation") || strings.Contains(joined, "warning") {
		t.Fatalf("exact generation with no warnings should pass generation checks: %q", joined)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{
		TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1,
		GenerationID: "stale-generation",
		Warnings:     []string{"未知 actor 曾被降级保存"},
	}); err != nil {
		t.Fatalf("save contaminated tick: %v", err)
	}
	joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
	for _, want := range []string{
		`world_tick.generation_id="stale-generation" 与活动 generation "simulation-opening-v2" 不一致`,
		"initial world_tick 留有未解决 warning: 未知 actor 曾被降级保存",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues=%q, want %q", joined, want)
		}
	}
}

func TestInitialWorldTickQualityIssuesDerivesAndScansProjectForbiddenTopics(t *testing.T) {
	st := newWorldTickBoundaryStore(t)
	if err := st.Outline.SavePremise("# 现实县城经营\n\n不写诡异、恐怖、末世、克系、邪神、收容或灵异。\n"); err != nil {
		t.Fatalf("save premise: %v", err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{
		Version: rules.SnapshotVersion,
		Status:  rules.StatusReady,
		Structured: rules.Structured{
			ForbiddenPhrases: []string{"不知为何"},
		},
		Preferences: "世界信息流不引入古代、官署、黑市或导师等偏离题材的元素。",
	}); err != nil {
		t.Fatalf("save user rules: %v", err)
	}
	if err := st.WorldSim.SaveAgendaLedger(domain.OffscreenAgendaLedger{Agendas: []domain.CharacterAgenda{{
		Name: "林澈", CurrentGoal: "调查灵异传闻", Status: "active",
	}}}); err != nil {
		t.Fatalf("save agenda: %v", err)
	}
	if err := st.Methodology.SaveSocialMood(domain.SocialMood{
		Mood: "居民观望", Intensity: 0.4,
		Rumors: []domain.Rumor{{Text: "旧仓出现恐怖怪谈", Credibility: 0.2, SpreadRate: 0.3}},
	}); err != nil {
		t.Fatalf("save social mood: %v", err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{Version: 1, Factions: []domain.WorldFaction{{
		ID: "merchant", Name: "商户联盟", Clock: &domain.FactionClock{
			Segments: 4, Progress: 1, Consequence: "黑市交易网络成形", Pace: "每弧一段",
		},
	}}}); err != nil {
		t.Fatalf("save book world: %v", err)
	}
	saveInitialWorldTickGateFixture(t, st, domain.WorldEvent{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"林澈"},
		Summary:           "县城项目收到新消息",
		Consequence:       "不知为何材料被退回",
		VisibilityChapter: 1,
		VisibilityPath:    "经灵异论坛扩散",
	}, domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1})

	joined := strings.Join(InitialWorldTickQualityIssues(st), "；")
	for _, want := range []string{
		"world_event[we-000001].visibility_path", `明确禁题材/禁语 "灵异"`,
		"offscreen_agenda[0:林澈].current_goal", `明确禁题材/禁语 "灵异"`,
		"social_mood.rumors[0].text", `明确禁题材/禁语 "恐怖"`,
		"book_world.factions[0:商户联盟].clock.consequence", `明确禁题材/禁语 "黑市"`,
		"world_event[we-000001].consequence", `明确禁题材/禁语 "不知为何"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("forbidden-topic scan missing %q: %s", want, joined)
		}
	}
}

func TestWorldTickForbiddenTopicsRequireExplicitProjectNegation(t *testing.T) {
	topics := worldTickExtractExplicitNegativeTopics(
		"本书是恐怖小说，主角调查灵异案件；经营信息用日常语言呈现，不写沉重现实主义或行业教程式表达。",
	)
	joined := strings.Join(topics, "|")
	if strings.Contains(joined, "恐怖") || strings.Contains(joined, "灵异") {
		t.Fatalf("positive genre description must not become a forbidden topic: %v", topics)
	}
	for _, want := range []string{"沉重现实主义", "行业教程"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("explicit negative topic %q not derived: %v", want, topics)
		}
	}
}

func newWorldTickBoundaryStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角"},
		{Name: "沈知遥", Role: "女主"},
	}); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	outline := make([]domain.OutlineEntry, 7)
	for i := range outline {
		outline[i] = domain.OutlineEntry{Chapter: i + 1, Title: "章节", CoreEvent: "林澈推进项目"}
	}
	outline[6].CoreEvent = "沈知遥进入项目现场"
	if err := st.Outline.SaveOutline(outline); err != nil {
		t.Fatalf("save outline: %v", err)
	}
	return st
}

func saveInitialWorldTickGateFixture(t *testing.T, st *store.Store, event domain.WorldEvent, tick domain.WorldTick) {
	t.Helper()
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{event}); err != nil {
		t.Fatalf("append world event: %v", err)
	}
	if err := st.WorldSim.SaveTick(tick); err != nil {
		t.Fatalf("save tick: %v", err)
	}
}
