package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// PrepareVerifiedCharacterReadinessReview authenticates one current pending
// boundary and its optional immutable paid audit under one metadata read lock.
// The returned input contains data only; Commit rebuilds authority after the
// caller's model operation instead of retaining this prefix across calls.
func (s *Store) PrepareVerifiedCharacterReadinessReview(session domain.CharacterActivationSession, reviewProtocol string) (domain.CharacterReadinessReviewInput, *domain.CharacterReadinessReviewAudit, error) {
	var input domain.CharacterReadinessReviewInput
	if err := domain.ValidateCharacterActivationSession(session); err != nil {
		return input, nil, err
	}
	root, err := characterActivationSessionDir(session.GenerationID, session.Chapter)
	if err != nil {
		return input, nil, err
	}
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return input, nil, err
	}
	var cached *domain.CharacterReadinessReviewAudit
	err = p.withProjectedReadLock(func() error {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, session.GenerationID, session.Chapter)
		if err != nil {
			return err
		}
		if prefix == nil || session.Phase != "assessing" || !jsonValuesEqual(prefix.Session(), session) {
			return fmt.Errorf("verified readiness preparation requires the exact current pending session")
		}
		cached, err = s.loadVerifiedReadinessAudit(root, *prefix)
		if err != nil {
			return err
		}
		if cached != nil {
			if cached.Input.ReviewProtocol != reviewProtocol {
				return fmt.Errorf("immutable verified readiness audit differs from the requested protocol")
			}
			// The private loader already rebuilt and checked this exact input.
			// Detach the returned input without copying verified steps twice.
			raw, err := json.Marshal(cached.Input)
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &input)
		}
		var context domain.CharacterReadinessContext
		if err := s.readCharacterActivationJSON(filepath.Join(root, "chapter_context.json"), &context); err != nil {
			return err
		}
		input, err = domain.NewCharacterReadinessReviewInputFromSteps(context, prefix.Session(), prefix.Steps(), reviewProtocol)
		return err
	})
	if err != nil {
		return domain.CharacterReadinessReviewInput{}, nil, err
	}
	return input, cached, nil
}

// CommitVerifiedCharacterReadinessReview publishes the immutable paid audit,
// its readiness receipt, then the applied cursor under one metadata write lock.
// Each prefix is authenticated once. A failure after either immutable write
// leaves that exact evidence available for an idempotent mechanical retry.
func (s *Store) CommitVerifiedCharacterReadinessReview(expectedSessionDigest string, audit domain.CharacterReadinessReviewAudit) (*domain.CharacterActivationSession, error) {
	return s.commitVerifiedCharacterReadinessReview(expectedSessionDigest, audit, s.writeCharacterActivationJSON)
}

// The writer parameter is a per-transaction storage seam for testing failures
// between the existing durable files; it carries no source authority or cache.
func (s *Store) commitVerifiedCharacterReadinessReview(expected string, audit domain.CharacterReadinessReviewAudit, write func(string, any, bool) error) (*domain.CharacterActivationSession, error) {
	if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
		return nil, err
	}
	if expected == "" || expected != audit.Input.SessionDigest {
		return nil, fmt.Errorf("verified readiness compare-and-swap does not name the audited pending boundary")
	}
	root, err := characterActivationSessionDir(audit.Receipt.GenerationID, audit.Receipt.Chapter)
	if err != nil {
		return nil, err
	}
	var result *domain.CharacterActivationSession
	err = s.withCharacterActivationWrite(func() error {
		prefix, err := s.loadVerifiedCharacterActivationPrefix(root, audit.Receipt.GenerationID, audit.Receipt.Chapter)
		if err != nil {
			return err
		}
		if prefix == nil {
			return fmt.Errorf("verified readiness commit has no source session")
		}
		current := prefix.Session()
		index := len(audit.Input.Trace.Cycles)
		if index <= len(current.ReadinessDigests) {
			// Loading this prefix already authenticated every applied audit at
			// its original assessing boundary. Match that exact immutable audit
			// and receipt again; rebuilding the same history adds no authority.
			if index < 1 || index > len(current.CycleDigests) || current.ReadinessDigests[index-1] != audit.Receipt.Digest || current.CycleDigests[index-1] != audit.Receipt.CycleDigest {
				return fmt.Errorf("immutable verified readiness retry conflicts with the applied boundary")
			}
			if err := s.requireExactReadinessTransactionFiles(root, index, audit, true); err != nil {
				return err
			}
			result = &current
			return nil
		}
		if current.Digest != expected || current.Phase != "assessing" || index != len(current.CycleDigests) || index != len(current.ReadinessDigests)+1 {
			return fmt.Errorf("verified readiness compare-and-swap conflict")
		}
		if err := s.validateVerifiedReadinessAudit(root, *prefix, audit); err != nil {
			return err
		}
		if err := s.requireExactReadinessTransactionFiles(root, index, audit, false); err != nil {
			return err
		}
		next, err := domain.ApplyVerifiedCharacterActivationReadiness(*prefix, audit.Receipt)
		if err != nil {
			return err
		}
		if err := write(characterReadinessAuditPath(root, index), audit, true); err != nil {
			return err
		}
		if err := write(activationReadinessPath(root, index), audit.Receipt, true); err != nil {
			return err
		}
		applied := next.Session()
		if err := write(filepath.Join(root, "session.json"), applied, false); err != nil {
			return err
		}
		result = &applied
		return nil
	})
	return result, err
}

func (s *Store) requireExactReadinessTransactionFiles(root string, index int, audit domain.CharacterReadinessReviewAudit, required bool) error {
	var savedAudit domain.CharacterReadinessReviewAudit
	if err := s.readCharacterActivationJSON(characterReadinessAuditPath(root, index), &savedAudit); err != nil {
		if required || !os.IsNotExist(err) {
			return err
		}
	} else if !jsonValuesEqual(savedAudit, audit) {
		return fmt.Errorf("immutable verified readiness audit already exists with different content")
	}
	var savedReadiness domain.CharacterChapterReadiness
	if err := s.readCharacterActivationJSON(activationReadinessPath(root, index), &savedReadiness); err != nil {
		if required || !os.IsNotExist(err) {
			return err
		}
	} else if !jsonValuesEqual(savedReadiness, audit.Receipt) {
		return fmt.Errorf("immutable verified readiness receipt already exists with different content")
	}
	return nil
}
