package glm

import (
	"fmt"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

const defaultBaseURL = "https://open.bigmodel.cn/api/paas/v4"

type Config = compat.Config

const (
	ProviderOptionDoSample   = "do_sample"
	ProviderOptionRequestID  = "request_id"
	ProviderOptionThinking   = "thinking"
	ProviderOptionToolStream = "tool_stream"
	ProviderOptionUserID     = "user_id"
)

var allowedProviderOptions = map[string]struct{}{
	ProviderOptionDoSample:   {},
	ProviderOptionRequestID:  {},
	ProviderOptionThinking:   {},
	ProviderOptionToolStream: {},
	ProviderOptionUserID:     {},
}

func New(cfg Config) (*compat.Provider, error) {
	return compat.New(cfg, compat.Spec{
		Name: "glm",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Request: compat.RequestSpec{
			MaxStopSequences:       1,
			JSONSchemaToPrompt:     true,
			ResponseFormat:         mapResponseFormat,
			Thinking:               mapThinking,
			ProviderOptions:        mapProviderOptions,
			AllowedProviderOptions: allowedProviderOptions,
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			ReasoningFields:           []string{"reasoning_content"},
			HasCompletionTokenDetails: true,
		},
		Stream: compat.StreamSpec{
			ReasoningFields: []string{"reasoning_content"},
		},
		Capabilities: func(model string, caps litellm.Capabilities) litellm.Capabilities {
			caps.Thinking.Efforts = litellm.PortableThinkingEfforts()
			caps.Thinking.BudgetTokens = litellm.SupportNo
			caps.Thinking.IncludeOutput = litellm.SupportNo
			caps.Thinking.Notes = []string{"thinking requires glm-4.5+; reasoning_effort requires glm-5.2+"}
			if !versionAtLeast(model, 4, 5) {
				caps.Thinking.Supported = litellm.SupportNo
				caps.Thinking.Disable = litellm.SupportNo
				caps.Thinking.Efforts = nil
			} else if !versionAtLeast(model, 5, 2) {
				caps.Thinking.Efforts = nil
			}
			caps.Structured.JSONSchema = litellm.SupportNo
			caps.Structured.PromptOnly = true
			return caps
		},
	})
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func mapResponseFormat(format *litellm.ResponseFormat) (any, error) {
	switch format.Type {
	case litellm.ResponseFormatText:
		return nil, nil
	case litellm.ResponseFormatJSONObject, litellm.ResponseFormatJSONSchema:
		return map[string]string{"type": "json_object"}, nil
	default:
		return nil, fmt.Errorf("glm: unsupported response format %q", format.Type)
	}
}

func mapThinking(thinking *litellm.Thinking, model string) (map[string]any, error) {
	if thinking == nil || thinking.Mode == litellm.ThinkingUnspecified {
		return nil, nil
	}
	if !versionAtLeast(model, 4, 5) {
		return nil, fmt.Errorf("glm: thinking is only supported for glm-4.5 or later")
	}
	switch thinking.Mode {
	case litellm.ThinkingDisabled:
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}, nil
	case litellm.ThinkingEnabled:
		body := map[string]any{"thinking": map[string]any{"type": "enabled"}}
		effort, err := reasoningEffort(thinking)
		if err != nil {
			return nil, err
		}
		if effort != "" {
			if !versionAtLeast(model, 5, 2) {
				return nil, fmt.Errorf("glm: reasoning_effort is only supported for glm-5.2 or later")
			}
			body["reasoning_effort"] = effort
		}
		return body, nil
	default:
		return nil, fmt.Errorf("glm: unsupported thinking mode %d", thinking.Mode)
	}
}

func mapProviderOptions(options litellm.ProviderOptions, body map[string]any, _ *litellm.Request) error {
	for key, value := range options {
		switch key {
		case ProviderOptionDoSample, ProviderOptionToolStream:
			v, ok := value.(bool)
			if !ok {
				return fmt.Errorf("glm: provider option %q must be bool", key)
			}
			body[key] = v
		case ProviderOptionRequestID, ProviderOptionUserID:
			v, ok := value.(string)
			if !ok {
				return fmt.Errorf("glm: provider option %q must be string", key)
			}
			body[key] = v
		case ProviderOptionThinking:
			if err := applyThinkingOption(value, body); err != nil {
				return err
			}
		default:
			return fmt.Errorf("glm: unsupported provider option %q", key)
		}
	}
	return nil
}

func applyThinkingOption(value any, body map[string]any) error {
	option, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("glm: provider option %q must be object", ProviderOptionThinking)
	}
	if len(option) == 0 {
		return fmt.Errorf("glm: provider option %q must not be empty", ProviderOptionThinking)
	}
	converted := make(map[string]any, len(option))
	for key, value := range option {
		switch key {
		case "type":
			v, ok := value.(string)
			if !ok {
				return fmt.Errorf("glm: provider option %q.type must be string", ProviderOptionThinking)
			}
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "enabled" && v != "disabled" {
				return fmt.Errorf("glm: provider option %q.type must be enabled or disabled", ProviderOptionThinking)
			}
			converted[key] = v
		case "clear_thinking":
			v, ok := value.(bool)
			if !ok {
				return fmt.Errorf("glm: provider option %q.clear_thinking must be bool", ProviderOptionThinking)
			}
			converted[key] = v
		default:
			return fmt.Errorf("glm: unsupported provider option %q.%s", ProviderOptionThinking, key)
		}
	}

	existing, ok := body[ProviderOptionThinking].(map[string]any)
	if !ok || existing == nil {
		body[ProviderOptionThinking] = converted
		return nil
	}
	if optionType, ok := converted["type"]; ok {
		if existingType, ok := existing["type"]; ok && existingType != optionType {
			return fmt.Errorf("glm: provider option %q.type conflicts with Request.Thinking", ProviderOptionThinking)
		}
	}
	merged := make(map[string]any, len(existing)+len(converted))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range converted {
		merged[key] = value
	}
	body[ProviderOptionThinking] = merged
	return nil
}

func reasoningEffort(thinking *litellm.Thinking) (string, error) {
	effort := strings.ToLower(strings.TrimSpace(thinking.Effort))
	switch effort {
	case "":
		return "", nil
	case "max", "xhigh", "high", "medium", "low", "minimal", "none":
		return effort, nil
	default:
		return "", fmt.Errorf("glm: unsupported reasoning_effort %q; use max, xhigh, high, medium, low, minimal, or none", effort)
	}
}

func versionAtLeast(model string, major, minor int) bool {
	_, after, ok := strings.Cut(strings.ToLower(model), "glm-")
	if !ok {
		return false
	}
	var maj, min int
	fmt.Sscanf(after, "%d.%d", &maj, &min)
	return maj > major || (maj == major && min >= minor)
}
