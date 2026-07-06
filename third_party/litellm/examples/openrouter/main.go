package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/examples/internal/exampleutil"
	"github.com/voocel/litellm/provider/openrouter"
)

func main() {
	mode := "stream"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	client, err := openrouter.NewClient(openrouter.Config{
		APIKey:  os.Getenv("OPENROUTER_API_KEY"),
		BaseURL: os.Getenv("OPENROUTER_BASE_URL"),
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
	default:
		log.Fatalf("unknown mode %q; use one of: chat | stream", mode)
	}
}

func model() string {
	if m := os.Getenv("OPENROUTER_MODEL"); m != "" {
		return m
	}
	return "openai/gpt-5.4-mini"
}

func runChat(ctx context.Context, client *litellm.Client) {
	resp, err := client.Chat(ctx, litellm.Request{
		Model: model(),
		Messages: []litellm.Message{
			litellm.UserText("Explain model routers in one sentence."),
		},
		MaxTokens: litellm.IntPtr(256),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resp.Text())
}

func runStream(ctx context.Context, client *litellm.Client) {
	printer := exampleutil.StreamPrinter{}
	resp, err := client.StreamWith(ctx, litellm.Request{
		Model: model(),
		Messages: []litellm.Message{
			litellm.UserText("Explain model routers in one sentence."),
		},
		MaxTokens: litellm.IntPtr(256),
	}, printer.Handler())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	fmt.Println()
	exampleutil.PrintUsage(resp.Usage)
}
