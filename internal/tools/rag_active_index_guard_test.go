package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
)

func TestActiveRAGFactHashesBindsIDAndContentHash(t *testing.T) {
	fact := rag.NormalizeChunk(domain.RAGChunk{ID: "fact-1", SourcePath: "project/world.md", SourceKind: "world", Text: "本书事实"})
	design := rag.NormalizeChunk(domain.RAGChunk{ID: "craft-1", SourcePath: "shared/craft.md", SourceKind: rag.CraftSourceKind, Text: "写法资料"})
	trusted := activeRAGFactHashes(&domain.RAGIndexState{Chunks: []domain.RAGChunk{fact, design}})
	if len(trusted) != 1 || trusted[fact.ID] != fact.Hash {
		t.Fatalf("unexpected trusted fact set: %+v", trusted)
	}
	foreign := rag.NormalizeChunk(domain.RAGChunk{ID: fact.ID, SourcePath: "other/world.md", SourceKind: "world", Text: "别的项目事实"})
	if trusted[foreign.ID] == foreign.Hash {
		t.Fatal("same ID with a foreign content hash must not pass the active-index guard")
	}
}
