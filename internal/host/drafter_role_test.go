package host

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/voocel/agentcore"
)

func TestApplyThinkingWriterAlsoUpdatesInheritedDrafter(t *testing.T) {
	calls := make(map[string]agentcore.ThinkingLevel)
	h := &Host{
		cfg: bootstrap.Config{Roles: map[string]bootstrap.RoleConfig{
			"writer": {ReasoningEffort: "high"},
		}},
		thinkingApplier: agents.ApplyThinking(func(role string, level agentcore.ThinkingLevel) {
			calls[role] = level
		}),
	}

	h.applyThinkingLocked("writer")
	if calls["writer"] != agentcore.ThinkingHigh || calls["drafter"] != agentcore.ThinkingHigh {
		t.Fatalf("inherited writer thinking calls = %#v, want writer+drafter high", calls)
	}

	calls = make(map[string]agentcore.ThinkingLevel)
	h.cfg.Roles["drafter"] = bootstrap.RoleConfig{ReasoningEffort: "xhigh"}
	h.applyThinkingLocked("writer")
	if calls["writer"] != agentcore.ThinkingHigh {
		t.Fatalf("explicit drafter case writer call = %#v", calls)
	}
	if _, linked := calls["drafter"]; linked {
		t.Fatalf("explicit drafter must not follow writer at runtime: %#v", calls)
	}
}

func TestRoleConfigForDrafterThinkingStartsFromWriter(t *testing.T) {
	wantFallback := bootstrap.ModelRef{Provider: "backup", Model: "fallback-model"}
	h := &Host{cfg: bootstrap.Config{Roles: map[string]bootstrap.RoleConfig{
		"writer": {
			Provider:  "primary",
			Model:     "planner-model",
			Fallbacks: []bootstrap.ModelRef{wantFallback},
			MaxTurns:  47,
		},
	}}}
	rc := h.roleConfigForThinkingLocked("drafter")
	if rc.Provider != "primary" || rc.Model != "planner-model" || rc.MaxTurns != 47 {
		t.Fatalf("seeded drafter config = %+v, want writer model/max-turn semantics", rc)
	}
	if len(rc.Fallbacks) != 1 || rc.Fallbacks[0] != wantFallback {
		t.Fatalf("seeded drafter fallbacks = %#v, want %#v", rc.Fallbacks, wantFallback)
	}
}
