package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/examples/internal/exampleutil"
	"github.com/voocel/litellm/provider/deepseek"
)

// Run a single capability at a time:
//
//	go run ./examples/deepseek chat     # basic completion
//	go run ./examples/deepseek stream   # token streaming (+ reasoning, usage)
//	go run ./examples/deepseek tool      # function-calling round trip
//
// Override the model with DEEPSEEK_MODEL (e.g. deepseek-v4-pro for higher
// quality). Reasoning is toggled in-request via Thinking, not by model name.
func main() {
	mode := "stream"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	client, err := deepseek.NewClient(deepseek.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: os.Getenv("DEEPSEEK_BASE_URL"),
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	switch mode {
	case "chat":
		runChat(ctx, client)
	case "stream":
		runStream(ctx, client)
	case "tool":
		runTool(ctx, client)
	default:
		log.Fatalf("unknown mode %q; use one of: chat | stream | tool", mode)
	}
}

func model() string {
	if m := os.Getenv("DEEPSEEK_MODEL"); m != "" {
		return m
	}
	return "deepseek-v4-flash"
}

// runChat is a basic non-streaming completion.
func runChat(ctx context.Context, client *litellm.Client) {
	resp, err := client.Chat(ctx, litellm.Request{
		Model: model(),
		Messages: []litellm.Message{
			litellm.System("You are concise."),
			litellm.UserText("Explain reasoning models in one sentence."),
		},
		MaxTokens: litellm.IntPtr(1024),
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Text())
	if reasoning := resp.Reasoning(); reasoning != "" {
		fmt.Println("\nreasoning:", reasoning)
	}
}

// runStream prints streamed text as it arrives, then prints final usage.
func runStream(ctx context.Context, client *litellm.Client) {
	printer := exampleutil.StreamPrinter{}
	resp, err := client.StreamWith(ctx, litellm.Request{
		Model: model(),
		Messages: []litellm.Message{
			litellm.UserText("Who are you?"),
		},
		MaxTokens: litellm.IntPtr(1024),
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	}, printer.Handler())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	fmt.Println()
	exampleutil.PrintUsage(resp.Usage)
}

// runTool performs a full function-calling round trip: the model requests a
// tool, we execute it locally, feed the result back, and print the final answer.
func runTool(ctx context.Context, client *litellm.Client) {
	weather, err := litellm.NewTool("get_weather", "Get the current weather for a city.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string", "description": "City name"},
		},
		"required": []string{"city"},
	})
	if err != nil {
		log.Fatal(err)
	}

	messages := []litellm.Message{
		litellm.UserText("What's the weather in Paris? Use the tool."),
	}

	resp, err := client.Chat(ctx, litellm.Request{
		Model:      model(),
		Messages:   messages,
		Tools:      []litellm.Tool{weather},
		ToolChoice: "auto",
		MaxTokens:  litellm.IntPtr(256),
	})
	if err != nil {
		log.Fatal(err)
	}

	calls := resp.ToolCalls()
	if len(calls) == 0 {
		fmt.Println("model did not call a tool:")
		fmt.Println(resp.Text())
		return
	}

	// Echo the assistant tool-call turn back, then answer each call.
	messages = append(messages, litellm.Assistant(resp.Blocks...))
	for _, call := range calls {
		fmt.Printf("tool call: %s(%s)\n", call.Name, string(call.Arguments))
		messages = append(messages, litellm.ToolResultText(call.ID, executeTool(call)))
	}

	final, err := client.Chat(ctx, litellm.Request{
		Model:     model(),
		Messages:  messages,
		Tools:     []litellm.Tool{weather},
		MaxTokens: litellm.IntPtr(256),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nfinal:", final.Text())
}

// executeTool is a stand-in for real tool execution.
func executeTool(call litellm.ToolUseBlock) string {
	var args struct {
		City string `json:"city"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	city := args.City
	if city == "" {
		city = "unknown"
	}
	result, _ := litellm.JSONRaw(map[string]any{
		"city":        city,
		"temperature": "22°C",
		"condition":   "sunny",
	})
	return string(result)
}
