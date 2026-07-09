package headless

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
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

func TestShouldStopAfterInitialWorldTickRequiresSubstantiveEvents(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("长篇", 120); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("empty zero-init world_tick baseline must not stop the stage")
	}
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{
		TickID:            "v1-a1",
		Chapter:           0,
		Actors:            []string{"县城商户"},
		Summary:           "青山县商户在开局前形成第一条离屏价格波动。",
		VisibilityChapter: 1,
		VisibilityPath:    "收据和街面闲聊",
	}}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("events without a substantive world_tick cursor must not stop the stage")
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, ThroughChapter: 0, EventCount: 1}); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("substantive world events should stop the stage")
	}
}

func TestShouldStopAfterInitialWorldTickUsesLayeredOutlineWhenProgressUninitialized(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		t.Fatal(err)
	}
	if shouldStopAfterInitialWorldTickReady(dir) {
		t.Fatal("layered outline without progress.layered must still reject empty world_tick baseline")
	}
}
