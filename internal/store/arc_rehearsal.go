package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const ArcRehearsalRoot = "meta/planning/arc_rehearsals"

type arcRehearsalIndex struct {
	InputDigest  string `json:"input_digest"`
	DraftDigest  string `json:"draft_digest,omitempty"`
	ReportDigest string `json:"report_digest,omitempty"`
}

func arcRehearsalPath(kind, digest string) (string, error) {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 || strings.Trim(digest[7:], "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid arc rehearsal digest")
	}
	return filepath.Join(ArcRehearsalRoot, kind, digest[7:]+".json"), nil
}

func (s *Store) readArcRehearsal(kind, digest string, out any) error {
	p, err := arcRehearsalPath(kind, digest)
	if err != nil {
		return err
	}
	return s.Progress.io.ReadJSON(p, out)
}

func (s *Store) saveArcRehearsalImmutable(kind, digest string, value any) error {
	p, err := arcRehearsalPath(kind, digest)
	if err != nil {
		return err
	}
	return s.Progress.io.WithWriteLock(func() error {
		wanted, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		old, err := s.Progress.io.ReadFileUnlocked(p)
		if err == nil {
			if string(old) != string(wanted) {
				return fmt.Errorf("immutable arc rehearsal artifact differs")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		return s.Progress.io.WriteFileUnlocked(p, wanted)
	})
}

func (s *Store) SaveArcRehearsalDraft(input domain.ArcRehearsalInput, draft domain.ArcRehearsalDraft) error {
	i, err := domain.FinalizeArcRehearsalInput(input)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(i, input) {
		return fmt.Errorf("rehearsal input is not finalized")
	}
	d, err := domain.FinalizeArcRehearsalDraft(input, draft)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(d, draft) {
		return fmt.Errorf("rehearsal draft is not finalized")
	}
	if err := s.saveArcRehearsalImmutable("inputs", input.InputDigest, input); err != nil {
		return err
	}
	if err := s.saveArcRehearsalImmutable("drafts", draft.DraftDigest, draft); err != nil {
		return err
	}
	return s.updateArcRehearsalIndex(arcRehearsalIndex{InputDigest: input.InputDigest, DraftDigest: draft.DraftDigest})
}

func (s *Store) updateArcRehearsalIndex(next arcRehearsalIndex) error {
	p, err := arcRehearsalPath("by_input", next.InputDigest)
	if err != nil {
		return err
	}
	return s.Progress.io.WithWriteLock(func() error {
		var old arcRehearsalIndex
		err := s.Progress.io.ReadJSONUnlocked(p, &old)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if old.InputDigest != next.InputDigest || old.DraftDigest != next.DraftDigest || old.ReportDigest != "" && next.ReportDigest != "" && old.ReportDigest != next.ReportDigest {
				return fmt.Errorf("rehearsal input already binds a different draft/review")
			}
			if next.ReportDigest == "" {
				next.ReportDigest = old.ReportDigest
			}
		}
		return s.Progress.io.WriteJSONUnlocked(p, next)
	})
}

func (s *Store) LoadArcRehearsalDraftForInput(inputDigest string) (*domain.ArcRehearsalDraft, *domain.ArcRehearsalInput, error) {
	var index arcRehearsalIndex
	if err := s.readArcRehearsal("by_input", inputDigest, &index); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if index.InputDigest != inputDigest {
		return nil, nil, fmt.Errorf("rehearsal input index mismatch")
	}
	var input domain.ArcRehearsalInput
	if err := s.readArcRehearsal("inputs", inputDigest, &input); err != nil {
		return nil, nil, err
	}
	checked, err := domain.FinalizeArcRehearsalInput(input)
	if err != nil || !jsonValuesEqual(checked, input) || input.InputDigest != inputDigest {
		return nil, nil, fmt.Errorf("invalid content-addressed rehearsal input")
	}
	var draft domain.ArcRehearsalDraft
	if err := s.readArcRehearsal("drafts", index.DraftDigest, &draft); err != nil {
		return nil, nil, err
	}
	valid, err := domain.FinalizeArcRehearsalDraft(input, draft)
	if err != nil || !jsonValuesEqual(valid, draft) || draft.DraftDigest != index.DraftDigest {
		return nil, nil, fmt.Errorf("invalid content-addressed rehearsal draft")
	}
	return &draft, &input, nil
}

func (s *Store) SaveArcRehearsalReport(input domain.ArcRehearsalInput, draft domain.ArcRehearsalDraft, report domain.ArcRehearsalReport) error {
	valid, err := domain.FinalizeArcRehearsalReport(input, draft, report)
	if err != nil {
		return err
	}
	if !jsonValuesEqual(valid, report) {
		return fmt.Errorf("rehearsal report is not finalized")
	}
	if err := s.SaveArcRehearsalDraft(input, draft); err != nil {
		return err
	}
	if err := s.saveArcRehearsalImmutable("reports", report.ReportDigest, report); err != nil {
		return err
	}
	return s.updateArcRehearsalIndex(arcRehearsalIndex{InputDigest: input.InputDigest, DraftDigest: draft.DraftDigest, ReportDigest: report.ReportDigest})
}

func (s *Store) LoadVerifiedArcRehearsal(digest string) (*domain.ArcRehearsalReport, *domain.ArcRehearsalInput, error) {
	var report domain.ArcRehearsalReport
	if err := s.readArcRehearsal("reports", digest, &report); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	draft, input, err := s.LoadArcRehearsalDraftForInput(report.InputDigest)
	if err != nil {
		return nil, nil, err
	}
	if input == nil || draft == nil {
		return nil, nil, fmt.Errorf("rehearsal report lost its input/draft sources")
	}
	valid, err := domain.FinalizeArcRehearsalReport(*input, *draft, report)
	if err != nil || !jsonValuesEqual(valid, report) || report.ReportDigest != digest {
		return nil, nil, fmt.Errorf("invalid content-addressed rehearsal report")
	}
	return &report, input, nil
}

func (s *Store) LoadVerifiedArcRehearsalForInput(inputDigest string) (*domain.ArcRehearsalReport, error) {
	var index arcRehearsalIndex
	if err := s.readArcRehearsal("by_input", inputDigest, &index); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if index.InputDigest != inputDigest {
		return nil, fmt.Errorf("rehearsal input index mismatch")
	}
	if index.ReportDigest == "" {
		return nil, nil
	}
	report, input, err := s.LoadVerifiedArcRehearsal(index.ReportDigest)
	if err != nil {
		return nil, err
	}
	if report == nil || input == nil || input.InputDigest != inputDigest {
		return nil, fmt.Errorf("rehearsal indexed review is missing or foreign")
	}
	return report, nil
}

// Freshness is separate from immutable historical verification. The caller also
// compares its own accepted canon and combined foundation/RAG SourceRoot.
func (s *Store) ValidateArcRehearsalInputFresh(input domain.ArcRehearsalInput) error {
	for rel, want := range input.SourceFiles {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean != rel {
			return fmt.Errorf("invalid rehearsal source path")
		}
		data, err := os.ReadFile(filepath.Join(s.Dir(), rel))
		if err != nil {
			return err
		}
		if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != want {
			return fmt.Errorf("arc rehearsal source changed: %s", rel)
		}
	}
	return nil
}
