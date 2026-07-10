package agents

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/llmcodex"
)

func TestParseThinkingLevelUltra(t *testing.T) {
	got, err := ParseThinkingLevel("ULTRA")
	if err != nil {
		t.Fatalf("ParseThinkingLevel(ultra): %v", err)
	}
	if got != ThinkingUltra {
		t.Fatalf("level = %q, want ultra", got)
	}
}

func TestResolveThinkingForCodexAllowsUltra(t *testing.T) {
	model := llmcodex.New("", "gpt-5.6-sol", "")
	got, ok := ResolveThinkingForModel(model, ThinkingUltra)
	if !ok || got != ThinkingUltra {
		t.Fatalf("resolved = %q, ok=%t; want ultra/true", got, ok)
	}
}
