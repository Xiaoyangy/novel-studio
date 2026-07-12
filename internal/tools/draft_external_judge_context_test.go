package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDraftExternalJudgeContextKeepsActionableFieldsOnly(t *testing.T) {
	dir := t.TempDir()
	reviewsDir := filepath.Join(dir, "reviews", "drafts")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
		"body_sha256":"abc",
		"ai_probability_percent":12,
		"pass_exclusive_percent":4,
		"blocking":true,
		"advice_complete":true,
		"summary":"对白像流程",
		"evidence":["连续轮流发言"],
		"revision_plan":["删去不改变局面的台词"],
		"dialogue_fix_plan":["保留真正有目的的开口"],
		"author_voice_plan":["强化主角偏见"],
		"rag_rules":["多人在场不等于人人发言"],
		"raw_response":"must-not-enter-context"
	}`)
	if err := os.WriteFile(filepath.Join(reviewsDir, "02_deepseek_ai_judge.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	context, err := loadDraftExternalJudgeContext(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if context == nil || context["ai_probability_percent"] != 12 {
		t.Fatalf("context = %#v", context)
	}
	if _, leaked := context["raw_response"]; leaked {
		t.Fatal("raw model response must not enter prose context")
	}
}
