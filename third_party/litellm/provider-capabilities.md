# Provider Capabilities

This page documents the capabilities exposed through the shared `litellm.Request` and `litellm.Response` model. It is based on the current provider adapters, not on every feature a provider may offer in its native API.

Legend: `yes` is supported through the shared API, `no` is not exposed through the shared API, `partial` has provider-specific limits.

Applications can query the same information at runtime:

```go
caps := client.Capabilities(model)
if caps.Thinking.SupportsEffort("high") {
	// Show or enable the "high" thinking option.
}
```

Capability data is advisory for UI and preflight checks. Provider adapters still validate every request and return explicit errors when a requested feature cannot be encoded.

## Thinking

Portable `Thinking.Effort` values are `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. Providers that require token budgets map effort values to `budget_tokens`.

| Provider | Enable thinking | Disable thinking | Effort | BudgetTokens | IncludeOutput | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| OpenAI Chat | partial | yes | partial | no | no | Only reasoning chat models are supported; accepted efforts are `low`, `medium`, `high`, and `xhigh`; disable sends `none`. |
| OpenAI Responses | yes | yes | yes | no | yes | `IncludeOutput` maps to `reasoning.summary=auto`. Responses also exposes native `ReasoningEffort` and `ReasoningSummary`. |
| Anthropic | yes | yes | yes | yes | no | Requires `MaxTokens`; effort maps to `budget_tokens`. |
| Bedrock | yes | yes | yes | yes | no | Claude models only; effort maps to Anthropic `thinking.budget_tokens`. |
| Gemini | yes | yes | yes | yes | no | Gemini 3 uses `thinkingLevel`; other thinking models use `thinkingBudget`. |
| DeepSeek | yes | yes | partial | no | no | `low/medium/high` map to `high`; `xhigh/max` map to `max` and emit a warning when folding low/medium. |
| GLM | yes | yes | partial | no | no | Thinking requires `glm-4.5+`; `reasoning_effort` requires `glm-5.2+`. |
| Grok | partial | partial | partial | no | no | `reasoning_effort` is enabled for `grok-4.3` and aliases; accepted effort values are `low`, `medium`, and `high`; disable sends `none`; `stop` and penalty options are rejected for reasoning models. |
| OpenRouter | yes | yes | yes | yes | no | `BudgetTokens` maps to `reasoning.max_tokens`; effort maps to `reasoning.effort`. |
| Ollama | yes | yes | yes | no | no | `minimal` maps to `low`; `xhigh` maps to `max`. |
| Qwen | yes | yes | no | yes | no | Use `BudgetTokens`; `Effort` returns an error. |
| MiMo | yes | yes | no | no | no | Thinking is a provider switch; effort and budget controls are rejected. |
| MiniMax | yes | partial | no | no | no | Thinking is adaptive; unspecified thinking is treated as enabled for `reasoning_split`; disabling is rejected for M2.x models. |

## Reasoning And Usage

| Provider | Reasoning response blocks | Streaming reasoning deltas | Reasoning tokens | Cache read/write usage |
| --- | --- | --- | --- | --- |
| OpenAI Chat | yes | yes | yes | cache read |
| OpenAI Responses | yes | yes | yes | cache read |
| Anthropic | yes | yes | yes | read and write |
| Bedrock | yes | yes | no | read and write |
| Gemini | yes | yes | yes | cache read |
| DeepSeek | yes | yes | yes | cache read |
| GLM | yes | yes | yes | cache read |
| Grok | yes | yes | yes | cache read |
| OpenRouter | yes | yes | yes | read and write |
| Ollama | yes | yes | no | no |
| Qwen | yes | yes | yes | cache read |
| MiMo | yes | yes | yes | cache read |
| MiniMax | yes | yes | yes | cache read |

`ThinkingDisabled` suppresses reasoning output where the provider emits it separately and the adapter can filter it. It does not change provider-native behavior beyond the request fields sent by each adapter.

## Cache Controls

| Provider | Block cache | Request cache policy | Prompt/cache key options |
| --- | --- | --- | --- |
| OpenAI Chat | no | no | `prompt_cache_key`, `prompt_cache_retention` via provider options |
| OpenAI Responses | no | no | native `PromptCacheKey`, `PromptCacheRetention` |
| Anthropic | yes | no | no |
| Bedrock | yes | yes | `cache_retention` provider option |
| Gemini | no | no | no |
| DeepSeek | no | no | no |
| GLM | no | no | no |
| Grok | no | no | no |
| OpenRouter | yes | no | `cache_retention` provider option |
| Ollama | no | no | no |
| Qwen | no | no | no |
| MiMo | no | no | no |
| MiniMax | no | no | no |

## Native APIs

OpenAI Responses is provider-native. Use `provider/openai.Provider.Responses` and `ResponsesStream` when you need hosted tools, conversation IDs, `previous_response_id`, or other Responses-only fields. Generic `Client.Chat` and `Client.Stream` continue to use the shared chat model.

Provider-specific request fields are exposed through typed constants in each provider package. Unknown provider options are rejected by default; compat providers can opt into pass-through with `AllowUnknownProviderOptions`.

Structured output support follows what the shared adapter can encode. For example, Bedrock exposes JSON object and JSON schema output through `outputConfig.textFormat`, while GLM injects JSON schema into the prompt and only sends `json_object`.
