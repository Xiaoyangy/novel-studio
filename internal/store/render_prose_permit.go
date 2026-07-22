package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

const (
	pipelineRenderProsePermitPath                        = "meta/runtime/render_prose_permit.json"
	pipelineRenderProsePermitVersion                     = 3
	renderPermitCandidateManifestVersionV2               = "pipeline-render-candidate.v2"
	renderPermitCandidateManifestVersionV3EffectiveStyle = "pipeline-render-candidate.v3-effective-style"
	renderPermitEffectiveStyleContractPath               = "meta/planning/effective_render_style_contract.json"
	renderPermitEffectiveStyleContractVersion            = "effective-render-style-contract.v1"
	renderPermitStyleContractProtocolVersion             = "render-style-contract.v4-surface-payload-guard-and-serial-memory"
	renderPermitStyleEpochIntentVersion                  = "pipeline-render-style-epoch-intent.v1"
	renderPermitStyleEpochIntentDir                      = "style-epochs"
	renderDispatchLedgerVersion                          = domain.PipelineRenderDispatchLedgerVersion
)

// PipelineRenderProsePermit is a one-shot, durable capability for entering the
// sealed Drafter subagent. It is bound to both the active execution lease and
// one exact persistent dispatch reservation.
type PipelineRenderProsePermit struct {
	Version                     int       `json:"version"`
	AuthorizationDigest         string    `json:"authorization_digest"`
	ReservationAttempt          int       `json:"reservation_attempt"`
	CandidateID                 string    `json:"candidate_id"`
	SourceOutputDir             string    `json:"source_output_dir"`
	GenerationID                string    `json:"generation_id"`
	Chapter                     int       `json:"chapter"`
	PlanDigest                  string    `json:"plan_digest"`
	PlanCheckpointSeq           int64     `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string    `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string    `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string    `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string    `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string    `json:"effective_style_receipt_digest,omitempty"`
	ExecutionOwner              string    `json:"execution_owner"`
	ExecutionProcessID          int       `json:"execution_process_id"`
	ExecutionAcquiredAt         time.Time `json:"execution_acquired_at"`
	ExecutionExpiresAt          time.Time `json:"execution_expires_at"`
	ArmedAt                     time.Time `json:"armed_at"`
}

type renderPermitCandidateManifest struct {
	Version                     string `json:"version"`
	CandidateID                 string `json:"candidate_id"`
	GenerationID                string `json:"generation_id"`
	Chapter                     int    `json:"chapter"`
	PlanDigest                  string `json:"plan_digest"`
	PlanCheckpointSeq           int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest,omitempty"`
	SourceOutputDir             string `json:"source_output_dir"`
}

type renderPermitStyleEpochIntent struct {
	Version                   string `json:"version"`
	CandidateProtocol         string `json:"candidate_protocol"`
	StyleContractProtocol     string `json:"style_contract_protocol"`
	CandidateID               string `json:"candidate_id"`
	GenerationID              string `json:"generation_id"`
	Chapter                   int    `json:"chapter"`
	PlanDigest                string `json:"plan_digest"`
	PlanCheckpointSeq         int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest     string `json:"projected_bundle_digest"`
	PromotionReceiptDigest    string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest string `json:"pipeline_render_input_digest"`
	RenderContextSHA256       string `json:"render_context_sha256"`
	IntentDigest              string `json:"intent_digest"`
}

type renderPermitEffectiveStyleSourceBody = stylestat.SerialMemorySourceBody

type renderPermitEffectiveStyleContractReceipt struct {
	Version                   string                                 `json:"version"`
	GenerationID              string                                 `json:"generation_id"`
	Chapter                   int                                    `json:"chapter"`
	PlanDigest                string                                 `json:"plan_digest"`
	PlanCheckpointSeq         int64                                  `json:"plan_checkpoint_seq"`
	BaseRenderContextSHA256   string                                 `json:"base_render_context_sha256"`
	PipelineRenderInputDigest string                                 `json:"pipeline_render_input_digest"`
	ProjectedBundleDigest     string                                 `json:"projected_bundle_digest"`
	PromotionReceiptDigest    string                                 `json:"promotion_receipt_digest"`
	CandidateID               string                                 `json:"candidate_id"`
	StyleID                   string                                 `json:"style_id"`
	StyleAssetSHA256          string                                 `json:"style_asset_sha256"`
	StyleContractProtocol     string                                 `json:"style_contract_protocol"`
	StyleContract             json.RawMessage                        `json:"style_contract"`
	StyleContractSHA256       string                                 `json:"style_contract_sha256"`
	SerialMemoryCompletedSet  []int                                  `json:"serial_memory_completed_chapters"`
	SourceChapterBodies       []renderPermitEffectiveStyleSourceBody `json:"source_chapter_bodies"`
	SerialMemoryStopwords     []string                               `json:"serial_memory_stopwords"`
	SerialMemoryCompiler      string                                 `json:"serial_memory_compiler_protocol"`
	SerialMemoryCompilerRoot  string                                 `json:"serial_memory_compiler_root_sha256"`
	CreatedAt                 string                                 `json:"created_at"`
	ReceiptDigest             string                                 `json:"receipt_digest"`
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
	if err := s.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return err
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			if err := s.validatePipelineRenderCandidateEvidenceTreeUnlocked(); err != nil {
				return err
			}
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
				Version:                     pipelineRenderProsePermitVersion,
				AuthorizationDigest:         authorization,
				ReservationAttempt:          attempt,
				CandidateID:                 manifest.CandidateID,
				SourceOutputDir:             manifest.SourceOutputDir,
				GenerationID:                manifest.GenerationID,
				Chapter:                     manifest.Chapter,
				PlanDigest:                  manifest.PlanDigest,
				PlanCheckpointSeq:           manifest.PlanCheckpointSeq,
				ProjectedBundleDigest:       manifest.ProjectedBundleDigest,
				PromotionReceiptDigest:      manifest.PromotionReceiptDigest,
				PipelineRenderInputDigest:   manifest.PipelineRenderInputDigest,
				RenderContextSHA256:         manifest.RenderContextSHA256,
				EffectiveStyleReceiptDigest: manifest.EffectiveStyleReceiptDigest,
				ExecutionOwner:              lock.Owner,
				ExecutionProcessID:          lock.ProcessID,
				ExecutionAcquiredAt:         lock.AcquiredAt,
				ExecutionExpiresAt:          lock.ExpiresAt,
				ArmedAt:                     now,
			}
			if err := s.io.WriteJSONUnlocked(pipelineRenderProsePermitPath, permit); err != nil {
				return fmt.Errorf("persist render prose permit: %w", err)
			}
			if err := syncRenderPermitDirectory(s.io.path("meta/runtime")); err != nil {
				return fmt.Errorf("sync render prose permit directory: %w", err)
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
	if err := s.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return err
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()

	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			if err := s.validatePipelineRenderCandidateEvidenceTreeUnlocked(); err != nil {
				return err
			}
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
				_ = syncRenderPermitDirectory(s.io.path("meta/runtime"))
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
			if err := syncRenderPermitDirectory(s.io.path("meta/runtime")); err != nil {
				return fmt.Errorf("sync consumed render prose permit directory: %w", err)
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
	if err := s.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return err
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()
	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			if err := s.validatePipelineRenderCandidateEvidenceTreeUnlocked(); err != nil {
				return err
			}
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
	if err := s.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return err
	}
	pipelineExecutionProcessMu.Lock()
	defer pipelineExecutionProcessMu.Unlock()
	return s.withPipelineExecutionTransaction(func() error {
		return s.io.WithWriteLock(func() error {
			if err := s.validatePipelineRenderCandidateEvidenceTreeUnlocked(); err != nil {
				return err
			}
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
			if err := syncRenderPermitDirectory(s.io.path("meta/runtime")); err != nil {
				return fmt.Errorf("sync cleared render prose permit directory: %w", err)
			}
			return nil
		})
	})
}

// ValidatePipelineRenderCandidateEvidenceTree rejects filesystem indirection
// before any candidate bytes can be assembled into a provider request. Render
// candidates are immutable, isolated snapshots; symlinks and special files
// have no legitimate role in their evidence tree and could otherwise redirect
// frozen context reads or provider-bound runtime writes outside the candidate.
// Non-candidate stores are left unchanged.
func (s *RuntimeStore) ValidatePipelineRenderCandidateEvidenceTree() error {
	if s == nil || s.io == nil {
		return fmt.Errorf("render candidate evidence store is nil")
	}
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.validatePipelineRenderCandidateEvidenceTreeUnlocked()
}

func (s *RuntimeStore) validatePipelineRenderCandidateEvidenceTreeUnlocked() error {
	root := strings.TrimSpace(s.io.dir)
	if filepath.Base(root) != "output" || filepath.Base(filepath.Dir(filepath.Dir(root))) != ".render-candidates" {
		return nil
	}
	if root == "" || !filepath.IsAbs(root) || root != filepath.Clean(root) {
		return fmt.Errorf("render candidate evidence root must be a clean absolute path")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect render candidate evidence root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("render candidate evidence root must be a real directory")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve render candidate evidence root: %w", err)
	}
	for _, rel := range []string{"meta", "meta/planning", "meta/runtime", "chapters"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect render candidate evidence directory %s: %w", rel, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("render candidate evidence directory %s must be real", rel)
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return fmt.Errorf("resolve render candidate evidence directory %s: %w", rel, resolveErr)
		}
		if filepath.Clean(resolved) != filepath.Join(filepath.Clean(resolvedRoot), filepath.FromSlash(rel)) {
			return fmt.Errorf("render candidate evidence directory %s escapes the candidate root", rel)
		}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("render candidate evidence contains symlink %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("render candidate evidence contains special file %s", path)
		}
		return nil
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
	if (manifest.Version != renderPermitCandidateManifestVersionV2 &&
		manifest.Version != renderPermitCandidateManifestVersionV3EffectiveStyle) ||
		manifest.CandidateID == "" ||
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
	if manifest.Version == renderPermitCandidateManifestVersionV3EffectiveStyle {
		if !validRenderPermitEffectiveStyleDigest(manifest.PipelineRenderInputDigest) ||
			!validRenderPermitEffectiveStyleDigest(manifest.RenderContextSHA256) ||
			!validRenderPermitEffectiveStyleDigest(manifest.EffectiveStyleReceiptDigest) {
			return manifest, "", fmt.Errorf("render candidate v3 effective-style identity is incomplete or malformed")
		}
		if err := requireRenderPermitV3StyleEpochIntent(
			namespace, resolvedNamespace, manifest,
		); err != nil {
			return manifest, "", err
		}
		if err := s.validateRenderPermitEffectiveStyleContractUnlocked(manifest); err != nil {
			return manifest, "", err
		}
	} else if strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
		strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
		strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
		return manifest, "", fmt.Errorf("render candidate v2 contains v3 effective-style identity")
	} else if err := rejectRenderPermitLegacyCandidateWithV3EpochIntent(
		namespace, resolvedNamespace, manifest.CandidateID,
	); err != nil {
		return manifest, "", err
	}
	ledgerPath := filepath.Join(
		convergenceDir, "dispatch_budget.json",
	)
	if err := validateRenderPermitLedgerFiles(ledgerPath); err != nil {
		return manifest, "", err
	}
	return manifest, ledgerPath, nil
}

func requireRenderPermitV3StyleEpochIntent(
	namespace string,
	resolvedNamespace string,
	manifest renderPermitCandidateManifest,
) error {
	path, exists, err := renderPermitStyleEpochIntentFile(
		namespace, resolvedNamespace, manifest.CandidateID,
	)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("v3 render prose permit requires its immutable style epoch intent")
	}
	expected := renderPermitStyleEpochIntent{
		Version:                   renderPermitStyleEpochIntentVersion,
		CandidateProtocol:         renderPermitCandidateManifestVersionV3EffectiveStyle,
		StyleContractProtocol:     renderPermitStyleContractProtocolVersion,
		CandidateID:               manifest.CandidateID,
		GenerationID:              manifest.GenerationID,
		Chapter:                   manifest.Chapter,
		PlanDigest:                manifest.PlanDigest,
		PlanCheckpointSeq:         manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:     manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:    manifest.PromotionReceiptDigest,
		PipelineRenderInputDigest: manifest.PipelineRenderInputDigest,
		RenderContextSHA256:       manifest.RenderContextSHA256,
	}
	digest, err := renderPermitStyleEpochIntentDigest(expected)
	if err != nil {
		return fmt.Errorf("digest expected render style epoch intent for prose permit: %w", err)
	}
	expected.IntentDigest = digest
	expectedRaw, err := renderPermitStyleEpochIntentBytes(expected)
	if err != nil {
		return fmt.Errorf("encode expected render style epoch intent for prose permit: %w", err)
	}
	raw, err := readRenderPermitStyleEpochIntentNoFollow(path)
	if err != nil {
		return fmt.Errorf("read render style epoch intent for prose permit: %w", err)
	}
	if !bytes.Equal(raw, expectedRaw) {
		return fmt.Errorf("v3 render style epoch intent is not canonical or does not match the sealed candidate identity")
	}
	return nil
}

func renderPermitStyleEpochIntentDigest(intent renderPermitStyleEpochIntent) (string, error) {
	intent.IntentDigest = ""
	digest, err := domain.DeterministicPlanningHash(intent)
	if err != nil {
		return "", err
	}
	return "sha256:" + strings.TrimPrefix(digest, "sha256:"), nil
}

func renderPermitStyleEpochIntentBytes(intent renderPermitStyleEpochIntent) ([]byte, error) {
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func renderPermitStyleEpochIntentFile(
	namespace string,
	resolvedNamespace string,
	candidateID string,
) (string, bool, error) {
	epochDir := filepath.Join(namespace, renderPermitStyleEpochIntentDir)
	info, err := os.Lstat(epochDir)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect render style epoch directory for prose permit: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("render style epoch directory for prose permit must be a real directory")
	}
	resolvedEpochDir, err := filepath.EvalSymlinks(epochDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve render style epoch directory for prose permit: %w", err)
	}
	if filepath.Clean(resolvedEpochDir) != filepath.Join(resolvedNamespace, renderPermitStyleEpochIntentDir) {
		return "", false, fmt.Errorf("render style epoch directory escapes prose permit namespace")
	}
	epochPath := filepath.Join(epochDir, candidateID+".json")
	info, err = os.Lstat(epochPath)
	if os.IsNotExist(err) {
		return epochPath, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect render style epoch intent for prose permit: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("render style epoch intent for prose permit must be a real file")
	}
	resolvedEpochPath, err := filepath.EvalSymlinks(epochPath)
	if err != nil {
		return "", false, fmt.Errorf("resolve render style epoch intent for prose permit: %w", err)
	}
	if filepath.Clean(resolvedEpochPath) != filepath.Join(
		resolvedNamespace, renderPermitStyleEpochIntentDir, candidateID+".json",
	) {
		return "", false, fmt.Errorf("render style epoch intent escapes prose permit namespace")
	}
	return epochPath, true, nil
}

func readRenderPermitStyleEpochIntentNoFollow(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// rejectRenderPermitLegacyCandidateWithV3EpochIntent is the provider-side
// downgrade barrier. Mere presence of the immutable CandidateID marker is
// sufficient to forbid relabeling that candidate as legacy; v3 permits apply
// the stronger canonical-byte and sealed-identity validation above.
func rejectRenderPermitLegacyCandidateWithV3EpochIntent(
	namespace string,
	resolvedNamespace string,
	candidateID string,
) error {
	_, exists, err := renderPermitStyleEpochIntentFile(namespace, resolvedNamespace, candidateID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return fmt.Errorf("immutable v3 render style epoch intent forbids a legacy v2 prose permit")
}

func (s *RuntimeStore) validateRenderPermitEffectiveStyleContractUnlocked(manifest renderPermitCandidateManifest) error {
	contractPath := s.io.path(renderPermitEffectiveStyleContractPath)
	info, err := os.Lstat(contractPath)
	if err != nil {
		return fmt.Errorf("inspect effective render style contract for prose permit: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("effective render style contract for prose permit must be a real file")
	}
	var receipt renderPermitEffectiveStyleContractReceipt
	if err := s.io.ReadJSONUnlocked(renderPermitEffectiveStyleContractPath, &receipt); err != nil {
		return fmt.Errorf("load effective render style contract for prose permit: %w", err)
	}
	receiptDigest, err := renderPermitEffectiveStyleReceiptDigest(receipt)
	if err != nil {
		return fmt.Errorf("digest effective render style contract for prose permit: %w", err)
	}
	if receipt.Version != renderPermitEffectiveStyleContractVersion ||
		strings.TrimSpace(receipt.GenerationID) == "" || receipt.Chapter <= 0 ||
		receipt.PlanCheckpointSeq <= 0 || strings.TrimSpace(receipt.CandidateID) == "" ||
		!validRenderPermitEffectiveStyleDigest(receipt.PlanDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.BaseRenderContextSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.PipelineRenderInputDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.ProjectedBundleDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.PromotionReceiptDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.StyleAssetSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.StyleContractSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.SerialMemoryCompilerRoot) ||
		!validRenderPermitEffectiveStyleDigest(receipt.ReceiptDigest) ||
		receipt.StyleContractProtocol != renderPermitStyleContractProtocolVersion ||
		receipt.SerialMemoryCompiler != stylestat.SerialMemoryCompilerProtocolVersion ||
		receipt.SerialMemoryCompletedSet == nil || receipt.SourceChapterBodies == nil ||
		receipt.SerialMemoryStopwords == nil ||
		len(receipt.StyleContract) == 0 ||
		receipt.StyleContractSHA256 != renderPermitEffectiveStyleSHA256(receipt.StyleContract) ||
		receipt.ReceiptDigest != receiptDigest {
		return fmt.Errorf("effective render style contract identity or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt); err != nil {
		return fmt.Errorf("effective render style contract timestamp is invalid: %w", err)
	}
	if receipt.GenerationID != manifest.GenerationID || receipt.Chapter != manifest.Chapter ||
		receipt.PlanDigest != manifest.PlanDigest || receipt.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		receipt.BaseRenderContextSHA256 != manifest.RenderContextSHA256 ||
		receipt.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		receipt.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		receipt.CandidateID != manifest.CandidateID ||
		receipt.ReceiptDigest != manifest.EffectiveStyleReceiptDigest {
		return fmt.Errorf("effective render style contract does not match render candidate identity")
	}
	canonicalCompleted, err := stylestat.CanonicalCompletedChapters(receipt.SerialMemoryCompletedSet)
	if err != nil || !slices.Equal(canonicalCompleted, receipt.SerialMemoryCompletedSet) {
		return fmt.Errorf("effective render style completed chapter set is invalid")
	}
	if !slices.Equal(stylestat.CanonicalStopwords(receipt.SerialMemoryStopwords), receipt.SerialMemoryStopwords) {
		return fmt.Errorf("effective render style serial-memory stopwords are not canonical")
	}
	expectedSourceCount := 0
	for _, chapter := range receipt.SerialMemoryCompletedSet {
		if chapter < receipt.Chapter {
			expectedSourceCount++
		}
	}
	if len(receipt.SourceChapterBodies) != expectedSourceCount {
		return fmt.Errorf("effective render style source bodies do not cover the completed chapter set")
	}
	previousChapter := 0
	for index, source := range receipt.SourceChapterBodies {
		if source.Chapter <= previousChapter || source.Chapter >= receipt.Chapter ||
			!validRenderPermitEffectiveStyleDigest(source.BodySHA256) ||
			index >= len(canonicalCompleted) || source.Chapter != canonicalCompleted[index] {
			return fmt.Errorf("effective render style source-body identity is invalid")
		}
		body, err := s.io.ReadFileUnlocked(fmt.Sprintf("chapters/%02d.md", source.Chapter))
		if os.IsNotExist(err) {
			body = nil
		} else if err != nil {
			return fmt.Errorf("load effective render style source chapter %d: %w", source.Chapter, err)
		}
		if renderPermitEffectiveStyleSHA256(body) != source.BodySHA256 {
			return fmt.Errorf("effective render style source chapter %d drift", source.Chapter)
		}
		previousChapter = source.Chapter
	}
	if receipt.SerialMemoryCompilerRoot != stylestat.SerialMemoryCompilerRoot(
		receipt.SerialMemoryCompletedSet,
		receipt.SourceChapterBodies,
		receipt.SerialMemoryStopwords,
	) {
		return fmt.Errorf("effective render style serial-memory compiler root is invalid")
	}
	projectStore := NewStore(s.io.dir)
	progress, err := projectStore.Progress.Load()
	if err != nil {
		return fmt.Errorf("load completed chapter set for effective render style: %w", err)
	}
	var currentCompletedRaw []int
	if progress != nil {
		currentCompletedRaw = progress.CompletedChapters
	}
	currentCompleted, err := stylestat.CanonicalCompletedChapters(currentCompletedRaw)
	if err != nil {
		return fmt.Errorf("canonicalize completed chapter set for effective render style: %w", err)
	}
	if !slices.Equal(currentCompleted, receipt.SerialMemoryCompletedSet) {
		return fmt.Errorf("effective render style completed chapter set drift")
	}
	currentStopwords, err := projectStore.CanonicalSerialStyleMemoryStopwords()
	if err != nil {
		return fmt.Errorf("load effective render style serial-memory stopwords: %w", err)
	}
	if !slices.Equal(currentStopwords, receipt.SerialMemoryStopwords) {
		return fmt.Errorf("effective render style serial-memory stopword drift")
	}
	if stylestat.SerialMemoryCompilerRoot(currentCompleted, receipt.SourceChapterBodies, currentStopwords) !=
		receipt.SerialMemoryCompilerRoot {
		return fmt.Errorf("effective render style serial-memory compiler drift")
	}
	return nil
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
		permit.SourceOutputDir != manifest.SourceOutputDir ||
		permit.GenerationID != manifest.GenerationID ||
		permit.Chapter != manifest.Chapter ||
		permit.PlanDigest != manifest.PlanDigest ||
		permit.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		permit.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		permit.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		permit.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		permit.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		permit.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest ||
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
		ledger.SourceOutputDir != manifest.SourceOutputDir ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter || ledger.Chapter != lock.TargetChapter ||
		ledger.PlanDigest != manifest.PlanDigest || ledger.PlanDigest != lock.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		ledger.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		ledger.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		ledger.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest {
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
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncRenderPermitDirectory(filepath.Dir(path))
}

func syncRenderPermitDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && syncErr != syscall.EINVAL && syncErr != syscall.ENOTSUP {
		return syncErr
	}
	return closeErr
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

func validRenderPermitEffectiveStyleDigest(value string) bool {
	if !validRenderPermitDigest(value) {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validateArchivedRenderPermitEffectiveStyleReceipt(
	raw []byte,
	chapter int,
	expectedReceiptDigest string,
	expectedPath string,
) (*renderPermitEffectiveStyleContractReceipt, error) {
	var receipt renderPermitEffectiveStyleContractReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, fmt.Errorf("decode archived effective render style receipt: %w", err)
	}
	receiptDigest, err := renderPermitEffectiveStyleReceiptDigest(receipt)
	if err != nil {
		return nil, err
	}
	if receipt.Version != renderPermitEffectiveStyleContractVersion ||
		receipt.Chapter != chapter || receipt.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(receipt.GenerationID) == "" ||
		strings.TrimSpace(receipt.StyleID) == "" ||
		strings.TrimSpace(receipt.CandidateID) == "" ||
		filepath.Base(receipt.CandidateID) != receipt.CandidateID ||
		strings.ContainsAny(receipt.CandidateID, `/\\`) ||
		!validRenderPermitEffectiveStyleDigest(receipt.PlanDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.BaseRenderContextSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.PipelineRenderInputDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.ProjectedBundleDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.PromotionReceiptDigest) ||
		!validRenderPermitEffectiveStyleDigest(receipt.StyleAssetSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.StyleContractSHA256) ||
		!validRenderPermitEffectiveStyleDigest(receipt.SerialMemoryCompilerRoot) ||
		!validRenderPermitEffectiveStyleDigest(receipt.ReceiptDigest) ||
		receipt.ReceiptDigest != expectedReceiptDigest || receipt.ReceiptDigest != receiptDigest ||
		receipt.StyleContractProtocol != renderPermitStyleContractProtocolVersion ||
		receipt.SerialMemoryCompiler != stylestat.SerialMemoryCompilerProtocolVersion ||
		receipt.SerialMemoryCompletedSet == nil || receipt.SourceChapterBodies == nil ||
		receipt.SerialMemoryStopwords == nil || len(receipt.StyleContract) == 0 ||
		receipt.StyleContractSHA256 != renderPermitEffectiveStyleSHA256(receipt.StyleContract) {
		return nil, fmt.Errorf("archived effective render style receipt identity or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt); err != nil {
		return nil, fmt.Errorf("archived effective render style receipt timestamp is invalid: %w", err)
	}
	wantPath := filepath.ToSlash(filepath.Join(
		"meta/planning/effective_render_style_contracts",
		fmt.Sprintf("ch%04d", receipt.Chapter),
		receipt.CandidateID,
		strings.TrimPrefix(receipt.ReceiptDigest, "sha256:")+".json",
	))
	if expectedPath != wantPath {
		return nil, fmt.Errorf("archived effective render style receipt path identity mismatch")
	}
	completed, err := stylestat.CanonicalCompletedChapters(receipt.SerialMemoryCompletedSet)
	if err != nil || !slices.Equal(completed, receipt.SerialMemoryCompletedSet) ||
		!slices.Equal(stylestat.CanonicalStopwords(receipt.SerialMemoryStopwords), receipt.SerialMemoryStopwords) {
		return nil, fmt.Errorf("archived effective render style serial-memory inputs are not canonical")
	}
	expectedSources := 0
	for _, completedChapter := range completed {
		if completedChapter < receipt.Chapter {
			expectedSources++
		}
	}
	if len(receipt.SourceChapterBodies) != expectedSources {
		return nil, fmt.Errorf("archived effective render style source body set is incomplete")
	}
	for i, source := range receipt.SourceChapterBodies {
		if i >= len(completed) || source.Chapter != completed[i] || source.Chapter >= receipt.Chapter ||
			!validRenderPermitEffectiveStyleDigest(source.BodySHA256) {
			return nil, fmt.Errorf("archived effective render style source body identity is invalid")
		}
	}
	if receipt.SerialMemoryCompilerRoot != stylestat.SerialMemoryCompilerRoot(
		receipt.SerialMemoryCompletedSet,
		receipt.SourceChapterBodies,
		receipt.SerialMemoryStopwords,
	) {
		return nil, fmt.Errorf("archived effective render style compiler root is invalid")
	}
	return &receipt, nil
}

func renderPermitEffectiveStyleReceiptDigest(receipt renderPermitEffectiveStyleContractReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return renderPermitEffectiveStyleSHA256(raw), nil
}

func renderPermitEffectiveStyleSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
