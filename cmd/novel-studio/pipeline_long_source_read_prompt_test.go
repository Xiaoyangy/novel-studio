package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestLongSourceRefreshReadsExactTargetAndNecessaryDependencies(t *testing.T) {
	live := longArchitectRefreshFixture(t, true)
	base, err := pipelineArchitectRefreshPrompt(live, "同步既有秘密来源，保留人物原字段", "characters")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := pipelineArchitectShortSelectedTargets("characters")
	if err != nil {
		t.Fatal(err)
	}
	prompt := pipelineArchitectShortRefreshTargetPrompt(base, targets[0])
	for _, want := range []string{"必须先完整读取本轮目标", `novel_context(source="world_rules.json")`, "依赖源读取失败时停止", "只允许调用一次 save_foundation(type=\"characters\")"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("LONG_SOURCE_READ_POLICY_MISSING: %q", want)
		}
	}
	if strings.Contains(prompt, "若读取失败，直接依据下方创作总令") {
		t.Fatal("long source refresh permits blind overwrite after failed read")
	}
}

func TestShortSourceRefreshPromptRetainsLegacyBytes(t *testing.T) {
	targets, err := pipelineArchitectShortSelectedTargets("characters")
	if err != nil {
		t.Fatal(err)
	}
	prompt := pipelineArchitectShortRefreshTargetPrompt("legacy short source refresh", targets[0])
	// Captured from the actual pre-change helper, not derived by the new branch.
	const want = "180133d512e75955ee58653499f6f899587dfc0a4e3ab097b3933c630a31f07d"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(prompt))); got != want {
		t.Fatalf("legacy short prompt changed: %s", got)
	}
}
