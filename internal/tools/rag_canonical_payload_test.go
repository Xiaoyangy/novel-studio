package tools

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRAGRecallTakesCanonicalPayloadDespiteForgedCachedHash(t *testing.T) {
	for _, channel := range []string{"qdrant", "local_vector"} {
		t.Run(channel, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			canonical := rag.NormalizeChunk(domain.RAGChunk{
				ID: "fact:permit", SourcePath: "project/world.json", SourceKind: "world", Facet: "world",
				Summary: "许可证仅在白天有效。", Text: "每晚零点许可证失效，持有人必须等待天亮。",
				Context: "本书已接受的通行规则", Metadata: map[string]any{"chapter": 0},
			})
			if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{canonical}}); err != nil {
				t.Fatal(err)
			}
			forged := canonical
			forged.Text = "FORGED_SECRET：主角拥有未被授权的夜间通行能力。"
			forged.Summary = "FORGED_SECRET：许可证永久有效。"
			forged.Context = "FORGED_SECRET：其他角色的隐藏记忆。"
			forged.SourcePath = "project/FORGED_SECRET.json"
			forged.Metadata = map[string]any{"hidden": "FORGED_SECRET"}
			// ID/hash remain those of the current canonical source, reproducing
			// cache payload drift that an ID + claimed-hash gate cannot detect.
			if forged.Hash != canonical.Hash || rag.RehashChunk(forged).Hash == canonical.Hash {
				t.Fatal("invalid forgery fixture")
			}
			tool := NewContextTool(st, References{}, "default").WithRAGEmbedder(contextTestEmbedder{})
			if channel == "qdrant" {
				tool.WithRAGVectorSearcher(contextTestSearcher{hits: []rag.VectorSearchHit{{Point: domain.RAGVectorPoint{ID: forged.ID, Chunk: forged}, Score: 0.9}}})
			} else {
				if err := st.RAG.SaveVectorStore(domain.RAGVectorStore{Points: []domain.RAGVectorPoint{{ID: forged.ID, Hash: forged.Hash, Chunk: forged, Vector: []float32{1, 0}}}}); err != nil {
					t.Fatal(err)
				}
			}
			items, trace := tool.selectRAGRecallFresh(context.Background(), contextBuildState{chapter: 1, currentEntry: &domain.OutlineEntry{Chapter: 1, Title: "核对许可证", CoreEvent: "核对许可证的有效时间"}})
			if len(items) != 1 || items[0].Summary != canonical.Summary || items[0].Key != canonical.ID {
				t.Fatalf("canonical summary lost: %+v", items)
			}
			if trace == nil || len(trace.Matches) != 1 || trace.Matches[0].ContentSHA256 != rag.RehashChunk(canonical).Hash || trace.Matches[0].SourcePath != canonical.SourcePath {
				t.Fatalf("noncanonical receipt: %+v", trace)
			}
			provenance := "qdrant:"
			if channel == "local_vector" {
				provenance = "vector:"
			}
			if !strings.Contains(items[0].Reason, provenance) {
				t.Fatalf("semantic provenance lost: %+v", items)
			}
			encoded, err := json.Marshal(struct {
				Items []domain.RecallItem
				Trace *domain.RetrievalTrace
			}{items, trace})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "FORGED_SECRET") {
				t.Fatalf("untrusted content leaked: %s", encoded)
			}
		})
	}
}

func TestRAGRecallRejectsNonFiniteRemoteScores(t *testing.T) {
	for _, score := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		st := store.NewStore(t.TempDir())
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
		fact := rag.NormalizeChunk(domain.RAGChunk{ID: "fact:known", SourcePath: "project/world.json", SourceKind: "world", Text: "许可证白天有效"})
		if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{fact}}); err != nil {
			t.Fatal(err)
		}
		tool := NewContextTool(st, References{}, "default").WithRAGEmbedder(contextTestEmbedder{}).WithRAGVectorSearcher(contextTestSearcher{hits: []rag.VectorSearchHit{{Point: domain.RAGVectorPoint{ID: fact.ID, Chunk: fact}, Score: score}}})
		items, trace := tool.selectRAGRecallFresh(context.Background(), contextBuildState{chapter: 1, currentEntry: &domain.OutlineEntry{Chapter: 1, Title: "许可证"}})
		if len(items) != 1 || strings.Contains(items[0].Reason, "qdrant:") {
			t.Fatalf("invalid remote score survived: %+v", items)
		}
		if _, err := json.Marshal(trace); err != nil {
			t.Fatalf("invalid float poisoned retrieval trace: %v", err)
		}
	}
}

func TestRAGRecallFiniteDuplicateScoresCannotOverflowTrace(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	fact := rag.NormalizeChunk(domain.RAGChunk{ID: "fact:known", SourcePath: "project/world.json", SourceKind: "world", Text: "许可证白天有效"})
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{fact}}); err != nil {
		t.Fatal(err)
	}
	hit := rag.VectorSearchHit{Point: domain.RAGVectorPoint{ID: fact.ID, Chunk: fact}, Score: math.MaxFloat64}
	tool := NewContextTool(st, References{}, "default").WithRAGEmbedder(contextTestEmbedder{}).WithRAGVectorSearcher(contextTestSearcher{hits: []rag.VectorSearchHit{hit, hit}})
	items, trace := tool.selectRAGRecallFresh(context.Background(), contextBuildState{chapter: 1, currentEntry: &domain.OutlineEntry{Chapter: 1, Title: "许可证"}})
	if len(items) != 1 || trace == nil || len(trace.Matches) != 1 || math.IsInf(trace.Matches[0].Score, 0) || math.IsNaN(trace.Matches[0].Score) {
		t.Fatalf("finite duplicate scores poisoned recall: %+v", trace)
	}
	if _, err := json.Marshal(trace); err != nil {
		t.Fatal(err)
	}
}
