package store

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRAGReadOnlyCachesInvalidateAfterSave(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{{ID: "one", Hash: "one", Text: "first"}}}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	first, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		t.Fatalf("LoadIndexStateReadOnly: %v", err)
	}
	second, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		t.Fatalf("LoadIndexStateReadOnly: %v", err)
	}
	if first != second {
		t.Fatal("unchanged index should reuse the read-only snapshot")
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{{ID: "two", Hash: "two", Text: "second version"}}}); err != nil {
		t.Fatalf("SaveIndexState 2: %v", err)
	}
	third, err := st.RAG.LoadIndexStateReadOnly()
	if err != nil {
		t.Fatalf("LoadIndexStateReadOnly 3: %v", err)
	}
	if third == second || len(third.Chunks) != 1 || third.Chunks[0].ID != "two" {
		t.Fatalf("save did not invalidate cached index: %+v", third)
	}

	vector := domain.RAGVectorStore{Points: []domain.RAGVectorPoint{{ID: "one", Vector: []float32{1}}}}
	if err := st.RAG.SaveVectorStore(vector); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	v1, err := st.RAG.LoadVectorStoreReadOnly()
	if err != nil {
		t.Fatalf("LoadVectorStoreReadOnly: %v", err)
	}
	v2, err := st.RAG.LoadVectorStoreReadOnly()
	if err != nil {
		t.Fatalf("LoadVectorStoreReadOnly: %v", err)
	}
	if v1 != v2 {
		t.Fatal("unchanged vector store should reuse the read-only snapshot")
	}
}
