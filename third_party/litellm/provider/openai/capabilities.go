package openai

import "github.com/voocel/litellm"

func (p *Provider) Capabilities(model string) litellm.Capabilities {
	reasoningModel := p.isReasoningModel(model)
	thinking := litellm.ThinkingCapabilities{
		Supported: litellm.SupportPartial,
		Disable:   litellm.SupportYes,
		Efforts:   []string{"low", "medium", "high", "xhigh"},
		Notes:     []string{"chat reasoning controls are only available on reasoning chat models"},
	}
	if !reasoningModel {
		thinking.Supported = litellm.SupportNo
		thinking.Efforts = nil
	}
	return litellm.Capabilities{
		Provider: p.Name(),
		Model:    model,
		Thinking: thinking,
		Reasoning: litellm.ReasoningCapabilities{
			Blocks:          litellm.SupportYes,
			StreamingDeltas: litellm.SupportYes,
			ReasoningTokens: litellm.SupportYes,
		},
		Tools: litellm.ToolCapabilities{
			Calls:               litellm.SupportYes,
			ParallelCalls:       litellm.SupportYes,
			StrictSchema:        litellm.SupportYes,
			Choice:              litellm.SupportYes,
			HostedProviderTools: litellm.SupportPartial,
		},
		Structured: litellm.StructuredCapabilities{
			JSONObject: litellm.SupportYes,
			JSONSchema: litellm.SupportYes,
			Strict:     litellm.SupportYes,
		},
		Media: litellm.MediaCapabilities{
			ImageURL:    litellm.SupportYes,
			ImageBytes:  litellm.SupportYes,
			FileURI:     litellm.SupportNo,
			ImageDetail: litellm.SupportYes,
		},
		Cache: litellm.CacheCapabilities{
			Block:      litellm.SupportNo,
			PromptKey:  litellm.SupportYes,
			Retention:  litellm.SupportYes,
			UsageRead:  litellm.SupportYes,
			UsageWrite: litellm.SupportNo,
		},
		Streaming: litellm.StreamingCapabilities{
			Supported:       litellm.SupportYes,
			Usage:           litellm.SupportYes,
			ReasoningDeltas: litellm.SupportYes,
			ToolCallDeltas:  litellm.SupportYes,
			NativeResponses: litellm.SupportYes,
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
