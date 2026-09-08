package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/voocel/agentcore"
)

const repeatedPlanningValidationLimit = 3

// RepeatedPlanningValidationError preserves the actual validator failure.
// Repeating an identical rejection is not a new model-planning outcome; stop
// this invocation without dropping its durable partial or pretending success.
type RepeatedPlanningValidationError struct {
	Chapter  int
	Tool     string
	Attempts int
	Cause    error
}

func (e *RepeatedPlanningValidationError) Error() string {
	return fmt.Sprintf("chapter %d %s repeated the same validation failure %d times; preserved partial plan, stop this invocation before another paid retry: %v", e.Chapter, e.Tool, e.Attempts, e.Cause)
}

func (e *RepeatedPlanningValidationError) Unwrap() error { return e.Cause }

// PlanningInputCapacityError is a host transport failure, not a request for
// the planner to rewrite authoritative evidence or retry its story choices.
type PlanningInputCapacityError struct {
	Chapter int
	Tool    string
	Cause   error
}

func (e *PlanningInputCapacityError) Error() string {
	return fmt.Sprintf("chapter %d %s host input capacity exceeded; preserved partial plan, stop this invocation before another paid retry: %v", e.Chapter, e.Tool, e.Cause)
}

func (e *PlanningInputCapacityError) Unwrap() error { return e.Cause }

type planningRepairGuard struct {
	mu      sync.Mutex
	chapter int
	counts  map[[32]byte]int
	err     error
	cancel  context.CancelCauseFunc
}

func newPlanningRepairGuard(parent context.Context, chapter int) (context.Context, *planningRepairGuard, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(nonNilContext(parent))
	guard := &planningRepairGuard{chapter: chapter, counts: make(map[[32]byte]int), cancel: cancel}
	return ctx, guard, func() { cancel(nil) }
}

func (g *planningRepairGuard) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err == nil {
		return nil
	}
	return g.err
}

func (g *planningRepairGuard) Middleware(ctx context.Context, call agentcore.ToolCall, next agentcore.ToolExecuteFunc) (json.RawMessage, error) {
	if terminal := g.Err(); terminal != nil {
		return nil, terminal
	}
	result, err := next(ctx, call.Args)
	if call.Name != "plan_structure" && call.Name != "plan_details" {
		return result, err
	}
	var capacity *PlanGroundingInputBudgetError
	if errors.As(err, &capacity) {
		g.mu.Lock()
		if g.err == nil {
			g.err = &PlanningInputCapacityError{Chapter: g.chapter, Tool: call.Name, Cause: err}
		}
		terminal := g.err
		g.mu.Unlock()
		g.cancel(terminal)
		return result, terminal
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		return result, err
	}
	// Harmless reads or another staged patch must not erase the history of a
	// repeatedly rejected finalize. Different failures retain separate budgets.
	key := sha256.Sum256([]byte(call.Name + "\x00" + strings.TrimSpace(err.Error())))
	g.mu.Lock()
	g.counts[key]++
	if g.counts[key] >= repeatedPlanningValidationLimit && g.err == nil {
		g.err = &RepeatedPlanningValidationError{Chapter: g.chapter, Tool: call.Name, Attempts: g.counts[key], Cause: err}
	}
	terminal := g.err
	g.mu.Unlock()
	if terminal != nil {
		g.cancel(terminal)
		return result, terminal
	}
	return result, err
}
