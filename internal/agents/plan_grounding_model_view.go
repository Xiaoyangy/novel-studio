package agents

import (
	"context"
	"errors"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

// Extend only the previously unrepresentable configured-Codex activation
// input. No successful old call, conservative unknown-window path, executed
// provider error or canonical input/audit is changed.
func retryPlanGroundingModelView(ctx context.Context, model agentcore.ChatModel, thinking agentcore.ThinkingLevel, input domain.PlanGroundingInput, raw []byte, messages []agentcore.Message, cause error) (*agentcore.LLMResponse, error) {
	if !planGroundingHasCodexExactTransport(model, input) || len(messages) != 2 || len(raw) > planGroundingExactInputByteLimit || !strings.HasPrefix(cause.Error(), "exact agent packet exceeds configured operating budget:") {
		return nil, cause
	}
	var executed interface {
		error
		LLMUsage() (*agentcore.Usage, string)
	}
	if errors.As(cause, &executed) || ctx.Err() != nil {
		return nil, cause
	}
	var budget *PlanGroundingInputBudgetError
	if !errors.As(classifyPlanGroundingInputBudgetError(model, input, cause), &budget) {
		return nil, cause
	}
	digest, err := domain.PlanGroundingInputDigest(input)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	compact, changed, err := modelinput.EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	if !changed {
		return nil, cause
	}
	packet, err := modelinput.NewExactAgentPacketMessage(modelinput.KindPlanGrounding, string(compact))
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	retry := append([]agentcore.Message(nil), messages...)
	retry[1] = packet
	// Reuse the same accounted adapter. Its original local reject is a skipped
	// attempt; only this exact retry can dispatch and report real provider usage.
	return model.Generate(ctx, retry, []agentcore.ToolSpec{planGroundingToolSpec()}, agentcore.WithThinking(thinking), agentcore.WithMaxTokens(6144))
}
