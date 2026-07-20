package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// savePipelineArcPlanningManifest publishes the immutable, prose-free proof
// that every chapter in exactly one arc was jointly planned before rendering
// starts. It deliberately contains no arc-level prose verdict: reviews remain
// exact-body, chapter-level evidence written only after each render.
func savePipelineArcPlanningManifest(
	st *store.Store,
	identity pipelineProjectAllIdentity,
	generation domain.PlanningGenerationV2,
) (*domain.ArcPlanningManifest, error) {
	if st == nil {
		return nil, fmt.Errorf("arc planning manifest requires store")
	}
	if generation.GenerationID != identity.Generation.GenerationID ||
		generation.ScopeID != identity.Generation.ScopeID ||
		generation.ProjectionScope != domain.PlanningProjectionScopeArcV2 {
		return nil, fmt.Errorf("arc planning manifest generation does not match current arc identity")
	}
	projected := st.ProjectedV2()
	bundles, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	registry, err := projected.LoadObligationRegistry(generation.GenerationID)
	if err != nil || registry == nil {
		return nil, fmt.Errorf("arc planning manifest obligation registry is unavailable: %w", err)
	}
	if len(bundles) != generation.ExpectedChapterCount {
		return nil, fmt.Errorf("arc planning manifest has %d/%d chapter bundles", len(bundles), generation.ExpectedChapterCount)
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Chapter < bundles[j].Chapter })
	if err := domain.ValidateProjectedChapterBundleChain(generation, bundles, *registry); err != nil {
		return nil, fmt.Errorf("arc planning manifest requires a valid explicit bundle chain: %w", err)
	}
	causalLinks, turns, payoffs, err := pipelineArcNarrativeEvidence(bundles)
	if err != nil {
		return nil, err
	}

	var minRunes, maxRunes int
	var sourceUserRulesDigest string
	if rules, rulesErr := st.UserRules.Load(); rulesErr != nil {
		return nil, fmt.Errorf("arc planning manifest reads user_rules: %w", rulesErr)
	} else if rules == nil || rules.Structured.ChapterWords == nil {
		return nil, fmt.Errorf("arc planning manifest requires user_rules.chapter_words")
	} else {
		minRunes = rules.Structured.ChapterWords.Min
		maxRunes = rules.Structured.ChapterWords.Max
		sourceUserRulesDigest = pipelineProjectAllDigest(*rules)
	}

	manifest := domain.ArcPlanningManifest{
		Version:           domain.ArcPlanningManifestVersion,
		ArcID:             identity.Generation.ScopeID,
		GenerationID:      generation.GenerationID,
		Volume:            identity.Arc.Volume,
		Arc:               identity.Arc.Arc,
		FirstChapter:      generation.FirstProjectedChapter,
		LastChapter:       generation.LastProjectedChapter,
		BookLastChapter:   generation.BookHorizonChapter,
		FullOutlineDigest: generation.StableOutlineRoot,
		ChapterBodyRunes: domain.ArcChapterBodyRuneContract{
			MinRunes:              minRunes,
			MaxRunes:              maxRunes,
			SourceUserRulesDigest: sourceUserRulesDigest,
		},
		CausalLinks: causalLinks,
		Turns:       turns,
		Payoffs:     payoffs,
		CreatedAt:   generation.CreatedAt,
	}
	for i := range bundles {
		bundle := bundles[i]
		wantChapter := generation.FirstProjectedChapter + i
		if bundle.Chapter != wantChapter {
			return nil, fmt.Errorf("arc planning manifest bundle order breaks at chapter %d, want %d", bundle.Chapter, wantChapter)
		}
		capacity := bundle.ChapterPlan.CausalSimulation.RenderCapacity
		if capacity == nil {
			return nil, fmt.Errorf("arc planning manifest chapter %d has no render_capacity", bundle.Chapter)
		}
		if err := capacity.Validate(minRunes, maxRunes); err != nil {
			return nil, fmt.Errorf("arc planning manifest chapter %d cannot sustain configured prose range: %w", bundle.Chapter, err)
		}
		manifest.Chapters = append(manifest.Chapters, domain.ArcChapterPlanningBinding{
			Chapter:        bundle.Chapter,
			BundleDigest:   bundle.BundleDigest,
			CapacityDigest: pipelineProjectAllDigest(*capacity),
		})
	}
	for _, obligation := range registry.Obligations {
		if obligation.State != domain.ObligationOpenV2 && obligation.State != domain.ObligationPlannedV2 {
			continue
		}
		if obligation.DueWindow.FromChapter <= generation.LastProjectedChapter {
			return nil, fmt.Errorf(
				"arc %s leaves obligation %s due inside the current arc unresolved",
				generation.ScopeID,
				obligation.ID,
			)
		}
		manifest.CarriedObligations = append(manifest.CarriedObligations, domain.ArcCarriedObligation{
			ObligationID:     obligation.ID,
			OriginChapter:    obligation.Origin.Chapter,
			DueChapter:       obligation.DueWindow.ToChapter,
			ObligationDigest: pipelineProjectAllDigest(obligation),
		})
	}
	sort.Slice(manifest.CarriedObligations, func(i, j int) bool {
		return manifest.CarriedObligations[i].ObligationID < manifest.CarriedObligations[j].ObligationID
	})
	signed, err := domain.SignArcPlanningManifest(manifest)
	if err != nil {
		return nil, err
	}
	existing, err := st.ArcCycle().ListArcPlanningManifests(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		if len(existing) != 1 || existing[0].ManifestDigest != signed.ManifestDigest {
			return nil, fmt.Errorf("arc planning manifest for generation %s differs from the immutable sealed plan", generation.GenerationID)
		}
		return &existing[0], nil
	}
	if _, err := st.ArcCycle().SaveArcPlanningManifest(signed); err != nil {
		return nil, err
	}
	return &signed, nil
}

func pipelineArcNarrativeEvidence(
	bundles []domain.ProjectedChapterBundle,
) ([]domain.ArcCausalLink, []domain.ArcNarrativeMarker, []domain.ArcNarrativeMarker, error) {
	var links []domain.ArcCausalLink
	var turns []domain.ArcNarrativeMarker
	var payoffs []domain.ArcNarrativeMarker
	for i, bundle := range bundles {
		var predecessor *domain.ProjectedPlanningPredecessorContractV2
		if i > 0 {
			value := bundles[i-1]
			contract := value.ChapterPlan.CausalSimulation.ArcTransition
			predecessor = &domain.ProjectedPlanningPredecessorContractV2{
				Chapter:                 value.Chapter,
				OutgoingConsequenceID:   contract.OutgoingConsequenceID,
				OutgoingConsequenceText: contract.OutgoingConsequenceText,
				BundleDigest:            value.BundleDigest,
				ProjectedPostStateRoot:  value.ProjectedPostStateRoot,
			}
		}
		if err := domain.ValidateArcChapterTransitionContract(bundle.ChapterPlan, predecessor); err != nil {
			return nil, nil, nil, fmt.Errorf("arc planning manifest chapter %d transition: %w", bundle.Chapter, err)
		}
		if predecessor != nil {
			contract := bundle.ChapterPlan.CausalSimulation.ArcTransition
			links = append(links, domain.ArcCausalLink{
				ID:          predecessor.OutgoingConsequenceID,
				FromChapter: predecessor.Chapter,
				ToChapter:   bundle.Chapter,
				Cause:       predecessor.OutgoingConsequenceText,
				Effect:      contract.ConsumedByCause,
			})
		}
		for j, summary := range compactProjectAllStrings(bundle.ChapterPlan.CausalSimulation.OutcomeShift) {
			turns = append(turns, domain.ArcNarrativeMarker{
				ID:      fmt.Sprintf("turn-%06d-%02d", bundle.Chapter, j+1),
				Chapter: bundle.Chapter,
				Summary: summary,
			})
		}
		chapterPayoffs := compactProjectAllStrings(append(
			append([]string(nil), bundle.ChapterPlan.Contract.PayoffPoints...),
			bundle.ChapterPlan.CausalSimulation.EndingContract.Consequence,
		))
		for j, summary := range chapterPayoffs {
			payoffs = append(payoffs, domain.ArcNarrativeMarker{
				ID:      fmt.Sprintf("payoff-%06d-%02d", bundle.Chapter, j+1),
				Chapter: bundle.Chapter,
				Summary: summary,
			})
		}
	}
	if len(turns) == 0 {
		return nil, nil, nil, fmt.Errorf("arc planning manifest requires at least one model-authored outcome_shift; goal/hook fallback is forbidden")
	}
	if len(payoffs) == 0 {
		return nil, nil, nil, fmt.Errorf("arc planning manifest requires at least one model-authored payoff_points or ending_consequence_contract.consequence; goal/hook fallback is forbidden")
	}
	return links, turns, payoffs, nil
}

func validatePipelineArcPlanningManifestEvidence(
	st *store.Store,
	generation domain.PlanningGenerationV2,
	manifest domain.ArcPlanningManifest,
) error {
	bundles, err := st.ProjectedV2().LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil {
		return err
	}
	registry, err := st.ProjectedV2().LoadObligationRegistry(generation.GenerationID)
	if err != nil || registry == nil {
		return fmt.Errorf("arc planning manifest obligation registry is unavailable: %w", err)
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Chapter < bundles[j].Chapter })
	if err := domain.ValidateProjectedChapterBundleChain(generation, bundles, *registry); err != nil {
		return fmt.Errorf("arc planning manifest bundle chain drifted: %w", err)
	}
	if len(manifest.Chapters) != len(bundles) {
		return fmt.Errorf("arc planning manifest chapter binding count drifted")
	}
	for i, bundle := range bundles {
		capacity := bundle.ChapterPlan.CausalSimulation.RenderCapacity
		if capacity == nil ||
			manifest.Chapters[i].Chapter != bundle.Chapter ||
			manifest.Chapters[i].BundleDigest != bundle.BundleDigest ||
			manifest.Chapters[i].CapacityDigest != pipelineProjectAllDigest(*capacity) {
			return fmt.Errorf("arc planning manifest chapter %d binding drifted from sealed bundle/capacity", bundle.Chapter)
		}
		if err := capacity.Validate(manifest.ChapterBodyRunes.MinRunes, manifest.ChapterBodyRunes.MaxRunes); err != nil {
			return fmt.Errorf("arc planning manifest chapter %d sealed render capacity is invalid: %w", bundle.Chapter, err)
		}
	}
	links, turns, payoffs, err := pipelineArcNarrativeEvidence(bundles)
	if err != nil {
		return err
	}
	if pipelineProjectAllDigest(manifest.CausalLinks) != pipelineProjectAllDigest(links) ||
		pipelineProjectAllDigest(manifest.Turns) != pipelineProjectAllDigest(turns) ||
		pipelineProjectAllDigest(manifest.Payoffs) != pipelineProjectAllDigest(payoffs) {
		return fmt.Errorf("arc planning manifest narrative evidence is not the exact model-authored bundle evidence")
	}
	return nil
}

func requirePipelineArcPlanningManifest(
	st *store.Store,
	generation *domain.PlanningGenerationV2,
) (*domain.ArcPlanningManifest, error) {
	if st == nil || generation == nil {
		return nil, fmt.Errorf("arc planning manifest validation requires store and generation")
	}
	manifests, err := st.ArcCycle().ListArcPlanningManifests(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if len(manifests) != 1 {
		return nil, fmt.Errorf("generation %s requires exactly one arc planning manifest, got %d", generation.GenerationID, len(manifests))
	}
	manifest := manifests[0]
	if manifest.GenerationID != generation.GenerationID ||
		manifest.ArcID != generation.ScopeID ||
		manifest.FirstChapter != generation.FirstProjectedChapter ||
		manifest.LastChapter != generation.LastProjectedChapter ||
		manifest.BookLastChapter != generation.BookHorizonChapter ||
		manifest.FullOutlineDigest != generation.StableOutlineRoot {
		return nil, fmt.Errorf("generation %s arc planning manifest identity drifted", generation.GenerationID)
	}
	if err := validatePipelineArcPlanningManifestEvidence(st, *generation, manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func savePipelineChapterAcceptance(
	outputDir string,
	st *store.Store,
	generation *domain.PlanningGenerationV2,
	chapter int,
	bodySHA string,
	outcome *domain.ActualOutcomeReceiptV2,
) (*domain.ChapterAcceptanceReceipt, error) {
	if st == nil || generation == nil || outcome == nil {
		return nil, fmt.Errorf("chapter acceptance requires store, generation and outcome")
	}
	manifest, err := requirePipelineArcPlanningManifest(st, generation)
	if err != nil {
		return nil, err
	}
	inspection := inspectCurrentChapterReview(outputDir, chapter)
	if len(inspection.Issues) > 0 || !pipelineReviewAcceptedForProjection(inspection.Verdict, inspection.Disposition) {
		return nil, fmt.Errorf("chapter %d exact-body review is not accepted", chapter)
	}
	if bodySHA != domain.PlanningV2DigestPrefix+inspection.BodySHA256 ||
		outcome.ChapterBodySHA256 != bodySHA ||
		outcome.GenerationID != generation.GenerationID ||
		outcome.Chapter != chapter {
		return nil, fmt.Errorf("chapter %d body/review/outcome identity mismatch", chapter)
	}
	bodyPath := filepath.Join(outputDir, "chapters", fmt.Sprintf("%02d.md", chapter))
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return nil, fmt.Errorf("chapter %d exact body read: %w", chapter, err)
	}
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("chapter %d exact body is not valid UTF-8", chapter)
	}
	bodyRunes := utf8.RuneCount(body)
	if err := domain.ValidateAcceptedChapterBodyRunes(chapter, bodyRunes, manifest.ChapterBodyRunes); err != nil {
		return nil, err
	}
	artifacts := make([]domain.ChapterReviewArtifactBinding, 0, len(inspection.Artifacts))
	for _, rel := range inspection.Artifacts {
		digest, digestErr := pipelineRequiredFileSHA(outputDir, rel)
		if digestErr != nil {
			return nil, digestErr
		}
		artifacts = append(artifacts, domain.ChapterReviewArtifactBinding{Path: rel, Digest: digest})
	}
	receipt := domain.ChapterAcceptanceReceipt{
		Version:              domain.ChapterAcceptanceReceiptVersion,
		ArcID:                manifest.ArcID,
		ArcManifestDigest:    manifest.ManifestDigest,
		GenerationID:         generation.GenerationID,
		Chapter:              chapter,
		ChapterBodySHA256:    bodySHA,
		ChapterBodyRunes:     bodyRunes,
		ReviewArtifacts:      domain.CanonicalChapterReviewArtifacts(artifacts),
		OutcomeReceiptDigest: outcome.ReceiptDigest,
		AcceptedAt:           outcome.AcceptedAt,
	}
	receipt, err = domain.SignChapterAcceptanceReceipt(receipt)
	if err != nil {
		return nil, err
	}
	if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(receipt); err != nil {
		return nil, err
	}
	if err := pipelineWatchdogProgressBody(
		pipelineWatchdogEventRenderChapterAccepted,
		bodySHA,
	); err != nil {
		return nil, fmt.Errorf("record chapter %d acceptance progress: %w", chapter, err)
	}
	return &receipt, nil
}

func pipelineChapterAcceptanceAlreadySaved(
	st *store.Store,
	generationID string,
	chapter int,
	bodySHA string,
	outcomeDigest string,
) (bool, error) {
	if st == nil || generationID == "" || chapter <= 0 {
		return false, nil
	}
	receipts, err := st.ArcCycle().ListChapterAcceptanceReceipts(generationID)
	if err != nil {
		return false, err
	}
	for _, receipt := range receipts {
		if receipt.Chapter != chapter {
			continue
		}
		if receipt.ChapterBodySHA256 != bodySHA || receipt.OutcomeReceiptDigest != outcomeDigest {
			return false, fmt.Errorf("chapter %d immutable acceptance does not match current body/outcome", chapter)
		}
		if err := st.ArcCycle().ValidateArcCycle(generationID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func requirePipelineChapterAcceptance(
	st *store.Store,
	generation *domain.PlanningGenerationV2,
	chapter int,
	outcomeDigest string,
) error {
	if st == nil || generation == nil || chapter <= 0 || strings.TrimSpace(outcomeDigest) == "" {
		return fmt.Errorf("chapter acceptance validation requires store, generation, chapter and outcome")
	}
	if err := st.ArcCycle().ValidateArcCycle(generation.GenerationID); err != nil {
		return err
	}
	receipts, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		return err
	}
	if _, err := validatePipelineArcOutcomeChain(st, generation, receipts); err != nil {
		return err
	}
	for _, receipt := range receipts {
		if receipt.Chapter != chapter {
			continue
		}
		if receipt.OutcomeReceiptDigest != outcomeDigest {
			return fmt.Errorf("chapter %d acceptance outcome digest drifted", chapter)
		}
		return nil
	}
	return fmt.Errorf("chapter %d has no immutable exact-body chapter acceptance receipt", chapter)
}

// validatePipelineArcOutcomeChain joins the arc-cycle receipt store back to
// the projected-store actual outcomes. ArcCycle deliberately owns no
// projected state, so every command-side unlock must replay this bridge: one
// exact outcome per acceptance, exact body identity, and an unbroken actual
// state-root chain beginning at the generation base.
func validatePipelineArcOutcomeChain(
	st *store.Store,
	generation *domain.PlanningGenerationV2,
	acceptances []domain.ChapterAcceptanceReceipt,
) ([]domain.ActualOutcomeReceiptV2, error) {
	if st == nil || generation == nil {
		return nil, fmt.Errorf("arc actual outcome validation requires store and generation")
	}
	if len(acceptances) > generation.ExpectedChapterCount {
		return nil, fmt.Errorf(
			"generation %s has %d outcome-bound acceptances, want at most %d",
			generation.GenerationID,
			len(acceptances),
			generation.ExpectedChapterCount,
		)
	}

	outcomes := make([]domain.ActualOutcomeReceiptV2, 0, len(acceptances))
	expectedPreStateRoot := generation.BaseStateRoot
	for i, acceptance := range acceptances {
		wantChapter := generation.FirstProjectedChapter + i
		if acceptance.GenerationID != generation.GenerationID || acceptance.Chapter != wantChapter {
			return nil, fmt.Errorf(
				"generation %s outcome acceptance order breaks at index %d: got generation=%s chapter=%d want chapter=%d",
				generation.GenerationID,
				i,
				acceptance.GenerationID,
				acceptance.Chapter,
				wantChapter,
			)
		}
		outcome, err := st.ProjectedV2().LoadActualOutcomeReceipt(
			generation.GenerationID,
			acceptance.Chapter,
			acceptance.OutcomeReceiptDigest,
		)
		if err != nil {
			return nil, fmt.Errorf("chapter %d exact actual outcome: %w", acceptance.Chapter, err)
		}
		if outcome == nil {
			return nil, fmt.Errorf(
				"chapter %d exact actual outcome %s is missing",
				acceptance.Chapter,
				acceptance.OutcomeReceiptDigest,
			)
		}
		if outcome.GenerationID != generation.GenerationID ||
			outcome.Chapter != acceptance.Chapter ||
			outcome.ReceiptDigest != acceptance.OutcomeReceiptDigest ||
			outcome.ChapterBodySHA256 != acceptance.ChapterBodySHA256 ||
			outcome.AcceptedAt != acceptance.AcceptedAt {
			return nil, fmt.Errorf(
				"chapter %d acceptance and actual outcome identity mismatch",
				acceptance.Chapter,
			)
		}
		if !outcome.ProjectionMatch || outcome.ActualPostStateRoot != outcome.ProjectedPostStateRoot {
			return nil, fmt.Errorf("chapter %d actual outcome is not an exact projection match", acceptance.Chapter)
		}
		if outcome.ActualPreStateRoot != expectedPreStateRoot {
			return nil, fmt.Errorf(
				"chapter %d actual pre-state root breaks the arc chain: got %s want %s",
				acceptance.Chapter,
				outcome.ActualPreStateRoot,
				expectedPreStateRoot,
			)
		}
		expectedPreStateRoot = outcome.ActualPostStateRoot
		outcomes = append(outcomes, *outcome)
	}
	return outcomes, nil
}

func completePipelineArcCycle(
	st *store.Store,
	generation *domain.PlanningGenerationV2,
	cursor *domain.RealizationCursorV2,
) (*domain.ArcCompletionReceipt, error) {
	if st == nil || generation == nil || cursor == nil {
		return nil, fmt.Errorf("arc completion requires store, generation and realization cursor")
	}
	manifest, err := requirePipelineArcPlanningManifest(st, generation)
	if err != nil {
		return nil, err
	}
	if err := st.ArcCycle().ValidateArcCycle(generation.GenerationID); err != nil {
		return nil, err
	}
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if len(acceptances) != generation.ExpectedChapterCount ||
		cursor.LastAcceptedChapter != generation.LastProjectedChapter ||
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return nil, fmt.Errorf("arc %s lacks a complete ordered chapter acceptance chain", generation.ScopeID)
	}
	outcomes, err := validatePipelineArcOutcomeChain(st, generation, acceptances)
	if err != nil {
		return nil, fmt.Errorf("arc %s actual outcome chain: %w", generation.ScopeID, err)
	}
	if len(outcomes) == 0 {
		return nil, fmt.Errorf("arc %s has no actual outcomes", generation.ScopeID)
	}
	lastOutcome := outcomes[len(outcomes)-1]
	if cursor.LastOutcomeReceiptDigest != lastOutcome.ReceiptDigest {
		return nil, fmt.Errorf(
			"arc %s realization cursor final outcome drifted: got %s want %s",
			generation.ScopeID,
			cursor.LastOutcomeReceiptDigest,
			lastOutcome.ReceiptDigest,
		)
	}
	completedAt := acceptances[len(acceptances)-1].AcceptedAt
	receipt, err := domain.NewArcCompletionReceipt(
		*manifest,
		acceptances,
		lastOutcome.ActualPostStateRoot,
		completedAt,
	)
	if err != nil {
		return nil, err
	}
	if _, err := st.ArcCycle().SaveArcCompletionReceipt(receipt); err != nil {
		return nil, err
	}
	if err := st.ArcCycle().ValidateArcCompletion(generation.GenerationID, receipt.ReceiptDigest); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func requirePipelineArcCompletion(
	st *store.Store,
	generation *domain.PlanningGenerationV2,
) (*domain.ArcCompletionReceipt, error) {
	if st == nil || generation == nil {
		return nil, fmt.Errorf("arc completion validation requires store and generation")
	}
	receipts, err := st.ArcCycle().ListArcCompletionReceipts(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if len(receipts) != 1 {
		return nil, fmt.Errorf("generation %s requires exactly one arc completion receipt, got %d", generation.GenerationID, len(receipts))
	}
	if _, err := requirePipelineArcPlanningManifest(st, generation); err != nil {
		return nil, err
	}
	if err := st.ArcCycle().ValidateArcCompletion(generation.GenerationID, receipts[0].ReceiptDigest); err != nil {
		return nil, err
	}
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	outcomes, err := validatePipelineArcOutcomeChain(st, generation, acceptances)
	if err != nil {
		return nil, fmt.Errorf("generation %s completed actual outcome chain: %w", generation.GenerationID, err)
	}
	if len(outcomes) != generation.ExpectedChapterCount {
		return nil, fmt.Errorf(
			"generation %s completed outcome chain has %d/%d chapters",
			generation.GenerationID,
			len(outcomes),
			generation.ExpectedChapterCount,
		)
	}
	lastOutcome := outcomes[len(outcomes)-1]
	if receipts[0].FinalOutcomeReceiptDigest != lastOutcome.ReceiptDigest ||
		receipts[0].FinalActualPostStateRoot != lastOutcome.ActualPostStateRoot {
		return nil, fmt.Errorf("generation %s completion is not bound to its final actual outcome/root", generation.GenerationID)
	}
	return &receipts[0], nil
}
