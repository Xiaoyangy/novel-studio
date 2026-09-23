package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCaptureActualChapterTwoGroundingInput(t *testing.T) {
	source, capture := os.Getenv("NOVEL_GROUNDING_SHADOW_READONLY"), os.Getenv("NOVEL_GROUNDING_INPUT_CAPTURE")
	if source == "" || capture == "" {
		t.Skip("explicit readonly source/capture required")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 2, Owner: "grounding-capture-probe", ExpiresAt: time.Now().UTC().Add(20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	partial, err := st.Drafts.LoadChapterPlanPartial(2)
	if err != nil || partial == nil {
		t.Fatalf("partial: %v", err)
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	plan, err := chapterPlanFromPartial(2, partial, merged)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || first == nil || first.GroundingReview == nil {
		t.Fatalf("original reviewer protocol unavailable: %v", err)
	}
	captured := false
	stop := errors.New("offline capture before provider/publication")
	reviewer := PlanGroundingReviewer{Protocol: first.GroundingReview.ReviewProtocol, Review: func(_ context.Context, input domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(capture, raw, 0600); err != nil {
			t.Fatal(err)
		}
		captured = true
		t.Logf("captured actual input bytes=%d cycles=%d", len(raw), len(input.Activation.Cycles))
		return domain.PlanGroundingVerdict{}, stop
	}}
	_, err = finalizeChapterPlan(st, plan, false, planGroundingExecution{t.Context(), reviewer})
	if !errors.Is(err, stop) || !captured {
		t.Fatalf("did not reach exact grounding input: %v", err)
	}
}
