package pricing

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestRegistryCalculate(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Set("model-a", ModelPricing{
		InputCostPerToken:      0.001,
		OutputCostPerToken:     0.002,
		CacheReadCostPerToken:  0.0005,
		CacheWriteCostPerToken: 0.0015,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cost, err := reg.Calculate("model-a", litellm.Usage{
		InputTokens:      100,
		OutputTokens:     20,
		CacheReadTokens:  40,
		CacheWriteTokens: 10,
	})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if !close(cost.Input, 0.06) || !close(cost.Output, 0.04) || !close(cost.CacheRead, 0.02) || !close(cost.CacheWrite, 0.015) {
		t.Fatalf("cost = %+v", cost)
	}
	if !close(cost.Total, 0.135) {
		t.Fatalf("total = %v", cost.Total)
	}
}

func close(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func TestCalculateDoesNotLoadImplicitly(t *testing.T) {
	_, err := Calculate("model-a", litellm.Usage{InputTokens: 1}, nil)
	if err == nil || !strings.Contains(err.Error(), "not in table") {
		t.Fatalf("expected missing table error, got %v", err)
	}
}

func TestRegistryLoadFromReader(t *testing.T) {
	reg := NewRegistry()
	err := reg.LoadFromReader(strings.NewReader(`{
		"sample_spec": {},
		"model-a": {
			"input_cost_per_token": 0.001,
			"output_cost_per_token": 0.002,
			"cache_read_input_token_cost": 0.0005,
			"litellm_provider": "openai",
			"max_input_tokens": 128000,
			"max_output_tokens": 4096,
			"supports_function_calling": true,
			"supports_vision": true,
			"supports_reasoning": true
		}
	}`))
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	price, ok := reg.Get("openai/model-a")
	if !ok || price.InputCostPerToken != 0.001 || price.OutputCostPerToken != 0.002 {
		t.Fatalf("price = %+v, ok=%v", price, ok)
	}
	caps, ok := reg.Capabilities("model-a")
	if !ok || caps.Provider != "openai" || !caps.SupportsTools || !caps.SupportsVision || !caps.SupportsReasoning {
		t.Fatalf("capabilities = %+v, ok=%v", caps, ok)
	}
}

func TestRegistryLoadFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pricing.json" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model-a":{"input_cost_per_token":0.001,"output_cost_per_token":0.002}}`))
	}))
	defer server.Close()

	reg := NewRegistry()
	if err := reg.LoadFromURL(context.Background(), server.URL+"/pricing.json"); err != nil {
		t.Fatalf("LoadFromURL: %v", err)
	}
	if _, ok := reg.Get("model-a"); !ok {
		t.Fatalf("model-a pricing not loaded")
	}
}
