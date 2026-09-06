package host

import (
	"context"
	"errors"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestScopedUsageKeepsFailureInLiveWALAndResumesBudget(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScopedUsageAccounting(context.Background(), st, "generation-1", bootstrap.BudgetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	failure := &hostAccountingUsageError{usage: hostAccountingKnown(100, .5)}
	ctx := agents.WithDirectUsageAgent(scope.Context(), "convergence_planner_continuation")
	m := scope.Decorate(ctx, "writer", "test-provider", "test-model", &hostAccountingModel{failure: failure})
	_, err = m.Generate(ctx, nil, nil)
	if err = scope.Finish(err); !errors.As(err, &failure) {
		t.Fatal("typed failure lost")
	}
	meter, err := NewDurableUsageMeter(store.NewStore(st.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	state := meter.Tracker().Snapshot()
	if state.PerAgent["writer"].Input != 100 || state.Overall.Cost != .5 || len(state.PendingUsageCalls) != 0 {
		t.Fatalf("failed scoped call disappeared: %+v", state)
	}
	if _, err := NewScopedUsageAccounting(context.Background(), st, "generation-1", bootstrap.BudgetConfig{BookUSD: .25, HardStop: true}); err == nil {
		t.Fatal("resume ignored live cumulative budget")
	}
}
