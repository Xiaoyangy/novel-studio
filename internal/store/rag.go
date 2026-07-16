package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// RAGStore 保存 RAG 索引状态与召回 trace。
type RAGStore struct {
	io *IO

	cacheMu     sync.Mutex
	indexCache  *domain.RAGIndexState
	indexStamp  ragFileStamp
	vectorCache *domain.RAGVectorStore
	vectorStamp ragFileStamp
}

type ragFileStamp struct {
	size    int64
	modNano int64
}

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

// LoadIndexStateReadOnly caches the large immutable read snapshot and invalidates
// it when the atomic file is replaced. Callers must not mutate the returned value.
func (s *RAGStore) LoadIndexStateReadOnly() (*domain.RAGIndexState, error) {
	stamp, err := s.fileStamp("meta/rag/index_state.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	s.cacheMu.Lock()
	if s.indexCache != nil && s.indexStamp == stamp {
		cached := s.indexCache
		s.cacheMu.Unlock()
		return cached, nil
	}
	s.cacheMu.Unlock()
	state, finalStamp, err := s.readStableIndexState(stamp)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	s.indexCache = state
	s.indexStamp = finalStamp
	s.cacheMu.Unlock()
	return state, nil
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

// AppendCraftRecallLogOnce makes a receipt's audit evidence recoverable: if a
// prior call saved the receipt but failed while appending the log, the next
// retry fills the missing audit row without duplicating successful rows.
func (s *RAGStore) AppendCraftRecallLogOnce(receiptID string, entry map[string]any) error {
	receiptID = strings.TrimSpace(receiptID)
	if !validCraftRecallReceiptID(receiptID) {
		return fmt.Errorf("invalid craft recall receipt id %q", receiptID)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	marker := []byte(`"receipt_id":"` + receiptID + `"`)
	return s.io.WithWriteLock(func() error {
		existing, readErr := s.io.ReadFileUnlocked("meta/rag/craft_recall_log.jsonl")
		if readErr == nil && bytes.Contains(existing, marker) {
			return nil
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if len(existing) > 0 && existing[len(existing)-1] != '\n' {
			if err := s.io.AppendLineUnlocked("meta/rag/craft_recall_log.jsonl", []byte("\n")); err != nil {
				return err
			}
		}
		return s.io.AppendLineUnlocked("meta/rag/craft_recall_log.jsonl", append(data, '\n'))
	})
}

func craftRecallReceiptPath(id string) string {
	id = strings.TrimSpace(id)
	return fmt.Sprintf("meta/rag/craft_receipts/%s.json", id)
}

func validCraftRecallReceiptID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) != 24 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// SaveCraftRecallReceipt atomically persists the replayable result before a
// staged plan is created. The immutable id is content-derived by the caller.
func (s *RAGStore) SaveCraftRecallReceipt(receipt domain.CraftRecallReceipt) error {
	if !validCraftRecallReceiptID(receipt.ID) {
		return fmt.Errorf("invalid craft recall receipt id %q", receipt.ID)
	}
	return s.io.WriteJSON(craftRecallReceiptPath(receipt.ID), receipt)
}

func (s *RAGStore) LoadCraftRecallReceipt(id string) (*domain.CraftRecallReceipt, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	if !validCraftRecallReceiptID(id) {
		return nil, fmt.Errorf("invalid craft recall receipt id %q", id)
	}
	var receipt domain.CraftRecallReceipt
	if err := s.io.ReadJSON(craftRecallReceiptPath(id), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &receipt, nil
}

func (s *RAGStore) SaveIndexState(state domain.RAGIndexState) error {
	err := s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/rag/index_state.json", state); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/rag/index_state.md", renderRAGIndexState(state))
	})
	if err == nil {
		s.cacheMu.Lock()
		s.indexCache = nil
		s.indexStamp = ragFileStamp{}
		s.cacheMu.Unlock()
	}
	return err
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

// LoadVectorStoreReadOnly is the vector fallback counterpart of
// LoadIndexStateReadOnly. The returned snapshot is immutable.
func (s *RAGStore) LoadVectorStoreReadOnly() (*domain.RAGVectorStore, error) {
	stamp, err := s.fileStamp("meta/rag/vector_store.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	s.cacheMu.Lock()
	if s.vectorCache != nil && s.vectorStamp == stamp {
		cached := s.vectorCache
		s.cacheMu.Unlock()
		return cached, nil
	}
	s.cacheMu.Unlock()
	state, finalStamp, err := s.readStableVectorStore(stamp)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	s.vectorCache = state
	s.vectorStamp = finalStamp
	s.cacheMu.Unlock()
	return state, nil
}

func (s *RAGStore) SaveVectorStore(state domain.RAGVectorStore) error {
	err := s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/rag/vector_store.json", state); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("meta/rag/vector_store.md", renderRAGVectorStore(state))
	})
	if err == nil {
		s.cacheMu.Lock()
		s.vectorCache = nil
		s.vectorStamp = ragFileStamp{}
		s.cacheMu.Unlock()
	}
	return err
}

func (s *RAGStore) fileStamp(rel string) (ragFileStamp, error) {
	info, err := os.Stat(s.io.path(rel))
	if err != nil {
		return ragFileStamp{}, err
	}
	return ragFileStamp{size: info.Size(), modNano: info.ModTime().UnixNano()}, nil
}

func (s *RAGStore) readStableIndexState(initial ragFileStamp) (*domain.RAGIndexState, ragFileStamp, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var state domain.RAGIndexState
		if err := s.io.ReadJSON("meta/rag/index_state.json", &state); err != nil {
			return nil, ragFileStamp{}, err
		}
		final, err := s.fileStamp("meta/rag/index_state.json")
		if err != nil {
			return nil, ragFileStamp{}, err
		}
		if final == initial {
			return &state, final, nil
		}
		initial = final
	}
	return nil, ragFileStamp{}, fmt.Errorf("RAG index changed while reading")
}

func (s *RAGStore) readStableVectorStore(initial ragFileStamp) (*domain.RAGVectorStore, ragFileStamp, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var state domain.RAGVectorStore
		if err := s.io.ReadJSON("meta/rag/vector_store.json", &state); err != nil {
			return nil, ragFileStamp{}, err
		}
		final, err := s.fileStamp("meta/rag/vector_store.json")
		if err != nil {
			return nil, ragFileStamp{}, err
		}
		if final == initial {
			return &state, final, nil
		}
		initial = final
	}
	return nil, ragFileStamp{}, fmt.Errorf("RAG vector store changed while reading")
}

func (s *RAGStore) LoadPendingUpserts() (*domain.RAGPendingUpserts, error) {
	var pending domain.RAGPendingUpserts
	if err := s.io.ReadJSON("meta/rag/pending_upserts.json", &pending); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &pending, nil
}

func (s *RAGStore) SavePendingUpserts(pending domain.RAGPendingUpserts) error {
	return s.io.WriteJSON("meta/rag/pending_upserts.json", pending)
}

func (s *RAGStore) ClearPendingUpserts() error {
	return s.io.RemoveFile("meta/rag/pending_upserts.json")
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
	fmt.Fprintf(&b, "- Schema：v%d\n", state.SchemaVersion)
	fmt.Fprintf(&b, "- Embedding 并发：%d\n", state.Config.EmbeddingConcurrency)
	fmt.Fprintf(&b, "- Qdrant 写入并发：%d\n", state.Config.QdrantWriteConcurrency)
	if state.Config.VectorBatchSize > 0 {
		fmt.Fprintf(&b, "- 向量批大小：%d\n", state.Config.VectorBatchSize)
	}
	if state.Config.EmbeddingModel != "" {
		fmt.Fprintf(&b, "- Embedding 模型：%s\n", state.Config.EmbeddingModel)
	}
	if state.Config.VectorDimension > 0 {
		fmt.Fprintf(&b, "- 向量维度：%d\n", state.Config.VectorDimension)
	}
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
