package agents

import (
	"strings"
	"testing"
)

func TestAgentPromptCacheKeyIsStableOpaqueAndConversationScoped(t *testing.T) {
	const secretPath = "/Users/example/Private Novel/项目甲"
	first := agentPromptCacheKey("World Arbiter", secretPath, "generation-1", "chapter-7")
	second := agentPromptCacheKey("World Arbiter", secretPath, "generation-1", "chapter-7")
	other := agentPromptCacheKey("World Arbiter", secretPath, "generation-1", "chapter-8")

	if first != second {
		t.Fatalf("same conversation identity produced unstable cache keys: %q != %q", first, second)
	}
	if first == other {
		t.Fatalf("different conversations shared one cache key: %q", first)
	}
	if strings.Contains(first, secretPath) || strings.Contains(first, "项目甲") {
		t.Fatalf("cache key leaked raw project identity: %q", first)
	}
	if !strings.HasPrefix(first, "novel-studio:world-arbiter:") {
		t.Fatalf("cache key role was not normalized: %q", first)
	}
	if len(first) > 64 {
		t.Fatalf("cache key should remain provider-friendly and bounded: len=%d key=%q", len(first), first)
	}
}

func TestSanitizePromptCacheRoleFallsBackForNonASCIIName(t *testing.T) {
	if got := sanitizePromptCacheRole(" 世界裁判 "); got != "agent" {
		t.Fatalf("unexpected non-ASCII role normalization: %q", got)
	}
}
