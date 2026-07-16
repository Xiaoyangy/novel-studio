package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestChapterWorldSimulationRejectsCapBelowCurrentOutlineTarget(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 3, CoreEvent: "已有摊主主动加入，试点由五家扩到十家；随后完成五十笔真实交易。",
	}}); err != nil {
		t.Fatal(err)
	}
	sim := domain.ChapterWorldSimulation{Chapter: 3, ProtagonistProjection: domain.ProtagonistDecisionProjection{
		ChosenDecision: "维持五摊上限，完成五十笔真实交付后只登记下一批。",
	}}
	gaps := chapterWorldSimulationQuantityGaps(s, sim)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "目标至少 10") || !strings.Contains(gaps[0], "硬上限为 5") {
		t.Fatalf("expected ten-vs-five world simulation conflict, got %v", gaps)
	}
	if strings.Contains(strings.Join(gaps, " "), "50") {
		t.Fatalf("transaction target must not be compared with stall cap: %v", gaps)
	}
}

func TestFinalizeChapterPlanRejectsRequiredTargetAboveProjectionCap(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 3,
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"观望摊主加入，试点扩至十家并形成真实交易。",
		}},
		CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "保持五摊上限，拒绝临时扩围。"},
	}
	if err := ValidateChapterQuantityResultContract(s, plan); err == nil || !strings.Contains(err.Error(), "chapter quantity result conflict") {
		t.Fatalf("expected plan quantity rejection, got %v", err)
	}
}

func TestQuantityContractAllowsFiveExistingPlusFiveNewToTen(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 3,
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"保持原五家继续营业，再新增五摊，试点扩到十家。",
		}},
		CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "完成十家试点后不再接受当晚临时特批。"},
	}
	if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
		t.Fatalf("valid five-plus-five expansion was rejected: %v", err)
	}
}

func TestQuantityContractAllowsOpeningCapBeforeTerminalExpansion(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter:  3,
		Contract: domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			ProtagonistDecision: "开场先维持五摊上限疏通桥口，随后扩到十家，最终拒绝第十一摊。",
		},
	}
	if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
		t.Fatalf("opening cap followed by a higher terminal target was rejected: %v", err)
	}
	plan.CausalSimulation.ProtagonistDecision = "先扩到十家，混乱后最终维持五摊上限。"
	if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
		t.Fatal("terminal lower cap after an earlier target must still fail")
	}
}

func TestQuantityContractDoesNotTreatFutureTargetAsCurrentTransition(t *testing.T) {
	for _, decision := range []string{
		"最终维持五摊上限，准备下一章再扩到十家。",
		"本章保持五家以内，明天扩到十家。",
	} {
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"本章试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("future target incorrectly erased current lower cap in %q", decision)
		}
	}
}

func TestQuantityContractIgnoresNegatedOrAlternativeTargets(t *testing.T) {
	for _, beat := range []string{
		"本章不扩到十家，只维持五家上限。",
		"本章不再扩到十家，只维持五家上限。",
		"暂不考虑扩到十家，先维持五家。",
		"拒绝扩到十家，继续维持五家。",
		"视客流扩到十家或保持五家。",
		"视客流扩到十家或五家。",
		"备选方案是扩到十家，主方案保持五家。",
		"把扩到十家作为备选，主方案保持五家。",
		"扩到十家（备选），主方案保持五家。",
	} {
		claims := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: beat}})
		if len(claims) != 0 {
			t.Fatalf("non-binding target was treated as hard in %q: %+v", beat, claims)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "维持五家上限。"},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("non-binding branch caused a false conflict in %q: %v", beat, err)
		}
	}
}

func TestQuantityContractKeepsExplicitMandatoryTarget(t *testing.T) {
	for _, beat := range []string{
		"本章必须扩到十家。",
		"客流已至，主角不得不扩到十家。",
		"客流已至，主角不会不扩到十家。",
		"客流已至，主角不可能不扩到十家。",
		"客流已至，试点扩到十家或更多。",
		"为下一章蓄客，本章必须扩到十家。",
		"虽然下一章另有安排但本章必须扩到十家。",
		"排除五家备选方案，本章必须扩到十家。",
		"不考虑候选摊主，本章必须扩到十家。",
		"排除五家备选方案后本章必须扩到十家。",
		"不考虑候选摊主后本章必须扩到十家。",
		"计划明确要求本章必须扩到十家。",
		"验收要求必须扩到十家。",
	} {
		claims := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: beat}})
		if len(claims) != 1 || claims[0].Count != 10 {
			t.Fatalf("mandatory target was dropped in %q: %+v", beat, claims)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "维持五家上限。"},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("mandatory ten target must conflict with cap five in %q", beat)
		}
	}
}

func TestQuantityContractIgnoresTentativeOrConditionalTargets(t *testing.T) {
	for _, beat := range []string{
		"本章可能扩到十家。",
		"本章计划扩到十家。",
		"本章考虑扩到十家。",
		"本章尝试扩到十家。",
		"本章争取扩到十家。",
		"若客流允许则扩到十家。",
		"如果桥口畅通扩到十家。",
		"视情况决定是否扩到十家。",
		"视客流反馈决定是否扩到十家。",
	} {
		claims := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: beat}})
		if len(claims) != 0 {
			t.Fatalf("tentative target was treated as hard in %q: %+v", beat, claims)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "维持五家上限。"},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("tentative target caused a false conflict in %q: %v", beat, err)
		}
	}
}

func TestQuantityContractIgnoresCompletedTargetCancellation(t *testing.T) {
	for _, beat := range []string{
		"扩到十家的旧计划已取消。",
		"扩到十家的旧计划已否决。",
		"扩到十家的旧计划已被否决。",
		"扩到十家的旧计划已作废。",
		"扩到十家的旧计划不再适用。",
	} {
		claims := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: beat}})
		if len(claims) != 0 {
			t.Fatalf("cancelled target was treated as hard in %q: %+v", beat, claims)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "维持五家上限。"},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("cancelled target caused a false conflict in %q: %v", beat, err)
		}
	}
}

func TestQuantityContractKeepsTargetWithCancellationRisk(t *testing.T) {
	for _, beat := range []string{
		"扩到十家的旧计划会取消。",
		"扩到十家的旧计划存在取消风险。",
		"扩到十家的旧计划可能被否决。",
	} {
		claims := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: beat}})
		if len(claims) != 1 || claims[0].Count != 10 {
			t.Fatalf("uncertain cancellation erased hard target in %q: %+v", beat, claims)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "维持五家上限。"},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("uncertain cancellation must not remove target in %q", beat)
		}
	}
}

func TestQuantityContractIgnoresFutureTargetAndRewardWindow(t *testing.T) {
	for _, beat := range []string{
		"本章维持五家、下一章扩到十家。",
		"本章维持五家，明天必须扩到十家。",
	} {
		plan := domain.ChapterPlan{
			Chapter:  3,
			Contract: domain.ChapterContract{RequiredBeats: []string{beat}},
			CausalSimulation: domain.ChapterCausalSimulation{
				ProtagonistDecision: "维持五家上限。",
				ReaderRewardPlan: domain.ReaderRewardPlan{
					ChapterWindow: "本章五家、下一章扩到十家。",
				},
			},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("future target became a current contract in %q: %v", beat, err)
		}
	}
}

func TestQuantityContractIgnoresExplicitlyRejectedOldCap(t *testing.T) {
	for _, decision := range []string{
		"扩到十家，而不是维持五摊上限。",
		"不维持五摊上限，而是扩到十家。",
		"不必再维持五摊上限，直接扩到十家。",
		"不需要维持五摊上限，直接扩到十家。",
		"无需维持五摊上限，直接扩到十家。",
		"无须继续维持五摊上限，直接扩到十家。",
		"不会维持五摊上限，直接扩到十家。",
		"不能维持五摊上限，直接扩到十家。",
		"不打算维持五摊上限，直接扩到十家。",
		"不准备维持五摊上限，直接扩到十家。",
		"不考虑维持五摊上限，直接扩到十家。",
		"不同意维持五摊上限，直接扩到十家。",
		"不选择维持五摊上限，直接扩到十家。",
		"并不维持五摊上限，直接扩到十家。",
		"避免维持五摊上限，改为扩到十家。",
		"禁止维持五摊上限，改为扩到十家。",
		"否决维持五摊上限，改为扩到十家。",
		"扩到十家，打破原先维持五摊上限的做法。",
		"扩到十家，维持五摊上限已作废。",
		"扩到十家，维持五摊上限已被取消。",
		"扩到十家，维持五摊上限已被打破。",
		"扩到十家，维持五摊上限已经被打破。",
		"原本打算维持五摊上限，复核后而是扩到十家。",
	} {
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("explicitly rejected legacy cap was treated as final in %q: %v", decision, err)
		}
	}
}

func TestQuantityContractDoesNotApplyNegationAcrossAnotherPredicate(t *testing.T) {
	for _, decision := range []string{
		"拒绝临时加摊，最终维持五摊上限。",
		"取消临时申请，仍维持五摊上限。",
		"禁止临时加摊，最终维持五摊上限。",
		"避免桥口拥堵后，仍维持五摊上限。",
		"现场条件不足，主角不得不维持五摊上限。",
		"现场条件不足，主角不能不维持五摊上限。",
		"现场条件不足，主角不会不维持五摊上限。",
		"现场条件不足，主角不可能不维持五摊上限。",
		"现场条件不足，主角不应不维持五摊上限。",
		"现场条件不足，主角不该不维持五摊上限。",
		"维持五摊上限被打破的风险不可接受。",
		"维持五摊上限会被打破，因此必须加强秩序。",
	} {
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("unrelated negation hid the terminal lower cap in %q", decision)
		}
	}
}

func TestQuantityContractIgnoresTentativeOrAlternativeCap(t *testing.T) {
	for _, decision := range []string{
		"可能维持五摊上限。",
		"也许会维持五摊上限。",
		"或许仍维持五摊上限。",
		"未必维持五摊上限。",
		"不一定维持五摊上限。",
		"考虑继续维持五摊上限。",
		"打算先维持五摊上限。",
		"准备维持五摊上限。",
		"计划本章维持五摊上限。",
		"维持五摊上限或直接扩围。",
		"维持五摊上限或者取消上限。",
	} {
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
			t.Fatalf("tentative or alternative cap was treated as hard in %q: %v", decision, err)
		}
	}
}

func TestQuantityContractKeepsDefiniteCapDespiteSimilarWords(t *testing.T) {
	for _, decision := range []string{
		"计划明确要求维持五摊上限。",
		"维持五摊上限或更低。",
		"维持五摊上限或者最多四摊。",
	} {
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("definite cap was incorrectly discarded in %q", decision)
		}
	}
}

func TestQuantityContractAllowsEqualCapAndRejectsLowerCap(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter:          3,
		Contract:         domain.ChapterContract{RequiredBeats: []string{"本章试点扩到十家。"}},
		CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "扩到十家后维持十家上限，拒绝第十一摊。"},
	}
	if err := ValidateChapterQuantityResultContract(s, plan); err != nil {
		t.Fatalf("equal target and cap should pass: %v", err)
	}
	plan.CausalSimulation.ProtagonistDecision = "维持九家上限。"
	if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
		t.Fatal("lower cap should fail")
	}
}

func TestQuantityContractRecognizesNaturalHardCapWordOrder(t *testing.T) {
	for _, decision := range []string{
		"维持在五家以内。",
		"保持在五摊上限。",
		"维持五家的上限。",
	} {
		plan := domain.ChapterPlan{
			Chapter:          3,
			Contract:         domain.ChapterContract{RequiredBeats: []string{"试点扩到十家。"}},
			CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: decision},
		}
		s := store.NewStore(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if err := ValidateChapterQuantityResultContract(s, plan); err == nil {
			t.Fatalf("natural hard-cap wording bypassed contract: %q", decision)
		}
	}
}

func TestQuantityContractSeparatesTenStallsFromFiftyOrders(t *testing.T) {
	targets := chapterQuantityTargetClaims([]chapterQuantityText{{Source: "test", Text: "试点扩到十家，并完成五十笔真实交易。"}})
	caps := chapterQuantityCapClaims([]chapterQuantityText{{Source: "decision", Text: "维持十家上限，最多五十笔订单。"}})
	if conflicts := chapterQuantityConflicts(targets, caps); len(conflicts) != 0 {
		t.Fatalf("equal per-entity quantities should pass independently: %+v", conflicts)
	}
	caps = chapterQuantityCapClaims([]chapterQuantityText{{Source: "decision", Text: "维持十家上限，最多四十九笔订单。"}})
	conflicts := chapterQuantityConflicts(targets, caps)
	if len(conflicts) != 1 || conflicts[0].Entity.Entity != "transaction" {
		t.Fatalf("only the order contract should fail: %+v", conflicts)
	}
}

func TestValidateReusableCausalPlanRejectsQuantityContractDrift(t *testing.T) {
	s := newChapterSimulationTestStore(t)
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, CoreEvent: "试点由五家扩到十家"}}); err != nil {
		t.Fatal(err)
	}
	sim := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "维持五摊上限", true),
			simulatedDecision("沈知遥", "继续检查现场", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"客流增加"}, HiddenPressures: []string{"下一批申请"},
			AvailableOptions: []string{"扩围", "维持"}, ChosenDecision: "维持五摊上限", DecisionReason: "先看承载量",
			PlanConstraints: []string{"不得越界"}, CausalChain: []string{"客流增加", "维持五摊上限"},
		},
	}
	sim.SimulationID = chapterWorldSimulationID(sim)
	if err := s.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "扩摊", Contract: domain.ChapterContract{RequiredBeats: []string{"扩到十家"}}}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	plan.CausalSimulation.ProtagonistDecision = sim.ProtagonistProjection.ChosenDecision
	plan.CausalSimulation.ContextSources = []string{"chapter_world_simulation:" + sim.SimulationID}
	plan.CausalSimulation.CausalBeats = []domain.CausalSimulationBeat{{Cause: "客流", CharacterChoice: "扩围", WorldResponse: "新增摊位", StoryResult: "进入下一批"}}
	plan.CausalSimulation.OutcomeShift = []string{"完成试点"}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(s, 1); err == nil || !strings.Contains(err.Error(), "quantity result conflict") {
		t.Fatalf("legacy render-only reuse bypassed quantity drift: %v", err)
	}
}

func TestParseChapterQuantityNumber(t *testing.T) {
	for raw, want := range map[string]int{"十": 10, "五十": 50, "一百零二": 102, "１２": 12, "两千三百": 2300} {
		got, ok := parseChapterQuantityNumber(raw)
		if !ok || got != want {
			t.Fatalf("parse %q = %d,%v want %d,true", raw, got, ok, want)
		}
	}
}
