package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestInspectDraftHardFactAnchorsRejectsGenericOneBowlAndAcceptsChineseEquivalent(t *testing.T) {
	st := newHardFactAnchorTestStore(t,
		[]string{"前面离开的母子必须返回，点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"正文至少建立两条主观链；系统只允许一次回应；第1章整篇复测2次。"},
	)

	failing, err := InspectDraftHardFactAnchors(st, 1, "一个带孩子的女人停下来，只要了一碗豆腐脑。")
	if err != nil {
		t.Fatal(err)
	}
	if failing.Passed ||
		!hasHardFactAnchor(failing.Missing, DraftHardFactAnchorEntityCount, 2, "碗", false) ||
		!hasHardFactAnchor(failing.Missing, DraftHardFactAnchorAmount, 12, "元", false) ||
		!hasHardFactLiteral(failing.Missing, "少糖") {
		t.Fatalf("generic one-bowl draft bypassed exact anchors: %+v", failing)
	}

	passing, err := InspectDraftHardFactAnchors(st, 1,
		"前面离开的母子又折回来，要了两碗豆腐脑，其中一碗少糖。她扫完码：一共十二块。")
	if err != nil {
		t.Fatal(err)
	}
	if !passing.Passed || len(passing.Missing) != 0 {
		t.Fatalf("normalized Chinese amount/count should pass: %+v", passing)
	}
	for _, anchor := range passing.Anchors {
		if anchor.Unit == "条" || anchor.Unit == "次" || anchor.Unit == "章" {
			t.Fatalf("writing-recipe count leaked into prose anchors: %+v", passing.Anchors)
		}
	}
}

func TestInspectDraftHardFactAnchorsAcceptsContextBoundChineseAmountWithoutYuan(t *testing.T) {
	st := newHardFactAnchorTestStore(t,
		[]string{"林澈获得一百万元青山县专项经营额度使用权，非个人存款。"}, nil,
	)

	for _, body := range []string{
		"手机上写得清楚：一百万专项经营额度已经绑定。",
		"屏幕只亮了一行，一百万到账了。",
		"可用的是100万的青山县专项经营额度，不是个人存款。",
		"青山县经营专用额度为1,000,000元。",
	} {
		inspection, err := InspectDraftHardFactAnchors(st, 1, body)
		if err != nil {
			t.Fatal(err)
		}
		if !inspection.Passed {
			t.Fatalf("context-bound equivalent amount should pass for %q: %+v", body, inspection)
		}
	}

	for _, body := range []string{
		"屏幕上只有一个裸数字：一百万。",
		"青山县常住人口接近一百万人。",
		"手机显示九十九万专项经营额度已经绑定。",
		"页面明确写着：没有一百万到账。",
		"页面仍是一百万尚未到账。",
	} {
		inspection, err := InspectDraftHardFactAnchors(st, 1, body)
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Passed || !hasHardFactAnchor(inspection.Missing, DraftHardFactAnchorAmount, 1_000_000, "元", false) {
			t.Fatalf("non-equivalent or negated amount bypassed anchor for %q: %+v", body, inspection)
		}
	}
}

func TestInspectDraftHardFactAnchorsRejectsNegatedRealityMarkers(t *testing.T) {
	st := newHardFactAnchorTestStore(t,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"}, nil,
	)
	// The amount is deliberately present in a different purchase. The narrow
	// mechanical gate does not pretend to solve arbitrary object coreference,
	// but negated count/literal facts must keep the combined reality tuple from
	// passing and therefore from reaching commit.
	inspection, err := InspectDraftHardFactAnchors(st, 1,
		"女人摆手说不要两碗豆腐脑，端上来的两碗都没少糖。她另花十二元买了瓶水。")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Passed {
		t.Fatalf("negated hard facts plus unrelated same amount passed: %+v", inspection)
	}
	if !hasHardFactLiteral(inspection.Missing, "少糖") {
		t.Fatalf("negated literal was treated as fulfilled: %+v", inspection)
	}

	countOnly := newHardFactAnchorTestStore(t, []string{"母子返回后点两碗豆腐脑。"}, nil)
	countInspection, err := InspectDraftHardFactAnchors(countOnly, 1, "女人摆手说不要两碗豆腐脑。")
	if err != nil {
		t.Fatal(err)
	}
	if countInspection.Passed || !hasHardFactAnchor(countInspection.Missing, DraftHardFactAnchorEntityCount, 2, "碗", false) {
		t.Fatalf("不要两碗 was treated as positive count evidence: %+v", countInspection)
	}
}

func TestInspectDraftHardFactAnchorsCoversChapterTwoRealityTuples(t *testing.T) {
	st := newHardFactAnchorTestStore(t,
		[]string{
			"68000元取货款必须继续阻断；只准落地五摊；灯具材料680元、五金360元、老丁人工300元分别准确；往返43公里、油费86元、半日人工180元全部留痕；首批五套；不得增加第六套。",
		},
		[]string{"五个摊位利益必须有差异；原拒绝者推来第六张桌子，必须停在黄线外。"},
	)
	body := "他试着付六万八千块钱买车，仍旧失败。五个摊位各拿到五套里的那一套。" +
		"板材灯条六百八十元，五金件三百六十块钱，老丁工钱三百块。" +
		"往返四十三千米，油费八十六元，人工一百八十块钱。原先拒绝的人又推来第六张桌子，停在黄线外。"
	passing, err := InspectDraftHardFactAnchors(st, 1, body)
	if err != nil {
		t.Fatal(err)
	}
	if !passing.Passed {
		t.Fatalf("chapter-two tuple equivalents should pass: %+v", passing)
	}
	if hasHardFactAnchor(passing.Anchors, DraftHardFactAnchorEntityCount, 6, "套", true) {
		t.Fatalf("negative sixth-set cap became a positive prose obligation: %+v", passing.Anchors)
	}
	if !hasHardFactAnchor(passing.Anchors, DraftHardFactAnchorEntityCount, 6, "桌", true) {
		t.Fatalf("sixth-table reality anchor missing: %+v", passing.Anchors)
	}

	wrong := strings.Replace(body, "油费八十六元", "油费八十七元", 1)
	failing, err := InspectDraftHardFactAnchors(st, 1, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if failing.Passed || !hasHardFactAnchor(failing.Missing, DraftHardFactAnchorAmount, 86, "元", false) {
		t.Fatalf("wrong oil amount was not blocked: %+v", failing)
	}
}

func TestExtractDraftHardFactAnchorsIgnoresAbstractWritingCounts(t *testing.T) {
	anchors := extractDraftHardFactAnchors([]string{
		"正文至少建立两条主观链；系统只允许一次回应；第2章按整篇复测三次；保留两次判断变化。",
	})
	if len(anchors) != 0 {
		t.Fatalf("abstract writing metrics must not become prose anchors: %+v", anchors)
	}
}

func TestExtractDraftHardFactAnchorsRejectsCommonNegativePrefixes(t *testing.T) {
	anchors := extractDraftHardFactAnchors([]string{
		"不要两碗豆腐脑；都没少糖；没付12元；尚未走43公里；全程无43公里；不点五个摊位。",
	})
	if len(anchors) != 0 {
		t.Fatalf("negative contexts became positive reality anchors: %+v", anchors)
	}
}

func TestExtractDraftHardFactAnchorsDoesNotTreatFutureOrHelplessAsNegation(t *testing.T) {
	anchors := extractDraftHardFactAnchors([]string{
		"无奈仍付12元；未来的行程还剩43公里。",
	})
	if !hasHardFactAnchor(anchors, DraftHardFactAnchorAmount, 12, "元", false) {
		t.Fatalf("无奈 was mistaken for bare 无 negation: %+v", anchors)
	}
	if !hasHardFactAnchor(anchors, DraftHardFactAnchorDistance, 43, "公里", false) {
		t.Fatalf("未来 was mistaken for bare 未 negation: %+v", anchors)
	}
}

func TestExtractDraftHardFactAnchorsTreatsBareBlockAsMoneyOnlyWithMoneyContext(t *testing.T) {
	anchors := extractDraftHardFactAnchors([]string{"油费八十六块必须留痕；五块价牌同时亮起。"})
	if !hasHardFactAnchor(anchors, DraftHardFactAnchorAmount, 86, "元", false) {
		t.Fatalf("bare 块 with money context was not normalized: %+v", anchors)
	}
	if hasHardFactAnchor(anchors, DraftHardFactAnchorAmount, 5, "元", false) {
		t.Fatalf("physical classifier 块 was mistaken for money: %+v", anchors)
	}
}

func TestInspectDraftHardFactAnchorsRejectsStalePlanEpoch(t *testing.T) {
	st := newHardFactAnchorTestStore(t, []string{"两碗豆腐脑合计12元，其中一碗少糖。"}, nil)
	sim, err := st.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil {
		t.Fatalf("load simulation: sim=%+v err=%v", sim, err)
	}
	sim.TimeWindow = "更新后的世界推演窗口"
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDraftHardFactAnchors(st, 1, "两碗豆腐脑，一碗少糖，十二元。"); err == nil {
		t.Fatal("stale plan epoch must return an error before prose inspection")
	}
}

func TestDraftChapterHardFactAnchorsRejectBeforeWrite(t *testing.T) {
	st := newProductionHardFactPlanStore(t,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"真实消费成立后主角才继续行动。"},
	)
	beforeCheckpoints := len(st.Checkpoints.All())
	_, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "mode": "write",
		"content": "第一章 测试章\n\n一个带孩子的女人停下来，只点了一碗豆腐脑。",
	}))
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("hard-fact deficient write was not rejected: %v", err)
	}
	for _, want := range []string{"kind=entity_count", "value=2", "unit=碗", "kind=amount", "value=12", "少糖", "source="} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("hard-fact error omitted %q: %v", want, err)
		}
	}
	if draft, loadErr := st.Drafts.LoadDraft(1); loadErr != nil || strings.TrimSpace(draft) != "" {
		t.Fatalf("rejected write changed real draft: draft=%q err=%v", draft, loadErr)
	}
	if got := len(st.Checkpoints.All()); got != beforeCheckpoints {
		t.Fatalf("rejected write appended checkpoint: before=%d after=%d", beforeCheckpoints, got)
	}
}

func TestEditChapterHardFactAnchorsRejectBeforeMutation(t *testing.T) {
	st := newProductionHardFactPlanStore(t,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"真实消费成立后主角才继续行动。"},
	)
	original := "第一章 测试章\n\n母子回来点了两碗豆腐脑，其中一碗少糖，合计十二元。"
	if err := st.Drafts.SaveDraft(1, original); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	beforeCheckpoints := len(st.Checkpoints.All())
	_, err := NewEditChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "old_string": "合计十二元", "new_string": "没有写明合计金额",
	}))
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "value=12") {
		t.Fatalf("amount-deleting edit was not rejected: %v", err)
	}
	if got, loadErr := st.Drafts.LoadDraft(1); loadErr != nil || got != original {
		t.Fatalf("rejected edit changed real draft: got=%q err=%v", got, loadErr)
	}
	if got := len(st.Checkpoints.All()); got != beforeCheckpoints {
		t.Fatalf("rejected edit appended checkpoint: before=%d after=%d", beforeCheckpoints, got)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "edit"); cp != nil {
		t.Fatalf("rejected edit emitted edit checkpoint: %+v", cp)
	}
}

func TestMergeChapterPartsHardFactAnchorsRejectBeforeDraftMutation(t *testing.T) {
	st := newProductionHardFactPlanStore(t,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"真实消费成立后主角才继续行动。"},
	)
	part := mustJSON(t, map[string]any{
		"chapter": 1, "part": 1, "total_parts": 1, "title": "整章", "focus": "真实消费",
		"content": "第一章 测试章\n\n一个带孩子的女人只点了一碗豆腐脑。",
	})
	if _, err := NewDraftChapterPartTool(st).Execute(context.Background(), part); err != nil {
		t.Fatalf("write part: %v", err)
	}
	beforeCheckpoints := len(st.Checkpoints.All())
	_, err := NewMergeChapterPartsTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "expected_parts": 1,
	}))
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "value=12") {
		t.Fatalf("hard-fact deficient merge was not rejected: %v", err)
	}
	if draft, loadErr := st.Drafts.LoadDraft(1); loadErr != nil || strings.TrimSpace(draft) != "" {
		t.Fatalf("rejected merge changed real draft: draft=%q err=%v", draft, loadErr)
	}
	if got := len(st.Checkpoints.All()); got != beforeCheckpoints {
		t.Fatalf("rejected merge appended checkpoint: before=%d after=%d", beforeCheckpoints, got)
	}
	if index, loadErr := st.Drafts.LoadDraftPartIndex(1); loadErr != nil || index == nil || len(index.Parts) != 1 {
		t.Fatalf("rejected merge did not preserve source parts: index=%+v err=%v", index, loadErr)
	}
}

func TestCheckConsistencyReportsHardFactAnchorViolations(t *testing.T) {
	st := newProductionHardFactPlanStore(t,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"真实消费成立后主角才继续行动。"},
	)
	body := "第一章 测试章\n\n一个带孩子的女人只点了一碗豆腐脑。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	raw, err := NewCheckConsistencyTool(st).Execute(context.Background(), mustJSON(t, map[string]any{"chapter": 1}))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		HardFacts   DraftHardFactAnchorResult   `json:"hard_fact_anchor_check"`
		HardGate    []string                    `json:"hard_gate_violations"`
		HardReceipt DraftHardConsistencyReceipt `json:"hard_consistency_receipt"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.HardFacts.Passed || len(payload.HardFacts.Missing) == 0 {
		t.Fatalf("check_consistency omitted hard-fact result: %s", raw)
	}
	if joined := strings.Join(payload.HardGate, "\n"); !strings.Contains(joined, "hard_fact_anchor") || !strings.Contains(joined, "value=12") {
		t.Fatalf("hard fact missing from hard_gate_violations: %#v", payload.HardGate)
	}
	if payload.HardReceipt.Passed || payload.HardReceipt.BodySHA256 != reviewreport.BodySHA256(body) {
		t.Fatalf("failed hard consistency receipt was not exact-body/fail-closed: %+v", payload.HardReceipt)
	}
	scope := domain.ChapterScope(1)
	if cp := st.Checkpoints.LatestByStep(scope, "consistency_check"); cp != nil {
		t.Fatalf("hard violations emitted a commit-eligible consistency checkpoint: %+v", cp)
	}
	if cp := st.Checkpoints.LatestByStep(scope, "consistency_check_failed"); cp == nil {
		t.Fatal("hard violations did not emit a diagnostic failed checkpoint")
	}
	if err := requireCurrentDraftConsistency(st, 1, body); err == nil || !strings.Contains(err.Error(), "passed=true") {
		t.Fatalf("failed hard receipt was accepted as consistency proof: %v", err)
	}
}

func TestCommitChapterRecomputesHardFactAnchorsAfterAllExactEvidencePasses(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("hard-fact-commit", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)
	setCurrentPlanHardFactAnchors(t, st,
		[]string{"母子返回后点两碗豆腐脑，其中一碗少糖，合计12元。"},
		[]string{"真实消费成立后主角才继续行动。"},
	)

	const detector = "zhuque"
	const mode = "novel-whole-text-single-segment"
	oldBody := "第一章 测试章\n\n旧候选正文。"
	high := appendRegisteredExternalDetection(t, st.Dir(), 1, oldBody, detector, mode, 86)
	if err := SetRegisteredExternalRerenderRequirement(st.Dir(), high); err != nil {
		t.Fatal(err)
	}
	body := "第一章 测试章\n\n母子回到摊前，只点了一碗豆腐脑。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, st.Dir(), 1, body, detector, mode, 2)
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	if report, gate := inspectDraftAIGCGate(st, 1, body); !draftAIGCRawLocalGateResult(report, gate).Passed {
		// The body is intentionally below the enforcement floor; keep the test
		// explicit that the local gate is not the reason commit is rejected.
		t.Fatalf("short exact-body fixture unexpectedly failed local gate: %+v", gate)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved || !inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("DeepSeek/named exact-hash evidence is not approved: inspection=%+v err=%v", inspection, err)
	}
	if _, err := NewCheckConsistencyTool(st).Execute(context.Background(), mustJSON(t, map[string]any{"chapter": 1})); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "consistency_check", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit", "consistency_check", "consistency_check_failed",
	); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentDraftConsistency(st, 1, body); err == nil {
		t.Fatal("ordinary exact-body checkpoint bypassed failed hard consistency receipt")
	}

	args := mustJSON(t, map[string]any{
		"chapter": 1, "summary": "真实消费元组仍不完整。",
		"characters": []string{"主角", "配角"}, "key_events": []string{"核对真实消费"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err == nil ||
		!errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "hard-fact anchor") {
		t.Fatalf("commit bypassed exact-body hard-fact recomputation: %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); final != "" {
		t.Fatalf("rejected commit wrote final chapter: %q", final)
	}
	if pending, err := st.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("rejected commit crossed pending boundary: pending=%+v err=%v", pending, err)
	}
}

func TestDraftChapterHardFactAnchorsKeepsNoAnchorLegacyCompatibility(t *testing.T) {
	st := newProductionHardFactPlanStore(t,
		[]string{"主角必须保持已经成立的关系边界。"},
		[]string{"主角核实名册后进入登记争议。"},
	)
	body := "第一章 测试章\n\n林砚把名册压在灯下，核对完最后一行才推门进去。"
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "mode": "write", "content": body,
	})); err != nil {
		t.Fatalf("ordinary plan without anchors lost compatibility: %v", err)
	}
	if got, _ := st.Drafts.LoadDraft(1); got != body {
		t.Fatalf("ordinary no-anchor draft was not written: %q", got)
	}
}

func TestHardConsistencyReceiptKeepsUncheckpointedLegacyCompatibility(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章 导入稿\n\n这是没有 pipeline body epoch 的旧项目正文。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentDraftConsistency(st, 1, body); err != nil {
		t.Fatalf("legacy draft without any managed body checkpoint lost compatibility: %v", err)
	}
}

func newProductionHardFactPlanStore(t *testing.T, preserve, required []string) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("hard-fact-production", 3); err != nil {
		t.Fatal(err)
	}
	plan, err := decodeChapterPlanArgs(planArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	plan.CausalSimulation.ReviewRefinement.PreserveConstraints = append([]string(nil), preserve...)
	plan.Contract.RequiredBeats = append([]string(nil), required...)
	if _, err := finalizeChapterPlan(st, plan, false); err != nil {
		t.Fatalf("finalize hard-fact production plan: %v", err)
	}
	return st
}

func setCurrentPlanHardFactAnchors(t *testing.T, st *store.Store, preserve, required []string) {
	t.Helper()
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load current plan: plan=%+v err=%v", plan, err)
	}
	plan.CausalSimulation.ReviewRefinement.PreserveConstraints = append([]string(nil), preserve...)
	plan.Contract.RequiredBeats = append([]string(nil), required...)
	if err := st.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		t.Fatal(err)
	}
}

func newHardFactAnchorTestStore(t *testing.T, preserve, required []string) *store.Store {
	t.Helper()
	fixture := newReusableRewriteFixture(t)
	st := fixture.store
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load plan: plan=%+v err=%v", plan, err)
	}
	plan.CausalSimulation.ReviewRefinement.PreserveConstraints = append([]string(nil), preserve...)
	if required != nil {
		plan.Contract.RequiredBeats = append([]string(nil), required...)
	}
	if err := st.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(st, 1); err != nil {
		t.Fatalf("hard-fact fixture plan must be reusable: %v", err)
	}
	if !ExplicitRerenderRequestActive(st, 1) {
		t.Fatal("hard-fact fixture rerender request must be active")
	}
	if _, err := validateCurrentChapterRenderPlan(st, 1); err != nil {
		t.Fatalf("hard-fact fixture must have a fresh current plan: %v", err)
	}
	return st
}

func hasHardFactAnchor(anchors []DraftHardFactAnchor, kind string, value int64, unit string, ordinal bool) bool {
	for _, anchor := range anchors {
		if anchor.Kind == kind && anchor.Value == value && anchor.Unit == unit && anchor.Ordinal == ordinal {
			return true
		}
	}
	return false
}

func hasHardFactLiteral(anchors []DraftHardFactAnchor, literal string) bool {
	for _, anchor := range anchors {
		if anchor.Kind == DraftHardFactAnchorLiteral && anchor.Literal == literal {
			return true
		}
	}
	return false
}
