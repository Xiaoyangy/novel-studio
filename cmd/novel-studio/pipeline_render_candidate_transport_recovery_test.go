package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestPreparePipelineRenderCandidatePrefersLatestEditAfterTransportFailure(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		Title:   "冻结计划",
	}); err != nil {
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
	frozen.PlanDigest = planCheckpoint.Digest
	frozen.PlanCheckpointSeq = planCheckpoint.Seq

	older, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	olderStore := store.NewStore(older.OutputDir)
	const olderBody = "第一章\n\n旧候选已经提交，但不是超时前最后保存的正文。"
	if err := olderStore.Drafts.SaveDraft(1, olderBody); err != nil {
		t.Fatal(err)
	}
	olderBodyCheckpoint, err := olderStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"draft",
		"drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := olderStore.Drafts.SaveFinalChapter(1, olderBody); err != nil {
		t.Fatal(err)
	}
	if _, err := olderStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"commit",
		"chapters/01.md",
		"plan", "draft", "edit", "commit",
	); err != nil {
		t.Fatal(err)
	}
	if err := olderStore.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatal(err)
	}
	inspectedOlder, ok := inspectReusablePipelineRenderCandidate(
		older.ContainerDir,
		live,
		frozen,
		older.ID,
	)
	if !ok || inspectedOlder.score != 3 {
		t.Fatalf("older fixture is not a complete reusable snapshot: ok=%v candidate=%+v", ok, inspectedOlder)
	}
	if err := retirePipelineRenderCandidate(older.ContainerDir, "stale-postcommit"); err != nil {
		t.Fatal(err)
	}

	activeContainer := filepath.Join(pipelineRenderCandidateRoot(live), older.ID)
	latest, err := prepareFreshPipelineRenderCandidate(
		live,
		frozen,
		older.ID,
		activeContainer,
	)
	if err != nil {
		t.Fatal(err)
	}
	latestStore := store.NewStore(latest.OutputDir)
	if err := latestStore.Drafts.SaveDraft(1, "第一章\n\n新一轮的初稿。\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := latestStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"draft",
		"drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	const latestBody = "第一章\n\n这是外部裁判超时前已落盘的最新编辑稿，必须原字节恢复。"
	if err := latestStore.Drafts.SaveDraft(1, latestBody); err != nil {
		t.Fatal(err)
	}
	latestBodyCheckpoint, err := latestStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"edit",
		"drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if latestBodyCheckpoint.Seq <= olderBodyCheckpoint.Seq {
		t.Fatalf(
			"fixture did not create a later edited body checkpoint: old=%d latest=%d",
			olderBodyCheckpoint.Seq,
			latestBodyCheckpoint.Seq,
		)
	}
	ledger, err := syncPipelineRenderConvergence(latestStore)
	if err != nil {
		t.Fatal(err)
	}
	latestBodySHA256 := reviewreport.BodySHA256(latestBody)
	latestRecord := findPipelineRenderConvergenceRecord(ledger, latestBodySHA256)
	if latestRecord == nil || !latestRecord.Edited || latestRecord.ExternalBlocking ||
		latestRecord.StructuralBlock || latestRecord.SemanticReject {
		t.Fatalf("latest edited body ledger is not a retryable exact-body record: %+v", latestRecord)
	}
	if reason := pipelineRenderCandidateErrorRetirementReason(
		latest.OutputDir,
		frozen,
		planCheckpoint,
		false,
	); reason != "stale-operational-error" {
		t.Fatalf("transport failure was classified as %q, want stale-operational-error", reason)
	}
	if err := retirePipelineRenderCandidate(latest.ContainerDir, "stale-operational-error"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	gotBody, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(1)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody != latestBody {
		t.Fatalf("recovery rolled back to an older body: got=%q want=%q", gotBody, latestBody)
	}
	receiptRaw, err := os.ReadFile(filepath.Join(
		recovered.OutputDir,
		"meta",
		"planning",
		"render_candidate_recovery.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var receipt pipelineRenderCandidateRecoveryReceipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SourceBodyCheckpointSeq != latestBodyCheckpoint.Seq ||
		receipt.SourceBodyCheckpointHash != latestBodyCheckpoint.Digest ||
		receipt.SourceBodyCheckpointStep != "edit" {
		t.Fatalf("recovery receipt selected the wrong body checkpoint: %+v", receipt)
	}

	recoveredManifest, err := loadPipelineRenderCandidateManifest(recovered.OutputDir)
	if err != nil || recoveredManifest == nil {
		t.Fatalf("load recovered manifest: manifest=%+v err=%v", recoveredManifest, err)
	}
	recoveredLedger, err := loadPipelineRenderConvergenceLedger(
		live,
		*recoveredManifest,
		pipelineRenderConvergenceLimit(store.NewStore(recovered.OutputDir), 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveredRecord := findPipelineRenderConvergenceRecord(recoveredLedger, latestBodySHA256)
	if recoveredRecord == nil || !recoveredRecord.Edited || recoveredRecord.ExternalBlocking ||
		recoveredRecord.StructuralBlock || recoveredRecord.SemanticReject {
		t.Fatalf("recovery lost or semantically changed the latest exact-body ledger: %+v", recoveredRecord)
	}

	// The same operational path must still quarantine an exact body once the
	// durable ledger records an actual gate decision.
	recoveredRecord.ExternalBlocking = true
	if err := savePipelineRenderConvergenceLedger(live, recoveredLedger); err != nil {
		t.Fatal(err)
	}
	if reason := pipelineRenderCandidateErrorRetirementReason(
		recovered.OutputDir,
		frozen,
		planCheckpoint,
		false,
	); reason != "rejected" {
		t.Fatalf("durable exact-body rejection was classified as %q", reason)
	}
}

func TestPreparePipelineRenderCandidateRecoversLegacyMisclassifiedRejectedEdit(t *testing.T) {
	const body = "第五章\n\n编辑稿已经落盘，旧 runner 却把裁判超时误标成 rejected。"
	live, frozen, candidate, bodyCheckpoint := pipelineLegacyRejectedEditFixture(t, body)
	inspected, ok := inspectReusablePipelineRenderCandidate(
		candidate.ContainerDir,
		live,
		frozen,
		candidate.ID,
	)
	if !ok || inspected.score != 2 || inspected.bodyCheckpoint.Step != "edit" {
		t.Fatalf("legacy fixture must be an uncommitted edit candidate: ok=%v candidate=%+v", ok, inspected)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.NewStore(recovered.OutputDir).Drafts.LoadDraft(frozen.Chapter)
	if err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Fatalf("legacy rejected transport candidate was not recovered exactly: got=%q want=%q", got, body)
	}
	receiptRaw, err := os.ReadFile(filepath.Join(
		recovered.OutputDir,
		"meta",
		"planning",
		"render_candidate_recovery.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var receipt pipelineRenderCandidateRecoveryReceipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SourceBodyCheckpointSeq != bodyCheckpoint.Seq ||
		receipt.SourceBodyCheckpointHash != bodyCheckpoint.Digest ||
		receipt.SourceBodyCheckpointStep != "edit" {
		t.Fatalf("legacy recovery did not bind the latest edit checkpoint: %+v", receipt)
	}
}

func TestPreparePipelineRenderCandidateDoesNotRecoverTombstonedLegacyRejectedEdit(t *testing.T) {
	const body = "第五章\n\n这份编辑稿已经完成正式评审并被明确退回。"
	live, frozen, candidate, _ := pipelineLegacyRejectedEditFixture(t, body)
	bodySHA256 := reviewreport.BodySHA256(body)
	persisted, err := persistPipelineRenderSemanticRejection(
		live,
		candidate,
		frozen,
		&pipelineRenderedChapterSnapshot{
			Body:       body,
			BodySHA256: domain.PlanningV2DigestPrefix + bodySHA256,
		},
		currentChapterReviewEvidence{
			Chapter:     frozen.Chapter,
			BodySHA256:  bodySHA256,
			Verdict:     "rewrite",
			Disposition: "是",
			Artifacts:   pipelineRenderRequiredReviewArtifacts(frozen.Chapter),
		},
	)
	if err != nil || !persisted {
		t.Fatalf("persist exact-body rejection tombstone: persisted=%v err=%v", persisted, err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}

	fresh, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.NewStore(fresh.OutputDir).Drafts.LoadDraft(frozen.Chapter); got != "" {
		t.Fatalf("tombstoned rejected body entered generic recovery: %q", got)
	}
}

func TestPreparePipelineRenderCandidateMergesLocalSoftChainAndExactJudgeCache(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	chapter := 5
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: chapter, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := liveStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter), "plan", "drafts/05.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.Chapter = chapter
	frozen.PlanDigest = planCheckpoint.Digest
	frozen.PlanCheckpointSeq = planCheckpoint.Seq

	chainCandidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	chainStore := store.NewStore(chainCandidate.OutputDir)
	preEdit := "第五章\n\n这不是提醒，而是命令。值班员把登记册推了回来。"
	if err := chainStore.Drafts.SaveDraft(chapter, preEdit); err != nil {
		t.Fatal(err)
	}
	seed, err := chainStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter), "draft", "drafts/05.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	quotaPayload := fmt.Sprintf(
		"draft-local-soft-edit/v1\nchapter=%d\nplan_seq=%d\nplan_digest=%s\nseed_step=%s\nseed_seq=%d\nseed_digest=%s\n",
		chapter, planCheckpoint.Seq, planCheckpoint.Digest, seed.Step, seed.Seq, seed.Digest,
	)
	quotaSum := sha256.Sum256([]byte(quotaPayload))
	quotaDigest := fmt.Sprintf("sha256:%x", quotaSum)
	tokenArtifact := filepath.ToSlash(filepath.Join(
		"meta", "runtime", "draft_local_soft_edit", fmt.Sprintf("ch%03d", chapter),
		strings.TrimPrefix(quotaDigest, "sha256:")+".json",
	))
	tokenRaw, err := json.MarshalIndent(struct {
		Version           int    `json:"version"`
		Chapter           int    `json:"chapter"`
		QuotaDigest       string `json:"quota_digest"`
		SeedCheckpointSeq int64  `json:"seed_checkpoint_seq"`
		PreEditBodySHA256 string `json:"pre_edit_body_sha256"`
	}{
		Version:           1,
		Chapter:           chapter,
		QuotaDigest:       quotaDigest,
		SeedCheckpointSeq: seed.Seq,
		PreEditBodySHA256: reviewreport.BodySHA256(preEdit),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(chainCandidate.OutputDir, filepath.FromSlash(tokenArtifact))
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, tokenRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := chainStore.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "draft-local-soft-edit-consumed", tokenArtifact,
	); err != nil {
		t.Fatal(err)
	}
	finalBody := "第五章\n\n门外脚步一停，值班员才把登记册推回来。许闻溪没有接，只念出监控编号。"
	if err := chainStore.Drafts.SaveDraft(chapter, finalBody); err != nil {
		t.Fatal(err)
	}
	if _, err := chainStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter), "edit", "drafts/05.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if tools.InspectDraftLocalSoftEditRecoveryChain(chainStore, chapter) == nil {
		t.Fatal("fixture source local-soft chain did not validate")
	}
	if err := retirePipelineRenderCandidate(chainCandidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}

	judgeCandidate, err := prepareFreshPipelineRenderCandidate(
		live,
		frozen,
		chainCandidate.ID,
		filepath.Join(pipelineRenderCandidateRoot(live), chainCandidate.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	judgeStore := store.NewStore(judgeCandidate.OutputDir)
	for index, version := range []struct {
		step string
		body string
	}{
		{step: "draft", body: "第五章\n\n外审候选初稿。"},
		{step: "edit", body: "第五章\n\n外审候选第一次编辑。"},
		{step: "draft", body: "第五章\n\n外审候选重新生成。"},
		{step: "edit", body: finalBody},
	} {
		if err := judgeStore.Drafts.SaveDraft(chapter, version.body); err != nil {
			t.Fatal(err)
		}
		if _, err := judgeStore.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(chapter), version.step, "drafts/05.draft.md",
			"plan", "rerender-request", "draft", "edit",
		); err != nil {
			t.Fatalf("append judge candidate body epoch %d: %v", index, err)
		}
	}
	selection := deepseekAIJudgeModelSelection{
		Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true,
	}
	passingResponse := strings.Replace(deepseekCompleteHumanResponse, `"ai_probability_percent":3`, `"ai_probability_percent":2`, 1)
	judge, err := runDeepSeekAIJudge(
		&reviewCacheModel{response: passingResponse}, selection, chapter, finalBody, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveDeepSeekAIJudgeCache(judgeCandidate.OutputDir, judge); err != nil {
		t.Fatal(err)
	}
	if err := saveDraftDeepSeekAIJudge(judgeCandidate.OutputDir, judge); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(judgeCandidate.ContainerDir, "stale-operational-error"); err != nil {
		t.Fatal(err)
	}

	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	recoveredStore := store.NewStore(recovered.OutputDir)
	if got, err := recoveredStore.Drafts.LoadDraft(chapter); err != nil || got != finalBody {
		t.Fatalf("merged recovery body=%q err=%v", got, err)
	}
	if tools.InspectDraftLocalSoftEditRecoveryChain(recoveredStore, chapter) == nil {
		t.Fatal("generic recovery lost the validated local-soft chain")
	}
	receiptRaw, err := os.ReadFile(filepath.Join(recovered.OutputDir, "meta", "planning", "render_candidate_recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt pipelineRenderCandidateRecoveryReceipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.LocalSoftChain == nil || !receipt.ExactJudgeReused || !receipt.ExactJudgeCacheReused ||
		receipt.ExactJudgeProbability != 2 || receipt.LocalSoftChain.QuotaDigest != quotaDigest {
		t.Fatalf("recovery did not merge chain and exact-body judge cache: %+v", receipt)
	}
	model := &reviewCacheModel{response: passingResponse}
	result := loadOrGenerateDeepSeekAIJudge(
		recovered.OutputDir, model, selection, chapter, finalBody, time.Second,
	)
	if result.Err != nil || !result.CacheHit || result.Artifact == nil ||
		result.Artifact.AIProbabilityPercent != 2 || model.callCount() != 0 {
		t.Fatalf("merged exact-body judge cache was not reusable without a model call: result=%+v calls=%d", result, model.callCount())
	}
}

func pipelineLegacyRejectedEditFixture(
	t *testing.T,
	body string,
) (string, *pipelineFrozenPlan, *pipelineRenderCandidate, *domain.Checkpoint) {
	t.Helper()
	live := pipelineRenderCandidateTestLive(t)
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	chapter := 5
	if err := liveStore.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: chapter,
		Title:   "冻结计划",
	}); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := liveStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"plan",
		"drafts/05.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.Chapter = chapter
	frozen.PlanDigest = planCheckpoint.Digest
	frozen.PlanCheckpointSeq = planCheckpoint.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(chapter, "第五章\n\n旧的整章初稿。"); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"draft",
		"drafts/05.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := candidateStore.Drafts.SaveDraft(chapter, body); err != nil {
		t.Fatal(err)
	}
	bodyCheckpoint, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"edit",
		"drafts/05.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	return live, frozen, candidate, bodyCheckpoint
}

func findPipelineRenderConvergenceRecord(
	ledger *pipelineRenderConvergenceLedger,
	bodySHA256 string,
) *pipelineRenderConvergenceRecord {
	if ledger == nil {
		return nil
	}
	for i := range ledger.Records {
		if ledger.Records[i].BodySHA256 == bodySHA256 {
			return &ledger.Records[i]
		}
	}
	return nil
}
