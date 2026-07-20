// Package flow 实现垂类路由：Host 根据事实决定下一个调哪个子代理做什么。
//
// 设计原则：
//   - Route 是纯函数：输入 State，输出 *Instruction。无 IO、无 Store 调用，可单测。
//   - State 由 LoadState（非纯）从 Store 构造，一次性把路由需要的事实读齐。
//   - 返回 nil 是合法的：表示"裁定场景，让 Coordinator LLM 自主决策"。
//
// Router 覆盖的是"查表型"决策（每章下一步、legacy 弧末后处理、队列驱动），
// 不覆盖"语义理解型"决策（选规划师、处理用户 Steer、输出总结）。
// sealed_two_pass_v2 的正文验收由外层 pipeline 按章完成；这里保留的
// scope=arc 只服务未迁移工程的结构复盘，绝不能替代章级 exact-body review。
package flow

import (
	"fmt"
	"strings"

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
	// Pipeline render execution is a frozen, chapter-scoped handoff. Its first
	// Drafter may only materialize one whole-body draft; exact-hash/static/provider
	// gates belong to the outer pipeline before any finalizer can run.
	NextActionPipelineRender bool
	// 同一 plan 下多个不同整章哈希反复触发 whole-text/segment 阻断时，
	// render-only 已耗尽，必须优先重做世界推演与 POV plan。
	NextActionStructuralReplanRequired bool
	NextActionStructuralReplanAttempts int
	NextActionStructuralReplanLimit    int
	NextActionStructuralReplanReason   string
	// 单世界全角色推演先于 POV plan。需要且未完成时，Host 派受限的
	// world_simulator；完成后才派 writer 规划主视角。
	NextActionWorldSimulationRequired bool
	NextActionWorldSimulationReady    bool
	NextActionWorldSimulationGaps     []string
	// 已有 staged plan partial 时，Router 必须优先派最小补丁修复，不能退化成
	// 普通“从头重做计划”提示，否则 Planner 会重复已完成批次。
	NextActionPlanPartial    bool
	NextActionPlanRepairTask string
	// 当前 plan 之后已经生成过 draft 时，恢复流程只做局部验收与提交，
	// 不应再次整章抽样覆盖；DeepSeek provider judge 或用户当前哈希抽查高分
	// 明确要求结构级重渲染时例外。
	NextActionDraftReady                    bool
	NextActionExplicitRerender              bool
	NextActionReviewRerenderRequired        bool
	NextActionDraftExternalRerenderRequired bool
	// A full rerender has consumed the blocking judgment's one-use hash token.
	// Host must stop prose dispatch until the outer pipeline judges the new hash.
	NextActionDraftExternalRejudgePending bool
	// The effective local gate has a deterministic non-whole-text failure. Host
	// may dispatch one targeted edit either before the first provider call or when
	// recovering an older passing hash; the changed final hash always returns to
	// DeepSeek before commit or an automated-hard retest.
	NextActionDraftLocalSoftEditPending bool
	NextActionDraftLocalSoftBeforeJudge bool
	// An explicitly configured automated detector/mode gate has passed for the
	// exact current draft hash. User-reported spot checks never set this state.
	NextActionDraftNamedPassFrozen bool

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
//  7. legacy 弧末结构复盘缺失  → editor(arc review; never chapter acceptance)
//  8. legacy 弧末复盘有但摘要缺失 → editor(arc summary)
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
		if s.NextActionDraftExternalRejudgePending && s.NextActionPlanReady && s.NextActionDraftReady && !s.NextActionExplicitRerender && !s.NextActionStructuralReplanRequired {
			// The outer pipeline owns the DeepSeek provider judge. Returning nil
			// lets the current headless run finish instead of dispatching another
			// prose agent against an unreviewed hash. A stale causal plan must take
			// the simulation/planning branch below first: an explicitly configured
			// automated_hard obligation applies to the eventual replacement, not
			// obsolete bytes. User-reported platform samples create no such duty.
			// A newer explicit rerender request has already superseded that hash and
			// may proceed directly.
			return nil
		}
		verb := "重写"
		if p.Flow == domain.FlowPolishing {
			verb = "打磨"
		}
		if !s.NextActionPlanReady {
			if task := strings.TrimSpace(s.NextActionPlanRepairTask); strings.HasPrefix(task, "Pipeline convergence replan") {
				return &Instruction{
					Agent: "writer", Task: task,
					Reason:  fmt.Sprintf("第 %d 章 exhausted sealed render 只允许定向建立 successor plan", ch),
					Chapter: ch,
				}
			}
			if s.NextActionStructuralReplanRequired || (s.NextActionWorldSimulationRequired && !s.NextActionWorldSimulationReady) {
				task := worldSimulationTask(ch, s.NextActionWorldSimulationGaps)
				reason := fmt.Sprintf("第 %d 章必须先完成单世界全角色推演", ch)
				if s.NextActionStructuralReplanRequired {
					task += "。render-only 已连续结构失败，禁止沿用旧推演和旧 plan；新推演必须保留事实结果但重组场景因果。"
					reason = s.NextActionStructuralReplanReason
				}
				return &Instruction{Agent: "world_simulator", Task: task, Reason: reason, Chapter: ch}
			}
			if s.NextActionPlanPartial {
				task := s.NextActionPlanRepairTask
				if task == "" {
					task = fmt.Sprintf("Pipeline staged-plan repair：第%d章已有 plan.partial。只调用 novel_context(chapter=%d) 一次，然后严格按 chapter_plan_stage.gap_summary 用 plan_details 提交最小缺口补丁；禁止重跑 simulate_chapter_world、plan_structure，禁止重发已完成字段，最后 finalize=true", ch, ch)
				}
				reason := fmt.Sprintf("第 %d 章 staged plan 只需最小补丁修复", ch)
				if strings.HasPrefix(strings.TrimSpace(task), "Pipeline world-simulation repair") {
					reason = fmt.Sprintf("第 %d 章必须先完成全角色世界推演", ch)
					return &Instruction{
						Agent:   "world_simulator",
						Task:    task,
						Reason:  reason,
						Chapter: ch,
					}
				}
				return &Instruction{
					Agent:   "writer",
					Task:    task,
					Reason:  reason,
					Chapter: ch,
				}
			}
			task := fmt.Sprintf(
				"返工目标锁定第 %d 章：按本章 rewrite_brief 重做写前推演计划（%s前）。该章已在 completed_chapters、进度游标指向下一章都属于正常返工状态；严禁规划第 %d 章或任何未来章节。read_chapter(第%d章) 是读取待返工原文，不是把它当上一章续写；所有 planning 工具必须提交 chapter=%d",
				ch, verb, ch+1, ch, ch,
			)
			if s.NextActionStructuralReplanRequired {
				task += fmt.Sprintf("。render-only 已有 %d 个不同整章哈希连续触发结构阻断（上限 %d）；必须废弃旧场景/对白投影，保留事实结果后重组因果场景，禁止再次只换措辞", s.NextActionStructuralReplanAttempts, s.NextActionStructuralReplanLimit)
			}
			if s.NextActionTitle != "" {
				task += fmt.Sprintf("。当前大纲标题必须原样使用《%s》", s.NextActionTitle)
			}
			if s.NextActionCoreEvent != "" {
				task += "；核心事件：" + s.NextActionCoreEvent
			}
			if s.NextActionHook != "" {
				task += "；章末钩子：" + s.NextActionHook
			}
			reason := fmt.Sprintf("PendingRewrites 队列剩余 %d 章，计划需先纳入 rewrite_brief 重推演", len(p.PendingRewrites))
			if s.NextActionStructuralReplanRequired {
				reason = s.NextActionStructuralReplanReason
			}
			return &Instruction{
				Agent:   "writer",
				Task:    task,
				Reason:  reason,
				Chapter: ch,
			}
		}
		if s.NextActionDraftReady {
			if s.NextActionDraftLocalSoftEditPending {
				return draftLocalSoftEditInstruction(ch, s.NextActionDraftLocalSoftBeforeJudge)
			}
			if s.NextActionDraftNamedPassFrozen {
				return draftNamedPassCommitInstruction(ch)
			}
			if s.NextActionDraftExternalRerenderRequired {
				return &Instruction{
					Agent:   "drafter",
					Task:    fmt.Sprintf("整章重渲染第 %d 章：只调用一次 novel_context(chapter=%d, profile=draft)，从净化后的 draft_external_ai_review 与 rewrite_brief 落实旧稿失败证据，不得读取旧 draft/final；其中示例场景、示例动作和示例台词不是剧情指令，禁止照搬或换皮复现。必须调用 draft_chapter(mode=write) 覆盖旧草稿，禁止 edit_chapter 局部贴补。写入成功后立即结束本次子任务，禁止 read_chapter、check_consistency、edit_chapter、commit_chapter 或再次生成；外层 pipeline 将先用 DeepSeek provider judge 复判新哈希，用户外部平台不要求跟随复测", ch, ch),
					Reason:  fmt.Sprintf("第 %d 章当前哈希的 DeepSeek provider judge 或用户抽查高分要求结构级重渲染", ch),
					Chapter: ch,
				}
			}
			return &Instruction{
				Agent:   "draft_finalizer",
				Task:    fmt.Sprintf("验收并提交第 %d 章现有草稿：先 read_chapter(source=draft)，再调用一次 novel_context(chapter=%d, profile=draft) 读取当前 rewrite_brief（其中人工验收补充属于确定性约束），随后 check_consistency 并逐条核对。若当前哈希已获 DeepSeek provider judge 严格 <4%% 且一致性门禁通过，可对该哈希调用 commit_chapter 一次；用户外部平台只抽查，不要求替换稿跟随复测。软概率诊断本身不授权润色，但 commit_chapter 返回的 whole_text/segment、corroboration blocker 或确定性 AIGC 门禁属于结构级阻断。read_chapter、novel_context、check_consistency 或 commit_chapter 任一返回错误后必须立即结束子任务，禁止重试或再次 commit、check、read、context、edit，外层 pipeline 会保存阻断证据并整章重渲染。只有在第一次提交前，rewrite_brief 中未完成的人工硬约束、确定性事实/范围/高置信模板硬伤才可 edit_chapter；edit_chapter 无论成功或返回错误都必须立即结束，禁止第二次调用，交外层 DeepSeek provider judge 复判新哈希。禁止重新整章生成", ch, ch),
				Reason:  fmt.Sprintf("第 %d 章已有绑定当前 plan 的草稿，恢复时只需验收提交", ch),
				Chapter: ch,
			}
		}
		if s.NextActionReviewRerenderRequired {
			return &Instruction{
				Agent: "drafter",
				Task: fmt.Sprintf(
					"正式复审要求第 %d 章产生新稿：只调用一次 novel_context(chapter=%d, profile=draft)，复用已批准的 world simulation 与 POV plan，逐条落实当前 rewrite_brief；不得读取旧 draft/final 或复用旧稿表面。随后调用 draft_chapter(mode=write) 一次整章覆盖；禁止 simulate_chapter_world、plan_structure、plan_details、edit_chapter、check_consistency 或 commit_chapter。写入新哈希后立即结束，交外层 pipeline 做 DeepSeek provider judge 裸正文判定",
					ch, ch,
				),
				Reason:  fmt.Sprintf("第 %d 章正式 rewrite 结论仍对应当前终稿，必须先生成不同哈希的新草稿", ch),
				Chapter: ch,
			}
		}
		if s.NextActionExplicitRerender {
			return &Instruction{
				Agent: "drafter",
				Task: fmt.Sprintf(
					"显式整章重渲染第 %d 章：只调用一次 novel_context(chapter=%d, profile=draft)，复用已经批准的 world simulation 与 POV plan；忽略其中已声明为 source-version-only 的旧正文/brief 哈希差，不得读取旧 draft/final 或复用旧稿表面。随后必须调用 draft_chapter(mode=write) 一次覆盖旧草稿，禁止调用 simulate_chapter_world、plan_structure、plan_details、edit_chapter、check_consistency 或 commit_chapter。新草稿写入后立即结束，交由外层 pipeline 做 DeepSeek provider judge 同哈希判定",
					ch, ch,
				),
				Reason:  fmt.Sprintf("第 %d 章收到显式 render-only 请求，旧草稿已失效", ch),
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

	// An explicitly short project may still use one layered volume/arc as its
	// sealed planning container. Once every chapter has an exact-body review, its
	// whole-book review takes precedence over legacy arc summaries or new-volume
	// expansion: this is a terminal short book, not the first arc of a long book.
	if s.BookCompleteByChapters && s.NeedsFinalGlobalReview && !s.HasFinalGlobalReview {
		return &Instruction{
			Agent: "editor",
			Task: fmt.Sprintf(
				"对全书做短篇完稿审阅（scope=global，chapter=%d）。先用 read_chapter(source=final, from=1, to=%d, max_runes=40000) 读取完整终稿；按完整正文检查结构闭合、承诺兑现、角色弧线、伏笔回收、AI 味和标点。accept 时 save_review 必须同时提交 publication：正式主标题、2—3 个备选书名、15—80 字冲突导语、无剧透简介、恰好 5 个标签；这些文案必须由正文事实生成，不得泄露真凶、终局反转或结局。accept 后系统会汇总正文.md 并推进 complete；polish/rewrite 必须只列出确需返工的 affected_chapters",
				s.LastCompleted,
				s.LastCompleted,
			),
			Reason:  "短篇/三万字内项目需要全文汇总终审和正式发布文案",
			Chapter: s.LastCompleted,
		}
	}

	// 6-10. 未迁移分层工程的 legacy 弧末后处理。sealed_two_pass_v2 在
	// 外层 render 中逐章验收，并以 arc completion receipt 聚合完整性；
	// 此处 scope=arc 只是结构复盘，不能成为正文 acceptance。
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("对第 %d 卷第 %d 弧做 legacy 结构复盘（scope=arc；不得替代任何章级正文审核）", b.Volume, b.Arc),
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

	if !p.Layered && s.BookCompleteByChapters {
		return nil
	}

	// 12. 正常续写
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if s.NextActionDraftExternalRejudgePending && s.NextActionPlanReady && s.NextActionDraftReady && !s.NextActionStructuralReplanRequired {
		return nil
	}
	// 阶段拆分：计划未落盘 → planner 先推演；已落盘 → drafter 渲染正文。
	if !s.NextActionPlanReady {
		if task := strings.TrimSpace(s.NextActionPlanRepairTask); strings.HasPrefix(task, "Pipeline convergence replan") {
			return &Instruction{
				Agent: "writer", Task: task,
				Reason:  fmt.Sprintf("第 %d 章 exhausted sealed render 只允许定向建立 successor plan", next),
				Chapter: next,
			}
		}
		if s.NextActionStructuralReplanRequired || (s.NextActionWorldSimulationRequired && !s.NextActionWorldSimulationReady) {
			task := worldSimulationTask(next, s.NextActionWorldSimulationGaps)
			reason := fmt.Sprintf("第 %d 章必须先完成单世界全角色推演", next)
			if s.NextActionStructuralReplanRequired {
				task += "。render-only 已连续结构失败，禁止沿用旧推演和旧 plan；新推演必须保留事实结果但重组场景因果。"
				reason = s.NextActionStructuralReplanReason
			}
			return &Instruction{Agent: "world_simulator", Task: task, Reason: reason, Chapter: next}
		}
		if s.NextActionPlanPartial {
			task := s.NextActionPlanRepairTask
			if task == "" {
				task = fmt.Sprintf("Pipeline staged-plan repair：第%d章已有 plan.partial。只调用 novel_context(chapter=%d) 一次，然后严格按 chapter_plan_stage.gap_summary 用 plan_details 提交最小缺口补丁；禁止重跑 simulate_chapter_world、plan_structure，禁止重发已完成字段，最后 finalize=true", next, next)
			}
			return &Instruction{Agent: "writer", Task: task, Reason: "下一章 staged plan 只需最小补丁修复", Chapter: next}
		}
		task := fmt.Sprintf("为第 %d 章做写前推演，落盘完整章节计划", next)
		if s.NextActionStructuralReplanRequired {
			task += fmt.Sprintf("；render-only 已有 %d 个不同整章哈希连续触发结构阻断（上限 %d），必须废弃旧场景/对白投影，保留事实结果后重组因果场景", s.NextActionStructuralReplanAttempts, s.NextActionStructuralReplanLimit)
		}
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
	if s.NextActionDraftReady {
		if s.NextActionDraftLocalSoftEditPending {
			return draftLocalSoftEditInstruction(next, s.NextActionDraftLocalSoftBeforeJudge)
		}
		if s.NextActionDraftNamedPassFrozen {
			return draftNamedPassCommitInstruction(next)
		}
		if s.NextActionDraftExternalRerenderRequired {
			return &Instruction{
				Agent:   "drafter",
				Task:    fmt.Sprintf("整章重渲染第 %d 章：只调用一次 novel_context(chapter=%d, profile=draft)，从净化后的 draft_external_ai_review 与 rewrite_brief 落实旧稿失败证据，不得读取旧 draft/final；其中示例场景、示例动作和示例台词不是剧情指令，禁止照搬或换皮复现。必须调用 draft_chapter(mode=write) 覆盖旧草稿，禁止 edit_chapter 局部贴补。写入成功后立即结束本次子任务，禁止 read_chapter、check_consistency、edit_chapter、commit_chapter 或再次生成；外层 pipeline 将先用 DeepSeek provider judge 复判新哈希，用户外部平台不要求跟随复测", next, next),
				Reason:  "当前哈希的 DeepSeek provider judge 或用户抽查高分要求结构级重渲染",
				Chapter: next,
			}
		}
		return &Instruction{
			Agent:   "draft_finalizer",
			Task:    fmt.Sprintf("验收并提交第 %d 章现有草稿：先 read_chapter(source=draft)，再调用一次 novel_context(chapter=%d, profile=draft)，随后 check_consistency 并逐条核对。若当前哈希已获 DeepSeek provider judge 严格 <4%% 且一致性门禁通过，只可调用 commit_chapter 一次。read_chapter、novel_context、check_consistency 或 commit_chapter 任一返回错误后必须立即结束子任务，禁止重试或再次 commit、check、read、context、edit，外层 pipeline 会根据已落盘阻断证据路由局部编辑或整章重渲染。只有在第一次提交前发现确定性事实/范围/高置信模板硬伤时才可 edit_chapter；edit_chapter 无论成功或返回错误都必须立即结束，禁止第二次调用，交外层 DeepSeek provider judge 复判新哈希。用户外部平台只抽查，不要求替换稿跟随复测。禁止重新整章生成", next, next),
			Reason:  "已有绑定当前 plan 的草稿，恢复时只需验收提交",
			Chapter: next,
		}
	}
	if s.NextActionPipelineRender {
		return pipelineRenderFirstDraftInstruction(next)
	}
	return &Instruction{
		Agent:   "drafter",
		Task:    fmt.Sprintf("写第 %d 章", next),
		Reason:  "下一章计划已就绪，渲染正文",
		Chapter: next,
	}
}

func pipelineRenderFirstDraftInstruction(chapter int) *Instruction {
	return &Instruction{
		Agent: "drafter",
		Task: fmt.Sprintf(
			"冻结 render 第 %d 章首稿：Host 会在首个真实 provider 调用前等价完成本章唯一一次 novel_context(chapter=%d, profile=draft)，校验 exact render_packet v11 与 anti_ai_render_contract，并通过 server-owned envelope 预注入；Drafter 工具集中没有 novel_context，禁止请求或尝试再次调用。首个响应直接只调用一次 draft_chapter(chapter=%d, mode=write) 写入完整正文。draft_chapter 返回 written=true 后立即结束本次 Drafter 子任务；禁止 read_chapter、check_consistency、edit_chapter、commit_chapter、draft_chapter_part、merge_chapter_parts、再次生成或派 draft_finalizer。即使工具返回字数、AIGC 或其他门禁提示，也禁止在本会话修补。控制权必须先交还外层 pipeline，由其依次核验当前精确草稿哈希、因果绑定与 hard-fact/title/word 静态门，并完成该精确哈希的 DeepSeek provider judge；只有这些门禁完成后，后续 Host turn 才可恢复验收或提交",
			chapter, chapter, chapter,
		),
		Reason:  "pipeline render 首次正文落盘必须在 exact-hash/static/provider 门禁前立即暂停",
		Chapter: chapter,
	}
}

func draftLocalSoftEditInstruction(chapter int, beforeJudge bool) *Instruction {
	phase := "当前精确哈希已通过 DeepSeek provider judge，但"
	reason := fmt.Sprintf("第 %d 章 DeepSeek provider judge 已通过，允许消费一次本地软门禁编辑", chapter)
	if beforeJudge {
		phase = "当前精确哈希在 provider judge 之前已命中确定性文本规则，且"
		reason = fmt.Sprintf("第 %d 章先消费一次确定性本地软门禁编辑，只判定最终新哈希", chapter)
	}
	return &Instruction{
		Agent: "draft_finalizer",
		Task: fmt.Sprintf(
			"第 %d 章%s本地有效 AIGC 门禁仍有非 whole-text/segment 的确定性失败：先 read_chapter(source=draft)，再调用一次 novel_context(chapter=%d, profile=draft) 并运行 check_consistency，严格依据 aigc_gate_check.rewrite_focus 只选择一处可验证的局部硬伤。read_chapter、novel_context 或 check_consistency 任一返回错误时立即结束，禁止重试。最多调用一次 edit_chapter；不得整章重写、不得调用 commit_chapter。edit_chapter 无论成功或返回错误都必须立即结束，禁止第二次调用；外层 pipeline 只会用 DeepSeek provider judge 判定新哈希（首次前移时即修改后的最终哈希）。用户外部抽查不作为后续放行前置",
			chapter, phase, chapter,
		),
		Reason:  reason,
		Chapter: chapter,
	}
}

func draftNamedPassCommitInstruction(chapter int) *Instruction {
	return &Instruction{
		Agent: "draft_finalizer",
		Task: fmt.Sprintf(
			"第 %d 章当前精确哈希已通过显式配置 automated_hard 的自动 detector/mode 严格 <4%% 门禁，载荷已经冻结。只可 read_chapter(source=draft)、novel_context(chapter=%d, profile=draft) 与 check_consistency 核验同一载荷；全部门禁通过后最多调用一次 commit_chapter。禁止 edit_chapter、draft_chapter 或任何正文改写。用户手工抽查不会进入此状态。若 consistency/commit 暴露新的确定性阻断，立即结束子任务并交外层产生新的显式整章重渲染授权",
			chapter, chapter,
		),
		Reason:  fmt.Sprintf("第 %d 章当前哈希已通过显式 automated_hard 自动门禁，只允许原样提交", chapter),
		Chapter: chapter,
	}
}

func worldSimulationTask(chapter int, gaps []string) string {
	task := fmt.Sprintf("为第 %d 章完成单世界全角色推演：只调用一次 novel_context(chapter=%d, profile=world_simulation)，再按 gaps 分批调用 simulate_chapter_world；覆盖每个实名角色的选项、决定、理由、行动与蝴蝶效应，最后提交 protagonist_projection 并 finalize=true。禁止调用 planning 或正文工具", chapter, chapter)
	if len(gaps) > 0 {
		limit := len(gaps)
		if limit > 8 {
			limit = 8
		}
		task += "。当前缺口：" + strings.Join(gaps[:limit], "；")
	}
	return task
}

// FormatMessage 把 Instruction 格式化为发给 Coordinator 的用户消息。
// 格式固定，便于 Coordinator prompt 识别与 LLM 直接响应。
func FormatMessage(i *Instruction) string {
	return fmt.Sprintf(
		"[Host 下达指令]\n下一步：调用 subagent(%s, %q)\nagent: %s\ntask: %q\n理由：%s\n这是流程层的明确指令，请立即执行；subagent 的 agent/task 参数必须原样使用上面的 agent/task，不要改写 task，不要先调 novel_context，不要先输出推理。",
		i.Agent, i.Task, i.Agent, i.Task, i.Reason,
	)
}
