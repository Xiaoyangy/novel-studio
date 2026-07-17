package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestProjectAllSuccessorArcCarriesAcceptedCanonRootIntoFirstPromotion(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()

	arcOne, arcOneRegistry := projectAllCmdTestArcGeneration(
		t,
		"v001-a001",
		1,
		2,
		"",
		projectAllCmdTestDigest("successor-arc-genesis-canon"),
		projectAllCmdTestDigest("successor-arc-genesis-state"),
	)
	sealedArcOne, arcOneBundle := projectAllCmdTestProjectAndSealSingleChapterArc(
		t,
		projected,
		arcOne,
		arcOneRegistry,
	)
	_, arcOneCursor, err := projected.ActivateSealedGeneration(sealedArcOne.GenerationID, nil)
	if err != nil {
		t.Fatalf("activate first arc: %v", err)
	}
	arcOnePromotion := projectAllCmdTestPromotion(t, arcOneBundle)
	if _, err := projected.Promote(*arcOneCursor, arcOnePromotion); err != nil {
		t.Fatalf("promote first arc chapter: %v", err)
	}
	promotedArcOneCursor, err := projected.LoadRealizationCursor()
	if err != nil || promotedArcOneCursor == nil {
		t.Fatalf("load first arc promoted cursor: cursor=%+v err=%v", promotedArcOneCursor, err)
	}
	arcOneOutcome := projectAllCmdTestOutcome(t, arcOneBundle, arcOnePromotion, 1)
	if _, err := projected.AcceptOutcome(*promotedArcOneCursor, arcOneOutcome); err != nil {
		t.Fatalf("accept first arc terminal outcome: %v", err)
	}
	completedArcOneCursor, err := projected.LoadRealizationCursor()
	if err != nil || completedArcOneCursor == nil {
		t.Fatalf("load completed first arc cursor: cursor=%+v err=%v", completedArcOneCursor, err)
	}
	if completedArcOneCursor.LastAcceptedChapter != sealedArcOne.LastProjectedChapter ||
		completedArcOneCursor.LastOutcomeReceiptDigest != arcOneOutcome.ReceiptDigest {
		t.Fatalf("first arc did not close on its exact terminal outcome: %+v", completedArcOneCursor)
	}

	arcTwo, _ := projectAllCmdTestArcGeneration(
		t,
		"v001-a002",
		2,
		2,
		sealedArcOne.GenerationID,
		arcOneOutcome.ActualCanonRoot,
		arcOneOutcome.ActualPostStateRoot,
	)
	sealedArcOneRegistry, err := projected.LoadObligationRegistry(sealedArcOne.GenerationID)
	if err != nil || sealedArcOneRegistry == nil {
		t.Fatalf("load first arc obligation registry: registry=%+v err=%v", sealedArcOneRegistry, err)
	}
	arcTwo, arcTwoRegistry, err := domain.CarryForwardArcObligationsV2(
		*sealedArcOne,
		*sealedArcOneRegistry,
		arcTwo,
	)
	if err != nil {
		t.Fatalf("carry first arc boundary into successor: %v", err)
	}
	if err := projected.ResetProjectionCursorForRestart(arcTwo.GenerationID); err != nil {
		t.Fatalf("select successor projection cursor: %v", err)
	}
	sealedArcTwo, arcTwoBundle := projectAllCmdTestProjectAndSealSingleChapterArc(
		t,
		projected,
		arcTwo,
		arcTwoRegistry,
	)

	_, arcTwoCursor, err := projected.ActivateSealedGeneration(sealedArcTwo.GenerationID, nil)
	if err != nil {
		t.Fatalf("activate successor arc: %v", err)
	}
	if arcTwoCursor.LastAcceptedChapter != sealedArcTwo.BaseCanonChapter ||
		arcTwoCursor.LastOutcomeReceiptDigest != arcOneOutcome.ReceiptDigest {
		t.Fatalf("successor activation did not carry predecessor outcome: %+v", arcTwoCursor)
	}
	expectedCanonRoot, err := pipelineProjectAllExpectedCanonRoot(
		projected,
		arcTwoCursor,
		sealedArcTwo,
	)
	if err != nil {
		t.Fatalf("successor arc rejected its accepted predecessor canon baseline: %v", err)
	}
	if expectedCanonRoot != arcOneOutcome.ActualCanonRoot ||
		expectedCanonRoot != sealedArcTwo.BaseCanonRoot {
		t.Fatalf(
			"successor canon baseline mismatch: got=%s outcome=%s generation=%s",
			expectedCanonRoot,
			arcOneOutcome.ActualCanonRoot,
			sealedArcTwo.BaseCanonRoot,
		)
	}

	arcTwoPromotion := projectAllCmdTestPromotion(t, arcTwoBundle)
	if _, err := projected.Promote(*arcTwoCursor, arcTwoPromotion); err != nil {
		t.Fatalf("promote successor arc first chapter: %v", err)
	}
	promotedArcTwoCursor, err := projected.LoadRealizationCursor()
	if err != nil || promotedArcTwoCursor == nil {
		t.Fatalf("load successor promoted cursor: cursor=%+v err=%v", promotedArcTwoCursor, err)
	}
	if promotedArcTwoCursor.ActiveGenerationID != sealedArcTwo.GenerationID ||
		promotedArcTwoCursor.ActivePromotedChapter != sealedArcTwo.FirstProjectedChapter ||
		promotedArcTwoCursor.LastOutcomeReceiptDigest != arcOneOutcome.ReceiptDigest {
		t.Fatalf("successor first promotion lost the carried arc boundary: %+v", promotedArcTwoCursor)
	}
}

func projectAllCmdTestArcGeneration(
	t *testing.T,
	scopeID string,
	chapter int,
	bookHorizon int,
	parentGenerationID string,
	baseCanonRoot string,
	baseStateRoot string,
) (domain.PlanningGenerationV2, domain.ObligationRegistryV2) {
	t.Helper()
	outlineRoot := projectAllCmdTestDigest("successor-arc-stable-outline")
	dependencyRoot := projectAllCmdTestDigest("successor-arc-dependencies-" + scopeID)
	seedRoot, err := domain.ComputePlanningSeedContractRootV2("successor arc regression seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		outlineRoot,
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
		BookHorizonChapter: bookHorizon,
		FirstChapter:       chapter,
		LastChapter:        chapter,
		Obligations:        []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		ParentGenerationID:     parentGenerationID,
		ProjectionScope:        domain.PlanningProjectionScopeArcV2,
		ScopeID:                scopeID,
		BookHorizonChapter:     bookHorizon,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       chapter - 1,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		AttemptID:              scopeID,
		FirstProjectedChapter:  chapter,
		LastProjectedChapter:   chapter,
		ExpectedChapterCount:   1,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              "2026-07-17T00:00:00Z",
	}
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
		t.Fatalf("arc generation fixture invalid: %v", err)
	}
	if err := domain.ValidateObligationRegistryAgainstGenerationV2(generation, registry); err != nil {
		t.Fatalf("arc obligation registry fixture invalid: %v", err)
	}
	return generation, registry
}

func projectAllCmdTestProjectAndSealSingleChapterArc(
	t *testing.T,
	projected *store.ProjectedStoreV2,
	generation domain.PlanningGenerationV2,
	registry domain.ObligationRegistryV2,
) (*domain.PlanningGenerationV2, domain.ProjectedChapterBundle) {
	t.Helper()
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generation.GenerationID,
		BaseCanonChapter:       generation.BaseCanonChapter,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("successor-arc-foundation-" + generation.ScopeID),
		RAGSnapshotRoot:        projectAllCmdTestDigest("successor-arc-rag-" + generation.ScopeID),
		CapturedAt:             "2026-07-17T00:00:00Z",
	}
	var err error
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatalf("create %s building generation: %v", generation.ScopeID, err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatalf("initialize %s projection cursor: %v", generation.ScopeID, err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(
		t,
		generation.GenerationID,
		generation.FirstProjectedChapter,
	)
	projectAllCmdTestBindPlanningContext(
		t,
		artifacts,
		generation,
		nil,
		registry,
		generation.FirstProjectedChapter,
	)
	previous, preState, err := pipelineProjectAllTail(generation, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		previous,
		preState,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatalf("build %s projected chapter: %v", generation.ScopeID, err)
	}
	nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.ProjectChapterAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		generation.ObligationRegistryRoot,
		*cursor,
		bundle,
		nextRegistry,
	); err != nil {
		t.Fatalf("persist %s projected chapter: %v", generation.ScopeID, err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatalf("seal %s: %v", generation.ScopeID, err)
	}
	sealed, err := projected.LoadSealedGeneration(generation.GenerationID)
	if err != nil || sealed == nil {
		t.Fatalf("load sealed %s: generation=%+v err=%v", generation.ScopeID, sealed, err)
	}
	return sealed, bundle
}
