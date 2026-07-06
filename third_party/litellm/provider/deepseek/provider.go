package deepseek

import (
	"fmt"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

const defaultBaseURL = "https://api.deepseek.com"

type Config = compat.Config

const (
	ProviderOptionLogprobs    = "logprobs"
	ProviderOptionTopLogprobs = "top_logprobs"
	ProviderOptionUserID      = "user_id"
)

var allowedProviderOptions = map[string]struct{}{
	ProviderOptionLogprobs:    {},
	ProviderOptionTopLogprobs: {},
	ProviderOptionUserID:      {},
}

func New(cfg Config) (*compat.Provider, error) {
	supportsStrict := strings.HasSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/beta")
	strictTools := compat.StrictToolsOmit
	if supportsStrict {
		strictTools = compat.StrictToolsRequireAll
	}
	return compat.New(cfg, compat.Spec{
		Name: "deepseek",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Request: compat.RequestSpec{
			Thinking:                               mapThinking,
			Warnings:                               thinkingWarnings,
			AllowedProviderOptions:                 allowedProviderOptions,
			EmitEmptyAssistantContentWithToolCalls: true,
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			ReasoningFields:           []string{"reasoning_content"},
			HasCompletionTokenDetails: true,
			HasCacheTokens:            true,
		},
		Stream: compat.StreamSpec{
			ReasoningFields: []string{"reasoning_content"},
		},
		Features: compat.FeatureSpec{
			StrictTools: strictTools,
		},
		Capabilities: func(_ string, caps litellm.Capabilities) litellm.Capabilities {
			caps.Thinking.Efforts = litellm.PortableThinkingEfforts()
			caps.Thinking.BudgetTokens = litellm.SupportNo
			caps.Thinking.IncludeOutput = litellm.SupportNo
			caps.Thinking.Notes = []string{"minimal, low, medium, and high map to high; xhigh and max map to max"}
			return caps
		},
	})
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func mapThinking(thinking *litellm.Thinking, _ string) (map[string]any, error) {
	if thinking == nil || thinking.Mode == litellm.ThinkingUnspecified {
		return nil, nil
	}
	switch thinking.Mode {
	case litellm.ThinkingDisabled:
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}, nil
	case litellm.ThinkingEnabled:
		body := map[string]any{"thinking": map[string]any{"type": "enabled"}}
		effort, err := thinkingEffort(thinkingValue(thinking))
		if err != nil {
			return nil, err
		}
		if effort != "" {
			body["reasoning_effort"] = effort
		}
		return body, nil
	default:
		return nil, fmt.Errorf("deepseek: unsupported thinking mode %d", thinking.Mode)
	}
}

func thinkingValue(thinking *litellm.Thinking) string {
	if thinking == nil {
		return ""
	}
	return thinking.Effort
}

func thinkingEffort(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "minimal", "low", "medium", "high":
		return "high", nil
	case "xhigh", "max":
		return "max", nil
	default:
		return "", fmt.Errorf("deepseek: unsupported thinking effort %q; use high or max", value)
	}
}

func thinkingWarnings(req *litellm.Request) []litellm.Warning {
	if req == nil || req.Thinking == nil || req.Thinking.Mode != litellm.ThinkingEnabled {
		return nil
	}
	value := strings.ToLower(strings.TrimSpace(thinkingValue(req.Thinking)))
	if value != "minimal" && value != "low" && value != "medium" {
		return nil
	}
	return []litellm.Warning{{
		Code:     "deepseek.thinking_effort_folded",
		Provider: "deepseek",
		Message:  fmt.Sprintf("DeepSeek only supports reasoning_effort high or max; mapped %q to high", value),
	}}
}
