package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestArcCompletionRequiresEveryExactActualOutcome(t *testing.T) {
	st, generation, cursor, outcomes := arcOutcomeChainTestFixture(t, 3)

	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := validatePipelineArcOutcomeChain(st, generation, acceptances)
	if err != nil || len(loaded) != 3 {
		t.Fatalf("valid three-chapter outcome chain rejected: outcomes=%d err=%v", len(loaded), err)
	}

	middlePath := filepath.Join(
		st.Dir(),
		"meta", "planning", "v2", "actual_outcomes",
		generation.GenerationID,
		"0002",
		outcomes[1].ReceiptDigest+".json",
	)
	backupPath := middlePath + ".missing"
	if err := os.Rename(middlePath, backupPath); err != nil {
		t.Fatal(err)
	}
	if _, err := completePipelineArcCycle(st, generation, cursor); err == nil ||
		!strings.Contains(err.Error(), "chapter 2 exact actual outcome") {
		t.Fatalf("arc completed without its intermediate actual outcome: %v", err)
	}
	if err := os.Rename(backupPath, middlePath); err != nil {
		t.Fatal(err)
	}

	completion, err := completePipelineArcCycle(st, generation, cursor)
	if err != nil {
		t.Fatalf("complete restored exact outcome chain: %v", err)
	}
	if completion.FinalOutcomeReceiptDigest != outcomes[2].ReceiptDigest ||
		completion.FinalActualPostStateRoot != outcomes[2].ActualPostStateRoot {
		t.Fatalf("completion did not bind final actual outcome/root: %+v", completion)
	}
	if _, err := requirePipelineArcCompletion(st, generation); err != nil {
		t.Fatalf("next-arc guard rejected intact completion: %v", err)
	}

	if err := os.Remove(middlePath); err != nil {
		t.Fatal(err)
	}
	if _, err := requirePipelineArcCompletion(st, generation); err == nil ||
		!strings.Contains(err.Error(), "chapter 2 exact actual outcome") {
		t.Fatalf("next-arc guard ignored missing intermediate actual outcome: %v", err)
	}
}

func TestArcNarrativeEvidenceNeverFabricatesTurnOrPayoffFromGoalAndHook(t *testing.T) {
	bundle := domain.ProjectedChapterBundle{
		Chapter: 1,
		ChapterPlan: domain.ChapterPlan{
			Chapter: 1,
			Goal:    "generic goal must not become a turn",
			Hook:    "generic hook must not become a payoff",
			CausalSimulation: domain.ChapterCausalSimulation{
				ArcTransition: domain.ArcChapterTransitionContract{
					OutgoingConsequenceID:   "explicit-edge-1",
					OutgoingConsequenceText: "an authored concrete consequence",
				},
			},
		},
	}
	if _, _, _, err := pipelineArcNarrativeEvidence([]domain.ProjectedChapterBundle{bundle}); err == nil ||
		!strings.Contains(err.Error(), "outcome_shift") {
		t.Fatalf("goal was fabricated into an arc turn: %v", err)
	}
	bundle.ChapterPlan.CausalSimulation.OutcomeShift = []string{"authored state turn"}
	if _, _, _, err := pipelineArcNarrativeEvidence([]domain.ProjectedChapterBundle{bundle}); err == nil ||
		!strings.Contains(err.Error(), "payoff_points") {
		t.Fatalf("hook/goal was fabricated into an arc payoff: %v", err)
	}
	bundle.ChapterPlan.CausalSimulation.EndingContract.Consequence = "authored ending consequence"
	_, turns, payoffs, err := pipelineArcNarrativeEvidence([]domain.ProjectedChapterBundle{bundle})
	if err != nil {
		t.Fatalf("authored turn/payoff evidence rejected: %v", err)
	}
	if len(turns) != 1 || turns[0].Summary != "authored state turn" ||
		len(payoffs) != 1 || payoffs[0].Summary != "authored ending consequence" {
		t.Fatalf("narrative markers did not preserve exact authored sources: turns=%+v payoffs=%+v", turns, payoffs)
	}
}

func arcOutcomeChainTestFixture(
	t *testing.T,
	count int,
) (*store.Store, *domain.PlanningGenerationV2, *domain.RealizationCursorV2, []domain.ActualOutcomeReceiptV2) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, count)
	arcID := domain.DeriveArcCycleID(1, 1, 1, count)
	generation.ProjectionScope = domain.PlanningProjectionScopeArcV2
	generation.ScopeID = arcID
	generation.BookHorizonChapter = count
	registry.ProjectionScope = generation.ProjectionScope
	registry.ScopeID = arcID
	registry.BookHorizonChapter = count
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
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generation.GenerationID,
		BaseCanonChapter:       generation.BaseCanonChapter,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FoundationSnapshotRoot: projectAllCmdTestDigest("arc-outcome-foundation"),
		RAGSnapshotRoot:        projectAllCmdTestDigest("arc-outcome-rag"),
		CapturedAt:             "2026-07-17T00:00:00Z",
	}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	projectionCursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}

	currentGeneration := generation
	currentRegistry := registry
	previous, preState, err := pipelineProjectAllTail(currentGeneration, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]domain.ProjectedChapterBundle, 0, count)
	for chapter := 1; chapter <= count; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, chapter)
		projectAllCmdTestBindPlanningContext(
			t,
			artifacts,
			currentGeneration,
			bundles,
			currentRegistry,
			chapter,
		)
		bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(
			currentGeneration,
			outline,
			previous,
			preState,
			artifacts,
			currentRegistry,
		)
		if err != nil {
			t.Fatalf("build projected chapter %d: %v", chapter, err)
		}
		nextRegistry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(nextRegistry)
		if err != nil {
			t.Fatal(err)
		}
		projectionCursor, err = projected.ProjectChapterAndAdvance(
			currentGeneration.GenerationDigest,
			currentGeneration.ChainTailRoot,
			currentGeneration.ObligationRegistryRoot,
			*projectionCursor,
			bundle,
			nextRegistry,
		)
		if err != nil {
			t.Fatalf("persist projected chapter %d: %v", chapter, err)
		}
		bundles = append(bundles, bundle)
		currentRegistry = nextRegistry
		loaded, err := projected.LoadBuildingGeneration(generation.GenerationID)
		if err != nil || loaded == nil {
			t.Fatalf("reload projected generation after chapter %d: generation=%+v err=%v", chapter, loaded, err)
		}
		currentGeneration = *loaded
		previous = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projected.ActivateSealedGeneration(generation.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	sealed, err := projected.LoadSealedGeneration(generation.GenerationID)
	if err != nil || sealed == nil {
		t.Fatalf("load sealed generation: generation=%+v err=%v", sealed, err)
	}
	links, turns, payoffs, err := pipelineArcNarrativeEvidence(bundles)
	if err != nil {
		t.Fatal(err)
	}

	manifest := domain.ArcPlanningManifest{
		Version:           domain.ArcPlanningManifestVersion,
		ArcID:             arcID,
		GenerationID:      sealed.GenerationID,
		Volume:            1,
		Arc:               1,
		FirstChapter:      1,
		LastChapter:       count,
		BookLastChapter:   count,
		FullOutlineDigest: sealed.StableOutlineRoot,
		ChapterBodyRunes: domain.ArcChapterBodyRuneContract{
			MinRunes:              2000,
			MaxRunes:              3300,
			SourceUserRulesDigest: projectAllCmdTestDigest("arc-outcome-user-rules"),
		},
		CausalLinks: links,
		Turns:       turns,
		Payoffs:     payoffs,
		CreatedAt:   sealed.CreatedAt,
	}
	for chapter, bundle := range bundles {
		manifest.Chapters = append(manifest.Chapters, domain.ArcChapterPlanningBinding{
			Chapter:        chapter + 1,
			BundleDigest:   bundle.BundleDigest,
			CapacityDigest: pipelineProjectAllDigest(*bundle.ChapterPlan.CausalSimulation.RenderCapacity),
		})
	}
	manifest, err = domain.SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArcCycle().SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}

	outcomes := make([]domain.ActualOutcomeReceiptV2, 0, count)
	for chapter, bundle := range bundles {
		chapter++
		cursor, err := projected.LoadRealizationCursor()
		if err != nil || cursor == nil {
			t.Fatalf("load realization cursor before chapter %d: cursor=%+v err=%v", chapter, cursor, err)
		}
		promotion := projectAllCmdTestPromotion(t, bundle)
		if _, err := projected.Promote(*cursor, promotion); err != nil {
			t.Fatalf("promote chapter %d: %v", chapter, err)
		}
		bodyPrefix := fmt.Sprintf("第%d章正文：角色以可见行动兑现封存因果。", chapter)
		body := []byte(bodyPrefix + strings.Repeat("青", 2100-utf8.RuneCountInString(bodyPrefix)))
		projectAllCmdTestWriteFile(
			t,
			filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)),
			string(body),
		)
		cursor, err = projected.LoadRealizationCursor()
		if err != nil || cursor == nil {
			t.Fatalf("load promoted cursor for chapter %d: cursor=%+v err=%v", chapter, cursor, err)
		}
		outcome := projectAllCmdTestOutcome(t, bundle, promotion, chapter)
		outcome.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256(body)
		outcome.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(outcome)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projected.AcceptOutcome(*cursor, outcome); err != nil {
			t.Fatalf("accept chapter %d outcome: %v", chapter, err)
		}

		reviewPath := filepath.Join("reviews", fmt.Sprintf("%02d.json", chapter))
		reviewBody := []byte(fmt.Sprintf(`{"chapter":%d,"verdict":"accept"}`, chapter))
		projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), reviewPath), string(reviewBody))
		acceptance, err := domain.SignChapterAcceptanceReceipt(domain.ChapterAcceptanceReceipt{
			Version:              domain.ChapterAcceptanceReceiptVersion,
			ArcID:                manifest.ArcID,
			ArcManifestDigest:    manifest.ManifestDigest,
			GenerationID:         sealed.GenerationID,
			Chapter:              chapter,
			ChapterBodySHA256:    outcome.ChapterBodySHA256,
			ChapterBodyRunes:     utf8.RuneCount(body),
			ReviewArtifacts:      []domain.ChapterReviewArtifactBinding{{Path: reviewPath, Digest: domain.ComputeArcArtifactSHA256(reviewBody)}},
			OutcomeReceiptDigest: outcome.ReceiptDigest,
			AcceptedAt:           outcome.AcceptedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(acceptance); err != nil {
			t.Fatalf("save chapter %d acceptance: %v", chapter, err)
		}
		outcomes = append(outcomes, outcome)
	}
	finalCursor, err := projected.LoadRealizationCursor()
	if err != nil || finalCursor == nil {
		t.Fatalf("load final realization cursor: cursor=%+v err=%v", finalCursor, err)
	}
	return st, sealed, finalCursor, outcomes
}
