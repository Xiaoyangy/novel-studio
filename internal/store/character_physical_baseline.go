package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const ProjectAllAcceptedCharacterBaselinePath = "meta/runtime/accepted_character_baseline.json"
const ProjectAllAcceptedCharacterBaselineVersion = "project-all-accepted-character-baseline.v1"

// A generation-bound server snapshot. It is created only from a validated
// accepted bundle, and never copied between isolated planning generations.
type ProjectAllAcceptedCharacterBaseline struct {
	Version             string                           `json:"version"`
	TargetGenerationID  string                           `json:"target_generation_id"`
	BaseCanonChapter    int                              `json:"base_canon_chapter"`
	BaseStateRoot       string                           `json:"base_state_root"`
	SourceGenerationID  string                           `json:"source_generation_id"`
	SourceBundleDigest  string                           `json:"source_bundle_digest"`
	SourceOutcomeDigest string                           `json:"source_outcome_digest"`
	PhysicalState       *domain.WorldPhysicalStateV2     `json:"physical_state"`
	StoryClock          *domain.StoryClockContext        `json:"story_clock,omitempty"`
	StoryTime           *domain.StoryTimeChapterSchedule `json:"story_time,omitempty"`
	Digest              string                           `json:"digest"`
}

func FinalizeProjectAllAcceptedCharacterBaseline(value ProjectAllAcceptedCharacterBaseline) (ProjectAllAcceptedCharacterBaseline, error) {
	if value.Version != ProjectAllAcceptedCharacterBaselineVersion || value.BaseCanonChapter <= 0 || value.PhysicalState == nil {
		return value, fmt.Errorf("accepted character baseline identity/state is incomplete")
	}
	for _, generation := range []string{value.TargetGenerationID, value.SourceGenerationID} {
		if err := validateArcCycleGenerationID(generation); err != nil {
			return value, err
		}
	}
	for _, digest := range []string{value.BaseStateRoot, value.SourceBundleDigest, value.SourceOutcomeDigest} {
		if err := validateArcCycleDigest("accepted baseline binding", digest); err != nil {
			return value, err
		}
	}
	state, err := domain.FinalizeWorldPhysicalStateV2(*value.PhysicalState)
	if err != nil {
		return value, err
	}
	value.PhysicalState = &state
	if value.StoryClock != nil {
		if err := domain.ValidateStoryClockContext(*value.StoryClock); err != nil {
			return value, err
		}
	}
	if (value.StoryClock == nil) != (value.StoryTime == nil) {
		return value, fmt.Errorf("accepted baseline clock/schedule must be bound together")
	}
	if value.StoryTime != nil {
		if err := domain.ValidateStoryTimeForClock(value.BaseCanonChapter, value.StoryTime, value.StoryClock); err != nil {
			return value, err
		}
	}
	value.Digest = ""
	value.Digest, err = domain.DeterministicPlanningHash(value)
	return value, err
}

// LoadProjectAllAcceptedCharacterBaseline does not consult a live workspace.
// Preparation/recovery authenticates the manifest against the live generation;
// this read binds its frozen snapshot to the exact projected context identity.
func (s *Store) LoadProjectAllAcceptedCharacterBaseline(generationID string, nextChapter int, stateRoot string) (*ProjectAllAcceptedCharacterBaseline, error) {
	var value ProjectAllAcceptedCharacterBaseline
	baselineRaw, err := s.Progress.io.ReadFile(ProjectAllAcceptedCharacterBaselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(baselineRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("accepted character baseline contains trailing JSON")
	}
	expected := value.Digest
	finalized, err := FinalizeProjectAllAcceptedCharacterBaseline(value)
	if err != nil {
		return nil, err
	}
	if expected == "" || finalized.Digest != expected || finalized.TargetGenerationID != generationID || finalized.BaseCanonChapter+1 != nextChapter || finalized.BaseStateRoot != stateRoot {
		return nil, fmt.Errorf("accepted character baseline digest/generation/chapter/state root mismatch")
	}
	var manifest struct {
		Version                         string `json:"version"`
		GenerationID                    string `json:"generation_id"`
		BaseChapter                     int    `json:"base_chapter"`
		Workspace                       string `json:"workspace"`
		IsolatedWrites                  bool   `json:"isolated_writes"`
		AcceptedCharacterBaselineDigest string `json:"accepted_character_baseline_digest"`
	}
	raw, err := s.Progress.io.ReadFile("meta/project_all_workspace_manifest.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != "project-all-workspace.v3" || manifest.GenerationID != generationID || manifest.BaseChapter != finalized.BaseCanonChapter || filepath.Clean(manifest.Workspace) != filepath.Clean(s.Dir()) || !manifest.IsolatedWrites || manifest.AcceptedCharacterBaselineDigest != expected {
		return nil, fmt.Errorf("accepted character baseline is not bound to this isolated workspace manifest")
	}
	return &finalized, nil
}
