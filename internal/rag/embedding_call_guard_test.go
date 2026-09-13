package rag

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEmbeddingCallGuardRefusesEveryHTTPRetry(t *testing.T) {
	deadline := errors.New("fake durable delivery deadline")
	var expired atomic.Bool
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		expired.Store(true)
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	embedder, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{Model: "fixture", BaseURL: srv.URL, BeforeCall: func() error {
		if expired.Load() {
			return deadline
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.Embed(context.Background(), "fake data"); !errors.Is(err, deadline) {
		t.Fatalf("retry did not retain guard cause: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests after deadline: %d", requests.Load())
	}
	if _, err := embedder.Embed(context.Background(), "new attempt"); !errors.Is(err, deadline) {
		t.Fatal("new embedding escaped guard")
	}
	if requests.Load() != 1 {
		t.Fatal("denied invocation reached HTTP")
	}
}

func TestEmbeddingCallGuardKeepsInflightResponseAndLocalModelDoesNotRestart(t *testing.T) {
	deadline := errors.New("fake durable delivery deadline")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var expired atomic.Bool
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("in-flight embedding was cancelled")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0]}]}`)
	}))
	defer srv.Close()
	guard := func() error {
		if expired.Load() {
			return deadline
		}
		return nil
	}
	embedder, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{Model: "fixture", BaseURL: srv.URL, BeforeCall: guard})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		vec, err := embedder.Embed(context.Background(), "data")
		if err == nil && (len(vec) != 2 || vec[0] != 1) {
			err = errors.New("lost completed vector")
		}
		done <- err
	}()
	<-entered
	expired.Store(true)
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalGGUFEmbedder(LocalGGUFConfig{BeforeCall: guard, GGUFPath: "/must-not-start-a-model.gguf"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Embed(context.Background(), "data"); !errors.Is(err, deadline) {
		t.Fatalf("local model attempted inference/restart instead of preserving deadline: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatal("in-flight completion caused another request")
	}
}
