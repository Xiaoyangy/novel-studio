package openai

import (
	"testing"

	"github.com/voocel/litellm"
)

func TestCapabilitiesReasoningModel(t *testing.T) {
	provider := mustProvider(t)
	caps := provider.Capabilities("gpt-5.1")
	if caps.Provider != "openai" || caps.Model != "gpt-5.1" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != litellm.SupportPartial || caps.Thinking.Disable != litellm.SupportYes {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if !caps.Thinking.SupportsEffort("xhigh") || caps.Thinking.SupportsEffort("minimal") {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if caps.Streaming.NativeResponses != litellm.SupportYes {
		t.Fatalf("native responses = %v, want yes", caps.Streaming.NativeResponses)
	}
}

func TestCapabilitiesNonReasoningModel(t *testing.T) {
	provider := mustProvider(t)
	caps := provider.Capabilities("gpt-4.1")
	if caps.Thinking.Supported != litellm.SupportNo || len(caps.Thinking.Efforts) != 0 {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
}
