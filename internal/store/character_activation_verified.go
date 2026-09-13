package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func activationBaselinePath(root string) string { return filepath.Join(root, "baseline.json") }
func activationFrozenInputPath(root string, index int) string {
	return filepath.Join(root, "work", fmt.Sprintf("%06d", index), "proof", "inputs.json")
}

func (s *Store) hasVerifiedActivationCycles(root, generation string, chapter, count int) (bool, error) {
	// Includes at most one uncommitted next-cycle slot during recovery.
	if count < 0 || count > 65 {
		return false, fmt.Errorf("activation source scan exceeds its bounded cycle limit")
	}
	for i := 1; i <= count; i++ {
		cycle, err := s.readVerifiedActivationCycle(root, generation, chapter, i)
		if err != nil {
			return false, err
		}
		if cycle != nil && (cycle.Version == domain.CharacterActivationCycleV2Version || cycle.Version == domain.CharacterActivationCycleV3Version) {
			return true, nil
		}
	}
	return false, nil
}

// LoadVerifiedCharacterActivationPrefix rebuilds runtime authority from the
// immutable empty baseline and flat stored evidence. It never advances a cursor
// or treats an uncommitted cycle as accepted history.
func (s *Store) LoadVerifiedCharacterActivationPrefix(generation string, chapter int) (*domain.VerifiedCharacterActivationPrefix, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s.ProjectedV2(), func() (*domain.VerifiedCharacterActivationPrefix, error) {
		return s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
	})
}

// Caller owns the projected metadata lock. Do not call public locked loaders
// from this helper: arbitration publication uses the same transaction.
func (s *Store) loadVerifiedCharacterActivationPrefix(root, generation string, chapter int) (*domain.VerifiedCharacterActivationPrefix, error) {
	var cursor domain.CharacterActivationSession
	if err := s.readCharacterActivationJSON(filepath.Join(root, "session.json"), &cursor); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if cursor.GenerationID != generation || cursor.Chapter != chapter {
		return nil, fmt.Errorf("verified activation cursor path mismatch")
	}
	if err := domain.ValidateCharacterActivationSession(cursor); err != nil {
		return nil, err
	}
	prefix, err := s.rebuildVerifiedCharacterActivationPrefix(root, cursor, len(cursor.CycleDigests), len(cursor.ReadinessDigests))
	if err != nil {
		return nil, err
	}
	if prefix.Session().Digest != cursor.Digest {
		return nil, fmt.Errorf("verified activation cursor disagrees with immutable source chain")
	}
	return &prefix, nil
}

func (s *Store) rebuildVerifiedCharacterActivationPrefix(root string, cursor domain.CharacterActivationSession, cycleCount, readinessCount int) (domain.VerifiedCharacterActivationPrefix, error) {
	var zero domain.VerifiedCharacterActivationPrefix
	if cycleCount < 0 || cycleCount > len(cursor.CycleDigests) || readinessCount < 0 || readinessCount > len(cursor.ReadinessDigests) || readinessCount > cycleCount || cycleCount-readinessCount > 1 {
		return zero, fmt.Errorf("invalid verified activation prefix bounds")
	}
	var baseline domain.CharacterActivationSession
	if err := s.readCharacterActivationJSON(activationBaselinePath(root), &baseline); err != nil {
		return zero, fmt.Errorf("verified activation requires its immutable empty baseline: %w", err)
	}
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(baseline)
	if err != nil {
		return zero, err
	}
	if baseline.GenerationID != cursor.GenerationID || baseline.Chapter != cursor.Chapter || baseline.ChapterContextDigest != cursor.ChapterContextDigest || baseline.InitialPhysicalRoot != cursor.InitialPhysicalRoot || baseline.InitialDay != cursor.InitialDay || baseline.MaxCycles != cursor.MaxCycles {
		return zero, fmt.Errorf("verified activation cursor changed its immutable baseline")
	}
	for i := 1; i <= cycleCount; i++ {
		cycle, err := s.readVerifiedActivationCycle(root, cursor.GenerationID, cursor.Chapter, i)
		if err != nil {
			return zero, err
		}
		if cycle == nil || cycle.Digest != cursor.CycleDigests[i-1] {
			return zero, fmt.Errorf("verified activation cycle %d is missing or has a different root", i)
		}
		input, err := s.loadVerifiedActivationInput(root, cursor.GenerationID, cursor.Chapter, i)
		if err != nil {
			return zero, err
		}
		if err := s.validateVerifiedActivationCycleProofs(prefix, *input, *cycle); err != nil {
			return zero, err
		}
		_, next, err := domain.VerifyCharacterActivationStep(prefix, *input, *cycle)
		if err != nil {
			return zero, fmt.Errorf("verify activation step %d: %w", i, err)
		}
		prefix = next
		if i <= readinessCount {
			readiness, err := s.loadVerifiedActivationReadiness(root, prefix)
			if err != nil {
				return zero, err
			}
			if readiness == nil || readiness.Digest != cursor.ReadinessDigests[i-1] {
				return zero, fmt.Errorf("verified activation readiness %d is missing or has a different root", i)
			}
			prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, *readiness)
			if err != nil {
				return zero, err
			}
		}
	}
	return prefix, nil
}

// Raw identity-checked reads are private. Only VerifyCharacterActivationStep
// turns these files into an authoritative cycle; no bare v2 validator exists.
func (s *Store) readVerifiedActivationCycle(root, generation string, chapter, index int) (*domain.CharacterActivationCycle, error) {
	var cycle domain.CharacterActivationCycle
	if index <= 0 {
		return nil, fmt.Errorf("activation cycle index must be positive")
	}
	if err := s.readCharacterActivationJSON(activationCyclePath(root, index), &cycle); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if cycle.GenerationID != generation || cycle.Chapter != chapter || cycle.Index != index {
		return nil, fmt.Errorf("activation cycle path identity mismatch")
	}
	return &cycle, nil
}

func (s *Store) loadVerifiedActivationInput(root, generation string, chapter, index int) (*domain.CharacterActivationInputSet, error) {
	if index <= 0 || index > 64 {
		return nil, fmt.Errorf("activation frozen input index is outside the cycle limit")
	}
	var input domain.CharacterActivationInputSet
	if err := s.readCharacterActivationJSON(activationFrozenInputPath(root, index), &input); err != nil {
		return nil, fmt.Errorf("activation step %d requires its frozen input: %w", index, err)
	}
	if err := domain.ValidateCharacterActivationInputSet(input); err != nil {
		return nil, err
	}
	if input.Stimulus.GenerationID != generation || input.Stimulus.Chapter != chapter {
		return nil, fmt.Errorf("frozen activation input path mismatch")
	}
	return &input, nil
}

func (s *Store) validateVerifiedReadinessAudit(root string, prefix domain.VerifiedCharacterActivationPrefix, audit domain.CharacterReadinessReviewAudit) error {
	if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
		return err
	}
	session := prefix.Session()
	var context domain.CharacterReadinessContext
	if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), &context); err != nil {
		return fmt.Errorf("verified readiness requires frozen chapter context: %w", err)
	}
	expected, err := domain.NewCharacterReadinessReviewInputFromSteps(context, session, prefix.Steps(), audit.Input.ReviewProtocol)
	if err != nil {
		return err
	}
	want, err := domain.CharacterReadinessReviewInputDigest(expected)
	if err != nil {
		return err
	}
	got, err := domain.CharacterReadinessReviewInputDigest(audit.Input)
	if err != nil {
		return err
	}
	if want != got {
		return fmt.Errorf("verified readiness audit differs from actual frozen context, session or source steps")
	}
	return nil
}

func (s *Store) loadVerifiedReadinessAudit(root string, prefix domain.VerifiedCharacterActivationPrefix) (*domain.CharacterReadinessReviewAudit, error) {
	var audit domain.CharacterReadinessReviewAudit
	index := len(prefix.Session().CycleDigests)
	if err := s.readCharacterActivationJSON(characterReadinessAuditPath(root, index), &audit); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := s.validateVerifiedReadinessAudit(root, prefix, audit); err != nil {
		return nil, err
	}
	return &audit, nil
}

func (s *Store) loadVerifiedReadinessAuditAt(root, generation string, chapter, index int) (*domain.CharacterReadinessReviewAudit, error) {
	current, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("verified readiness audit has no source session")
	}
	session := current.Session()
	prefix := *current
	// The current cursor was fully source-verified above. When it is already
	// this exact assessing boundary, replaying the same prefix adds no proof.
	if len(session.CycleDigests) != index || len(session.ReadinessDigests) != index-1 {
		prefix, err = s.rebuildVerifiedCharacterActivationPrefix(root, session, index, index-1)
		if err != nil {
			return nil, err
		}
	}
	return s.loadVerifiedReadinessAudit(root, prefix)
}

func (s *Store) SaveVerifiedCharacterReadinessReviewAudit(audit domain.CharacterReadinessReviewAudit) error {
	root, err := characterActivationSessionDir(audit.Receipt.GenerationID, audit.Receipt.Chapter)
	if err != nil {
		return err
	}
	return s.withCharacterActivationWrite(func() error { return s.saveVerifiedCharacterReadinessReviewAudit(root, audit) })
}

func (s *Store) saveVerifiedCharacterReadinessReviewAudit(root string, audit domain.CharacterReadinessReviewAudit) error {
	prefix, err := s.loadVerifiedCharacterActivationPrefix(root, audit.Receipt.GenerationID, audit.Receipt.Chapter)
	if err != nil {
		return err
	}
	if prefix == nil {
		return fmt.Errorf("verified readiness audit has no source session")
	}
	index := len(audit.Input.Trace.Cycles)
	// A retry after application still checks the original assessing boundary,
	// not the later mutable cursor or a newly fabricated review input.
	session := prefix.Session()
	boundary := *prefix
	if len(session.CycleDigests) != index || len(session.ReadinessDigests) != index-1 {
		boundary, err = s.rebuildVerifiedCharacterActivationPrefix(root, session, index, index-1)
		if err != nil {
			return err
		}
	}
	if err := s.validateVerifiedReadinessAudit(root, boundary, audit); err != nil {
		return err
	}
	return s.writeCharacterActivationJSON(characterReadinessAuditPath(root, index), audit, true)
}

func (s *Store) ApplyVerifiedCharacterChapterReadiness(expected string, readiness domain.CharacterChapterReadiness) (*domain.CharacterActivationSession, error) {
	root, err := characterActivationSessionDir(readiness.GenerationID, readiness.Chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		var err error
		result, err = s.applyVerifiedCharacterChapterReadiness(root, expected, readiness)
		return err
	})
	return result, err
}

func (s *Store) applyVerifiedCharacterChapterReadiness(root, expected string, readiness domain.CharacterChapterReadiness) (*domain.CharacterActivationSession, error) {
	prefix, err := s.loadVerifiedCharacterActivationPrefix(root, readiness.GenerationID, readiness.Chapter)
	if err != nil {
		return nil, err
	}
	if prefix == nil {
		return nil, fmt.Errorf("verified activation session missing")
	}
	current := prefix.Session()
	for i, digest := range current.ReadinessDigests {
		if digest == readiness.Digest && current.CycleDigests[i] == readiness.CycleDigest {
			var stored domain.CharacterChapterReadiness
			if err := s.readCharacterActivationJSON(activationReadinessPath(root, i+1), &stored); err != nil {
				return nil, err
			}
			if !jsonValuesEqual(stored, readiness) {
				return nil, fmt.Errorf("immutable verified readiness conflict")
			}
			return &current, nil
		}
	}
	if current.Digest != expected {
		return nil, fmt.Errorf("verified readiness compare-and-swap conflict")
	}
	audit, err := s.loadVerifiedReadinessAudit(root, *prefix)
	if err != nil {
		return nil, err
	}
	if audit == nil || !jsonValuesEqual(audit.Receipt, readiness) {
		return nil, fmt.Errorf("verified readiness lacks its exact durable audit")
	}
	next, err := domain.ApplyVerifiedCharacterActivationReadiness(*prefix, readiness)
	if err != nil {
		return nil, err
	}
	if err := s.writeCharacterActivationJSON(activationReadinessPath(root, len(current.CycleDigests)), readiness, true); err != nil {
		return nil, err
	}
	session := next.Session()
	if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), session, false); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Store) loadVerifiedActivationReadiness(root string, prefix domain.VerifiedCharacterActivationPrefix) (*domain.CharacterChapterReadiness, error) {
	var receipt domain.CharacterChapterReadiness
	if err := s.readCharacterActivationJSON(activationReadinessPath(root, len(prefix.Session().CycleDigests)), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	audit, err := s.loadVerifiedReadinessAudit(root, prefix)
	if err != nil {
		return nil, err
	}
	if audit == nil || audit.Receipt.Digest != receipt.Digest || !jsonValuesEqual(audit.Receipt, receipt) {
		return nil, fmt.Errorf("verified readiness requires its exact durable review audit")
	}
	return &receipt, nil
}

func (s *Store) AppendVerifiedCharacterActivationCycle(expected string, cycle domain.CharacterActivationCycle) (*domain.CharacterActivationSession, error) {
	root, err := characterActivationSessionDir(cycle.GenerationID, cycle.Chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, cycle.GenerationID, cycle.Chapter)
		if err != nil {
			return err
		}
		if prefix == nil {
			return fmt.Errorf("verified activation session missing")
		}
		current := prefix.Session()
		if cycle.Index > 0 && cycle.Index <= len(current.CycleDigests) {
			stored := prefix.Steps()[cycle.Index-1].Cycle()
			if !jsonValuesEqual(stored, cycle) {
				return fmt.Errorf("immutable verified activation cycle conflict")
			}
			result = &current
			return nil
		}
		if current.Digest != expected {
			return fmt.Errorf("verified activation compare-and-swap conflict")
		}
		input, err := s.loadVerifiedActivationInput(root, cycle.GenerationID, cycle.Chapter, cycle.Index)
		if err != nil {
			return err
		}
		if err := s.validateVerifiedActivationCycleProofs(*prefix, *input, cycle); err != nil {
			return err
		}
		_, next, err := domain.VerifyCharacterActivationStep(*prefix, *input, cycle)
		if err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(activationCyclePath(root, cycle.Index), cycle, true); err != nil {
			return err
		}
		session := next.Session()
		if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), session, false); err != nil {
			return err
		}
		result = &session
		return nil
	})
	return result, err
}

func (s *Store) RecoverVerifiedCharacterActivationSession(generation string, chapter int) (*domain.CharacterActivationSession, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		var err error
		result, err = s.recoverVerifiedCharacterActivationSession(root, generation, chapter)
		return err
	})
	return result, err
}

func (s *Store) recoverVerifiedCharacterActivationSession(root, generation string, chapter int) (*domain.CharacterActivationSession, error) {
	prefix, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
	if err != nil || prefix == nil {
		return nil, err
	}
	for n := 0; n < 2*prefix.Session().MaxCycles; n++ {
		current := prefix.Session()
		var next domain.VerifiedCharacterActivationPrefix
		switch current.Phase {
		case "collecting":
			cycle, err := s.readVerifiedActivationCycle(root, generation, chapter, len(current.CycleDigests)+1)
			if err != nil {
				return nil, err
			}
			if cycle == nil {
				return &current, nil
			}
			input, err := s.loadVerifiedActivationInput(root, generation, chapter, cycle.Index)
			if err != nil {
				return nil, err
			}
			if err := s.validateVerifiedActivationCycleProofs(*prefix, *input, *cycle); err != nil {
				return nil, err
			}
			_, next, err = domain.VerifyCharacterActivationStep(*prefix, *input, *cycle)
			if err != nil {
				return nil, err
			}
		case "assessing":
			readiness, err := s.loadVerifiedActivationReadiness(root, *prefix)
			if err != nil {
				return nil, err
			}
			if readiness == nil {
				return &current, nil
			}
			next, err = domain.ApplyVerifiedCharacterActivationReadiness(*prefix, *readiness)
			if err != nil {
				return nil, err
			}
		default:
			return &current, nil
		}
		session := next.Session()
		if err := s.writeCharacterActivationJSON(filepath.Join(root, "session.json"), session, false); err != nil {
			return nil, err
		}
		prefix = &next
	}
	session := prefix.Session()
	return &session, nil
}

func (s *Store) collectVerifiedCharacterActivationChapterEvidence(root, generation string, chapter int) (*domain.CharacterActivationChapterEvidence, error) {
	var result *domain.CharacterActivationChapterEvidence
	err := s.withCharacterActivationWrite(func() error {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, generation, chapter)
		if err != nil {
			return err
		}
		if prefix == nil || prefix.Session().Phase != "ready" {
			return fmt.Errorf("verified activation chapter is not ready")
		}
		var context domain.CharacterReadinessContext
		if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), &context); err != nil {
			return err
		}
		value := domain.CharacterActivationChapterEvidence{Context: context, Session: prefix.Session()}
		for i, step := range prefix.Steps() {
			var audit domain.CharacterReadinessReviewAudit
			if err := s.readCharacterActivationJSON(characterReadinessAuditPath(root, i+1), &audit); err != nil {
				return err
			}
			value.Cycles = append(value.Cycles, step.Cycle())
			value.Inputs = append(value.Inputs, step.Input())
			value.Reviews = append(value.Reviews, audit)
		}
		value.ProtocolDigest = value.Cycles[0].Evidence.ProtocolDigest
		value, err = domain.FinalizeCharacterActivationChapterEvidence(value)
		if err != nil {
			return err
		}
		if err := s.writeCharacterActivationJSON(filepath.Join(root, "chapter_evidence.json"), value, true); err != nil {
			return err
		}
		result = &value
		return nil
	})
	return result, err
}
