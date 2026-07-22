package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

type pipelineAcceptedActualMismatchFixture struct {
	live           string
	frozen         *pipelineFrozenPlan
	planCheckpoint *domain.Checkpoint
	candidate      *pipelineRenderCandidate
	body           string
	bodySHA256     string
	actualMatchSHA string
}

func TestPreparePipelineRenderCandidateRecoversOnlyAcceptedActualMismatchForRematch(t *testing.T) {
	fixture := newPipelineAcceptedActualMismatchFixture(t)
	if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.RecoveredAcceptedActualMismatch {
		t.Fatal("formal-accepted actual-mismatch candidate did not enter deterministic rematch recovery")
	}
	got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter)
	if err != nil || got != fixture.body {
		t.Fatalf("recovered body=%q err=%v, want exact accepted bytes", got, err)
	}

	snapshot, skipWriter, err := loadPipelineRecoveredActualMismatchSnapshot(
		recovered,
		fixture.frozen,
		fixture.planCheckpoint,
	)
	if err != nil || !skipWriter || snapshot == nil || snapshot.Body != fixture.body {
		t.Fatalf("accepted rematch did not select the no-Writer path: snapshot=%+v skip=%v err=%v", snapshot, skipWriter, err)
	}
	preflightCalls, formalCalls, finalMatcherCalls := 0, 0, 0
	if !skipWriter {
		_ = runPipelineSealedFormalReviewAfterBodyEvidence(
			recovered.OutputDir,
			snapshot,
			func(*pipelineRenderedChapterSnapshot) (pipelineSealedActualDeltaMatch, error) {
				preflightCalls++
				return pipelineSealedActualDeltaMatch{ProjectionMatch: true, Complete: true}, nil
			},
			func() error {
				formalCalls++
				return nil
			},
		)
	} else {
		// This is the caller's unchanged post-return deterministic rematch.
		finalMatcherCalls++
	}
	if preflightCalls != 0 || formalCalls != 0 || finalMatcherCalls != 1 {
		t.Fatalf(
			"formal-accepted recovery calls preflight/formal/final=%d/%d/%d, want 0/0/1",
			preflightCalls,
			formalCalls,
			finalMatcherCalls,
		)
	}

	receiptRaw, err := os.ReadFile(filepath.Join(
		recovered.OutputDir,
		"meta",
		"planning",
		"render_candidate_actual_mismatch_recovery.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var receipt pipelineRenderActualMismatchRecoveryReceipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.CandidateID != recovered.ID ||
		receipt.PipelineRunInputDigest != fixture.frozen.PipelineRunInputDigest ||
		receipt.BodySHA256 != fixture.bodySHA256 ||
		receipt.SourceActualMatchSHA256 != fixture.actualMatchSHA {
		t.Fatalf("actual-mismatch recovery receipt lost exact input/body binding: %+v", receipt)
	}

	// Recovery does not bless the old matcher result.  The caller still sees a
	// negative receipt and must execute matchPipelineSealedRenderActualDelta
	// again before directory publication.
	var oldMatch pipelineSealedActualDeltaMatch
	oldMatchRaw, err := os.ReadFile(filepath.Join(
		recovered.OutputDir,
		"meta",
		"planning",
		"sealed_actual_match.json",
	))
	if err != nil || json.Unmarshal(oldMatchRaw, &oldMatch) != nil || oldMatch.ProjectionMatch {
		t.Fatalf("recovery silently promoted the old matcher result: match=%+v err=%v", oldMatch, err)
	}
}

func TestPreparePipelineRenderCandidateRecoversFirstPassAcceptedActualMismatch(t *testing.T) {
	fixture := newPipelineAcceptedActualMismatchFixture(t)
	manifest, err := loadPipelineRenderCandidateManifest(fixture.candidate.OutputDir)
	if err != nil || manifest == nil {
		t.Fatalf("load fixture manifest: manifest=%+v err=%v", manifest, err)
	}
	ledger, err := loadPipelineRenderConvergenceLedger(fixture.live, *manifest, 3)
	if err != nil {
		t.Fatal(err)
	}
	record := findPipelineRenderConvergenceRecord(ledger, fixture.bodySHA256)
	if record == nil {
		t.Fatal("fixture convergence record is absent")
	}
	// A first-pass accepted body was never semantically rejected, so there is no
	// FormalAccepted resolution event. Exact review and external-gate evidence
	// are the authority for deterministic matcher recovery.
	record.StructuralBlock = false
	record.SemanticReject = false
	record.FormalAccepted = false
	if err := savePipelineRenderConvergenceLedger(fixture.live, ledger); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.RecoveredAcceptedActualMismatch {
		t.Fatal("first-pass formal accept did not enter deterministic actual-mismatch rematch")
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter); err != nil || got != fixture.body {
		t.Fatalf("recovered first-pass body=%q err=%v", got, err)
	}
}

func TestPreparePipelineRenderCandidateDoesNotCrossRenderInputForActualMismatch(t *testing.T) {
	fixture := newPipelineAcceptedActualMismatchFixture(t)
	if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
		t.Fatal(err)
	}
	drifted := *fixture.frozen
	drifted.PipelineRunInputDigest = "sha256:different-render-input"

	recovered, err := preparePipelineRenderCandidate(fixture.live, &drifted)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RecoveredAcceptedActualMismatch {
		t.Fatal("actual-mismatch recovery crossed PipelineRunInputDigest identity")
	}
	if got, _ := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(drifted.Chapter); strings.TrimSpace(got) != "" {
		t.Fatalf("different render input inherited accepted prose: %q", got)
	}
}

func TestPreparePipelineRenderCandidateDoesNotBypassRejectedActualMismatchProse(t *testing.T) {
	fixture := newPipelineAcceptedActualMismatchFixture(t)
	reviewPath := filepath.Join(
		fixture.candidate.OutputDir,
		"reviews",
		"01.json",
	)
	var review domain.ReviewEntry
	reviewRaw, err := os.ReadFile(reviewPath)
	if err != nil || json.Unmarshal(reviewRaw, &review) != nil {
		t.Fatalf("load fixture review: %v", err)
	}
	review.Verdict = "rewrite"
	review.AffectedChapters = []int{fixture.frozen.Chapter}
	mustWriteJSONFile(t, reviewPath, review)
	if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RecoveredAcceptedActualMismatch {
		t.Fatal("rewrite verdict entered the accepted no-Writer rematch path")
	}
	if got, _ := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter); strings.TrimSpace(got) != "" {
		t.Fatalf("rejected actual-mismatch prose was replayed as accepted: %q", got)
	}
}

func TestPreparePipelineRenderCandidateRequiresExactBodyFormalResolutionForActualMismatch(t *testing.T) {
	t.Run("review body hash drift", func(t *testing.T) {
		fixture := newPipelineAcceptedActualMismatchFixture(t)
		reviewPath := filepath.Join(fixture.candidate.OutputDir, "reviews", "01.json")
		var review domain.ReviewEntry
		reviewRaw, err := os.ReadFile(reviewPath)
		if err != nil || json.Unmarshal(reviewRaw, &review) != nil {
			t.Fatalf("load fixture review: %v", err)
		}
		review.BodySHA256 = strings.Repeat("f", 64)
		mustWriteJSONFile(t, reviewPath, review)
		if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
			t.Fatal(err)
		}
		recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.RecoveredAcceptedActualMismatch {
			t.Fatal("stale-body review entered actual-mismatch recovery")
		}
	})

	t.Run("formal resolution absent", func(t *testing.T) {
		fixture := newPipelineAcceptedActualMismatchFixture(t)
		manifest, err := loadPipelineRenderCandidateManifest(fixture.candidate.OutputDir)
		if err != nil || manifest == nil {
			t.Fatalf("load fixture manifest: manifest=%+v err=%v", manifest, err)
		}
		ledger, err := loadPipelineRenderConvergenceLedger(fixture.live, *manifest, 3)
		if err != nil {
			t.Fatal(err)
		}
		record := findPipelineRenderConvergenceRecord(ledger, fixture.bodySHA256)
		if record == nil {
			t.Fatal("fixture convergence record is absent")
		}
		record.FormalAccepted = false
		if err := savePipelineRenderConvergenceLedger(fixture.live, ledger); err != nil {
			t.Fatal(err)
		}
		if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
			t.Fatal(err)
		}
		recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.RecoveredAcceptedActualMismatch {
			t.Fatal("unresolved formal rejection entered actual-mismatch recovery")
		}
	})
}

func TestPreparePipelineRenderCandidateRejectsActualMismatchWithoutCurrentExternalPass(t *testing.T) {
	t.Run("current judge artifact missing", func(t *testing.T) {
		fixture := newPipelineAcceptedActualMismatchFixture(t)
		judgePath := filepath.Join(
			fixture.candidate.OutputDir,
			"reviews",
			"drafts",
			"01_deepseek_ai_judge.json",
		)
		if err := os.Remove(judgePath); err != nil {
			t.Fatal(err)
		}
		if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
			t.Fatal(err)
		}
		recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.RecoveredAcceptedActualMismatch {
			t.Fatal("missing current DeepSeek artifact entered deterministic rematch")
		}
	})

	t.Run("convergence records external block", func(t *testing.T) {
		fixture := newPipelineAcceptedActualMismatchFixture(t)
		manifest, err := loadPipelineRenderCandidateManifest(fixture.candidate.OutputDir)
		if err != nil || manifest == nil {
			t.Fatalf("load fixture manifest: manifest=%+v err=%v", manifest, err)
		}
		ledger, err := loadPipelineRenderConvergenceLedger(fixture.live, *manifest, 3)
		if err != nil {
			t.Fatal(err)
		}
		record := findPipelineRenderConvergenceRecord(ledger, fixture.bodySHA256)
		if record == nil {
			t.Fatal("fixture convergence record is absent")
		}
		record.ExternalBlocking = true
		if err := savePipelineRenderConvergenceLedger(fixture.live, ledger); err != nil {
			t.Fatal(err)
		}
		if err := retirePipelineRenderCandidate(fixture.candidate.ContainerDir, "actual-mismatch"); err != nil {
			t.Fatal(err)
		}
		recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.RecoveredAcceptedActualMismatch {
			t.Fatal("historical external block entered deterministic rematch")
		}
	})
}

func newPipelineAcceptedActualMismatchFixture(t *testing.T) pipelineAcceptedActualMismatchFixture {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := liveStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanPath = "drafts/01.plan.json"
	frozen.PlanDigest = planCheckpoint.Digest
	frozen.PlanCheckpointSeq = planCheckpoint.Seq
	frozen.PlanningDependencyRoot = pipelineBytesSHA([]byte("actual-mismatch-planning-dependencies"))
	frozen.ProjectedPlanSHA256 = pipelineBytesSHA([]byte("actual-mismatch-projected-plan"))
	frozen.ProjectedPreStateRoot = pipelineBytesSHA([]byte("actual-mismatch-pre-state"))
	frozen.ProjectedPostStateRoot = pipelineBytesSHA([]byte("actual-mismatch-post-state"))
	renderContext := freezeTestDraftRenderContext(t, liveStore, frozen.Chapter, frozen.PlanDigest)
	renderDependencies, err := capturePipelineFrozenRenderDependencies(live)
	if err != nil {
		t.Fatal(err)
	}
	frozen.RenderDependencySHA256 = renderDependencies
	frozen.RenderContextPath = toolspkg.FrozenDraftRenderContextPath
	frozen.RenderContextSHA256 = renderContext.PayloadSHA256
	frozen.FrozenAt = renderContext.FrozenAt
	if _, err := writePipelinePlanningJSON(
		filepath.Join(live, filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	mustUseLegacyPipelineRenderCandidateForTest(t, candidate, frozen)
	if _, err := writePipelinePlanningJSON(
		filepath.Join(candidate.OutputDir, filepath.FromSlash(pipelineFrozenPlanPath)),
		frozen,
	); err != nil {
		t.Fatalf("persist isolated frozen identity fixture: %v", err)
	}

	const body = "第一章\n\n门外的脚步停了。林澈没有开门，只把录音时间写在纸角。"
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter),
		"edit",
		"drafts/01.draft.md",
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
		"chapters/01.md",
		"plan", "draft", "edit", "commit",
	); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Progress.Save(&domain.Progress{CompletedChapters: []int{frozen.Chapter}}); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, frozen.Chapter, "accept")
	if err := requirePipelineAcceptedExactReview(candidate.OutputDir, frozen.Chapter); err != nil {
		t.Fatalf("fixture exact review is not accepted: %v", err)
	}
	selection := deepseekAIJudgeModelSelection{
		Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true,
	}
	judge, err := runDeepSeekAIJudge(
		&reviewCacheModel{response: deepseekCompleteHumanResponse},
		selection,
		frozen.Chapter,
		body,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudgeCache(candidate.OutputDir, judge); err != nil {
		t.Fatal(err)
	}
	if err := saveDraftDeepSeekAIJudge(candidate.OutputDir, judge); err != nil {
		t.Fatal(err)
	}
	gate, err := toolspkg.InspectDraftExternalGateWithStore(candidateStore, frozen.Chapter)
	if err != nil || gate.Status != toolspkg.DraftExternalGateApproved {
		t.Fatalf("fixture exact-body external gate is not approved: gate=%+v err=%v", gate, err)
	}

	bodySHA := reviewreport.BodySHA256(body)
	manifest, err := loadPipelineRenderCandidateManifest(candidate.OutputDir)
	if err != nil || manifest == nil {
		t.Fatalf("load fixture manifest: manifest=%+v err=%v", manifest, err)
	}
	ledger := newPipelineRenderConvergenceLedger(*manifest, 3)
	record := pipelineRenderConvergenceRecordFor(&ledger, bodySHA)
	record.WholeDraft = true
	record.ExternalJudged = true
	record.StructuralBlock = true
	record.SemanticReject = true
	record.FormalAccepted = true
	if err := savePipelineRenderConvergenceLedger(live, &ledger); err != nil {
		t.Fatal(err)
	}

	actualMatch := pipelineSealedActualDeltaMatch{
		ActualDelta: domain.ProjectedDelta{
			Version: domain.ProjectedDeltaV2Version,
			Timeline: []domain.StateMutationV2{{
				StableID:  "timeline:fixture",
				Subject:   "chapter:1",
				Field:     "time",
				Operation: "advance",
				After:     "next beat",
				Cause:     "accepted actual-mismatch fixture",
			}},
		},
		ProjectionMatch: false,
		Complete:        true,
		MismatchReasons: []string{"old deterministic locator did not aggregate the multi-span beat"},
	}
	if err := savePipelineSealedActualMatch(candidate.OutputDir, actualMatch); err != nil {
		t.Fatal(err)
	}
	actualMatchRaw, err := os.ReadFile(filepath.Join(
		candidate.OutputDir,
		"meta",
		"planning",
		"sealed_actual_match.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	return pipelineAcceptedActualMismatchFixture{
		live:           live,
		frozen:         frozen,
		planCheckpoint: planCheckpoint,
		candidate:      candidate,
		body:           body,
		bodySHA256:     bodySHA,
		actualMatchSHA: pipelineBytesSHA(actualMatchRaw),
	}
}
