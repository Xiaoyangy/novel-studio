package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// Opt-in, stopped-source regression. Every write goes to CopyFS(t.TempDir()).
// The normal grounding transport uses a fake CLI verdict, so this proves paid
// partial recovery/transport/persistence, NOT narrative or semantic acceptance.
func TestActualChapterTwoPartialResumeThroughGroundingCLI(t *testing.T) {
	source := os.Getenv("NOVEL_PARTIAL_RESUME_ACTUAL_SOURCE")
	if source == "" {
		t.Skip("requires explicit stopped read-only C2 shadow source")
	}
	simPath := filepath.Join(source, "meta/chapter_simulations/002.json")
	partialPath := filepath.Join(source, "drafts/02.plan.partial.json")
	sourceSim, err := os.ReadFile(simPath)
	if err != nil {
		t.Fatal(err)
	}
	sourcePartial, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	partial, err := st.Drafts.LoadChapterPlanPartial(2)
	if err != nil || partial == nil {
		t.Fatalf("missing real paid partial: %v", err)
	}
	causalRaw, _ := json.Marshal(partial["causal_simulation"])
	var expected domain.ChapterCausalSimulation
	if err := json.Unmarshal(causalRaw, &expected); err != nil {
		t.Fatal(err)
	}
	first, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || first == nil || first.GroundingReview == nil {
		t.Fatalf("missing original reviewer protocol: %v", err)
	}
	const owner = "partial-resume-offline-transport"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 2, Owner: owner, ExpiresAt: time.Now().UTC().Add(20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil {
			t.Error(err)
		}
	})
	bundle := assets.Load("default")
	packet, err := tools.NewContextTool(st, bundle.References, "default").Execute(t.Context(), json.RawMessage(`{"chapter":2,"profile":"planning"}`))
	if err != nil {
		t.Fatal(err)
	}
	binary, calls, _, _ := groundingBudgetFakeCLI(t)
	model := llmcodex.New(binary, "partial-resume-offline", "high", llmcodex.WithContextWindow(272000))
	var originalInput []byte
	reviews := 0
	reviewer := tools.PlanGroundingReviewer{Protocol: first.GroundingReview.ReviewProtocol, Review: func(ctx context.Context, input domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		reviews++
		originalInput, err = json.Marshal(input)
		if err != nil {
			return domain.PlanGroundingVerdict{}, err
		}
		return runPlanGroundingReview(ctx, model, agentcore.ThinkingHigh, input)
	}}
	result, attempted, err := tools.NewPlanDetailsTool(st).WithGroundingReviewer(reviewer).ResumePartial(t.Context(), 2, packet)
	if err != nil || !attempted {
		t.Fatalf("real six-batch partial could not resume: attempted=%v err=%v", attempted, err)
	}
	var outcome struct {
		Planned bool `json:"planned"`
	}
	if err := json.Unmarshal(result, &outcome); err != nil || !outcome.Planned {
		t.Fatalf("not finalized: %s %v", result, err)
	}
	plan, cp, err := loadCurrentProjectedPlan(st, 2)
	if err != nil || plan == nil || cp == nil || plan.GroundingReview == nil {
		t.Fatalf("no verified formal plan/checkpoint: %v", err)
	}
	if after, _ := st.Drafts.LoadChapterPlanPartial(2); after != nil {
		t.Fatal("finalized partial not removed")
	}
	actual := plan.CausalSimulation
	expected.ContextSources = nil
	actual.ContextSources = nil
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("normal resume changed existing causal fields beyond access/source anchors")
	}
	structure := partial["structure"].(map[string]any)
	// The established independent-plan finalizer binds Goal to the accepted
	// character choice, even before this recovery path. Compare that canonical
	// anchor, not the model-authored staged Goal which normal finalization replaces.
	var originalSimulation domain.ChapterWorldSimulation
	if err := json.Unmarshal(sourceSim, &originalSimulation); err != nil {
		t.Fatal(err)
	}
	wantGoal := "落实本轮世界模拟后的主角选择：" + originalSimulation.ProtagonistProjection.ChosenDecision
	if plan.Title != structure["title"] || plan.Goal != wantGoal || plan.Conflict != structure["conflict"] || plan.Hook != structure["hook"] {
		t.Fatal("resume reauthored saved structure")
	}
	audit, err := st.LoadPlanGroundingAudit(plan.GroundingReview.InputDigest)
	if err != nil || audit == nil {
		t.Fatalf("no original grounding audit: %v", err)
	}
	auditInput, err := json.Marshal(audit.Input)
	if err != nil || !bytes.Equal(originalInput, auditInput) {
		t.Fatal("transport compaction changed canonical audit input")
	}
	copySim, err := os.ReadFile(filepath.Join(dir, "meta/chapter_simulations/002.json"))
	if err != nil || !bytes.Equal(sourceSim, copySim) {
		t.Fatal("resume changed character simulation")
	}
	for path, before := range map[string][]byte{simPath: sourceSim, partialPath: sourcePartial} {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("read-only source changed: %s", path)
		}
	}
	log, err := os.ReadFile(calls)
	if err != nil || reviews != 1 || strings.Count(string(log), "exec\n") != 1 {
		t.Fatalf("unexpected grounding calls: reviews=%d log=%q err=%v", reviews, log, err)
	}
	t.Logf("real C2 resumed through one fake grounding CLI; canonical_input_bytes=%d; no Planner or character calls; not a semantic pass", len(originalInput))
}
