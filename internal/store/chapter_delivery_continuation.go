package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const chapterDeliveryContinuationVersion = "chapter-delivery-continuation.v1"
const chapterDeliveryObserveOnly = "observe_only"

// ChapterDeliveryContinuationV1 records explicit host authorization, not a
// model capability or a replacement for the frozen budget or acceptance proof.
type ChapterDeliveryContinuationV1 struct {
	Version              string    `json:"version"`
	Mode                 string    `json:"mode"`
	GenerationID         string    `json:"generation_id"`
	Reason               string    `json:"reason"`
	AuthorizedAt         time.Time `json:"authorized_at"`
	OriginalPolicy       string    `json:"original_policy"`
	OriginalLimitSeconds int       `json:"original_limit_seconds"`
	AuthorizationDigest  string    `json:"authorization_digest"`
}

// AuthorizeChapterDeliveryOverrun preserves the original clocks. Normally no
// execution lease may be active; the sole exception is the explicitly named
// current-process project_all lease for this generation's first chapter.
func (s *Store) AuthorizeChapterDeliveryOverrun(generation domain.PlanningGenerationV2, reason string, now time.Time, ownLeaseOwner ...string) error {
	return s.authorizeChapterDeliveryOverrun(generation, reason, func() time.Time { return now }, ownLeaseOwner...)
}

func (s *Store) AuthorizeChapterDeliveryOverrunNow(generation domain.PlanningGenerationV2, reason string, ownLeaseOwner ...string) error {
	return s.authorizeChapterDeliveryOverrun(generation, reason, time.Now, ownLeaseOwner...)
}

func (s *Store) authorizeChapterDeliveryOverrun(generation domain.PlanningGenerationV2, reason string, clock func() time.Time, owners ...string) error {
	if s == nil || !validChapterDeliveryContinuationReason(reason) {
		return fmt.Errorf("chapter delivery continuation requires a nonblank canonical reason (at most 8192 UTF-8 bytes)")
	}
	if len(owners) > 1 || len(owners) == 1 && (strings.TrimSpace(owners[0]) == "" || strings.TrimSpace(owners[0]) != owners[0]) {
		return fmt.Errorf("chapter delivery continuation requires at most one exact nonblank own lease owner")
	}
	actual, err := s.authenticateChapterDeliveryGeneration(generation)
	if err != nil {
		return err
	}
	if actual.ChapterDeliveryBudget == nil {
		return fmt.Errorf("chapter delivery continuation requires an originally budgeted generation")
	}
	// Reject missing/corrupt/unarmed ledgers before creating any transaction
	// guards. The authoritative read is repeated under both locks below.
	ledger, err := s.readChapterDeliveryAuthorizationPreflight()
	if err != nil {
		return err
	}
	if _, err := chapterDeliveryEntry(ledger, *actual); err != nil {
		return err
	}
	for _, rel := range []string{".", "meta", "meta/runtime"} {
		info, err := os.Lstat(filepath.Join(s.dir, rel))
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("chapter delivery authorization runtime path is redirected")
		}
	}
	if info, err := os.Lstat(filepath.Join(s.dir, pipelineExecutionGuardPath)); err == nil {
		if err := validateArcCycleSealedEvidenceFileInfo(info); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Match Acquire/Release's order. Do not hold Runtime IO locks while the
	// delivery ledger calls planning readers, and never call public lease
	// readers here: those would reacquire this transaction and clean up files.
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()
	return s.Runtime.withPipelineExecutionTransaction(func() error {
		return s.withChapterDeliveryLock(func() error {
			now := clock()
			if err := s.validateChapterDeliveryAuthorizationLease(*actual, now, owners); err != nil {
				return err
			}
			ledger, err := s.readChapterDeliveryLedger()
			if err != nil {
				return err
			}
			entry, err := chapterDeliveryEntry(ledger, *actual)
			if err != nil {
				return err
			}
			if entry.Continuation != nil {
				if entry.Continuation.Reason != reason {
					return fmt.Errorf("chapter delivery continuation already has a different immutable authorization reason")
				}
				return nil
			}
			if err := chapterDeliveryObserve(ledger, now); err != nil {
				return err
			}
			grant := &ChapterDeliveryContinuationV1{Version: chapterDeliveryContinuationVersion, Mode: chapterDeliveryObserveOnly,
				GenerationID: actual.GenerationID, Reason: reason, AuthorizedAt: now.UTC().Round(0), OriginalPolicy: entry.Policy, OriginalLimitSeconds: entry.LimitSeconds}
			grant.AuthorizationDigest, err = chapterDeliveryContinuationDigest(*grant)
			if err != nil {
				return err
			}
			entry.Continuation = grant
			ledger.Generations[actual.GenerationID] = entry
			return s.writeChapterDeliveryLedger(ledger)
		})
	})
}

// The runtime ledger is replaced atomically, not immutable. Its strict
// path/inode reader must share the existing writer's flock so a concurrent
// rename cannot unlink the inode being authenticated. This preflight never
// creates directories or guards: malformed/unarmed requests remain read-only.
// Release this lock before entering the process -> runtime -> delivery order.
func (s *Store) readChapterDeliveryAuthorizationPreflight() (*chapterDeliveryLedgerV1, error) {
	path, before, err := validateArcCycleSealedEvidenceFilesystemPath(s.dir, filepath.Join(chapterDeliveryRoot, ".write.lock"))
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	opened, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateArcCycleSealedEvidenceFileInfo(opened); err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("chapter delivery preflight lock changed before open")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_, current, err := validateArcCycleSealedEvidenceFilesystemPath(s.dir, filepath.Join(chapterDeliveryRoot, ".write.lock"))
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, current) {
		return nil, fmt.Errorf("chapter delivery preflight lock changed while acquiring")
	}
	return s.readChapterDeliveryLedger()
}

func (s *Store) validateChapterDeliveryAuthorizationLease(generation domain.PlanningGenerationV2, now time.Time, owners []string) error {
	var lock domain.PipelineExecutionLock
	err := s.readChapterDeliveryProofJSON(pipelineExecutionPath, &lock)
	if os.IsNotExist(err) {
		if len(owners) != 0 {
			return fmt.Errorf("chapter delivery continuation own execution lease is missing")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateStoredPipelineExecution(lock); err != nil {
		return err
	}
	if !lock.ActiveAt(now) {
		if len(owners) != 0 {
			return fmt.Errorf("chapter delivery continuation own execution lease is expired")
		}
		return nil // Do not clean up or rewrite an expired lease.
	}
	if len(owners) == 1 && lock.Owner == owners[0] && lock.ProcessID == os.Getpid() && !lock.AcquiredAt.IsZero() &&
		lock.Mode == domain.PipelineExecutionProjectAll && lock.TargetChapter == generation.FirstProjectedChapter {
		return nil
	}
	return fmt.Errorf("chapter delivery continuation cannot be authorized during an active execution lease except the caller's exact project_all lease")
}

func validChapterDeliveryContinuationReason(reason string) bool {
	return reason != "" && strings.TrimSpace(reason) == reason && len(reason) <= 8192 && utf8.ValidString(reason)
}

func chapterDeliveryContinuationDigest(grant ChapterDeliveryContinuationV1) (string, error) {
	grant.AuthorizationDigest = ""
	raw, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	return domain.ComputeArcArtifactSHA256(raw), nil
}

func validateChapterDeliveryContinuation(entry chapterDeliveryGenerationV1, observed time.Time) error {
	grant := entry.Continuation
	if grant == nil {
		return nil
	}
	if grant.Version != chapterDeliveryContinuationVersion || grant.Mode != chapterDeliveryObserveOnly || grant.GenerationID != entry.GenerationID ||
		!validChapterDeliveryContinuationReason(grant.Reason) || grant.AuthorizedAt.IsZero() || grant.AuthorizedAt.Before(entry.ArmedAt) || grant.AuthorizedAt.After(observed) ||
		grant.OriginalPolicy != entry.Policy || grant.OriginalLimitSeconds != entry.LimitSeconds {
		return fmt.Errorf("chapter delivery continuation authorization identity, original budget or time is invalid")
	}
	want, err := chapterDeliveryContinuationDigest(*grant)
	if err != nil || want != grant.AuthorizationDigest {
		return fmt.Errorf("chapter delivery continuation authorization digest mismatch")
	}
	return nil
}
