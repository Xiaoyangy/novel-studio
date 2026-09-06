package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/agentcore"
)

func TestUsageTrackerPrefersProviderCostIncludingZero(t *testing.T) {
	tracker := NewUsageTracker(nil, nil)
	for _, reported := range []float64{0, 0.125} {
		cost, _, _ := tracker.resolveCost("gpt-6-astra", agentcore.Usage{Input: 300_000, Cost: &agentcore.Cost{Total: reported}})
		if cost != reported {
			t.Fatalf("Host replaced provider cost %v with registry cost %v", reported, cost)
		}
	}
}

func TestUsageTrackerPricesCodexBreakdownPerRequestAndReplaysMetadata(t *testing.T) {
	message := agentcore.Message{Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 300_000},
		Metadata: map[string]any{
			"codex_usage_breakdown_complete": true,
			"codex_usage_breakdown": []*agentcore.Usage{
				{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000},
				{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000},
			},
		},
	}
	tracker := NewUsageTracker(nil, nil)
	tracker.Record("writer", message)
	if cost, input, _, _, _ := tracker.Totals(); cost != 3 || input != 300_000 {
		t.Fatalf("aggregate completion was priced as one long request: cost=%v input=%d", cost, input)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "writer-ch01.jsonl")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	restored := NewUsageTracker(nil, nil)
	if count, err := restored.replayFile(path, "writer"); err != nil || count != 1 {
		t.Fatalf("replay count=%d err=%v", count, err)
	}
	if cost, input, _, _, _ := restored.Totals(); cost != 3 || input != 300_000 {
		t.Fatalf("replay discarded per-request pricing boundaries: cost=%v input=%d", cost, input)
	}
	message.Metadata["codex_usage_breakdown_complete"] = false
	message.Metadata["codex_usage_breakdown"] = []*agentcore.Usage{{Model: "gpt-6-astra", Input: 150_000}, nil}
	partial := NewUsageTracker(nil, nil)
	partial.Record("writer", message)
	if cost, input, _, _, _ := partial.Totals(); cost != 1.5 || input != 300_000 || partial.MissingAssistantUsage() == 0 {
		t.Fatalf("partial breakdown lost aggregate tokens or hid unknown cost: cost=%v input=%d missing=%d", cost, input, partial.MissingAssistantUsage())
	}
	message.Metadata = nil
	legacy := NewUsageTracker(nil, nil)
	legacy.Record("writer", message)
	if cost, input, _, _, _ := legacy.Totals(); cost != 0 || input != 300_000 || legacy.MissingAssistantUsage() == 0 {
		t.Fatalf("legacy aggregate without request boundaries received a fabricated tier: cost=%v input=%d", cost, input)
	}
}
