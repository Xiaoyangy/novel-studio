package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRenderConvergenceGuardCarriesSealedSuccessorAncestorHashesWithoutBudgetCharge(t *testing.T) {
	st, originalPlan, marker := sealedRAGGuardFixture(t, true)
	oldCheckpoint, err := CurrentChapterPlanCheckpoint(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	replacement := originalPlan
	replacement.Notes += "\nsuccessor scene allocation"
	if err := st.Drafts.SaveChapterPlan(replacement); err != nil {
		t.Fatal(err)
	}
	newCheckpoint, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	replacementSemantic, err := domain.ComputeChapterPlanV2Digest(replacement)
	if err != nil {
		t.Fatal(err)
	}
	h0 := reviewreport.BodySHA256("epoch zero rejected body zero")
	h0b := reviewreport.BodySHA256("epoch zero rejected body one")
	h1 := reviewreport.BodySHA256("epoch one rejected body zero")
	h1b := reviewreport.BodySHA256("epoch one rejected body one")
	ctx := sealedRAGGuardDigest(t, "successor-context")
	previous := domain.SealedConvergenceReplanReceipt{
		Version:      domain.SealedConvergenceReplanReceiptVersion,
		GenerationID: marker.PlanningGenerationID, Chapter: 1,
		BundleDigest: marker.ProjectedBundleDigest, PromotionReceiptDigest: marker.PromotionReceiptDigest,
		SealedPlanSemanticDigest:     marker.ProjectedPlanSHA256,
		PreviousPlanCheckpointDigest: sealedRAGGuardDigest(t, "predecessor-plan"), PreviousPlanCheckpointSeq: oldCheckpoint.Seq - 1,
		ExhaustedCandidateID: "render-ch0001-epoch-zero", ExhaustedLedgerSHA256: sealedRAGGuardDigest(t, "ledger-zero"),
		FailedBodySHA256: []string{h0, h0b}, FailureCount: 2, FailureLimit: 2,
		ReplacementPlanSemanticDigest:   marker.ProjectedPlanSHA256,
		ReplacementPlanCheckpointDigest: oldCheckpoint.Digest, ReplacementPlanCheckpointSeq: oldCheckpoint.Seq,
		ReplacementRenderContextSHA256: sealedRAGGuardDigest(t, "context-zero"),
		StateContractDigest:            sealedRAGGuardDigest(t, "state"), FeedbackDigest: sealedRAGGuardDigest(t, "feedback-zero"),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if previous.PreviousPlanCheckpointSeq <= 0 {
		t.Fatalf("fixture needs a predecessor checkpoint: old=%d", oldCheckpoint.Seq)
	}
	previous.ReceiptDigest, err = domain.ComputeSealedConvergenceReplanReceiptDigest(previous)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.SealedConvergenceReplanReceipt{
		Version:      domain.SealedConvergenceReplanReceiptVersion,
		GenerationID: marker.PlanningGenerationID, Chapter: 1,
		BundleDigest:                 marker.ProjectedBundleDigest,
		PromotionReceiptDigest:       marker.PromotionReceiptDigest,
		SealedPlanSemanticDigest:     marker.ProjectedPlanSHA256,
		PreviousPlanCheckpointDigest: oldCheckpoint.Digest, PreviousPlanCheckpointSeq: oldCheckpoint.Seq,
		PreviousReplanReceiptDigest: previous.ReceiptDigest,
		ExhaustedCandidateID:        "render-ch0001-epoch-one", ExhaustedLedgerSHA256: sealedRAGGuardDigest(t, "ledger-one"),
		FailedBodySHA256: []string{h1, h1b}, FailureCount: 2, FailureLimit: 2,
		ReplacementPlanSemanticDigest:   replacementSemantic,
		ReplacementPlanCheckpointDigest: newCheckpoint.Digest, ReplacementPlanCheckpointSeq: newCheckpoint.Seq,
		ReplacementRenderContextSHA256: ctx,
		StateContractDigest:            sealedRAGGuardDigest(t, "state"),
		FeedbackDigest:                 sealedRAGGuardDigest(t, "feedback"),
		CreatedAt:                      time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest, err = domain.ComputeSealedConvergenceReplanReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	marker.PlanDigest = newCheckpoint.Digest
	marker.PlanCheckpointSeq = newCheckpoint.Seq
	marker.RenderContextSHA256 = ctx
	marker.ConvergenceReplanReceiptDigest = receipt.ReceiptDigest
	writeSealedRAGGuardMarker(t, st, marker)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(st.Dir(), sealedConvergenceReplanReceiptDir, receipt.ReceiptDigest+".json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	previousRaw, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(receiptPath), previous.ReceiptDigest+".json"), previousRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The current epoch contributes no failures. Ancestors are projected only
	// into the pre-write tombstone guard, not into a convergence ledger/count.
	if err := SaveRenderConvergenceGuard(st, 1, newCheckpoint.Digest, nil); err != nil {
		t.Fatal(err)
	}
	for _, oldBody := range []string{
		"epoch zero rejected body zero", "epoch zero rejected body one",
		"epoch one rejected body zero", "epoch one rejected body one",
	} {
		if err := rejectPreviouslyFailedRenderBody(st, 1, oldBody); err == nil {
			t.Fatalf("ancestor exact body %q was resurrected", oldBody)
		}
	}
	if err := rejectPreviouslyFailedRenderBody(st, 1, "genuinely new successor body"); err != nil {
		t.Fatalf("new successor body was charged against ancestor failures: %v", err)
	}
	guardRaw, err := os.ReadFile(renderConvergenceGuardPath(st.Dir(), 1))
	if err != nil {
		t.Fatal(err)
	}
	var guard renderConvergenceGuard
	if err := json.Unmarshal(guardRaw, &guard); err != nil {
		t.Fatal(err)
	}
	if len(guard.FailedBodySHA256) != 4 {
		t.Fatalf("ancestor guard set = %v, want 4 across two plan epochs while current ledger remains empty", guard.FailedBodySHA256)
	}

	copyReceipt := receipt
	copyReceipt.FailedBodySHA256 = []string{h0, reviewreport.BodySHA256("forged")}
	forged, _ := json.Marshal(copyReceipt)
	if err := os.WriteFile(receiptPath, forged, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveRenderConvergenceGuard(st, 1, newCheckpoint.Digest, nil); err == nil {
		t.Fatal("tampered ancestor receipt did not fail closed")
	}
}

func TestDraftChapterRejectsPreviouslyFailedExactHashAcrossProcessReload(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧稿不能回来"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	failed := "第一章 旧稿不能回来\n\n" + strings.Repeat("林澈停下来听完，再决定下一步。", 150)
	if err := SaveRenderConvergenceGuard(st, 1, checkpoint.Digest, []string{reviewreport.BodySHA256(failed)}); err != nil {
		t.Fatal(err)
	}

	// NewStore proves the guard is durable rather than an in-memory allowance.
	reloaded := store.NewStore(st.Dir())
	_, err = NewDraftChapterTool(reloaded).Execute(context.Background(), renderConvergenceMustJSON(t, map[string]any{
		"chapter": 1,
		"content": failed,
		"mode":    "write",
	}))
	if err == nil || !strings.Contains(err.Error(), "禁止重复落盘或复判同一 exact hash") {
		t.Fatalf("previously rejected exact hash was accepted: %v", err)
	}
	if got, loadErr := reloaded.Drafts.LoadDraft(1); loadErr != nil || got != "" {
		t.Fatalf("exact-hash guard mutated the draft: body=%q err=%v", got, loadErr)
	}
}

func TestRenderConvergenceGuardDoesNotPoisonNewPlan(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "新计划"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	old, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	body := "第一章 新计划\n\n" + strings.Repeat("林澈等电话那头说完，才把纸翻到背面。", 130)
	if err := SaveRenderConvergenceGuard(st, 1, old.Digest, []string{reviewreport.BodySHA256(body)}); err != nil {
		t.Fatal(err)
	}
	plan.Notes = "new causal epoch"
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := rejectPreviouslyFailedRenderBody(store.NewStore(st.Dir()), 1, body); err != nil {
		t.Fatalf("old plan guard blocked a new plan: %v", err)
	}
}

func TestManagedDraftKeepsProviderPendingProbabilityOutOfConvergenceBudget(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	candidateID := "render-ch0001-in-process"
	candidateDir := filepath.Join(root, "output", ".render-candidates", candidateID, "output")
	st := store.NewStore(candidateDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)
	plan, err := CurrentChapterPlanCheckpoint(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	manifest := toolRenderCandidateManifest{
		Version:                toolRenderCandidateManifestVersion,
		CandidateID:            candidateID,
		GenerationID:           "generation",
		Chapter:                1,
		PlanDigest:             plan.Digest,
		PlanCheckpointSeq:      plan.Seq,
		ProjectedBundleDigest:  "sha256:bundle",
		PromotionReceiptDigest: "sha256:promotion",
		SourceOutputDir:        live,
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicDraftIntent(
		filepath.Join(candidateDir, "meta", "planning", "render_candidate.json"),
		manifestRaw,
	); err != nil {
		t.Fatal(err)
	}
	priorSemanticSHA := strings.Repeat("a", 64)
	ledger := &toolRenderConvergenceLedger{
		Version:                toolRenderConvergenceLedgerVersion,
		CandidateID:            manifest.CandidateID,
		GenerationID:           manifest.GenerationID,
		Chapter:                manifest.Chapter,
		PlanDigest:             manifest.PlanDigest,
		PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
		PromotionReceiptDigest: manifest.PromotionReceiptDigest,
		FailureLimit:           3,
		Records: []toolRenderConvergenceRecord{{
			BodySHA256:     priorSemanticSHA,
			SemanticReject: true,
		}},
	}
	if err := saveToolRenderConvergenceLedger(ledger, toolRenderConvergenceLedgerPath(&manifest)); err != nil {
		t.Fatal(err)
	}
	if err := SaveRenderConvergenceGuard(st, 1, plan.Digest, []string{priorSemanticSHA}); err != nil {
		t.Fatal(err)
	}

	body := "第一章 测试章\n\n林澈把价牌放好，核对票据后留在公共监控里。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	report := aigc.Report{
		AIGCPercent: 79.07, WholeTextSegmentGate: 79.07,
		Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi},
	}
	gate := draftAIGCGateResult{
		RawLocalGatePercent: 79.07, EffectiveGatePercent: 79.07,
		PassExclusivePercent: 4, Enforced: true,
		RewriteFocus: []string{"保留本地概率诊断，等待当前哈希外判。"},
	}
	if err := persistDraftAIGCRerenderRequirement(st, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	if err := checkpointDraftStructuralBlock(st, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	current, err := loadToolRenderConvergenceLedger(st, &manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolRenderConvergenceFailureCount(current); got != 1 {
		t.Fatalf("provider-pending local proxy consumed convergence budget: got %d, want 1", got)
	}
	exhaustion, err := InspectRenderConvergenceExhaustion(st, 1)
	if err != nil || exhaustion.Required || exhaustion.Attempts != 1 {
		t.Fatalf("provider-pending proxies exhausted the sealed plan: inspection=%+v err=%v", exhaustion, err)
	}
	if err := RequireRenderConvergenceAttemptAvailable(st, 1); err != nil {
		t.Fatalf("provider-pending proxies closed another render attempt: %v", err)
	}
}

func TestSemanticRejectedEditedAttemptAuthorizesExactlyNextWholeDraftBoundary(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	candidateID := "render-ch0001-edited-semantic-reject"
	candidateDir := filepath.Join(root, "output", ".render-candidates", candidateID, "output")
	st := store.NewStore(candidateDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, st)
	plan := domain.ChapterPlan{Chapter: 1, Title: "编辑结算尝试"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	wholeBody := "第一章 编辑结算尝试\n\n林澈把初稿放在桌角。"
	if err := st.Drafts.SaveDraft(1, wholeBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	intermediateBody := "第一章 编辑结算尝试\n\n林澈把初稿压在桌角，又划掉一句。"
	if err := st.Drafts.SaveDraft(1, intermediateBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	settledBody := "第一章 编辑结算尝试\n\n林澈把编辑退回的稿纸压在桌角，等对方先说完。"
	if err := st.Drafts.SaveDraft(1, settledBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, settledBody); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{
		Chapter: 1, BodySHA256: reviewreport.BodySHA256(settledBody),
		Scope: "chapter", Verdict: "rewrite", ContractStatus: "met",
		Summary: "保留事实，重写表达。",
	}); err != nil {
		t.Fatal(err)
	}
	manifest := toolRenderCandidateManifest{
		Version:                toolRenderCandidateManifestVersion,
		CandidateID:            candidateID,
		GenerationID:           "generation",
		Chapter:                1,
		PlanDigest:             planCheckpoint.Digest,
		PlanCheckpointSeq:      planCheckpoint.Seq,
		ProjectedBundleDigest:  "sha256:bundle",
		PromotionReceiptDigest: "sha256:promotion",
		SourceOutputDir:        live,
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicDraftIntent(
		filepath.Join(candidateDir, "meta", "planning", "render_candidate.json"),
		manifestRaw,
	); err != nil {
		t.Fatal(err)
	}
	ledger := &toolRenderConvergenceLedger{
		Version:                toolRenderConvergenceLedgerVersion,
		CandidateID:            manifest.CandidateID,
		GenerationID:           manifest.GenerationID,
		Chapter:                manifest.Chapter,
		PlanDigest:             manifest.PlanDigest,
		PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
		PromotionReceiptDigest: manifest.PromotionReceiptDigest,
		FailureLimit:           3,
		Records: []toolRenderConvergenceRecord{
			{BodySHA256: reviewreport.BodySHA256(wholeBody), WholeDraft: true},
			{BodySHA256: reviewreport.BodySHA256(intermediateBody), Edited: true},
			{
				BodySHA256: reviewreport.BodySHA256(settledBody),
				Edited:     true, ExternalJudged: true, SemanticReject: true,
			},
		},
	}
	if err := saveToolRenderConvergenceLedger(ledger, toolRenderConvergenceLedgerPath(&manifest)); err != nil {
		t.Fatal(err)
	}

	settled, err := currentRenderConvergenceAttemptSettledBySemanticReject(st, 1)
	if err != nil || !settled {
		t.Fatalf("final judged/rejected edit did not settle its whole-draft attempt: settled=%v err=%v", settled, err)
	}
	formalSeedPending := DraftExternalGateInspection{
		Status: DraftExternalGateRejudgePending, LocalSoftEditPending: true,
	}
	if draftChapterRejudgePendingBlocksWrite(formalSeedPending, false, settled) {
		t.Fatal("formal-review local-soft seed blocked the next whole-draft attempt")
	}
	missingOrStaleJudge := formalSeedPending
	missingOrStaleJudge.LocalSoftEditBeforeJudge = true
	if !draftChapterRejudgePendingBlocksWrite(missingOrStaleJudge, false, settled) {
		t.Fatal("pre-judge local-soft state used historical ledger evidence to waive a missing/stale current-hash judge")
	}

	// Once that next whole draft lands, it no longer matches the rejected final
	// body. Its ordinary current-hash provider boundary must close a second write.
	replacementPending := DraftExternalGateInspection{Status: DraftExternalGateRejudgePending}
	if !draftChapterRejudgePendingBlocksWrite(replacementPending, false, false) {
		t.Fatal("unjudged replacement hash acquired a second whole-draft write")
	}
	if !draftChapterRejudgePendingBlocksWrite(replacementPending, false, settled) {
		t.Fatal("semantic settlement incorrectly waived an ordinary rejudge-pending hash")
	}
}

func renderConvergenceMustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
