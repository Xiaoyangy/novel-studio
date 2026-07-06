package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/examples/internal/exampleutil"
	"github.com/voocel/litellm/provider/glm"
)

func main() {
	mode := "stream"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	client, err := glm.NewClient(glm.Config{
		APIKey:  os.Getenv("GLM_API_KEY"),
		BaseURL: os.Getenv("GLM_BASE_URL"),
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
	if m := os.Getenv("GLM_MODEL"); m != "" {
		return m
	}
	return "glm-5.2"
}

func runChat(ctx context.Context, client *litellm.Client) {
	resp, err := client.Chat(ctx, litellm.Request{
		Model: model(),
		Messages: []litellm.Message{
			litellm.UserText("Explain Zhipu GLM in one sentence."),
		},
		MaxTokens: litellm.IntPtr(256),
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled},
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
			litellm.UserText("Explain Zhipu GLM in one sentence."),
		},
		MaxTokens: litellm.IntPtr(256),
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	}, printer.Handler())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	fmt.Println()
	exampleutil.PrintUsage(resp.Usage)
}
