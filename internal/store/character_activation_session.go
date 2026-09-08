package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// This execution lock is separate from the short metadata write lock. Holding
// it during inference prevents two runners from spending money on the same
// missing cycle, while readers can still inspect the durable session cursor.
func (s *Store) WithCharacterActivationExecution(ctx context.Context, generation string, chapter int, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return err
	}
	rel := filepath.Join(root, "execution.lock")
	if err := validateCharacterMemoryPublicationPath(s.CharacterAgents.io, rel); err != nil {
		return err
	}
	if err := os.MkdirAll(s.CharacterAgents.io.path(root), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.CharacterAgents.io.path(rel), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("chapter activation execution is already owned: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return err
	}
	return run()
}

func characterActivationSessionDir(generation string, chapter int) (string, error) {
	if err := validateCharacterAgentPathComponent("activation generation", generation); err != nil {
		return "", err
	}
	if chapter <= 0 {
		return "", fmt.Errorf("activation session chapter must be positive")
	}
	return filepath.Join(characterAgentRoot, "activation_sessions", generation, fmt.Sprintf("%06d", chapter)), nil
}

func activationCyclePath(root string, index int) string {
	return filepath.Join(root, "cycles", fmt.Sprintf("%06d.json", index))
}
func activationReadinessPath(root string, index int) string {
	return filepath.Join(root, "readiness", fmt.Sprintf("%06d.json", index))
}

func (s *Store) withCharacterActivationWrite(fn func() error) error {
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return err
	}
	return p.withProjectedWriteLock(fn)
}

func (s *Store) readCharacterActivationJSON(path string, value any) error {
	if err := validateCharacterMemoryPublicationPath(s.CharacterAgents.io, path); err != nil {
		return err
	}
	return s.CharacterAgents.io.ReadJSON(path, value)
}

func (s *Store) writeCharacterActivationJSON(path string, value any, immutable bool) error {
	if err := validateCharacterMemoryPublicationPath(s.CharacterAgents.io, path); err != nil {
		return err
	}
	if immutable {
		return s.CharacterAgents.writeImmutable(path, value)
	}
	return s.CharacterAgents.io.WriteJSON(path, value)
}

func (s *Store) CreateCharacterActivationSession(session domain.CharacterActivationSession) error {
	if err := domain.ValidateCharacterActivationSession(session); err != nil {
		return err
	}
	if len(session.CycleDigests) != 0 || session.Phase != "collecting" || session.CurrentPhysicalRoot != session.InitialPhysicalRoot || session.CurrentDay != session.InitialDay {
		return fmt.Errorf("new activation session must start from its exact empty baseline")
	}
	root, err := characterActivationSessionDir(session.GenerationID, session.Chapter)
	if err != nil {
		return err
	}
	return s.withCharacterActivationWrite(func() error {
		if err := s.writeCharacterActivationJSON(activationBaselinePath(root), session, true); err != nil {
			return err
		}
		return s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), session, true)
	})
}

func (s *Store) LoadCharacterActivationSession(generation string, chapter int) (*domain.CharacterActivationSession, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s.ProjectedV2(), func() (*domain.CharacterActivationSession, error) {
		return s.loadCharacterActivationSession(root, generation, chapter)
	})
}

func (s *Store) loadCharacterActivationSession(root, generation string, chapter int) (*domain.CharacterActivationSession, error) {
	var session domain.CharacterActivationSession
	if err := s.readCharacterActivationJSON(filepath.Join(root, "session.json"), &session); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if session.GenerationID != generation || session.Chapter != chapter {
		return nil, fmt.Errorf("activation session path binding mismatch")
	}
	if err := domain.ValidateCharacterActivationSession(session); err != nil {
		return nil, err
	}
	if verified, err := s.hasVerifiedActivationCycles(root, session.GenerationID, session.Chapter, len(session.CycleDigests)); err != nil {
		return nil, err
	} else if verified {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
		if err != nil || prefix == nil {
			return nil, err
		}
		value := prefix.Session()
		return &value, nil
	}
	// Rebuild the cursor from exact immutable evidence, never trust a re-signed
	// manifest claiming a future state or ready flag on its own.
	rebuilt := session
	rebuilt.CycleDigests = nil
	rebuilt.ReadinessDigests = nil
	rebuilt.Phase = "collecting"
	rebuilt.CurrentPhysicalRoot = session.InitialPhysicalRoot
	rebuilt.CurrentDay = session.InitialDay
	rebuilt.PendingHardConflict = false
	// The empty baseline digest is deterministically reconstructed through the
	// first cycle's real pre-state (or the stored empty session when no cycle).
	if len(session.CycleDigests) > 0 {
		first, err := s.loadCharacterActivationCycle(root, generation, chapter, 1)
		if err != nil || first == nil {
			return nil, fmt.Errorf("activation session missing first cycle: %w", err)
		}
		rebuilt, err = domain.NewCharacterActivationSession(generation, chapter, session.ChapterContextDigest, *first.Evidence.Stimulus.PhysicalState, session.InitialDay, session.MaxCycles)
		if err != nil {
			return nil, err
		}
		if rebuilt.InitialPhysicalRoot != session.InitialPhysicalRoot {
			return nil, fmt.Errorf("activation session baseline differs from first cycle")
		}
	}
	for i, digest := range session.CycleDigests {
		cycle, err := s.loadCharacterActivationCycle(root, generation, chapter, i+1)
		if err != nil || cycle == nil {
			return nil, fmt.Errorf("activation session missing cycle %d: %w", i+1, err)
		}
		if cycle.Digest != digest {
			return nil, fmt.Errorf("activation session cycle link mismatch")
		}
		rebuilt, err = domain.AppendCharacterActivationCycle(rebuilt, *cycle)
		if err != nil {
			return nil, err
		}
		if i < len(session.ReadinessDigests) {
			readiness, err := s.loadCharacterChapterReadiness(root, generation, chapter, i+1)
			if err != nil || readiness == nil {
				return nil, fmt.Errorf("activation session missing assessment: %w", err)
			}
			if readiness.Digest != session.ReadinessDigests[i] {
				return nil, fmt.Errorf("activation session assessment link mismatch")
			}
			rebuilt, err = domain.ApplyCharacterChapterReadiness(rebuilt, *readiness)
			if err != nil {
				return nil, err
			}
		}
	}
	if rebuilt.Digest != session.Digest {
		return nil, fmt.Errorf("activation session cursor disagrees with immutable cycle chain")
	}
	return &session, nil
}

func (s *Store) loadCharacterActivationCycle(root, generation string, chapter, index int) (*domain.CharacterActivationCycle, error) {
	var cycle domain.CharacterActivationCycle
	if err := s.readCharacterActivationJSON(activationCyclePath(root, index), &cycle); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if cycle.GenerationID != generation || cycle.Chapter != chapter || cycle.Index != index {
		return nil, fmt.Errorf("activation cycle path identity mismatch")
	}
	if cycle.Version == domain.CharacterActivationCycleV2Version || cycle.Version == domain.CharacterActivationCycleV3Version {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
		if err != nil {
			return nil, err
		}
		if prefix == nil {
			return nil, fmt.Errorf("v2 activation cycle is not part of the verified committed prefix")
		}
		step, exists := prefix.Step(index - 1)
		if !exists {
			return nil, fmt.Errorf("v2 activation cycle is not part of the verified committed prefix")
		}
		verified := step.Cycle()
		if !jsonValuesEqual(cycle, verified) {
			return nil, fmt.Errorf("activation cycle differs from verified source step")
		}
		return &verified, nil
	}
	if err := domain.ValidateCharacterActivationCycle(cycle); err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (s *Store) LoadCharacterActivationCycle(generation string, chapter, index int) (*domain.CharacterActivationCycle, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	if index <= 0 {
		return nil, fmt.Errorf("activation cycle index must be positive")
	}
	if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s.ProjectedV2(), func() (*domain.CharacterActivationCycle, error) {
		return s.loadCharacterActivationCycle(root, generation, chapter, index)
	})
}

func (s *Store) loadCharacterChapterReadiness(root, generation string, chapter, index int) (*domain.CharacterChapterReadiness, error) {
	var readiness domain.CharacterChapterReadiness
	if err := s.readCharacterActivationJSON(activationReadinessPath(root, index), &readiness); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	want, err := domain.FinalizeCharacterChapterReadiness(readiness)
	if err != nil {
		return nil, err
	}
	if readiness.GenerationID != generation || readiness.Chapter != chapter || readiness.Digest != want.Digest {
		return nil, fmt.Errorf("activation assessment identity/digest mismatch")
	}
	if readiness.Version == domain.CharacterReadinessReviewedVersion {
		audit, err := s.loadCharacterReadinessReviewAudit(root, generation, chapter, index)
		if err != nil {
			return nil, err
		}
		if audit == nil || audit.Receipt.Digest != readiness.Digest {
			return nil, fmt.Errorf("reviewed readiness is missing its full input/verdict audit")
		}
	}
	return &readiness, nil
}

func (s *Store) AppendCharacterActivationCycle(expected string, cycle domain.CharacterActivationCycle) (*domain.CharacterActivationSession, error) {
	if cycle.Version == domain.CharacterActivationCycleV2Version || cycle.Version == domain.CharacterActivationCycleV3Version {
		return s.AppendVerifiedCharacterActivationCycle(expected, cycle)
	}
	if err := domain.ValidateCharacterActivationCycle(cycle); err != nil {
		return nil, err
	}
	root, err := characterActivationSessionDir(cycle.GenerationID, cycle.Chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		current, err := s.loadCharacterActivationSession(root, cycle.GenerationID, cycle.Chapter)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("activation session missing")
		}
		if cycle.Index <= len(current.CycleDigests) {
			if current.CycleDigests[cycle.Index-1] != cycle.Digest {
				return fmt.Errorf("immutable activation cycle conflict")
			}
			result = current
			return nil
		}
		if current.Digest != expected {
			return fmt.Errorf("activation session compare-and-swap conflict")
		}
		next, err := domain.AppendCharacterActivationCycle(*current, cycle)
		if err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(activationCyclePath(root, cycle.Index), cycle, true); err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), next, false); err != nil {
			return err
		}
		result = &next
		return nil
	})
	return result, err
}

func (s *Store) ApplyCharacterChapterReadiness(expected string, readiness domain.CharacterChapterReadiness) (*domain.CharacterActivationSession, error) {
	want, err := domain.FinalizeCharacterChapterReadiness(readiness)
	if err != nil {
		return nil, err
	}
	if readiness.Digest != want.Digest {
		return nil, fmt.Errorf("chapter readiness digest mismatch")
	}
	root, err := characterActivationSessionDir(readiness.GenerationID, readiness.Chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		current, err := s.loadCharacterActivationSession(root, readiness.GenerationID, readiness.Chapter)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("activation session missing")
		}
		if verified, err := s.hasVerifiedActivationCycles(root, readiness.GenerationID, readiness.Chapter, len(current.CycleDigests)); err != nil {
			return err
		} else if verified {
			result, err = s.applyVerifiedCharacterChapterReadiness(root, expected, readiness)
			return err
		}
		for i, digest := range current.ReadinessDigests {
			if digest == readiness.Digest && current.CycleDigests[i] == readiness.CycleDigest {
				result = current
				return nil
			}
		}
		if current.Digest != expected {
			return fmt.Errorf("activation readiness compare-and-swap conflict")
		}
		if readiness.Version == domain.CharacterReadinessReviewedVersion {
			audit, err := s.loadCharacterReadinessReviewAudit(root, readiness.GenerationID, readiness.Chapter, len(current.CycleDigests))
			if err != nil {
				return err
			}
			if audit == nil || audit.Receipt.Digest != readiness.Digest || audit.Input.SessionDigest != current.Digest {
				return fmt.Errorf("reviewed readiness lacks its exact pending-session input audit")
			}
		}
		next, err := domain.ApplyCharacterChapterReadiness(*current, readiness)
		if err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(activationReadinessPath(root, len(current.CycleDigests)), readiness, true); err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), next, false); err != nil {
			return err
		}
		result = &next
		return nil
	})
	return result, err
}

// Recover an immutable-result-before-cursor crash without invoking an Agent or
// re-signing any result. Recovery is bounded by the declared cycle limit.
func (s *Store) RecoverCharacterActivationSession(generation string, chapter int) (*domain.CharacterActivationSession, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		current, err := s.loadCharacterActivationSession(root, generation, chapter)
		if err != nil || current == nil {
			return err
		}
		if verified, err := s.hasVerifiedActivationCycles(root, generation, chapter, len(current.CycleDigests)+1); err != nil {
			return err
		} else if verified {
			result, err = s.recoverVerifiedCharacterActivationSession(root, generation, chapter)
			return err
		}
		for step := 0; step < 2*current.MaxCycles; step++ {
			var next domain.CharacterActivationSession
			if current.Phase == "collecting" {
				cycle, err := s.loadCharacterActivationCycle(root, generation, chapter, len(current.CycleDigests)+1)
				if err != nil {
					return err
				}
				if cycle == nil {
					break
				}
				next, err = domain.AppendCharacterActivationCycle(*current, *cycle)
				if err != nil {
					return err
				}
			} else if current.Phase == "assessing" {
				readiness, err := s.loadCharacterChapterReadiness(root, generation, chapter, len(current.CycleDigests))
				if err != nil {
					return err
				}
				if readiness == nil {
					break
				}
				next, err = domain.ApplyCharacterChapterReadiness(*current, *readiness)
				if err != nil {
					return err
				}
			} else {
				break
			}
			if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), next, false); err != nil {
				return err
			}
			current = &next
		}
		result = current
		return nil
	})
	return result, err
}
