package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func setupRewriteCommitSaga(t *testing.T) (*store.Store, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("commit-saga", 3); err != nil {
		t.Fatal(err)
	}
	oldBody := "# 第一章 旧稿\n\n旧终稿留在柜台上。"
	newBody := "# 第一章 新稿\n\n返工后，许闻溪把授权单压在柜台角上，只保留能由日志证明的那一笔。"
	if err := st.Drafts.SaveFinalChapter(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(oldBody)), "desire", "quest"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "测试可恢复返工提交"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, newBody); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter": 1, "summary": "许闻溪收束授权链路证据",
		"characters": []string{"许闻溪"}, "key_events": []string{"收束授权链路"},
		"hook_type": "desire", "dominant_strand": "quest",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, args, newBody
}

func oneShotCommitFailure(point string) func(string) error {
	fired := false
	return func(got string) error {
		if got != point || fired {
			return nil
		}
		fired = true
		return errors.New("simulated process stop")
	}
}

func TestRewriteCommitRecoversAfterFinalReplace(t *testing.T) {
	st, args, newBody := setupRewriteCommitSaga(t)
	tool := NewCommitChapterTool(st)
	tool.failCommitAt = oneShotCommitFailure(commitFailureAfterFinalSaved)
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), commitFailureAfterFinalSaved) {
		t.Fatalf("expected injected stop after final replace, got %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); final != newBody {
		t.Fatalf("candidate was not written before injected stop: %q", final)
	}
	pending, err := st.Signals.LoadPendingCommit()
	if err != nil || pending == nil || pending.Mode != domain.CommitModeRewrite || pending.Stage != domain.CommitStageStarted {
		t.Fatalf("rewrite identity was not durable at final-write crash: pending=%+v err=%v", pending, err)
	}

	// Recovery must use the persisted payload. The caller only identifies the
	// chapter; changing its summary cannot alter the in-flight commit intent.
	recoveryArgs := json.RawMessage(`{"chapter":1,"summary":"不应覆盖 saga payload"}`)
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), recoveryArgs); err != nil {
		t.Fatalf("recover rewrite after final replace: %v", err)
	}
	if pending, _ := st.Signals.LoadPendingCommit(); pending != nil {
		t.Fatalf("pending commit not cleared after recovery: %+v", pending)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("recovered rewrite did not append commit checkpoint")
	}
	if summary, _ := st.Summaries.LoadSummary(1); summary == nil || summary.Summary != "许闻溪收束授权链路证据" {
		t.Fatalf("recovery used mutable call args instead of persisted payload: %+v", summary)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "reviews", "01_ai_gate.json")); err != nil {
		t.Fatalf("quality stage missing after recovery: %v", err)
	}
}

func TestRewriteCommitRecoversAfterQueueDrainBeforeCheckpoint(t *testing.T) {
	st, args, newBody := setupRewriteCommitSaga(t)
	tool := NewCommitChapterTool(st)
	tool.failCommitAt = oneShotCommitFailure(commitFailureAfterProgressUpdated)
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), commitFailureAfterProgressUpdated) {
		t.Fatalf("expected injected stop after CompleteRewrite, got %v", err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatal(err)
	}
	if len(progress.PendingRewrites) != 0 || progress.Flow != domain.FlowWriting {
		t.Fatalf("fixture did not stop after queue drain: %+v", progress)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp != nil {
		t.Fatalf("checkpoint should not exist before recovery: %+v", cp)
	}
	pending, _ := st.Signals.LoadPendingCommit()
	if pending == nil || pending.Stage != domain.CommitStageProgressMarked {
		t.Fatalf("progress stage was not persisted before stop: %+v", pending)
	}

	// final==draft and an empty PendingRewrites queue used to route this call to
	// the completed-chapter skip path. The pending saga must win both races.
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("recover drained rewrite: %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); final != newBody {
		t.Fatalf("recovered final changed unexpectedly: %q", final)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("recovery skipped checkpoint after queue drain")
	}
	if pending, _ := st.Signals.LoadPendingCommit(); pending != nil {
		t.Fatalf("pending commit not cleared: %+v", pending)
	}
}

func TestCompletedInitialCommitRecoveryFailsClosedAfterDraftMutation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("commit-saga", 2); err != nil {
		t.Fatal(err)
	}
	body := "# 第一章 原稿\n\n许闻溪把钥匙收进抽屉。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter": 1, "summary": "许闻溪收好钥匙", "characters": []string{"许闻溪"},
		"key_events":              []string{"收好钥匙"},
		"character_stage_records": testCharacterStageRecords("许闻溪"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewCommitChapterTool(st)
	tool.failCommitAt = oneShotCommitFailure(commitFailureAfterProgressUpdated)
	_, stoppedErr := tool.Execute(context.Background(), args)
	if stoppedErr == nil {
		t.Fatal("expected stop after completed progress")
	}
	if !st.Progress.IsChapterCompleted(1) {
		t.Fatalf("fixture did not reach completed progress: %v", stoppedErr)
	}
	if err := st.Drafts.SaveDraft(1, "# 第一章 被替换\n\n这是另一个候选正文。"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "draft SHA/字数已变化") {
		t.Fatalf("completed recovery did not fail closed on draft mutation: %v", err)
	}
	if pending, _ := st.Signals.LoadPendingCommit(); pending == nil {
		t.Fatal("failed-closed recovery must retain pending evidence")
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp != nil {
		t.Fatalf("mutated draft must not receive a commit checkpoint: %+v", cp)
	}
}
