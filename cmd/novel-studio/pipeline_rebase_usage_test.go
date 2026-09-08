package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestRebasePreservesUsageWALAndIdempotentRecovery(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	archive := filepath.Join(root, "archives", "old", "output", "novel")
	candidate := filepath.Join(root, ".canon-rebase", "rebase-test", "output")
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	meter, err := host.NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	msg := agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{Provider: "test", Model: "test", Input: 100, Cost: &agentcore.Cost{Total: 1.25}}}
	if err := meter.Record("paid-before-rebase", "writer", msg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(live, store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "meta/runtime/queue.jsonl", `{"obsolete":"generation activity"}`)
	if err := copyPipelineRenderCandidateTree(live, archive); err != nil {
		t.Fatal(err)
	}
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		t.Fatal(err)
	}
	liveRoot, _ := store.DirectoryContentRoot(live)
	archiveRoot, _ := store.DirectoryContentRoot(archive)
	if err := resetPipelineAllChapterCandidate(candidate, &zeroInitProject{}, archive); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(candidate, store.UsageAuditPath))
	if err != nil || string(after) != string(before) {
		t.Fatalf("billing audit was deleted or rewritten: %v", err)
	}
	if _, err := os.Stat(filepath.Join(candidate, "meta/runtime/queue.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("old runtime survived: %v", err)
	}
	restored, err := host.NewDurableUsageMeter(store.NewStore(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Record("paid-before-rebase", "writer", msg); err != nil {
		t.Fatal(err)
	}
	cost, input, _, _, _ := restored.Tracker().Totals()
	if cost != 1.25 || input != 100 {
		t.Fatalf("rebase replay changed paid usage: %v %v", cost, input)
	}
	if current, _ := store.DirectoryContentRoot(live); current != liveRoot {
		t.Fatal("candidate reset changed live")
	}
	if current, _ := store.DirectoryContentRoot(archive); current != archiveRoot {
		t.Fatal("candidate reset changed archive")
	}
}

func TestRebaseRuntimeCleanupRejectsLinksBeforeRemovingOtherEntries(t *testing.T) {
	for _, linked := range []string{"runtime", "audit"} {
		t.Run(linked, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			rebaseAllTestWriteFile(t, outside, "keep", "unchanged")
			if linked == "runtime" {
				if err := os.MkdirAll(filepath.Join(root, "meta"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "meta", "runtime")); err != nil {
					t.Fatal(err)
				}
			} else {
				rebaseAllTestWriteFile(t, root, "meta/runtime/queue.jsonl", "also unchanged")
				if err := os.Symlink(filepath.Join(outside, "keep"), filepath.Join(root, store.UsageAuditPath)); err != nil {
					t.Fatal(err)
				}
			}
			if err := resetPipelineRebaseRuntimePreservingUsage(root); err == nil {
				t.Fatal("unsafe linked runtime accepted")
			}
			if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "unchanged" {
				t.Fatal("cleanup escaped candidate")
			}
			if linked == "audit" {
				if data, err := os.ReadFile(filepath.Join(root, "meta/runtime/queue.jsonl")); err != nil || string(data) != "also unchanged" {
					t.Fatal("cleanup wrote before its safety preflight")
				}
			}
		})
	}
}
