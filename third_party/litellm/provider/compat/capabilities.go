package compat

import "github.com/voocel/litellm"

func (s Spec) defaultCapabilities(provider, model string) litellm.Capabilities {
	caps := litellm.Capabilities{
		Provider: provider,
		Model:    model,
		Thinking: litellm.ThinkingCapabilities{
			Supported: litellm.SupportNo,
			Disable:   litellm.SupportNo,
		},
		Reasoning: litellm.ReasoningCapabilities{
			Blocks:          supportFromBool(len(s.Response.ReasoningFields) > 0),
			StreamingDeltas: supportFromBool(len(s.Response.ReasoningFields) > 0 || len(s.Stream.ReasoningFields) > 0),
			ReasoningTokens: supportFromBool(s.Response.HasCompletionTokenDetails),
		},
		Tools: litellm.ToolCapabilities{
			Calls:         litellm.SupportYes,
			ParallelCalls: litellm.SupportUnknown,
			StrictSchema:  strictToolSupport(s.Features.StrictTools),
			Choice:        litellm.SupportYes,
		},
		Structured: litellm.StructuredCapabilities{
			JSONObject: litellm.SupportYes,
			JSONSchema: supportFromBool(s.Request.SupportsJSONSchema),
			Strict:     strictJSONSchemaSupport(s.Request),
			PromptOnly: s.Request.JSONSchemaToPrompt,
		},
		Streaming: litellm.StreamingCapabilities{
			Supported:       litellm.SupportYes,
			Usage:           supportFromBool(!s.Stream.OmitStreamOptions),
			ReasoningDeltas: supportFromBool(len(s.Response.ReasoningFields) > 0 || len(s.Stream.ReasoningFields) > 0),
			ToolCallDeltas:  litellm.SupportYes,
			IdleTimeout:     litellm.SupportYes,
		},
		Usage: litellm.UsageCapabilities{
			InputTokens:      litellm.SupportYes,
			OutputTokens:     litellm.SupportYes,
			TotalTokens:      litellm.SupportYes,
			ReasoningTokens:  supportFromBool(s.Response.HasCompletionTokenDetails),
			CacheReadTokens:  cacheReadSupport(s.Response),
			CacheWriteTokens: cacheWriteSupport(s.Response),
		},
	}
	if s.Request.Thinking != nil {
		caps.Thinking = litellm.ThinkingCapabilities{
			Supported: litellm.SupportYes,
			Disable:   litellm.SupportYes,
		}
	}
	return caps
}

func cacheReadSupport(spec ResponseSpec) litellm.Support {
	if spec.HasCacheTokens {
		return litellm.SupportYes
	}
	if spec.HasCompletionTokenDetails {
		return litellm.SupportPartial
	}
	return litellm.SupportNo
}

func cacheWriteSupport(spec ResponseSpec) litellm.Support {
	if spec.HasCompletionTokenDetails {
		return litellm.SupportPartial
	}
	return litellm.SupportNo
}

func supportFromBool(ok bool) litellm.Support {
	if ok {
		return litellm.SupportYes
	}
	return litellm.SupportNo
}

func strictToolSupport(mode StrictToolMode) litellm.Support {
	switch mode {
	case StrictToolsForward, StrictToolsRequireAll:
		return litellm.SupportYes
	default:
		return litellm.SupportNo
	}
}

func strictJSONSchemaSupport(spec RequestSpec) litellm.Support {
	if !spec.SupportsJSONSchema {
		return litellm.SupportNo
	}
	return litellm.SupportPartial
}
