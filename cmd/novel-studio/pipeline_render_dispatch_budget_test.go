package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineRenderDispatchFixture(t *testing.T) (string, string, pipelineRenderCandidateManifest) {
	t.Helper()
	base := t.TempDir()
	liveOutputDir := filepath.Join(base, "output")
	candidateOutputDir := filepath.Join(base, ".render-candidates", "render-ch0001-fixture", "output")
	if err := os.MkdirAll(filepath.Join(candidateOutputDir, "meta", "planning"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(liveOutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                pipelineRenderCandidateManifestVersion,
		CandidateID:            "render-ch0001-fixture",
		GenerationID:           "pg2_dispatch-fixture",
		Chapter:                1,
		PlanDigest:             domain.ComputeArcArtifactSHA256([]byte("plan")),
		PlanCheckpointSeq:      9,
		ProjectedBundleDigest:  domain.ComputeArcArtifactSHA256([]byte("bundle")),
		PromotionReceiptDigest: domain.ComputeArcArtifactSHA256([]byte("promotion")),
		SourceOutputDir:        liveOutputDir,
		SourceLiveRoot:         domain.ComputeArcArtifactSHA256([]byte("live")),
		PreparedAt:             "2026-07-20T08:00:00Z",
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidateOutputDir, "meta", "planning", "render_candidate.json"),
		raw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidateOutputDir)
	if err := candidateStore.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: manifest.Chapter,
		PlanDigest: manifest.PlanDigest, Owner: "render-dispatch-fixture",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return liveOutputDir, candidateOutputDir, manifest
}

func writePipelineRenderDispatchManifest(
	t *testing.T,
	candidateOutputDir string,
	manifest pipelineRenderCandidateManifest,
) {
	t.Helper()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidateOutputDir, "meta", "planning", "render_candidate.json"),
		raw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

func requireNoPipelineRenderDispatchState(t *testing.T, liveOutputDir, candidateID string) {
	t.Helper()
	dir, err := pipelineRenderConvergenceDir(liveOutputDir, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("dispatch path validation left an external side effect at %s: %v", dir, err)
	}
}

func TestPipelineRenderDispatchBudgetIsPersistentIdempotentAndPreProvider(t *testing.T) {
	liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
	first, reused, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-a", 1)
	if err != nil || reused || first.Attempt != 1 {
		t.Fatalf("first reservation=%+v reused=%v err=%v", first, reused, err)
	}
	if !strings.HasPrefix(first.AuthorizationDigest, domain.PlanningV2DigestPrefix) ||
		len(first.AuthorizationDigest) != len(domain.PlanningV2DigestPrefix)+64 {
		t.Fatalf("dispatch authorization is not canonical sha256 evidence: %q", first.AuthorizationDigest)
	}
	firstAgain, reused, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-a", 1)
	if err != nil || !reused || *firstAgain != *first {
		t.Fatalf("same authorization was not idempotent: first=%+v again=%+v reused=%v err=%v", first, firstAgain, reused, err)
	}
	for attempt, invocation := range []string{"invocation-b", "invocation-c"} {
		reservation, wasReused, reserveErr := reservePipelineWholeBodyDispatch(candidateOutputDir, invocation, 1)
		if reserveErr != nil || wasReused || reservation.Attempt != attempt+2 {
			t.Fatalf("reservation %s=%+v reused=%v err=%v", invocation, reservation, wasReused, reserveErr)
		}
	}
	if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-d", 1); err == nil {
		t.Fatal("fourth whole-body dispatch was not blocked before provider call")
	} else {
		var exhausted *pipelineRenderDispatchBudgetExhaustedError
		if !errors.As(err, &exhausted) || exhausted.Reserved != 3 || exhausted.Limit != 3 {
			t.Fatalf("fourth dispatch error=%T %v", err, err)
		}
	}

	ledger, err := loadPipelineRenderDispatchLedger(liveOutputDir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	reservations := pipelineRenderDispatchReservations(ledger)
	if len(reservations) != pipelineRenderWholeBodyDispatchLimit {
		t.Fatalf("persistent reservations=%d want=%d", len(reservations), pipelineRenderWholeBodyDispatchLimit)
	}
	for i, reservation := range reservations {
		if reservation.Attempt != i+1 {
			t.Fatalf("reservation attempts are not monotonic: %+v", reservations)
		}
	}
}

func TestPipelineRenderDispatchCompletionIsImmutable(t *testing.T) {
	_, candidateOutputDir, _ := pipelineRenderDispatchFixture(t)
	reservation, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-finish", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewStore(candidateOutputDir).Runtime.ArmPipelineRenderProsePermit(reservation.AuthorizationDigest, reservation.Attempt); err != nil {
		t.Fatal(err)
	}
	if err := store.NewStore(candidateOutputDir).Runtime.ConsumePipelineRenderProsePermit(1); err != nil {
		t.Fatal(err)
	}
	bodySHA := domain.ComputeArcArtifactSHA256([]byte("body"))
	if err := finishPipelineWholeBodyDispatch(candidateOutputDir, reservation.AuthorizationDigest, "body_ready", bodySHA, 10); err != nil {
		t.Fatal(err)
	}
	if err := finishPipelineWholeBodyDispatch(candidateOutputDir, reservation.AuthorizationDigest, "body_ready", bodySHA, 10); err != nil {
		t.Fatalf("same completion was not idempotent: %v", err)
	}
	if err := finishPipelineWholeBodyDispatch(candidateOutputDir, reservation.AuthorizationDigest, "provider_error", "", 0); err == nil {
		t.Fatal("completion evidence drift did not fail closed")
	}
}

func TestPipelineRenderDispatchConcurrentSameAuthorizationConsumesOneSlot(t *testing.T) {
	liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	digests := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reservation, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "same-invocation", 1)
			if err != nil {
				errs <- err
				return
			}
			digests <- reservation.AuthorizationDigest
		}()
	}
	wg.Wait()
	close(errs)
	close(digests)
	for err := range errs {
		t.Errorf("concurrent reserve: %v", err)
	}
	unique := map[string]struct{}{}
	for digest := range digests {
		unique[digest] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("same authorization produced %d digests", len(unique))
	}
	ledger, err := loadPipelineRenderDispatchLedger(liveOutputDir, manifest)
	if err != nil || len(ledger.Reservations) != 1 {
		t.Fatalf("concurrent ledger=%+v err=%v", ledger, err)
	}
}

func TestPipelineRenderDispatchLedgerRejectsPlanIdentityDrift(t *testing.T) {
	_, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
	if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-a", 1); err != nil {
		t.Fatal(err)
	}
	manifest.PlanDigest = domain.ComputeArcArtifactSHA256([]byte("drifted-plan"))
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidateOutputDir, "meta", "planning", "render_candidate.json"),
		raw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "invocation-b", 1); err == nil {
		t.Fatal("drifted plan reused dispatch budget ledger")
	}
}

func TestPipelineRenderDispatchRejectsUnboundManifestPathsWithoutSideEffects(t *testing.T) {
	t.Run("relative source", func(t *testing.T) {
		liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
		externalLive := filepath.Join(t.TempDir(), "relative-source")
		if err := os.MkdirAll(externalLive, 0o755); err != nil {
			t.Fatal(err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(cwd, externalLive)
		if err != nil || filepath.IsAbs(relative) {
			t.Fatalf("relative source=%q err=%v", relative, err)
		}
		manifest.SourceOutputDir = relative
		writePipelineRenderDispatchManifest(t, candidateOutputDir, manifest)
		if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "relative", 1); err == nil {
			t.Fatal("relative source_output_dir reached dispatch reservation")
		}
		requireNoPipelineRenderDispatchState(t, externalLive, manifest.CandidateID)
		requireNoPipelineRenderDispatchState(t, liveOutputDir, manifest.CandidateID)
	})

	t.Run("external absolute source", func(t *testing.T) {
		liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
		externalLive := filepath.Join(t.TempDir(), "external-source")
		if err := os.MkdirAll(externalLive, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest.SourceOutputDir = externalLive
		writePipelineRenderDispatchManifest(t, candidateOutputDir, manifest)
		if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "external", 1); err == nil {
			t.Fatal("external source_output_dir reached dispatch reservation")
		}
		if err := finishPipelineWholeBodyDispatch(
			candidateOutputDir,
			domain.ComputeArcArtifactSHA256([]byte("authorization")),
			"provider_or_host_error",
			"",
			0,
		); err == nil {
			t.Fatal("external source_output_dir reached dispatch completion")
		}
		if _, err := syncPipelineRenderConvergence(store.NewStore(candidateOutputDir)); err == nil {
			t.Fatal("external source_output_dir reached convergence sync")
		}
		requireNoPipelineRenderDispatchState(t, externalLive, manifest.CandidateID)
		requireNoPipelineRenderDispatchState(t, liveOutputDir, manifest.CandidateID)
	})

	t.Run("candidate path alias", func(t *testing.T) {
		liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
		alias := candidateOutputDir + string(filepath.Separator) + ".." + string(filepath.Separator) + "output"
		if filepath.Clean(alias) != candidateOutputDir || alias == filepath.Clean(alias) {
			t.Fatalf("test alias is not an unclean spelling of candidate: %s", alias)
		}
		if _, _, err := reservePipelineWholeBodyDispatch(alias, "alias", 1); err == nil {
			t.Fatal("candidate path alias reached dispatch reservation")
		}
		requireNoPipelineRenderDispatchState(t, liveOutputDir, manifest.CandidateID)
	})

	t.Run("symlinked candidate namespace", func(t *testing.T) {
		liveOutputDir, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
		namespace := pipelineRenderCandidateRoot(liveOutputDir)
		escapedNamespace := filepath.Join(filepath.Dir(namespace), "escaped-render-candidates")
		if err := os.Rename(namespace, escapedNamespace); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(escapedNamespace, namespace); err != nil {
			t.Fatal(err)
		}
		if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "symlink", 1); err == nil {
			t.Fatal("symlinked candidate namespace reached dispatch reservation")
		}
		escapedLive := filepath.Join(filepath.Dir(escapedNamespace), "output")
		requireNoPipelineRenderDispatchState(t, escapedLive, manifest.CandidateID)
	})

	t.Run("symlinked convergence root", func(t *testing.T) {
		_, candidateOutputDir, manifest := pipelineRenderDispatchFixture(t)
		root := filepath.Join(pipelineRenderCandidateRoot(manifest.SourceOutputDir), "convergence")
		external := t.TempDir()
		if err := os.Symlink(external, root); err != nil {
			t.Fatal(err)
		}
		if _, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "symlink-root", 1); err == nil {
			t.Fatal("symlinked convergence root reached dispatch reservation")
		}
		if err := finishPipelineWholeBodyDispatch(
			candidateOutputDir,
			domain.ComputeArcArtifactSHA256([]byte("authorization")),
			"provider_or_host_error",
			"",
			0,
		); err == nil {
			t.Fatal("symlinked convergence root reached dispatch completion")
		}
		if _, err := syncPipelineRenderConvergence(store.NewStore(candidateOutputDir)); err == nil {
			t.Fatal("symlinked convergence root reached convergence sync")
		}
		if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
			t.Fatalf("symlinked convergence root received external side effects: entries=%v err=%v", entries, err)
		}
	})

	t.Run("permit rejects replaced convergence root", func(t *testing.T) {
		liveOutputDir, candidateOutputDir, _ := pipelineRenderDispatchFixture(t)
		reservation, _, err := reservePipelineWholeBodyDispatch(candidateOutputDir, "permit-symlink", 1)
		if err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "convergence")
		savedRoot := filepath.Join(filepath.Dir(root), "saved-convergence")
		if err := os.Rename(root, savedRoot); err != nil {
			t.Fatal(err)
		}
		external := t.TempDir()
		if err := os.Symlink(external, root); err != nil {
			t.Fatal(err)
		}
		if err := store.NewStore(candidateOutputDir).Runtime.ArmPipelineRenderProsePermit(
			reservation.AuthorizationDigest,
			reservation.Attempt,
		); err == nil {
			t.Fatal("provider permit followed a replaced convergence root")
		}
		if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
			t.Fatalf("provider permit left external side effects: entries=%v err=%v", entries, err)
		}
	})
}

func TestPipelineRenderDispatchClassificationLeavesLegacyRenderUnchanged(t *testing.T) {
	dir := t.TempDir()
	needed, baseline, err := pipelineWholeBodyDispatchNeeded(store.NewStore(dir), 1)
	if err != nil || needed || baseline != 0 {
		t.Fatalf("legacy render dispatch classification needed=%v baseline=%d err=%v", needed, baseline, err)
	}
}
