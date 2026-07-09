package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// RAGStore 保存 RAG 索引状态与召回 trace。
type RAGStore struct{ io *IO }

func NewRAGStore(io *IO) *RAGStore { return &RAGStore{io: io} }

func (s *RAGStore) LoadIndexState() (*domain.RAGIndexState, error) {
	var state domain.RAGIndexState
	if err := s.io.ReadJSON("meta/rag/index_state.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

// AppendCraftRecallLog 记录一次设计时刻的手法库检索（含 no_material 事件），
// 落 meta/rag/craft_recall_log.jsonl 供审计回放。
func (s *RAGStore) AppendCraftRecallLog(entry map[string]any) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		if existing, err := s.io.ReadFileUnlocked("meta/rag/craft_recall_log.jsonl"); err == nil && len(existing) > 0 && existing[len(existing)-1] != '\n' {
			if err := s.io.AppendLineUnlocked("meta/rag/craft_recall_log.jsonl", []byte("\n")); err != nil {
				return err
			}
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		return s.io.AppendLineUnlocked("meta/rag/craft_recall_log.jsonl", append(data, '\n'))
	})
}

func (s *RAGStore) SaveIndexState(state domain.RAGIndexState) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/rag/index_state.json", state); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/rag/index_state.md", renderRAGIndexState(state))
	})
}

func (s *RAGStore) LoadVectorStore() (*domain.RAGVectorStore, error) {
	var state domain.RAGVectorStore
	if err := s.io.ReadJSON("meta/rag/vector_store.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func (s *RAGStore) SaveVectorStore(state domain.RAGVectorStore) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/rag/vector_store.json", state); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/rag/vector_store.md", renderRAGVectorStore(state))
	})
}

func (s *RAGStore) AppendTrace(trace domain.RetrievalTrace) error {
	data, err := marshalJSONLine(trace)
	if err != nil {
		return err
	}
	return s.io.AppendLine("meta/rag/retrieval_trace.jsonl", data)
}

func renderRAGIndexState(state domain.RAGIndexState) string {
	var b strings.Builder
	b.WriteString("# RAG 索引状态\n\n")
	fmt.Fprintf(&b, "- Embedding 并发：%d\n", state.Config.EmbeddingConcurrency)
	fmt.Fprintf(&b, "- Qdrant 写入并发：%d\n", state.Config.QdrantWriteConcurrency)
	if state.Config.Collection != "" {
		fmt.Fprintf(&b, "- Collection：%s\n", state.Config.Collection)
	}
	fmt.Fprintf(&b, "- Chunk 数：%d\n", len(state.Chunks))
	fmt.Fprintf(&b, "- Chunk hash 数：%d\n", len(state.ChunkHashes))
	if state.UpdatedAt != "" {
		fmt.Fprintf(&b, "- 更新时间：%s\n", state.UpdatedAt)
	}
	facets := map[string]int{}
	for _, ch := range state.Chunks {
		facets[ch.Facet]++
	}
	if len(facets) > 0 {
		b.WriteString("\n## Facets\n\n")
		for facet, n := range facets {
			if facet == "" {
				facet = "(none)"
			}
			fmt.Fprintf(&b, "- %s：%d\n", facet, n)
		}
	}
	return b.String()
}

func renderRAGVectorStore(state domain.RAGVectorStore) string {
	var b strings.Builder
	b.WriteString("# RAG 向量索引状态\n\n")
	if state.Config.VectorStore != "" {
		fmt.Fprintf(&b, "- Vector store：%s\n", state.Config.VectorStore)
	}
	if state.Config.QdrantURL != "" {
		fmt.Fprintf(&b, "- Qdrant URL：%s\n", state.Config.QdrantURL)
	}
	if state.Config.Collection != "" {
		fmt.Fprintf(&b, "- Collection：%s\n", state.Config.Collection)
	}
	if state.Config.EmbeddingProvider != "" {
		fmt.Fprintf(&b, "- Embedding provider：%s\n", state.Config.EmbeddingProvider)
	}
	if state.Config.EmbeddingModel != "" {
		fmt.Fprintf(&b, "- Embedding model：%s\n", state.Config.EmbeddingModel)
	}
	if state.Config.VectorDimension > 0 {
		fmt.Fprintf(&b, "- Vector dimension：%d\n", state.Config.VectorDimension)
	}
	fmt.Fprintf(&b, "- Point 数：%d\n", len(state.Points))
	if state.UpdatedAt != "" {
		fmt.Fprintf(&b, "- 更新时间：%s\n", state.UpdatedAt)
	}
	return b.String()
}

func marshalJSONLine(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
