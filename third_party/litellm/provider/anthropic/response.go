package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	Usage      anthropicUsage     `json:"usage"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens              int                           `json:"input_tokens"`
	OutputTokens             int                           `json:"output_tokens"`
	CacheCreationInputTokens int                           `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                           `json:"cache_read_input_tokens,omitempty"`
	OutputTokensDetails      *anthropicOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type anthropicOutputTokensDetails struct {
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
}

func convertResponse(resp *anthropicResponse, fallbackModel string) (*litellm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("anthropic: response cannot be nil")
	}
	out := &litellm.Response{
		Model:        resp.Model,
		Provider:     "anthropic",
		FinishReason: litellm.NormalizeFinishReason(resp.StopReason),
		Usage: litellm.Usage{
			InputTokens:      resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.OutputTokens,
			ReasoningTokens:  resp.Usage.reasoningTokens(),
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
			Provider:         "anthropic",
		},
	}
	if out.Model == "" {
		out.Model = fallbackModel
	}
	out.Usage.Model = out.Model
	for _, content := range resp.Content {
		switch content.Type {
		case "text":
			out.Blocks = append(out.Blocks, litellm.TextBlock{Text: content.Text})
		case "thinking":
			out.Blocks = append(out.Blocks, litellm.ReasoningBlock{Text: content.Thinking, Signature: content.Signature})
		case "redacted_thinking":
			out.Blocks = append(out.Blocks, litellm.ReasoningBlock{Redacted: append([]byte(nil), content.Data...)})
		case "tool_use":
			args, err := json.Marshal(content.Input)
			if err != nil {
				return nil, fmt.Errorf("anthropic: marshal tool use %q arguments: %w", content.Name, err)
			}
			out.Blocks = append(out.Blocks, litellm.ToolUseBlock{
				ID:        content.ID,
				Name:      content.Name,
				Arguments: args,
			})
		default:
			return nil, fmt.Errorf("anthropic: unsupported response content type %q", content.Type)
		}
	}
	return out, nil
}

func (u anthropicUsage) reasoningTokens() int {
	if u.OutputTokensDetails == nil {
		return 0
	}
	return u.OutputTokensDetails.ThinkingTokens
}
