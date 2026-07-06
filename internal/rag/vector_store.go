package rag

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// MemoryVectorWriter 收集 BuildIndex 产出的向量点，随后由调用方落盘到 store。
// 这个 writer 让本地向量索引与外部向量库走同一套 BuildIndex 流程。
type MemoryVectorWriter struct {
	mu     sync.Mutex
	points []domain.RAGVectorPoint
}

func NewMemoryVectorWriter() *MemoryVectorWriter { return &MemoryVectorWriter{} }

type TeeVectorWriter struct {
	writers []VectorWriter
}

func NewTeeVectorWriter(writers ...VectorWriter) *TeeVectorWriter {
	out := &TeeVectorWriter{}
	for _, writer := range writers {
		if writer != nil {
			out.writers = append(out.writers, writer)
		}
	}
	return out
}

func (w *TeeVectorWriter) Write(ctx context.Context, point VectorPoint) error {
	for _, writer := range w.writers {
		if err := writer.Write(ctx, point); err != nil {
			return err
		}
	}
	return nil
}

func (w *MemoryVectorWriter) Write(_ context.Context, point VectorPoint) error {
	chunk := NormalizeChunk(point.Chunk)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.points = append(w.points, domain.RAGVectorPoint{
		ID:        point.ID,
		Hash:      chunk.Hash,
		Vector:    append([]float32(nil), point.Vector...),
		Payload:   clonePayload(point.Payload),
		Chunk:     chunk,
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	return nil
}

func (w *MemoryVectorWriter) VectorStore(cfg domain.RAGIndexConfig) domain.RAGVectorStore {
	w.mu.Lock()
	defer w.mu.Unlock()
	points := append([]domain.RAGVectorPoint(nil), w.points...)
	sort.SliceStable(points, func(i, j int) bool { return points[i].ID < points[j].ID })
	if cfg.VectorStore == "" {
		cfg.VectorStore = "local_json"
	}
	if cfg.VectorDimension == 0 {
		for _, point := range points {
			if len(point.Vector) > 0 {
				cfg.VectorDimension = len(point.Vector)
				break
			}
		}
	}
	return domain.RAGVectorStore{
		Config:    cfg,
		Points:    dedupeVectorPoints(points),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
}

type VectorSearchHit struct {
	Point domain.RAGVectorPoint
	Score float64
}

func SearchVectorStore(store *domain.RAGVectorStore, query []float32, maxResults int) []VectorSearchHit {
	if store == nil || len(query) == 0 || maxResults <= 0 {
		return nil
	}
	var hits []VectorSearchHit
	for _, point := range store.Points {
		if IsForbiddenChunk(point.Chunk) || len(point.Vector) == 0 {
			continue
		}
		score := cosineFloat32(query, point.Vector)
		if score <= 0 {
			continue
		}
		hits = append(hits, VectorSearchHit{Point: point, Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return strings.Compare(hits[i].Point.ID, hits[j].Point.ID) < 0
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}
	return hits
}

// MergeVectorStores returns a local JSON vector store where update points replace
// matching IDs and any existing points from the same source paths.
func MergeVectorStores(base *domain.RAGVectorStore, update domain.RAGVectorStore, replaceSourcePaths ...string) domain.RAGVectorStore {
	merged := domain.RAGVectorStore{
		Config: update.Config,
		Points: append([]domain.RAGVectorPoint(nil), update.Points...),
	}
	if base != nil {
		merged.Config = mergeVectorStoreConfig(base.Config, update.Config)
	}
	if merged.Config.VectorStore == "" {
		merged.Config.VectorStore = "local_json"
	}

	replaceIDs := map[string]struct{}{}
	replaceSources := map[string]struct{}{}
	for _, sourcePath := range replaceSourcePaths {
		if sourcePath = strings.TrimSpace(sourcePath); sourcePath != "" {
			replaceSources[sourcePath] = struct{}{}
		}
	}
	for _, point := range update.Points {
		if point.ID != "" {
			replaceIDs[point.ID] = struct{}{}
		}
		if sourcePath := strings.TrimSpace(point.Chunk.SourcePath); sourcePath != "" {
			replaceSources[sourcePath] = struct{}{}
		}
	}

	if base != nil {
		kept := make([]domain.RAGVectorPoint, 0, len(base.Points)+len(update.Points))
		for _, point := range base.Points {
			if _, ok := replaceIDs[point.ID]; ok && point.ID != "" {
				continue
			}
			if _, ok := replaceSources[point.Chunk.SourcePath]; ok && point.Chunk.SourcePath != "" {
				continue
			}
			kept = append(kept, point)
		}
		merged.Points = append(kept, update.Points...)
	}

	merged.Points = dedupeVectorPoints(merged.Points)
	sort.SliceStable(merged.Points, func(i, j int) bool { return merged.Points[i].ID < merged.Points[j].ID })
	if merged.Config.VectorDimension == 0 {
		for _, point := range merged.Points {
			if len(point.Vector) > 0 {
				merged.Config.VectorDimension = len(point.Vector)
				break
			}
		}
	}
	merged.UpdatedAt = time.Now().Format(time.RFC3339)
	return merged
}

func mergeVectorStoreConfig(base, update domain.RAGIndexConfig) domain.RAGIndexConfig {
	out := base
	if update.Collection != "" {
		out.Collection = update.Collection
	}
	if update.EmbeddingConcurrency != 0 {
		out.EmbeddingConcurrency = update.EmbeddingConcurrency
	}
	if update.QdrantWriteConcurrency != 0 {
		out.QdrantWriteConcurrency = update.QdrantWriteConcurrency
	}
	if update.EmbeddingProvider != "" {
		out.EmbeddingProvider = update.EmbeddingProvider
	}
	if update.EmbeddingModel != "" {
		out.EmbeddingModel = update.EmbeddingModel
	}
	if update.VectorStore != "" {
		out.VectorStore = update.VectorStore
	}
	if update.VectorDimension != 0 {
		out.VectorDimension = update.VectorDimension
	}
	return out
}

func cosineFloat32(left, right []float32) float64 {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	if n == 0 {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := 0; i < n; i++ {
		l := float64(left[i])
		r := float64(right[i])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func dedupeVectorPoints(points []domain.RAGVectorPoint) []domain.RAGVectorPoint {
	seen := map[string]struct{}{}
	out := make([]domain.RAGVectorPoint, 0, len(points))
	for _, point := range points {
		key := point.ID
		if key == "" {
			key = point.Hash
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, point)
	}
	return out
}

func clonePayload(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	return out
}
