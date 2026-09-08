package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func projectedStateViewFixture(t *testing.T, chapter int) domain.ProjectedPlanningContextV2 {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	state := domain.ProjectedPlanningContextV2{
		Version: domain.ProjectedPlanningContextV2Version, GenerationID: "pg2_view_test",
		NextChapter: chapter, ThroughChapter: chapter - 1, StateRoot: digest,
	}
	if chapter > 1 {
		state.CumulativeState = []domain.ProjectedPlanningStateFactV2{{
			Category: "timeline", StableID: "clock", Subject: "world", Field: "story_day", Value: "0.0125", ThroughChapter: chapter - 1,
		}}
		state.RecentTransitions = []domain.ProjectedPlanningTransitionV2{{
			Chapter: chapter - 1, BundleDigest: digest, ProjectedPostStateRoot: digest,
			Delta: domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version, Timeline: []domain.StateMutationV2{{
				StableID: "clock", Subject: "world", Field: "story_day", Operation: "advance", After: "0.0125", Cause: "accepted action elapsed time", Evidence: "completed receipt",
			}}},
		}}
	}
	var err error
	state.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedPlanningContextV2(state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestProjectedStateViewsCannotMasqueradeAsCanonicalDigest(t *testing.T) {
	for _, chapter := range []int{1, 2} {
		for _, profile := range []string{"world_simulation", "planning", "full"} {
			for _, representation := range []string{"value", "pointer", "map"} {
				t.Run(strings.Join([]string{string(rune('0' + chapter)), profile, representation}, "/"), func(t *testing.T) {
					state := projectedStateViewFixture(t, chapter)
					var value any = state
					if representation == "pointer" {
						value = &state
					}
					if representation == "map" {
						raw, _ := json.Marshal(state)
						var mapped map[string]any
						if err := json.Unmarshal(raw, &mapped); err != nil {
							t.Fatal(err)
						}
						value = mapped
					}
					before, _ := json.Marshal(value)
					token, _ := domain.ProjectedPlanningContextSourceTokenV2(state.ContextDigest)
					result := map[string]any{"project_all_state": value, "project_all_state_source_token": token, "chapter_world_simulation": map[string]any{"status": "ready"}}
					raw, err := finalizeContextResult(result, chapter, profile)
					if err != nil {
						t.Fatal(err)
					}
					var envelope struct {
						State json.RawMessage `json:"project_all_state"`
						Token string          `json:"project_all_state_source_token"`
					}
					if err := json.Unmarshal(raw, &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Token != token {
						t.Fatal("display compaction changed the canonical source token")
					}
					var decoded domain.ProjectedPlanningContextV2
					if err := json.Unmarshal(envelope.State, &decoded); err != nil {
						t.Fatal(err)
					}
					if profile == "full" {
						if err := domain.ValidateProjectedPlanningContextV2(decoded); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(state, decoded) {
							t.Fatal("full profile modified canonical state")
						}
					} else {
						var view map[string]any
						_ = json.Unmarshal(envelope.State, &view)
						if _, exists := view["context_digest"]; exists {
							t.Fatal("cropped JSON still claims the canonical digest")
						}
						if view["version"] != projectedPlanningContextViewVersion || view["source_version"] != state.Version || view["source_context_digest"] != state.ContextDigest {
							t.Fatalf("display source binding lost: %+v", view)
						}
						if err := domain.ValidateProjectedPlanningContextV2(decoded); err == nil {
							t.Fatal("display projection was accepted as exact canonical authority")
						}
						// Applying a profile again must not relabel a view digest as
						// the source digest or mutate the original map supplied by a caller.
						applyChapterContextProfile(result, profile)
						if result["project_all_state"].(map[string]any)["source_context_digest"] != state.ContextDigest {
							t.Fatal("repeated projection changed its source binding")
						}
					}
					after, _ := json.Marshal(value)
					if string(before) != string(after) {
						t.Fatal("display projection mutated the source object")
					}
				})
			}
		}
	}
}

func TestWorldSimulationContextAndExactStateReaderShareSourceBinding(t *testing.T) {
	st := newPhaseTestStore(t)
	want, token := installPlanningContextAccessProjectAll(t, st, 1, "exact-state-view-test")
	if want.RecentTransitions != nil {
		t.Fatal("fixture must cover the nil-to-empty first chapter regression")
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(projectAllStateContextPath))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := NewContextTool(st, References{}, "").Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		State map[string]any `json:"project_all_state"`
		Token string         `json:"project_all_state_source_token"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token != token || payload.State["source_context_digest"] != want.ContextDigest || payload.State["version"] != projectedPlanningContextViewVersion {
		t.Fatalf("context view claims incorrect authority: %+v", payload)
	}
	exact, exactToken, err := LoadProjectAllStateForExecution(st, 1)
	if err != nil || exact == nil {
		t.Fatalf("exact reader failed after novel_context: state=%+v err=%v", exact, err)
	}
	if exactToken != token || !reflect.DeepEqual(*exact, want) {
		t.Fatal("exact reader returned a cropped or rebound context")
	}
	if err := domain.ValidateProjectedPlanningContextV2(*exact); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("context view generation rewrote canonical authority")
	}
}
