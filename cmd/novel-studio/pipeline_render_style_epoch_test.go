package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineRenderV3StyleEpochTestFrozen(t *testing.T, live string) (*pipelineFrozenPlan, *domain.Checkpoint) {
	t.Helper()
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "协议纪元"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(seed string) string { return "sha256:" + strings.Repeat(seed, 64) }
	frozen := &pipelineFrozenPlan{
		Version: pipelinePlanningSchema, Chapter: 1,
		PlanDigest: plan.Digest, PlanCheckpointSeq: plan.Seq,
		PlanningGenerationID: "pg2_v3_style_epoch_test", ProjectionBinding: "sealed_v2",
		ProjectedBundleDigest: digest("2"), PromotionReceiptDigest: digest("3"),
		PipelineRunInputDigest: digest("4"), RenderContextSHA256: digest("5"),
		EffectiveStyleProtocol: pipelineRenderCandidateManifestVersion,
	}
	return frozen, plan
}

func TestPipelineRenderV3StyleEpochIntentBlocksCombinedManifestAndFrozenDowngrade(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, live)
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := inspectPipelineRenderV3StyleEpochIntent(live, frozen, candidate.ID)
	if err != nil || intent == nil {
		t.Fatalf("fresh v3 candidate has no immutable epoch intent: intent=%+v err=%v", intent, err)
	}
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest pipelineRenderCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = pipelineRenderCandidatePreviousManifestVersion
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	if _, err := writePipelinePlanningJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	// Simulate the former composite downgrade: both mutable protocol fields are
	// cleared while the non-disposable CandidateID intent remains.
	frozen.EffectiveStyleProtocol = ""
	if _, err := pipelineRenderCandidateStyleProtocol(candidate.OutputDir, frozen); err == nil ||
		!strings.Contains(err.Error(), "immutable v3 style epoch intent") {
		t.Fatalf("combined v3->v2 downgrade escaped immutable epoch intent: %v", err)
	}
}

func TestReviewIntentBlocksLegacyDowngradeWithoutTransactionRoot(t *testing.T) {
	live, frozen, _, candidate, _ := newPipelineChapterRenderTransactionFixture(t)
	frozen.EffectiveStyleProtocol = pipelineRenderCandidateManifestVersion
	for _, outputDir := range []string{live, candidate.OutputDir} {
		if _, err := writePipelinePlanningJSON(
			filepath.Join(outputDir, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, candidate.ID); err != nil {
		t.Fatal(err)
	}
	frozen.EffectiveStyleProtocol = ""
	if _, err := writePipelinePlanningJSON(
		filepath.Join(candidate.OutputDir, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(candidate.OutputDir), 1); err == nil ||
		!strings.Contains(err.Error(), "immutable v3 style epoch intent") {
		t.Fatalf("review downgraded v3 intent after transaction loss: %v", err)
	}
}

func TestPublishedReviewCannotSpoofCandidateIDToMissV3Intent(t *testing.T) {
	live, frozen, _, candidate, _ := newPipelineChapterRenderTransactionFixture(t)
	frozen.EffectiveStyleProtocol = pipelineRenderCandidateManifestVersion
	originalID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, originalID); err != nil {
		t.Fatal(err)
	}
	frozen.EffectiveStyleProtocol = ""
	if _, err := writePipelinePlanningJSON(
		filepath.Join(live, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
	); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	var manifest pipelineRenderCandidateManifest
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.CandidateID = "render-ch0001-spoofed-style-epoch"
	manifest.SourceOutputDir = live
	if _, err := writePipelinePlanningJSON(
		filepath.Join(live, "meta", "planning", "render_candidate.json"), manifest,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(live), 1); err == nil ||
		!strings.Contains(err.Error(), "CandidateID differs from deterministic sealed identity") {
		t.Fatalf("published review spoofed CandidateID to miss immutable v3 intent: %v", err)
	}
}

func TestPublishedReviewCannotSpoofCandidateIDAfterFrozenIdentityLoss(t *testing.T) {
	live, frozen, _, candidate, _ := newPipelineChapterRenderTransactionFixture(t)
	frozen.EffectiveStyleProtocol = pipelineRenderCandidateManifestVersion
	originalID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, originalID); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")
	var manifest pipelineRenderCandidateManifest
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.CandidateID = "render-ch0001-spoofed-after-frozen-loss"
	manifest.Version = pipelineRenderCandidatePreviousManifestVersion
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	manifest.SourceOutputDir = live
	if _, err := writePipelinePlanningJSON(
		filepath.Join(live, "meta", "planning", "render_candidate.json"), manifest,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(live, filepath.FromSlash(pipelineFrozenPlanPath))); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReviewEffectiveStyleRequirement(store.NewStore(live), 1); err == nil ||
		!strings.Contains(err.Error(), "immutable v3 style epoch intent") {
		t.Fatalf("published review spoofed CandidateID after frozen identity loss: %v", err)
	}
}

func TestReviewIntentIgnoresDifferentSealedIdentityForSameChapter(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, live)
	candidateID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, candidateID); err != nil {
		t.Fatal(err)
	}

	legacyIdentity := *frozen
	legacyIdentity.PlanningGenerationID = "pg2_unrelated_legacy_generation"
	legacyIdentity.PlanDigest = pipelineBytesSHA([]byte("unrelated legacy plan"))
	legacyIdentity.PlanCheckpointSeq++
	legacyIdentity.ProjectedBundleDigest = pipelineBytesSHA([]byte("unrelated legacy bundle"))
	legacyIdentity.PromotionReceiptDigest = pipelineBytesSHA([]byte("unrelated legacy promotion"))
	legacyIdentity.PipelineRunInputDigest = pipelineBytesSHA([]byte("unrelated legacy render input"))
	legacyID, err := pipelineRenderTransactionID(&legacyIdentity)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest := &pipelineRenderCandidateManifest{
		Version:                   pipelineRenderCandidatePreviousManifestVersion,
		CandidateID:               legacyID,
		GenerationID:              legacyIdentity.PlanningGenerationID,
		Chapter:                   legacyIdentity.Chapter,
		PlanDigest:                legacyIdentity.PlanDigest,
		PlanCheckpointSeq:         legacyIdentity.PlanCheckpointSeq,
		ProjectedBundleDigest:     legacyIdentity.ProjectedBundleDigest,
		PromotionReceiptDigest:    legacyIdentity.PromotionReceiptDigest,
		PipelineRenderInputDigest: "",
		RenderContextSHA256:       "",
	}
	intent, err := inspectPipelineRenderV3StyleEpochIntentForManifest(live, legacyManifest)
	if err != nil || intent != nil {
		t.Fatalf("unrelated same-chapter v3 intent blocked genuine legacy identity: intent=%+v err=%v", intent, err)
	}
}

func TestPipelineRenderV3StyleEpochIntentIsIdempotentAndTamperEvident(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, live)
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, id)
	if err != nil || second == nil || first.IntentDigest != second.IntentDigest {
		t.Fatalf("idempotent epoch intent reuse failed: first=%+v second=%+v err=%v", first, second, err)
	}
	path, err := pipelineRenderStyleEpochIntentPath(live, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, id); err == nil {
		t.Fatal("tampered immutable epoch intent was replaced or ignored")
	}
}

func TestLegacyRenderInputDriftEmptyCandidateStopsBeforeDispatch(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, plan := pipelineRenderV3StyleEpochTestFrozen(t, live)
	// This is an explicit historical v2 fixture: it predates the v3 intent and
	// has no body, commit, formal acceptance, or provider dispatch evidence.
	frozen.EffectiveStyleProtocol = ""
	candidateID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareFreshPipelineRenderCandidateForStyleEpoch(
		live,
		frozen,
		candidateID,
		filepath.Join(pipelineRenderCandidateRoot(live), candidateID),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewritePipelineRenderCandidateAsLegacyV2(candidate.OutputDir, frozen); err != nil {
		t.Fatal(err)
	}
	liveStore := store.NewStore(live)
	owner := "legacy-input-drift-no-provider-test"
	if err := liveStore.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 1,
		PlanDigest: frozen.PlanDigest, Owner: owner,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = liveStore.Runtime.ReleasePipelineExecution(owner) }()

	_, _, err = runPipelineSealedRenderCandidate(
		cliOptions{},
		pipelineFlags{},
		&domain.PipelineState{},
		bootstrap.Config{OutputDir: live},
		assets.Bundle{},
		frozen,
		plan,
		nil,
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "没有可免 provider 收口") {
		t.Fatalf("empty legacy drift candidate did not fail before provider: %v", err)
	}
	dispatchPath, err := pipelineRenderDispatchLedgerPath(live, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dispatchPath); !os.IsNotExist(err) {
		t.Fatalf("legacy drift failure created provider dispatch evidence: %v", err)
	}
}

func TestV3FinalCannotFallBackToHistoricalReceiptAfterManifestAndTransactionLoss(t *testing.T) {
	frozen := &pipelineFrozenPlan{EffectiveStyleProtocol: pipelineRenderCandidateManifestVersion}
	receipt := pipelineRenderReceipt{}
	if _, _, err := validatePipelineRenderFinalStyleProtocol(t.TempDir(), frozen, receipt, os.ErrNotExist); err == nil ||
		!strings.Contains(err.Error(), "完整风格归档与 chapter acceptance digest") {
		t.Fatalf("cleared v3 final fields entered historical compatibility: %v", err)
	}
	receipt.EffectiveStyleReceiptPath = "meta/planning/effective_render_style_contracts/ch0001/render-ch0001-test/receipt.json"
	receipt.EffectiveStyleReceiptDigest = "sha256:" + strings.Repeat("a", 64)
	receipt.EffectiveStyleArtifactSHA256 = "sha256:" + strings.Repeat("b", 64)
	receipt.ChapterAcceptanceDigest = "sha256:" + strings.Repeat("c", 64)
	if _, _, err := validatePipelineRenderFinalStyleProtocol(t.TempDir(), frozen, receipt, os.ErrNotExist); err == nil ||
		!strings.Contains(err.Error(), "缺少不可变风格协议清单") {
		t.Fatalf("v3 final without manifest entered untracked historical fallback: %v", err)
	}
}

func TestV3FinalIntentBlocksCombinedFrozenManifestAndTransactionDowngrade(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, live)
	candidateID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensurePipelineRenderV3StyleEpochIntent(live, frozen, candidateID); err != nil {
		t.Fatal(err)
	}
	// Simulate all mutable v3 declarations and downstream transaction/style
	// fields being cleared while the candidate-adjacent immutable intent stays.
	frozen.EffectiveStyleProtocol = ""
	if _, v3, err := validatePipelineRenderFinalStyleProtocol(
		live, frozen, pipelineRenderReceipt{}, os.ErrNotExist,
	); err == nil || !v3 ||
		!strings.Contains(err.Error(), "完整风格归档与 chapter acceptance digest") {
		t.Fatalf("immutable v3 intent allowed final legacy fallback: v3=%t err=%v", v3, err)
	}
}
