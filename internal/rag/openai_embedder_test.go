package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIEmbedderRetriesTransientFailures(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/embeddings" {
			t.Fatalf("unexpected path %s", got)
		}
		if atomic.AddInt32(&calls, 1) <= 2 {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float32{0.1, 0.2, 0.3}}},
		})
	}))
	defer srv.Close()

	embedder, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: srv.URL,
		Model:   "test-embedding",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	vec, err := embedder.Embed(context.Background(), "维度探针")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("unexpected vector: %+v", vec)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}

func TestOpenAIEmbedderDoesNotRetryAuthFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "missing key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	embedder, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: srv.URL,
		Model:   "test-embedding",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	_, err = embedder.Embed(context.Background(), "维度探针")
	if err == nil || !strings.Contains(err.Error(), "after 1 attempts") {
		t.Fatalf("expected non-retry auth error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}
