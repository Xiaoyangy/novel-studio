/*
Package litellm provides a small, explicit multi-provider LLM SDK core.

The root package owns the provider-agnostic domain model: Request, Response,
Message, Block, Stream, Event, structured errors, warnings, hooks, and pricing
helpers. Concrete providers live in provider-specific subpackages.

# Quick Start

Create a provider with its package-specific config, then bind a Client:

	import (
	    "context"
	    "fmt"
	    "os"

	    "github.com/voocel/litellm"
	    "github.com/voocel/litellm/provider/anthropic"
	)

	provider, err := anthropic.New(anthropic.Config{
	    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
	})
	if err != nil {
	    panic(err)
	}
	client, err := litellm.New(provider)
	if err != nil {
	    panic(err)
	}

	maxTokens := 1024
	resp, err := client.Chat(context.Background(), litellm.Request{
	    Model:     "claude-sonnet-4-5",
	    MaxTokens: &maxTokens,
	    Messages:  []litellm.Message{litellm.UserText("Explain AI in one sentence.")},
	})
	if err != nil {
	    panic(err)
	}
	fmt.Println(resp.Text())

# Blocks

Message and Response content is represented as ordered Blocks. This preserves
the order of text, reasoning, tool use, tool results, cache markers, and opaque
provider signatures across multi-turn agent workflows.

# Streaming

Providers stream typed Event values. Use a type switch for real-time handling
or Collect to aggregate a stream into a Response:
Stream is intended for single-goroutine consumption; do not call Next
concurrently.

	stream, err := client.Stream(ctx, req)
	if err != nil {
	    panic(err)
	}
	defer stream.Close()

	for {
	    event, err := stream.Next()
	    if err != nil {
	        panic(err)
	    }
	    switch e := event.(type) {
	    case litellm.ContentDelta:
	        fmt.Print(e.Text)
	    case litellm.DoneEvent:
	        return
	    }
	}

# Design

The SDK is intentionally not a gateway, router, agent runtime, account system,
or request scheduler. It binds one Client to one Provider and exposes explicit
configuration and local validation.
*/
package litellm
