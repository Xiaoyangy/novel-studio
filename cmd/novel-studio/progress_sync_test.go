package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSyncCompletedChapterWordCountsUsesFinalChapterText(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("测试长篇", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 5461, "crisis", "growth"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	finalText := "许闻溪把辞职信塞回抽屉。\n\n她说，不急。"
	if err := st.Drafts.SaveFinalChapter(1, finalText); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}

	updated, err := syncCompletedChapterWordCounts(st)
	if err != nil {
		t.Fatalf("syncCompletedChapterWordCounts: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated=%d, want 1", updated)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Load progress: %v", err)
	}
	want := domain.WordCount(finalText)
	if progress.ChapterWordCounts[1] != want || progress.TotalWordCount != want {
		t.Fatalf("word counts not synced: chapter=%d total=%d want=%d", progress.ChapterWordCounts[1], progress.TotalWordCount, want)
	}
	if len(progress.HookHistory) != 1 || progress.HookHistory[0] != "crisis" {
		t.Fatalf("hook history not preserved: %+v", progress.HookHistory)
	}
	if len(progress.StrandHistory) != 1 || progress.StrandHistory[0] != "growth" {
		t.Fatalf("strand history not preserved: %+v", progress.StrandHistory)
	}

	updated, err = syncCompletedChapterWordCounts(st)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if updated != 0 {
		t.Fatalf("second sync updated=%d, want 0", updated)
	}
}

func TestSyncCompletedChapterWordCountsClearsCompletedInProgressPointer(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("测试长篇", 3); err != nil {
		t.Fatal(err)
	}
	finalText := "第一章\n\n这一章已经交付。"
	if err := st.Drafts.SaveFinalChapter(1, finalText); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, domain.WordCount(finalText), "crisis", "growth"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Load progress: %v", err)
	}
	progress.InProgressChapter = 1
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("seed stale progress: %v", err)
	}

	updated, err := syncCompletedChapterWordCounts(st)
	if err != nil {
		t.Fatalf("syncCompletedChapterWordCounts: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated=%d, want 1 for stale pointer repair", updated)
	}
	progress, err = st.Progress.Load()
	if err != nil {
		t.Fatalf("Load repaired progress: %v", err)
	}
	if progress.InProgressChapter != 0 || progress.CurrentChapter != 2 {
		t.Fatalf("stale in-progress pointer not repaired: %+v", progress)
	}
}
