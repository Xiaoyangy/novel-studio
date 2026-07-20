package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestExpressionOnlySealedReviewDraftJudgeCheckCommit(t *testing.T) {
	st, _, _, _ := sealedCommitControlTestFixture(t)
	markPipelineManaged(t, st)
	if err := st.Progress.MarkChapterComplete(1, 1000, "scene", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, "正式复审仅要求表达层重写", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}

	oldBody := "第一章\n\n林澈看见欠费单。她不是急着接钥匙，而是先确认欠费单，再交接钥匙。"
	if err := st.Drafts.SaveFinalChapter(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	oldSHA := reviewreport.BodySHA256(oldBody)
	review := domain.ReviewEntry{
		Chapter: 1, BodySHA256: oldSHA, Scope: "chapter", ContractStatus: "met", Verdict: "rewrite",
		Summary: "只修正文表达，不改变既定世界结果。",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Verdict: "pass"},
			{Dimension: "character", Verdict: "pass"},
			{Dimension: "pacing", Verdict: "pass"},
			{Dimension: "continuity", Verdict: "pass"},
			{Dimension: "foreshadow", Verdict: "pass"},
			{Dimension: "hook", Verdict: "pass"},
		},
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Description: "把主角的犹豫落到手指停顿，只重渲染表达。"}},
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	const feedbackToken = "把主角的犹豫落到手指停顿"
	brief := "# rewrite brief\n\n- body_sha256: " + oldSHA + "\n- " + feedbackToken + "，不改变事实结果。\n"
	if err := st.Drafts.SaveRewriteBrief(1, brief); err != nil {
		t.Fatal(err)
	}
	gate := reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: oldSHA,
		RuleViolations: []rules.Violation{{Rule: "pov_interiority_thin", Severity: rules.SeverityError}},
	}
	rawGate, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "reviews", "01_ai_gate.json"), rawGate, 0o644); err != nil {
		t.Fatal(err)
	}
	// The committed seed already crossed the provider gate before the later
	// formal semantic review rejected its expression. That old pass must not be
	// mistaken for approval of the replacement hash.
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: oldSHA, AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})

	seedContext, err := NewContextTool(st, References{}, "").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err != nil || !strings.Contains(string(seedContext), feedbackToken) {
		t.Fatalf("exact rejected seed did not receive its bounded review overlay: err=%v context=%s", err, seedContext)
	}
	if err := ValidateCurrentChapterRenderPlanForExecution(st, 1); err != nil {
		t.Fatalf("exact sealed expression-only seed could not enter draft_chapter: %v", err)
	}

	newBody := "第一章\n\n林澈把欠费单推回桌面，指尖在金额栏停了一下。她从头核到尾，确认每一项都对得上，才接过钥匙。先确认欠费单，再交接钥匙，这个顺序没有被跳过。"
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": newBody, "mode": "write",
	})); err != nil {
		t.Fatalf("sealed expression-only draft write: %v", err)
	}
	if ReviewRequiresFreshDraft(st, 1) {
		t.Fatal("new hash did not consume the exact rejected-seed permission")
	}
	replacementContext, err := NewContextTool(st, References{}, "").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(replacementContext), feedbackToken) || strings.Contains(string(replacementContext), "sealed_rerender_feedback") {
		t.Fatalf("old formal review leaked into the replacement-hash context: %s", replacementContext)
	}
	if err := ValidateCurrentChapterRenderPlanForExecution(st, 1); err == nil {
		t.Fatal("unjudged replacement hash reused the sealed plan before DeepSeek")
	}

	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newBody), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	if err := ValidateCurrentChapterRenderPlanForExecution(st, 1); err != nil {
		t.Fatalf("current-hash DeepSeek pass did not restore ordinary frozen-plan validation: %v", err)
	}
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": newBody + "\n", "mode": "write",
	})); err == nil || !strings.Contains(err.Error(), "单次表达层重渲染") {
		t.Fatalf("formal review authorized more than one replacement hash: %v", err)
	}

	consistencyRaw, err := NewCheckConsistencyTool(st).Execute(
		context.Background(), json.RawMessage(`{"chapter":1}`),
	)
	if err != nil {
		t.Fatalf("check replacement hash: %v", err)
	}
	var consistency struct {
		HardReceipt DraftHardConsistencyReceipt `json:"hard_consistency_receipt"`
	}
	if err := json.Unmarshal(consistencyRaw, &consistency); err != nil {
		t.Fatal(err)
	}
	if !consistency.HardReceipt.Passed {
		t.Fatalf("replacement did not earn an exact-body hard receipt: %s", consistencyRaw)
	}
	// Preserve this exact pre-commit state for the stale-receipt branch. The
	// primary branch below proves that a valid receipt closes edit; the clone
	// proves that receipt identity mismatch does not accidentally close it.
	mismatchRoot := filepath.Join(t.TempDir(), "receipt-mismatch")
	if err := os.CopyFS(mismatchRoot, os.DirFS(st.Dir())); err != nil {
		t.Fatalf("clone commit-ready fixture: %v", err)
	}
	if _, err := NewEditChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "old_string": "金额栏停了一下", "new_string": "金额栏停了半拍",
	})); err == nil || !strings.Contains(err.Error(), "直接 commit_chapter") {
		t.Fatalf("commit-ready expression-only replacement reopened subjective editing: %v", err)
	}
	if draft, err := st.Drafts.LoadDraft(1); err != nil || draft != newBody {
		t.Fatalf("rejected edit changed commit-ready replacement: err=%v draft=%q", err, draft)
	}
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1,
		"summary": "林澈逐项确认欠费单后接过钥匙。",
	})); err != nil {
		t.Fatalf("sealed expression-only replacement commit: %v", err)
	}
	if final, err := st.Drafts.LoadChapterText(1); err != nil || final != newBody {
		t.Fatalf("replacement commit did not install the judged exact hash: err=%v final=%q", err, final)
	}

	mismatchStore := store.NewStore(mismatchRoot)
	receiptPath := filepath.Join(mismatchRoot, draftHardConsistencyReceiptArtifact(1))
	receiptRaw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var staleReceipt DraftHardConsistencyReceipt
	if err := json.Unmarshal(receiptRaw, &staleReceipt); err != nil {
		t.Fatal(err)
	}
	staleReceipt.BodySHA256 = strings.Repeat("f", 64)
	receiptRaw, err = json.MarshalIndent(staleReceipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, receiptRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEditChapterTool(mismatchStore).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "old_string": "金额栏停了一下", "new_string": "金额栏停了半拍",
	})); err != nil {
		t.Fatalf("mismatched hard receipt incorrectly froze edit_chapter: %v", err)
	}
	if draft, err := mismatchStore.Drafts.LoadDraft(1); err != nil || !strings.Contains(draft, "金额栏停了半拍") {
		t.Fatalf("receipt-mismatch edit did not reach draft: err=%v draft=%q", err, draft)
	}
}

func TestExpressionOnlyReplacementCanBeJudgedThenExactBlockingFeedbackRendersThirdHash(t *testing.T) {
	st, _, _, _ := sealedCommitControlTestFixture(t)
	markPipelineManaged(t, st)
	if err := st.Progress.MarkChapterComplete(1, 1000, "scene", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, "正式复审仅要求表达层重写", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}

	formalBody := "第一章\n\n林澈看见欠费单。她不是急着接钥匙，而是先确认欠费单，再交接钥匙。"
	if err := st.Drafts.SaveFinalChapter(1, formalBody); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, formalBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	formalSHA := reviewreport.BodySHA256(formalBody)
	review := domain.ReviewEntry{
		Chapter: 1, BodySHA256: formalSHA, Scope: "chapter", ContractStatus: "met", Verdict: "rewrite",
		Summary: "只修正文表达，不改变既定世界结果。",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Verdict: "pass"},
			{Dimension: "character", Verdict: "pass"},
			{Dimension: "pacing", Verdict: "pass"},
			{Dimension: "continuity", Verdict: "pass"},
			{Dimension: "foreshadow", Verdict: "pass"},
			{Dimension: "hook", Verdict: "pass"},
		},
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Description: "减少解释句，增加真实判断。"}},
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveRewriteBrief(1, "# rewrite brief\n\n- body_sha256: "+formalSHA+"\n- 减少解释句，增加真实判断。\n"); err != nil {
		t.Fatal(err)
	}
	gate := reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: formalSHA,
		RuleViolations: []rules.Violation{{Rule: "pov_interiority_thin", Severity: rules.SeverityError}},
	}
	rawGate, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "reviews", "01_ai_gate.json"), rawGate, 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: formalSHA, AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})

	firstReplacement := "第一章\n\n林澈把欠费单推回桌面，指尖在金额栏停了一下。她从头核到尾，确认每一项都对得上，才接过钥匙。先确认欠费单，再交接钥匙，这个顺序没有被跳过。"
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": firstReplacement, "mode": "write",
	})); err != nil {
		t.Fatalf("first expression-only replacement: %v", err)
	}
	if _, err := InspectDraftHardFactAnchors(st, 1, firstReplacement); err == nil {
		t.Fatal("unjudged replacement unexpectedly acquired the ordinary write-side plan capability")
	}
	if inspection, err := InspectDraftHardFactAnchorsForExternalJudge(st, 1, firstReplacement); err != nil || !inspection.Passed {
		t.Fatalf("unjudged replacement could not reach deterministic judge preflight: inspection=%+v err=%v", inspection, err)
	}

	// Recovery may retain a later rejected edit's 18% marker while the current
	// draft has rolled back to this earlier replacement. Model that exact stale-
	// judge/current-draft mismatch before replaying the cached 3% result.
	firstSHA := reviewreport.BodySHA256(firstReplacement)
	blockedReplacement := strings.Replace(firstReplacement, "金额栏停了一下", "数字栏停了半拍", 1)
	blockedSHA := reviewreport.BodySHA256(blockedReplacement)
	const feedbackToken = "删去成交式问答，让两位女主的判断互相错开"
	blockedStatus := draftExternalJudgeStatus{
		BodySHA256: blockedSHA, Blocking: true, AdviceComplete: true,
		AIProbabilityPercent: 18, PassExclusivePercent: 4,
		Summary:         feedbackToken,
		Evidence:        []string{"连续三段都先解释判断再补动作"},
		RevisionPlan:    []string{feedbackToken + "，保留欠费单与钥匙交接的事实顺序"},
		DialogueFixPlan: []string{"让一次漏答改变关系压力"},
		AuthorVoicePlan: []string{"用林澈的误判组织观察顺序"},
		RAGRules:        []string{"不得改变先核欠费单再接钥匙"},
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, blockedStatus)
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: blockedSHA, InitialDraftBodySHA256: formalSHA,
		Source: "deepseek_ai_judge", Evaluator: "deepseek",
		AIProbabilityPercent: 18, PassExclusivePercent: 4,
		Summary: feedbackToken, Evidence: blockedStatus.Evidence,
		RevisionPlan: blockedStatus.RevisionPlan, AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	if stale, err := InspectDraftExternalGateWithStore(st, 1); err != nil || stale.Status != DraftExternalGateRejudgePending {
		t.Fatalf("stale blocked edit marker did not leave the rolled-back hash judgeable: inspection=%+v err=%v", stale, err)
	}
	if err := ClearDraftExternalRerenderRequirement(st.Dir(), 1); err != nil {
		t.Fatal(err)
	}

	// This is the cached 3% result for the rolled-back replacement. It authorizes
	// only finalization/editing of this exact hash, not another whole render; the
	// judge has cleared the spent one-shot requirement, as production does after
	// a pass, and that crash/legacy shape must not make the sealed plan stale.
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: firstSHA, AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	if err := ValidateCurrentChapterRenderPlanForExecution(st, 1); err != nil {
		t.Fatalf("marker-cleared cached passing replacement did not restore finalizer capability: %v", err)
	}
	if _, err := NewCheckConsistencyTool(st).Execute(
		context.Background(), json.RawMessage(`{"chapter":1}`),
	); err != nil {
		t.Fatalf("marker-cleared cached passing replacement could not run check_consistency: %v", err)
	}
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": firstReplacement + "\n", "mode": "write",
	})); err == nil || !strings.Contains(err.Error(), "单次表达层重渲染") {
		t.Fatalf("cleared requirement reopened a second whole-draft write: %v", err)
	}

	// Model the deterministic finalizer's single edit. Its resulting hash has a
	// cached complete 18% judgment and must route to one fresh render, not back to
	// judging or editing the earlier replacement.
	if err := st.Drafts.SaveDraft(1, blockedReplacement); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, blockedStatus)
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: blockedSHA, InitialDraftBodySHA256: formalSHA,
		Source: "deepseek_ai_judge", Evaluator: "deepseek",
		AIProbabilityPercent: 18, PassExclusivePercent: 4,
		Summary: feedbackToken, Evidence: blockedStatus.Evidence,
		RevisionPlan: blockedStatus.RevisionPlan, AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized ||
		inspection.EvaluatedBodySHA256 != blockedSHA {
		t.Fatalf("exact 18%% replacement rejection did not authorize one rerender: inspection=%+v err=%v", inspection, err)
	}
	contextRaw, err := NewContextTool(st, References{}, "").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err != nil || !strings.Contains(string(contextRaw), feedbackToken) ||
		!strings.Contains(string(contextRaw), blockedSHA) {
		t.Fatalf("sealed context did not consume exact blocked replacement feedback: err=%v context=%s", err, contextRaw)
	}

	thirdHashBody := "第一章\n\n欠费单被林澈压在桌角。沈知遥伸手来拿钥匙，她却先问了金额里最不起眼的一项。对方漏答的那一秒，她把钥匙又收回掌心。先确认欠费单，再交接钥匙；账目逐项对上后，她才松开手。"
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": thirdHashBody, "mode": "write",
	})); err != nil {
		t.Fatalf("exact blocked replacement could not produce third hash: %v", err)
	}
	after, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || after.Status != DraftExternalGateRejudgePending {
		t.Fatalf("third hash did not return to exact-body rejudge lock: inspection=%+v err=%v", after, err)
	}
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": thirdHashBody + "\n", "mode": "write",
	})); err == nil || !strings.Contains(err.Error(), "必须先运行 DeepSeek provider judge") {
		t.Fatalf("blocking feedback authorized more than one fresh hash: %v", err)
	}
}

func TestExpressionOnlyFinalizerCapabilityMatchesActiveLocalSoftRecoveryShape(t *testing.T) {
	const bodySHA = "9b602638f3233279f72c5c1ec0fbf1bf6912031f46943cce25306d3988263624"
	status := &draftExternalJudgeStatus{
		BodySHA256: bodySHA, AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	}
	activeShape := DraftExternalGateInspection{
		Status:                   DraftExternalGateRejudgePending,
		CurrentBodySHA256:        bodySHA,
		EvaluatedBodySHA256:      bodySHA,
		ArtifactExists:           true,
		LocalSoftEditPending:     true,
		Requirement:              nil,
		RegisteredRetestDeferred: false,
	}
	if !expressionOnlyReviewReplacementReadyForFinalizer(status, bodySHA, activeShape, nil) {
		t.Fatal("exact current-hash DeepSeek pass plus marker-cleared local-soft state did not grant finalizer/check capability")
	}
	if err := draftExternalGateEditPrecondition(3, activeShape); err != nil {
		t.Fatalf("active local-soft shape did not preserve its exactly-one-edit route: %v", err)
	}
	ordinaryPending := activeShape
	ordinaryPending.LocalSoftEditPending = false
	if expressionOnlyReviewReplacementReadyForFinalizer(status, bodySHA, ordinaryPending, nil) {
		t.Fatal("ordinary unjudged rejudge_pending hash acquired finalizer capability")
	}
	blocking := *status
	blocking.Blocking = true
	if expressionOnlyReviewReplacementReadyForFinalizer(&blocking, bodySHA, activeShape, nil) {
		t.Fatal("blocking current-hash judgment acquired pass-only finalizer capability")
	}
	mismatch := activeShape
	mismatch.EvaluatedBodySHA256 = strings.Repeat("a", 64)
	if expressionOnlyReviewReplacementReadyForFinalizer(status, bodySHA, mismatch, nil) {
		t.Fatal("stale external artifact acquired exact-hash finalizer capability")
	}
}
