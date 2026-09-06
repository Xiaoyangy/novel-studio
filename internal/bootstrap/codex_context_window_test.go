package bootstrap

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/voocel/agentcore"
)

func codexBudgetForTest(t *testing.T, model agentcore.ChatModel) int {
	t.Helper()
	codex, ok := model.(*llmcodex.CodexModel)
	if !ok {
		t.Fatalf("not a Codex target: %T", model)
	}
	return codex.ExactAgentContextWindow()
}

func TestCodexContextWindowConfigurationReachesDefaultRoleFallbackAndSwap(t *testing.T) {
	cfg := Config{Provider: "subscription", ModelName: "custom-default", ContextWindow: 200_000,
		Providers:      map[string]ProviderConfig{"subscription": {Type: "codex-cli", BaseURL: "/never-execute-context-window-test"}},
		ContextWindows: map[string]int{"custom-role": 128_000, "custom-fallback": 96_000, "custom-next": 372_000},
		Roles:          map[string]RoleConfig{"world_arbiter": {Provider: "subscription", Model: "custom-role", Fallbacks: []ModelRef{{Provider: "subscription", Model: "custom-fallback"}}}},
	}
	models, err := NewModelSet(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := codexBudgetForTest(t, models.Default.SwappableModel.Current()); got != 200_000 {
		t.Fatalf("default budget=%d", got)
	}
	if got := codexBudgetForTest(t, models.models["world_arbiter"].SwappableModel.Current()); got != 128_000 {
		t.Fatalf("role-specific budget=%d", got)
	}
	if got := codexBudgetForTest(t, models.fallbacks["world_arbiter"][0].model); got != 96_000 {
		t.Fatalf("fallback used the primary model window: %d", got)
	}
	if err := models.Swap("world_arbiter", "subscription", "custom-next"); err != nil {
		t.Fatal(err)
	}
	if got := codexBudgetForTest(t, models.models["world_arbiter"].SwappableModel.Current()); got != 372_000 {
		t.Fatalf("swapped model retained stale window: %d", got)
	}
	if err := models.Swap("default", "subscription", "custom-fallback"); err != nil {
		t.Fatal(err)
	}
	if got := codexBudgetForTest(t, models.Default.SwappableModel.Current()); got != 96_000 {
		t.Fatalf("default swap retained stale window: %d", got)
	}
}

func TestCodexContextWindowUnknownKeepsLegacyAndRegistryWindowIsOperational(t *testing.T) {
	for _, name := range []string{"unknown-codex-window-fixture-84939", "gpt-5.3-codex", "gpt-6-astra"} {
		cfg := Config{Provider: "subscription", ModelName: name, ContextWindows: map[string]int{"gpt-6-astra": 272_000}, Providers: map[string]ProviderConfig{"subscription": {Type: "codex-cli", BaseURL: "/never-execute-context-window-test"}}}
		models, err := NewModelSet(cfg)
		if err != nil {
			t.Fatal(err)
		}
		window, source := cfg.ResolveContextWindow(name)
		if name == "gpt-5.3-codex" && (source != CtxWindowRegistry || window <= 0) {
			t.Fatal("known-model fixture does not exercise the registry window path")
		}
		if name == "gpt-6-astra" && (source != CtxWindowConfig || window != 272_000) {
			t.Fatal("explicit GPT6 operating budget did not take precedence over registry/default")
		}
		if source == CtxWindowDefault {
			window = 0
		}
		if got := codexBudgetForTest(t, models.Default.SwappableModel.Current()); got != window {
			t.Fatalf("%s budget=%d, expected %d from %s", name, got, window, source)
		}
	}
	cache := map[string]agentcore.ChatModel{}
	provider := ProviderConfig{Type: "codex-cli", BaseURL: "/never-execute-context-window-test"}
	first, err := createModelFromConfig("subscription", "same-target", provider, cache, modelCreateOptions{contextWindow: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	second, err := createModelFromConfig("subscription", "same-target", provider, cache, modelCreateOptions{contextWindow: 200_000})
	if err != nil || first == second || codexBudgetForTest(t, second) != 200_000 {
		t.Fatal("cache reused a model with a different operating budget")
	}
}
