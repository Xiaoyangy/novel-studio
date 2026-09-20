package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Negative evidence only: even incomplete/corrupt artifacts cannot authorize
// replacing a lost start. This runs under the caller's live projected read
// lock, so do not call public session/prefix loaders or acquire another Store
// lock. The ordinary production execution lease still serializes dispatch;
// the live metadata lock does not claim to lock another workspace's files.
func (s *Store) requireNoChapterDeliveryExecutionEvidence(generation string, chapter int) error {
	activation, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return err
	}
	for _, root := range []string{activation, characterAgentChapterDir(generation, chapter)} {
		// The path validator expects a file target and verifies every ancestor
		// is a real directory. No probe file is created or read.
		if err := validateCharacterMemoryPublicationPath(s.CharacterAgents.io, filepath.Join(root, "session.json")); err != nil {
			return fmt.Errorf("chapter delivery original start is missing; unsafe prior execution path: %w", err)
		}
		absolute := filepath.Join(s.dir, root)
		if _, err := os.Lstat(absolute); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		err := filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("prior execution path is a symlink")
			}
			if entry.IsDir() {
				return nil
			}
			// The execution wrapper can create this empty lock before it creates
			// a session or dispatches anything. No other file is exempted.
			if root == activation && path == filepath.Join(absolute, "execution.lock") && entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().IsRegular() && info.Size() == 0 {
					return nil
				}
			}
			return fmt.Errorf("prior chapter execution artifact exists at %s", filepath.Base(path))
		})
		if err != nil {
			return fmt.Errorf("chapter delivery original start is missing for chapter %d with prior execution evidence; refusing to reset its clock: %w", chapter, err)
		}
	}
	// Independent character usage has an explicit chapter. Do not infer it
	// from the global accounting-start marker or an old unscoped call_start.
	raw, err := readArcCycleSealedEvidenceFile(s.dir, filepath.Join(characterAgentRoot, "usage.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for i, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var usage domain.CharacterAgentUsage
		if err := json.Unmarshal(line, &usage); err != nil {
			return fmt.Errorf("chapter delivery cannot establish fresh execution: character usage line %d is corrupt: %w", i+1, err)
		}
		if usage.GenerationID == generation && usage.Chapter == chapter {
			return fmt.Errorf("chapter delivery original start is missing for chapter %d with prior usage; refusing to reset its clock", chapter)
		}
	}
	return nil
}
