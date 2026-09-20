package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"time"
)

// SyncChapterDeliveryBudgetForPublish preserves Check's live high-water clock
// in an isolated render candidate. Only LastObserved and its integrity digest
// may differ: starts, deadlines, closures and accepted proof bindings are never
// merged. The original complete directory root proves every other live byte
// stayed unchanged. The returned root must still pass ordinary publication CAS.
// This is not a dispatch check: a response already in flight may finish after
// its deadline; it must not reset the clock or lose its durable result.
func (s *Store) SyncChapterDeliveryBudgetForPublish(candidateDir, expectedLiveRoot string) (string, error) {
	livePath, err := filepath.EvalSymlinks(s.dir)
	if err != nil {
		return "", err
	}
	candidatePath, err := filepath.EvalSymlinks(candidateDir)
	if err != nil {
		return "", err
	}
	if pathsOverlap(livePath, candidatePath) {
		return "", fmt.Errorf("render budget publication requires disjoint live and candidate directories")
	}
	candidate := NewStore(candidateDir)
	liveLedger, err := s.readChapterDeliveryLedger()
	if err != nil {
		return "", err
	}
	copyLedger, err := candidate.readChapterDeliveryLedger()
	if err != nil {
		return "", err
	}
	if liveLedger == nil && copyLedger == nil {
		// Legacy candidates keep the original CAS and create no timing files.
		return expectedLiveRoot, nil
	}
	if liveLedger == nil || copyLedger == nil {
		return "", fmt.Errorf("render budget publication requires both original ledgers")
	}
	var result string
	first, second := s, candidate
	if candidatePath < livePath {
		first, second = candidate, s
	}
	err = first.withChapterDeliveryLock(func() error {
		return second.withChapterDeliveryLock(func() error {
			live, err := s.readChapterDeliveryLedger()
			if err != nil {
				return err
			}
			baseline, err := candidate.readChapterDeliveryLedger()
			if err != nil {
				return err
			}
			if live == nil || baseline == nil || live.Version != baseline.Version ||
				!reflect.DeepEqual(live.Generations, baseline.Generations) ||
				live.LastObserved.Before(baseline.LastObserved) || live.LastObserved.After(time.Now()) {
				return fmt.Errorf("render budget publication permits only forward host observation, not timing or proof changes")
			}
			// Reauthenticate stored generations and any already closed proof files;
			// discovering an unrecorded acceptance is not a high-water-only change.
			if err := s.requireArmedChapterDeliveryGenerations(live, ""); err != nil {
				return err
			}
			if err := s.recoverChapterDeliveryLedger(live, live.LastObserved); err != nil {
				return err
			}
			if !reflect.DeepEqual(live.Generations, baseline.Generations) {
				return fmt.Errorf("render budget publication cannot recover or replace acceptance evidence")
			}
			before, err := DirectoryContentRoot(s.dir)
			if err != nil {
				return err
			}
			if before != expectedLiveRoot {
				oldRaw, err := readArcCycleSealedEvidenceFile(candidate.dir, chapterDeliveryLedgerPath)
				if err != nil {
					return err
				}
				originalRoot, err := directoryContentRootOnceWithFileOverride(s.dir, chapterDeliveryLedgerPath, oldRaw)
				if err != nil {
					return err
				}
				if originalRoot != expectedLiveRoot {
					return fmt.Errorf("live directory changed outside the permitted chapter delivery observation")
				}
			}
			raw, err := readArcCycleSealedEvidenceFile(s.dir, chapterDeliveryLedgerPath)
			if err != nil {
				return err
			}
			validatedRaw, err := json.MarshalIndent(live, "", "  ")
			if err != nil {
				return err
			}
			if !bytes.Equal(bytes.TrimSpace(raw), validatedRaw) {
				return fmt.Errorf("chapter delivery ledger changed after validation")
			}
			validatedRoot, err := directoryContentRootOnceWithFileOverride(s.dir, chapterDeliveryLedgerPath, raw)
			if err != nil {
				return err
			}
			after, err := DirectoryContentRoot(s.dir)
			if err != nil {
				return err
			}
			if before != after || validatedRoot != after {
				return fmt.Errorf("live directory changed during budget publication synchronization")
			}
			// IO's atomic writer preserves the exact authenticated bytes; no model
			// receipt, candidate content or live file is rewritten here.
			if err := newIO(candidate.dir).WriteFileUnlocked(chapterDeliveryLedgerPath, raw); err != nil {
				return err
			}
			if err := syncProjectedDirs(filepath.Join(candidate.dir, chapterDeliveryRoot)); err != nil {
				return err
			}
			result = after
			return nil
		})
	})
	return result, err
}
