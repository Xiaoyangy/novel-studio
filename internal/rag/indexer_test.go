package rag

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len([]rune(text)))}, nil
}

type eofThenEmbedder struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (e *eofThenEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= e.failures {
		return nil, io.EOF
	}
	return []float32{1, 0}, nil
}

type eofBatchWriter struct {
	mu       sync.Mutex
	failures int
	calls    int
	points   []VectorPoint
}

func TestChunkPayloadRejectsReservedMetadataCollisions(t *testing.T) {
	chunk := domain.RAGChunk{
		ID:         "chunk:trusted",
		SourcePath: "deconstruction-library/writing-techniques/dialogue.md",
		SourceKind: CraftSourceKind,
		Facet:      "dialogue",
		ParentID:   "parent:trusted",
		Hash:       "hash:trusted",
		Context:    "context:trusted",
		Summary:    "summary:trusted",
		Keywords:   []string{"可信关键词"},
		Metadata: map[string]any{
			"source_path": "meta/spoof.md",
			"source_kind": "chapter_summary_facts",
			"facet":       "spoof",
			"parent_id":   "parent:spoof",
			"hash":        "hash:spoof",
			"context":     "context:spoof",
			"summary":     "summary:spoof",
			"keywords":    []string{"伪造关键词"},
			"chunk_id":    "chunk:spoof",
			"chunk":       "spoof",
			"custom":      "kept",
		},
	}
	payload := chunkPayload(chunk)
	want := map[string]any{
		"source_path": chunk.SourcePath,
		"source_kind": chunk.SourceKind,
		"facet":       chunk.Facet,
		"parent_id":   chunk.ParentID,
		"hash":        chunk.Hash,
		"context":     chunk.Context,
		"summary":     chunk.Summary,
	}
	for key, value := range want {
		if payload[key] != value {
			t.Fatalf("payload[%q] = %#v, want trusted %#v", key, payload[key], value)
		}
	}
	if keywords, ok := payload["keywords"].([]string); !ok || len(keywords) != 1 || keywords[0] != "可信关键词" {
		t.Fatalf("payload keywords were spoofed: %#v", payload["keywords"])
	}
	if _, ok := payload["chunk_id"]; ok {
		t.Fatalf("metadata injected reserved chunk_id: %#v", payload["chunk_id"])
	}
	if _, ok := payload["chunk"]; ok {
		t.Fatalf("metadata injected reserved chunk: %#v", payload["chunk"])
	}
	if payload["custom"] != "kept" {
		t.Fatalf("non-reserved metadata was lost: %#v", payload["custom"])
	}
}

func (w *eofBatchWriter) Write(ctx context.Context, point VectorPoint) error {
	return w.WriteBatch(ctx, []VectorPoint{point})
}

func (w *eofBatchWriter) WriteBatch(_ context.Context, points []VectorPoint) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls <= w.failures {
		return io.ErrUnexpectedEOF
	}
	w.points = append(w.points, points...)
	return nil
}

func TestBuildIndexRetriesEOFInEmbeddingAndBatchWrite(t *testing.T) {
	embedder := &eofThenEmbedder{failures: 2}
	writer := &eofBatchWriter{failures: 2}
	result, err := BuildIndex(context.Background(), []domain.RAGChunk{
		{SourcePath: "outline.md", SourceKind: "planning", Text: "青山县第一家返乡门店。"},
		{SourcePath: "world_rules.md", SourceKind: "world", Text: "投资必须留下真实经营结果。"},
	}, nil, domain.RAGIndexConfig{
		EmbeddingConcurrency: 1, QdrantWriteConcurrency: 1, VectorBatchSize: 16,
	}, embedder, writer)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if embedder.calls != 4 {
		t.Fatalf("embedding calls=%d, want 4", embedder.calls)
	}
	if writer.calls != 3 || len(writer.points) != 2 {
		t.Fatalf("batch retries not applied: calls=%d points=%d", writer.calls, len(writer.points))
	}
	if result.Embedded != 2 || result.Written != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBuildIndexPermanentEOFWritesNothing(t *testing.T) {
	embedder := &eofThenEmbedder{failures: 100}
	writer := &eofBatchWriter{}
	_, err := BuildIndex(context.Background(), []domain.RAGChunk{
		{SourcePath: "outline.md", SourceKind: "planning", Text: "第一章经营计划。"},
	}, nil, domain.RAGIndexConfig{EmbeddingConcurrency: 1}, embedder, writer)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "eof") {
		t.Fatalf("expected EOF failure, got %v", err)
	}
	if writer.calls != 0 || len(writer.points) != 0 {
		t.Fatalf("vectors were written before all embeddings succeeded: calls=%d points=%d", writer.calls, len(writer.points))
	}
}

type fakeWriter struct {
	mu     sync.Mutex
	points []VectorPoint
}

func (w *fakeWriter) Write(_ context.Context, point VectorPoint) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.points = append(w.points, point)
	return nil
}

func TestBuildIndexDedupesAndPreservesFacets(t *testing.T) {
	writer := &fakeWriter{}
	chunks := []domain.RAGChunk{
		{SourcePath: "output/novel/meta/writing_assets.md", SourceKind: "note", Facet: "craft", Text: "开篇用具体物件压住设定。"},
		{SourcePath: "output/novel/meta/writing_assets.md", SourceKind: "note", Facet: "craft", Text: "开篇用具体物件压住设定。"},
		{SourcePath: "output/novel/world_rules.md", SourceKind: "note", Facet: "world", Text: "鬼市只在雨夜开门。"},
	}
	result, err := BuildIndex(context.Background(), chunks, nil, domain.RAGIndexConfig{
		EmbeddingConcurrency:   3,
		QdrantWriteConcurrency: 2,
		Collection:             "novel",
	}, fakeEmbedder{}, writer)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if result.Embedded != 2 || result.Written != 2 || result.SkippedDup != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if len(writer.points) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(writer.points))
	}
	if writer.points[0].Payload["facet"] == "" {
		t.Fatalf("expected facet payload, got %+v", writer.points[0].Payload)
	}
	if len(result.State.ChunkHashes) != 2 {
		t.Fatalf("expected 2 hashes, got %d", len(result.State.ChunkHashes))
	}
}

type capturingEmbedder struct {
	mu    sync.Mutex
	texts []string
}

func (e *capturingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.texts = append(e.texts, text)
	return []float32{1}, nil
}

func TestBuildIndexEmbedsContextualChunkText(t *testing.T) {
	embedder := &capturingEmbedder{}
	writer := &fakeWriter{}
	chunks := []domain.RAGChunk{
		{
			SourcePath: "output/novel/meta/writing_assets.md",
			SourceKind: "note",
			Facet:      "craft",
			Summary:    "开篇用具体物件压住设定。",
			Text:       "门铃、欠费单和账本要承担规则推进。",
			Metadata: map[string]any{
				"title": "夜租商铺",
				"tags":  []any{"资产链", "诡异末日"},
			},
		},
	}
	result, err := BuildIndex(context.Background(), chunks, nil, domain.RAGIndexConfig{}, embedder, writer)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if result.Embedded != 1 || result.Written != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if len(embedder.texts) != 1 {
		t.Fatalf("expected one embedding input, got %d", len(embedder.texts))
	}
	if !strings.Contains(embedder.texts[0], "夜租商铺") {
		t.Fatalf("expected embedding text to include derived context, got %q", embedder.texts[0])
	}
	if !strings.Contains(embedder.texts[0], "开篇用具体物件") {
		t.Fatalf("expected embedding text to include summary, got %q", embedder.texts[0])
	}
	if !strings.Contains(embedder.texts[0], "资产链") {
		t.Fatalf("expected embedding text to include keywords, got %q", embedder.texts[0])
	}
	if writer.points[0].Payload["context"] == "" {
		t.Fatalf("expected payload context, got %+v", writer.points[0].Payload)
	}
}
