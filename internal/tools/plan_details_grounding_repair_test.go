package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestPlanDetailsGroundingRepairDoesNotSplitOneFactualCorrection(t *testing.T) {
	st, _, sim := newPhysicalArbitratedStoreForTest(t)
	sim.Sources = append(sim.Sources, domain.PlanGroundingPolicyV1)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: sim.Chapter, Goal: "第五周期才收妥旧物", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	calls := 0
	reviewer := PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("1", 64), Review: func(_ context.Context, input domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		calls++
		return domain.PlanGroundingVerdict{Findings: []domain.PlanGroundingFinding{{Kind: "time", PlanPath: "/plan/goal", PlanQuote: input.Plan.Goal, SourcePath: "/simulation/time_window", SourceQuote: input.Simulation.TimeWindow, Explanation: "同一收物时序在causal_beats与render_capacity中均应同步修正"}}}, nil
	}}
	for range 2 {
		cause := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{t.Context(), reviewer})
		if !errors.Is(cause, errs.ErrToolPrecondition) {
			t.Fatalf("real grounding must reject before repair: %v", cause)
		}
		// Exercise the same wrapping path used by PlanDetails.finalizePartial,
		// including a wrapper, so routing cannot depend on string matching.
		err := planDetailsFinalizeRepairError(sim.Chapter, map[string]any{"causal_beats": "saved", "render_capacity": "saved"}, fmt.Errorf("wrapped: %w", cause))
		if !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "/plan/goal") || !strings.Contains(err.Error(), "/simulation/time_window") {
			t.Fatalf("repair lost validated findings or error identity: %v", err)
		}
		for _, want := range []string{"同一事实矛盾", "causal_beats", "render_capacity", "一次提交", "角色选择与裁决不可改"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("repair missing %q: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "下一轮只补 recommended_batches") || strings.Contains(err.Error(), "recommended_batches=") {
			t.Errorf("factual repair was routed back to incomplete-field batches: %v", err)
		}
		if plan.GroundingReview != nil {
			t.Fatal("failed grounding gained a passing receipt")
		}
	}
	if calls != 1 {
		t.Fatalf("identical failed input was reviewed %d times; cache must remain intact", calls)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(sim.Chapter), "plan"); cp != nil {
		t.Fatal("feedback changed a planning checkpoint")
	}
}

func TestPlanDetailsNonGroundingFailureKeepsExistingBatches(t *testing.T) {
	for _, cause := range []error{
		fmt.Errorf("missing longform_opening: %w", errs.ErrToolPrecondition),
		fmt.Errorf("计划违背最终角色裁决 is untrusted text: %w", errs.ErrToolPrecondition),
		fmt.Errorf("provider failed: %w", ErrPlanGroundingReviewRequired),
	} {
		err := planDetailsFinalizeRepairError(1, map[string]any{"project_promise": "saved"}, cause)
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "下一轮只补 recommended_batches") {
			t.Fatalf("ordinary missing-field/provider failure changed routing: %v", err)
		}
		if strings.Contains(err.Error(), "同一事实矛盾") {
			t.Fatalf("unvalidated text impersonated a grounding verdict: %v", err)
		}
	}
}
