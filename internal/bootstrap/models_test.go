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

func errFromString(s string) error {
	if s == "" {
		return nil
	}
	return fmt.Errorf("%s", s)
}
