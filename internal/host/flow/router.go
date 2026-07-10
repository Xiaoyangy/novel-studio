// Package flow 实现垂类路由：Host 根据事实决定下一个调哪个子代理做什么。
//
// 设计原则：
//   - Route 是纯函数：输入 State，输出 *Instruction。无 IO、无 Store 调用，可单测。
//   - State 由 LoadState（非纯）从 Store 构造，一次性把路由需要的事实读齐。
//   - 返回 nil 是合法的：表示"裁定场景，让 Coordinator LLM 自主决策"。
//
// Router 覆盖的是"查表型"决策（每章下一步、弧末后处理、队列驱动），
// 不覆盖"语义理解型"决策（选规划师、处理用户 Steer、输出总结）。
package flow

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
)

// Instruction 指示 Host 下一步要求 Coordinator 调用的子代理与任务。
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // 给子代理的任务描述
	Reason  string // 给 Coordinator 看的理由（可选，方便调试与日志）
	Chapter int    // writer 任务涉及的章节号（续写/重写/打磨）；0 表示不涉及（editor/architect 任务）
}

// State 是 Route 的输入：所有事实必须在此显式声明，禁止 Route 内部读 Store。
type State struct {
	Progress *domain.Progress

	// 上一个已完成章节（Progress.CompletedChapters 末尾）；为 0 表示尚未开始写作。
	LastCompleted int

	// 上一章的弧边界信息；IsArcEnd=false 时其他字段无意义。
	// 当 LastCompleted=0 或非 Layered 模式时应为 nil。
	ArcBoundary *storepkg.ArcBoundary

	// 弧末后处理的三个事实：评审 / 弧摘要 / 卷摘要是否已完成。
	HasChapterReview       bool
	UnreviewedChapter      int
	HasArcReview           bool
	HasArcSummary          bool
	HasVolumeSummary       bool
	BookCompleteByChapters bool
	NeedsFinalGlobalReview bool
	HasFinalGlobalReview   bool

	// 基础设定缺项（规划阶段的补齐信号）。
	FoundationMissing []string

	// 阶段拆分：下一个要处理的章节的计划是否已就绪可交 drafter 渲染。
	// 未就绪 → 派 planner（writer）先推演落盘计划；就绪 → 派 drafter 渲染正文。
	NextActionPlanReady bool

	// 当前写作目标的大纲锚点。LoadState 读取后随 Host 指令直达 planner，
	// 避免大上下文裁剪把当前章标题/核心事件挤掉。
	NextActionTitle     string
	NextActionCoreEvent string
	NextActionHook      string
}

// Route 根据事实返回下一步指令；返回 nil 表示让 Coordinator LLM 自主裁定。
//
// 决策优先级（互斥，自上而下匹配第一个）：
//  1. Phase=Complete        → nil（LLM 输出总结）
//  2. Phase!=Writing        → nil（LLM 裁定规划师选型 / 规划补齐）
//  3. PendingRewrites 非空  → writer 按队列重写/打磨
//  4. Flow=Reviewing        → nil（editor 刚保存 review，verdict 分叉由工具层处理）
//  5. Flow=Steering         → nil（用户干预处理中）
//  6. 已完成章章级评审缺失    → editor(chapter review)
//  7. 弧末评审缺失           → editor(arc review)
//  8. 弧末评审有但弧摘要缺失  → editor(arc summary)
//  9. 卷末弧摘要有但卷摘要缺失 → editor(volume summary)
//
// 10. 下一弧是骨架           → architect_long(expand_arc)
//
// 11. 卷末需决策下一卷       → architect_long(append_volume / complete_book)
// 12. 短篇全文终审缺失       → editor(global review)
// 13. 其它                  → writer(写 next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. 终态：让 LLM 输出总结
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. 规划阶段由 Coordinator 裁定（选 architect_long/short + 补齐循环）
	if p.Phase != domain.PhaseWriting {
		return nil
	}

	// 3. 重写/打磨队列优先（事实已在工具层落盘，Router 只照单派发）。
	//    阶段拆分：计划未按审阅结论重推演 → planner 先重做计划；已就绪 → drafter 渲染。
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "重写"
		if p.Flow == domain.FlowPolishing {
			verb = "打磨"
		}
		if !s.NextActionPlanReady {
			task := fmt.Sprintf(
				"返工目标锁定第 %d 章：按本章 rewrite_brief 重做写前推演计划（%s前）。该章已在 completed_chapters、进度游标指向下一章都属于正常返工状态；严禁规划第 %d 章或任何未来章节。read_chapter(第%d章) 是读取待返工原文，不是把它当上一章续写；所有 planning 工具必须提交 chapter=%d",
				ch, verb, ch+1, ch, ch,
			)
			if s.NextActionTitle != "" {
				task += fmt.Sprintf("。当前大纲标题必须原样使用《%s》", s.NextActionTitle)
			}
			if s.NextActionCoreEvent != "" {
				task += "；核心事件：" + s.NextActionCoreEvent
			}
			if s.NextActionHook != "" {
				task += "；章末钩子：" + s.NextActionHook
			}
			return &Instruction{
				Agent:   "writer",
				Task:    task,
				Reason:  fmt.Sprintf("PendingRewrites 队列剩余 %d 章，计划需先纳入 rewrite_brief 重推演", len(p.PendingRewrites)),
				Chapter: ch,
			}
		}
		return &Instruction{
			Agent:   "drafter",
			Task:    fmt.Sprintf("%s第 %d 章", verb, ch),
			Reason:  fmt.Sprintf("PendingRewrites 队列剩余 %d 章，计划已就绪", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. 审阅中：save_review 刚落盘，verdict 升级/降级由工具层处理，路由不介入
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. 用户干预处理中：Coordinator 正在裁定，Host 不抢占
	if p.Flow == domain.FlowSteering {
		return nil
	}

	// 6. 每个已完成章节都必须先通过章级审阅，再允许续写或完结。
	if s.UnreviewedChapter > 0 {
		return &Instruction{
			Agent:   "editor",
			Task:    fmt.Sprintf("对第 %d 章做章级审阅（scope=chapter）；必须核对章节契约、角色一致性、资源账本、AI 味/aigc_report、语意困惑度和标点功能。只有 save_review verdict=accept 后才允许续写、弧末处理或完结", s.UnreviewedChapter),
			Reason:  "存在已完成章节尚未通过章级审阅",
			Chapter: s.UnreviewedChapter,
		}
	}

	// 6-10. 分层模式的弧末后处理
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("对第 %d 卷第 %d 弧做弧级评审（scope=arc）", b.Volume, b.Arc),
				Reason: "弧末评审未完成",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷第 %d 弧摘要（save_arc_summary）", b.Volume, b.Arc),
				Reason: "弧摘要未完成",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷卷摘要（save_volume_summary）", b.Volume),
				Reason: "卷摘要未完成",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("展开第 %d 卷第 %d 弧（save_foundation type=expand_arc）", b.NextVolume, b.NextArc),
				Reason: "下一弧骨架待展开",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "评估后调用 save_foundation type=append_volume（继续写）或 type=complete_book（全书结束）",
				Reason: "卷末需决定追加新卷或结束全书",
			}
		}
	}

	if !p.Layered && s.BookCompleteByChapters && s.NeedsFinalGlobalReview && !s.HasFinalGlobalReview {
		return &Instruction{
			Agent:   "editor",
			Task:    fmt.Sprintf("对全书做短篇完稿审阅（scope=global，chapter=%d）。先用 read_chapter(source=final, from=1, to=%d, max_runes=40000) 读取完整终稿；按完整正文检查结构闭合、承诺兑现、角色弧线、伏笔回收、AI 味和标点。accept 后系统会汇总正文.md 并推进 complete；polish/rewrite 必须只列出确需返工的 affected_chapters", s.LastCompleted, s.LastCompleted),
			Reason:  "短篇/三万字内项目需要全文汇总终审",
			Chapter: s.LastCompleted,
		}
	}
	if !p.Layered && s.BookCompleteByChapters {
		return nil
	}

	// 12. 正常续写
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	// 阶段拆分：计划未落盘 → planner 先推演；已落盘 → drafter 渲染正文。
	if !s.NextActionPlanReady {
		task := fmt.Sprintf("为第 %d 章做写前推演，落盘完整章节计划", next)
		if s.NextActionTitle != "" {
			task += fmt.Sprintf("；计划标题必须原样使用《%s》", s.NextActionTitle)
		}
		if s.NextActionCoreEvent != "" {
			task += "；核心事件：" + s.NextActionCoreEvent
		}
		if s.NextActionHook != "" {
			task += "；章末钩子：" + s.NextActionHook
		}
		return &Instruction{
			Agent:   "writer",
			Task:    task,
			Reason:  "下一章计划待推演",
			Chapter: next,
		}
	}
	return &Instruction{
		Agent:   "drafter",
		Task:    fmt.Sprintf("写第 %d 章", next),
		Reason:  "下一章计划已就绪，渲染正文",
		Chapter: next,
	}
}

// FormatMessage 把 Instruction 格式化为发给 Coordinator 的用户消息。
// 格式固定，便于 Coordinator prompt 识别与 LLM 直接响应。
func FormatMessage(i *Instruction) string {
	return fmt.Sprintf(
		"[Host 下达指令]\n下一步：调用 subagent(%s, %q)\nagent: %s\ntask: %q\n理由：%s\n这是流程层的明确指令，请立即执行；subagent 的 agent/task 参数必须原样使用上面的 agent/task，不要改写 task，不要先调 novel_context，不要先输出推理。",
		i.Agent, i.Task, i.Agent, i.Task, i.Reason,
	)
}
