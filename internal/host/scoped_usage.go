package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// ScopedUsageAccounting attaches an explicitly authorized direct operation to
// the live book's existing WAL. It does not construct a Host, select models or
// authorize a dispatch. The caller must prove the live directory and generation.
type ScopedUsageAccounting struct {
	accounting *hostProviderAccounting
	ctx        context.Context
	cancel     context.CancelCauseFunc
}

func NewScopedUsageAccounting(ctx context.Context, live *store.Store, generationID string, budget bootstrap.BudgetConfig) (*ScopedUsageAccounting, error) {
	if ctx == nil || ctx.Err() != nil || live == nil || generationID == "" {
		return nil, fmt.Errorf("scoped usage requires an active context and bound live generation")
	}
	a, err := newHostProviderAccounting(live)
	if err != nil {
		return nil, err
	}
	a.generation = generationID
	runCtx, cancel := context.WithCancelCause(ctx)
	s := &ScopedUsageAccounting{accounting: a, ctx: runCtx, cancel: cancel}
	a.onError = func(err error) { cancel(err) }
	sentinel := NewBudgetSentinel(budget, func() float64 { cost, _, _, _, _ := a.meter.Tracker().Totals(); return cost }, func(reason string) { cancel(errors.New(reason)) }, func(string, string) {})
	if err := sentinel.Refuse(); err != nil {
		cancel(err)
		return nil, err
	}
	a.beforeCall = func() error {
		if err := context.Cause(runCtx); err != nil {
			return err
		}
		if budget.HardStop {
			return sentinel.Refuse()
		}
		return nil
	}
	a.meter.Tracker().SetOnCost(sentinel.OnCost)
	return s, nil
}

func (s *ScopedUsageAccounting) Context() context.Context { return s.ctx }
func (s *ScopedUsageAccounting) Decorate(ctx context.Context, role, provider, model string, raw agentcore.ChatModel) agentcore.ChatModel {
	return s.accounting.decorate(ctx, role, provider, model, raw)
}
func (s *ScopedUsageAccounting) Record(agent string, message agentcore.AgentMessage) {
	s.accounting.record(agent, message)
}

// Finish never disables the meter: a provider returning after cancellation
// still closes the same durable call ID. Errors are joined, never hidden.
func (s *ScopedUsageAccounting) Finish(runErr error) error {
	err := errors.Join(runErr, context.Cause(s.ctx), s.accounting.flush())
	s.cancel(nil)
	return err
}
