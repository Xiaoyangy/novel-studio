package tools

import (
	"context"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

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
