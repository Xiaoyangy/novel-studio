package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	storepkg "github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// Dispatcher 在子代理返回的同步工具边界计算路由并下达 Host 指令。
type Dispatcher struct {
	coordinator *agentcore.Agent
	store       *storepkg.Store

	enabled atomic.Bool // 由 Host 控制是否派发（启动完成前应关）

	// 重复追踪同时记录 durable progress token。相同任务只要落了新 checkpoint
	// 或更新 simulation/plan/draft partial，就视为仍在分步推进；否则按阈值熔断。
	lastMu            sync.Mutex
	lastSent          *Instruction
	lastProgressToken string
	repeats           int

	// onRepeat 是提前告警；onStall 在确认无 durable progress 后触发 Host 中止。
	onRepeat func(agent, task string, n int)
	onStall  func(agent, task string, n int)

	// onWriterDispatch 是同步旁路刷新钩子。Host 用它在 writer 指令已经通过章节校验、
	// 且 Progress.InProgressChapter 已更新后刷新 Writer restore pack；不参与路由裁定。
	onWriterDispatch func(chapter int)
}

const (
	repeatNotifyAt = 2
	stallAbortAt   = 3
)

// NewDispatcher 创建 Dispatcher。
func NewDispatcher(coordinator *agentcore.Agent, store *storepkg.Store) *Dispatcher {
	d := &Dispatcher{coordinator: coordinator, store: store}
	return d
}

// Enable 打开路由派发；关闭时 Dispatch 不产生指令。
// Host 在 Start/Resume 完成首条 prompt 之后启用，避免与启动流程冲突。
func (d *Dispatcher) Enable() { d.enabled.Store(true) }

// Dispatch 立即计算路由并下达指令；可被 Host 在特殊时机（如 Resume 后）主动调用。
func (d *Dispatcher) Dispatch() {
	if !d.enabled.Load() {
		return
	}
	state := LoadState(d.store)
	inst := Route(state)
	if inst == nil {
		return
	}
	progressToken := d.durableProgressToken(inst)
	n, stalled := d.trackRepeat(inst, progressToken)
	if stalled {
		if n == stallAbortAt && d.onStall != nil {
			d.onStall(inst.Agent, inst.Task, n)
		}
		return
	}
	// Writer 任务：在派发同一刻把章节标为进行中，UI 右侧大纲立即反映"▸ 进行中"，
	// 不用等 plan_chapter 真正执行（plan_chapter 会再调一次 StartChapter，幂等）。
	if inst.Agent == "writer" && inst.Chapter > 0 && d.store != nil {
		if err := d.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			slog.Error("flow router refuses invalid writer dispatch", "module", "host.flow", "chapter", inst.Chapter, "err", err)
			return
		}
		if err := d.store.Progress.StartChapter(inst.Chapter); err != nil {
			slog.Warn("flow router pre-mark in-progress failed", "module", "host.flow", "chapter", inst.Chapter, "err", err)
		}
		if d.onWriterDispatch != nil {
			d.onWriterDispatch(inst.Chapter)
		}
	}
	msg := formatDispatchMessage(inst, n)
	slog.Debug("flow router dispatch", "module", "host.flow", "agent", inst.Agent, "reason", inst.Reason, "repeat", n)
	d.coordinator.Steer(agentcore.UserMsg(msg))
}

// formatDispatchMessage 组装下达给 Coordinator 的指令消息。
// n>1 时附加重复事实——告知"上次派发后路由事实未变化"并放开核对许可，
// 让 LLM 自己裁定照常执行还是改派；不在 Host 层做任何强制分支。
func formatDispatchMessage(inst *Instruction, n int) string {
	msg := FormatMessage(inst)
	if n > 1 {
		msg += fmt.Sprintf("\n（注意：本指令为第 %d 次下达——上次派发后路由事实未变化。本次允许先调 novel_context 核对事实，再裁定照常执行或改派其它子代理。）", n)
	}
	return msg
}

// SetOnRepeat 注册重复指令的 telemetry 回调。须在派发开始前调用一次。
func (d *Dispatcher) SetOnRepeat(cb func(agent, task string, n int)) {
	d.onRepeat = cb
}

// SetOnStall 注册无 durable progress 时的熔断回调。须在派发开始前调用一次。
func (d *Dispatcher) SetOnStall(cb func(agent, task string, n int)) {
	d.onStall = cb
}

// SetOnWriterDispatch registers a synchronous observer fired after a writer
// dispatch has been validated and pre-marked in progress.
func (d *Dispatcher) SetOnWriterDispatch(cb func(chapter int)) {
	d.onWriterDispatch = cb
}

// trackRepeat 记录相同指令在同一 durable progress token 上的连续次数。
// Agent、Task 或 token 任一变化都会重新计数；返回 stalled=true 时调用方
// 必须停止派发。Reason 是展示文本，不参与相等性。
func (d *Dispatcher) trackRepeat(next *Instruction, progressToken string) (n int, stalled bool) {
	d.lastMu.Lock()
	if d.lastSent != nil &&
		d.lastSent.Agent == next.Agent &&
		d.lastSent.Task == next.Task &&
		d.lastProgressToken == progressToken {
		d.repeats++
	} else {
		cp := *next
		d.lastSent = &cp
		d.lastProgressToken = progressToken
		d.repeats = 1
	}
	n = d.repeats
	stalled = n >= stallAbortAt
	d.lastMu.Unlock()

	if n == repeatNotifyAt && d.onRepeat != nil {
		d.onRepeat(next.Agent, next.Task, n)
	}
	return n, stalled
}

func (d *Dispatcher) durableProgressToken(inst *Instruction) string {
	if d.store == nil {
		return ""
	}
	var token string
	if cp := d.store.Checkpoints.LatestGlobal(); cp != nil {
		token = fmt.Sprintf("cp:%d:%s", cp.Seq, cp.Digest)
	}
	if inst == nil || inst.Chapter <= 0 {
		return token
	}
	for _, rel := range []string{
		filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.partial.json", inst.Chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.plan.partial.json", inst.Chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", inst.Chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.parts", inst.Chapter), "index.json"),
	} {
		body, err := os.ReadFile(filepath.Join(d.store.Dir(), rel))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		token += "|" + filepath.ToSlash(rel) + ":" + hex.EncodeToString(sum[:8])
	}
	return token
}

// ResetRepeat 清空重复追踪。Resume / 新 Start 时 Host 调用，
// 确保恢复或新建后首条指令以"第 1 次"语义下达。
func (d *Dispatcher) ResetRepeat() {
	d.lastMu.Lock()
	defer d.lastMu.Unlock()
	d.lastSent = nil
	d.lastProgressToken = ""
	d.repeats = 0
}
