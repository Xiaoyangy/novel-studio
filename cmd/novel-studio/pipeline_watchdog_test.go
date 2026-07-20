package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineWatchdogHeartbeatAndProgressAreIndependent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: dir, InvocationID: "invocation-1", RunIdentity: "sha256:run",
		Stage: "render", Chapter: 4, PlanDigest: "sha256:plan",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchdog.Stop() })

	initial := loadPipelineWatchdogStateForTest(t, dir)
	if initial.Status != pipelineWatchdogRunning || initial.HeartbeatAt != initial.LastProgressAt {
		t.Fatalf("unexpected initial state: %+v", initial)
	}

	now = now.Add(4 * time.Minute)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	active := loadPipelineWatchdogStateForTest(t, dir)
	if active.Status != pipelineWatchdogRunning || active.HeartbeatAt == initial.HeartbeatAt ||
		active.LastProgressAt != initial.LastProgressAt || active.ProgressSeq != 0 {
		t.Fatalf("heartbeat incorrectly advanced progress: initial=%+v active=%+v", initial, active)
	}

	now = now.Add(time.Minute)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	stalled := loadPipelineWatchdogStateForTest(t, dir)
	if stalled.Status != pipelineWatchdogStalled || stalled.LastProgressAt != initial.LastProgressAt {
		t.Fatalf("five-minute no-progress interval was not stalled: %+v", stalled)
	}

	if err := watchdog.Progress("checkpoint_commit"); err != nil {
		t.Fatal(err)
	}
	progressed := loadPipelineWatchdogStateForTest(t, dir)
	if progressed.Status != pipelineWatchdogRunning || progressed.ProgressSeq != 1 ||
		progressed.LastProgressAt != now.Format(time.RFC3339Nano) ||
		progressed.LastProgressKind != "checkpoint_commit" {
		t.Fatalf("explicit progress did not recover watchdog: %+v", progressed)
	}
}

func TestPipelineWatchdogProgressBodyPersistsOnlyOpaqueIdentity(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 8, 30, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: dir, InvocationID: "invocation-body", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchdog.Stop() })

	const prose = "她把整章正文误传给了诊断桥，这段文字不得落盘"
	now = now.Add(time.Second)
	if err := watchdog.ProgressBody(pipelineWatchdogEventRenderBodyCommitted, prose); err != nil {
		t.Fatal(err)
	}
	state := loadPipelineWatchdogStateForTest(t, dir)
	if state.BodySHA256 == prose || !strings.HasPrefix(state.BodySHA256, "sha256:") ||
		state.LastProgressKind != pipelineWatchdogEventRenderBodyCommitted || state.ProgressSeq != 1 {
		t.Fatalf("body progress was not reduced to opaque identity: %+v", state)
	}
	raw, err := os.ReadFile(filepath.Join(pipelineWatchdogControlRootForTest(t, dir), filepath.FromSlash(pipelineWatchdogStatePath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), prose) {
		t.Fatalf("watchdog state leaked body prose: %s", raw)
	}
}

func TestPipelineWatchdogEmitsOneRedactedDiagnosticAtTwentyFiveMinutes(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	now := started
	const prose = "她在零点四十分打开门，正文绝不能进入诊断快照"
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: dir, InvocationID: "invocation-2", RunIdentity: "sha256:run",
		Stage: prose, Chapter: 5, PlanDigest: prose, BodySHA256: prose,
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchdog.Stop() })
	if err := watchdog.Progress(prose); err != nil {
		t.Fatal(err)
	}

	now = started.Add(25*time.Minute - time.Nanosecond)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	before := loadPipelineWatchdogStateForTest(t, dir)
	if before.DiagnosticRelPath != "" {
		t.Fatalf("diagnostic emitted before threshold: %+v", before)
	}

	now = started.Add(25 * time.Minute)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	first := loadPipelineWatchdogStateForTest(t, dir)
	if first.DiagnosticRelPath == "" || first.DiagnosticEmittedAt == "" {
		t.Fatalf("25-minute diagnostic missing: %+v", first)
	}
	diagnosticPath := filepath.Join(
		pipelineWatchdogControlRootForTest(t, dir),
		filepath.FromSlash(first.DiagnosticRelPath),
	)
	raw, err := os.ReadFile(diagnosticPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), prose) {
		t.Fatalf("diagnostic leaked prose: %s", raw)
	}
	var diagnostic pipelineWatchdogDiagnostic
	if err := json.Unmarshal(raw, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.ElapsedMS != (25*time.Minute).Milliseconds() ||
		diagnostic.NoProgressMS != (25*time.Minute).Milliseconds() ||
		diagnostic.Status != pipelineWatchdogStalled {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}
	firstRaw := append([]byte(nil), raw...)
	firstEmittedAt := first.DiagnosticEmittedAt

	now = started.Add(40 * time.Minute)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	second := loadPipelineWatchdogStateForTest(t, dir)
	secondRaw, err := os.ReadFile(diagnosticPath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(pipelineWatchdogControlRootForTest(t, dir), filepath.FromSlash(pipelineWatchdogDiagnosticPath)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || second.DiagnosticRelPath != first.DiagnosticRelPath ||
		second.DiagnosticEmittedAt != firstEmittedAt || string(secondRaw) != string(firstRaw) {
		t.Fatalf("diagnostic was not one-shot: first=%+v second=%+v entries=%d", first, second, len(entries))
	}
}

func TestPipelineWatchdogRecoveryKeepsOriginalTimelineAndDiagnostic(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	now := started
	config := func() pipelineWatchdogConfig {
		return pipelineWatchdogConfig{
			OutputDir: dir, InvocationID: "invocation-recovery", RunIdentity: "sha256:run",
			Stage: "render", Chapter: 6, PlanDigest: "sha256:plan",
			HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
		}
	}
	first, err := newPipelineWatchdog(config())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	now = started.Add(25 * time.Minute)
	if err := first.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	// Simulate process loss without Stop: the next owner with the same durable
	// invocation must resume the timeline rather than reset the SLO clock.
	first.mu.Lock()
	first.stopped = true
	close(first.stopCh)
	first.mu.Unlock()
	<-first.doneCh

	now = started.Add(30 * time.Minute)
	recovered, err := newPipelineWatchdog(config())
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.Stop() })
	state := loadPipelineWatchdogStateForTest(t, dir)
	if state.StartedAt != started.Format(time.RFC3339Nano) || state.DiagnosticRelPath == "" {
		t.Fatalf("recovery reset original timeline or diagnostic: %+v", state)
	}
	entries, err := os.ReadDir(filepath.Join(pipelineWatchdogControlRootForTest(t, dir), filepath.FromSlash(pipelineWatchdogDiagnosticPath)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("recovery duplicated diagnostic: entries=%d err=%v", len(entries), err)
	}
}

func TestPipelineWatchdogStopIsPersistentAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: dir, InvocationID: "invocation-stop", Stage: "render", Chapter: 7,
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := watchdog.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := loadPipelineWatchdogStateForTest(t, dir)
	if stopped.Status != pipelineWatchdogStopped || stopped.StoppedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("stop was not persisted: %+v", stopped)
	}
	if err := watchdog.Stop(); err != nil {
		t.Fatalf("second stop must be idempotent: %v", err)
	}
	if err := watchdog.EvaluateAt(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after := loadPipelineWatchdogStateForTest(t, dir)
	if after.Status != pipelineWatchdogStopped || after.HeartbeatAt != stopped.HeartbeatAt {
		t.Fatalf("post-stop evaluation revived watchdog: before=%+v after=%+v", stopped, after)
	}
	if err := watchdog.Progress("late_progress"); err == nil {
		t.Fatal("post-stop progress unexpectedly succeeded")
	}
}

func TestPipelineWatchdogPauseDoesNotRecreateLiveRootDuringDirectorySwap(t *testing.T) {
	parent := t.TempDir()
	live := filepath.Join(parent, "live")
	archive := filepath.Join(parent, "archive")
	candidate := filepath.Join(parent, "candidate")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "candidate.marker"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "invocation-directory-swap", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := bindCurrentPipelineWatchdog(watchdog)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	defer func() { _ = watchdog.Stop() }()

	err = withPipelineWatchdogPaused(func() error {
		if err := os.Rename(live, archive); err != nil {
			return err
		}
		now = now.Add(time.Minute)
		if err := watchdog.EvaluateAt(now); err != nil {
			return err
		}
		if _, err := os.Stat(live); !os.IsNotExist(err) {
			return fmt.Errorf("paused heartbeat recreated missing live root: %v", err)
		}
		if err := watchdog.Progress("candidate_installed"); err != nil {
			return err
		}
		return os.Rename(candidate, live)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(live, "candidate.marker")); err != nil {
		t.Fatalf("candidate was not installed as live: %v", err)
	}
	state := loadPipelineWatchdogStateForTest(t, live)
	if state.ProgressSeq != 1 || state.LastProgressKind != "candidate_installed" {
		t.Fatalf("paused progress was not flushed into new live root: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(archive, filepath.FromSlash(pipelineWatchdogStatePath))); !os.IsNotExist(err) {
		t.Fatalf("watchdog state leaked into archived live root: %v", err)
	}
}

func TestPipelineWatchdogControlRootDoesNotDriftLiveCAS(t *testing.T) {
	live := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "canon.json"), []byte("{\"chapter\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 12, 30, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "invocation-cas", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watchdog.Stop() }()
	now = now.Add(20 * time.Second)
	if err := watchdog.EvaluateAt(now); err != nil {
		t.Fatal(err)
	}
	after, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("watchdog heartbeat drifted live directory CAS: before=%s after=%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(
		pipelineWatchdogControlRootForTest(t, live),
		filepath.FromSlash(pipelineWatchdogStatePath),
	)); err != nil {
		t.Fatalf("watchdog state missing from sibling control root: %v", err)
	}
}

func TestPipelineWatchdogPublishFailureDoesNotRecreateMissingLiveRoot(t *testing.T) {
	parent := t.TempDir()
	live := filepath.Join(parent, "live")
	archive := filepath.Join(parent, "archive")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "invocation-failed-swap", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := bindCurrentPipelineWatchdog(watchdog)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	publishErr := errors.New("fault after live archive")
	err = withPipelineWatchdogPaused(func() error {
		if err := os.Rename(live, archive); err != nil {
			return err
		}
		return publishErr
	})
	if !errors.Is(err, publishErr) {
		t.Fatalf("publish fault was lost: %v", err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("resume recreated missing live root: %v", err)
	}
	if err := watchdog.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("stop recreated missing live root: %v", err)
	}
}

func TestPipelineWatchdogNestedPauseFlushesOnlyAtOutermostResume(t *testing.T) {
	live := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 13, 30, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "invocation-nested-pause", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := bindCurrentPipelineWatchdog(watchdog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release()
		_ = watchdog.Stop()
	})

	initial := loadPipelineWatchdogStateForTest(t, live)
	err = withPipelineWatchdogPaused(func() error {
		now = now.Add(time.Second)
		if err := pipelineWatchdogProgress("outer_progress"); err != nil {
			return err
		}
		if persisted := loadPipelineWatchdogStateForTest(t, live); persisted.ProgressSeq != initial.ProgressSeq {
			return fmt.Errorf("outer paused progress flushed early: %+v", persisted)
		}
		if err := withPipelineWatchdogPaused(func() error {
			now = now.Add(time.Second)
			return pipelineWatchdogProgress("inner_progress")
		}); err != nil {
			return err
		}
		watchdog.mu.Lock()
		depth := watchdog.pauseDepth
		watchdog.mu.Unlock()
		if depth != 1 {
			return fmt.Errorf("inner resume pause depth=%d want=1", depth)
		}
		if persisted := loadPipelineWatchdogStateForTest(t, live); persisted.ProgressSeq != initial.ProgressSeq {
			return fmt.Errorf("inner resume flushed before outer resume: %+v", persisted)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	watchdog.mu.Lock()
	depth := watchdog.pauseDepth
	watchdog.mu.Unlock()
	if depth != 0 {
		t.Fatalf("outer resume pause depth=%d want=0", depth)
	}
	flushed := loadPipelineWatchdogStateForTest(t, live)
	if flushed.ProgressSeq != initial.ProgressSeq+2 || flushed.LastProgressKind != "inner_progress" {
		t.Fatalf("outermost resume did not flush accumulated progress: %+v", flushed)
	}
}

func TestPipelineWatchdogPauseProgressEvaluateConcurrent(t *testing.T) {
	live := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "invocation-concurrent-pause", Stage: "render",
		HeartbeatInterval: time.Hour, Now: func() time.Time { return started },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := bindCurrentPipelineWatchdog(watchdog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release()
		_ = watchdog.Stop()
	})

	const workers = 48
	errCh := make(chan error, workers*3)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			if err := withPipelineWatchdogPaused(func() error {
				return pipelineWatchdogProgress("paused_progress")
			}); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := pipelineWatchdogProgress("concurrent_progress"); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := watchdog.EvaluateAt(started.Add(time.Duration(i+1) * time.Millisecond)); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for concurrentErr := range errCh {
		t.Error(concurrentErr)
	}
	if t.Failed() {
		return
	}
	watchdog.mu.Lock()
	depth := watchdog.pauseDepth
	watchdog.mu.Unlock()
	if depth != 0 {
		t.Fatalf("concurrent pause depth=%d want=0", depth)
	}
	state := loadPipelineWatchdogStateForTest(t, live)
	if state.ProgressSeq != workers*2 {
		t.Fatalf("concurrent progress seq=%d want=%d", state.ProgressSeq, workers*2)
	}
}

func loadPipelineWatchdogStateForTest(t *testing.T, dir string) pipelineWatchdogState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pipelineWatchdogControlRootForTest(t, dir), filepath.FromSlash(pipelineWatchdogStatePath)))
	if err != nil {
		t.Fatal(err)
	}
	var state pipelineWatchdogState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func pipelineWatchdogControlRootForTest(t *testing.T, outputDir string) string {
	t.Helper()
	root, err := pipelineWatchdogControlRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
