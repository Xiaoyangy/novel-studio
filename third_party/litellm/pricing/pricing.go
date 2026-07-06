package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/voocel/litellm"
)

const DefaultURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type ModelPricing struct {
	InputCostPerToken      float64 `json:"input_cost_per_token"`
	OutputCostPerToken     float64 `json:"output_cost_per_token"`
	CacheReadCostPerToken  float64 `json:"cache_read_input_token_cost,omitempty"`
	CacheWriteCostPerToken float64 `json:"cache_creation_input_token_cost,omitempty"`
}

type ModelCapabilities struct {
	Provider          string `json:"litellm_provider"`
	MaxInputTokens    int    `json:"max_input_tokens"`
	MaxOutputTokens   int    `json:"max_output_tokens"`
	SupportsTools     bool   `json:"supports_function_calling"`
	SupportsVision    bool   `json:"supports_vision"`
	SupportsReasoning bool   `json:"supports_reasoning"`
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
}

type entry struct {
	inputCostPerToken      float64
	outputCostPerToken     float64
	cacheReadCostPerToken  float64
	cacheWriteCostPerToken float64
	hasInputPricing        bool
	hasOutputPricing       bool
	provider               string
	maxInputTokens         int
	maxOutputTokens        int
	supportsTools          bool
	supportsVision         bool
	supportsReasoning      bool
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]entry)}
}

func (r *Registry) Set(model string, price ModelPricing) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("pricing: model is required")
	}
	if err := validatePricing(price); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]entry)
	}
	e := r.entries[model]
	e.inputCostPerToken = price.InputCostPerToken
	e.outputCostPerToken = price.OutputCostPerToken
	e.cacheReadCostPerToken = price.CacheReadCostPerToken
	e.cacheWriteCostPerToken = price.CacheWriteCostPerToken
	e.hasInputPricing = true
	e.hasOutputPricing = true
	r.entries[model] = e
	return nil
}

func (r *Registry) Get(model string) (ModelPricing, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.lookup(model)
	if !ok || !e.hasInputPricing || !e.hasOutputPricing {
		return ModelPricing{}, false
	}
	return ModelPricing{
		InputCostPerToken:      e.inputCostPerToken,
		OutputCostPerToken:     e.outputCostPerToken,
		CacheReadCostPerToken:  e.cacheReadCostPerToken,
		CacheWriteCostPerToken: e.cacheWriteCostPerToken,
	}, true
}

func (r *Registry) Capabilities(model string) (ModelCapabilities, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.lookup(model)
	if !ok {
		return ModelCapabilities{}, false
	}
	return ModelCapabilities{
		Provider:          e.provider,
		MaxInputTokens:    e.maxInputTokens,
		MaxOutputTokens:   e.maxOutputTokens,
		SupportsTools:     e.supportsTools,
		SupportsVision:    e.supportsVision,
		SupportsReasoning: e.supportsReasoning,
	}, true
}

func (r *Registry) Calculate(model string, usage litellm.Usage) (Cost, error) {
	price, ok := r.Get(model)
	if !ok {
		return Cost{}, fmt.Errorf("pricing: model %q is not loaded", model)
	}
	return Calculate(model, usage, map[string]ModelPricing{model: price})
}

func (r *Registry) LoadFromURL(ctx context.Context, url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("pricing: url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("pricing: create request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pricing: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pricing: fetch HTTP %d", resp.StatusCode)
	}
	return r.LoadFromReader(resp.Body)
}

func (r *Registry) LoadFromReader(reader io.Reader) error {
	entries, err := parseRegistry(reader)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.entries = entries
	r.mu.Unlock()
	return nil
}

func Calculate(model string, usage litellm.Usage, table map[string]ModelPricing) (Cost, error) {
	price, ok := table[model]
	if !ok {
		return Cost{}, fmt.Errorf("pricing: model %q is not in table", model)
	}
	if err := validatePricing(price); err != nil {
		return Cost{}, err
	}
	nonCachedInput := usage.InputTokens - usage.CacheReadTokens
	if nonCachedInput < 0 {
		nonCachedInput = usage.InputTokens
	}
	inputCost := float64(nonCachedInput) * price.InputCostPerToken
	outputCost := float64(usage.OutputTokens) * price.OutputCostPerToken
	cacheReadRate := price.CacheReadCostPerToken
	if cacheReadRate == 0 {
		cacheReadRate = price.InputCostPerToken
	}
	cacheWriteRate := price.CacheWriteCostPerToken
	if cacheWriteRate == 0 {
		cacheWriteRate = price.InputCostPerToken
	}
	cacheReadCost := float64(usage.CacheReadTokens) * cacheReadRate
	cacheWriteCost := float64(usage.CacheWriteTokens) * cacheWriteRate
	return Cost{
		Input:      inputCost,
		Output:     outputCost,
		CacheRead:  cacheReadCost,
		CacheWrite: cacheWriteCost,
		Total:      inputCost + outputCost + cacheReadCost + cacheWriteCost,
		Currency:   "USD",
	}, nil
}

func parseRegistry(reader io.Reader) (map[string]entry, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(reader).Decode(&raw); err != nil {
		return nil, fmt.Errorf("pricing: decode registry: %w", err)
	}
	entries := make(map[string]entry, len(raw))
	for model, rawData := range raw {
		if model == "sample_spec" {
			continue
		}
		var parsed struct {
			InputCostPerToken      *float64 `json:"input_cost_per_token"`
			OutputCostPerToken     *float64 `json:"output_cost_per_token"`
			CacheReadCostPerToken  *float64 `json:"cache_read_input_token_cost"`
			CacheWriteCostPerToken *float64 `json:"cache_creation_input_token_cost"`
			Provider               string   `json:"litellm_provider"`
			MaxInputTokens         int      `json:"max_input_tokens"`
			MaxOutputTokens        int      `json:"max_output_tokens"`
			SupportsTools          bool     `json:"supports_function_calling"`
			SupportsVision         bool     `json:"supports_vision"`
			SupportsReasoning      bool     `json:"supports_reasoning"`
		}
		if err := json.Unmarshal(rawData, &parsed); err != nil {
			return nil, fmt.Errorf("pricing: decode model %q: %w", model, err)
		}
		e := entry{
			provider:          parsed.Provider,
			maxInputTokens:    parsed.MaxInputTokens,
			maxOutputTokens:   parsed.MaxOutputTokens,
			supportsTools:     parsed.SupportsTools,
			supportsVision:    parsed.SupportsVision,
			supportsReasoning: parsed.SupportsReasoning,
		}
		if parsed.InputCostPerToken != nil {
			e.inputCostPerToken = *parsed.InputCostPerToken
			e.hasInputPricing = true
		}
		if parsed.OutputCostPerToken != nil {
			e.outputCostPerToken = *parsed.OutputCostPerToken
			e.hasOutputPricing = true
		}
		if parsed.CacheReadCostPerToken != nil {
			e.cacheReadCostPerToken = *parsed.CacheReadCostPerToken
		}
		if parsed.CacheWriteCostPerToken != nil {
			e.cacheWriteCostPerToken = *parsed.CacheWriteCostPerToken
		}
		entries[model] = e
	}
	return entries, nil
}

func (r *Registry) lookup(model string) (entry, bool) {
	if r == nil || r.entries == nil {
		return entry{}, false
	}
	if e, ok := r.entries[model]; ok {
		return e, true
	}
	if _, after, ok := strings.Cut(model, "/"); ok {
		e, found := r.entries[after]
		return e, found
	}
	return entry{}, false
}

func validatePricing(price ModelPricing) error {
	if price.InputCostPerToken < 0 {
		return fmt.Errorf("pricing: input cost per token must be non-negative")
	}
	if price.OutputCostPerToken < 0 {
		return fmt.Errorf("pricing: output cost per token must be non-negative")
	}
	if price.CacheReadCostPerToken < 0 {
		return fmt.Errorf("pricing: cache read cost per token must be non-negative")
	}
	if price.CacheWriteCostPerToken < 0 {
		return fmt.Errorf("pricing: cache write cost per token must be non-negative")
	}
	return nil
}
