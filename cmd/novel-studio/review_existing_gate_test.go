package main

import (
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestReviewExistingAIGCGatePercentUsesRawForShortChapter(t *testing.T) {
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 3000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
	}

	if got := reviewExistingAIGCGatePercent(report); got != 80 {
		t.Fatalf("short chapter gate percent = %.2f, want raw 80.00", got)
	}
}

func TestReviewExistingAIGCGatePercentUsesHumanAnchorCap(t *testing.T) {
	capValue := 4.8
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 3000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
		HumanAnchorFinalCap:    &capValue,
	}

	if got := reviewExistingAIGCGatePercent(report); got != 4.8 {
		t.Fatalf("human-anchor capped chapter gate percent = %.2f, want 4.80", got)
	}
}

func TestReviewExistingAIGCGatePercentAllowsBlendedForLongChapter(t *testing.T) {
	report := aigc.Report{
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     4.8,
		Stats:                  aigc.Stats{Hanzi: 6000},
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 16.8,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 8.24},
	}

	if got := reviewExistingAIGCGatePercent(report); got != 4.8 {
		t.Fatalf("long chapter gate percent = %.2f, want blended 4.80", got)
	}
}

func TestParseReviewIssuesSkipsNonActionablePraiseAndOptionalAdvice(t *testing.T) {
	md := `# ch01 评审

## 主要问题（按严重度排序）
1. **无严重问题。** 本章各项 red flag 检测的"警告"实为优秀写作的表现。
2. **次要优化建议（审美，非必要）：** 某句可以更含混，但非必需。
3. protagonist_waver_missing 已有正文证据，无需补充修改。
4. dialogue_conveyor_overuse 已被动作打断，不构成当前章问题。
5. 某个判断可再强化，但已满足后果链，属可选优化。

## 结论
通过，不建议改写。`

	if issues := parseReviewIssues(md); len(issues) != 0 {
		t.Fatalf("expected non-actionable lines to be skipped, got %+v", issues)
	}
}

func TestParseReviewDimensionsPreservesStructuralClearanceTail(t *testing.T) {
	longPrefix := strings.Repeat("前置证据，", 70)
	md := "| 8 AI 腔检测 | 3 | " + longPrefix +
		"dialogue_conveyor_overuse warning：已有动作换挡，不触发返工。" +
		"object_response_rhythm_flat warning：已有延迟回应，无需改写。 |"
	dimensions := parseReviewDimensions(md)
	if len(dimensions) != 1 {
		t.Fatalf("dimensions=%d, want 1", len(dimensions))
	}
	for _, want := range []string{
		"dialogue_conveyor_overuse warning", "不触发返工",
		"object_response_rhythm_flat warning", "无需改写",
	} {
		if !strings.Contains(dimensions[0].Comment, want) {
			t.Fatalf("long dimension comment lost clearance %q: %s", want, dimensions[0].Comment)
		}
	}
}

func TestParseReviewIssuesKeepsActionableIssue(t *testing.T) {
	md := `# ch01 评审

## 主要问题（按严重度排序）
1. 第12段总结腔仍需改成动作后果。

## 结论
建议打磨。`

	issues := parseReviewIssues(md)
	if len(issues) != 1 {
		t.Fatalf("expected one actionable issue, got %+v", issues)
	}
	if issues[0].Description != "第12段总结腔仍需改成动作后果。" {
		t.Fatalf("unexpected issue: %+v", issues[0])
	}
}

func TestSanitizeEditorReviewAcceptsExplicitZeroActionRewrite(t *testing.T) {
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dimensions = append(dimensions, domain.DimensionScore{
			Dimension: name, Score: 100, Verdict: "pass", Comment: "通过",
		})
	}
	zeroAction := "无。本章所有 review_checks 逐项通过；AI 腔仅命中非阻断 warning，本章已有独立事件与结果，无需返工。"
	entry := domain.ReviewEntry{
		Chapter: 3, ContractStatus: "met", Verdict: "rewrite", AffectedChapters: []int{3},
		Dimensions: dimensions,
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning", Description: zeroAction, Evidence: zeroAction,
		}},
	}

	removed := sanitizeEditorReviewForProject(nil, 3, "正文", domain.AIVoiceAnalysis{}, &entry)
	if len(removed) != 1 || len(entry.Issues) != 0 || entry.Verdict != "accept" || len(entry.AffectedChapters) != 0 {
		t.Fatalf("zero-action contradiction was not normalized: removed=%v entry=%+v", removed, entry)
	}
}

func TestSanitizeEditorReviewKeepsExplicitActionableRewrite(t *testing.T) {
	entry := domain.ReviewEntry{
		Chapter: 3, ContractStatus: "met", Verdict: "rewrite", AffectedChapters: []int{3},
		Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: 100, Verdict: "pass", Comment: "通过"}},
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "无需整章返工，但第12段对白传送带仍需修改，必须返工该段。",
		}},
	}

	removed := sanitizeEditorReviewForProject(nil, 3, "正文", domain.AIVoiceAnalysis{}, &entry)
	if len(removed) != 0 || len(entry.Issues) != 1 || entry.Verdict != "rewrite" {
		t.Fatalf("actionable rewrite was incorrectly relaxed: removed=%v entry=%+v", removed, entry)
	}
}

func TestCallEditorOnChapterIncludesProjectRules(t *testing.T) {
	model := &deepseekJudgeCaptureModel{}
	rules := `{"preferences":"系统会接话吐槽并始终支持林澈，不能写成纯任务机器人。"}`
	plan := `{"trend_language_plan":[{"item":"呱，","character_carrier":"赵航本人"}]}`
	if _, err := callEditorOnChapter(model, "故事前提", rules, plan, 1, "第一章\n\n林澈看向手机。", domain.AIVoiceAnalysis{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(model.messages))
	}
	if got := model.messages[1].TextContent(); !strings.Contains(got, "项目用户规则（最高优先级）") || !strings.Contains(got, "系统会接话吐槽") {
		t.Fatalf("project rules missing from editor input: %s", got)
	}
	if got := model.messages[1].TextContent(); !strings.Contains(got, "本章已批准写前 plan") || !strings.Contains(got, "赵航本人") {
		t.Fatalf("approved chapter plan missing from editor input: %s", got)
	}
	if got := model.messages[0].TextContent(); !strings.Contains(got, "禁止建议把系统改成冷硬") {
		t.Fatalf("editor system guard missing: %s", got)
	}
}

func TestSanitizeEditorReviewDropsClaimsContradictedByBodyAndApprovedPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{
				Item: "呱，", CharacterCarrier: "赵航本人；由他在饭桌说出口。",
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := `赵航把碗一放：“呱，这也叫关心？”

【真实改善核验完成。】

【解锁小额改善额度五万元。】

【这一笔，算你自己挣的。^_^】

付款页面已经打开，林澈的拇指却迟迟没落下。`
	entry := domain.ReviewEntry{
		Chapter: 1,
		Summary: "主角动摇缺失，缺少一次真实迟疑。",
		Dimensions: []domain.DimensionScore{{
			Dimension: "ai_voice_detection", Score: 80, Verdict: "pass",
			Comment: "命中 protagonist_waver_missing。",
		}},
		Issues: []domain.ConsistencyIssue{
			{Severity: "warning", Description: "主角动摇缺失，缺少一次真实迟疑。"},
			{Severity: "warning", Description: "系统消息一次塞入过多功能，同时承担奖励和任务。"},
			{Severity: "warning", Description: "热梗‘呱’落地方位错误，放在赵航这个配角口中会削弱记忆点。"},
			{Severity: "warning", Description: "章末颜文字位置风险，容易被当成正式系统条款。"},
			{Severity: "warning", Description: "老丁的一句报价仍可再口语化。"},
		},
	}
	analysis := domain.AIVoiceAnalysis{Metrics: domain.ChapterAIVoiceMetrics{ProtagonistWaver: true}}
	removed := sanitizeEditorReviewForProject(s, 1, body, analysis, &entry)
	if len(removed) != 4 {
		t.Fatalf("removed=%v, want four deterministic contradictions", removed)
	}
	if len(entry.Issues) != 1 || !strings.Contains(entry.Issues[0].Description, "老丁") {
		t.Fatalf("unexpected surviving issues: %+v", entry.Issues)
	}
	if strings.Contains(entry.Summary, "动摇缺失") || strings.Contains(entry.Dimensions[0].Comment, "waver_missing") {
		t.Fatalf("stale contradicted diagnosis survived: %+v", entry)
	}
}

func TestSanitizeEditorReviewDropsFutureAppointmentAndExistingCauseChoiceMisreads(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1,
		Contract: domain.ChapterContract{
			RequiredBeats:   []string{"返工完成后约好明早九点复看；主角先应下未来约定，再主动查看首笔结算。"},
			ForbiddenMoves:  []string{"不得提前完成次日复看。"},
			EvaluationFocus: []string{"父亲护场必须直接推动主角守密选择。"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，", CharacterCarrier: "表弟可选使用。"}},
			DecisionPoints: []string{
				"父亲替主角挡话后，主角必须在公开秘密与守密之间选择，落点为收起手机。",
				"应下明早九点后，主角主动查看结算；复看本身留到次日。",
			},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "父亲替主角挡住追问并接走目光。",
				CharacterChoice: "主角压下公开秘密的冲动，收起手机守密。",
			}},
			EndingContract: domain.EndingConsequenceContract{
				EndingMode:      "未来约定先落地，能力结算随后确认。",
				ConcreteAnchor:  "主角应下明早九点后主动查看结算。",
				NextChapterPull: "次日复看尚未发生。",
				ForbiddenEndings: []string{
					"提前写次日复看。",
				},
			},
		},
	}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	body := `父亲把鱼盘推开。只要把那张额度卡亮出来，桌上的追问马上能停；可父亲仍握着酒杯，像替他挡住问话，也接走了全桌人的目光。

他把手机塞回兜里，没有报出数字。

巡查人说：“明早九点我再来复看。”

“九点，我到。”

等两人的话落稳，他才主动点开卡片。

【首笔支出结算完成。】`
	dimensions := []domain.DimensionScore{
		{Dimension: "pacing", Score: 90, Verdict: "pass", Comment: "鱼盘后的父亲护场因果缺失，未兑现守密选择。"},
		{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "热梗‘呱’没有出现，应补上。Contract 要求结算延至次日九点，需将当前结算移至次日九点场景。dialogue_conveyor_overuse warning：饭桌已有沉默和动作换挡，不触发返工。"},
	}
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "rewrite", AffectedChapters: []int{1}, Dimensions: dimensions,
		Summary: "当前章违反延迟查看约定，家庭守密因果也断裂。",
		Issues: []domain.ConsistencyIssue{
			{Severity: "warning", Description: "系统结算违反延迟约定，需将蓝色卡片结算移至次日九点场景。"},
			{Severity: "warning", Description: "饭桌因果断裂：父亲护场没有推动守密选择，鱼盘后的心理因果缺失。"},
			{Severity: "warning", Description: "某句标签化，仍可改得更具体。"},
		},
	}

	removed := sanitizeEditorReviewForProject(s, 1, body, domain.AIVoiceAnalysis{}, &entry)
	if len(entry.Issues) != 1 || !strings.Contains(entry.Issues[0].Description, "标签化") {
		t.Fatalf("mixed issues were not narrowly sanitized: removed=%v issues=%+v", removed, entry.Issues)
	}
	if strings.Contains(entry.Summary, "违反延迟") || strings.Contains(entry.Summary, "因果也断裂") {
		t.Fatalf("stale summary survived: %s", entry.Summary)
	}
	if strings.Contains(entry.Dimensions[0].Comment, "因果缺失") ||
		!strings.Contains(entry.Dimensions[0].Comment, "护场已经直接推动守密选择") {
		t.Fatalf("cause-choice dimension was not repaired: %s", entry.Dimensions[0].Comment)
	}
	aiComment := entry.Dimensions[1].Comment
	for _, stale := range []string{"呱", "移至次日九点"} {
		if strings.Contains(aiComment, stale) {
			t.Fatalf("stale AI dimension claim %q survived: %s", stale, aiComment)
		}
	}
	for _, want := range []string{"object_response_rhythm_flat warning", "无需改写", "dialogue_conveyor_overuse warning", "不触发返工"} {
		if !strings.Contains(aiComment, want) {
			t.Fatalf("AI dimension missing %q after repair: %s", want, aiComment)
		}
	}
}

func TestDeferredCheckMisreadGuardKeepsRealOrUnprovenFailures(t *testing.T) {
	basePlan := domain.ChapterPlan{
		Contract: domain.ChapterContract{
			RequiredBeats:  []string{"约好明早九点后，主角主动查看结算。"},
			ForbiddenMoves: []string{"不得提前完成次日复看。"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			DecisionPoints: []string{"应下明早九点后主动查看结算。"},
			EndingContract: domain.EndingConsequenceContract{
				NextChapterPull:  "次日复看尚未发生。",
				ForbiddenEndings: []string{"提前写次日复看。"},
			},
		},
	}
	complaint := "需将结算移至次日九点场景。"
	validBody := "巡查人约明早九点复看。主角说：‘九点，我到。’等人走后，他才主动点开卡片。【支出结算完成。】"
	if !reviewMisreadsDeferredCheckAfterFutureAppointment(complaint, validBody, &basePlan) {
		t.Fatal("valid appointment -> acceptance -> active check -> settlement evidence was not recognized")
	}

	arrivalPlan := basePlan
	arrivalPlan.Contract.RequiredBeats = []string{"次日九点到场复看完成后，主角主动查看结算。"}
	arrivalPlan.CausalSimulation.DecisionPoints = []string{"次日九点复看完成后主动查看结算。"}
	arrivalPlan.CausalSimulation.EndingContract = domain.EndingConsequenceContract{}
	if reviewMisreadsDeferredCheckAfterFutureAppointment(complaint, validBody, &arrivalPlan) {
		t.Fatal("guard cleared a plan that truly requires next-day arrival before checking")
	}

	reversedBody := "主角先主动点开卡片。【支出结算完成。】巡查人才约明早九点复看。主角说：‘九点，我到。’"
	if reviewMisreadsDeferredCheckAfterFutureAppointment(complaint, reversedBody, &basePlan) {
		t.Fatal("guard cleared reversed body evidence")
	}

	farBody := "巡查人约明早九点复看。主角说：‘九点，我到。’" + strings.Repeat("过场", 1000) + "他才主动点开卡片。【支出结算完成。】"
	if reviewMisreadsDeferredCheckAfterFutureAppointment(complaint, farBody, &basePlan) {
		t.Fatal("guard stitched evidence across a distant scene")
	}
}

func TestSanitizeEditorReviewDoesNotOverrideContractMiss(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 1,
		Contract: domain.ChapterContract{
			RequiredBeats:  []string{"约好明早九点后主动查看结算。"},
			ForbiddenMoves: []string{"不得提前完成次日复看。"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			DecisionPoints: []string{"应下明早九点后主动查看结算。"},
			EndingContract: domain.EndingConsequenceContract{NextChapterPull: "次日复看尚未发生。", ForbiddenEndings: []string{"提前写次日复看。"}},
		},
	}
	s := store.NewStore(t.TempDir())
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	body := "巡查人约明早九点复看。主角说：‘九点，我到。’他才主动点开卡片。【支出结算完成。】"
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "miss", ContractMisses: []string{"真实合同缺口"}, Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{Severity: "warning", Description: "需将结算移至次日九点场景。"}},
		Dimensions: []domain.DimensionScore{{
			Dimension: "continuity", Score: 60, Verdict: "fail",
			Comment: "系统结算需将当前结算移至次日九点场景。",
		}},
	}
	removed := sanitizeEditorReviewForProject(s, 1, body, domain.AIVoiceAnalysis{}, &entry)
	if len(removed) != 0 || len(entry.Issues) != 1 {
		t.Fatalf("contract miss was incorrectly overridden: removed=%v entry=%+v", removed, entry)
	}
	if entry.Dimensions[0].Score != 60 || entry.Dimensions[0].Verdict != "fail" ||
		!strings.Contains(entry.Dimensions[0].Comment, "移至次日九点") {
		t.Fatalf("contract-miss dimension evidence was incorrectly sanitized: %+v", entry.Dimensions[0])
	}
}

func TestSanitizeEditorReviewRepairsColdDrinkOwnerVetoMisread(t *testing.T) {
	body := `冷饮摊那边第一次把短杆固定在桌脚外侧，老板把空箱往外一推，轮子差点擦上支架。她当场摇头，让他们往里收两指。贺骁拆下重装。`
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dimensions = append(dimensions, domain.DimensionScore{Dimension: name, Score: 90, Verdict: "pass", Comment: "通过"})
	}
	dimensions[1].Score = 80
	dimensions[1].Comment = "冷饮摊的空箱试走否决是沈知遥和林澈自己做的，第四家本人没有行动级阻力。"
	entry := domain.ReviewEntry{
		Chapter: 2, ContractStatus: "met", Verdict: "accept", Dimensions: dimensions,
		Issues: []domain.ConsistencyIssue{{
			Type: "character", Severity: "warning",
			Description: "冷饮摊本人没有行动级阻力，未由摊主现场否决。",
		}},
	}

	removed := sanitizeEditorReviewForProject(nil, 2, body, domain.AIVoiceAnalysis{}, &entry)
	if len(removed) != 1 || len(entry.Issues) != 0 {
		t.Fatalf("veto misread was not removed: removed=%v issues=%+v", removed, entry.Issues)
	}
	if entry.Dimensions[1].Score < 90 || !strings.Contains(entry.Dimensions[1].Comment, "老板亲自把空箱往外推") ||
		!strings.Contains(entry.Dimensions[1].Comment, "迫使方案拆下重装") {
		t.Fatalf("character dimension was not repaired from body facts: %+v", entry.Dimensions[1])
	}
}

func TestReviewVerdictAllPassingDimensionsDoNotForcePolish(t *testing.T) {
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dimensions = append(dimensions, domain.DimensionScore{
			Dimension: name,
			Score:     80,
			Verdict:   "pass",
		})
	}
	md := "## 是否需要改写：是"
	if got := reviewVerdictFromMarkdown(md, dimensions); got != "accept" {
		t.Fatalf("all-pass review verdict = %q, want accept", got)
	}
	dimensions[2].Score = 70
	dimensions[2].Verdict = "warning"
	if got := reviewVerdictFromMarkdown(md, dimensions); got != "polish" {
		t.Fatalf("warning-dimension review verdict = %q, want polish", got)
	}
}
