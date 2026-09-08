package store

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// AcceptedPlanningWindowBoundaryV1 is a read-only result reconstructed from
// immutable formal bundles and every actual chapter acceptance. It is not a
// serialized claim that can replace those original sources on reload.
type AcceptedPlanningWindowBoundaryV1 struct {
	Generation  domain.PlanningGenerationV2
	LastBundle  domain.ProjectedChapterBundle
	LastOutcome domain.ActualOutcomeReceiptV2
	Predecessor domain.ProjectedPlanningPredecessorContractV2
}

func (s *ProjectedStoreV2) ValidatePlanningDetailWindowSourcesV1(generation domain.PlanningGenerationV2, source domain.PlanningSourceSnapshotV2) error {
	return s.withProjectedReadLock(func() error {
		return s.validatePlanningDetailWindowSourcesUnlocked(generation, source)
	})
}

func (s *ProjectedStoreV2) validatePlanningDetailWindowSourcesUnlocked(generation domain.PlanningGenerationV2, source domain.PlanningSourceSnapshotV2) error {
	window := generation.DetailWindow
	if window == nil {
		if source.DetailWindow != nil {
			return fmt.Errorf("planning source snapshot detail window differs from legacy generation")
		}
		return nil
	}
	if err := domain.ValidatePlanningDetailWindowV1(generation); err != nil {
		return err
	}
	if err := domain.ValidatePlanningSourceSnapshotAgainstGenerationV2(source, generation); err != nil {
		return err
	}
	store := NewStore(s.io.dir)
	report, input, err := store.LoadVerifiedArcRehearsal(window.RehearsalDigest)
	if err != nil {
		return fmt.Errorf("detail window requires its verified whole-arc rehearsal: %w", err)
	}
	if report == nil || input == nil || !report.ReadyForDetail || report.Authority != "speculative" ||
		report.ReportDigest != window.RehearsalDigest || report.InputDigest != window.RehearsalInputDigest || input.InputDigest != window.RehearsalInputDigest {
		return fmt.Errorf("detail window lacks the exact ready whole-arc rehearsal/input")
	}
	sourceRoot, err := domain.ComputeArcRehearsalSourceRootV1(source.FoundationSnapshotRoot, source.RAGSnapshotRoot)
	if err != nil {
		return err
	}
	if report.ArcID != window.ArcID || input.ArcID != window.ArcID ||
		report.ArcFirstChapter != window.ArcFirstChapter || input.ArcFirstChapter != window.ArcFirstChapter ||
		report.ArcLastChapter != window.ArcLastChapter || input.ArcLastChapter != window.ArcLastChapter ||
		report.BaseCanonChapter != generation.BaseCanonChapter || input.BaseCanonChapter != generation.BaseCanonChapter ||
		report.BaseCanonRoot != generation.BaseCanonRoot || input.BaseCanonRoot != generation.BaseCanonRoot ||
		report.SourceRoot != sourceRoot || input.SourceRoot != sourceRoot {
		return fmt.Errorf("detail window rehearsal differs from its logical arc, accepted base or exact source roots")
	}
	// Accepted canon naturally advances after sealing. Historical sealed
	// windows keep their immutable source binding; new/building work must also
	// prove the current author files still match the original rehearsal input.
	if generation.Status == domain.PlanningGenerationBuildingV2 {
		if err := store.ValidateArcRehearsalInputFresh(*input); err != nil {
			return fmt.Errorf("detail window rehearsal source drift: %w", err)
		}
	}
	if window.AcceptedPredecessor == nil {
		return nil
	}
	if err := validateProjectedPathComponent("parent_generation_id", generation.ParentGenerationID); err != nil {
		return err
	}
	var previous domain.PlanningGenerationV2
	if err := s.readJSONUnlocked(filepath.Join(projectedSealedGenerationPath(generation.ParentGenerationID), projectedGenerationManifestFile), &previous); err != nil {
		return fmt.Errorf("detail window accepted predecessor generation: %w", err)
	}
	if err := domain.ValidatePlanningGenerationV2(previous); err != nil {
		return err
	}
	// Check strict backward progress before recursively authenticating the
	// predecessor's own sources, so a tampered parent cycle cannot recurse.
	if previous.Status != domain.PlanningGenerationSealedV2 || previous.GenerationID != generation.ParentGenerationID ||
		previous.BaseCanonChapter >= generation.BaseCanonChapter || previous.LastProjectedChapter != generation.BaseCanonChapter ||
		previous.DetailWindow == nil || previous.ScopeID != generation.ScopeID ||
		previous.BookHorizonChapter != generation.BookHorizonChapter ||
		previous.DetailWindow.ArcFirstChapter != window.ArcFirstChapter || previous.DetailWindow.ArcLastChapter != window.ArcLastChapter {
		return fmt.Errorf("detail window predecessor is not an earlier contiguous window of the same logical arc")
	}
	boundary, err := s.loadAcceptedPlanningWindowBoundaryUnlocked(generation.ParentGenerationID)
	if err != nil {
		return err
	}
	if boundary.LastOutcome.ReceiptDigest != window.AcceptedOutcomeDigest || boundary.LastOutcome.ActualPostStateRoot != generation.BaseStateRoot ||
		!jsonValuesEqual(boundary.Predecessor, *window.AcceptedPredecessor) {
		return fmt.Errorf("detail window accepted predecessor does not match original bundle/outcome evidence")
	}
	return nil
}

func (s *ProjectedStoreV2) validatePlanningDetailWindowRegistryUnlocked(generation domain.PlanningGenerationV2, registry domain.ObligationRegistryV2, initialCreate bool) error {
	if generation.DetailWindow == nil || generation.DetailWindow.AcceptedPredecessor == nil {
		return nil
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return err
	}
	if registry.GenerationID != generation.GenerationID || registry.ProjectionScope != generation.ProjectionScope || registry.ScopeID != generation.ScopeID ||
		registry.BookHorizonChapter != generation.BookHorizonChapter || registry.FirstChapter != generation.FirstProjectedChapter || registry.LastChapter != generation.LastProjectedChapter {
		return fmt.Errorf("detail window obligation scope differs from generation")
	}
	base := projectedSealedGenerationPath(generation.ParentGenerationID)
	var previous domain.PlanningGenerationV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), &previous); err != nil {
		return err
	}
	previousRegistry, err := s.loadRegistryAtUnlocked(base)
	if err != nil {
		return err
	}
	initial := generation
	initial.Status, initial.ProjectedChapterCount = domain.PlanningGenerationBuildingV2, 0
	initial.ChainHeadRoot, initial.ChainTailRoot, initial.SealedAt = "", "", ""
	initial.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(initial)
	if err != nil {
		return err
	}
	_, expected, err := domain.CarryForwardArcObligationsV2(previous, *previousRegistry, initial)
	if err != nil {
		return err
	}
	if initialCreate {
		if !jsonValuesEqual(expected, registry) {
			return fmt.Errorf("detail window initial registry differs from the accepted predecessor's carried obligations")
		}
		return nil
	}
	// An in-flight chapter transaction may have written its next registry
	// before advancing the generation manifest. The existing intent protocol
	// verifies that transition; do not mistake its temporary root/count lag
	// for a lost inherited obligation while recovering it.
	current := map[string]domain.ObligationV2{}
	for _, obligation := range registry.Obligations {
		current[obligation.ID] = obligation
	}
	for _, original := range expected.Obligations {
		actual, exists := current[original.ID]
		if !exists || actual.Kind != original.Kind || actual.Contract != original.Contract || actual.Hardness != original.Hardness ||
			!jsonValuesEqual(actual.Origin, original.Origin) || !jsonValuesEqual(actual.DueWindow, original.DueWindow) ||
			!jsonValuesEqual(actual.ConsumerChapters, original.ConsumerChapters) {
			return fmt.Errorf("detail window changed the source identity, contract or deadline of carried obligation %s", original.ID)
		}
	}
	return nil
}

func (s *ProjectedStoreV2) LoadAcceptedPlanningWindowBoundaryV1(generationID string) (*AcceptedPlanningWindowBoundaryV1, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*AcceptedPlanningWindowBoundaryV1, error) {
		return s.loadAcceptedPlanningWindowBoundaryUnlocked(generationID)
	})
}

func (s *ProjectedStoreV2) loadAcceptedPlanningWindowBoundaryUnlocked(generationID string) (*AcceptedPlanningWindowBoundaryV1, error) {
	if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
		return nil, err
	}
	generation, err := s.loadGenerationAtUnlocked(projectedSealedGenerationPath(generationID))
	if err != nil || generation == nil {
		return nil, fmt.Errorf("accepted planning window generation is unavailable: %w", err)
	}
	if !domain.IsArcPlanningGenerationV2(*generation) {
		return nil, fmt.Errorf("accepted planning window requires an arc-scoped generation")
	}
	arc := NewStore(s.io.dir).ArcCycle()
	if err := arc.ValidateArcCycle(generationID); err != nil {
		return nil, fmt.Errorf("accepted planning window body/review evidence: %w", err)
	}
	manifests, err := arc.ListArcPlanningManifests(generationID)
	if err != nil || len(manifests) != 1 {
		return nil, fmt.Errorf("accepted planning window requires one immutable manifest: %w", err)
	}
	manifest := manifests[0]
	if manifest.ArcID != generation.ScopeID || manifest.FirstChapter != generation.FirstProjectedChapter || manifest.LastChapter != generation.LastProjectedChapter ||
		manifest.BookLastChapter != generation.BookHorizonChapter || manifest.FullOutlineDigest != generation.StableOutlineRoot ||
		!jsonValuesEqual(manifest.DetailWindow, generation.DetailWindow) {
		return nil, fmt.Errorf("accepted planning window manifest differs from its formal generation")
	}
	acceptances, err := arc.ListChapterAcceptanceReceipts(generationID)
	if err != nil || len(acceptances) != generation.ExpectedChapterCount {
		return nil, fmt.Errorf("planning window lacks its complete ordered chapter acceptance chain: %w", err)
	}
	var result AcceptedPlanningWindowBoundaryV1
	result.Generation = *generation
	preStateRoot := generation.BaseStateRoot
	for i, acceptance := range acceptances {
		chapter := generation.FirstProjectedChapter + i
		if acceptance.Chapter != chapter {
			return nil, fmt.Errorf("planning window acceptance order differs at chapter %d", chapter)
		}
		bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(generationID), chapter)
		if err != nil {
			return nil, err
		}
		if bundle.BundleDigest != manifest.Chapters[i].BundleDigest {
			return nil, fmt.Errorf("planning window manifest does not bind chapter %d original bundle", chapter)
		}
		capacity := bundle.ChapterPlan.CausalSimulation.RenderCapacity
		if capacity == nil {
			return nil, fmt.Errorf("planning window chapter %d has no formal render capacity", chapter)
		}
		if err := capacity.Validate(manifest.ChapterBodyRunes.MinRunes, manifest.ChapterBodyRunes.MaxRunes); err != nil {
			return nil, err
		}
		capacityHash, err := domain.DeterministicPlanningHash(*capacity)
		if err != nil || "sha256:"+strings.TrimPrefix(capacityHash, "sha256:") != manifest.Chapters[i].CapacityDigest {
			return nil, fmt.Errorf("planning window chapter %d render capacity differs from manifest", chapter)
		}
		var outcome domain.ActualOutcomeReceiptV2
		if err := s.readJSONUnlocked(projectedActualOutcomePath(generationID, chapter, acceptance.OutcomeReceiptDigest), &outcome); err != nil {
			return nil, err
		}
		valid, err := s.hasExactActualOutcomeReceiptUnlocked(generationID, chapter, acceptance.OutcomeReceiptDigest, outcome.PromotionReceiptDigest)
		if err != nil || !valid {
			return nil, fmt.Errorf("planning window chapter %d lacks original promotion/outcome evidence: %w", chapter, err)
		}
		if outcome.ActualPreStateRoot != preStateRoot || !outcome.ProjectionMatch || outcome.ActualPostStateRoot != bundle.ProjectedPostStateRoot ||
			outcome.ChapterBodySHA256 != acceptance.ChapterBodySHA256 {
			return nil, fmt.Errorf("planning window chapter %d accepted outcome/body/state chain differs", chapter)
		}
		preStateRoot = outcome.ActualPostStateRoot
		result.LastBundle, result.LastOutcome = *bundle, outcome
	}
	contract := result.LastBundle.ChapterPlan.CausalSimulation.ArcTransition
	result.Predecessor = domain.ProjectedPlanningPredecessorContractV2{
		Chapter: result.LastBundle.Chapter, OutgoingConsequenceID: contract.OutgoingConsequenceID, OutgoingConsequenceText: contract.OutgoingConsequenceText,
		BundleDigest: result.LastBundle.BundleDigest, ProjectedPostStateRoot: result.LastOutcome.ActualPostStateRoot,
	}
	return &result, nil
}
