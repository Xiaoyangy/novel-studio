package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineChapterRenderTransactionProtocolVersion = "sealed-v2-render-transaction.v1|" +
	editorReviewProtocolVersion + "|" + deepseekAIJudgeReviewProtocolVersion

// pipelineChapterRenderTransactionFaultHook is a test-only crash boundary. It
// runs after the named immutable phase has been fsynced, so returning an error
// faithfully models a process dying after durable work but before its caller
// can move to the next stage.
var pipelineChapterRenderTransactionFaultHook func(domain.ChapterRenderPhase) error

func pipelineChapterRenderPlanIdentity(
	frozen *pipelineFrozenPlan,
) (domain.ChapterRenderPlanIdentity, error) {
	if frozen == nil || frozen.ProjectionBinding != "sealed_v2" {
		return domain.ChapterRenderPlanIdentity{}, fmt.Errorf("chapter render transaction requires sealed_v2 frozen plan")
	}
	identity := domain.ChapterRenderPlanIdentity{
		Version:                        domain.ChapterRenderPlanIdentityVersion,
		ProtocolVersion:                pipelineChapterRenderTransactionProtocolVersion,
		GenerationID:                   frozen.PlanningGenerationID,
		Chapter:                        frozen.Chapter,
		PlanDigest:                     frozen.PlanDigest,
		PlanCheckpointSeq:              frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:          frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:         frozen.PromotionReceiptDigest,
		PipelineRunInputDigest:         frozen.PipelineRunInputDigest,
		RenderContextSHA256:            frozen.RenderContextSHA256,
		ConvergenceReplanReceiptDigest: frozen.ConvergenceReplanReceiptDigest,
	}
	if err := domain.ValidateChapterRenderPlanIdentity(identity); err != nil {
		return domain.ChapterRenderPlanIdentity{}, err
	}
	return identity, nil
}

func pipelineChapterRenderBodyIdentity(
	frozen *pipelineFrozenPlan,
	bodySHA string,
) (domain.ChapterRenderBodyIdentity, error) {
	plan, err := pipelineChapterRenderPlanIdentity(frozen)
	if err != nil {
		return domain.ChapterRenderBodyIdentity{}, err
	}
	return domain.NewChapterRenderBodyIdentity(plan, bodySHA)
}

func pipelineChapterRenderBodyTracked(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA string,
) (bool, error) {
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return false, err
	}
	identities, err := store.NewChapterRenderTransactionStore(liveOutputDir).ListPlanBodies(identity.Plan)
	if err != nil {
		return false, err
	}
	for _, candidate := range identities {
		if candidate == identity {
			return true, nil
		}
	}
	return false, nil
}

func pipelineVerifyChapterRenderSnapshot(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	expected *pipelineRenderedChapterSnapshot,
) (*pipelineRenderedChapterSnapshot, *domain.Checkpoint, error) {
	verified, err := loadPipelineRenderedChapterSnapshot(outputDir, frozen, planCheckpoint)
	if err != nil {
		return nil, nil, err
	}
	if expected != nil && (verified.BodySHA256 != expected.BodySHA256 ||
		verified.Commit.Seq != expected.Commit.Seq ||
		verified.Commit.Digest != expected.Commit.Digest ||
		verified.ChapterPath != expected.ChapterPath ||
		!bytes.Equal([]byte(verified.Body), []byte(expected.Body))) {
		return nil, nil, fmt.Errorf("chapter render snapshot drifted between stage and transaction boundary")
	}
	bodyCheckpoint, err := tools.CurrentChapterBodyCheckpoint(verified.Store, frozen.Chapter)
	if err != nil {
		return nil, nil, fmt.Errorf("chapter render transaction verify exact draft checkpoint: %w", err)
	}
	if bodyCheckpoint.Digest != verified.BodySHA256 {
		return nil, nil, fmt.Errorf(
			"chapter render draft/commit body mismatch: draft=%s commit=%s",
			bodyCheckpoint.Digest,
			verified.BodySHA256,
		)
	}
	return verified, bodyCheckpoint, nil
}

func pipelineEnsureChapterRenderCommitted(
	liveOutputDir string,
	artifactOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	expected *pipelineRenderedChapterSnapshot,
) (domain.ChapterRenderBodyIdentity, error) {
	verified, bodyCheckpoint, err := pipelineVerifyChapterRenderSnapshot(
		artifactOutputDir,
		frozen,
		planCheckpoint,
		expected,
	)
	if err != nil {
		return domain.ChapterRenderBodyIdentity{}, err
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, verified.BodySHA256)
	if err != nil {
		return domain.ChapterRenderBodyIdentity{}, err
	}
	txnStore := store.NewChapterRenderTransactionStore(liveOutputDir)
	if _, err := txnStore.BeginBody(identity, []byte(verified.Body), domain.ChapterRenderPhaseEvidence{
		BodyCheckpointSeq:    bodyCheckpoint.Seq,
		BodyCheckpointDigest: bodyCheckpoint.Digest,
	}); err != nil {
		return domain.ChapterRenderBodyIdentity{}, fmt.Errorf("persist body_ready render transaction: %w", err)
	}
	receipts, err := txnStore.LoadReceipts(identity)
	if err != nil {
		return domain.ChapterRenderBodyIdentity{}, err
	}
	if committed := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseCommitted); committed != nil {
		if committed.Evidence.CommitCheckpointSeq != verified.Commit.Seq ||
			committed.Evidence.CommitDigest != verified.Commit.Digest ||
			committed.Evidence.CommitDigest != verified.BodySHA256 ||
			committed.Evidence.CandidateRoot != domain.PlanningV2DigestPrefix+verified.ActualCanonRoot {
			return domain.ChapterRenderBodyIdentity{}, fmt.Errorf("durable committed transaction no longer matches exact commit/canonical evidence")
		}
		return identity, nil
	}
	if _, err := txnStore.Advance(identity, domain.ChapterRenderPhaseCommitted, domain.ChapterRenderPhaseEvidence{
		CommitCheckpointSeq: verified.Commit.Seq,
		CommitDigest:        verified.Commit.Digest,
		// Bind every canonical artifact while deliberately excluding mutable
		// operational state such as meta/runtime. The same root is recomputed
		// at every recovery and publish boundary.
		CandidateRoot: domain.PlanningV2DigestPrefix + verified.ActualCanonRoot,
	}); err != nil {
		return domain.ChapterRenderBodyIdentity{}, fmt.Errorf("persist committed render transaction: %w", err)
	}
	if err := pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseCommitted); err != nil {
		return domain.ChapterRenderBodyIdentity{}, err
	}
	return identity, nil
}

func pipelineAdvanceChapterRenderStructuralBlock(
	liveOutputDir string,
	artifactOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	snapshot *pipelineRenderedChapterSnapshot,
) error {
	identity, err := pipelineEnsureChapterRenderCommitted(
		liveOutputDir, artifactOutputDir, frozen, planCheckpoint, snapshot,
	)
	if err != nil {
		return err
	}
	rel := "meta/planning/sealed_actual_match.json"
	raw, err := os.ReadFile(filepath.Join(artifactOutputDir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read structural-block diagnostic: %w", err)
	}
	var match pipelineSealedActualDeltaMatch
	if err := decodePipelineChapterRenderJSONStrict(raw, &match); err != nil {
		return fmt.Errorf("decode structural-block diagnostic: %w", err)
	}
	if match.ProjectionMatch {
		return fmt.Errorf("structural-block diagnostic unexpectedly records projection_match=true")
	}
	reasons := normalizePipelineSealedReasons(match.MismatchReasons)
	if len(reasons) == 0 {
		reasons = []string{"projection_match=false"}
	}
	reason := fmt.Sprintf("%s@%s: %s", rel, pipelineBytesSHA(raw), strings.Join(reasons, "；"))
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		domain.ChapterRenderPhaseStructurallyBlocked,
		domain.ChapterRenderPhaseEvidence{Reason: reason},
	); err != nil {
		return fmt.Errorf("persist structurally_blocked render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseStructurallyBlocked)
}

func pipelineAdvanceChapterRenderFormal(
	liveOutputDir string,
	artifactOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	snapshot *pipelineRenderedChapterSnapshot,
	accepted bool,
) error {
	identity, err := pipelineEnsureChapterRenderCommitted(
		liveOutputDir, artifactOutputDir, frozen, planCheckpoint, snapshot,
	)
	if err != nil {
		return err
	}
	inspection := inspectCurrentChapterReview(artifactOutputDir, frozen.Chapter)
	if len(inspection.Issues) > 0 {
		return fmt.Errorf("formal transaction review artifacts are stale: %s", strings.Join(inspection.Issues, "；"))
	}
	if domain.PlanningV2DigestPrefix+inspection.BodySHA256 != identity.BodySHA256 {
		return fmt.Errorf("formal transaction review body hash differs from immutable body")
	}
	isAccepted := pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition)
	if isAccepted != accepted {
		return fmt.Errorf("formal transaction verdict mismatch: accepted=%t verdict=%q disposition=%q", accepted, inspection.Verdict, inspection.Disposition)
	}
	artifacts, err := pipelineChapterRenderArtifactBindings(artifactOutputDir, inspection.Artifacts)
	if err != nil {
		return err
	}
	phase := domain.ChapterRenderPhaseFormalRejected
	if accepted {
		phase = domain.ChapterRenderPhaseFormalAccepted
	}
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		phase,
		domain.ChapterRenderPhaseEvidence{
			ReviewVerdict:     inspection.Verdict,
			ReviewDisposition: inspection.Disposition,
			ReviewArtifacts:   artifacts,
		},
	); err != nil {
		return fmt.Errorf("persist %s render transaction: %w", phase, err)
	}
	return pipelineChapterRenderTransactionFault(phase)
}

func pipelineAdvanceChapterRenderActualMatch(
	liveOutputDir string,
	artifactOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA string,
) error {
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return err
	}
	rel := "meta/planning/sealed_actual_match.json"
	raw, err := os.ReadFile(filepath.Join(artifactOutputDir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read sealed actual-match artifact: %w", err)
	}
	var match pipelineSealedActualDeltaMatch
	if err := decodePipelineChapterRenderJSONStrict(raw, &match); err != nil {
		return fmt.Errorf("decode sealed actual-match artifact: %w", err)
	}
	if !match.ProjectionMatch || !match.Complete || domain.ValidateProjectedDeltaV2(match.ActualDelta) != nil {
		return fmt.Errorf("sealed actual-match artifact is not a complete valid match")
	}
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		domain.ChapterRenderPhaseActualMatched,
		domain.ChapterRenderPhaseEvidence{ActualMatchDigest: pipelineBytesSHA(raw)},
	); err != nil {
		return fmt.Errorf("persist actual_matched render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseActualMatched)
}

func pipelineAdvanceChapterRenderPublished(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	bodySHA string,
) error {
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return err
	}
	transactionID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return err
	}
	publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(liveOutputDir))
	publishReceipt, err := publisher.LoadDirectoryPublishReceiptEvidence(transactionID)
	if err != nil {
		return fmt.Errorf("load directory publish evidence: %w", err)
	}
	if publishReceipt == nil ||
		publishReceipt.TransactionID != transactionID ||
		filepath.Clean(publishReceipt.LiveDir) != filepath.Clean(liveOutputDir) ||
		filepath.Clean(publishReceipt.CandidateDir) != filepath.Join(
			pipelineRenderCandidateRoot(liveOutputDir), transactionID, "output",
		) ||
		publishReceipt.CandidateRoot != publishReceipt.CommittedLiveRoot ||
		strings.TrimSpace(publishReceipt.ReceiptDigest) == "" {
		return fmt.Errorf("directory publish evidence is missing or incomplete")
	}
	verified, _, err := pipelineVerifyChapterRenderSnapshot(liveOutputDir, frozen, planCheckpoint, nil)
	if err != nil {
		return fmt.Errorf("verify published live snapshot: %w", err)
	}
	if verified.BodySHA256 != identity.BodySHA256 {
		return fmt.Errorf("published live body differs from transaction body")
	}
	receipts, err := store.NewChapterRenderTransactionStore(liveOutputDir).LoadReceipts(identity)
	if err != nil {
		return err
	}
	// A restart necessarily writes a new pipeline_execution owner before this
	// adapter runs, so the whole-directory root is allowed to differ from the
	// historical committed root. The publish store has already authenticated
	// that historical root; independently revalidate every immutable render
	// artifact here instead of trusting mutable runtime bytes or the receipt
	// alone.
	if err := pipelineVerifyChapterRenderRecoveryArtifacts(
		liveOutputDir,
		frozen,
		planCheckpoint,
		identity,
		receipts,
	); err != nil {
		return fmt.Errorf("verify published immutable render artifacts: %w", err)
	}
	manifest, err := loadPipelineRenderCandidateManifest(liveOutputDir)
	if err != nil {
		return fmt.Errorf("load published render candidate manifest: %w", err)
	}
	if manifest == nil {
		return fmt.Errorf("published render candidate manifest is missing")
	}
	if manifest.CandidateID != transactionID ||
		manifest.GenerationID != frozen.PlanningGenerationID ||
		manifest.Chapter != frozen.Chapter ||
		manifest.PlanDigest != frozen.PlanDigest ||
		manifest.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		manifest.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		filepath.Clean(manifest.SourceOutputDir) != filepath.Clean(liveOutputDir) ||
		manifest.SourceLiveRoot != publishReceipt.BeforeLiveRoot {
		return fmt.Errorf("published render candidate manifest identity drifted")
	}
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		domain.ChapterRenderPhasePublished,
		domain.ChapterRenderPhaseEvidence{
			DirectoryPublishID:     transactionID,
			DirectoryPublishDigest: publishReceipt.ReceiptDigest,
		},
	); err != nil {
		return fmt.Errorf("persist published render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhasePublished)
}

func pipelineAdvanceChapterRenderOutcome(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	commit *domain.Checkpoint,
	bodySHA string,
	outcome *domain.ActualOutcomeReceiptV2,
) error {
	if outcome == nil || commit == nil {
		return fmt.Errorf("outcome transaction requires durable outcome and commit")
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return err
	}
	loaded, err := store.NewStore(liveOutputDir).ProjectedV2().LoadActualOutcomeReceipt(
		frozen.PlanningGenerationID,
		frozen.Chapter,
		outcome.ReceiptDigest,
	)
	if err != nil {
		return fmt.Errorf("load durable actual outcome receipt: %w", err)
	}
	if loaded == nil {
		return fmt.Errorf("durable actual outcome receipt is missing")
	}
	if loaded.ReceiptDigest != outcome.ReceiptDigest ||
		loaded.ChapterBodySHA256 != identity.BodySHA256 ||
		loaded.CommitCheckpointSeq != commit.Seq ||
		loaded.GenerationID != frozen.PlanningGenerationID ||
		loaded.Chapter != frozen.Chapter || !loaded.ProjectionMatch {
		return fmt.Errorf("durable actual outcome receipt identity drifted")
	}
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		domain.ChapterRenderPhaseOutcomeAccepted,
		domain.ChapterRenderPhaseEvidence{OutcomeReceiptDigest: loaded.ReceiptDigest},
	); err != nil {
		return fmt.Errorf("persist outcome_accepted render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseOutcomeAccepted)
}

func pipelineAdvanceChapterRenderAcceptance(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA string,
	acceptance *domain.ChapterAcceptanceReceipt,
) error {
	if acceptance == nil {
		return fmt.Errorf("chapter acceptance transaction requires durable receipt")
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return err
	}
	loaded, err := store.NewStore(liveOutputDir).ArcCycle().LoadChapterAcceptanceReceipt(
		frozen.PlanningGenerationID,
		frozen.Chapter,
		acceptance.ReceiptDigest,
	)
	if err != nil {
		return fmt.Errorf("load durable chapter acceptance receipt: %w", err)
	}
	if loaded == nil {
		return fmt.Errorf("durable chapter acceptance receipt is missing")
	}
	if loaded.ReceiptDigest != acceptance.ReceiptDigest ||
		loaded.ChapterBodySHA256 != identity.BodySHA256 ||
		loaded.GenerationID != frozen.PlanningGenerationID ||
		loaded.Chapter != frozen.Chapter {
		return fmt.Errorf("durable chapter acceptance receipt identity drifted")
	}
	if _, err := store.NewChapterRenderTransactionStore(liveOutputDir).Advance(
		identity,
		domain.ChapterRenderPhaseChapterAccepted,
		domain.ChapterRenderPhaseEvidence{ChapterAcceptanceDigest: loaded.ReceiptDigest},
	); err != nil {
		return fmt.Errorf("persist chapter_accepted render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseChapterAccepted)
}

func pipelineAdvanceChapterRenderCompleted(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA string,
	expectedDigest string,
) error {
	identity, err := pipelineChapterRenderBodyIdentity(frozen, bodySHA)
	if err != nil {
		return err
	}
	path := filepath.Join(liveOutputDir, filepath.FromSlash(pipelineRenderReceiptPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read durable render receipt: %w", err)
	}
	if pipelineBytesSHA(raw) != expectedDigest {
		return fmt.Errorf("durable render receipt digest drifted")
	}
	var receipt pipelineRenderReceipt
	if err := decodePipelineChapterRenderJSONStrict(raw, &receipt); err != nil {
		return fmt.Errorf("decode durable render receipt: %w", err)
	}
	if receipt.Version != pipelinePlanningSchema ||
		receipt.Chapter != frozen.Chapter ||
		receipt.PlanDigest != frozen.PlanDigest ||
		receipt.PlanCheckpointSeq != frozen.PlanCheckpointSeq ||
		receipt.ChapterBodySHA256 != identity.BodySHA256 ||
		receipt.CommitDigest != identity.BodySHA256 ||
		!receipt.ProjectionBound ||
		receipt.PlanningGenerationID != frozen.PlanningGenerationID ||
		receipt.ProjectedBundleDigest != frozen.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != frozen.PromotionReceiptDigest ||
		strings.TrimSpace(receipt.OutcomeReceiptDigest) == "" ||
		strings.TrimSpace(receipt.DirectoryPublishID) == "" ||
		strings.TrimSpace(receipt.DirectoryPublishDigest) == "" {
		return fmt.Errorf("durable render receipt does not bind completed sealed transaction")
	}
	txnStore := store.NewChapterRenderTransactionStore(liveOutputDir)
	receipts, err := txnStore.LoadReceipts(identity)
	if err != nil {
		return err
	}
	committed := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseCommitted)
	published := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhasePublished)
	outcome := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseOutcomeAccepted)
	acceptance := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseChapterAccepted)
	if committed == nil || published == nil || outcome == nil || acceptance == nil ||
		committed.Evidence.CommitCheckpointSeq != receipt.CommitCheckpointSeq ||
		committed.Evidence.CommitDigest != receipt.CommitDigest ||
		published.Evidence.DirectoryPublishID != receipt.DirectoryPublishID ||
		published.Evidence.DirectoryPublishDigest != receipt.DirectoryPublishDigest ||
		outcome.Evidence.OutcomeReceiptDigest != receipt.OutcomeReceiptDigest {
		return fmt.Errorf("durable render receipt conflicts with prior transaction evidence")
	}
	if _, err := txnStore.Advance(
		identity,
		domain.ChapterRenderPhaseCompleted,
		domain.ChapterRenderPhaseEvidence{RenderReceiptDigest: expectedDigest},
	); err != nil {
		return fmt.Errorf("persist completed render transaction: %w", err)
	}
	return pipelineChapterRenderTransactionFault(domain.ChapterRenderPhaseCompleted)
}

// pipelineRecoverChapterRenderCompletionIfTracked closes the final tiny crash
// window between current_render_receipt.json's atomic rename and the completed
// phase receipt. Projects rendered before this transaction protocol have no
// matching plan body and remain readable; once a transaction exists, an
// accepted render receipt may not silently outrun its chain.
func pipelineRecoverChapterRenderCompletionIfTracked(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	bodySHA string,
) error {
	plan, err := pipelineChapterRenderPlanIdentity(frozen)
	if err != nil {
		return err
	}
	identity, err := domain.NewChapterRenderBodyIdentity(plan, bodySHA)
	if err != nil {
		return err
	}
	txnStore := store.NewChapterRenderTransactionStore(liveOutputDir)
	identities, err := txnStore.ListPlanBodies(plan)
	if err != nil {
		return err
	}
	tracked := false
	for _, candidate := range identities {
		if candidate == identity {
			tracked = true
			break
		}
	}
	if !tracked {
		return nil
	}
	receipts, err := txnStore.LoadReceipts(identity)
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		return fmt.Errorf("tracked completed render transaction has no phase receipts")
	}
	latest := receipts[len(receipts)-1].Phase
	if latest != domain.ChapterRenderPhaseChapterAccepted && latest != domain.ChapterRenderPhaseCompleted {
		return fmt.Errorf("durable render receipt outran chapter transaction phase %s", latest)
	}
	digest, err := pipelineRequiredFileSHA(liveOutputDir, pipelineRenderReceiptPath)
	if err != nil {
		return err
	}
	return pipelineAdvanceChapterRenderCompleted(liveOutputDir, frozen, bodySHA, digest)
}

func pipelineChapterRenderArtifactBindings(
	outputDir string,
	paths []string,
) ([]domain.ChapterRenderArtifactBinding, error) {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	bindings := make([]domain.ChapterRenderArtifactBinding, 0, len(paths))
	for _, rel := range paths {
		fullPath := filepath.Join(outputDir, filepath.FromSlash(rel))
		info, err := os.Lstat(fullPath)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("chapter render evidence is not a regular file: %s", rel)
		}
		digest, err := pipelineRequiredFileSHA(outputDir, rel)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, domain.ChapterRenderArtifactBinding{Path: rel, Digest: digest})
	}
	return domain.CanonicalChapterRenderArtifacts(bindings), nil
}

func pipelineChapterRenderReceiptForPhase(
	receipts []domain.ChapterRenderPhaseReceipt,
	phase domain.ChapterRenderPhase,
) *domain.ChapterRenderPhaseReceipt {
	for i := range receipts {
		if receipts[i].Phase == phase {
			copy := receipts[i]
			return &copy
		}
	}
	return nil
}

func pipelineChapterRenderTransactionFault(phase domain.ChapterRenderPhase) error {
	if pipelineChapterRenderTransactionFaultHook == nil {
		return nil
	}
	if err := pipelineChapterRenderTransactionFaultHook(phase); err != nil {
		return fmt.Errorf("injected crash after durable render phase %s: %w", phase, err)
	}
	return nil
}

// recoverDurableChapterRenderCandidate closes the non-atomic boundary between
// commit_chapter's own durable checkpoint and BeginBody/committed in the
// sibling transaction store. It restores the full committed tree and marks it
// for a zero-Writer replay. Negative matcher/review outcomes keep their
// dedicated rewrite/diagnostic routing and are never revived here.
func recoverDurableChapterRenderCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderCandidate, error) {
	// Invalid legacy test identities cannot participate in the v1 transaction
	// protocol. Production frozen plans have already passed locked preflight.
	if _, err := pipelineChapterRenderPlanIdentity(frozen); err != nil {
		return nil, nil
	}
	paths := []string{activeContainer}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list durable committed render candidates: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), id+"-") {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}
	var best *reusablePipelineRenderCandidate
	for _, candidatePath := range paths {
		candidate, ok := inspectReusablePipelineRenderCandidate(
			candidatePath, liveOutputDir, frozen, id,
		)
		if !ok || candidate.score < 3 {
			continue
		}
		output := filepath.Join(candidate.container, "output")
		durablyRejected, rejectErr := pipelineRenderBodyHasDurableConvergenceRejection(
			liveOutputDir,
			frozen,
			id,
			reviewBodySHA(candidate.body),
		)
		if rejectErr != nil {
			return nil, fmt.Errorf("inspect durable commit convergence rejection: %w", rejectErr)
		}
		if durablyRejected {
			continue
		}
		rejected, inspectErr := pipelineRenderBodyHasEffectiveSemanticRejection(
			liveOutputDir,
			frozen,
			id,
			reviewBodySHA(candidate.body),
		)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if rejected || pipelineChapterRenderCandidateHasNegativeActualMatch(output) {
			continue
		}
		inspection := inspectCurrentChapterReview(output, frozen.Chapter)
		if len(inspection.Issues) == 0 &&
			!pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) {
			continue
		}
		if newerReusablePipelineRenderCandidate(candidate, best) {
			copy := candidate
			best = &copy
		}
	}
	if best == nil {
		return nil, nil
	}
	livePlan, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(liveOutputDir), frozen.Chapter)
	if err != nil || livePlan.Digest != frozen.PlanDigest || livePlan.Seq != frozen.PlanCheckpointSeq {
		return nil, fmt.Errorf("recover durable commit requires unchanged live frozen plan: current=%v err=%v", livePlan, err)
	}
	currentLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(best.container) != filepath.Clean(activeContainer) {
		if _, statErr := os.Stat(activeContainer); statErr == nil {
			if err := retirePipelineRenderCandidate(activeContainer, "stale-superseded-by-durable-commit"); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := copyPipelineRenderCandidateTree(
			filepath.Join(best.container, "output"),
			filepath.Join(activeContainer, "output"),
		); err != nil {
			_ = retirePipelineRenderCandidate(activeContainer, "durable-commit-copy-failed")
			return nil, err
		}
	}
	output := filepath.Join(activeContainer, "output")
	manifest := best.manifest
	manifest.SourceLiveRoot = currentLiveRoot
	manifest.PreparedAt = pipelineChapterRenderNowUTC()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		append(raw, '\n'),
		0o644,
	); err != nil {
		return nil, err
	}
	planCheckpoint, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(output), frozen.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load restored durable render candidate plan: %w", err)
	}
	if !pipelineRenderCandidateHasExactDurableCommit(output, frozen, planCheckpoint) {
		return nil, fmt.Errorf("restored durable render candidate failed exact commit verification")
	}
	return &pipelineRenderCandidate{
		ID:                     id,
		ContainerDir:           activeContainer,
		OutputDir:              output,
		TransactionRoot:        pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:         currentLiveRoot,
		RecoveredDurableCommit: true,
	}, nil
}

func pipelineChapterRenderCandidateHasNegativeActualMatch(outputDir string) bool {
	raw, err := os.ReadFile(filepath.Join(outputDir, "meta", "planning", "sealed_actual_match.json"))
	if err != nil {
		return false
	}
	var match pipelineSealedActualDeltaMatch
	return decodePipelineChapterRenderJSONStrict(raw, &match) == nil && !match.ProjectionMatch
}

func reviewBodySHA(body string) string {
	return strings.TrimPrefix(domain.ComputeChapterRenderBodySHA256([]byte(body)), domain.PlanningV2DigestPrefix)
}

// recoverChapterRenderTransactionCandidate replays an already durable
// candidate directory, never an in-memory snapshot. A committed phase skips
// Writer but still proceeds through deterministic/formal review; a validated
// formal_accepted (or actual_matched) phase skips both Writer and reviewers.
// Invalid legacy fixture identities are treated as non-applicable here only;
// every production call has already passed the locked sealed-v2 preflight and
// the stage adapters remain strict.
func recoverChapterRenderTransactionCandidate(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	id string,
	activeContainer string,
) (*pipelineRenderCandidate, error) {
	plan, err := pipelineChapterRenderPlanIdentity(frozen)
	if err != nil {
		return nil, nil
	}
	txnStore := store.NewChapterRenderTransactionStore(liveOutputDir)
	identities, err := txnStore.ListPlanBodies(plan)
	if err != nil {
		return nil, fmt.Errorf("list chapter render transaction bodies: %w", err)
	}
	if len(identities) == 0 {
		return nil, nil
	}

	paths := []string{activeContainer}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(liveOutputDir), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list retired transaction candidates: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), id+"-") {
				paths = append(paths, filepath.Join(retiredRoot, entry.Name()))
			}
		}
	}
	sort.Strings(paths)

	type recoveryChoice struct {
		candidate reusablePipelineRenderCandidate
		identity  domain.ChapterRenderBodyIdentity
		phase     domain.ChapterRenderPhase
	}
	var best *recoveryChoice
	highestRecoverableOrdinal := 0
	for _, identity := range identities {
		receipts, loadErr := txnStore.LoadReceipts(identity)
		if loadErr != nil || len(receipts) == 0 {
			if loadErr != nil {
				return nil, loadErr
			}
			continue
		}
		latest := receipts[len(receipts)-1].Phase
		if domain.ChapterRenderPhaseOrdinal(latest) < domain.ChapterRenderPhaseOrdinal(domain.ChapterRenderPhaseCommitted) ||
			latest == domain.ChapterRenderPhaseStructurallyBlocked ||
			latest == domain.ChapterRenderPhaseFormalRejected ||
			domain.ChapterRenderPhaseOrdinal(latest) >= domain.ChapterRenderPhaseOrdinal(domain.ChapterRenderPhasePublished) {
			continue
		}
		// The semantic-rejection ledger/tombstone is persisted before the
		// transaction can advance from committed to formal_rejected. A crash in
		// that window must not let the earlier committed receipt resurrect the
		// exact rejected bytes.
		if latest == domain.ChapterRenderPhaseCommitted {
			bodySHA := strings.TrimPrefix(identity.BodySHA256, domain.PlanningV2DigestPrefix)
			durablyRejected, rejectErr := pipelineRenderBodyHasDurableConvergenceRejection(
				liveOutputDir,
				frozen,
				id,
				bodySHA,
			)
			if rejectErr != nil {
				return nil, fmt.Errorf("inspect committed transaction convergence rejection: %w", rejectErr)
			}
			tombstoned, tombstoneErr := pipelineRenderBodyHasEffectiveSemanticRejection(
				liveOutputDir,
				frozen,
				id,
				bodySHA,
			)
			if tombstoneErr != nil {
				return nil, fmt.Errorf("inspect committed transaction semantic rejection: %w", tombstoneErr)
			}
			if durablyRejected || tombstoned {
				continue
			}
		}
		if ordinal := domain.ChapterRenderPhaseOrdinal(latest); ordinal > highestRecoverableOrdinal {
			highestRecoverableOrdinal = ordinal
		}
		immutableBody, loadErr := txnStore.LoadBody(identity)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, candidatePath := range paths {
			candidate, ok := inspectReusablePipelineRenderCandidate(
				candidatePath,
				liveOutputDir,
				frozen,
				id,
			)
			if !ok || candidate.score < 3 ||
				!bytes.Equal([]byte(candidate.body), immutableBody) ||
				candidate.bodyCheckpoint.Digest != identity.BodySHA256 {
				continue
			}
			candidateStore := store.NewStore(filepath.Join(candidate.container, "output"))
			candidatePlan, planErr := tools.CurrentChapterPlanCausalCheckpoint(candidateStore, frozen.Chapter)
			if planErr != nil {
				continue
			}
			if verifyErr := pipelineVerifyChapterRenderRecoveryArtifacts(
				filepath.Join(candidate.container, "output"),
				frozen,
				candidatePlan,
				identity,
				receipts,
			); verifyErr != nil {
				continue
			}
			choice := recoveryChoice{candidate: candidate, identity: identity, phase: latest}
			if best == nil ||
				domain.ChapterRenderPhaseOrdinal(choice.phase) > domain.ChapterRenderPhaseOrdinal(best.phase) ||
				(domain.ChapterRenderPhaseOrdinal(choice.phase) == domain.ChapterRenderPhaseOrdinal(best.phase) &&
					newerReusablePipelineRenderCandidate(choice.candidate, &best.candidate)) {
				best = &choice
			}
		}
	}
	if best == nil {
		if highestRecoverableOrdinal > 0 {
			return nil, fmt.Errorf(
				"chapter render transaction has durable phase ordinal %d but no candidate tree passes exact artifact verification",
				highestRecoverableOrdinal,
			)
		}
		return nil, nil
	}
	if domain.ChapterRenderPhaseOrdinal(best.phase) < highestRecoverableOrdinal {
		return nil, fmt.Errorf(
			"newest chapter render transaction evidence drifted: highest=%d recoverable=%s",
			highestRecoverableOrdinal,
			best.phase,
		)
	}

	livePlan, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(liveOutputDir), frozen.Chapter)
	if err != nil || livePlan.Digest != frozen.PlanDigest || livePlan.Seq != frozen.PlanCheckpointSeq {
		return nil, fmt.Errorf("recover chapter render transaction requires unchanged live frozen plan: current=%v err=%v", livePlan, err)
	}
	currentLiveRoot, err := store.DirectoryContentRoot(liveOutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot live root for chapter render recovery: %w", err)
	}
	if filepath.Clean(best.candidate.container) != filepath.Clean(activeContainer) {
		if _, statErr := os.Stat(activeContainer); statErr == nil {
			if err := retirePipelineRenderCandidate(activeContainer, "stale-superseded-by-transaction"); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := copyPipelineRenderCandidateTree(
			filepath.Join(best.candidate.container, "output"),
			filepath.Join(activeContainer, "output"),
		); err != nil {
			_ = retirePipelineRenderCandidate(activeContainer, "transaction-copy-failed")
			return nil, fmt.Errorf("restore chapter render transaction candidate: %w", err)
		}
	}
	output := filepath.Join(activeContainer, "output")
	manifest := best.candidate.manifest
	manifest.SourceLiveRoot = currentLiveRoot
	manifest.PreparedAt = pipelineChapterRenderNowUTC()
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicWriteRewriteFile(
		filepath.Join(output, "meta", "planning", "render_candidate.json"),
		append(manifestRaw, '\n'),
		0o644,
	); err != nil {
		return nil, fmt.Errorf("refresh transaction candidate manifest: %w", err)
	}
	planCheckpoint, err := tools.CurrentChapterPlanCausalCheckpoint(store.NewStore(output), frozen.Chapter)
	if err != nil {
		return nil, err
	}
	receipts, err := txnStore.LoadReceipts(best.identity)
	if err != nil {
		return nil, err
	}
	if err := pipelineVerifyChapterRenderRecoveryArtifacts(
		output, frozen, planCheckpoint, best.identity, receipts,
	); err != nil {
		return nil, fmt.Errorf("revalidate restored chapter render transaction candidate: %w", err)
	}
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:render] 恢复 sealed exact-body 事务（chapter=%d phase=%s body=%s）；不重复已完成的模型阶段\n",
		frozen.Chapter,
		best.phase,
		best.identity.BodySHA256,
	)
	return &pipelineRenderCandidate{
		ID:                               id,
		ContainerDir:                     activeContainer,
		OutputDir:                        output,
		TransactionRoot:                  pipelineRenderTransactionRoot(liveOutputDir),
		SourceLiveRoot:                   currentLiveRoot,
		RecoveredChapterTransactionPhase: best.phase,
	}, nil
}

func pipelineVerifyChapterRenderRecoveryArtifacts(
	outputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	identity domain.ChapterRenderBodyIdentity,
	receipts []domain.ChapterRenderPhaseReceipt,
) error {
	snapshot, bodyCheckpoint, err := pipelineVerifyChapterRenderSnapshot(outputDir, frozen, planCheckpoint, nil)
	if err != nil {
		return err
	}
	if snapshot.BodySHA256 != identity.BodySHA256 {
		return fmt.Errorf("recovery body differs from transaction identity")
	}
	bodyReady := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseBodyReady)
	committed := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseCommitted)
	if bodyReady == nil || committed == nil ||
		bodyReady.Evidence.BodyCheckpointSeq != bodyCheckpoint.Seq ||
		bodyReady.Evidence.BodyCheckpointDigest != bodyCheckpoint.Digest ||
		committed.Evidence.CommitCheckpointSeq != snapshot.Commit.Seq ||
		committed.Evidence.CommitDigest != snapshot.Commit.Digest {
		return fmt.Errorf("recovery checkpoint evidence differs from durable candidate")
	}
	currentCanonRoot := domain.PlanningV2DigestPrefix + snapshot.ActualCanonRoot
	if committed.Evidence.CandidateRoot != currentCanonRoot {
		return fmt.Errorf(
			"recovery canonical root drifted from committed transaction: committed=%s current=%s",
			committed.Evidence.CandidateRoot,
			currentCanonRoot,
		)
	}
	formal := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseFormalAccepted)
	if formal != nil {
		inspection := inspectCurrentChapterReview(outputDir, frozen.Chapter)
		if len(inspection.Issues) > 0 ||
			!pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) ||
			domain.PlanningV2DigestPrefix+inspection.BodySHA256 != identity.BodySHA256 {
			return fmt.Errorf("recovery formal accept is no longer exact/current")
		}
		artifacts, err := pipelineChapterRenderArtifactBindings(outputDir, inspection.Artifacts)
		if err != nil {
			return err
		}
		if formal.Evidence.ReviewVerdict != inspection.Verdict ||
			formal.Evidence.ReviewDisposition != inspection.Disposition ||
			!reflect.DeepEqual(formal.Evidence.ReviewArtifacts, artifacts) {
			return fmt.Errorf("recovery formal review artifact digests drifted")
		}
	}
	if actual := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseActualMatched); actual != nil {
		raw, err := os.ReadFile(filepath.Join(outputDir, "meta", "planning", "sealed_actual_match.json"))
		if err != nil || pipelineBytesSHA(raw) != actual.Evidence.ActualMatchDigest {
			return fmt.Errorf("recovery actual-match artifact digest drifted")
		}
		var match pipelineSealedActualDeltaMatch
		if decodePipelineChapterRenderJSONStrict(raw, &match) != nil || !match.ProjectionMatch || !match.Complete {
			return fmt.Errorf("recovery actual-match artifact is not complete")
		}
	}
	return nil
}

func loadPipelineRecoveredChapterTransactionSnapshot(
	candidate *pipelineRenderCandidate,
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (*pipelineRenderedChapterSnapshot, bool, bool, error) {
	if candidate == nil || candidate.RecoveredChapterTransactionPhase == "" {
		return nil, false, false, nil
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, planCheckpoint)
	if err != nil {
		return nil, false, false, fmt.Errorf("load recovered chapter transaction snapshot: %w", err)
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	if err != nil {
		return nil, false, false, err
	}
	receipts, err := store.NewChapterRenderTransactionStore(liveOutputDir).LoadReceipts(identity)
	if err != nil {
		return nil, false, false, err
	}
	if err := pipelineVerifyChapterRenderRecoveryArtifacts(
		candidate.OutputDir,
		frozen,
		planCheckpoint,
		identity,
		receipts,
	); err != nil {
		return nil, false, false, err
	}
	formalAccepted := pipelineChapterRenderReceiptForPhase(
		receipts,
		domain.ChapterRenderPhaseFormalAccepted,
	) != nil
	return snapshot, formalAccepted, true, nil
}

func loadPipelineRecoveredDurableCommitSnapshot(
	candidate *pipelineRenderCandidate,
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
) (*pipelineRenderedChapterSnapshot, bool, bool, error) {
	if candidate == nil || !candidate.RecoveredDurableCommit {
		return nil, false, false, nil
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, planCheckpoint)
	if err != nil {
		return nil, false, false, err
	}
	if _, err := pipelineEnsureChapterRenderCommitted(
		liveOutputDir,
		candidate.OutputDir,
		frozen,
		planCheckpoint,
		snapshot,
	); err != nil {
		return nil, false, false, err
	}
	inspection := inspectCurrentChapterReview(candidate.OutputDir, frozen.Chapter)
	formalAccepted := len(inspection.Issues) == 0 &&
		pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) &&
		domain.PlanningV2DigestPrefix+inspection.BodySHA256 == snapshot.BodySHA256
	if formalAccepted {
		if err := pipelineAdvanceChapterRenderFormal(
			liveOutputDir,
			candidate.OutputDir,
			frozen,
			planCheckpoint,
			snapshot,
			true,
		); err != nil {
			return nil, false, false, err
		}
	}
	return snapshot, formalAccepted, true, nil
}

func pipelineChapterRenderFormalAcceptedOnDisk(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	planCheckpoint *domain.Checkpoint,
	snapshot *pipelineRenderedChapterSnapshot,
) (bool, error) {
	if snapshot == nil {
		return false, fmt.Errorf("formal-accepted recovery requires exact live snapshot")
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	if err != nil {
		return false, err
	}
	receipts, err := store.NewChapterRenderTransactionStore(liveOutputDir).LoadReceipts(identity)
	if err != nil {
		return false, err
	}
	if pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseFormalAccepted) == nil {
		return false, nil
	}
	if err := pipelineVerifyChapterRenderRecoveryArtifacts(
		liveOutputDir,
		frozen,
		planCheckpoint,
		identity,
		receipts,
	); err != nil {
		return false, fmt.Errorf("formal-accepted live transaction evidence drifted: %w", err)
	}
	return true, nil
}

func pipelineChapterRenderNowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func decodePipelineChapterRenderJSONStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
