package headless

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestShouldStopAfterChapterWaitsForPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("返工小说", 10); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 5; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, "已有正文"); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 5, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.SetPendingRewrites([]int{2, 5}, "重建前五章"); err != nil {
		t.Fatal(err)
	}

	if shouldStopAfterChapter(dir, 5) {
		t.Fatal("expected stop-after to wait while target-range rewrites are pending")
	}

	if err := st.Progress.ClearPendingRewrites(); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapter(dir, 5) {
		t.Fatal("expected stop-after to fire after pending rewrites are drained")
	}
}
