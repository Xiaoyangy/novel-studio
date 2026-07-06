package litellm

import (
	"context"
	"testing"
)

type capabilityProvider struct {
	testProvider
	caps Capabilities
}

func (p capabilityProvider) Capabilities(string) Capabilities {
	return p.caps
}

func TestGetCapabilitiesFallback(t *testing.T) {
	caps := GetCapabilities(&testProvider{name: "test"}, "model")
	if caps.Provider != "test" || caps.Model != "model" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != SupportUnknown {
		t.Fatalf("thinking support = %v, want unknown", caps.Thinking.Supported)
	}
}

func TestGetCapabilitiesFillsProviderAndModel(t *testing.T) {
	provider := capabilityProvider{
		testProvider: testProvider{name: "test"},
		caps: Capabilities{
			Thinking: ThinkingCapabilities{Supported: SupportYes},
		},
	}
	caps := GetCapabilities(&provider, "model")
	if caps.Provider != "test" || caps.Model != "model" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != SupportYes {
		t.Fatalf("thinking support = %v, want yes", caps.Thinking.Supported)
	}
}

func TestClientCapabilities(t *testing.T) {
	provider := &capabilityProvider{
		testProvider: testProvider{name: "test"},
		caps: Capabilities{
			Thinking: ThinkingCapabilities{Supported: SupportYes},
		},
	}
	client, err := New(provider)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := client.Capabilities("model")
	if caps.Provider != "test" || caps.Model != "model" || caps.Thinking.Supported != SupportYes {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestThinkingCapabilitiesSupportsEffort(t *testing.T) {
	caps := ThinkingCapabilities{Efforts: []string{"low", "high"}}
	if !caps.SupportsEffort("high") || caps.SupportsEffort("max") {
		t.Fatalf("effort support mismatch")
	}
}

func (p capabilityProvider) Chat(context.Context, *Request) (*Response, error) {
	return nil, nil
}

func (p capabilityProvider) Stream(context.Context, *Request) (Stream, error) {
	return nil, nil
}
