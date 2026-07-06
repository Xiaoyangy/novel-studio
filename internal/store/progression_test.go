package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRefreshChapterProgressLedgerBuildsReadableHandoff(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("测试长篇", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "江烬", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("Save characters: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "午夜欠费单", CoreEvent: "江烬收到首张欠费单", Scenes: []string{"冥雾降临"}},
		{Chapter: 2, Title: "七分钟宽限", CoreEvent: "江烬验证代缴确认规则", Scenes: []string{"收租鬼上门"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "江烬在冥雾中确认阳间支付失效，收到1704欠费单。",
		Characters: []string{"江烬"},
		KeyEvents:  []string{"冥府黑卡激活", "1704欠费单送达"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.World.SaveTimeline([]domain.TimelineEvent{{
		Chapter: 1, Time: "00:11", Event: "1704收到夜租欠费单", Characters: []string{"江烬"},
	}}); err != nil {
		t.Fatalf("SaveTimeline: %v", err)
	}
	if err := s.World.AppendStateChanges([]domain.StateChange{{
		Chapter: 1, Entity: "江烬", Field: "持有物", NewValue: "冥府黑卡激活", Reason: "冥雾法域触发",
	}}); err != nil {
		t.Fatalf("AppendStateChanges: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3200, "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", Summary: "章级审阅通过"}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	ledger, err := s.RefreshChapterProgressLedger(1, nil)
	if err != nil {
		t.Fatalf("RefreshChapterProgressLedger: %v", err)
	}
	if ledger.Protagonist != "江烬" {
		t.Fatalf("expected protagonist 江烬, got %q", ledger.Protagonist)
	}
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].ProtagonistChanges) != 1 {
		t.Fatalf("expected protagonist change entry, got %+v", ledger.Entries)
	}
	if ledger.NextPlan == nil || ledger.NextPlan.Chapter != 2 || !strings.Contains(ledger.NextPlan.CoreEvent, "代缴确认") {
		t.Fatalf("unexpected next plan: %+v", ledger.NextPlan)
	}
	if len(ledger.NextPlan.CharacterContinuity) == 0 {
		t.Fatalf("expected character continuity hints in next plan")
	}
	if !strings.Contains(ledger.NextPlan.PlanningInstructions[len(ledger.NextPlan.PlanningInstructions)-1], "不合适本章") {
		t.Fatalf("expected non-gate character planning instruction, got %+v", ledger.NextPlan.PlanningInstructions)
	}
	md, err := os.ReadFile(filepath.Join(dir, "meta", "chapter_progress.md"))
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	for _, want := range []string{"章节推进与人物变化台账", "江烬", "下一章动态计划", "七分钟宽限", "人物续用参考（非审核项）"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("markdown missing %q:\n%s", want, string(md))
		}
	}
	charMD, err := os.ReadFile(filepath.Join(dir, "meta", "character_continuity.md"))
	if err != nil {
		t.Fatalf("read character continuity markdown: %v", err)
	}
	for _, want := range []string{"人物回归与续用规划台账", "不作为章级审阅通过/失败条件", "江烬"} {
		if !strings.Contains(string(charMD), want) {
			t.Fatalf("character continuity markdown missing %q:\n%s", want, string(charMD))
		}
	}
	charLedger, err := s.LoadCharacterContinuityLedger()
	if err != nil || charLedger == nil {
		t.Fatalf("LoadCharacterContinuityLedger: %v", err)
	}
	if len(charLedger.Entries) == 0 || charLedger.Entries[0].Dynamics.CurrentGoal == "" {
		t.Fatalf("expected character dynamics profile, got %+v", charLedger)
	}
	dynamics := charLedger.Entries[0].Dynamics
	if len(dynamics.KnowledgeLedger.ForbiddenKnowledge) == 0 ||
		dynamics.DecisionFrame.MinimumEvidenceRequired == "" ||
		dynamics.EmotionAppraisal.CopingStrategy == "" ||
		dynamics.ArcAxis.Need == "" {
		t.Fatalf("expected expanded character dynamics fields, got %+v", dynamics)
	}
	if len(charLedger.Entries[0].ConsistencyChecks) == 0 {
		t.Fatalf("expected behavior consistency checks, got %+v", charLedger.Entries[0])
	}
	for _, want := range []string{"角色动力学", "当前目标", "知识账本", "决策框架", "情绪评价", "长期弧线轴", "行为一致性检查"} {
		if !strings.Contains(string(charMD), want) {
			t.Fatalf("character continuity markdown missing %q:\n%s", want, string(charMD))
		}
	}
	projectLedger, err := s.LoadProjectProgressLedger()
	if err != nil || projectLedger == nil {
		t.Fatalf("LoadProjectProgressLedger: %v", err)
	}
	if len(projectLedger.PromiseEntries) != 1 {
		t.Fatalf("expected project promise entry, got %+v", projectLedger)
	}
	if len(projectLedger.ProtagonistArc) != 2 {
		t.Fatalf("expected protagonist arc for completed and planned chapters, got %+v", projectLedger.ProtagonistArc)
	}
	if projectLedger.ProtagonistArc[0].Source != "actual" || projectLedger.ProtagonistArc[1].Source != "planned" {
		t.Fatalf("unexpected protagonist arc sources: %+v", projectLedger.ProtagonistArc)
	}
	projectMD, err := os.ReadFile(filepath.Join(dir, "meta", "project_progress.md"))
	if err != nil {
		t.Fatalf("read project progress markdown: %v", err)
	}
	for _, want := range []string{"项目级推进仪表盘", "主角变化路线图", "逐章承诺兑现", "下一步项目动作"} {
		if !strings.Contains(string(projectMD), want) {
			t.Fatalf("project progress markdown missing %q:\n%s", want, string(projectMD))
		}
	}
	evolutionReport, err := s.LoadEvolutionReport()
	if err != nil || evolutionReport == nil {
		t.Fatalf("LoadEvolutionReport: %v", err)
	}
	if evolutionReport.Health.Completed != 1 {
		t.Fatalf("unexpected evolution health: %+v", evolutionReport.Health)
	}
	evolutionMD, err := os.ReadFile(filepath.Join(dir, "meta", "evolution_report.md"))
	if err != nil {
		t.Fatalf("read evolution report markdown: %v", err)
	}
	for _, want := range []string{"自动进化报告", "护栏", "验证计划"} {
		if !strings.Contains(string(evolutionMD), want) {
			t.Fatalf("evolution report markdown missing %q:\n%s", want, string(evolutionMD))
		}
	}
}

func TestRefreshChapterProgressLedgerDerivesProtagonistChangeFromResources(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("测试长篇", 1); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "江烬", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("Save characters: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "产权小票", CoreEvent: "江烬取得便利店确认权"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter: 1,
		Summary: "江烬购买午夜便利店最低经营份额。",
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.ResourceLedger.MergeClaims(1, []domain.ResourceClaim{{
		ID:    "store-confirm-right",
		Name:  "午夜便利店收银台有限确认权",
		Owner: "江烬",
		Kind:  "permission",
	}}, nil); err != nil {
		t.Fatalf("MergeClaims: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "asset", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	ledger, err := s.RefreshChapterProgressLedger(1, nil)
	if err != nil {
		t.Fatalf("RefreshChapterProgressLedger: %v", err)
	}
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].ProtagonistChanges) != 1 {
		t.Fatalf("expected derived protagonist change, got %+v", ledger.Entries)
	}
	change := ledger.Entries[0].ProtagonistChanges[0]
	if !strings.Contains(change.Field, "派生") || !strings.Contains(change.NewValue, "收银台有限确认权") {
		t.Fatalf("unexpected derived change: %+v", change)
	}
	md, err := os.ReadFile(filepath.Join(dir, "meta", "chapter_progress.md"))
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(string(md), "本章资源/权限推进") {
		t.Fatalf("markdown missing derived protagonist change:\n%s", string(md))
	}
}

func TestRefreshCharacterContinuityLedgerTracksOptionalCameos(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("测试长篇", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "江烬", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("Save characters: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "登记", CoreEvent: "江烬登记客户"},
		{Chapter: 2, Title: "谈判", CoreEvent: "江烬谈判"},
		{Chapter: 3, Title: "后续", CoreEvent: "江烬处理新风险"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "江烬登记客户，老周在旁边维持秩序。",
		Characters: []string{"江烬", "老周"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.Cast.MergeAppearances(1, []string{"老周"}, []domain.CastIntro{{Name: "老周", BriefRole: "普通客户代表"}}, map[string]bool{"江烬": true}); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", Summary: "通过"}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	_, err := s.RefreshChapterProgressLedger(1, nil)
	if err != nil {
		t.Fatalf("RefreshChapterProgressLedger: %v", err)
	}
	charLedger, err := s.LoadCharacterContinuityLedger()
	if err != nil {
		t.Fatalf("LoadCharacterContinuityLedger: %v", err)
	}
	if charLedger == nil {
		t.Fatalf("expected character continuity ledger")
	}
	found := false
	for _, entry := range charLedger.Entries {
		if entry.Name == "老周" {
			found = true
			if entry.ReturnMode != "偶发露脸候选" {
				t.Fatalf("expected 老周 optional cameo candidate, got %+v", entry)
			}
			if !strings.Contains(entry.PlanningNote, "不强求") {
				t.Fatalf("expected non-forced planning note, got %q", entry.PlanningNote)
			}
		}
	}
	if !found {
		t.Fatalf("老周 missing from character continuity entries: %+v", charLedger.Entries)
	}
}

func TestRefreshCharacterContinuityLedgerInfersCastRoleAndTargetsOutlineUse(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("测试长篇", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "江烬", Role: "主角", Tier: "core"},
		{Name: "唐未晞", Role: "重要配角", Tier: "important"},
	}); err != nil {
		t.Fatalf("Save characters: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "封控", CoreEvent: "江烬确认七楼边界"},
		{Chapter: 2, Title: "探视牌", CoreEvent: "江烬整理客户名单；唐未晞确认北城已有多名学生失联；温梨远程核对七楼押金。"},
		{Chapter: 3, Title: "背叛", CoreEvent: "江烬处理观察位背叛"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "镇厄局北城行动队封控3栋，江烬拒绝交出完整客户名单。",
		Characters: []string{"江烬", "镇厄局北城行动队", "观察位少年"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.Cast.MergeAppearances(1, []string{"镇厄局北城行动队", "观察位少年"}, nil, map[string]bool{"江烬": true, "唐未晞": true}); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	_, err := s.RefreshChapterProgressLedger(1, nil)
	if err != nil {
		t.Fatalf("RefreshChapterProgressLedger: %v", err)
	}
	charLedger, err := s.LoadCharacterContinuityLedger()
	if err != nil {
		t.Fatalf("LoadCharacterContinuityLedger: %v", err)
	}
	if charLedger == nil {
		t.Fatalf("expected character continuity ledger")
	}
	var inferredRole string
	var listedRole string
	var tangAction string
	for _, entry := range charLedger.Entries {
		switch entry.Name {
		case "镇厄局北城行动队":
			inferredRole = entry.BriefRole
		case "观察位少年":
			listedRole = entry.BriefRole
		case "唐未晞":
			if len(entry.FutureUses) > 0 {
				tangAction = entry.FutureUses[0].Action
			}
		}
	}
	if !strings.Contains(inferredRole, "由第1章摘要推断") || !strings.Contains(inferredRole, "镇厄局北城行动队封控3栋") {
		t.Fatalf("expected inferred cast role from summary, got %q", inferredRole)
	}
	if !strings.Contains(listedRole, "由第1章出场名单推断") || !strings.Contains(listedRole, "江烬拒绝交出完整客户名单") {
		t.Fatalf("expected inferred cast role from character list, got %q", listedRole)
	}
	if !strings.Contains(tangAction, "唐未晞确认北城已有多名学生失联") {
		t.Fatalf("expected targeted 唐未晞 outline action, got %q", tangAction)
	}
	if strings.Contains(tangAction, "江烬整理客户名单") || strings.Contains(tangAction, "温梨远程核对") {
		t.Fatalf("expected outline action to omit unrelated clauses, got %q", tangAction)
	}
}
