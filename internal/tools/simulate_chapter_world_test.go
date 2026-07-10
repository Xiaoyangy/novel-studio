package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newChapterSimulationTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveSimulationCast(domain.SimulationCast{Assignments: []domain.TierAssignment{
		{Name: "林澈", Tier: domain.TierProtagonistCircle},
		{Name: "沈知遥", Tier: domain.TierProtagonistCircle},
	}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func simulatedDecision(name, decision string, visible bool) domain.CharacterWorldDecision {
	return domain.CharacterWorldDecision{
		Character:         name,
		Location:          "青山县",
		CurrentGoal:       "按自己的目标推进一天",
		Pressure:          "时间和关系都在施压",
		KnowledgeBoundary: "只知道亲见和合法通信获得的信息",
		AvailableOptions:  []string{"现在行动", "继续观察"},
		Decision:          decision,
		DecisionReason:    "当前目标更重要，且手上证据只支持这一步",
		Action:            "落实选择并承担结果",
		ActionDuration:    "两小时",
		CompletionState:   "completed",
		ImmediateResult:   "世界状态发生可追踪变化",
		StateAfter:        "进入下一步但没有提前完成长期目标",
		VisibleToPOV:      visible,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:            "改变下一次接触时的资源和信息条件",
			Targets:           []string{"林澈"},
			TransmissionPath:  "亲见或延迟通信",
			ArrivalChapter:    1,
			Visibility:        map[bool]string{true: "visible", false: "delayed"}[visible],
			ProtagonistImpact: "改变林澈本章或后续可选择的行动",
		}},
	}
}

func TestSimulateChapterWorldRequiresEveryCharacterAndButterflyEffect(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	tool := NewSimulateChapterWorldTool(st)
	first, _ := json.Marshal(map[string]any{
		"chapter":             1,
		"time_window":         "同一天晚饭前后两小时",
		"character_decisions": []domain.CharacterWorldDecision{simulatedDecision("林澈", "在饭桌承认失业", true)},
		"finalize":            true,
	})
	if _, err := tool.Execute(context.Background(), first); err == nil || !strings.Contains(err.Error(), "沈知遥") {
		t.Fatalf("finalize must reject missing cast decisions, got %v", err)
	}

	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"亲戚追问把失业事实推到桌面"},
		HiddenPressures:   []string{"沈知遥的工作安排尚未传到林澈"},
		AvailableOptions:  []string{"继续隐瞒", "承认失业"},
		ChosenDecision:    "在饭桌承认失业",
		DecisionReason:    "继续隐瞒会让家人按错误信息安排明天",
		PlanConstraints:   []string{"不能提前知道沈知遥的离屏行动"},
		CausalChain:       []string{"亲戚追问", "父母护短", "物证压缩退路", "林澈承认失业"},
	}
	second, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"character_decisions": []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "在饭桌承认失业", true),
			simulatedDecision("沈知遥", "继续完成夜市检查准备", false),
		},
		"protagonist_projection": projection,
		"sources":                []string{"world_tick:v1-a1", "character_dossiers", "current_chapter_outline"},
		"finalize":               true,
	})
	raw, err := tool.Execute(context.Background(), second)
	if err != nil {
		t.Fatalf("complete simulation should finalize: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["simulated"] != true || strings.TrimSpace(result["simulation_id"].(string)) == "" {
		t.Fatalf("unexpected finalize result: %s", raw)
	}
	sim, err := st.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil || len(sim.CharacterDecisions) != 2 {
		t.Fatalf("saved simulation mismatch: sim=%+v err=%v", sim, err)
	}
}

func TestPOVPlanMustReferenceFinalizedWorldSimulation(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	sim := domain.ChapterWorldSimulation{
		Version:    1,
		Chapter:    1,
		TimeWindow: "晚饭前后两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "承认失业", true),
			simulatedDecision("沈知遥", "继续检查准备", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "林澈",
			ObservableEffects: []string{"饭桌追问"},
			HiddenPressures:   []string{"夜市检查准备"},
			AvailableOptions:  []string{"隐瞒", "承认"},
			ChosenDecision:    "承认失业",
			DecisionReason:    "物证已经压缩退路",
			PlanConstraints:   []string{"限知视角"},
			CausalChain:       []string{"追问", "承认"},
		},
	}
	sim.SimulationID = chapterWorldSimulationID(sim)
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{
		WorldSimulationID:   "wrong",
		ProtagonistDecision: "承认失业",
		ContextSources:      []string{"chapter_world_simulation:" + sim.SimulationID},
	}}
	if err := validateChapterWorldSimulationReference(st, plan); err == nil {
		t.Fatal("wrong simulation id must be rejected")
	}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	if err := validateChapterWorldSimulationReference(st, plan); err != nil {
		t.Fatalf("matching POV reference should pass: %v", err)
	}
}

func TestMigrateLegacyPlanStagePreservesWorldDecisionsOutsidePOVPlan(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	partial := map[string]any{
		"structure": map[string]any{"chapter": 1, "title": "失业饭桌"},
		"causal_simulation": map[string]any{
			"offscreen_character_stage": []any{map[string]any{
				"character": "林澈", "location": "林家饭桌", "environment": "亲戚追问",
				"current_action": "用玩笑顶回去", "pressure": "失业事实", "decision": "停止隐瞒",
				"knowledge_boundary": "只知道自己的解雇通知", "timeline_consistency": "同一晚推进",
			}},
		},
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, partial); err != nil {
		t.Fatal(err)
	}
	changed, err := MigrateLegacyPlanStageToChapterSimulation(st, 1, partial)
	if err != nil || !changed {
		t.Fatalf("migration failed: changed=%v err=%v", changed, err)
	}
	seed, err := st.LoadChapterWorldSimulationPartial(1)
	if err != nil || seed == nil || len(seed.CharacterDecisions) != 1 || seed.CharacterDecisions[0].Decision != "停止隐瞒" {
		t.Fatalf("simulation seed not preserved: seed=%+v err=%v", seed, err)
	}
	reloaded, _ := st.Drafts.LoadChapterPlanPartial(1)
	merged := reloaded["causal_simulation"].(map[string]any)
	if _, exists := merged["offscreen_character_stage"]; exists {
		t.Fatal("legacy all-character stage must be removed from the POV plan partial")
	}
	if len(merged) != 0 {
		t.Fatalf("active causal fields must be rebuilt after migration: %+v", merged)
	}
	archive, ok := reloaded["legacy_causal_simulation_archive"].(map[string]any)
	if !ok || archive["offscreen_character_stage"] == nil {
		t.Fatalf("legacy causal fields were not archived: %+v", reloaded)
	}
}

func TestRewriteWorldSimulationRequiresFactCoverageAndCommittedVisibleCast(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	source := prepareRewriteSourceTest(t, st,
		"第一章 失业饭桌\n\n林澈在夜市付款4280元，沈知遥章末检查票据。",
		"# brief\n\n## 保留事实\n\n- 林澈在夜市付款4280元。\n- 沈知遥章末检查票据。\n")
	tool := NewSimulateChapterWorldTool(st)
	projection := domain.ProtagonistDecisionProjection{
		Protagonist: "林澈", ObservableEffects: []string{"夜市付款和票据检查"}, HiddenPressures: []string{"额度边界未知"},
		AvailableOptions: []string{"放弃", "限额试验"}, ChosenDecision: "限额试验", DecisionReason: "只承担可控损失",
		PlanConstraints: []string{"保留夜市事实链"}, CausalChain: []string{"付款", "灯亮", "检查票据"},
	}
	base := map[string]any{
		"chapter": 1, "time_window": "当日晚饭至夜市收摊",
		"character_decisions": []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "限额试验", true),
			simulatedDecision("沈知遥", "章末检查票据", true),
		},
		"protagonist_projection": projection,
		"sources":                []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		"finalize":               true,
	}
	raw, _ := json.Marshal(base)
	if _, err := tool.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "rewrite_fact_coverage") {
		t.Fatalf("rewrite simulation without fact coverage must fail, got %v", err)
	}
	base["rewrite_fact_coverage"] = []domain.ChapterRewriteFactCoverage{
		{Fact: source.PreserveFacts[0], SimulationEvidence: []string{"林澈决定限额付款，支付结果进入夜市流水"}},
		{Fact: source.PreserveFacts[1], SimulationEvidence: []string{"沈知遥 visible_to_pov=true，章末核对票据"}},
	}
	raw, _ = json.Marshal(base)
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatalf("rewrite simulation with pinned facts should finalize: %v", err)
	}
	sim, err := st.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil || !rewriteSourceEqual(sim.RewriteSource, source) {
		t.Fatalf("rewrite source not persisted: sim=%+v err=%v", sim, err)
	}
}
