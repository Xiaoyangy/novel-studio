package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func frozenCompleteBookContract(t *testing.T, st *store.Store, chapters, words int) domain.OutlineAllExecutionReceipt {
	t.Helper()
	r := sealedShortOutlineReceipt(t, st.Dir())
	r.Status = domain.OutlineAllExecutionComplete
	r.EstimatedScale = fmt.Sprintf("1-1卷，%d-%d章；正文%d-%d字", chapters, chapters, words, words)
	r.MinChapters, r.MaxChapters, r.TargetChapters = chapters, chapters, chapters
	r.TargetWords, r.TargetWordsPerChapter = words, words/chapters
	r.FinalLayeredDigest = "sha256:" + strings.Repeat("3", 64)
	r.FinalFlatDigest = "sha256:" + strings.Repeat("4", 64)
	r.ArchitectReadinessJSONDigest = "sha256:" + strings.Repeat("5", 64)
	r.ArchitectReadinessMDDigest = "sha256:" + strings.Repeat("6", 64)
	r, err := domain.SignOutlineAllExecutionReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(r); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || loaded == nil || loaded.ReceiptDigest != r.ReceiptDigest {
		t.Fatalf("fixture did not persist a valid complete whole-book contract: %v", err)
	}
	// Initialize the existing runtime flock file before measuring tool writes.
	if _, err := st.Runtime.LoadPipelineExecution(); err != nil {
		t.Fatal(err)
	}
	return r
}

func completeBookAcceptedChapter(t *testing.T, st *store.Store, chapter, words int) {
	t.Helper()
	if err := st.Drafts.SaveFinalChapter(chapter, strings.Repeat("文", words)); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(chapter, words, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: chapter, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteBookFrozenContractChecksActualCoverageAndWords(t *testing.T) {
	for _, name := range []string{"partial", "missing", "duplicate", "outside", "under", "over", "missing_body", "empty_body", "stale_review", "unreviewed", "corrupt_receipt", "building_receipt"} {
		t.Run(name, func(t *testing.T) {
			st := completeBookSetup(t)
			if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
				t.Fatal(err)
			}
			r := frozenCompleteBookContract(t, st, 3, 6000)
			for chapter := 1; chapter <= 3; chapter++ {
				completeBookAcceptedChapter(t, st, chapter, 2000)
			}
			p, err := st.Progress.Load()
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "partial":
				p.CompletedChapters, p.TotalChapters = []int{1}, 1
			case "missing":
				p.CompletedChapters = []int{1, 3}
			case "duplicate":
				p.CompletedChapters = []int{1, 2, 2}
			case "outside":
				completeBookAcceptedChapter(t, st, 4, 2000)
				p.CompletedChapters = []int{1, 2, 3, 4}
			case "under", "over":
				words := 1999
				if name == "over" {
					words = 2001
				}
				completeBookAcceptedChapter(t, st, 3, words)
				// Deliberately lie in progress: only real body bytes count.
				p.TotalWordCount, p.ChapterWordCounts[3] = 6000, 2000
			case "missing_body":
				if err := os.Remove(filepath.Join(st.Dir(), "chapters", "03.md")); err != nil {
					t.Fatal(err)
				}
			case "empty_body":
				completeBookAcceptedChapter(t, st, 3, 0)
			case "stale_review":
				if err := st.Drafts.SaveFinalChapter(3, strings.Repeat("改", 2000)); err != nil {
					t.Fatal(err)
				}
			case "unreviewed":
				if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 3, Scope: "chapter", Verdict: "revise"}); err != nil {
					t.Fatal(err)
				}
			case "corrupt_receipt":
				if err := os.WriteFile(filepath.Join(st.Dir(), store.OutlineAllExecutionReceiptPath), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "building_receipt":
				r.Status = domain.OutlineAllExecutionBuilding
				r, err = domain.SignOutlineAllExecutionReceipt(r)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.SaveOutlineAllExecutionReceipt(r); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Progress.Save(p); err != nil {
				t.Fatal(err)
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"complete_book","content":{}}`)); err == nil {
				t.Fatal("incomplete frozen book was marked complete")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatal("failed terminal guard wrote state", err)
			}
		})
	}
}

func TestCompleteBookFrozenContractRejectsZeroChapters(t *testing.T) {
	st := completeBookSetup(t)
	frozenCompleteBookContract(t, st, 150, 300000)
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"type": "complete_book", "content": map[string]any{}})
	result, err := NewSaveFoundationTool(st).Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("PREMATURE_FROZEN_BOOK_COMPLETION: 150 chapters / 300000 words frozen, zero written chapters, tool accepted: %s", result)
	}
	after, loadErr := store.DirectoryContentRoot(st.Dir())
	if loadErr != nil || before != after {
		t.Fatalf("rejection must be zero-write: %v", loadErr)
	}
}

func TestCompleteBookFrozenContractUsesExplicitRangeNotMidpointOrProgressCache(t *testing.T) {
	for _, words := range []int{2000, 2100, 2200} {
		t.Run(fmt.Sprint(words), func(t *testing.T) {
			st := completeBookSetup(t)
			if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
				t.Fatal(err)
			}
			r := frozenCompleteBookContract(t, st, 3, 6300)
			r.EstimatedScale = "1-1卷，3-3章；正文6000-6600字"
			r, err := domain.SignOutlineAllExecutionReceipt(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveOutlineAllExecutionReceipt(r); err != nil {
				t.Fatal(err)
			}
			for chapter := 1; chapter <= 3; chapter++ {
				completeBookAcceptedChapter(t, st, chapter, words)
			}
			p, err := st.Progress.Load()
			if err != nil {
				t.Fatal(err)
			}
			p.TotalWordCount, p.TotalChapters, p.ChapterWordCounts = 1, 1, nil
			if err := st.Progress.Save(p); err != nil {
				t.Fatal(err)
			}
			if _, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"complete_book","content":{}}`)); err != nil {
				t.Fatalf("actual reviewed prose in explicit frozen range was rejected: %v", err)
			}
			p, err = st.Progress.Load()
			if err != nil || p.Phase != domain.PhaseComplete {
				t.Fatal("complete result not persisted", err)
			}
		})
	}
}

func TestCompleteBookFrozenContractCannotChangeReviewPolicyAndRequiresCurrentGlobal(t *testing.T) {
	st := completeBookSetup(t)
	frozenCompleteBookContract(t, st, 3, 6000)
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 3; chapter++ {
		completeBookAcceptedChapter(t, st, chapter, 2000)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Layered = true // Explicit short policy still requires its existing global review.
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	for _, scale := range []string{"", "long"} {
		before, err := store.DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		args, _ := json.Marshal(map[string]any{"type": "complete_book", "content": map[string]any{}, "scale": scale})
		if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil {
			t.Fatal("missing global review bypassed")
		}
		after, err := store.DirectoryContentRoot(st.Dir())
		if err != nil || before != after {
			t.Fatal("rejected terminal request changed planning tier or phase", err)
		}
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 3, Scope: "global", Verdict: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"complete_book","content":{}}`)); err != nil {
		t.Fatal(err)
	}
	// A later body replacement invalidates global review even if a fresh
	// chapter review exists for that replacement.
	p.Phase = domain.PhaseWriting
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, strings.Repeat("改", 2000)); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"complete_book","content":{}}`)); err == nil {
		t.Fatal("stale global body binding accepted")
	}
}

func TestSaveReviewFrozenContractGuardsEveryAutomaticCompletionPath(t *testing.T) {
	for _, path := range []string{"nonlayered", "reopened_layered", "short_global"} {
		t.Run(path, func(t *testing.T) {
			st := completeBookSetup(t)
			frozenCompleteBookContract(t, st, 3, 6000)
			completeBookAcceptedChapter(t, st, 1, 2000)
			p, err := st.Progress.Load()
			if err != nil {
				t.Fatal(err)
			}
			p.TotalChapters = 1 // The mutable completion cache must not shrink the contract.
			r := domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}
			tier := domain.PlanningTierLong
			if path == "reopened_layered" {
				p.Layered, p.ReopenedFromComplete = true, true
			}
			if path == "short_global" {
				tier, r.Scope = domain.PlanningTierShort, "global"
				if err := st.World.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.RunMeta.SetPlanningTier(tier); err != nil {
				t.Fatal(err)
			}
			if err := st.Progress.Save(p); err != nil {
				t.Fatal(err)
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			done, _, err := NewSaveReviewTool(st).completeBookIfReady(r, p)
			if err == nil || done {
				t.Fatal("save_review completed an incomplete frozen book")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatal("automatic terminal guard changed phase or merged manuscript", err)
			}
		})
	}
}

func TestSaveReviewFrozenContractRealToolCannotShrinkBookToProgressCache(t *testing.T) {
	st := completeBookSetup(t)
	frozenCompleteBookContract(t, st, 3, 6000)
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatal(err)
	}
	completeBookAcceptedChapter(t, st, 1, 2000)
	if err := st.Progress.SetTotalChapters(1); err != nil {
		t.Fatal(err)
	}
	r := domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", ContractStatus: "met", Summary: "本章当前终稿已按八项逐一核查。"}
	for name := range expectedReviewDimensions {
		r.Dimensions = append(r.Dimensions, domain.DimensionScore{Dimension: name, Score: 90, Comment: "当前正文已逐项核查，未发现阻断。"})
	}
	args, _ := json.Marshal(r)
	_, err := NewSaveReviewTool(st).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "1-3") {
		t.Fatalf("expected frozen full-book guard after valid review submission: %v", err)
	}
	p, err := st.Progress.Load()
	if err != nil || p.Phase != domain.PhaseWriting {
		t.Fatal("review submission prematurely completed frozen book", err)
	}
	if !st.World.HasAcceptedChapterReview(1) {
		t.Fatal("legitimate in-flight chapter review should remain saved")
	}
}

func TestCompleteBookFrozenLongContractAccepts150ActualReviewedChapters(t *testing.T) {
	st := completeBookSetup(t)
	frozenCompleteBookContract(t, st, 150, 300000)
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 150; chapter++ {
		completeBookAcceptedChapter(t, st, chapter, 2000)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), json.RawMessage(`{"type":"complete_book","content":{}}`)); err != nil {
		t.Fatal(err)
	}
	p, err := st.Progress.Load()
	if err != nil || p.Phase != domain.PhaseComplete || len(p.CompletedChapters) != 150 {
		t.Fatal("complete reviewed scope did not finish", err)
	}
}
