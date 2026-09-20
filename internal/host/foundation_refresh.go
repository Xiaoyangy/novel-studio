package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/agentcore"
)

func (h *Host) foundationRefreshBusyLocked() bool {
	if h.foundationRefreshDone == nil {
		return false
	}
	select {
	case <-h.foundationRefreshDone:
		return false
	default:
		return true
	}
}

// StartFoundationRefresh dispatches an already-extracted Architect task to
// the one source target selected at Host construction. It does not ask a
// Coordinator model to rewrite or transport the task and does not select a
// model/agent from its text. This narrow sidecar is not a generic entry router.
func (h *Host) StartFoundationRefresh(task string) error {
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("foundation refresh task is required")
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	h.mu.Lock()
	if h.closed || h.closing || h.lifecycle != lifecycleIdle || h.cocreating || h.foundationRefreshDone != nil {
		h.mu.Unlock()
		return fmt.Errorf("host cannot start another direct foundation refresh")
	}
	if h.foundationRefreshDispatch == nil || h.foundationRefreshTarget == "" || !h.disableFlowRouter || !h.preserveCheckpointsOnStart {
		h.mu.Unlock()
		return fmt.Errorf("direct foundation refresh requires its restricted host capability")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.foundationRefreshCancel, h.foundationRefreshDone = cancel, done
	h.observer.setAborting(false)
	h.lifecycle = lifecycleRunning
	h.mu.Unlock()
	// Reserve the direct lifecycle before preparation so concurrent Close can
	// cancel it and wait, but do not hold h.mu during Store or observer work.
	if err := h.resetStartRuntimeState(); err != nil {
		cancel()
		h.finishFoundationRefresh(done, err)
		return err
	}
	if err := h.ensureStartProgressInitialized(); err != nil {
		cancel()
		h.finishFoundationRefresh(done, err)
		return err
	}
	if err := ctx.Err(); err != nil {
		h.finishFoundationRefresh(done, err)
		return err
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "宿主直接派发受限 Architect 来源修订", Level: "info"})
	go h.runFoundationRefresh(ctx, done, task)
	return nil
}

func (h *Host) runFoundationRefresh(ctx context.Context, done chan struct{}, task string) {
	args, err := json.Marshal(struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}{Agent: "architect_long", Task: task})
	if err != nil {
		h.finishFoundationRefresh(done, err)
		return
	}
	// These are real host-dispatched tool events, not assistant/provider
	// messages. The existing observer receives the same synchronous child
	// progress relay as a normal subagent call, without fabricated usage.
	hostProgress := &agentcore.ProgressPayload{Agent: "host"}
	h.observer.handle(agentcore.Event{Type: agentcore.EventToolExecStart, Tool: "subagent", Args: args, Progress: hostProgress})
	progressCtx := agentcore.WithToolProgress(ctx, func(progress agentcore.ProgressPayload) {
		h.observer.handle(agentcore.Event{Type: agentcore.EventToolExecUpdate, Tool: "subagent", Args: args, Progress: &progress})
	})
	result, runErr := h.foundationRefreshDispatch(progressCtx, task)
	if runErr != nil {
		result, _ = json.Marshal(runErr.Error())
	}
	end := agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "subagent", Args: args, Result: result, IsError: runErr != nil, Progress: hostProgress}
	h.observer.handle(end)
	h.budget.HandleEvent(end) // The direct call bypasses Coordinator subscribers.
	h.finishFoundationRefresh(done, runErr)
}

func (h *Host) finishFoundationRefresh(done chan struct{}, err error) {
	h.observer.finalize()
	h.mu.Lock()
	h.foundationRefreshErr = err
	if h.lifecycle == lifecycleRunning {
		h.lifecycle = lifecycleIdle
	}
	h.mu.Unlock()
	h.signalDone()
	close(done) // Last action: Close may now flush usage and close output channels.
}

// WaitFoundationRefresh waits for all child/observer callbacks, not merely
// cancellation. Headless still verifies changed artifacts and a new matching
// refresh checkpoint; successful dispatch alone is not a successful refresh.
func (h *Host) WaitFoundationRefresh() error {
	h.mu.Lock()
	done := h.foundationRefreshDone
	h.mu.Unlock()
	if done == nil {
		return fmt.Errorf("no direct foundation refresh was started")
	}
	<-done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.foundationRefreshErr
}
