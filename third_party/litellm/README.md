# LiteLLM Go

[中文](README_CN.md) | English

LiteLLM is a small, explicit Go SDK for calling LLM providers through one typed core model. The root package owns the provider-agnostic API; concrete providers live in `provider/<name>` subpackages.

## Install

```bash
go get github.com/voocel/litellm
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/openai"
)

func main() {
	client, err := openai.NewClient(openai.Config{
		APIKey: os.Getenv("OPENAI_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.Chat(context.Background(), litellm.Request{
		Model: "gpt-5.4-mini",
		Messages: []litellm.Message{
			litellm.System("You are concise."),
			litellm.UserText("Explain Go interfaces in one sentence."),
		},
		MaxTokens: litellm.IntPtr(120),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Text())
}
```

`openai.NewClient(cfg, opts...)` builds the provider first, then returns a ready `*litellm.Client`; every provider package exposes it. The explicit two-step form — `provider, _ := openai.New(cfg)` then `litellm.New(provider, opts...)` — is equivalent; prefer it when you want to share one provider across multiple clients. Both forms accept the same `ClientOption`s.

## Core Model

Messages and responses use ordered `Block` values:

- `TextBlock`
- `ImageBlock`
- `ReasoningBlock`
- `ToolUseBlock`
- `ToolResultBlock`
- `ToolReferenceBlock`

`Response.Blocks` is the canonical response content. `Text()`, `Reasoning()`, and `ToolCalls()` are convenience views.

```go
msgs := []litellm.Message{
	litellm.User(litellm.Text("What is in this image?"), litellm.ImageURL("https://example.com/cat.png")),
}

resp, err := client.Chat(ctx, litellm.Request{Model: "gpt-5.4-mini", Messages: msgs})
_ = resp
_ = err
```

For multi-turn tool workflows, append the previous response blocks directly:

```go
args, err := litellm.JSONRaw(map[string]any{"ok": true})
if err != nil {
	log.Fatal(err)
}

msgs = append(msgs,
	litellm.Assistant(resp.Blocks...),
	litellm.ToolResultText("call_1", string(args)),
)
```

`JSONRaw` returns marshal errors instead of silently producing invalid tool arguments. Use `MustJSONRaw` only for static test data or package-level examples where panic is acceptable.

By default the SDK validates message history strictly. Dirty tool histories, invalid tool IDs, missing tool results, and unsupported provider options return errors. If you need to import legacy history, enable repair explicitly:

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithMessageRepair(litellm.RepairAll))
```

Repairs and provider normalizations that change observable data are exposed through `Response.Warnings`, `WarningEvent`, and `Hook.OnWarning`.

Raw provider response bodies are not retained by default. Enable them explicitly when debugging:

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithCaptureRawResponse(true))
```

## Streaming

Streams emit typed `Event` values.
`Stream` is intended for single-goroutine consumption; do not call `Next` concurrently.
Use `WithStreamIdleTimeout` when you want an explicit per-event idle timeout; it is off by default.
`WithStreamIdleTimeout` only covers generic `Client.Stream`; OpenAI Responses native streaming uses `openai.Config.StreamIdleTimeout`.
For example:

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithStreamIdleTimeout(120*time.Second))
```

```go
stream, err := client.Stream(ctx, litellm.Request{
	Model:    "gpt-5.4-mini",
	Messages: []litellm.Message{litellm.UserText("Tell me a short joke.")},
})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

for {
	event, err := stream.Next()
	if err != nil {
		log.Fatal(err)
	}
	switch e := event.(type) {
	case litellm.ContentDelta:
		fmt.Print(e.Text)
	case litellm.ReasoningDelta:
		fmt.Print(e.Text)
	case litellm.ProviderEvent:
		// Provider-native lifecycle/hosted-tool event.
	case litellm.DoneEvent:
		return
	}
}
```

To aggregate a stream:

```go
resp, err := litellm.Collect(stream)
```

## Retry

Retries are off by default. Enable them per provider:

```go
import "github.com/voocel/litellm/retry"

provider, err := openai.New(openai.Config{
	APIKey: os.Getenv("OPENAI_API_KEY"),
	Retry:  retry.DefaultPolicy(),
})
```

Bedrock retries re-sign each attempt internally, so users do not need to compose SigV4 transports by hand.

If you need a proxy, tracing, or a custom base transport, pass `Transport` together with `Retry`. A custom `HTTPClient` is an advanced escape hatch and cannot be combined with `Retry`; configure retry inside that client yourself.

Choose the smallest configuration that matches your use case:

| Use case | Config |
| --- | --- |
| Normal retries | `Retry: retry.DefaultPolicy()` |
| Retries plus proxy/tracing/custom base transport | `Retry` + `Transport` |
| Fully custom request execution | `HTTPClient`, without `Retry`/`Transport` |

`APIKeyFunc` is resolved once when a request is created; retry attempts reuse that request. If you use extremely short-lived Bearer tokens, inject auth in a lower-level custom `Transport` or `HTTPClient`. Normal API keys and the default retry window do not need special handling.

## Tools

```go
tool, err := litellm.NewTool("get_weather", "Get weather for a city.", map[string]any{
	"type": "object",
	"properties": map[string]any{
		"city": map[string]any{"type": "string"},
	},
	"required": []string{"city"},
})
if err != nil {
	log.Fatal(err)
}
tool.Strict = litellm.StrictEnabled

resp, err := client.Chat(ctx, litellm.Request{
	Model:      "gpt-5.4-mini",
	Messages:   []litellm.Message{litellm.UserText("Weather in Paris?")},
	Tools:      []litellm.Tool{tool},
	ToolChoice: "auto",
})
```

## Structured Output

```go
format, err := litellm.NewResponseFormatJSONSchema("person", "", map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name": map[string]any{"type": "string"},
	},
	"required": []string{"name"},
}, litellm.StrictEnabled)
if err != nil {
	log.Fatal(err)
}

resp, err := client.Chat(ctx, litellm.Request{
	Model:          "gpt-5.4-mini",
	Messages:       []litellm.Message{litellm.UserText("Generate a person.")},
	ResponseFormat: format,
})
```

## Thinking

Thinking is explicit. If `Thinking` is nil, the SDK sends no thinking control fields.

```go
resp, err := client.Chat(ctx, litellm.Request{
	Model:    "claude-sonnet-4-5-20250929",
	Messages: []litellm.Message{litellm.UserText("Explain the tradeoffs.")},
	MaxTokens: litellm.IntPtr(2048),
	Thinking: &litellm.Thinking{
		Mode:  litellm.ThinkingEnabled,
		Effort: "low",
	},
})
```

Provider constraints are validated locally. For example, Anthropic thinking requires `max_tokens >= 1024`, a budget or effort, and no conflicting explicit temperature.
Portable effort values are `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`; providers that require token budgets map these values to `budget_tokens`.
Use `client.Capabilities(model)` or `litellm.GetCapabilities(provider, model)` for UI/preflight checks across providers.

## OpenAI Responses

OpenAI Responses is provider-native and lives on `provider/openai.Provider`, not the generic client.

```go
oai, err := openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")})
if err != nil {
	log.Fatal(err)
}

resp, err := oai.Responses(ctx, &openai.ResponsesRequest{
	Model: "gpt-5.5",
	Messages: []litellm.Message{
		litellm.UserText("Solve 15*8 step by step."),
	},
	ReasoningEffort:  "medium",
	ReasoningSummary: "auto",
	MaxOutputTokens:  litellm.IntPtr(800),
	OpenAITools: []openai.ResponsesTool{
		{"type": "web_search_preview"},
	},
})
```

Streaming Responses uses the same typed event model:

```go
oai, err := openai.New(openai.Config{
	APIKey:            os.Getenv("OPENAI_API_KEY"),
	StreamIdleTimeout: 120 * time.Second,
})

stream, err := oai.ResponsesStream(ctx, &openai.ResponsesRequest{
	Model:    "gpt-5.5",
	Messages: []litellm.Message{litellm.UserText("Search and summarize.")},
})
```

## Providers

Provider configs are provider-specific. Authentication is not forced into a single API-key shape.

```go
import (
	"github.com/voocel/litellm/provider/anthropic"
	"github.com/voocel/litellm/provider/bedrock"
	"github.com/voocel/litellm/provider/deepseek"
	"github.com/voocel/litellm/provider/gemini"
	"github.com/voocel/litellm/provider/glm"
	"github.com/voocel/litellm/provider/grok"
	"github.com/voocel/litellm/provider/minimax"
	"github.com/voocel/litellm/provider/ollama"
	"github.com/voocel/litellm/provider/openrouter"
	"github.com/voocel/litellm/provider/qwen"
)
```

Examples:

```go
anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
gemini.New(gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY")})
deepseek.New(deepseek.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY")})
ollama.New(ollama.Config{})

bedrock.New(bedrock.Config{
	Region: "us-east-1",
	Credentials: bedrock.StaticCredentials(
		os.Getenv("AWS_ACCESS_KEY_ID"),
		os.Getenv("AWS_SECRET_ACCESS_KEY"),
		os.Getenv("AWS_SESSION_TOKEN"),
	),
})
```

Supported provider packages currently include OpenAI, Anthropic, Gemini, Bedrock, DeepSeek, Qwen, GLM, OpenRouter, MiniMax, Grok, MiMo, and Ollama.
See [Provider Capabilities](provider-capabilities.md) for thinking, reasoning, usage, and cache support across providers.

## Model Listing

```go
models, err := client.ListModels(ctx)
```

Only providers that implement `ModelLister` support this. Returned fields are best-effort.

## Provider Options

Provider-specific request options go in `Request.ProviderOptions`. Unknown keys error by default.

```go
resp, err := client.Chat(ctx, litellm.Request{
	Model:    "gpt-5.4-mini",
	Messages: []litellm.Message{litellm.UserText("Hello")},
	ProviderOptions: litellm.ProviderOptions{
		openai.ProviderOptionPromptCacheRetention: "24h",
	},
})
```

## Hooks And OTel

Hooks observe requests, responses, warnings, and stream events. Hook inputs are copies; mutating them does not affect provider calls, returned responses, or events seen by the caller. Core hooks do not recover panics.

```go
client, err := litellm.New(provider, litellm.WithHook(litellm.HookFuncs{
	OnStreamEventFunc: func(ctx context.Context, meta litellm.CallMeta, event litellm.Event) {
		if delta, ok := event.(litellm.ContentDelta); ok {
			fmt.Print(delta.Text)
		}
	},
}))
```

The optional `github.com/voocel/litellm/otel` module adapts hooks to OpenTelemetry spans.

## Pricing

Pricing is explicit. Cost calculation never loads remote pricing implicitly.

```go
import "github.com/voocel/litellm/pricing"

reg := pricing.NewRegistry()
err := reg.LoadFromURL(ctx, pricing.DefaultURL)
cost, err := reg.Calculate(resp.Model, resp.Usage)

err = reg.Set("my-model", pricing.ModelPricing{
	InputCostPerToken:  0.000001,
	OutputCostPerToken: 0.000002,
})
```

## Custom Providers

Implement the small provider interface:

```go
type Provider interface {
	Name() string
	Chat(context.Context, *litellm.Request) (*litellm.Response, error)
	Stream(context.Context, *litellm.Request) (litellm.Stream, error)
}
```

## License

Apache License
