package rag

import (
	"math"
	"strconv"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestSearchVectorStoreSkipsDimensionDriftAndNonFiniteVectors(t *testing.T) {
	valid := NormalizeChunk(domain.RAGChunk{SourcePath: "valid.md", Text: "青山县门店经营事实。"})
	mismatch := NormalizeChunk(domain.RAGChunk{SourcePath: "mismatch.md", Text: "错误维度。"})
	nonFinite := NormalizeChunk(domain.RAGChunk{SourcePath: "nan.md", Text: "损坏向量。"})
	store := &domain.RAGVectorStore{Points: []domain.RAGVectorPoint{
		{ID: valid.ID, Vector: []float32{1, 0}, Chunk: valid},
		{ID: mismatch.ID, Vector: []float32{1}, Chunk: mismatch},
		{ID: nonFinite.ID, Vector: []float32{float32(math.NaN()), 0}, Chunk: nonFinite},
	}}
	hits := SearchVectorStore(store, []float32{1, 0}, 10)
	if len(hits) != 1 || hits[0].Point.ID != valid.ID {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestSearchVectorStoreFiltersDesignOnlyBeforeTopK(t *testing.T) {
	store := &domain.RAGVectorStore{}
	for i := range 18 {
		chunk := NormalizeChunk(domain.RAGChunk{
			SourcePath: "deconstruction-library/writing-techniques/craft-" + strconv.Itoa(i) + ".md",
			SourceKind: CraftSourceKind,
			Text:       "高相似度写作手法",
		})
		store.Points = append(store.Points, domain.RAGVectorPoint{
			ID: chunk.ID, Vector: []float32{1, 0}, Chunk: chunk,
		})
	}
	fact := NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/chapter_facts.md",
		SourceKind: "chapter_summary_facts",
		Text:       "本书已发生的经营事实",
	})
	store.Points = append(store.Points, domain.RAGVectorPoint{
		ID: fact.ID, Vector: []float32{0.8, 0.6}, Chunk: fact,
	})

	unfiltered := SearchVectorStore(store, []float32{1, 0}, 18)
	if len(unfiltered) != 18 {
		t.Fatalf("unfiltered hits = %d, want 18", len(unfiltered))
	}
	for _, hit := range unfiltered {
		if hit.Point.ID == fact.ID {
			t.Fatal("test fixture invalid: rank-19 fact unexpectedly entered unfiltered top 18")
		}
	}

	filtered := SearchVectorStoreWithOptions(store, []float32{1, 0}, 18, VectorSearchOptions{ExcludeDesignOnly: true})
	if len(filtered) != 1 || filtered[0].Point.ID != fact.ID {
		t.Fatalf("fact should survive filtering before top-k truncation: %+v", filtered)
	}
}

func TestNormalizeChunkHashCoversSemanticMetadata(t *testing.T) {
	base := domain.RAGChunk{
		SourcePath: "outline.md", SourceKind: "planning", Text: "同一段正文。",
		Summary: "旧摘要", Keywords: []string{"旧关键词"}, Metadata: map[string]any{"chapter": 1},
	}
	left := NormalizeChunk(base)
	base.Summary = "新摘要"
	right := NormalizeChunk(base)
	if left.Hash == right.Hash {
		t.Fatal("summary change must invalidate chunk hash")
	}
	base.Summary = "旧摘要"
	base.Metadata = map[string]any{"chapter": 2}
	right = NormalizeChunk(base)
	if left.Hash == right.Hash {
		t.Fatal("metadata change must invalidate chunk hash")
	}
	short := NormalizeChunk(domain.RAGChunk{Hash: "short", Text: "兼容旧数据"})
	if len(short.ID) != len("chunk:")+16 {
		t.Fatalf("short legacy hash produced invalid ID: %q", short.ID)
	}
}
