package reminder

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host/flow"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// StopGuard 是"物理不可停机"的最后防线。
// 当 LLM 试图 end_turn 时：
//   - Progress.Phase = Complete → 放行
//   - 否则注入 user message，让 agent 继续下一 turn
//   - 连续阻拦超过 maxConsecutive 次 → Escalate 终止 run（说明 prompt/reminder 严重失灵）
//
// Guard 内部维护 consecutive block 计数；一旦成功放行或成功注入后重置为 0。
// 真正驱动 Coordinator 行为的是 Reminder + Prompt，StopGuard 只是兜底。
const maxConsecutiveBlocks = 5

// NewStopGuard 构造 Coordinator 专用 StopGuard。
// onBlock 可选，非 nil 时每次阻拦调一次，用于审计。
// HARNESS-METADATA: name=coordinator_stop_guard class=business_logic note=全书未完不许收工的业务不变量
func NewStopGuard(st *store.Store, onBlock func(reason string, consecutive int32)) agentcore.StopGuard {
	var consecutive atomic.Int32
	var lastBlockTurn atomic.Int64 // 上次 block 的 TurnIndex；-1 表示尚未 block 过
	lastBlockTurn.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		progress, _ := st.Progress.Load()
		if progress != nil && progress.Phase == domain.PhaseComplete {
			consecutive.Store(0)
			lastBlockTurn.Store(-1)
			return agentcore.StopDecision{Allow: true}
		}
		// A full draft rerender intentionally pauses before consistency/commit so
		// the outer pipeline can judge the new body hash. Treat this as a normal
		// host boundary, not an unfinished-book failure loop.
		if awaitingDraftExternalRejudge(st) {
			slog.Info("stop_guard 放行 end_turn：等待草稿新哈希外判",
				"module", "host.reminder", "turn", info.TurnIndex)
			consecutive.Store(0)
			lastBlockTurn.Store(-1)
			return agentcore.StopDecision{Allow: true}
		}
		// 零章卡点场景放行：foundation 已齐但第 1 章的 zero-init 未就绪时，
		// writer 派发会被 writerZeroInitGate 拒绝，Coordinator 在会话内没有合法下一步——
		// 必须允许收工，交还宿主 pipeline 自动执行 --zero-init 后续跑。
		if tools.FoundationCoreComplete(st.Dir()) && tools.EnsureZeroInitReadyForChapterOne(st) != nil {
			slog.Info("stop_guard 放行 end_turn：等待宿主执行零章初始化",
				"module", "host.reminder", "turn", info.TurnIndex)
			consecutive.Store(0)
			lastBlockTurn.Store(-1)
			return agentcore.StopDecision{Allow: true}
		}
		// 只有"相邻 turn 连续被拦"才累计计数；否则视为新一轮（LLM 已做过 tool call 取得过进展，
		// 或用户注入 / resume 导致 TurnIndex 倒流），重置计数。
		last := lastBlockTurn.Load()
		if last < 0 || int64(info.TurnIndex) != last+1 {
			consecutive.Store(0)
		}
		lastBlockTurn.Store(int64(info.TurnIndex))
		n := consecutive.Add(1)
		if n > maxConsecutiveBlocks {
			slog.Error("stop_guard 连续阻拦超限，升级为终止",
				"module", "host.reminder", "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock("escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		inject := blockMessage(st, progress)
		if progress != nil && len(progress.PendingRewrites) > 0 {
			inject = fmt.Sprintf("禁止结束对话。待重写队列未清：%v，请立即调 writer 处理。", progress.PendingRewrites)
		}
		slog.Warn("stop_guard 拦截 end_turn",
			"module", "host.reminder", "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock("blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: inject}
	}
}

func blockMessage(st *store.Store, progress *domain.Progress) string {
	if progress != nil && flow.Route(flow.LoadState(st)) != nil {
		return "禁止结束对话。Phase 尚未 Complete；请等待并执行 Host 下达的 [Host 下达指令]，不要自行调用 novel_context 或 subagent。"
	}
	return "禁止结束对话。Phase 尚未 Complete，且当前没有 Host 路由指令；这是 Coordinator 裁定场景，请按 coordinator.md 的裁定规则继续处理，不要空等 Host 指令。"
}
