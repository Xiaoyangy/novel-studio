package models

// Keep official new-release fallbacks separate from generated OpenRouter data.
// A generated entry wins once the generator knows the model, and normal runtime
// registry refresh can update these baseline prices. CLI subscription usage is
// still an estimate at API list prices, never an invoice.
// Source (verified 2026-09-05): https://developers.openai.com/api/docs/models/gpt-6-astra
var officialModelFallbacks = []ModelEntry{{
	Provider: "openai", ID: "gpt-6-astra", Name: "GPT-6 Astra", ContextWindow: 1_050_000, MaxTokens: 128_000,
	InputCostPer1M: 10, OutputCostPer1M: 50, CacheReadCostPer1M: 1, CacheWriteCostPer1M: 12.50,
}}

func appendOfficialModelFallbacks(entries []ModelEntry) []ModelEntry {
	for _, fallback := range officialModelFallbacks {
		if _, exists := lookupModelEntry(entries, fallback.Provider, fallback.ID); !exists {
			entries = append(entries, fallback)
		}
	}
	return entries
}
