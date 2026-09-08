package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineProjectAllAcceptedCharacterBaseline(liveOutputDir, generationID string, baseChapter int) (*store.ProjectAllAcceptedCharacterBaseline, error) {
	if baseChapter <= 0 {
		return nil, nil
	}
	live := store.NewStore(liveOutputDir)
	generation, err := live.ProjectedV2().LoadBuildingGeneration(generationID)
	if err != nil {
		return nil, err
	}
	if generation == nil {
		generation, err = live.ProjectedV2().LoadSealedGeneration(generationID)
		if err != nil {
			return nil, err
		}
	}
	if generation == nil || generation.CharacterAgentProtocol != domain.CharacterAgentDecisionProtocolV2Version {
		return nil, nil
	}
	if generation.BaseCanonChapter != baseChapter || generation.FirstProjectedChapter != baseChapter+1 {
		return nil, fmt.Errorf("physical baseline does not match the new generation chapter boundary")
	}
	bundle, err := live.LoadAcceptedCharacterAgentBundle(baseChapter)
	if err != nil {
		return nil, err
	}
	if bundle.ProjectedPostStateRoot != generation.BaseStateRoot {
		return nil, fmt.Errorf("accepted physical state root differs from new generation baseline")
	}
	cursor, err := live.ProjectedV2().LoadRealizationCursor()
	if err != nil {
		return nil, err
	}
	if cursor == nil || cursor.LastAcceptedChapter != baseChapter || cursor.LastOutcomeReceiptDigest == "" {
		return nil, fmt.Errorf("accepted physical baseline lost its outcome cursor")
	}
	stimulus := bundle.CharacterOpeningStimulus()
	if stimulus == nil {
		return nil, fmt.Errorf("accepted character baseline lacks its opening stimulus")
	}
	value, err := store.FinalizeProjectAllAcceptedCharacterBaseline(store.ProjectAllAcceptedCharacterBaseline{
		Version: store.ProjectAllAcceptedCharacterBaselineVersion, TargetGenerationID: generationID, BaseCanonChapter: baseChapter, BaseStateRoot: generation.BaseStateRoot,
		SourceGenerationID: bundle.GenerationID, SourceBundleDigest: bundle.BundleDigest, SourceOutcomeDigest: cursor.LastOutcomeReceiptDigest,
		PhysicalState: bundle.ChapterWorldSimulation.PhysicalState, StoryClock: stimulus.StoryClock, StoryTime: bundle.ChapterWorldSimulation.StoryTime,
	})
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func validatePipelineProjectAllAcceptedBaseline(workspace string, manifest pipelineProjectAllWorkspaceManifest, expected *store.ProjectAllAcceptedCharacterBaseline) error {
	if expected == nil {
		if manifest.AcceptedCharacterBaselineDigest != "" {
			return fmt.Errorf("unexpected accepted character baseline on a legacy/chapter-zero workspace")
		}
		if _, err := os.Lstat(filepath.Join(workspace, store.ProjectAllAcceptedCharacterBaselinePath)); !os.IsNotExist(err) {
			return fmt.Errorf("unexpected accepted physical baseline file: %v", err)
		}
		return nil
	}
	if manifest.AcceptedCharacterBaselineDigest != expected.Digest {
		return fmt.Errorf("workspace accepted physical baseline differs from verified live generation source")
	}
	actual, err := store.NewStore(workspace).LoadProjectAllAcceptedCharacterBaseline(manifest.GenerationID, manifest.BaseChapter+1, expected.BaseStateRoot)
	if err != nil {
		return err
	}
	if actual == nil || actual.Digest != expected.Digest {
		return fmt.Errorf("workspace accepted physical baseline is missing or changed")
	}
	return nil
}
