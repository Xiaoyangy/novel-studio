package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestOutlineAllUsageFailedCallsStayLiveAndPublishPreservesEntireBaseline(t *testing.T) {
	live, candidate := projectAccountingStores(t)
	base := domain.AgentUsageTotals{Input: 511468, Output: 34756, Cost: 6.716544}
	if err := live.Usage.Save(domain.UsageState{Schema: domain.UsageSchemaVersion, Overall: base, PerAgent: map[string]domain.AgentUsageTotals{"historical": base}, MissingUsage: 2}); err != nil {
		t.Fatal(err)
	}
	calls := []agentcore.Message{}
	for i, v := range []struct {
		input, output int
		cost          float64
	}{{46122, 766, .431552}, {46585, 2486, .522182}, {45090, 7860, .8439}, {52370, 6805, .86395}} {
		calls = append(calls, agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: v.input, Output: v.output, Cost: &agentcore.Cost{Total: v.cost}}, Metadata: map[string]any{"usage_audit_id": []string{"outline-1", "outline-2", "outline-3", "outline-4"}[i]}})
	}
	failed := errors.New("candidate failed after paid responses")
	cfg := bootstrap.Config{OutputDir: candidate.Dir()}
	err := runPipelineOutlineAllArchitectWithUsage(context.Background(), cfg, assets.Bundle{}, "private outline request", func(_ context.Context, _ bootstrap.Config, _ assets.Bundle, output, _ string, recorders ...agents.UsageRecorder) error {
		if output != candidate.Dir() {
			t.Fatal("model writer escaped isolated candidate")
		}
		for _, message := range calls {
			recorders[0]("architect_outline_all", message)
			recorders[0]("architect_outline_all", message)
		}
		if usage, err := candidate.Usage.Load(); err != nil || usage != nil {
			t.Fatal("candidate accumulated a competing usage ledger")
		}
		return failed
	}, live.Dir())
	if !errors.Is(err, failed) {
		t.Fatalf("model failure lost: %v", err)
	}
	assertTotals := func(st *store.Store) {
		t.Helper()
		usage, err := st.Usage.Load()
		if err != nil || usage == nil {
			t.Fatal(err)
		}
		if usage.Overall.Input != 701635 || usage.Overall.Output != 52673 || math.Abs(usage.Overall.Cost-9.378128) > 1e-9 || usage.MissingUsage != 2 {
			t.Fatalf("outline replaced or double-counted the previous book total: %+v", usage)
		}
	}
	assertTotals(live)
	meter, err := host.NewDurableUsageMeter(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := meter.StartCall("pending-preserved", "writer", "pg2_pending", os.Getpid(), "test-birth"); err != nil {
		t.Fatal(err)
	}
	proof, err := copyPipelineOutlineAllUsageForPublish(live.Dir(), candidate.Dir())
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPipelineOutlineAllUsageCopy(live.Dir(), proof); err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(t.TempDir())
	if _, err := publisher.PublishDirectory(store.PublishDirectoryRequest{TransactionID: "outline-usage-preservation", LiveDir: live.Dir(), CandidateDir: candidate.Dir(), ExpectedLiveRoot: before}); err != nil {
		t.Fatal(err)
	}
	if err := publisher.FinalizeDirectoryPublish("outline-usage-preservation"); err != nil {
		t.Fatal(err)
	}
	assertTotals(store.NewStore(live.Dir()))
	reloaded, err := host.NewDurableUsageMeter(store.NewStore(live.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.PendingCalls()) != 1 || !reloaded.Has("outline-4") {
		t.Fatal("directory publication lost pending or accounted call IDs")
	}
	// Recovering only from the WAL must retain the full old baseline too.
	if err := os.Remove(filepath.Join(live.Dir(), "meta/usage.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := host.NewDurableUsageMeter(store.NewStore(live.Dir())); err != nil {
		t.Fatal(err)
	}
	assertTotals(store.NewStore(live.Dir()))
}

func TestOutlineAllUsagePublishRejectsTailAppendedAfterCopy(t *testing.T) {
	live, candidate := projectAccountingStores(t)
	meter, err := host.NewDurableUsageMeter(live)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := copyPipelineOutlineAllUsageForPublish(live.Dir(), candidate.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DirectoryContentRoot(live.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record("late-call", "architect", projectUsageMessage("late-call", .5)); err != nil {
		t.Fatal(err)
	}
	if err := verifyPipelineOutlineAllUsageCopy(live.Dir(), proof); err == nil {
		t.Fatal("new live consumption could be replaced by stale candidate usage")
	}
}

func TestOutlineAllUsagePublishPreservesUnimportedLegacyCandidate(t *testing.T) {
	live, candidate := projectAccountingStores(t)
	if err := candidate.Usage.Save(domain.UsageState{Schema: domain.UsageSchemaVersion, Overall: domain.AgentUsageTotals{Input: 55, Cost: 1}, PerAgent: map[string]domain.AgentUsageTotals{}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(candidate.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyPipelineOutlineAllUsageForPublish(live.Dir(), candidate.Dir()); err == nil {
		t.Fatal("unimported legacy candidate usage was silently erased or blindly merged")
	}
	if after, err := store.DirectoryContentRoot(candidate.Dir()); err != nil || after != before {
		t.Fatal("legacy evidence changed after refusal")
	}
}
