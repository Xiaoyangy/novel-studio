package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestQdrantClientWritesAndSearchesChunks(t *testing.T) {
	var storedPayload map[string]any
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections":
			_, _ = w.Write([]byte(`{"result":{"collections":[]}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/collections/test_collection":
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/test_collection":
			var body struct {
				Vectors struct {
					Size     int    `json:"size"`
					Distance string `json:"distance"`
				} `json:"vectors"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Vectors.Size != 2 || body.Vectors.Distance != "Cosine" {
				t.Fatalf("unexpected collection body: %+v", body)
			}
			created = true
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/test_collection/points":
			if !created {
				t.Fatal("points written before collection create")
			}
			var body struct {
				Points []struct {
					ID      string         `json:"id"`
					Vector  []float32      `json:"vector"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode points body: %v", err)
			}
			if len(body.Points) != 1 || !strings.Contains(body.Points[0].ID, "-") {
				t.Fatalf("unexpected point body: %+v", body)
			}
			storedPayload = body.Points[0].Payload
			_, _ = w.Write([]byte(`{"result":{"operation_id":1,"status":"completed"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test_collection/points/search":
			if storedPayload == nil {
				t.Fatal("search before payload stored")
			}
			resp := map[string]any{
				"result": []map[string]any{{
					"id":      qdrantPointID("chunk:qdrant"),
					"score":   0.91,
					"payload": storedPayload,
				}},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/collections/test_collection/points/count":
			_, _ = w.Write([]byte(`{"result":{"count":1}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "test_collection", ResetCollection: true})
	if err != nil {
		t.Fatalf("NewQdrantClient: %v", err)
	}
	chunk := NormalizeChunk(domain.RAGChunk{
		ID:         "chunk:qdrant",
		SourcePath: "meta/rag/test.md",
		SourceKind: "note",
		Facet:      "plot",
		Context:    "本地 Qdrant",
		Summary:    "语义命中",
		Text:       "夜租商铺和资产链推进。",
	})
	if err := client.Write(context.Background(), VectorPoint{ID: chunk.ID, Vector: []float32{1, 0}, Chunk: chunk}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	hits, err := client.Search(context.Background(), []float32{1, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Point.Chunk.ID != "chunk:qdrant" || hits[0].Score != 0.91 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	count, err := client.Count(context.Background(), true)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestQdrantSearchFiltersDesignOnlyBeforeTopK(t *testing.T) {
	type rankedPoint struct {
		chunk domain.RAGChunk
		score float64
	}
	var ranked []rankedPoint
	for i := range 18 {
		chunk := NormalizeChunk(domain.RAGChunk{
			SourcePath: "deconstruction-library/writing-techniques/craft-" + strconv.Itoa(i) + ".md",
			SourceKind: CraftSourceKind,
			Text:       "高相似度写作手法",
			Metadata: map[string]any{
				"source_kind": "chapter_summary_facts",
				"source_path": "meta/spoofed-fact.md",
			},
		})
		ranked = append(ranked, rankedPoint{chunk: chunk, score: 1 - float64(i)/100})
	}
	fact := NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/chapter_facts.md",
		SourceKind: "chapter_summary_facts",
		Text:       "本书已发生的经营事实",
	})
	ranked = append(ranked, rankedPoint{chunk: fact, score: 0.7})

	var sawFactFilter bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/collections/fact_collection/points/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Limit  int `json:"limit"`
			Filter *struct {
				MustNot []struct {
					Key   string `json:"key"`
					Match struct {
						Value string `json:"value"`
					} `json:"match"`
				} `json:"must_not"`
			} `json:"filter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		excluded := map[string]bool{}
		if body.Filter != nil {
			for _, condition := range body.Filter.MustNot {
				if condition.Key == "source_kind" {
					excluded[condition.Match.Value] = true
				}
			}
			sawFactFilter = excluded[CraftSourceKind] && excluded[BenchmarkSourceKind] && excluded[CalibrationSourceKind]
			if !sawFactFilter {
				t.Fatalf("incomplete design-only filter: %+v", body.Filter.MustNot)
			}
		}

		result := make([]map[string]any, 0, body.Limit)
		for _, point := range ranked {
			payload := chunkPayload(point.chunk)
			payloadKind, _ := payload["source_kind"].(string)
			if excluded[payloadKind] {
				continue
			}
			payload["chunk_id"] = point.chunk.ID
			payload["chunk"] = point.chunk
			result = append(result, map[string]any{
				"id": qdrantPointID(point.chunk.ID), "score": point.score, "payload": payload,
			})
			if len(result) == body.Limit {
				break
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	defer server.Close()

	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "fact_collection"})
	if err != nil {
		t.Fatalf("NewQdrantClient: %v", err)
	}
	unfiltered, err := client.Search(context.Background(), []float32{1, 0}, 18)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(unfiltered) != 18 {
		t.Fatalf("unfiltered hits = %d, want 18", len(unfiltered))
	}
	for _, hit := range unfiltered {
		if hit.Point.ID == fact.ID {
			t.Fatal("test fixture invalid: rank-19 fact unexpectedly entered unfiltered top 18")
		}
	}

	filtered, err := client.SearchWithOptions(context.Background(), []float32{1, 0}, 18, VectorSearchOptions{ExcludeDesignOnly: true})
	if err != nil {
		t.Fatalf("SearchWithOptions: %v", err)
	}
	if !sawFactFilter {
		t.Fatal("fact search did not send a pre-truncation Qdrant filter")
	}
	if len(filtered) != 1 || filtered[0].Point.ID != fact.ID {
		t.Fatalf("rank-19 fact should survive server-side filtering: %+v", filtered)
	}
}

func TestQdrantClientRetriesTransportEOF(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collections/eof_collection/points/count" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("test server does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"result":{"count":7}}`))
	}))
	defer server.Close()

	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "eof_collection"})
	if err != nil {
		t.Fatalf("NewQdrantClient: %v", err)
	}
	count, err := client.Count(context.Background(), true)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 7 || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("count=%d calls=%d", count, calls)
	}
}

func TestQdrantClientRetriesServerFailureAndTruncatedSuccessJSON(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			http.Error(w, "warming up", http.StatusServiceUnavailable)
		case 2:
			_, _ = w.Write([]byte(`{"result":`))
		default:
			_, _ = w.Write([]byte(`{"result":{"collections":[]}}`))
		}
	}))
	defer server.Close()

	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "health_collection"})
	if err != nil {
		t.Fatalf("NewQdrantClient: %v", err)
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls=%d, want 3", calls)
	}
}

func TestQdrantClientRejectsExistingDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/collections/dimension_collection" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":3,"distance":"Cosine"}}}}}`))
	}))
	defer server.Close()

	client, err := NewQdrantClient(QdrantClientConfig{URL: server.URL, Collection: "dimension_collection"})
	if err != nil {
		t.Fatalf("NewQdrantClient: %v", err)
	}
	err = client.EnsureCollection(context.Background(), 2)
	if err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("expected dimension mismatch, got %v", err)
	}
}

func TestCollectionNameIsStableAndSafe(t *testing.T) {
	got := CollectionName("Novel Studio", "/tmp/她的第二算法/output/novel")
	if got != CollectionName("Novel Studio", "/tmp/她的第二算法/output/novel") {
		t.Fatalf("collection name should be stable")
	}
	if !strings.HasPrefix(got, "novel_studio_") {
		t.Fatalf("unexpected collection prefix: %s", got)
	}
}
