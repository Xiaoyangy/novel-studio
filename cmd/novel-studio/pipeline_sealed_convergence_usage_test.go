package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestSealedConvergenceUsageAcceptsExactPromotedLiveController(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := preparePipelineRenderCandidate(st.Dir(), frozen)
	if err != nil {
		t.Fatal(err)
	}
	mustUseLegacyPipelineRenderCandidateForTest(t, candidate, frozen)
	for i := range 3 {
		body := fmt.Sprintf("第一章\n\n林澈收起第%d张回执，等脚步远去才重新开门。", i)
		cs := store.NewStore(candidate.OutputDir)
		if err := cs.Drafts.SaveDraft(1, body); err != nil {
			t.Fatal(err)
		}
		if _, err := cs.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "plan", "rerender-request", "draft", "edit"); err != nil {
			t.Fatal(err)
		}
		artifact, err := runDeepSeekAIJudge(&reviewCacheModel{response: pipelineRenderConvergenceBlockingResponse}, deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}, 1, body, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := saveDeepSeekAIJudgeCache(candidate.OutputDir, artifact); err != nil {
			t.Fatal(err)
		}
		if err := saveDraftDeepSeekAIJudge(candidate.OutputDir, artifact); err != nil {
			t.Fatal(err)
		}
		if _, err := syncPipelineRenderConvergence(cs); err != nil {
			t.Fatal(err)
		}
	}
	eligibility, err := inspectPipelineSealedConvergenceReplanEligibility(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	owner := pipelineExecutionOwner("convergence-replan", 1)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 1, Owner: owner, ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	ctx, scope, err := newPipelineSealedConvergenceUsage(bootstrap.Config{OutputDir: st.Dir()}, st, &eligibility.Intent)
	if err != nil {
		t.Fatal(err)
	}
	if ctx == nil || scope == nil {
		t.Fatal("exact controller did not bind accounting")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	state, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Overall.Input != 0 || state.Overall.Cost != 0 || len(state.PendingUsageCalls) != 0 {
		t.Fatal("pre-dispatch binding charged a request")
	}
}

func TestSealedConvergenceUsageRefusesCandidateAndUnverifiedLiveBeforeAccounting(t *testing.T) {
	live, _, candidate := pipelineRenderConvergenceFixture(t)
	if err := validatePipelineSealedConvergenceUsageRoot(store.NewStore(live)); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineSealedConvergenceUsageRoot(store.NewStore(candidate.OutputDir)); err == nil {
		t.Fatal("isolated candidate accepted as accounting owner")
	}
	st := store.NewStore(t.TempDir())
	marker := filepath.Join(st.Dir(), filepath.FromSlash(pipelineProjectAllWorkspaceManifestPath))
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineSealedConvergenceUsageRoot(st); err == nil {
		t.Fatal("project-all shadow accepted as cost owner")
	}
	st = store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	intent := &pipelineSealedConvergenceReplanIntent{SourceFrozen: pipelineFrozenPlan{Chapter: 1, PlanningGenerationID: "unverified"}}
	if _, _, err := newPipelineSealedConvergenceUsage(bootstrap.Config{OutputDir: st.Dir()}, st, intent); err == nil {
		t.Fatal("unbound live dispatch permitted")
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), store.UsageAuditPath)); !os.IsNotExist(err) {
		t.Fatalf("rejected controller wrote usage: %v", err)
	}
}

func TestSealedConvergenceGroundingBindingPreservesRestrictedToolBoundary(t *testing.T) {
	st := store.NewStore(t.TempDir())
	inner := tools.NewPlanDetailsTool(st)
	wrapped := newPipelineSealedConvergenceMutablePlanDetailsTool(inner, 5, pipelineSealedConvergenceSeededMutableKeys).(*pipelineSealedConvergenceMutablePlanDetailsTool)
	before := wrapped.Schema()
	calls := 0
	got, err := wrapped.WithPlanGroundingReviewer(tools.PlanGroundingReviewer{Protocol: "test", Review: func(context.Context, domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		calls++
		return domain.PlanGroundingVerdict{Pass: true}, nil
	}})
	if err != nil || got != wrapped {
		t.Fatalf("wrapper was replaced: %v", err)
	}
	if !reflect.DeepEqual(before, wrapped.Schema()) || wrapped.ReadOnly(nil) || wrapped.ConcurrencySafe(nil) || wrapped.Label() != inner.Label() {
		t.Fatal("reviewer widened schema or lost mutation metadata")
	}
	if _, err := wrapped.Execute(context.Background(), json.RawMessage(`{"chapter":5,"causal_simulation":{"world_simulation_id":"forged"},"finalize":true}`)); err == nil {
		t.Fatal("reviewer bypassed original allowlist")
	}
	if wrapped.calls != 0 || calls != 0 {
		t.Fatal("denied mutable patch reached formal tool or reviewer")
	}
	wrapped.calls = 2
	if _, err := wrapped.Execute(context.Background(), json.RawMessage(`{"chapter":5,"causal_simulation":{"decision_points":["choice"]},"finalize":true}`)); err == nil {
		t.Fatal("reviewer reset two-call ceiling")
	}
	if _, err := wrapped.WithPlanGroundingReviewer(tools.PlanGroundingReviewer{}); err == nil {
		t.Fatal("active wrapper accepted reviewer rebinding")
	}
}
