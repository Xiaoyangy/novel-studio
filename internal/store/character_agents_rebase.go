package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// ResetForChapterZeroRebase clears rejected-story state only inside an explicit
// .canon-rebase candidate backed by a verified archive. Stable identities and
// usage remain; ordinary resume/canon migration must never call this method.
// A retry may contain a subset of archived runtime files after interruption,
// but may not contain any new or changed unarchived content.
func (s *CharacterAgentStore) ResetForChapterZeroRebase(archiveOutput string) error {
	root := s.io.path(characterAgentRoot)
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := DirectoryContentRoot(root); err != nil {
		return fmt.Errorf("verify candidate character-agent tree: %w", err)
	}
	hasState := false
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && path != filepath.Join(root, "usage.jsonl") {
			hasState = true
		}
		return nil
	}); err != nil {
		return err
	}
	// Store.Init creates empty memory/projected directories even for legacy
	// books that never registered an Agent. There is no story state to reset.
	if !hasState {
		return nil
	}
	candidate, err := filepath.EvalSymlinks(s.io.dir)
	if err != nil {
		return err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return err
	}
	insideCandidate := false
	for path := candidate; filepath.Dir(path) != path; path = filepath.Dir(path) {
		if filepath.Base(path) == ".canon-rebase" {
			insideCandidate = true
			break
		}
	}
	if !insideCandidate || strings.TrimSpace(archiveOutput) == "" {
		return fmt.Errorf("character-agent chapter-zero reset requires an archived .canon-rebase candidate")
	}
	archive, err := filepath.EvalSymlinks(archiveOutput)
	if err != nil {
		return fmt.Errorf("character-agent reset archive is unavailable: %w", err)
	}
	archive, err = filepath.Abs(archive)
	if err != nil {
		return err
	}
	if archive == candidate || strings.HasPrefix(archive, candidate+string(filepath.Separator)) || strings.HasPrefix(candidate, archive+string(filepath.Separator)) {
		return fmt.Errorf("character-agent reset archive must be independent of the candidate")
	}
	archiveRoot := filepath.Join(archive, characterAgentRoot)
	archiveDigest, err := DirectoryContentRoot(archiveRoot)
	if err != nil {
		return fmt.Errorf("verify archived character-agent state: %w", err)
	}
	archivedRegistry, err := NewStore(archive).CharacterAgents.LoadRegistry()
	if err != nil || archivedRegistry == nil {
		return fmt.Errorf("character-agent reset requires a verified archived registry: %v", err)
	}
	reset := *archivedRegistry
	reset.Entries = append([]domain.CharacterAgentRecord(nil), archivedRegistry.Entries...)
	for i := range reset.Entries {
		reset.Entries[i].Status = domain.CharacterAgentSleeping
		reset.Entries[i].FirstRegisteredChapter = 0
		reset.Entries[i].LastActivatedChapter = 0
		reset.Entries[i].MemoryVersion = 0
		reset.Entries[i].UpdatedAt = "rebase:chapter-000000"
	}
	reset, err = domain.FinalizeCharacterAgentRegistry(reset)
	if err != nil {
		return err
	}
	resetJSON, err := json.Marshal(reset)
	if err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		if _, err := DirectoryContentRoot(root); err != nil {
			return fmt.Errorf("verify candidate character-agent state: %w", err)
		}
		// Verify all surviving bytes before the first mutation. This also
		// makes interrupted/repeated candidate resets safely idempotent.
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == "." {
				return err
			}
			part := strings.Split(filepath.ToSlash(rel), "/")[0]
			switch part {
			case "registry.json", "usage.jsonl", "memory", "projected", "successors":
			default:
				return fmt.Errorf("unrecognized character-agent rebase artifact %s; no reset performed", rel)
			}
			if entry.IsDir() {
				return nil
			}
			current, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			archived, err := os.ReadFile(filepath.Join(archiveRoot, rel))
			if err != nil {
				return fmt.Errorf("character-agent candidate %s has no archived copy: %w", rel, err)
			}
			if bytes.Equal(current, archived) || (rel == "registry.json" && sameJSON(current, resetJSON)) {
				return nil
			}
			return fmt.Errorf("character-agent candidate %s differs from its archive; no reset performed", rel)
		}); err != nil {
			return err
		}
		for _, name := range []string{"memory", "projected", "successors"} {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return fmt.Errorf("clear candidate character-agent %s: %w", name, err)
			}
		}
		if err := s.io.WriteJSONUnlocked(characterAgentRegistryPath(), reset); err != nil {
			return err
		}
		if after, err := DirectoryContentRoot(archiveRoot); err != nil || after != archiveDigest {
			return fmt.Errorf("character-agent archive changed during candidate reset: %v", err)
		}
		return nil
	})
}
