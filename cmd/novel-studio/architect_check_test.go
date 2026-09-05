package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestArchitectChapterRangeFromPremiseIgnoresBeatRanges(t *testing.T) {
	tests := []struct {
		premise string
		min     int
		max     int
		ok      bool
	}{
		{premise: "正文严格控制在三万字，建议十二章；第 4—5 章前兑现七份外卖", min: 12, max: 12, ok: true},
		{premise: "第4-5章前完成定位，共12章", min: 12, max: 12, ok: true},
		{premise: "约60-75章", min: 60, max: 75, ok: true},
		{premise: "第4-5章前完成定位", ok: false},
	}
	for _, tt := range tests {
		min, max, ok := architectChapterRangeFromPremise(tt.premise)
		if min != tt.min || max != tt.max || ok != tt.ok {
			t.Fatalf("architectChapterRangeFromPremise(%q) = %d,%d,%v; want %d,%d,%v", tt.premise, min, max, ok, tt.min, tt.max, tt.ok)
		}
	}
}

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
	dir := seedZeroInitProject(t)
	writeArchitectCheckFile(t, dir, "layered_outline.json", `[{
  "index": 1,
  "title": "鬼城账本",
  "stage_goal": "江烬确认红账不是普通欠费",
  "arcs": [{
    "index": 1,
    "title": "午夜欠费",
    "goal": "核验鬼城的第一条债务规则",
    "chapters": [{
      "chapter": 1,
      "title": "午夜欠费单",
      "core_event": "江烬收到鬼城入住欠费单，被迫核验第一条规则。",
      "hook": "欠费单上的妹妹姓名多出一笔红账。"
    }]
  }]
}]`)
	writeArchitectCheckFile(t, dir, "outline.json", `[{
  "chapter": 1,
  "title": "午夜欠费单",
  "core_event": "江烬收到鬼城入住欠费单，被迫核验第一条规则。",
  "hook": "欠费单上的妹妹姓名多出一笔红账。"
}]`)
	// Place the receipt just after the fixture writes so unrelated foundation
	// mtimes cannot obscure the layered-vs-flat freshness assertion below.
	generatedAt := time.Now().UTC().Add(architectFreshnessGrace + time.Second).Truncate(time.Millisecond)
	before := generatedAt.Add(-10 * time.Second)
	if err := os.Chtimes(
		filepath.Join(dir, "layered_outline.json"),
		before,
		before,
	); err != nil {
		t.Fatal(err)
	}
	readiness := assessArchitectReadiness(dir)
	if !readiness.Ready {
		t.Fatalf("fixture not architect ready: %+v", readiness.Issues)
	}
	readiness.GeneratedAt = generatedAt.Format(time.RFC3339)
	if err := writeArchitectReadiness(dir, readiness); err != nil {
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

func TestArchitectReadinessAllowsWorldTickClockProgressButRejectsAuthoredWorldDrift(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	world, err := st.World.LoadBookWorld()
	if err != nil || world == nil {
		t.Fatalf("load book world: world=%+v err=%v", world, err)
	}
	world.Factions[0].Clock.Progress++
	if err := st.World.SaveBookWorld(*world); err != nil {
		t.Fatal(err)
	}
	if ok, reason := architectReadinessState(dir); !ok {
		t.Fatalf("world tick clock progress invalidated authored-world proof: %s", reason)
	}

	world.Factions[0].Goal += "，并垄断新凭证"
	if err := st.World.SaveBookWorld(*world); err != nil {
		t.Fatal(err)
	}
	if ok, reason := architectReadinessState(dir); ok || !strings.Contains(reason, "source digest") {
		t.Fatalf("authored BookWorld drift was not rejected: ok=%v reason=%q", ok, reason)
	}
}

func TestArchitectCheckPipelinePersistsV2WorldCoherenceProof(t *testing.T) {
	dir := seedZeroInitProject(t) // also keeps the legacy-v1 readiness path covered.
	st := store.NewStore(dir)

	world, err := st.World.LoadBookWorld()
	if err != nil || world == nil {
		t.Fatalf("load book world: world=%+v err=%v", world, err)
	}
	world.Version = domain.CurrentBookWorldSchemaVersion
	world.Routes = []domain.WorldRoute{{
		From: "old_block", To: "corner_store", Description: "步行穿过夜间街巷", Risk: "封路时延迟半日", TravelDays: 0.1,
	}}
	world.Factions[0].Resources = []string{"欠费名册", "催收凭证"}
	world.Factions[0].Relations = []domain.FactionRelation{{Target: "residents", Kind: "pressures"}}
	world.Factions[1].Resources = []string{"邻里消息", "互助人情"}
	world.Factions[1].Relations = []domain.FactionRelation{{Target: "debt_rule", Kind: "resists"}}
	if err := st.World.SaveBookWorld(*world); err != nil {
		t.Fatal(err)
	}

	codex := zeroInitTestWorldCodex()
	codex.SchemaVersion = domain.CurrentWorldCodexSchemaVersion
	for i := range codex.AbilityTiers {
		codex.AbilityTiers[i].Cost = "每次核验都会消耗可追踪的时间或凭证"
	}
	codex.SkillDomains[0].Constraints = []string{"必须接触账单或有效副本"}
	codex.WeaponCategories[0].Constraints = []string{"现实器物不能改写契约"}
	codex.EquipmentCategories[0].Constraints = []string{"凭证必须能核验来源"}
	codex.Mechanisms = []domain.CodexMechanism{{
		ID: "debt_verification", Name: "债务核验", Visibility: "formal",
		SectionRefs: []string{"mechanism_structure"}, ActorScope: []string{"持有账单或有效副本的人"},
		Trigger: "有人对欠费提出异议", Preconditions: []string{"能接触可核验凭证"},
		Inputs: []string{"账单或有效副本"}, Costs: []string{"耗费半日并暴露核验意图"},
		Effects: []string{"确认债务有效性或登记争议"}, FailureModes: []string{"无凭证时核验被阻断"},
		Observability: []string{"登记者与在场持有人可看到回执"}, Timing: "半日后生效",
	}}
	codex.CounterfactualTests = []domain.CodexCounterfactualProbe{{
		ID: "debt_without_receipt", Given: []string{"角色只有传闻，没有账单或副本"},
		Action: "要求免除债务", ExpectedOutcome: "核验被阻断并保留原债务状态",
		ForbiddenOutcome: "为了推进剧情直接免债", MechanismRefs: []string{"debt_verification"},
	}}
	if err := st.SaveWorldCodex(codex); err != nil {
		t.Fatal(err)
	}

	if err := architectCheckPipeline(cliOptions{}, []string{"--dir", dir}); err != nil {
		t.Fatalf("architect check command failed: %v", err)
	}
	report, err := st.LoadWorldCoherenceReport()
	if err != nil || report == nil || !report.Ready || report.ReportDigest == "" {
		t.Fatalf("v2 coherence proof not persisted: report=%+v err=%v", report, err)
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
