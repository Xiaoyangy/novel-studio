package store_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func usageCopyTestStore(t *testing.T, role string, cost float64) (*store.Store, *host.DurableUsageMeter) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	meter, err := host.NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("same-call-id", role, agentcore.Message{Usage: &agentcore.Usage{Provider: "test", Model: "copy-model", Input: 12, Output: 2, Cost: &agentcore.Cost{Total: cost}}}); err != nil {
		t.Fatal(err)
	}
	return st, meter
}

func usageCopyTestRead(t *testing.T, st *store.Store, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(st.Dir(), rel))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func usageCopyTestSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestUsageCopyToCandidatePreservesExactWholeLedgerWithoutAddingTotals(t *testing.T) {
	live, meter := usageCopyTestStore(t, "architect", 2.5)
	candidate, _ := usageCopyTestStore(t, "writer", 99)
	if err := meter.StartCall("pending-real-call", "writer", "pg2_copy", 123, "boot-1"); err != nil {
		t.Fatal(err)
	}
	if err := meter.Cover("covered-role-group"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		st        *store.Store
		rel, text string
	}{
		{live, "private-source-only.txt", "source-private"}, {candidate, "candidate-only.txt", "candidate-private"},
		{live, "meta/runtime/usage_audit.lock", "source-lock"}, {candidate, "meta/runtime/usage_audit.lock", "candidate-lock"},
	} {
		if err := os.WriteFile(filepath.Join(pair.st.Dir(), pair.rel), []byte(pair.text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.DirectoryContentRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.Stat(filepath.Join(candidate.Dir(), "meta/runtime/usage_audit.lock"))
	proof, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage)
	if err != nil || proof == nil {
		t.Fatalf("copy failed: %v", err)
	}
	for _, rel := range []string{"meta/usage.json", store.UsageAuditPath} {
		if !bytes.Equal(usageCopyTestRead(t, live, rel), usageCopyTestRead(t, candidate, rel)) {
			t.Fatalf("copy changed exact source bytes: %s", rel)
		}
	}
	if proof.SnapshotSHA256 != usageCopyTestSHA(usageCopyTestRead(t, live, "meta/usage.json")) || proof.JournalSHA256 != usageCopyTestSHA(usageCopyTestRead(t, live, store.UsageAuditPath)) || proof.AuditOffset != int64(len(usageCopyTestRead(t, live, store.UsageAuditPath))) {
		t.Fatal("copy proof does not bind the complete exact pair")
	}
	if got, _ := store.DirectoryContentRoot(live.Dir()); got != before {
		t.Fatal("candidate accounting copy changed live state")
	}
	if _, err := os.Stat(filepath.Join(candidate.Dir(), "private-source-only.txt")); !os.IsNotExist(err) || string(usageCopyTestRead(t, candidate, "candidate-only.txt")) != "candidate-private" {
		t.Fatal("copy reached beyond its two allowed accounting artifacts")
	}
	lockAfter, _ := os.Stat(filepath.Join(candidate.Dir(), "meta/runtime/usage_audit.lock"))
	sourceLock, _ := os.Stat(filepath.Join(live.Dir(), "meta/runtime/usage_audit.lock"))
	if !os.SameFile(lockBefore, lockAfter) || os.SameFile(sourceLock, lockAfter) || string(usageCopyTestRead(t, candidate, "meta/runtime/usage_audit.lock")) != "candidate-lock" {
		t.Fatal("copy replaced or shared the source accounting lock inode")
	}
	restored, err := host.NewDurableUsageMeter(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if cost, _, _, _, _ := restored.Tracker().Totals(); cost != 2.5 || len(restored.PendingCalls()) != 1 || !restored.Covered("covered-role-group") {
		t.Fatal("candidate combined separate ledgers or lost pending/group identities")
	}
}

func TestUsageCopyToCandidateIsIdempotentAndRestoresMissingSnapshot(t *testing.T) {
	live, meter := usageCopyTestStore(t, "architect", 3)
	candidate := store.NewStore(t.TempDir())
	if err := meter.StartCall("pending", "writer", "pg2_copy", 321, ""); err != nil {
		t.Fatal(err)
	}
	first, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.DirectoryContentRoot(candidate.Dir())
	infoBefore, _ := os.Stat(filepath.Join(candidate.Dir(), "meta/usage.json"))
	second, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("identical source retry changed proof: %v", err)
	}
	infoAfter, _ := os.Stat(filepath.Join(candidate.Dir(), "meta/usage.json"))
	if after, _ := store.DirectoryContentRoot(candidate.Dir()); after != before || !os.SameFile(infoBefore, infoAfter) {
		t.Fatal("identical retry rewrote candidate accounting")
	}
	// Simulate the safe crash window after WAL installation but before the
	// final snapshot rename. The WAL alone must recover the complete source.
	if err := os.Remove(filepath.Join(candidate.Dir(), "meta/usage.json")); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := host.NewDurableUsageMeter(candidate)
	if err != nil || len(rebuilt.PendingCalls()) != 1 {
		t.Fatalf("candidate WAL failed to recover its source ledger: %v", err)
	}
	if cost, _, _, _, _ := rebuilt.Tracker().Totals(); cost != 3 {
		t.Fatal("WAL-only recovery lost the source's accounting base")
	}
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(usageCopyTestRead(t, live, "meta/usage.json"), usageCopyTestRead(t, candidate, "meta/usage.json")) {
		t.Fatal("recopy did not restore the exact frozen source snapshot")
	}
}

func TestUsageCopyToCandidateRejectsLaggingSnapshotWithoutTouchingCandidate(t *testing.T) {
	live, meter := usageCopyTestStore(t, "architect", 1)
	candidate, _ := usageCopyTestStore(t, "writer", 5)
	old := usageCopyTestRead(t, live, "meta/usage.json")
	if err := meter.StartCall("new-tail", "writer", "pg2_copy", 123, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live.Dir(), "meta/usage.json"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := store.DirectoryContentRoot(candidate.Dir())
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage); err == nil || !strings.Contains(err.Error(), "WAL end") {
		t.Fatalf("lagging source snapshot was copied: %v", err)
	}
	if after, _ := store.DirectoryContentRoot(candidate.Dir()); after != before {
		t.Fatal("invalid source changed candidate accounting")
	}
	if err := meter.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage); err != nil {
		t.Fatalf("flushed source did not become copyable: %v", err)
	}
}

func TestUsageCopyToCandidateRejectsOverlapAndSymlinkPaths(t *testing.T) {
	live, _ := usageCopyTestStore(t, "architect", 1)
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(live.Usage); err == nil {
		t.Fatal("copy accepted its own live store as staging")
	}
	nestedDir := filepath.Join(live.Dir(), "nested-candidate")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(store.NewStore(nestedDir).Usage); err == nil {
		t.Fatal("copy accepted a candidate nested in live")
	}
	candidate, _ := usageCopyTestStore(t, "writer", 5)
	snapshotPath := filepath.Join(candidate.Dir(), "meta/usage.json")
	if err := os.Remove(snapshotPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(live.Dir(), "meta/usage.json"), snapshotPath); err != nil {
		t.Fatal(err)
	}
	original := usageCopyTestRead(t, live, "meta/usage.json")
	if _, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage); err == nil {
		t.Fatal("copy followed candidate symlink into live accounting")
	}
	if !bytes.Equal(original, usageCopyTestRead(t, live, "meta/usage.json")) {
		t.Fatal("rejected target symlink changed source")
	}
}

func TestUsageCopyToCandidateReverseConcurrentCopiesUseConsistentLockOrder(t *testing.T) {
	left, _ := usageCopyTestStore(t, "architect", 1)
	right, _ := usageCopyTestStore(t, "writer", 2)
	errs := make(chan error, 2)
	go func() { _, err := left.Usage.CopyAuditedSnapshotToCandidate(right.Usage); errs <- err }()
	go func() { _, err := right.Usage.CopyAuditedSnapshotToCandidate(left.Usage); errs <- err }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reverse candidate copies deadlocked on usage locks")
		}
	}
	if !bytes.Equal(usageCopyTestRead(t, left, store.UsageAuditPath), usageCopyTestRead(t, right, store.UsageAuditPath)) {
		t.Fatal("serialized copy left mixed independent journals")
	}
}

func TestUsageCopyToCandidateRejectsHardlinkedLockBeforeAcquiringTwice(t *testing.T) {
	live, _ := usageCopyTestStore(t, "architect", 1)
	candidate, _ := usageCopyTestStore(t, "writer", 2)
	sourceLock := filepath.Join(live.Dir(), "meta/runtime/usage_audit.lock")
	targetLock := filepath.Join(candidate.Dir(), "meta/runtime/usage_audit.lock")
	if err := os.Remove(targetLock); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(sourceLock, targetLock); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := live.Usage.CopyAuditedSnapshotToCandidate(candidate.Usage); result <- err }()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "lock inode") {
			t.Fatalf("shared source lock was not rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy deadlocked by locking a shared inode twice")
	}
}
