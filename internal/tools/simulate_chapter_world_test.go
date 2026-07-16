package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
		ChosenDecision:    "错误的重复选择",
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
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{"structure": map[string]any{"chapter": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPendingSteer("Pipeline world-simulation repair：test"); err != nil {
		t.Fatal(err)
	}
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
	if sim.ProtagonistProjection.ChosenDecision != "在饭桌承认失业" {
		t.Fatalf("projection should be normalized to protagonist decision: %+v", sim.ProtagonistProjection)
	}
	if partial, err := st.Drafts.LoadChapterPlanPartial(1); err != nil || partial != nil {
		t.Fatalf("new simulation must invalidate old POV plan partial: partial=%v err=%v", partial, err)
	}
	if meta, err := st.RunMeta.Load(); err != nil || (meta != nil && meta.PendingSteer != "") {
		t.Fatalf("completed simulation repair steer must clear: meta=%+v err=%v", meta, err)
	}
}

func TestSimulateChapterWorldReusesValidFinalAndDropsRedundantPartial(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	sim := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "承认失业", true),
			simulatedDecision("沈知遥", "继续夜市检查", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"饭桌追问"}, HiddenPressures: []string{"夜市检查"},
			AvailableOptions: []string{"隐瞒", "承认"}, ChosenDecision: "承认失业", DecisionReason: "证据已公开",
			PlanConstraints: []string{"限知"}, CausalChain: []string{"追问", "承认"},
		},
	}
	sim.SimulationID = chapterWorldSimulationID(sim)
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, CharacterDecisions: []domain.CharacterWorldDecision{simulatedDecision("林澈", "重复推演", true)},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter":             1,
		"character_decisions": []domain.CharacterWorldDecision{simulatedDecision("林澈", "再次重复", true)},
	})
	raw, err := NewSimulateChapterWorldTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["simulated"] != true || out["reused"] != true || out["simulation_id"] != sim.SimulationID {
		t.Fatalf("expected immutable simulation reuse, got %s", raw)
	}
	if partial, err := st.LoadChapterWorldSimulationPartial(1); err != nil || partial != nil {
		t.Fatalf("redundant partial must be removed: partial=%+v err=%v", partial, err)
	}
	saved, err := st.LoadChapterWorldSimulation(1)
	if err != nil || saved == nil || saved.CharacterDecisions[0].Decision != "承认失业" {
		t.Fatalf("final simulation was overwritten: saved=%+v err=%v", saved, err)
	}
}

func TestSimulationCheckpointCreatesNewEpochForAtoBtoA(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	makeSimulation := func(decision string) domain.ChapterWorldSimulation {
		sim := domain.ChapterWorldSimulation{
			Version: 1, Chapter: 1, TimeWindow: "当晚两小时",
			CharacterDecisions: []domain.CharacterWorldDecision{
				simulatedDecision("林澈", decision, true),
				simulatedDecision("沈知遥", "继续夜市检查", false),
			},
			ProtagonistProjection: domain.ProtagonistDecisionProjection{
				Protagonist: "林澈", ObservableEffects: []string{"饭桌追问"}, HiddenPressures: []string{"夜市检查"},
				AvailableOptions: []string{"隐瞒", decision}, ChosenDecision: decision, DecisionReason: "当前证据支持",
				PlanConstraints: []string{"限知"}, CausalChain: []string{"追问", decision},
			},
		}
		sim.SimulationID = chapterWorldSimulationID(sim)
		return sim
	}
	reuse := func() *domain.Checkpoint {
		raw, _ := json.Marshal(map[string]any{"chapter": 1})
		if _, err := NewSimulateChapterWorldTool(st).Execute(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		checkpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation")
		if checkpoint == nil {
			t.Fatal("valid final simulation was reused without a checkpoint")
		}
		return checkpoint
	}

	simA := makeSimulation("承认失业")
	simB := makeSimulation("暂缓承认")
	if err := st.SaveChapterWorldSimulation(simA); err != nil {
		t.Fatal(err)
	}
	cpA1 := reuse()
	if err := st.SaveChapterWorldSimulation(simB); err != nil {
		t.Fatal(err)
	}
	cpB := reuse()
	if cpB.Seq <= cpA1.Seq || cpB.Digest == cpA1.Digest {
		t.Fatalf("A->B did not establish a new simulation epoch: A=%+v B=%+v", cpA1, cpB)
	}
	if err := st.SaveChapterWorldSimulation(simA); err != nil {
		t.Fatal(err)
	}
	cpA2 := reuse()
	if cpA2.Seq <= cpB.Seq || cpA2.Digest != cpA1.Digest {
		t.Fatalf("A->B->A must append a new A epoch: A1=%+v B=%+v A2=%+v", cpA1, cpB, cpA2)
	}
	if repeated := reuse(); repeated.Seq != cpA2.Seq {
		t.Fatalf("adjacent retry of the same simulation should remain idempotent: before=%+v after=%+v", cpA2, repeated)
	}
}

func TestStructuralEscalationForcesReplacementOfReadySimulation(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	source := prepareRewriteSourceTest(t, st,
		"第一章\n\n旧稿保留既定结果。",
		"# brief\n\n## 保留事实\n\n- 无额外条目。\n\n## 必须修正\n\n- 重建场景因果。\n",
	)
	old := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "旧推演时段", RewriteSource: source,
		Sources: []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "沿用旧选择", true),
			simulatedDecision("沈知遥", "继续旧安排", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"旧结果"}, HiddenPressures: []string{"旧压力"},
			AvailableOptions: []string{"等待", "沿用旧选择"}, ChosenDecision: "沿用旧选择", DecisionReason: "旧证据",
			PlanConstraints: []string{"保留事实"}, CausalChain: []string{"旧压力", "沿用旧选择"},
		},
	}
	old.SimulationID = chapterWorldSimulationID(old)
	if err := st.SaveChapterWorldSimulation(old); err != nil {
		t.Fatal(err)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧计划"}
	plan.CausalSimulation.WorldSimulationID = old.SimulationID
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 2
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"sha256:blocked-a", "sha256:blocked-b"} {
		if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "draft-structural-block", "reviews/drafts/01_full_rerender_required.json", digest); err != nil {
			t.Fatal(err)
		}
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); !escalation.Required {
		t.Fatalf("test did not establish structural escalation: %+v", escalation)
	}

	projection := domain.ProtagonistDecisionProjection{
		Protagonist: "林澈", ObservableEffects: []string{"新场景后果"}, HiddenPressures: []string{"新的离屏压力"},
		AvailableOptions: []string{"拒绝", "重做选择"}, ChosenDecision: "重做选择", DecisionReason: "新证据改变了可选项",
		PlanConstraints: []string{"保留既定结果但废弃旧场景"}, CausalChain: []string{"新压力", "误判", "重做选择"},
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "time_window": "新推演时段",
		"character_decisions": []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "重做选择", true),
			simulatedDecision("沈知遥", "改变安排", false),
		},
		"protagonist_projection": projection,
		"finalize":               true,
	})
	raw, err := NewSimulateChapterWorldTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("forced structural resimulation failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["reused"] == true || result["simulation_id"] == old.SimulationID {
		t.Fatalf("ready old simulation was reused during structural escalation: %s", raw)
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); escalation.Required {
		t.Fatalf("new simulation checkpoint did not open a fresh causal epoch: %+v", escalation)
	}
}

func TestStructuralEscalationRestartsEmptySimulationShellOnFirstGroundedBatch(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	source := prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈保住既定结果。",
		"# brief\n\n## 保留事实\n\n- 林澈保住既定结果。\n",
	)
	old := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "旧时段", RewriteSource: source,
		Sources: []string{rewriteSourceToken(source), rewriteBriefToken(source)},
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "旧选择", true),
			simulatedDecision("沈知遥", "旧安排", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"旧结果"}, HiddenPressures: []string{"旧压力"},
			AvailableOptions: []string{"停止", "继续"}, ChosenDecision: "旧选择", DecisionReason: "旧证据",
			PlanConstraints: []string{"限知"}, CausalChain: []string{"旧因", "旧果"},
		},
		RewriteFactCoverage: []domain.ChapterRewriteFactCoverage{{
			Fact: "林澈保住既定结果。", SimulationEvidence: []string{"旧证据"},
		}},
	}
	old.SimulationID = chapterWorldSimulationID(old)
	if err := st.SaveChapterWorldSimulation(old); err != nil {
		t.Fatal(err)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧计划"}
	plan.CausalSimulation.WorldSimulationID = old.SimulationID
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 2
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"sha256:blocked-a", "sha256:blocked-b"} {
		if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md", digest); err != nil {
			t.Fatal(err)
		}
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); !escalation.Required {
		t.Fatalf("test did not establish structural escalation: %+v", escalation)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "未读到上下文时猜的时段",
		ProtagonistProjection: domain.ProtagonistDecisionProjection{Protagonist: "林澈"},
		Sources:               []string{"novel_context hard budget failed; ungrounded shell"},
	}); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":             1,
		"time_window":         "当前上下文确认的新时段",
		"character_decisions": []domain.CharacterWorldDecision{simulatedDecision("林澈", "重做选择", true)},
		"sources":             []string{"current_chapter_outline", "character_dossiers"},
	})
	raw, err := NewSimulateChapterWorldTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("first grounded batch should restart the empty shell: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["partial_restarted"] != true {
		t.Fatalf("empty-shell restart was not observable: %s", raw)
	}
	partial, err := st.LoadChapterWorldSimulationPartial(1)
	if err != nil || partial == nil {
		t.Fatalf("grounded replacement partial missing: partial=%+v err=%v", partial, err)
	}
	if partial.TimeWindow != "当前上下文确认的新时段" || len(partial.CharacterDecisions) != 1 ||
		partial.CharacterDecisions[0].Decision != "重做选择" {
		t.Fatalf("grounded replacement did not replace the shell: %+v", partial)
	}
	if slices.Contains(partial.Sources, "novel_context hard budget failed; ungrounded shell") {
		t.Fatalf("ungrounded shell provenance leaked into replacement epoch: %+v", partial.Sources)
	}
	if !slices.Contains(partial.Sources, rewriteSourceToken(source)) || !slices.Contains(partial.Sources, "current_chapter_outline") {
		t.Fatalf("replacement partial lost canonical sources: %+v", partial.Sources)
	}
	if checkpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation"); checkpoint == nil {
		t.Fatal("existing formal simulation checkpoint should remain until replacement finalizes")
	}
}

func TestSimulateChapterWorldRejectsEmptyAndOversizedBatches(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	tool := NewSimulateChapterWorldTool(st)
	empty, _ := json.Marshal(map[string]any{"chapter": 1})
	if _, err := tool.Execute(context.Background(), empty); err == nil || !strings.Contains(err.Error(), "空提交无效") {
		t.Fatalf("empty simulation patch must fail, got %v", err)
	}
	decisions := make([]domain.CharacterWorldDecision, 0, 9)
	for _, name := range []string{"甲", "乙", "丙", "丁", "戊", "己", "庚", "辛", "壬"} {
		decisions = append(decisions, simulatedDecision(name, "继续自己的行动", false))
	}
	oversized, _ := json.Marshal(map[string]any{"chapter": 1, "character_decisions": decisions})
	if _, err := tool.Execute(context.Background(), oversized); err == nil || !strings.Contains(err.Error(), "最多提交8名") {
		t.Fatalf("oversized simulation batch must fail, got %v", err)
	}
}

func TestSimulateChapterWorldCanonicalizesCastAliasToCharacterIdentity(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
		{Name: "梁广财", Aliases: []string{"二姨夫"}, Role: "农户代表", Tier: "secondary"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Cast.Save([]domain.CastEntry{{Name: "二姨夫", BriefRole: "饭桌长辈", LastSeenChapter: 1}}); err != nil {
		t.Fatal(err)
	}

	tool := NewSimulateChapterWorldTool(st)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "time_window": "返乡饭桌当晚",
		"character_decisions": []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "离开饭桌", true),
			simulatedDecision("沈知遥", "继续夜市巡查", false),
			simulatedDecision("二姨夫", "继续追问工作", true),
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	partial, err := st.LoadChapterWorldSimulationPartial(1)
	if err != nil {
		t.Fatal(err)
	}
	names := characterDecisionNames(partial.CharacterDecisions)
	if !slices.Contains(names, "梁广财") || slices.Contains(names, "二姨夫") {
		t.Fatalf("alias should normalize to one canonical decision: %v", names)
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

func TestPlanningClearsWorldPartialWhenSimulationNoLongerRequired(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Chapter: 1, TimeWindow: "旧 cast 下的 staged simulation",
	}); err != nil {
		t.Fatal(err)
	}
	if sim, err := ensureChapterWorldSimulationReadyForPlanning(st, 1); err != nil || sim != nil {
		t.Fatalf("optional simulation recovery failed: sim=%+v err=%v", sim, err)
	}
	if partial, err := st.LoadChapterWorldSimulationPartial(1); err != nil || partial != nil {
		t.Fatalf("no-longer-required partial was not cleared: partial=%+v err=%v", partial, err)
	}
}

func TestIdenticalWorldSimulationCreatesNewEpochAfterStructuralEscalation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	sim := domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: "same-semantic-simulation", TimeWindow: "同一事实窗口",
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	firstSimulation := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation")
	if firstSimulation == nil {
		t.Fatal("missing initial simulation checkpoint")
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < defaultRenderOnlyReplanLimit; i++ {
		if _, err := st.Checkpoints.Append(
			domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md",
			fmt.Sprintf("sha256:structural-%d", i),
		); err != nil {
			t.Fatal(err)
		}
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); !escalation.Required {
		t.Fatalf("regression setup did not exhaust the old causal budget: %+v", escalation)
	}

	// The formal simulation file is intentionally byte-identical. Finalizing
	// it after structural escalation must still open a new causal epoch.
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	currentSimulation := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation")
	if currentSimulation == nil || currentSimulation.Seq <= firstSimulation.Seq {
		t.Fatalf("identical simulation did not create a fresh epoch: first=%+v current=%+v", firstSimulation, currentSimulation)
	}
	if currentSimulation.Digest != firstSimulation.Digest {
		t.Fatalf("regression setup must keep simulation bytes identical: first=%s current=%s", firstSimulation.Digest, currentSimulation.Digest)
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, 1); escalation.Required || escalation.Attempts != 0 {
		t.Fatalf("new simulation epoch should reset the structural rendering budget: %+v", escalation)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	retry := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation")
	if retry == nil || retry.Seq != currentSimulation.Seq {
		t.Fatalf("adjacent retry of identical simulation should remain idempotent: current=%+v retry=%+v", currentSimulation, retry)
	}
}
