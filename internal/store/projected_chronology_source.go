package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const projectedChronologySourceFile = "chronology_source.json"
const projectedChronologySourceVersion = "projected-chronology-source.v1"

type projectedChronologyAcceptedSource struct {
	GenerationID  string `json:"generation_id"`
	Chapter       int    `json:"chapter"`
	BundleDigest  string `json:"bundle_digest"`
	OutcomeDigest string `json:"outcome_digest"`
}

// This capsule contains original author bytes and their existing frozen
// foundation inventory, or immutable accepted-outcome references. It contains
// no caller-supplied physical before-state and cannot replace the source root.
type projectedChronologySource struct {
	Version              string                             `json:"version"`
	GenerationID         string                             `json:"generation_id"`
	SourceSnapshotDigest string                             `json:"source_snapshot_digest"`
	Foundation           ProjectAllFoundationSnapshot       `json:"foundation"`
	Characters           []byte                             `json:"characters,omitempty"`
	Registry             []byte                             `json:"registry,omitempty"`
	Accepted             *projectedChronologyAcceptedSource `json:"accepted,omitempty"`
	Digest               string                             `json:"digest"`
}

func projectedChronologySourceDigest(value projectedChronologySource) (string, error) {
	value.Digest = ""
	hash, err := domain.DeterministicPlanningHash(value)
	if err != nil {
		return "", err
	}
	return "sha256:" + strings.TrimPrefix(hash, "sha256:"), nil
}

func (s *ProjectedStoreV2) readChronologySourceOriginal(rel string, inventory ProjectAllFoundationSnapshot) ([]byte, error) {
	if err := validateCharacterMemoryPublicationPath(s.io, rel); err != nil {
		return nil, err
	}
	raw, err := s.io.ReadFileUnlocked(rel)
	if os.IsNotExist(err) {
		raw = nil
		err = nil
	}
	if err != nil {
		return nil, err
	}
	want := inventory.Artifacts[rel]
	if (len(raw) == 0 && want != "") || (len(raw) > 0 && characterMemoryPublicationSHA(raw) != want) {
		return nil, fmt.Errorf("chronology source %s differs from its frozen foundation fingerprint", rel)
	}
	return raw, nil
}

func chronologyInitialCharacters(value projectedChronologySource) ([]domain.Character, domain.CharacterAgentRegistry, error) {
	var zero domain.CharacterAgentRegistry
	if len(value.Characters) == 0 {
		return nil, zero, fmt.Errorf("chronology initial source requires frozen characters.json")
	}
	for rel, raw := range map[string][]byte{"characters.json": value.Characters, "meta/character_agents/registry.json": value.Registry} {
		want := value.Foundation.Artifacts[rel]
		if (len(raw) == 0 && want != "") || (len(raw) > 0 && characterMemoryPublicationSHA(raw) != want) {
			return nil, zero, fmt.Errorf("chronology source original bytes differ from frozen inventory: %s", rel)
		}
	}
	var characters []domain.Character
	if err := json.Unmarshal(value.Characters, &characters); err != nil {
		return nil, zero, err
	}
	registry := domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	if len(value.Registry) > 0 {
		if err := json.Unmarshal(value.Registry, &registry); err != nil {
			return nil, zero, err
		}
		checked, err := domain.FinalizeCharacterAgentRegistry(registry)
		if err != nil {
			return nil, zero, err
		}
		if checked.RegistryRoot != registry.RegistryRoot {
			return nil, zero, fmt.Errorf("frozen character registry root mismatch")
		}
	}
	for _, character := range characters {
		if character.InitialState == nil {
			continue
		}
		if _, ok := registry.Resolve(character.Name); ok {
			continue
		}
		var err error
		registry, _, err = registry.UpsertCharacter(character.Name, character.Aliases, character.Tier, 0, "")
		if err != nil {
			return nil, zero, err
		}
	}
	return characters, registry, nil
}

func chronologyInitialSource(value projectedChronologySource) (domain.WorldPhysicalStateV2, error) {
	characters, registry, err := chronologyInitialCharacters(value)
	if err != nil {
		return domain.WorldPhysicalStateV2{}, err
	}
	state, err := domain.BuildWorldPhysicalStateFromInitialV2(characters, registry)
	if err != nil {
		return state, err
	}
	return state, nil
}

// Reload proves the accepted source using immutable bundle/promotion/outcome
// roots, not the mutable current cursor (which can advance after publication).
func (s *ProjectedStoreV2) chronologyAcceptedSource(generation domain.PlanningGenerationV2, ref projectedChronologyAcceptedSource) (domain.WorldPhysicalStateV2, error) {
	var zero domain.WorldPhysicalStateV2
	if ref.GenerationID == generation.GenerationID || ref.Chapter != generation.BaseCanonChapter || ref.Chapter+1 != generation.FirstProjectedChapter {
		return zero, fmt.Errorf("chronology accepted source has a foreign predecessor boundary")
	}
	if err := validateProjectedPathComponent("source_generation", ref.GenerationID); err != nil {
		return zero, err
	}
	if err := validateProjectedPathComponent("source_outcome", ref.OutcomeDigest); err != nil {
		return zero, err
	}
	predecessor, err := s.loadGenerationAtUnlocked(projectedSealedGenerationPath(ref.GenerationID))
	if err != nil {
		return zero, err
	}
	if predecessor == nil || predecessor.BaseCanonChapter >= generation.BaseCanonChapter || predecessor.FirstProjectedChapter > ref.Chapter || predecessor.LastProjectedChapter < ref.Chapter {
		return zero, fmt.Errorf("chronology accepted source is not an earlier acyclic sealed range")
	}
	if _, err := s.validateSealedGenerationUnlocked(ref.GenerationID); err != nil {
		return zero, err
	}
	bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(ref.GenerationID), ref.Chapter)
	if err != nil {
		return zero, err
	}
	if bundle == nil || bundle.BundleDigest != ref.BundleDigest || bundle.ProjectedPostStateRoot != generation.BaseStateRoot || bundle.ChapterWorldSimulation.PhysicalState == nil {
		return zero, fmt.Errorf("chronology source differs from its accepted sealed physical bundle")
	}
	var outcome domain.ActualOutcomeReceiptV2
	if err := s.readJSONUnlocked(projectedActualOutcomePath(ref.GenerationID, ref.Chapter, ref.OutcomeDigest), &outcome); err != nil {
		return zero, err
	}
	if outcome.ReceiptDigest != ref.OutcomeDigest || outcome.GenerationID != ref.GenerationID || outcome.Chapter != ref.Chapter || !outcome.ProjectionMatch {
		return zero, fmt.Errorf("chronology source lacks its exact accepted outcome")
	}
	var promotion domain.PromotionReceiptV2
	if err := s.readJSONUnlocked(projectedPromotionReceiptPath(ref.GenerationID, ref.Chapter, outcome.PromotionReceiptDigest), &promotion); err != nil {
		return zero, err
	}
	if err := domain.ValidateActualOutcomeAgainstPromotionV2(outcome, promotion, *bundle); err != nil {
		return zero, err
	}
	actual, err := domain.ComputeProjectedDeltaV2Digest(outcome.ActualDelta)
	if err != nil {
		return zero, err
	}
	projected, err := domain.ComputeProjectedDeltaV2Digest(bundle.ProjectedDelta)
	if err != nil {
		return zero, err
	}
	if actual != projected || outcome.ActualPostStateRoot != generation.BaseStateRoot {
		return zero, fmt.Errorf("chronology accepted source actual delta/root differs from its projection")
	}
	canon := outcome.ActualCanonRoot
	publication, err := s.loadCharacterMemoryPublicationUnlocked(outcome.ReceiptDigest)
	if err != nil {
		return zero, err
	}
	if publication != nil {
		canon = publication.AfterCanonRoot
	}
	if canon != generation.BaseCanonRoot {
		return zero, fmt.Errorf("chronology accepted source differs from the generation's frozen canon root")
	}
	return *bundle.ChapterWorldSimulation.PhysicalState, nil
}

func (s *ProjectedStoreV2) captureChronologySource(generation domain.PlanningGenerationV2, source domain.PlanningSourceSnapshotV2) (projectedChronologySource, error) {
	value := projectedChronologySource{Version: projectedChronologySourceVersion, GenerationID: generation.GenerationID, SourceSnapshotDigest: source.SnapshotDigest}
	inventory, root, err := CaptureProjectAllFoundationSnapshot(s.io.dir)
	if err != nil {
		return value, err
	}
	if root != source.FoundationSnapshotRoot {
		return value, fmt.Errorf("chronology source capsule is missing and current source no longer matches frozen foundation root; source recovery is required")
	}
	value.Foundation = inventory
	if generation.BaseCanonChapter == 0 {
		if generation.FirstProjectedChapter != 1 {
			return value, fmt.Errorf("chronology initial source has an invalid first chapter")
		}
		value.Characters, err = s.readChronologySourceOriginal("characters.json", inventory)
		if err != nil {
			return value, err
		}
		value.Registry, err = s.readChronologySourceOriginal("meta/character_agents/registry.json", inventory)
		if err != nil {
			return value, err
		}
	} else {
		cursor, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return value, err
		}
		if cursor == nil || cursor.LastAcceptedChapter != generation.BaseCanonChapter || cursor.LastOutcomeReceiptDigest == "" {
			return value, fmt.Errorf("chronology source capture requires the exact currently accepted predecessor cursor")
		}
		if err := s.validateRealizationControlUnlocked(*cursor); err != nil {
			return value, err
		}
		live := NewStore(s.io.dir)
		progress, err := live.Progress.Load()
		if err != nil {
			return value, err
		}
		if progress == nil || !slices.Contains(progress.CompletedChapters, generation.BaseCanonChapter) {
			return value, fmt.Errorf("chronology source predecessor has no accepted body")
		}
		simulation, err := live.LoadChapterWorldSimulation(generation.BaseCanonChapter)
		if err != nil {
			return value, err
		}
		if simulation == nil {
			return value, fmt.Errorf("chronology source predecessor simulation is missing")
		}
		bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(simulation.GenerationID), generation.BaseCanonChapter)
		if err != nil {
			return value, err
		}
		if bundle == nil || !jsonValuesEqual(bundle.ChapterWorldSimulation, *simulation) {
			return value, fmt.Errorf("chronology source simulation differs from accepted sealed source")
		}
		value.Accepted = &projectedChronologyAcceptedSource{GenerationID: simulation.GenerationID, Chapter: generation.BaseCanonChapter, BundleDigest: bundle.BundleDigest, OutcomeDigest: cursor.LastOutcomeReceiptDigest}
		var outcome domain.ActualOutcomeReceiptV2
		if err := s.readJSONUnlocked(projectedActualOutcomePath(simulation.GenerationID, generation.BaseCanonChapter, cursor.LastOutcomeReceiptDigest), &outcome); err != nil {
			return value, err
		}
		publication, err := s.loadCharacterMemoryPublicationUnlocked(outcome.ReceiptDigest)
		if err != nil {
			return value, err
		}
		if publication != nil {
			complete, err := inspectCharacterMemoryPublicationFiles(s.io, publication.Files)
			if err != nil {
				return value, err
			}
			if !complete {
				return value, fmt.Errorf("chronology source capture requires fully published accepted character memory")
			}
		}
		rel := filepath.Join("chapters", fmt.Sprintf("%02d.md", generation.BaseCanonChapter))
		if err := validateCharacterMemoryPublicationPath(s.io, rel); err != nil {
			return value, err
		}
		body, err := s.io.ReadFileUnlocked(rel)
		if err != nil {
			return value, err
		}
		commit := NewCheckpointStore(newIO(s.io.dir)).LatestByStep(domain.ChapterScope(generation.BaseCanonChapter), "commit")
		if characterMemoryPublicationSHA(body) != outcome.ChapterBodySHA256 || commit == nil || commit.Seq != outcome.CommitCheckpointSeq || commit.Digest != outcome.ChapterBodySHA256 {
			return value, fmt.Errorf("chronology source capture lacks its exact body/commit acceptance")
		}
	}
	value.Digest, err = projectedChronologySourceDigest(value)
	return value, err
}

func (s *ProjectedStoreV2) validateChronologySource(generation domain.PlanningGenerationV2, source domain.PlanningSourceSnapshotV2, value projectedChronologySource) (domain.WorldPhysicalStateV2, error) {
	var zero domain.WorldPhysicalStateV2
	if value.Version != projectedChronologySourceVersion || value.GenerationID != generation.GenerationID || value.SourceSnapshotDigest != source.SnapshotDigest {
		return zero, fmt.Errorf("chronology source capsule identity mismatch")
	}
	root, err := ComputeProjectAllFoundationSnapshotRoot(value.Foundation)
	if err != nil {
		return zero, err
	}
	if root != source.FoundationSnapshotRoot {
		return zero, fmt.Errorf("chronology source capsule changed its frozen foundation inventory")
	}
	digest, err := projectedChronologySourceDigest(value)
	if err != nil || digest != value.Digest {
		return zero, fmt.Errorf("chronology source capsule digest mismatch")
	}
	if generation.BaseCanonChapter == 0 {
		if value.Accepted != nil {
			return zero, fmt.Errorf("chapter-zero chronology source cannot claim accepted history")
		}
		return chronologyInitialSource(value)
	}
	if value.Accepted == nil || len(value.Characters)+len(value.Registry) > 0 {
		return zero, fmt.Errorf("chronology accepted source capsule is incomplete or mixed")
	}
	return s.chronologyAcceptedSource(generation, *value.Accepted)
}

// On a building generation a missing capsule may be reconstructed only while
// its actual files still match the old frozen inventory root. Readers validate
// this in memory; a successful write/seal freezes the original evidence first.
// Sealed generations never fall back to later live canon.
func (s *ProjectedStoreV2) validateProjectedChronologySourceUnlocked(base string, generation domain.PlanningGenerationV2, bundle domain.ProjectedChapterBundle, persist bool) error {
	opening := bundle.CharacterOpeningStimulus()
	if opening == nil || !domain.HasCharacterSelfChronologyPolicyV1(opening.Sources) {
		return nil
	}
	if bundle.Chapter != generation.FirstProjectedChapter {
		first, err := s.loadBundleAtUnlocked(base, generation.FirstProjectedChapter)
		if err != nil {
			return err
		}
		if first == nil {
			return fmt.Errorf("chronology chain lacks its first source-bound bundle")
		}
		if err := s.validateProjectedChronologySourceUnlocked(base, generation, *first, persist); err != nil {
			return err
		}
		previous, err := s.loadBundleAtUnlocked(base, bundle.Chapter-1)
		if err != nil {
			return err
		}
		if previous == nil || previous.ChapterWorldSimulation.PhysicalState == nil {
			return fmt.Errorf("chronology opening lacks its preceding projected source")
		}
		return domain.ValidateCharacterChronologyOpeningFromSourceV1(*previous.ChapterWorldSimulation.PhysicalState, *opening)
	}
	source, err := s.loadSourceAtUnlocked(base)
	if err != nil {
		return err
	}
	if err := domain.ValidatePlanningSourceSnapshotAgainstGenerationV2(*source, generation); err != nil {
		return err
	}
	var capsule projectedChronologySource
	path := filepath.Join(base, projectedChronologySourceFile)
	err = s.readJSONUnlocked(path, &capsule)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if missing {
		if generation.Status == domain.PlanningGenerationSealedV2 {
			return fmt.Errorf("sealed chronology generation lacks its frozen original source capsule")
		}
		capsule, err = s.captureChronologySource(generation, *source)
		if err != nil {
			return err
		}
	}
	raw, err := s.validateChronologySource(generation, *source, capsule)
	if err != nil {
		return err
	}
	if err := domain.ValidateCharacterChronologyOpeningFromSourceV1(raw, *opening); err != nil {
		return err
	}
	if missing && persist {
		payload, err := json.MarshalIndent(capsule, "", "  ")
		if err != nil {
			return err
		}
		if err := s.io.writeFileNoReplaceUnlocked(path, payload); err != nil {
			if !os.IsExist(err) {
				return err
			}
			actual, err := s.io.ReadFileUnlocked(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(actual, payload) {
				return fmt.Errorf("immutable chronology source capsule conflict")
			}
		}
	}
	return nil
}

func (s *ProjectedStoreV2) copyChronologySourceUnlocked(from, to string) error {
	rel := filepath.Join(from, projectedChronologySourceFile)
	if err := validateCharacterMemoryPublicationPath(s.io, rel); err != nil {
		return err
	}
	raw, err := s.io.ReadFileUnlocked(rel)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.io.WriteFileUnlocked(filepath.Join(to, projectedChronologySourceFile), raw)
}
