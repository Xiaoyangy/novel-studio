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

func TestDraftChapterPartsMergeAndRead(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	partTool := NewDraftChapterPartTool(s)
	part1 := mustJSON(t, map[string]any{
		"chapter":     1,
		"part":        1,
		"total_parts": 2,
		"title":       "开场",
		"focus":       "建立冲突",
		"content":     "第一章 测试\n\n她先推开门，看见屏幕还亮着。",
	})
	if _, err := partTool.Execute(context.Background(), part1); err != nil {
		t.Fatalf("part1 Execute: %v", err)
	}

	readTool := NewReadChapterTool(s)
	indexRaw := mustJSON(t, map[string]any{"chapter": 1, "source": "draft_part"})
	indexResult, err := readTool.Execute(context.Background(), indexRaw)
	if err != nil {
		t.Fatalf("read part index: %v", err)
	}
	if !strings.Contains(string(indexResult), "part-01.md") {
		t.Fatalf("part index should mention part-01.md, got %s", indexResult)
	}

	partRaw := mustJSON(t, map[string]any{"chapter": 1, "source": "draft_part", "part": 1})
	partResult, err := readTool.Execute(context.Background(), partRaw)
	if err != nil {
		t.Fatalf("read part content: %v", err)
	}
	if !strings.Contains(string(partResult), "她先推开门") {
		t.Fatalf("part content missing, got %s", partResult)
	}

	mergeTool := NewMergeChapterPartsTool(s)
	missingResult, err := mergeTool.Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter":        1,
		"expected_parts": 2,
	}))
	if err != nil {
		t.Fatalf("merge with missing part should return structured result, got err %v", err)
	}
	if !strings.Contains(string(missingResult), `"merged":false`) || !strings.Contains(string(missingResult), `"missing":[2]`) {
		t.Fatalf("missing merge result = %s", missingResult)
	}

	part2 := mustJSON(t, map[string]any{
		"chapter":     1,
		"part":        2,
		"total_parts": 2,
		"title":       "收束",
		"focus":       "留下选择后果",
		"content":     "傅行简没有催她，只把椅子往后让了半寸。\n\n她在那半寸空隙里确认了自己的判断。",
	})
	if _, err := partTool.Execute(context.Background(), part2); err != nil {
		t.Fatalf("part2 Execute: %v", err)
	}

	mergedResult, err := mergeTool.Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter":        1,
		"expected_parts": 2,
	}))
	if err != nil {
		t.Fatalf("merge Execute: %v", err)
	}
	if !strings.Contains(string(mergedResult), `"merged":true`) {
		t.Fatalf("merged result = %s", mergedResult)
	}
	draft, err := s.Drafts.LoadDraft(1)
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	first := strings.Index(draft, "她先推开门")
	second := strings.Index(draft, "傅行简没有催她")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("draft should contain parts in order, got %q", draft)
	}
}

func TestMergeChapterPartsRejectsRepeatMergeBeforeConsistency(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	partTool := NewDraftChapterPartTool(s)
	for i, text := range []string{"第一章 测试\n\n第一片。", "第二片。"} {
		if _, err := partTool.Execute(context.Background(), mustJSON(t, map[string]any{
			"chapter":     1,
			"part":        i + 1,
			"total_parts": 2,
			"title":       "片段",
			"focus":       "测试",
			"content":     text,
		})); err != nil {
			t.Fatalf("part %d Execute: %v", i+1, err)
		}
	}
	mergeTool := NewMergeChapterPartsTool(s)
	args := mustJSON(t, map[string]any{"chapter": 1, "expected_parts": 2})
	if _, err := mergeTool.Execute(context.Background(), args); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if _, err := mergeTool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "尚未执行 check_consistency") {
		t.Fatalf("expected repeat merge rejection, got %v", err)
	}
}

func TestDraftChapterPartRejectsPipelineWritingWithoutPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "pipeline.json"), []byte(`{"stages":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args := mustJSON(t, map[string]any{
		"chapter": 1, "part": 1, "total_parts": 2,
		"title": "开场", "focus": "冲突", "content": "第一章\n\n试图跳过正式计划。",
	})
	if _, err := NewDraftChapterPartTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "缺少计划") {
		t.Fatalf("pipeline part write without plan bypassed prewrite gates: %v", err)
	}
	if index, _ := s.Drafts.LoadDraftPartIndex(1); index != nil {
		t.Fatalf("rejected no-plan part write created index: %+v", index)
	}
}

func TestDraftChapterPartsCannotBypassWholeDraftExternalState(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	body := "第一章\n\n当前整章草稿。"
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		AIProbabilityPercent: 79, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"整章重排"},
	}); err != nil {
		t.Fatal(err)
	}
	args := mustJSON(t, map[string]any{
		"chapter": 1, "part": 1, "total_parts": 2,
		"title": "旁路", "focus": "不应写入", "content": "试图用分片替代整章重渲染。",
	})
	if _, err := NewDraftChapterPartTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "分片不能替代") {
		t.Fatalf("draft parts bypassed whole-draft external state: %v", err)
	}
	if index, _ := s.Drafts.LoadDraftPartIndex(1); index != nil {
		t.Fatalf("rejected external-state part write created index: %+v", index)
	}
}

func TestMergeChapterPartsRunsWholeTextStructuralGate(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	content := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	partArgs := mustJSON(t, map[string]any{
		"chapter": 1, "part": 1, "total_parts": 1,
		"title": "整章", "focus": "结构门禁", "content": content,
	})
	if _, err := NewDraftChapterPartTool(s).Execute(context.Background(), partArgs); err != nil {
		t.Fatal(err)
	}
	result, err := NewMergeChapterPartsTool(s).Execute(context.Background(), mustJSON(t, map[string]any{
		"chapter": 1, "expected_parts": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["local_structural_rerender_required"] != true || payload["stop_prose_modification"] != true || payload["hard_gate_passed"] != false {
		t.Fatalf("merged whole-text block was not surfaced as a stop boundary: %#v", payload)
	}
	inspection, err := InspectDraftExternalGate(s.Dir(), 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || inspection.Requirement == nil || inspection.Requirement.Source != "local_mechanical_gate" {
		t.Fatalf("merged structural failure did not persist a full-rerender marker: inspection=%+v err=%v", inspection, err)
	}
	if _, err := NewDraftChapterPartTool(s).Execute(context.Background(), partArgs); err == nil || !strings.Contains(err.Error(), "分片不能替代") {
		t.Fatalf("post-merge local blocker remained writable through parts: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return raw
}
