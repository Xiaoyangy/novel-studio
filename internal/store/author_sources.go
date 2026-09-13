package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const AuthorSourcesPath = "meta/author_sources.json"
const authorSourcesLockPath = "meta/.author_sources.lock"

// SaveAuthorSources is host-only immutable initialization. The catalog stores
// original author inputs, and is never writable through a model tool.
func (s *Store) SaveAuthorSources(value domain.AuthorSourcesV1) error {
	finalized, err := domain.FinalizeAuthorSourcesV1(value)
	if err != nil {
		return err
	}
	return s.Outline.io.WithWriteLock(func() error {
		return withAuthorSourcesFileLock(s.Outline.io, func() error {
			existing, err := loadAuthorSourcesUnlocked(s.Outline.io)
			if err != nil {
				return err
			}
			if existing != nil {
				if !reflect.DeepEqual(*existing, finalized) {
					return fmt.Errorf("immutable author source catalog already exists with different content")
				}
				return nil
			}
			raw, err := json.MarshalIndent(finalized, "", "  ")
			if err != nil {
				return err
			}
			return s.Outline.io.writeFileNoReplaceUnlocked(AuthorSourcesPath, raw)
		})
	})
}

func (s *Store) LoadAuthorSources() (*domain.AuthorSourcesV1, error) {
	s.Outline.io.mu.RLock()
	defer s.Outline.io.mu.RUnlock()
	return loadAuthorSourcesUnlocked(s.Outline.io)
}

func loadAuthorSourcesUnlocked(storage *IO) (*domain.AuthorSourcesV1, error) {
	if err := validateCharacterMemoryPublicationPath(storage, AuthorSourcesPath); err != nil {
		return nil, err
	}
	raw, err := storage.ReadFileUnlocked(AuthorSourcesPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 32<<20 {
		return nil, fmt.Errorf("author source catalog file exceeds encoded size limit")
	}
	var catalog domain.AuthorSourcesV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("author source catalog has trailing JSON content")
	}
	finalized, err := domain.FinalizeAuthorSourcesV1(catalog)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(finalized, catalog) {
		return nil, fmt.Errorf("author source catalog lacks its exact policy or content hashes")
	}
	return &catalog, nil
}

func withAuthorSourcesFileLock(storage *IO, fn func() error) error {
	if err := validateCharacterMemoryPublicationPath(storage, authorSourcesLockPath); err != nil {
		return err
	}
	if err := os.MkdirAll(storage.path("meta"), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(storage.path(authorSourcesLockPath), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

// PrepareCompass validates and materializes without creating files or locks.
// SaveCompass repeats this check under its write transaction after any model
// or tool work, so this returned value is not a reusable write capability.
func (s *OutlineStore) PrepareCompass(compass domain.StoryCompass) (domain.StoryCompass, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.prepareCompassUnlocked(compass)
}

func (s *OutlineStore) prepareCompassUnlocked(compass domain.StoryCompass) (domain.StoryCompass, error) {
	if compass.EndingDirection == "" {
		return compass, fmt.Errorf("ending_direction 不能为空")
	}
	catalog, err := loadAuthorSourcesUnlocked(s.io)
	if err != nil {
		return compass, err
	}
	if catalog == nil {
		if compass.AuthorContracts != nil {
			return compass, fmt.Errorf("source-bound compass requires its host author source catalog")
		}
		if err := s.requireLegacyCompassWithoutAuthorBindingUnlocked(); err != nil {
			return compass, err
		}
		return compass, nil
	}
	prepared, err := domain.MaterializeCompassAuthorContractsV1(compass, *catalog)
	if err != nil {
		return compass, err
	}
	if err := validateCharacterMemoryPublicationPath(s.io, "meta/compass.json"); err != nil {
		return compass, err
	}
	var old domain.StoryCompass
	if err := s.io.ReadJSONUnlocked("meta/compass.json", &old); err != nil {
		if !os.IsNotExist(err) {
			return compass, err
		}
		return prepared, nil
	}
	if old.AuthorContracts != nil {
		// Validate the original reference identities before allowing updates.
		// Only an explicit Save may restore their mechanical text; Load never
		// repairs altered or missing NonNegotiables.
		old.NonNegotiables = nil
		if _, err := domain.MaterializeCompassAuthorContractsV1(old, *catalog); err != nil {
			return compass, err
		}
		refs := map[domain.AuthorSourceParagraphRefV1]bool{}
		for _, ref := range prepared.AuthorContracts.Refs {
			refs[ref] = true
		}
		for _, ref := range old.AuthorContracts.Refs {
			if !refs[ref] {
				return compass, fmt.Errorf("existing author paragraph contracts cannot be removed or replaced")
			}
		}
	}
	return prepared, nil
}

// Absence of the catalog does not prove legacy mode: a previously persisted
// source binding must survive a missing catalog without being overwritten by
// an unbound model payload. Callers already hold the outline IO lock.
func (s *OutlineStore) requireLegacyCompassWithoutAuthorBindingUnlocked() error {
	const path = "meta/compass.json"
	if err := validateCharacterMemoryPublicationPath(s.io, path); err != nil {
		return err
	}
	raw, err := s.io.ReadFileUnlocked(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("existing compass cannot safely establish legacy mode")
	}
	var existing domain.StoryCompass
	if err := json.Unmarshal(raw, &existing); err != nil {
		return fmt.Errorf("existing compass cannot safely establish legacy mode: %w", err)
	}
	if existing.AuthorContracts != nil {
		return fmt.Errorf("existing source-bound compass has lost its author catalog; refusing legacy downgrade")
	}
	if existing.EndingDirection == "" {
		return fmt.Errorf("existing compass cannot safely establish legacy mode without its required ending direction")
	}
	return nil
}

func (s *OutlineStore) validateLoadedCompassAuthorContracts(compass domain.StoryCompass) error {
	catalog, err := loadAuthorSourcesUnlocked(s.io)
	if err != nil {
		return err
	}
	if catalog == nil {
		if compass.AuthorContracts != nil {
			return fmt.Errorf("source-bound compass has no host author source catalog")
		}
		return nil
	}
	materialized, err := domain.MaterializeCompassAuthorContractsV1(compass, *catalog)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(materialized.NonNegotiables, compass.NonNegotiables) {
		return fmt.Errorf("stored compass contracts differ from exact author paragraphs; refusing to repair during read")
	}
	return nil
}

func (s *OutlineStore) savePreparedCompassUnlocked(compass domain.StoryCompass) error {
	prepared, err := s.prepareCompassUnlocked(compass)
	if err != nil {
		return err
	}
	if err := s.io.WriteJSONUnlocked("meta/compass.json", prepared); err != nil {
		return err
	}
	return syncProjectedDirs(filepath.Dir(s.io.path("meta/compass.json")))
}
