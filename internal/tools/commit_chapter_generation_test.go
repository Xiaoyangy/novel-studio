package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCurrentGenerationIDUsesExactSealedRenderBinding(t *testing.T) {
	const (
		progressGeneration = "progress-generation"
		planningGeneration = "pg2_planning_generation"
		lockedPlanDigest   = "sha256:locked-plan"
	)
	tests := []struct {
		name             string
		markerChapter    int
		markerPlanDigest string
		want             string
	}{
		{
			name:             "exact frozen marker uses planning generation",
			markerChapter:    3,
			markerPlanDigest: lockedPlanDigest,
			want:             planningGeneration,
		},
		{
			name:             "marker and render lock mismatch falls back to progress",
			markerChapter:    3,
			markerPlanDigest: "sha256:different-plan",
			want:             progressGeneration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			if err := st.Progress.Save(&domain.Progress{
				NovelName:    "generation binding test",
				GenerationID: progressGeneration,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
				Mode:          domain.PipelineExecutionRender,
				TargetChapter: 3,
				PlanDigest:    lockedPlanDigest,
				Owner:         "commit-generation-test",
			}); err != nil {
				t.Fatal(err)
			}
			writeCurrentGenerationFrozenMarker(
				t,
				st.Dir(),
				tt.markerChapter,
				tt.markerPlanDigest,
				planningGeneration,
			)

			if got := currentGenerationID(st); got != tt.want {
				t.Fatalf("currentGenerationID() = %q want %q", got, tt.want)
			}
		})
	}
}

func writeCurrentGenerationFrozenMarker(
	t *testing.T,
	root string,
	chapter int,
	planDigest string,
	planningGenerationID string,
) {
	t.Helper()
	path := filepath.Join(root, "meta", "planning", "current_frozen_plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"chapter":                chapter,
		"plan_digest":            planDigest,
		"projection_binding":     "sealed_v2",
		"planning_generation_id": planningGenerationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
