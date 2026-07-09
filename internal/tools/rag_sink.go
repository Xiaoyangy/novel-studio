package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func upsertRAGChunks(ctx context.Context, st *store.Store, embedder rag.Embedder, vectorWriter rag.VectorWriter, chunks []domain.RAGChunk, cfg domain.RAGIndexConfig) error {
	return UpsertRAGChunks(ctx, st, embedder, vectorWriter, chunks, cfg)
}

func UpsertRAGChunks(ctx context.Context, st *store.Store, embedder rag.Embedder, vectorWriter rag.VectorWriter, chunks []domain.RAGChunk, cfg domain.RAGIndexConfig) error {
	chunks = normalizeRAGChunks(chunks)
	chunks = filterProjectContaminatedRAGChunks(st, chunks)
	if len(chunks) == 0 {
		return nil
	}
	sources := chunkSourcePaths(chunks)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return err
	}
	if state == nil {
		state = &domain.RAGIndexState{Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	if strings.TrimSpace(state.Config.Collection) == "" {
		state.Config.Collection = "local_keyword"
	}
	filtered := state.Chunks[:0]
	for _, chunk := range state.Chunks {
		if rag.IsForbiddenChunk(chunk) || isProjectContaminatedRAGChunk(st, chunk) {
			continue
		}
		if _, replace := sources[chunk.SourcePath]; replace {
			continue
		}
		filtered = append(filtered, rag.NormalizeChunk(chunk))
	}
	filtered = append(filtered, chunks...)
	state.Chunks = filtered
	state.ChunkHashes = rebuildRAGChunkHashes(filtered)
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return err
	}
	if embedder == nil {
		return nil
	}
	return upsertRAGVectors(ctx, st, embedder, vectorWriter, chunks, *state, cfg, sources)
}

func upsertRAGVectors(ctx context.Context, st *store.Store, embedder rag.Embedder, vectorWriter rag.VectorWriter, chunks []domain.RAGChunk, state domain.RAGIndexState, cfg domain.RAGIndexConfig, sources map[string]struct{}) error {
	existing, err := st.RAG.LoadVectorStore()
	if err != nil {
		return err
	}
	vectorCfg := mergeRAGVectorConfig(state.Config, cfg, existing)
	if info, ok := vectorWriter.(interface {
		Collection() string
		URL() string
	}); ok {
		vectorCfg.Collection = info.Collection()
		vectorCfg.QdrantURL = info.URL()
		vectorCfg.VectorStore = "qdrant"
	}
	if deleter, ok := vectorWriter.(rag.SourcePathDeleter); ok {
		for sourcePath := range sources {
			if err := deleter.DeleteSourcePath(ctx, sourcePath); err != nil {
				return fmt.Errorf("delete qdrant source %s: %w", sourcePath, err)
			}
		}
	}
	memory := rag.NewMemoryVectorWriter()
	writer := rag.VectorWriter(memory)
	if vectorWriter != nil {
		writer = rag.NewTeeVectorWriter(memory, vectorWriter)
	}
	result, err := rag.BuildIndex(ctx, chunks, nil, vectorCfg, embedder, writer)
	if err != nil {
		return err
	}
	update := memory.VectorStore(result.State.Config)
	if len(update.Points) == 0 {
		return nil
	}
	sourceList := make([]string, 0, len(sources))
	for sourcePath := range sources {
		sourceList = append(sourceList, sourcePath)
	}
	sort.Strings(sourceList)
	merged := rag.MergeVectorStores(existing, update, sourceList...)
	return st.RAG.SaveVectorStore(merged)
}

func normalizeRAGChunks(chunks []domain.RAGChunk) []domain.RAGChunk {
	out := make([]domain.RAGChunk, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.ID == "" || chunk.Hash == "" || rag.IsForbiddenChunk(chunk) {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func filterProjectContaminatedRAGChunks(st *store.Store, chunks []domain.RAGChunk) []domain.RAGChunk {
	if st == nil || len(chunks) == 0 {
		return chunks
	}
	out := chunks[:0]
	for _, chunk := range chunks {
		if isProjectContaminatedRAGChunk(st, chunk) {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func isProjectContaminatedRAGChunk(st *store.Store, chunk domain.RAGChunk) bool {
	if st == nil {
		return false
	}
	text := strings.Join([]string{chunk.Text, chunk.Summary, chunk.Context, strings.Join(chunk.Keywords, " ")}, "\n")
	return len(SecondAlgorithmProjectContaminationViolations(st, text)) > 0
}

func chunkSourcePaths(chunks []domain.RAGChunk) map[string]struct{} {
	out := map[string]struct{}{}
	for _, chunk := range chunks {
		if sourcePath := strings.TrimSpace(chunk.SourcePath); sourcePath != "" {
			out[sourcePath] = struct{}{}
		}
	}
	return out
}

func mergeRAGVectorConfig(stateCfg, override domain.RAGIndexConfig, existing *domain.RAGVectorStore) domain.RAGIndexConfig {
	cfg := stateCfg
	if existing != nil {
		cfg = existing.Config
	}
	if override.Collection != "" {
		cfg.Collection = override.Collection
	}
	if override.EmbeddingConcurrency != 0 {
		cfg.EmbeddingConcurrency = override.EmbeddingConcurrency
	}
	if override.QdrantWriteConcurrency != 0 {
		cfg.QdrantWriteConcurrency = override.QdrantWriteConcurrency
	}
	if override.EmbeddingProvider != "" {
		cfg.EmbeddingProvider = override.EmbeddingProvider
	}
	if override.EmbeddingModel != "" {
		cfg.EmbeddingModel = override.EmbeddingModel
	}
	if override.VectorStore != "" {
		cfg.VectorStore = override.VectorStore
	}
	if override.VectorDimension != 0 {
		cfg.VectorDimension = override.VectorDimension
	}
	if override.QdrantURL != "" {
		cfg.QdrantURL = override.QdrantURL
	}
	if strings.TrimSpace(cfg.Collection) == "" || cfg.Collection == "local_keyword" {
		cfg.Collection = "local_vector"
	}
	if cfg.VectorStore == "" {
		cfg.VectorStore = "local_json"
	}
	return cfg
}

func chunksFromRAGText(sourcePath, sourceKind, facet, contextText, text, summary string, keywords []string, metadata map[string]any, maxRunes int) []domain.RAGChunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = 1200
	}
	parts := splitRunesForRAG(text, maxRunes)
	chunks := make([]domain.RAGChunk, 0, len(parts))
	for i, part := range parts {
		meta := cloneMetadata(metadata)
		if len(parts) > 1 {
			meta["part"] = i + 1
			meta["parts"] = len(parts)
		}
		chunks = append(chunks, domain.RAGChunk{
			SourcePath: sourcePath,
			SourceKind: sourceKind,
			Facet:      facet,
			Context:    contextText,
			Text:       part,
			Summary:    truncateRunes(summary, 120),
			Keywords:   append([]string(nil), keywords...),
			Metadata:   meta,
		})
	}
	return chunks
}

func splitRunesForRAG(text string, maxRunes int) []string {
	rs := []rune(strings.TrimSpace(text))
	if len(rs) <= maxRunes {
		return []string{string(rs)}
	}
	var out []string
	for start := 0; start < len(rs); start += maxRunes {
		end := start + maxRunes
		if end > len(rs) {
			end = len(rs)
		}
		out = append(out, strings.TrimSpace(string(rs[start:end])))
	}
	return out
}

func cloneMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata)+2)
	for k, v := range metadata {
		out[k] = v
	}
	return out
}
