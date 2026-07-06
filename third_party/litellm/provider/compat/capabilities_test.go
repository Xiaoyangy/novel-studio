package compat

import (
	"testing"

	"github.com/voocel/litellm"
)

func TestCapabilitiesDefaultFromSpec(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.test"}, Spec{
		Name: "compat-test",
		Request: RequestSpec{
			Thinking:           func(*litellm.Thinking, string) (map[string]any, error) { return map[string]any{"thinking": true}, nil },
			SupportsJSONSchema: true,
		},
		Response: ResponseSpec{
			ReasoningFields:           []string{"reasoning_content"},
			HasCompletionTokenDetails: true,
			HasCacheTokens:            true,
		},
		Features: FeatureSpec{StrictTools: StrictToolsForward},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := provider.Capabilities("model")
	if caps.Provider != "compat-test" || caps.Model != "model" {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Thinking.Supported != litellm.SupportYes || caps.Structured.JSONSchema != litellm.SupportYes {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Structured.Strict != litellm.SupportPartial {
		t.Fatalf("structured strict = %v, want partial", caps.Structured.Strict)
	}
	if caps.Tools.StrictSchema != litellm.SupportYes || caps.Reasoning.StreamingDeltas != litellm.SupportYes {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Usage.CacheReadTokens != litellm.SupportYes || caps.Usage.CacheWriteTokens != litellm.SupportPartial {
		t.Fatalf("usage caps = %+v", caps.Usage)
	}
}

func TestCapabilitiesMapperCanOverrideDefaults(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.test"}, Spec{
		Name: "compat-test",
		Capabilities: func(model string, caps litellm.Capabilities) litellm.Capabilities {
			caps.Model = model + "-override"
			caps.Thinking.Supported = litellm.SupportNo
			return caps
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := provider.Capabilities("model")
	if caps.Model != "model-override" || caps.Thinking.Supported != litellm.SupportNo {
		t.Fatalf("caps = %+v", caps)
	}
}
