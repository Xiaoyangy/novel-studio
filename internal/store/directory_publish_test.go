package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type directoryPublishFixture struct {
	base          string
	live          string
	candidate     string
	transactions  string
	beforeRoot    string
	candidateRoot string
	request       PublishDirectoryRequest
}

func TestDirectoryContentRootExcludesOnlyReservedAtomicTemps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stable.json"), []byte("stable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := DirectoryContentRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"usage.json.tmp-2326855527", ".planning-12345.tmp", ".receipt.json.tmp-987"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("transient\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := DirectoryContentRoot(root); err != nil || got != want {
		t.Fatalf("reserved atomic temps changed directory root: got=%s want=%s err=%v", got, want, err)
	}
	if err := os.WriteFile(filepath.Join(root, "durable.tmp-not-a-number"), []byte("durable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DirectoryContentRoot(root); err != nil || got == want {
		t.Fatalf("non-reserved durable file was excluded: got=%s want_old=%s err=%v", got, want, err)
	}
}

func TestDirectoryPublishRecoversEveryJournaledPhaseAndFinalizes(t *testing.T) {
	for _, stage := range []string{
		"intent_written",
		"live_archived",
		"candidate_promoted",
		"receipt_written",
	} {
		t.Run(stage, func(t *testing.T) {
			fixture := newDirectoryPublishFixture(t, "tx-"+stage)
			publisher := NewDirectoryPublishStore(fixture.transactions)
			fired := false
			injected := errors.New("injected " + stage)
			publisher.testFault = func(current string) error {
				if !fired && current == stage {
					fired = true
					return injected
				}
				return nil
			}
			if _, err := publisher.PublishDirectory(fixture.request); !errors.Is(err, injected) {
				t.Fatalf("publish fault = %v, want %v", err, injected)
			}
			if !fired {
				t.Fatalf("fault stage %q was not reached", stage)
			}

			restarted := NewDirectoryPublishStore(fixture.transactions)
			receipt, err := restarted.RecoverDirectoryPublish(fixture.request.TransactionID)
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			assertDirectoryPublishCommitted(t, fixture, *receipt)

			repeated, err := restarted.PublishDirectory(fixture.request)
			if err != nil {
				t.Fatalf("idempotent publish: %v", err)
			}
			if repeated.ReceiptDigest != receipt.ReceiptDigest ||
				repeated.CommittedAt != receipt.CommittedAt {
				t.Fatalf("idempotent publish changed receipt: first=%+v second=%+v", receipt, repeated)
			}

			if err := restarted.FinalizeDirectoryPublish(fixture.request.TransactionID); err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if err := restarted.FinalizeDirectoryPublish(fixture.request.TransactionID); err != nil {
				t.Fatalf("idempotent finalize: %v", err)
			}
			if _, err := os.Lstat(receipt.ArchiveDir); !os.IsNotExist(err) {
				t.Fatalf("archive survived finalize: %v", err)
			}
			if _, err := os.Lstat(fixture.candidate); !os.IsNotExist(err) {
				t.Fatalf("candidate residue survived finalize: %v", err)
			}
			state, err := restarted.LoadDirectoryPublishState(fixture.request.TransactionID)
			if err != nil {
				t.Fatalf("load finalized state: %v", err)
			}
			if state == nil || state.Phase != DirectoryPublishFinalized ||
				state.Intent != nil || state.Receipt == nil {
				t.Fatalf("unexpected finalized state: %+v", state)
			}
		})
	}
}

func TestDirectoryPublishFinalizedReceiptAllowsLaterCanonicalWrites(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "finalized-live-may-advance")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	if _, err := publisher.PublishDirectory(fixture.request); err != nil {
		t.Fatal(err)
	}
	if err := publisher.FinalizeDirectoryPublish(fixture.request.TransactionID); err != nil {
		t.Fatal(err)
	}
	// Outcome/cursor/render receipts and execution-lock release are legitimate
	// writes after the candidate bytes have been atomically published. The
	// finalized receipt is historical evidence of the publish event; it must
	// not freeze the live directory forever.
	mustWriteDirectoryPublishFile(
		t,
		filepath.Join(fixture.live, "meta", "planning", "actual_outcome.json"),
		[]byte(`{"accepted":true}`),
	)
	if err := publisher.FinalizeDirectoryPublish(fixture.request.TransactionID); err != nil {
		t.Fatalf("idempotent finalize rejected later canonical writes: %v", err)
	}
	state, err := publisher.LoadDirectoryPublishState(fixture.request.TransactionID)
	if err != nil {
		t.Fatalf("load finalized state after later writes: %v", err)
	}
	if state == nil || state.Phase != DirectoryPublishFinalized ||
		state.Receipt == nil {
		t.Fatalf("unexpected finalized state: %+v", state)
	}
}

func TestDirectoryPublishRecoverAllRestoresLiveMissingStartupState(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "startup-recovery")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	injected := errors.New("crash after archive")
	publisher.testFault = func(stage string) error {
		if stage == "live_archived" {
			return injected
		}
		return nil
	}
	if _, err := publisher.PublishDirectory(fixture.request); !errors.Is(err, injected) {
		t.Fatalf("publish fault = %v", err)
	}
	if _, err := os.Lstat(fixture.live); !os.IsNotExist(err) {
		t.Fatalf("live should be absent in crash window: %v", err)
	}
	for _, reserved := range []string{"candidates", "retired", "scratch"} {
		if err := os.MkdirAll(filepath.Join(fixture.transactions, reserved), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	restarted := NewDirectoryPublishStore(fixture.transactions)
	receipts, err := restarted.RecoverAllDirectoryPublishes()
	if err != nil {
		t.Fatalf("recover all: %v", err)
	}
	if len(receipts) != 1 || receipts[0].TransactionID != fixture.request.TransactionID {
		t.Fatalf("unexpected recovered receipts: %+v", receipts)
	}
	assertDirectoryPublishCommitted(t, fixture, receipts[0])

	again, err := restarted.RecoverAllDirectoryPublishes()
	if err != nil {
		t.Fatalf("repeat recover all: %v", err)
	}
	if len(again) != 1 || again[0].ReceiptDigest != receipts[0].ReceiptDigest {
		t.Fatalf("repeat recover all changed result: %+v", again)
	}
}

func TestDirectoryPublishReceiptEvidenceAllowsKnownLiveRuntimeDrift(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "receipt-evidence-runtime-drift")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	receipt, err := publisher.PublishDirectory(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteDirectoryPublishFile(
		t,
		filepath.Join(fixture.live, "meta", "runtime", "pipeline_execution.json"),
		[]byte(`{"owner":"restarted-owner"}`),
	)
	if _, err := publisher.LoadDirectoryPublishState(fixture.request.TransactionID); err == nil {
		t.Fatal("strict directory state unexpectedly ignored live runtime root drift")
	}
	evidence, err := publisher.LoadDirectoryPublishReceiptEvidence(fixture.request.TransactionID)
	if err != nil {
		t.Fatalf("immutable receipt evidence was coupled to mutable live root: %v", err)
	}
	if evidence == nil || evidence.ReceiptDigest != receipt.ReceiptDigest ||
		evidence.IntentDigest != receipt.IntentDigest {
		t.Fatalf("immutable receipt evidence drifted: %+v", evidence)
	}
}

func TestDirectoryPublishReceiptEvidenceRejectsIntentAndReceiptDigestTamper(t *testing.T) {
	t.Run("receipt", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "receipt-evidence-tampered-receipt")
		publisher := NewDirectoryPublishStore(fixture.transactions)
		if _, err := publisher.PublishDirectory(fixture.request); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(
			publisher.transactionDir(fixture.request.TransactionID),
			directoryPublishReceiptName,
		)
		var receipt DirectoryPublishReceipt
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &receipt) != nil {
			t.Fatalf("read receipt: %v", err)
		}
		receipt.ReceiptDigest = "sha256:" + strings.Repeat("0", 64)
		raw, err = json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.LoadDirectoryPublishReceiptEvidence(fixture.request.TransactionID); err == nil {
			t.Fatal("tampered receipt digest was accepted")
		}
	})

	t.Run("intent", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "receipt-evidence-tampered-intent")
		publisher := NewDirectoryPublishStore(fixture.transactions)
		if _, err := publisher.PublishDirectory(fixture.request); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(
			publisher.transactionDir(fixture.request.TransactionID),
			directoryPublishIntentName,
		)
		var intent DirectoryPublishIntent
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &intent) != nil {
			t.Fatalf("read intent: %v", err)
		}
		intent.IntentDigest = "sha256:" + strings.Repeat("0", 64)
		raw, err = json.MarshalIndent(intent, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.LoadDirectoryPublishReceiptEvidence(fixture.request.TransactionID); err == nil {
			t.Fatal("tampered intent digest was accepted")
		}
	})
}

func TestDirectoryPublishReceiptEvidenceRejectsMissingFinalizeAfterIntentRemoval(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "receipt-evidence-missing-finalize")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	if _, err := publisher.PublishDirectory(fixture.request); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(
		publisher.transactionDir(fixture.request.TransactionID),
		directoryPublishIntentName,
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.LoadDirectoryPublishReceiptEvidence(fixture.request.TransactionID); err == nil {
		t.Fatal("receipt without intent or finalize evidence was accepted")
	}
}

func TestDirectoryPublishConcurrentIdempotentPublish(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "concurrent")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	const workers = 8
	var wait sync.WaitGroup
	wait.Add(workers)
	receipts := make(chan *DirectoryPublishReceipt, workers)
	errs := make(chan error, workers)
	for range workers {
		go func() {
			defer wait.Done()
			receipt, err := publisher.PublishDirectory(fixture.request)
			if err != nil {
				errs <- err
				return
			}
			receipts <- receipt
		}()
	}
	wait.Wait()
	close(receipts)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent publish: %v", err)
	}
	var digest string
	for receipt := range receipts {
		if digest == "" {
			digest = receipt.ReceiptDigest
		} else if receipt.ReceiptDigest != digest {
			t.Fatalf("concurrent publish produced different receipts: %s vs %s", digest, receipt.ReceiptDigest)
		}
	}
	if digest == "" {
		t.Fatal("concurrent publish produced no receipt")
	}
}

func TestDirectoryPublishAbortBeforePromotion(t *testing.T) {
	for _, stage := range []string{"intent_written", "live_archived"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newDirectoryPublishFixture(t, "abort-"+stage)
			publisher := NewDirectoryPublishStore(fixture.transactions)
			injected := errors.New("stop at " + stage)
			publisher.testFault = func(current string) error {
				if current == stage {
					return injected
				}
				return nil
			}
			if _, err := publisher.PublishDirectory(fixture.request); !errors.Is(err, injected) {
				t.Fatalf("publish fault = %v", err)
			}
			publisher.testFault = nil
			if err := publisher.AbortDirectoryPublish(fixture.request.TransactionID); err != nil {
				t.Fatalf("abort: %v", err)
			}
			if err := publisher.AbortDirectoryPublish(fixture.request.TransactionID); err != nil {
				t.Fatalf("idempotent abort: %v", err)
			}
			root, err := directoryContentRoot(fixture.live)
			if err != nil {
				t.Fatal(err)
			}
			if root != fixture.beforeRoot {
				t.Fatalf("abort restored root %s, want %s", root, fixture.beforeRoot)
			}
			candidateRoot, err := directoryContentRoot(fixture.candidate)
			if err != nil {
				t.Fatal(err)
			}
			if candidateRoot != fixture.candidateRoot {
				t.Fatalf("abort changed candidate root %s", candidateRoot)
			}
			state, err := publisher.LoadDirectoryPublishState(fixture.request.TransactionID)
			if err != nil {
				t.Fatal(err)
			}
			if state == nil || state.Phase != DirectoryPublishAborted {
				t.Fatalf("unexpected aborted state: %+v", state)
			}
			if _, err := publisher.RecoverDirectoryPublish(fixture.request.TransactionID); !errors.Is(err, ErrDirectoryPublishAborted) {
				t.Fatalf("recover aborted transaction = %v", err)
			}
			if _, err := publisher.PublishDirectory(fixture.request); err == nil ||
				!strings.Contains(err.Error(), "aborted") {
				t.Fatalf("aborted transaction ID was reused: %v", err)
			}
		})
	}
}

func TestDirectoryPublishRejectsAbortAfterPromotion(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "abort-promoted")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	injected := errors.New("stop after promotion")
	publisher.testFault = func(stage string) error {
		if stage == "candidate_promoted" {
			return injected
		}
		return nil
	}
	if _, err := publisher.PublishDirectory(fixture.request); !errors.Is(err, injected) {
		t.Fatalf("publish fault = %v", err)
	}
	publisher.testFault = nil
	if err := publisher.AbortDirectoryPublish(fixture.request.TransactionID); err == nil ||
		!strings.Contains(err.Error(), "promoted") {
		t.Fatalf("abort after promotion = %v", err)
	}
	if _, err := publisher.RecoverDirectoryPublish(fixture.request.TransactionID); err != nil {
		t.Fatalf("finish promoted transaction: %v", err)
	}
}

func TestDirectoryPublishRejectsPathOverlapAndMissingInputs(t *testing.T) {
	t.Run("empty transaction root", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "empty-root")
		_, err := NewDirectoryPublishStore("").PublishDirectory(fixture.request)
		if err == nil || !strings.Contains(err.Error(), "transaction_root") {
			t.Fatalf("empty transaction root = %v", err)
		}
	})
	t.Run("candidate inside live", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "candidate-in-live")
		candidate := filepath.Join(fixture.live, "candidate")
		mustWriteDirectoryPublishFile(t, filepath.Join(candidate, "chapter.md"), []byte("nested"))
		fixture.request.CandidateDir = candidate
		_, err := NewDirectoryPublishStore(fixture.transactions).PublishDirectory(fixture.request)
		if err == nil || !strings.Contains(err.Error(), "inside live_dir") {
			t.Fatalf("candidate/live overlap = %v", err)
		}
	})
	t.Run("live inside candidate", func(t *testing.T) {
		base := t.TempDir()
		candidate := filepath.Join(base, "candidate")
		live := filepath.Join(candidate, "live")
		mustWriteDirectoryPublishFile(t, filepath.Join(live, "old.md"), []byte("old"))
		_, err := NewDirectoryPublishStore(filepath.Join(base, "transactions")).PublishDirectory(
			PublishDirectoryRequest{
				TransactionID:    "live-in-candidate",
				LiveDir:          live,
				CandidateDir:     candidate,
				ExpectedLiveRoot: "sha256:" + strings.Repeat("0", 64),
			},
		)
		if err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Fatalf("live/candidate overlap = %v", err)
		}
	})
	t.Run("transaction root inside live", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "root-in-live")
		_, err := NewDirectoryPublishStore(filepath.Join(fixture.live, "transactions")).
			PublishDirectory(fixture.request)
		if err == nil || !strings.Contains(err.Error(), "transaction_root") {
			t.Fatalf("live/root overlap = %v", err)
		}
	})
	t.Run("missing candidate", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "missing-candidate")
		if err := os.RemoveAll(fixture.candidate); err != nil {
			t.Fatal(err)
		}
		_, err := NewDirectoryPublishStore(fixture.transactions).PublishDirectory(fixture.request)
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("missing candidate = %v", err)
		}
	})
	t.Run("resolved alias overlap", func(t *testing.T) {
		fixture := newDirectoryPublishFixture(t, "resolved-overlap")
		aliasParent := filepath.Join(fixture.base, "alias")
		if err := os.Symlink(fixture.base, aliasParent); err != nil {
			t.Fatal(err)
		}
		fixture.request.CandidateDir = filepath.Join(aliasParent, filepath.Base(fixture.live))
		_, err := NewDirectoryPublishStore(fixture.transactions).PublishDirectory(fixture.request)
		if err == nil || !strings.Contains(err.Error(), "resolved") {
			t.Fatalf("resolved path overlap = %v", err)
		}
	})
}

func TestDirectoryPublishRejectsParameterAndContentDrift(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "drift")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	injected := errors.New("intent durable")
	publisher.testFault = func(stage string) error {
		if stage == "intent_written" {
			return injected
		}
		return nil
	}
	if _, err := publisher.PublishDirectory(fixture.request); !errors.Is(err, injected) {
		t.Fatalf("publish fault = %v", err)
	}
	publisher.testFault = nil

	otherCandidate := filepath.Join(fixture.base, "other-candidate")
	mustWriteDirectoryPublishFile(t, filepath.Join(otherCandidate, "chapter.md"), []byte("other"))
	changed := fixture.request
	changed.CandidateDir = otherCandidate
	if _, err := publisher.PublishDirectory(changed); err == nil ||
		!strings.Contains(err.Error(), "parameter drift") {
		t.Fatalf("changed candidate path = %v", err)
	}

	mustWriteDirectoryPublishFile(t, filepath.Join(fixture.candidate, "chapter.md"), []byte("mutated"))
	if _, err := publisher.RecoverDirectoryPublish(fixture.request.TransactionID); err == nil ||
		!strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("mutated candidate content = %v", err)
	}
}

func TestDirectoryPublishRejectsReceiptThatDoesNotBindIntent(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "receipt-binding")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	receipt, err := publisher.PublishDirectory(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	receipt.BeforeLiveRoot = receipt.CandidateRoot
	receipt.ReceiptDigest, err = computeDirectoryPublishReceiptDigest(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(fixture.transactions, fixture.request.TransactionID, directoryPublishReceiptName),
		raw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.RecoverDirectoryPublish(fixture.request.TransactionID); err == nil ||
		!strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("unbound receipt = %v", err)
	}
}

func TestDirectoryPublishFinalizeReceiptRetryIsDeterministic(t *testing.T) {
	fixture := newDirectoryPublishFixture(t, "finalize-retry")
	publisher := NewDirectoryPublishStore(fixture.transactions)
	receipt, err := publisher.PublishDirectory(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	err = publisher.withWriteLock(func() error {
		intent, err := publisher.loadIntentUnlocked(fixture.request.TransactionID)
		if err != nil {
			return err
		}
		return publisher.ensureFinalizeReceiptUnlocked(*intent, *receipt)
	})
	if err != nil {
		t.Fatalf("write finalize receipt crash window: %v", err)
	}
	publisher.now = func() time.Time {
		return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if err := publisher.FinalizeDirectoryPublish(fixture.request.TransactionID); err != nil {
		t.Fatalf("resume finalize with later clock: %v", err)
	}
}

func TestDirectoryPublishRecoverAllRejectsMalformedIntent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transactions")
	if err := os.MkdirAll(filepath.Join(root, "malformed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "malformed", directoryPublishIntentName),
		[]byte(`{"version":"bad"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectoryPublishStore(root).RecoverAllDirectoryPublishes(); err == nil {
		t.Fatal("malformed durable intent was silently ignored")
	}
}

func newDirectoryPublishFixture(t *testing.T, transactionID string) directoryPublishFixture {
	t.Helper()
	base := t.TempDir()
	live := filepath.Join(base, "live")
	candidate := filepath.Join(base, "candidate")
	transactions := filepath.Join(base, "transactions")
	mustWriteDirectoryPublishFile(t, filepath.Join(live, "chapters", "1.md"), []byte("old chapter\n"))
	mustWriteDirectoryPublishFile(t, filepath.Join(live, "meta", "progress.json"), []byte(`{"chapter":1}`))
	if err := os.MkdirAll(filepath.Join(live, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteDirectoryPublishFile(t, filepath.Join(candidate, "chapters", "1.md"), []byte("new chapter\n"))
	mustWriteDirectoryPublishFile(t, filepath.Join(candidate, "chapters", "2.md"), []byte("new second chapter\n"))
	mustWriteDirectoryPublishFile(t, filepath.Join(candidate, "meta", "progress.json"), []byte(`{"chapter":2}`))
	if err := os.MkdirAll(filepath.Join(candidate, "new-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	beforeRoot, err := directoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, err := directoryContentRoot(candidate)
	if err != nil {
		t.Fatal(err)
	}
	return directoryPublishFixture{
		base:          base,
		live:          live,
		candidate:     candidate,
		transactions:  transactions,
		beforeRoot:    beforeRoot,
		candidateRoot: candidateRoot,
		request: PublishDirectoryRequest{
			TransactionID:    transactionID,
			LiveDir:          live,
			CandidateDir:     candidate,
			ExpectedLiveRoot: beforeRoot,
		},
	}
}

func mustWriteDirectoryPublishFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryPublishCommitted(
	t *testing.T,
	fixture directoryPublishFixture,
	receipt DirectoryPublishReceipt,
) {
	t.Helper()
	if receipt.TransactionID != fixture.request.TransactionID ||
		receipt.BeforeLiveRoot != fixture.beforeRoot ||
		receipt.CandidateRoot != fixture.candidateRoot ||
		receipt.CommittedLiveRoot != fixture.candidateRoot {
		t.Fatalf("receipt does not bind directory roots: %+v", receipt)
	}
	liveRoot, err := directoryContentRoot(fixture.live)
	if err != nil {
		t.Fatal(err)
	}
	if liveRoot != fixture.candidateRoot {
		t.Fatalf("live root = %s, want candidate %s", liveRoot, fixture.candidateRoot)
	}
	if _, err := os.Lstat(fixture.candidate); !os.IsNotExist(err) {
		t.Fatalf("candidate path should have been renamed: %v", err)
	}
	archiveRoot, err := directoryContentRoot(receipt.ArchiveDir)
	if err != nil {
		t.Fatal(err)
	}
	if archiveRoot != fixture.beforeRoot {
		t.Fatalf("archive root = %s, want before %s", archiveRoot, fixture.beforeRoot)
	}
	state, err := NewDirectoryPublishStore(fixture.transactions).
		LoadDirectoryPublishState(fixture.request.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != DirectoryPublishReceiptWritten ||
		state.Intent == nil || state.Receipt == nil {
		t.Fatalf("unexpected committed state: %+v", state)
	}
}
