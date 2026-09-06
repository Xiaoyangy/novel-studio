package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestDurableUsageCallsStartIsDurableUnchargedAndIdempotent(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	if err := meter.Record("known-before", "writer", durableUsageTestMessage(31, .31)); err != nil {
		t.Fatal(err)
	}
	if err := meter.StartCall("started-a", "writer", "pg2_calls", 1234, "boot-identity:42"); err != nil {
		t.Fatal(err)
	}
	first := meter.PendingCalls()["started-a"]
	before, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if meter.Has("started-a") || first.Agent != "writer" || first.ProcessID != 1234 || first.ProcessStart != "boot-identity:42" || first.GenerationID != "pg2_calls" {
		t.Fatalf("start did not remain pure pending provenance: %+v", first)
	}
	if err := meter.StartCall("started-a", "writer", "pg2_calls", 1234, "boot-identity:42"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if !bytes.Equal(before, after) || meter.PendingCalls()["started-a"] != first {
		t.Fatal("duplicate pending start changed timestamp or appended another lifecycle event")
	}
	if cost, input, _, _, _ := meter.Tracker().Totals(); cost != .31 || input != 31 || meter.Tracker().MissingAssistantUsage() != 0 {
		t.Fatal("start changed known usage subtotal or missing usage")
	}
	copy := meter.PendingCalls()
	delete(copy, "started-a")
	if len(meter.PendingCalls()) != 1 {
		t.Fatal("caller modified meter pending map through returned snapshot")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(after), []byte{'\n'}) {
		var record usageAuditRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.Kind == "started" && (record.CallStart == nil || record.Usage != nil || record.Accounting != nil || record.Baseline != nil || len(record.Metadata) != 0) {
			t.Fatal("start journal contains pricing, usage or message material")
		}
	}
	ordinary := NewUsageTracker(nil, st)
	if loaded, err := ordinary.LoadFromStore(); err != nil || !loaded {
		t.Fatal(err)
	}
	ordinary.Record("editor", durableUsageTestMessage(2, .02))
	if err := ordinary.SaveNow(); err != nil {
		t.Fatal(err)
	}
	saved, err := st.Usage.Load()
	if err != nil || saved.PendingUsageCalls["started-a"] != first {
		t.Fatalf("normal Host erased pending process provenance: %v", err)
	}
}

func TestDurableUsageCallsSnapshotFailuresRecoverStartAndCompletion(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	meter.saveSnapshot = func(*store.UsageAuditTransaction, domain.UsageState) error { return errors.New("snapshot unavailable") }
	if err := meter.StartCall("call", "writer", "pg2_calls", 2222, "opaque-start"); err == nil {
		t.Fatal("start snapshot publication failure hidden")
	}
	first := meter.PendingCalls()["call"]
	if first.StartedAt == "" || meter.Has("call") {
		t.Fatal("fsynced start disappeared or acquired a monetary identity")
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil || restored.PendingCalls()["call"] != first {
		t.Fatalf("start tail not recovered exactly: %v", err)
	}
	if err := restored.StartCall("call", "writer", "pg2_calls", 2222, "opaque-start"); err != nil {
		t.Fatal(err)
	}
	restored.saveSnapshot = meter.saveSnapshot
	if err := restored.Record("call", "writer", durableUsageTestMessage(42, .42)); err == nil {
		t.Fatal("completion snapshot publication failure hidden")
	}
	again, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.PendingCalls()) != 0 || !again.Has("call") {
		t.Fatal("completed WAL call remained pending after snapshot recovery")
	}
	if err := again.Record("call", "writer", durableUsageTestMessage(42, .42)); err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := again.Tracker().Totals(); cost != .42 || input != 42 || again.Tracker().MissingAssistantUsage() != 0 {
		t.Fatal("completion recovery changed known subtotal")
	}
}

func TestDurableUsageCallsSkipRequiresStartAndNeverCharges(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	before, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err := meter.SkipCall("never-started"); err == nil {
		t.Fatal("unknown skipped call was accepted")
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if !bytes.Equal(before, after) {
		t.Fatal("invalid skip poisoned the durable journal")
	}
	if err := meter.StartCall("skip", "writer", "pg2_calls", 123, ""); err != nil {
		t.Fatal(err)
	}
	if err := meter.SkipCall("skip"); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err := meter.SkipCall("skip"); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if !bytes.Equal(before, after) || len(meter.PendingCalls()) != 0 || !meter.Has("skip") || meter.Covered("skip") {
		t.Fatal("skip marker did not close idempotently and distinctly from a cover")
	}
	if cost, input, _, _, _ := meter.Tracker().Totals(); cost != 0 || input != 0 || meter.Tracker().MissingAssistantUsage() != 0 {
		t.Fatal("unexecuted skipped request was charged or counted missing")
	}
	if err := meter.Record("skip", "writer", durableUsageTestMessage(1, .1)); err == nil {
		t.Fatal("skipped identity accepted a conflicting completion")
	}
	if err := meter.StartCall("skip", "writer", "pg2_calls", 123, ""); err == nil {
		t.Fatal("closed identity silently reopened a new request")
	}
}

func TestDurableUsageCallsIdentityConflictsFailBeforeAppend(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	if err := meter.StartCall("call", "writer", "pg2_calls", 123, "process-1"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	for _, start := range []domain.UsageCallStart{
		{Agent: "editor", ProcessID: 123, ProcessStart: "process-1", GenerationID: "pg2_calls"},
		{Agent: "writer", ProcessID: 124, ProcessStart: "process-1", GenerationID: "pg2_calls"},
		{Agent: "writer", ProcessID: 123, ProcessStart: "process-2", GenerationID: "pg2_calls"},
		{Agent: "writer", ProcessID: 123, ProcessStart: "process-1", GenerationID: "pg2_other"},
		{Agent: "writer", ProcessID: 0}, {Agent: "writer", ProcessID: 123, ProcessStart: "private prompt\nbody"},
	} {
		if err := meter.StartCall("call", start.Agent, start.GenerationID, start.ProcessID, start.ProcessStart); err == nil {
			t.Fatalf("conflicting/unsafe start accepted: %+v", start)
		}
	}
	if err := meter.Record("call", "editor", durableUsageTestMessage(1, .1)); err == nil {
		t.Fatal("another role closed the pending request")
	}
	msg := durableUsageTestMessage(1, .1)
	msg.Metadata["generation_id"] = "pg2_other"
	if err := meter.Record("call", "writer", msg); err == nil {
		t.Fatal("another generation closed the pending request")
	}
	if err := meter.Cover("call"); err == nil {
		t.Fatal("group cover replaced a pending real request")
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if !bytes.Equal(before, after) || len(meter.PendingCalls()) != 1 {
		t.Fatal("rejected identity operation changed the WAL or pending request")
	}
}

func TestDurableUsageCallsDeletedSnapshotReplaysPendingAndClosedExactlyOnce(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	for _, id := range []string{"known", "unknown", "skipped", "pending"} {
		if err := meter.StartCall(id, "writer", "pg2_calls", 123, "boot-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := meter.Record("known", "writer", durableUsageTestMessage(20, .2)); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("unknown", "writer", agentcore.Message{Role: agentcore.RoleAssistant, Metadata: map[string]any{"codex_usage_source": "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if err := meter.SkipCall("skipped"); err != nil {
		t.Fatal(err)
	}
	want := meter.Tracker().Snapshot()
	if err := os.Remove(filepath.Join(st.Dir(), "meta/usage.json")); err != nil {
		t.Fatal(err)
	}
	restored, err := NewDurableUsageMeter(st)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Tracker().Snapshot()
	if !reflect.DeepEqual(got.PendingUsageCalls, want.PendingUsageCalls) || !reflect.DeepEqual(got.AccountedUsageIDs, want.AccountedUsageIDs) || got.Overall != want.Overall || got.MissingUsage != want.MissingUsage || got.MissingUsage == 0 || got.AuditOffset != want.AuditOffset {
		t.Fatalf("journal-only replay changed lifecycle or known subtotal: got=%+v want=%+v", got, want)
	}
}

func TestDurableUsageCallsConcurrentMetersMergePendingAndCompletions(t *testing.T) {
	_, st := newDurableUsageTestMeter(t)
	meters := make([]*DurableUsageMeter, 4)
	for i := range meters {
		var err error
		meters[i], err = NewDurableUsageMeter(store.NewStore(st.Dir()))
		if err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- meters[i%4].StartCall(fmt.Sprintf("call-%d", i), "writer", "pg2_calls", 123, "boot-1")
		}(i)
	}
	wg.Wait()
	for i := 0; i < 12; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, meter := range meters {
		if err := meter.Flush(); err != nil || len(meter.PendingCalls()) != 12 {
			t.Fatalf("cross-instance pending tail was not merged: %v", err)
		}
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, meter := fmt.Sprintf("call-%d", i), meters[(i+1)%4]
			if i%2 == 0 {
				errs <- meter.Record(id, "writer", durableUsageTestMessage(10, 1))
			} else {
				errs <- meter.SkipCall(id)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 12; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, meter := range meters {
		if err := meter.Flush(); err != nil || len(meter.PendingCalls()) != 0 {
			t.Fatalf("completed call remained pending in another meter: %v", err)
		}
		if cost, input, _, _, _ := meter.Tracker().Totals(); cost != 6 || input != 60 || meter.Tracker().MissingAssistantUsage() != 0 {
			t.Fatal("concurrent lifecycle events changed known subtotal")
		}
	}
}

func TestDurableUsageCallsStoreRejectsPendingChangesWithoutJournalAdvance(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	if err := meter.StartCall("pending", "writer", "pg2_calls", 123, "boot-1"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(st.Dir(), "meta/usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.UsageState){
		func(state *domain.UsageState) { state.PendingUsageCalls = nil },
		func(state *domain.UsageState) {
			start := state.PendingUsageCalls["pending"]
			start.ProcessID++
			state.PendingUsageCalls["pending"] = start
		},
		func(state *domain.UsageState) {
			state.PendingUsageCalls["injected"] = state.PendingUsageCalls["pending"]
		},
	} {
		state := meter.Tracker().Snapshot()
		mutate(&state)
		if err := st.Usage.Save(state); err == nil {
			t.Fatal("snapshot rewrote pending lifecycle without a WAL event")
		}
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), "meta/usage.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected snapshot mutation changed the live accounting file")
	}
	if err := meter.SkipCall("pending"); err != nil {
		t.Fatalf("journal-bound lifecycle closure was blocked: %v", err)
	}
}

func TestDurableUsageCallsConcurrentSameIDRetainsFirstStart(t *testing.T) {
	_, st := newDurableUsageTestMeter(t)
	meters := make([]*DurableUsageMeter, 4)
	for i := range meters {
		var err error
		meters[i], err = NewDurableUsageMeter(store.NewStore(st.Dir()))
		if err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, len(meters))
	for _, meter := range meters {
		go func(m *DurableUsageMeter) { errs <- m.StartCall("same", "writer", "pg2_calls", 123, "boot-1") }(meter)
	}
	for range meters {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var want domain.UsageCallStart
	for i, meter := range meters {
		if err := meter.Flush(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = meter.PendingCalls()["same"]
		}
		if len(meter.PendingCalls()) != 1 || meter.PendingCalls()["same"] != want || meter.Has("same") {
			t.Fatal("same call start differs between concurrent live meters")
		}
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte(`"kind":"started"`)) != 1 {
		t.Fatal("concurrent duplicate starts appended more than one event")
	}
}

func TestDurableUsageCallsRejectsImpossibleJournalLifecycleWithoutTruncating(t *testing.T) {
	meter, st := newDurableUsageTestMeter(t)
	if err := meter.StartCall("real-pending", "writer", "pg2_calls", 123, "boot-1"); err != nil {
		t.Fatal(err)
	}
	raw, err := finalizeUsageAuditRecord(usageAuditRecord{Version: 1, Kind: "skipped", ID: "never-started"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Usage.WithAuditTransaction(func(tx *store.UsageAuditTransaction) error {
		_, err := tx.Append(meter.Tracker().Snapshot().AuditOffset, raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if _, err := NewDurableUsageMeter(st); err == nil {
		t.Fatal("validly signed but impossible skip silently closed unknown work")
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(), store.UsageAuditPath))
	if !bytes.Equal(before, after) {
		t.Fatal("failed lifecycle recovery truncated durable journal evidence")
	}
}
