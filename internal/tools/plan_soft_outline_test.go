package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestIndependentArbitrationReplansSoftOutlineWithoutChangingHardObligations(t *testing.T) {
	tool, args := newStoryClockArbitrationTool(t, false)
	raw, _ := json.Marshal(args)
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	st := tool.store
	entry := domain.OutlineEntry{Chapter: 3, Title: "稳定章位", CoreEvent: "旧软情节让离屏人物到场出示秘密", Hook: "旧软钩子提前公布谜底", Scenes: []string{"[project-all hard-obligation:sealed-claim] 保留已接受的签认责任"}}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{entry}); err != nil {
		t.Fatal(err)
	}
	legacy := "完整兑现本章大纲核心事件（允许压缩、并场和换序，但终态不可改变）：" + entry.CoreEvent
	structure := map[string]any{"title": "待改名", "goal": "把眼前账本装袋", "hook": "原件已装袋但尚未交接", "required_beats": []string{legacy, "林默保留原件"}}
	if err := applyOutlineAnchorsToStructure(st, 3, structure, false); err != nil {
		t.Fatal(err)
	}
	if structure["title"] != entry.Title || structure["goal"] != "把眼前账本装袋" || structure["hook"] != "原件已装袋但尚未交接" {
		t.Fatalf("soft outline overrode actual choice: %+v", structure)
	}
	if got := stringSliceFromAny(structure["required_beats"]); len(got) != 1 || got[0] != "林默保留原件" {
		t.Fatalf("host-injected obsolete beat remains: %v", got)
	}
	sim, err := st.LoadChapterWorldSimulation(3)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 3, Hook: "原件已装袋但尚未交接", Contract: domain.ChapterContract{RequiredBeats: []string{legacy, "林默保留原件"}}, CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	if err := applyOutlineAnchorsToPlan(st, &plan, false); err != nil {
		t.Fatal(err)
	}
	if plan.Hook != structure["hook"] || strings.Contains(plan.Goal, entry.CoreEvent) {
		t.Fatalf("plan replaced finalized outcome: %+v", plan)
	}
	if len(plan.Contract.RequiredBeats) != 2 || !strings.Contains(plan.Contract.RequiredBeats[1], "sealed-claim") {
		t.Fatalf("genuine hard obligation was lost: %v", plan.Contract.RequiredBeats)
	}
	plan.CausalSimulation.WorldSimulationID = "wrong"
	if err := applyOutlineAnchorsToPlan(st, &plan, false); !errors.Is(err, errs.ErrToolPrecondition) ||
		!strings.Contains(err.Error(), sim.SimulationID) || !strings.Contains(err.Error(), sim.ProtagonistProjection.ChosenDecision) {
		t.Fatalf("unbound plan did not return a repairable exact-source precondition: %v", err)
	}
	entry.Scenes = nil
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{entry}); err != nil {
		t.Fatal(err)
	}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	plan.Contract.RequiredBeats = []string{legacy}
	if err := applyOutlineAnchorsToPlan(st, &plan, false); err == nil || !strings.Contains(err.Error(), "plan_structure") {
		t.Fatalf("removing a host-invented beat must request actual result beats, not save an empty contract: %v", err)
	}
	// A modified simulation cannot grant the soft-outline exception.
	sim.CharacterDecisions[0].Decision = "改为另一决定"
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	if err := applyOutlineAnchorsToStructure(st, 3, structure, false); err == nil {
		t.Fatal("corrupt arbitration bypassed outline authority")
	}
}
