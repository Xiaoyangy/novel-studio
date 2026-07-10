package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRewriteRecoveryRecognizesSwappedCandidateWithoutOldReview(t *testing.T) {
	root := t.TempDir()
	st := store.NewStore(root)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	before := "# 第一章\n\n旧正文"
	candidate := "# 第一章\n\n新正文"
	if err := stageRewriteRecovery(root, 1, before, candidate); err != nil {
		t.Fatal(err)
	}
	pending, err := rewriteAwaitingReview(root, st, 1, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("swapped candidate without a fresh review must resume at review")
	}
}

func TestRewriteRecoveryDoesNotTreatUnswappedBodyAsPending(t *testing.T) {
	root := t.TempDir()
	st := store.NewStore(root)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	before := "# 第一章\n\n旧正文"
	if err := stageRewriteRecovery(root, 1, before, "# 第一章\n\n候选正文"); err != nil {
		t.Fatal(err)
	}
	pending, err := rewriteAwaitingReview(root, st, 1, before)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("body that was never swapped must remain on the normal rewrite path")
	}
}

func TestRewriteRecoverySupportsLegacyMatchingCheckpoint(t *testing.T) {
	root := t.TempDir()
	st := store.NewStore(root)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "# 第一章\n\n已换入正文"
	chapterRel := filepath.ToSlash(filepath.Join("chapters", "01.md"))
	if err := os.MkdirAll(filepath.Join(root, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(chapterRel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rewrite-existing", chapterRel); err != nil {
		t.Fatal(err)
	}
	pending, err := rewriteAwaitingReview(root, st, 1, body)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("legacy matching rewrite checkpoint must resume at review")
	}
}
