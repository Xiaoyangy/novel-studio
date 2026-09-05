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

func TestCapabilitiesPromptCacheParamsAreGatedForCompatibleEndpoints(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want litellm.Support
	}{
		{name: "official default", cfg: Config{APIKey: "test"}, want: litellm.SupportYes},
		{name: "official explicit URL", cfg: Config{APIKey: "test", BaseURL: "https://api.openai.com/v1"}, want: litellm.SupportYes},
		{name: "unknown compatible relay", cfg: Config{APIKey: "test", BaseURL: "https://relay.example/v1"}, want: litellm.SupportUnknown},
		{name: "compatible relay opt-in", cfg: Config{APIKey: "test", BaseURL: "https://relay.example/v1", PromptCacheParams: true}, want: litellm.SupportYes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := New(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			caps := provider.Capabilities("gpt-5.1")
			if caps.Cache.PromptKey != tt.want || caps.Cache.Retention != tt.want {
				t.Fatalf("cache capability = %+v, want prompt/retention %v", caps.Cache, tt.want)
			}
		})
	}
}
