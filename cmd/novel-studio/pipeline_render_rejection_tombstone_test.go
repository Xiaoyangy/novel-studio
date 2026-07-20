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
)

func TestPersistPipelineRenderSemanticRejectionRequiresCompleteFreshNonAccept(t *testing.T) {
	tests := []struct {
		name       string
		inspection func(currentChapterReviewEvidence) currentChapterReviewEvidence
	}{
		{
			name: "accepted review",
			inspection: func(value currentChapterReviewEvidence) currentChapterReviewEvidence {
				value.Verdict = "accept"
				value.Disposition = "否"
				return value
			},
		},
		{
			name: "freshness failure",
			inspection: func(value currentChapterReviewEvidence) currentChapterReviewEvidence {
				value.Issues = []string{"reviews/01.md (current body fingerprint missing)"}
				return value
			},
		},
		{
			name: "incomplete artifacts",
			inspection: func(value currentChapterReviewEvidence) currentChapterReviewEvidence {
				value.Artifacts = value.Artifacts[:len(value.Artifacts)-1]
				return value
			},
		},
		{
			name: "missing verdict",
			inspection: func(value currentChapterReviewEvidence) currentChapterReviewEvidence {
				value.Verdict = ""
				return value
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := pipelineRenderCandidateTestLive(t)
			frozen := pipelineRenderCandidateTestFrozen()
			id, err := pipelineRenderTransactionID(frozen)
			if err != nil {
				t.Fatal(err)
			}
			const body = "第一章\n\n完整审核所绑定的正文。"
			bodySHA := reviewreport.BodySHA256(body)
			candidate := &pipelineRenderCandidate{ID: id}
			snapshot := &pipelineRenderedChapterSnapshot{
				Body:       body,
				BodySHA256: domain.PlanningV2DigestPrefix + bodySHA,
			}
			inspection := tc.inspection(pipelineRenderRejectedReviewEvidence(frozen.Chapter, bodySHA))
			persisted, err := persistPipelineRenderSemanticRejection(
				live, candidate, frozen, snapshot, inspection,
			)
			if err != nil {
				t.Fatal(err)
			}
			if persisted {
				t.Fatal("non-semantic/incomplete review unexpectedly produced a tombstone")
			}
			path, err := pipelineRenderRejectionTombstonePath(live, id, bodySHA)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("freshness/operational path left tombstone %s: %v", path, err)
			}
		})
	}
}

func TestPersistPipelineRenderSemanticRejectionWritesVersionedExactBodyTombstone(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen := pipelineRenderCandidateTestFrozen()
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	const body = "第一章\n\n这一份正文完成审核后被明确拒绝。"
	bodySHA := reviewreport.BodySHA256(body)
	candidate := &pipelineRenderCandidate{ID: id}
	snapshot := &pipelineRenderedChapterSnapshot{
		Body:       body,
		BodySHA256: domain.PlanningV2DigestPrefix + bodySHA,
	}
	inspection := pipelineRenderRejectedReviewEvidence(frozen.Chapter, bodySHA)
	for attempt := 0; attempt < 2; attempt++ {
		persisted, err := persistPipelineRenderSemanticRejection(
			live, candidate, frozen, snapshot, inspection,
		)
		if err != nil || !persisted {
			t.Fatalf("attempt %d persisted=%v err=%v", attempt, persisted, err)
		}
	}
	path, err := pipelineRenderRejectionTombstonePath(live, id, bodySHA)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tombstone pipelineRenderRejectionTombstone
	if err := json.Unmarshal(raw, &tombstone); err != nil {
		t.Fatal(err)
	}
	if tombstone.Version != pipelineRenderRejectionTombstoneVersion ||
		tombstone.CandidateID != id || tombstone.BodySHA256 != bodySHA ||
		tombstone.Verdict != "rewrite" || tombstone.Disposition != "是" {
		t.Fatalf("wrong tombstone: %+v", tombstone)
	}
	if exists, err := pipelineRenderBodyHasSemanticRejection(live, frozen, id, bodySHA); err != nil || !exists {
		t.Fatalf("exact tombstone exists=%v err=%v", exists, err)
	}
}

func TestPersistPipelineRenderSemanticRejectionRejectsUnboundSnapshotHashDomain(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen := pipelineRenderCandidateTestFrozen()
	id, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		t.Fatal(err)
	}
	const body = "第一章\n\n审核正文与 checkpoint 必须各自使用正确的 hash 域。"
	bodySHA := reviewreport.BodySHA256(body)
	persisted, err := persistPipelineRenderSemanticRejection(
		live,
		&pipelineRenderCandidate{ID: id},
		frozen,
		&pipelineRenderedChapterSnapshot{
			Body: body,
			// A rendered snapshot is bound to the checkpoint digest domain.
			// Supplying the review/path hash here must remain fail-closed.
			BodySHA256: bodySHA,
		},
		pipelineRenderRejectedReviewEvidence(frozen.Chapter, bodySHA),
	)
	if err == nil || !strings.Contains(err.Error(), "exact-body identity mismatch") || persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
	path, pathErr := pipelineRenderRejectionTombstonePath(live, id, bodySHA)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("identity-drifted snapshot left tombstone %s: %v", path, statErr)
	}
}

func TestPreparePipelineRenderCandidateSkipsTombstonedStaleExactBody(t *testing.T) {
	live, frozen, candidate, body := pipelineRenderRecoverableCandidateFixture(t)
	bodySHA := reviewreport.BodySHA256(body)
	persisted, err := persistPipelineRenderSemanticRejection(
		live,
		candidate,
		frozen,
		&pipelineRenderedChapterSnapshot{
			Body:       body,
			BodySHA256: domain.PlanningV2DigestPrefix + bodySHA,
		},
		pipelineRenderRejectedReviewEvidence(frozen.Chapter, bodySHA),
	)
	if err != nil || !persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}

	fresh, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.NewStore(fresh.OutputDir).Drafts.LoadDraft(frozen.Chapter); strings.TrimSpace(got) != "" {
		t.Fatalf("tombstoned exact body was recovered: %q", got)
	}
	if _, err := os.Stat(filepath.Join(fresh.OutputDir, "meta", "planning", "render_candidate_recovery.json")); !os.IsNotExist(err) {
		t.Fatalf("tombstoned body unexpectedly produced a recovery receipt: %v", err)
	}
}

func TestPreparePipelineRenderCandidatePrefersTombstoneBoundRewriteSourceOverOlderStaleBody(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := liveStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq

	older, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	const olderBody = "第一章\n\n这是正式审核前的旧哈希，不得在拒稿后复活。"
	olderStore := store.NewStore(older.OutputDir)
	if err := olderStore.Drafts.SaveDraft(1, olderBody); err != nil {
		t.Fatal(err)
	}
	if _, err := olderStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(older.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}

	rejected, err := prepareFreshPipelineRenderCandidate(live, frozen, older.ID, older.ContainerDir)
	if err != nil {
		t.Fatal(err)
	}
	rejectedStore := store.NewStore(rejected.OutputDir)
	const rejectedBody = "第一章\n\n这份正文已完成正式审核，下一轮必须消费它的返工意见。"
	if err := rejectedStore.Drafts.SaveDraft(1, rejectedBody); err != nil {
		t.Fatal(err)
	}
	if _, err := rejectedStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := rejectedStore.Drafts.SaveFinalChapter(1, rejectedBody); err != nil {
		t.Fatal(err)
	}
	commit, err := rejectedStore.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "commit", "chapters/01.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectedStore.Progress.Init("semantic seed", 2); err != nil {
		t.Fatal(err)
	}
	if err := rejectedStore.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := rejectedStore.Progress.MarkChapterComplete(1, len([]rune(rejectedBody)), "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	bodySHA := reviewreport.BodySHA256(rejectedBody)
	review := domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: bodySHA,
		Verdict: "rewrite", ContractStatus: "met", Summary: "删掉清单感，补主角真实判断。",
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "catalog stuffing"}},
	}
	if err := rejectedStore.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := rejectedStore.Progress.SetPendingRewritesAndFlow([]int{1}, review.Summary, domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	brief := "# ch01 rewrite brief\n\n- 待返工正文 SHA-256：`" + bodySHA + "`。\n- 必须修正清单感并补主角判断。\n"
	if err := rejectedStore.Drafts.SaveRewriteBrief(1, brief); err != nil {
		t.Fatal(err)
	}
	snapshot := &pipelineRenderedChapterSnapshot{
		Store: rejectedStore, Commit: commit, ChapterPath: "chapters/01.md",
		Body: rejectedBody, BodySHA256: domain.PlanningV2DigestPrefix + bodySHA,
	}
	persisted, err := persistPipelineRenderSemanticRejection(
		live, rejected, frozen, snapshot, pipelineRenderRejectedReviewEvidence(1, bodySHA),
	)
	if err != nil || !persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
	if err := retirePipelineRenderCandidate(rejected.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	recoveredStore := store.NewStore(recovered.OutputDir)
	if got, err := recoveredStore.Drafts.LoadDraft(1); err != nil || got != rejectedBody {
		t.Fatalf("semantic rewrite source body=%q err=%v", got, err)
	}
	if got, err := recoveredStore.Drafts.LoadRewriteBrief(1); err != nil || got != brief {
		t.Fatalf("semantic rewrite brief=%q err=%v", got, err)
	}
	progress, err := recoveredStore.Progress.Load()
	if err != nil || progress == nil || len(progress.PendingRewrites) != 1 || progress.PendingRewrites[0] != 1 {
		t.Fatalf("semantic rewrite queue=%+v err=%v", progress, err)
	}
	if got, _ := recoveredStore.Drafts.LoadDraft(1); got == olderBody {
		t.Fatal("older pre-review stale body won recovery after a semantic rejection")
	}
}

func TestPreparePipelineRenderCandidateIgnoresDifferentBodyTombstone(t *testing.T) {
	live, frozen, candidate, body := pipelineRenderRecoverableCandidateFixture(t)
	const otherBody = "第一章\n\n另一份被拒绝的正文。"
	otherSHA := reviewreport.BodySHA256(otherBody)
	tombstone := pipelineRenderRejectionTombstone{
		Version:                pipelineRenderRejectionTombstoneVersion,
		CandidateID:            candidate.ID,
		GenerationID:           frozen.PlanningGenerationID,
		Chapter:                frozen.Chapter,
		PlanDigest:             frozen.PlanDigest,
		PlanCheckpointSeq:      frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:  frozen.ProjectedBundleDigest,
		PromotionReceiptDigest: frozen.PromotionReceiptDigest,
		BodySHA256:             otherSHA,
		Verdict:                "rewrite",
		Disposition:            "是",
		ReviewArtifacts:        pipelineRenderRequiredReviewArtifacts(frozen.Chapter),
		RejectedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(tombstone, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path, err := pipelineRenderRejectionTombstonePath(live, candidate.ID, otherSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteRewriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(frozen.Chapter); err != nil || got != body {
		t.Fatalf("different-body tombstone blocked recovery: body=%q err=%v", got, err)
	}
}

func TestPreparePipelineRenderCandidateFailsClosedOnCorruptExactTombstone(t *testing.T) {
	live, frozen, candidate, body := pipelineRenderRecoverableCandidateFixture(t)
	path, err := pipelineRenderRejectionTombstonePath(
		live, candidate.ID, reviewreport.BodySHA256(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := preparePipelineRenderCandidate(live, frozen); err == nil ||
		!strings.Contains(err.Error(), "exact render rejection tombstone") {
		t.Fatalf("corrupt exact tombstone did not fail closed: %v", err)
	}
}

func TestPreparePipelineRenderCandidateKeepsNewerActiveSemanticContinuation(t *testing.T) {
	fixture := pipelineRenderSemanticSeedTestFixture(t)
	active, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	activeStore := store.NewStore(active.OutputDir)
	const newerBody = "第一章\n\n这是已经消费拒稿意见后产生的更新正文，不能被旧 seed 回滚。"
	if err := activeStore.Drafts.SaveDraft(fixture.frozen.Chapter, newerBody); err != nil {
		t.Fatal(err)
	}
	newerCheckpoint, err := activeStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(fixture.frozen.Chapter), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	retiredBefore, err := os.ReadDir(filepath.Join(pipelineRenderCandidateRoot(fixture.live), "retired"))
	if err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter); err != nil || got != newerBody {
		t.Fatalf("newer active semantic continuation rolled back: body=%q err=%v", got, err)
	}
	currentCheckpoint := store.NewStore(recovered.OutputDir).Checkpoints.LatestByStep(
		domain.ChapterScope(fixture.frozen.Chapter),
		"edit",
	)
	if currentCheckpoint == nil || currentCheckpoint.Seq != newerCheckpoint.Seq ||
		currentCheckpoint.Digest != newerCheckpoint.Digest {
		t.Fatalf("newer active checkpoint changed: got=%+v want=%+v", currentCheckpoint, newerCheckpoint)
	}
	retiredAfter, err := os.ReadDir(filepath.Join(pipelineRenderCandidateRoot(fixture.live), "retired"))
	if err != nil {
		t.Fatal(err)
	}
	if len(retiredAfter) != len(retiredBefore) {
		t.Fatalf("newer active candidate was unnecessarily retired: before=%d after=%d", len(retiredBefore), len(retiredAfter))
	}
}

func TestPreparePipelineRenderCandidateRestoresRetiredSemanticSeedOverInvalidActive(t *testing.T) {
	fixture := pipelineRenderSemanticSeedTestFixture(t)
	activeContainer := filepath.Join(pipelineRenderCandidateRoot(fixture.live), fixture.candidateID)
	active, err := prepareFreshPipelineRenderCandidate(
		fixture.live,
		fixture.frozen,
		fixture.candidateID,
		activeContainer,
	)
	if err != nil {
		t.Fatal(err)
	}
	activeStore := store.NewStore(active.OutputDir)
	if err := activeStore.Drafts.SaveDraft(fixture.frozen.Chapter, "第一章\n\n缺少正式审核 hand-off 的无效 active。"); err != nil {
		t.Fatal(err)
	}
	if _, err := activeStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(fixture.frozen.Chapter), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter); err != nil || got != fixture.rejectedBody {
		t.Fatalf("invalid active blocked retired semantic recovery: body=%q err=%v", got, err)
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadRewriteBrief(fixture.frozen.Chapter); err != nil || got != fixture.rewriteBrief {
		t.Fatalf("invalid active recovery lost rewrite brief: brief=%q err=%v", got, err)
	}
}

func TestPreparePipelineRenderCandidateRestoresNewerRetiredSemanticContinuationOverOlderActive(t *testing.T) {
	fixture := pipelineRenderSemanticSeedTestFixture(t)
	active, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	activeStore := store.NewStore(active.OutputDir)
	const newestBody = "第一章\n\n这是 retired 中 checkpoint 更新的 semantic continuation。"
	if err := activeStore.Drafts.SaveDraft(fixture.frozen.Chapter, newestBody); err != nil {
		t.Fatal(err)
	}
	newestCheckpoint, err := activeStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(fixture.frozen.Chapter), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(active.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}
	// Recreate a valid but older active semantic seed from the original rejected
	// snapshot. The newer retired continuation must still win by body checkpoint.
	if err := copyPipelineRenderCandidateTree(
		filepath.Join(fixture.retiredSeedContainer, "output"),
		active.OutputDir,
	); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(fixture.live, fixture.frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(fixture.frozen.Chapter); err != nil || got != newestBody {
		t.Fatalf("older active beat newer retired semantic continuation: body=%q err=%v", got, err)
	}
	currentCheckpoint := store.NewStore(recovered.OutputDir).Checkpoints.LatestByStep(
		domain.ChapterScope(fixture.frozen.Chapter),
		"edit",
	)
	if currentCheckpoint == nil || currentCheckpoint.Seq != newestCheckpoint.Seq ||
		currentCheckpoint.Digest != newestCheckpoint.Digest {
		t.Fatalf("newer retired checkpoint was not restored: got=%+v want=%+v", currentCheckpoint, newestCheckpoint)
	}
}

type pipelineRenderSemanticSeedFixture struct {
	live                 string
	frozen               *pipelineFrozenPlan
	candidateID          string
	retiredSeedContainer string
	rejectedBody         string
	rewriteBrief         string
}

func pipelineRenderSemanticSeedTestFixture(t *testing.T) pipelineRenderSemanticSeedFixture {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := liveStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidate.OutputDir)
	const rejectedBody = "第一章\n\n这份正文已完成正式审核，必须作为后续 semantic rewrite seed。"
	if err := candidateStore.Drafts.SaveDraft(frozen.Chapter, rejectedBody); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Drafts.SaveFinalChapter(frozen.Chapter, rejectedBody); err != nil {
		t.Fatal(err)
	}
	commit, err := candidateStore.Checkpoints.AppendArtifact(
		domain.ChapterScope(frozen.Chapter), "commit", "chapters/01.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Progress.Init("semantic seed regression", 2); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Progress.StartChapter(frozen.Chapter); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Progress.MarkChapterComplete(
		frozen.Chapter,
		len([]rune(rejectedBody)),
		"mystery",
		"quest",
	); err != nil {
		t.Fatal(err)
	}
	bodySHA := reviewreport.BodySHA256(rejectedBody)
	review := domain.ReviewEntry{
		Chapter: frozen.Chapter, Scope: "chapter", BodySHA256: bodySHA,
		Verdict: "rewrite", ContractStatus: "met", Summary: "减少清单感，增加人物判断。",
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "catalog stuffing"}},
	}
	if err := candidateStore.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Progress.SetPendingRewritesAndFlow(
		[]int{frozen.Chapter},
		review.Summary,
		domain.FlowRewriting,
	); err != nil {
		t.Fatal(err)
	}
	rewriteBrief := "# ch01 rewrite brief\n\n- 待返工正文 SHA-256：`" + bodySHA + "`。\n- 必须减少清单感并增加人物判断。\n"
	if err := candidateStore.Drafts.SaveRewriteBrief(frozen.Chapter, rewriteBrief); err != nil {
		t.Fatal(err)
	}
	snapshot := &pipelineRenderedChapterSnapshot{
		Store: candidateStore, Commit: commit, ChapterPath: "chapters/01.md",
		Body: rejectedBody, BodySHA256: domain.PlanningV2DigestPrefix + bodySHA,
	}
	persisted, err := persistPipelineRenderSemanticRejection(
		live,
		candidate,
		frozen,
		snapshot,
		pipelineRenderRejectedReviewEvidence(frozen.Chapter, bodySHA),
	)
	if err != nil || !persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(live), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil {
		t.Fatal(err)
	}
	retiredSeedContainer := ""
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), candidate.ID+"-rejected-") {
			retiredSeedContainer = filepath.Join(retiredRoot, entry.Name())
			break
		}
	}
	if retiredSeedContainer == "" {
		t.Fatal("semantic rejected fixture did not create a retired seed")
	}
	return pipelineRenderSemanticSeedFixture{
		live:                 live,
		frozen:               frozen,
		candidateID:          candidate.ID,
		retiredSeedContainer: retiredSeedContainer,
		rejectedBody:         rejectedBody,
		rewriteBrief:         rewriteBrief,
	}
}

func pipelineRenderRecoverableCandidateFixture(
	t *testing.T,
) (string, *pipelineFrozenPlan, *pipelineRenderCandidate, string) {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	const body = "第一章\n\n这个 exact body 可以跨进程恢复。"
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(frozen.Chapter, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(frozen.Chapter), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	return live, frozen, candidate, body
}

func pipelineRenderRejectedReviewEvidence(chapter int, bodySHA string) currentChapterReviewEvidence {
	return currentChapterReviewEvidence{
		Chapter:     chapter,
		BodySHA256:  bodySHA,
		Verdict:     "rewrite",
		Disposition: "是",
		Artifacts:   pipelineRenderRequiredReviewArtifacts(chapter),
	}
}
