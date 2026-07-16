package bootstrap

import (
	"fmt"
	"testing"

	"github.com/voocel/agentcore"
)

func TestIsBillingExhaustedError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits.", true},
		{"You exceeded your current quota, please check your plan and billing details", true},
		{"insufficient balance", true},
		{"账户余额不足", true},
		{"invalid api key", false},
		{"rate limit exceeded", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isBillingExhaustedError(errFromString(c.msg)); got != c.want {
			t.Errorf("isBillingExhaustedError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestCallOptionsForTargetOverridesInheritedReasoning(t *testing.T) {
	opts := callOptionsForTarget(
		[]agentcore.CallOption{agentcore.WithThinking(agentcore.ThinkingLevel("ultra"))},
		modelTarget{reasoningEffort: "high"},
	)
	if got := agentcore.ResolveCallConfig(opts).ThinkingLevel; got != agentcore.ThinkingHigh {
		t.Fatalf("fallback thinking = %q, want high", got)
	}
}

func TestModelSetDrafterAliasAndExplicitSelection(t *testing.T) {
	base := Config{
		Provider:  "local",
		ModelName: "default-model",
		Providers: map[string]ProviderConfig{
			"local": {Type: "openai"},
		},
		Roles: map[string]RoleConfig{
			"writer": {
				Provider: "local",
				Model:    "planner-model",
				Fallbacks: []ModelRef{
					{Provider: "local", Model: "planner-fallback"},
				},
			},
		},
	}
	ms, err := NewModelSet(base)
	if err != nil {
		t.Fatalf("NewModelSet inherited drafter: %v", err)
	}
	for _, role := range []string{"drafter", "draft_finalizer"} {
		provider, model, explicit := ms.CurrentSelection(role)
		if provider != "local" || model != "planner-model" || !explicit {
			t.Errorf("%s selection = %s/%s explicit=%v, want inherited local/planner-model true", role, provider, model, explicit)
		}
		fallbacks := ms.FallbackTargets(role)
		if len(fallbacks) != 1 || fallbacks[0].Model != "planner-fallback" {
			t.Errorf("%s fallbacks = %#v, want inherited planner-fallback", role, fallbacks)
		}
	}
	provider, model, _ := ms.CurrentSelection("world_simulator")
	if provider != "local" || model != "planner-model" {
		t.Errorf("world_simulator selection = %s/%s, want writer model", provider, model)
	}

	explicit := base
	explicit.Roles = map[string]RoleConfig{
		"writer":  base.Roles["writer"],
		"drafter": {Provider: "local", Model: "prose-model"},
	}
	ms, err = NewModelSet(explicit)
	if err != nil {
		t.Fatalf("NewModelSet explicit drafter: %v", err)
	}
	if provider, model, selected := ms.CurrentSelection("drafter"); provider != "local" || model != "prose-model" || !selected {
		t.Errorf("explicit drafter = %s/%s explicit=%v, want local/prose-model true", provider, model, selected)
	}
	if _, model, _ := ms.CurrentSelection("draft_finalizer"); model != "prose-model" {
		t.Errorf("draft_finalizer model = %s, want explicit drafter prose-model", model)
	}
	if _, model, _ := ms.CurrentSelection("world_simulator"); model != "planner-model" {
		t.Errorf("world_simulator moved with explicit drafter: got %s", model)
	}
}

func errFromString(s string) error {
	if s == "" {
		return nil
	}
	return fmt.Errorf("%s", s)
}
