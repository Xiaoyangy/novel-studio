package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestActualChapterTwoPlanningProjectionFitsBudget(t *testing.T) {
	source := os.Getenv("NOVEL_ACTUAL_SHADOW_READONLY")
	if source == "" {
		t.Skip("actual source not configured")
	}
	target := t.TempDir()
	if err := os.CopyFS(target, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(target)
	refs := assets.Load("default").References
	raw, err := tools.NewContextTool(st, refs, "default").Execute(context.Background(), json.RawMessage(`{"chapter":2,"profile":"planning"}`))
	if err != nil {
		t.Fatalf("actual chapter-two planning context cannot be delivered: %v", err)
	}
	if len(raw) > 160*1024 {
		t.Fatalf("budget exceeded: %d", len(raw))
	}
	sourceBytes, err := os.ReadFile(filepath.Join(source, "meta/chapter_simulations/002.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sim domain.ChapterWorldSimulation
	if err := json.Unmarshal(sourceBytes, &sim); err != nil {
		t.Fatal(err)
	}
	digest, err := domain.DeterministicPlanningHash(sim)
	if err != nil {
		t.Fatal(err)
	}
	binding := modelinput.PlanningProjectionSourceBinding{SimulationID: sim.SimulationID, SimulationDigest: "sha256:" + digest}
	var contextBody struct {
		World struct {
			Projection json.RawMessage `json:"protagonist_projection"`
		} `json:"chapter_world_simulation"`
	}
	if err := json.Unmarshal(raw, &contextBody); err != nil {
		t.Fatal(err)
	}
	decoded, err := modelinput.DecodePlanningProjectionModelViewV1(contextBody.World.Projection, binding)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(sim.ProtagonistProjection)
	var fields map[string]json.RawMessage
	json.Unmarshal(original, &fields)
	original, _ = json.Marshal(fields)
	if !bytes.Equal(decoded, original) {
		t.Fatal("actual full projection changed, not lossless")
	}
	after, err := os.ReadFile(filepath.Join(source, "meta/chapter_simulations/002.json"))
	if err != nil || !bytes.Equal(after, sourceBytes) {
		t.Fatal("source simulation changed")
	}
	if capture := os.Getenv("NOVEL_PLANNER_CONTEXT_CAPTURE"); capture != "" {
		if err := os.WriteFile(capture, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual context bytes=%d", len(raw))
}
