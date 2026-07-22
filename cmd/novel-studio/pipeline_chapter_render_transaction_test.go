package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineChapterRenderTransactionCrashReplayBoundaries(t *testing.T) {
	t.Run("commit artifact crash before transaction still skips writer", func(t *testing.T) {
		live, frozen, plan, _, snapshot := newPipelineChapterRenderTransactionFixture(t)
		recovered, err := preparePipelineRenderCandidate(live, frozen)
		if err != nil {
			t.Fatal(err)
		}
		if !recovered.RecoveredDurableCommit {
			t.Fatal("durable commit crash window fell through to draft replay")
		}
		replayed, formalAccepted, ok, err := loadPipelineRecoveredDurableCommitSnapshot(
			recovered, live, frozen, plan,
		)
		if err != nil || !ok || formalAccepted {
			t.Fatalf("durable replay ok=%t formal=%t err=%v", ok, formalAccepted, err)
		}
		if replayed.BodySHA256 != snapshot.BodySHA256 || replayed.Commit.Seq != snapshot.Commit.Seq {
			t.Fatal("pre-transaction commit recovery changed exact commit")
		}
		if countPipelineChapterRenderCheckpoint(replayed.Store, frozen.Chapter, "draft")+
			countPipelineChapterRenderCheckpoint(replayed.Store, frozen.Chapter, "edit") != 1 {
			t.Fatal("pre-transaction commit recovery reran Writer")
		}
	})

	t.Run("commit crash restores exact body without writer", func(t *testing.T) {
		live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
		pipelineChapterRenderTransactionFaultHook = func(phase domain.ChapterRenderPhase) error {
			if phase == domain.ChapterRenderPhaseCommitted {
				return errors.New("crash")
			}
			return nil
		}
		t.Cleanup(func() { pipelineChapterRenderTransactionFaultHook = nil })
		if _, err := pipelineEnsureChapterRenderCommitted(
			live, candidate.OutputDir, frozen, plan, snapshot,
		); err == nil {
			t.Fatal("expected injected crash after committed")
		}
		pipelineChapterRenderTransactionFaultHook = nil

		recovered, err := preparePipelineRenderCandidate(live, frozen)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.RecoveredChapterTransactionPhase != domain.ChapterRenderPhaseCommitted {
			t.Fatalf("recovered phase=%s", recovered.RecoveredChapterTransactionPhase)
		}
		replayed, formalAccepted, ok, err := loadPipelineRecoveredChapterTransactionSnapshot(
			recovered, live, frozen, plan,
		)
		if err != nil || !ok || formalAccepted {
			t.Fatalf("replay ok=%t formal=%t err=%v", ok, formalAccepted, err)
		}
		if replayed.BodySHA256 != snapshot.BodySHA256 || replayed.Commit.Seq != snapshot.Commit.Seq {
			t.Fatal("commit recovery replaced or recommitted the exact body")
		}
		if countPipelineChapterRenderCheckpoint(replayed.Store, frozen.Chapter, "draft")+
			countPipelineChapterRenderCheckpoint(replayed.Store, frozen.Chapter, "edit") != 1 {
			t.Fatal("commit recovery appended a second Writer body checkpoint")
		}
	})

	t.Run("formal accept crash skips writer and reviewers", func(t *testing.T) {
		live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
		if _, err := pipelineEnsureChapterRenderCommitted(
			live, candidate.OutputDir, frozen, plan, snapshot,
		); err != nil {
			t.Fatal(err)
		}
		mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
		pipelineChapterRenderTransactionFaultHook = func(phase domain.ChapterRenderPhase) error {
			if phase == domain.ChapterRenderPhaseFormalAccepted {
				return errors.New("crash")
			}
			return nil
		}
		t.Cleanup(func() { pipelineChapterRenderTransactionFaultHook = nil })
		if err := pipelineAdvanceChapterRenderFormal(
			live, candidate.OutputDir, frozen, plan, snapshot, true,
		); err == nil {
			t.Fatal("expected injected crash after formal accept")
		}
		pipelineChapterRenderTransactionFaultHook = nil

		recovered, err := preparePipelineRenderCandidate(live, frozen)
		if err != nil {
			t.Fatal(err)
		}
		replayed, formalAccepted, ok, err := loadPipelineRecoveredChapterTransactionSnapshot(
			recovered, live, frozen, plan,
		)
		if err != nil || !ok || !formalAccepted {
			t.Fatalf("replay ok=%t formal=%t err=%v", ok, formalAccepted, err)
		}
		if replayed.BodySHA256 != snapshot.BodySHA256 {
			t.Fatal("formal recovery replaced exact body")
		}
		inspection := inspectCurrentChapterReview(recovered.OutputDir, frozen.Chapter)
		if len(inspection.Issues) != 0 || inspection.Verdict != "accept" {
			t.Fatalf("formal recovery lost review evidence: %+v", inspection)
		}
	})

	t.Run("publish crash replays receipt without second directory swap", func(t *testing.T) {
		live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
		if _, err := pipelineEnsureChapterRenderCommitted(
			live, candidate.OutputDir, frozen, plan, snapshot,
		); err != nil {
			t.Fatal(err)
		}
		mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
		if err := pipelineAdvanceChapterRenderFormal(
			live, candidate.OutputDir, frozen, plan, snapshot, true,
		); err != nil {
			t.Fatal(err)
		}
		match := pipelineChapterRenderTransactionTestMatch()
		if err := savePipelineSealedActualMatch(candidate.OutputDir, match); err != nil {
			t.Fatal(err)
		}
		if err := pipelineAdvanceChapterRenderActualMatch(
			live, candidate.OutputDir, frozen, snapshot.BodySHA256,
		); err != nil {
			t.Fatal(err)
		}
		publishReceipt, err := publishPipelineRenderCandidate(live, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.NewStore(live).Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode:          domain.PipelineExecutionRender,
			TargetChapter: frozen.Chapter,
			PlanDigest:    frozen.PlanDigest,
			Owner:         "restarted-render-owner",
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		currentRoot, err := store.DirectoryContentRoot(live)
		if err != nil {
			t.Fatal(err)
		}
		if currentRoot == publishReceipt.CommittedLiveRoot {
			t.Fatal("fixture did not model runtime lock owner root drift")
		}
		pipelineChapterRenderTransactionFaultHook = func(phase domain.ChapterRenderPhase) error {
			if phase == domain.ChapterRenderPhasePublished {
				return errors.New("crash")
			}
			return nil
		}
		t.Cleanup(func() { pipelineChapterRenderTransactionFaultHook = nil })
		if err := pipelineAdvanceChapterRenderPublished(
			live, frozen, plan, snapshot.BodySHA256,
		); err == nil {
			t.Fatal("expected injected crash after publish")
		}
		pipelineChapterRenderTransactionFaultHook = nil
		publisher := store.NewDirectoryPublishStore(pipelineRenderTransactionRoot(live))
		before, err := publisher.LoadDirectoryPublishReceiptEvidence(candidate.ID)
		if err != nil || before == nil {
			t.Fatalf("load immutable publish evidence under runtime drift: receipt=%+v err=%v", before, err)
		}
		if before.ReceiptDigest != publishReceipt.ReceiptDigest {
			t.Fatal("publish crash changed committed directory receipt")
		}
		if err := pipelineAdvanceChapterRenderPublished(
			live, frozen, plan, snapshot.BodySHA256,
		); err != nil {
			t.Fatalf("published phase replay must be idempotent: %v", err)
		}
		identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
		if err != nil {
			t.Fatal(err)
		}
		latest, err := store.NewChapterRenderTransactionStore(live).LoadLatest(identity)
		if err != nil || latest == nil || latest.Phase != domain.ChapterRenderPhasePublished ||
			latest.Evidence.DirectoryPublishDigest != publishReceipt.ReceiptDigest {
			t.Fatalf("publish replay duplicated or lost transaction evidence: latest=%+v err=%v", latest, err)
		}
		body, err := os.ReadFile(filepath.Join(live, "chapters", "01.md"))
		if err != nil || domain.ComputeChapterRenderBodySHA256(body) != snapshot.BodySHA256 {
			t.Fatalf("published live canon body drifted: err=%v", err)
		}
		if err := os.WriteFile(filepath.Join(live, "premise.md"), []byte("# drifted canon\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := pipelineAdvanceChapterRenderPublished(
			live, frozen, plan, snapshot.BodySHA256,
		); err == nil {
			t.Fatal("published phase replay accepted canonical drift")
		}
	})
}

func TestPipelineChapterRenderPublishedRejectsEveryCanonicalNamespaceDrift(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		body string
	}{
		{name: "premise", rel: "premise.md", body: "# changed premise\n"},
		{name: "characters", rel: "characters.json", body: "{\"changed\":true}\n"},
		{name: "old chapter", rel: "chapters/01.md", body: "changed accepted chapter\n"},
		{name: "summary", rel: "summaries/01.md", body: "changed summary\n"},
		{name: "meta canon", rel: "meta/state.json", body: "changed canonical state\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixtureForChapter(t, 2)
			if _, err := pipelineEnsureChapterRenderCommitted(
				live, candidate.OutputDir, frozen, plan, snapshot,
			); err != nil {
				t.Fatal(err)
			}
			mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
			if err := pipelineAdvanceChapterRenderFormal(
				live, candidate.OutputDir, frozen, plan, snapshot, true,
			); err != nil {
				t.Fatal(err)
			}
			if err := savePipelineSealedActualMatch(
				candidate.OutputDir, pipelineChapterRenderTransactionTestMatch(),
			); err != nil {
				t.Fatal(err)
			}
			if err := pipelineAdvanceChapterRenderActualMatch(
				live, candidate.OutputDir, frozen, snapshot.BodySHA256,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := publishPipelineRenderCandidate(live, candidate); err != nil {
				t.Fatal(err)
			}
			driftPath := filepath.Join(live, filepath.FromSlash(tt.rel))
			if err := os.MkdirAll(filepath.Dir(driftPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(driftPath, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := pipelineAdvanceChapterRenderPublished(
				live, frozen, plan, snapshot.BodySHA256,
			); err == nil {
				t.Fatalf("published boundary accepted %s drift", tt.rel)
			}
			identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
			if err != nil {
				t.Fatal(err)
			}
			latest, err := store.NewChapterRenderTransactionStore(live).LoadLatest(identity)
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || latest.Phase != domain.ChapterRenderPhaseActualMatched {
				t.Fatalf("canonical drift advanced transaction: %+v", latest)
			}
		})
	}
}

func TestPipelineChapterRenderCommittedRecoveryRejectsCanonicalDrift(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "meta", "state.json"),
		[]byte("drifted canonical state\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := preparePipelineRenderCandidate(live, frozen); err == nil {
		t.Fatal("committed recovery accepted canonical drift")
	}
}

func TestPipelineChapterRenderTransactionRejectKeepsDiagnosticAndCannotSkipRevalidation(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "rewrite")
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, false,
	); err != nil {
		t.Fatal(err)
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := store.NewChapterRenderTransactionStore(live).LoadReceipts(identity)
	if err != nil {
		t.Fatal(err)
	}
	rejected := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseFormalRejected)
	if rejected == nil || len(rejected.Evidence.ReviewArtifacts) != 5 {
		t.Fatalf("formal rejection diagnostic is incomplete: %+v", rejected)
	}
	if _, err := store.NewChapterRenderTransactionStore(live).Advance(
		identity,
		domain.ChapterRenderPhaseActualMatched,
		domain.ChapterRenderPhaseEvidence{ActualMatchDigest: pipelineBytesSHA([]byte("forbidden"))},
	); err == nil {
		t.Fatal("formal rejection must be terminal")
	}
}

func TestPipelineChapterRenderTransactionSameBodyCacheRevalidationAccepts(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "rewrite")
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, false,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, true,
	); err != nil {
		t.Fatalf("same-body cache-only formal revalidation was blocked: %v", err)
	}
	match := pipelineChapterRenderTransactionTestMatch()
	if err := savePipelineSealedActualMatch(candidate.OutputDir, match); err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderActualMatch(
		live, candidate.OutputDir, frozen, snapshot.BodySHA256,
	); err != nil {
		t.Fatalf("revalidated same body could not advance to actual match: %v", err)
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := store.NewChapterRenderTransactionStore(live).LoadReceipts(identity)
	if err != nil {
		t.Fatal(err)
	}
	rejected := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseFormalRejected)
	accepted := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseFormalAccepted)
	actual := pipelineChapterRenderReceiptForPhase(receipts, domain.ChapterRenderPhaseActualMatched)
	if rejected == nil || accepted == nil || actual == nil {
		t.Fatalf("revalidation chain incomplete: %+v", receipts)
	}
	if rejected.ReceiptDigest == accepted.ReceiptDigest ||
		accepted.PreviousReceiptDigest != rejected.ReceiptDigest ||
		len(rejected.Evidence.ReviewArtifacts) == 0 ||
		len(accepted.Evidence.ReviewArtifacts) == 0 ||
		rejected.Evidence.ReviewArtifacts[0].Digest == accepted.Evidence.ReviewArtifacts[0].Digest {
		t.Fatal("formal rejection diagnostic was overwritten instead of chained")
	}
}

func TestPipelineChapterRenderTransactionFinalReceiptCrashRecovery(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	legacyManifest, err := os.ReadFile(filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	projectAllCmdTestWriteFile(t, filepath.Join(live, "meta", "planning", "render_candidate.json"), string(legacyManifest))
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, true,
	); err != nil {
		t.Fatal(err)
	}
	match := pipelineChapterRenderTransactionTestMatch()
	if err := savePipelineSealedActualMatch(candidate.OutputDir, match); err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderActualMatch(
		live, candidate.OutputDir, frozen, snapshot.BodySHA256,
	); err != nil {
		t.Fatal(err)
	}
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	if err != nil {
		t.Fatal(err)
	}
	publishID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	publishDigest := pipelineBytesSHA([]byte("publish-receipt"))
	actualPreStateRoot := pipelineBytesSHA([]byte("actual-pre-state"))
	actualPostStateRoot, err := domain.DeriveProjectedPostStateRootV2(actualPreStateRoot, match.ActualDelta)
	if err != nil {
		t.Fatal(err)
	}
	outcome := domain.ActualOutcomeReceiptV2{
		Version:                     domain.ActualOutcomeReceiptV2Version,
		GenerationID:                frozen.PlanningGenerationID,
		Chapter:                     frozen.Chapter,
		PromotionReceiptDigest:      frozen.PromotionReceiptDigest,
		ChapterBodySHA256:           snapshot.BodySHA256,
		CommitCheckpointSeq:         snapshot.Commit.Seq,
		ActualDelta:                 match.ActualDelta,
		ActualPreStateRoot:          actualPreStateRoot,
		ActualPostStateRoot:         actualPostStateRoot,
		ActualCanonRoot:             pipelineBytesSHA([]byte("actual-canon")),
		ProjectedPostStateRoot:      actualPostStateRoot,
		ObligationsSatisfied:        []string{},
		ObligationsCreatedUnplanned: []string{},
		ProjectionMatch:             true,
		AcceptedAt:                  "2026-07-22T00:00:00Z",
	}
	outcome.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
	if err != nil {
		t.Fatal(err)
	}
	outcomeDigest := outcome.ReceiptDigest
	if _, err := writePipelinePlanningJSON(filepath.Join(
		live,
		"meta", "planning", "v2", "actual_outcomes",
		frozen.PlanningGenerationID,
		fmt.Sprintf("%04d", frozen.Chapter),
		outcomeDigest+".json",
	), outcome); err != nil {
		t.Fatal(err)
	}
	acceptance := domain.ChapterAcceptanceReceipt{
		Version:              domain.ChapterAcceptanceReceiptLegacyVersion,
		ArcID:                "arc:transaction-test",
		ArcManifestDigest:    pipelineBytesSHA([]byte("arc-manifest")),
		GenerationID:         frozen.PlanningGenerationID,
		Chapter:              frozen.Chapter,
		ChapterBodySHA256:    snapshot.BodySHA256,
		ChapterBodyRunes:     len([]rune(snapshot.Body)),
		ReviewArtifacts:      []domain.ChapterReviewArtifactBinding{{Path: "reviews/01.md", Digest: pipelineBytesSHA([]byte("review"))}},
		OutcomeReceiptDigest: outcomeDigest,
		AcceptedAt:           outcome.AcceptedAt,
	}
	acceptance, err = domain.SignChapterAcceptanceReceipt(acceptance)
	if err != nil {
		t.Fatal(err)
	}
	acceptanceDigest := acceptance.ReceiptDigest
	if _, err := writePipelinePlanningJSON(filepath.Join(
		live,
		"meta", "planning", "v3", "arc_cycle", "acceptances",
		frozen.PlanningGenerationID,
		fmt.Sprintf("%06d", frozen.Chapter),
		acceptanceDigest+".json",
	), acceptance); err != nil {
		t.Fatal(err)
	}
	txnStore := store.NewChapterRenderTransactionStore(live)
	if _, err := txnStore.Advance(identity, domain.ChapterRenderPhasePublished, domain.ChapterRenderPhaseEvidence{
		DirectoryPublishID: publishID, DirectoryPublishDigest: publishDigest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := txnStore.Advance(identity, domain.ChapterRenderPhaseOutcomeAccepted, domain.ChapterRenderPhaseEvidence{
		OutcomeReceiptDigest: outcomeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := txnStore.Advance(identity, domain.ChapterRenderPhaseChapterAccepted, domain.ChapterRenderPhaseEvidence{
		ChapterAcceptanceDigest: acceptanceDigest,
	}); err != nil {
		t.Fatal(err)
	}
	receipt := pipelineRenderReceipt{
		Version:                pipelinePlanningSchema,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		CommitDigest:           snapshot.Commit.Digest,
		CommitCheckpointSeq:    snapshot.Commit.Seq,
		ChapterPath:            snapshot.ChapterPath,
		ChapterBodySHA256:      snapshot.BodySHA256,
		ActualCanonRoot:        snapshot.ActualCanonRoot,
		ProjectionBound:        true,
		PlanningGenerationID:   frozen.PlanningGenerationID,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		OutcomeReceiptDigest:   outcomeDigest,
		DirectoryPublishID:     publishID,
		DirectoryPublishDigest: publishDigest,
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(live, pipelineRenderReceiptPath), receipt); err != nil {
		t.Fatal(err)
	}
	if err := pipelineRecoverChapterRenderCompletionIfTracked(live, frozen, snapshot.BodySHA256); err != nil {
		t.Fatalf("final render receipt crash recovery failed: %v", err)
	}
	latest, err := txnStore.LoadLatest(identity)
	if err != nil || latest == nil || latest.Phase != domain.ChapterRenderPhaseCompleted {
		t.Fatalf("completed phase missing after replay: latest=%+v err=%v", latest, err)
	}
}

func TestPipelineChapterRenderTransactionV3StyleAcceptanceFinalChain(t *testing.T) {
	opts, setupStore, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	live := setupStore.Dir()
	frozen, plan, err := loadAndVerifyPipelineFrozenPlan(live)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	styleReceipt, err := tools.PublishEffectiveRenderStyleContract(
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
		"v3-chain-style",
		"# V3 闭环风格\n- 叙述声音：克制、具体。\n- 句法：压力处缩短。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindPipelineRenderCandidateEffectiveStyle(candidate.OutputDir, frozen, styleReceipt); err != nil {
		t.Fatal(err)
	}

	prefix := "第一章\n\n主角在截止前完成小额验证，并把可复核票据收进衣袋。"
	body := prefix + strings.Repeat("县", 2100-len([]rune(prefix)))
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "commit", "chapters/01.md",
		"plan", "draft", "edit", "commit",
	); err != nil {
		t.Fatal(err)
	}
	progress, err := candidateStore.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load candidate progress: %+v %v", progress, err)
	}
	progress.CompletedChapters = []int{1}
	if err := candidateStore.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, 1, "accept")
	mustPersistReviewModelProvenanceForBody(t, candidate.OutputDir, 1, body)
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, true,
	); err != nil {
		t.Fatal(err)
	}
	binding, err := validatePipelineSealedRenderBinding(store.NewStore(live), frozen, false)
	if err != nil {
		t.Fatal(err)
	}
	actualMatch := pipelineSealedActualDeltaMatch{
		ActualDelta:          binding.Bundle.ProjectedDelta,
		ProjectionMatch:      true,
		Complete:             true,
		ObligationsSatisfied: append([]string(nil), binding.Bundle.ObligationsConsumed...),
	}
	if err := savePipelineSealedActualMatch(candidate.OutputDir, actualMatch); err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderActualMatch(
		live, candidate.OutputDir, frozen, snapshot.BodySHA256,
	); err != nil {
		t.Fatal(err)
	}
	publishReceipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}
	liveSnapshot, err := loadPipelineRenderedChapterSnapshot(live, frozen, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderPublished(live, frozen, plan, liveSnapshot.BodySHA256); err != nil {
		t.Fatal(err)
	}
	if err := finalizePipelineRenderCandidate(live, publishReceipt.TransactionID); err != nil {
		t.Fatal(err)
	}
	liveStore := store.NewStore(live)
	outcome, err := acceptPipelineSealedRenderOutcome(
		liveStore,
		binding,
		liveSnapshot.Commit,
		liveSnapshot.BodySHA256,
		pipelineProjectAllCanonRootFromSnapshot(liveSnapshot.ActualCanonRoot),
		&actualMatch,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderOutcome(
		live, frozen, liveSnapshot.Commit, liveSnapshot.BodySHA256, outcome,
	); err != nil {
		t.Fatal(err)
	}
	acceptance, err := savePipelineChapterAcceptance(
		live, liveStore, &binding.Generation, 1, liveSnapshot.BodySHA256, outcome,
	)
	if err != nil {
		t.Fatal(err)
	}
	if acceptance.Version != domain.ChapterAcceptanceReceiptVersion ||
		acceptance.EffectiveStyleReceiptDigest != styleReceipt.ReceiptDigest {
		t.Fatalf("v3 acceptance lost effective style identity: %+v", acceptance)
	}
	if err := pipelineAdvanceChapterRenderAcceptance(
		live, frozen, liveSnapshot.BodySHA256, acceptance,
	); err != nil {
		t.Fatal(err)
	}
	styleEvidence, err := pipelineEffectiveStyleArchiveEvidence(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	progress, err = liveStore.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load live progress: %+v %v", progress, err)
	}
	actualCanonRoot, err := pipelineCanonRoot(live, progress)
	if err != nil {
		t.Fatal(err)
	}
	finalReceipt := pipelineRenderReceipt{
		Version:                      pipelinePlanningSchema,
		Chapter:                      1,
		PlanDigest:                   frozen.PlanDigest,
		PlanCheckpointSeq:            frozen.PlanCheckpointSeq,
		CommitDigest:                 liveSnapshot.Commit.Digest,
		CommitCheckpointSeq:          liveSnapshot.Commit.Seq,
		ChapterPath:                  liveSnapshot.ChapterPath,
		ChapterBodySHA256:            liveSnapshot.BodySHA256,
		ActualCanonRoot:              actualCanonRoot,
		RenderDependencySHA256:       maps.Clone(frozen.RenderDependencySHA256),
		PipelineRunInputDigest:       frozen.PipelineRunInputDigest,
		RenderContextSHA256:          frozen.RenderContextSHA256,
		EffectiveStyleReceiptPath:    styleEvidence.Path,
		EffectiveStyleReceiptDigest:  styleEvidence.ReceiptDigest,
		EffectiveStyleArtifactSHA256: styleEvidence.ArtifactSHA256,
		ProjectedStateRoot:           frozen.ProjectedPostStateRoot,
		ProjectionBound:              true,
		PlanningGenerationID:         frozen.PlanningGenerationID,
		ProjectedBundleDigest:        frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:       frozen.PromotionReceiptDigest,
		OutcomeReceiptDigest:         outcome.ReceiptDigest,
		ChapterAcceptanceDigest:      acceptance.ReceiptDigest,
		DirectoryPublishID:           publishReceipt.TransactionID,
		DirectoryPublishDigest:       publishReceipt.ReceiptDigest,
		RenderedAt:                   outcome.AcceptedAt,
	}
	finalDigest, err := writePipelinePlanningJSON(
		filepath.Join(live, pipelineRenderReceiptPath), finalReceipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipelineAdvanceChapterRenderCompleted(
		live, frozen, liveSnapshot.BodySHA256, finalDigest,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPipelineRenderStage(live, domain.PipelineStageEvidence{Stage: "render"}); err != nil {
		t.Fatalf("complete v3 style/acceptance/final chain failed verification: %v", err)
	}

	reviewPath := filepath.Join(live, acceptance.ReviewArtifacts[0].Path)
	originalReview, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, append(originalReview, []byte("\n漂移")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPipelineRenderStage(live, domain.PipelineStageEvidence{Stage: "render"}); err == nil ||
		!strings.Contains(err.Error(), "审核/风格证据已漂移") {
		t.Fatalf("v3 final accepted review artifacts outside its acceptance chain: %v", err)
	}
}

func mustPersistReviewModelProvenanceForBody(t *testing.T, dir string, chapter int, body string) {
	t.Helper()
	chapterReviewContext, err := buildEditorChapterReviewContextWithStyle(
		store.NewStore(dir), chapter, tools.References{}, "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	editorPolicy := newEditorReviewCachePolicy(
		"test-openai", "editor-test", "premise", "rules", chapterReviewContext,
		chapter, body, "ai-voice-context",
	)
	editorCache := &editorReviewCacheArtifact{
		Chapter:     chapter,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		CacheKey:    reviewExistingCacheKey(editorPolicy),
		CachePolicy: editorPolicy,
		Markdown:    editorCacheTestMarkdown,
	}
	if err := saveEditorReviewCache(dir, editorCache); err != nil {
		t.Fatal(err)
	}
	selection := deepseekAIJudgeModelSelection{
		Provider: "deepseek", Model: "deepseek-test", Explicit: true,
	}
	deepSeekPolicy := newDeepSeekAIJudgeCachePolicy(selection, chapter, body)
	deepSeek := &deepseekAIJudgeArtifact{
		Chapter: chapter, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		CacheKey: reviewExistingCacheKey(deepSeekPolicy), CachePolicy: deepSeekPolicy,
		Provider: selection.Provider, Model: selection.Model, ReviewerExplicit: selection.Explicit,
		ReasoningEffort: string(deepseekAIJudgeReasoningEffort), RawBodyOnly: true,
		UserPayloadKind: "chapter_body_only", BodySHA256: reviewreport.BodySHA256(body),
		Verdict: "human_like", RiskLevel: "low", AIProbabilityPercent: 2,
		PassExclusivePercent: deepseekAIJudgePassExclusive, Confidence: "high",
		AdviceComplete: true, AttemptCount: 1, Summary: "裸正文外审通过。",
		RawResponse: deepseekCompleteHumanResponse, ModelSelection: selection,
	}
	if err := saveDeepSeekAIJudgeCache(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudge(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	if err := appendDeepSeekAIJudgeToUnifiedMarkdown(dir, deepSeek); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildReviewSummary(dir); err != nil {
		t.Fatal(err)
	}
	var finalEditor domain.ReviewEntry
	readJSONFileForFreshness(
		t, filepath.Join(dir, "reviews", fmt.Sprintf("%02d.json", chapter)), &finalEditor,
	)
	if err := persistReviewModelProvenance(
		store.NewStore(dir), chapter, reviewreport.BodySHA256(body), editorCache, &finalEditor, deepSeek,
	); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineChapterRenderTransactionLegacyPostCommitRemainsUntracked(t *testing.T) {
	live, frozen, _, _, snapshot := newPipelineChapterRenderTransactionFixture(t)
	tracked, err := pipelineChapterRenderBodyTracked(live, frozen, snapshot.BodySHA256)
	if err != nil {
		t.Fatal(err)
	}
	if tracked {
		t.Fatal("legacy body without directory/transaction evidence was adopted")
	}
	txnRoot := store.NewChapterRenderTransactionStore(live).Root()
	if _, err := os.Stat(txnRoot); !os.IsNotExist(err) {
		t.Fatalf("read-only legacy probe created a partial transaction root: %v", err)
	}
}

func TestPipelineChapterRenderTransactionAcceptedArtifactDriftFailsClosed(t *testing.T) {
	live, frozen, plan, candidate, snapshot := newPipelineChapterRenderTransactionFixture(t)
	if _, err := pipelineEnsureChapterRenderCommitted(
		live, candidate.OutputDir, frozen, plan, snapshot,
	); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
	if err := pipelineAdvanceChapterRenderFormal(
		live, candidate.OutputDir, frozen, plan, snapshot, true,
	); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(candidate.OutputDir, "reviews", "01.md")
	if err := os.WriteFile(report, []byte("tampered review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := preparePipelineRenderCandidate(live, frozen); err == nil {
		t.Fatal("accepted artifact drift must fail closed instead of dispatching Writer/Reviewer")
	}
}

func newPipelineChapterRenderTransactionFixture(t *testing.T) (
	string,
	*pipelineFrozenPlan,
	*domain.Checkpoint,
	*pipelineRenderCandidate,
	*pipelineRenderedChapterSnapshot,
) {
	t.Helper()
	return newPipelineChapterRenderTransactionFixtureForChapter(t, 1)
}

func newPipelineChapterRenderTransactionFixtureForChapter(t *testing.T, chapter int) (
	string,
	*pipelineFrozenPlan,
	*domain.Checkpoint,
	*pipelineRenderCandidate,
	*pipelineRenderedChapterSnapshot,
) {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	planRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.plan.json", chapter)))
	planPath := filepath.Join(live, filepath.FromSlash(planRel))
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(fmt.Sprintf("{\"chapter\":%d}\n", chapter)), 0o644); err != nil {
		t.Fatal(err)
	}
	liveStore := store.NewStore(live)
	plan, err := liveStore.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "plan", planRel,
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := &pipelineFrozenPlan{
		Version:                pipelinePlanningSchema,
		Chapter:                chapter,
		PlanPath:               planRel,
		PlanDigest:             plan.Digest,
		PlanCheckpointSeq:      plan.Seq,
		BaselineCommitSeq:      0,
		PlanningGenerationID:   fmt.Sprintf("pg2_chapter_render_transaction_test_%d", chapter),
		PlanningDependencyRoot: pipelineBytesSHA([]byte("planning-dependencies")),
		ProjectionBinding:      "sealed_v2",
		ProjectedPlanSHA256:    pipelineBytesSHA([]byte("projected-plan")),
		ProjectedPreStateRoot:  pipelineBytesSHA([]byte("projected-pre-state")),
		ProjectedPostStateRoot: pipelineBytesSHA([]byte("projected-post-state")),
		ProjectedBundleDigest:  pipelineBytesSHA([]byte("bundle")),
		PromotionReceiptDigest: pipelineBytesSHA([]byte("promotion")),
		PipelineRunInputDigest: pipelineBytesSHA([]byte("render-input")),
	}
	renderContext := freezeTestDraftRenderContext(t, liveStore, chapter, plan.Digest)
	renderDependencies, err := capturePipelineFrozenRenderDependencies(live)
	if err != nil {
		t.Fatal(err)
	}
	frozen.RenderDependencySHA256 = renderDependencies
	frozen.RenderContextPath = tools.FrozenDraftRenderContextPath
	frozen.RenderContextSHA256 = renderContext.PayloadSHA256
	frozen.FrozenAt = renderContext.FrozenAt
	if _, err := writePipelinePlanningJSON(filepath.Join(live, pipelineFrozenPlanPath), frozen); err != nil {
		t.Fatal(err)
	}
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	container := filepath.Join(pipelineRenderCandidateRoot(live), id)
	candidate, err := prepareFreshPipelineRenderCandidateForStyleEpoch(
		live, frozen, id, container, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	mustUseLegacyPipelineRenderCandidateForTest(t, candidate, frozen)
	const body = "第一章\n\n门外脚步停住。林澈把录音时间写在纸角，随后关掉了灯。"
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter),
		"draft",
		fmt.Sprintf("drafts/%02d.draft.md", frozen.Chapter),
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Drafts.SaveFinalChapter(frozen.Chapter, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter),
		"commit",
		fmt.Sprintf("chapters/%02d.md", frozen.Chapter),
		"plan", "draft", "edit", "commit",
	); err != nil {
		t.Fatal(err)
	}
	completed := make([]int, chapter)
	for i := range completed {
		completed[i] = i + 1
	}
	if err := candidateStore.Progress.Save(&domain.Progress{
		GenerationID:      frozen.PlanningGenerationID,
		CompletedChapters: completed,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, plan)
	if err != nil {
		t.Fatal(err)
	}
	return live, frozen, plan, candidate, snapshot
}

func pipelineChapterRenderTransactionTestMatch() pipelineSealedActualDeltaMatch {
	return pipelineSealedActualDeltaMatch{
		ActualDelta: domain.ProjectedDelta{
			Version: domain.ProjectedDeltaV2Version,
			Timeline: []domain.StateMutationV2{{
				StableID:  "timeline:test:1",
				Subject:   "测试时间线",
				Field:     "阶段",
				Operation: "advance",
				After:     "已发生",
				Cause:     "正文证据",
			}},
		},
		ProjectionMatch: true,
		Complete:        true,
	}
}

func countPipelineChapterRenderCheckpoint(st *store.Store, chapter int, step string) int {
	count := 0
	for _, checkpoint := range st.Checkpoints.All() {
		if checkpoint.Scope.Matches(domain.ChapterScope(chapter)) && checkpoint.Step == step {
			count++
		}
	}
	return count
}
