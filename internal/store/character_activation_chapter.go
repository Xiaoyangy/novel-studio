package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func (s *Store) LoadCharacterActivationChapterEvidence(generation string, chapter int) (*domain.CharacterActivationChapterEvidence, error) {
	verified, err := s.LoadVerifiedCharacterActivationChapter(generation, chapter)
	if err != nil || verified == nil {
		return nil, err
	}
	value := verified.Evidence()
	return &value, nil
}

// Read actual immutable source bytes for this operation. No Store-level cache
// or caller-supplied digest can replace full chapter authentication.
func (s *Store) LoadVerifiedCharacterActivationChapter(generation string, chapter int) (*domain.VerifiedCharacterActivationChapter, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	var value domain.CharacterActivationChapterEvidence
	if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_evidence.json"), &value); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if value.Session.GenerationID != generation || value.Session.Chapter != chapter {
		return nil, fmt.Errorf("chapter activation evidence path mismatch")
	}
	for _, cycle := range value.Cycles {
		if cycle.Version == domain.CharacterActivationCycleV3Version {
			if err := validateCharacterMemoryPublicationPath(s.ProjectedV2().io, projectedWriteLockFile); err != nil {
				return nil, err
			}
			return withProjectedReadResult(s.ProjectedV2(), func() (*domain.VerifiedCharacterActivationChapter, error) {
				// Reload under the same lock as every original cycle/proof/audit.
				if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_evidence.json"), &value); err != nil {
					return nil, err
				}
				if err := s.validateStoredActivationChapterV3(root, generation, chapter, value); err != nil {
					return nil, err
				}
				verified, err := domain.VerifyCharacterActivationChapter(value)
				if err != nil {
					return nil, err
				}
				return &verified, nil
			})
		}
	}
	verified, err := domain.VerifyCharacterActivationChapter(value)
	if err != nil {
		return nil, err
	}
	return &verified, nil
}

// Collect only fully verified, immutable sources. It never repairs or invents
// missing model outcomes and never derives completeness from a ready flag alone.
func (s *Store) CollectCharacterActivationChapterEvidence(generation string, chapter int) (*domain.CharacterActivationChapterEvidence, error) {
	session, err := s.LoadCharacterActivationSession(generation, chapter)
	if err != nil {
		return nil, err
	}
	if session == nil || session.Phase != "ready" {
		return nil, fmt.Errorf("chapter activation session is not ready")
	}
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	if verified, err := s.hasVerifiedActivationCycles(root, generation, chapter, len(session.CycleDigests)); err != nil {
		return nil, err
	} else if verified {
		return s.collectVerifiedCharacterActivationChapterEvidence(root, generation, chapter)
	}
	context, err := s.LoadCharacterReadinessContext(generation, chapter)
	if err != nil {
		return nil, err
	}
	if context == nil {
		return nil, fmt.Errorf("chapter activation context is missing")
	}
	value := domain.CharacterActivationChapterEvidence{Context: *context, Session: *session}
	for i := range session.CycleDigests {
		cycle, err := s.LoadCharacterActivationCycle(generation, chapter, i+1)
		if err != nil {
			return nil, err
		}
		if cycle == nil {
			return nil, fmt.Errorf("chapter activation cycle is missing")
		}
		inputs, err := s.CharacterAgents.LoadActivationInputsForCycle(*cycle)
		if err != nil {
			return nil, err
		}
		review, err := s.LoadCharacterReadinessReviewAudit(generation, chapter, i+1)
		if err != nil {
			return nil, err
		}
		if review == nil {
			return nil, fmt.Errorf("chapter activation readiness audit is missing")
		}
		value.Cycles = append(value.Cycles, *cycle)
		value.Inputs = append(value.Inputs, *inputs)
		value.Reviews = append(value.Reviews, *review)
	}
	value.ProtocolDigest = value.Cycles[0].Evidence.ProtocolDigest
	value, err = domain.FinalizeCharacterActivationChapterEvidence(value)
	if err != nil {
		return nil, err
	}
	if err := s.withCharacterActivationWrite(func() error {
		return s.writeCharacterActivationJSON(filepath.Join(root, "chapter_evidence.json"), value, true)
	}); err != nil {
		return nil, err
	}
	return &value, nil
}
