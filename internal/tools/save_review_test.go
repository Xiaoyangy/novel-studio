package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSaveReviewPersistsContractAssessment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":           3,
		"scope":             "chapter",
		"dimensions":        []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"}, {"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"}, {"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"}, {"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"}, {"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"}, {"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"}, {"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"}, {"dimension": "ai_voice_detection", "score": 84, "verdict": "pass", "comment": "AI腔指标通过"}},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"contract_misses":   []string{"未明确埋下内门试炼邀请"},
		"contract_notes":    "主线推进达成，但 contract 中的第二个推进项没有落地。",
		"verdict":           "polish",
		"summary":           "本章基本完成目标，但 contract 仍有漏项。",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal output: %v", err)
	}
	if out["writing_feedback"] != "meta/writing_assets.md" {
		t.Fatalf("expected writing feedback path, got %+v", out)
	}
	if n, _ := out["writing_feedback_entries"].(float64); n == 0 {
		t.Fatalf("expected writing feedback entries, got %+v", out)
	}

	review, err := s.World.LoadReview(3)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review == nil {
		t.Fatal("expected review saved, got nil")
	}
	if review.ContractStatus != "partial" {
		t.Fatalf("unexpected contract status: %q", review.ContractStatus)
	}
	if len(review.ContractMisses) != 1 || review.ContractMisses[0] != "未明确埋下内门试炼邀请" {
		t.Fatalf("unexpected contract misses: %+v", review.ContractMisses)
	}
	if review.Dimension("aesthetic") == nil {
		t.Fatalf("expected aesthetic dimension persisted, got %+v", review.Dimensions)
	}
	lib, err := s.WritingAssets.Load()
	if err != nil {
		t.Fatalf("Load writing assets: %v", err)
	}
	if lib == nil || len(lib.Feedback) == 0 {
		t.Fatalf("expected review feedback sedimented into writing assets, got %+v", lib)
	}
}

func TestSaveReviewShortGlobalAcceptCompletesAndWritesManuscript(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("短篇测试", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "开端"},
		{Chapter: 2, Title: "收束"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	for ch, body := range map[int]string{1: "第一章正文。", 2: "第二章正文，完成收束。"} {
		if err := s.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter(%d): %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(body)), "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
		if err := s.World.SaveReview(domain.ReviewEntry{Chapter: ch, Scope: "chapter", Verdict: "accept"}); err != nil {
			t.Fatalf("SaveReview chapter %d: %v", ch, err)
		}
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         2,
		"scope":           "global",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"contract_notes":  "全书闭合。",
		"verdict":         "accept",
		"summary":         "短篇全文终审通过。",
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute global review: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["book_complete"] != true {
		t.Fatalf("expected book_complete=true, got %+v", out)
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("expected phase complete, got %s", progress.Phase)
	}
	manuscript := filepath.Join(dir, "正文.md")
	data, err := os.ReadFile(manuscript)
	if err != nil {
		t.Fatalf("Read merged manuscript: %v", err)
	}
	text := string(data)
	for _, want := range []string{"# 短篇测试", "## 第 1 章 开端", "第一章正文。", "## 第 2 章 收束", "第二章正文，完成收束。"} {
		if !strings.Contains(text, want) {
			t.Fatalf("merged manuscript missing %q:\n%s", want, text)
		}
	}
}

func TestSaveReviewNonShortFinalChapterAcceptCompletesWithoutMergedManuscript(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("中篇测试", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierMid); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}
	for ch := 1; ch <= 2; ch++ {
		body := strings.Repeat("正文。", 5000)
		if err := s.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter(%d): %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(body)), "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("SaveReview chapter 1: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         2,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"contract_notes":  "末章完成。",
		"verdict":         "accept",
		"summary":         "末章章级审阅通过。",
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute chapter review: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["book_complete"] != true {
		t.Fatalf("expected book_complete=true, got %+v", out)
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("expected phase complete, got %s", progress.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "正文.md")); !os.IsNotExist(err) {
		t.Fatalf("non-short project should not write merged manuscript, stat err=%v", err)
	}
}

func TestSaveReviewRejectsMissingDimensions(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"}},
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimensions must contain exactly") {
		t.Fatalf("expected dimensions validation error, got %v", err)
	}
}

func TestSaveReviewRejectsDimensionWithoutComment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "基本一致"},
			{"dimension": "character", "score": 82, "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78},
			{"dimension": "continuity", "score": 84, "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "comment": "正常"},
			{"dimension": "hook", "score": 76, "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "comment": "语言基本成立"},
			{"dimension": "ai_voice_detection", "score": 84, "comment": "AI腔指标通过"},
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimension comment is required: pacing") {
		t.Fatalf("expected dimension comment validation error, got %v", err)
	}
}

func TestSaveReviewRejectsUnfinishedAffectedChapter(t *testing.T) {
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

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 58,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "基本一致"},
			{"dimension": "character", "score": 82, "comment": "人设稳定"},
			{"dimension": "pacing", "score": 58, "comment": "节奏需要重写"},
			{"dimension": "continuity", "score": 84, "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "comment": "正常"},
			{"dimension": "hook", "score": 76, "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "comment": "语言基本成立"},
			{"dimension": "ai_voice_detection", "score": 84, "comment": "AI腔指标通过"},
		},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"verdict":           "polish",
		"summary":           "需要打磨第 58 章，不能把未完成章节入队。",
		"affected_chapters": []int{65},
		"contract_misses":   []string{"节奏超出本章职责"},
		"contract_notes":    "应只处理已完成章节。",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "pending_rewrites 只能包含已完成章节") {
		t.Fatalf("expected unfinished affected chapter rejection, got %v", err)
	}
	review, err := s.World.LoadReview(58)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review != nil {
		t.Fatalf("review should not be saved when pending rewrite validation fails: %+v", review)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowWriting && p.Flow != "" {
		t.Fatalf("flow should not enter rewrite/polish, got %s", p.Flow)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

// TestSaveReviewDerivesVerdictFromScore 验证：verdict 由 score 确定性推导，模型给的
// 不一致 verdict（如 score=85 却填 warning）不再报错，而是被覆写成正确值（pass）。
// 防回归 issue：弱模型 score/verdict 打架曾导致 save_review 反复失败。
func TestSaveReviewDerivesVerdictFromScore(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "一致"},
			{"dimension": "character", "score": 82, "comment": "稳定"}, // 省略 verdict
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 85, "verdict": "warning", "comment": "语言成立"}, // 不一致：85 却填 warning
			{"dimension": "ai_voice_detection", "score": 84, "verdict": "pass", "comment": "AI腔指标通过"},
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute should succeed (verdict auto-derived), got %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	// 85 → pass（覆写模型给的 warning）；82 省略 → pass。
	if d := review.Dimension("aesthetic"); d == nil || d.Verdict != "pass" {
		t.Fatalf("aesthetic verdict should be derived to pass, got %+v", d)
	}
	if d := review.Dimension("character"); d == nil || d.Verdict != "pass" {
		t.Fatalf("character verdict should be derived to pass, got %+v", d)
	}
}

func TestSaveReviewWritesAIVoiceReportAndHistory(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	body := "卡莱尔问：“你怕吗？”\n\n她抬起手，又放下。“怕。”"
	if err := s.Drafts.SaveFinalChapter(3, body); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, len([]rune(body)), "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         3,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"verdict":         "accept",
		"summary":         "AI 腔专项通过。",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reportPath := filepath.Join(dir, "reviews", "03.md")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("expected unified review markdown: %v", err)
	}
	reportText := string(report)
	for _, want := range []string{"# 第003章 统一审核", "## AI 味信号", "## Editor 复审"} {
		if !strings.Contains(reportText, want) {
			t.Fatalf("unified review missing %q:\n%s", want, reportText)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "第003章_AI味审核.md")); !os.IsNotExist(err) {
		t.Fatalf("new review should not create legacy ai markdown, err=%v", err)
	}
	metrics, err := s.AIVoice.LoadChapterMetrics(3)
	if err != nil || metrics == nil {
		t.Fatalf("LoadChapterMetrics: %v", err)
	}
	foundEditorPoint := false
	for _, p := range metrics.AIVoiceScoreHistory {
		if p.Source == "editor" {
			foundEditorPoint = true
		}
	}
	if !foundEditorPoint {
		t.Fatalf("expected editor score history, got %+v", metrics.AIVoiceScoreHistory)
	}
}

func TestSaveReviewAIVoiceCatalogStuffingEscalatesToRewrite(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	body := "江烬没接话。他把短租庇护收据、保护费凭证、1602的物资押金条分成三叠。退烧贴的胶粘在文件袋里，撕不下来；蓝皮欠条被灰水泡软，边上还沾着半粒米。桌脚旁还散着竹柄雨伞、裂口搪瓷杯、旧台历夹、粉笔头、桦皮袖扣、蓼蓝布头、荞麦壳、陶埙裂片、绢纱穗、菖蒲根、贝母钮和紫铜铃舌。江烬只拨了三样入档，其余推到面单外。"
	if err := s.Drafts.SaveFinalChapter(3, body); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, len([]rune(body)), "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.AIVoice.SaveRedFlags(domain.AIVoiceAnalysis{
		Chapter: 3,
		Label:   "✅ 可通过",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 3, AIVoiceScore: 0.01},
	}); err != nil {
		t.Fatalf("Save stale redflags: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         3,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"verdict":         "accept",
		"summary":         "模型误判为可接受，但规则红旗必须接管。",
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["final_verdict"] != "rewrite" {
		t.Fatalf("expected final_verdict rewrite, got %+v", out)
	}
	reason := fmt.Sprint(out["escalation_reason"])
	if !strings.Contains(reason, "AI红旗硬门禁") || !strings.Contains(reason, "catalog_stuffing") {
		t.Fatalf("expected catalog stuffing escalation reason, got %+v", out)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowRewriting || len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 3 {
		t.Fatalf("expected chapter 3 queued for rewrite, got flow=%s pending=%v", p.Flow, p.PendingRewrites)
	}
	analysis, err := s.AIVoice.LoadRedFlags(3)
	if err != nil || analysis == nil {
		t.Fatalf("LoadRedFlags: %v", err)
	}
	if !reviewRedFlagExists(analysis.RedFlags, "catalog_stuffing") {
		t.Fatalf("expected recomputed catalog_stuffing redflag, got %+v", analysis.RedFlags)
	}
}

func TestSaveReviewChapterAcceptRefreshesProgressLedger(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("进度台账测试", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "江烬", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("Save characters: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "开端", CoreEvent: "主角拿到账单"},
		{Chapter: 2, Title: "推进", CoreEvent: "主角验证规则"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	body := "江烬把账单压在桌上。"
	if err := s.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:   1,
		Summary:   "江烬收到第一张账单。",
		KeyEvents: []string{"账单送达"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.World.AppendStateChanges([]domain.StateChange{{
		Chapter: 1, Entity: "江烬", Field: "目标", NewValue: "核验账单",
	}}); err != nil {
		t.Fatalf("AppendStateChanges: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, len([]rune(body)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         1,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"verdict":         "accept",
		"summary":         "章级审阅通过。",
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["chapter_progress"] != "meta/chapter_progress.md" {
		t.Fatalf("expected chapter_progress path, got %+v", out)
	}
	if out["project_progress"] != "meta/project_progress.md" {
		t.Fatalf("expected project_progress path, got %+v", out)
	}
	if out["evolution_report"] != "meta/evolution_report.md" {
		t.Fatalf("expected evolution_report path, got %+v", out)
	}
	ledger, err := s.LoadChapterProgressLedger()
	if err != nil || ledger == nil {
		t.Fatalf("LoadChapterProgressLedger: %v", err)
	}
	if ledger.NextPlan == nil || ledger.NextPlan.Chapter != 2 {
		t.Fatalf("expected next plan for chapter 2, got %+v", ledger.NextPlan)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "chapter_progress.md")); err != nil {
		t.Fatalf("expected progress markdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "project_progress.md")); err != nil {
		t.Fatalf("expected project progress markdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "evolution_report.md")); err != nil {
		t.Fatalf("expected evolution report markdown: %v", err)
	}
	if out["project_memory_rag_indexed"] != true {
		t.Fatalf("expected project memory RAG indexed, got %+v", out)
	}
	ragState, err := s.RAG.LoadIndexState()
	if err != nil || ragState == nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	for _, sourcePath := range []string{
		"meta/chapter_progress.md",
		"meta/character_continuity.md",
		"meta/project_progress.md",
		"meta/evolution_report.md",
		"outline.md",
	} {
		if !ragHasSourcePath(ragState, sourcePath) {
			t.Fatalf("expected RAG source %s in %+v", sourcePath, ragSourcePaths(ragState))
		}
	}
}

func ragHasSourcePath(state *domain.RAGIndexState, sourcePath string) bool {
	for _, chunk := range state.Chunks {
		if chunk.SourcePath == sourcePath {
			return true
		}
	}
	return false
}

func ragSourcePaths(state *domain.RAGIndexState) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, chunk := range state.Chunks {
		if _, ok := seen[chunk.SourcePath]; ok {
			continue
		}
		seen[chunk.SourcePath] = struct{}{}
		out = append(out, chunk.SourcePath)
	}
	return out
}

func TestSaveReviewEscalatesCriticalIssueEvenWhenVerdictAccept(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":         3,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"contract_status": "met",
		"issues": []map[string]any{
			{
				"type":        "continuity",
				"severity":    "critical",
				"description": "上一章已经销毁的欠费单在本章无解释复活。",
				"evidence":    "第2章销毁欠费单；第3章又直接拿出同一张。",
				"suggestion":  "重写相关场景，补出新欠费单来源或改用别的证据。",
			},
		},
		"verdict": "accept",
		"summary": "模型误判为可接受，但 issue 已经是 critical。",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["final_verdict"] != "rewrite" {
		t.Fatalf("expected final_verdict rewrite, got %+v", out)
	}
	if !strings.Contains(fmt.Sprint(out["escalation_reason"]), "critical issues") {
		t.Fatalf("expected critical issue escalation reason, got %+v", out)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowRewriting || len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 3 {
		t.Fatalf("expected chapter 3 queued for rewrite, got flow=%s pending=%v", p.Flow, p.PendingRewrites)
	}
}

func TestSaveReviewEscalatesNonCriticalDimensionFailToPolish(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	dimensions := acceptDimensions()
	for _, dim := range dimensions {
		if dim["dimension"] == "ai_voice_detection" {
			dim["score"] = 45
			dim["comment"] = "比喻密度超标且格言命中"
		}
	}

	tool := NewSaveReviewTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":           3,
		"scope":             "chapter",
		"dimensions":        dimensions,
		"contract_status":   "met",
		"issues":            []map[string]any{},
		"verdict":           "accept",
		"summary":           "模型误判放行，但 AI 腔维度失败。",
		"affected_chapters": []int{3},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["final_verdict"] != "polish" {
		t.Fatalf("expected final_verdict polish, got %+v", out)
	}
}

func TestSaveReviewEscalatesPolishToRewriteForCriticalDimensionFail(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	dimensions := acceptDimensions()
	for _, dim := range dimensions {
		if dim["dimension"] == "continuity" {
			dim["score"] = 45
			dim["comment"] = "关键连续性失败"
		}
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":           3,
		"scope":             "chapter",
		"dimensions":        dimensions,
		"contract_status":   "met",
		"issues":            []map[string]any{},
		"verdict":           "polish",
		"summary":           "模型只要求打磨，但关键维度失败。",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["final_verdict"] != "rewrite" {
		t.Fatalf("expected final_verdict rewrite, got %+v", out)
	}
	if !strings.Contains(fmt.Sprint(out["escalation_reason"]), "关键维度不合格") {
		t.Fatalf("expected scorecard escalation reason, got %+v", out)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowRewriting || len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 3 {
		t.Fatalf("expected chapter 3 queued for rewrite, got flow=%s pending=%v", p.Flow, p.PendingRewrites)
	}
}

func TestSaveReviewRejectsMissingAffectedChaptersForRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"},
			{"dimension": "ai_voice_detection", "score": 84, "verdict": "pass", "comment": "AI腔指标通过"},
		},
		"issues":  []map[string]any{},
		"verdict": "rewrite",
		"summary": "需要重写",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "affected_chapters is required") {
		t.Fatalf("expected affected_chapters validation error, got %v", err)
	}
}

func TestSaveReviewRejectsIssueWithoutEvidence(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"},
			{"dimension": "ai_voice_detection", "score": 84, "verdict": "pass", "comment": "AI腔指标通过"},
		},
		"issues": []map[string]any{
			{"type": "hook", "severity": "warning", "description": "章末钩子偏弱"},
		},
		"verdict":           "polish",
		"summary":           "需要补强钩子。",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "issue evidence is required") {
		t.Fatalf("expected issue evidence validation error, got %v", err)
	}
}

func acceptDimensions() []map[string]any {
	return []map[string]any{
		{"dimension": "consistency", "score": 86, "comment": "设定一致"},
		{"dimension": "character", "score": 86, "comment": "人设稳定"},
		{"dimension": "pacing", "score": 86, "comment": "节奏闭合"},
		{"dimension": "continuity", "score": 86, "comment": "叙事连贯"},
		{"dimension": "foreshadow", "score": 86, "comment": "伏笔回收"},
		{"dimension": "hook", "score": 86, "comment": "钩子完成"},
		{"dimension": "aesthetic", "score": 86, "comment": "原文具体，表达稳定"},
		{"dimension": "ai_voice_detection", "score": 86, "comment": "AI腔指标通过"},
	}
}

func reviewRedFlagExists(flags []domain.AIVoiceRedFlag, rule string) bool {
	for _, flag := range flags {
		if flag.Rule == rule {
			return true
		}
	}
	return false
}

func TestSaveReviewHistoryAndRegressionLoop(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("init progress: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhasePremise); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "crisis", "quest"); err != nil {
		t.Fatalf("mark complete: %v", err)
	}
	tool := NewSaveReviewTool(s)

	exec := func(verdict string) map[string]any {
		out, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
			`{"chapter":1,"scope":"chapter","dimensions":[{"dimension":"consistency","score":85,"verdict":"pass","comment":"ok"},{"dimension":"character","score":82,"verdict":"pass","comment":"ok"},{"dimension":"pacing","score":82,"verdict":"pass","comment":"ok"},{"dimension":"continuity","score":84,"verdict":"pass","comment":"ok"},{"dimension":"foreshadow","score":80,"verdict":"pass","comment":"ok"},{"dimension":"hook","score":81,"verdict":"pass","comment":"ok"},{"dimension":"aesthetic","score":81,"verdict":"pass","comment":"ok"},{"dimension":"ai_voice_detection","score":84,"verdict":"pass","comment":"ok"}],"verdict":%q,"summary":"第1轮问题：结尾钩子弱","issues":[{"severity":"major","description":"结尾钩子弱","evidence":"末段无悬置动作或未答问题"}],"affected_chapters":[1]}`, verdict)))
		if err != nil {
			t.Fatalf("save review: %v", err)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("result: %v", err)
		}
		return result
	}

	// 第 1 轮：无历史，round=1
	r1 := exec("rewrite")
	if r1["review_round"] != float64(1) {
		t.Fatalf("首轮 round 应为 1: %v", r1["review_round"])
	}

	// 复审语境：PendingRewrites 已清时，Writer/Editor 上下文应注入 previous_review
	_ = s.Progress.SetPendingRewrites(nil, "")
	ctxTool := NewContextTool(s, References{}, "default")
	seed := newChapterContextEnvelope()
	ctxTool.prepareChapterContext(1, &seed, func(string, error) {})
	if _, ok := seed.Working["previous_review"]; !ok {
		t.Fatalf("复审应注入 previous_review: %v", seed.Working["previous_review"])
	}
	if _, ok := seed.Working["previous_review_policy"]; !ok {
		t.Fatal("缺回归验证指引")
	}

	// 第 2 轮：上一轮被归档，round=2
	r2 := exec("rewrite")
	if r2["review_round"] != float64(2) {
		t.Fatalf("第 2 轮 round 应为 2: %v", r2["review_round"])
	}
	if history := s.World.LoadReviewHistory(1); len(history) != 1 {
		t.Fatalf("应归档 1 轮历史: %d", len(history))
	}

	// 第 3 轮仍 rewrite：出现循环刹车提示
	_ = s.Progress.SetPendingRewrites(nil, "")
	r3 := exec("rewrite")
	if r3["review_round"] != float64(3) {
		t.Fatalf("第 3 轮 round 应为 3: %v", r3["review_round"])
	}
	if _, ok := r3["review_round_note"]; !ok {
		t.Fatal("第 3 轮仍 rewrite 应透出循环刹车提示")
	}

	// accept 的旧结论不触发复审注入
	_ = s.Progress.SetPendingRewrites(nil, "")
	_ = exec("accept")
	seed2 := newChapterContextEnvelope()
	ctxTool.prepareChapterContext(1, &seed2, func(string, error) {})
	if _, ok := seed2.Working["previous_review"]; ok {
		t.Fatal("上轮已 accept 不应再注入复审块")
	}
}
