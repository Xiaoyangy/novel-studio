package store

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRAGFactReceiptContentAddressedArtifactIsImmutable(t *testing.T) {
	st := NewStore(t.TempDir())
	first, err := domain.NewRAGFactReceipt(1, "青山县租约", []string{"租约"}, "local_bm25_v1", "", []domain.RAGFactReceiptHit{{
		Rank: 1, ChunkID: "fact:lease", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourcePath: "summaries/00.json", SourceKind: "chapter_summary_facts", Facet: "plot",
	}})
	if err != nil {
		t.Fatal(err)
	}
	first.CreatedAt = "2026-07-16T01:00:00Z"
	if err := st.RAG.SaveRAGFactReceipt(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.CreatedAt = "2026-07-16T02:00:00Z"
	if err := st.RAG.SaveRAGFactReceipt(second); err != nil {
		t.Fatal(err)
	}
	got, err := st.RAG.LoadRAGFactReceipt(first.ID)
	if err != nil || got == nil {
		t.Fatalf("load immutable receipt: got=%+v err=%v", got, err)
	}
	if got.CreatedAt != first.CreatedAt {
		t.Fatalf("same content id was rewritten: got created_at=%s want=%s", got.CreatedAt, first.CreatedAt)
	}
	latest, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || latest == nil || latest.CreatedAt != first.CreatedAt {
		t.Fatalf("latest pointer did not retain immutable receipt: latest=%+v err=%v", latest, err)
	}
}
