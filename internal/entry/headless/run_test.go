package headless

import (
	"os"
	"path/filepath"
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

func TestShouldStopAfterFoundationChangedRequiresDigestChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json", filepath.Join("meta", "compass.json")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"ready":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"outline.json", "layered_outline.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	initial := foundationRevisionDigest(dir)
	if initial == "" {
		t.Fatal("expected initial foundation digest")
	}
	if shouldStopAfterFoundationChanged(dir, initial) {
		t.Fatal("unchanged foundation should not stop")
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterFoundationChanged(dir, initial) {
		t.Fatal("changed foundation should stop")
	}
}

func TestShouldStopAfterChapterCommitRequiresNewCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "第一版正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	initial := latestChapterCommitSeq(dir, 1)
	if shouldStopAfterChapterCommit(dir, 1, initial) {
		t.Fatal("existing commit must not stop a resumed rewrite")
	}
	if err := st.Drafts.SaveFinalChapter(1, "第二版正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit", "chapters/01.md"); err != nil {
		t.Fatal(err)
	}
	if !shouldStopAfterChapterCommit(dir, 1, initial) {
		t.Fatal("new commit checkpoint should return control to pipeline review")
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
