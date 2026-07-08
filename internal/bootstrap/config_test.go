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

func TestResolveContextWindowPerModel(t *testing.T) {
	cfg := Config{
		ContextWindow:  200000, // 全局
		ContextWindows: map[string]int{"gpt-5.5": 400000, "MiniMax-M3[1M]": 128000},
	}
	if w, src := cfg.ResolveContextWindow("gpt-5.5"); w != 400000 || src != CtxWindowConfig {
		t.Fatalf("gpt-5.5 应取 per-model 400000, got %d/%s", w, src)
	}
	if w, _ := cfg.ResolveContextWindow("MiniMax-M3[1M]"); w != 128000 {
		t.Fatalf("MiniMax 应取 per-model 128000, got %d", w)
	}
	// 未在 per-model 里的模型回落全局。
	if w, _ := cfg.ResolveContextWindow("other-model"); w != 200000 {
		t.Fatalf("未指定模型应回落全局 200000, got %d", w)
	}
}
