package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/rules"
)

func TestPlanDetailsLongformGapPrecedesFinalize(t *testing.T) {
	st := newPhaseTestStore(t)
	if err := st.Progress.Init("longform", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlanStructureTool(st).Execute(t.Context(), planStructureArgs(1)); err != nil {
		t.Fatal(err)
	}
	merged := testCausalSimulation(false)
	opening := merged["longform_opening"]
	delete(merged, "longform_opening")
	args, _ := json.Marshal(map[string]any{"chapter": 1, "causal_simulation": merged})
	raw, err := NewPlanDetailsTool(st).Execute(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	var staged map[string]any
	if err := json.Unmarshal(raw, &staged); err != nil {
		t.Fatal(err)
	}
	assertGap := func(view map[string]any) {
		t.Helper()
		gaps := strings.Join(stringSliceFromAny(view["gap_summary"]), " | ")
		batches := strings.Join(stringSliceFromAny(view["recommended_batches"]), " | ")
		if !strings.Contains(gaps, "longform_opening") || !strings.Contains(batches, "本章当前必补：longform_opening") {
			t.Errorf("required opening hidden until finalize: gaps=%s batches=%s", gaps, batches)
		}
	}
	assertGap(staged)
	view, ok, err := NewContextTool(st, References{}, "").stagedPlanRepairContext(1, 1, false)
	if err != nil || !ok {
		t.Fatalf("staged context: ok=%t err=%v", ok, err)
	}
	assertGap(view)
	if _, err := NewPlanDetailsTool(st).Execute(t.Context(), json.RawMessage(`{"chapter":1,"finalize":true}`)); err == nil || !strings.Contains(err.Error(), "causal_simulation.longform_opening") {
		t.Fatalf("existing final acceptance requirement changed: %v", err)
	}
	args, _ = json.Marshal(map[string]any{"chapter": 1, "causal_simulation": map[string]any{"longform_opening": opening}, "finalize": true})
	if _, err := NewPlanDetailsTool(st).Execute(t.Context(), args); err != nil {
		t.Fatalf("minimal opening patch failed existing finalizer: %v", err)
	}
	if plan, err := st.Drafts.LoadChapterPlan(1); err != nil || plan == nil {
		t.Fatalf("no real saved plan after patch: %v", err)
	}
}

func TestPlanDetailsLongformGapMatchesChapterScopeAndCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total       int
		chapter     int
		preferences string
		state       string
		want        bool
	}{
		{"below threshold", 49, 1, "", "absent", false},
		{"threshold", 50, 1, "", "absent", true},
		{"later chapter", 100, 2, "", "absent", false},
		{"explicit longform", 3, 1, "长篇小说", "absent", true},
		{"neutral mention", 3, 1, "无论长篇还是短篇，统一采用第三人称", "absent", false},
		{"null", 100, 1, "", "null", true},
		{"incomplete", 100, 1, "", "incomplete", true},
		{"complete", 100, 1, "", "complete", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newPhaseTestStore(t)
			if err := st.Progress.Init(tc.name, tc.total); err != nil {
				t.Fatal(err)
			}
			if err := st.UserRules.Save(&rules.Snapshot{Preferences: tc.preferences}); err != nil {
				t.Fatal(err)
			}
			partial := map[string]any{"structure": map[string]any{"chapter": tc.chapter}}
			merged := testCausalSimulation(false)
			switch tc.state {
			case "absent":
				delete(merged, "longform_opening")
			case "null":
				merged["longform_opening"] = nil
			case "incomplete":
				merged["longform_opening"] = map[string]any{"target_reader": "长篇读者"}
			}
			before, _ := json.Marshal(merged)
			gaps := strings.Join(planDetailsGapSummary(st, tc.chapter, partial, merged), " | ")
			batches := strings.Join(planDetailsRecommendedBatchesForState(st, tc.chapter, partial, merged), " | ")
			if strings.Contains(gaps, "longform_opening") != tc.want || strings.Contains(batches, "本章当前必补：longform_opening") != tc.want {
				t.Fatalf("want missing=%t; gaps=%s batches=%s", tc.want, gaps, batches)
			}
			after, _ := json.Marshal(merged)
			if string(before) != string(after) {
				t.Fatal("diagnostic changed model-authored fields")
			}
		})
	}
}
