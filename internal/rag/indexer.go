package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Embedder 负责把 chunk 文本转成向量。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorWriter 负责把向量写入后端。真实实现可对接 Qdrant。
type VectorWriter interface {
	Write(ctx context.Context, point VectorPoint) error
}

// BatchVectorWriter lets remote stores persist a small idempotent batch in one
// request. BuildIndex falls back to VectorWriter when a backend does not expose
// batch support.
type BatchVectorWriter interface {
	WriteBatch(ctx context.Context, points []VectorPoint) error
}

type VectorPoint struct {
	ID      string
	Vector  []float32
	Payload map[string]any
	Chunk   domain.RAGChunk
}

type IndexResult struct {
	State      domain.RAGIndexState
	Embedded   int
	Written    int
	SkippedDup int
}

// BuildIndex 流式并行完成 chunk 去重、Embedding 和向量写入。
func BuildIndex(
	ctx context.Context,
	chunks []domain.RAGChunk,
	existingHashes []string,
	cfg domain.RAGIndexConfig,
	embedder Embedder,
	writer VectorWriter,
) (IndexResult, error) {
	if embedder == nil {
		return IndexResult{}, ErrNilBackend("embedding")
	}
	if writer == nil {
		return IndexResult{}, ErrNilBackend("vector_writer")
	}
	if cfg.EmbeddingConcurrency <= 0 {
		cfg.EmbeddingConcurrency = 2
	}
	if cfg.QdrantWriteConcurrency <= 0 {
		cfg.QdrantWriteConcurrency = 2
	}
	if cfg.VectorBatchSize <= 0 {
		cfg.VectorBatchSize = 32
	}
	if cfg.VectorBatchSize > 128 {
		cfg.VectorBatchSize = 128
	}

	known := make(map[string]struct{}, len(existingHashes)+len(chunks))
	for _, hash := range existingHashes {
		if hash = strings.TrimSpace(hash); hash != "" {
			known[hash] = struct{}{}
		}
	}
	accepted := make([]domain.RAGChunk, 0, len(chunks))
	skippedDup := 0
	for _, chunk := range chunks {
		chunk = NormalizeChunk(chunk)
		if chunk.Hash == "" {
			continue
		}
		if _, ok := known[chunk.Hash]; ok {
			skippedDup++
			continue
		}
		known[chunk.Hash] = struct{}{}
		accepted = append(accepted, chunk)
	}
	if len(accepted) == 0 {
		hashes := make([]string, 0, len(known))
		for hash := range known {
			hashes = append(hashes, hash)
		}
		slices.Sort(hashes)
		return IndexResult{
			State: domain.RAGIndexState{
				SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
				Config:        cfg, ChunkHashes: hashes, UpdatedAt: time.Now().Format(time.RFC3339),
			},
			SkippedDup: skippedDup,
		}, nil
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	vectors := make([][]float32, len(accepted))
	jobs := make(chan int)
	var embedWG sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	for worker := 0; worker < cfg.EmbeddingConcurrency; worker++ {
		embedWG.Add(1)
		go func() {
			defer embedWG.Done()
			for index := range jobs {
				var vector []float32
				err := retryRAGIO(workCtx, 3, func(_ int) error {
					var err error
					vector, err = embedder.Embed(workCtx, EmbeddingText(accepted[index]))
					return err
				})
				if err != nil {
					setErr(fmt.Errorf("embed chunk %s: %w", accepted[index].ID, err))
					return
				}
				if err := ValidateVector(vector); err != nil {
					setErr(fmt.Errorf("embed chunk %s returned invalid vector: %w", accepted[index].ID, err))
					return
				}
				vectors[index] = vector
			}
		}()
	}
sendEmbeds:
	for index := range accepted {
		select {
		case jobs <- index:
		case <-workCtx.Done():
			break sendEmbeds
		}
	}
	close(jobs)
	embedWG.Wait()
	errMu.Lock()
	embedErr := firstErr
	errMu.Unlock()
	if embedErr != nil {
		return IndexResult{}, embedErr
	}
	if err := ctx.Err(); err != nil {
		return IndexResult{}, err
	}

	vectorDimension := len(vectors[0])
	for index, vector := range vectors {
		if len(vector) != vectorDimension {
			return IndexResult{}, fmt.Errorf("embedding dimension drift: chunk=%s got=%d want=%d", accepted[index].ID, len(vector), vectorDimension)
		}
	}
	if cfg.VectorDimension > 0 && cfg.VectorDimension != vectorDimension {
		return IndexResult{}, fmt.Errorf("embedding dimension changed: got=%d configured=%d", vectorDimension, cfg.VectorDimension)
	}
	cfg.VectorDimension = vectorDimension

	points := make([]VectorPoint, 0, len(accepted))
	for index, chunk := range accepted {
		points = append(points, VectorPoint{
			ID: chunk.ID, Vector: vectors[index], Chunk: chunk, Payload: chunkPayload(chunk),
		})
	}
	written, err := WriteVectorPoints(ctx, writer, points, cfg)
	if err != nil {
		return IndexResult{}, err
	}

	hashes := make([]string, 0, len(known))
	for hash := range known {
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	return IndexResult{
		State: domain.RAGIndexState{
			SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
			Config:        cfg, Chunks: accepted, ChunkHashes: hashes, UpdatedAt: time.Now().Format(time.RFC3339),
		},
		Embedded: len(accepted), Written: written, SkippedDup: skippedDup,
	}, nil
}

// WriteVectorPoints writes precomputed vectors in bounded, idempotent batches.
// It is exported so callers can finish all embedding work before replacing a
// remote source, then replay the staged vectors without embedding them again.
func WriteVectorPoints(ctx context.Context, writer VectorWriter, points []VectorPoint, cfg domain.RAGIndexConfig) (int, error) {
	if writer == nil {
		return 0, ErrNilBackend("vector_writer")
	}
	if len(points) == 0 {
		return 0, nil
	}
	vectorDimension := len(points[0].Vector)
	for _, point := range points {
		if len(point.Vector) != vectorDimension {
			return 0, fmt.Errorf("vector batch dimension drift: point=%s got=%d want=%d", point.ID, len(point.Vector), vectorDimension)
		}
		if err := ValidateVector(point.Vector); err != nil {
			return 0, fmt.Errorf("invalid vector point %s: %w", point.ID, err)
		}
	}
	if cfg.VectorBatchSize <= 0 {
		cfg.VectorBatchSize = 32
	}
	if cfg.VectorBatchSize > 128 {
		cfg.VectorBatchSize = 128
	}
	if cfg.QdrantWriteConcurrency <= 0 {
		cfg.QdrantWriteConcurrency = 2
	}
	batches := make([][]VectorPoint, 0, (len(points)+cfg.VectorBatchSize-1)/cfg.VectorBatchSize)
	for start := 0; start < len(points); start += cfg.VectorBatchSize {
		end := min(start+cfg.VectorBatchSize, len(points))
		batches = append(batches, points[start:end])
	}

	writeCtx, cancelWrites := context.WithCancel(ctx)
	defer cancelWrites()
	writeJobs := make(chan []VectorPoint)
	var writeErrMu sync.Mutex
	var firstWriteErr error
	setWriteErr := func(err error) {
		if err == nil {
			return
		}
		writeErrMu.Lock()
		if firstWriteErr == nil {
			firstWriteErr = err
			cancelWrites()
		}
		writeErrMu.Unlock()
	}
	written := 0
	var writtenMu sync.Mutex
	var writeWG sync.WaitGroup
	for worker := 0; worker < cfg.QdrantWriteConcurrency; worker++ {
		writeWG.Add(1)
		go func() {
			defer writeWG.Done()
			for batch := range writeJobs {
				if err := writeVectorBatch(writeCtx, writer, batch); err != nil {
					setWriteErr(fmt.Errorf("write vector batch: %w", err))
					return
				}
				writtenMu.Lock()
				written += len(batch)
				writtenMu.Unlock()
			}
		}()
	}
sendWrites:
	for _, batch := range batches {
		select {
		case writeJobs <- batch:
		case <-writeCtx.Done():
			break sendWrites
		}
	}
	close(writeJobs)
	writeWG.Wait()
	writeErrMu.Lock()
	writeErr := firstWriteErr
	writeErrMu.Unlock()
	if writeErr != nil {
		return written, writeErr
	}
	if err := ctx.Err(); err != nil {
		return written, err
	}
	return written, nil
}

func writeVectorBatch(ctx context.Context, writer VectorWriter, points []VectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	if batchWriter, ok := writer.(BatchVectorWriter); ok {
		return retryRAGIO(ctx, defaultRAGIOAttempts, func(_ int) error {
			return batchWriter.WriteBatch(ctx, points)
		})
	}
	for _, point := range points {
		point := point
		if err := retryRAGIO(ctx, defaultRAGIOAttempts, func(_ int) error {
			return writer.Write(ctx, point)
		}); err != nil {
			return err
		}
	}
	return nil
}

func NormalizeChunk(chunk domain.RAGChunk) domain.RAGChunk {
	chunk.Text = strings.TrimSpace(chunk.Text)
	chunk.Summary = strings.TrimSpace(chunk.Summary)
	chunk.Context = strings.TrimSpace(chunk.Context)
	if chunk.SourceKind == "" {
		chunk.SourceKind = "knowledge"
	}
	if chunk.Context == "" {
		chunk.Context = deriveChunkContext(chunk)
	}
	chunk.Keywords = normalizeKeywords(chunk.Keywords)
	if len(chunk.Keywords) == 0 {
		chunk.Keywords = deriveChunkKeywords(chunk)
	}
	if chunk.Hash == "" {
		metadata, _ := json.Marshal(chunk.Metadata)
		sum := sha256.Sum256([]byte(strings.Join([]string{
			chunk.SourcePath,
			chunk.SourceKind,
			chunk.Facet,
			chunk.ParentID,
			chunk.Context,
			chunk.Summary,
			strings.Join(chunk.Keywords, "\x1f"),
			chunk.Text,
			string(metadata),
		}, "\x00")))
		chunk.Hash = hex.EncodeToString(sum[:])
	}
	if chunk.ID == "" {
		idHash := chunk.Hash
		if len(idHash) < 16 {
			sum := sha256.Sum256([]byte(idHash))
			idHash = hex.EncodeToString(sum[:])
		}
		chunk.ID = "chunk:" + idHash[:16]
	}
	return chunk
}

// RehashChunk migrates a chunk to the current semantic hash while preserving
// its stable logical ID. This intentionally ignores a persisted legacy hash.
func RehashChunk(chunk domain.RAGChunk) domain.RAGChunk {
	chunk.Hash = ""
	return NormalizeChunk(chunk)
}

// ValidateVector rejects values that would poison cosine ranking or be refused
// by a remote vector store.
func ValidateVector(vector []float32) error {
	if len(vector) == 0 {
		return fmt.Errorf("empty vector")
	}
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("non-finite value")
		}
		norm += float64(value) * float64(value)
	}
	if norm == 0 {
		return fmt.Errorf("zero vector")
	}
	return nil
}

func chunkPayload(chunk domain.RAGChunk) map[string]any {
	payload := make(map[string]any, len(chunk.Metadata)+8)
	for k, v := range chunk.Metadata {
		if isReservedChunkPayloadKey(k) {
			continue
		}
		payload[k] = v
	}
	trusted := map[string]any{
		"source_path": chunk.SourcePath,
		"source_kind": chunk.SourceKind,
		"facet":       chunk.Facet,
		"hash":        chunk.Hash,
	}
	for k, v := range trusted {
		payload[k] = v
	}
	if chunk.ParentID != "" {
		payload["parent_id"] = chunk.ParentID
	}
	if chunk.Context != "" {
		payload["context"] = chunk.Context
	}
	if chunk.Summary != "" {
		payload["summary"] = chunk.Summary
	}
	if len(chunk.Keywords) > 0 {
		payload["keywords"] = chunk.Keywords
	}
	return payload
}

func isReservedChunkPayloadKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "source_path", "source_kind", "facet", "hash", "parent_id", "context", "summary", "keywords", "chunk_id", "chunk":
		return true
	default:
		return false
	}
}

// EmbeddingText returns the enriched text used for semantic indexing.
// The raw chunk stays intact for audit; the embedding input carries local context so
// short chunks do not lose the book/section/technique they came from.
func EmbeddingText(chunk domain.RAGChunk) string {
	chunk = NormalizeChunk(chunk)
	var parts []string
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, label+": "+value)
		}
	}
	add("context", chunk.Context)
	add("facet", chunk.Facet)
	add("summary", chunk.Summary)
	if len(chunk.Keywords) > 0 {
		add("keywords", strings.Join(chunk.Keywords, " "))
	}
	add("text", chunk.Text)
	return strings.Join(parts, "\n")
}

// SearchText returns the local lexical retrieval surface. It intentionally includes
// metadata and contextual headers, mirroring contextual BM25 without requiring a
// separate search backend.
func SearchText(chunk domain.RAGChunk) string {
	chunk = NormalizeChunk(chunk)
	var parts []string
	for _, part := range []string{
		chunk.SourceKind,
		chunk.Facet,
		chunk.SourcePath,
		chunk.ParentID,
		chunk.Context,
		chunk.Summary,
		strings.Join(chunk.Keywords, " "),
		chunk.Text,
	} {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(chunk.Metadata) > 0 {
		keys := make([]string, 0, len(chunk.Metadata))
		for k := range chunk.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := metadataValueString(chunk.Metadata[k]); v != "" {
				parts = append(parts, k, v)
			}
		}
	}
	return strings.Join(parts, " ")
}

// QueryTerms extracts a small deterministic set of lexical hints from Chinese or
// mixed-language query text. It is not a tokenizer; it gives local RAG enough
// short overlap terms to avoid exact-long-phrase misses.
func QueryTerms(parts ...string) []string {
	seen := map[string]struct{}{}
	var terms []string
	add := func(term string) {
		term = cleanTerm(term)
		runeLen := len([]rune(term))
		if runeLen < 2 || runeLen > 32 {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		normalized := normalizeSeparators(part)
		for _, field := range strings.Fields(normalized) {
			add(field)
			runes := []rune(cleanTerm(field))
			if len(runes) < 4 || len(runes) > 80 {
				continue
			}
			for n := 4; n >= 2; n-- {
				for i := 0; i+n <= len(runes); i++ {
					add(string(runes[i : i+n]))
					if len(terms) >= 80 {
						return terms
					}
				}
			}
		}
		if len(terms) >= 80 {
			return terms
		}
	}
	return terms
}

func deriveChunkContext(chunk domain.RAGChunk) string {
	var parts []string
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, label+"="+value)
		}
	}
	add("source_kind", chunk.SourceKind)
	add("facet", chunk.Facet)
	if chunk.SourcePath != "" {
		add("source", filepath.Base(chunk.SourcePath))
	}
	for _, key := range []string{
		"project", "book", "title", "source_title", "section", "heading",
		"volume", "arc", "chapter", "genre", "tags",
	} {
		add(key, metadataValueString(chunk.Metadata[key]))
	}
	return truncateRunes(strings.Join(parts, " | "), 240)
}

func deriveChunkKeywords(chunk domain.RAGChunk) []string {
	var parts []string
	parts = append(parts, chunk.SourceKind, chunk.Facet, chunk.Context, chunk.Summary)
	if chunk.SourcePath != "" {
		parts = append(parts, strings.ReplaceAll(chunk.SourcePath, "/", " "))
	}
	for _, key := range []string{"title", "section", "heading", "genre", "tags"} {
		parts = append(parts, metadataValueString(chunk.Metadata[key]))
	}
	return normalizeKeywords(QueryTerms(parts...))
}

func normalizeKeywords(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = cleanTerm(item)
		if len([]rune(item)) < 2 || len([]rune(item)) > 24 {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
		if len(out) >= 24 {
			break
		}
	}
	return out
}

func metadataValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case []string:
		return strings.Join(x, " ")
	case []any:
		var parts []string
		for _, item := range x {
			if s := metadataValueString(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func normalizeSeparators(text string) string {
	replacer := strings.NewReplacer(
		"\n", " ", "\t", " ", "\r", " ",
		"，", " ", "。", " ", "、", " ", "；", " ", "：", " ",
		"！", " ", "？", " ", "（", " ", "）", " ", "《", " ", "》", " ",
		"“", " ", "”", " ", "\"", " ", "'", " ", "·", " ",
		",", " ", ".", " ", ";", " ", ":", " ", "!", " ", "?", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
		"/", " ", "\\", " ", "|", " ", "-", " ", "_", " ",
	)
	return replacer.Replace(text)
}

func cleanTerm(term string) string {
	return strings.Trim(strings.TrimSpace(term), " \t\r\n,.;:!?，。；：、！？（）()[]{}《》“”\"'`")
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func ErrNilBackend(name string) error {
	return fmt.Errorf("rag %s backend is nil", name)
}
