package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

var ragFactReceiptProcessMu sync.Mutex

func ragFactReceiptPath(id string) string {
	return filepath.Join("meta", "rag", "fact_receipts", strings.TrimSpace(id)+".json")
}

func latestRAGFactReceiptPath(chapter int) string {
	return filepath.Join("meta", "rag", "fact_receipts", "latest", fmt.Sprintf("%06d.json", chapter))
}

// SaveRAGFactReceipt persists the immutable content-addressed receipt before
// advancing the chapter latest pointer. A crash can leave an unreachable
// immutable receipt, but can never point planning at a partially written one.
func (s *RAGStore) SaveRAGFactReceipt(receipt domain.RAGFactReceipt) error {
	if err := domain.ValidateRAGFactReceipt(receipt); err != nil {
		return err
	}
	ragFactReceiptProcessMu.Lock()
	defer ragFactReceiptProcessMu.Unlock()
	return s.io.WithWriteLock(func() error {
		var existing domain.RAGFactReceipt
		err := s.io.ReadJSONUnlocked(ragFactReceiptPath(receipt.ID), &existing)
		switch {
		case err == nil:
			if validateErr := domain.ValidateRAGFactReceipt(existing); validateErr != nil {
				return validateErr
			}
			if existing.PayloadSHA256 != receipt.PayloadSHA256 ||
				existing.SelectedFactsSHA256 != receipt.SelectedFactsSHA256 ||
				existing.Chapter != receipt.Chapter {
				return fmt.Errorf("RAG fact receipt id collision for %s", receipt.ID)
			}
			// The id excludes observation time. Preserve the first immutable
			// content-addressed artifact instead of rewriting it on a cache hit.
			receipt = existing
		case !os.IsNotExist(err):
			return err
		default:
			if err := s.io.WriteJSONUnlocked(ragFactReceiptPath(receipt.ID), receipt); err != nil {
				return err
			}
		}
		return s.io.WriteJSONUnlocked(latestRAGFactReceiptPath(receipt.Chapter), receipt)
	})
}

func (s *RAGStore) LoadRAGFactReceipt(id string) (*domain.RAGFactReceipt, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var receipt domain.RAGFactReceipt
	if err := s.io.ReadJSON(ragFactReceiptPath(id), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateRAGFactReceipt(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *RAGStore) LoadLatestRAGFactReceipt(chapter int) (*domain.RAGFactReceipt, error) {
	if chapter <= 0 {
		return nil, fmt.Errorf("RAG fact receipt chapter must be > 0")
	}
	var receipt domain.RAGFactReceipt
	if err := s.io.ReadJSON(latestRAGFactReceiptPath(chapter), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateRAGFactReceipt(receipt); err != nil {
		return nil, err
	}
	if receipt.Chapter != chapter {
		return nil, fmt.Errorf("latest RAG fact receipt chapter mismatch: got %d want %d", receipt.Chapter, chapter)
	}
	return &receipt, nil
}
