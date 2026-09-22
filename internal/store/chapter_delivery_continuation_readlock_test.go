package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChapterDeliveryContinuationPreflightRejectsMissingOrRedirectedLockWithoutWrites(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			g := chapterDeliveryTestGeneration(t, st, "authorization-read-lock-"+mode, true)
			now := chapterDeliveryTestNow()
			if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), chapterDeliveryRoot, ".write.lock")
			outside := filepath.Join(t.TempDir(), "other-lock")
			if err := os.Rename(path, outside); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			before := generationProtocolFilesForTest(t, st.Dir())
			outsideBefore, err := os.Lstat(outside)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AuthorizeChapterDeliveryOverrun(g, "must not repair a guard", now); err == nil {
				t.Fatal("missing or redirected read lock accepted")
			}
			if !reflect.DeepEqual(before, generationProtocolFilesForTest(t, st.Dir())) {
				t.Fatal("invalid read lock wrote files")
			}
			outsideAfter, err := os.Lstat(outside)
			if err != nil || !os.SameFile(outsideBefore, outsideAfter) {
				t.Fatalf("outside guard changed: %v", err)
			}
			if mode == "symlink" {
				if target, err := os.Readlink(path); err != nil || target != outside {
					t.Fatalf("symlink changed: %v", err)
				}
			}
		})
	}
}

func TestChapterDeliveryContinuationPreflightWaitsForLedgerWriter(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "authorization-read-transaction", true)
	now := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	ledger, err := st.readChapterDeliveryLedger()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	// Model a writer's uncommitted state under its real OS lock. An
	// authorization reader must wait, not inspect this in-flight snapshot.
	if err := st.withChapterDeliveryLock(func() error {
		if err := os.WriteFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath), []byte("{writer-not-published"), 0o644); err != nil {
			return err
		}
		started := make(chan struct{})
		go func() {
			close(started)
			finished <- NewStore(st.Dir()).AuthorizeChapterDeliveryOverrun(g, "read only a committed ledger", now)
		}()
		<-started
		select {
		case err := <-finished:
			t.Errorf("authorization preflight read through the ledger writer lock: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		return st.writeChapterDeliveryLedger(ledger)
	}); err != nil {
		t.Fatal(err)
	}
	if t.Failed() {
		return
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("authorization failed after committed ledger publication: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("authorization did not resume after writer released lock")
	}
}
