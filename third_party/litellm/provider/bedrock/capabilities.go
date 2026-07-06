package bedrock

import (
	"strings"

	"github.com/voocel/litellm"
)

func (p *Provider) Capabilities(model string) litellm.Capabilities {
	claude := strings.Contains(strings.ToLower(model), "claude")
	thinking := litellm.ThinkingCapabilities{
		Supported: litellm.SupportNo,
		Disable:   litellm.SupportNo,
	}
	if claude {
		thinking = litellm.ThinkingCapabilities{
			Supported:    litellm.SupportYes,
			Disable:      litellm.SupportYes,
			Efforts:      litellm.PortableThinkingEfforts(),
			BudgetTokens: litellm.SupportYes,
			Notes:        []string{"thinking is exposed for Claude models and maps effort to budget_tokens"},
		}
	}
	return litellm.Capabilities{
		Provider: p.Name(),
		Model:    model,
		Thinking: thinking,
		Reasoning: litellm.ReasoningCapabilities{
			Blocks:          litellm.SupportYes,
			StreamingDeltas: litellm.SupportYes,
			ReasoningTokens: litellm.SupportNo,
		},
		Tools: litellm.ToolCapabilities{
			Calls:               litellm.SupportYes,
			StrictSchema:        litellm.SupportYes,
			Choice:              litellm.SupportYes,
			MultimodalResults:   litellm.SupportYes,
			RoundTripSignatures: litellm.SupportYes,
		},
		Structured: litellm.StructuredCapabilities{
			JSONObject: litellm.SupportYes,
			JSONSchema: litellm.SupportYes,
			Strict:     litellm.SupportNo,
		},
		Media: litellm.MediaCapabilities{
			ImageURL:   litellm.SupportNo,
			ImageBytes: litellm.SupportYes,
			FileURI:    litellm.SupportNo,
		},
		Cache: litellm.CacheCapabilities{
			Block:         litellm.SupportYes,
			RequestPolicy: litellm.SupportYes,
			Retention:     litellm.SupportYes,
			UsageRead:     litellm.SupportYes,
			UsageWrite:    litellm.SupportYes,
		},
		Streaming: litellm.StreamingCapabilities{
			Supported:       litellm.SupportYes,
			Usage:           litellm.SupportYes,
			ReasoningDeltas: litellm.SupportYes,
			ToolCallDeltas:  litellm.SupportYes,
			IdleTimeout:     litellm.SupportYes,
		},
		Usage: litellm.UsageCapabilities{
			InputTokens:      litellm.SupportYes,
			OutputTokens:     litellm.SupportYes,
			TotalTokens:      litellm.SupportYes,
			ReasoningTokens:  litellm.SupportNo,
			CacheReadTokens:  litellm.SupportYes,
			CacheWriteTokens: litellm.SupportYes,
		},
	}
}
