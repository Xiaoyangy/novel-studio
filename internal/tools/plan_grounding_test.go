package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPlanGroundingCachesRejectionAndPassAcrossRestart(t *testing.T) {
	st, _, sim := newPhysicalArbitratedStoreForTest(t)
	sim.Sources = append(sim.Sources, domain.PlanGroundingPolicyV1)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: sim.Chapter, Goal: "错误场景", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	calls := 0
	reviewer := PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("1", 64), Review: func(_ context.Context, input domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		calls++
		if input.Plan.Goal == "错误场景" {
			return domain.PlanGroundingVerdict{Findings: []domain.PlanGroundingFinding{{Kind: "time", PlanPath: "/plan/goal", PlanQuote: input.Plan.Goal, SourcePath: "/simulation/time_window", SourceQuote: input.Simulation.TimeWindow, Explanation: "修正测试场景"}}}, nil
		}
		return domain.PlanGroundingVerdict{Pass: true}, nil
	}}
	if err := reviewChapterPlanGrounding(st, &plan); err == nil {
		t.Fatal("unconfigured gate bypassed")
	}
	for range 2 {
		err := reviewChapterPlanGrounding(store.NewStore(st.Dir()), &plan, planGroundingExecution{context.Background(), reviewer})
		if !errors.Is(err, errs.ErrToolPrecondition) {
			t.Fatalf("rejection not repairable: %v", err)
		}
		if plan.GroundingReview != nil {
			t.Fatal("rejected plan got passing receipt")
		}
	}
	if calls != 1 {
		t.Fatalf("identical failed plan recharged %d times", calls)
	}
	plan.Goal = "正确场景"
	for range 2 {
		if err := reviewChapterPlanGrounding(store.NewStore(st.Dir()), &plan, planGroundingExecution{context.Background(), reviewer}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || plan.GroundingReview == nil {
		t.Fatalf("cache/receipt failed: %d", calls)
	}
	if err := validateCurrentPlanGrounding(st, plan); err != nil {
		t.Fatal(err)
	}
	plan.Hook = "未经裁决的新结局"
	if err := validateCurrentPlanGrounding(st, plan); err == nil {
		t.Fatal("post-review edit bypassed read gate")
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(sim.Chapter), "plan"); cp != nil {
		t.Fatal("review itself advanced planning checkpoint")
	}
}

func TestPlanGroundingCancelledBeforeProviderAndFailureNotCached(t *testing.T) {
	st, _, sim := newPhysicalArbitratedStoreForTest(t)
	sim.Sources = append(sim.Sources, domain.PlanGroundingPolicyV1)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: sim.Chapter, CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	calls := 0
	reviewer := PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("1", 64), Review: func(context.Context, domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		calls++
		return domain.PlanGroundingVerdict{}, errors.New("provider unavailable")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{ctx, reviewer}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("cancelled call reached provider")
	}
	for range 2 {
		if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{context.Background(), reviewer}); err == nil {
			t.Fatal("provider failure became pass")
		}
	}
	if calls != 2 {
		t.Fatal("provider failure cached as verdict")
	}
}

func TestPlanGroundingHostOnlyReusesPaidPassButCannotReviewChangedPlan(t *testing.T) {
	st, _, sim := newPhysicalArbitratedStoreForTest(t)
	sim.Sources = append(sim.Sources, domain.PlanGroundingPolicyV1)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	partial := map[string]any{"structure": map[string]any{"chapter": sim.Chapter, "goal": "原目标"}}
	if err := st.Drafts.SaveChapterPlanPartial(sim.Chapter, partial); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: sim.Chapter, Goal: "原目标", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	calls := 0
	reviewer := PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("1", 64), Review: func(context.Context, domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		calls++
		return domain.PlanGroundingVerdict{Pass: true}, nil
	}}
	if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{context.Background(), reviewer}); err != nil {
		t.Fatal(err)
	}
	plan.GroundingReview = nil // Model-authored partial decoding cannot supply it.
	if err := reviewChapterPlanGrounding(store.NewStore(st.Dir()), &plan); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || plan.GroundingReview == nil {
		t.Fatal("host-only failed to reuse exact paid pass")
	}
	plan.Goal = "增加新会面"
	if err := reviewChapterPlanGrounding(store.NewStore(st.Dir()), &plan); !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("changed plan bypassed cache-only: %v", err)
	}
	if calls != 1 {
		t.Fatal("host-only secretly dispatched another model")
	}
}
