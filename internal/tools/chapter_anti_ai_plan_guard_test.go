package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCurrentRepairAntiAIPlanRejectsEmptyAndPartialForRegisteredExternalFailure(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章\n\n主角把手机扣在桌面，没接那句追问。"
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, st.Dir(), 1, body, "zhuque", "novel-whole-text-single-segment", 86)

	plan := domain.ChapterPlan{Chapter: 1, Title: "第一章"}
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, true); err == nil || !strings.Contains(err.Error(), "anti_ai_execution_plan") {
		t.Fatalf("empty anti-AI plan bypassed current registered external failure: %v", err)
	}

	plan.CausalSimulation.AntiAIPlan = domain.AntiAIExecutionPlan{
		RiskSignals:          []string{"整章流程与对白等距推进"},
		CounterMoves:         []string{"让主角的羞耻先制造误判，再由安全选择纠正"},
		SentenceRhythmPolicy: "饭桌保留主观长段，选择处收短，经营流程压成结果。",
		DialogueFunctionPlan: "梁广财只追问一次，其他人允许漏答和沉默。",
		ReviewChecks:         []string{"系统结算是否晚于人物的安全选择"},
	}
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, true); err == nil || !strings.Contains(err.Error(), "object_response_budget") {
		t.Fatalf("partial anti-AI plan without object response policy passed: %v", err)
	}

	plan.CausalSimulation.AntiAIPlan.ObjectResponseBudget = "旧债拒付后系统静默；人物完成选择后才允许一次结算回应。"
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, true); err == nil || !strings.Contains(err.Error(), "literary_rendering_plan") {
		t.Fatalf("focused anti-AI checklist without a POV rendering projection passed: %v", err)
	}
	plan.CausalSimulation.LiteraryRendering = completeRepairLiteraryRenderingPlan()
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, true); err != nil {
		t.Fatalf("complete focused anti-AI plan rejected: %v", err)
	}
}

func completeRepairLiteraryRenderingPlan() *domain.LiteraryRenderingPlan {
	return &domain.LiteraryRenderingPlan{
		Focalizer:             "主角",
		NarrativeAccess:       domain.LiteraryNarrativeAccessInternal,
		KnowledgeBoundary:     "只进入主角能感知、回忆和误判的范围，不读取他人内心。",
		PerceptualBias:        "先注意会暴露失业和欠情的细节，再注意经营结果。",
		SummaryOmissionPolicy: "采购与安装只留造成选择变化的结果，其余概述或省略。",
		Afterimage:            "被主动关掉的灯与仍未说出口的话留在章末。",
		SourceRefs:            []string{"literary-rendering#focalization-boundary", "literary-rendering#scene-summary"},
		SceneModes: []domain.LiterarySceneRenderingMode{{
			Target: "饭桌守密选择", Mode: domain.LiterarySceneModeScene,
			Distance: domain.LiteraryNarrativeDistanceClose, StateChange: "主角决定不向家人展示异常余额",
			RenderMove: "让准备好的解释被父亲的具体动作打断，决定先发生，说明留空。",
		}},
		ActiveLenses: []domain.LiteraryRenderingLens{{
			Kind: "受限误读", Target: "父亲护场动作", Move: "先按主角的难堪误读，再由欠情后果修正判断。",
			Why: "主观判断必须改变守密选择。", Avoid: "不写他意识到、不替父亲解释内心。",
			SourceRefs: []string{"literary-rendering#goal-causality", "literary-rendering#free-indirect-discourse"},
		}},
	}
}

func TestCurrentRepairAntiAIPlanStaysOptionalWithoutAIGCContract(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "普通返工"}
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, true); err != nil {
		t.Fatalf("ordinary rewrite was forced to create an anti-AI method table: %v", err)
	}
	if err := ValidateChapterAntiAIExecutionPlanForCurrentRepair(st, plan, false); err != nil {
		t.Fatalf("ordinary new chapter was forced to create an anti-AI method table: %v", err)
	}
}

func TestReusableRerenderCannotBypassCurrentRepairAntiAIPlan(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章\n\n主角把账本合上，先去关掉越过边线的灯。"
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, st.Dir(), 1, body, "zhuque", "novel-whole-text-single-segment", 86)
	plan := domain.ChapterPlan{Chapter: 1, Title: "第一章"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	err := ValidateReusableCausalPlanForRerender(st, 1)
	if err == nil || !strings.Contains(err.Error(), "anti_ai_execution_plan") {
		t.Fatalf("render-only plan reuse bypassed empty anti-AI contract: %v", err)
	}
	if RenderOnlyRerenderReady(st, 1) {
		t.Fatal("empty current-repair anti-AI plan authorized render-only prose")
	}
}
