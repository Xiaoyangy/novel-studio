package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCraftRecallFiltersSecondAlgorithmCrossProjectChunks(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "许闻溪", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	tool := NewCraftRecallTool(st)
	chunks := []domain.RAGChunk{
		{ID: "bad", SourcePath: "deconstruction-library/ghost.md", Hash: "bad", Text: "江烬收到欠费单。"},
		{ID: "ok", SourcePath: "assets/references/dialogue.md", Hash: "ok", Text: "对白要有潜台词和动作拍。"},
	}
	filtered, dropped := tool.filterCrossProjectCraftChunks(chunks)
	if dropped != 1 || len(filtered) != 1 || filtered[0].ID != "ok" {
		t.Fatalf("expected only clean chunk, dropped=%d filtered=%+v", dropped, filtered)
	}
}

func TestCraftRecallInfersChapterAndStopsBudgetLoop(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 70, GenerationID: "simulation-test"}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		Chunks: []domain.RAGChunk{{
			ID:         "method-1",
			SourcePath: "writing-techniques/novel-craft-methodology/人物.md",
			SourceKind: "craft_technique",
			Facet:      "craft",
			Hash:       "method-1",
			Context:    "人物刻画",
			Summary:    "人物行动要由处境和动机推出。",
			Text:       "人物刻画要让情绪变成选择，而不是标签。",
			Metadata: map[string]any{
				"craft_category": "novel-craft-methodology",
			},
		}},
		ChunkHashes: []string{"method-1"},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	tool := NewCraftRecallTool(st)
	args := json.RawMessage(`{"field":"methodology","topic":"人物刻画 情感叙事"}`)
	for i := 1; i <= 4; i++ {
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("call %d Execute: %v", i, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("call %d json: %v", i, err)
		}
		if got["budget_exhausted"] == true {
			t.Fatalf("call %d should still be allowed: %s", i, raw)
		}
		if got["chapter"].(float64) != 1 {
			t.Fatalf("call %d should infer chapter 1, got %v", i, got["chapter"])
		}
	}
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("budget call Execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("budget json: %v", err)
	}
	if got["budget_exhausted"] != true || got["chapter"].(float64) != 1 {
		t.Fatalf("expected budget exhausted for inferred chapter 1, got %s", raw)
	}
}
