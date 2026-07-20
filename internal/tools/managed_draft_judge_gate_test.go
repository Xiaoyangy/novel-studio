package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func prepareManagedDraftJudgePlan(t *testing.T, st *store.Store, chapter int) {
	t.Helper()
	plan, err := decodeChapterPlanArgs(planArgs(chapter))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeChapterPlan(st, plan, false); err != nil {
		t.Fatalf("finalize managed test plan: %v", err)
	}
	markPipelineManaged(t, st)
}

func TestManagedFullDraftStopsForCurrentHashDeepSeekBeforeCommit(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)

	body := "第一章 测试章\n\n林砚推开登记口的木门，把名册压在灯下。"
	result, err := NewDraftChapterTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "content": body, "mode": "write",
	}))
	if err != nil {
		t.Fatalf("managed full draft: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["external_rejudge_required"] != true ||
		payload["external_rejudge_required_now"] != true ||
		payload["stop_prose_modification"] != true || payload["hard_gate_passed"] != false ||
		!strings.Contains(payload["next_step"].(string), "DeepSeek") {
		t.Fatalf("managed full draft did not return current-hash judge stop: %#v", payload)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.ArtifactExists {
		t.Fatalf("managed unjudged draft must be pending: inspection=%+v err=%v", inspection, err)
	}

	commitArgs := mustJSON(t, map[string]any{
		"chapter":                 1,
		"summary":                 "登记口出现一份需要核验的名册。",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"主角检查名册"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), commitArgs); err == nil || !strings.Contains(err.Error(), "DeepSeek") {
		t.Fatalf("commit accepted managed NotRequired/missing judge state: %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); final != "" {
		t.Fatalf("rejected managed commit wrote final chapter: %q", final)
	}

	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: false,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateAdviceIncomplete {
		t.Fatalf("same-hash but incomplete DeepSeek artifact unlocked managed draft: inspection=%+v err=%v", inspection, err)
	}

	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved {
		t.Fatalf("same-hash DeepSeek pass did not approve managed draft: inspection=%+v err=%v", inspection, err)
	}
}

func TestManagedPassingHashStillRoutesThroughLocalGateWithoutPriorRerenderMarker(t *testing.T) {
	if !draftCurrentHashNeedsLocalGateRouting(nil, true) {
		t.Fatal("pipeline-managed current hash must classify local structural/soft state after DeepSeek pass")
	}
	if draftCurrentHashNeedsLocalGateRouting(nil, false) {
		t.Fatal("legacy unmarked draft must retain compatibility when it has no rerender marker")
	}
}

func TestManagedWholeTextProbabilityWaitsForExactJudgeWithoutRerenderArtifacts(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)
	body := "第一章 测试章\n\n林砚把登记册推回窗口，请值班员先核对公共监控的时间。"
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
		RewriteFocus: []string{"保留原始本地诊断，等待当前哈希外判。"},
	}
	if err := persistDraftAIGCRerenderRequirement(st, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	if err := checkpointDraftStructuralBlock(st, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft-structural-block"); cp != nil {
		t.Fatalf("provider-pending probability proxy consumed a structural attempt: %+v", cp)
	}
	marker := filepath.Join(st.Dir(), "reviews", "drafts", "01_full_rerender_required.json")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("provider-pending probability proxy persisted rerender marker: %v", err)
	}
}

func TestManagedLegacyMixedProbabilityMarkerRoutesJudgeThenOneLocalSoftEdit(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)

	content := `“先把桌子挪开。”林澈说。

“价牌放左边。”沈知遥接话。

“车只能跑一趟。”贺骁补充。

“那就先装三家。”老丁回答。

“剩下两家怎么办？”摊主追问。

“下午再送。”林澈解释。

“票据别忘了。”沈知遥提醒。

“我现在就开。”老丁点头。`
	report := aigc.Report{
		AIGCPercent: 79.07, WholeTextSegmentGate: 79.07,
		Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi},
	}
	gate := draftAIGCGateResult{
		RawLocalGatePercent: 79.07, EffectiveGatePercent: 79.07,
		PassExclusivePercent: 4, Enforced: true,
	}
	if !draftAIGCLocalProbabilityComponentCalibratable(content, report) ||
		draftAIGCLocalProbabilityOnly(content, report) ||
		len(draftAIGCExternalCurrentBodyBlockers(content)) == 0 {
		t.Fatalf("fixture did not separate probability from statistical warnings: report=%+v blockers=%+v", report, draftAIGCExternalCurrentBodyBlockers(content))
	}
	if err := st.Drafts.SaveDraft(1, content); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	// This is the exact predicate used to downgrade an older current-body local
	// marker from whole-rerender authority to provider-first recovery.
	missingRouted, err := draftAIGCLocalMarkerProviderRouted(st, 1, content, report, gate)
	if err != nil || !missingRouted {
		t.Fatalf("missing exact judge did not route legacy marker to RejudgePending: routed=%v err=%v", missingRouted, err)
	}
	if err := persistDraftAIGCRerenderRequirement(st, 1, content, report, gate); err != nil {
		t.Fatal(err)
	}
	if err := checkpointDraftStructuralBlock(st, 1, content, report, gate); err != nil {
		t.Fatal(err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft-structural-block"); cp != nil {
		t.Fatalf("provider-pending mixed signal consumed a structural attempt: %+v", cp)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	corroborated := corroborateDraftAIGCGate(st, 1, content, report, gate)
	afterPassRouted, err := draftAIGCLocalMarkerProviderRouted(st, 1, content, report, corroborated)
	if err != nil || !afterPassRouted {
		t.Fatalf("exact pass did not retire legacy whole-rerender authority: routed=%v err=%v", afterPassRouted, err)
	}
	structural, soft := draftExternalLocalGateDisposition(content, report, corroborated)
	if structural || !soft {
		t.Fatalf("exact pass did not split probability from one local-soft warning repair: structural=%v soft=%v", structural, soft)
	}
	if required, routeErr := draftAIGCWholeDraftRerenderRequired(st, 1, content, report, corroborated); routeErr != nil || required {
		t.Fatalf("resolved probability plus statistical warnings still requested a whole rerender: required=%v err=%v", required, routeErr)
	}

	hard := report
	hard.ContentIntegrityFloor = 80
	if draftAIGCLocalProbabilityComponentCalibratable(content, hard) {
		t.Fatal("content-integrity evidence entered provider calibration")
	}
	hard.ContentIntegrityFloor = 0
	hard.Stats.Hanzi = 1000
	hard.Stats.Repeated12Extra = 100
	if draftAIGCLocalProbabilityComponentCalibratable(content, hard) {
		t.Fatal("extreme exact repetition entered provider calibration")
	}
}

func TestManagedFirstMergeStopsForCurrentHashDeepSeek(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)

	part := mustJSON(t, map[string]any{
		"chapter": 1, "part": 1, "total_parts": 1,
		"title": "完整章", "focus": "登记冲突",
		"content": "第一章 测试章\n\n林砚把名册翻到最后一页，门外的钟已经响了。",
	})
	if _, err := NewDraftChapterPartTool(st).Execute(context.Background(), part); err != nil {
		t.Fatalf("managed draft part: %v", err)
	}
	result, err := NewMergeChapterPartsTool(st).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "expected_parts": 1,
	}))
	if err != nil {
		t.Fatalf("managed first merge: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["external_rejudge_required"] != true ||
		payload["external_rejudge_required_now"] != true ||
		payload["stop_prose_modification"] != true || payload["hard_gate_passed"] != false ||
		!strings.Contains(payload["next_step"].(string), "DeepSeek") {
		t.Fatalf("managed first merge did not return current-hash judge stop: %#v", payload)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.ArtifactExists {
		t.Fatalf("managed unjudged merge must be pending: inspection=%+v err=%v", inspection, err)
	}
}

func TestLegacyJournaledDraftWithoutPipelineKeepsNotRequiredCompatibility(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("legacy-import", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "第一章\n\n导入项目保留的旧草稿。"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
	); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || inspection.Status != DraftExternalGateNotRequired {
		t.Fatalf("legacy/import draft lost NotRequired compatibility: inspection=%+v err=%v", inspection, err)
	}
	if err := RequireDraftExternalApprovalWithStore(st, 1); err != nil {
		t.Fatalf("legacy/import draft unexpectedly required managed DeepSeek gate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "meta", "pipeline.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy compatibility setup accidentally became pipeline-managed: %v", err)
	}
}
