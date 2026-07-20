package tools

import (
	"context"
	"io"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type permanentEOFRAGEmbedder struct{}

func (permanentEOFRAGEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, io.EOF
}

type trackingRAGWriter struct {
	deletes atomic.Int32
	writes  atomic.Int32
}

type countingRAGEmbedder struct{ calls atomic.Int32 }

func (e *countingRAGEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls.Add(1)
	return []float32{1, 0}, nil
}

func (w *trackingRAGWriter) Write(context.Context, rag.VectorPoint) error {
	w.writes.Add(1)
	return nil
}

func (w *trackingRAGWriter) DeleteSourcePath(context.Context, string) error {
	w.deletes.Add(1)
	return nil
}

func TestUpsertRAGChunksEmbeddingEOFPreservesCommittedSnapshot(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	oldChunk := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "outline.md", SourceKind: "planning", Text: "旧的青山县经营计划。",
	})
	oldState := domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_vector", VectorDimension: 2},
		Chunks: []domain.RAGChunk{oldChunk}, ChunkHashes: []string{oldChunk.Hash}, UpdatedAt: "old-index",
	}
	oldVectors := domain.RAGVectorStore{
		Config:    domain.RAGIndexConfig{Collection: "local_vector", VectorDimension: 2},
		Points:    []domain.RAGVectorPoint{{ID: oldChunk.ID, Hash: oldChunk.Hash, Vector: []float32{1, 0}, Chunk: oldChunk}},
		UpdatedAt: "old-vectors",
	}
	if err := s.RAG.SaveIndexState(oldState); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := s.RAG.SaveVectorStore(oldVectors); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	remote := &trackingRAGWriter{}
	err := UpsertRAGChunks(context.Background(), s, permanentEOFRAGEmbedder{}, remote, []domain.RAGChunk{
		{SourcePath: "outline.md", SourceKind: "planning", Text: "新的返乡经营计划。"},
	}, domain.RAGIndexConfig{})
	if err == nil {
		t.Fatal("expected embedding EOF")
	}
	if remote.deletes.Load() != 0 || remote.writes.Load() != 0 {
		t.Fatalf("remote source changed before embedding stage completed: deletes=%d writes=%d", remote.deletes.Load(), remote.writes.Load())
	}
	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	vectors, err := s.RAG.LoadVectorStore()
	if err != nil {
		t.Fatalf("LoadVectorStore: %v", err)
	}
	if state.UpdatedAt != "old-index" || len(state.Chunks) != 1 || state.Chunks[0].Hash != oldChunk.Hash {
		t.Fatalf("index state changed after failed staging: %+v", state)
	}
	if vectors.UpdatedAt != "old-vectors" || len(vectors.Points) != 1 || vectors.Points[0].Hash != oldChunk.Hash {
		t.Fatalf("vector store changed after failed staging: %+v", vectors)
	}
	pending, err := s.RAG.LoadPendingUpserts()
	if err != nil {
		t.Fatalf("LoadPendingUpserts: %v", err)
	}
	if pending == nil || len(pending.Chunks) != 1 || pending.Chunks[0].SourcePath != "outline.md" {
		t.Fatalf("failed upsert was not queued for replay: %+v", pending)
	}
}

func TestUpsertRAGChunksUnchangedSourceIsNoOp(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		SourcePath: "meta/project_progress.md", SourceKind: "ledger", Text: "青山县门店进入试营业。",
	})
	cfg := domain.RAGIndexConfig{Collection: "local_vector", EmbeddingModel: "test", VectorDimension: 2}
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{Config: cfg, Chunks: []domain.RAGChunk{chunk}, ChunkHashes: []string{chunk.Hash}, UpdatedAt: "stable"}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := s.RAG.SaveVectorStore(domain.RAGVectorStore{
		Config:    cfg,
		Points:    []domain.RAGVectorPoint{{ID: chunk.ID, Hash: chunk.Hash, Vector: []float32{1, 0}, Chunk: chunk}},
		UpdatedAt: "stable",
	}); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	embedder := &countingRAGEmbedder{}
	remote := &trackingRAGWriter{}
	if err := UpsertRAGChunks(context.Background(), s, embedder, remote, []domain.RAGChunk{chunk}, domain.RAGIndexConfig{}); err != nil {
		t.Fatalf("UpsertRAGChunks: %v", err)
	}
	if embedder.calls.Load() != 0 || remote.deletes.Load() != 0 || remote.writes.Load() != 0 {
		t.Fatalf("unchanged source should be a no-op: embeds=%d deletes=%d writes=%d", embedder.calls.Load(), remote.deletes.Load(), remote.writes.Load())
	}
	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if state.UpdatedAt != "stable" {
		t.Fatalf("no-op rewrote index state: %+v", state)
	}
}

func TestUpsertRAGChunksReplaysPendingUpsertsOnNextSuccess(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	oldChunk := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "outline.md", SourceKind: "planning", Text: "旧计划。"})
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{oldChunk}}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := s.RAG.SaveVectorStore(domain.RAGVectorStore{
		Config: domain.RAGIndexConfig{VectorDimension: 2},
		Points: []domain.RAGVectorPoint{{ID: oldChunk.ID, Hash: oldChunk.Hash, Vector: []float32{1, 0}, Chunk: oldChunk}},
	}); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	pendingChunk := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "outline.md", SourceKind: "planning", Text: "待回填的新计划。"})
	if err := s.RAG.SavePendingUpserts(domain.RAGPendingUpserts{Chunks: []domain.RAGChunk{pendingChunk}, LastError: "EOF"}); err != nil {
		t.Fatalf("SavePendingUpserts: %v", err)
	}
	embedder := &countingRAGEmbedder{}
	remote := &trackingRAGWriter{}
	current := domain.RAGChunk{SourcePath: "reviews/01.json", SourceKind: "review", Text: "本轮审核结论。"}
	if err := UpsertRAGChunks(context.Background(), s, embedder, remote, []domain.RAGChunk{current}, domain.RAGIndexConfig{}); err != nil {
		t.Fatalf("UpsertRAGChunks: %v", err)
	}
	if embedder.calls.Load() != 2 || remote.deletes.Load() != 2 || remote.writes.Load() != 2 {
		t.Fatalf("pending replay counts: embeds=%d deletes=%d writes=%d", embedder.calls.Load(), remote.deletes.Load(), remote.writes.Load())
	}
	pending, err := s.RAG.LoadPendingUpserts()
	if err != nil {
		t.Fatalf("LoadPendingUpserts: %v", err)
	}
	if pending != nil {
		t.Fatalf("pending queue was not cleared: %+v", pending)
	}
	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if len(state.Chunks) != 2 {
		t.Fatalf("replayed state missing chunks: %+v", state.Chunks)
	}
	for _, chunk := range state.Chunks {
		if chunk.Hash == oldChunk.Hash {
			t.Fatalf("old source survived replay: %+v", state.Chunks)
		}
	}
}

func TestUpsertRAGChunksDropsForbiddenExistingChunks(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{
			{
				ID:         "chunk:forbidden",
				SourcePath: "/tmp/拆文库/参考/样本.md",
				SourceKind: "deconstruction",
				Facet:      "craft",
				Hash:       "forbidden",
				Text:       "不应继续留在本书写作 RAG。",
			},
			{
				ID:         "chunk:kept",
				SourcePath: "outline.md",
				SourceKind: "planning",
				Facet:      "planning",
				Hash:       "kept",
				Text:       "当前项目大纲事实。",
			},
		},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	err := UpsertRAGChunks(context.Background(), s, nil, nil, []domain.RAGChunk{
		{
			SourcePath: "meta/writing_assets.md",
			SourceKind: "craft",
			Facet:      "craft",
			Text:       "当前项目写法资产事实。",
		},
	}, domain.RAGIndexConfig{})
	if err != nil {
		t.Fatalf("UpsertRAGChunks: %v", err)
	}

	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	for _, chunk := range state.Chunks {
		if chunk.ID == "chunk:forbidden" || chunk.SourceKind == "deconstruction" {
			t.Fatalf("forbidden chunk was kept: %+v", chunk)
		}
	}
	if len(state.Chunks) != 2 {
		t.Fatalf("expected kept project chunk plus new chunk, got %+v", state.Chunks)
	}
}

func TestUpsertRAGChunksDropsConfiguredProjectContaminationChunks(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	saveTestProjectContaminationTerms(t, s, "过期流程术语")
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{
			{
				ID:         "chunk:old-engine",
				SourcePath: "summaries/01.json",
				SourceKind: "chapter_summary_facts",
				Facet:      "plot",
				Hash:       "old-engine",
				Text:       "过期流程术语会污染下一章。",
			},
		},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	err := UpsertRAGChunks(context.Background(), s, nil, nil, []domain.RAGChunk{
		{
			SourcePath: "reviews/01_deepseek_ai_judge.json",
			SourceKind: "review",
			Facet:      "review",
			Text:       "后续只使用当前项目记录。",
		},
		{
			SourcePath: "reviews/old.json",
			SourceKind: "review",
			Facet:      "review",
			Text:       "过期流程术语继续作为隐喻。",
		},
	}, domain.RAGIndexConfig{})
	if err != nil {
		t.Fatalf("UpsertRAGChunks: %v", err)
	}

	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	for _, chunk := range state.Chunks {
		if ProjectContaminationViolations(s, chunk.Text) != nil {
			t.Fatalf("configured contamination chunk was kept: %+v", chunk)
		}
	}
	if len(state.Chunks) != 1 {
		t.Fatalf("expected only clean chunk, got %+v", state.Chunks)
	}
}
