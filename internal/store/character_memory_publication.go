package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const characterMemoryPublicationVersion = "accepted-character-memory-publication.v1"
const characterMemoryPublicationsRoot = "meta/planning/v2/character_memory_publications"

type CharacterMemoryPublicationFile struct {
	Path         string `json:"path"`
	BeforeExists bool   `json:"before_exists"`
	Before       []byte `json:"before,omitempty"`
	After        []byte `json:"after"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
}

type CharacterMemoryPublicationCandidate struct {
	Version                string                           `json:"version"`
	GenerationID           string                           `json:"generation_id"`
	Chapter                int                              `json:"chapter"`
	BundleDigest           string                           `json:"bundle_digest"`
	OutcomeReceiptDigest   string                           `json:"outcome_receipt_digest"`
	OutcomeActualCanonRoot string                           `json:"outcome_actual_canon_root"`
	Files                  []CharacterMemoryPublicationFile `json:"files"`
	Outcome                domain.ActualOutcomeReceiptV2    `json:"outcome"`
}

type CharacterMemoryPublicationManifest struct {
	Version              string                           `json:"version"`
	GenerationID         string                           `json:"generation_id"`
	Chapter              int                              `json:"chapter"`
	BundleDigest         string                           `json:"bundle_digest"`
	OutcomeReceiptDigest string                           `json:"outcome_receipt_digest"`
	BeforeCanonRoot      string                           `json:"before_canon_root"`
	AfterCanonRoot       string                           `json:"after_canon_root"`
	Files                []CharacterMemoryPublicationFile `json:"files"`
	Outcome              domain.ActualOutcomeReceiptV2    `json:"outcome"`
	PublicationDigest    string                           `json:"publication_digest"`
}

func publicationOverrides(files []CharacterMemoryPublicationFile, before bool) map[string]string {
	out := make(map[string]string, len(files))
	for _, file := range files {
		if before {
			out[file.Path] = file.BeforeSHA256
		} else {
			out[file.Path] = file.AfterSHA256
		}
	}
	return out
}
func (c *CharacterMemoryPublicationCandidate) BeforeOverrides() map[string]string {
	return publicationOverrides(c.Files, true)
}
func (c *CharacterMemoryPublicationCandidate) AfterOverrides() map[string]string {
	return publicationOverrides(c.Files, false)
}
func (m *CharacterMemoryPublicationManifest) BeforeOverrides() map[string]string {
	return publicationOverrides(m.Files, true)
}
func (m *CharacterMemoryPublicationManifest) AfterOverrides() map[string]string {
	return publicationOverrides(m.Files, false)
}

// Prepare records the exact deterministic transition before acceptance. It does
// not write canon and cannot itself authorize Apply. Whole-canon roots are
// supplied by the pipeline's restricted override proof, not inferred from the
// potentially half-published current files.
func (s *Store) PrepareCharacterMemoryPublication(candidate *CharacterMemoryPublicationCandidate, beforeCanonRoot, afterCanonRoot string) (*CharacterMemoryPublicationManifest, error) {
	if candidate == nil {
		return nil, fmt.Errorf("character memory publication candidate is required")
	}
	manifest := &CharacterMemoryPublicationManifest{
		Version: candidate.Version, GenerationID: candidate.GenerationID, Chapter: candidate.Chapter,
		BundleDigest: candidate.BundleDigest, OutcomeReceiptDigest: candidate.OutcomeReceiptDigest,
		BeforeCanonRoot: beforeCanonRoot, AfterCanonRoot: afterCanonRoot,
		Files: candidate.Files, Outcome: candidate.Outcome,
	}
	if candidate.OutcomeActualCanonRoot != beforeCanonRoot {
		return nil, fmt.Errorf("character memory publication before root differs from candidate outcome")
	}
	var err error
	manifest.PublicationDigest, err = computeCharacterMemoryPublicationDigest(*manifest)
	if err != nil {
		return nil, err
	}
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	err = p.withProjectedWriteLock(func() error {
		if err := p.validateCharacterMemoryPublicationUnlocked(manifest); err != nil {
			return err
		}
		previous, err := p.loadCharacterMemoryPublicationUnlocked(manifest.OutcomeReceiptDigest)
		if err != nil {
			return err
		}
		if previous != nil {
			if !jsonValuesEqual(*previous, *manifest) {
				return fmt.Errorf("character memory publication immutable content conflict")
			}
			manifest = previous
			_, err = inspectCharacterMemoryPublicationFiles(p.io, manifest.Files)
			if err != nil {
				return err
			}
			return syncProjectedDirs(p.io.path(characterMemoryPublicationsRoot), p.io.path(projectedPlanningV2Root), p.io.path("meta/planning"), p.io.path("meta"))
		}
		// A new intent must capture the actual before state. Only a durable
		// existing intent permits the before/after mixture used by recovery.
		for _, file := range manifest.Files {
			raw, exists, err := readCharacterMemoryPublicationFile(p.io, file.Path)
			if err != nil {
				return err
			}
			if exists != file.BeforeExists || !bytes.Equal(raw, file.Before) {
				return fmt.Errorf("character memory publication before file changed: %s", file.Path)
			}
		}
		rel, _ := characterMemoryPublicationPath(manifest.OutcomeReceiptDigest)
		if err := validateCharacterMemoryPublicationPath(p.io, rel); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		if err := p.io.writeFileNoReplaceUnlocked(rel, raw); err != nil {
			return err
		}
		return syncProjectedDirs(p.io.path(projectedPlanningV2Root), p.io.path("meta/planning"), p.io.path("meta"))
	})
	return manifest, err
}
func (s *Store) LoadCharacterMemoryPublication(outcomeReceiptDigest string) (*CharacterMemoryPublicationManifest, error) {
	return s.ProjectedV2().LoadCharacterMemoryPublication(outcomeReceiptDigest)
}
func (s *ProjectedStoreV2) LoadCharacterMemoryPublication(outcomeReceiptDigest string) (*CharacterMemoryPublicationManifest, error) {
	if _, err := characterMemoryPublicationPath(outcomeReceiptDigest); err != nil {
		return nil, err
	}
	if err := validateCharacterMemoryPublicationPath(s.io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*CharacterMemoryPublicationManifest, error) {
		return s.loadCharacterMemoryPublicationUnlocked(outcomeReceiptDigest)
	})
}
func (s *Store) ApplyPreparedCharacterMemoryPublication(manifest *CharacterMemoryPublicationManifest) error {
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return err
	}
	return p.withProjectedWriteLock(func() error {
		stored, err := p.exactCharacterMemoryPublicationUnlocked(manifest)
		if err != nil {
			return err
		}
		if err := p.requireAcceptedCharacterMemoryPublicationUnlocked(stored); err != nil {
			return err
		}
		// Preflight every target before the first mutation. Unknown third-state
		// bytes are never overwritten, even if other files are recoverable.
		if _, err := inspectCharacterMemoryPublicationFiles(p.io, stored.Files); err != nil {
			return err
		}
		for _, file := range stored.Files {
			raw, exists, err := readCharacterMemoryPublicationFile(p.io, file.Path)
			if err != nil {
				return err
			}
			if exists && bytes.Equal(raw, file.After) {
				continue
			}
			if exists != file.BeforeExists || !bytes.Equal(raw, file.Before) {
				return fmt.Errorf("character memory publication target changed after preflight: %s", file.Path)
			}
			if err := p.io.WriteFileUnlocked(file.Path, file.After); err != nil {
				return fmt.Errorf("publish character memory %s: %w", file.Path, err)
			}
			if err := syncProjectedDirs(filepath.Dir(p.io.path(file.Path)), p.io.path(characterAgentRoot), p.io.path("meta")); err != nil {
				return err
			}
		}
		// An earlier attempt may have completed a rename but failed its
		// directory sync. Exact-after retries must finish that durability step.
		for _, file := range stored.Files {
			if err := syncProjectedDirs(filepath.Dir(p.io.path(file.Path))); err != nil {
				return err
			}
		}
		if err := syncProjectedDirs(p.io.path(characterAgentRoot), p.io.path("meta")); err != nil {
			return err
		}
		complete, err := inspectCharacterMemoryPublicationFiles(p.io, stored.Files)
		if err != nil {
			return err
		}
		if !complete {
			return fmt.Errorf("character memory publication did not reach its exact after state")
		}
		return nil
	})
}
func (s *Store) InspectCharacterMemoryPublication(manifest *CharacterMemoryPublicationManifest) (bool, error) {
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return false, err
	}
	return withProjectedReadResult(p, func() (bool, error) {
		stored, err := p.exactCharacterMemoryPublicationUnlocked(manifest)
		if err != nil {
			return false, err
		}
		return inspectCharacterMemoryPublicationFiles(p.io, stored.Files)
	})
}

func characterMemoryPublicationSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func characterMemoryPublicationPath(digest string) (string, error) {
	if !characterAgentDigestPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid character memory outcome digest %q", digest)
	}
	return filepath.Join(characterMemoryPublicationsRoot, digest+".json"), nil
}

func computeCharacterMemoryPublicationDigest(manifest CharacterMemoryPublicationManifest) (string, error) {
	manifest.PublicationDigest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return domain.ComputePlanningV2JSONDigest(raw)
}

func validateCharacterMemoryTarget(path string) error {
	if path == characterAgentRegistryPath() {
		return nil
	}
	prefix := filepath.Join(characterAgentRoot, "memory") + string(filepath.Separator)
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".json") {
		return fmt.Errorf("character memory publication target is not canonical memory: %s", path)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), ".json")
	if err := validateCharacterAgentPathComponent("memory agent_id", id); err != nil {
		return err
	}
	if characterAgentMemoryPath(id) != path {
		return fmt.Errorf("noncanonical character memory target %q", path)
	}
	return nil
}

// Reject symlinks at every existing component, including an existing target.
// Missing suffixes are allowed for a before image that does not yet exist.
func validateCharacterMemoryPublicationPath(io *IO, rel string) error {
	if filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("unsafe character memory publication path %q", rel)
	}
	path := io.dir
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return fmt.Errorf("character memory publication path is not a safe regular file: %s", path)
		}
	}
	return nil
}

func readCharacterMemoryPublicationFile(io *IO, rel string) ([]byte, bool, error) {
	if err := validateCharacterMemoryPublicationPath(io, rel); err != nil {
		return nil, false, err
	}
	raw, err := io.ReadFileUnlocked(rel)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func inspectCharacterMemoryPublicationFiles(io *IO, files []CharacterMemoryPublicationFile) (bool, error) {
	complete := true
	for _, file := range files {
		raw, exists, err := readCharacterMemoryPublicationFile(io, file.Path)
		if err != nil {
			return false, err
		}
		if exists && bytes.Equal(raw, file.After) {
			continue
		}
		complete = false
		if exists != file.BeforeExists || !bytes.Equal(raw, file.Before) {
			return false, fmt.Errorf("character memory publication target differs from exact before and after: %s", file.Path)
		}
	}
	return complete, nil
}

func (s *ProjectedStoreV2) loadCharacterMemoryPublicationUnlocked(digest string) (*CharacterMemoryPublicationManifest, error) {
	rel, err := characterMemoryPublicationPath(digest)
	if err != nil {
		return nil, err
	}
	raw, exists, err := readCharacterMemoryPublicationFile(s.io, rel)
	if err != nil || !exists {
		return nil, err
	}
	var manifest CharacterMemoryPublicationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.OutcomeReceiptDigest != digest {
		return nil, fmt.Errorf("character memory publication path identity mismatch")
	}
	if err := s.validateCharacterMemoryPublicationUnlocked(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (s *ProjectedStoreV2) exactCharacterMemoryPublicationUnlocked(manifest *CharacterMemoryPublicationManifest) (*CharacterMemoryPublicationManifest, error) {
	if manifest == nil {
		return nil, fmt.Errorf("durable character memory publication is required")
	}
	stored, err := s.loadCharacterMemoryPublicationUnlocked(manifest.OutcomeReceiptDigest)
	if err != nil {
		return nil, err
	}
	if stored == nil || !jsonValuesEqual(*stored, *manifest) {
		return nil, fmt.Errorf("character memory publication is not the exact durable prepared manifest")
	}
	return stored, nil
}

func (s *ProjectedStoreV2) validateCharacterMemoryPublicationUnlocked(manifest *CharacterMemoryPublicationManifest) error {
	if manifest.Version != characterMemoryPublicationVersion || manifest.Chapter <= 0 || len(manifest.Files) == 0 {
		return fmt.Errorf("invalid character memory publication version/chapter/files")
	}
	if err := validateProjectedPathComponent("generation_id", manifest.GenerationID); err != nil {
		return err
	}
	for _, digest := range []string{manifest.BundleDigest, manifest.OutcomeReceiptDigest, manifest.BeforeCanonRoot, manifest.AfterCanonRoot, manifest.PublicationDigest} {
		if !characterAgentDigestPattern.MatchString(digest) {
			return fmt.Errorf("invalid character memory publication digest %q", digest)
		}
	}
	if err := domain.ValidateActualOutcomeReceiptV2(manifest.Outcome); err != nil {
		return fmt.Errorf("character memory publication outcome: %w", err)
	}
	outcome := manifest.Outcome
	if outcome.GenerationID != manifest.GenerationID || outcome.Chapter != manifest.Chapter || outcome.ReceiptDigest != manifest.OutcomeReceiptDigest || outcome.ActualCanonRoot != manifest.BeforeCanonRoot || !outcome.ProjectionMatch {
		return fmt.Errorf("character memory publication outcome identity/root mismatch")
	}
	wantDigest, err := computeCharacterMemoryPublicationDigest(*manifest)
	if err != nil || wantDigest != manifest.PublicationDigest {
		return fmt.Errorf("character memory publication digest mismatch")
	}
	previous := ""
	for _, file := range manifest.Files {
		if err := validateCharacterMemoryTarget(file.Path); err != nil {
			return err
		}
		if previous >= file.Path {
			return fmt.Errorf("character memory publication files must be unique and sorted")
		}
		previous = file.Path
		if (file.BeforeExists && characterMemoryPublicationSHA(file.Before) != file.BeforeSHA256) || (!file.BeforeExists && (len(file.Before) != 0 || file.BeforeSHA256 != "")) || len(file.After) == 0 || characterMemoryPublicationSHA(file.After) != file.AfterSHA256 {
			return fmt.Errorf("character memory publication file bytes/digest mismatch: %s", file.Path)
		}
	}
	bundle, err := s.loadCharacterMemoryPublicationBundleUnlocked(manifest)
	if err != nil {
		return err
	}
	derived, err := deriveCharacterMemoryPublicationFiles(*bundle, outcome, manifest.Files)
	if err != nil {
		return fmt.Errorf("derive character memory publication: %w", err)
	}
	if !jsonValuesEqual(derived, manifest.Files) {
		return fmt.Errorf("character memory publication after images are not the deterministic sealed outcome transition")
	}
	return nil
}

func (s *ProjectedStoreV2) loadCharacterMemoryPublicationBundleUnlocked(manifest *CharacterMemoryPublicationManifest) (*domain.ProjectedChapterBundle, error) {
	if _, err := s.validateSealedGenerationUnlocked(manifest.GenerationID); err != nil {
		return nil, err
	}
	rel := projectedBundlePath(projectedSealedGenerationPath(manifest.GenerationID), manifest.Chapter)
	if err := validateCharacterMemoryPublicationPath(s.io, rel); err != nil {
		return nil, err
	}
	bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(manifest.GenerationID), manifest.Chapter)
	if err != nil {
		return nil, err
	}
	if bundle.BundleDigest != manifest.BundleDigest || bundle.GenerationID != manifest.GenerationID || bundle.ProjectedPostStateRoot != manifest.Outcome.ProjectedPostStateRoot || bundle.ChapterWorldSimulation.Version < 2 || !bundle.HasCharacterEvidence() {
		return nil, fmt.Errorf("character memory publication is not bound to its exact sealed character bundle")
	}
	var promotion domain.PromotionReceiptV2
	if err := s.readJSONUnlocked(projectedPromotionReceiptPath(manifest.GenerationID, manifest.Chapter, manifest.Outcome.PromotionReceiptDigest), &promotion); err != nil {
		return nil, err
	}
	if err := domain.ValidateActualOutcomeAgainstPromotionV2(manifest.Outcome, promotion, *bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

func (s *ProjectedStoreV2) requireAcceptedCharacterMemoryPublicationUnlocked(manifest *CharacterMemoryPublicationManifest) error {
	var accepted domain.ActualOutcomeReceiptV2
	rel := projectedActualOutcomePath(manifest.GenerationID, manifest.Chapter, manifest.OutcomeReceiptDigest)
	if err := validateCharacterMemoryPublicationPath(s.io, rel); err != nil {
		return err
	}
	if err := s.readJSONUnlocked(rel, &accepted); err != nil {
		return fmt.Errorf("character memory publication requires durable accepted outcome: %w", err)
	}
	if !jsonValuesEqual(accepted, manifest.Outcome) {
		return fmt.Errorf("character memory publication durable outcome mismatch")
	}
	cursor, err := s.loadRealizationCursorUnlocked()
	if err != nil {
		return err
	}
	if cursor == nil || cursor.LastAcceptedChapter != manifest.Chapter || cursor.LastOutcomeReceiptDigest != manifest.OutcomeReceiptDigest {
		return fmt.Errorf("character memory publication requires the exact accepted realization cursor")
	}
	if err := s.validateRealizationControlUnlocked(*cursor); err != nil {
		return err
	}
	return nil
}
