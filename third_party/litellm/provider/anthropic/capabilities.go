package anthropic

import "github.com/voocel/litellm"

func (p *Provider) Capabilities(model string) litellm.Capabilities {
	return litellm.Capabilities{
		Provider: p.Name(),
		Model:    model,
		Thinking: litellm.ThinkingCapabilities{
			Supported:    litellm.SupportYes,
			Disable:      litellm.SupportYes,
			Efforts:      litellm.PortableThinkingEfforts(),
			BudgetTokens: litellm.SupportYes,
			Notes:        []string{"thinking requires max_tokens and maps effort to budget_tokens"},
		},
		Reasoning: litellm.ReasoningCapabilities{
			Blocks:          litellm.SupportYes,
			StreamingDeltas: litellm.SupportYes,
			ReasoningTokens: litellm.SupportYes,
		},
		Tools: litellm.ToolCapabilities{
			Calls:               litellm.SupportYes,
			StrictSchema:        litellm.SupportYes,
			Choice:              litellm.SupportPartial,
			MultimodalResults:   litellm.SupportYes,
			RoundTripSignatures: litellm.SupportYes,
		},
		Structured: litellm.StructuredCapabilities{
			JSONObject: litellm.SupportNo,
			JSONSchema: litellm.SupportNo,
			Strict:     litellm.SupportNo,
		},
		Media: litellm.MediaCapabilities{
			ImageURL:   litellm.SupportYes,
			ImageBytes: litellm.SupportYes,
			FileURI:    litellm.SupportNo,
		},
		Cache: litellm.CacheCapabilities{
			Block:      litellm.SupportYes,
			UsageRead:  litellm.SupportYes,
			UsageWrite: litellm.SupportYes,
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
			ReasoningTokens:  litellm.SupportYes,
			CacheReadTokens:  litellm.SupportYes,
			CacheWriteTokens: litellm.SupportYes,
		},
	}
}
