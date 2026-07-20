package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestReplayDraftLocalSoftEditRecoveryChainPreservesQuotaAndBoundedAcceptance(t *testing.T) {
	source := store.NewStore(t.TempDir())
	if err := source.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "一次软修恢复"}
	if err := source.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	preEdit := "第一章\n\n这不是提醒，而是命令。值班员把登记册推了回来。"
	if err := source.Drafts.SaveDraft(1, preEdit); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	wantQuota, wantSeedSeq, err := draftLocalSoftEditQuotaIdentity(source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumeDraftLocalSoftEditQuota(source, 1); err != nil {
		t.Fatal(err)
	}
	edited := "第一章\n\n门外脚步一停，值班员才把登记册推回来。林砚没有接，只念出监控编号。"
	if err := source.Drafts.SaveDraft(1, edited); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	chain := InspectDraftLocalSoftEditRecoveryChain(source, 1)
	if chain == nil {
		t.Fatal("valid source local-soft chain was not captured")
	}

	target := store.NewStore(t.TempDir())
	if err := target.Init(); err != nil {
		t.Fatal(err)
	}
	if err := target.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := target.Drafts.SaveDraft(1, edited); err != nil {
		t.Fatal(err)
	}
	receipt, replayed, err := ReplayDraftLocalSoftEditRecoveryChain(target, chain)
	if err != nil || !replayed || receipt == nil {
		t.Fatalf("replay local-soft chain: replayed=%v receipt=%+v err=%v", replayed, receipt, err)
	}
	gotQuota, gotSeedSeq, err := draftLocalSoftEditQuotaIdentity(target, 1)
	if err != nil || gotQuota != wantQuota || gotSeedSeq != wantSeedSeq {
		t.Fatalf("replayed quota identity drifted: got=%s#%d want=%s#%d err=%v", gotQuota, gotSeedSeq, wantQuota, wantSeedSeq, err)
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(target, 1)
	if err != nil || !consumed {
		t.Fatalf("replayed local-soft quota is not durably consumed: consumed=%v err=%v", consumed, err)
	}
	writeDraftExternalJudgeStatus(t, target.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256:           reviewreport.BodySHA256(edited),
		AdviceComplete:       true,
		AIProbabilityPercent: 2,
		PassExclusivePercent: 4,
	})
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, Passed: true, ExternalCorroborated: true,
		RawLocalGatePercent: 10.3, EffectiveGatePercent: 2, PassExclusivePercent: 4,
	}
	if !draftAIGCLocalSoftSatisfiedAfterBoundedEdit(target, 1, edited, report, gate) {
		t.Fatal("replayed chain plus exact-body DeepSeek pass did not satisfy bounded local-soft gate")
	}
	if receipt.ReplayedDraftSeedSeq >= receipt.ReplayedConsumedSeq ||
		receipt.ReplayedConsumedSeq >= receipt.ReplayedFinalEditSeq ||
		receipt.ReplayedFinalEditDigest != "sha256:"+reviewreport.BodySHA256(edited) {
		t.Fatalf("replay receipt lost causal order or exact-body digest: %+v", receipt)
	}
}
