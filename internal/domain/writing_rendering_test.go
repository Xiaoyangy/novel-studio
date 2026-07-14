package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func validLiteraryRenderingPlan() *LiteraryRenderingPlan {
	return &LiteraryRenderingPlan{
		Focalizer:             "林砚",
		NarrativeAccess:       LiteraryNarrativeAccessMixed,
		KnowledgeBoundary:     "只写林砚看见的名册、钟声和他据此做出的判断",
		PerceptualBias:        "先注意登记漏洞，再注意旁人的脸色",
		SummaryOmissionPolicy: "登记队伍只概述人数变化，跳过不改变状态的重复核验",
		Afterimage:            "名册缺口被夜风掀起，又压回林砚指下",
		SourceRefs: []string{
			"literary-rendering#focalization-boundary",
			"https://example.com/craft/focalization",
		},
		SceneModes: []LiterarySceneRenderingMode{{
			Target:      "林砚发现名册异样",
			Mode:        LiterarySceneModeScene,
			Distance:    LiteraryNarrativeDistanceClose,
			StateChange: "他从等待登记变为主动保留证据",
			RenderMove:  "让指腹停在纸张缺口，不先解释幕后安排",
		}},
		ActiveLenses: []LiteraryRenderingLens{{
			Kind:       "weathered-household-object",
			Target:     "缺角名册",
			Move:       "用纸边磨损和新墨渗痕形成时间差",
			Why:        "让异常从可见物证进入主角判断",
			Avoid:      "旁白直接宣布名册被人调换",
			SourceRefs: []string{"rag-trace:craft-42"},
		}},
	}
}

func TestChapterCausalSimulationLiteraryRenderingJSONCompatibility(t *testing.T) {
	legacyJSON, err := json.Marshal(ChapterCausalSimulation{})
	if err != nil {
		t.Fatalf("marshal legacy simulation: %v", err)
	}
	if strings.Contains(string(legacyJSON), "literary_rendering_plan") {
		t.Fatalf("nil literary plan must be omitted, got %s", legacyJSON)
	}

	var legacy ChapterCausalSimulation
	if err := json.Unmarshal([]byte(`{"project_promise":"旧计划"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy simulation: %v", err)
	}
	if legacy.LiteraryRendering != nil {
		t.Fatalf("legacy JSON should leave literary rendering nil: %+v", legacy.LiteraryRendering)
	}

	want := ChapterCausalSimulation{LiteraryRendering: validLiteraryRenderingPlan()}
	roundTripJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal literary simulation: %v", err)
	}
	var got ChapterCausalSimulation
	if err := json.Unmarshal(roundTripJSON, &got); err != nil {
		t.Fatalf("unmarshal literary simulation: %v", err)
	}
	if got.LiteraryRendering == nil || got.LiteraryRendering.Focalizer != "林砚" {
		t.Fatalf("literary plan did not round-trip: %+v", got.LiteraryRendering)
	}
	if got.LiteraryRendering.ActiveLenses[0].Kind != "weathered-household-object" {
		t.Fatalf("open lens kind did not round-trip: %+v", got.LiteraryRendering.ActiveLenses)
	}
}

func TestLiteraryRenderingPlanValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*LiteraryRenderingPlan)
		wantErr string
	}{
		{name: "valid open lens and mixed source refs"},
		{
			name: "empty scene and lens arrays are allowed",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SceneModes = nil
				plan.ActiveLenses = nil
			},
		},
		{
			name: "invalid narrative access",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.NarrativeAccess = "omniscient"
			},
			wantErr: "narrative_access",
		},
		{
			name: "invalid scene mode",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SceneModes[0].Mode = "montage"
			},
			wantErr: "scene_modes[0].mode",
		},
		{
			name: "invalid narrative distance",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SceneModes[0].Distance = "remote"
			},
			wantErr: "scene_modes[0].distance",
		},
		{
			name: "missing scene item field",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SceneModes[0].RenderMove = " "
			},
			wantErr: "scene_modes[0].render_move",
		},
		{
			name: "missing lens item field",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.ActiveLenses[0].Why = ""
			},
			wantErr: "active_lenses[0].why",
		},
		{
			name: "top level provenance required",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SourceRefs = nil
			},
			wantErr: "source_refs must contain at least one",
		},
		{
			name: "top level provenance unique after trimming",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.SourceRefs = []string{"literary-rendering#scene-summary", " literary-rendering#scene-summary "}
			},
			wantErr: "duplicates",
		},
		{
			name: "lens provenance required",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.ActiveLenses[0].SourceRefs = nil
			},
			wantErr: "active_lenses[0].source_refs must contain at least one",
		},
		{
			name: "blank provenance rejected",
			mutate: func(plan *LiteraryRenderingPlan) {
				plan.ActiveLenses[0].SourceRefs = []string{" "}
			},
			wantErr: "active_lenses[0].source_refs[0] must be nonempty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := validLiteraryRenderingPlan()
			if tc.mutate != nil {
				tc.mutate(plan)
			}
			err := plan.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}

	var nilPlan *LiteraryRenderingPlan
	if err := nilPlan.Validate(); err != nil {
		t.Fatalf("nil optional plan should validate: %v", err)
	}
}
