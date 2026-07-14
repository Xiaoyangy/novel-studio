package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func literaryRenderingPlanArgs() map[string]any {
	return map[string]any{
		"focalizer":               "林砚",
		"narrative_access":        "mixed",
		"knowledge_boundary":      "只写林砚能看见的名册、钟声和由此产生的判断",
		"perceptual_bias":         "先找登记漏洞，再看旁人反应",
		"summary_omission_policy": "重复核验只概述人数变化，不逐项复述流程",
		"afterimage":              "缺角名册在钟声里压回林砚指下",
		"source_refs": []string{
			"literary-rendering#focalization-boundary",
			"https://example.com/craft/focalization",
		},
		"scene_modes": []map[string]any{{
			"target":       "林砚发现名册被动过",
			"mode":         "scene",
			"distance":     "close",
			"state_change": "林砚从等待登记变为主动保留证据",
			"render_move":  "让手指停在缺口，不由旁白解释幕后安排",
		}},
		"active_lenses": []map[string]any{{
			"kind":        "weathered-household-object",
			"target":      "缺角名册",
			"move":        "用纸边磨损和新墨渗痕形成时间差",
			"why":         "让异常经可见物证进入主角判断",
			"avoid":       "旁白直接宣布名册被人调换",
			"source_refs": []string{"rag-trace:craft-42"},
		}},
	}
}

func newLiteraryRenderingPlanStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	return st
}

func executeLiteraryRenderingPlan(t *testing.T, st *store.Store, rendering any) error {
	t.Helper()
	sim := testCausalSimulation(false)
	if rendering != nil {
		sim["literary_rendering_plan"] = rendering
	}
	args, err := json.Marshal(map[string]any{
		"chapter":           1,
		"title":             "名册缺口",
		"goal":              "确认登记异常并保留证据",
		"conflict":          "钟声倒计时逼林砚立即签名",
		"hook":              "新墨从名册背面洇出来",
		"causal_simulation": sim,
	})
	if err != nil {
		t.Fatalf("Marshal args: %v", err)
	}
	_, err = NewPlanChapterTool(st).Execute(context.Background(), args)
	return err
}

func TestPlanChapterSchemaIncludesOptionalLiteraryRenderingPlan(t *testing.T) {
	root := NewPlanChapterTool(nil).Schema()
	rootProperties := root["properties"].(map[string]any)
	causal := rootProperties["causal_simulation"].(map[string]any)
	causalProperties := causal["properties"].(map[string]any)
	rendering, ok := causalProperties["literary_rendering_plan"].(map[string]any)
	if !ok {
		t.Fatal("causal_simulation schema missing literary_rendering_plan")
	}
	for _, required := range causal["required"].([]string) {
		if required == "literary_rendering_plan" {
			t.Fatal("literary_rendering_plan must remain optional for legacy plans")
		}
	}

	properties := rendering["properties"].(map[string]any)
	access := properties["narrative_access"].(map[string]any)
	if got, want := access["enum"], []string{"internal", "external", "mixed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("narrative_access enum = %#v, want %#v", got, want)
	}
	sceneModes := properties["scene_modes"].(map[string]any)
	if _, hasMinimum := sceneModes["minItems"]; hasMinimum {
		t.Fatal("scene_modes must not have a quantity floor")
	}
	sceneProperties := sceneModes["items"].(map[string]any)["properties"].(map[string]any)
	if got, want := sceneProperties["mode"].(map[string]any)["enum"], []string{"scene", "summary", "omission", "pause"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scene mode enum = %#v, want %#v", got, want)
	}
	if got, want := sceneProperties["distance"].(map[string]any)["enum"], []string{"close", "medium", "far"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("distance enum = %#v, want %#v", got, want)
	}
	activeLenses := properties["active_lenses"].(map[string]any)
	if _, hasMinimum := activeLenses["minItems"]; hasMinimum {
		t.Fatal("active_lenses must not have a quantity floor")
	}
	lensKind := activeLenses["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
	if _, closed := lensKind["enum"]; closed {
		t.Fatal("active_lenses.kind must remain an open string")
	}

	sourceDescription := properties["source_refs"].(map[string]any)["description"].(string)
	if !strings.Contains(sourceDescription, "literary-rendering#<card_id>") {
		t.Fatalf("schema should describe stable card reference format: %s", sourceDescription)
	}
}

func TestPlanChapterPersistsLiteraryRenderingPlan(t *testing.T) {
	st := newLiteraryRenderingPlanStore(t)
	if err := executeLiteraryRenderingPlan(t, st, literaryRenderingPlanArgs()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil {
		t.Fatalf("LoadChapterPlan: %v", err)
	}
	if plan == nil || plan.CausalSimulation.LiteraryRendering == nil {
		t.Fatalf("saved plan missing literary rendering: %+v", plan)
	}
	rendering := plan.CausalSimulation.LiteraryRendering
	if rendering.Focalizer != "林砚" || rendering.NarrativeAccess != "mixed" {
		t.Fatalf("unexpected focalization fields: %+v", rendering)
	}
	if len(rendering.ActiveLenses) != 1 || rendering.ActiveLenses[0].Kind != "weathered-household-object" {
		t.Fatalf("open lens kind was not preserved: %+v", rendering.ActiveLenses)
	}
	if len(rendering.SceneModes) != 1 || rendering.SceneModes[0].Mode != "scene" || rendering.SceneModes[0].Distance != "close" {
		t.Fatalf("scene rendering mode was not preserved: %+v", rendering.SceneModes)
	}
}

func TestPlanChapterAllowsLegacyPlanWithoutLiteraryRendering(t *testing.T) {
	st := newLiteraryRenderingPlanStore(t)
	if err := executeLiteraryRenderingPlan(t, st, nil); err != nil {
		t.Fatalf("legacy plan without literary rendering should pass: %v", err)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil {
		t.Fatalf("LoadChapterPlan: %v", err)
	}
	if plan.CausalSimulation.LiteraryRendering != nil {
		t.Fatalf("legacy plan unexpectedly gained literary rendering: %+v", plan.CausalSimulation.LiteraryRendering)
	}
}

func TestPlanChapterRejectsInvalidLiteraryRenderingPlan(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "closed narrative access",
			mutate: func(plan map[string]any) {
				plan["narrative_access"] = "omniscient"
			},
			wantErr: "narrative_access",
		},
		{
			name: "duplicate provenance",
			mutate: func(plan map[string]any) {
				plan["source_refs"] = []string{"literary-rendering#scene-summary", " literary-rendering#scene-summary "}
			},
			wantErr: "duplicates",
		},
		{
			name: "incomplete lens",
			mutate: func(plan map[string]any) {
				lenses := plan["active_lenses"].([]map[string]any)
				delete(lenses[0], "move")
			},
			wantErr: "active_lenses[0].move",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newLiteraryRenderingPlanStore(t)
			rendering := literaryRenderingPlanArgs()
			tc.mutate(rendering)
			err := executeLiteraryRenderingPlan(t, st, rendering)
			if err == nil || !strings.Contains(err.Error(), "literary_rendering_plan") || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Execute error = %v, want literary_rendering_plan and %q", err, tc.wantErr)
			}
		})
	}
}
