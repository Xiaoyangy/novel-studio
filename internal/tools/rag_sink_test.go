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

func TestUpsertRAGChunksDropsSecondAlgorithmDeprecatedEngineChunks(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise("《她的第二算法》女性职场成长文，主角许闻溪。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{
			{
				ID:         "chunk:old-engine",
				SourcePath: "summaries/01.json",
				SourceKind: "chapter_summary_facts",
				Facet:      "plot",
				Hash:       "old-engine",
				Text:       "待签纪要和日志窗口会污染下一章。",
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
			Text:       "后续用会后记录、后台明细和职场压力替代旧取证词。",
		},
		{
			SourcePath: "reviews/old.json",
			SourceKind: "review",
			Facet:      "review",
			Text:       "日志窗口继续作为隐喻。",
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
		if SecondAlgorithmProjectContaminationViolations(s, chunk.Text) != nil {
			t.Fatalf("deprecated engine chunk was kept: %+v", chunk)
		}
	}
	if len(state.Chunks) != 1 {
		t.Fatalf("expected only clean chunk, got %+v", state.Chunks)
	}
}
