package host

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

// The real Architect/tool/store integration is exercised in
// foundation_refresh_direct_test.go. These dispatches isolate Host lifecycle
// ordering and never call a provider.
func TestFoundationRefreshLifecycleAbortFromDispatchCallbackDoesNotWait(t *testing.T) {
	h, coordinator, architect, _ := newDirectRefreshHost(t, "world_rules")
	abortReturned := make(chan bool, 1)
	h.foundationRefreshDispatch = func(ctx context.Context, _ string) (json.RawMessage, error) {
		// Provider accounting and hard-budget callbacks can synchronously abort
		// their own dispatch. Waiting for directDone here would deadlock.
		abortReturned <- h.Abort()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := h.StartFoundationRefresh("仅测试同步取消回调，不调用模型。"); err != nil {
		t.Fatal(err)
	}
	select {
	case accepted := <-abortReturned:
		if !accepted {
			t.Fatal("Abort did not accept the active direct refresh")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Abort waited for the dispatch that synchronously called it")
	}
	if err := waitDirectRefresh(t, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFoundationRefresh error = %v; want context.Canceled", err)
	}
	h.mu.Lock()
	state := h.lifecycle
	h.mu.Unlock()
	if state != lifecyclePaused {
		t.Fatalf("state after cancelled dispatch = %q; want paused", state)
	}
	if coordinator.calls.Load() != 0 || architect.calls.Load() != 0 {
		t.Fatal("lifecycle-only dispatch unexpectedly called a provider")
	}
}

func TestFoundationRefreshLifecycleCloseWaitsForCancellationCallbacks(t *testing.T) {
	h, coordinator, architect, _ := newDirectRefreshHost(t, "world_rules")
	started := make(chan struct{})
	callbackEntered := make(chan struct{})
	callbackFinished := make(chan struct{})
	releaseCallback := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseCallback) })
	originalEmit := h.observer.emitEv
	h.observer.emitEv = func(ev Event) {
		if ev.Category == "TOOL" && ev.Agent == "architect_long" && ev.Summary == "save_foundation" && ev.FinishedAt.IsZero() {
			close(callbackEntered)
			<-releaseCallback
			originalEmit(ev)
			close(callbackFinished)
			return
		}
		originalEmit(ev)
	}
	h.foundationRefreshDispatch = func(ctx context.Context, _ string) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		// A child can still be draining a tool event when cancellation arrives.
		// Close must wait until this synchronous observer callback returns.
		agentcore.ReportToolProgress(ctx, agentcore.ProgressPayload{
			Kind: agentcore.ProgressToolStart, Agent: "architect_long", Tool: "save_foundation",
		})
		return nil, ctx.Err()
	}
	if err := h.StartFoundationRefresh("仅测试取消后的回调收尾，不调用模型。"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("direct refresh did not start")
	}
	closed := make(chan struct{})
	go func() {
		h.Close()
		close(closed)
	}()
	select {
	case <-callbackEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the active direct refresh")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the cancellation callback finished")
	case <-time.After(50 * time.Millisecond):
	}
	h.mu.Lock()
	channelsClosed := h.closed
	h.mu.Unlock()
	if channelsClosed {
		t.Fatal("Host closed output channels while an observer callback was active")
	}
	releaseOnce.Do(func() { close(releaseCallback) })
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete after observer callbacks returned")
	}
	select {
	case <-callbackFinished:
	default:
		t.Fatal("Close completed before the observer emitted its final progress event")
	}
	if err := waitDirectRefresh(t, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFoundationRefresh error = %v; want context.Canceled", err)
	}
	var dispatchFinished, toolFinished bool
	for ev := range h.Events() {
		if ev.Category == "ERROR" {
			t.Errorf("cancellation produced an unexpected error event: %s", ev.Summary)
		}
		if ev.Agent == "architect_long" && !ev.FinishedAt.IsZero() && ev.Failed {
			dispatchFinished = dispatchFinished || ev.Category == "DISPATCH"
			toolFinished = toolFinished || ev.Category == "TOOL"
		}
	}
	if !dispatchFinished || !toolFinished {
		t.Fatalf("Close lost terminal callback events: dispatch finished=%v, tool finished=%v", dispatchFinished, toolFinished)
	}
	if coordinator.calls.Load() != 0 || architect.calls.Load() != 0 {
		t.Fatal("lifecycle-only dispatch unexpectedly called a provider")
	}
}
