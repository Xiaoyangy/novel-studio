package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	pipelineRenderProsePermitPath    = "meta/runtime/render_prose_permit.json"
	pipelineRenderProsePermitVersion = 2
	renderDispatchLedgerVersion      = domain.PipelineRenderDispatchLedgerVersion
)

// PipelineRenderProsePermit is a one-shot, durable capability for entering the
// sealed Drafter subagent. It is bound to both the active execution lease and
// one exact persistent dispatch reservation.
type PipelineRenderProsePermit struct {
	Version                int       `json:"version"`
	AuthorizationDigest    string    `json:"authorization_digest"`
	ReservationAttempt     int       `json:"reservation_attempt"`
	CandidateID            string    `json:"candidate_id"`
	GenerationID           string    `json:"generation_id"`
	Chapter                int       `json:"chapter"`
	PlanDigest             string    `json:"plan_digest"`
	PlanCheckpointSeq      int64     `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string    `json:"projected_bundle_digest"`
	PromotionReceiptDigest string    `json:"promotion_receipt_digest"`
	ExecutionOwner         string    `json:"execution_owner"`
	ExecutionProcessID     int       `json:"execution_process_id"`
	ExecutionAcquiredAt    time.Time `json:"execution_acquired_at"`
	ExecutionExpiresAt     time.Time `json:"execution_expires_at"`
	ArmedAt                time.Time `json:"armed_at"`
}

type renderPermitCandidateManifest struct {
	Version                string `json:"version"`
	CandidateID            string `json:"candidate_id"`
	GenerationID           string `json:"generation_id"`
	Chapter                int    `json:"chapter"`
	PlanDigest             string `json:"plan_digest"`
	PlanCheckpointSeq      int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string `json:"projected_bundle_digest"`
	PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	SourceOutputDir        string `json:"source_output_dir"`
}

type renderPermitDispatchReservation = domain.PipelineRenderDispatchReservation
type renderPermitDispatchLedger = domain.PipelineRenderDispatchLedger

// ArmPipelineRenderProsePermit converts one unconsumed durable dispatch
// reservation into a one-shot Drafter capability. The reservation is marked
// armed before the permit is written, so a crash can waste a slot but can never
// create an unbudgeted provider call.
func (s *RuntimeStore) ArmPipelineRenderProsePermit(authorization string, attempt int) error {
	authorization = strings.TrimSpace(authorization)
	if !validRenderPermitDigest(authorization) || attempt <= 0 {
		return fmt.Errorf("render prose permit requires a valid reservation authorization and attempt")
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			lock, err := s.loadActiveRenderExecutionUnlocked()
			if err != nil {
				return err
			}
			var existing PipelineRenderProsePermit
			if err := s.io.ReadJSONUnlocked(pipelineRenderProsePermitPath, &existing); err == nil {
				return fmt.Errorf("render prose permit is already armed")
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("load render prose permit: %w", err)
			}

			manifest, ledgerPath, err := s.renderPermitEvidencePathsUnlocked(lock)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			if err := mutateRenderPermitDispatchLedger(ledgerPath, func(ledger *renderPermitDispatchLedger) error {
				if err := validateRenderPermitLedgerIdentity(ledger, manifest, lock); err != nil {
					return err
				}
				for i := range ledger.Reservations {
					reservation := &ledger.Reservations[i]
					if reservation.AuthorizationDigest != authorization || reservation.Attempt != attempt {
						continue
					}
					if reservation.Status != "reserved" || reservation.FinishedAt != "" ||
						reservation.PermitArmedAt != "" || reservation.ProviderDispatchedAt != "" {
						return fmt.Errorf("render dispatch reservation is old, finished, armed, or already consumed")
					}
					reservation.Status = "permit_armed"
					reservation.PermitArmedAt = now.Format(time.RFC3339Nano)
					return nil
				}
				return fmt.Errorf("render prose permit authorization is not present in dispatch ledger")
			}); err != nil {
				return fmt.Errorf("arm render prose permit from dispatch ledger: %w", err)
			}

			permit := PipelineRenderProsePermit{
				Version:                pipelineRenderProsePermitVersion,
				AuthorizationDigest:    authorization,
				ReservationAttempt:     attempt,
				CandidateID:            manifest.CandidateID,
				GenerationID:           manifest.GenerationID,
				Chapter:                manifest.Chapter,
				PlanDigest:             manifest.PlanDigest,
				PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
				ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
				PromotionReceiptDigest: manifest.PromotionReceiptDigest,
				ExecutionOwner:         lock.Owner,
				ExecutionProcessID:     lock.ProcessID,
				ExecutionAcquiredAt:    lock.AcquiredAt,
				ExecutionExpiresAt:     lock.ExpiresAt,
				ArmedAt:                now,
			}
			if err := s.io.WriteJSONUnlocked(pipelineRenderProsePermitPath, permit); err != nil {
				return fmt.Errorf("persist render prose permit: %w", err)
			}
			return nil
		})
	})
}

// ConsumePipelineRenderProsePermit atomically consumes the one-shot permit at
// the Drafter subagent gate. It durably marks the ledger reservation dispatched
// before removing the permit. Any crash window therefore fails closed.
func (s *RuntimeStore) ConsumePipelineRenderProsePermit(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("render prose permit requires an exact target chapter")
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			lock, err := s.loadActiveRenderExecutionUnlocked()
			if err != nil {
				return err
			}
			var permit PipelineRenderProsePermit
			if err := s.io.ReadJSONUnlocked(pipelineRenderProsePermitPath, &permit); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("render prose permit is absent or already consumed")
				}
				return fmt.Errorf("load render prose permit: %w", err)
			}
			manifest, ledgerPath, err := s.renderPermitEvidencePathsUnlocked(lock)
			if err != nil {
				return err
			}
			if err := validateRenderProsePermit(permit, manifest, lock, chapter, time.Now().UTC()); err != nil {
				_ = s.io.RemoveFileUnlocked(pipelineRenderProsePermitPath)
				return err
			}
			now := time.Now().UTC()
			if err := mutateRenderPermitDispatchLedger(ledgerPath, func(ledger *renderPermitDispatchLedger) error {
				if err := validateRenderPermitLedgerIdentity(ledger, manifest, lock); err != nil {
					return err
				}
				for i := range ledger.Reservations {
					reservation := &ledger.Reservations[i]
					if reservation.AuthorizationDigest != permit.AuthorizationDigest ||
						reservation.Attempt != permit.ReservationAttempt {
						continue
					}
					if reservation.Status != "permit_armed" || reservation.FinishedAt != "" ||
						reservation.ProviderDispatchedAt != "" || reservation.PermitArmedAt == "" {
						return fmt.Errorf("render prose permit reservation is old, finished, or already consumed")
					}
					reservation.Status = "provider_dispatched"
					reservation.ProviderDispatchedAt = now.Format(time.RFC3339Nano)
					return nil
				}
				return fmt.Errorf("render prose permit reservation is missing")
			}); err != nil {
				return fmt.Errorf("consume render prose permit in dispatch ledger: %w", err)
			}
			if err := s.io.RemoveFileUnlocked(pipelineRenderProsePermitPath); err != nil {
				return fmt.Errorf("remove consumed render prose permit: %w", err)
			}
			return nil
		})
	})
}

// ValidatePipelineRenderProsePermit performs the Coordinator-side availability
// precheck without consuming provider evidence. The primed model must still
// call ConsumePipelineRenderProsePermit immediately before its downstream
// provider; this method only avoids starting a Drafter session that cannot be
// authorized.
func (s *RuntimeStore) ValidatePipelineRenderProsePermit(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("render prose permit requires an exact target chapter")
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()
	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			lock, err := s.loadActiveRenderExecutionUnlocked()
			if err != nil {
				return err
			}
			var permit PipelineRenderProsePermit
			if err := s.io.ReadJSONUnlocked(pipelineRenderProsePermitPath, &permit); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("render prose permit is absent or already consumed")
				}
				return fmt.Errorf("load render prose permit: %w", err)
			}
			manifest, ledgerPath, err := s.renderPermitEvidencePathsUnlocked(lock)
			if err != nil {
				return err
			}
			if err := validateRenderProsePermit(permit, manifest, lock, chapter, time.Now().UTC()); err != nil {
				return err
			}
			return inspectRenderPermitDispatchLedger(ledgerPath, func(ledger *renderPermitDispatchLedger) error {
				if err := validateRenderPermitLedgerIdentity(ledger, manifest, lock); err != nil {
					return err
				}
				return validateArmedRenderPermitReservation(ledger, permit)
			})
		})
	})
}

// ClearPipelineRenderProsePermit removes an unconsumed permit before/after a
// Host turn. A non-empty authorization prevents an old turn from clearing a
// newer turn's capability.
func (s *RuntimeStore) ClearPipelineRenderProsePermit(authorization string) error {
	authorization = strings.TrimSpace(authorization)
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()
	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			if authorization != "" {
				var permit PipelineRenderProsePermit
				if err := s.io.ReadJSONUnlocked(pipelineRenderProsePermitPath, &permit); err != nil {
					if os.IsNotExist(err) {
						return nil
					}
					return fmt.Errorf("load render prose permit for cleanup: %w", err)
				}
				if permit.AuthorizationDigest != authorization {
					return fmt.Errorf("refuse to clear a different render prose permit")
				}
			}
			if err := s.io.RemoveFileUnlocked(pipelineRenderProsePermitPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("clear render prose permit: %w", err)
			}
			return nil
		})
	})
}

func (s *RuntimeStore) loadActiveRenderExecutionUnlocked() (domain.PipelineExecutionLock, error) {
	var lock domain.PipelineExecutionLock
	if err := s.io.ReadJSONUnlocked(pipelineExecutionPath, &lock); err != nil {
		return lock, fmt.Errorf("load render execution for prose permit: %w", err)
	}
	if err := validateStoredPipelineExecution(lock); err != nil {
		return lock, err
	}
	now := time.Now().UTC()
	if lock.Mode != domain.PipelineExecutionRender || !lock.ActiveAt(now) ||
		!pipelineExecutionOwnerProcessAlive(lock) || lock.ProcessID != os.Getpid() {
		return lock, fmt.Errorf("render prose permit requires the current process's active render execution lease")
	}
	return lock, nil
}

func (s *RuntimeStore) renderPermitEvidencePathsUnlocked(lock domain.PipelineExecutionLock) (renderPermitCandidateManifest, string, error) {
	var manifest renderPermitCandidateManifest
	if err := s.io.ReadJSONUnlocked("meta/planning/render_candidate.json", &manifest); err != nil {
		return manifest, "", fmt.Errorf("load render candidate for prose permit: %w", err)
	}
	if manifest.Version != "pipeline-render-candidate.v2" || manifest.CandidateID == "" ||
		manifest.CandidateID == "." || manifest.CandidateID == ".." ||
		filepath.Base(manifest.CandidateID) != manifest.CandidateID || strings.ContainsAny(manifest.CandidateID, `/\\`) ||
		strings.TrimSpace(manifest.GenerationID) == "" ||
		manifest.Chapter != lock.TargetChapter || manifest.PlanDigest != lock.PlanDigest ||
		!validRenderPermitDigest(manifest.PlanDigest) || manifest.PlanCheckpointSeq <= 0 ||
		!validRenderPermitDigest(manifest.ProjectedBundleDigest) ||
		!validRenderPermitDigest(manifest.PromotionReceiptDigest) ||
		!filepath.IsAbs(manifest.SourceOutputDir) ||
		manifest.SourceOutputDir != filepath.Clean(manifest.SourceOutputDir) ||
		!filepath.IsAbs(s.io.dir) || s.io.dir != filepath.Clean(s.io.dir) {
		return manifest, "", fmt.Errorf("render candidate identity does not match prose permit execution lease")
	}
	namespace := filepath.Join(filepath.Dir(manifest.SourceOutputDir), ".render-candidates")
	container := filepath.Join(namespace, manifest.CandidateID)
	expectedCandidateOutput := filepath.Join(
		container, "output",
	)
	if s.io.dir != expectedCandidateOutput {
		return manifest, "", fmt.Errorf("render candidate path does not match prose permit manifest")
	}
	convergenceRoot := filepath.Join(namespace, "convergence")
	convergenceDir := filepath.Join(convergenceRoot, manifest.CandidateID)
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "source output", path: manifest.SourceOutputDir},
		{name: "candidate namespace", path: namespace},
		{name: "candidate container", path: container},
		{name: "candidate output", path: s.io.dir},
		{name: "convergence root", path: convergenceRoot},
		{name: "candidate convergence directory", path: convergenceDir},
	} {
		info, err := os.Lstat(item.path)
		if err != nil {
			return manifest, "", fmt.Errorf("inspect render prose permit %s: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return manifest, "", fmt.Errorf("render prose permit %s must be a real directory", item.name)
		}
	}
	resolvedSource, err := filepath.EvalSymlinks(manifest.SourceOutputDir)
	if err != nil {
		return manifest, "", fmt.Errorf("resolve render prose permit source output: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(s.io.dir)
	if err != nil {
		return manifest, "", fmt.Errorf("resolve render prose permit candidate output: %w", err)
	}
	resolvedConvergence, err := filepath.EvalSymlinks(convergenceDir)
	if err != nil {
		return manifest, "", fmt.Errorf("resolve render prose permit convergence directory: %w", err)
	}
	resolvedNamespace := filepath.Join(filepath.Dir(resolvedSource), ".render-candidates")
	if filepath.Clean(resolvedCandidate) != filepath.Join(resolvedNamespace, manifest.CandidateID, "output") ||
		filepath.Clean(resolvedConvergence) != filepath.Join(resolvedNamespace, "convergence", manifest.CandidateID) {
		return manifest, "", fmt.Errorf("render prose permit paths escape their authenticated source namespace")
	}
	ledgerPath := filepath.Join(
		convergenceDir, "dispatch_budget.json",
	)
	if err := validateRenderPermitLedgerFiles(ledgerPath); err != nil {
		return manifest, "", err
	}
	return manifest, ledgerPath, nil
}

func validateRenderProsePermit(
	permit PipelineRenderProsePermit,
	manifest renderPermitCandidateManifest,
	lock domain.PipelineExecutionLock,
	chapter int,
	now time.Time,
) error {
	if permit.Version != pipelineRenderProsePermitVersion || !validRenderPermitDigest(permit.AuthorizationDigest) ||
		permit.ReservationAttempt <= 0 ||
		permit.CandidateID != manifest.CandidateID ||
		permit.GenerationID != manifest.GenerationID ||
		permit.Chapter != manifest.Chapter ||
		permit.PlanDigest != manifest.PlanDigest ||
		permit.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		permit.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		permit.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		permit.Chapter != chapter || permit.Chapter != lock.TargetChapter || permit.PlanDigest != lock.PlanDigest ||
		permit.ExecutionOwner != lock.Owner || permit.ExecutionProcessID != lock.ProcessID ||
		!permit.ExecutionAcquiredAt.Equal(lock.AcquiredAt) || !permit.ExecutionExpiresAt.Equal(lock.ExpiresAt) ||
		permit.ArmedAt.IsZero() || permit.ArmedAt.Before(lock.AcquiredAt) || !permit.ArmedAt.Before(lock.ExpiresAt) ||
		!now.Before(permit.ExecutionExpiresAt) {
		return fmt.Errorf("render prose permit is malformed, stale, or bound to another execution lease")
	}
	return nil
}

func validateRenderPermitLedgerIdentity(ledger *renderPermitDispatchLedger, manifest renderPermitCandidateManifest, lock domain.PipelineExecutionLock) error {
	if err := domain.ValidatePipelineRenderDispatchLedger(ledger); err != nil {
		return err
	}
	if ledger == nil || ledger.Version != renderDispatchLedgerVersion || ledger.CandidateID != manifest.CandidateID ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter || ledger.Chapter != lock.TargetChapter ||
		ledger.PlanDigest != manifest.PlanDigest || ledger.PlanDigest != lock.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest {
		return fmt.Errorf("render dispatch ledger identity does not match prose permit")
	}
	return nil
}

func validateArmedRenderPermitReservation(ledger *renderPermitDispatchLedger, permit PipelineRenderProsePermit) error {
	for i := range ledger.Reservations {
		reservation := &ledger.Reservations[i]
		if reservation.AuthorizationDigest != permit.AuthorizationDigest || reservation.Attempt != permit.ReservationAttempt {
			continue
		}
		if reservation.Status != "permit_armed" || reservation.FinishedAt != "" ||
			reservation.ProviderDispatchedAt != "" || reservation.PermitArmedAt == "" {
			return fmt.Errorf("render prose permit reservation is old, finished, or already consumed")
		}
		return nil
	}
	return fmt.Errorf("render prose permit reservation is missing")
}

func mutateRenderPermitDispatchLedger(path string, mutate func(*renderPermitDispatchLedger) error) error {
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}

	raw, err := readRenderPermitLedgerNoFollow(path)
	if err != nil {
		return err
	}
	var ledger renderPermitDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return err
	}
	if err := domain.ValidatePipelineRenderDispatchLedger(&ledger); err != nil {
		return err
	}
	if err := mutate(&ledger); err != nil {
		return err
	}
	ledger.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := domain.ValidatePipelineRenderDispatchLedger(&ledger); err != nil {
		return err
	}
	raw, err = json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicWriteRenderPermitLedger(path, raw)
}

func inspectRenderPermitDispatchLedger(path string, inspect func(*renderPermitDispatchLedger) error) error {
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}
	raw, err := readRenderPermitLedgerNoFollow(path)
	if err != nil {
		return err
	}
	var ledger renderPermitDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return err
	}
	if err := domain.ValidatePipelineRenderDispatchLedger(&ledger); err != nil {
		return err
	}
	return inspect(&ledger)
}

func atomicWriteRenderPermitLedger(path string, data []byte) error {
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := validateRenderPermitLedgerFiles(path); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func validateRenderPermitLedgerFiles(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || path != filepath.Clean(path) {
		return fmt.Errorf("render prose permit ledger path must be clean and absolute")
	}
	dir := filepath.Dir(path)
	root := filepath.Dir(dir)
	namespace := filepath.Dir(root)
	for _, item := range []struct {
		name string
		path string
		dir  bool
	}{
		{name: "candidate namespace", path: namespace, dir: true},
		{name: "convergence root", path: root, dir: true},
		{name: "candidate convergence directory", path: dir, dir: true},
		{name: "dispatch ledger", path: path},
	} {
		info, err := os.Lstat(item.path)
		if err != nil {
			return fmt.Errorf("inspect render prose permit %s: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || item.dir && !info.IsDir() || !item.dir && !info.Mode().IsRegular() {
			return fmt.Errorf("render prose permit %s has an unsafe filesystem type", item.name)
		}
	}
	if info, err := os.Lstat(path + ".lock"); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("render prose permit ledger lock has an unsafe filesystem type")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readRenderPermitLedgerNoFollow(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func validRenderPermitDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}
