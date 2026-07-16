package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
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

func TestPassingDraftJudgePreservesReproducibleLocalStructuralMarker(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if err := toolspkg.SetDraftExternalRerenderRequirement(st.Dir(), toolspkg.DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		AIProbabilityPercent: 18, PassExclusivePercent: 4, AdviceComplete: true,
		RevisionPlan: []string{"整章重排同型流程，重建人物选择与关系后果。"},
	}); err != nil {
		t.Fatal(err)
	}
	reviewDir := filepath.Join(st.Dir(), "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	passingStatus, _ := json.Marshal(map[string]any{
		"body_sha256": reviewreport.BodySHA256(body), "blocking": false,
		"advice_complete": true, "ai_probability_percent": 3, "pass_exclusive_percent": 4,
	})
	if err := os.WriteFile(filepath.Join(reviewDir, "01_deepseek_ai_judge.json"), passingStatus, 0o644); err != nil {
		t.Fatal(err)
	}
	if !toolspkg.CurrentDraftHasLocalStructuralBlock(st, 1) {
		t.Fatal("fixture no longer reproduces a local whole-text/segment structural block")
	}

	cleared, err := clearDraftRerenderRequirementAfterPassingJudge(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
		t.Fatal("independent DeepSeek pass cleared a reproducible local structural marker")
	}
	marker := filepath.Join(reviewDir, "01_full_rerender_required.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("local structural marker was not preserved: %v", err)
	}
	inspection, err := toolspkg.InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != toolspkg.DraftExternalGateRerenderAuthorized {
		t.Fatalf("passing judge unlocked locally blocked hash: inspection=%+v err=%v", inspection, err)
	}
}

func TestPassingDraftJudgeClearsMarkerWhenLocalStructureIsClean(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章 雨停以后\n\n林澈推开门，桌边的人只看了他一眼，又低头去分那袋刚送来的橘子。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if err := toolspkg.SetDraftExternalRerenderRequirement(st.Dir(), toolspkg.DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		AIProbabilityPercent: 12, PassExclusivePercent: 4, AdviceComplete: true,
		RevisionPlan: []string{"调整局部节奏。"},
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := clearDraftRerenderRequirementAfterPassingJudge(st, 1)
	if err != nil || !cleared {
		t.Fatalf("clean current hash did not clear obsolete marker: cleared=%v err=%v", cleared, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "reviews", "drafts", "01_full_rerender_required.json")); !os.IsNotExist(err) {
		t.Fatalf("obsolete marker still exists: %v", err)
	}
}
