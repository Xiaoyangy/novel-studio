package openai

import (
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

func convertResponse(resp *chatResponse, req *litellm.Request) (*litellm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("openai: response cannot be nil")
	}
	out := &litellm.Response{
		Model:        resp.Model,
		Provider:     "openai",
		FinishReason: "",
		Usage:        convertUsage(&resp.Usage, resp.Model),
	}
	if out.Model == "" && req != nil {
		out.Model = req.Model
		out.Usage.Model = req.Model
	}
	if len(resp.Choices) == 0 {
		return out, nil
	}
	choice := resp.Choices[0]
	out.FinishReason = litellm.NormalizeFinishReason(choice.FinishReason)
	if reasoning := extractReasoning(choice); reasoning != "" && thinkingEnabled(req) {
		out.Blocks = append(out.Blocks, litellm.ReasoningBlock{Text: reasoning, Summary: choice.ReasoningSummary != nil})
	}
	blocks, err := convertContent(choice.Message.Content)
	if err != nil {
		return nil, err
	}
	out.Blocks = append(out.Blocks, blocks...)
	for _, call := range choice.Message.ToolCalls {
		out.Blocks = append(out.Blocks, litellm.ToolUseBlock{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	return out, nil
}

func convertUsage(u *usage, model string) litellm.Usage {
	if u == nil {
		return litellm.Usage{}
	}
	out := litellm.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
		Provider:     "openai",
		Model:        model,
	}
	if u.PromptTokensDetails != nil {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

func convertContent(raw json.RawMessage) ([]litellm.Block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []litellm.Block{litellm.TextBlock{Text: text}}, nil
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("openai: unsupported content payload: %w", err)
	}
	blocks := make([]litellm.Block, 0, len(parts))
	for _, part := range parts {
		partType, _ := part["type"].(string)
		switch partType {
		case "text":
			text, _ := part["text"].(string)
			if text != "" {
				annotations, err := annotations(part)
				if err != nil {
					return nil, err
				}
				logprobs, err := rawField(part, "logprobs")
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, litellm.TextBlock{Text: text, Annotations: annotations, Logprobs: logprobs})
			}
		case "image_url":
			if rawImage, ok := part["image_url"].(map[string]any); ok {
				url, _ := rawImage["url"].(string)
				detail, _ := rawImage["detail"].(string)
				if url != "" {
					blocks = append(blocks, litellm.ImageBlock{URL: url, Detail: detail})
				}
			}
		default:
			return nil, fmt.Errorf("openai: unsupported content part type %q", partType)
		}
	}
	return blocks, nil
}

func annotations(part map[string]any) ([]litellm.Annotation, error) {
	raw, ok := part["annotations"].([]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	out := make([]litellm.Annotation, 0, len(raw))
	for _, entry := range raw {
		data, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		extra, err := marshalRaw(data)
		if err != nil {
			return nil, fmt.Errorf("openai: marshal annotation: %w", err)
		}
		ann := litellm.Annotation{Extra: extra}
		ann.Type, _ = data["type"].(string)
		ann.Text, _ = data["text"].(string)
		ann.URL, _ = data["url"].(string)
		out = append(out, ann)
	}
	return out, nil
}

func rawField(part map[string]any, key string) (json.RawMessage, error) {
	value, ok := part[key]
	if !ok {
		return nil, nil
	}
	raw, err := marshalRaw(value)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal %s: %w", key, err)
	}
	return raw, nil
}

func marshalRaw(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func extractReasoning(choice choice) string {
	if choice.ReasoningSummary != nil && choice.ReasoningSummary.Text != "" {
		return choice.ReasoningSummary.Text
	}
	if choice.Message.Reasoning != "" {
		return choice.Message.Reasoning
	}
	return choice.Message.ReasoningContent
}

func extractDeltaReasoning(delta delta) (string, bool) {
	if delta.ReasoningSummary != nil && delta.ReasoningSummary.Text != "" {
		return delta.ReasoningSummary.Text, true
	}
	if delta.Reasoning != "" {
		return delta.Reasoning, false
	}
	return delta.ReasoningContent, false
}

func thinkingEnabled(req *litellm.Request) bool {
	return req == nil || req.Thinking == nil || req.Thinking.Mode != litellm.ThinkingDisabled
}
