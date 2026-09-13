package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRAGCallGuardReachesInferenceNotConstructionOrLocalHash(t *testing.T) {
	denied := errors.New("frozen wall deadline")
	var checks, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/embeddings" {
			t.Errorf("embedding route changed: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0]}]}`)
	}))
	defer server.Close()
	cfg := Config{BeforeProviderCall: func() error { checks.Add(1); return denied }}
	cfg.RAG.Embedding = RAGEmbeddingConfig{Enabled: true, Provider: "openai", Model: "test-vector", BaseURL: server.URL}
	embedder, enabled, err := NewRAGEmbedder(cfg)
	if err != nil || !enabled {
		t.Fatalf("construct embedder: %v", err)
	}
	if checks.Load() != 0 {
		t.Fatal("constructing an embedder consumed an inference guard")
	}
	if _, err := embedder.Embed(context.Background(), "request"); !errors.Is(err, denied) {
		t.Fatalf("bootstrap lost provider guard: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("deadline reached real HTTP")
	}
	cfg.RAG.Embedding = RAGEmbeddingConfig{Enabled: true, Provider: "local", Model: "local-hash"}
	local, _, err := NewRAGEmbedder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := checks.Load()
	if _, err := local.Embed(context.Background(), "pure deterministic data transform"); err != nil {
		t.Fatal(err)
	}
	if checks.Load() != before {
		t.Fatal("pure local hash was treated as a model call")
	}
	cfg.BeforeProviderCall = nil
	cfg.RAG.Embedding = RAGEmbeddingConfig{Enabled: true, Provider: "openai", Model: "test-vector", BaseURL: server.URL}
	embedder, _, err = NewRAGEmbedder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.Embed(context.Background(), "request"); err != nil || calls.Load() != 1 {
		t.Fatalf("nil guard changed legacy inference: calls=%d err=%v", calls.Load(), err)
	}
}
