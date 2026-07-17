package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// requirePublishedOutlineAllIfPresent is a test seam for validation performed
// while the caller already owns the run-root exclusive control lease.
var requirePublishedOutlineAllIfPresent = requirePublishedOutlineAllWithControlHeld

func outlineAllOutputDirForInvocation(opts cliOptions) (string, bool, error) {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return "", false, nil
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return "", false, fmt.Errorf("outline-all published gate load config: %w", err)
	}
	if err := normalizeOutputAndRAGForInvocation(
		&cfg,
		opts.Dir,
		hasConfiguredRAGQdrantCollection(opts),
	); err != nil {
		return "", false, err
	}
	liveDir, err := normalizePublishedOutlineAllOutputDir(cfg.OutputDir)
	if err != nil {
		return "", false, err
	}
	return liveDir, true, nil
}

func normalizePublishedOutlineAllOutputDir(outputDir string) (string, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return "", fmt.Errorf("outline-all published gate requires output directory")
	}
	liveDir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("outline-all published gate resolve output directory: %w", err)
	}
	return filepath.Clean(liveDir), nil
}

func acquirePublishedOutlineAllStageAtOutput(outputDir string) (string, func() error, error) {
	liveDir, err := normalizePublishedOutlineAllOutputDir(outputDir)
	if err != nil {
		return "", nil, err
	}
	release, err := acquirePipelineOutlineAllControl(liveDir, true)
	if err != nil {
		return "", nil, fmt.Errorf("outline-all published gate acquire run-root exclusive control: %w", err)
	}
	if err := recoverAllDirectoryPublishesWithControlHeld(liveDir); err != nil {
		_ = release()
		return "", nil, err
	}
	if err := requirePublishedOutlineAllIfPresent(liveDir); err != nil {
		if releaseErr := release(); releaseErr != nil {
			return "", nil, fmt.Errorf("%w; release run-root exclusive control: %v", err, releaseErr)
		}
		return "", nil, err
	}
	if receipt, loadErr := store.NewStore(liveDir).LoadOutlineAllExecutionReceipt(); loadErr != nil {
		_ = release()
		return "", nil, loadErr
	} else if receipt != nil {
		if _, verifyErr := verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(liveDir); verifyErr != nil {
			_ = release()
			return "", nil, verifyErr
		}
	}
	return liveDir, release, nil
}

func acquirePublishedOutlineAllStageForInvocation(opts cliOptions) (string, func() error, error) {
	liveDir, configured, err := outlineAllOutputDirForInvocation(opts)
	if err != nil {
		return "", nil, err
	}
	if !configured {
		return "", func() error { return nil }, nil
	}
	return acquirePublishedOutlineAllStageAtOutput(liveDir)
}

func releasePublishedOutlineAllStage(
	release func() error,
	stage string,
	returnErr *error,
) {
	if release == nil {
		return
	}
	if err := release(); err != nil && returnErr != nil && *returnErr == nil {
		*returnErr = fmt.Errorf("%s release outline-all run-root exclusive control: %w", stage, err)
	}
}

// RequirePublishedOutlineAllIfPresent preserves compatibility only for projects
// that have never entered outline-all: no live requirement marker, no live
// receipt, and no active/in-flight outline-all work. Once the persistent
// requirement is present, every downstream planning or writing stage must
// consume only a fully published live result. A candidate-local status=complete
// receipt is not a publication capability.
func RequirePublishedOutlineAllIfPresent(outputDir string) error {
	_, release, err := acquirePublishedOutlineAllStageAtOutput(outputDir)
	if err != nil {
		return err
	}
	return release()
}

func requirePublishedOutlineAllWithControlHeld(liveDir string) error {
	st := store.NewStore(liveDir)
	required, err := loadPipelineOutlineAllRequirement(liveDir)
	if err != nil {
		return fmt.Errorf("outline-all published gate load persistent requirement: %w", err)
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return fmt.Errorf("outline-all published gate load execution receipt: %w", err)
	}
	if receipt == nil {
		if required {
			return fmt.Errorf("outline-all is persistently required but no published live receipt exists; downstream stages are blocked")
		}
		return requireNoUnpublishedOutlineAllWork(liveDir, st)
	}
	if receipt.Status != domain.OutlineAllExecutionComplete {
		return fmt.Errorf(
			"outline-all execution exists but is not complete (status=%s); downstream stages are blocked",
			receipt.Status,
		)
	}

	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("outline-all published gate load layered outline: %w", err)
	}
	layeredDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		return fmt.Errorf("outline-all published gate digest layered outline: %w", err)
	}
	if layeredDigest != receipt.FinalLayeredDigest {
		return fmt.Errorf("outline-all published layered outline digest drift")
	}
	flat, err := st.Outline.LoadOutline()
	if err != nil {
		return fmt.Errorf("outline-all published gate load flat outline: %w", err)
	}
	flatDigest, err := domain.ComputeFlatOutlineDigest(flat)
	if err != nil {
		return fmt.Errorf("outline-all published gate digest flat outline: %w", err)
	}
	if flatDigest != receipt.FinalFlatDigest {
		return fmt.Errorf("outline-all published flat outline digest drift")
	}
	if domain.TotalChapters(volumes) != receipt.TargetChapters || len(flat) != receipt.TargetChapters {
		return fmt.Errorf(
			"outline-all published outline count drift: layered=%d flat=%d target=%d",
			domain.TotalChapters(volumes),
			len(flat),
			receipt.TargetChapters,
		)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("outline-all published gate load progress: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("outline-all published gate requires progress bound to the published receipt")
	}
	if strings.TrimSpace(receipt.GenerationID) == "" || progress.GenerationID != receipt.GenerationID {
		return fmt.Errorf(
			"outline-all published progress generation_id=%q does not match execution receipt generation_id=%q",
			progress.GenerationID,
			receipt.GenerationID,
		)
	}
	if progress.TotalChapters != receipt.TargetChapters {
		return fmt.Errorf(
			"outline-all published progress total_chapters=%d does not match target=%d",
			progress.TotalChapters,
			receipt.TargetChapters,
		)
	}

	if strings.TrimSpace(receipt.PublishedCandidateRoot) == "" ||
		strings.TrimSpace(receipt.DirectoryPublishReceiptDigest) == "" ||
		strings.TrimSpace(receipt.ExpectedLiveDirectoryRoot) == "" {
		return fmt.Errorf("outline-all complete receipt is not bound to a published directory transaction")
	}
	if !strings.HasPrefix(receipt.AttemptID, "oa-") {
		return fmt.Errorf("outline-all published attempt_id=%q is outside the outline-all namespace", receipt.AttemptID)
	}
	expectedAttemptID, err := pipelineOutlineAllAttemptIDFromReceipt(st, receipt)
	if err != nil || expectedAttemptID != receipt.AttemptID {
		return fmt.Errorf("outline-all published attempt identity cannot be replayed: got=%s want=%s err=%v",
			receipt.AttemptID, expectedAttemptID, err)
	}
	expectedCandidate, err := filepath.Abs(pipelineOutlineAllCandidatePath(liveDir, receipt.AttemptID))
	if err != nil {
		return fmt.Errorf("outline-all published gate resolve candidate directory: %w", err)
	}
	if filepath.Clean(receipt.CandidateDir) != filepath.Clean(expectedCandidate) {
		return fmt.Errorf("outline-all published candidate directory is outside the live attempt namespace")
	}

	publishRoot, err := filepath.Abs(pipelineOutlineAllPublishRoot(liveDir))
	if err != nil {
		return fmt.Errorf("outline-all published gate resolve transaction namespace: %w", err)
	}
	publisher := store.NewDirectoryPublishStore(filepath.Clean(publishRoot))
	state, err := publisher.LoadDirectoryPublishState(receipt.AttemptID)
	if err != nil {
		return fmt.Errorf("outline-all published gate load finalized transaction: %w", err)
	}
	if state == nil || state.Phase != store.DirectoryPublishFinalized || state.Receipt == nil {
		return fmt.Errorf("outline-all directory transaction %s is not finalized", receipt.AttemptID)
	}
	published := state.Receipt
	expectedArchive := filepath.Join(filepath.Clean(publishRoot), receipt.AttemptID, "archive", "live")
	if state.TransactionID != receipt.AttemptID ||
		published.TransactionID != receipt.AttemptID ||
		filepath.Clean(published.LiveDir) != liveDir ||
		filepath.Clean(published.CandidateDir) != filepath.Clean(expectedCandidate) ||
		filepath.Clean(published.ArchiveDir) != expectedArchive {
		return fmt.Errorf("outline-all finalized transaction is outside the expected live namespace")
	}
	if published.BeforeLiveRoot != receipt.ExpectedLiveDirectoryRoot ||
		published.CandidateRoot != receipt.PublishedCandidateRoot ||
		published.CommittedLiveRoot != receipt.PublishedCandidateRoot ||
		published.ReceiptDigest != receipt.DirectoryPublishReceiptDigest {
		return fmt.Errorf("outline-all finalized transaction roots or receipt digest do not match execution receipt")
	}
	return nil
}

func requirePublishedOutlineAllChapterZeroProgressWithControlHeld(outputDir string) error {
	liveDir, err := normalizePublishedOutlineAllOutputDir(outputDir)
	if err != nil {
		return err
	}
	st := store.NewStore(liveDir)
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return fmt.Errorf("outline-all chapter-zero gate load execution receipt: %w", err)
	}
	if receipt == nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("outline-all chapter-zero gate load progress: %w", err)
	}
	if err := domain.ValidateOutlineAllChapterZeroProgress(progress, *receipt); err != nil {
		return err
	}
	if progress.TotalChapters != receipt.TargetChapters {
		return fmt.Errorf(
			"outline-all chapter-zero progress total_chapters=%d does not match target=%d",
			progress.TotalChapters,
			receipt.TargetChapters,
		)
	}
	return nil
}

// requireNoUnpublishedOutlineAllWork distinguishes a genuinely legacy project
// from the live side of an in-flight outline-all attempt. The execution receipt
// is created in the isolated candidate, so checking only the live receipt would
// leave a race in which zero-init or preplan can mutate live foundation while
// Architect is still building the candidate.
func requireNoUnpublishedOutlineAllWork(liveDir string, st *store.Store) error {
	publishRoot := pipelineOutlineAllPublishRoot(liveDir)
	if entries, err := os.ReadDir(publishRoot); err == nil {
		publisher := store.NewDirectoryPublishStore(publishRoot)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			state, loadErr := publisher.LoadDirectoryPublishState(entry.Name())
			if loadErr != nil {
				return fmt.Errorf("outline-all unpublished gate inspect transaction %s: %w", entry.Name(), loadErr)
			}
			if state != nil && state.Phase != store.DirectoryPublishFinalized && state.Phase != store.DirectoryPublishAborted {
				return fmt.Errorf("outline-all directory transaction %s is still %s; downstream stages are blocked", entry.Name(), state.Phase)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("outline-all unpublished gate list publish namespace: %w", err)
	}

	candidateRoot := filepath.Join(pipelineOutlineAllRunRoot(liveDir), pipelineOutlineAllRunDirName)
	if entries, err := os.ReadDir(candidateRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "oa-") {
				continue
			}
			candidateDir := pipelineOutlineAllCandidatePath(liveDir, entry.Name())
			info, statErr := os.Lstat(candidateDir)
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return fmt.Errorf("outline-all unpublished gate inspect candidate %s: %w", entry.Name(), statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("outline-all candidate %s is not a regular directory", entry.Name())
			}
			candidateReceipt, loadErr := store.NewStore(candidateDir).LoadOutlineAllExecutionReceipt()
			if loadErr != nil {
				return fmt.Errorf("outline-all unpublished gate load candidate %s: %w", entry.Name(), loadErr)
			}
			if candidateReceipt != nil {
				return fmt.Errorf(
					"outline-all candidate %s is %s but is not published to live; downstream stages are blocked",
					entry.Name(),
					candidateReceipt.Status,
				)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("outline-all unpublished gate list candidate namespace: %w", err)
	}

	if _, err := os.Stat(liveDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("outline-all unpublished gate inspect live directory: %w", err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("outline-all unpublished gate load live execution lock: %w", err)
	}
	if lock != nil && lock.Mode == domain.PipelineExecutionOutlineAll {
		return fmt.Errorf("outline-all live execution lock is active for %s; downstream stages are blocked", lock.Owner)
	}
	return nil
}
