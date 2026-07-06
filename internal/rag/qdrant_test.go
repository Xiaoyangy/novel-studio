package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCollectionNameIsStableAndSafe(t *testing.T) {
	got := CollectionName("Novel Studio", "/tmp/她的第二算法/output/novel")
	if got != CollectionName("Novel Studio", "/tmp/她的第二算法/output/novel") {
		t.Fatalf("collection name should be stable")
	}
	if !strings.HasPrefix(got, "novel_studio_") {
		t.Fatalf("unexpected collection prefix: %s", got)
	}
}
