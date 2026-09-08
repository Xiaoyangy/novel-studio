package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPlanGroundingResolvesCurrentProtocolBeforeReadingCachedPass(t *testing.T) {
	st, _, sim := newPhysicalArbitratedStoreForTest(t)
	sim.Sources = append(sim.Sources, domain.PlanGroundingPolicyV1)
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: sim.Chapter, Goal: "缓存计划", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	a, b, resolves := 0, 0, 0
	first := PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("1", 64), Review: func(context.Context, domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
		a++
		return domain.PlanGroundingVerdict{Pass: true}, nil
	}}
	if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{context.Background(), first}); err != nil {
		t.Fatal(err)
	}
	first.Resolve = func() (PlanGroundingReviewer, error) {
		resolves++
		return PlanGroundingReviewer{Protocol: "sha256:" + strings.Repeat("2", 64), Review: func(_ context.Context, input domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error) {
			b++
			return domain.PlanGroundingVerdict{Findings: []domain.PlanGroundingFinding{{Kind: "time", PlanPath: "/plan/goal", PlanQuote: input.Plan.Goal, SourcePath: "/simulation/time_window", SourceQuote: input.Simulation.TimeWindow, Explanation: "new model rejects the same plan"}}}, nil
		}}, nil
	}
	for range 2 {
		if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{context.Background(), first}); err == nil {
			t.Fatal("old model's passing cache bypassed current resolved reviewer")
		}
	}
	if a != 1 || b != 1 || resolves != 2 {
		t.Fatalf("cache keyed by stale model or failed to reuse exact new rejection: %d/%d/%d", a, b, resolves)
	}
	failure := errors.New("no atomic reviewer snapshot")
	first.Resolve = func() (PlanGroundingReviewer, error) { return PlanGroundingReviewer{}, failure }
	if err := reviewChapterPlanGrounding(st, &plan, planGroundingExecution{context.Background(), first}); !errors.Is(err, failure) {
		t.Fatalf("resolve failure bypassed via cached pass: %v", err)
	}
	if a != 1 || b != 1 {
		t.Fatal("failed resolution invoked provider")
	}
}
