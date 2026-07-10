package agents

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
)

type fakeRoleSelection map[string]string

func (f fakeRoleSelection) CurrentSelection(role string) (string, string, bool) {
	model, ok := f[role]
	return "test", model, ok
}

func TestLoadBearingToolClassifier(t *testing.T) {
	// novel_context 承载世界/角色/计划注入，受保护，不可激进压缩。
	if loadBearingToolClassifier("novel_context") {
		t.Fatal("novel_context 应受保护(classifier=false)")
	}
	// 可再取的结果允许压缩。
	for _, name := range []string{"read_chapter", "check_consistency", "craft_recall", "web_research", "plan_details"} {
		if !loadBearingToolClassifier(name) {
			t.Fatalf("%s 应允许压缩(classifier=true)", name)
		}
	}
}

func TestWritingContextProfileForDrafterIsTighter(t *testing.T) {
	writer := writingContextProfileFor("writer")
	drafter := writingContextProfileFor("drafter")

	if drafter.keepRecentTokens >= writer.keepRecentTokens {
		t.Fatalf("drafter keepRecentTokens = %d, want tighter than writer %d", drafter.keepRecentTokens, writer.keepRecentTokens)
	}
	if drafter.toolKeepRecent >= writer.toolKeepRecent {
		t.Fatalf("drafter toolKeepRecent = %d, want tighter than writer %d", drafter.toolKeepRecent, writer.toolKeepRecent)
	}
	if drafter.storeKeepRecentTokens >= writer.storeKeepRecentTokens {
		t.Fatalf("drafter storeKeepRecentTokens = %d, want tighter than writer %d", drafter.storeKeepRecentTokens, writer.storeKeepRecentTokens)
	}
	if !drafter.commitOnProject {
		t.Fatal("drafter should commit projected compaction to avoid repeated light_trim warnings")
	}
	if writer.commitOnProject {
		t.Fatal("writer planner should keep non-committed projection behavior")
	}
	if drafter.lightTrim.KeepRecent >= 4 {
		t.Fatalf("drafter light trim KeepRecent = %d, want below default protection window", drafter.lightTrim.KeepRecent)
	}
}

func TestResolveRoleContextWindowUsesRoleSelectionBeforeWrappedModelFallback(t *testing.T) {
	cfg := bootstrap.Config{
		ContextWindow: 200000,
		ContextWindows: map[string]int{
			"gpt-5.6-sol": 372000,
		},
	}
	window, source, model := resolveRoleContextWindow(cfg, fakeRoleSelection{"writer": "gpt-5.6-sol"}, "writer", nil)
	if window != 372000 {
		t.Fatalf("window = %d, want role-specific 372000", window)
	}
	if source != bootstrap.CtxWindowConfig {
		t.Fatalf("source = %s, want config", source)
	}
	if model != "gpt-5.6-sol" {
		t.Fatalf("model = %q, want gpt-5.6-sol", model)
	}
}
