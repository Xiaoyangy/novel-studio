package reminder

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// subagentMaxConsecutiveBlocks 连续阻拦 N 次后升级为终止，避免弱模型死循环。
const subagentMaxConsecutiveBlocks = 3

// hardStopReasons 是无法用催促消息恢复的 provider 端拒答原因。注入
// "必须 commit" 对它们无效，反而每次产生一次完整 LLM 调用的 token 消耗，
// 并最终升级 escalate 后让 coordinator 重派整个 SubAgent，叠加多倍浪费
// （实测 ch02 撞 safety 时一次写章产生 3 次重派 17 次 LLM 调用、命中率
// 从 50% 跌到 2.8%）。
//
// 注意 StopReasonError / StopReasonAborted 不需要列入：agentcore 在
// loop.go 收到这两种 stop reason 时直接终止 run，根本不会调用 StopGuard。
// 这里只列那些会真正走到 StopGuard 的 provider 拒答语义。
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard 构造一个 StopGuard：
// 在 baseline 之后若未出现指定 step 的 checkpoint，则拒绝 end_turn。
// baseline 由调用方在 factory 时刻捕获，保证 per-run 语义正确。
// HARNESS-METADATA: name=subagent_checkpoint_delta_guard class=model_gap review=2027-Q1
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg string) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// 不可恢复错误：直接升级，不浪费一次催促。
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard 检测到不可恢复停机，立即升级",
				"module", "host.reminder", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// 倒序扫描：新 checkpoint 在尾部，遇到 <= baseline 即可 break。
		all := st.Checkpoints.All()
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if _, ok := need[cp.Step]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard 连续阻拦超限，升级为终止",
				"module", "host.reminder", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard 拦截 end_turn",
			"module", "host.reminder", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg}
	}
}

// NewWriterStopGuard 要求 writer（正文渲染阶段/drafter）本轮至少产生一次 commit_chapter。
// HARNESS-METADATA: name=writer_stop_guard class=model_gap review=2027-Q1
func NewWriterStopGuard(st *store.Store) agentcore.StopGuard {
	baseline := latestCheckpointSeq(st)
	base := newCheckpointDeltaGuard(st, "writer",
		[]string{"commit"},
		"你必须调用 commit_chapter 提交本章后才能结束。draft_chapter / draft_chapter_part / merge_chapter_parts 只是保存草稿，不算完成；分片写完要先合并、回读、check_consistency，再 commit_chapter。",
	)
	localSoftEdit := newCheckpointDeltaGuard(st, "writer_local_soft_edit",
		[]string{"edit"},
		"当前精确哈希已通过 DeepSeek，但本地非结构性 AIGC 门禁仍未通过。必须严格按 check_consistency 返回的 rewrite_focus 调用 edit_chapter 一次；禁止 commit 或整章重写。edit 落盘后立即结束，交外层 pipeline 复判新哈希。",
	)
	return func(ctx context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		if draftPauseBoundaryReached(st, baseline) {
			return agentcore.StopDecision{Allow: true}
		}
		if currentDraftLocalSoftEditPending(st) {
			return localSoftEdit(ctx, info)
		}
		return base(ctx, info)
	}
}

func latestCheckpointSeq(st *store.Store) int64 {
	if st != nil {
		if cp := st.Checkpoints.LatestGlobal(); cp != nil {
			return cp.Seq
		}
	}
	return 0
}

// draftPauseBoundaryReached allows a writer/coordinator run to end at either a
// provider rejudge boundary or a *new* deterministic whole-text structural
// block produced in this run. An old rerender_authorized marker at factory time
// is deliberately insufficient: the writer still has to consume that one-shot
// authorization and emit a different draft before control returns to Host.
func draftPauseBoundaryReached(st *store.Store, structuralBaseline int64) bool {
	if awaitingDraftExternalRejudge(st) {
		return true
	}
	chapter := activeDraftChapter(st)
	if chapter <= 0 {
		return false
	}
	cp := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "draft-structural-block")
	return cp != nil && cp.Seq > structuralBaseline && tools.CurrentDraftHasLocalStructuralBlock(st, chapter)
}

func activeDraftChapter(st *store.Store) int {
	if st == nil {
		return 0
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return 0
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0]
	}
	return progress.NextChapter()
}

func awaitingDraftExternalRejudge(st *store.Store) bool {
	chapter := activeDraftChapter(st)
	if chapter <= 0 {
		return false
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	return err == nil && !inspection.LocalSoftEditPending && (inspection.Status == tools.DraftExternalGateRejudgePending ||
		inspection.Status == tools.DraftExternalGateAdviceIncomplete)
}

func currentDraftLocalSoftEditPending(st *store.Store) bool {
	chapter := activeDraftChapter(st)
	if chapter <= 0 {
		return false
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	return err == nil && inspection.LocalSoftEditPending
}

// NewPlannerStopGuard 要求推演阶段（planner）本轮至少落盘一次章节计划（plan checkpoint）。
// 阶段拆分后 planner 不 commit，收敛信号是计划落盘，不是正文提交。
// HARNESS-METADATA: name=planner_stop_guard class=model_gap review=2027-Q1
func NewPlannerStopGuard(st *store.Store) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "planner",
		[]string{"plan"},
		"你必须调用 plan_chapter（或两阶段 plan_structure + plan_details finalize=true）把本章计划落盘后才能结束。只输出计划文字等于没落盘。",
	)
}

// NewWorldSimulatorStopGuard requires the dedicated simulation checkpoint.
// It deliberately does not accept a plan checkpoint: this agent must finish
// the world before a separate planner projects the POV chapter.
func NewWorldSimulatorStopGuard(st *store.Store) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "world_simulator",
		[]string{"chapter_world_simulation"},
		"你必须继续调用 simulate_chapter_world，按 gaps 分批补齐角色决定、rewrite_fact_coverage 和 protagonist_projection，直到返回 simulated=true 后才能结束。",
	)
}

// NewArchitectStopGuard 要求 architect 本轮至少落盘一次 save_foundation。
// HARNESS-METADATA: name=architect_stop_guard class=model_gap review=2027-Q1
func NewArchitectStopGuard(st *store.Store) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"premise", "outline", "layered_outline", "characters", "world_rules", "book_world",
			"expand_arc", "append_volume", "update_compass", "complete_book",
			// 开局/弧末 world_tick 也是 architect 的合法收尾产物：被派去只跑 save_world_tick
			// （第 1 章前的离屏世界推演、或弧末推进）时，落盘 world_tick 即算完成，否则
			// stop_guard 会因"没产出 foundation 类 checkpoint"连拦到升级终止（虽能重派恢复但浪费轮次）。
			"world_tick",
		},
		"你必须调用 save_foundation / save_world_tick 将产出落盘后才能结束。只输出 Markdown/JSON 文字等于丢失。",
	)
}

// NewEditorStopGuard 要求 editor 本轮落盘与"任务"匹配的产物后才能结束。
//
// 任务感知：被派去生成摘要时，仅 save_review（复核）不算完成——必须产出对应摘要。
// 否则"被派生成弧摘要却先复核"的 editor 会满足旧的宽松判据提前结束，弧摘要永不落盘
// （配合 dispatcher 去重哑火曾导致卷中骨架弧死循环，详见 outline-exhaustion-livelock）。
// StopAfterTool 退出会绕过 StopGuard（loop.go），故 build.go 同步把 save_review 移出硬停，
// 让复核后能继续走到摘要工具，再由本 guard 把关收尾。
// HARNESS-METADATA: name=editor_stop_guard class=model_gap review=2027-Q1
func NewEditorStopGuard(st *store.Store, task string) agentcore.StopGuard {
	switch {
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "卷摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			"本次任务是生成卷摘要：你必须调用 save_volume_summary 落盘后才能结束，save_review 复核不算完成。")
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "弧摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			"本次任务是生成弧摘要：你必须调用 save_arc_summary 落盘后才能结束，save_review 复核不算完成。")
	default:
		// 评审或临时任务：任一审阅/摘要落盘即可（保持既有宽松行为）。
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			"你必须调用 save_review / save_arc_summary / save_volume_summary 之一落盘结果后才能结束。")
	}
}
