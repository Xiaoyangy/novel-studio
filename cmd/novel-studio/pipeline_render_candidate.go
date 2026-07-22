package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineRenderCandidateManifestVersion         = "pipeline-render-candidate.v3-effective-style"
	pipelineRenderCandidatePreStyleManifestVersion = "pipeline-render-candidate.v3-pre-style"
	pipelineRenderCandidatePreviousManifestVersion = "pipeline-render-candidate.v2"
	pipelineRenderCandidateLegacyManifestVersion   = "pipeline-render-candidate.v1"
	pipelineRenderRejectionTombstoneVersion        = "pipeline-render-semantic-rejection.v1"
)

type pipelineRenderCandidate struct {
	ID              string
	ContainerDir    string
	OutputDir       string
	TransactionRoot string
	SourceLiveRoot  string
	// RecoveredChapterTransactionPhase is set only after the live-root sibling
	// transaction store and every corresponding candidate artifact have both
	// validated. committed skips Writer; formal_accepted/actual_matched also skip
	// formal reviewers.
	RecoveredChapterTransactionPhase domain.ChapterRenderPhase
	// RecoveredDurableCommit closes the tiny crash window between the commit
	// saga's checkpoint and the sibling transaction receipt. The full candidate
	// tree has already been revalidated, so Writer must not run again.
	RecoveredDurableCommit bool
	// RecoveredAcceptedActualMismatch is set only when a retired candidate
	// already has an exact-body formal accept and the previous failure happened
	// after review, inside the sealed actual-delta matcher.  The render runner
	// uses this narrow marker to rerun that deterministic matcher without
	// dispatching Writer or either reviewer again.
	RecoveredAcceptedActualMismatch bool
}

type pipelineRenderCandidateManifest struct {
	Version                     string `json:"version"`
	CandidateID                 string `json:"candidate_id"`
	GenerationID                string `json:"generation_id"`
	Chapter                     int    `json:"chapter"`
	PlanDigest                  string `json:"plan_digest"`
	PlanCheckpointSeq           int64  `json:"plan_checkpoint_seq,omitempty"`
	ProjectedBundleDigest       string `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string `json:"pipeline_render_input_digest,omitempty"`
	RenderContextSHA256         string `json:"render_context_sha256,omitempty"`
	EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest,omitempty"`
	SourceOutputDir             string `json:"source_output_dir"`
	SourceLiveRoot              string `json:"source_live_root"`
	PreparedAt                  string `json:"prepared_at"`
}

// pipelineRenderRejectionTombstone is an immutable, exact-body negative
// result for one sealed render transaction. It is deliberately stored outside
// the candidate tree so a later stale operational snapshot cannot resurrect
// bytes that already completed a fresh review and received a semantic reject.
// Historical rejected directories are not evidence for this protocol: only a
// validated versioned tombstone written by the current path has authority.
type pipelineRenderRejectionTombstone struct {
	Version                string   `json:"version"`
	CandidateID            string   `json:"candidate_id"`
	GenerationID           string   `json:"generation_id"`
	Chapter                int      `json:"chapter"`
	PlanDigest             string   `json:"plan_digest"`
	PlanCheckpointSeq      int64    `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest  string   `json:"projected_bundle_digest"`
	PromotionReceiptDigest string   `json:"promotion_receipt_digest"`
	BodySHA256             string   `json:"body_sha256"`
	Verdict                string   `json:"verdict"`
	Disposition            string   `json:"disposition"`
	ReviewArtifacts        []string `json:"review_artifacts"`
	RejectedAt             string   `json:"rejected_at"`
}

type pipelineRenderedChapterSnapshot struct {
	Store           *store.Store
	Commit          *domain.Checkpoint
	ChapterPath     string
	Body            string
	BodySHA256      string
	ActualCanonRoot string
}

var errPipelineSealedActualBodyEvidenceMismatch = errors.New("sealed actual body evidence mismatch")

type pipelineSealedActualBodyEvidenceMatchFunc func(
	*pipelineRenderedChapterSnapshot,
) (pipelineSealedActualDeltaMatch, error)

// runPipelineSealedActualBodyEvidencePreflight runs the same deterministic
// matcher used by the final sealed receipt before any formal model review is
// dispatched. A negative result is persisted beside the exact candidate body
// so the caller can stop without paying for DeepSeek/Editor. A positive result
// is deliberately not treated as the final receipt: formal review and the
// existing post-review matcher still run unchanged.
func runPipelineSealedActualBodyEvidencePreflight(
	outputDir string,
	snapshot *pipelineRenderedChapterSnapshot,
	matchBody pipelineSealedActualBodyEvidenceMatchFunc,
) error {
	if snapshot == nil || snapshot.Store == nil || strings.TrimSpace(snapshot.Body) == "" {
		return fmt.Errorf("sealed actual body-evidence preflight requires an exact rendered snapshot")
	}
	if strings.TrimSpace(outputDir) == "" || matchBody == nil {
		return fmt.Errorf("sealed actual body-evidence preflight requires output and matcher")
	}
	actualMatch, err := matchBody(snapshot)
	if err != nil {
		return fmt.Errorf("sealed actual body-evidence preflight matcher: %w", err)
	}
	if actualMatch.ProjectionMatch {
		if err := pipelineWatchdogProgressBody(
			pipelineWatchdogEventRenderPreflightPassed,
			snapshot.BodySHA256,
		); err != nil {
			return fmt.Errorf("record sealed actual body-evidence preflight progress: %w", err)
		}
		return nil
	}
	if err := savePipelineSealedActualMatch(outputDir, actualMatch); err != nil {
		return fmt.Errorf("%w: persist mismatch: %v", errPipelineSealedActualBodyEvidenceMismatch, err)
	}
	reasons := normalizePipelineSealedReasons(actualMatch.MismatchReasons)
	if len(reasons) == 0 {
		reasons = []string{"projection_match=false"}
	}
	return fmt.Errorf(
		"%w: %s",
		errPipelineSealedActualBodyEvidenceMismatch,
		strings.Join(reasons, "；"),
	)
}

func runPipelineSealedFormalReviewAfterBodyEvidence(
	outputDir string,
	snapshot *pipelineRenderedChapterSnapshot,
	matchBody pipelineSealedActualBodyEvidenceMatchFunc,
	formalReview func() error,
) error {
	if err := runPipelineSealedActualBodyEvidencePreflight(outputDir, snapshot, matchBody); err != nil {
		return err
	}
	if formalReview == nil {
		return fmt.Errorf("sealed formal review callback is nil")
	}
	if err := formalReview(); err != nil {
		return err
	}
	if err := pipelineWatchdogProgressBody(
		pipelineWatchdogEventRenderFormalReviewed,
		snapshot.BodySHA256,
	); err != nil {
		return fmt.Errorf("record sealed formal review progress: %w", err)
	}
	return nil
}

func pipelineRenderTransactionRoot(outputDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(outputDir)), ".render-publish")
}

// recoverPipelineRenderPublishesBeforeLoad runs before loadCfgBundle because
// that loader writes the prompt manifest under OutputDir. If a prior process
// crashed after live→archive, touching OutputDir first could recreate an empty
// live directory and make recovery ambiguous.
func recoverPipelineRenderPublishesBeforeLoad(opts cliOptions) error {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return nil
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("render recovery load config: %w", err)
	}
	if err := normalizeOutputAndRAGForInvocation(
		&cfg,
		opts.Dir,
		hasConfiguredRAGQdrantCollection(opts),
	); err != nil {
		return err
	}
	releaseControl, err := acquirePipelineOutlineAllControl(cfg.OutputDir, true)
	if err != nil {
		return err
	}
	defer func() { _ = releaseControl() }()
	return recoverAllDirectoryPublishesWithControlHeld(cfg.OutputDir)
}

func recoverPipelineRenderPublishesWithControlHeld(outputDir string) error {
	publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(outputDir))
	transactionRoot := pipelineRenderTransactionRoot(outputDir)
	entries, err := os.ReadDir(transactionRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("render recovery list directory publishes: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		state, stateErr := publisher.LoadDirectoryPublishState(id)
		if stateErr != nil {
			return fmt.Errorf("render recovery inspect transaction %s: %w", id, stateErr)
		}
		if state == nil || state.Phase == store.DirectoryPublishFinalized ||
			state.Phase == store.DirectoryPublishAborted {
			if state != nil && state.Receipt != nil &&
				filepath.Clean(state.Receipt.LiveDir) != filepath.Clean(outputDir) {
				return fmt.Errorf(
					"render recovery transaction %s targets unexpected live dir %s",
					id,
					state.Receipt.LiveDir,
				)
			}
			continue
		}
		if state.Intent == nil ||
			filepath.Clean(state.Intent.LiveDir) != filepath.Clean(outputDir) ||
			!pathContainsPipelineRenderCandidate(
				pipelineRenderCandidateRoot(outputDir),
				state.Intent.CandidateDir,
			) {
			return fmt.Errorf(
				"render recovery transaction %s is not bound to this live/candidate root",
				id,
			)
		}
		var receipt *store.DirectoryPublishReceipt
		recoverErr := withPipelineWatchdogPaused(func() error {
			var err error
			receipt, err = publisher.RecoverDirectoryPublish(id)
			return err
		})
		if recoverErr != nil {
			return fmt.Errorf("render recovery pending directory publish %s: %w", id, recoverErr)
		}
		if err := publisher.FinalizeDirectoryPublish(id); err != nil {
			return fmt.Errorf("render recovery finalize directory publish %s: %w", id, err)
		}
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:render] 已恢复并封存目录发布事务 %s（live=%s）\n",
			receipt.TransactionID,
			receipt.CommittedLiveRoot,
		)
	}
	return nil
}

func pipelineRenderCandidateRoot(outputDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(outputDir)), ".render-candidates")
}

func pipelineRenderRejectionTombstonePath(
	liveOutputDir string,
	candidateID string,
	bodySHA256 string,
) (string, error) {
	candidateID = strings.TrimSpace(candidateID)
	bodySHA256 = strings.TrimSpace(bodySHA256)
	if candidateID == "" || filepath.Base(candidateID) != candidateID ||
		strings.ContainsAny(candidateID, `/\\`) {
		return "", fmt.Errorf("render rejection tombstone candidate id is malformed")
	}
	if !pipelineRenderExactBodySHA256(bodySHA256) {
		return "", fmt.Errorf("render rejection tombstone body sha256 is malformed")
	}
	return filepath.Join(
		pipelineRenderCandidateRoot(liveOutputDir),
		"rejections",
		candidateID,
		bodySHA256+".json",
	), nil
}

func pipelineRenderExactBodySHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func pipelineRenderRequiredReviewArtifacts(chapter int) []string {
	return []string{
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter),
		fmt.Sprintf("reviews/%02d.json", chapter),
		fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter),
		fmt.Sprintf("reviews/%02d.md", chapter),
	}
}

func pipelineRenderHasCompleteReviewArtifacts(chapter int, artifacts []string) bool {
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		seen[filepath.ToSlash(strings.TrimSpace(artifact))] = true
	}
	for _, required := range pipelineRenderRequiredReviewArtifacts(chapter) {
		if !seen[required] {
			return false
		}
	}
	return true
}

// persistPipelineRenderSemanticRejection writes negative evidence only after
// the complete formal review set is proven fresh for the exact committed body.
// Missing/stale review evidence and operational failures deliberately remain
// recoverable and therefore do not create a tombstone.
func persistPipelineRenderSemanticRejection(
	liveOutputDir string,
	candidate *pipelineRenderCandidate,
	frozen *pipelineFrozenPlan,
	snapshot *pipelineRenderedChapterSnapshot,
	inspection currentChapterReviewEvidence,
) (bool, error) {
	if len(inspection.Issues) > 0 ||
		pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) ||
		strings.TrimSpace(inspection.Verdict) == "" ||
		strings.TrimSpace(inspection.Disposition) == "" ||
		!pipelineRenderHasCompleteReviewArtifacts(inspection.Chapter, inspection.Artifacts) {
		return false, nil
	}
	if candidate == nil || frozen == nil || snapshot == nil {
		return false, fmt.Errorf("persist render semantic rejection requires candidate, frozen plan and snapshot")
	}
	wantID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return false, err
	}
	exactBodySHA256 := reviewreport.BodySHA256(snapshot.Body)
	checkpointBodyDigest := domain.PlanningV2DigestPrefix + exactBodySHA256
	if candidate.ID != wantID || inspection.Chapter != frozen.Chapter ||
		inspection.BodySHA256 != exactBodySHA256 ||
		snapshot.BodySHA256 != checkpointBodyDigest {
		return false, fmt.Errorf("persist render semantic rejection exact-body identity mismatch")
	}
	tombstone := pipelineRenderRejectionTombstone{
		Version:                pipelineRenderRejectionTombstoneVersion,
		CandidateID:            candidate.ID,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		BodySHA256:             exactBodySHA256,
		Verdict:                strings.TrimSpace(inspection.Verdict),
		Disposition:            strings.TrimSpace(inspection.Disposition),
		ReviewArtifacts:        append([]string(nil), inspection.Artifacts...),
		RejectedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	sort.Strings(tombstone.ReviewArtifacts)
	// A pre-existing exact path is immutable evidence. Validate it and reuse it;
	// never overwrite a malformed or identity-drifted tombstone.
	if exists, err := pipelineRenderBodyHasSemanticRejection(
		liveOutputDir,
		frozen,
		candidate.ID,
		exactBodySHA256,
	); err != nil {
		return false, err
	} else if exists {
		return true, nil
	}
	raw, err := json.MarshalIndent(tombstone, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append(raw, '\n')
	path, err := pipelineRenderRejectionTombstonePath(
		liveOutputDir,
		candidate.ID,
		exactBodySHA256,
	)
	if err != nil {
		return false, err
	}
	if err := atomicWriteRewriteFile(path, raw, 0o644); err != nil {
		return false, fmt.Errorf("save render semantic rejection tombstone: %w", err)
	}
	if exists, err := pipelineRenderBodyHasSemanticRejection(
		liveOutputDir,
		frozen,
		candidate.ID,
		exactBodySHA256,
	); err != nil {
		return false, err
	} else if !exists {
		return false, fmt.Errorf("saved render semantic rejection tombstone is not readable")
	}
	return true, nil
}

// pipelineRenderBodyHasSemanticRejection checks only the exact transaction and
// body path. Unrelated or historical files have no authority. If that exact
// path exists, malformed/version-drifted content fails closed rather than
// allowing the stale body to be replayed.
func pipelineRenderBodyHasSemanticRejection(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidateID string,
	bodySHA256 string,
) (bool, error) {
	path, err := pipelineRenderRejectionTombstonePath(liveOutputDir, candidateID, bodySHA256)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read exact render rejection tombstone: %w", err)
	}
	var tombstone pipelineRenderRejectionTombstone
	if err := json.Unmarshal(raw, &tombstone); err != nil {
		return false, fmt.Errorf("decode exact render rejection tombstone: %w", err)
	}
	if err := validatePipelineRenderRejectionTombstone(
		&tombstone,
		frozen,
		candidateID,
		bodySHA256,
	); err != nil {
		return false, fmt.Errorf("invalid exact render rejection tombstone: %w", err)
	}
	return true, nil
}

func validatePipelineRenderRejectionTombstone(
	tombstone *pipelineRenderRejectionTombstone,
	frozen *pipelineFrozenPlan,
	candidateID string,
	bodySHA256 string,
) error {
	if tombstone == nil || frozen == nil {
		return fmt.Errorf("missing tombstone or frozen plan")
	}
	wantID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return err
	}
	if tombstone.Version != pipelineRenderRejectionTombstoneVersion ||
		candidateID != wantID || tombstone.CandidateID != wantID ||
		tombstone.GenerationID != frozen.PlanningGenerationID ||
		tombstone.Chapter != frozen.Chapter ||
		tombstone.PlanDigest != frozen.PlanDigest ||
		tombstone.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		tombstone.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		tombstone.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		tombstone.BodySHA256 != bodySHA256 ||
		!pipelineRenderExactBodySHA256(tombstone.BodySHA256) {
		return fmt.Errorf("versioned sealed transaction/body identity mismatch")
	}
	if strings.TrimSpace(tombstone.Verdict) == "" ||
		strings.TrimSpace(tombstone.Disposition) == "" ||
		pipelineReviewAcceptedForProjection(tombstone.Verdict, tombstone.Disposition) {
		return fmt.Errorf("tombstone does not record a semantic non-accept")
	}
	if !pipelineRenderHasCompleteReviewArtifacts(tombstone.Chapter, tombstone.ReviewArtifacts) {
		return fmt.Errorf("tombstone review artifact set is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, tombstone.RejectedAt); err != nil {
		return fmt.Errorf("tombstone rejected_at is invalid: %w", err)
	}
	return nil
}

func pipelineRenderTransactionID(frozen *pipelineFrozenPlan) (string, error) {
	if frozen == nil ||
		frozen.ProjectionBinding != "sealed_v2" ||
		frozen.Chapter <= 0 ||
		frozen.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(frozen.PlanningGenerationID) == "" ||
		strings.TrimSpace(frozen.ProjectedBundleDigest) == "" ||
		strings.TrimSpace(frozen.PromotionReceiptDigest) == "" {
		return "", fmt.Errorf("sealed render transaction requires exact generation/chapter/plan checkpoint/bundle/promotion")
	}
	digest, err := domain.DeterministicPlanningHash(struct {
		Version     string `json:"version"`
		Generation  string `json:"generation"`
		Chapter     int    `json:"chapter"`
		Plan        string `json:"plan"`
		Bundle      string `json:"bundle"`
		Promotion   string `json:"promotion"`
		RenderInput string `json:"render_input"`
	}{
		Version:     "sealed-render-directory-publish.v1",
		Generation:  frozen.PlanningGenerationID,
		Chapter:     frozen.Chapter,
		Plan:        frozen.PlanDigest,
		Bundle:      frozen.ProjectedBundleDigest,
		Promotion:   frozen.PromotionReceiptDigest,
		RenderInput: frozen.PipelineRunInputDigest,
	})
	if err != nil {
		return "", err
	}
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) < 24 {
		return "", fmt.Errorf("sealed render transaction digest is malformed")
	}
	return fmt.Sprintf("render-ch%04d-%s", frozen.Chapter, digest[:24]), nil
}

func preparePipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
) (*pipelineRenderCandidate, error) {
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return nil, err
	}
	root := pipelineRenderCandidateRoot(liveOutputDir)
	container := filepath.Join(root, id)
	if recovered, err := recoverChapterRenderTransactionCandidate(
		liveOutputDir,
		frozen,
		id,
		container,
	); err != nil {
		return nil, err
	} else if recovered != nil {
		return recovered, nil
	}
	if recovered, err := recoverAcceptedActualMismatchPipelineRenderCandidate(
		liveOutputDir,
		frozen,
		id,
		container,
	); err != nil {
		return nil, err
	} else if recovered != nil {
		return recovered, nil
	}
	// A complete semantic reject is not an ordinary crash snapshot.  Resume the
	// rejected candidate as a rewrite seed so Writer receives its exact final
	// body, structured review and rewrite brief.  Falling through to the generic
	// exact-body recovery here can resurrect an earlier pre-review draft from the
	// same sealed transaction and deterministically repeat the rejected attempt.
	if recovered, err := recoverSemanticRejectedPipelineRenderCandidate(
		liveOutputDir,
		frozen,
		id,
		container,
	); err != nil {
		return nil, err
	} else if recovered != nil {
		return recovered, nil
	}
	if recovered, err := recoverDurableChapterRenderCandidate(
		liveOutputDir,
		frozen,
		id,
		container,
	); err != nil {
		return nil, err
	} else if recovered != nil {
		return recovered, nil
	}
	if recovered, err := recoverReusablePipelineRenderCandidate(
		liveOutputDir,
		frozen,
		id,
		container,
	); err != nil {
		return nil, err
	} else if recovered != nil {
		return recovered, nil
	}
	styleEpoch, err := inspectPipelineRenderStyleEpochRecovery(
		liveOutputDir,
		frozen,
		id,
		container,
	)
	if err != nil {
		return nil, err
	}
	declareV3Epoch := styleEpoch != nil && !styleEpoch.legacyV2
	if styleEpoch == nil {
		declareV3Epoch = frozen.EffectiveStyleProtocol == pipelineRenderCandidateManifestVersion
	}
	fresh, err := prepareFreshPipelineRenderCandidateForStyleEpoch(
		liveOutputDir, frozen, id, container, declareV3Epoch,
	)
	if err != nil {
		return nil, err
	}
	if styleEpoch == nil {
		return fresh, nil
	}
	if styleEpoch.legacyV2 {
		if err := rewritePipelineRenderCandidateAsLegacyV2(fresh.OutputDir, frozen); err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "legacy-style-epoch-failed")
			return nil, err
		}
		return fresh, nil
	}
	if styleEpoch.receipt == nil {
		return fresh, nil
	}
	if err := restorePipelineRenderCandidateEffectiveStyleReceipt(fresh.OutputDir, styleEpoch.receipt); err != nil {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "style-receipt-recovery-failed")
		return nil, err
	}
	return fresh, nil
}

type pipelineRenderActualMismatchRecoveryReceipt struct {
	Version                 string `json:"version"`
	CandidateID             string `json:"candidate_id"`
	GenerationID            string `json:"generation_id"`
	Chapter                 int    `json:"chapter"`
	PlanDigest              string `json:"plan_digest"`
	PlanCheckpointSeq       int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest   string `json:"projected_bundle_digest"`
	PromotionReceiptDigest  string `json:"promotion_receipt_digest"`
	PipelineRunInputDigest  string `json:"pipeline_run_input_digest"`
	BodySHA256              string `json:"body_sha256"`
	SourceCandidateDir      string `json:"source_candidate_dir"`
	SourceActualMatchSHA256 string `json:"source_actual_match_sha256"`
	CurrentLiveRoot         string `json:"current_live_root"`
	RecoveredAt             string `json:"recovered_at"`
}

// recoverAcceptedActualMismatchPipelineRenderCandidate is deliberately
// narrower than ordinary crash recovery.  An actual-mismatch directory was
// retired only after Writer, commit and formal review had all succeeded.  If
// those exact bytes later receive a deterministic matcher fix, rerunning Writer
// would both waste a model call and replace already accepted prose.
//
// Recovery therefore requires all of the following before restoring the whole
// committed candidate tree: exact sealed transaction identity (the candidate
// id cryptographically includes PipelineRunInputDigest), an exact-body current
// accept, an exact-body approved DeepSeek/current gate, no effective semantic
// rejection, and a complete negative sealed_actual_match receipt. A body that
// was historically rejected additionally needs a durable FormalAccepted
// resolution; a first-pass formal accept has no rejection bit to resolve.
// Rejected prose and ordinary stale candidates continue through their existing
// rewrite/replay paths.
func recoverAcceptedActualMismatchPipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderCandidate, error) {
	root := pipelineRenderCandidateRoot(liveOutputDir)
	retiredRoot := filepath.Join(root, "retired")
	paths := []string{activeContainer}
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list actual-mismatch render candidates: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), id+"-actual-mismatch-") {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}

	var best *reusablePipelineRenderCandidate
	bestActualMatchSHA := ""
	for _, candidatePath := range paths {
		candidate, ok := inspectReusablePipelineRenderCandidate(
			candidatePath,
			liveOutputDir,
			frozen,
			id,
		)
		if !ok || candidate.score < 3 {
			continue
		}
		actualMatchSHA, accepted, inspectErr := inspectAcceptedActualMismatchPipelineRenderCandidate(
			liveOutputDir,
			frozen,
			candidate,
		)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if !accepted {
			continue
		}
		if newerReusablePipelineRenderCandidate(candidate, best) {
			copy := candidate
			best = &copy
			bestActualMatchSHA = actualMatchSHA
		}
	}
	if best == nil {
		return nil, nil
	}

	livePlan, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(liveOutputDir), frozen.Chapter)
	if err != nil || livePlan.Digest != frozen.PlanDigest || livePlan.Seq != frozen.PlanCheckpointSeq {
		return nil, fmt.Errorf(
			"recover accepted actual-mismatch candidate requires live plan %s#%d (current=%v err=%v)",
			frozen.PlanDigest,
			frozen.PlanCheckpointSeq,
			livePlan,
			err,
		)
	}
	currentLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot current live root for actual-mismatch recovery: %w", err)
	}

	if filepath.Clean(best.container) != filepath.Clean(activeContainer) {
		if _, statErr := os.Stat(activeContainer); statErr == nil {
			if err := retirePipelineRenderCandidate(activeContainer, "stale-superseded-by-actual-mismatch"); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := copyPipelineRenderCandidateTree(
			filepath.Join(best.container, "output"),
			filepath.Join(activeContainer, "output"),
		); err != nil {
			_ = retirePipelineRenderCandidate(activeContainer, "actual-mismatch-copy-failed")
			return nil, fmt.Errorf("restore accepted actual-mismatch render candidate: %w", err)
		}
	}

	output := filepath.Join(activeContainer, "output")
	manifest := best.manifest
	manifest.SourceLiveRoot = currentLiveRoot
	manifest.PreparedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		append(manifestRaw, '\n'),
		0o644,
	); err != nil {
		return nil, fmt.Errorf("refresh actual-mismatch render candidate manifest: %w", err)
	}
	receipt := pipelineRenderActualMismatchRecoveryReceipt{
		Version:                 "pipeline-render-actual-mismatch-recovery.v1",
		CandidateID:             id,
		GenerationID:            frozen.PlanningGenerationID,
		Chapter:                 frozen.Chapter,
		PlanDigest:              frozen.PlanDigest,
		PlanCheckpointSeq:       frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:   frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:  frozen.PromotionReceiptDigest,
		PipelineRunInputDigest:  frozen.PipelineRunInputDigest,
		BodySHA256:              reviewreport.BodySHA256(best.body),
		SourceCandidateDir:      filepath.Clean(best.container),
		SourceActualMatchSHA256: bestActualMatchSHA,
		CurrentLiveRoot:         currentLiveRoot,
		RecoveredAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	receiptRaw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate_actual_mismatch_recovery.json"),
		append(receiptRaw, '\n'),
		0o644,
	); err != nil {
		return nil, fmt.Errorf("save actual-mismatch render recovery receipt: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 恢复同一 sealed 输入的 formal-accepted actual-mismatch 候选（chapter=%d body=%s）；仅重跑 deterministic matcher\n",
		frozen.Chapter,
		best.bodyCheckpoint.Digest,
	)
	return &pipelineRenderCandidate{
		ID:                              id,
		ContainerDir:                    activeContainer,
		OutputDir:                       output,
		TransactionRoot:                 pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:                  currentLiveRoot,
		RecoveredAcceptedActualMismatch: true,
	}, nil
}

func inspectAcceptedActualMismatchPipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidate reusablePipelineRenderCandidate,
) (string, bool, error) {
	if frozen == nil || candidate.score < 3 || strings.TrimSpace(candidate.body) == "" {
		return "", false, nil
	}
	output := filepath.Join(candidate.container, "output")
	plan := store.NewStore(output).Checkpoints.LatestByStep(
		domain.ChapterScope(frozen.Chapter),
		"plan",
	)
	if plan == nil || plan.Seq != frozen.PlanCheckpointSeq || plan.Digest != frozen.PlanDigest {
		return "", false, nil
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(output, frozen, plan)
	if err != nil || snapshot.Body != candidate.body ||
		snapshot.BodySHA256 != candidate.bodyCheckpoint.Digest {
		return "", false, nil
	}
	if err := requirePipelineAcceptedExactReview(output, frozen.Chapter); err != nil {
		return "", false, nil
	}
	progress, err := snapshot.Store.Progress.Load()
	if err != nil || progress == nil || slices.Contains(progress.PendingRewrites, frozen.Chapter) {
		return "", false, nil
	}

	bodySHA := reviewreport.BodySHA256(candidate.body)
	if candidate.judge == nil {
		return "", false, nil
	}
	gate, err := tools.InspectDraftExternalGateWithStore(snapshot.Store, frozen.Chapter)
	if err != nil {
		return "", false, fmt.Errorf("inspect actual-mismatch exact-body gate: %w", err)
	}
	if gate.Status != tools.DraftExternalGateApproved || !gate.ArtifactExists ||
		gate.CurrentBodySHA256 != bodySHA || gate.EvaluatedBodySHA256 != bodySHA {
		return "", false, nil
	}
	rejected, err := pipelineRenderBodyHasEffectiveSemanticRejection(
		liveOutputDir,
		frozen,
		candidate.manifest.CandidateID,
		bodySHA,
	)
	if err != nil {
		return "", false, err
	}
	if rejected {
		return "", false, nil
	}
	ledger, err := loadPipelineRenderConvergenceLedger(
		liveOutputDir,
		candidate.manifest,
		pipelineRenderConvergenceLimit(snapshot.Store, frozen.Chapter),
	)
	if err != nil {
		return "", false, nil
	}
	var record *pipelineRenderConvergenceRecord
	for i := range ledger.Records {
		if ledger.Records[i].BodySHA256 == bodySHA {
			record = &ledger.Records[i]
			break
		}
	}
	if record == nil || !record.ExternalJudged || record.ExternalBlocking ||
		(record.SemanticReject && !record.FormalAccepted) {
		return "", false, nil
	}

	actualMatchPath := filepath.Join(output, "meta", "planning", "sealed_actual_match.json")
	actualMatchRaw, err := os.ReadFile(actualMatchPath)
	if err != nil {
		return "", false, nil
	}
	var actualMatch pipelineSealedActualDeltaMatch
	if json.Unmarshal(actualMatchRaw, &actualMatch) != nil || actualMatch.ProjectionMatch ||
		!actualMatch.Complete || len(actualMatch.MismatchReasons) == 0 ||
		domain.ValidateProjectedDeltaV2(actualMatch.ActualDelta) != nil {
		return "", false, nil
	}
	return pipelineBytesSHA(actualMatchRaw), true, nil
}

// recoverSemanticRejectedPipelineRenderCandidate restores only a fully
// reviewed, tombstone-bound candidate.  Unlike ordinary draft crash recovery,
// the whole isolated tree is required here: save_review already materialized
// PendingRewrites, the exact rejected chapter, structured review artifacts and
// reviews/NN_rewrite_brief.md.  Those are the existing rewrite_source hand-off
// consumed by Writer; copying only an older draft hash loses that hand-off.
func recoverSemanticRejectedPipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderCandidate, error) {
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list semantic rejected render candidates: %w", err)
	}
	paths := []string{activeContainer}
	if err == nil {
		for _, entry := range entries {
			semanticRejected := strings.HasPrefix(entry.Name(), id+"-rejected-")
			semanticContinuation := strings.HasPrefix(entry.Name(), id+"-stale-")
			if entry.IsDir() && (semanticRejected || semanticContinuation) {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}
	var best *reusablePipelineRenderCandidate
	for _, candidatePath := range paths {
		candidate, ok := inspectReusablePipelineRenderCandidate(
			candidatePath,
			liveOutputDir,
			frozen,
			id,
		)
		if !ok || candidate.score < 3 {
			continue
		}
		seedStore := store.NewStore(filepath.Join(candidate.container, "output"))
		review, reviewErr := seedStore.World.LoadReview(frozen.Chapter)
		finalBody, finalErr := seedStore.Drafts.LoadChapterText(frozen.Chapter)
		if reviewErr != nil || review == nil || finalErr != nil ||
			review.BodySHA256 != reviewreport.BodySHA256(finalBody) ||
			(review.Verdict != "rewrite" && review.Verdict != "polish") {
			continue
		}
		bodySHA := review.BodySHA256
		tombstoned, tombstoneErr := pipelineRenderBodyHasEffectiveSemanticRejection(
			liveOutputDir,
			frozen,
			id,
			bodySHA,
		)
		if tombstoneErr != nil {
			return nil, fmt.Errorf("inspect semantic rewrite seed tombstone: %w", tombstoneErr)
		}
		if !tombstoned {
			continue
		}
		progress, progressErr := seedStore.Progress.Load()
		if progressErr != nil || progress == nil ||
			!slices.Contains(progress.PendingRewrites, frozen.Chapter) {
			continue
		}
		briefPath := filepath.Join(
			seedStore.Dir(),
			"reviews",
			fmt.Sprintf("%02d_rewrite_brief.md", frozen.Chapter),
		)
		brief, briefErr := os.ReadFile(briefPath)
		if briefErr != nil || !strings.Contains(string(brief), bodySHA) {
			continue
		}
		if best == nil || candidate.bodyCheckpoint.Seq > best.bodyCheckpoint.Seq ||
			(candidate.bodyCheckpoint.Seq == best.bodyCheckpoint.Seq &&
				candidate.manifest.PreparedAt > best.manifest.PreparedAt) {
			copy := candidate
			best = &copy
		}
	}
	if best == nil {
		return nil, nil
	}

	currentLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot current live root for semantic render recovery: %w", err)
	}
	// A live semantic continuation may already contain a newer Writer/Edit body
	// than the retired rejection seed. Keep that tree in place: retiring it and
	// copying the older seed would roll the body checkpoint backwards. Refresh
	// only the manifest CAS root when a process restart changed live metadata;
	// this is the same root refresh previously performed after a retired copy.
	if filepath.Clean(best.container) == filepath.Clean(activeContainer) {
		manifest := best.manifest
		if manifest.SourceLiveRoot != currentLiveRoot {
			manifest.SourceLiveRoot = currentLiveRoot
			manifest.PreparedAt = time.Now().UTC().Format(time.RFC3339Nano)
			raw, marshalErr := json.MarshalIndent(manifest, "", "  ")
			if marshalErr != nil {
				return nil, marshalErr
			}
			if err := atomicWriteRewriteFile(
				filepath.Join(activeContainer, "output", "meta", "planning", "render_candidate.json"),
				append(raw, '\n'),
				0o644,
			); err != nil {
				return nil, fmt.Errorf("refresh active semantic render candidate manifest: %w", err)
			}
		}
		return &pipelineRenderCandidate{
			ID:              id,
			ContainerDir:    activeContainer,
			OutputDir:       filepath.Join(activeContainer, "output"),
			TransactionRoot: pipelineRenderTransactionRoot(liveOutputDir),
			SourceLiveRoot:  currentLiveRoot,
		}, nil
	}
	if _, err := os.Stat(activeContainer); err == nil {
		if err := retirePipelineRenderCandidate(activeContainer, "stale-superseded-by-semantic-rewrite"); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	output := filepath.Join(activeContainer, "output")
	if err := copyPipelineRenderCandidateTree(filepath.Join(best.container, "output"), output); err != nil {
		_ = retirePipelineRenderCandidate(activeContainer, "semantic-rewrite-copy-failed")
		return nil, fmt.Errorf("restore semantic rejected render candidate: %w", err)
	}
	manifest := best.manifest
	manifest.SourceLiveRoot = currentLiveRoot
	manifest.PreparedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = retirePipelineRenderCandidate(activeContainer, "semantic-rewrite-manifest-failed")
		return nil, err
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		append(raw, '\n'),
		0o644,
	); err != nil {
		_ = retirePipelineRenderCandidate(activeContainer, "semantic-rewrite-manifest-failed")
		return nil, fmt.Errorf("refresh semantic rewrite candidate manifest: %w", err)
	}
	_ = os.Remove(filepath.Join(output, "meta", "planning", "render_candidate_recovery.json"))
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 恢复同一 sealed 身份的 semantic rewrite_source（chapter=%d rejected_body=%s）\n",
		frozen.Chapter,
		best.bodyCheckpoint.Digest,
	)
	return &pipelineRenderCandidate{
		ID:              id,
		ContainerDir:    activeContainer,
		OutputDir:       output,
		TransactionRoot: pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:  currentLiveRoot,
	}, nil
}

func prepareFreshPipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	container string,
) (*pipelineRenderCandidate, error) {
	return prepareFreshPipelineRenderCandidateForStyleEpoch(
		liveOutputDir, frozen, id, container, true,
	)
}

func prepareFreshPipelineRenderCandidateForStyleEpoch(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	container string,
	declareV3Epoch bool,
) (*pipelineRenderCandidate, error) {
	if declareV3Epoch {
		if _, err := ensurePipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, id); err != nil {
			return nil, fmt.Errorf("declare immutable v3 render style epoch: %w", err)
		}
	}
	output := filepath.Join(container, "output")
	if _, err := os.Stat(container); err == nil {
		if err := retirePipelineRenderCandidate(container, "stale"); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	sourceLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot sealed render live root: %w", err)
	}
	if err := copyPipelineRenderCandidateTree(liveOutputDir, output); err != nil {
		_ = retirePipelineRenderCandidate(container, "copy-failed")
		return nil, fmt.Errorf("prepare sealed render candidate: %w", err)
	}
	afterCopyLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		_ = retirePipelineRenderCandidate(container, "source-recheck-failed")
		return nil, fmt.Errorf("recheck sealed render live root: %w", err)
	}
	if afterCopyLiveRoot != sourceLiveRoot {
		_ = retirePipelineRenderCandidate(container, "source-drift")
		return nil, fmt.Errorf("live canon changed while preparing sealed render candidate")
	}
	manifest := pipelineRenderCandidateManifest{
		Version:                   pipelineRenderCandidatePreStyleManifestVersion,
		CandidateID:               id,
		GenerationID:              frozen.PlanningGenerationID,
		Chapter:                   frozen.Chapter,
		PlanDigest:                frozen.PlanDigest,
		PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
		PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
		RenderContextSHA256:       frozen.RenderContextSHA256,
		SourceOutputDir:           filepath.Clean(liveOutputDir),
		SourceLiveRoot:            sourceLiveRoot,
		PreparedAt:                time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		raw,
		0o644,
	); err != nil {
		_ = retirePipelineRenderCandidate(container, "manifest-failed")
		return nil, fmt.Errorf("save sealed render candidate manifest: %w", err)
	}
	return &pipelineRenderCandidate{
		ID:              id,
		ContainerDir:    container,
		OutputDir:       output,
		TransactionRoot: pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:  sourceLiveRoot,
	}, nil
}

func bindPipelineRenderCandidateEffectiveStyle(
	outputDir string,
	frozen *pipelineFrozenPlan,
	receipt *tools.EffectiveRenderStyleContractReceipt,
) error {
	if frozen == nil || receipt == nil || strings.TrimSpace(receipt.ReceiptDigest) == "" {
		return fmt.Errorf("bind render candidate effective style requires frozen plan and receipt")
	}
	path := filepath.Join(outputDir, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode render candidate before style binding: %w", err)
	}
	if manifest.Version != pipelineRenderCandidateManifestVersion &&
		manifest.Version != pipelineRenderCandidatePreStyleManifestVersion {
		return fmt.Errorf("render candidate style binding refuses manifest version %q", manifest.Version)
	}
	if manifest.CandidateID != receipt.CandidateID || manifest.GenerationID != frozen.PlanningGenerationID ||
		manifest.Chapter != frozen.Chapter || manifest.PlanDigest != frozen.PlanDigest ||
		manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		receipt.GenerationID != frozen.PlanningGenerationID || receipt.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		receipt.BaseRenderContextSHA256 != frozen.RenderContextSHA256 ||
		receipt.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
		receipt.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return fmt.Errorf("render candidate/effective style receipt sealed identity mismatch")
	}
	if manifest.Version == pipelineRenderCandidateManifestVersion &&
		(manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			(strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" &&
				manifest.EffectiveStyleReceiptDigest != receipt.ReceiptDigest)) {
		return fmt.Errorf("refuse to repair drifted v3 render candidate effective-style binding")
	}
	if manifest.Version == pipelineRenderCandidatePreStyleManifestVersion &&
		(manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return fmt.Errorf("pre-style render candidate input drifted before effective-style binding")
	}
	manifest.Version = pipelineRenderCandidateManifestVersion
	manifest.PlanCheckpointSeq = frozen.PlanCheckpointSeq
	manifest.PipelineRenderInputDigest = frozen.PipelineRunInputDigest
	manifest.RenderContextSHA256 = frozen.RenderContextSHA256
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteRewriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("bind render candidate effective style receipt: %w", err)
	}
	return nil
}

func restorePipelineRenderCandidateEffectiveStyleReceipt(
	outputDir string,
	receipt *tools.EffectiveRenderStyleContractReceipt,
) error {
	if receipt == nil || !pipelineRenderInputSHA256(receipt.ReceiptDigest) {
		return fmt.Errorf("restore render candidate effective style requires a valid receipt")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	path := filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))
	if err := atomicWriteRewriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("restore exact effective style receipt: %w", err)
	}
	return nil
}

func loadPipelineRenderCandidateEffectiveStyleReceipt(
	outputDir string,
	manifest pipelineRenderCandidateManifest,
	frozen *pipelineFrozenPlan,
) (*tools.EffectiveRenderStyleContractReceipt, error) {
	if frozen == nil || manifest.Version != pipelineRenderCandidateManifestVersion ||
		manifest.Chapter != frozen.Chapter || manifest.PlanDigest != frozen.PlanDigest ||
		manifest.CandidateID == "" || manifest.EffectiveStyleReceiptDigest == "" {
		return nil, fmt.Errorf("v3 effective style receipt recovery identity is incomplete")
	}
	st := store.NewStore(outputDir)
	currentPath := filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))
	if info, err := os.Lstat(currentPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("v3 effective style receipt current pointer must be a real file")
		}
		_, receipt, err := tools.LoadEffectiveRenderStyleContract(st, frozen.Chapter, frozen.PlanDigest)
		if err == nil {
			return receipt, nil
		}
		// A committed target legitimately changes progress and cast-derived
		// stopwords after compilation. Recovery may authenticate the immutable
		// archive here; any later provider dispatch still passes the stricter
		// mutable-input prose-permit validation independently.
		_, archived, _, archiveErr := tools.LoadBoundArchivedEffectiveRenderStyleContract(
			st, frozen.Chapter, frozen.PlanDigest,
		)
		if archiveErr != nil {
			return nil, err
		}
		return archived, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	archivePath, err := tools.EffectiveRenderStyleContractArchivePath(
		manifest.Chapter,
		manifest.CandidateID,
		manifest.EffectiveStyleReceiptDigest,
	)
	if err != nil {
		return nil, err
	}
	receipt, err := tools.LoadArchivedEffectiveRenderStyleContract(
		st,
		archivePath,
		manifest.Chapter,
		manifest.EffectiveStyleReceiptDigest,
	)
	if err != nil {
		return nil, fmt.Errorf("load archived v3 effective style receipt: %w", err)
	}
	if receipt.GenerationID != frozen.PlanningGenerationID ||
		receipt.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		receipt.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
		receipt.BaseRenderContextSHA256 != frozen.RenderContextSHA256 ||
		receipt.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return nil, fmt.Errorf("archived v3 effective style receipt differs from frozen identity")
	}
	if err := restorePipelineRenderCandidateEffectiveStyleReceipt(outputDir, receipt); err != nil {
		return nil, err
	}
	_, restored, _, err := tools.LoadBoundArchivedEffectiveRenderStyleContract(
		st, frozen.Chapter, frozen.PlanDigest,
	)
	if err != nil {
		return nil, err
	}
	return restored, nil
}

// rewritePipelineRenderCandidateAsLegacyV2 preserves the previous render
// protocol for an exact v1/v2 recovery source. It deliberately does not attach
// current style assets: legacy prose and any later legacy rerender consume only
// the style contract embedded in the frozen render context.
func rewritePipelineRenderCandidateAsLegacyV2(
	outputDir string,
	frozen *pipelineFrozenPlan,
) error {
	if frozen == nil {
		return fmt.Errorf("legacy render candidate recovery requires frozen plan")
	}
	if err := validatePipelineRenderCandidatePlanningTree(outputDir); err != nil {
		return err
	}
	path := filepath.Join(outputDir, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Version != pipelineRenderCandidatePreStyleManifestVersion ||
		manifest.GenerationID != frozen.PlanningGenerationID || manifest.Chapter != frozen.Chapter ||
		manifest.PlanDigest != frozen.PlanDigest || manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
		manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
		strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
		return fmt.Errorf("fresh render candidate cannot preserve legacy style epoch")
	}
	manifest.Version = pipelineRenderCandidatePreviousManifestVersion
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	raw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteRewriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// resolvePipelineRenderCandidateSourceOutput derives candidate topology from
// the inspected directory, never from the mutable SourceOutputDir field. This
// prevents an isolated active/retired candidate from pointing SourceOutputDir
// at itself and hiding live CandidateID-scoped transaction or epoch evidence.
func resolvePipelineRenderCandidateSourceOutput(
	outputDir string,
	manifest *pipelineRenderCandidateManifest,
) (string, bool, error) {
	if manifest == nil {
		return "", false, fmt.Errorf("render candidate source topology requires manifest")
	}
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" || !filepath.IsAbs(outputDir) || outputDir != filepath.Clean(outputDir) {
		return "", false, fmt.Errorf("render candidate output must be a clean absolute path")
	}
	manifestSource := strings.TrimSpace(manifest.SourceOutputDir)
	if manifestSource != "" &&
		(!filepath.IsAbs(manifestSource) || manifestSource != filepath.Clean(manifestSource)) {
		return "", false, fmt.Errorf("render candidate source output must be a clean absolute path")
	}

	derivedSource := outputDir
	isolated := false
	candidateNamespace := ""
	if filepath.Base(outputDir) == "output" {
		container := filepath.Dir(outputDir)
		parent := filepath.Dir(container)
		switch {
		case filepath.Base(parent) == ".render-candidates":
			isolated = true
			candidateNamespace = parent
			if filepath.Base(container) != manifest.CandidateID {
				return "", false, fmt.Errorf("active render candidate container does not match CandidateID")
			}
		case filepath.Base(parent) == "retired" &&
			filepath.Base(filepath.Dir(parent)) == ".render-candidates":
			isolated = true
			candidateNamespace = filepath.Dir(parent)
			if !strings.HasPrefix(filepath.Base(container), manifest.CandidateID+"-") {
				return "", false, fmt.Errorf("retired render candidate container does not match CandidateID")
			}
		}
	}
	if isolated {
		if manifestSource == "" {
			return "", false, fmt.Errorf("isolated render candidate is missing source output")
		}
		if filepath.Clean(pipelineRenderCandidateRoot(manifestSource)) != filepath.Clean(candidateNamespace) {
			return "", false, fmt.Errorf("render candidate source output conflicts with directory topology")
		}
		derivedSource = manifestSource
	} else if manifestSource != "" && manifestSource != derivedSource {
		return "", false, fmt.Errorf("render candidate source output conflicts with directory topology")
	}
	return derivedSource, isolated, nil
}

func pipelineRenderCandidateStyleProtocol(
	outputDir string,
	frozen *pipelineFrozenPlan,
) (string, error) {
	if frozen == nil {
		return "", fmt.Errorf("render candidate style protocol requires frozen plan")
	}
	if err := validatePipelineRenderCandidatePlanningTree(outputDir); err != nil {
		return "", err
	}
	path := filepath.Join(outputDir, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	if _, _, err := resolvePipelineRenderCandidateSourceOutput(outputDir, &manifest); err != nil {
		return "", err
	}
	if frozen.ProjectionBinding == "sealed_v2" {
		expectedCandidateID, err := pipelineRenderTransactionID(frozen)
		if err != nil {
			return "", err
		}
		if manifest.CandidateID != expectedCandidateID {
			return "", fmt.Errorf("render candidate CandidateID differs from deterministic sealed identity")
		}
	}
	if manifest.GenerationID != frozen.PlanningGenerationID || manifest.Chapter != frozen.Chapter ||
		manifest.PlanDigest != frozen.PlanDigest || manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return "", fmt.Errorf("render candidate style protocol identity mismatch")
	}
	switch manifest.Version {
	case pipelineRenderCandidateManifestVersion:
		if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			!pipelineRenderInputSHA256(manifest.EffectiveStyleReceiptDigest) {
			return "", fmt.Errorf("v3 render candidate effective style identity is incomplete")
		}
	case pipelineRenderCandidatePreStyleManifestVersion:
		if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			return "", fmt.Errorf("pre-style render candidate identity is incomplete")
		}
	case pipelineRenderCandidatePreviousManifestVersion:
		if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			return "", fmt.Errorf("legacy v2 candidate carries v3 effective-style identity")
		}
		if err := rejectPipelineLegacyStyleDowngradeEvidence(outputDir, manifest, frozen); err != nil {
			return "", err
		}
	case pipelineRenderCandidateLegacyManifestVersion:
		if strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			return "", fmt.Errorf("legacy v1 candidate carries v3 effective-style identity")
		}
		if err := rejectPipelineLegacyStyleDowngradeEvidence(outputDir, manifest, frozen); err != nil {
			return "", err
		}
		// v1→v2 is a schema-only migration. Both protocols consume the exact
		// embedded frozen style and carry no effective-style receipt.
		manifest.Version = pipelineRenderCandidatePreviousManifestVersion
		manifest.PlanCheckpointSeq = frozen.PlanCheckpointSeq
		raw, err = json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return "", err
		}
		if err := atomicWriteRewriteFile(path, append(raw, '\n'), 0o644); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported render candidate style protocol %q", manifest.Version)
	}
	if manifest.Version == pipelineRenderCandidatePreviousManifestVersion {
		if err := os.Remove(filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return manifest.Version, nil
}

func validatePipelineRenderCandidatePlanningTree(outputDir string) error {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" || !filepath.IsAbs(outputDir) || outputDir != filepath.Clean(outputDir) {
		return fmt.Errorf("render candidate planning output must be a clean absolute path")
	}
	paths := []struct {
		name string
		path string
	}{
		{name: "output", path: outputDir},
		{name: "meta", path: filepath.Join(outputDir, "meta")},
		{name: "planning", path: filepath.Join(outputDir, "meta", "planning")},
	}
	for _, item := range paths {
		info, err := os.Lstat(item.path)
		if err != nil {
			return fmt.Errorf("inspect render candidate %s directory: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("render candidate %s must be a real directory", item.name)
		}
	}
	resolvedOutput, err := filepath.EvalSymlinks(outputDir)
	if err != nil {
		return err
	}
	resolvedPlanning, err := filepath.EvalSymlinks(filepath.Join(outputDir, "meta", "planning"))
	if err != nil {
		return err
	}
	if filepath.Clean(resolvedPlanning) != filepath.Join(filepath.Clean(resolvedOutput), "meta", "planning") {
		return fmt.Errorf("render candidate planning directory escapes output")
	}
	return nil
}

func rejectPipelineLegacyStyleDowngradeEvidence(
	outputDir string,
	manifest pipelineRenderCandidateManifest,
	frozen *pipelineFrozenPlan,
) error {
	if frozen == nil {
		return fmt.Errorf("legacy style protocol requires frozen identity")
	}
	sourceOutputDir, _, err := resolvePipelineRenderCandidateSourceOutput(outputDir, &manifest)
	if err != nil {
		return err
	}
	if intent, err := inspectPipelineRenderV3StyleEpochIntent(
		sourceOutputDir, frozen, manifest.CandidateID,
	); err != nil {
		return fmt.Errorf("inspect immutable v3 style epoch intent: %w", err)
	} else if intent != nil {
		return fmt.Errorf("candidate has an immutable v3 style epoch intent and cannot be relabeled as legacy v1/v2")
	}
	if frozen.EffectiveStyleProtocol == pipelineRenderCandidateManifestVersion {
		return fmt.Errorf("v3 frozen style epoch cannot be relabeled as legacy v1/v2")
	}
	if strings.TrimSpace(frozen.RenderInputUpgradeID) != "" ||
		strings.TrimSpace(frozen.RenderInputUpgradeReceiptDigest) != "" {
		return fmt.Errorf("render-input-upgraded candidate cannot be relabeled as legacy v1/v2 style protocol")
	}
	archiveDir := filepath.Join(
		outputDir,
		filepath.FromSlash(tools.EffectiveRenderStyleContractArchiveDir),
		fmt.Sprintf("ch%04d", manifest.Chapter),
		manifest.CandidateID,
	)
	if info, err := os.Lstat(archiveDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("candidate effective-style archive namespace is invalid")
		}
		entries, err := os.ReadDir(archiveDir)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("candidate has durable v3 effective-style archive evidence and cannot be relabeled as legacy v1/v2")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	currentPath := filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))
	if raw, err := os.ReadFile(currentPath); err == nil {
		var receipt tools.EffectiveRenderStyleContractReceipt
		if json.Unmarshal(raw, &receipt) == nil &&
			receipt.CandidateID == manifest.CandidateID &&
			receipt.Chapter == manifest.Chapter &&
			receipt.PlanDigest == manifest.PlanDigest {
			return fmt.Errorf("candidate has a v3 effective-style receipt and cannot be relabeled as legacy v1/v2")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

type reusablePipelineRenderCandidate struct {
	container      string
	manifest       pipelineRenderCandidateManifest
	styleReceipt   *tools.EffectiveRenderStyleContractReceipt
	body           string
	bodyCheckpoint domain.Checkpoint
	judge          *deepseekAIJudgeArtifact
	editorCache    *editorReviewCacheArtifact
	localSoftChain *tools.DraftLocalSoftEditRecoveryChain
	score          int
}

type pipelineRenderStyleEpochRecovery struct {
	legacyV2   bool
	receipt    *tools.EffectiveRenderStyleContractReceipt
	preparedAt string
}

// inspectPipelineRenderStyleEpochRecovery recovers protocol identity even when
// a provider crash happened before any draft checkpoint existed. The sibling
// dispatch ledger is keyed by CandidateID and survives candidate retirement,
// so rebuilding the same candidate under another style epoch would otherwise
// either deadlock recovery or relabel a consumed provider attempt.
func inspectPipelineRenderStyleEpochRecovery(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderStyleEpochRecovery, error) {
	if frozen == nil || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("render style epoch recovery requires frozen identity")
	}
	intent, err := inspectPipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, id)
	if err != nil {
		return nil, err
	}
	paths := []string{activeContainer}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), id+"-") {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}
	sort.Strings(paths)
	var bestV3 *pipelineRenderStyleEpochRecovery
	var bestLegacy *pipelineRenderStyleEpochRecovery
	for _, container := range paths {
		manifestPath := filepath.Join(container, "output", "meta", "planning", "render_candidate.json")
		info, statErr := os.Lstat(manifestPath)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("render style epoch manifest must be a real file: %s", manifestPath)
		}
		raw, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			return nil, readErr
		}
		var manifest pipelineRenderCandidateManifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.CandidateID != id ||
			manifest.GenerationID != frozen.PlanningGenerationID || manifest.Chapter != frozen.Chapter ||
			manifest.PlanDigest != frozen.PlanDigest ||
			manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
			manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
			filepath.Clean(manifest.SourceOutputDir) != filepath.Clean(liveOutputDir) {
			continue
		}
		switch manifest.Version {
		case pipelineRenderCandidateManifestVersion:
			if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
				manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
				manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
				!pipelineRenderInputSHA256(manifest.EffectiveStyleReceiptDigest) {
				return nil, fmt.Errorf("v3 render style epoch identity is incomplete")
			}
			receipt, loadErr := loadPipelineRenderCandidateEffectiveStyleReceipt(
				filepath.Join(container, "output"),
				manifest,
				frozen,
			)
			if loadErr != nil {
				return nil, fmt.Errorf("validate v3 render style epoch receipt: %w", loadErr)
			}
			if receipt == nil || receipt.ReceiptDigest != manifest.EffectiveStyleReceiptDigest {
				return nil, fmt.Errorf("v3 render style epoch receipt digest mismatch")
			}
			candidate := &pipelineRenderStyleEpochRecovery{receipt: receipt, preparedAt: manifest.PreparedAt}
			if bestV3 != nil && bestV3.receipt != nil &&
				bestV3.receipt.ReceiptDigest != receipt.ReceiptDigest {
				return nil, fmt.Errorf(
					"candidate id has multiple incompatible v3 effective-style receipt digests (%s, %s)",
					bestV3.receipt.ReceiptDigest,
					receipt.ReceiptDigest,
				)
			}
			if bestV3 == nil || candidate.preparedAt > bestV3.preparedAt {
				bestV3 = candidate
			}
		case pipelineRenderCandidatePreviousManifestVersion:
			if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
				strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
				strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
				strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
				return nil, fmt.Errorf("legacy v2 render style epoch carries v3-only fields")
			}
			if err := rejectPipelineLegacyStyleDowngradeEvidence(
				filepath.Join(container, "output"), manifest, frozen,
			); err != nil {
				return nil, err
			}
			candidate := &pipelineRenderStyleEpochRecovery{legacyV2: true, preparedAt: manifest.PreparedAt}
			if bestLegacy == nil || candidate.preparedAt > bestLegacy.preparedAt {
				bestLegacy = candidate
			}
		case pipelineRenderCandidateLegacyManifestVersion:
			if strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
				strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
				strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
				return nil, fmt.Errorf("legacy v1 render style epoch carries v3-only fields")
			}
			if err := rejectPipelineLegacyStyleDowngradeEvidence(
				filepath.Join(container, "output"), manifest, frozen,
			); err != nil {
				return nil, err
			}
			candidate := &pipelineRenderStyleEpochRecovery{legacyV2: true, preparedAt: manifest.PreparedAt}
			if bestLegacy == nil || candidate.preparedAt > bestLegacy.preparedAt {
				bestLegacy = candidate
			}
		case pipelineRenderCandidatePreStyleManifestVersion:
			if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
				manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
				manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
				strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
				return nil, fmt.Errorf("pre-style render epoch identity is malformed")
			}
			receipt, receiptErr := loadPipelinePreStyleCandidateReceipt(
				filepath.Join(container, "output"), manifest, frozen,
			)
			if receiptErr != nil {
				return nil, receiptErr
			}
			if receipt != nil {
				candidate := &pipelineRenderStyleEpochRecovery{receipt: receipt, preparedAt: manifest.PreparedAt}
				if bestV3 != nil && bestV3.receipt != nil &&
					bestV3.receipt.ReceiptDigest != receipt.ReceiptDigest {
					return nil, fmt.Errorf(
						"candidate id has multiple incompatible pre-style/v3 receipt digests (%s, %s)",
						bestV3.receipt.ReceiptDigest,
						receipt.ReceiptDigest,
					)
				}
				if bestV3 == nil || candidate.preparedAt > bestV3.preparedAt {
					bestV3 = candidate
				}
			}
		default:
			continue
		}
	}
	if bestV3 != nil && bestLegacy != nil {
		return nil, fmt.Errorf("candidate id is shared by incompatible legacy and v3 style epochs")
	}
	if intent != nil && bestLegacy != nil {
		return nil, fmt.Errorf("immutable v3 style epoch intent forbids legacy v1/v2 candidate recovery")
	}
	if bestV3 != nil {
		return bestV3, nil
	}
	if intent != nil {
		return &pipelineRenderStyleEpochRecovery{}, nil
	}
	return bestLegacy, nil
}

func loadPipelinePreStyleCandidateReceipt(
	outputDir string,
	manifest pipelineRenderCandidateManifest,
	frozen *pipelineFrozenPlan,
) (*tools.EffectiveRenderStyleContractReceipt, error) {
	archiveDir := filepath.Join(
		outputDir,
		filepath.FromSlash(tools.EffectiveRenderStyleContractArchiveDir),
		fmt.Sprintf("ch%04d", manifest.Chapter),
		manifest.CandidateID,
	)
	entries, err := os.ReadDir(archiveDir)
	if os.IsNotExist(err) {
		return recoverPipelinePreStyleCurrentOnlyReceipt(outputDir, manifest, frozen)
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return recoverPipelinePreStyleCurrentOnlyReceipt(outputDir, manifest, frozen)
	}
	var recovered *tools.EffectiveRenderStyleContractReceipt
	for _, entry := range entries {
		path := filepath.Join(archiveDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
			filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("pre-style effective-style archive namespace contains an invalid entry")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var candidate tools.EffectiveRenderStyleContractReceipt
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return nil, fmt.Errorf("decode pre-style archived receipt: %w", err)
		}
		rel, err := tools.EffectiveRenderStyleContractArchivePath(
			candidate.Chapter,
			candidate.CandidateID,
			candidate.ReceiptDigest,
		)
		if err != nil {
			return nil, err
		}
		verified, err := tools.LoadArchivedEffectiveRenderStyleContract(
			store.NewStore(outputDir), rel, manifest.Chapter, candidate.ReceiptDigest,
		)
		if err != nil {
			return nil, fmt.Errorf("validate pre-style archived receipt: %w", err)
		}
		if verified.GenerationID != frozen.PlanningGenerationID ||
			verified.Chapter != frozen.Chapter ||
			verified.PlanDigest != frozen.PlanDigest ||
			verified.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			verified.CandidateID != manifest.CandidateID ||
			verified.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			verified.BaseRenderContextSHA256 != frozen.RenderContextSHA256 ||
			verified.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
			verified.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
			return nil, fmt.Errorf("pre-style archived receipt differs from frozen candidate identity")
		}
		if recovered != nil && recovered.ReceiptDigest != verified.ReceiptDigest {
			return nil, fmt.Errorf("pre-style candidate has multiple incompatible effective-style archives")
		}
		recovered = verified
	}
	if recovered == nil {
		return nil, nil
	}
	currentPath := filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))
	if raw, err := os.ReadFile(currentPath); err == nil {
		var current tools.EffectiveRenderStyleContractReceipt
		if json.Unmarshal(raw, &current) == nil &&
			current.CandidateID == manifest.CandidateID &&
			current.ReceiptDigest != recovered.ReceiptDigest {
			return nil, fmt.Errorf("pre-style current receipt conflicts with immutable archive")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return recovered, nil
}

func recoverPipelinePreStyleCurrentOnlyReceipt(
	outputDir string,
	manifest pipelineRenderCandidateManifest,
	frozen *pipelineFrozenPlan,
) (*tools.EffectiveRenderStyleContractReceipt, error) {
	if frozen == nil {
		return nil, fmt.Errorf("pre-style current-only recovery requires frozen identity")
	}
	currentPath := filepath.Join(outputDir, filepath.FromSlash(tools.EffectiveRenderStyleContractPath))
	if _, err := os.Lstat(currentPath); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	recovered, _, err := tools.RecoverEffectiveRenderStyleContractArchiveFromCurrent(
		store.NewStore(outputDir),
		tools.EffectiveRenderStyleContractIdentity{
			GenerationID:              frozen.PlanningGenerationID,
			Chapter:                   frozen.Chapter,
			PlanDigest:                frozen.PlanDigest,
			PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
			BaseRenderContextSHA256:   frozen.RenderContextSHA256,
			PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
			ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
			PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
			CandidateID:               manifest.CandidateID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("recover pre-style archive from current receipt: %w", err)
	}
	return recovered, nil
}

// newerReusablePipelineRenderCandidate compares the durable body event before
// snapshot completeness. A committed/reviewed snapshot is useful cache
// evidence, but it must never make an older body outrank a later edit produced
// by the same sealed transaction after an operational retry.
func newerReusablePipelineRenderCandidate(
	candidate reusablePipelineRenderCandidate,
	current *reusablePipelineRenderCandidate,
) bool {
	if current == nil {
		return true
	}
	if candidate.bodyCheckpoint.Seq != current.bodyCheckpoint.Seq {
		return candidate.bodyCheckpoint.Seq > current.bodyCheckpoint.Seq
	}
	if !candidate.bodyCheckpoint.OccurredAt.Equal(current.bodyCheckpoint.OccurredAt) {
		return candidate.bodyCheckpoint.OccurredAt.After(current.bodyCheckpoint.OccurredAt)
	}
	if candidate.manifest.PreparedAt != current.manifest.PreparedAt {
		return candidate.manifest.PreparedAt > current.manifest.PreparedAt
	}
	return candidate.score > current.score
}

type pipelineRenderCandidateRecoveryReceipt struct {
	Version                  string                                   `json:"version"`
	Chapter                  int                                      `json:"chapter"`
	CandidateID              string                                   `json:"candidate_id"`
	SourceCandidateDir       string                                   `json:"source_candidate_dir"`
	SourceManifestVersion    string                                   `json:"source_manifest_version"`
	SourceLiveRoot           string                                   `json:"source_live_root"`
	SourceBodyCheckpointSeq  int64                                    `json:"source_body_checkpoint_seq"`
	SourceBodyCheckpointStep string                                   `json:"source_body_checkpoint_step"`
	SourceBodyCheckpointHash string                                   `json:"source_body_checkpoint_digest"`
	ReplayedBodyCheckpoint   int64                                    `json:"replayed_body_checkpoint_seq"`
	ReplayedBodyStep         string                                   `json:"replayed_body_checkpoint_step"`
	ReplayedBodyDigest       string                                   `json:"replayed_body_digest"`
	ExactJudgeReused         bool                                     `json:"exact_judge_reused"`
	ExactJudgeCacheReused    bool                                     `json:"exact_judge_cache_reused"`
	ExactJudgeProbability    int                                      `json:"exact_judge_probability_percent,omitempty"`
	ExactEditorCacheReused   bool                                     `json:"exact_editor_cache_reused"`
	LocalSoftChain           *tools.DraftLocalSoftEditRecoveryReceipt `json:"local_soft_chain,omitempty"`
	CurrentLiveRoot          string                                   `json:"current_live_root"`
	RecoveredAt              string                                   `json:"recovered_at"`
}

// recoverReusablePipelineRenderCandidate preserves exact draft bytes across a
// process crash without publishing an obsolete whole-directory snapshot. An
// active candidate or a mechanically retired stale/rejected candidate may be a
// source because older runners used "rejected" for transport failures too.
// Directory names are not semantic evidence: exact-body tombstones and durable
// convergence blocks remain quarantined. The source plan/body checkpoints must
// bind the exact frozen epoch. Recovery then deep-copies the current live
// directory and mechanically replays only the proven draft bytes.
// Runtime queues, hard-consistency receipts and telemetry are deliberately not
// copied. A provider judge may be restored only after its full cache identity
// and exact body binding validate; otherwise the ordinary render loop reruns it.
// The new candidate always receives a current CAS root.
func recoverReusablePipelineRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderCandidate, error) {
	root := pipelineRenderCandidateRoot(liveOutputDir)
	paths := []string{activeContainer}
	retiredRoot := filepath.Join(root, "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list retired render candidates: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			legacyRejected := strings.HasPrefix(entry.Name(), id+"-rejected-")
			mechanicallyStale := strings.HasPrefix(entry.Name(), id+"-stale-")
			if entry.IsDir() && (mechanicallyStale || legacyRejected) {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}

	var best *reusablePipelineRenderCandidate
	var reusable []reusablePipelineRenderCandidate
	for _, candidatePath := range paths {
		candidate, ok := inspectReusablePipelineRenderCandidate(
			candidatePath,
			liveOutputDir,
			frozen,
			id,
		)
		if !ok {
			continue
		}
		tombstoned, err := pipelineRenderBodyHasEffectiveSemanticRejection(
			liveOutputDir,
			frozen,
			id,
			reviewreport.BodySHA256(candidate.body),
		)
		if err != nil {
			return nil, fmt.Errorf("inspect reusable render candidate rejection: %w", err)
		}
		if tombstoned {
			continue
		}
		durablyRejected, err := pipelineRenderBodyHasDurableConvergenceRejection(
			liveOutputDir,
			frozen,
			id,
			reviewreport.BodySHA256(candidate.body),
		)
		if err != nil {
			return nil, fmt.Errorf("inspect reusable render candidate convergence rejection: %w", err)
		}
		if durablyRejected {
			continue
		}
		reusable = append(reusable, candidate)
		if newerReusablePipelineRenderCandidate(candidate, best) {
			copy := candidate
			best = &copy
		}
	}
	if best == nil {
		return nil, nil
	}
	// Review caches are immutable exact-input artifacts. A newer operational
	// attempt may contain the Editor half while an older same-body attempt keeps
	// the original DeepSeek response cache. Combine only self-validating caches
	// whose body checkpoint digest is exactly the chosen body; later review still
	// recomputes its policy key and will ignore any context-drifted artifact.
	for i := range reusable {
		candidate := &reusable[i]
		if candidate.bodyCheckpoint.Digest != best.bodyCheckpoint.Digest {
			continue
		}
		if candidate.judge != nil &&
			(best.judge == nil || (!reusableDeepSeekJudgeHasCache(best.judge) && reusableDeepSeekJudgeHasCache(candidate.judge))) {
			best.judge = candidate.judge
		}
		if best.editorCache == nil && candidate.editorCache != nil {
			best.editorCache = candidate.editorCache
		}
		if best.localSoftChain == nil && candidate.localSoftChain != nil {
			best.localSoftChain = candidate.localSoftChain
		}
	}
	livePlan, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(liveOutputDir), frozen.Chapter)
	if err != nil || livePlan.Digest != frozen.PlanDigest || livePlan.Seq != frozen.PlanCheckpointSeq {
		return nil, fmt.Errorf(
			"recover sealed render candidate requires live plan %s#%d (current=%v err=%v)",
			frozen.PlanDigest,
			frozen.PlanCheckpointSeq,
			livePlan,
			err,
		)
	}
	currentLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot current live root for render recovery: %w", err)
	}
	// If the reusable source is active, archive it before constructing the fresh
	// candidate. The stale source remains recoverable if fresh-copy/replay fails.
	if filepath.Clean(best.container) == filepath.Clean(activeContainer) {
		if err := retirePipelineRenderCandidate(activeContainer, "stale-recovery-source"); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(activeContainer); err == nil {
		if err := retirePipelineRenderCandidate(activeContainer, "stale-superseded-empty"); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	declareV3Epoch := best.manifest.Version == pipelineRenderCandidateManifestVersion ||
		best.manifest.Version == pipelineRenderCandidatePreStyleManifestVersion
	fresh, err := prepareFreshPipelineRenderCandidateForStyleEpoch(
		liveOutputDir,
		frozen,
		id,
		activeContainer,
		declareV3Epoch,
	)
	if err != nil {
		return nil, err
	}
	switch best.manifest.Version {
	case pipelineRenderCandidateManifestVersion:
		if best.styleReceipt == nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-style-receipt-missing")
			return nil, fmt.Errorf("v3 render recovery source lost its effective style receipt")
		}
		if err := restorePipelineRenderCandidateEffectiveStyleReceipt(fresh.OutputDir, best.styleReceipt); err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-style-receipt-failed")
			return nil, err
		}
	case pipelineRenderCandidatePreviousManifestVersion, pipelineRenderCandidateLegacyManifestVersion:
		if err := rewritePipelineRenderCandidateAsLegacyV2(fresh.OutputDir, frozen); err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-legacy-style-epoch-failed")
			return nil, err
		}
	default:
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-style-epoch-invalid")
		return nil, fmt.Errorf("render recovery source has unsupported style epoch %q", best.manifest.Version)
	}
	freshStore := store.NewStore(fresh.OutputDir)
	if err := freshStore.Drafts.SaveDraft(frozen.Chapter, best.body); err != nil {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-replay-failed")
		return nil, fmt.Errorf("replay recovered render draft: %w", err)
	}
	replayStep := best.bodyCheckpoint.Step
	if replayStep != "draft" && replayStep != "edit" {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-identity-failed")
		return nil, fmt.Errorf("recovered render body checkpoint has unsupported step %q", replayStep)
	}
	var replayed *domain.Checkpoint
	var localSoftReceipt *tools.DraftLocalSoftEditRecoveryReceipt
	if best.localSoftChain != nil {
		chainReceipt, chainReplayed, chainErr := tools.ReplayDraftLocalSoftEditRecoveryChain(
			freshStore,
			best.localSoftChain,
		)
		if chainErr != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-local-soft-chain-failed")
			return nil, fmt.Errorf("replay recovered local-soft edit chain: %w", chainErr)
		}
		if chainReplayed {
			localSoftReceipt = chainReceipt
			replayed, err = tools.CurrentChapterBodyCheckpoint(freshStore, frozen.Chapter)
			if err != nil {
				_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-local-soft-chain-failed")
				return nil, fmt.Errorf("verify recovered local-soft final edit: %w", err)
			}
		}
	}
	if replayed == nil {
		replayed, err = freshStore.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(frozen.Chapter),
			replayStep,
			fmt.Sprintf("drafts/%02d.draft.md", frozen.Chapter),
			"plan", "rerender-request", "draft", "edit",
		)
		if err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-checkpoint-failed")
			return nil, fmt.Errorf("checkpoint recovered render draft: %w", err)
		}
	}
	if replayed.Digest != best.bodyCheckpoint.Digest || replayed.Seq <= frozen.PlanCheckpointSeq {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-identity-failed")
		return nil, fmt.Errorf("replayed render draft did not preserve exact body identity")
	}
	judgeCacheReused := false
	if best.judge != nil {
		if err := saveDraftDeepSeekAIJudge(fresh.OutputDir, best.judge); err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-judge-failed")
			return nil, fmt.Errorf("restore exact-body DeepSeek judge: %w", err)
		}
		// Project-facing judge artifacts may intentionally omit raw_response.
		// Such an artifact is still valid exact-body gate evidence but cannot be
		// promoted into the model-response cache. Reuse the cache only when its
		// stricter payload contract validates; otherwise later formal review may
		// make a fresh provider call.
		if validateDeepSeekAIJudgeCacheArtifact(best.judge, best.judge.CachePolicy) == nil {
			if err := saveDeepSeekAIJudgeCache(fresh.OutputDir, best.judge); err != nil {
				_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-judge-cache-failed")
				return nil, fmt.Errorf("restore exact-body DeepSeek judge cache: %w", err)
			}
			judgeCacheReused = true
		}
	}
	if best.editorCache != nil {
		if err := saveEditorReviewCache(fresh.OutputDir, best.editorCache); err != nil {
			_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-editor-cache-failed")
			return nil, fmt.Errorf("restore exact-input Editor cache: %w", err)
		}
	}
	receipt := pipelineRenderCandidateRecoveryReceipt{
		Version:                  "pipeline-render-candidate-recovery.v1",
		Chapter:                  frozen.Chapter,
		CandidateID:              id,
		SourceCandidateDir:       filepath.Clean(best.container),
		SourceManifestVersion:    best.manifest.Version,
		SourceLiveRoot:           best.manifest.SourceLiveRoot,
		SourceBodyCheckpointSeq:  best.bodyCheckpoint.Seq,
		SourceBodyCheckpointStep: best.bodyCheckpoint.Step,
		SourceBodyCheckpointHash: best.bodyCheckpoint.Digest,
		ReplayedBodyCheckpoint:   replayed.Seq,
		ReplayedBodyStep:         replayed.Step,
		ReplayedBodyDigest:       replayed.Digest,
		ExactJudgeReused:         best.judge != nil,
		ExactJudgeCacheReused:    judgeCacheReused,
		ExactEditorCacheReused:   best.editorCache != nil,
		LocalSoftChain:           localSoftReceipt,
		CurrentLiveRoot:          currentLiveRoot,
		RecoveredAt:              time.Now().UTC().Format(time.RFC3339Nano),
	}
	if best.judge != nil {
		receipt.ExactJudgeProbability = best.judge.AIProbabilityPercent
	}
	receiptRaw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-receipt-failed")
		return nil, err
	}
	receiptRaw = append(receiptRaw, '\n')
	if err := atomicWriteRewriteFile(
		filepath.Join(fresh.OutputDir, "meta", "planning", "render_candidate_recovery.json"),
		receiptRaw,
		0o644,
	); err != nil {
		_ = retirePipelineRenderCandidate(fresh.ContainerDir, "recovery-receipt-failed")
		return nil, fmt.Errorf("save recovered render candidate receipt: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 从同一 sealed 身份重放 exact-body 候选（chapter=%d score=%d body=%s）\n",
		frozen.Chapter,
		best.score,
		best.bodyCheckpoint.Digest,
	)
	return fresh, nil
}

func inspectReusablePipelineRenderCandidate(
	container string,
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
) (reusablePipelineRenderCandidate, bool) {
	var result reusablePipelineRenderCandidate
	manifestPath := filepath.Join(container, "output", "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return result, false
	}
	var manifest pipelineRenderCandidateManifest
	if json.Unmarshal(raw, &manifest) != nil ||
		(manifest.Version != pipelineRenderCandidateManifestVersion &&
			manifest.Version != pipelineRenderCandidatePreStyleManifestVersion &&
			manifest.Version != pipelineRenderCandidatePreviousManifestVersion &&
			manifest.Version != pipelineRenderCandidateLegacyManifestVersion) ||
		manifest.CandidateID != id ||
		manifest.GenerationID != frozen.PlanningGenerationID ||
		manifest.Chapter != frozen.Chapter ||
		manifest.PlanDigest != frozen.PlanDigest ||
		manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		filepath.Clean(manifest.SourceOutputDir) != filepath.Clean(liveOutputDir) {
		return result, false
	}
	var styleReceipt *tools.EffectiveRenderStyleContractReceipt
	if manifest.Version == pipelineRenderCandidateManifestVersion {
		if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			!pipelineRenderInputSHA256(manifest.PipelineRenderInputDigest) ||
			!pipelineRenderInputSHA256(manifest.RenderContextSHA256) ||
			!pipelineRenderInputSHA256(manifest.EffectiveStyleReceiptDigest) {
			return result, false
		}
		receipt, receiptErr := loadPipelineRenderCandidateEffectiveStyleReceipt(
			filepath.Join(container, "output"),
			manifest,
			frozen,
		)
		if receiptErr != nil || receipt == nil ||
			receipt.ReceiptDigest != manifest.EffectiveStyleReceiptDigest ||
			receipt.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
			receipt.BaseRenderContextSHA256 != manifest.RenderContextSHA256 {
			return result, false
		}
		styleReceipt = receipt
	} else if manifest.Version == pipelineRenderCandidatePreStyleManifestVersion {
		if manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != frozen.PipelineRunInputDigest ||
			manifest.RenderContextSHA256 != frozen.RenderContextSHA256 ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			return result, false
		}
		// pre-style is a mechanical construction state. Any prose checkpoint in
		// such a tree predates the immutable style binding and must never be
		// recovered or retroactively attributed to a later receipt.
		return result, false
	} else if manifest.Version == pipelineRenderCandidatePreviousManifestVersion &&
		(manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
			strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return result, false
	} else if manifest.Version == pipelineRenderCandidateLegacyManifestVersion &&
		(strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return result, false
	}
	output := filepath.Join(container, "output")
	st := store.NewStore(output)
	plan, err := tools.CurrentChapterPlanCausalCheckpoint(st, frozen.Chapter)
	if err != nil || frozen.PlanCheckpointSeq <= 0 ||
		plan.Digest != frozen.PlanDigest || plan.Seq != frozen.PlanCheckpointSeq {
		return result, false
	}
	draft, err := st.Drafts.LoadDraft(frozen.Chapter)
	if err != nil || strings.TrimSpace(draft) == "" {
		return result, false
	}
	body, err := tools.CurrentChapterBodyCheckpoint(st, frozen.Chapter)
	if err != nil || body.Seq <= frozen.PlanCheckpointSeq {
		return result, false
	}
	judge := reusablePipelineRenderCandidateJudge(output, frozen.Chapter, draft, body)
	editorCache := reusablePipelineRenderCandidateEditorCache(output, frozen.Chapter, draft)
	localSoftChain := tools.InspectDraftLocalSoftEditRecoveryChain(st, frozen.Chapter)
	score := 2
	if _, err := loadPipelineRenderedChapterSnapshot(output, frozen, plan); err == nil {
		score = 3
	}
	return reusablePipelineRenderCandidate{
		container:      container,
		manifest:       manifest,
		styleReceipt:   styleReceipt,
		body:           draft,
		bodyCheckpoint: *body,
		judge:          judge,
		editorCache:    editorCache,
		localSoftChain: localSoftChain,
		score:          score,
	}, true
}

// reusablePipelineRenderCandidateJudge returns only a self-authenticating
// provider artifact whose cache policy, model identity and exact body hash all
// validate under the current review protocol. A malformed, blocking, stale or
// threshold-edge artifact is treated as absent and the ordinary outer judge is
// run again; recovery never copies a raw status file on trust alone.
func reusablePipelineRenderCandidateJudge(
	outputDir string,
	chapter int,
	body string,
	bodyCheckpoint *domain.Checkpoint,
) *deepseekAIJudgeArtifact {
	if bodyCheckpoint == nil || chapter <= 0 || strings.TrimSpace(body) == "" {
		return nil
	}
	path := filepath.Join(
		outputDir,
		"reviews",
		"drafts",
		fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter),
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var artifact deepseekAIJudgeArtifact
	if json.Unmarshal(raw, &artifact) != nil ||
		validateDeepSeekAIJudgeArtifactIdentity(&artifact, artifact.CachePolicy) != nil ||
		artifact.Chapter != chapter ||
		artifact.BodySHA256 != reviewreport.BodySHA256(body) ||
		bodyCheckpoint.Digest != "sha256:"+artifact.BodySHA256 ||
		!artifact.AdviceComplete || artifact.Blocking ||
		artifact.AIProbabilityPercent >= deepseekAIJudgePassExclusive {
		return nil
	}
	if cached, err := loadDeepSeekAIJudgeCache(outputDir, artifact.CachePolicy); err == nil &&
		cached != nil && cached.Chapter == chapter &&
		cached.BodySHA256 == artifact.BodySHA256 &&
		cached.AdviceComplete && !cached.Blocking &&
		cached.AIProbabilityPercent < deepseekAIJudgePassExclusive {
		return cached
	}
	return &artifact
}

func reusableDeepSeekJudgeHasCache(artifact *deepseekAIJudgeArtifact) bool {
	return artifact != nil &&
		validateDeepSeekAIJudgeCacheArtifact(artifact, artifact.CachePolicy) == nil
}

func reusablePipelineRenderCandidateEditorCache(
	outputDir string,
	chapter int,
	body string,
) *editorReviewCacheArtifact {
	dir := filepath.Join(outputDir, "reviews", "cache", editorReviewCacheBranch)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	bodySHA := reviewreport.BodySHA256(body)
	var best *editorReviewCacheArtifact
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var artifact editorReviewCacheArtifact
		if json.Unmarshal(raw, &artifact) != nil ||
			validateEditorReviewCacheArtifact(&artifact, artifact.CachePolicy) != nil ||
			artifact.Chapter != chapter || artifact.CachePolicy.BodySHA256 != bodySHA {
			continue
		}
		copy := artifact
		best = &copy
	}
	return best
}

func copyPipelineRenderCandidateTree(source, target string) error {
	source = filepath.Clean(source)
	target = filepath.Clean(target)
	if source == target || pathContainsPipelineRenderCandidate(source, target) {
		return fmt.Errorf("render candidate target must be outside live output: live=%s target=%s", source, target)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0o755)
		}
		dst := filepath.Join(target, rel)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("render candidate refuses symlink %s", path)
		}
		if pipelineAtomicWriteTempName(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("render candidate refuses non-regular file %s", path)
		}
		// Never hard-link a candidate: the writer and review stages update many
		// append-only ledgers, and a shared inode would violate canon isolation.
		return copyProjectAllFile(path, dst, info.Mode().Perm())
	})
}

func pathContainsPipelineRenderCandidate(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func retirePipelineRenderCandidate(container, reason string) error {
	if _, err := os.Stat(container); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := filepath.Dir(container)
	retiredRoot := filepath.Join(root, "retired")
	if err := os.MkdirAll(retiredRoot, 0o755); err != nil {
		return err
	}
	base := filepath.Base(container)
	reason = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.TrimSpace(reason))
	if reason == "" {
		reason = "retired"
	}
	target := filepath.Join(
		retiredRoot,
		fmt.Sprintf("%s-%s-%s", base, reason, time.Now().UTC().Format("20060102T150405.000000000Z")),
	)
	if err := os.Rename(container, target); err != nil {
		return fmt.Errorf("retire render candidate: %w", err)
	}
	return nil
}

func loadPipelineRenderedChapterSnapshot(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (*pipelineRenderedChapterSnapshot, error) {
	if frozen == nil || planCheckpoint == nil {
		return nil, fmt.Errorf("rendered chapter snapshot requires frozen plan and plan checkpoint")
	}
	st := store.NewStore(outputDir)
	currentPlan, err := tools.CurrentChapterPlanCausalCheckpoint(st, frozen.Chapter)
	if err != nil {
		return nil, fmt.Errorf("render 后正式计划不可验证: %w", err)
	}
	if currentPlan.Digest != frozen.PlanDigest || currentPlan.Seq != planCheckpoint.Seq {
		return nil, fmt.Errorf(
			"render 期间第 %d 章正式计划漂移（frozen=%s#%d current=%s#%d）",
			frozen.Chapter,
			frozen.PlanDigest,
			planCheckpoint.Seq,
			currentPlan.Digest,
			currentPlan.Seq,
		)
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(frozen.Chapter), "commit")
	if commit == nil || commit.Seq <= frozen.BaselineCommitSeq {
		return nil, fmt.Errorf(
			"render 第 %d 章没有产生晚于冻结基线 #%d 的 commit checkpoint",
			frozen.Chapter,
			frozen.BaselineCommitSeq,
		)
	}
	chapterPath := fmt.Sprintf("chapters/%02d.md", frozen.Chapter)
	bodyPath := filepath.Join(outputDir, filepath.FromSlash(chapterPath))
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return nil, fmt.Errorf("render 读取正文: %w", err)
	}
	bodySHA, err := pipelineRequiredFileSHA(outputDir, chapterPath)
	if err != nil {
		return nil, fmt.Errorf("render 验证正文: %w", err)
	}
	if commit.Artifact != chapterPath || commit.Digest != bodySHA {
		return nil, fmt.Errorf(
			"render 第 %d 章 commit checkpoint 未绑定当前正文（artifact=%q digest=%s current=%s）",
			frozen.Chapter,
			commit.Artifact,
			commit.Digest,
			bodySHA,
		)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, fmt.Errorf("render 读取提交后 progress: %w", err)
	}
	actualCanonRoot, err := pipelineCanonRoot(outputDir, progress)
	if err != nil {
		return nil, fmt.Errorf("render 计算提交后 canon root: %w", err)
	}
	snapshot := &pipelineRenderedChapterSnapshot{
		Store:           st,
		Commit:          commit,
		ChapterPath:     chapterPath,
		Body:            string(body),
		BodySHA256:      bodySHA,
		ActualCanonRoot: actualCanonRoot,
	}
	if err := pipelineWatchdogProgressBody(
		pipelineWatchdogEventRenderBodyCommitted,
		bodySHA,
	); err != nil {
		return nil, fmt.Errorf("record rendered chapter commit progress: %w", err)
	}
	return snapshot, nil
}

// pipelineRenderCandidateHasCurrentExactDeepSeekReviewCache proves that sealed
// post-commit recovery can refresh Editor without ever re-calling DeepSeek.
// Editor cache identity is intentionally not checked here: protocol, prompt or
// deterministic-rule drift is exactly why the same immutable body needs a
// current formal review before Writer is allowed to replace it.
func pipelineRenderCandidateHasCurrentExactDeepSeekReviewCache(
	cfg bootstrap.Config,
	chapter int,
	body string,
) (bool, error) {
	if chapter <= 0 || strings.TrimSpace(body) == "" {
		return false, nil
	}
	st := store.NewStore(cfg.OutputDir)
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return false, err
	}
	reviewerProvider, reviewerModel, reviewerExplicit := models.CurrentSelection("reviewer")
	reviewerSelection := deepseekAIJudgeModelSelection{
		Provider: reviewerProvider,
		Model:    reviewerModel,
		Explicit: reviewerExplicit,
	}
	deepseekCache, err := loadDeepSeekAIJudgeCache(
		cfg.OutputDir,
		newDeepSeekAIJudgeCachePolicy(reviewerSelection, chapter, body),
	)
	if err != nil || deepseekCache == nil {
		return false, err
	}
	if deepseekCache.Blocking ||
		deepseekCache.AIProbabilityPercent >= deepseekAIJudgePassExclusive ||
		!deepseekCache.AdviceComplete {
		return false, nil
	}
	// review-existing sanitizes the cached response before saving it. If that
	// would empty its advice, review-existing intentionally calls the model
	// again; such a cache is not eligible for this zero-call recovery path.
	probe := *deepseekCache
	sanitizeDeepSeekAIJudgeForProject(st, &probe)
	return probe.AdviceComplete, nil
}

func tryPipelineRenderReviewFirstFormalRevalidation(
	opts cliOptions,
	flags pipelineFlags,
	liveOutputDir string,
	candidateCfg bootstrap.Config,
	candidate *pipelineRenderCandidate,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	matchBody pipelineSealedActualBodyEvidenceMatchFunc,
) (*pipelineRenderedChapterSnapshot, bool, error) {
	st := store.NewStore(candidate.OutputDir)
	ledger, err := syncPipelineRenderConvergence(st)
	if err != nil || ledger == nil {
		return nil, false, err
	}
	body, err := st.Drafts.LoadDraft(frozen.Chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return nil, false, err
	}
	bodySHA := reviewreport.BodySHA256(body)
	currentGate, err := tools.InspectDraftExternalGateWithStore(st, frozen.Chapter)
	if err != nil {
		return nil, false, fmt.Errorf("inspect current exact-body gate before formal revalidation: %w", err)
	}
	pending := false
	for _, record := range ledger.Records {
		if pipelineRenderRecordNeedsCachedFormalRevalidation(record, bodySHA, currentGate.Status) {
			pending = true
			break
		}
	}
	if !pending || currentGate.Status != tools.DraftExternalGateApproved ||
		!currentGate.ArtifactExists || currentGate.CurrentBodySHA256 != bodySHA ||
		currentGate.EvaluatedBodySHA256 != bodySHA ||
		!pipelineRenderCandidateHasExactDurableCommit(
			candidate.OutputDir,
			frozen,
			planCheckpoint,
		) {
		return nil, false, nil
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, planCheckpoint)
	if err != nil || reviewreport.BodySHA256(snapshot.Body) != bodySHA {
		return nil, false, err
	}
	if err := runPipelineSealedActualBodyEvidencePreflight(
		candidate.OutputDir,
		snapshot,
		matchBody,
	); err != nil {
		return nil, false, err
	}
	deepseekCacheOnly, err := pipelineRenderCandidateHasCurrentExactDeepSeekReviewCache(
		candidateCfg,
		frozen.Chapter,
		snapshot.Body,
	)
	if err != nil {
		return nil, false, err
	}
	if !deepseekCacheOnly {
		return nil, false, fmt.Errorf(
			"render 第 %d 章 review-first 缺少 current exact-body DeepSeek pass cache；禁止回源或落入 Writer",
			frozen.Chapter,
		)
	}

	reviewArgs := []string{"--from", fmt.Sprint(frozen.Chapter), "--to", fmt.Sprint(frozen.Chapter)}
	if flags.Budget > 0 {
		reviewArgs = append(reviewArgs, "--budget", flags.Budget.String())
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 第 %d 章 exact-body 已提交且 DeepSeek gate/cache 已批准；先做 current Editor formal revalidation，暂不调用 Writer\n",
		frozen.Chapter,
	)
	reviewStarted := time.Now()
	reviewErr := reviewExistingPipelineAtOutputWithPolicy(
		opts,
		reviewArgs,
		candidate.OutputDir,
		true,
		reviewExistingRunPolicy{RequireCurrentDeepSeekCache: true},
	)
	reviewStatus := "ok"
	if reviewErr != nil {
		reviewStatus = "error"
	}
	recordPipelineChapterTiming(
		candidate.OutputDir,
		newPipelineTimingInvocationID(reviewStarted),
		"formal_review_revalidation",
		frozen.Chapter,
		1,
		reviewStarted,
		flags.Budget,
		reviewStatus,
		reviewErr,
	)
	if reviewErr != nil {
		return nil, false, fmt.Errorf("render 第 %d 章 review-first formal revalidation 失败: %w", frozen.Chapter, reviewErr)
	}
	inspection := inspectCurrentChapterReview(candidate.OutputDir, frozen.Chapter)
	if len(inspection.Issues) > 0 {
		return nil, false, fmt.Errorf(
			"render 第 %d 章 review-first 未产生 fresh exact-body formal review: %s",
			frozen.Chapter, strings.Join(inspection.Issues, "；"),
		)
	}
	if err := pipelineWatchdogProgressBody(
		pipelineWatchdogEventRenderFormalReviewed,
		snapshot.BodySHA256,
	); err != nil {
		return nil, false, fmt.Errorf("record review-first formal review progress: %w", err)
	}
	if !pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) {
		if _, ledgerErr := recordPipelineRenderSemanticRejection(
			liveOutputDir,
			candidate.OutputDir,
			frozen,
			bodySHA,
		); ledgerErr != nil {
			return nil, false, ledgerErr
		}
		return nil, false, nil
	}
	ledger, err = syncPipelineRenderConvergence(store.NewStore(candidate.OutputDir))
	if err != nil {
		return nil, false, err
	}
	resolved := false
	for _, record := range ledger.Records {
		if record.BodySHA256 == bodySHA && record.SemanticReject && record.FormalAccepted {
			resolved = true
			break
		}
	}
	if !resolved {
		return nil, false, fmt.Errorf("render 第 %d 章 formal accept 未写入持久化收敛账本", frozen.Chapter)
	}
	snapshot, err = loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, planCheckpoint)
	if err != nil {
		return nil, false, err
	}
	return snapshot, true, nil
}

// pipelineRenderRecordNeedsCachedFormalRevalidation keeps historical failure
// flags immutable while deciding whether they still have current routing
// authority. Older builds could combine a calibratable probability proxy with
// a local structural flag. Once the same bytes now have an exact-body approved
// gate, that historical bit must not force Writer to replace an otherwise
// committed, double-cached chapter; formal review is the only remaining step.
func pipelineRenderRecordNeedsCachedFormalRevalidation(
	record pipelineRenderConvergenceRecord,
	bodySHA string,
	currentGateStatus tools.DraftExternalGateStatus,
) bool {
	if record.BodySHA256 != bodySHA || !record.SemanticReject || record.FormalAccepted || record.ExternalBlocking {
		return false
	}
	return !record.StructuralBlock || currentGateStatus == tools.DraftExternalGateApproved
}

func runPipelineSealedRenderCandidate(
	opts cliOptions,
	flags pipelineFlags,
	state *domain.PipelineState,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	matchBody pipelineSealedActualBodyEvidenceMatchFunc,
	legacyRenderInputDrift bool,
) (_ *pipelineRenderCandidate, _ *pipelineRenderedChapterSnapshot, returnErr error) {
	candidate, err := preparePipelineRenderCandidate(cfg.OutputDir, frozen)
	if err != nil {
		return nil, nil, err
	}
	semanticReviewRejected := false
	defer func() {
		if returnErr != nil {
			// Budget exhaustion is a successful convergence decision, not a bad
			// candidate. Keep the exact body, all judgments and the formal rewrite
			// feedback active for the explicitly requested plan stage.
			if pipelineRenderRequiresPlanStage(returnErr) {
				return
			}
			reason := "actual-mismatch"
			if !errors.Is(returnErr, errPipelineSealedActualBodyEvidenceMismatch) {
				reason = pipelineRenderCandidateErrorRetirementReason(
					candidate.OutputDir,
					frozen,
					planCheckpoint,
					semanticReviewRejected,
				)
			}
			_ = retirePipelineRenderCandidate(candidate.ContainerDir, reason)
		}
	}()
	if err := pipelineWatchdogProgress(pipelineWatchdogEventRenderCandidatePrepared); err != nil {
		return nil, nil, fmt.Errorf("record sealed render candidate preparation progress: %w", err)
	}
	if err := bindCurrentRenderExecutionToCandidate(cfg.OutputDir, candidate, frozen); err != nil {
		return nil, nil, err
	}
	styleProtocol, err := pipelineRenderCandidateStyleProtocol(candidate.OutputDir, frozen)
	if err != nil {
		return nil, nil, err
	}
	if styleProtocol == pipelineRenderCandidateManifestVersion ||
		styleProtocol == pipelineRenderCandidatePreStyleManifestVersion {
		resolvedStyle := bundle.ResolveStyle(cfg.Style)
		styleReceipt, publishErr := tools.PublishEffectiveRenderStyleContract(
			store.NewStore(candidate.OutputDir),
			tools.EffectiveRenderStyleContractIdentity{
				GenerationID:              frozen.PlanningGenerationID,
				Chapter:                   frozen.Chapter,
				PlanDigest:                frozen.PlanDigest,
				PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
				BaseRenderContextSHA256:   frozen.RenderContextSHA256,
				PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
				ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
				PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
				CandidateID:               candidate.ID,
			},
			resolvedStyle.ID,
			resolvedStyle.Body,
		)
		if publishErr != nil {
			return nil, nil, fmt.Errorf("materialize sealed effective style contract: %w", publishErr)
		}
		if err := bindPipelineRenderCandidateEffectiveStyle(candidate.OutputDir, frozen, styleReceipt); err != nil {
			return nil, nil, err
		}
	}
	candidateCfg := cfg
	candidateCfg.OutputDir = candidate.OutputDir
	candidateCfg.DisableLiveRAG = true
	renderFlags := flags
	renderFlags.WriteTo = frozen.Chapter
	renderFlags.StopAfterCommit = frozen.Chapter
	renderFlags.RenderOnly = true
	var snapshot *pipelineRenderedChapterSnapshot
	if recoveredSnapshot, formalAccepted, recovered, recoveryErr := loadPipelineRecoveredChapterTransactionSnapshot(
		candidate,
		cfg.OutputDir,
		frozen,
		planCheckpoint,
	); recoveryErr != nil {
		return nil, nil, recoveryErr
	} else if recovered {
		snapshot = recoveredSnapshot
		if formalAccepted {
			fmt.Fprintf(
				os.Stderr,
				"[pipeline:render] 第 %d 章事务已封存 exact-body formal accept；跳过 Writer/Reviewer，继续 deterministic matcher\n",
				frozen.Chapter,
			)
			return candidate, snapshot, nil
		}
		if legacyRenderInputDrift {
			return nil, nil, fmt.Errorf(
				"render 第 %d 章 legacy input 已漂移；committed 候选仍需新 formal-review provider，必须恢复旧输入或显式重跑 plan",
				frozen.Chapter,
			)
		}
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:render] 第 %d 章事务已封存 exact-body commit；跳过 Writer，继续 deterministic/formal review\n",
			frozen.Chapter,
		)
	}
	if snapshot == nil {
		if recoveredSnapshot, formalAccepted, recovered, recoveryErr := loadPipelineRecoveredDurableCommitSnapshot(
			candidate,
			cfg.OutputDir,
			frozen,
			planCheckpoint,
		); recoveryErr != nil {
			return nil, nil, recoveryErr
		} else if recovered {
			snapshot = recoveredSnapshot
			if formalAccepted {
				fmt.Fprintf(
					os.Stderr,
					"[pipeline:render] 第 %d 章 durable commit 与 formal accept 已恢复；跳过 Writer/Reviewer\n",
					frozen.Chapter,
				)
				return candidate, snapshot, nil
			}
			if legacyRenderInputDrift {
				return nil, nil, fmt.Errorf(
					"render 第 %d 章 legacy input 已漂移；durable commit 仍需新 formal-review provider，必须恢复旧输入或显式重跑 plan",
					frozen.Chapter,
				)
			}
			fmt.Fprintf(
				os.Stderr,
				"[pipeline:render] 第 %d 章 durable commit 已恢复；跳过 Writer，继续 deterministic/formal review\n",
				frozen.Chapter,
			)
		}
	}
	if snapshot == nil {
		if recoveredSnapshot, recovered, recoveryErr := loadPipelineRecoveredActualMismatchSnapshot(
			candidate,
			frozen,
			planCheckpoint,
		); recoveryErr != nil {
			return nil, nil, recoveryErr
		} else if recovered {
			if err := pipelineAdvanceChapterRenderFormal(
				cfg.OutputDir, candidate.OutputDir, frozen, planCheckpoint, recoveredSnapshot, true,
			); err != nil {
				return nil, nil, err
			}
			fmt.Fprintf(
				os.Stderr,
				"[pipeline:render] 第 %d 章 exact-body formal accept 已恢复；跳过 Writer/Reviewer，继续重新执行 sealed actual matcher\n",
				frozen.Chapter,
			)
			return candidate, recoveredSnapshot, nil
		}
		if legacyRenderInputDrift {
			return nil, nil, fmt.Errorf(
				"render 第 %d 章 legacy model/provider/prompt 输入已漂移，且没有可免 provider 收口的 formal-accepted exact body；必须恢复旧输入或显式重跑 plan",
				frozen.Chapter,
			)
		}
		if revalidated, accepted, revalidationErr := tryPipelineRenderReviewFirstFormalRevalidation(
			opts,
			renderFlags,
			cfg.OutputDir,
			candidateCfg,
			candidate,
			frozen,
			planCheckpoint,
			matchBody,
		); revalidationErr != nil {
			return nil, nil, revalidationErr
		} else if accepted {
			if err := pipelineAdvanceChapterRenderFormal(
				cfg.OutputDir, candidate.OutputDir, frozen, planCheckpoint, revalidated, true,
			); err != nil {
				return nil, nil, err
			}
			return candidate, revalidated, nil
		}
		// Frozen preflight may defer an exhausted semantic-only ledger just long
		// enough to attempt the cache-only revalidation above. This strict check is
		// the barrier that prevents Writer from running when the cache is absent or
		// the normalized decision remains a reject.
		if convergenceErr := requirePipelineRenderConvergenceAvailable(
			store.NewStore(candidate.OutputDir),
		); convergenceErr != nil {
			return nil, nil, convergenceErr
		}
		writeErr := pipelineWriteConfigured(opts, renderFlags, state, candidateCfg, bundle)
		if _, syncErr := syncPipelineRenderConvergence(store.NewStore(candidate.OutputDir)); syncErr != nil {
			return nil, nil, fmt.Errorf("render 第 %d 章持久化同 plan 收敛账本失败: %w", frozen.Chapter, syncErr)
		}
		if convergenceErr := requirePipelineRenderConvergenceAvailable(store.NewStore(candidate.OutputDir)); convergenceErr != nil {
			return nil, nil, convergenceErr
		}
		if writeErr != nil {
			return nil, nil, fmt.Errorf(
				"render 第 %d 章候选失败（live canon 未变；render lock 已禁止临时重规划）: %w",
				frozen.Chapter,
				writeErr,
			)
		}
		snapshot, err = loadPipelineRenderedChapterSnapshot(
			candidate.OutputDir,
			frozen,
			planCheckpoint,
		)
		if err != nil {
			return nil, nil, err
		}
		if _, err := pipelineEnsureChapterRenderCommitted(
			cfg.OutputDir, candidate.OutputDir, frozen, planCheckpoint, snapshot,
		); err != nil {
			return nil, nil, err
		}
	}
	reviewArgs := []string{"--from", fmt.Sprint(frozen.Chapter), "--to", fmt.Sprint(frozen.Chapter)}
	if flags.Budget > 0 {
		reviewArgs = append(reviewArgs, "--budget", flags.Budget.String())
	}
	if err := runPipelineSealedFormalReviewAfterBodyEvidence(
		candidate.OutputDir,
		snapshot,
		matchBody,
		func() error {
			reviewStarted := time.Now()
			reviewErr := reviewExistingPipelineAtOutput(
				opts,
				reviewArgs,
				candidate.OutputDir,
				true,
			)
			reviewStatus := "ok"
			if reviewErr != nil {
				reviewStatus = "error"
			}
			recordPipelineChapterTiming(
				candidate.OutputDir,
				newPipelineTimingInvocationID(reviewStarted),
				"formal_review",
				frozen.Chapter,
				1,
				reviewStarted,
				flags.Budget,
				reviewStatus,
				reviewErr,
			)
			return reviewErr
		},
	); err != nil {
		if errors.Is(err, errPipelineSealedActualBodyEvidenceMismatch) {
			if txnErr := pipelineAdvanceChapterRenderStructuralBlock(
				cfg.OutputDir,
				candidate.OutputDir,
				frozen,
				planCheckpoint,
				snapshot,
			); txnErr != nil {
				return nil, nil, fmt.Errorf(
					"render 第 %d 章保存 structural-block 事务失败: %w",
					frozen.Chapter,
					txnErr,
				)
			}
			return nil, nil, fmt.Errorf(
				"render 第 %d 章候选 deterministic body-evidence preflight 未实现 sealed projected delta（formal review 未调用；live canon 未变）: %w",
				frozen.Chapter,
				err,
			)
		}
		return nil, nil, fmt.Errorf(
			"render 第 %d 章候选 fresh exact-body review 失败（live canon 未变）: %w",
			frozen.Chapter,
			err,
		)
	}
	if err := requirePipelineAcceptedExactReview(candidate.OutputDir, frozen.Chapter); err != nil {
		inspection := inspectCurrentChapterReview(candidate.OutputDir, frozen.Chapter)
		tombstoned, tombstoneErr := persistPipelineRenderSemanticRejection(
			cfg.OutputDir,
			candidate,
			frozen,
			snapshot,
			inspection,
		)
		if tombstoneErr != nil {
			return nil, nil, fmt.Errorf(
				"render 第 %d 章候选语义拒绝 tombstone 持久化失败（live canon 未变；review=%v）: %w",
				frozen.Chapter,
				err,
				tombstoneErr,
			)
		}
		semanticReviewRejected = tombstoned
		if tombstoned {
			if txnErr := pipelineAdvanceChapterRenderFormal(
				cfg.OutputDir,
				candidate.OutputDir,
				frozen,
				planCheckpoint,
				snapshot,
				false,
			); txnErr != nil {
				return nil, nil, fmt.Errorf(
					"render 第 %d 章保存 formal-rejected 事务失败: %w",
					frozen.Chapter,
					txnErr,
				)
			}
		}
		if tombstoned {
			ledger, ledgerErr := recordPipelineRenderSemanticRejection(
				cfg.OutputDir,
				candidate.OutputDir,
				frozen,
				reviewreport.BodySHA256(snapshot.Body),
			)
			if ledgerErr != nil {
				return nil, nil, fmt.Errorf(
					"render 第 %d 章正式拒稿反馈写入同 plan 收敛账本失败: %w",
					frozen.Chapter,
					ledgerErr,
				)
			}
			if convergenceErr := pipelineRenderConvergenceError(ledger); convergenceErr != nil {
				return nil, nil, convergenceErr
			}
		}
		return nil, nil, fmt.Errorf(
			"render 第 %d 章候选未通过 fresh exact-body accept；sealed generation 保持在本章，候选已隔离，必须只重渲染当前冻结计划: %w",
			frozen.Chapter,
			err,
		)
	}
	// Reload after review because the reviewer writes exact-body checkpoints and
	// quality artifacts into the candidate tree that will be promoted together.
	snapshot, err = loadPipelineRenderedChapterSnapshot(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := pipelineAdvanceChapterRenderFormal(
		cfg.OutputDir,
		candidate.OutputDir,
		frozen,
		planCheckpoint,
		snapshot,
		true,
	); err != nil {
		return nil, nil, err
	}
	return candidate, snapshot, nil
}

func loadPipelineRecoveredActualMismatchSnapshot(
	candidate *pipelineRenderCandidate,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (*pipelineRenderedChapterSnapshot, bool, error) {
	if candidate == nil || !candidate.RecoveredAcceptedActualMismatch {
		return nil, false, nil
	}
	if frozen == nil || planCheckpoint == nil {
		return nil, false, fmt.Errorf("accepted actual-mismatch recovery requires frozen plan and checkpoint")
	}
	if !pipelineRenderCandidateHasExactDurableCommit(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
	) {
		return nil, false, fmt.Errorf(
			"render 第 %d 章 accepted actual-mismatch recovery 丢失 exact durable commit",
			frozen.Chapter,
		)
	}
	if err := requirePipelineAcceptedExactReview(candidate.OutputDir, frozen.Chapter); err != nil {
		return nil, false, fmt.Errorf(
			"render 第 %d 章 accepted actual-mismatch recovery review 漂移: %w",
			frozen.Chapter,
			err,
		)
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
	)
	if err != nil {
		return nil, false, fmt.Errorf(
			"render 第 %d 章 accepted actual-mismatch recovery snapshot 漂移: %w",
			frozen.Chapter,
			err,
		)
	}
	if err := pipelineWatchdogProgressBody(
		pipelineWatchdogEventRenderFormalReviewed,
		snapshot.BodySHA256,
	); err != nil {
		return nil, false, fmt.Errorf("record recovered formal review progress: %w", err)
	}
	return snapshot, true, nil
}

func pipelineRenderCandidateHasExactDurableCommit(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) bool {
	if frozen == nil || planCheckpoint == nil {
		return false
	}
	st := store.NewStore(outputDir)
	plan, err := tools.CurrentChapterPlanCausalCheckpoint(st, frozen.Chapter)
	if err != nil || plan.Digest != frozen.PlanDigest ||
		plan.Seq != frozen.PlanCheckpointSeq || plan.Seq != planCheckpoint.Seq {
		return false
	}
	commit := st.Checkpoints.LatestByStep(domain.ChapterScope(frozen.Chapter), "commit")
	if commit == nil || commit.Seq <= frozen.BaselineCommitSeq {
		return false
	}
	chapterPath := fmt.Sprintf("chapters/%02d.md", frozen.Chapter)
	bodySHA, err := pipelineRequiredFileSHA(outputDir, chapterPath)
	if err != nil || commit.Artifact != chapterPath || commit.Digest != bodySHA {
		return false
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return false
	}
	for _, chapter := range progress.CompletedChapters {
		if chapter == frozen.Chapter {
			return true
		}
	}
	return false
}

// pipelineRenderCandidateErrorRetirementReason distinguishes a durable body
// rejection from an operational interruption. Provider timeouts and transport
// failures often lose their typed cause while review branches are aggregated,
// so the decision is intentionally based on exact-body durable evidence rather
// than error text. Every stale-* directory remains eligible for exact-body
// recovery on the next invocation; only a proven rejection is quarantined.
func pipelineRenderCandidateErrorRetirementReason(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	semanticReviewRejected bool,
) string {
	if semanticReviewRejected || pipelineRenderCandidateHasDurableBodyRejection(outputDir, frozen) {
		return "rejected"
	}
	if pipelineRenderCandidateHasExactDurableCommit(outputDir, frozen, planCheckpoint) {
		return "stale-postcommit"
	}
	return "stale-operational-error"
}

func pipelineRenderCandidateHasDurableBodyRejection(
	outputDir string,
	frozen *pipelineFrozenPlan,
) bool {
	if frozen == nil {
		return false
	}
	manifest, err := loadPipelineRenderCandidateManifest(outputDir)
	if err != nil || manifest == nil {
		return false
	}
	wantID, err := pipelineRenderTransactionID(frozen)
	if err != nil || manifest.CandidateID != wantID ||
		manifest.GenerationID != frozen.PlanningGenerationID ||
		manifest.Chapter != frozen.Chapter ||
		manifest.PlanDigest != frozen.PlanDigest ||
		manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return false
	}
	st := store.NewStore(outputDir)
	body, err := st.Drafts.LoadDraft(frozen.Chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return false
	}
	ledger, err := loadPipelineRenderConvergenceLedger(
		filepath.Clean(manifest.SourceOutputDir),
		*manifest,
		pipelineRenderConvergenceLimit(st, frozen.Chapter),
	)
	if err != nil {
		return false
	}
	bodySHA256 := reviewreport.BodySHA256(body)
	for _, record := range ledger.Records {
		if record.BodySHA256 != bodySHA256 {
			continue
		}
		return record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted)
	}
	return false
}

// bindCurrentRenderExecutionToCandidate refreshes the isolated candidate with
// the exact live render lease owned by this process. A fresh candidate normally
// receives it through the deep copy, but explicitly rebinding closes the crash
// recovery window where a prior PID's lease is cleaned on first tool access.
// Reusing the live Owner is required because the candidate directory later
// becomes live and pipelineRender's deferred release must still own the lease.
func bindCurrentRenderExecutionToCandidate(
	liveOutputDir string,
	candidate *pipelineRenderCandidate,
	frozen *pipelineFrozenPlan,
) error {
	if candidate == nil || frozen == nil {
		return fmt.Errorf("bind render execution requires candidate and frozen plan")
	}
	liveLock, err := store.NewStore(liveOutputDir).Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("load live render execution for candidate: %w", err)
	}
	if liveLock == nil ||
		liveLock.Mode != domain.PipelineExecutionRender ||
		liveLock.TargetChapter != frozen.Chapter ||
		liveLock.PlanDigest != frozen.PlanDigest ||
		liveLock.ProcessID != os.Getpid() ||
		strings.TrimSpace(liveLock.Owner) == "" {
		return fmt.Errorf(
			"live render execution does not bind current process/frozen chapter (lock=%+v chapter=%d plan=%s pid=%d)",
			liveLock,
			frozen.Chapter,
			frozen.PlanDigest,
			os.Getpid(),
		)
	}
	if err := store.NewStore(candidate.OutputDir).Runtime.AcquirePipelineExecution(
		domain.PipelineExecutionLock{
			Mode:          domain.PipelineExecutionRender,
			TargetChapter: frozen.Chapter,
			PlanDigest:    frozen.PlanDigest,
			Owner:         liveLock.Owner,
			ExpiresAt:     liveLock.ExpiresAt,
		},
	); err != nil {
		return fmt.Errorf("bind current render execution to candidate: %w", err)
	}
	bound, err := store.NewStore(candidate.OutputDir).Runtime.LoadPipelineExecution()
	if err != nil || bound == nil ||
		bound.Owner != liveLock.Owner ||
		bound.Mode != domain.PipelineExecutionRender ||
		bound.TargetChapter != frozen.Chapter ||
		bound.PlanDigest != frozen.PlanDigest ||
		bound.ProcessID != os.Getpid() {
		return fmt.Errorf("verify candidate render execution binding: lock=%+v err=%v", bound, err)
	}
	// A previous process can crash after consuming/committing prose but before
	// the Host-turn cleanup runs. Recovery may then skip Host entirely. Clear
	// any copied/stale one-shot capability at this mandatory pre-recovery bind
	// boundary so it can neither be consumed nor published into live.
	if err := store.NewStore(candidate.OutputDir).Runtime.ClearPipelineRenderProsePermit(""); err != nil {
		return fmt.Errorf("clear stale render prose permit before candidate recovery: %w", err)
	}
	return nil
}

func publishPipelineRenderCandidate(
	liveOutputDir string,
	candidate *pipelineRenderCandidate,
) (*store.DirectoryPublishReceipt, error) {
	if candidate == nil {
		return nil, fmt.Errorf("publish sealed render candidate is nil")
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	var receipt *store.DirectoryPublishReceipt
	err := withPipelineWatchdogPaused(func() error {
		var publishErr error
		receipt, publishErr = publisher.PublishDirectory(store.PublishDirectoryRequest{
			TransactionID:    candidate.ID,
			LiveDir:          liveOutputDir,
			CandidateDir:     candidate.OutputDir,
			ExpectedLiveRoot: candidate.SourceLiveRoot,
		})
		return publishErr
	})
	if err != nil {
		return nil, fmt.Errorf("publish sealed render candidate: %w", err)
	}
	if receipt == nil ||
		receipt.TransactionID != candidate.ID ||
		filepath.Clean(receipt.LiveDir) != filepath.Clean(liveOutputDir) ||
		receipt.CandidateRoot != receipt.CommittedLiveRoot ||
		strings.TrimSpace(receipt.ReceiptDigest) == "" {
		return nil, fmt.Errorf("sealed render directory publish returned incomplete receipt")
	}
	if err := pipelineWatchdogProgress(pipelineWatchdogEventRenderCandidatePublished); err != nil {
		return nil, fmt.Errorf("record sealed render candidate publish progress: %w", err)
	}
	return receipt, nil
}

func finalizePipelineRenderCandidate(
	outputDir string,
	transactionID string,
) error {
	if strings.TrimSpace(transactionID) == "" {
		return nil
	}
	publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(outputDir))
	if err := publisher.FinalizeDirectoryPublish(transactionID); err != nil {
		return fmt.Errorf("finalize sealed render directory publish: %w", err)
	}
	// CandidateDir itself has been renamed into live; remove only its now-empty
	// container. A non-empty container is retained for diagnosis.
	_ = os.Remove(filepath.Join(pipelineRenderCandidateRoot(outputDir), transactionID))
	return nil
}

func savePipelineSealedActualMatch(
	outputDir string,
	match pipelineSealedActualDeltaMatch,
) error {
	raw, err := json.MarshalIndent(match, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := atomicWriteRewriteFile(
		filepath.Join(outputDir, "meta", "planning", "sealed_actual_match.json"),
		raw,
		0o644,
	); err != nil {
		return err
	}
	if match.ProjectionMatch {
		if err := pipelineWatchdogProgress(pipelineWatchdogEventRenderActualMatched); err != nil {
			return fmt.Errorf("record sealed actual match progress: %w", err)
		}
	}
	return nil
}
