package exampleutil

import (
	"fmt"

	"github.com/voocel/litellm"
)

type StreamPrinter struct {
	reasoning bool
	answer    bool
}

func (p *StreamPrinter) WriteReasoning(text string) {
	if !p.reasoning {
		fmt.Println("reasoning:")
		p.reasoning = true
	}
	fmt.Print(text)
}

func (p *StreamPrinter) WriteAnswer(text string) {
	if !p.answer {
		if p.reasoning {
			fmt.Println()
			fmt.Println()
		}
		fmt.Println("answer:")
		p.answer = true
	}
	fmt.Print(text)
}

func (p *StreamPrinter) Handler() litellm.StreamHandler {
	return litellm.StreamHandler{
		Reasoning: func(text string) error {
			p.WriteReasoning(text)
			return nil
		},
		Content: func(text string) error {
			p.WriteAnswer(text)
			return nil
		},
	}
}

func PrintUsage(usage litellm.Usage) {
	if !usage.HasTokens() {
		return
	}
	fmt.Printf("usage: input=%d output=%d total=%d reasoning=%d cache_read=%d cache_write=%d\n",
		usage.InputTokens,
		usage.OutputTokens,
		usage.TotalTokens,
		usage.ReasoningTokens,
		usage.CacheReadTokens,
		usage.CacheWriteTokens,
	)
}
