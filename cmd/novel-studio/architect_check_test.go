package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestArchitectFactionClockIssuesRequireClocks(t *testing.T) {
	world := &domain.BookWorld{Factions: []domain.WorldFaction{
		{ID: "team", Name: "无钟势力", Goal: "推进目标"},
		{ID: "bad", Name: "坏钟势力", Goal: "推进目标", Clock: &domain.FactionClock{Segments: 4, Progress: 5}},
		{ID: "ok", Name: "有钟势力", Goal: "推进目标", Clock: &domain.FactionClock{Segments: 6, Progress: 1, Consequence: "阶段后果", Pace: "每弧 1 段"}},
	}}

	issues := architectFactionClockIssues(world)
	joined := strings.Join(issues, "\n")
	for _, want := range []string{"缺少势力进度钟", "progress 不能大于 segments", "consequence 不能为空"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected issue %q in %v", want, issues)
		}
	}
}

func TestArchitectReadinessRejectsDanglingFactionRelations(t *testing.T) {
	dir := t.TempDir()
	writeArchitectCheckFile(t, dir, "brainstorm.md", "重启脑爆")
	writeArchitectCheckFile(t, dir, "premise.md", "约60-75章")
	writeArchitectCheckFile(t, dir, "characters.json", `[
  {"name":"许闻溪","role":"主角","tier":"core"},
  {"name":"梁渡","role":"男主","tier":"core"},
  {"name":"程棠","role":"同事","tier":"important"},
  {"name":"乔安","role":"HRBP","tier":"important"},
  {"name":"夏岚","role":"上级","tier":"important"}
]`)
	writeArchitectCheckFile(t, dir, "world_rules.json", `[
  {"category":"职业边界","rule":"AI提效不能吞掉人的处境判断","boundary":"正文必须用具体工作压力呈现"},
  {"category":"情感线","rule":"慢热互信","boundary":"男主不兜底"},
  {"category":"成长线","rule":"女主必须从被评价走向定规则","boundary":"每阶段有代价"}
]`)
	writeArchitectCheckFile(t, dir, "world_codex.json", `{
  "version":1,
  "immutability_policy":"现实职场世界，不引入硬科幻规则。",
  "sections":[
    {"key":"technology","content":"AI进入普通办公流程。"},
    {"key":"social_order","content":"组织用效率叙事推动岗位合并。"},
    {"key":"daily_life","content":"通勤、会议、家庭电话构成生活压力。"}
  ]
}`)
	writeArchitectCheckFile(t, dir, "outline.json", `[
  {"chapter":1,"title":"发布会","core_event":"溪流助手复述许闻溪的复盘并展示岗位合并建议","hook":"许闻溪拒绝签确认栏","scenes":["发布会后台","会后确认"]}
]`)
	writeArchitectCheckFile(t, dir, "layered_outline.json", `[{
  "index":1,
  "title":"第一卷",
  "stage_goal":"许闻溪看见自己被替代",
  "arcs":[{"index":1,"title":"第一弧","goal":"拒绝被动确认","chapters":[{"chapter":1,"title":"发布会","core_event":"溪流助手复述许闻溪的复盘并展示岗位合并建议","hook":"许闻溪拒绝签确认栏"}]}]
}]`)
	writeArchitectCheckFile(t, dir, "book_world.json", `{
  "version":1,
  "places":[{"id":"hq","name":"澄光生活总部"}],
  "routes":[{"from":"hq","to":"bridgepoint","description":"地铁四站"}],
  "factions":[
    {"id":"operations_center","name":"运营中心","goal":"维持现场结果","relations":[{"target":"store_ops","kind":"frontline_partner"}],"clock":{"segments":6,"progress":1,"consequence":"完成一次人员缩编"}}
  ]
}`)
	writeArchitectCheckFile(t, dir, "meta/compass.json", `{"ending_direction":"许闻溪成为能定规则的人","open_threads":["第二算法方法论"]}`)

	readiness := assessArchitectReadiness(dir)
	if readiness.Ready {
		t.Fatalf("dangling relation should fail readiness: %+v", readiness)
	}
	if got := strings.Join(readiness.Issues, "\n"); !strings.Contains(got, "store_ops") {
		t.Fatalf("expected dangling target issue, got %v", readiness.Issues)
	}
}

func TestArchitectReadinessUsesLayeredOutlineAsFreshnessAuthority(t *testing.T) {
	dir := t.TempDir()
	writeArchitectCheckFile(t, dir, "layered_outline.json", `[{"index":1,"arcs":[]}]`)
	writeArchitectCheckFile(t, dir, "outline.json", `[{"chapter":1,"title":"derived"}]`)
	generatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	before := generatedAt.Add(-10 * time.Second)
	if err := os.Chtimes(
		filepath.Join(dir, "layered_outline.json"),
		before,
		before,
	); err != nil {
		t.Fatal(err)
	}
	if err := writeArchitectReadiness(dir, architectReadiness{
		Ready:         true,
		SchemaVersion: architectReadinessSchemaVersion,
		GeneratedAt:   generatedAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	after := generatedAt.Add(10 * time.Second)
	if err := os.Chtimes(
		filepath.Join(dir, "outline.json"),
		after,
		after,
	); err != nil {
		t.Fatal(err)
	}
	if ok, reason := architectReadinessState(dir); !ok {
		t.Fatalf("derived flat outline invalidated layered readiness: %s", reason)
	}

	if err := os.Chtimes(
		filepath.Join(dir, "layered_outline.json"),
		after,
		after,
	); err != nil {
		t.Fatal(err)
	}
	if ok, _ := architectReadinessState(dir); ok {
		t.Fatal("authored layered outline change did not invalidate readiness")
	}
}

func writeArchitectCheckFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
