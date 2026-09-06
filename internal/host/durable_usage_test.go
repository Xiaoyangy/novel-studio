package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func durableUsageTestMessage(input int, cost float64) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleAssistant,
		Content:  []agentcore.ContentBlock{agentcore.TextBlock("private fictional text must never enter accounting")},
		Usage:    &agentcore.Usage{Provider: "test-provider", Model: "test-model", Input: input, Output: 3, CacheRead: 2, Cost: &agentcore.Cost{Total: cost}},
		Metadata: map[string]any{"prompt": "PRIVATE PROMPT", "reasoning": "PRIVATE REASONING", "stage": "project_all_planner"},
	}
}

func newDurableUsageTestMeter(t *testing.T) (*DurableUsageMeter, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	meter, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	return meter, st
}

func TestDurableUsageMeterIDsPrivacyAndNormalHostRoundTrip(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	if err := meter.Cover("character-round"); err != nil {
		t.Fatal(err)
	}
	if !meter.Has("character-round") || !meter.Covered("character-round") {
		t.Fatal("cover marker was not durable")
	}
	msg := durableUsageTestMessage(100, 1.25)
	msg.Metadata["usage_audit_id"], msg.Metadata["usage_group_id"] = "call-a", "character-round"
	for i := 0; i < 3; i++ {
		if err := meter.Record("call-a", "writer", msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := meter.Record("imported", "world_arbiter", durableUsageTestMessage(25, .25)); err != nil {
		t.Fatal(err)
	}
	if meter.Covered("imported") {
		t.Fatal("aggregate record was mistaken for a covered per-call group")
	}
	if err := meter.Cover("imported"); err != nil || meter.Covered("imported") {
		t.Fatalf("cover overwrote an imported aggregate: %v", err)
	}
	if err := meter.Record("character-round", "writer", msg); err == nil {
		t.Fatal("cover id accepted monetary record")
	}
	msg.Usage.Input++
	if err := meter.Record("call-a", "writer", msg); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("same id changed accounting: %v", err)
	}
	cost, input, _, _, _ := meter.Tracker().Totals()
	if cost != 1.5 || input != 125 {
		t.Fatalf("duplicates changed totals: %v %v", cost, input)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private fictional", "PRIVATE PROMPT", "PRIVATE REASONING", `"content"`, `"reasoning"`} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("private data entered usage audit: %s", secret)
		}
	}
	if !bytes.Contains(raw, []byte(`"stage":"project_all_planner"`)) {
		t.Fatal("pure accounting stage was dropped")
	}
	ordinary := NewUsageTracker(nil, st)
	if loaded, err := ordinary.LoadFromStore(); err != nil || !loaded {
		t.Fatalf("normal host load: %t %v", loaded, err)
	}
	before := ordinary.Snapshot()
	ordinary.Record("editor", durableUsageTestMessage(4, .1))
	if err := ordinary.SaveNow(); err != nil {
		t.Fatal(err)
	}
	after, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.AuditOffset != before.AuditOffset || after.AccountedUsageIDs["call-a"] != before.AccountedUsageIDs["call-a"] {
		t.Fatal("ordinary host lost audit cursor/identities")
	}
}

func TestDurableUsageMeterSnapshotFailureReplaysOnlyJournalTail(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	before, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	meter.saveSnapshot = func(*store.UsageAuditTransaction, domain.UsageState) error {
		return errors.New("snapshot publication failed")
	}
	if err := meter.Record("failed-save-call", "writer", durableUsageTestMessage(61, .61)); err == nil {
		t.Fatal("snapshot failure hidden")
	}
	if !meter.Has("failed-save-call") {
		t.Fatal("fsynced call lost in live tracker")
	}
	unchanged, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AuditOffset != before.AuditOffset || unchanged.Overall.Input != 0 {
		t.Fatal("failed publication modified snapshot")
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, in, _, _, _ := restored.Tracker().Totals(); cost != .61 || in != 61 {
		t.Fatalf("WAL tail not replayed once: %v %d", cost, in)
	}
	if err := restored.Record("failed-save-call", "writer", durableUsageTestMessage(61, .61)); err != nil {
		t.Fatal(err)
	}
	if cost, in, _, _, _ := restored.Tracker().Totals(); cost != .61 || in != 61 {
		t.Fatalf("retry double-counted replayed call: %v %d", cost, in)
	}
	final, _ := st.Usage.Load()
	info, _ := os.Stat(filepath.Join(st.Dir(), store.UsageAuditPath))
	if final.AuditOffset != info.Size() {
		t.Fatal("snapshot did not advance to committed newline")
	}
}

func TestDurableUsageMeterDeletedSnapshotRebuildsBaselineWithoutSessionDoubleCount(t *testing.T) {
	st := store.NewStore(t.TempDir())
	path := filepath.Join(st.Dir(), "meta", "sessions", "coordinator.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := durableUsageTestMessage(11, .11)
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	meter, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("new-call", "writer", durableUsageTestMessage(22, .22)); err != nil {
		t.Fatal(err)
	}
	if err := meter.Cover("group-covered"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(st.Dir(), "meta", "usage.json")); err != nil {
		t.Fatal(err)
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, in, _, _, _ := restored.Tracker().Totals(); cost != .33 || in != 33 {
		t.Fatalf("baseline/session/WAL replay double-counted: %v %d", cost, in)
	}
	if !restored.Has("new-call") || !restored.Covered("group-covered") {
		t.Fatal("rebuild lost dedup markers")
	}
	if err := restored.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestDurableUsageMeterExistingSnapshotWinsOverLegacySessions(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Usage.Save(domain.UsageState{Overall: domain.AgentUsageTotals{Input: 40, Cost: 4}, PerAgent: map[string]domain.AgentUsageTotals{"architect": {Input: 40, Cost: 4}}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "meta", "sessions", "coordinator.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(durableUsageTestMessage(500, 5))
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	meter, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if cost, in, _, _, _ := meter.Tracker().Totals(); cost != 4 || in != 40 {
		t.Fatalf("existing live totals were replayed/reset: %v %d", cost, in)
	}
}

func TestDurableUsageMeterEmptyLegacyPriceSourceRemainsUnknown(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	msg := durableUsageTestMessage(4, .4)
	msg.Usage.Cost = nil
	msg.Metadata["codex_usage_source"] = ""
	msg.Metadata["codex_usage_breakdown"] = []any{nil}
	msg.Metadata["codex_usage_breakdown_complete"] = true
	if err := meter.Record("legacy-no-price-source", "writer", msg); err != nil {
		t.Fatal(err)
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Tracker().MissingAssistantUsage() != 1 {
		t.Fatal("legacy unknown pricing became silently free")
	}
}

func TestDurableUsageMeterConcurrentInstancesAndReentrantCostObserver(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	other, err := NewDurableUsageMeter(store.NewStore(st.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	callback := make(chan struct{}, 100)
	meter.Tracker().SetOnCost(func(float64) { _ = meter.Has("shared"); _ = meter.Tracker().Snapshot(); callback <- struct{}{} })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := meter
			if i%2 == 1 {
				m = other
			}
			if err := m.Record("shared", "writer", durableUsageTestMessage(10, 1)); err != nil {
				t.Error(err)
			}
			if err := m.Record(fmt.Sprintf("unique-%d", i), "writer", durableUsageTestMessage(1, .5)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent recording / callback deadlocked")
	}
	if err := meter.Flush(); err != nil {
		t.Fatal(err)
	}
	if cost, in, _, _, _ := meter.Tracker().Totals(); cost != 11 || in != 30 {
		t.Fatalf("cross-instance records lost or doubled: %v %d", cost, in)
	}
	if len(callback) == 0 {
		t.Fatal("budget callback was not called")
	}
}

func TestDurableUsageMeterRejectsCorruptJournalWithoutDiscardingDurablePrefix(t *testing.T) {
	for _, suffix := range []string{`{"partial":`, "{\"bad\":true}\n", "\n"} {
		t.Run(fmt.Sprintf("%q", suffix), func(t *testing.T) {
			meter, st := newDurableUsageTestMeter(t)
			if err := meter.Record("good", "writer", durableUsageTestMessage(8, .8)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), store.UsageAuditPath)
			prefix, _ := os.ReadFile(path)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString(suffix)
			_ = f.Sync()
			_ = f.Close()
			broken, _ := os.ReadFile(path)
			if _, err := NewDurableUsageMeter(st); err == nil {
				t.Fatal("corrupt journal was silently skipped")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, broken) || !bytes.HasPrefix(after, prefix) {
				t.Fatal("recovery discarded durable journal data")
			}
		})
	}
}

func TestDurableUsageMeterWriteFailureAndStaleHostCannotEraseAudit(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	stale := NewUsageTracker(nil, st)
	if loaded, err := stale.LoadFromStore(); !loaded || err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("new", "writer", durableUsageTestMessage(4, .4)); err != nil {
		t.Fatal(err)
	}
	if err := stale.SaveNow(); err == nil {
		t.Fatal("stale normal Host erased journal-backed totals")
	}
	state, _ := st.Usage.Load()
	if state.Overall.Input != 4 {
		t.Fatal("stale save altered totals")
	}
	journal := filepath.Join(st.Dir(), store.UsageAuditPath)
	backup := journal + ".held"
	if err := os.Rename(journal, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("failed-append", "writer", durableUsageTestMessage(7, .7)); err == nil {
		t.Fatal("journal append failure hidden")
	}
	if meter.Has("failed-append") {
		t.Fatal("failed append entered accounting")
	}
}
