package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func writeAccountingTestSession(t *testing.T, st *store.Store, name string, messages ...agentcore.Message) {
	t.Helper()
	var data []byte
	for _, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, append(raw, '\n')...)
	}
	path := filepath.Join(st.Dir(), "meta", "sessions", name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHostUsageReplayStableSessionIdentityAndWALAreNotDoubleCounted(t *testing.T) {
	producer, _ := newAccountingTest(t)
	model := producer.decorate(context.Background(), "architect_long", "test-provider", "test-model", &hostAccountingModel{usage: hostAccountingKnown(100, .25)})
	response, err := model.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(t.TempDir())
	writeAccountingTestSession(t, st, "coordinator.jsonl", response.Message)
	writeAccountingTestSession(t, st, "agents/architect_long-ch01.jsonl", response.Message)
	a, err := newHostProviderAccounting(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := a.meter.Tracker().Totals(); cost != .25 || input != 100 {
		t.Fatalf("duplicate session import: %v/%d", cost, input)
	}
	a.record("coordinator", response.Message)
	if err := a.flush(); err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := a.meter.Tracker().Totals(); cost != .25 || input != 100 {
		t.Fatalf("imported ID charged by OnMessage: %v/%d", cost, input)
	}
	// A WAL is authoritative even when session files subsequently contain a
	// copied response and an unrelated legacy receipt with no stable ID.
	writeAccountingTestSession(t, st, "coordinator.jsonl", response.Message, agentcore.Message{Role: agentcore.RoleAssistant, Usage: hostAccountingKnown(900, 9)})
	if err := os.Remove(filepath.Join(st.Dir(), "meta", "usage.json")); err != nil {
		t.Fatal(err)
	}
	restored, err := newHostProviderAccounting(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := restored.meter.Tracker().Totals(); cost != .25 || input != 100 {
		t.Fatalf("session was replayed alongside WAL: %v/%d", cost, input)
	}
	if restored.meter.Tracker().Snapshot().PerAgent["architect"].Input != 100 {
		t.Fatal("restart lost originating role")
	}
}

func TestHostUsageReplayConflictingSessionIDFailsBeforePublishingBaseline(t *testing.T) {
	st := store.NewStore(t.TempDir())
	m := agentcore.Message{Role: agentcore.RoleAssistant, Usage: hostAccountingKnown(10, .1), Metadata: map[string]any{"usage_audit_id": "same_call", "usage_audit_agent": "writer"}}
	writeAccountingTestSession(t, st, "coordinator.jsonl", m)
	m.Usage = hostAccountingKnown(20, .2)
	writeAccountingTestSession(t, st, "agents/writer-ch01.jsonl", m)
	if _, err := NewDurableUsageMeter(st); !errors.Is(err, errUsageReplayIdentityConflict) {
		t.Fatalf("conflicting identity accepted: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatal("failed replay published a partial baseline")
	}
}

func TestHostUsageLegacyBaselineRemainsExplicitlyIncomplete(t *testing.T) {
	st := store.NewStore(t.TempDir())
	writeAccountingTestSession(t, st, "agents/writer-ch01.jsonl", agentcore.Message{Role: agentcore.RoleAssistant, Usage: hostAccountingKnown(10, .1)})
	meter, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if meter.Tracker().MissingAssistantUsage() == 0 {
		t.Fatal("no-ID history presented as complete")
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Tracker().MissingAssistantUsage() != meter.Tracker().MissingAssistantUsage() {
		t.Fatal("legacy warning grew on restart")
	}
}

func TestHostProviderAccountingFullSummaryWithoutOnMessageIsDurable(t *testing.T) {
	a, st := newAccountingTest(t)
	m := a.decorate(context.Background(), "coordinator", "test-provider", "test-model", &hostAccountingModel{usage: hostAccountingKnown(111, .125)})
	strategy := corecontext.NewFullSummary(corecontext.FullSummaryConfig{Model: m, KeepRecentTokens: 1})
	view := []agentcore.AgentMessage{agentcore.UserMsg(strings.Repeat("old narrative state ", 300)), agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("old answer")}}, agentcore.UserMsg("recent task")}
	_, result, err := strategy.ForceApply(context.Background(), nil, view, corecontext.Budget{Window: 2000, Threshold: 1000, Tokens: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatal("fixture did not execute real summary strategy")
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := restored.Tracker().Totals(); cost != .125 || input != 111 {
		t.Fatalf("summary without OnMessage unmetered: %v/%d", cost, input)
	}
}

func TestHostProviderAccountingRealFailoverBillsBothAttempts(t *testing.T) {
	a, _ := newAccountingTest(t)
	cfg := bootstrap.Config{Provider: "local", ModelName: "primary", Providers: map[string]bootstrap.ProviderConfig{"local": {Type: "openai"}}, Roles: map[string]bootstrap.RoleConfig{"writer": {Provider: "local", Model: "primary", Fallbacks: []bootstrap.ModelRef{{Provider: "local", Model: "fallback"}}}}}
	ms, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ms.SetAttemptDecorator(func(ctx context.Context, role, provider, name string, _ agentcore.ChatModel) agentcore.ChatModel {
		m := &hostAccountingModel{usage: hostAccountingKnown(20, .2)}
		if name == "primary" {
			m.failure = errors.New("insufficient balance")
		}
		return a.decorate(ctx, role, provider, name, m)
	})
	r, err := ms.ForRoleWithFailover("drafter", nil).Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.record("coordinator", r.Message)
	if cost, input, _, _, _ := a.meter.Tracker().Totals(); cost != .2 || input != 20 {
		t.Fatalf("fallback was not charged exactly once: %v/%d", cost, input)
	}
	if a.meter.Tracker().MissingAssistantUsage() != 1 {
		t.Fatal("failed attempt without receipt disappeared")
	}
	if len(a.meter.PendingCalls()) != 0 {
		t.Fatal("failover left unfinished intents")
	}
}

func TestHostProviderAccountingPreflightRefusalDoesNotStartUsage(t *testing.T) {
	a, st := newAccountingTest(t)
	want := errors.New("hard budget exhausted")
	a.beforeCall = func() error { return want }
	before, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	m := a.decorate(context.Background(), "coordinator", "test-provider", "test-model", &hostAccountingModel{usage: hostAccountingKnown(10, .1)})
	if _, err := m.Generate(context.Background(), nil, nil); !errors.Is(err, want) {
		t.Fatalf("budget not enforced before dispatch: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(a.meter.PendingCalls()) != 0 || a.meter.Tracker().MissingAssistantUsage() != 0 {
		t.Fatal("preflight refusal became a paid/unknown attempt")
	}
}
