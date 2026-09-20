package headless

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func directFoundationRefreshOptionsForTest() Options {
	return Options{
		FoundationRefreshTarget: "world_rules", StopAfterFoundationChange: true,
		OneShotFoundationRefresh: true, RecordFoundationRefreshEpoch: true,
		DisableFlowRouter: true, PreserveCheckpointsOnStart: true, PreserveUserRules: true,
		Prompt: "[宿主强制路由：Coordinator 必须逐字服从]\nCoordinator 只转交，不得修改。\n\n[Architect 执行任务]\n完整来源任务；保留数字9007199254740993与私有作者资料。\n字面引用 [Architect 执行任务] 不再切分。",
	}
}

func TestDirectFoundationRefreshTaskPreservesExactArchitectBody(t *testing.T) {
	opts := directFoundationRefreshOptionsForTest()
	got, direct, err := directFoundationRefreshTask(opts)
	if err != nil || !direct {
		t.Fatalf("restricted refresh was not direct: %t %v", direct, err)
	}
	want := "[Architect 执行任务]\n完整来源任务；保留数字9007199254740993与私有作者资料。\n字面引用 [Architect 执行任务] 不再切分。"
	if got != want || strings.Contains(got, "Coordinator 只转交") {
		t.Fatalf("host task extraction changed the Architect body or retained its Coordinator prefix: %q", got)
	}
}

func TestDirectFoundationRefreshTaskRejectsIncompleteCapabilityAndRouting(t *testing.T) {
	for _, mode := range []string{"stop", "one-shot", "epoch", "router", "checkpoints", "user-rules", "prefix", "marker", "empty-task", "resume"} {
		t.Run(mode, func(t *testing.T) {
			opts := directFoundationRefreshOptionsForTest()
			switch mode {
			case "stop":
				opts.StopAfterFoundationChange = false
			case "one-shot":
				opts.OneShotFoundationRefresh = false
			case "epoch":
				opts.RecordFoundationRefreshEpoch = false
			case "router":
				opts.DisableFlowRouter = false
			case "checkpoints":
				opts.PreserveCheckpointsOnStart = false
			case "user-rules":
				opts.PreserveUserRules = false
			case "prefix":
				opts.Prompt = "普通用户内容\n\n[Architect 执行任务]\n不能成为入口能力。"
			case "marker":
				opts.Prompt = "[宿主强制路由：Coordinator 必须逐字服从]\n缺失实际任务。"
			case "empty-task":
				opts.Prompt = "[宿主强制路由：Coordinator 必须逐字服从]\n\n[Architect 执行任务]\n"
			case "resume":
				opts.Prompt = ""
			}
			if task, direct, err := directFoundationRefreshTask(opts); err == nil || !direct || task != "" {
				t.Fatalf("incomplete exact refresh could fall back to Coordinator: task=%q direct=%t err=%v", task, direct, err)
			}
		})
	}
}

func TestDirectFoundationRefreshTaskLeavesOrdinaryHeadlessRouteUnchanged(t *testing.T) {
	for _, opts := range []Options{{}, {Prompt: "ordinary task"}, {Prompt: "world_tick task", StopAfterInitialWorldTick: true}} {
		if task, direct, err := directFoundationRefreshTask(opts); err != nil || direct || task != "" {
			t.Fatalf("ordinary invocation acquired a direct source capability: %q %t %v", task, direct, err)
		}
	}
}

func TestDirectFoundationRefreshWaitCancelsBeforeWaitingOnOutputFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outputErr := errors.New("stdout broken pipe")
	enteredWait := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- waitDirectFoundationRefresh(outputErr, func() bool {
			cancel()
			return true
		}, func() error {
			close(enteredWait)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-enteredWait:
	case <-time.After(time.Second):
		t.Fatal("completion never reached child wait")
	}
	if ctx.Err() == nil {
		t.Error("output failure waited on a running child before cancelling it")
		cancel() // Drain the intentionally blocked child even on the regression.
	}
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("cleanup cancellation would override the original stdout error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion did not return after cancellation")
	}
}

func TestDirectFoundationRefreshWaitPreservesIndependentChildFailure(t *testing.T) {
	childErr := errors.New("original paid child failure")
	for _, consumeErr := range []error{nil, ErrFoundationChangeIncomplete} {
		abortCalls := 0
		got := waitDirectFoundationRefresh(consumeErr, func() bool {
			abortCalls++
			return false // Already terminal; no cleanup cancellation was initiated.
		}, func() error { return childErr })
		if !errors.Is(got, childErr) {
			t.Fatalf("child failure was replaced: %v", got)
		}
		if consumeErr == nil && abortCalls != 0 {
			t.Fatal("normal completion unnecessarily aborted the host")
		}
	}
	got := waitDirectFoundationRefresh(ErrFoundationChangeIncomplete, func() bool { return false }, func() error { return context.Canceled })
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("independent cancellation was turned into a retryable incomplete refresh: %v", got)
	}
}
