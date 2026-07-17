package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPreparePipelineRenderCandidateDeepCopiesWithoutTouchingLive(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen := pipelineRenderCandidateTestFrozen()
	before := pipelineRenderCandidateTestSnapshot(t, live)

	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(live, "meta", "state.json")
	candidateFile := filepath.Join(candidate.OutputDir, "meta", "state.json")
	liveInfo, err := os.Stat(liveFile)
	if err != nil {
		t.Fatal(err)
	}
	candidateInfo, err := os.Stat(candidateFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(liveInfo, candidateInfo) {
		t.Fatal("render candidate reused a live inode through a hard link")
	}
	if err := os.WriteFile(candidateFile, []byte("candidate-only mutation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := pipelineRenderCandidateTestSnapshot(t, live)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("candidate mutation touched live output:\nbefore=%v\nafter=%v", before, after)
	}
	if body, err := os.ReadFile(liveFile); err != nil || string(body) != "live-state\n" {
		t.Fatalf("live file changed through candidate inode: body=%q err=%v", body, err)
	}
}

func TestPipelineRenderTransactionIDBindsExactFrozenInputs(t *testing.T) {
	base := pipelineRenderCandidateTestFrozen()
	want, err := pipelineRenderTransactionID(base)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := pipelineRenderTransactionID(base)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != want {
		t.Fatalf("exact frozen binding produced unstable transaction IDs: %s != %s", replayed, want)
	}

	cases := []struct {
		name   string
		mutate func(*pipelineFrozenPlan)
	}{
		{"generation", func(value *pipelineFrozenPlan) { value.PlanningGenerationID += "-drift" }},
		{"chapter", func(value *pipelineFrozenPlan) { value.Chapter++ }},
		{"plan", func(value *pipelineFrozenPlan) { value.PlanDigest += "-drift" }},
		{"bundle", func(value *pipelineFrozenPlan) { value.ProjectedBundleDigest += "-drift" }},
		{"promotion", func(value *pipelineFrozenPlan) { value.PromotionReceiptDigest += "-drift" }},
		{"render input", func(value *pipelineFrozenPlan) { value.PipelineRunInputDigest += "-drift" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			drifted := *base
			tc.mutate(&drifted)
			got, err := pipelineRenderTransactionID(&drifted)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("%s drift reused transaction ID %s", tc.name, want)
			}
		})
	}
}

func TestPublishAndFinalizePipelineRenderCandidatePreservesProtocolEvidence(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	before := pipelineRenderCandidateTestSnapshot(t, live)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("candidate accepted body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	candidateBeforePublish := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)

	receipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, candidateBeforePublish) {
		t.Fatalf("published live tree differs from candidate:\ngot=%v\nwant=%v", got, candidateBeforePublish)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, receipt.ArchiveDir); !reflect.DeepEqual(got, before) {
		t.Fatalf("publish archive did not preserve prior live:\ngot=%v\nwant=%v", got, before)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	state, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishReceiptWritten {
		t.Fatalf("publish state=%+v, want receipt_written", state)
	}

	if err := finalizePipelineRenderCandidate(live, candidate.ID); err != nil {
		t.Fatal(err)
	}
	state, err = publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishFinalized ||
		state.Receipt == nil || state.Receipt.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("finalized state lost publish evidence: %+v", state)
	}
	if _, err := os.Stat(receipt.ArchiveDir); !os.IsNotExist(err) {
		t.Fatalf("finalize retained rollback archive: %v", err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, candidateBeforePublish) {
		t.Fatalf("finalize changed committed live tree:\ngot=%v\nwant=%v", got, candidateBeforePublish)
	}
}

func TestRecoverAllDirectoryPublishesRestoresLiveArchivedRenderCandidate(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("candidate crash-recovery body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	wantLive := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)
	receipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}

	// Recreate the exact live_archived crash window: intent and archive are
	// durable, candidate still exists, live is absent, receipt is not written.
	if err := os.Rename(live, candidate.OutputDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(candidate.TransactionRoot, candidate.ID, "receipt.json")); err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	state, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishLiveArchived {
		t.Fatalf("simulated crash state=%+v, want live_archived", state)
	}
	if candidate.TransactionRoot != pipelineRenderTransactionRoot(live) {
		t.Fatalf("startup recovery root=%s want=%s", candidate.TransactionRoot, pipelineRenderTransactionRoot(live))
	}
	releaseControl, err := acquirePipelineOutlineAllControl(live, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverAllDirectoryPublishesWithControlHeld(live); err != nil {
		_ = releaseControl()
		t.Fatal(err)
	}
	if err := releaseControl(); err != nil {
		t.Fatal(err)
	}
	finalized, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil || finalized == nil || finalized.Phase != store.DirectoryPublishFinalized ||
		finalized.Receipt == nil || finalized.Receipt.IntentDigest != receipt.IntentDigest ||
		finalized.Receipt.CandidateRoot != receipt.CandidateRoot ||
		finalized.Receipt.CommittedLiveRoot != receipt.CommittedLiveRoot {
		t.Fatalf("combined recovery returned wrong state: %+v err=%v", finalized, err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("startup recovery restored wrong live tree:\ngot=%v\nwant=%v", got, wantLive)
	}
}

func TestRejectedPipelineRenderCandidateRetiresWithoutChangingLive(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	before := pipelineRenderCandidateTestSnapshot(t, live)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("rejected candidate body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, before) {
		t.Fatalf("retiring rejected candidate changed live:\ngot=%v\nwant=%v", got, before)
	}
	if _, err := os.Stat(candidate.ContainerDir); !os.IsNotExist(err) {
		t.Fatalf("rejected candidate remained active: %v", err)
	}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(live), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), candidate.ID+"-rejected-") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rejected candidate was not retained under %s", retiredRoot)
	}
}

func pipelineRenderCandidateTestFrozen() *pipelineFrozenPlan {
	return &pipelineFrozenPlan{
		Version:                "pipeline-planning.v1",
		Chapter:                1,
		PlanDigest:             "sha256:plan-v1",
		PlanningGenerationID:   "pg2_render_candidate_test",
		ProjectionBinding:      "sealed_v2",
		ProjectedBundleDigest:  "sha256:bundle-v1",
		PromotionReceiptDigest: "sha256:promotion-v1",
		PipelineRunInputDigest: "sha256:render-input-v1",
	}
}

func pipelineRenderCandidateTestLive(t *testing.T) string {
	t.Helper()
	live := filepath.Join(t.TempDir(), "output", "novel")
	for rel, body := range map[string]string{
		"chapters/01.md":             "live chapter body\n",
		"meta/state.json":            "live-state\n",
		"nested/ledger/events.jsonl": "{\"event\":\"live\"}\n",
	} {
		path := filepath.Join(live, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return live
}

func pipelineRenderCandidateTestSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
