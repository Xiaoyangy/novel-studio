package store

import (
	"fmt"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const arcWindowAggregateDirV1 = "meta/planning/v3/arc_cycle/window_aggregates"

func arcWindowAggregatePathV1(generationID, digest string) string {
	return filepath.Join(arcWindowAggregateDirV1, generationID, digest+".json")
}

// CompleteWindowedArcV1 writes only an immutable aggregate receipt, never a
// chapter, review, acceptance, manifest or planning generation. IDs select the
// exact ancestor chain; neither IDs nor their count define the logical arc.
func (s *Store) CompleteWindowedArcV1(generationIDs []string, completedAt string) (*domain.ArcWindowAggregateCompletionV1, error) {
	projected, arc := s.ProjectedV2(), s.ArcCycle()
	return withProjectedReadResult(projected, func() (*domain.ArcWindowAggregateCompletionV1, error) {
		windows, err := projected.loadArcWindowAggregateEvidenceUnlocked(generationIDs)
		if err != nil {
			return nil, err
		}
		receipt, err := domain.NewArcWindowAggregateCompletionV1(windows, completedAt)
		if err != nil {
			return nil, err
		}
		// Lock hierarchy is Projected -> ArcCycle. Source loading above may use
		// ArcCycle readers; once holding its write lock, use unlocked ArcCycle
		// checks only. In particular do not re-enter the boundary loader here.
		err = arc.withWriteLock(func() error {
			if err := arc.validateArcWindowAggregateArtifactsUnlocked(windows); err != nil {
				return err
			}
			return arc.writeContentAddressedJSONUnlocked(arcWindowAggregatePathV1(receipt.FinalGenerationID, receipt.ReceiptDigest), receipt)
		})
		if err != nil {
			return nil, err
		}
		return &receipt, nil
	})
}

// LoadVerifiedWindowedArcCompletionV1 authenticates the receipt and every
// original source again. A prior successful completion is not a validation cache.
func (s *Store) LoadVerifiedWindowedArcCompletionV1(finalGenerationID, digest string) (*domain.ArcWindowAggregateCompletionV1, error) {
	if err := validateArcCycleGenerationID(finalGenerationID); err != nil {
		return nil, err
	}
	if err := validateArcCycleDigest("aggregate_digest", digest); err != nil {
		return nil, err
	}
	projected, arc := s.ProjectedV2(), s.ArcCycle()
	return withProjectedReadResult(projected, func() (*domain.ArcWindowAggregateCompletionV1, error) {
		var receipt domain.ArcWindowAggregateCompletionV1
		if err := arc.withReadLock(func() error {
			return arc.readJSONStrictUnlocked(arcWindowAggregatePathV1(finalGenerationID, digest), &receipt)
		}); err != nil {
			return nil, err
		}
		if err := domain.ValidateArcWindowAggregateCompletionV1(receipt); err != nil {
			return nil, err
		}
		if receipt.FinalGenerationID != finalGenerationID || receipt.ReceiptDigest != digest {
			return nil, fmt.Errorf("window aggregate content-addressed identity differs")
		}
		ids := make([]string, len(receipt.Windows))
		for i, window := range receipt.Windows {
			ids[i] = window.GenerationID
		}
		windows, err := projected.loadArcWindowAggregateEvidenceUnlocked(ids)
		if err != nil {
			return nil, err
		}
		if err := domain.ValidateArcWindowAggregateCompletionAgainstWindowsV1(receipt, windows); err != nil {
			return nil, err
		}
		if err := arc.withReadLock(func() error { return arc.validateArcWindowAggregateArtifactsUnlocked(windows) }); err != nil {
			return nil, err
		}
		return &receipt, nil
	})
}

func (s *ProjectedStoreV2) loadArcWindowAggregateEvidenceUnlocked(generationIDs []string) ([]domain.ArcWindowCompletionEvidenceV1, error) {
	if len(generationIDs) < 2 {
		return nil, fmt.Errorf("window aggregate requires at least two selected windows")
	}
	seen := map[string]bool{}
	for _, id := range generationIDs {
		if err := validateArcCycleGenerationID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("window aggregate contains duplicate generation %s", id)
		}
		seen[id] = true
	}
	// Follow original parent fields from the selected final generation back to
	// the bound ArcFirst, then require the selection to equal that exact chain.
	// Read/validate manifests without source recursion during this first pass.
	var reverse []string
	var next *domain.PlanningGenerationV2
	id := generationIDs[len(generationIDs)-1]
	seen = map[string]bool{}
	for {
		if err := validateArcCycleGenerationID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("window aggregate original parent chain cycles")
		}
		seen[id] = true
		var generation domain.PlanningGenerationV2
		if err := s.readJSONUnlocked(filepath.Join(projectedSealedGenerationPath(id), projectedGenerationManifestFile), &generation); err != nil {
			return nil, err
		}
		if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
			return nil, err
		}
		window := generation.DetailWindow
		if generation.GenerationID != id || generation.Status != domain.PlanningGenerationSealedV2 || window == nil {
			return nil, fmt.Errorf("window aggregate requires original sealed detail-window generations")
		}
		if next == nil {
			if generation.LastProjectedChapter != window.ArcLastChapter {
				return nil, fmt.Errorf("window aggregate selected final window is not the logical arc end")
			}
		} else if generation.LastProjectedChapter != next.FirstProjectedChapter-1 || generation.ScopeID != next.ScopeID ||
			window.ArcFirstChapter != next.DetailWindow.ArcFirstChapter || window.ArcLastChapter != next.DetailWindow.ArcLastChapter || generation.BookHorizonChapter != next.BookHorizonChapter {
			return nil, fmt.Errorf("window aggregate original parent chain changes arc or leaves a gap")
		}
		reverse = append(reverse, id)
		if generation.FirstProjectedChapter == window.ArcFirstChapter {
			break
		}
		id, next = generation.ParentGenerationID, &generation
	}
	if len(reverse) != len(generationIDs) {
		return nil, fmt.Errorf("window aggregate selection omits original logical-arc windows")
	}
	for i, id := range generationIDs {
		if id != reverse[len(reverse)-1-i] {
			return nil, fmt.Errorf("window aggregate selection differs from original parent chain")
		}
	}
	arc := NewStore(s.io.dir).ArcCycle()
	var windows []domain.ArcWindowCompletionEvidenceV1
	for _, id := range generationIDs {
		boundary, err := s.loadAcceptedPlanningWindowBoundaryUnlocked(id)
		if err != nil {
			return nil, err
		}
		base := projectedSealedGenerationPath(id)
		registry, err := s.loadRegistryAtUnlocked(base)
		if err != nil {
			return nil, err
		}
		bundles, err := s.loadBundlesAtUnlocked(base)
		if err != nil {
			return nil, err
		}
		manifests, err := arc.ListArcPlanningManifests(id)
		if err != nil || len(manifests) != 1 {
			return nil, fmt.Errorf("window aggregate requires one original manifest: %w", err)
		}
		acceptances, err := arc.ListChapterAcceptanceReceipts(id)
		if err != nil {
			return nil, err
		}
		evidence := domain.ArcWindowCompletionEvidenceV1{Generation: boundary.Generation, Manifest: manifests[0], Registry: *registry, Bundles: bundles, Acceptances: acceptances}
		for _, acceptance := range acceptances {
			var outcome domain.ActualOutcomeReceiptV2
			if err := s.readJSONUnlocked(projectedActualOutcomePath(id, acceptance.Chapter, acceptance.OutcomeReceiptDigest), &outcome); err != nil {
				return nil, err
			}
			evidence.Outcomes = append(evidence.Outcomes, outcome)
		}
		windows = append(windows, evidence)
	}
	return windows, nil
}

func (s *ArcCycleStore) validateArcWindowAggregateArtifactsUnlocked(windows []domain.ArcWindowCompletionEvidenceV1) error {
	for _, window := range windows {
		manifests, err := s.listManifestsUnlocked(window.Generation.GenerationID)
		if err != nil || len(manifests) != 1 || !jsonValuesEqual(manifests[0], window.Manifest) {
			return fmt.Errorf("window aggregate original manifest drift: %w", err)
		}
		acceptances, err := s.listAcceptancesUnlocked(window.Generation.GenerationID)
		if err != nil || !jsonValuesEqual(acceptances, window.Acceptances) {
			return fmt.Errorf("window aggregate original acceptance drift: %w", err)
		}
		for _, acceptance := range acceptances {
			if err := s.validateAcceptedBodyUnlocked(acceptance, window.Manifest); err != nil {
				return err
			}
			if err := s.validateReviewArtifactsUnlocked(acceptance, window.Manifest); err != nil {
				return err
			}
		}
	}
	return nil
}
