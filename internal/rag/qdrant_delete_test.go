package rag

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestQdrantDeleteSourceBeforeFirstWrite(t *testing.T) {
	var created, written atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/fresh/points/delete":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"Collection fresh doesn't exist"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/collections/fresh":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"Collection fresh doesn't exist"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/fresh":
			created.Store(true)
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/fresh/points":
			if !created.Load() {
				t.Error("write preceded collection creation")
			}
			written.Store(true)
			_, _ = w.Write([]byte(`{"result":{"status":"completed"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise recovery of a cached collection that was removed externally too.
	client.ensured, client.vectorDim = true, 2
	if err := client.DeleteSourcePath(context.Background(), "premise.md"); err != nil {
		t.Fatal(err)
	}
	if client.ensured || client.vectorDim != 0 {
		t.Fatal("missing collection cache was not invalidated")
	}
	chunk := NormalizeChunk(domain.RAGChunk{SourcePath: "premise.md", Text: "新书事实"})
	if err := client.Write(context.Background(), VectorPoint{ID: chunk.ID, Vector: []float32{1, 0}, Chunk: chunk}); err != nil {
		t.Fatal(err)
	}
	if !written.Load() {
		t.Fatal("first vector was not written")
	}
}

func TestQdrantDeleteSourceDoesNotHideOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		deleteStatus, inspectStatus int
		inspectBody                 string
		wantStatus                  int
	}{
		{"unauthorized delete", http.StatusUnauthorized, 0, "", http.StatusUnauthorized},
		{"forbidden delete", http.StatusForbidden, 0, "", http.StatusForbidden},
		{"missing route existing collection", http.StatusNotFound, http.StatusOK, `{"result":{"config":{"params":{"vectors":{"size":2}}}}}`, http.StatusNotFound},
		{"unauthorized inspection", http.StatusNotFound, http.StatusUnauthorized, `{"status":{"error":"unauthorized"}}`, http.StatusUnauthorized},
		{"corrupt inspection", http.StatusNotFound, http.StatusOK, `not-json`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inspections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(tc.deleteStatus)
					_, _ = w.Write([]byte(`{"status":{"error":"delete failed"}}`))
					return
				}
				inspections.Add(1)
				if tc.inspectStatus == 0 {
					t.Error("unexpected inspection")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(tc.inspectStatus)
				_, _ = w.Write([]byte(tc.inspectBody))
			}))
			defer server.Close()
			client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			err = client.DeleteSourcePath(context.Background(), "premise.md")
			if err == nil {
				t.Fatal("source deletion error was hidden")
			}
			if tc.wantStatus != 0 && !isQdrantHTTPStatus(err, tc.wantStatus) {
				t.Fatalf("wrong error: %v", err)
			}
			if tc.wantStatus == 0 && !strings.Contains(err.Error(), "parse qdrant response") {
				t.Fatalf("corrupt response not reported: %v", err)
			}
			if tc.inspectStatus == 0 && inspections.Load() != 0 {
				t.Fatal("non-404 deletion inspected collection")
			}
		})
	}
}

func TestQdrantDeleteSourcePreservesCanceledContext(t *testing.T) {
	client, err := NewQdrantClient(QdrantClientConfig{URL: "http://127.0.0.1:1", Collection: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.DeleteSourcePath(ctx, "premise.md"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}
