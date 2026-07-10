# LiteLLM Go

[English](README.md) | 中文

LiteLLM 是一个小巧、显式、类型化的 Go LLM SDK。根包拥有跨 Provider 的领域模型，具体 Provider 放在 `provider/<name>` 子包。

## 安装

```bash
go get github.com/voocel/litellm
```

## 快速开始

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
			litellm.UserText("用一句话解释 Go interface。"),
		},
		MaxTokens: litellm.IntPtr(120),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Text())
}
```

`openai.NewClient(cfg, opts...)` 会先创建 provider，再创建 `*litellm.Client`；每个 provider 包都提供。显式两步写法 —— `provider, _ := openai.New(cfg)` 再 `litellm.New(provider, opts...)` —— 完全等价；当你想把同一个 provider 复用到多个 client 时用它。两种写法接受相同的 `ClientOption`。

## 核心模型

消息和响应都由有序 `Block` 表达：

- `TextBlock`
- `ImageBlock`
- `ReasoningBlock`
- `ToolUseBlock`
- `ToolResultBlock`
- `ToolReferenceBlock`

`Response.Blocks` 是规范响应内容；`Text()`、`Reasoning()`、`ToolCalls()` 都只是便利视图。

```go
msgs := []litellm.Message{
	litellm.User(litellm.Text("图里有什么？"), litellm.ImageURL("https://example.com/cat.png")),
}

resp, err := client.Chat(ctx, litellm.Request{Model: "gpt-5.4-mini", Messages: msgs})
_ = resp
_ = err
```

多轮工具调用可以把上一轮响应块原样接回去：

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

`JSONRaw` 会返回 marshal 错误，不会静默生成非法工具参数。`MustJSONRaw` 只建议用于测试数据或允许 panic 的静态示例。

SDK 默认严格校验消息历史。脏的工具调用历史、非法 tool ID、缺失 tool result、不支持的 Provider 选项都会直接返回错误。如果需要导入历史数据，必须显式开启 repair：

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithMessageRepair(litellm.RepairAll))
```

任何会改变可观察数据的修复或 Provider 规范化都会通过 `Response.Warnings`、`WarningEvent` 和 `Hook.OnWarning` 暴露。

默认不会保存 Provider 原始响应体。调试时需要显式开启：

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithCaptureRawResponse(true))
```

## 流式

流式返回 typed `Event`。
`Stream` 设计为单 goroutine 消费；不要并发调用 `Next`。
如果需要每个事件之间的空闲超时，用 `WithStreamIdleTimeout` 显式开启；默认关闭。
`WithStreamIdleTimeout` 只覆盖通用 `Client.Stream`；OpenAI Responses 原生流用 `openai.Config.StreamIdleTimeout`。
例如：

```go
client, err := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")}, litellm.WithStreamIdleTimeout(120*time.Second))
```

```go
stream, err := client.Stream(ctx, litellm.Request{
	Model:    "gpt-5.4-mini",
	Messages: []litellm.Message{litellm.UserText("讲个短笑话。")},
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
		// Provider 原生生命周期或 hosted tool 事件。
	case litellm.DoneEvent:
		return
	}
}
```

聚合流式响应：

```go
resp, err := litellm.Collect(stream)
```

## Retry

默认不重试。需要时在具体 Provider 上显式开启：

```go
import "github.com/voocel/litellm/retry"

provider, err := openai.New(openai.Config{
	APIKey: os.Getenv("OPENAI_API_KEY"),
	Retry:  retry.DefaultPolicy(),
})
```

Bedrock 的 retry 会在每次 attempt 内部重新签名，用户不需要手动组合 SigV4 transport。

如果需要代理、trace 或自定义底层链路，使用 `Transport` 搭配 `Retry`。完整 `HTTPClient` 是高级逃生口，不能和 `Retry` 同时使用；这种情况下需要用户在自定义 client 内自行配置 retry。

选择规则：

| 场景 | 配置 |
| --- | --- |
| 普通重试 | `Retry: retry.DefaultPolicy()` |
| 重试 + 代理/trace/自定义底层链路 | `Retry` + `Transport` |
| 完全自定义请求执行 | `HTTPClient`，不和 `Retry`/`Transport` 混用 |

`APIKeyFunc` 会在请求创建时解析一次；retry attempt 会复用该请求。如果你使用极短有效期的 Bearer token，请用自定义 `Transport` 或 `HTTPClient` 在更底层注入认证。常规 API key 和默认 retry 窗口不需要关心这个细节。

## 工具调用

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
	Messages:   []litellm.Message{litellm.UserText("巴黎天气？")},
	Tools:      []litellm.Tool{tool},
	ToolChoice: "auto",
})
```

## 结构化输出

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
	Messages:       []litellm.Message{litellm.UserText("生成一个人。")},
	ResponseFormat: format,
})
```

## Thinking

Thinking 必须显式设置。`Thinking == nil` 时 SDK 不发送任何 thinking/reasoning 控制字段。

```go
resp, err := client.Chat(ctx, litellm.Request{
	Model:    "claude-sonnet-4-5-20250929",
	Messages: []litellm.Message{litellm.UserText("解释一下取舍。")},
	MaxTokens: litellm.IntPtr(2048),
	Thinking: &litellm.Thinking{
		Mode:  litellm.ThinkingEnabled,
		Effort: "low",
	},
})
```

Provider 约束会在本地校验。例如 Anthropic thinking 要求 `max_tokens >= 1024`，必须有 budget 或 effort，且不能和用户显式 temperature 冲突。
可跨 Provider 使用的 effort 值为 `minimal`、`low`、`medium`、`high`、`xhigh`、`max`；需要 token budget 的 Provider 会把这些值映射为 `budget_tokens`。
多 Provider UI 或预检可以用 `client.Capabilities(model)` 或 `litellm.GetCapabilities(provider, model)` 查询能力。

## OpenAI Responses

OpenAI Responses 是 provider-native 能力，挂在 `provider/openai.Provider` 上，不进入通用 `Client`。

```go
oai, err := openai.New(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")})
if err != nil {
	log.Fatal(err)
}

resp, err := oai.Responses(ctx, &openai.ResponsesRequest{
	Model: "gpt-5.6-sol",
	Messages: []litellm.Message{
		litellm.UserText("逐步计算 15*8。"),
	},
	ReasoningEffort:  "medium",
	ReasoningSummary: "auto",
	MaxOutputTokens:  litellm.IntPtr(800),
	OpenAITools: []openai.ResponsesTool{
		{"type": "web_search_preview"},
	},
})
```

Responses streaming 使用同一套 typed event：

```go
oai, err := openai.New(openai.Config{
	APIKey:            os.Getenv("OPENAI_API_KEY"),
	StreamIdleTimeout: 120 * time.Second,
})

stream, err := oai.ResponsesStream(ctx, &openai.ResponsesRequest{
	Model:    "gpt-5.6-sol",
	Messages: []litellm.Message{litellm.UserText("搜索并总结。")},
})
```

## Provider

Provider 配置属于各自子包。认证不会被强行收口成一个 API key string。

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

示例：

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

当前 provider 子包包括 OpenAI、Anthropic、Gemini、Bedrock、DeepSeek、Qwen、GLM、OpenRouter、MiniMax、Grok、MiMo、Ollama。
各 Provider 的 thinking、reasoning、usage、cache 支持见 [Provider Capabilities](provider-capabilities.md)。

## 模型列表

```go
models, err := client.ListModels(ctx)
```

只有实现了 `ModelLister` 的 Provider 支持该能力，返回字段为 best-effort。

## Provider Options

Provider 特定请求选项放在 `Request.ProviderOptions`。未知 key 默认报错。

```go
resp, err := client.Chat(ctx, litellm.Request{
	Model:    "gpt-5.4-mini",
	Messages: []litellm.Message{litellm.UserText("Hello")},
	ProviderOptions: litellm.ProviderOptions{
		openai.ProviderOptionPromptCacheRetention: "24h",
	},
})
```

## Hooks 与 OTel

Hooks 只观察请求、响应、warning 和 stream event。Hook 收到的是副本；修改它们不会影响 Provider 调用、最终返回的 response，也不会影响调用方看到的 event。核心 hooks 不 recover panic。

```go
client, err := litellm.New(provider, litellm.WithHook(litellm.HookFuncs{
	OnStreamEventFunc: func(ctx context.Context, meta litellm.CallMeta, event litellm.Event) {
		if delta, ok := event.(litellm.ContentDelta); ok {
			fmt.Print(delta.Text)
		}
	},
}))
```

可选的 `github.com/voocel/litellm/otel` 模块会把 hooks 适配成 OpenTelemetry span。

## Pricing

Pricing 是显式行为。成本计算绝不会隐式联网加载远程定价。

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

## 自定义 Provider

实现很小的 Provider 接口即可：

```go
type Provider interface {
	Name() string
	Chat(context.Context, *litellm.Request) (*litellm.Response, error)
	Stream(context.Context, *litellm.Request) (litellm.Stream, error)
}
```

## 许可证

Apache License
