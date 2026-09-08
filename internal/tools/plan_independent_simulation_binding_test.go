package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func independentPlanningFixture(t *testing.T, characters ...string) (*store.Store, *domain.ChapterWorldSimulation, string) {
	t.Helper()
	st, _, sim := newPhysicalArbitratedStoreForTest(t, characters...)
	saveRewriteCraftIndex(t, st, "independent-plan-fixture")
	state, _ := installPlanningContextAccessProjectAll(t, st, 1, "independent-planning-test")
	state.GenerationID = sim.GenerationID
	var err error
	state.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(st.Dir(), projectAllStateContextPath), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := domain.ProjectedPlanningContextSourceTokenV2(state.ContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	sim.Sources = append(sim.Sources, token)
	sim.SimulationID = chapterWorldSimulationID(*sim)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	if err := ensureChapterWorldSimulationCheckpoint(st, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "核验", CoreEvent: "旧软大纲", Hook: "旧软钩子"}}); err != nil {
		t.Fatal(err)
	}
	return st, sim, token
}

func TestIndependentSimulationWithoutLegacyCastBindsRealPlanningTools(t *testing.T) {
	st, sim, token := independentPlanningFixture(t)
	cast, err := st.WorldSim.LoadSimulationCast()
	if err != nil || len(cast.Assignments) != 0 {
		t.Fatal("test must have no legacy cast")
	}
	required, ready, gaps := ChapterWorldSimulationStatus(st, 1)
	if !required || !ready || len(gaps) != 0 {
		t.Fatalf("independent receipt was optional/invalid: %t %t %v", required, ready, gaps)
	}
	result := map[string]any{}
	NewContextTool(st, References{}, "").buildChapterWorldSimulationContext(result, 1, func(name string, err error) {
		if err != nil {
			t.Errorf("context warning %s: %v", name, err)
		}
	})
	view, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok || view["status"] != "ready" || view["simulation_id"] != sim.SimulationID {
		t.Fatalf("planning context omitted v2 receipt: %v", view)
	}
	if _, err := NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1)); err != nil {
		t.Fatal(err)
	}
	partial, _ := st.Drafts.LoadChapterPlanPartial(1)
	if partial["structure"].(map[string]any)[planStructureWorldSimulationKey] != sim.SimulationID {
		t.Fatal("plan_structure failed to stamp independent simulation")
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": map[string]any{"context_sources": []string{token}, "project_promise": "已付费字段"}})
	if _, err := NewPlanDetailsTool(st).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	partial, _ = st.Drafts.LoadChapterPlanPartial(1)
	merged := partial["causal_simulation"].(map[string]any)
	if merged["world_simulation_id"] != sim.SimulationID || merged["protagonist_decision"] != effectiveProtagonistDecision(sim.ProtagonistProjection) {
		t.Fatal("plan_details did not fill host source anchors")
	}
	plan := domain.ChapterPlan{Chapter: 1, CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: effectiveProtagonistDecision(sim.ProtagonistProjection), ContextSources: []string{token, "chapter_world_simulation:" + sim.SimulationID}}}
	if err := validateChapterWorldSimulationReference(st, plan); err != nil {
		t.Fatal(err)
	}
	plan.CausalSimulation.WorldSimulationID = ""
	if err := validateChapterWorldSimulationReference(st, plan); err == nil {
		t.Fatal("empty cast bypassed v2 reference validation")
	}
}

func TestIndependentMissingPlanStampNeedsReplanAndPreservesAudit(t *testing.T) {
	for _, kind := range []string{"recover", "recover_via_structure", "wrong_stamp", "wrong_rewrite_stamp", "wrong_causal_id", "wrong_decision", "different_context", "missing_context", "wrong_chapter", "foreign_lease", "invalid_simulation"} {
		t.Run(kind, func(t *testing.T) {
			st, sim, token := independentPlanningFixture(t)
			partial := map[string]any{"structure": map[string]any{"chapter": 1, "title": "已付费标题", "goal": "已付费目标", "conflict": "已付费阻力", "hook": "已付费钩子"}, "causal_simulation": map[string]any{"context_sources": []string{token}, "project_promise": "保留原承诺", "world_rules_in_force": []string{"保留原规则与边界"}}}
			structure := partial["structure"].(map[string]any)
			merged := partial["causal_simulation"].(map[string]any)
			switch kind {
			case "wrong_stamp":
				structure[planStructureWorldSimulationKey] = "another-simulation"
			case "wrong_rewrite_stamp":
				structure[planStructureRewriteSHAKey] = "different-body-source"
			case "wrong_causal_id":
				merged["world_simulation_id"] = "another-simulation"
			case "wrong_decision":
				merged["protagonist_decision"] = "另一项选择"
			case "different_context":
				merged["context_sources"] = []string{token, "project-all-state:sha256:" + strings.Repeat("0", 64)}
			case "missing_context":
				merged["context_sources"] = []string{"unbound"}
			case "wrong_chapter":
				structure["chapter"] = 2
			case "foreign_lease":
				lease, _ := st.Runtime.InspectPipelineExecution()
				lease.ProcessID = os.Getpid() + 100000
				raw, _ := json.Marshal(lease)
				if err := os.WriteFile(filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json"), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "invalid_simulation":
				sim.CharacterDecisions[0].Action = "tampered"
				if err := st.SaveChapterWorldSimulation(*sim); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Drafts.SaveChapterPlanPartial(1, partial); err != nil {
				t.Fatal(err)
			}
			before, _ := st.Drafts.LoadChapterPlanPartial(1)
			if kind == "recover" {
				view, ok, viewErr := NewContextTool(st, References{}, "").stagedPlanRepairContext(1, 1, false)
				if viewErr != nil || !ok || view["structure_source_status"] != "needs_replan" {
					t.Fatalf("context still calls an unattested old partial ready: %v %v", view, viewErr)
				}
			}
			args := json.RawMessage(`{"chapter":1,"causal_simulation":{"chapter_function":"新一批合法补充"}}`)
			var err error
			if kind == "recover_via_structure" {
				_, err = NewPlanStructureTool(st).Execute(context.Background(), planStructureArgs(1))
				if err == nil {
					_, err = NewPlanDetailsTool(st).Execute(context.Background(), args)
				}
			} else {
				_, err = NewPlanDetailsTool(st).Execute(context.Background(), args)
			}
			after, _ := st.Drafts.LoadChapterPlanPartial(1)
			if err == nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("unattested or inconsistent partial was accepted/changed: %v", err)
			}
			if (kind == "recover" || kind == "recover_via_structure") && !errors.Is(err, ErrIndependentPlanNeedsReplan) {
				t.Fatalf("same source identity did not produce explicit needs_replan: %v", err)
			}
		})
	}
}

func TestIndependentPlanningActualReadonlyBindingAudit(t *testing.T) {
	dir := os.Getenv("NOVEL_STUDIO_INDEPENDENT_PLAN_AUDIT_DIR")
	if dir == "" {
		t.Skip("set an explicit stopped workspace for read-only audit")
	}
	before, err := store.DirectoryContentRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	sim, err := ensureChapterWorldSimulationReadyForPlanning(st, 1)
	if err != nil || sim == nil || sim.Version != 2 {
		t.Fatalf("real independent simulation is not planning-ready: %v", err)
	}
	required, ready, gaps := ChapterWorldSimulationStatus(st, 1)
	if !required || !ready || len(gaps) != 0 {
		t.Fatalf("real empty-cast status: %t %t %v", required, ready, gaps)
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(1)
	if err != nil || partial == nil {
		t.Fatal("real paid partial absent")
	}
	raw, err := os.ReadFile(filepath.Join(dir, projectAllStateContextPath))
	if err != nil {
		t.Fatal(err)
	}
	var state domain.ProjectedPlanningContextV2
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedPlanningContextV2(state); err != nil {
		t.Fatal(err)
	}
	token, err := domain.ProjectedPlanningContextSourceTokenV2(state.ContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	if state.GenerationID != sim.GenerationID || state.NextChapter != 1 {
		t.Fatal("real context differs from simulation")
	}
	if !projectAllStateSourcesContain(sim.Sources, token) || !projectAllStateSourcesContain(stringSliceFromAny(partial["causal_simulation"].(map[string]any)["context_sources"]), token) {
		t.Fatal("real partial and simulation lack the same exact context token")
	}
	if err := validateIndependentPlanCausalBindings(partial["causal_simulation"].(map[string]any), sim); err != nil {
		t.Fatal(err)
	}
	after, err := store.DirectoryContentRoot(dir)
	if err != nil || before != after {
		t.Fatal("read-only binding audit changed actual files")
	}
}
