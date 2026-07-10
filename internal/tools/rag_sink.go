package tools

import (
	"context"
	"errors"
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
	pending, err := st.RAG.LoadPendingUpserts()
	if err != nil {
		return err
	}
	if pending != nil && len(pending.Chunks) > 0 {
		chunks = mergeRAGPendingChunks(pending.Chunks, chunks)
		chunks = filterProjectContaminatedRAGChunks(st, chunks)
	}
	if len(chunks) == 0 {
		return st.RAG.ClearPendingUpserts()
	}
	queuePending := func(cause error) error {
		if cause == nil {
			return nil
		}
		queueErr := st.RAG.SavePendingUpserts(domain.RAGPendingUpserts{
			Chunks: chunks, LastError: cause.Error(), UpdatedAt: time.Now().Format(time.RFC3339),
		})
		if queueErr != nil {
			return errors.Join(cause, fmt.Errorf("save pending RAG upserts: %w", queueErr))
		}
		return cause
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return queuePending(err)
	}
	if state == nil {
		state = &domain.RAGIndexState{SchemaVersion: domain.CurrentRAGIndexSchemaVersion, Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	if strings.TrimSpace(state.Config.Collection) == "" {
		state.Config.Collection = "local_keyword"
	}
	cleanExisting := make([]domain.RAGChunk, 0, len(state.Chunks))
	for _, chunk := range state.Chunks {
		if rag.IsForbiddenChunk(chunk) || isProjectContaminatedRAGChunk(st, chunk) {
			continue
		}
		cleanExisting = append(cleanExisting, rag.NormalizeChunk(chunk))
	}
	sanitized := len(cleanExisting) != len(state.Chunks)
	var existingVectors *domain.RAGVectorStore
	if embedder != nil {
		existingVectors, err = st.RAG.LoadVectorStore()
		if err != nil {
			return queuePending(err)
		}
	}
	changedSources := changedRAGSourcePaths(
		cleanExisting, chunks, existingVectors, embedder != nil, ragVectorConfigChanged(state.Config, cfg),
	)
	if len(changedSources) == 0 {
		if !sanitized {
			return st.RAG.ClearPendingUpserts()
		}
		state.Chunks = cleanExisting
		state.ChunkHashes = rebuildRAGChunkHashes(cleanExisting)
		state.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := st.RAG.SaveIndexState(*state); err != nil {
			return queuePending(err)
		}
		return st.RAG.ClearPendingUpserts()
	}
	changedChunks := make([]domain.RAGChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if _, changed := changedSources[chunk.SourcePath]; changed {
			changedChunks = append(changedChunks, chunk)
		}
	}
	filtered := make([]domain.RAGChunk, 0, len(cleanExisting)+len(changedChunks))
	for _, chunk := range cleanExisting {
		if _, replace := changedSources[chunk.SourcePath]; !replace {
			filtered = append(filtered, chunk)
		}
	}
	filtered = append(filtered, changedChunks...)
	state.Chunks = filtered
	state.ChunkHashes = rebuildRAGChunkHashes(filtered)
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	if embedder == nil {
		if err := st.RAG.SaveIndexState(*state); err != nil {
			return queuePending(err)
		}
		return st.RAG.ClearPendingUpserts()
	}
	if err := upsertRAGVectors(ctx, st, embedder, vectorWriter, changedChunks, *state, cfg, changedSources, existingVectors); err != nil {
		return queuePending(err)
	}
	return st.RAG.ClearPendingUpserts()
}

func upsertRAGVectors(ctx context.Context, st *store.Store, embedder rag.Embedder, vectorWriter rag.VectorWriter, chunks []domain.RAGChunk, state domain.RAGIndexState, cfg domain.RAGIndexConfig, sources map[string]struct{}, existing *domain.RAGVectorStore) error {
	vectorCfg := mergeRAGVectorConfig(state.Config, cfg, existing)
	if info, ok := vectorWriter.(interface {
		Collection() string
		URL() string
	}); ok {
		vectorCfg.Collection = info.Collection()
		vectorCfg.QdrantURL = info.URL()
		vectorCfg.VectorStore = "qdrant"
	}
	// Stage every embedding locally first. No persisted state or remote source is
	// touched until the full source has produced valid, dimension-consistent vectors.
	vectorChunks := make([]domain.RAGChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			vectorChunks = append(vectorChunks, chunk)
		}
	}
	memory := rag.NewMemoryVectorWriter()
	result := rag.IndexResult{State: domain.RAGIndexState{Config: vectorCfg}}
	var err error
	if len(vectorChunks) > 0 {
		result, err = rag.BuildIndex(ctx, vectorChunks, nil, vectorCfg, embedder, memory)
		if err != nil {
			return err
		}
	}
	update := memory.VectorStore(result.State.Config)
	sourceList := make([]string, 0, len(sources))
	for sourcePath := range sources {
		sourceList = append(sourceList, sourcePath)
	}
	sort.Strings(sourceList)
	if vectorWriter != nil {
		if deleter, ok := vectorWriter.(rag.SourcePathDeleter); ok {
			for _, sourcePath := range sourceList {
				if err := deleter.DeleteSourcePath(ctx, sourcePath); err != nil {
					return fmt.Errorf("delete qdrant source %s: %w", sourcePath, err)
				}
			}
		}
		points := make([]rag.VectorPoint, 0, len(update.Points))
		for _, point := range update.Points {
			points = append(points, rag.VectorPoint{
				ID: point.ID, Vector: point.Vector, Payload: point.Payload, Chunk: point.Chunk,
			})
		}
		if len(points) > 0 {
			if _, err := rag.WriteVectorPoints(ctx, vectorWriter, points, result.State.Config); err != nil {
				return fmt.Errorf("write staged rag vectors: %w", err)
			}
		}
	}
	merged := rag.MergeVectorStores(existing, update, sourceList...)
	state.Config = result.State.Config
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	// Index state is the commit marker. If its final write fails, the next run
	// sees old hashes and safely rebuilds the already-idempotent vector points.
	if err := st.RAG.SaveVectorStore(merged); err != nil {
		return err
	}
	return st.RAG.SaveIndexState(state)
}

func normalizeRAGChunks(chunks []domain.RAGChunk) []domain.RAGChunk {
	out := make([]domain.RAGChunk, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.ID == "" || chunk.Hash == "" || strings.TrimSpace(chunk.SourcePath) == "" || rag.IsForbiddenChunk(chunk) {
			continue
		}
		if _, duplicate := seen[chunk.Hash]; duplicate {
			continue
		}
		seen[chunk.Hash] = struct{}{}
		out = append(out, chunk)
	}
	return out
}

func mergeRAGPendingChunks(pending, incoming []domain.RAGChunk) []domain.RAGChunk {
	incomingSources := chunkSourcePaths(incoming)
	combined := make([]domain.RAGChunk, 0, len(pending)+len(incoming))
	for _, chunk := range pending {
		if _, replaced := incomingSources[strings.TrimSpace(chunk.SourcePath)]; replaced {
			continue
		}
		combined = append(combined, chunk)
	}
	combined = append(combined, incoming...)
	return normalizeRAGChunks(combined)
}

func changedRAGSourcePaths(existing, incoming []domain.RAGChunk, vectors *domain.RAGVectorStore, requireVectors, force bool) map[string]struct{} {
	existingHashes := make(map[string]map[string]struct{})
	incomingHashes := make(map[string]map[string]struct{})
	incomingFactHashes := make(map[string][]string)
	for _, chunk := range existing {
		addRAGSourceHash(existingHashes, chunk.SourcePath, chunk.Hash)
	}
	for _, chunk := range incoming {
		addRAGSourceHash(incomingHashes, chunk.SourcePath, chunk.Hash)
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			incomingFactHashes[chunk.SourcePath] = append(incomingFactHashes[chunk.SourcePath], chunk.Hash)
		}
	}
	vectorHashes := make(map[string]struct{})
	if vectors != nil {
		for _, point := range vectors.Points {
			hash := strings.TrimSpace(point.Hash)
			if hash == "" {
				hash = strings.TrimSpace(point.Chunk.Hash)
			}
			if hash != "" {
				vectorHashes[hash] = struct{}{}
			}
		}
	}
	changed := make(map[string]struct{})
	for sourcePath, hashes := range incomingHashes {
		if force || !sameRAGHashSet(hashes, existingHashes[sourcePath]) {
			changed[sourcePath] = struct{}{}
			continue
		}
		if requireVectors {
			for _, hash := range incomingFactHashes[sourcePath] {
				if _, ok := vectorHashes[hash]; !ok {
					changed[sourcePath] = struct{}{}
					break
				}
			}
		}
	}
	return changed
}

func addRAGSourceHash(target map[string]map[string]struct{}, sourcePath, hash string) {
	if target[sourcePath] == nil {
		target[sourcePath] = make(map[string]struct{})
	}
	target[sourcePath][hash] = struct{}{}
}

func sameRAGHashSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for hash := range left {
		if _, ok := right[hash]; !ok {
			return false
		}
	}
	return true
}

func ragVectorConfigChanged(current, override domain.RAGIndexConfig) bool {
	return (override.EmbeddingProvider != "" && !strings.EqualFold(override.EmbeddingProvider, current.EmbeddingProvider)) ||
		(override.EmbeddingModel != "" && override.EmbeddingModel != current.EmbeddingModel) ||
		(override.VectorDimension != 0 && override.VectorDimension != current.VectorDimension)
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
	if override.VectorBatchSize != 0 {
		cfg.VectorBatchSize = override.VectorBatchSize
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
