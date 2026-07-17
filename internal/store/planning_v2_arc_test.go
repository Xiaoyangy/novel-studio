package store

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectedStoreV2RejectsArcRegistryScopeMismatchAtCreate(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, _ := projectedStoreV2ArcFixture(t, "v001-a001", 1, 10)
	registry.ProjectionScope = ""
	registry.ScopeID = ""
	registry.BookHorizonChapter = 0
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	// Bind the mismatched registry root so this test proves scope identity is
	// checked independently of the ordinary root equality guard.
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err == nil ||
		!strings.Contains(err.Error(), "identity/scope/root") {
		t.Fatalf("arc generation accepted a legacy-scoped registry: %v", err)
	}
}

func TestProjectedStoreV2PrepareCarriedArcGenerationFromSealedPredecessor(t *testing.T) {
	root := t.TempDir()
	projected := NewStore(root).ProjectedV2()
	previous, source, registry, bundles := projectedStoreV2ArcFixture(
		t,
		"v001-a001",
		2,
		10,
	)
	if err := projected.CreateBuildingGeneration(previous, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(previous.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	current := previous
	for _, bundle := range bundles {
		cursor, err = projected.ProjectBundleAndAdvance(
			current.GenerationDigest,
			current.ChainTailRoot,
			*cursor,
			bundle,
		)
		if err != nil {
			t.Fatalf("project predecessor chapter %d: %v", bundle.Chapter, err)
		}
		loaded, loadErr := projected.LoadBuildingGeneration(previous.GenerationID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		current = *loaded
	}
	if _, err := projected.SealGeneration(previous.GenerationID); err != nil {
		t.Fatalf("seal predecessor arc: %v", err)
	}

	next, nextSource := projectedStoreV2NextArcFixture(
		t,
		previous,
		bundles[len(bundles)-1].ProjectedPostStateRoot,
		"v001-a002",
		6,
		7,
	)
	rebound, carried, err := projected.PrepareCarriedArcGeneration(previous.GenerationID, next)
	if err != nil {
		t.Fatalf("prepare carried arc generation: %v", err)
	}
	if carried.GenerationID != rebound.GenerationID ||
		carried.ScopeID != "v001-a002" ||
		carried.FirstChapter != 6 || carried.LastChapter != 7 ||
		carried.BookHorizonChapter != 10 ||
		len(carried.Obligations) != 0 ||
		rebound.ObligationRegistryRoot != carried.RegistryRoot {
		t.Fatalf("prepared arc identity mismatch: generation=%+v registry=%+v", rebound, carried)
	}
	if err := projected.CreateBuildingGeneration(rebound, nextSource, carried); err != nil {
		t.Fatalf("prepared pair was not accepted by atomic generation create: %v", err)
	}
}

func projectedStoreV2ArcFixture(
	t *testing.T,
	scopeID string,
	count int,
	horizon int,
) (
	domain.PlanningGenerationV2,
	domain.PlanningSourceSnapshotV2,
	domain.ObligationRegistryV2,
	[]domain.ProjectedChapterBundle,
) {
	t.Helper()
	generation, source, registry, _ := projectedStoreV2FixtureWithAttempt(t, count, scopeID)
	generation.ProjectionScope = domain.PlanningProjectionScopeArcV2
	generation.ScopeID = scopeID
	generation.BookHorizonChapter = horizon
	registry.ProjectionScope = generation.ProjectionScope
	registry.ScopeID = generation.ScopeID
	registry.BookHorizonChapter = generation.BookHorizonChapter
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]domain.ProjectedChapterBundle, 0, count)
	previousDigest := genesis
	preState := generation.BaseStateRoot
	for chapter := generation.FirstProjectedChapter; chapter <= generation.LastProjectedChapter; chapter++ {
		planningContext, deriveErr := domain.DeriveProjectedPlanningContextV2(
			generation,
			bundles,
			registry,
			chapter,
		)
		if deriveErr != nil {
			t.Fatal(deriveErr)
		}
		bundle := projectedStoreV2Bundle(
			t,
			generation.GenerationID,
			chapter,
			previousDigest,
			preState,
			planningContext.ContextDigest,
			planningContext.PredecessorContract,
			false,
		)
		bundles = append(bundles, bundle)
		previousDigest = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot
	}
	return generation, source, registry, bundles
}

func projectedStoreV2NextArcFixture(
	t *testing.T,
	previous domain.PlanningGenerationV2,
	baseStateRoot string,
	scopeID string,
	first int,
	last int,
) (domain.PlanningGenerationV2, domain.PlanningSourceSnapshotV2) {
	t.Helper()
	baseCanonRoot := projectedStoreV2Digest("canon-after-" + previous.ScopeID)
	dependencyRoot := projectedStoreV2Digest("dependencies-" + scopeID)
	seedRoot, err := domain.ComputePlanningSeedContractRootV2("deterministic next arc seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		previous.StableOutlineRoot,
		dependencyRoot,
		seedRoot,
		scopeID,
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := domain.ObligationRegistryV2{
		Version:            domain.ObligationRegistryV2Version,
		GenerationID:       generationID,
		ProjectionScope:    domain.PlanningProjectionScopeArcV2,
		ScopeID:            scopeID,
		BookHorizonChapter: previous.BookHorizonChapter,
		FirstChapter:       first,
		LastChapter:        last,
		Obligations:        []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		ParentGenerationID:     previous.GenerationID,
		ProjectionScope:        domain.PlanningProjectionScopeArcV2,
		ScopeID:                scopeID,
		BookHorizonChapter:     previous.BookHorizonChapter,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       first - 1,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      previous.StableOutlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		AttemptID:              scopeID,
		FirstProjectedChapter:  first,
		LastProjectedChapter:   last,
		ExpectedChapterCount:   last - first + 1,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              projectedStoreV2Time(),
	}
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generationID,
		BaseCanonChapter:       generation.BaseCanonChapter,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FoundationSnapshotRoot: projectedStoreV2Digest("foundation-" + scopeID),
		RAGSnapshotRoot:        projectedStoreV2Digest("rag-" + scopeID),
		CapturedAt:             projectedStoreV2Time(),
	}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	return generation, source
}
