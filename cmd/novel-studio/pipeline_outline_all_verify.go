package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func verifyPipelineOutlineAllStage(
	outputDir string,
	evidence domain.PipelineStageEvidence,
) (domain.PipelineStageEvidence, error) {
	artifacts, err := verifyPipelineOutlineAllReceiptAndArtifacts(outputDir)
	evidence.Artifacts = append(evidence.Artifacts, artifacts...)
	if err != nil {
		return evidence, err
	}
	evidence.Message = "full-book outline contracts published and verified"
	return evidence, nil
}

func verifyPipelineOutlineAllReceiptAndArtifacts(outputDir string) ([]string, error) {
	_, release, err := acquirePublishedOutlineAllStageAtOutput(outputDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	return verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(outputDir)
}

func verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(outputDir string) ([]string, error) {
	st := store.NewStore(outputDir)
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("outline-all verifier requires published execution receipt: %w", err)
	}
	if receipt.Status != domain.OutlineAllExecutionComplete || receipt.PendingAction != nil {
		return nil, fmt.Errorf("outline-all verifier found incomplete execution status=%s", receipt.Status)
	}
	expectedAttemptID, err := pipelineOutlineAllAttemptIDFromReceipt(st, receipt)
	if err != nil {
		return nil, fmt.Errorf("outline-all replay attempt identity: %w", err)
	}
	if receipt.AttemptID != expectedAttemptID {
		return nil, fmt.Errorf("outline-all attempt id does not match its source/model/prompt identity")
	}
	expectedCandidate, err := filepath.Abs(pipelineOutlineAllCandidatePath(outputDir, receipt.AttemptID))
	if err != nil {
		return nil, err
	}
	if filepath.Clean(receipt.CandidateDir) != filepath.Clean(expectedCandidate) {
		return nil, fmt.Errorf("outline-all receipt candidate directory is outside deterministic namespace")
	}

	compass, err := st.Outline.LoadCompass()
	if err != nil || compass == nil {
		return nil, fmt.Errorf("outline-all verifier requires compass: %w", err)
	}
	if len(compass.NonNegotiables) == 0 {
		return nil, fmt.Errorf("outline-all verifier refuses empty compass.non_negotiables")
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(*compass)
	if err != nil || compassDigest != receipt.CompassDigest {
		return nil, fmt.Errorf("outline-all compass digest drift")
	}
	mode, err := st.LoadWritingPipelineMode()
	if err != nil || mode == nil || mode.Mode != receipt.WritingMode || mode.ReceiptDigest != receipt.WritingModeReceiptDigest {
		return nil, fmt.Errorf("outline-all writing-mode receipt drift")
	}
	target := domain.BookScaleTarget{
		Range: domain.BookScaleRange{
			MinVolumes: receipt.MinVolumes, MaxVolumes: receipt.MaxVolumes,
			MinChapters: receipt.MinChapters, MaxChapters: receipt.MaxChapters,
		},
		TargetVolumes: receipt.TargetVolumes, TargetChapters: receipt.TargetChapters,
		TargetWords: receipt.TargetWords, TargetWordsPerChapter: receipt.TargetWordsPerChapter,
		StoryTimeHint: receipt.StoryTimeHint,
	}
	if err := target.Range.Validate(); err != nil {
		return nil, err
	}
	resolved, err := domain.ResolveBookScaleTarget(compass.EstimatedScale, 0, 0)
	if err != nil || resolved.Range != target.Range ||
		resolved.TargetVolumes != target.TargetVolumes || resolved.TargetChapters != target.TargetChapters ||
		resolved.TargetWords != target.TargetWords || resolved.TargetWordsPerChapter != target.TargetWordsPerChapter ||
		resolved.StoryTimeHint != target.StoryTimeHint {
		return nil, fmt.Errorf("outline-all deterministic scale target drift")
	}
	volumes, err := validatePipelineOutlineAllFinal(st, *compass, target)
	if err != nil {
		return nil, err
	}
	layeredDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil || layeredDigest != receipt.FinalLayeredDigest {
		return nil, fmt.Errorf("outline-all final layered digest drift")
	}
	flat := domain.FlattenOutline(volumes)
	flatDigest, err := domain.ComputeFlatOutlineDigest(flat)
	if err != nil || flatDigest != receipt.FinalFlatDigest {
		return nil, fmt.Errorf("outline-all final flat digest drift")
	}
	if err := validatePipelineOutlineAllOperationChain(st, receipt); err != nil {
		return nil, err
	}
	for rel, want := range map[string]string{
		"meta/architect_readiness.json": receipt.ArchitectReadinessJSONDigest,
		"meta/architect_readiness.md":   receipt.ArchitectReadinessMDDigest,
	} {
		got, err := pipelineRequiredFileSHA(outputDir, rel)
		if err != nil || got != want {
			return nil, fmt.Errorf("outline-all refreshed readiness digest drift at %s", rel)
		}
	}
	if ok, reason := architectReadinessState(outputDir); !ok {
		return nil, fmt.Errorf("outline-all refreshed architect readiness is invalid: %s", reason)
	}

	// Before any downstream zero-init artifact exists, re-prove the entire
	// chapter-zero isolation baseline. Later stages legitimately add world and
	// simulation state, while the receipt-backed outline/readiness remain fixed.
	if !pipelineZeroInitEvidenceExists(outputDir) {
		if err := validatePipelineOutlineAllEntry(st); err != nil {
			return nil, err
		}
		progress, err := st.Progress.Load()
		if err != nil {
			return nil, err
		}
		if err := domain.ValidateOutlineAllChapterZeroProgress(progress, *receipt); err != nil {
			return nil, err
		}
		if current, err := pipelineOutlineAllProtectedCanonRoot(outputDir); err != nil {
			return nil, err
		} else if current != receipt.ProtectedCanonRoot {
			return nil, fmt.Errorf("outline-all published protected canon drift")
		}
		if err := validatePipelineOutlineAllStableInputs(outputDir, receipt.StableProgressRoot, receipt.FoundationContextRoot); err != nil {
			return nil, err
		}
	}

	artifacts := []string{
		"layered_outline.json", "layered_outline.md", "outline.json", "outline.md",
		store.OutlineAllExecutionReceiptPath,
		"meta/architect_readiness.json", "meta/architect_readiness.md",
		pipelineOutlineAllRequirementPath,
	}
	if repair, err := loadPipelineOutlineRepairEvidence(st, receipt.AttemptID, receipt.SourceSnapshotRoot); err != nil {
		return nil, err
	} else if repair != nil {
		artifacts = append(artifacts, pipelineOutlineRepairIntentPath, pipelineOutlineRepairReceiptPath)
	}
	if receipt.CompletedActionCount > 0 {
		artifacts = append(artifacts,
			pipelineOutlineAllOperationIntentPath(receipt.CompletedActionCount),
			pipelineOutlineAllOperationReceiptPath(receipt.CompletedActionCount),
		)
	}
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact) == "" {
			return nil, fmt.Errorf("outline-all verifier produced an empty artifact path")
		}
	}
	return artifacts, nil
}
