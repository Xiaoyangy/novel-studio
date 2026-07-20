package agents

import (
	"strings"
	"testing"
)

func TestRenderDrafterSystemPromptOverridesRetiredContextCallBeforeFirstToken(t *testing.T) {
	base := `执行协议：先调用一次 novel_context(chapter=N, profile="draft")。`
	ordinary := renderDrafterSystemPrompt(base, false)
	if ordinary != base {
		t.Fatalf("ordinary Drafter prompt changed: %q", ordinary)
	}

	sealed := renderDrafterSystemPrompt(base, true)
	for _, required := range []string{
		"最高优先级",
		"覆盖上方执行协议第 1 步",
		"Host 已在本次真实 provider 调用前",
		"novel_context 在本会话不可用且禁止调用",
		"不得用一次控制响应试探工具",
		"第一次响应直接调用 draft_chapter",
		"生成首字前执行其中 anti_ai_render_contract",
	} {
		if !strings.Contains(sealed, required) {
			t.Fatalf("sealed Drafter prompt lost override %q: %s", required, sealed)
		}
	}
	if strings.LastIndex(sealed, "覆盖上方执行协议第 1 步") < strings.LastIndex(sealed, "先调用一次 novel_context") {
		t.Fatal("server-primed override did not appear after the historical base protocol")
	}
}
