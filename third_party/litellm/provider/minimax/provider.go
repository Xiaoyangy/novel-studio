package minimax

import (
	"fmt"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

const defaultBaseURL = "https://api.minimax.io/v1"

type Config = compat.Config

const ProviderOptionServiceTier = "service_tier"

var allowedProviderOptions = map[string]struct{}{
	ProviderOptionServiceTier: {},
}

func New(cfg Config) (*compat.Provider, error) {
	return compat.New(cfg, compat.Spec{
		Name: "minimax",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Request: compat.RequestSpec{
			MaxTokensField:         "max_completion_tokens",
			Thinking:               mapThinking,
			ProviderOptions:        mapProviderOptions,
			AllowedProviderOptions: allowedProviderOptions,
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			ReasoningFields:           []string{"reasoning_details", "reasoning_content"},
			HasCompletionTokenDetails: true,
		},
		Stream: compat.StreamSpec{
			ReasoningFields:            []string{"reasoning_details", "reasoning_content"},
			ReasoningCumulative:        true,
			ContentCumulative:          true,
			ContentCumulativeCondition: "thinking_enabled",
		},
		Capabilities: func(model string, caps litellm.Capabilities) litellm.Capabilities {
			caps.Thinking.Efforts = nil
			caps.Thinking.BudgetTokens = litellm.SupportNo
			caps.Thinking.IncludeOutput = litellm.SupportNo
			caps.Thinking.Notes = []string{"thinking is adaptive; unspecified thinking is treated as enabled for reasoning_split"}
			if isM2(model) {
				caps.Thinking.Disable = litellm.SupportNo
				caps.Thinking.Notes = append(caps.Thinking.Notes, "disabling thinking is rejected for M2.x models")
			} else {
				caps.Thinking.Disable = litellm.SupportPartial
			}
			return caps
		},
	})
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func mapThinking(thinking *litellm.Thinking, model string) (map[string]any, error) {
	if thinking == nil || thinking.Mode == litellm.ThinkingUnspecified {
		return nil, nil
	}
	thinkingType := ""
	switch thinking.Mode {
	case litellm.ThinkingDisabled:
		if isM2(model) {
			return nil, fmt.Errorf("minimax: thinking cannot be disabled for M2.x models")
		}
		thinkingType = "disabled"
	case litellm.ThinkingEnabled:
		if thinking.Effort != "" {
			return nil, fmt.Errorf("minimax: thinking effort is not supported")
		}
		if thinking.BudgetTokens != nil {
			return nil, fmt.Errorf("minimax: thinking budget_tokens is not supported")
		}
		thinkingType = "adaptive"
	default:
		return nil, fmt.Errorf("minimax: unsupported thinking mode %d", thinking.Mode)
	}
	body := map[string]any{"thinking": map[string]any{"type": thinkingType}}
	if thinkingType == "adaptive" {
		body["reasoning_split"] = true
	}
	return body, nil
}

func mapProviderOptions(options litellm.ProviderOptions, body map[string]any, req *litellm.Request) error {
	if effectiveThinkingEnabled(req) {
		body["reasoning_split"] = true
	}
	for key, value := range options {
		switch key {
		case ProviderOptionServiceTier:
			tier, ok := value.(string)
			if !ok {
				return fmt.Errorf("minimax: provider option %q must be string", key)
			}
			tier = strings.ToLower(strings.TrimSpace(tier))
			if tier != "standard" && tier != "priority" {
				return fmt.Errorf("minimax: provider option %q must be standard or priority", key)
			}
			body[key] = tier
		default:
			return fmt.Errorf("minimax: unsupported provider option %q", key)
		}
	}
	return nil
}

func effectiveThinkingEnabled(req *litellm.Request) bool {
	if req.Thinking == nil || req.Thinking.Mode == litellm.ThinkingUnspecified {
		return true
	}
	return req.Thinking.Mode == litellm.ThinkingEnabled
}

func isM2(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "minimax-m2")
}
