package models

import (
	"encoding/json"
	"math"
)

const (
	CostSourceReported  = "reported"
	CostSourceEstimated = "estimated"
	CostSourceUnknown   = "unknown"
)

// TokenUsage uses the provider-normalized convention: Input includes cache
// reads, while cache creation tokens are reported separately in CacheWrite.
// This package deliberately has no dependency on Host or any model adapter.
type TokenUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// TokenUsageRecord is the bounded adapter-neutral shape of one actual request
// in an aggregated completion. It can round-trip through message metadata
// without importing agentcore into the pricing package.
type TokenUsageRecord struct {
	TokenUsage
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Cost     *struct {
		Total float64 `json:"total"`
	} `json:"cost,omitempty"`
}

func DecodeTokenUsageBreakdown(value any) ([]*TokenUsageRecord, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var calls []*TokenUsageRecord
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, err
	}
	return calls, nil
}

type TokenCost struct {
	USD    float64
	Source string
}

// ResolveTokenCost prefers a provider cost (including an explicit zero) over
// token-price estimates. An absent price is unknown, not a measured free call.
func ResolveTokenCost(u TokenUsage, entry *ModelEntry, reported *float64) TokenCost {
	if reported != nil && *reported >= 0 && !math.IsNaN(*reported) && !math.IsInf(*reported, 0) {
		return TokenCost{USD: *reported, Source: CostSourceReported}
	}
	if entry == nil || (entry.InputCostPer1M <= 0 && entry.OutputCostPer1M <= 0 && entry.CacheReadCostPer1M <= 0 && entry.CacheWriteCostPer1M <= 0) {
		return TokenCost{Source: CostSourceUnknown}
	}
	return TokenCost{USD: ComputeTokenCost(u, *entry), Source: CostSourceEstimated}
}

// ComputeTokenCost is shared by Host and independent character/Arbiter loops.
func ComputeTokenCost(u TokenUsage, entry ModelEntry) float64 {
	entry = tokenTierPrices(u, entry)
	nonCachedInput := u.Input - u.CacheRead
	if nonCachedInput < 0 {
		// Preserve the existing Host fallback for malformed provider counts.
		nonCachedInput = u.Input
	}
	return float64(nonCachedInput)*entry.InputCostPer1M/1_000_000 +
		float64(u.Output)*entry.OutputCostPer1M/1_000_000 +
		float64(u.CacheRead)*entry.CacheReadCostPer1M/1_000_000 +
		float64(u.CacheWrite)*entry.CacheWriteCostPer1M/1_000_000
}

func ComputeTokenCacheSavings(u TokenUsage, entry ModelEntry) float64 {
	entry = tokenTierPrices(u, entry)
	if u.CacheRead <= 0 || entry.InputCostPer1M <= 0 || entry.InputCostPer1M <= entry.CacheReadCostPer1M {
		return 0
	}
	return float64(u.CacheRead) * (entry.InputCostPer1M - entry.CacheReadCostPer1M) / 1_000_000
}

func tokenTierPrices(u TokenUsage, entry ModelEntry) ModelEntry {
	// Official GPT-6 Astra standard pricing, verified 2026-09-05:
	// https://developers.openai.com/api/docs/models/gpt-6-astra
	// The threshold applies per request, not to the sum of an Agent loop.
	if TokenUsageCrossesPricingTier(u, entry) {
		entry.InputCostPer1M *= 2
		entry.CacheReadCostPer1M *= 2
		entry.CacheWriteCostPer1M *= 2
		entry.OutputCostPer1M *= 1.5
	}
	return entry
}

// TokenUsageCrossesPricingTier indicates when an aggregate needs actual
// per-request boundaries before its applicable rate can be determined.
func TokenUsageCrossesPricingTier(u TokenUsage, entry ModelEntry) bool {
	return entry.Provider == "openai" && entry.ID == "gpt-6-astra" && u.Input+u.CacheWrite > 272_000
}
