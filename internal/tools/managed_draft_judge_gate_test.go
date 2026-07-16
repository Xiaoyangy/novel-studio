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
