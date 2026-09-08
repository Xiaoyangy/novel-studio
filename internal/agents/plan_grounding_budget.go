package agents

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

// This bounds the local activation serialization, not provider tokens. The
// Codex adapter independently bounds the complete system/tools/input/output
// schema transport and reserves output space against its operating window.
const planGroundingExactInputByteLimit = 2 * 1024 * 1024

// PlanGroundingInputBudgetError is a local evidence-transport failure, not a
// grounding verdict or a request to rewrite the story to make evidence fit.
// Keep the original error chain for usage accounting and caller stop guards.
type PlanGroundingInputBudgetError struct{ Cause error }

func (e *PlanGroundingInputBudgetError) Error() string { return e.Cause.Error() }
func (e *PlanGroundingInputBudgetError) Unwrap() error { return e.Cause }

func planGroundingHasCodexExactTransport(model agentcore.ChatModel, input domain.PlanGroundingInput) bool {
	provider, ok := model.(agentcore.ProviderNamer)
	return input.Activation != nil && ok && provider.ProviderName() == "codex-cli"
}

func checkPlanGroundingInputBudget(model agentcore.ChatModel, input domain.PlanGroundingInput, raw []byte) error {
	if planGroundingHasCodexExactTransport(model, input) {
		if len(raw) > planGroundingExactInputByteLimit {
			return &PlanGroundingInputBudgetError{Cause: fmt.Errorf("exact activation grounding input exceeds local byte limit: input_bytes=%d max_input_bytes=%d; cannot truncate authoritative evidence; no provider call", len(raw), planGroundingExactInputByteLimit)}
		}
		return nil
	}
	if utf8.RuneCount(raw) > 82000 {
		return &PlanGroundingInputBudgetError{Cause: fmt.Errorf("exact plan grounding input exceeds 82000 runes; cannot truncate authoritative evidence")}
	}
	return nil
}

func classifyPlanGroundingInputBudgetError(model agentcore.ChatModel, input domain.PlanGroundingInput, err error) error {
	if err == nil || !planGroundingHasCodexExactTransport(model, input) {
		return err
	}
	// Every executed Codex CLI failure has a typed usage receipt, even when
	// token counts are unavailable. Never reinterpret its message as a local
	// budget failure, even if a provider happens to use identical wording.
	var executed interface {
		error
		LLMUsage() (*agentcore.Usage, string)
	}
	if errors.As(err, &executed) {
		return err
	}
	var classified *PlanGroundingInputBudgetError
	if errors.As(err, &classified) {
		return err
	}
	// The committed adapter currently exposes these pre-dispatch errors as
	// plain Go errors. Recognize only its exact local-budget prefixes, not a
	// generic context/budget substring or arbitrary remote failure message.
	for _, prefix := range []string{
		"exact agent packet exceeds absolute input byte limit:",
		"exact agent packet exceeds configured operating budget:",
		"exact agent packet has invalid configured context budget; no provider call",
		"exact agent packet plus system/tool instructions exceeds Codex prompt budget ",
		"exact agent packet and latest complete feedback exceed Codex prompt budget ",
	} {
		if strings.HasPrefix(err.Error(), prefix) {
			return &PlanGroundingInputBudgetError{Cause: err}
		}
	}
	return err
}
