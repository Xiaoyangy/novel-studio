package grok

import (
	"fmt"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

const defaultBaseURL = "https://api.x.ai/v1"

type Config = compat.Config

func New(cfg Config) (*compat.Provider, error) {
	return compat.New(cfg, compat.Spec{
		Name: "grok",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Request: compat.RequestSpec{
			SupportsJSONSchema: true,
			Thinking:           mapThinking,
			ProviderOptions:    mapProviderOptions,
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			HasCompletionTokenDetails: true,
		},
		Capabilities: func(model string, caps litellm.Capabilities) litellm.Capabilities {
			if !supportsReasoningEffort(model) {
				caps.Thinking.Supported = litellm.SupportNo
				caps.Thinking.Disable = litellm.SupportNo
				caps.Thinking.Efforts = nil
			} else {
				caps.Thinking.Supported = litellm.SupportYes
				caps.Thinking.Efforts = []string{"low", "medium", "high"}
			}
			caps.Thinking.BudgetTokens = litellm.SupportNo
			caps.Thinking.IncludeOutput = litellm.SupportNo
			caps.Thinking.Notes = []string{"reasoning_effort is supported for grok-4.3 and aliases; use ThinkingDisabled to send none"}
			caps.Structured.JSONSchema = litellm.SupportYes
			caps.Structured.Strict = litellm.SupportYes
			return caps
		},
	})
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func mapProviderOptions(options litellm.ProviderOptions, body map[string]any, req *litellm.Request) error {
	if supportsReasoningEffort(req.Model) && len(req.Stop) > 0 {
		return fmt.Errorf("grok: stop is not supported for grok-4.3 reasoning models")
	}
	for key, value := range options {
		if supportsReasoningEffort(req.Model) && isUnsupportedReasoningOption(key) {
			return fmt.Errorf("grok: provider option %q is not supported for grok-4.3 reasoning models", key)
		}
		if _, exists := body[key]; exists {
			return fmt.Errorf("grok: provider option %q conflicts with generated request field", key)
		}
		body[key] = value
	}
	return nil
}

func mapThinking(thinking *litellm.Thinking, model string) (map[string]any, error) {
	if thinking == nil || thinking.Mode == litellm.ThinkingUnspecified {
		return nil, nil
	}
	if !supportsReasoningEffort(model) {
		return nil, fmt.Errorf("grok: reasoning_effort is only supported for grok-4.3 and aliases")
	}
	if thinking.Mode == litellm.ThinkingDisabled {
		return map[string]any{"reasoning_effort": "none"}, nil
	}
	if thinking.Mode != litellm.ThinkingEnabled {
		return nil, fmt.Errorf("grok: unsupported thinking mode %d", thinking.Mode)
	}
	if thinking.Effort != "" {
		effort, err := reasoningEffort(thinking.Effort)
		if err != nil {
			return nil, err
		}
		return map[string]any{"reasoning_effort": effort}, nil
	}
	return nil, fmt.Errorf("grok: thinking effort is required")
}

func supportsReasoningEffort(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "grok-4.3", "grok-4.3-latest", "grok-latest":
		return true
	default:
		return false
	}
}

func isUnsupportedReasoningOption(key string) bool {
	switch key {
	case "stop", "presence_penalty", "frequency_penalty", "presencePenalty", "frequencyPenalty":
		return true
	default:
		return false
	}
}

func reasoningEffort(effort string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	switch normalized {
	case "low", "medium", "high":
		return normalized, nil
	default:
		return "", fmt.Errorf("grok: unsupported reasoning_effort %q; use low, medium, or high", effort)
	}
}
