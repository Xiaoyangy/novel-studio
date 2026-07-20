package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestClearStaleFinalGlobalReviewIncludesExplicitLayeredShort(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		Layered: true, TotalChapters: 2, TotalWordCount: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "global", Verdict: "accept"}); err != nil {
		t.Fatal(err)
	}
	tool := NewCommitChapterTool(st)
	progress, _ := st.Progress.Load()
	tool.clearStaleFinalGlobalReview(progress)
	if review, err := st.World.LoadLastReview(2); err != nil || review != nil {
		t.Fatalf("stale layered-short global review survived commit invalidation: review=%+v err=%v", review, err)
	}
}
