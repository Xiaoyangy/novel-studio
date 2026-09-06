package host

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type hostAccountingModel struct {
	agentcore.ChatModel
	failure          error
	entered, release chan struct{}
	usage            *agentcore.Usage
}

func (m *hostAccountingModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.entered != nil {
		close(m.entered)
		<-m.release
	}
	if m.failure != nil {
		return nil, m.failure
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("PRIVATE-MODEL-TEXT")}, Usage: m.usage}}, nil
}

type hostAccountingUsageError struct{ usage *agentcore.Usage }

func (e *hostAccountingUsageError) Error() string                        { return "reported provider failure" }
func (e *hostAccountingUsageError) LLMUsage() (*agentcore.Usage, string) { return e.usage, "reported" }

func newAccountingTest(t *testing.T) (*hostProviderAccounting, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	a, err := newHostProviderAccounting(st)
	if err != nil {
		t.Fatal(err)
	}
	return a, st
}
func hostAccountingKnown(input int, cost float64) *agentcore.Usage {
	return &agentcore.Usage{Provider: "test-provider", Model: "test-model", Input: input, Output: 2, Cost: &agentcore.Cost{Total: cost}}
}

func TestHostProviderAccountingDeduplicatesForwardingAndPreservesRoleAndLateUsage(t *testing.T) {
	a, st := newAccountingTest(t)
	model := a.decorate(context.Background(), "architect_long", "test-provider", "test-model", &hostAccountingModel{usage: hostAccountingKnown(100, .25)})
	response, err := model.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.record("architect_long", response.Message)
	a.record("coordinator", response.Message)
	if cost, input, _, _, _ := a.meter.Tracker().Totals(); cost != .25 || input != 100 {
		t.Fatalf("OnMessage or forwarding was double counted: %v/%d", cost, input)
	}
	if a.meter.Tracker().Snapshot().PerAgent["architect"].Input != 100 {
		t.Fatal("forwarding changed accounting role")
	}
	late := &hostAccountingModel{usage: hostAccountingKnown(200, .5), entered: make(chan struct{}), release: make(chan struct{})}
	wrapped := a.decorate(context.Background(), "coordinator", "test-provider", "test-model", late)
	done := make(chan error, 1)
	go func() { _, err := wrapped.Generate(context.Background(), nil, nil); done <- err }()
	<-late.entered
	coordinator := agentcore.NewAgent(agentcore.WithModel(wrapped))
	h := &Host{store: st, coordinator: coordinator, usageAccounting: a, usage: a.meter.Tracker(), done: make(chan struct{}, 1), events: make(chan Event, 1), streamCh: make(chan string, 1)}
	h.observer = newObserver(coordinator, st, func(Event) {}, func(string) {}, func() {})
	h.Close()
	close(late.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	restored, err := NewDurableUsageMeter(store.NewStore(st.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := restored.Tracker().Totals(); cost != .75 || input != 300 {
		t.Fatalf("close discarded late response: %v/%d", cost, input)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("PRIVATE-MODEL-TEXT")) {
		t.Fatal("WAL retained model content")
	}
}

func TestHostProviderAccountingTypedFailureCompactionAndGroundingRemainDistinct(t *testing.T) {
	a, st := newAccountingTest(t)
	failure := &hostAccountingUsageError{usage: hostAccountingKnown(50, .125)}
	failed := a.decorate(context.Background(), "writer", "test-provider", "test-model", &hostAccountingModel{failure: failure})
	if _, err := failed.Generate(context.Background(), nil, nil); !errors.As(err, &failure) {
		t.Fatal("provider error identity lost")
	}
	for _, role := range []string{"coordinator", "plan_grounding"} {
		ctx := agents.WithDirectUsageAgent(context.Background(), role)
		model := a.decorate(ctx, "world_arbiter", "test-provider", "test-model", &hostAccountingModel{usage: hostAccountingKnown(10, .125)})
		if _, err := model.Generate(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	state, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Overall.Input != 70 || state.Overall.Cost != .375 || state.PerAgent["writer"].Input != 50 || state.PerAgent["coordinator"].Input != 10 || state.PerAgent["world_arbiter"].Input != 10 {
		t.Fatalf("failure/context-manager/grounding usage lost: %+v", state)
	}
	restored, err := NewDurableUsageMeter(store.NewStore(st.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudgetSentinel(bootstrap.BudgetConfig{BookUSD: .25, HardStop: true}, func() float64 { cost, _, _, _, _ := restored.Tracker().Totals(); return cost }, func(string) {}, func(string, string) {})
	if budget.Refuse() == nil {
		t.Fatal("restored cumulative cost did not constrain budget")
	}
}
