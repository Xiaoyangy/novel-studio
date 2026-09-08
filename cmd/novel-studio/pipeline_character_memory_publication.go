package main

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func pipelineCharacterMemoryCanonRoot(st *store.Store, overrides map[string]string) (string, error) {
	if st == nil {
		return "", fmt.Errorf("character memory canon root requires a store")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return "", err
	}
	root, err := pipelineCanonRootWithCharacterMemoryOverrides(st.Dir(), progress, overrides)
	if err != nil {
		return "", err
	}
	return pipelineProjectAllCanonRootFromSnapshot(root), nil
}

func preparePipelineAcceptedCharacterMemoryPublication(st *store.Store, bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) error {
	if bundle.ChapterWorldSimulation.Version < 2 {
		return nil
	}
	existing, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
	if err != nil {
		return err
	}
	if existing != nil {
		return validatePipelineAcceptedCharacterMemoryPublication(st, &outcome, false)
	}
	candidate, err := st.PrepareAcceptedCharacterMemoryCandidate(bundle, outcome)
	if err != nil {
		return err
	}
	if candidate == nil {
		return fmt.Errorf("accepted character memory candidate is missing")
	}
	before, err := pipelineCharacterMemoryCanonRoot(st, candidate.BeforeOverrides())
	if err != nil {
		return err
	}
	if before != outcome.ActualCanonRoot {
		return fmt.Errorf("character memory candidate before canon root differs from accepted outcome")
	}
	after, err := pipelineCharacterMemoryCanonRoot(st, candidate.AfterOverrides())
	if err != nil {
		return err
	}
	manifest, err := st.PrepareCharacterMemoryPublication(candidate, before, after)
	if err != nil {
		return err
	}
	if manifest == nil {
		return fmt.Errorf("accepted character memory publication was not durably prepared")
	}
	return validatePipelineAcceptedCharacterMemoryPublication(st, &outcome, false)
}

func applyPipelineAcceptedCharacterMemoryPublication(st *store.Store, bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) error {
	if bundle.ChapterWorldSimulation.Version < 2 {
		return nil
	}
	manifest, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
	if err != nil {
		return err
	}
	if manifest == nil {
		return fmt.Errorf("accepted character memory lacks a prepared publication; resume with the original protocol version or use an explicit successor/rebase, never regenerate accepted history")
	}
	if manifest.BundleDigest != bundle.BundleDigest {
		return fmt.Errorf("character memory publication belongs to another sealed bundle")
	}
	if err := validatePipelineAcceptedCharacterMemoryPublication(st, &outcome, false); err != nil {
		return err
	}
	if err := st.ApplyPreparedCharacterMemoryPublication(manifest); err != nil {
		return err
	}
	return validatePipelineAcceptedCharacterMemoryPublication(st, &outcome, true)
}

// Both reconstructions cover the entire canonical tree. Overriding only the
// manifest's whitelisted exact memory files permits a prepared partial write,
// but cannot hide an unrelated chapter, world or character-profile mutation.
func validatePipelineAcceptedCharacterMemoryPublication(st *store.Store, outcome *domain.ActualOutcomeReceiptV2, requireApplied bool) error {
	if outcome == nil {
		return nil
	}
	manifest, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
	if err != nil {
		return err
	}
	if manifest == nil {
		return nil
	} // Historical outcomes retain their original root contract.
	if manifest.OutcomeReceiptDigest != outcome.ReceiptDigest || manifest.GenerationID != outcome.GenerationID || manifest.Chapter != outcome.Chapter || manifest.BeforeCanonRoot != outcome.ActualCanonRoot {
		return fmt.Errorf("character memory publication does not bind the accepted outcome")
	}
	applied, err := st.InspectCharacterMemoryPublication(manifest)
	if err != nil {
		return err
	}
	if requireApplied && !applied {
		return fmt.Errorf("accepted character memory publication is incomplete; resume the exact accepted chapter before advancing")
	}
	before, err := pipelineCharacterMemoryCanonRoot(st, manifest.BeforeOverrides())
	if err != nil {
		return err
	}
	after, err := pipelineCharacterMemoryCanonRoot(st, manifest.AfterOverrides())
	if err != nil {
		return err
	}
	if before != manifest.BeforeCanonRoot || after != manifest.AfterCanonRoot {
		return fmt.Errorf("canonical state drift outside the exact prepared character memory publication")
	}
	return nil
}

func pipelineOutcomePublishedCanonRoot(projected *store.ProjectedStoreV2, outcome *domain.ActualOutcomeReceiptV2) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("accepted outcome is required")
	}
	manifest, err := projected.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
	if err != nil {
		return "", err
	}
	if manifest == nil {
		return outcome.ActualCanonRoot, nil
	}
	if manifest.OutcomeReceiptDigest != outcome.ReceiptDigest || manifest.GenerationID != outcome.GenerationID || manifest.Chapter != outcome.Chapter || manifest.BeforeCanonRoot != outcome.ActualCanonRoot {
		return "", fmt.Errorf("published character memory canon root does not bind the accepted outcome")
	}
	return manifest.AfterCanonRoot, nil
}

func validatePipelineCharacterMemoryBeforeSource(st *store.Store, progress *domain.Progress) error {
	projected := st.ProjectedV2()
	cursor, err := projected.LoadRealizationCursor()
	if err != nil {
		return err
	}
	if cursor == nil || cursor.LastOutcomeReceiptDigest == "" {
		return nil
	} // Earlier non-project-all canon.
	if cursor.LastAcceptedChapter != progress.LatestCompleted() {
		return fmt.Errorf("canonical source progress differs from last accepted outcome")
	}
	generation, err := projected.LoadSealedGeneration(cursor.ActiveGenerationID)
	if err != nil {
		return err
	}
	if generation == nil {
		return fmt.Errorf("canonical source has no sealed accepted generation")
	}
	return validatePipelineProjectAllLiveCanonForPromotion(st.Dir(), progress, projected, cursor, generation)
}

// A committed render transaction predates the receipt-derived memory write.
// Only the exact accepted body/commit/promotion may reconstruct that earlier
// root. Prepared partial files are allowed so normal acceptance recovery can
// finish them; an unknown file state or unrelated canon mutation is not.
func validatePipelineCommittedCanonWithMemoryPublication(outputDir string, frozen *pipelineFrozenPlan, bodySHA string, commit *domain.Checkpoint, committedRoot, currentRawRoot string) error {
	if committedRoot == domain.PlanningV2DigestPrefix+currentRawRoot {
		return nil
	}
	if frozen == nil || commit == nil || frozen.ProjectionBinding != "sealed_v2" {
		return fmt.Errorf("committed canonical root drifted")
	}
	st := store.NewStore(outputDir)
	cursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil {
		return err
	}
	if cursor == nil || cursor.ActiveGenerationID != frozen.PlanningGenerationID || cursor.LastAcceptedChapter != frozen.Chapter || cursor.LastOutcomeReceiptDigest == "" {
		return fmt.Errorf("committed canonical root drifted without an exact accepted outcome")
	}
	outcome, err := st.ProjectedV2().LoadActualOutcomeReceipt(frozen.PlanningGenerationID, frozen.Chapter, cursor.LastOutcomeReceiptDigest)
	if err != nil {
		return err
	}
	if outcome == nil || !outcome.ProjectionMatch || outcome.ChapterBodySHA256 != bodySHA || outcome.CommitCheckpointSeq != commit.Seq || commit.Digest != bodySHA || outcome.PromotionReceiptDigest != frozen.PromotionReceiptDigest {
		return fmt.Errorf("committed memory recovery outcome does not bind exact body/commit/promotion")
	}
	manifest, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
	if err != nil {
		return err
	}
	if manifest == nil || manifest.BundleDigest != frozen.ProjectedBundleDigest {
		return fmt.Errorf("committed canonical root drifted without its exact memory publication")
	}
	if err := validatePipelineAcceptedCharacterMemoryPublication(st, outcome, false); err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	before, err := pipelineCanonRootWithCharacterMemoryOverrides(outputDir, progress, manifest.BeforeOverrides())
	if err != nil {
		return err
	}
	if committedRoot != domain.PlanningV2DigestPrefix+before {
		return fmt.Errorf("memory publication before-state differs from committed canonical root")
	}
	return nil
}
