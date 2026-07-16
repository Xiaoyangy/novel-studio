package bootstrap

import "testing"

func TestConfigResolveReasoningEffort(t *testing.T) {
	cfg := Config{
		ReasoningEffort: "low", // 顶层默认
		Roles: map[string]RoleConfig{
			"writer":    {Provider: "p", Model: "m", ReasoningEffort: "high"}, // 角色覆盖
			"architect": {Provider: "p", Model: "m"},                          // 无 reasoning_effort，应回落默认
		},
	}

	cases := []struct {
		role string
		want string
	}{
		{"writer", "high"},     // 角色覆盖优先
		{"architect", "low"},   // 角色未配 → 回落顶层默认
		{"editor", "low"},      // 角色不存在 → 顶层默认
		{"", "low"},            // 空 → 顶层默认
		{"default", "low"},     // default → 顶层默认
		{"coordinator", "low"}, // 未配 → 顶层默认
	}
	for _, c := range cases {
		if got := cfg.ResolveReasoningEffort(c.role); got != c.want {
			t.Errorf("ResolveReasoningEffort(%q) = %q, want %q", c.role, got, c.want)
		}
	}

	// 顶层默认也为空时，未覆盖角色返回 ""（不覆盖）。
	empty := Config{Roles: map[string]RoleConfig{"writer": {ReasoningEffort: "xhigh"}}}
	if got := empty.ResolveReasoningEffort("editor"); got != "" {
		t.Errorf("空默认下 editor 应返回 \"\"，得 %q", got)
	}
	if got := empty.ResolveReasoningEffort("writer"); got != "xhigh" {
		t.Errorf("空默认下 writer 覆盖应生效，得 %q", got)
	}
}

func TestDrafterConfigInheritsWriterUntilExplicitlyConfigured(t *testing.T) {
	cfg := Config{
		ReasoningEffort: "low",
		Roles: map[string]RoleConfig{
			"writer": {ReasoningEffort: "high", MaxTurns: 42},
		},
	}
	for _, role := range []string{"drafter", "draft_finalizer"} {
		if got := cfg.ResolveReasoningEffort(role); got != "high" {
			t.Errorf("ResolveReasoningEffort(%q) = %q, want inherited writer high", role, got)
		}
		if got := cfg.ResolveMaxTurns(role, 80); got != 42 {
			t.Errorf("ResolveMaxTurns(%q) = %d, want inherited writer 42", role, got)
		}
	}
	if got := cfg.ResolveReasoningEffort("world_simulator"); got != "high" {
		t.Errorf("world_simulator reasoning = %q, want writer high", got)
	}

	cfg.Roles["drafter"] = RoleConfig{ReasoningEffort: "xhigh", MaxTurns: 63}
	for _, role := range []string{"drafter", "draft_finalizer"} {
		if got := cfg.ResolveReasoningEffort(role); got != "xhigh" {
			t.Errorf("explicit ResolveReasoningEffort(%q) = %q, want xhigh", role, got)
		}
		if got := cfg.ResolveMaxTurns(role, 80); got != 63 {
			t.Errorf("explicit ResolveMaxTurns(%q) = %d, want 63", role, got)
		}
	}
	if got := cfg.ResolveReasoningEffort("world_simulator"); got != "high" {
		t.Errorf("explicit drafter must not move world_simulator off writer, got %q", got)
	}
}

func TestResolveContextWindowPerModel(t *testing.T) {
	cfg := Config{
		ContextWindow:  200000, // 全局
		ContextWindows: map[string]int{"gpt-5.6-sol": 372000, "MiniMax-M3[1M]": 128000},
	}
	if w, src := cfg.ResolveContextWindow("gpt-5.6-sol"); w != 372000 || src != CtxWindowConfig {
		t.Fatalf("gpt-5.6-sol 应取 per-model 372000, got %d/%s", w, src)
	}
	if w, _ := cfg.ResolveContextWindow("MiniMax-M3[1M]"); w != 128000 {
		t.Fatalf("MiniMax 应取 per-model 128000, got %d", w)
	}
	// 未在 per-model 里的模型回落全局。
	if w, _ := cfg.ResolveContextWindow("other-model"); w != 200000 {
		t.Fatalf("未指定模型应回落全局 200000, got %d", w)
	}
}
