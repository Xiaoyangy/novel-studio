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

func TestDraftChapterRejectsUnfinishedPendingRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 80); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for ch := 1; ch <= 58; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}

	p, _ := s.Progress.Load()
	p.Flow = domain.FlowPolishing
	p.PendingRewrites = []int{65}
	if err := s.Progress.Save(p); err != nil {
		t.Fatalf("Save corrupt progress: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 65,
		"content": "错误写入未来章节。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "pending_rewrites 只能包含已完成章节") {
		t.Fatalf("expected invalid pending_rewrites rejection, got %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.InProgressChapter == 65 {
		t.Fatalf("future chapter should not become in progress")
	}
}

func TestDraftChapterRejectsPendingRewriteWithoutPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	oldBody := "第一章\n\n旧正文。"
	if err := s.Drafts.SaveFinalChapter(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, len([]rune(oldBody)), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewritesAndFlow([]int{1}, "必须重做推演和计划", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "pipeline.json"), []byte(`{"stages":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一章\n\n试图绕过计划直接覆盖的新正文。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "缺少计划") {
		t.Fatalf("pending rewrite without plan bypassed prewrite gates: %v", err)
	}
	if draft, err := s.Drafts.LoadDraft(1); err != nil || strings.TrimSpace(draft) != "" {
		t.Fatalf("rejected no-plan rewrite wrote a draft: draft=%q err=%v", draft, err)
	}
}

func TestDraftNeedsConsistencyCheckAfterEdit(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "第一章\n\n初稿。"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "consistency_check", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if draftNeedsConsistencyCheck(st, 1) {
		t.Fatal("fresh consistency checkpoint did not clear the draft event")
	}
	if err := st.Drafts.SaveDraft(1, "第一章\n\n编辑后的正文。"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "edit", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if !draftNeedsConsistencyCheck(st, 1) {
		t.Fatal("edit after consistency was ignored as a new body event")
	}
}

func TestDraftChapterRejectsPipelineWritingWithoutPlan(t *testing.T) {
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
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "第一章\n\n试图跳过计划直接写正文。", "mode": "write",
	})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "缺少计划") {
		t.Fatalf("pipeline writing without plan bypassed prewrite gates: %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); strings.TrimSpace(draft) != "" {
		t.Fatalf("rejected no-plan pipeline write produced draft=%q", draft)
	}
}

func TestDraftChapterRejectsUnexpandedLayeredChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 5); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}, {
			Index:             2,
			Title:             "第二弧",
			EstimatedChapters: 3,
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"content": "越界正文。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "expand_arc") {
		t.Fatalf("expected unexpanded chapter rejection, got %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.InProgressChapter == 3 {
		t.Fatalf("unexpanded chapter should not become in progress")
	}
}

func TestDraftChapterRejectsConsecutiveDraftWithoutConsistencyCheck(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	tool := NewDraftChapterTool(s)
	first, err := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一版草稿。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal first: %v", err)
	}
	if _, err := tool.Execute(context.Background(), first); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	second, err := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第二版草稿。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal second: %v", err)
	}
	if _, err := tool.Execute(context.Background(), second); err == nil || !strings.Contains(err.Error(), "禁止连续 draft_chapter") {
		t.Fatalf("expected consecutive draft rejection, got %v", err)
	}
}

func TestDraftChapterConsumesExactlyOneExternalRerenderToken(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	oldDraft := "第一章\n\n旧草稿仍然像流程报告。"
	if err := s.Drafts.SaveDraft(1, oldDraft); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(oldDraft), AIProbabilityPercent: 17, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"重排人物体验和页面结果"},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "第一章\n\n这是从人物选择重新组织的整章正文。", "mode": "write",
	})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(s.Dir(), 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.Requirement == nil {
		t.Fatalf("gate after full write = %+v, err=%v", inspection, err)
	}
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "必须先运行外部草稿复判") {
		t.Fatalf("expected second rerender rejection, got %v", err)
	}
}

func TestDraftChapterExplicitRerenderSupersedesPendingJudgeHash(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "第一章\n\n第一份完整候选正文。", "mode": "write",
	})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "第一章\n\n人工已否掉旧候选，这是一份不同的新正文。", "mode": "write",
	})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), second); err != nil {
		t.Fatalf("newer explicit rerender request should allow replacing unjudged old draft: %v", err)
	}
	inspection, err := InspectDraftExternalGateWithStore(s, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending {
		t.Fatalf("clean explicit replacement lost its durable pending-judge boundary: inspection=%+v err=%v", inspection, err)
	}
	if err := RequireDraftExternalApprovalWithStore(s, 1); err == nil || !strings.Contains(err.Error(), "尚未完成该哈希") {
		t.Fatalf("clean explicit replacement became committable before whole-draft rejudge: %v", err)
	}
}

func TestDraftChapterExplicitRerenderRejectsUnchangedCurrentHash(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	body := "第一章\n\n人工明确要求重渲染前保留的完整候选正文。"
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": body, "mode": "write"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "当前草稿哈希相同") {
		t.Fatalf("explicit rerender accepted unchanged current bytes: %v", err)
	}
}

func TestDraftChapterExplicitRerenderRejectsAppend(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章\n\n旧候选正文。"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": "只追加一小段。", "mode": "append"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "append 不能消费") {
		t.Fatalf("explicit full rerender was consumed by append: %v", err)
	}
}

func TestDraftLocalStructuralBlockAuthorizesOnlyBoundedFullRerenders(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	firstBody := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	first, _ := json.Marshal(map[string]any{"chapter": 1, "content": firstBody, "mode": "write"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if !draftNeedsConsistencyCheck(s, 1) {
		t.Fatal("new structural draft must still require consistency check")
	}
	secondBody := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到第二家。", 100)
	second, _ := json.Marshal(map[string]any{"chapter": 1, "content": secondBody, "mode": "write"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), second); err != nil {
		t.Fatalf("fresh local whole-text evidence should authorize the second full render: %v", err)
	}
	thirdBody := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到第三家。", 100)
	third, _ := json.Marshal(map[string]any{"chapter": 1, "content": thirdBody, "mode": "write"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), third); err != nil {
		t.Fatalf("current causal budget should allow its final bounded full render: %v", err)
	}
	if escalation := InspectRenderOnlyReplanEscalation(s, 1); !escalation.Required {
		t.Fatalf("three distinct local structural failures must exhaust the causal render budget: %+v", escalation)
	}
	fourthBody := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到第四家。", 100)
	fourth, _ := json.Marshal(map[string]any{"chapter": 1, "content": fourthBody, "mode": "write"})
	if _, err := NewDraftChapterTool(s).Execute(context.Background(), fourth); err == nil || !strings.Contains(err.Error(), "render-only") {
		t.Fatalf("exhausted local render budget must force causal replanning: %v", err)
	}
	structuralAttempts := 0
	for _, cp := range s.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "draft-structural-block" {
			structuralAttempts++
		}
	}
	if structuralAttempts != 3 {
		t.Fatalf("distinct blocked bodies should consume exactly three attempts, got %d", structuralAttempts)
	}
}

func TestDraftChapterAppendCannotClearExternalRerenderRequirement(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	oldDraft := "第一章\n\n旧草稿。"
	if err := s.Drafts.SaveDraft(1, oldDraft); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(oldDraft), AIProbabilityPercent: 17, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"整章重排"},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "只补一段。", "mode": "append",
	})
	_, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "append 不能解除") {
		t.Fatalf("expected append rejection, got %v", err)
	}
}

func TestDraftChapterRejectsOperationalErrorReportAsProse(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "未能提交。\n\n阻塞原因：当前工作区为只读，Qdrant 不可用，本会话未挂载 draft_chapter。",
		"mode":    "write",
	})
	_, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "不能写入小说草稿") {
		t.Fatalf("expected operational-report rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "" {
		t.Fatalf("operational report persisted: %q", draft)
	}
}

func TestDraftChapterRejectsASCIIChineseDialogueQuoteBeforeWrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一章\n\n江烬问：\"明早八点还来吗？\"",
		"mode":    "write",
	})
	_, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), rules.ASCIIChineseDialogueQuoteRule) {
		t.Fatalf("expected ASCII Chinese dialogue quote rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "" {
		t.Fatalf("rejected ASCII-quoted dialogue persisted: %q", draft)
	}
	if err := validateDraftProsePayload("第一章\n\n所谓\"只能花在青山县\"的规则，还要结合受益对象理解。"); err != nil {
		t.Fatalf("ordinary quoted concept should not be rejected: %v", err)
	}
}

func TestDraftChapterRejectsGenericSystemRefusalAndAphoristicSummary(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	content := "第一章\n\n理由一条比一条正经，只有钥匙揣进谁兜里被他漏了。\n\n【不是，哥们，这笔不能这么花。】"
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": content, "mode": "write"})
	_, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "高置信正文模板") {
		t.Fatalf("expected high-confidence prose-template rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "" {
		t.Fatalf("rejected template persisted: %q", draft)
	}
}

func TestDraftChapterExternalRerenderKeepsOldDraftWhenCandidateTooShort(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 100, Max: 200},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "旧草稿正文应当保留。"); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256("旧草稿正文应当保留。"), AIProbabilityPercent: 17, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"整章重排"},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": "太短。", "mode": "write",
	})
	_, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "一次提交完整小说正文") {
		t.Fatalf("expected completeness rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "旧草稿正文应当保留。" {
		t.Fatalf("short candidate overwrote old draft: %q", draft)
	}
	if required, _ := DraftExternalRerenderRequired(s.Dir(), 1); !required {
		t.Fatal("short candidate cleared full-rerender requirement")
	}
}

func TestDraftChapterPersistsButFlagsWordContractFailure(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 10, Max: 20},
	}}); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("长", 25)
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": content, "mode": "write"})
	raw, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["hard_gate_passed"] != false || !strings.Contains(result["next_step"].(string), "edit_chapter") {
		t.Fatalf("expected hard gate guidance, got %s", raw)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != content {
		t.Fatalf("out-of-range draft should remain editable, got %q", draft)
	}
}

func TestValidateDraftChapterHeadingRequiresPlanTitleAndChapterNumber(t *testing.T) {
	plan := domain.ChapterPlan{Chapter: 2, Title: "皮卡一到，五个摊主点头了"}
	if err := validateDraftChapterHeading(plan, "皮卡一到，五个摊主点头了\n\n正文"); err == nil {
		t.Fatal("heading without chapter number must be rejected")
	}
	for _, heading := range []string{"第2章 皮卡一到，五个摊主点头了", "第二章 皮卡一到，五个摊主点头了"} {
		if err := validateDraftChapterHeading(plan, heading+"\n\n正文"); err != nil {
			t.Fatalf("valid heading %q rejected: %v", heading, err)
		}
	}
}

func TestValidateDraftWorldVisibilityRejectsOffscreenNamedCharacter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 2, SimulationID: "sim-2", TimeWindow: "当天",
		CharacterDecisions: []domain.CharacterWorldDecision{
			{Character: "林澈", VisibleToPOV: true},
			{Character: "梁广财", VisibleToPOV: false},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftWorldVisibility(s, 2, "第2章 标题\n\n梁广财端来两盘货，催着老丁改牌。"); err == nil || !strings.Contains(err.Error(), "梁广财") {
		t.Fatalf("offscreen role drift should be rejected, got %v", err)
	}
	if err := validateDraftWorldVisibility(s, 2, "第2章 标题\n\n林澈端来两盘货，催着老丁改牌。"); err != nil {
		t.Fatalf("visible protagonist rejected: %v", err)
	}
}

func TestDraftChapterRejectsSecondAlgorithmCrossProjectContamination(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("她的第二算法", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "许闻溪", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一章\n\n江烬收到欠费单，鬼城的门牌亮了一下。",
		"mode":    "write",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "跨项目污染") {
		t.Fatalf("expected cross-project contamination rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "" {
		t.Fatalf("contaminated draft must not persist: %s", draft)
	}
}
