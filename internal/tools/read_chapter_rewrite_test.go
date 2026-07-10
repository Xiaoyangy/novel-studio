package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestReadChapterWithholdsOldFinalDuringRewriteReplanning(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		TotalChapters:     2,
		CompletedChapters: []int{1},
		PendingRewrites:   []int{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "# 第1章 旧终稿\n\n旧事件"); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	finalTime := time.Now()
	if err := os.Chtimes(filepath.Join(dir, "drafts/01.plan.json"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "chapters/01.md"), finalTime, finalTime); err != nil {
		t.Fatal(err)
	}

	tool := NewReadChapterTool(st)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var blocked map[string]any
	if err := json.Unmarshal(raw, &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked["withheld"] != true || blocked["stage"] != "rewrite_replanning" {
		t.Fatalf("expected planning-stage withholding, got %s", raw)
	}
	if _, ok := blocked["content"]; ok {
		t.Fatal("old final content must not enter rewrite planning context")
	}

	newTime := finalTime.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "drafts/01.plan.json"), newTime, newTime); err != nil {
		t.Fatal(err)
	}
	raw, err = tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"final"}`))
	if err != nil {
		t.Fatal(err)
	}
	var allowed map[string]any
	if err := json.Unmarshal(raw, &allowed); err != nil {
		t.Fatal(err)
	}
	if allowed["content"] == nil || allowed["withheld"] == true {
		t.Fatalf("expected final read after fresh plan, got %s", raw)
	}
}
