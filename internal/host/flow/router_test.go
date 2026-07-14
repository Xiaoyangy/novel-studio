package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// helper：构造一个处于 Writing 阶段、分层模式的 Progress。
func writingProgress(completed []int, flow domain.FlowState) *domain.Progress {
	return &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              flow,
		Layered:           true,
		CompletedChapters: completed,
	}
}

func TestRoute_NilProgress(t *testing.T) {
	if got := Route(State{Progress: nil}); got != nil {
		t.Fatalf("expected nil for nil progress, got %+v", got)
	}
}

func TestRoute_PhaseComplete(t *testing.T) {
	s := State{Progress: &domain.Progress{Phase: domain.PhaseComplete}}
	if got := Route(s); got != nil {
		t.Fatalf("expected nil at PhaseComplete, got %+v", got)
	}
}

func TestRoute_NonWritingPhasesDelegateToLLM(t *testing.T) {
	for _, phase := range []domain.Phase{domain.PhaseInit, domain.PhasePremise, domain.PhaseOutline} {
		s := State{Progress: &domain.Progress{Phase: phase}, FoundationMissing: []string{"premise"}}
		if got := Route(s); got != nil {
			t.Fatalf("phase %s should return nil, got %+v", phase, got)
		}
	}
}

func TestRoute_PendingRewritesFirst(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{3, 5}
	// 阶段拆分：计划未按审阅重推演 → planner 先重做计划。
	got := Route(State{
		Progress:            p,
		NextActionPlanReady: false,
		NextActionTitle:     "第三章标题",
		NextActionCoreEvent: "第三章核心事件",
		NextActionHook:      "第三章章末钩子",
	})
	if got == nil || got.Agent != "writer" || got.Chapter != 3 {
		t.Fatalf("expected planner(writer) re-plan for rewrite ch3, got %+v", got)
	}
	for _, want := range []string{
		"返工目标锁定第 3 章", "严禁规划第 4 章", "chapter=3",
		"《第三章标题》", "第三章核心事件", "第三章章末钩子",
	} {
		if !strings.Contains(got.Task, want) {
			t.Fatalf("rewrite planning task missing %q: %s", want, got.Task)
		}
	}
	// 计划就绪 → drafter 渲染重写。
	got = Route(State{Progress: p, NextActionPlanReady: true})
	if got == nil || got.Agent != "drafter" {
		t.Fatalf("expected drafter for rewrites, got %+v", got)
	}
	if got.Task != "重写第 3 章" {
		t.Errorf("expected '重写第 3 章', got %q", got.Task)
	}
	if got.Chapter != 3 {
		t.Errorf("expected Chapter=3, got %d", got.Chapter)
	}
}

func TestRoute_StagedPlanRepairBeatsGenericRewritePlanning(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{1}
	steer := "Pipeline staged-plan repair：只补 trend_language_plan 与 system_companion_voice"
	got := Route(State{
		Progress:                 p,
		NextActionPlanReady:      false,
		NextActionPlanPartial:    true,
		NextActionPlanRepairTask: steer,
		NextActionTitle:          "失业饭桌",
		NextActionCoreEvent:      "饭桌受辱",
		NextActionHook:           "系统绑定",
	})
	if got == nil || got.Agent != "writer" || got.Task != steer {
		t.Fatalf("staged repair task must beat generic rewrite prompt, got %+v", got)
	}
}

func TestRoute_WorldSimulationRepairUsesRestrictedAgent(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{1}
	steer := "Pipeline world-simulation repair：只补全角色决定"
	got := Route(State{
		Progress:                 p,
		NextActionPlanReady:      false,
		NextActionPlanPartial:    true,
		NextActionPlanRepairTask: steer,
	})
	if got == nil || got.Agent != "world_simulator" || got.Task != steer {
		t.Fatalf("world simulation repair must use restricted agent, got %+v", got)
	}
}

func TestRoute_PendingPolishingVerb(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowPolishing)
	p.PendingRewrites = []int{2}
	got := Route(State{Progress: p, NextActionPlanReady: true})
	if got == nil || got.Agent != "drafter" || got.Task != "打磨第 2 章" {
		t.Fatalf("expected drafter polish verb, got %+v", got)
	}
}

func TestRoute_ExistingDraftUsesRestrictedFinalizer(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{Progress: p, NextActionPlanReady: true, NextActionDraftReady: true})
	if got == nil || got.Agent != "draft_finalizer" || !strings.Contains(got.Task, "禁止重新整章生成") ||
		!strings.Contains(got.Task, "novel_context(chapter=2, profile=draft)") || !strings.Contains(got.Task, "人工验收补充属于确定性约束") ||
		!strings.Contains(got.Task, "任何 commit_chapter 失败后必须立即结束") {
		t.Fatalf("existing draft should use restricted finalizer, got %+v", got)
	}
}

func TestRoute_ExplicitRerenderUsesDrafterWithoutReplanning(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{
		Progress: p, NextActionPlanReady: true, NextActionExplicitRerender: true,
	})
	if got == nil || got.Agent != "drafter" || !strings.Contains(got.Task, "profile=draft") ||
		!strings.Contains(got.Task, "draft_chapter(mode=write)") || !strings.Contains(got.Task, "禁止调用 simulate_chapter_world") {
		t.Fatalf("explicit rerender must bypass replanning and force one full draft, got %+v", got)
	}
}

func TestRoute_BlockingFormalReviewRequiresFreshWholeDraft(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{
		Progress: p, NextActionPlanReady: true, NextActionReviewRerenderRequired: true,
	})
	if got == nil || got.Agent != "drafter" || !strings.Contains(got.Task, "draft_chapter(mode=write)") ||
		!strings.Contains(got.Task, "正式复审要求") || !strings.Contains(got.Task, "禁止 simulate_chapter_world") {
		t.Fatalf("same-hash blocking formal review must create a fresh whole draft, got %+v", got)
	}
}

func TestRoute_BlockingExternalDraftReviewUsesDrafterForWholeChapter(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{
		Progress: p, NextActionPlanReady: true, NextActionDraftReady: true,
		NextActionDraftExternalRerenderRequired: true,
	})
	if got == nil || got.Agent != "drafter" || !strings.Contains(got.Task, "draft_chapter(mode=write)") || !strings.Contains(got.Task, "禁止 edit_chapter") ||
		!strings.Contains(got.Task, "rewrite_brief") || !strings.Contains(got.Task, "profile=draft") || !strings.Contains(got.Task, "写入成功后立即结束") {
		t.Fatalf("blocking external review must route to full drafter, got %+v", got)
	}
}

func TestRoute_ExternalRejudgePendingStopsProseDispatch(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{
		Progress: p, NextActionPlanReady: true, NextActionDraftReady: true,
		NextActionDraftExternalRejudgePending: true,
	})
	if got != nil {
		t.Fatalf("pending external rejudge must pause host routing, got %+v", got)
	}
}

func TestRoute_ExplicitRerenderSupersedesPendingJudgeForOldDraft(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowRewriting)
	p.PendingRewrites = []int{2}
	got := Route(State{
		Progress: p, NextActionPlanReady: true, NextActionExplicitRerender: true,
		NextActionDraftExternalRejudgePending: true,
	})
	if got == nil || got.Agent != "drafter" || !strings.Contains(got.Task, "显式整章重渲染") {
		t.Fatalf("newer explicit rerender should skip judging superseded draft, got %+v", got)
	}
}

func TestRoute_ReviewingDelegatesToLLM(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowReviewing)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("expected nil during reviewing, got %+v", got)
	}
}

func TestRoute_SteeringDelegatesToLLM(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowSteering)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("expected nil during steering, got %+v", got)
	}
}

func TestRoute_ChapterReviewBeforeContinue(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	got := Route(State{Progress: p, LastCompleted: 3, UnreviewedChapter: 3})
	if got == nil || got.Agent != "editor" {
		t.Fatalf("expected editor for chapter review, got %+v", got)
	}
	if got.Chapter != 3 {
		t.Errorf("expected Chapter=3, got %d", got.Chapter)
	}
	if got.Reason != "存在已完成章节尚未通过章级审阅" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_EarlierUnreviewedChapterBeforeContinue(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	got := Route(State{Progress: p, LastCompleted: 3, HasChapterReview: true, UnreviewedChapter: 2})
	if got == nil || got.Agent != "editor" || got.Chapter != 2 {
		t.Fatalf("expected editor for earliest unreviewed chapter, got %+v", got)
	}
}

func TestRoute_ArcEndNeedsReview(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:         p,
		LastCompleted:    10,
		HasChapterReview: true,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("expected editor for arc review, got %+v", got)
	}
	if got.Reason != "弧末评审未完成" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_ArcEndHasReviewNeedsSummary(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:         p,
		LastCompleted:    10,
		HasChapterReview: true,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
		HasArcReview: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" || got.Reason != "弧摘要未完成" {
		t.Fatalf("expected arc summary editor call, got %+v", got)
	}
}

func TestRoute_VolumeEndNeedsVolumeSummary(t *testing.T) {
	p := writingProgress([]int{20}, domain.FlowWriting)
	s := State{
		Progress:         p,
		LastCompleted:    20,
		HasChapterReview: true,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:    true,
			IsVolumeEnd: true,
			Volume:      1,
			Arc:         3,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Reason != "卷摘要未完成" {
		t.Fatalf("expected volume summary request, got %+v", got)
	}
}

func TestRoute_NeedsArcExpansion(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:         p,
		LastCompleted:    10,
		HasChapterReview: true,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			Volume:         1,
			Arc:            2,
			NextVolume:     1,
			NextArc:        3,
			NeedsExpansion: true,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("expected architect_long for expansion, got %+v", got)
	}
	if got.Reason != "下一弧骨架待展开" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_NeedsNewVolume(t *testing.T) {
	p := writingProgress([]int{30}, domain.FlowWriting)
	s := State{
		Progress:         p,
		LastCompleted:    30,
		HasChapterReview: true,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			IsVolumeEnd:    true,
			Volume:         2,
			Arc:            4,
			NeedsNewVolume: true,
		},
		HasArcReview:     true,
		HasArcSummary:    true,
		HasVolumeSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" || got.Reason != "卷末需决定追加新卷或结束全书" {
		t.Fatalf("expected append_volume/complete_book dispatch, got %+v", got)
	}
}

func TestRoute_NormalContinue(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	p.TotalChapters = 20
	// 阶段拆分：计划未落盘 → planner 先推演。
	got := Route(State{Progress: p, LastCompleted: 3, HasChapterReview: true, NextActionPlanReady: false})
	if got == nil || got.Agent != "writer" || got.Chapter != 4 {
		t.Fatalf("expected planner(writer) for ch4, got %+v", got)
	}
	// 计划就绪 → drafter 渲染正文。
	got = Route(State{Progress: p, LastCompleted: 3, HasChapterReview: true, NextActionPlanReady: true})
	if got == nil || got.Agent != "drafter" {
		t.Fatalf("expected drafter for next chapter, got %+v", got)
	}
	if got.Task != "写第 4 章" {
		t.Errorf("expected '写第 4 章', got %q", got.Task)
	}
	if got.Chapter != 4 {
		t.Errorf("expected Chapter=4, got %d", got.Chapter)
	}
}

func TestRoute_WorldSimulationPrecedesPOVPlanner(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowWriting)
	p.TotalChapters = 20
	got := Route(State{
		Progress:                          p,
		NextActionPlanReady:               false,
		NextActionWorldSimulationRequired: true,
		NextActionWorldSimulationReady:    false,
		NextActionWorldSimulationGaps:     []string{"missing character decision: 沈知遥"},
	})
	if got == nil || got.Agent != "world_simulator" || got.Chapter != 2 {
		t.Fatalf("world simulation must precede POV planning, got %+v", got)
	}
	if !strings.Contains(got.Task, "profile=world_simulation") || !strings.Contains(got.Task, "沈知遥") {
		t.Fatalf("world simulator task lost profile or gaps: %q", got.Task)
	}
}

func TestRoute_ShortCompleteNeedsFinalGlobalReview(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{1, 2},
		TotalChapters:     2,
	}
	got := Route(State{
		Progress:               p,
		LastCompleted:          2,
		HasChapterReview:       true,
		BookCompleteByChapters: true,
		NeedsFinalGlobalReview: true,
	})
	if got == nil || got.Agent != "editor" || got.Chapter != 2 {
		t.Fatalf("expected editor for final global review, got %+v", got)
	}
	if !contains(got.Task, "scope=global") {
		t.Fatalf("expected global review task, got %q", got.Task)
	}
}

func TestRoute_NonLayeredCompleteDoesNotWriteOutOfRange(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{1, 2},
		TotalChapters:     2,
	}
	got := Route(State{
		Progress:               p,
		LastCompleted:          2,
		HasChapterReview:       true,
		BookCompleteByChapters: true,
		NeedsFinalGlobalReview: false,
	})
	if got != nil {
		t.Fatalf("expected nil instead of writing chapter 3 out of range, got %+v", got)
	}
}

func TestRoute_ArcEndNonLayeredSkipsBoundary(t *testing.T) {
	// 非 Layered 模式即使 ArcBoundary 非 nil 也不走弧末分支
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{10},
		TotalChapters:     20,
	}
	s := State{
		Progress:         p,
		LastCompleted:    10,
		HasChapterReview: true,
		ArcBoundary:      &storepkg.ArcBoundary{IsArcEnd: true, Volume: 1, Arc: 2},
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("non-layered should fall through to writer, got %+v", got)
	}
}

func TestFormatMessage(t *testing.T) {
	msg := FormatMessage(&Instruction{Agent: "writer", Task: "写第 5 章", Reason: "续写"})
	for _, want := range []string{"[Host 下达指令]", "subagent(writer, \"写第 5 章\")", "agent: writer", "task: \"写第 5 章\"", "续写", "必须原样使用", "不要改写 task", "不要先调 novel_context"} {
		if !contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDispatcher_TrackRepeat(t *testing.T) {
	// 不需要真实 coordinator / store；trackRepeat 只读自己的缓存。
	d := &Dispatcher{}
	inst := &Instruction{Agent: "writer", Task: "写第 5 章", Reason: "续写"}
	if got, stalled := d.trackRepeat(inst, "cp:7"); got != 1 || stalled {
		t.Fatalf("首次下达应计 1，got %d", got)
	}
	if got, stalled := d.trackRepeat(inst, "cp:7"); got != 2 || stalled {
		t.Fatalf("同 Agent+Task 重复下达应计 2，got %d", got)
	}
	// Reason 不同、Agent+Task 相同时视为同一指令继续累计
	sameTaskDiffReason := &Instruction{Agent: "writer", Task: "写第 5 章", Reason: "弧末后继续"}
	if got, stalled := d.trackRepeat(sameTaskDiffReason, "cp:7"); got != 3 || !stalled {
		t.Fatalf("仅 Reason 不同应累计到 3 并熔断，got %d stalled=%v", got, stalled)
	}
	// 同一任务新增 checkpoint 后属于真实推进，重新计数。
	if got, stalled := d.trackRepeat(inst, "cp:8"); got != 1 || stalled {
		t.Fatalf("checkpoint 推进后应重置为 1，got %d stalled=%v", got, stalled)
	}
	other := &Instruction{Agent: "writer", Task: "写第 6 章", Reason: "续写"}
	if got, stalled := d.trackRepeat(other, "cp:8"); got != 1 || stalled {
		t.Fatalf("Task 变更后应重置为 1，got %d", got)
	}
	d.ResetRepeat()
	if got, stalled := d.trackRepeat(other, "cp:8"); got != 1 || stalled {
		t.Fatalf("ResetRepeat 后首次应计 1，got %d", got)
	}
}

func TestFormatDispatchMessage_RepeatNotice(t *testing.T) {
	inst := &Instruction{Agent: "writer", Task: "写第 5 章", Reason: "续写"}
	first := formatDispatchMessage(inst, 1)
	if first != FormatMessage(inst) {
		t.Fatalf("首次下达不应附加重复注记: %s", first)
	}
	third := formatDispatchMessage(inst, 3)
	for _, want := range []string{"第 3 次下达", "路由事实未变化", "novel_context", "改派"} {
		if !contains(third, want) {
			t.Errorf("重复注记缺少 %q: %s", want, third)
		}
	}
}

func TestDispatcher_OnRepeatFiresOnceAtThreshold(t *testing.T) {
	d := &Dispatcher{}
	var fired []string
	d.SetOnRepeat(func(agent, task string, n int) {
		fired = append(fired, fmt.Sprintf("%s|%s|%d", agent, task, n))
	})

	inst := &Instruction{Agent: "writer", Task: "写第 5 章"}
	for range 6 {
		d.trackRepeat(inst, "") // n=1..6：只在告警阈值回调一次
	}
	if len(fired) != 1 || fired[0] != fmt.Sprintf("writer|写第 5 章|%d", repeatNotifyAt) {
		t.Fatalf("应恰好在第 %d 次触发一次，got %v", repeatNotifyAt, fired)
	}

	// 键变更后重新武装：换任务再连续 3 次 → 再触发一次
	other := &Instruction{Agent: "writer", Task: "写第 6 章"}
	for range repeatNotifyAt {
		d.trackRepeat(other, "")
	}
	if len(fired) != 2 {
		t.Fatalf("键变更后应重新武装，got %v", fired)
	}
}

func TestDispatcher_StallFiresOnceAndCheckpointProgressRearms(t *testing.T) {
	d := &Dispatcher{}
	var fired []string
	d.SetOnStall(func(agent, task string, n int) {
		fired = append(fired, fmt.Sprintf("%s|%s|%d", agent, task, n))
	})
	inst := &Instruction{Agent: "writer", Task: "写第 5 章"}

	for range 5 {
		n, stalled := d.trackRepeat(inst, "cp:9")
		if stalled && n == stallAbortAt {
			d.onStall(inst.Agent, inst.Task, n)
		}
	}
	if len(fired) != 1 {
		t.Fatalf("同一 checkpoint 上应只熔断一次，got %v", fired)
	}

	for range stallAbortAt {
		n, stalled := d.trackRepeat(inst, "cp:10")
		if stalled && n == stallAbortAt {
			d.onStall(inst.Agent, inst.Task, n)
		}
	}
	if len(fired) != 2 {
		t.Fatalf("checkpoint 推进后应重新武装，got %v", fired)
	}
}

func TestDispatcher_PartialArtifactCountsAsDurableProgress(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(nil, st)
	inst := &Instruction{Agent: "writer", Task: "规划第 1 章", Chapter: 1}
	before := d.durableProgressToken(inst)
	if got, stalled := d.trackRepeat(inst, before); got != 1 || stalled {
		t.Fatalf("first dispatch got=%d stalled=%v", got, stalled)
	}
	if got, stalled := d.trackRepeat(inst, before); got != 2 || stalled {
		t.Fatalf("second dispatch got=%d stalled=%v", got, stalled)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{"causal_simulation": map[string]any{"initial_state": []any{"saved"}}}); err != nil {
		t.Fatal(err)
	}
	after := d.durableProgressToken(inst)
	if before == after {
		t.Fatal("partial plan fingerprint did not change progress token")
	}
	if got, stalled := d.trackRepeat(inst, after); got != 1 || stalled {
		t.Fatalf("partial progress must rearm counter: got=%d stalled=%v", got, stalled)
	}
}

func TestDispatcher_OnWriterDispatchRunsAfterInProgressMark(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("init progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase writing: %v", err)
	}

	d := NewDispatcher(agentcore.NewAgent(), st)
	d.Enable()

	var called int
	d.SetOnWriterDispatch(func(chapter int) {
		called++
		if chapter != 1 {
			t.Fatalf("writer dispatch chapter = %d, want 1", chapter)
		}
		progress, err := st.Progress.Load()
		if err != nil {
			t.Fatalf("load progress: %v", err)
		}
		if progress.InProgressChapter != 1 {
			t.Fatalf("InProgressChapter = %d, want 1", progress.InProgressChapter)
		}
	})

	d.Dispatch()
	if called != 1 {
		t.Fatalf("on writer dispatch called %d times, want 1", called)
	}
}

func TestDispatcher_SteersAfterSuccessfulBoundaryToolBeforeNextModelCall(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("init progress: %v", err)
	}

	var secondReq *agentcore.LLMRequest
	var dispatcher *Dispatcher
	coordinator := agentcore.NewAgent(
		agentcore.WithModel(sequentialFlowTestModel(func(i int, req *agentcore.LLMRequest) (*agentcore.LLMResponse, error) {
			if i == 0 {
				return &agentcore.LLMResponse{Message: flowTestToolCallMsg(agentcore.ToolCall{
					ID:   "tc-subagent",
					Name: "subagent",
					Args: json.RawMessage(`{"agent":"architect_long","task":"plan"}`),
				})}, nil
			}
			secondReq = req
			return &agentcore.LLMResponse{Message: flowTestAssistantMsg("done", agentcore.StopReasonStop)}, nil
		})),
		agentcore.WithTools(agentcore.NewFuncTool("subagent", "fake subagent", map[string]any{
			"type": "object",
		}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
			if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
				return nil, err
			}
			return json.RawMessage(`"foundation_ready=true"`), nil
		})),
		agentcore.WithMiddlewares(func(ctx context.Context, call agentcore.ToolCall, next agentcore.ToolExecuteFunc) (json.RawMessage, error) {
			out, err := next(ctx, call.Args)
			if err == nil && call.Name == "subagent" {
				dispatcher.Dispatch()
			}
			return out, err
		}),
	)

	dispatcher = NewDispatcher(coordinator, st)
	dispatcher.Enable()

	if err := coordinator.Prompt(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	coordinator.WaitForIdle()

	if secondReq == nil {
		t.Fatal("expected second model request")
	}
	if len(secondReq.Messages) < 4 {
		t.Fatalf("expected tool result and Host instruction in second request, got %d messages", len(secondReq.Messages))
	}
	if result := secondReq.Messages[len(secondReq.Messages)-2]; result.Role != agentcore.RoleTool {
		t.Fatalf("expected tool result immediately before Host instruction, got %q", result.Role)
	}
	got := secondReq.Messages[len(secondReq.Messages)-1].TextContent()
	// 阶段拆分：第 1 章无计划 → Host 先派 planner(writer) 做写前推演。
	for _, want := range []string{"[Host 下达指令]", "subagent(writer", "第 1 章"} {
		if !contains(got, want) {
			t.Fatalf("Host instruction missing %q: %s", want, got)
		}
	}
}

type flowTestSequentialModel struct {
	fn  func(i int, req *agentcore.LLMRequest) (*agentcore.LLMResponse, error)
	idx int64
}

func sequentialFlowTestModel(fn func(i int, req *agentcore.LLMRequest) (*agentcore.LLMResponse, error)) *flowTestSequentialModel {
	return &flowTestSequentialModel{fn: fn}
}

func (m *flowTestSequentialModel) take(msgs []agentcore.Message, tools []agentcore.ToolSpec) (*agentcore.LLMResponse, error) {
	i := int(atomic.AddInt64(&m.idx, 1) - 1)
	return m.fn(i, &agentcore.LLMRequest{Messages: msgs, Tools: tools})
}

func (m *flowTestSequentialModel) Generate(_ context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.take(msgs, tools)
}

func (m *flowTestSequentialModel) GenerateStream(_ context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, err := m.take(msgs, tools)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *flowTestSequentialModel) SupportsTools() bool { return true }

func flowTestAssistantMsg(text string, stop agentcore.StopReason) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(text)},
		StopReason: stop,
	}
}

func flowTestToolCallMsg(calls ...agentcore.ToolCall) agentcore.Message {
	blocks := make([]agentcore.ContentBlock, len(calls))
	for i, call := range calls {
		blocks[i] = agentcore.ToolCallBlock(call)
	}
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    blocks,
		StopReason: agentcore.StopReasonToolUse,
	}
}
