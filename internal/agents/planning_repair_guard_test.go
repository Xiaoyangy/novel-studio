package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/voocel/agentcore"
)

func repairGuardResponse(id string, finalize bool) agentcore.Message {
	args, _ := json.Marshal(map[string]any{"finalize": finalize})
	return agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: "plan_details", Args: args})}}
}

func TestPlanningRepairGuardStopsPaidLoopAndPreservesStagedWork(t *testing.T) {
	ctx, guard, cancel := newPlanningRepairGuard(context.Background(), 1)
	defer cancel()
	model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{
		repairGuardResponse("fail-1", true), repairGuardResponse("patch-1", false),
		repairGuardResponse("fail-2", true), repairGuardResponse("patch-2", false), repairGuardResponse("fail-3", true),
	}, response: repairGuardResponse("must-not-call", true)}
	staged := 0
	tool := agentcore.NewFuncTool("plan_details", "fake bounded planner", map[string]any{"type": "object"}, func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Finalize bool `json:"finalize"`
		}
		_ = json.Unmarshal(args, &input)
		if input.Finalize {
			return nil, fmt.Errorf("same POV contract contradiction: %w", errs.ErrToolPrecondition)
		}
		staged++
		return json.RawMessage(`{"staged":true}`), nil
	})
	for range agentcore.AgentLoop(ctx, []agentcore.AgentMessage{agentcore.UserMsg("finish the plan")}, agentcore.AgentContext{Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{
		Model: model, MaxTurns: 36, MaxToolErrors: 0, Middlewares: []agentcore.ToolMiddleware{guard.Middleware},
	}) {
	}
	if model.calls != 5 || staged != 2 {
		t.Fatalf("continued paying or erased partial: calls=%d staged=%d", model.calls, staged)
	}
	var failure *RepeatedPlanningValidationError
	if !errors.As(guard.Err(), &failure) || failure.Attempts != 3 || !errors.Is(failure, errs.ErrToolPrecondition) {
		t.Fatalf("lost typed validator failure: %v", guard.Err())
	}
	if !errors.As(context.Cause(ctx), &failure) || !strings.Contains(failure.Error(), "preserved partial") {
		t.Fatal("wrong cancellation cause")
	}
	called := false
	_, _ = guard.Middleware(ctx, agentcore.ToolCall{Name: "plan_details"}, func(context.Context, json.RawMessage) (json.RawMessage, error) { called = true; return nil, nil })
	if called {
		t.Fatal("invoked another mutation after terminal guard")
	}
}

func TestPlanningRepairGuardDoesNotConflateDifferentErrorsOrArguments(t *testing.T) {
	ctx, guard, cancel := newPlanningRepairGuard(context.Background(), 2)
	defer cancel()
	for _, message := range []string{"missing field A", "missing field B", "missing field A", "missing field B"} {
		_, _ = guard.Middleware(ctx, agentcore.ToolCall{Name: "plan_details"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("%s: %w", message, errs.ErrToolPrecondition)
		})
	}
	for range 5 {
		_, _ = guard.Middleware(ctx, agentcore.ToolCall{Name: "plan_details"}, func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errs.ErrToolArgs })
		_, _ = guard.Middleware(ctx, agentcore.ToolCall{Name: "novel_context"}, func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errs.ErrToolPrecondition })
	}
	if guard.Err() != nil || ctx.Err() != nil {
		t.Fatalf("unrelated corrections exhausted one error budget: %v", guard.Err())
	}
}

func TestPlanningRepairGuardStopsAtFirstHostCapacityFailure(t *testing.T) {
	ctx, guard, cancel := newPlanningRepairGuard(context.Background(), 1)
	defer cancel()
	model := &outlineAllOperationCaptureModel{response: repairGuardResponse("capacity", true)}
	localBudget := errors.New("complete evidence exceeds configured local budget")
	budget := &PlanGroundingInputBudgetError{Cause: localBudget}
	staged := json.RawMessage(`{"saved_fields":["causal_beats"],"finalized":false}`)
	tool := agentcore.NewFuncTool("plan_details", "fake planner", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return staged, errors.Join(errs.ErrToolPrecondition, fmt.Errorf("plan grounding review did not complete: %w", budget))
	})
	for range agentcore.AgentLoop(ctx, []agentcore.AgentMessage{agentcore.UserMsg("finish the plan")}, agentcore.AgentContext{Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{
		Model: model, MaxTurns: 36, MaxToolErrors: 0, Middlewares: []agentcore.ToolMiddleware{guard.Middleware},
	}) {
	}
	if model.calls != 1 {
		t.Fatalf("paid another call for host capacity: calls=%d", model.calls)
	}
	var terminal *PlanningInputCapacityError
	var original *PlanGroundingInputBudgetError
	if !errors.As(guard.Err(), &terminal) || terminal.Chapter != 1 || terminal.Tool != "plan_details" ||
		!errors.As(context.Cause(ctx), &original) || original != budget ||
		!errors.Is(guard.Err(), localBudget) || !errors.Is(guard.Err(), errs.ErrToolPrecondition) {
		t.Fatalf("lost capacity error or original cause: %v", guard.Err())
	}
	var repeated *RepeatedPlanningValidationError
	if errors.As(guard.Err(), &repeated) || len(guard.counts) != 0 {
		t.Fatal("capacity failure was counted as a story correction")
	}
}

func TestPlanningRepairGuardCapacityScopeAndPreservedResult(t *testing.T) {
	for _, name := range []string{"plan_structure", "plan_details", "novel_context"} {
		t.Run(name, func(t *testing.T) {
			ctx, guard, cancel := newPlanningRepairGuard(context.Background(), 2)
			defer cancel()
			result := json.RawMessage(`{"staged":true}`)
			budget := &PlanGroundingInputBudgetError{Cause: errors.New("local capacity")}
			got, err := guard.Middleware(ctx, agentcore.ToolCall{Name: name}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return result, budget // No precondition sentinel is required for a typed host failure.
			})
			if string(got) != string(result) || !errors.Is(err, budget) {
				t.Fatalf("lost staged result or cause: %s %v", got, err)
			}
			if name == "novel_context" {
				if guard.Err() != nil || ctx.Err() != nil {
					t.Fatal("intercepted an unrelated tool")
				}
				return
			}
			if guard.Err() == nil || ctx.Err() == nil {
				t.Fatal("did not stop on first capacity failure")
			}
			called := false
			_, _ = guard.Middleware(ctx, agentcore.ToolCall{Name: name}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				called = true
				return nil, nil
			})
			if called {
				t.Fatal("executed another tool after the terminal failure")
			}
		})
	}
}
