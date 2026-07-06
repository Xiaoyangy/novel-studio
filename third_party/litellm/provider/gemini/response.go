package gemini

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/voocel/litellm"
)

var generatedToolCallSeq atomic.Uint64

func convertResponse(resp *response, req *litellm.Request) (*litellm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("gemini: response cannot be nil")
	}
	if resp.PromptFeedback != nil {
		return nil, promptFeedbackError(resp.PromptFeedback)
	}
	out := &litellm.Response{
		Provider: "gemini",
	}
	if req != nil {
		out.Model = req.Model
	}
	if resp.UsageMetadata != nil {
		out.Usage = litellm.Usage{
			InputTokens:     resp.UsageMetadata.PromptTokenCount,
			OutputTokens:    resp.UsageMetadata.CandidatesTokenCount,
			ReasoningTokens: resp.UsageMetadata.ThoughtsTokenCount,
			TotalTokens:     resp.UsageMetadata.TotalTokenCount,
			CacheReadTokens: resp.UsageMetadata.CachedContentTokenCount,
			Provider:        "gemini",
			Model:           out.Model,
		}
	}
	if len(resp.Candidates) == 0 {
		return out, nil
	}
	candidate := resp.Candidates[0]
	if len(candidate.Content.Parts) == 0 && candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
		return nil, candidateFinishError(candidate)
	}
	out.FinishReason = litellm.NormalizeFinishReason(candidate.FinishReason)
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			if part.Thought != nil && *part.Thought {
				if thinkingEnabled(req) {
					out.Blocks = append(out.Blocks, litellm.ReasoningBlock{Text: part.Text, Signature: part.ThoughtSignature})
				}
			} else {
				out.Blocks = append(out.Blocks, litellm.TextBlock{Text: part.Text})
			}
		}
		if part.FunctionCall != nil {
			args, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, fmt.Errorf("gemini: marshal function call %q arguments: %w", part.FunctionCall.Name, err)
			}
			id := part.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", generatedToolCallSeq.Add(1))
				out.Warnings = append(out.Warnings, generatedToolCallIDWarning(part.FunctionCall.Name, id))
			}
			out.Blocks = append(out.Blocks, litellm.ToolUseBlock{
				ID:        id,
				Name:      part.FunctionCall.Name,
				Arguments: args,
				Signature: part.ThoughtSignature,
			})
		}
	}
	return out, nil
}

func generatedToolCallIDWarning(name, id string) litellm.Warning {
	message := fmt.Sprintf("Gemini function call %q was missing id; generated %q", name, id)
	return litellm.Warning{
		Code:     "gemini.tool_call_id_synthesized",
		Provider: "gemini",
		Message:  message,
	}
}

func thinkingEnabled(req *litellm.Request) bool {
	return req == nil || req.Thinking == nil || req.Thinking.Mode != litellm.ThinkingDisabled
}

func formatSafetyRatings(ratings []safetyRating) string {
	if len(ratings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ratings))
	for _, rating := range ratings {
		if rating.Category == "" && rating.Probability == "" && !rating.Blocked {
			continue
		}
		item := rating.Category
		if rating.Probability != "" {
			if item != "" {
				item += "="
			}
			item += rating.Probability
		}
		if rating.Blocked {
			if item != "" {
				item += ","
			}
			item += "blocked"
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, "; ")
}
