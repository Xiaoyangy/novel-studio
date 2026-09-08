package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func characterReadinessAuditPath(root string, index int) string {
	return filepath.Join(root, "readiness_audits", fmt.Sprintf("%06d.json", index))
}

func (s *Store) LoadCharacterReadinessReviewAudit(generation string, chapter, index int) (*domain.CharacterReadinessReviewAudit, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	if index <= 0 {
		return nil, fmt.Errorf("readiness audit cycle index must be positive")
	}
	if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s.ProjectedV2(), func() (*domain.CharacterReadinessReviewAudit, error) {
		return s.loadCharacterReadinessReviewAudit(root, generation, chapter, index)
	})
}

func (s *Store) loadCharacterReadinessReviewAudit(root, generation string, chapter, index int) (*domain.CharacterReadinessReviewAudit, error) {
	if verified, err := s.hasVerifiedActivationCycles(root, generation, chapter, index); err != nil {
		return nil, err
	} else if verified {
		return s.loadVerifiedReadinessAuditAt(root, generation, chapter, index)
	}
	var audit domain.CharacterReadinessReviewAudit
	if err := s.readCharacterActivationJSON(characterReadinessAuditPath(root, index), &audit); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if audit.Receipt.GenerationID != generation || audit.Receipt.Chapter != chapter || len(audit.Input.Trace.Cycles) != index {
		return nil, fmt.Errorf("readiness audit path binding mismatch")
	}
	if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
		return nil, err
	}
	if err := s.validateReadinessAuditTrace(root, audit); err != nil {
		return nil, err
	}
	return &audit, nil
}

func (s *Store) validateReadinessAuditTrace(root string, audit domain.CharacterReadinessReviewAudit) error {
	cycles := make([]domain.CharacterActivationCycle, 0, len(audit.Input.Trace.Cycles))
	for i := range audit.Input.Trace.Cycles {
		cycle, err := s.loadCharacterActivationCycle(root, audit.Receipt.GenerationID, audit.Receipt.Chapter, i+1)
		if err != nil {
			return err
		}
		if cycle == nil {
			return fmt.Errorf("readiness audit lacks its actual source cycle")
		}
		if cycle.ChapterContextDigest != audit.Input.Context.Digest {
			return fmt.Errorf("readiness audit uses a different chapter context")
		}
		cycles = append(cycles, *cycle)
	}
	trace, err := domain.BuildCharacterReadinessTrace(cycles)
	if err != nil {
		return err
	}
	left, _ := json.Marshal(trace)
	right, _ := json.Marshal(audit.Input.Trace)
	if string(left) != string(right) {
		return fmt.Errorf("readiness audit altered its actual event/state projection")
	}
	return nil
}

// The paid classification is saved before the loop advances its cursor. If
// cancellation follows the successful tool call, resumption reuses this exact
// immutable audit rather than charging for a duplicate model judgment.
func (s *Store) SaveCharacterReadinessReviewAudit(audit domain.CharacterReadinessReviewAudit) error {
	if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
		return err
	}
	root, err := characterActivationSessionDir(audit.Receipt.GenerationID, audit.Receipt.Chapter)
	if err != nil {
		return err
	}
	return s.withCharacterActivationWrite(func() error {
		if verified, err := s.hasVerifiedActivationCycles(root, audit.Receipt.GenerationID, audit.Receipt.Chapter, len(audit.Input.Trace.Cycles)); err != nil {
			return err
		} else if verified {
			return s.saveVerifiedCharacterReadinessReviewAudit(root, audit)
		}
		existing, err := s.loadCharacterReadinessReviewAudit(root, audit.Receipt.GenerationID, audit.Receipt.Chapter, len(audit.Input.Trace.Cycles))
		if err != nil {
			return err
		}
		if existing != nil {
			left, _ := json.Marshal(existing)
			right, _ := json.Marshal(audit)
			if string(left) != string(right) {
				return fmt.Errorf("immutable readiness review already exists")
			}
			return nil
		}
		current, err := s.loadCharacterActivationSession(root, audit.Receipt.GenerationID, audit.Receipt.Chapter)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("readiness audit has no activation session")
		}
		cycles, err := readinessSourceCyclesForAudit(s, root, audit)
		if err != nil {
			return err
		}
		expected, err := domain.NewCharacterReadinessReviewInput(audit.Input.Context, *current, cycles, audit.Input.ReviewProtocol)
		if err != nil {
			return err
		}
		left, _ := domain.CharacterReadinessReviewInputDigest(expected)
		right, _ := domain.CharacterReadinessReviewInputDigest(audit.Input)
		if left != right {
			return fmt.Errorf("readiness audit is not bound to the exact pending session")
		}
		return s.writeCharacterActivationJSON(characterReadinessAuditPath(root, len(current.CycleDigests)), audit, true)
	})
}

func readinessSourceCyclesForAudit(s *Store, root string, audit domain.CharacterReadinessReviewAudit) ([]domain.CharacterActivationCycle, error) {
	cycles := make([]domain.CharacterActivationCycle, 0, len(audit.Input.Trace.Cycles))
	for i := range audit.Input.Trace.Cycles {
		cycle, err := s.loadCharacterActivationCycle(root, audit.Receipt.GenerationID, audit.Receipt.Chapter, i+1)
		if err != nil {
			return nil, err
		}
		if cycle == nil {
			return nil, fmt.Errorf("readiness source cycle %d is missing", i+1)
		}
		cycles = append(cycles, *cycle)
	}
	return cycles, nil
}

func (s *Store) SaveCharacterReadinessContext(value domain.CharacterReadinessContext) error {
	checked, err := domain.FinalizeCharacterReadinessContext(value)
	if err != nil {
		return err
	}
	if checked.Digest != value.Digest {
		return fmt.Errorf("readiness context digest mismatch")
	}
	root, err := characterActivationSessionDir(value.GenerationID, value.Chapter)
	if err != nil {
		return err
	}
	return s.withCharacterActivationWrite(func() error {
		return s.writeCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), value, true)
	})
}

func (s *Store) LoadCharacterReadinessContext(generation string, chapter int) (*domain.CharacterReadinessContext, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	var value domain.CharacterReadinessContext
	if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), &value); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	checked, err := domain.FinalizeCharacterReadinessContext(value)
	if err != nil {
		return nil, err
	}
	if checked.Digest != value.Digest || value.GenerationID != generation || value.Chapter != chapter {
		return nil, fmt.Errorf("readiness chapter context binding mismatch")
	}
	return &value, nil
}
