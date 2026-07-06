package openrouter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/compat"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

type Config = compat.Config

const (
	ProviderOptionCacheRetention = "cache_retention"
	ProviderOptionSessionID      = "session_id"
)

func New(cfg Config) (*compat.Provider, error) {
	return compat.New(cfg, compat.Spec{
		Name: "openrouter",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Headers: compat.HeaderSpec{
			Extra: map[string]string{
				"HTTP-Referer": "https://github.com/voocel/litellm",
				"X-Title":      "litellm",
			},
		},
		Request: compat.RequestSpec{
			SupportsJSONSchema: true,
			Thinking:           mapThinking,
			ProviderOptions:    mapExtra,
			Messages:           mapMessages,
			CleanSchema:        cleanStrictSchema,
			AllowedProviderOptions: map[string]struct{}{
				ProviderOptionCacheRetention: {},
				ProviderOptionSessionID:      {},
			},
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			ContentAsInterface:        true,
			ReasoningFields:           []string{"reasoning_details", "reasoning", "reasoning_content"},
			HasCompletionTokenDetails: true,
		},
		Stream: compat.StreamSpec{
			ReasoningFields: []string{"reasoning_details", "reasoning", "reasoning_content"},
		},
		Capabilities: func(_ string, caps litellm.Capabilities) litellm.Capabilities {
			caps.Thinking.Efforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}
			caps.Thinking.BudgetTokens = litellm.SupportYes
			caps.Thinking.IncludeOutput = litellm.SupportNo
			caps.Cache.Block = litellm.SupportYes
			caps.Cache.Retention = litellm.SupportYes
			caps.Usage.CacheWriteTokens = litellm.SupportYes
			caps.Structured.JSONSchema = litellm.SupportYes
			caps.Structured.Strict = litellm.SupportYes
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
	if thinking.Mode == litellm.ThinkingDisabled {
		return map[string]any{"reasoning": map[string]any{"effort": "none"}}, nil
	}
	if thinking.Mode != litellm.ThinkingEnabled {
		return nil, fmt.Errorf("openrouter: unsupported thinking mode %d", thinking.Mode)
	}
	reasoning := map[string]any{}
	if thinking.BudgetTokens != nil {
		if *thinking.BudgetTokens <= 0 {
			return nil, fmt.Errorf("openrouter: thinking budget_tokens must be positive")
		}
		reasoning["max_tokens"] = *thinking.BudgetTokens
	} else if thinking.Effort != "" {
		effort, err := reasoningEffort(thinking.Effort)
		if err != nil {
			return nil, err
		}
		reasoning["effort"] = effort
	} else {
		reasoning["enabled"] = true
	}
	return map[string]any{"reasoning": reasoning}, nil
}

func reasoningEffort(effort string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	switch normalized {
	case "max", "xhigh", "high", "medium", "low", "minimal", "none":
		return normalized, nil
	default:
		return "", fmt.Errorf("openrouter: unsupported reasoning effort %q", effort)
	}
}

func mapExtra(options litellm.ProviderOptions, body map[string]any, req *litellm.Request) error {
	for key, value := range options {
		switch key {
		case ProviderOptionCacheRetention:
			retention, ok := value.(string)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be string", key)
			}
			cc, err := cacheControl(retention, req.Model)
			if err != nil {
				return err
			}
			if cc != nil {
				body["cache_control"] = cc
			}
		case ProviderOptionSessionID:
			sessionID, ok := value.(string)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be string", key)
			}
			if len(sessionID) > 256 {
				return fmt.Errorf("openrouter: provider option %q must be at most 256 characters", key)
			}
			body["session_id"] = sessionID
		default:
			return fmt.Errorf("openrouter: unsupported provider option %q", key)
		}
	}
	return nil
}

func cacheControl(retention, model string) (map[string]any, error) {
	if !strings.HasPrefix(strings.ToLower(model), "anthropic/") {
		return nil, fmt.Errorf("openrouter: cache_retention is only supported for anthropic models")
	}
	switch strings.ToLower(strings.TrimSpace(retention)) {
	case "none":
		return nil, nil
	case "long", "1h":
		return map[string]any{"type": "ephemeral", "ttl": "1h"}, nil
	case "short", "5m":
		return map[string]any{"type": "ephemeral"}, nil
	}
	return nil, fmt.Errorf("openrouter: unsupported cache_retention %q", retention)
}

func mapMessages(messages []litellm.Message) (any, error) {
	out := make([]map[string]any, 0, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case litellm.RoleSystem, litellm.RoleUser, litellm.RoleAssistant:
			content, toolCalls, reasoning, err := mapBlocks(msg.Blocks)
			if err != nil {
				return nil, fmt.Errorf("messages[%d]: %w", i, err)
			}
			converted := map[string]any{"role": string(msg.Role)}
			if content != nil {
				converted["content"] = content
			}
			if len(toolCalls) > 0 {
				converted["tool_calls"] = toolCalls
			}
			for key, value := range reasoning {
				converted[key] = value
			}
			out = append(out, converted)
		case litellm.RoleTool:
			for _, block := range msg.Blocks {
				result, ok := block.(litellm.ToolResultBlock)
				if !ok {
					return nil, fmt.Errorf("messages[%d]: tool role only supports ToolResultBlock, got %T", i, block)
				}
				text, err := textOnly(result.Content)
				if err != nil {
					return nil, err
				}
				out = append(out, map[string]any{"role": "tool", "tool_call_id": result.ToolUseID, "content": text})
			}
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported role %q", i, msg.Role)
		}
	}
	return out, nil
}

func mapBlocks(blocks []litellm.Block) (any, []map[string]any, map[string]any, error) {
	parts := make([]map[string]any, 0, len(blocks))
	var text strings.Builder
	var tools []map[string]any
	reasoning := make(map[string]any)
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if b.Text == "" {
				continue
			}
			part := map[string]any{"type": "text", "text": b.Text}
			if b.Cache != nil {
				cache, err := mapCache(b.Cache)
				if err != nil {
					return nil, nil, nil, err
				}
				part["cache_control"] = cache
			}
			parts = append(parts, part)
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		case litellm.ImageBlock:
			if b.URL == "" {
				return nil, nil, nil, fmt.Errorf("openrouter image blocks require URL")
			}
			image := map[string]any{"url": b.URL}
			if b.Detail != "" {
				image["detail"] = b.Detail
			}
			part := map[string]any{"type": "image_url", "image_url": image}
			if b.Cache != nil {
				cache, err := mapCache(b.Cache)
				if err != nil {
					return nil, nil, nil, err
				}
				part["cache_control"] = cache
			}
			parts = append(parts, part)
		case litellm.ToolUseBlock:
			tools = append(tools, map[string]any{
				"id":   b.ID,
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": string(b.Arguments),
				},
			})
		case litellm.ReasoningBlock:
			if b.Signature != "" || len(b.Redacted) > 0 {
				return nil, nil, nil, fmt.Errorf("OpenRouter Chat does not accept signed or redacted reasoning blocks in message history")
			}
			if err := putReasoning(reasoning, b); err != nil {
				return nil, nil, nil, err
			}
		default:
			return nil, nil, nil, fmt.Errorf("unsupported block %T", block)
		}
	}
	if len(parts) == 0 {
		return nil, tools, reasoning, nil
	}
	if len(parts) == 1 && parts[0]["type"] == "text" {
		if _, hasCache := parts[0]["cache_control"]; !hasCache {
			return text.String(), tools, reasoning, nil
		}
	}
	return parts, tools, reasoning, nil
}

func putReasoning(out map[string]any, block litellm.ReasoningBlock) error {
	if len(block.Extra) > 0 {
		var details []any
		if err := json.Unmarshal(block.Extra, &details); err != nil {
			return fmt.Errorf("OpenRouter reasoning_details must be a valid JSON array: %w", err)
		}
		out["reasoning_details"] = details
		delete(out, "reasoning")
		return nil
	}
	if block.Text == "" || out["reasoning_details"] != nil {
		return nil
	}
	if current, _ := out["reasoning"].(string); current != "" {
		out["reasoning"] = current + "\n\n" + block.Text
		return nil
	}
	out["reasoning"] = block.Text
	return nil
}

func mapCache(cache *litellm.CacheControl) (map[string]any, error) {
	if cache == nil {
		return nil, nil
	}
	cacheType := cache.Type
	if cacheType == "" {
		cacheType = litellm.CacheTypeEphemeral
	}
	if cacheType != litellm.CacheTypeEphemeral {
		return nil, fmt.Errorf("openrouter: unsupported cache type %q", cache.Type)
	}
	out := map[string]any{"type": cacheType}
	switch cache.TTL {
	case "", litellm.CacheTTL5m:
	case litellm.CacheTTL1h:
		out["ttl"] = cache.TTL
	default:
		return nil, fmt.Errorf("openrouter: unsupported cache ttl %q", cache.TTL)
	}
	return out, nil
}

func textOnly(blocks []litellm.Block) (string, error) {
	var text strings.Builder
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		default:
			return "", fmt.Errorf("OpenRouter tool results only support text content, got %T", block)
		}
	}
	return text.String(), nil
}

func cleanStrictSchema(schema litellm.Schema) (any, error) {
	var decoded any
	if len(schema) == 0 {
		return map[string]any{"type": "object"}, nil
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		return nil, err
	}
	return addAdditionalPropertiesFalse(decoded), nil
}

func addAdditionalPropertiesFalse(schema any) any {
	switch s := schema.(type) {
	case map[string]any:
		out := make(map[string]any, len(s)+1)
		for key, value := range s {
			out[key] = addAdditionalPropertiesFalse(value)
		}
		if out["type"] == "object" {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(s))
		for i, value := range s {
			out[i] = addAdditionalPropertiesFalse(value)
		}
		return out
	default:
		return schema
	}
}
