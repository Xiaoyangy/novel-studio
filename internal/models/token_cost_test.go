package models

import (
	"math"
	"testing"
)

func TestTokenCostUsesSharedCacheAccountingAndReportedPrecedence(t *testing.T) {
	entry := ModelEntry{InputCostPer1M: 3, OutputCostPer1M: 15, CacheReadCostPer1M: 0.3, CacheWriteCostPer1M: 3.75}
	usage := TokenUsage{Input: 100_000, Output: 20_000, CacheRead: 80_000, CacheWrite: 10_000}
	quote := ResolveTokenCost(usage, &entry, nil)
	if math.Abs(quote.USD-0.4215) > 1e-12 || quote.Source != CostSourceEstimated {
		t.Fatalf("cache reads were double charged or write tokens omitted: %+v", quote)
	}
	for _, reported := range []float64{0, 0.123} {
		quote := ResolveTokenCost(usage, &entry, &reported)
		if quote.USD != reported || quote.Source != CostSourceReported {
			t.Fatalf("provider cost did not take precedence: %+v", quote)
		}
	}
	if quote := ResolveTokenCost(usage, nil, nil); quote.Source != CostSourceUnknown || quote.USD != 0 {
		t.Fatalf("missing model price was not unknown: %+v", quote)
	}
	if quote := ResolveTokenCost(usage, &ModelEntry{}, nil); quote.Source != CostSourceUnknown {
		t.Fatalf("all-zero missing prices were presented as measured free usage: %+v", quote)
	}
}

func TestOfficialGPT6FallbackAndPerRequestLongContextTier(t *testing.T) {
	entry, ok := NewModelRegistry().Resolve("gpt-6-astra")
	if !ok || entry.ContextWindow != 1_050_000 || entry.MaxTokens != 128_000 || entry.InputCostPer1M != 10 || entry.OutputCostPer1M != 50 || entry.CacheReadCostPer1M != 1 || entry.CacheWriteCostPer1M != 12.5 {
		t.Fatalf("official fallback missing or inconsistent: %+v", entry)
	}
	for _, tc := range []struct {
		usage TokenUsage
		want  float64
	}{
		{TokenUsage{Input: 272_000, CacheRead: 72_000, Output: 1000}, 2.122},
		{TokenUsage{Input: 272_001, CacheRead: 72_000, Output: 1000}, 4.21902},
		{TokenUsage{Input: 272_000, CacheWrite: 1, Output: 1}, 5.4401},
	} {
		if got := ComputeTokenCost(tc.usage, *entry); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("usage=%+v cost=%v want=%v", tc.usage, got, tc.want)
		}
	}
	// Two short calls do not become a single long-context call when summed.
	if got := 2 * ComputeTokenCost(TokenUsage{Input: 150_000}, *entry); got != 3 {
		t.Fatalf("short-call accounting changed: %v", got)
	}
	custom := *entry
	custom.ID = "another-model"
	if got := ComputeTokenCost(TokenUsage{Input: 300_000}, custom); got != 3 {
		t.Fatalf("GPT-6-specific tier was applied to another model: %v", got)
	}
	existing := *entry
	existing.InputCostPer1M = 11
	rows := appendOfficialModelFallbacks([]ModelEntry{existing})
	if len(rows) != 1 || rows[0].InputCostPer1M != 11 {
		t.Fatal("fallback replaced a generated/refreshed entry")
	}
}
