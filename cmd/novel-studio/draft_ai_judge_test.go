package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDraftAIJudgeFlags(t *testing.T) {
	flags, err := parseDraftAIJudgeFlags([]string{"--chapter", "2", "--budget", "2m"})
	if err != nil {
		t.Fatal(err)
	}
	if flags.Chapter != 2 || flags.Budget.String() != "2m0s" {
		t.Fatalf("flags = %+v", flags)
	}
	if _, err := parseDraftAIJudgeFlags([]string{"--chapter", "2", "--from", "1"}); err == nil {
		t.Fatal("--chapter with --from must fail")
	}
}

func TestSaveDraftDeepSeekAIJudgeDoesNotOverwriteFinalArtifact(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "reviews", "02_deepseek_ai_judge.json")
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath, []byte("final-sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	policy := newDeepSeekAIJudgeCachePolicy(selection, 2, "第二章\n\n正文")
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 2, CachePolicy: policy, CacheKey: reviewExistingCacheKey(policy),
		Provider: selection.Provider, Model: selection.Model,
		GeneratedAt: time.Now().Format(time.RFC3339), ReasoningEffort: policy.ReasoningEffort,
		BodySHA256: policy.BodySHA256, RawBodyOnly: true, UserPayloadKind: policy.UserPayloadKind,
		ModelSelection: selection,
	}
	if err := saveDraftDeepSeekAIJudge(dir, artifact); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "final-sentinel" {
		t.Fatalf("final artifact changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "drafts", "02_deepseek_ai_judge.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDraftChapterNumbersAndSelection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.draft.md", "03.draft.md", "03.plan.json", "notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	chapters, err := draftChapterNumbers(dir)
	if err != nil {
		t.Fatal(err)
	}
	selected := selectDraftJudgeChapters(chapters, draftAIJudgeFlags{Start: 2, End: 3})
	if len(selected) != 1 || selected[0] != 3 {
		t.Fatalf("selected = %v", selected)
	}
}
