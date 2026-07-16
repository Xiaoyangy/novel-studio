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
	agentcoretools "github.com/voocel/agentcore/tools"
)

// TestEditChapterAppliesEdit 正常路径：drafts 已有内容，唯一匹配替换成功。
func TestEditChapterAppliesEdit(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "他握紧了拳头，指节发白。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "指节发白",
		"new_string": "指节泛起青白",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := s.Drafts.LoadDraft(2)
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	if !strings.Contains(got, "指节泛起青白") {
		t.Fatalf("expected draft to contain new text, got %q", got)
	}
	if strings.Contains(got, "指节发白") {
		t.Fatalf("old text should be replaced, got %q", got)
	}
}

// TestEditChapterSeedsFromFinalChapter drafts 不存在但 chapters 有 → 自动从 chapters 播种。
func TestEditChapterSeedsFromFinalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// 模拟第 3 章已提交且进入打磨队列
	original := "风从窗缝里钻进来，带着潮湿的泥土气味。"
	if err := s.Drafts.SaveFinalChapter(3, original); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{3}, "测试打磨"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    3,
		"old_string": "潮湿的泥土气味",
		"new_string": "泥土和铁锈混杂的气味",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// drafts 应被播种且包含新文本
	draft, err := s.Drafts.LoadDraft(3)
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	if !strings.Contains(draft, "泥土和铁锈混杂的气味") {
		t.Fatalf("expected draft seeded + edited, got %q", draft)
	}

	// chapters 保持原样（edit_chapter 不碰终稿）
	final, err := s.Drafts.LoadChapterText(3)
	if err != nil {
		t.Fatalf("LoadChapterText: %v", err)
	}
	if final != original {
		t.Fatalf("final chapter must stay untouched, got %q", final)
	}
}

// TestEditChapterRejectsCompletedWithoutQueue 已完成且不在重写队列中 → 拒绝。
func TestEditChapterRejectsCompletedWithoutQueue(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	original := "第二章原始正文。"
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "原始正文",
		"new_string": "篡改内容",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected rejection for completed chapter not in PendingRewrites")
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("expected ErrToolPrecondition, got %v", err)
	}
}

func TestEditChapterRejectsBlockingExternalFullRerender(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "旧草稿仍按流程逐项推进。"); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256("旧草稿仍按流程逐项推进。"), AIProbabilityPercent: 17, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"整章重排"},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "逐项推进", "new_string": "换一种推进",
	})
	_, err := NewEditChapterTool(s).Execute(context.Background(), args)
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "整章覆盖") {
		t.Fatalf("expected full-rerender rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "旧草稿仍按流程逐项推进。" {
		t.Fatalf("blocking edit changed draft: %q", draft)
	}
}

func TestEditChapterApprovedHashAllowsOneEditThenRequiresExternalRejudge(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	content := "林澈把价牌放稳，沈知遥站在一旁看着。"
	if err := s.Drafts.SaveDraft(1, content); err != nil {
		t.Fatal(err)
	}
	reviewDir := filepath.Join(s.Dir(), "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	if err := os.WriteFile(filepath.Join(reviewDir, "01_deepseek_ai_judge.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "放稳", "new_string": "扶稳",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("first edit of approved hash should be allowed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["external_rejudge_required"] != true || !strings.Contains(payload["next_step"].(string), "立即停止") {
		t.Fatalf("edit must force immediate rejudge: %+v", payload)
	}

	args, _ = json.Marshal(map[string]any{
		"chapter": 1, "old_string": "一旁", "new_string": "旁边",
	})
	_, err = tool.Execute(context.Background(), args)
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "尚未获得 DeepSeek provider judge 结论") {
		t.Fatalf("second edit before rejudge must be blocked: %v", err)
	}
}

func TestDraftExternalGateEditPreconditionAllowsOnlyLocalSoftPending(t *testing.T) {
	soft := DraftExternalGateInspection{
		Status:               DraftExternalGateRejudgePending,
		LocalSoftEditPending: true,
	}
	if err := draftExternalGateEditPrecondition(1, soft); err != nil {
		t.Fatalf("DeepSeek-passing local soft gate should permit one edit: %v", err)
	}
	if err := draftExternalGateEditPrecondition(1, DraftExternalGateInspection{Status: DraftExternalGateRejudgePending}); err == nil || !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("ordinary unjudged hash must remain fail-closed for edit: %v", err)
	}
}

func TestEditChapterCrashAfterMarkdownReplaceRecoversExactEditCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	prior := "第一章 县城试点\n\n林澈把价牌放稳，才抬头看向门口。"
	if err := s.Drafts.SaveDraft(1, prior); err != nil {
		t.Fatal(err)
	}

	tool := NewEditChapterTool(s)
	tool.afterDraftWrite = func() error { return errors.New("simulated process exit") }
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "放稳", "new_string": "扶稳",
	})
	// Real Host calls may carry a cwd override. The isolated candidate planner
	// must still edit only its temp copy before the intent becomes durable.
	ctx := agentcoretools.WithCwd(context.Background(), dir)
	if _, err := tool.Execute(ctx, args); err == nil || !strings.Contains(err.Error(), "simulated process exit") {
		t.Fatalf("expected injected crash after Markdown replace, got %v", err)
	}
	candidate, err := s.Drafts.LoadDraft(1)
	if err != nil || !strings.Contains(candidate, "扶稳") {
		t.Fatalf("candidate bytes did not reach disk before crash: body=%q err=%v", candidate, err)
	}
	if _, err := os.Stat(draftWriteIntentPath(dir, 1)); err != nil {
		t.Fatalf("edit crash did not retain write intent: %v", err)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "edit"); cp != nil {
		t.Fatalf("fixture crossed checkpoint boundary before crash: %+v", cp)
	}

	// A fresh process first inspects the external gate. That inspection must
	// finish the edit saga in the same Store cache used by subsequent tools.
	recovered := store.NewStore(dir)
	if _, err := InspectDraftExternalGateWithStore(recovered, 1); err != nil {
		t.Fatalf("recover edit intent through gate inspection: %v", err)
	}
	checkpoint, err := CurrentChapterBodyCheckpoint(recovered, 1)
	if err != nil {
		t.Fatalf("recovered edit remained unbound: %v", err)
	}
	if checkpoint.Step != "edit" || checkpoint.Digest != "sha256:"+reviewreport.BodySHA256(candidate) {
		t.Fatalf("recovery emitted wrong body checkpoint: %+v", checkpoint)
	}
	if _, err := os.Stat(draftWriteIntentPath(dir, 1)); !os.IsNotExist(err) {
		t.Fatalf("recovered edit intent was not cleared: %v", err)
	}
	checkArgs, _ := json.Marshal(map[string]any{"chapter": 1})
	if _, err := NewCheckConsistencyTool(recovered).Execute(context.Background(), checkArgs); err != nil {
		t.Fatalf("recovered edit still deadlocked consistency: %v", err)
	}
}

func TestEditChapterRejectsApprovedHashAfterStructuralBudgetExhaustion(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	content := "第一章 县城试点\n\n林澈把价牌放稳，沈知遥站在一旁看着。"
	if err := s.Drafts.SaveDraft(1, content); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"sha256:2222222222222222222222222222222222222222222222222222222222222222",
		"sha256:3333333333333333333333333333333333333333333333333333333333333333",
	} {
		if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md", digest); err != nil {
			t.Fatal(err)
		}
	}
	reviewDir := filepath.Join(s.Dir(), "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(content), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	if err := os.WriteFile(filepath.Join(reviewDir, "01_deepseek_ai_judge.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "放稳", "new_string": "扶稳",
	})
	_, err := NewEditChapterTool(s).Execute(context.Background(), args)
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "绕过重规划") {
		t.Fatalf("edit bypassed exhausted structural budget: %v", err)
	}
	if got, _ := s.Drafts.LoadDraft(1); got != content {
		t.Fatalf("rejected edit changed current draft: %q", got)
	}
}

func TestEditChapterSurfacesNewWholeTextStructuralBoundary(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章 县城试点\n\n待替换短段。"); err != nil {
		t.Fatal(err)
	}
	replacement := strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "待替换短段。", "new_string": replacement,
	})
	result, err := NewEditChapterTool(s).Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["local_structural_rerender_required"] != true || payload["stop_prose_modification"] != true {
		t.Fatalf("edit-created whole-text block was not surfaced as a stop boundary: %#v", payload)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft-structural-block"); cp == nil {
		t.Fatal("edit-created whole-text block has no structural checkpoint")
	}
	inspection, err := InspectDraftExternalGateWithStore(s, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized {
		t.Fatalf("edit-created whole-text block did not authorize bounded full rerender: inspection=%+v err=%v", inspection, err)
	}
}

// TestEditChapterRejectsAmbiguousMatch 多处匹配且未开 replace_all → 报错。
func TestEditChapterRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "他笑了。她也笑了。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "笑了",
		"new_string": "沉默了",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected rejection for ambiguous match")
	}
}

// TestEditChapterReplaceAll replace_all=true 时所有匹配均被替换。
func TestEditChapterReplaceAll(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "他笑了。她也笑了。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":     2,
		"old_string":  "笑了",
		"new_string":  "沉默了",
		"replace_all": true,
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := s.Drafts.LoadDraft(2)
	if strings.Contains(got, "笑了") {
		t.Fatalf("all occurrences should be replaced, got %q", got)
	}
	if strings.Count(got, "沉默了") != 2 {
		t.Fatalf("expected 2 replacements, got %q", got)
	}
}

// TestEditChapterRejectsEmptyOldString 空 old_string → 参数非法。
func TestEditChapterRejectsEmptyOldString(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "",
		"new_string": "xxx",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected rejection for empty old_string")
	}
	if !errors.Is(err, errs.ErrToolArgs) {
		t.Fatalf("expected ErrToolArgs, got %v", err)
	}
}

// TestEditChapterRejectsNoDraftNoFinal drafts 与 chapters 都不存在 → 报错提示先 draft_chapter。
func TestEditChapterRejectsNoDraftNoFinal(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    5,
		"old_string": "任何",
		"new_string": "替换",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected rejection when neither draft nor chapter exists")
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("expected ErrToolPrecondition, got %v", err)
	}
}

// TestEditChapterWorksWithCommitValidation 整条链路：edit_chapter → commit_chapter 成功 drain 队列。
// 验证新工具与 commit_chapter 的 drafts≠chapters 硬校验配合良好。
func TestEditChapterWorksWithCommitValidation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	original := "风从窗缝里钻进来，带着潮湿的泥土气味。"
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "打磨"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	editTool := NewEditChapterTool(s)
	editArgs, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "潮湿的泥土气味",
		"new_string": "泥土和铁锈混杂的气味",
	})
	if _, err := editTool.Execute(context.Background(), editArgs); err != nil {
		t.Fatalf("edit_chapter: %v", err)
	}
	checkArgs, _ := json.Marshal(map[string]any{"chapter": 2})
	if _, err := NewCheckConsistencyTool(s).Execute(context.Background(), checkArgs); err != nil {
		t.Fatalf("check_consistency after edit: %v", err)
	}

	commitTool := NewCommitChapterTool(s)
	commitArgs, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "打磨后摘要",
		"characters": []string{"主角"},
		"key_events": []string{"完成打磨"},
	})
	if _, err := commitTool.Execute(context.Background(), commitArgs); err != nil {
		t.Fatalf("commit_chapter after edit: %v", err)
	}

	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("expected queue drained, got %v", progress.PendingRewrites)
	}
}
