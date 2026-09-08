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

func seedVerifiedRebaseRefresh(t *testing.T) (*store.Store, map[string]any) {
	t.Helper()
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	archive := filepath.Join(root, "archives", "sealed-rebase-test", "output", "novel")
	st, archived := store.NewStore(live), store.NewStore(archive)
	for _, item := range []*store.Store{st, archived} {
		if err := item.Init(); err != nil {
			t.Fatal(err)
		}
		seedChapterZeroRefreshFoundation(t, item)
	}
	if err := archived.Progress.Save(&domain.Progress{Phase: domain.PhaseWriting, CurrentChapter: 2, CompletedChapters: []int{1}, TotalWordCount: 2000, GenerationID: "old-generation"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{Phase: domain.PhaseInit, CurrentChapter: 0, TotalChapters: 12,
		GenerationID: "simulation-rebased", GenerationMode: domain.GenerationModeSimulationRestartFromSeed}); err != nil {
		t.Fatal(err)
	}
	digest, err := store.DirectoryContentRoot(archive)
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{
		"version": "pipeline-all-chapter-rebase.v1", "source_output": live, "source_root": digest,
		"archive_output": archive, "archive_root": digest, "previous_progress": filepath.Join(archive, "meta", "progress.json"),
		"new_generation_id": "simulation-rebased", "rebased_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeRebaseRefreshReceipt(t, st, receipt)
	return st, receipt
}

func writeRebaseRefreshReceipt(t *testing.T, st *store.Store, receipt map[string]any) {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "all_chapter_rebase.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedRebaseInitGenerationAllowsOnlyExplicitOneShotFoundationRefresh(t *testing.T) {
	st, receipt := seedVerifiedRebaseRefresh(t)
	before, _ := store.DirectoryContentRoot(st.Dir())
	if err := RequireChapterZeroFoundationRefreshState(st); err != nil {
		t.Fatalf("verified rebase init generation could not reach explicit Architect refresh: %v", err)
	}
	if after, _ := store.DirectoryContentRoot(st.Dir()); after != before {
		t.Fatal("read-only refresh preflight changed progress or receipt bytes")
	}
	if NewSaveFoundationTool(st).chapterZeroRebaseOutlineReplacementAuthorized() {
		t.Fatal("archive proof alone granted an unrestricted tool mutation")
	}
	tool := NewSaveFoundationTool(st).WithFoundationTypeRestriction("layered_outline").WithFoundationRefreshEpoch(true).WithOneShotFoundationRefresh(true)
	if tool.chapterZeroRebaseOutlineReplacementAuthorized() {
		t.Fatal("rebase refresh capability was accepted without its execution lease")
	}
	const owner = "verified-rebase-refresh-test"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(owner) })
	if NewSaveFoundationTool(st).WithChapterZeroFoundationRefresh(true).chapterZeroFoundationRefreshAuthorized() {
		t.Fatal("general refresh flag bypassed the rebased one-shot/type restriction")
	}
	if !tool.chapterZeroRebaseOutlineReplacementAuthorized() {
		t.Fatal("verified rebase with current lease and one-shot target was rejected")
	}
	args := json.RawMessage(`{"type":"layered_outline","scale":"short","content":[{"index":1,"title":"第一卷","theme":"保全证据","arcs":[{"index":1,"title":"雨夜","goal":"完成当班更正","chapters":[{"chapter":1,"title":"原件","core_event":"林澄发现封条异常并当面记录经手顺序","hook":"记录里的数量仍待核对"}]}]}]}`)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("one-shot rebased outline refresh failed: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("same sidecar reused its one-shot replacement authority")
	}
	if p, _ := st.Progress.Load(); p.GenerationID != receipt["new_generation_id"] || p.GenerationMode != domain.GenerationModeSimulationRestartFromSeed {
		t.Fatal("refresh silently removed the rebase generation identity")
	}
	if actual, _ := store.DirectoryContentRoot(receipt["archive_output"].(string)); actual != receipt["archive_root"] {
		t.Fatal("refresh rewrote archive content")
	}
}

func TestRebaseFoundationRefreshRejectsForgedStaleOrDirtyEvidence(t *testing.T) {
	for _, variant := range []string{"bare marker", "version", "generation", "source root", "source path", "archive path", "previous progress", "archive bytes", "mode", "prose", "zero-init", "cursor", "character memory", "old checkpoint"} {
		t.Run(variant, func(t *testing.T) {
			st, receipt := seedVerifiedRebaseRefresh(t)
			switch variant {
			case "bare marker":
				receipt = map[string]any{"new_generation_id": "simulation-rebased"}
			case "version":
				receipt["version"] = "untrusted"
			case "generation":
				receipt["new_generation_id"] = "stale-generation"
			case "source root":
				receipt["source_root"] = "sha256:other"
			case "source path":
				receipt["source_output"] = receipt["archive_output"]
			case "archive path":
				receipt["archive_output"] = st.Dir()
			case "previous progress":
				receipt["previous_progress"] = filepath.Join(st.Dir(), "meta", "progress.json")
			case "archive bytes":
				if err := os.WriteFile(filepath.Join(receipt["archive_output"].(string), "premise.md"), []byte("changed archive"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				p, _ := st.Progress.Load()
				p.GenerationMode = "other-generation"
				if err := st.Progress.Save(p); err != nil {
					t.Fatal(err)
				}
			case "prose", "zero-init", "cursor", "character memory":
				rel := map[string]string{"prose": "chapters/nested/01.md", "zero-init": "meta/initial_character_dynamics.json", "cursor": "meta/planning/v2/projection_cursor.json", "character memory": "meta/character_agents/memory/ca_old.json"}[variant]
				path := filepath.Join(st.Dir(), filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"dirty":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "old checkpoint":
				if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "plan", "drafts/01.plan.json", "old-plan"); err != nil {
					t.Fatal(err)
				}
			}
			writeRebaseRefreshReceipt(t, st, receipt)
			before, _ := store.DirectoryContentRoot(st.Dir())
			if err := RequireChapterZeroFoundationRefreshState(st); err == nil {
				t.Fatal("invalid rebase evidence granted foundation refresh authority")
			}
			if after, _ := store.DirectoryContentRoot(st.Dir()); after != before {
				t.Fatal("failed preflight changed user data")
			}
		})
	}
}
