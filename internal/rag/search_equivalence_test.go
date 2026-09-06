package rag

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Keep the original document-scan algorithm as an independent ranking oracle
// and benchmark baseline. It intentionally does not use the postings or heap.
type scanBM25 struct {
	docs   []scanBM25Doc
	df     map[string]int
	avgLen float64
}

type scanBM25Doc struct {
	chunk  domain.RAGChunk
	tf     map[string]int
	length int
}

func buildScanBM25(chunks []domain.RAGChunk) scanBM25 {
	idx := scanBM25{df: map[string]int{}}
	total := 0
	for _, chunk := range chunks {
		chunk = NormalizeChunk(chunk)
		if IsForbiddenChunk(chunk) {
			continue
		}
		tokens := TokenizeForBM25(SearchText(chunk))
		if len(tokens) == 0 {
			continue
		}
		tf := map[string]int{}
		for _, tok := range tokens {
			tf[tok]++
		}
		for tok := range tf {
			idx.df[tok]++
		}
		idx.docs = append(idx.docs, scanBM25Doc{chunk: chunk, tf: tf, length: len(tokens)})
		total += len(tokens)
	}
	if len(idx.docs) > 0 {
		idx.avgLen = float64(total) / float64(len(idx.docs))
	}
	return idx
}

func (idx scanBM25) search(query string, limit int) []BM25Hit {
	if len(idx.docs) == 0 || limit <= 0 {
		return nil
	}
	var unique []string
	seen := map[string]bool{}
	for _, tok := range TokenizeForBM25(query) {
		if !seen[tok] {
			seen[tok] = true
			unique = append(unique, tok)
		}
	}
	n := float64(len(idx.docs))
	var hits []BM25Hit
	for _, doc := range idx.docs {
		score := 0.0
		for _, tok := range unique {
			tf := doc.tf[tok]
			if tf == 0 {
				continue
			}
			df := float64(idx.df[tok])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			tfNorm := (float64(tf) * (bm25K1 + 1)) / (float64(tf) + bm25K1*(1-bm25B+bm25B*float64(doc.length)/idx.avgLen))
			score += idf * tfNorm
		}
		if score > 0 {
			hits = append(hits, BM25Hit{Chunk: doc.chunk, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits[:min(limit, len(hits))]
}

func searchFixture(count int) []domain.RAGChunk {
	chunks := make([]domain.RAGChunk, 0, count)
	for i := range count {
		chunks = append(chunks, NormalizeChunk(domain.RAGChunk{
			ID: fmt.Sprintf("chunk:%06d", count-i), SourcePath: "facts.json", SourceKind: "chapter_summary_facts",
			Context: "已接受的章节事实", Keywords: []string{"夜租"},
			Text: fmt.Sprintf("夜租账单记录 %s group%04d", strings.Repeat("门禁现金影子 ", 1+i%5), i%1000),
		}))
	}
	return chunks
}

func TestBM25PostingsMatchScanOracle(t *testing.T) {
	chunks := searchFixture(1200)
	// Include identical documents to exercise stable order at the cutoff,
	// and a forbidden document that would otherwise score highly.
	chunks = append(chunks, chunks[0], domain.RAGChunk{ID: "forbidden", SourceKind: "deconstruction", Text: "夜租 group0001"})
	idx, reference := BuildBM25Index(chunks), buildScanBM25(chunks)
	queries := []string{"夜租", "group0001", "夜租 group0001 门禁 夜租", "门禁现金影子", "absent", "！！！", "GROUP0001"}
	for _, query := range queries {
		for _, limit := range []int{-1, 0, 1, 6, 18, 5000} {
			got, want := idx.Search(query, limit), reference.search(query, limit)
			if len(got) != len(want) {
				t.Fatalf("query=%q limit=%d lengths %d != %d", query, limit, len(got), len(want))
			}
			for i := range got {
				if got[i].Chunk.Hash != want[i].Chunk.Hash || got[i].Score != want[i].Score || got[i].Chunk.ID != want[i].Chunk.ID {
					t.Fatalf("query=%q limit=%d rank=%d got=%s/%g want=%s/%g", query, limit, i, got[i].Chunk.ID, got[i].Score, want[i].Chunk.ID, want[i].Score)
				}
			}
		}
	}
	// The index is shared by concurrent planning/recall calls and must keep
	// all query scores and selection scratch private to each request.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				got := idx.Search("夜租 group0001", 18)
				if len(got) != 18 {
					t.Errorf("concurrent search returned %d hits", len(got))
				}
			}
		})
	}
	wg.Wait()
}

func scanVectorSearch(store *domain.RAGVectorStore, query []float32, limit int, options VectorSearchOptions) []VectorSearchHit {
	if limit <= 0 || len(query) == 0 {
		return nil
	}
	var hits []VectorSearchHit
	for _, point := range store.Points {
		if IsForbiddenChunk(point.Chunk) || (options.ExcludeDesignOnly && IsDesignOnlySourceKind(point.Chunk.SourceKind)) || len(point.Vector) != len(query) {
			continue
		}
		var dot, leftNorm, rightNorm float64
		valid := true
		for i, value := range query {
			l, r := float64(value), float64(point.Vector[i])
			if math.IsNaN(l) || math.IsInf(l, 0) || math.IsNaN(r) || math.IsInf(r, 0) {
				valid = false
				break
			}
			dot += l * r
			leftNorm += l * l
			rightNorm += r * r
		}
		if !valid || leftNorm == 0 || rightNorm == 0 {
			continue
		}
		score := dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
		if score > 0 {
			hits = append(hits, VectorSearchHit{Point: point, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Point.ID < hits[j].Point.ID
		}
		return hits[i].Score > hits[j].Score
	})
	return hits[:min(limit, len(hits))]
}

func vectorSearchFixture(count, dimension int) (*domain.RAGVectorStore, []float32) {
	random := rand.New(rand.NewSource(42))
	store := &domain.RAGVectorStore{Points: make([]domain.RAGVectorPoint, 0, count)}
	for _, chunk := range searchFixture(count) {
		vector := make([]float32, dimension)
		for i := range vector {
			vector[i] = random.Float32()
		}
		store.Points = append(store.Points, domain.RAGVectorPoint{ID: chunk.ID, Chunk: chunk, Vector: vector})
	}
	query := make([]float32, dimension)
	for i := range query {
		query[i] = random.Float32()
	}
	return store, query
}

func TestVectorTopKMatchesFullSortOracle(t *testing.T) {
	store, query := vectorSearchFixture(240, 16)
	for i := 0; i < 40; i++ {
		store.Points[i].Vector = append([]float32(nil), query...)
		if i%2 == 0 {
			store.Points[i].ID = "duplicate-id"
		}
	}
	store.Points[1].Chunk.SourceKind = CraftSourceKind
	store.Points[2].Chunk.SourceKind = "deconstruction"
	store.Points[3].Chunk.Metadata = map[string]any{"nested": map[string]any{"source": "data/reference-library/other-book.md"}}
	store.Points[4].Vector = []float32{1}
	store.Points[5].Vector[0] = float32(math.NaN())
	store.Points[6].Vector[0] = float32(math.Inf(1))
	store.Points[7].Vector = make([]float32, len(query))
	for _, excludeDesignOnly := range []bool{false, true} {
		options := VectorSearchOptions{ExcludeDesignOnly: excludeDesignOnly}
		for _, limit := range []int{-1, 0, 1, 6, 18, 5000} {
			for _, q := range [][]float32{query, nil, make([]float32, len(query)), {float32(math.NaN())}, {float32(math.Inf(1))}} {
				got, want := SearchVectorStoreWithOptions(store, q, limit, options), scanVectorSearch(store, q, limit, options)
				if len(got) != len(want) {
					t.Fatalf("limit=%d got %d hits want %d", limit, len(got), len(want))
				}
				for i := range got {
					if got[i].Point.Chunk.Hash != want[i].Point.Chunk.Hash || got[i].Point.ID != want[i].Point.ID || got[i].Score != want[i].Score {
						t.Fatalf("limit=%d rank=%d got=%s/%g want=%s/%g", limit, i, got[i].Point.ID, got[i].Score, want[i].Point.ID, want[i].Score)
					}
				}
			}
		}
	}
}

func BenchmarkBM25Search(b *testing.B) {
	chunks := searchFixture(10000)
	idx, reference := BuildBM25Index(chunks), buildScanBM25(chunks)
	for _, query := range []struct{ name, text string }{{"sparse", "group0001"}, {"common", "夜租"}} {
		b.Run(query.name+"/postings", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				idx.Search(query.text, 18)
			}
		})
		b.Run(query.name+"/scan_baseline", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				reference.search(query.text, 18)
			}
		})
	}
}

func BenchmarkVectorSearchTopK(b *testing.B) {
	store, query := vectorSearchFixture(4096, 128)
	b.Run("bounded", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			SearchVectorStore(store, query, 18)
		}
	})
	b.Run("full_sort_baseline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scanVectorSearch(store, query, 18, VectorSearchOptions{})
		}
	})
}
