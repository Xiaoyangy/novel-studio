package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"github.com/voocel/agentcore"
	"log/slog"
)

// isCancellationNoise 判断一个错误是否为 abort 引发的衍生噪声。
// 仅当 Host 处于 aborting 态时返回 true 才有意义——非 abort 期间的
// context.Canceled 可能反映真实问题（如外部 ctx 被取消），仍应上报。
func (o *observer) isCancellationNoise(err error, msg string) bool {
	if !o.aborting.Load() {
		return false
	}
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(msg), "context canceled")
}

func (o *observer) handle(ev agentcore.Event) {
	switch ev.Type {
	case agentcore.EventToolExecStart:
		o.handleToolStart(ev)
	case agentcore.EventToolExecUpdate:
		o.handleToolUpdate(ev)
	case agentcore.EventToolExecEnd:
		o.handleToolEnd(ev)
	case agentcore.EventMessageUpdate:
		o.handleMessageUpdate(ev)
	case agentcore.EventMessageEnd:
		o.streamClear()
	case agentcore.EventTurnStart:
		if ev.Progress != nil && ev.Progress.Kind == agentcore.ProgressTurnCounter {
			o.updateAgent(ev.Progress.Agent, func(a *agentState) {
				a.turn = ev.Progress.Turn
			})
		}
	case agentcore.EventRetry:
		if ev.RetryInfo != nil {
			msg := ""
			if ev.RetryInfo.Err != nil {
				msg = ev.RetryInfo.Err.Error()
			}
			prefix := retryPrefix(ev.RetryInfo.Attempt, ev.RetryInfo.MaxRetries, ev.RetryInfo.Delay)
			retryEv := Event{
				ID:       o.retryEventID("coordinator", ev.RetryInfo.Attempt),
				Time:     time.Now(),
				Category: "SYSTEM",
				Summary:  prefix + truncate(msg, 80),
				Detail:   prefix + msg,
				Kind:     errorKind(ev.RetryInfo.Err, msg),
				Level:    "warn",
			}
			o.emitEv(retryEv)
			o.persistEvent(retryEv)
		}
	case agentcore.EventError:
		if ev.Err != nil {
			fullMsg := ev.Err.Error()
			if o.isCancellationNoise(ev.Err, fullMsg) {
				// 用户主动 abort 衍生的 ctx-cancel 错误；已有"用户手动暂停"事件，不再重复刷屏。
				o.flushActiveCalls(true)
				slog.Debug("suppressed cancel-derived error", "module", "agent", "msg", fullMsg)
				return
			}
			o.flushActiveCalls(true)
			errEv := Event{
				Time:     time.Now(),
				Category: "ERROR",
				Summary:  truncate(fullMsg, 120),
				Detail:   fullMsg,
				Kind:     errorKind(ev.Err, fullMsg),
				Level:    "error",
			}
			o.emitEv(errEv)
			o.persistEvent(errEv)
		}
	}
}

func retryPrefix(attempt, maxRetries int, delay time.Duration) string {
	if text := formatRetryDelay(delay); text != "" {
		return fmt.Sprintf("重试 (%d/%d，%s后): ", attempt, maxRetries, text)
	}
	return fmt.Sprintf("重试 (%d/%d): ", attempt, maxRetries)
}

func formatRetryDelay(delay time.Duration) string {
	if delay <= 0 {
		return ""
	}
	seconds := int64(delay / time.Second)
	if delay%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return (time.Duration(seconds) * time.Second).String()
}

func (o *observer) handleMessageUpdate(ev agentcore.Event) {
	if ev.Delta == "" {
		return
	}
	if ev.DeltaKind == agentcore.DeltaToolCall {
		o.handleCoordinatorToolDelta(ev)
		return
	}
	o.emitStreamDelta(ev.Delta, ev.DeltaKind == agentcore.DeltaThinking)
}

func (o *observer) handleThinkingProgress(ev agentcore.Event) {
	agent := ev.Progress.Agent
	thinking := ev.Progress.Thinking
	if agent == "" || thinking == "" {
		return
	}

	prev := o.lastThinkingByAgent[agent]
	delta := thinking
	if strings.HasPrefix(thinking, prev) {
		delta = thinking[len(prev):]
	}
	o.lastThinkingByAgent[agent] = thinking
	if delta == "" {
		return
	}
	o.emitStreamDelta(delta, true)
}

func (o *observer) handleContextProgress(ev agentcore.Event) {
	if ev.Progress == nil || len(ev.Progress.Meta) == 0 {
		return
	}
	var payload struct {
		Tokens        int     `json:"tokens"`
		ContextWindow int     `json:"context_window"`
		Percent       float64 `json:"percent"`
		Scope         string  `json:"scope"`
		Strategy      string  `json:"strategy"`
	}
	if json.Unmarshal(ev.Progress.Meta, &payload) != nil {
		return
	}

	agent := ev.Progress.Agent
	if agent == "" {
		agent = "coordinator"
	}

	// 更新 agent 快照（TUI 侧边栏始终可见）
	o.updateAgent(agent, func(a *agentState) {
		a.context = AgentContextSnapshot{
			Tokens:        payload.Tokens,
			ContextWindow: payload.ContextWindow,
			Percent:       payload.Percent,
			Scope:         payload.Scope,
			Strategy:      payload.Strategy,
		}
	})

	level := "info"
	if payload.Percent > 85 {
		level = "warn"
	}
	summary := fmt.Sprintf("%s 上下文 %.0f%% (%d/%d) 策略: %s", agent, payload.Percent, payload.Tokens, payload.ContextWindow, payload.Strategy)

	depth := 0
	if agent != "coordinator" {
		depth = 1
	}

	if payload.Strategy != "" {
		// 触发了压缩 → 事件流 + 日志
		ctxEv := Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: summary, Level: level, Depth: depth}
		o.emitEv(ctxEv)
		o.persistEvent(ctxEv)
	} else {
		// 普通使用率报告 → 仅日志
		slogLevel := slog.LevelInfo
		if level == "warn" {
			slogLevel = slog.LevelWarn
		}
		slog.Log(context.Background(), slogLevel, summary, "module", "context", "agent", agent)
	}
}
