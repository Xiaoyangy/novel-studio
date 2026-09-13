package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const OutlineAllExecutionReceiptPath = "meta/planning/outline_all_execution.json"

// ValidateOutlineAllChapterZeroWorkspace rejects a progress-only reset. The
// exact live/candidate root being planned must contain no committed chapter,
// no draft artifact, and no prior formal chapter-plan generation.
func (s *Store) ValidateOutlineAllChapterZeroWorkspace() error {
	checks := []struct {
		rel       string
		forbidden func(string, fs.DirEntry) bool
		label     string
	}{
		{
			rel: "chapters",
			forbidden: func(path string, entry fs.DirEntry) bool {
				return entry.Type()&os.ModeSymlink != 0 ||
					(!entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md"))
			},
			label: "committed chapter",
		},
		{
			rel:       "drafts",
			forbidden: func(_ string, entry fs.DirEntry) bool { return !entry.IsDir() },
			label:     "draft prose or formal plan",
		},
		{
			rel:       "meta/planning/chapters",
			forbidden: func(_ string, entry fs.DirEntry) bool { return !entry.IsDir() },
			label:     "formal chapter plan",
		},
		{
			rel:       "meta/planning/generations",
			forbidden: func(_ string, entry fs.DirEntry) bool { return !entry.IsDir() },
			label:     "formal planning generation",
		},
		{
			rel:       "meta/planning/v2",
			forbidden: func(_ string, entry fs.DirEntry) bool { return !entry.IsDir() },
			label:     "formal planning generation",
		},
	}
	for _, check := range checks {
		root := filepath.Join(s.dir, filepath.FromSlash(check.rel))
		var found string
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if check.forbidden(path, entry) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect outline-all chapter-zero workspace %s: %w", check.rel, err)
		}
		if found != "" {
			rel, relErr := filepath.Rel(s.dir, found)
			if relErr != nil {
				rel = found
			}
			return fmt.Errorf("outline-all chapter-zero workspace contains %s at %s", check.label, filepath.ToSlash(rel))
		}
	}
	for _, rel := range []string{
		"正文.md",
		"meta/planning/current_plan.json",
		"meta/planning/preplan_receipt.json",
	} {
		if _, err := os.Lstat(filepath.Join(s.dir, filepath.FromSlash(rel))); err == nil {
			return fmt.Errorf("outline-all chapter-zero workspace contains prior prose/formal plan at %s", rel)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect outline-all chapter-zero workspace %s: %w", rel, err)
		}
	}
	return nil
}

// SaveOutlineAllExecutionReceipt atomically publishes a fully signed
// outline-all checkpoint. The domain validator prevents a caller from
// persisting an unsigned or internally inconsistent capability receipt.
func (s *Store) SaveOutlineAllExecutionReceipt(
	receipt domain.OutlineAllExecutionReceipt,
) error {
	if err := domain.ValidateOutlineAllExecutionReceipt(receipt); err != nil {
		return err
	}
	s.Outline.io.mu.RLock()
	defer s.Outline.io.mu.RUnlock()
	if err := s.validateOutlineAllReceiptAuthorContractsUnlocked(receipt); err != nil {
		return err
	}
	return s.Progress.io.WriteJSON(OutlineAllExecutionReceiptPath, receipt)
}

func (s *Store) LoadOutlineAllExecutionReceipt() (*domain.OutlineAllExecutionReceipt, error) {
	s.Outline.io.mu.RLock()
	defer s.Outline.io.mu.RUnlock()
	var receipt domain.OutlineAllExecutionReceipt
	if err := s.Progress.io.ReadJSON(OutlineAllExecutionReceiptPath, &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateOutlineAllExecutionReceipt(receipt); err != nil {
		return nil, err
	}
	if err := s.validateOutlineAllReceiptAuthorContractsUnlocked(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// UpdateOutlineAllExecutionReceipt performs a compare-and-swap update under
// the receipt file lock, then recomputes and validates the digest before the
// atomic rename. It is suitable for crash recovery rebinds and per-mutation
// pending/completed checkpoints.
func (s *Store) UpdateOutlineAllExecutionReceipt(
	expectedDigest string,
	mutate func(*domain.OutlineAllExecutionReceipt) error,
) (*domain.OutlineAllExecutionReceipt, error) {
	expectedDigest = strings.TrimSpace(expectedDigest)
	if expectedDigest == "" {
		return nil, fmt.Errorf("outline-all receipt update requires expected digest")
	}
	if mutate == nil {
		return nil, fmt.Errorf("outline-all receipt update requires mutate function")
	}
	var updated domain.OutlineAllExecutionReceipt
	// Match cross-domain outline/progress mutations' lock order. Validation
	// below uses unlocked outline reads and never reacquires this RWMutex.
	s.Outline.io.mu.RLock()
	defer s.Outline.io.mu.RUnlock()
	err := s.Progress.io.WithWriteLock(func() error {
		var current domain.OutlineAllExecutionReceipt
		if err := s.Progress.io.ReadJSONUnlocked(OutlineAllExecutionReceiptPath, &current); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("outline-all execution receipt is missing")
			}
			return err
		}
		if err := domain.ValidateOutlineAllExecutionReceipt(current); err != nil {
			return err
		}
		if err := s.validateOutlineAllReceiptAuthorContractsUnlocked(current); err != nil {
			return err
		}
		if current.ReceiptDigest != expectedDigest {
			return fmt.Errorf("outline-all execution receipt changed before update")
		}
		if err := mutate(&current); err != nil {
			return err
		}
		signed, err := domain.SignOutlineAllExecutionReceipt(current)
		if err != nil {
			return err
		}
		if err := s.validateOutlineAllReceiptAuthorContractsUnlocked(signed); err != nil {
			return err
		}
		if err := s.Progress.io.WriteJSONUnlocked(OutlineAllExecutionReceiptPath, signed); err != nil {
			return err
		}
		updated = signed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// Domain validation checks the receipt's shape. New source-bound receipts
// additionally require the actual immutable author catalog and current exact
// source-materialized compass; a self-signed policy marker is not authority.
func (s *Store) validateOutlineAllReceiptAuthorContractsUnlocked(receipt domain.OutlineAllExecutionReceipt) error {
	catalog, err := loadAuthorSourcesUnlocked(s.Outline.io)
	if err != nil {
		return err
	}
	if catalog == nil && receipt.AuthorContracts == nil {
		return s.Outline.requireLegacyCompassWithoutAuthorBindingUnlocked()
	}
	if catalog == nil || receipt.AuthorContracts == nil {
		return fmt.Errorf("outline-all receipt author source mode differs from the actual catalog")
	}
	var compass domain.StoryCompass
	if err := s.Outline.io.ReadJSONUnlocked("meta/compass.json", &compass); err != nil {
		return err
	}
	if compass.AuthorContracts == nil {
		return fmt.Errorf("outline-all source-bound receipt lacks its actual bound compass")
	}
	materialized, err := domain.MaterializeCompassAuthorContractsV1(compass, *catalog)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(materialized.NonNegotiables, compass.NonNegotiables) {
		return fmt.Errorf("outline-all receipt cannot repair altered stored author contracts")
	}
	digest, err := domain.ComputeStoryCompassDigest(compass)
	if err != nil {
		return err
	}
	if digest != receipt.CompassDigest || !reflect.DeepEqual(receipt.AuthorContracts, compass.AuthorContracts) ||
		receipt.AuthorContracts.SourcesDigest != catalog.Digest || receipt.EstimatedScale != compass.EstimatedScale ||
		receipt.EndingDirection != compass.EndingDirection || !slices.Equal(receipt.NonNegotiables, compass.NonNegotiables) {
		return fmt.Errorf("outline-all receipt author binding differs from the exact current compass/catalog")
	}
	return nil
}
