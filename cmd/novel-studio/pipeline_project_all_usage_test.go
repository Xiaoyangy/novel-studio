package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func projectAccountingStores(t *testing.T) (*store.Store, *store.Store) {
	t.Helper()
	live, shadow := store.NewStore(t.TempDir()), store.NewStore(t.TempDir())
	for _, st := range []*store.Store{live, shadow} {
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
	}
	return live, shadow
}

func projectUsageMessage(id string, cost float64) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("PRIVATE-REASONING-MUST-NOT-BE-AUDITED")},
		Usage: &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 100, Output: 10, Cost: &agentcore.Cost{Total: cost}}, Metadata: map[string]any{"usage_audit_id": id}}
}

func TestPipelineProjectAllAccountingImportsIndependentLedgerOnceAndKeepsHistoryUnknown(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	row := domain.CharacterAgentUsage{UsageID: "existing-character", GenerationID: "pg2_existing", AgentID: "ca_one", Role: "character", Input: 300_000, CostUSD: 3, CostSource: "estimated", Attempts: 2}
	if err := shadow.CharacterAgents.AppendUsage(row); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_existing")
		if err != nil {
			t.Fatal(err)
		}
		cost, input, _, _, _ := a.meter.Tracker().Totals()
		if cost != 3 || input != 300_000 {
			t.Fatalf("independent ledger duplicated or repriced: cost=%v input=%d", cost, input)
		}
		if a.meter.Tracker().MissingAssistantUsage() != 1 {
			t.Fatalf("historical planner gap became known free: %d", a.meter.Tracker().MissingAssistantUsage())
		}
		if err := a.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPipelineProjectAllAccountingCoveredCharacterCallsDoNotDoubleCountLedger(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_new")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.hooks().CoverCharacterUsage("character-group"); err != nil {
		t.Fatal(err)
	}
	msg := projectUsageMessage("character-call", .2)
	msg.Metadata["usage_group_id"] = "character-group"
	a.record("character_ca_one", msg)
	if err := a.importCharacterUsage(domain.CharacterAgentUsage{UsageID: "character-group", GenerationID: "pg2_new", AgentID: "ca_one", Role: "character", Input: 100, CostUSD: .2, CostSource: "reported"}); err != nil {
		t.Fatal(err)
	}
	if cost, input, _, _, _ := a.meter.Tracker().Totals(); cost != .2 || input != 100 {
		t.Fatalf("round ledger double-counted per-call bill: %v/%d", cost, input)
	}
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineProjectAllAccountingRejectsChangedImportedUsageIdentity(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_import_conflict")
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	row := domain.CharacterAgentUsage{UsageID: "immutable-import", GenerationID: "pg2_import_conflict", AgentID: "one", Role: "character", Input: 100, CostUSD: .2, CostSource: "reported"}
	if err := a.importCharacterUsage(row); err != nil {
		t.Fatal(err)
	}
	row.CostUSD = .4
	if err := a.importCharacterUsage(row); err == nil {
		t.Fatal("changed amount reused an already imported usage_id")
	}
}

func TestPipelineProjectAllAccountingUsesConfiguredBudgetWithoutDefaultCap(t *testing.T) {
	for _, mode := range []string{"hard", "soft", "unset"} {
		t.Run(mode, func(t *testing.T) {
			live, shadow := projectAccountingStores(t)
			cfg := bootstrap.Config{}
			if mode != "unset" {
				cfg.Budget = bootstrap.BudgetConfig{BookUSD: .1, WarnRatio: .8, HardStop: mode == "hard"}
			}
			a, err := newPipelineProjectAllAccounting(context.Background(), cfg, live, shadow, "pg2_budget")
			if err != nil {
				t.Fatal(err)
			}
			a.record("project_all_planner", projectUsageMessage("planner-one", .2))
			if mode == "hard" && context.Cause(a.ctx) == nil {
				t.Fatal("hard budget did not cancel")
			}
			if mode != "hard" && a.ctx.Err() != nil {
				t.Fatal("soft or disabled budget stopped inside agent")
			}
			boundaryErr := a.afterAgent()
			if mode == "unset" && boundaryErr != nil || mode != "unset" && boundaryErr == nil {
				t.Fatalf("incorrect budget boundary for %s: %v", mode, boundaryErr)
			}
			_ = a.close()
			if mode != "unset" {
				if resumed, err := newPipelineProjectAllAccounting(context.Background(), cfg, live, shadow, "pg2_budget"); err == nil {
					_ = resumed.close()
					t.Fatal("resume ignored spent book budget")
				}
			}
		})
	}
}

func TestPipelineProjectAllAccountingPreservesCanonAndSourceRoots(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	if err := live.Progress.Init("accounting-only", 3); err != nil {
		t.Fatal(err)
	}
	if err := live.Outline.SavePremise("immutable foundation"); err != nil {
		t.Fatal(err)
	}
	progress, err := live.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	foundationBefore, err := pipelineProjectAllFoundationSnapshotRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	canonBefore, err := pipelineProjectAllLiveCanonRoot(live.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_roots")
	if err != nil {
		t.Fatal(err)
	}
	message := projectUsageMessage("planner-breakdown", 0)
	message.Usage = &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 300_000}
	message.Metadata["codex_usage_breakdown"] = []*agentcore.Usage{{Input: 150_000}, {Input: 150_000}}
	message.Metadata["codex_usage_breakdown_complete"] = true
	a.record("project_all_planner", message)
	if err := a.close(); err != nil {
		t.Fatal(err)
	}
	foundationAfter, err := pipelineProjectAllFoundationSnapshotRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	canonAfter, err := pipelineProjectAllLiveCanonRoot(live.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	if foundationBefore != foundationAfter || canonBefore != canonAfter {
		t.Fatal("live accounting modified canonical or planning source identity")
	}
	state, err := live.Usage.Load()
	if err != nil || state == nil {
		t.Fatalf("accounting not durable: %v", err)
	}
	if state.Overall.Cost != 3 || state.Overall.Input != 300_000 {
		t.Fatalf("aggregate crossed pricing tier: %+v", state.Overall)
	}
	paths, err := filepath.Glob(filepath.Join(live.Dir(), "meta/runtime/*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "PRIVATE-REASONING") {
			t.Fatal("private model content leaked into accounting audit")
		}
	}
}

func TestPipelineProjectAllAccountingPersistenceFailureCancelsAndReturns(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_save_failure")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(live.Dir(), "meta/usage.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	a.record("project_all_planner", projectUsageMessage("unflushed-planner", .2))
	if !errors.Is(a.ctx.Err(), context.Canceled) || a.close() == nil {
		t.Fatal("usage persistence failure was silent")
	}
}
