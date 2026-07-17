package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectedStoreV2BuildSealImmutableAndFactManifest(t *testing.T) {
	root := t.TempDir()
	projected := NewStore(root).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 2, true)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := generation
	for _, bundle := range bundles {
		cursor, err = projected.ProjectBundleAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			*cursor,
			bundle,
		)
		if err != nil {
			t.Fatalf("project chapter %d: %v", bundle.Chapter, err)
		}
		loaded, err := projected.LoadBuildingGeneration(generation.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		currentGeneration = *loaded
	}
	if cursor.LastProjectedChapter != bundles[1].Chapter ||
		cursor.LastBundleDigest != bundles[1].BundleDigest {
		t.Fatalf("projection cursor did not follow chain: %+v", cursor)
	}

	seal, err := projected.SealGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if seal.ReceiptDigest == "" {
		t.Fatal("seal receipt has no digest")
	}
	if _, err := os.Stat(filepath.Join(root, projectedBuildingGenerationPath(generation.GenerationID))); !os.IsNotExist(err) {
		t.Fatalf("building generation survived seal: %v", err)
	}
	manifest, err := projected.LoadProjectedChainManifest(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.FactReceiptDigests) != len(bundles) ||
		len(manifest.CraftReceiptDigests) != len(bundles) {
		t.Fatalf("seal manifest did not provide full dual-receipt coverage: %+v", manifest)
	}
	for i, bundle := range bundles {
		ragDigest, digestErr := domain.RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		craftDigest, digestErr := domain.CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		if manifest.Entries[i].RAGFactReceiptDigest != ragDigest ||
			manifest.Entries[i].CraftRecallReceiptDigest != craftDigest {
			t.Fatalf("seal manifest did not bind chapter %d dual receipts: %+v", bundle.Chapter, manifest.Entries[i])
		}
	}
	if err := projected.SaveProjectedChapterBundle(bundles[1]); err == nil ||
		!strings.Contains(err.Error(), "sealed") {
		t.Fatalf("sealed generation accepted in-place bundle write: %v", err)
	}
	for _, rel := range []string{"chapters", "drafts", "meta/progress.json"} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("projected store contaminated canon path %s: %v", rel, err)
		}
	}
	sealedBundlePath := filepath.Join(
		root,
		projectedBundlePath(projectedSealedGenerationPath(generation.GenerationID), bundles[0].Chapter),
	)
	raw, err := os.ReadFile(sealedBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var tampered map[string]any
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["authority"] = "canonical"
	raw, _ = json.MarshalIndent(tampered, "", "  ")
	if err := os.WriteFile(sealedBundlePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.LoadSealedGeneration(generation.GenerationID); err == nil {
		t.Fatal("sealed generation tampering was not detected")
	}
}

func TestProjectedStoreV2LoadMissingSealedGenerationReturnsNil(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, _, _, _ := projectedStoreV2Fixture(t, 1, false)
	got, err := projected.LoadSealedGeneration(generation.GenerationID)
	if err != nil {
		t.Fatalf("missing sealed generation returned an operational error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing sealed generation returned %+v", got)
	}
}

func TestProjectedStoreV2ResetProjectionCursorForSuccessorRestart(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	first, firstSource, firstRegistry, _ := projectedStoreV2Fixture(t, 1, false)
	if err := projected.CreateBuildingGeneration(first, firstSource, firstRegistry); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.InitializeProjectionCursor(first.GenerationID); err != nil {
		t.Fatal(err)
	}

	second, secondSource, secondRegistry, _ := projectedStoreV2FixtureWithAttempt(
		t,
		1,
		"successor-restart",
	)
	if err := projected.CreateBuildingGeneration(second, secondSource, secondRegistry); err != nil {
		t.Fatal(err)
	}
	if err := projected.ResetProjectionCursorForRestart(second.GenerationID); err != nil {
		t.Fatal(err)
	}
	if cursor, err := projected.LoadProjectionCursor(); err != nil || cursor != nil {
		t.Fatalf("predecessor cursor survived explicit restart: cursor=%+v err=%v", cursor, err)
	}
	_, cursor, err := projected.RecoverBuildingProjection(second.GenerationID)
	if err != nil {
		t.Fatalf("recover successor projection: %v", err)
	}
	if cursor == nil ||
		cursor.GenerationID != second.GenerationID ||
		cursor.NextProjectChapter != second.FirstProjectedChapter {
		t.Fatalf("successor cursor not reconstructed: %+v", cursor)
	}
	if err := projected.ResetProjectionCursorForRestart(second.GenerationID); err != nil {
		t.Fatal(err)
	}
	if preserved, err := projected.LoadProjectionCursor(); err != nil ||
		preserved == nil ||
		preserved.CursorDigest != cursor.CursorDigest {
		t.Fatalf("idempotent restart erased successor progress: cursor=%+v err=%v", preserved, err)
	}
}

func TestProjectedStoreV2RecoversBundleManifestCursorCrashWindow(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 2, false)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	initial, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CompareAndSwapProjectedChapterBundle(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		bundles[0],
	); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.LoadProjectionCursor(); err == nil ||
		!strings.Contains(err.Error(), "durable chain") {
		t.Fatalf("torn projection state was not detected: %v", err)
	}
	recoveredGeneration, recoveredCursor, err := projected.RecoverBuildingProjection(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredGeneration.ProjectedChapterCount != 1 ||
		recoveredCursor.LastProjectedChapter != bundles[0].Chapter ||
		recoveredCursor.LastBundleDigest != bundles[0].BundleDigest {
		t.Fatalf("projection recovery mismatch: generation=%+v cursor=%+v", recoveredGeneration, recoveredCursor)
	}
	if initial.CursorDigest == recoveredCursor.CursorDigest {
		t.Fatal("projection recovery did not advance cursor digest")
	}
	if realization, err := projected.LoadRealizationCursor(); err != nil || realization != nil {
		t.Fatalf("projection recovery mutated realization cursor: got=%+v err=%v", realization, err)
	}
}

func TestProjectedStoreV2ActivatePromoteOutcomeAndInvalidation(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 2, false)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := generation
	for _, bundle := range bundles {
		cursor, err = projected.ProjectBundleAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			*cursor,
			bundle,
		)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := projected.LoadBuildingGeneration(generation.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		currentGeneration = *loaded
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	active, realization, err := projected.ActivateSealedGeneration(generation.GenerationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if active.GenerationID != generation.GenerationID ||
		realization.NextPromoteChapter != bundles[0].Chapter {
		t.Fatalf("activation mismatch: active=%+v realization=%+v", active, realization)
	}
	replayedActive, replayedCursor, err := projected.ActivateSealedGeneration(generation.GenerationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replayedActive.RecordDigest != active.RecordDigest ||
		replayedCursor.CursorDigest != realization.CursorDigest {
		t.Fatal("exact activation replay changed control digests")
	}

	promotion := projectedStoreV2Promotion(t, bundles[0])
	if _, err := projected.Promote(*realization, promotion); err != nil {
		t.Fatal(err)
	}
	promoted, err := projected.LoadRealizationCursor()
	if err != nil {
		t.Fatal(err)
	}
	if promoted.ActivePromotedChapter != bundles[0].Chapter ||
		promoted.ActivePromotionReceiptDigest != promotion.ReceiptDigest {
		t.Fatalf("promotion did not bind receipt: %+v", promoted)
	}
	if _, err := projected.SavePromotionReceipt(promotion); err != nil {
		t.Fatalf("exact promotion replay failed: %v", err)
	}
	afterPromotionReplay, err := projected.LoadRealizationCursor()
	if err != nil || afterPromotionReplay.CursorDigest != promoted.CursorDigest {
		t.Fatalf("promotion replay changed cursor: got=%+v err=%v", afterPromotionReplay, err)
	}
	outcome := projectedStoreV2Outcome(t, bundles[0], promotion)
	if _, err := projected.AcceptOutcome(*promoted, outcome); err != nil {
		t.Fatal(err)
	}
	accepted, err := projected.LoadRealizationCursor()
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ActivePromotedChapter != 0 ||
		accepted.LastAcceptedChapter != bundles[0].Chapter ||
		accepted.LastOutcomeReceiptDigest != outcome.ReceiptDigest {
		t.Fatalf("accepted outcome did not advance exact control state: %+v", accepted)
	}
	if _, err := projected.SaveActualOutcomeReceipt(outcome); err != nil {
		t.Fatalf("exact outcome replay failed: %v", err)
	}
	afterOutcomeReplay, err := projected.LoadRealizationCursor()
	if err != nil || afterOutcomeReplay.CursorDigest != accepted.CursorDigest {
		t.Fatalf("outcome replay changed cursor: got=%+v err=%v", afterOutcomeReplay, err)
	}

	invalidation := domain.SuffixInvalidationReceiptV2{
		Version:            domain.SuffixInvalidationV2Version,
		GenerationID:       generation.GenerationID,
		FromChapter:        bundles[1].Chapter,
		ThroughChapter:     bundles[1].Chapter,
		CauseReceiptDigest: outcome.ReceiptDigest,
		Reason:             "accepted outcome changed the future suffix",
		InvalidatedAt:      projectedStoreV2Time(),
	}
	invalidation.ReceiptDigest, err = domain.ComputeSuffixInvalidationReceiptV2Digest(invalidation)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := projected.SaveSuffixInvalidationReceipt(invalidation)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := projected.SaveSuffixInvalidationReceipt(invalidation)
	if err != nil || firstDigest != secondDigest {
		t.Fatalf("exact invalidation replay was not idempotent: %s %s %v", firstDigest, secondDigest, err)
	}
	nextPromotion := projectedStoreV2Promotion(t, bundles[1])
	if _, err := projected.Promote(*accepted, nextPromotion); err == nil ||
		!strings.Contains(err.Error(), "invalidated") {
		t.Fatalf("invalidated chapter was promoted: %v", err)
	}
}

func TestProjectedStoreV2AbandonsOnlyDurablyInvalidatedActivePromotion(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, false)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.ProjectBundleAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		*cursor,
		bundles[0],
	); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	_, realization, err := projected.ActivateSealedGeneration(generation.GenerationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	promotion := projectedStoreV2Promotion(t, bundles[0])
	if _, err := projected.Promote(*realization, promotion); err != nil {
		t.Fatal(err)
	}
	promoted, err := projected.LoadRealizationCursor()
	if err != nil || promoted == nil {
		t.Fatalf("load promoted cursor: %+v err=%v", promoted, err)
	}
	invalidation := domain.SuffixInvalidationReceiptV2{
		Version:                 domain.SuffixInvalidationV2Version,
		GenerationID:            generation.GenerationID,
		FromChapter:             bundles[0].Chapter,
		ThroughChapter:          bundles[0].Chapter,
		CauseReceiptDigest:      promotion.ReceiptDigest,
		Reason:                  "sealed render proved this plan impossible",
		ReplacementGenerationID: "pg2_successor_for_abandon_test",
		InvalidatedAt:           projectedStoreV2Time(),
	}
	invalidation.ReceiptDigest, err =
		domain.ComputeSuffixInvalidationReceiptV2Digest(invalidation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.AbandonInvalidatedPromotion(*promoted, invalidation); err == nil {
		t.Fatal("active promotion was abandoned before its invalidation became durable")
	}
	if _, err := projected.SaveSuffixInvalidationReceipt(invalidation); err != nil {
		t.Fatal(err)
	}
	abandoned, err := projected.AbandonInvalidatedPromotion(*promoted, invalidation)
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.ActivePromotedChapter != 0 ||
		abandoned.ActivePromotionReceiptDigest != "" ||
		abandoned.LastAcceptedChapter != generation.BaseCanonChapter ||
		abandoned.NextPromoteChapter != bundles[0].Chapter {
		t.Fatalf("abandoned cursor changed accepted canon counters: %+v", abandoned)
	}
	if _, err := projected.Promote(*abandoned, promotion); err == nil ||
		!strings.Contains(err.Error(), "invalidated") {
		t.Fatalf("invalidated old promotion became executable again: %v", err)
	}
}

func TestProjectedStoreV2ProjectChapterRecoversRegistryFirstCrash(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 2, false)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	obligation := projectedStoreV2Obligation(t, generation.GenerationID)
	nextRegistry := registry
	nextRegistry.Obligations = []domain.ObligationV2{obligation}
	nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundles[0]
	planningContext, err := domain.DeriveProjectedPlanningContextV2(
		generation,
		nil,
		nextRegistry,
		bundle.Chapter,
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.PlanningContextDigest = planningContext.ContextDigest
	contextToken, err := domain.ProjectedPlanningContextSourceTokenV2(planningContext.ContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	bundle.ChapterWorldSimulation.Sources = append(bundle.ChapterWorldSimulation.Sources, contextToken)
	bundle.ChapterPlan.CausalSimulation.ContextSources = append(
		bundle.ChapterPlan.CausalSimulation.ContextSources,
		contextToken,
	)
	bundle.ObligationsCreated = []string{obligation.ID}
	bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{{
		StableID:  "obligation:" + obligation.ID,
		Subject:   obligation.ID,
		Field:     "state",
		Operation: "create",
		After:     "planned",
		Cause:     "本章选择产生跨章资源义务",
	}}
	bundle.ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(
		bundle.ProjectedPreStateRoot,
		bundle.ProjectedDelta,
	)
	if err != nil {
		t.Fatal(err)
	}
	projectedStoreV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a process dying after the new registry rename and before
	// generation.json/bundle/cursor. The high-level transaction must continue
	// from the original expected roots.
	if err := projected.io.WriteJSON(
		filepath.Join(projectedBuildingGenerationPath(generation.GenerationID), projectedObligationRegistryFile),
		nextRegistry,
	); err != nil {
		t.Fatal(err)
	}
	recoveredGeneration, recoveredCursor, err := projected.RecoverBuildingProjection(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredGeneration.ObligationRegistryRoot != nextRegistry.RegistryRoot ||
		recoveredCursor.LastProjectedChapter != generation.BaseCanonChapter {
		t.Fatalf("registry-first recovery did not preserve projection position: generation=%+v cursor=%+v",
			recoveredGeneration, recoveredCursor)
	}
	cursor = recoveredCursor
	nextCursor, err := projected.ProjectChapterAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		registry.RegistryRoot,
		*cursor,
		bundle,
		nextRegistry,
	)
	if err != nil {
		t.Fatal(err)
	}
	if nextCursor.LastProjectedChapter != bundle.Chapter ||
		nextCursor.LastBundleDigest != bundle.BundleDigest {
		t.Fatalf("chapter transaction did not advance cursor: %+v", nextCursor)
	}
	gotRegistry, err := projected.LoadObligationRegistry(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	gotGeneration, err := projected.LoadBuildingGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRegistry.RegistryRoot != nextRegistry.RegistryRoot ||
		gotGeneration.ObligationRegistryRoot != nextRegistry.RegistryRoot {
		t.Fatalf("registry/generation roots diverged: registry=%s generation=%s",
			gotRegistry.RegistryRoot, gotGeneration.ObligationRegistryRoot)
	}
}

func TestProjectedStoreV2ActivateSealedGenerationRepairsPartialControlWrite(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, false)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err = projected.ProjectBundleAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		*cursor,
		bundles[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = cursor
	seal, err := projected.SealGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	active := domain.ActivePlanningGenerationV2{
		Version:           domain.ActivePlanningGenerationV2Version,
		GenerationID:      generation.GenerationID,
		SealReceiptDigest: seal.ReceiptDigest,
		ActivatedAt:       projectedStoreV2Time(),
	}
	active.RecordDigest, err = domain.ComputeActivePlanningGenerationV2Digest(active)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.io.WriteJSON(projectedActiveGenerationPath, active); err != nil {
		t.Fatal(err)
	}
	gotActive, gotCursor, err := projected.ActivateSealedGeneration(generation.GenerationID, []int{9, 9, 7})
	if err != nil {
		t.Fatal(err)
	}
	if gotActive.RecordDigest != active.RecordDigest ||
		gotCursor.ActiveGenerationID != generation.GenerationID ||
		gotCursor.NextPromoteChapter != bundles[0].Chapter ||
		fmt.Sprint(gotCursor.BlockedByRewrites) != "[7 9]" {
		t.Fatalf("partial activation was not repaired: active=%+v cursor=%+v", gotActive, gotCursor)
	}
}

func TestProjectedStoreV2IntentRecoversEveryChapterTransactionRenameWindow(t *testing.T) {
	stages := []string{
		"intent_written",
		"registry_written",
		"registry_generation_written",
		"bundle_written",
		"bundle_generation_written",
		"cursor_written",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			projected := NewStore(t.TempDir()).ProjectedV2()
			generation, source, registry, bundles := projectedStoreV2Fixture(t, 2, false)
			if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
				t.Fatal(err)
			}
			cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
			if err != nil {
				t.Fatal(err)
			}
			obligation := projectedStoreV2Obligation(t, generation.GenerationID)
			nextRegistry := registry
			nextRegistry.Obligations = []domain.ObligationV2{obligation}
			nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
			if err != nil {
				t.Fatal(err)
			}
			bundle := bundles[0]
			planningContext, err := domain.DeriveProjectedPlanningContextV2(
				generation,
				nil,
				nextRegistry,
				bundle.Chapter,
			)
			if err != nil {
				t.Fatal(err)
			}
			bundle.PlanningContextDigest = planningContext.ContextDigest
			contextToken, tokenErr := domain.ProjectedPlanningContextSourceTokenV2(planningContext.ContextDigest)
			if tokenErr != nil {
				t.Fatal(tokenErr)
			}
			bundle.ChapterWorldSimulation.Sources = append(bundle.ChapterWorldSimulation.Sources, contextToken)
			bundle.ChapterPlan.CausalSimulation.ContextSources = append(
				bundle.ChapterPlan.CausalSimulation.ContextSources,
				contextToken,
			)
			bundle.ObligationsCreated = []string{obligation.ID}
			bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{{
				StableID:  "obligation:" + obligation.ID,
				Subject:   obligation.ID,
				Field:     "state",
				Operation: "create",
				After:     "planned",
				Cause:     "本章选择产生跨章资源义务",
			}}
			bundle.ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(
				bundle.ProjectedPreStateRoot,
				bundle.ProjectedDelta,
			)
			if err != nil {
				t.Fatal(err)
			}
			projectedStoreV2RebindRenderContext(t, &bundle)
			bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
			if err != nil {
				t.Fatal(err)
			}

			fired := false
			projected.testFault = func(current string) error {
				if !fired && current == stage {
					fired = true
					return errors.New("injected projected transaction crash")
				}
				return nil
			}
			if _, err := projected.ProjectChapterAndAdvance(
				generation.GenerationDigest,
				generation.ChainTailRoot,
				registry.RegistryRoot,
				*cursor,
				bundle,
				nextRegistry,
			); err == nil {
				t.Fatalf("stage %s did not interrupt transaction", stage)
			}
			if !fired {
				t.Fatalf("stage %s fault hook was not reached", stage)
			}
			projected.testFault = nil
			recoveredGeneration, recoveredCursor, err := projected.RecoverBuildingProjection(generation.GenerationID)
			if err != nil {
				t.Fatalf("stage %s recovery: %v", stage, err)
			}
			if recoveredGeneration.ProjectedChapterCount != 1 ||
				recoveredGeneration.ObligationRegistryRoot != nextRegistry.RegistryRoot ||
				recoveredCursor.LastProjectedChapter != bundle.Chapter ||
				recoveredCursor.LastBundleDigest != bundle.BundleDigest {
				t.Fatalf("stage %s recovered wrong state: generation=%+v cursor=%+v",
					stage, recoveredGeneration, recoveredCursor)
			}
			var pending []projectedChapterIntentV2
			if err := projected.withProjectedReadLock(func() error {
				var err error
				pending, err = projected.loadPendingProjectedChapterIntentsUnlocked(generation.GenerationID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Fatalf("stage %s left %d pending intents", stage, len(pending))
			}
			replayed, err := projected.ProjectChapterAndAdvance(
				generation.GenerationDigest,
				generation.ChainTailRoot,
				registry.RegistryRoot,
				*cursor,
				bundle,
				nextRegistry,
			)
			if err != nil || replayed.CursorDigest != recoveredCursor.CursorDigest {
				t.Fatalf("stage %s exact completed-intent replay failed: cursor=%+v err=%v",
					stage, replayed, err)
			}
		})
	}
}

func TestProjectedStoreV2SerializesTwoStoreInstancesOnSameIntent(t *testing.T) {
	root := t.TempDir()
	left := NewStore(root).ProjectedV2()
	right := NewStore(root).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, false)
	if err := left.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := left.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, projected := range []*ProjectedStoreV2{left, right} {
		wg.Add(1)
		go func(projected *ProjectedStoreV2) {
			defer wg.Done()
			_, err := projected.ProjectChapterAndAdvance(
				generation.GenerationDigest,
				generation.ChainTailRoot,
				registry.RegistryRoot,
				*cursor,
				bundles[0],
				registry,
			)
			errs <- err
		}(projected)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("same intent was not cross-instance idempotent: %v", err)
		}
	}
	gotBundles, err := left.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBundles) != 1 || gotBundles[0].BundleDigest != bundles[0].BundleDigest {
		t.Fatalf("concurrent stores produced wrong chain: %+v", gotBundles)
	}
}

func projectedStoreV2Fixture(
	t *testing.T,
	count int,
	withRAG bool,
) (domain.PlanningGenerationV2, domain.PlanningSourceSnapshotV2, domain.ObligationRegistryV2, []domain.ProjectedChapterBundle) {
	return projectedStoreV2FixtureForAttempt(t, count, withRAG, "")
}

func projectedStoreV2FixtureWithAttempt(
	t *testing.T,
	count int,
	attemptID string,
) (domain.PlanningGenerationV2, domain.PlanningSourceSnapshotV2, domain.ObligationRegistryV2, []domain.ProjectedChapterBundle) {
	return projectedStoreV2FixtureForAttempt(t, count, false, attemptID)
}

func projectedStoreV2FixtureForAttempt(
	t *testing.T,
	count int,
	withRAG bool,
	attemptID string,
) (domain.PlanningGenerationV2, domain.PlanningSourceSnapshotV2, domain.ObligationRegistryV2, []domain.ProjectedChapterBundle) {
	t.Helper()
	baseCanonRoot := projectedStoreV2Digest("canon")
	baseStateRoot := projectedStoreV2Digest("state")
	outlineRoot := projectedStoreV2Digest("outline")
	dependencyRoot := projectedStoreV2Digest("dependencies")
	seedRoot, err := domain.ComputePlanningSeedContractRootV2("deterministic test seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		outlineRoot,
		dependencyRoot,
		seedRoot,
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := domain.ObligationRegistryV2{
		Version:      domain.ObligationRegistryV2Version,
		GenerationID: generationID,
		FirstChapter: 4,
		LastChapter:  3 + count,
		Obligations:  []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       3,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		AttemptID:              attemptID,
		FirstProjectedChapter:  4,
		LastProjectedChapter:   3 + count,
		ExpectedChapterCount:   count,
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
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FoundationSnapshotRoot: projectedStoreV2Digest("foundation-snapshot"),
		RAGSnapshotRoot:        projectedStoreV2Digest("rag-snapshot"),
		CapturedAt:             projectedStoreV2Time(),
	}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	previous := genesis
	pre := baseStateRoot
	bundles := make([]domain.ProjectedChapterBundle, 0, count)
	for chapter := 4; chapter < 4+count; chapter++ {
		planningContext, err := domain.DeriveProjectedPlanningContextV2(
			generation,
			bundles,
			registry,
			chapter,
		)
		if err != nil {
			t.Fatal(err)
		}
		bundle := projectedStoreV2Bundle(
			t,
			generationID,
			chapter,
			previous,
			pre,
			planningContext.ContextDigest,
			planningContext.PredecessorContract,
			withRAG && chapter == 4,
		)
		bundles = append(bundles, bundle)
		previous = bundle.BundleDigest
		pre = bundle.ProjectedPostStateRoot
	}
	return generation, source, registry, bundles
}

func projectedStoreV2Bundle(
	t *testing.T,
	generationID string,
	chapter int,
	previous string,
	pre string,
	planningContextDigest string,
	predecessor *domain.ProjectedPlanningPredecessorContractV2,
	withRAG bool,
) domain.ProjectedChapterBundle {
	t.Helper()
	simulationID := fmt.Sprintf("sim-%s-%d", generationID, chapter)
	fullSimulation := domain.ChapterWorldSimulation{
		Version:      1,
		SimulationID: simulationID,
		Chapter:      chapter,
		GenerationID: generationID,
		TimeWindow:   "上午到中午",
		CharacterDecisions: []domain.CharacterWorldDecision{{
			Character:         "protagonist",
			Location:          "street",
			CurrentGoal:       "验证规则",
			Pressure:          "时间",
			KnowledgeBoundary: "只知道亲历结果",
			AvailableOptions:  []string{"先验证再扩张", "立即扩张"},
			Decision:          "先验证再扩张",
			DecisionReason:    "保留撤回空间",
			Action:            "执行小额试验",
			ActionDuration:    "三小时",
			CompletionState:   "completed",
			ImmediateResult:   "获得反馈",
			StateAfter:        "证据增加",
		}},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:      "protagonist",
			AvailableOptions: []string{"先验证再扩张", "立即扩张"},
			ChosenDecision:   "先验证再扩张",
			DecisionReason:   "保留撤回空间",
			PlanConstraints:  []string{"不得全知泄漏"},
			CausalChain:      []string{"额度出现→验证→反馈"},
		},
	}
	plan := domain.ChapterPlan{
		Chapter:  chapter,
		Title:    fmt.Sprintf("第%d章", chapter),
		Goal:     "完成可撤回的小范围试验",
		Conflict: "资源和信任同时受压",
		Hook:     "下一项真实选择到来",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"角色主动选择"},
			ForbiddenMoves:   []string{"不得替角色总结道理"},
			ContinuityChecks: []string{"资源数量连续"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID:   simulationID,
			ProtagonistDecision: "先验证再扩张",
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "额度出现",
				CharacterChoice: "小额验证",
				WorldResponse:   "规则结算",
				StoryResult:     "获得反馈",
			}},
			DecisionPoints: []string{"是否扩大试验"},
			OutcomeShift:   []string{"从猜测转为有证据"},
		},
	}
	plan.CausalSimulation.ArcTransition = domain.ArcChapterTransitionContract{
		OutgoingConsequenceID:   fmt.Sprintf("fixture-consequence-%06d", chapter),
		OutgoingConsequenceText: fmt.Sprintf("chapter %d leaves a concrete consequence for chapter %d", chapter, chapter+1),
	}
	if predecessor != nil {
		plan.CausalSimulation.ArcTransition.IncomingConsequenceID = predecessor.OutgoingConsequenceID
		plan.CausalSimulation.ArcTransition.IncomingConsequenceText = predecessor.OutgoingConsequenceText
		plan.CausalSimulation.ArcTransition.ConsumedByCause = plan.CausalSimulation.CausalBeats[0].Cause
	}
	contextToken, err := domain.ProjectedPlanningContextSourceTokenV2(planningContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	fullSimulation.Sources = append(fullSimulation.Sources, contextToken)
	factReceipt := projectedStoreV2NoMaterialRAGReceipt(t, chapter)
	if withRAG {
		factReceipt = projectedStoreV2RAGReceipt(t, chapter)
	}
	craftReceipt := projectedStoreV2NoMaterialCraftReceipt(t, generationID, chapter, planningContextDigest)
	factDigest, err := domain.RAGFactReceiptDigestV2(factReceipt)
	if err != nil {
		t.Fatal(err)
	}
	craftDigest, err := domain.CraftRecallReceiptDigestV2(craftReceipt)
	if err != nil {
		t.Fatal(err)
	}
	plan.CausalSimulation.ContextSources = append(
		plan.CausalSimulation.ContextSources,
		contextToken,
		factReceipt.SourceToken(),
		domain.CraftRecallReceiptSourceTokenV2(craftReceipt),
	)
	renderContext, err := json.Marshal(map[string]any{
		"_context_profile": "draft",
		"draft_packet": map[string]any{
			"chapter": chapter,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta := projectedStoreV2Delta(chapter)
	post, err := domain.DeriveProjectedPostStateRootV2(pre, delta)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.ProjectedChapterBundle{
		Version:                domain.ProjectedChapterBundleV2Version,
		GenerationID:           generationID,
		Chapter:                chapter,
		Authority:              domain.ProjectedAuthorityV2,
		State:                  domain.ProjectedStateV2,
		ProjectionLevel:        domain.FormalProjectionLevelV2,
		PreviousBundleDigest:   previous,
		ProjectedPreStateRoot:  pre,
		ChapterWorldSimulation: fullSimulation,
		ChapterPlan:            plan,
		FormalWorldSimulation: domain.FormalWorldSimulationV2{
			SimulationID: simulationID,
			InitialConditions: []domain.SimulationStateFactV2{{
				ID: "initial", Subject: "protagonist", Field: "certainty", Value: "low",
			}},
			Actors: []domain.SimulationActorV2{{
				CharacterID:      "protagonist",
				Motivation:       "验证规则",
				KnownFacts:       []string{"额度可见"},
				UnknownFacts:     []string{"结算结果"},
				OffscreenState:   "等待下一次观察",
				AvailableActions: []string{"验证", "放弃"},
			}},
			AvailableChoices: []string{"验证", "放弃"},
			ChosenDecision:   "验证",
			CausalSteps: []domain.SimulationCausalStepV2{{
				ID:               "step-1",
				CauseIDs:         []string{"initial"},
				ActorID:          "protagonist",
				Decision:         "验证",
				ImmediateEffect:  "执行小额动作",
				DownstreamEffect: "获得可复核反馈",
			}},
			Counterfactuals: []domain.SimulationCounterfactualV2{{
				Choice: "立即扩张", RejectedBy: "信息不足", Consequence: "失去撤回空间",
			}},
			TerminalConditions: []domain.SimulationStateFactV2{{
				ID: "terminal", Subject: "protagonist", Field: "certainty", Value: "higher",
			}},
			TimeAdvance:  "推进三小时",
			LocationFlow: []string{"家中", "街面"},
		},
		POVPlan: domain.POVPlanV2{
			POVCharacterID:    "protagonist",
			KnowledgeBoundary: []string{"只知道亲历结果"},
			Unknowns:          []string{"场外角色完整计划"},
			Motivations: []domain.POVCharacterMotivationV2{{
				CharacterID: "protagonist", Goal: "验证", Pressure: "时间", Choice: "小额行动",
			}},
			OffscreenStates: []domain.POVOffscreenStateV2{{
				CharacterID: "helper", State: "等待联络", CausalImpact: "限制资源",
			}},
			Scenes: []domain.POVSceneV2{{
				SceneID:        "scene-1",
				Location:       "街面",
				Time:           "上午",
				PresentActors:  []string{"protagonist"},
				POVKnows:       []string{"当前价格"},
				POVDoesNotKnow: []string{"后台处理"},
				CausalPurpose:  "用行动验证规则",
			}},
			TimeAdvance: "推进三小时",
		},
		HardRenderContract: domain.HardRenderContractV2{
			MustOccur:    []string{"完成小额验证"},
			MustNotOccur: []string{"不得全知泄漏"},
			MustPreserve: []string{"资源数量"},
			RevealBudget: []domain.RevealBudgetItemV2{{
				FactID: "fact-1", Action: "partial", Limit: "只呈现结果",
			}},
		},
		SourceBindings: []domain.SourceBindingV2{
			{
				Kind:            "source_snapshot",
				SourceID:        generationID,
				SourceDigest:    projectedStoreV2Digest("source-" + generationID),
				ExactReferences: []string{"outline:chapter"},
				UsableFacts:     []string{"稳定章位"},
				Transformation:  "转换为本章行动边界",
				DoNotUse:        []string{"不复制原始措辞"},
			},
			{
				Kind:            "rag_fact_receipt",
				SourceID:        factReceipt.ID,
				SourceDigest:    factDigest,
				ExactReferences: []string{factReceipt.SourceToken()},
				UsableFacts:     []string{"只使用 receipt 允许的事实边界"},
				Transformation:  "转化为角色可见的行动细节",
				DoNotUse:        []string{"不把原始召回交给正文"},
			},
			{
				Kind:            "craft_recall_receipt",
				SourceID:        craftReceipt.ID,
				SourceDigest:    craftDigest,
				ExactReferences: []string{domain.CraftRecallReceiptSourceTokenV2(craftReceipt)},
				UsableFacts:     []string{"两类 craft need 均为 no_material"},
				Transformation:  "保持 no_material 边界",
				DoNotUse:        []string{"不得伪造 craft 引用"},
			},
		},
		RAGFactReceipt:           &factReceipt,
		RAGFactReceiptDigest:     factDigest,
		CraftRecallReceipt:       &craftReceipt,
		CraftRecallReceiptDigest: craftDigest,
		PlanningContextDigest:    planningContextDigest,
		RenderContext:            renderContext,
		ObligationsConsumed:      []string{},
		ObligationsCreated:       []string{},
		ObligationsCarried:       []string{},
		ProjectedDelta:           delta,
		ProjectedPostStateRoot:   post,
	}
	bundle.RenderContext, err = domain.BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("fixture bundle invalid: %v", err)
	}
	return bundle
}

func projectedStoreV2RebindRenderContext(
	t *testing.T,
	bundle *domain.ProjectedChapterBundle,
) {
	t.Helper()
	var err error
	bundle.RenderContext, err = domain.BindProjectedRenderContextV2(
		bundle.RenderContext,
		*bundle,
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(
		bundle.RenderContext,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func projectedStoreV2Delta(chapter int) domain.ProjectedDelta {
	return domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  fmt.Sprintf("timeline:%d", chapter),
			Subject:   "story_clock",
			Field:     "chapter",
			Operation: "advance",
			Before:    fmt.Sprintf("%d", chapter-1),
			After:     fmt.Sprintf("%d", chapter),
			Cause:     "本章行动完成",
		}},
		CharacterState: []domain.StateMutationV2{},
		Relationships:  []domain.StateMutationV2{},
		Resources:      []domain.StateMutationV2{},
		Knowledge:      []domain.StateMutationV2{},
		Locations:      []domain.StateMutationV2{},
		Foreshadows:    []domain.StateMutationV2{},
		Obligations:    []domain.StateMutationV2{},
	}
}

func projectedStoreV2RAGReceipt(t *testing.T, chapter int) domain.RAGFactReceipt {
	t.Helper()
	receipt, err := domain.NewRAGFactReceipt(
		chapter,
		"摊位 灯具",
		[]string{"摊位", "灯具"},
		"project_facts_exact_v1",
		strings.Repeat("b", 64),
		[]domain.RAGFactReceiptHit{{
			Rank:          1,
			ChunkID:       "chunk-1",
			ContentSHA256: strings.Repeat("a", 64),
			SourcePath:    "refs/fact.md",
			SourceKind:    "project",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectedStoreV2NoMaterialRAGReceipt(t *testing.T, chapter int) domain.RAGFactReceipt {
	t.Helper()
	receipt, err := domain.NewRAGFactReceipt(
		chapter,
		"chapter fixture",
		[]string{"fixture"},
		"no_material_v1",
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectedStoreV2NoMaterialCraftReceipt(
	t *testing.T,
	generationID string,
	chapter int,
	planningContextDigest string,
) domain.CraftRecallReceipt {
	t.Helper()
	receipt := domain.CraftRecallReceipt{
		Version:               1,
		ID:                    fmt.Sprintf("%024x", chapter),
		Chapter:               chapter,
		Stage:                 domain.ProjectAllCraftReceiptStage,
		GenerationID:          generationID,
		PlanningContextDigest: planningContextDigest,
		IndexIdentity:         "fixture-index",
		Enforcement:           true,
		CreatedAt:             projectedStoreV2Time(),
		Attempts: []domain.CraftRecallReceiptAttempt{
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-methodology", Field: "methodology", Topic: "fixture methodology"},
				NoMaterial: true,
			},
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-scene", Field: "scene_situation", Topic: "fixture scene"},
				NoMaterial: true,
			},
		},
	}
	receipt.PayloadSHA256 = domain.ComputeCraftRecallReceiptPayloadSHA256(receipt)
	if err := domain.ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectedStoreV2Obligation(t *testing.T, generationID string) domain.ObligationV2 {
	t.Helper()
	contract := "第五章前必须兑现资源代价"
	id, err := domain.DeriveObligationIDV2(domain.ObligationResourceV2, 4, contract)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ObligationV2{
		ID:       id,
		Kind:     domain.ObligationResourceV2,
		Contract: contract,
		Origin: domain.ObligationOriginV2{
			GenerationID: generationID,
			Chapter:      4,
			SourceDigest: projectedStoreV2Digest("obligation-source"),
		},
		DueWindow: domain.ObligationDueWindowV2{
			FromChapter: 4,
			ToChapter:   5,
		},
		Hardness:         domain.ObligationHardV2,
		State:            domain.ObligationPlannedV2,
		ConsumerChapters: []int{5},
		Evidence:         []domain.ObligationEvidenceV2{},
		Supersedes:       []string{},
	}
}

func projectedStoreV2Promotion(t *testing.T, bundle domain.ProjectedChapterBundle) domain.PromotionReceiptV2 {
	t.Helper()
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.PromotionReceiptV2{
		Version:               domain.PromotionReceiptV2Version,
		GenerationID:          bundle.GenerationID,
		Chapter:               bundle.Chapter,
		BundleDigest:          bundle.BundleDigest,
		ActualPreStateRoot:    bundle.ProjectedPreStateRoot,
		ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
		RenderDependencyRoot:  projectedStoreV2Digest("render-dependencies"),
		FrozenPlanDigest:      planDigest,
		Mode:                  domain.ExactPromotionModeV2,
		PromotedAt:            projectedStoreV2Time(),
	}
	receipt.ReceiptDigest, err = domain.ComputePromotionReceiptV2Digest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectedStoreV2Outcome(
	t *testing.T,
	bundle domain.ProjectedChapterBundle,
	promotion domain.PromotionReceiptV2,
) domain.ActualOutcomeReceiptV2 {
	t.Helper()
	receipt := domain.ActualOutcomeReceiptV2{
		Version:                     domain.ActualOutcomeReceiptV2Version,
		GenerationID:                bundle.GenerationID,
		Chapter:                     bundle.Chapter,
		PromotionReceiptDigest:      promotion.ReceiptDigest,
		ChapterBodySHA256:           projectedStoreV2Digest("body"),
		CommitCheckpointSeq:         12,
		ActualDelta:                 bundle.ProjectedDelta,
		ActualPreStateRoot:          bundle.ProjectedPreStateRoot,
		ActualPostStateRoot:         bundle.ProjectedPostStateRoot,
		ActualCanonRoot:             projectedStoreV2Digest("actual-canon"),
		ProjectedPostStateRoot:      bundle.ProjectedPostStateRoot,
		ObligationsSatisfied:        []string{},
		ObligationsCreatedUnplanned: []string{},
		ProjectionMatch:             true,
		AcceptedAt:                  projectedStoreV2Time(),
	}
	var err error
	receipt.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func projectedStoreV2Digest(value string) string {
	raw, _ := json.Marshal(map[string]string{"value": value})
	digest, _ := domain.ComputePlanningV2JSONDigest(raw)
	return digest
}

func projectedStoreV2Time() string {
	return time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC).Format(time.RFC3339)
}
