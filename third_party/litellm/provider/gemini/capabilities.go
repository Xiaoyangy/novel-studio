package gemini

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
			Notes:        []string{"Gemini 3 uses thinkingLevel; other thinking models use thinkingBudget"},
		},
		Reasoning: litellm.ReasoningCapabilities{
			Blocks:          litellm.SupportYes,
			StreamingDeltas: litellm.SupportYes,
			ReasoningTokens: litellm.SupportYes,
		},
		Tools: litellm.ToolCapabilities{
			Calls:               litellm.SupportYes,
			StrictSchema:        litellm.SupportNo,
			Choice:              litellm.SupportYes,
			RoundTripSignatures: litellm.SupportYes,
		},
		Structured: litellm.StructuredCapabilities{
			JSONObject: litellm.SupportYes,
			JSONSchema: litellm.SupportYes,
			Strict:     litellm.SupportNo,
		},
		Media: litellm.MediaCapabilities{
			ImageURL:    litellm.SupportYes,
			ImageBytes:  litellm.SupportYes,
			FileURI:     litellm.SupportYes,
			ImageDetail: litellm.SupportNo,
		},
		Cache: litellm.CacheCapabilities{
			UsageRead: litellm.SupportYes,
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
			CacheWriteTokens: litellm.SupportNo,
		},
	}
}
