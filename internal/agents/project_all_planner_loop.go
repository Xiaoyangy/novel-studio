package agents

import (
	"context"
	"errors"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

// The direct Planner has no interactive queues or external Agent subscribers.
// AgentLoop exposes the same result-aware stop hook used by the coordinator's
// Planner, while retaining StopGuard and the caller's provider usage wrapper.
func runProjectAllPlannerLoop(
	ctx context.Context,
	chapter int,
	prompt string,
	agentCtx agentcore.AgentContext,
	config agentcore.LoopConfig,
) error {
	message, err := modelinput.NewExactAgentPacketMessage(modelinput.KindPlannerContext, prompt)
	if err != nil {
		return err
	}
	plannerCtx, repairGuard, cancel := newPlanningRepairGuard(ctx, chapter)
	defer cancel()
	config.Middlewares = append([]agentcore.ToolMiddleware{repairGuard.Middleware}, config.Middlewares...)
	config.StopAfterToolResult = plannerShouldStopAfterToolResult
	var runErr error
	for event := range agentcore.AgentLoop(plannerCtx, []agentcore.AgentMessage{message}, agentCtx, config) {
		if event.Type == agentcore.EventError && event.Err != nil {
			runErr = errors.Join(runErr, event.Err)
		}
	}
	// Drain the loop before flushing on every exit, including a terminal tool,
	// repair cancellation, provider failure and the turn safety limit. The
	// caller still reloads and verifies the actual durable plan/checkpoint.
	return errors.Join(runErr, repairGuard.Err(), projectedAccountingAfter(ctx))
}
