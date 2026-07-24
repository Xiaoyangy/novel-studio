package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestEnsureGenerationIfEmptyIsIdempotentAndPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "progress.json")
	raw := []byte(`{"novel_name":"test","phase":"writing","current_chapter":0,"total_chapters":12,"completed_chapters":null,"total_word_count":0,"future_state":{"keep":true}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	progress, created, err := st.Progress.EnsureGenerationIfEmpty(
		"generation-1",
		domain.GenerationModeSimulationRestartFromSeed,
	)
	if err != nil || !created || progress == nil || progress.GenerationID != "generation-1" {
		t.Fatalf("initialize generation: progress=%+v created=%v err=%v", progress, created, err)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(afterFirst, &fields); err != nil {
		t.Fatal(err)
	}
	var futureState map[string]bool
	if err := json.Unmarshal(fields["future_state"], &futureState); err != nil || !futureState["keep"] {
		t.Fatalf("unknown progress field changed: %s err=%v", fields["future_state"], err)
	}

	progress, created, err = st.Progress.EnsureGenerationIfEmpty(
		"generation-2",
		domain.GenerationModeSimulationRestartFromSeed,
	)
	if err != nil || created || progress == nil || progress.GenerationID != "generation-1" {
		t.Fatalf("reuse generation: progress=%+v created=%v err=%v", progress, created, err)
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Fatal("idempotent generation reuse rewrote progress")
	}
}

func TestEnsureGenerationIfEmptyRejectsPartialLineageWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{GenerationID: "partial", TotalChapters: 12}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "progress.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Progress.EnsureGenerationIfEmpty(
		"replacement",
		domain.GenerationModeSimulationRestartFromSeed,
	); err == nil {
		t.Fatal("partially initialized generation was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected partial generation mutated progress")
	}
}

func TestEnsureGenerationIfEmptySerializesIndependentStores(t *testing.T) {
	dir := t.TempDir()
	firstStore := NewStore(dir)
	secondStore := NewStore(dir)
	if err := firstStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.Progress.Init("concurrent", 12); err != nil {
		t.Fatal(err)
	}
	type result struct {
		progress *domain.Progress
		created  bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for index, st := range []*Store{firstStore, secondStore} {
		proposed := fmt.Sprintf("generation-%d", index+1)
		go func() {
			<-start
			progress, created, err := st.Progress.EnsureGenerationIfEmpty(
				proposed,
				domain.GenerationModeSimulationRestartFromSeed,
			)
			results <- result{progress: progress, created: created, err: err}
		}()
	}
	close(start)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.progress == nil || second.progress == nil {
		t.Fatalf("concurrent generation initialization failed: first=%+v second=%+v", first, second)
	}
	if first.created == second.created {
		t.Fatalf("exactly one initializer must create the lineage: first=%+v second=%+v", first, second)
	}
	if first.progress.GenerationID != second.progress.GenerationID {
		t.Fatalf("concurrent initializers returned different lineages: first=%+v second=%+v", first, second)
	}
}

func TestSetTotalChaptersPreservesUnknownProgressFields(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "progress.json")
	raw := []byte(`{"novel_name":"test","phase":"init","current_chapter":0,"total_chapters":1,"completed_chapters":null,"total_word_count":0,"future_state":{"keep":true}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetTotalChapters(12); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(after, &fields); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := json.Unmarshal(fields["total_chapters"], &total); err != nil || total != 12 {
		t.Fatalf("total_chapters=%d err=%v", total, err)
	}
	var futureState map[string]bool
	if err := json.Unmarshal(fields["future_state"], &futureState); err != nil || !futureState["keep"] {
		t.Fatalf("unknown progress field changed: %s err=%v", fields["future_state"], err)
	}
}

func TestSetTotalChaptersRejectsCorruptKnownFieldWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "progress.json")
	before := []byte(`{"phase":"init","current_chapter":"corrupt","total_chapters":1}`)
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetTotalChapters(12); err == nil {
		t.Fatal("total_chapters update accepted a corrupt known progress field")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected total_chapters update mutated corrupt progress")
	}
}

func TestSetFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Flow != domain.FlowRewriting {
		t.Errorf("expected FlowRewriting, got %s", p.Flow)
	}
}

func TestSetNovelName(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetNovelName("长夜燃灯"); err != nil {
		t.Fatalf("SetNovelName: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.NovelName != "长夜燃灯" {
		t.Fatalf("expected novel name updated, got %q", p.NovelName)
	}
}

func TestSetFlowRejectsInvalidTransition(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow rewriting: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowReviewing); err == nil {
		t.Fatal("expected invalid flow transition to be rejected")
	}
}

func TestUpdatePhaseRejectsRegression(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase outline: %v", err)
	}
	if err := store.Progress.UpdatePhase(domain.PhasePremise); err == nil {
		t.Fatal("expected phase regression to be rejected")
	}
}

func TestStartChapter(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.StartChapter(1); err != nil {
		t.Fatalf("StartChapter: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("expected phase writing, got %s", p.Phase)
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("expected flow writing, got %s", p.Flow)
	}
	if p.CurrentChapter != 1 {
		t.Fatalf("expected current chapter 1, got %d", p.CurrentChapter)
	}
	if p.InProgressChapter != 1 {
		t.Fatalf("expected in-progress chapter 1, got %d", p.InProgressChapter)
	}
}

func TestStartChapterRejectsCompletePhase(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.UpdatePhase(domain.PhaseWriting)
	_ = store.Progress.MarkComplete()

	if err := store.Progress.StartChapter(1); err == nil {
		t.Fatal("expected complete -> writing transition to be rejected")
	}
	p, _ := store.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		t.Fatalf("phase changed to %s, want complete", p.Phase)
	}
	if p.InProgressChapter != 0 {
		t.Fatalf("in-progress chapter = %d, want 0", p.InProgressChapter)
	}
}

func TestStartChapterPreservesRewriteFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(1, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{1}, "测试重写")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	if err := store.Progress.StartChapter(1); err != nil {
		t.Fatalf("StartChapter: %v", err)
	}
	p, _ := store.Progress.Load()
	if p.Flow != domain.FlowRewriting {
		t.Fatalf("flow = %s, want rewriting", p.Flow)
	}
	if len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 1 {
		t.Fatalf("pending rewrites changed: %v", p.PendingRewrites)
	}
}

func TestStartChapterRejectsCorruptFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	p, _ := store.Progress.Load()
	p.Flow = domain.FlowState("bogus")
	if err := store.Progress.Save(p); err != nil {
		t.Fatalf("Save corrupt progress: %v", err)
	}

	if err := store.Progress.StartChapter(1); err == nil {
		t.Fatal("expected corrupt flow to be rejected")
	}
	p, _ = store.Progress.Load()
	if p.InProgressChapter != 0 {
		t.Fatalf("in-progress chapter = %d, want 0", p.InProgressChapter)
	}
}

func TestIsChapterCompleted(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if store.Progress.IsChapterCompleted(1) {
		t.Fatal("chapter 1 should not be completed initially")
	}

	_ = store.Progress.StartChapter(1)
	_ = store.Progress.MarkChapterComplete(1, 5000, "", "")

	if !store.Progress.IsChapterCompleted(1) {
		t.Fatal("chapter 1 should be completed after MarkChapterComplete")
	}
	if store.Progress.IsChapterCompleted(2) {
		t.Fatal("chapter 2 should not be completed")
	}
}

func TestSetPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")

	chapters := []int{3, 5, 7}
	if err := store.Progress.SetPendingRewrites(chapters, "角色动机不连贯"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(p.PendingRewrites))
	}
	if p.RewriteReason != "角色动机不连贯" {
		t.Errorf("reason mismatch: %s", p.RewriteReason)
	}
}

func TestSetPendingRewritesAndFlowEscalatesPolishAtomically(t *testing.T) {
	store := NewStore(t.TempDir())
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(1, 2600, "", "")
	if err := store.Progress.SetPendingRewritesAndFlow([]int{1}, "先打磨", domain.FlowPolishing); err != nil {
		t.Fatal(err)
	}
	if err := store.Progress.SetPendingRewritesAndFlow([]int{1}, "用户要求整章重写", domain.FlowRewriting); err != nil {
		t.Fatalf("polishing -> rewriting should be valid: %v", err)
	}
	p, err := store.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.Flow != domain.FlowRewriting || len(p.PendingRewrites) != 1 || p.RewriteReason != "用户要求整章重写" {
		t.Fatalf("unexpected atomic rewrite state: %+v", p)
	}
}

func TestSetPendingRewritesRejectsUnfinishedChapters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")

	if err := store.Progress.SetPendingRewrites([]int{3, 5}, "测试"); err == nil {
		t.Fatal("expected unfinished chapter to be rejected")
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

func TestValidateChapterWorkRejectsCorruptPendingRewriteQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 80)
	for ch := 1; ch <= 58; ch++ {
		_ = store.Progress.MarkChapterComplete(ch, 3000, "", "")
	}

	p, _ := store.Progress.Load()
	p.Flow = domain.FlowPolishing
	p.PendingRewrites = []int{65}
	if err := store.Progress.Save(p); err != nil {
		t.Fatalf("Save corrupt progress: %v", err)
	}

	if err := store.Progress.ValidateChapterWork(65); err == nil {
		t.Fatal("expected corrupt pending_rewrites to be rejected")
	}
}

func TestCompleteRewrite(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5, 7}, "测试重写")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// 完成第 5 章
	if err := store.Progress.CompleteRewrite(5); err != nil {
		t.Fatalf("CompleteRewrite(5): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Fatalf("expected 2 pending after removing 5, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowRewriting {
		t.Errorf("flow should still be rewriting, got %s", p.Flow)
	}

	// 完成第 3 章
	_ = store.Progress.CompleteRewrite(3)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(p.PendingRewrites))
	}

	// 完成最后一章 → 自动重置 Flow
	_ = store.Progress.CompleteRewrite(7)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("expected 0 pending, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow should reset to writing, got %s", p.Flow)
	}
	if p.RewriteReason != "" {
		t.Errorf("reason should be cleared, got %s", p.RewriteReason)
	}
	// Saga replay after queue drain must be a no-op, not writing -> writing
	// transition failure.
	if err := store.Progress.CompleteRewrite(7); err != nil {
		t.Fatalf("CompleteRewrite replay after drain: %v", err)
	}
}

func TestCompleteRewrite_NotInQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5}, "测试")

	// 完成不在队列中的章节不应报错
	if err := store.Progress.CompleteRewrite(99); err != nil {
		t.Fatalf("CompleteRewrite(99): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Errorf("queue should be unchanged, got %d", len(p.PendingRewrites))
	}
}

func TestClearPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(1, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{1, 2, 3}, "测试")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	if err := store.Progress.ClearPendingRewrites(); err != nil {
		t.Fatalf("ClearPendingRewrites: %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Errorf("expected empty, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow should be writing, got %s", p.Flow)
	}
}
