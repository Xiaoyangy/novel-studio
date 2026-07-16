package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const editorCacheTestMarkdown = `# ch01 评审

## 总体评分：35 / 40
## 是否需要改写：否
## 一句话诊断：正文通过。`

type reviewCacheModel struct {
	response string
	calls    atomic.Int32
}

func (m *reviewCacheModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.response)},
	}}, nil
}

func (m *reviewCacheModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *reviewCacheModel) SupportsTools() bool { return false }

func (m *reviewCacheModel) callCount() int { return int(m.calls.Load()) }

func TestBuildEditorChapterReviewContextUsesResultLevelContract(t *testing.T) {
	st := store.NewStore(t.TempDir())
	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "失业饭桌",
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"系统显示一百万元额度；林澈用旧债等两至三个短动作验证边界。",
			"系统绑定一百万元额度。",
		}},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	context := buildEditorChapterReviewContext(st, 1)
	if strings.Contains(context, "两至三个短动作") {
		t.Fatalf("Editor context leaked process recipe: %s", context)
	}
	for _, want := range []string{"系统显示一百万元额度", "结果级要求", "逐项照抄 plan"} {
		if !strings.Contains(context, want) {
			t.Fatalf("Editor context missing %q: %s", want, context)
		}
	}
	if strings.Contains(context, "系统绑定一百万元额度") {
		t.Fatalf("Editor context kept duplicate staging language instead of the visible result: %s", context)
	}
}

func TestEditorReviewRequiredOutcomesDropsOnlyProcessRecipes(t *testing.T) {
	compound := "68000元取货款必须继续阻断；只准落地五摊；灯具材料680元、五金360元、老丁人工300元分别准确；往返43公里、油费86元、半日人工180元全部留痕；冷饮支架只允许唯一一次失败复测，不得增加第六套。"
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{RequiredBeats: []string{
		"系统显示一百万元额度；林澈用旧债等两至三个短动作验证边界；旧债测试被明确拒绝。",
		compound,
		"逐笔票据核查",
	}}}

	complete := toolspkg.RenderRequiredOutcomes(plan)
	if len(complete) == 0 || !strings.Contains(complete[0], "两至三个短动作") {
		t.Fatalf("Drafter-facing hard outcome was unexpectedly shortened: %#v", complete)
	}
	got := editorReviewRequiredOutcomes(plan)
	if len(got) != 2 || got[0] != "系统显示一百万元额度；旧债测试被明确拒绝" || got[1] != strings.TrimSuffix(compound, "。") {
		t.Fatalf("Editor result projection removed a hard result or kept a recipe: %#v", got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"68000元", "五摊", "680元", "360元", "300元", "43公里", "86元", "180元", "唯一一次失败复测",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Editor projection lost hard result %q: %s", want, joined)
		}
	}
	for _, recipe := range []string{"两至三个短动作", "逐笔票据核查"} {
		if strings.Contains(joined, recipe) {
			t.Fatalf("Editor projection kept process recipe %q: %s", recipe, joined)
		}
	}
}

func TestSanitizeEditorReviewRemovesMissingPayloadAndAbsentOptionalTrendClaims(t *testing.T) {
	st := store.NewStore(t.TempDir())
	plan := domain.ChapterPlan{
		Chapter: 1,
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……", CharacterCarrier: "赵航"}},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	body := "第一章 失业饭桌\n\n赵航替林澈挡了一句，饭桌安静下来。"
	analysis := domain.AIVoiceAnalysis{
		Chapter:    1,
		BodySHA256: reviewreport.BodySHA256(body),
		Label:      "✅ 可通过",
		Summary:    "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{
			Chapter: 1, ParagraphCount: 2, SentenceCount: 2,
			FigurativeDensity: 0.02, DialogueRatio: 0.31, ProtagonistWaver: true,
		},
	}
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		score, comment := 90, "通过"
		verdict := "pass"
		if name == "ai_voice_detection" {
			score = 70
			verdict = "warning"
			comment = "未读取red flag JSON，需补检。"
		}
		dimensions = append(dimensions, domain.DimensionScore{Dimension: name, Score: score, Verdict: verdict, Comment: comment})
	}
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "polish", Summary: "AI腔检测流程缺失，需补检。",
		Dimensions: dimensions,
		Issues: []domain.ConsistencyIssue{
			{Type: "aesthetic", Severity: "warning", Description: "未读取red flag JSON，需补检。"},
			{Type: "aesthetic", Severity: "warning", Description: "赵航热梗未完全落地：呱，功能仅完成50%。"},
		},
	}

	removed := sanitizeEditorReviewForProject(st, 1, body, analysis, &entry)
	if len(removed) != 2 || len(entry.Issues) != 0 {
		t.Fatalf("sanitized issues = %#v removed=%#v", entry.Issues, removed)
	}
	if entry.Verdict != "accept" || strings.Contains(entry.Summary, "需补检") {
		t.Fatalf("sanitized review did not recover accept: %+v", entry)
	}
	for _, dimension := range entry.Dimensions {
		if dimension.Dimension == "ai_voice_detection" && (dimension.Score < 90 || dimension.Verdict != "pass") {
			t.Fatalf("AI voice dimension not repaired from deterministic payload: %+v", dimension)
		}
	}
}

func TestReconcileWarningOnlyEditorReviewUsesIndependentSameHashGates(t *testing.T) {
	body := "第一章 失业饭桌\n\n林澈把事情办成了。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "polish",
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "可再打磨"}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "aesthetic", Score: 60, Verdict: "warning", Comment: "主观建议"},
			{Dimension: "consistency", Score: 90, Verdict: "pass"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{Chapter: 1, BodySHA256: hash}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "✅ 可通过", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true, Blocking: false,
	}

	if !reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("same-hash independent passing gates should reconcile warning-only Editor drift")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || entry.Dimensions[0].Score < 80 {
		t.Fatalf("reconciled entry = %+v", entry)
	}

	blocked := entry
	blocked.Verdict = "polish"
	blockedJudge := *judge
	blockedJudge.Blocking = true
	if reconcileWarningOnlyEditorReview(&blocked, hash, mechanical, analysis, &blockedJudge) {
		t.Fatal("blocking independent judge must never be overridden")
	}
}

func TestEditorAIVoicePayloadPassedAllowsNonblockingChapterFunctionAdvice(t *testing.T) {
	body := "第三章\n\n林澈把车停在桥头，改口问摊主愿不愿意先试一晚。"
	for _, severity := range []string{"info", "warning"} {
		t.Run(severity, func(t *testing.T) {
			analysis := domain.AIVoiceAnalysis{
				Chapter: 3, BodySHA256: reviewreport.BodySHA256(body), Label: "✅ 可通过",
				Summary:  "规则引擎未发现硬性 AI 腔红旗；记录 1 项非阻断跨章规划建议。",
				Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 3, ParagraphCount: 2, SentenceCount: 2},
				RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: severity}},
			}
			if !editorAIVoicePayloadPassed(3, body, analysis) {
				t.Fatalf("nonblocking planning advice severity=%q invalidated an otherwise clean exact-body payload", severity)
			}
		})
	}
}

func TestEditorSystemPromptKeepsCrossChapterAdviceNonblocking(t *testing.T) {
	for _, want := range []string{"chapter_function_repetition", "非阻断规划建议", "不得降低当前章任何维度评分", "不得写入当前章主要问题"} {
		if !strings.Contains(editorSystemPrompt, want) {
			t.Fatalf("embedded review-existing prompt missing cross-chapter boundary %q", want)
		}
	}
}

func TestReconcileWarningOnlyEditorReviewKeepsStructuralProseFailureBlocking(t *testing.T) {
	body := "第一章\n\n众人轮流把流程说完。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "dialogue_conveyor_overuse：对白传送带让场景像工作汇报。",
		}},
		Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: 75, Verdict: "warning"}},
	}
	mechanical := &reviewreport.MechanicalGatePayload{Chapter: 1, BodySHA256: hash}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "可通过", Summary: "未发现机械硬伤",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("external low score must not erase a structural prose failure")
	}
	if entry.Verdict != "rewrite" || len(entry.Issues) != 1 {
		t.Fatalf("structural warning was mutated: %+v", entry)
	}
}

func TestMechanicalHasStructuralProseWarning(t *testing.T) {
	if mechanicalHasStructuralProseWarning(nil) {
		t.Fatal("nil mechanical gate must not report a structural warning")
	}
	plain := &reviewreport.MechanicalGatePayload{RuleViolations: []rules.Violation{{
		Rule: "object_response_overuse", Severity: rules.SeverityWarning,
	}}}
	if mechanicalHasStructuralProseWarning(plain) {
		t.Fatal("ordinary style warning must not enter Editor structural calibration path")
	}
	plain.RuleViolations = append(plain.RuleViolations, rules.Violation{
		Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning,
	})
	if !mechanicalHasStructuralProseWarning(plain) {
		t.Fatal("structural warning should enter the narrow Editor calibration path")
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsExplicitlyClearedStructuralWarning(t *testing.T) {
	body := "第三章\n\n林澈把手机塞回口袋，先看了一眼桥口的人群。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 3, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "一句现场调侃略硬，但当前表达不伤害阅读体验。",
		}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass", Comment: "合同一致。"},
			{Dimension: "character", Score: 100, Verdict: "pass", Comment: "人物一致。"},
			{Dimension: "pacing", Score: 100, Verdict: "pass", Comment: "节奏通过。"},
			{Dimension: "continuity", Score: 100, Verdict: "pass", Comment: "连续性通过。"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass", Comment: "伏笔通过。"},
			{Dimension: "hook", Score: 100, Verdict: "pass", Comment: "钩子通过。"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass", Comment: "审美通过。"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "dialogue_conveyor_overuse 提示为 warning，原文已通过主视角停留有效打断，无需改写。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 3, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 2.57, BlendedAIGCPercent: 2.57,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(98), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{
			Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 1,
		}},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 3, BodySHA256: hash, Label: "需打磨", Summary: "命中 1 项 warning 级红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 3, ParagraphCount: 40, SentenceCount: 80},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 3, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
	}

	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("an explicitly inspected and effectively interrupted structural warning should not preempt consensus reconciliation")
	}
	if !reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("same-hash independent passing gates should reconcile the explicitly cleared warning")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 {
		t.Fatalf("reconciled entry = %+v", entry)
	}
	if !reviewreport.ApplyExternalCorroborationWithEditor(mechanical, deepSeekExternalAIJudge(judge), &entry) {
		t.Fatal("accepted exact-body Editor evidence should permit same-hash external calibration")
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("shared artifact gate disposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsChineseExactBodyClearance(t *testing.T) {
	body := "第一章\n\n饭桌上的快接话停在父亲推开的鱼盘旁。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "pacing", Severity: "warning",
			Description: "对白传送带在家庭饭桌快接话中成立，但本章不构成强制改写。",
		}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 90, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "物件回应延迟命中3次、对白传送带19个重叠窗口，均为警示级别；正文已有动作与沉默作为有效打断，且有物件延迟与缺席；不影响本单章阅读，无需改写。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 7.17, ZhuqueCompositePercent: 2.02, LegacyHeuristicPercent: 1.49,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 3},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 19},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 7.17},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "需打磨", Summary: "仅有 advisory warning。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 52, SentenceCount: 105},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}

	if !reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("exact-body Chinese clearance and passing external evidence should reconcile statistical warnings")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated ||
		reviewExistingAIGCGatePercent(mechanical.AIGCReport) >= deepseekAIJudgePassExclusive {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsCalibratedLocalFalsePositive(t *testing.T) {
	body := "第一章\n\n林澈回到青山县，先把眼前的事做完。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "rewrite",
		Issues:     []domain.ConsistencyIssue{{Type: "pacing", Severity: "warning", Description: "饭桌对白略平"}},
		Dimensions: []domain.DimensionScore{{Dimension: "pacing", Score: 90, Verdict: "pass"}},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 11.19, ZhuqueCompositePercent: 3.36, LegacyHeuristicPercent: 4.3,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_overuse", Severity: rules.SeverityWarning},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 11.19},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "需打磨", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}

	if !reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("current-hash external pass should reconcile local heuristic false positive")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsNonblockingAIVoiceFailDimension(t *testing.T) {
	body := "第二章\n\n林澈借来皮卡，和沈知遥把五块价牌立在桥头。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 2, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{
			{Type: "aesthetic", Severity: "warning", Description: "可删一处不是而是句式"},
			{Type: "aesthetic", Severity: "warning", Description: "章末共同动作可再收声"},
		},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 50, Verdict: "fail", Comment: "命中 warning 级章节功能重复"},
		},
	}
	cap := 2.0
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 2, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 9.18, HumanAnchorFinalCap: &cap,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "object_response_overuse", Severity: rules.SeverityWarning}},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 2, BodySHA256: hash, Label: "需打磨", Summary: "命中 1 项 warning 级红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 2, ParagraphCount: 40, SentenceCount: 80},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 2, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}
	if !reconcileWarningOnlyEditorReview(&entry, hash, mechanical, analysis, judge) {
		t.Fatal("warning-only AI voice fail dimension must not override same-hash external and human-readable chapter")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || entry.Dimensions[3].Verdict != "pass" {
		t.Fatalf("reconciled entry = %+v", entry)
	}
}

func TestEditorReviewCacheHitSkipsModelCallAndUsesDedicatedArtifact(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把手机翻过来。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	model := &reviewCacheModel{response: editorCacheTestMarkdown}

	first := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if first.Err != nil || first.CacheHit || first.CacheArtifact == nil {
		t.Fatalf("first editor branch = %+v", first)
	}
	if err := saveEditorReviewCache(dir, first.CacheArtifact); err != nil {
		t.Fatalf("saveEditorReviewCache: %v", err)
	}

	// Generated timestamps are persistence metadata, not review context. A fresh
	// deterministic analysis for the same body must still hit the same cache.
	secondAnalysis := editorCacheTestAnalysis(body, "2026-07-11T10:05:00+08:00")
	second := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, secondAnalysis, time.Second,
	)
	if second.Err != nil || !second.CacheHit {
		t.Fatalf("second editor branch = %+v", second)
	}
	if model.callCount() != 1 {
		t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
	}
	cachePath := reviewExistingCachePath(dir, editorReviewCacheBranch, first.CacheArtifact.CacheKey)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("dedicated Editor cache artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "01.json")); !os.IsNotExist(err) {
		t.Fatalf("Editor cache must not reuse or create final ReviewEntry, stat err=%v", err)
	}
}

func TestEditorReviewCacheDriftCausesModelMiss(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈停在门口。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	baseModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	base := loadOrGenerateEditorReview(
		dir, baseModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if base.Err != nil || base.CacheArtifact == nil {
		t.Fatalf("base editor branch = %+v", base)
	}
	if err := saveEditorReviewCache(dir, base.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		provider       string
		model          string
		premise        string
		userRules      string
		chapterContext string
		body           string
	}{
		{name: "body", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body + "\n新一句。"},
		{name: "provider", provider: "anthropic", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "model", provider: "openai", model: "editor-v2", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "premise", provider: "openai", model: "editor-v1", premise: "premise drift", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "user rules", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules drift", chapterContext: "chapter-context", body: body},
		{name: "chapter context", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context drift", body: body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &reviewCacheModel{response: editorCacheTestMarkdown}
			caseAnalysis := editorCacheTestAnalysis(tt.body, "2026-07-11T10:10:00+08:00")
			result := loadOrGenerateEditorReview(
				dir, model, tt.provider, tt.model, tt.premise, tt.userRules, tt.chapterContext,
				1, tt.body, caseAnalysis, time.Second,
			)
			if result.Err != nil || result.CacheHit {
				t.Fatalf("drifted editor branch = %+v", result)
			}
			if model.callCount() != 1 {
				t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
			}
		})
	}

	driftedPolicy := base.CacheArtifact.CachePolicy
	driftedPolicy.ReviewProtocolVersion += "-drift"
	if reviewExistingCacheKey(driftedPolicy) == base.CacheArtifact.CacheKey {
		t.Fatal("review protocol drift must change the cache key")
	}
	if cached, err := loadEditorReviewCache(dir, driftedPolicy); err != nil || cached != nil {
		t.Fatalf("protocol-drifted cache load = artifact:%+v err:%v, want miss", cached, err)
	}
}

func TestReviewBranchCachesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把门推开。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	seedModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	seed := loadOrGenerateEditorReview(
		dir, seedModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if seed.Err != nil || seed.CacheArtifact == nil {
		t.Fatalf("seed editor branch = %+v", seed)
	}
	if err := saveEditorReviewCache(dir, seed.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	editorModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel := &reviewCacheModel{response: deepseekCompleteHumanResponse}
	editorResult, deepseekResult := runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || !editorResult.CacheHit || editorModel.callCount() != 0 {
		t.Fatalf("Editor independent hit failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || deepseekResult.CacheHit || deepseekModel.callCount() != 1 {
		t.Fatalf("DeepSeek independent miss failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
	if err := saveDeepSeekAIJudgeCache(dir, deepseekResult.Artifact); err != nil {
		t.Fatal(err)
	}

	editorModel = &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel = &reviewCacheModel{response: deepseekCompleteHumanResponse}
	editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "changed-chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || editorResult.CacheHit || editorModel.callCount() != 1 {
		t.Fatalf("Editor independent miss failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || !deepseekResult.CacheHit || deepseekModel.callCount() != 0 {
		t.Fatalf("DeepSeek independent hit failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
}

func TestRunReviewExistingBranchesConcurrentlyStartsBoth(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var editorResult editorReviewBranchResult
	var deepseekResult deepseekAIJudgeBranchResult
	go func() {
		editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
			func() editorReviewBranchResult {
				started <- "editor"
				<-release
				return editorReviewBranchResult{Review: "editor-done"}
			},
			func() deepseekAIJudgeBranchResult {
				started <- "deepseek"
				<-release
				return deepseekAIJudgeBranchResult{Artifact: &deepseekAIJudgeArtifact{Summary: "deepseek-done"}}
			},
		)
		close(done)
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case branch := <-started:
			seen[branch] = true
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("both review branches did not start before release")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parallel review helper did not return")
	}
	if editorResult.Review != "editor-done" || deepseekResult.Artifact == nil || deepseekResult.Artifact.Summary != "deepseek-done" {
		t.Fatalf("unexpected branch results: editor=%+v deepseek=%+v", editorResult, deepseekResult)
	}
}

func editorCacheTestAnalysis(body, generatedAt string) domain.AIVoiceAnalysis {
	return domain.AIVoiceAnalysis{
		Chapter:     1,
		BodySHA256:  reviewreport.BodySHA256(body),
		Label:       "可通过",
		Summary:     "规则分析稳定",
		GeneratedAt: generatedAt,
		Metrics: domain.ChapterAIVoiceMetrics{
			Chapter:     1,
			GeneratedAt: generatedAt,
			AIVoiceScoreHistory: []domain.AIVoiceScorePoint{{
				Round: 1, Source: "rules", Score: 0.1, At: generatedAt,
			}},
		},
	}
}
