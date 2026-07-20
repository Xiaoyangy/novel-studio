package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPipelineExecutionLeaseOwnershipAndRelease(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 7,
		Owner:         "planner-run",
	}); err != nil {
		t.Fatalf("acquire preplan execution: %v", err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		t.Fatal(err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionPreplan || lock.TargetChapter != 7 {
		t.Fatalf("unexpected execution lock: %+v", lock)
	}
	if lock.ExpiresAt.Sub(lock.AcquiredAt) <= 0 {
		t.Fatalf("expected bounded execution lease: %+v", lock)
	}

	if err := NewStore(st.Dir()).Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 7,
		Owner:         "other-run",
	}); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("different owner should not replace active lease: %v", err)
	}
	if err := st.Runtime.ReleasePipelineExecution("other-run"); err == nil || !strings.Contains(err.Error(), "not") {
		t.Fatalf("different owner should not release active lease: %v", err)
	}
	if err := st.Runtime.ReleasePipelineExecution("planner-run"); err != nil {
		t.Fatalf("release own lease: %v", err)
	}
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil || lock != nil {
		t.Fatalf("expected released lease, lock=%+v err=%v", lock, err)
	}
}

func TestPipelineExecutionPromoteLeaseNeedsNoRenderPlanDigest(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPromote,
		TargetChapter: 1,
		Owner:         "promote-run",
	}); err != nil {
		t.Fatalf("acquire promote execution: %v", err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		t.Fatalf("load promote execution: lock=%+v err=%v", lock, err)
	}
	if lock.Mode != domain.PipelineExecutionPromote || lock.PlanDigest != "" {
		t.Fatalf("unexpected promote execution lock: %+v", lock)
	}
}

func TestPipelineExecutionExpiredLeaseCleansItself(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	expired := domain.PipelineExecutionLock{
		Version:       pipelineExecutionVersion,
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 2,
		PlanDigest:    "sha256:plan",
		Owner:         "crashed-run",
		AcquiredAt:    time.Now().Add(-2 * time.Hour).UTC(),
		ExpiresAt:     time.Now().Add(-time.Hour).UTC(),
	}
	if err := st.Runtime.io.WriteJSON(pipelineExecutionPath, expired); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		t.Fatalf("expired lock must not block a later run: %+v", lock)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), pipelineExecutionPath)); !os.IsNotExist(err) {
		t.Fatalf("expired lock file should be removed, stat err=%v", err)
	}
}

func TestPipelineExecutionDeadOwnerCleansActiveLease(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	dead := domain.PipelineExecutionLock{
		Version:       pipelineExecutionVersion,
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 2,
		Owner:         "pipeline-render-ch000002-pid999999999-epoch",
		ProcessID:     999999999,
		AcquiredAt:    time.Now().UTC(),
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := st.Runtime.io.WriteJSON(pipelineExecutionPath, dead); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		t.Fatalf("dead process must not retain an active execution lease: %+v", lock)
	}
}

func TestPipelineExecutionOverwritesCallerSuppliedProcessID(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 3,
		Owner:         "caller-with-forged-pid",
		ProcessID:     999999999,
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		t.Fatal(err)
	}
	if lock == nil || lock.ProcessID != os.Getpid() {
		t.Fatalf("execution lease trusted caller PID: %+v", lock)
	}
	if err := st.Runtime.ReleasePipelineExecution("caller-with-forged-pid"); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineRenderExecutionRequiresPlanDigest(t *testing.T) {
	st := NewStore(t.TempDir())
	err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		Owner:         "render-run",
	})
	if err == nil || !strings.Contains(err.Error(), "plan_digest") {
		t.Fatalf("render lock without plan digest should fail: %v", err)
	}
}

func TestPipelineExecutionFlockHelper(t *testing.T) {
	guardPath := os.Getenv("NOVEL_STUDIO_TEST_FLOCK_PATH")
	if guardPath == "" {
		return
	}
	f, err := pipelineExecutionGuardFD(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := os.WriteFile(os.Getenv("NOVEL_STUDIO_TEST_FLOCK_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(100 * time.Millisecond)
	}
}

func TestPipelineExecutionCrossProcessFlockBlocksAndCrashReleases(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	guardPath := filepath.Join(st.Dir(), pipelineExecutionGuardPath)
	readyPath := filepath.Join(st.Dir(), "meta", "runtime", "flock-helper.ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPipelineExecutionFlockHelper$")
	cmd.Env = append(os.Environ(),
		"NOVEL_STUDIO_TEST_FLOCK_PATH="+guardPath,
		"NOVEL_STUDIO_TEST_FLOCK_READY="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not acquire cross-process flock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	oldWait := pipelineExecutionGuardWait
	pipelineExecutionGuardWait = 100 * time.Millisecond
	err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 9,
		Owner:         "replacement-owner",
	})
	pipelineExecutionGuardWait = oldWait
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("separate process flock must block acquisition, err=%v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed flock helper unexpectedly exited cleanly")
	}
	cmd.Process = nil

	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionPreplan,
		TargetChapter: 9,
		Owner:         "replacement-owner",
	}); err != nil {
		t.Fatalf("kernel must release flock after helper crash: %v", err)
	}
	if err := st.Runtime.ReleasePipelineExecution("replacement-owner"); err != nil {
		t.Fatal(err)
	}
}
