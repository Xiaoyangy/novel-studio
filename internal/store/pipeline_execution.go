package store

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	pipelineExecutionPath       = "meta/runtime/pipeline_execution.json"
	pipelineExecutionGuardPath  = "meta/runtime/pipeline_execution.guard"
	pipelineExecutionVersion    = 1
	defaultPipelineExecutionTTL = 30 * time.Minute
)

var pipelineExecutionGuardWait = 5 * time.Second

// Different Store instances can coexist in one process. Their IO mutexes are
// instance-local, so lock acquisition also needs a process-wide critical
// section.
var pipelineExecutionProcessMu sync.Mutex

// AcquirePipelineExecution acquires or refreshes a chapter-scoped execution
// lease. Re-entry by the same owner is allowed; a different active owner is
// rejected. A zero ExpiresAt receives a bounded default TTL.
func (s *RuntimeStore) AcquirePipelineExecution(lock domain.PipelineExecutionLock) error {
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	now := time.Now().UTC()
	lock.Owner = strings.TrimSpace(lock.Owner)
	lock.PlanDigest = strings.TrimSpace(lock.PlanDigest)
	if lock.TargetChapter <= 0 {
		return fmt.Errorf("pipeline execution target_chapter must be > 0")
	}
	if lock.Owner == "" {
		return fmt.Errorf("pipeline execution owner must not be empty")
	}
	switch lock.Mode {
	case domain.PipelineExecutionFoundation, domain.PipelineExecutionWorldTick, domain.PipelineExecutionOutlineAll, domain.PipelineExecutionPreplan, domain.PipelineExecutionProjectAll:
	case domain.PipelineExecutionRender:
		if lock.PlanDigest == "" {
			return fmt.Errorf("pipeline render execution requires plan_digest")
		}
	default:
		return fmt.Errorf("invalid pipeline execution mode %q", lock.Mode)
	}
	lock.AcquiredAt = now
	// Process identity is local runtime evidence, never caller-controlled
	// metadata. Trusting a supplied PID would let a live owner be mistaken for a
	// dead process and have its lease overwritten.
	lock.ProcessID = os.Getpid()
	if lock.ExpiresAt.IsZero() {
		lock.ExpiresAt = now.Add(defaultPipelineExecutionTTL)
	} else {
		lock.ExpiresAt = lock.ExpiresAt.UTC()
	}
	if !lock.ExpiresAt.After(now) {
		return fmt.Errorf("pipeline execution lease must expire in the future")
	}
	lock.Version = pipelineExecutionVersion

	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			var existing domain.PipelineExecutionLock
			err := s.io.ReadJSONUnlocked(pipelineExecutionPath, &existing)
			switch {
			case err == nil &&
				existing.ActiveAt(now) &&
				pipelineExecutionOwnerProcessAlive(existing) &&
				strings.TrimSpace(existing.Owner) != lock.Owner:
				return fmt.Errorf(
					"pipeline execution is owned by %q until %s",
					existing.Owner,
					existing.ExpiresAt.Format(time.RFC3339),
				)
			case err != nil && !os.IsNotExist(err):
				return fmt.Errorf("load pipeline execution lock: %w", err)
			}
			return s.io.WriteJSONUnlocked(pipelineExecutionPath, lock)
		})
	})
}

// LoadPipelineExecution returns the active execution lease. Expired leases are
// removed atomically and behave exactly like the legacy no-lock path.
func (s *RuntimeStore) LoadPipelineExecution() (*domain.PipelineExecutionLock, error) {
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	now := time.Now().UTC()
	var result *domain.PipelineExecutionLock
	err := s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			var lock domain.PipelineExecutionLock
			if err := s.io.ReadJSONUnlocked(pipelineExecutionPath, &lock); err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return fmt.Errorf("load pipeline execution lock: %w", err)
			}
			if err := validateStoredPipelineExecution(lock); err != nil {
				return err
			}
			if !lock.ActiveAt(now) || !pipelineExecutionOwnerProcessAlive(lock) {
				return s.io.RemoveFileUnlocked(pipelineExecutionPath)
			}
			copy := lock
			result = &copy
			return nil
		})
	})
	return result, err
}

// ReleasePipelineExecution removes the lease owned by owner. Owner matching
// prevents one concurrent pipeline from clearing another pipeline's boundary.
// Expired leases can be released by any caller because they are no longer
// active.
func (s *RuntimeStore) ReleasePipelineExecution(owner string) error {
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("pipeline execution owner must not be empty")
	}
	now := time.Now().UTC()
	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			var lock domain.PipelineExecutionLock
			if err := s.io.ReadJSONUnlocked(pipelineExecutionPath, &lock); err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return fmt.Errorf("load pipeline execution lock: %w", err)
			}
			if lock.ActiveAt(now) && strings.TrimSpace(lock.Owner) != owner {
				return fmt.Errorf("pipeline execution is owned by %q, not %q", lock.Owner, owner)
			}
			return s.io.RemoveFileUnlocked(pipelineExecutionPath)
		})
	})
}

// withPipelineExecutionTransaction serializes the read-check-write sequence
// across independent novel-studio processes. The JSON lease is the durable
// phase boundary; the OS advisory lock only makes acquisition, expiry cleanup
// and release atomic. The kernel releases flock automatically if a process
// crashes, so recovery never relies on a racy stat-and-unlink stale heuristic.
func (s *RuntimeStore) withPipelineExecutionTransaction(fn func() error) error {
	guardPath := s.io.path(pipelineExecutionGuardPath)
	if err := os.MkdirAll(s.io.path("meta/runtime"), 0o755); err != nil {
		return fmt.Errorf("create pipeline execution guard directory: %w", err)
	}
	f, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open pipeline execution transaction guard: %w", err)
	}
	defer func() { _ = f.Close() }()

	deadline := time.Now().Add(pipelineExecutionGuardWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return fmt.Errorf("flock pipeline execution transaction guard: %w", err)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for pipeline execution transaction guard %s", guardPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}()
	return fn()
}

// pipelineExecutionGuardFD is kept private but exposed to the package tests so
// they can prove that a separate process holding flock blocks acquisition.
func pipelineExecutionGuardFD(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func validateStoredPipelineExecution(lock domain.PipelineExecutionLock) error {
	if lock.Version != pipelineExecutionVersion {
		return fmt.Errorf("unsupported pipeline execution lock version %d", lock.Version)
	}
	if lock.TargetChapter <= 0 || strings.TrimSpace(lock.Owner) == "" || lock.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid pipeline execution lock metadata")
	}
	switch lock.Mode {
	case domain.PipelineExecutionFoundation, domain.PipelineExecutionWorldTick, domain.PipelineExecutionOutlineAll, domain.PipelineExecutionPreplan, domain.PipelineExecutionProjectAll:
		return nil
	case domain.PipelineExecutionRender:
		if strings.TrimSpace(lock.PlanDigest) == "" {
			return fmt.Errorf("invalid pipeline render execution lock: plan_digest is empty")
		}
		return nil
	default:
		return fmt.Errorf("invalid pipeline execution mode %q", lock.Mode)
	}
}

func pipelineExecutionOwnerProcessAlive(lock domain.PipelineExecutionLock) bool {
	pid := lock.ProcessID
	if pid <= 0 {
		pid = pipelineExecutionOwnerPID(lock.Owner)
	}
	if pid <= 0 {
		// Legacy/custom owners without a PID remain lease-protected.
		return true
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func pipelineExecutionOwnerPID(owner string) int {
	owner = strings.TrimSpace(owner)
	const marker = "-pid"
	idx := strings.Index(owner, marker)
	if idx < 0 {
		return 0
	}
	start := idx + len(marker)
	end := start
	for end < len(owner) && owner[end] >= '0' && owner[end] <= '9' {
		end++
	}
	if end == start {
		return 0
	}
	pid, err := strconv.Atoi(owner[start:end])
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
