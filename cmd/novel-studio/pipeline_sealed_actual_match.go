package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// pipelineSealedActualDeltaMatch is the fail-closed result of comparing an
// accepted chapter's independently persisted commit metadata with one sealed
// projected transition. ActualDelta may equal the projected canonical delta
// only after every mutation has independent evidence; equality is a result,
// never an input assumption.
type pipelineSealedActualDeltaMatch struct {
	ActualDelta          domain.ProjectedDelta          `json:"actual_delta"`
	ProjectionMatch      bool                           `json:"projection_match"`
	Complete             bool                           `json:"complete"`
	MismatchReasons      []string                       `json:"mismatch_reasons,omitempty"`
	Evidence             []pipelineSealedActualEvidence `json:"evidence,omitempty"`
	ObligationsSatisfied []string                       `json:"obligations_satisfied,omitempty"`
}

type pipelineSealedActualEvidence struct {
	Category string `json:"category"`
	StableID string `json:"stable_id"`
	Locator  string `json:"locator"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after"`
}

type pipelineSealedActualRequirements struct {
	RequiredBeats     []string
	Obligations       map[string]domain.ObligationV2
	VisibleMutation   map[string]bool
	OffscreenEvidence map[string]string
	Bundle            *domain.ProjectedChapterBundle
}

type pipelineSealedActualFact struct {
	Category string
	Subject  string
	Object   string
	Field    string
	Before   string
	After    string
	Evidence string
	Locator  string
	Hard     bool
}

// matchPipelineSealedRenderActualDelta is the integration entry point for a
// sealed render. The bundle must already have passed the active
// generation/promotion checks. This function independently verifies the exact
// durable ChapterWorldDelta, chapter-local canon ledgers and body evidence.
func matchPipelineSealedRenderActualDelta(
	st *store.Store,
	bundle *domain.ProjectedChapterBundle,
	candidate *domain.ChapterWorldDelta,
	chapterBody string,
	verifiedBindings ...*pipelineSealedRenderBinding,
) (pipelineSealedActualDeltaMatch, error) {
	result := pipelineSealedActualDeltaMatch{
		ActualDelta: domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version},
	}
	if st == nil || bundle == nil {
		return result, fmt.Errorf("sealed actual matcher requires store and projected bundle")
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		return result, fmt.Errorf("sealed actual matcher projected bundle: %w", err)
	}
	if strings.TrimSpace(chapterBody) == "" {
		result.MismatchReasons = []string{"chapter body is empty; no prose evidence can be located"}
		return result, nil
	}

	durable, err := st.LoadChapterWorldDelta(bundle.Chapter)
	if err != nil {
		return result, fmt.Errorf("load durable chapter world delta: %w", err)
	}
	if candidate == nil {
		candidate = durable
	}
	var preliminary []string
	switch {
	case candidate == nil:
		preliminary = append(preliminary, "commit metadata is missing: meta/chapter_world_deltas has no candidate")
	case durable == nil:
		preliminary = append(preliminary, "candidate ChapterWorldDelta is not durably persisted")
	case !samePipelineSealedActualJSON(*candidate, *durable):
		preliminary = append(preliminary, "candidate ChapterWorldDelta differs from the durable commit artifact")
	}
	if candidate == nil {
		result.MismatchReasons = normalizePipelineSealedReasons(preliminary)
		return result, nil
	}
	if candidate.Version <= 0 || candidate.Chapter != bundle.Chapter {
		preliminary = append(preliminary, fmt.Sprintf(
			"commit metadata identity mismatch: version=%d chapter=%d want chapter=%d",
			candidate.Version,
			candidate.Chapter,
			bundle.Chapter,
		))
	}
	if strings.TrimSpace(candidate.GenerationID) == "" ||
		candidate.GenerationID != bundle.GenerationID {
		preliminary = append(preliminary, fmt.Sprintf(
			"commit metadata generation mismatch: got=%q want=%q",
			candidate.GenerationID,
			bundle.GenerationID,
		))
	}
	if pipelineSealedActualSourceContains(candidate.Sources, "project-all sealed projection") {
		preliminary = append(preliminary, "commit metadata is a projected shadow artifact, not an independent commit result")
	}
	if !pipelineSealedActualSourceContains(candidate.Sources, "commit_chapter") {
		preliminary = append(preliminary, "commit metadata lacks independent commit_chapter provenance")
	}

	requirements := pipelineSealedActualRequirements{
		RequiredBeats: append(
			append([]string(nil), bundle.ChapterPlan.Contract.RequiredBeats...),
			bundle.HardRenderContract.MustOccur...,
		),
		Obligations:       make(map[string]domain.ObligationV2),
		VisibleMutation:   make(map[string]bool),
		OffscreenEvidence: make(map[string]string),
		Bundle:            bundle,
	}
	livePlan, err := st.Drafts.LoadChapterPlan(bundle.Chapter)
	if err != nil {
		return result, fmt.Errorf("load live promoted plan: %w", err)
	}
	if livePlan == nil {
		preliminary = append(preliminary, "live promoted chapter plan is missing")
	} else {
		liveDigest, digestErr := domain.ComputeChapterPlanV2Digest(*livePlan)
		bundleDigest, bundleDigestErr := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
		if digestErr != nil || bundleDigestErr != nil {
			preliminary = append(preliminary, "live promoted chapter plan differs from the sealed bundle")
		} else if liveDigest != bundleDigest {
			if err := validatePipelineSealedActualConvergenceOverlay(
				bundle,
				*livePlan,
				liveDigest,
				bundleDigest,
				verifiedBindings,
			); err != nil {
				preliminary = append(preliminary, "live promoted chapter plan differs from the sealed bundle and has no valid convergence successor binding: "+err.Error())
			}
		}
	}
	classifyPipelineSealedMutationVisibility(bundle, &requirements)
	if pipelineSealedHasConsumedObligation(bundle.ProjectedDelta.Obligations) {
		registry, loadErr := st.ProjectedV2().LoadObligationRegistry(bundle.GenerationID)
		if loadErr != nil {
			preliminary = append(preliminary,
				"consumed obligation metadata could not be read from the sealed registry: "+loadErr.Error(),
			)
		} else if registry == nil {
			preliminary = append(preliminary, "consumed obligation metadata is unavailable from the sealed registry")
		} else {
			for _, obligation := range registry.Obligations {
				requirements.Obligations[obligation.ID] = obligation
			}
		}
	}

	facts, err := collectPipelineSealedActualFacts(st, *candidate)
	if err != nil {
		return result, err
	}
	result = matchPipelineSealedActualFacts(
		bundle.ProjectedDelta,
		facts,
		chapterBody,
		requirements,
	)
	result.MismatchReasons = normalizePipelineSealedReasons(
		append(preliminary, result.MismatchReasons...),
	)
	result.ProjectionMatch = result.Complete && len(result.MismatchReasons) == 0
	if result.ProjectionMatch {
		projectedDigest, projectedErr := domain.ComputeProjectedDeltaV2Digest(bundle.ProjectedDelta)
		actualDigest, actualErr := domain.ComputeProjectedDeltaV2Digest(result.ActualDelta)
		if projectedErr != nil || actualErr != nil || projectedDigest != actualDigest {
			result.ProjectionMatch = false
			result.MismatchReasons = normalizePipelineSealedReasons(append(
				result.MismatchReasons,
				"independently reconstructed actual delta differs from the sealed canonical transition",
			))
		}
	}
	return result, nil
}

// validatePipelineSealedActualConvergenceOverlay accepts a replacement plan
// only through the exact binding already verified against the live control
// plane before the isolated candidate was created. The actual delta, required
// beats and hard render contract below continue to come from the original
// immutable bundle; this overlay changes planning execution shape, not state.
func validatePipelineSealedActualConvergenceOverlay(
	bundle *domain.ProjectedChapterBundle,
	livePlan domain.ChapterPlan,
	livePlanDigest string,
	bundlePlanDigest string,
	verifiedBindings []*pipelineSealedRenderBinding,
) error {
	if bundle == nil || len(verifiedBindings) != 1 || verifiedBindings[0] == nil {
		return fmt.Errorf("verified convergence binding is absent")
	}
	binding := verifiedBindings[0]
	receipt := binding.ConvergenceReplanReceipt
	if receipt == nil {
		return fmt.Errorf("verified convergence receipt is absent")
	}
	if err := domain.ValidateSealedConvergenceReplanReceipt(*receipt); err != nil {
		return err
	}
	if binding.Bundle.BundleDigest != bundle.BundleDigest ||
		binding.Bundle.GenerationID != bundle.GenerationID ||
		binding.Bundle.Chapter != bundle.Chapter ||
		receipt.GenerationID != bundle.GenerationID ||
		receipt.Chapter != bundle.Chapter ||
		receipt.BundleDigest != bundle.BundleDigest ||
		receipt.PromotionReceiptDigest != binding.Promotion.ReceiptDigest ||
		receipt.SealedPlanSemanticDigest != bundlePlanDigest ||
		receipt.ReplacementPlanSemanticDigest != livePlanDigest ||
		receipt.StateContractDigest != pipelineSealedConvergenceStateContractDigest(
			livePlan,
			bundle.ChapterWorldSimulation,
			bundle.ProjectedDelta,
		) {
		return fmt.Errorf("verified convergence receipt does not bind exact bundle/replacement state")
	}
	return nil
}

func matchPipelineSealedActualFacts(
	projected domain.ProjectedDelta,
	facts []pipelineSealedActualFact,
	chapterBody string,
	requirements pipelineSealedActualRequirements,
) pipelineSealedActualDeltaMatch {
	result := pipelineSealedActualDeltaMatch{
		ActualDelta: domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version},
	}
	matchedFacts := make([]bool, len(facts))
	var reasons []string
	var evidence []pipelineSealedActualEvidence

	reasons = append(
		reasons,
		pipelineSealedPOVBoundaryViolations(chapterBody, requirements.Bundle)...,
	)

	for _, beat := range compactProjectAllStrings(requirements.RequiredBeats) {
		if locator := locatePipelineSealedBodyContract(chapterBody, beat, facts); locator == "" {
			reasons = append(reasons, fmt.Sprintf(
				"hard required beat has no locatable body evidence: %q",
				truncatePipelineSealedText(beat, 120),
			))
		} else {
			evidence = append(evidence, pipelineSealedActualEvidence{
				Category: "required_beat",
				StableID: "required-beat",
				Locator:  locator,
				After:    beat,
			})
		}
	}

	for _, category := range pipelineSealedProjectedCategories(projected) {
		for _, mutation := range category.Mutations {
			if category.Name == "obligation" {
				switch mutation.Operation {
				case "create", "carry":
					pipelineSealedAppendActualMutation(&result.ActualDelta, category.Name, mutation)
					evidence = append(evidence, pipelineSealedActualEvidence{
						Category: category.Name,
						StableID: mutation.StableID,
						Locator:  "sealed-control-plane:" + mutation.Subject,
						After:    mutation.After,
					})
					continue
				case "consume", "resolve":
					obligation, ok := requirements.Obligations[mutation.Subject]
					if !ok || strings.TrimSpace(obligation.Contract) == "" {
						reasons = append(reasons, fmt.Sprintf(
							"obligation[%s] is consumed but only an opaque id is available; sealed contract text is required",
							mutation.Subject,
						))
						continue
					}
					locator := ""
					if obligation.Hardness == domain.ObligationSoftV2 {
						locator = pipelineSealedOffscreenObligationEvidence(
							requirements.Bundle,
							obligation,
						)
					} else {
						locator = locatePipelineSealedBodyContract(chapterBody, obligation.Contract, facts)
					}
					if locator == "" {
						if obligation.Hardness == domain.ObligationSoftV2 {
							reasons = append(reasons, fmt.Sprintf(
								"soft obligation[%s] consume has no sealed offscreen simulation evidence",
								mutation.Subject,
							))
						} else {
							reasons = append(reasons, fmt.Sprintf(
								"obligation[%s] consume has no locatable body evidence (semantic) for contract %q",
								mutation.Subject,
								truncatePipelineSealedText(obligation.Contract, 120),
							))
						}
						continue
					}
					pipelineSealedAppendActualMutation(&result.ActualDelta, category.Name, mutation)
					result.ObligationsSatisfied = append(result.ObligationsSatisfied, mutation.Subject)
					evidence = append(evidence, pipelineSealedActualEvidence{
						Category: category.Name,
						StableID: mutation.StableID,
						Locator:  locator,
						After:    mutation.After,
					})
					for i := range facts {
						if facts[i].Category == "obligation" &&
							pipelineSealedIdentityEqual(facts[i].Subject, mutation.Subject) {
							matchedFacts[i] = true
						}
					}
					continue
				}
			}

			identityMatches := make([]int, 0)
			afterMatches := make([]int, 0)
			beforeMatches := make([]int, 0)
			for i := range facts {
				if !pipelineSealedActualIdentityMatches(category.Name, mutation, facts[i]) {
					continue
				}
				identityMatches = append(identityMatches, i)
				if pipelineSealedActualSupports(facts[i].After, mutation.After) {
					afterMatches = append(afterMatches, i)
				}
				if strings.TrimSpace(mutation.Before) != "" &&
					strings.TrimSpace(facts[i].Before) != "" &&
					pipelineSealedActualSupports(facts[i].Before, mutation.Before) {
					beforeMatches = append(beforeMatches, i)
				}
			}
			if len(identityMatches) == 0 {
				reasons = append(reasons, pipelineSealedMissingIdentityReason(category.Name, mutation))
				continue
			}
			if len(afterMatches) == 0 {
				reasons = append(reasons, fmt.Sprintf(
					"%s[%s] after mismatch: projected=%q actual=%v",
					category.Name,
					mutation.StableID,
					truncatePipelineSealedText(mutation.After, 100),
					pipelineSealedActualFactValues(facts, identityMatches),
				))
				pipelineSealedAppendActualMutation(
					&result.ActualDelta,
					category.Name,
					pipelineSealedActualMutationFromFact(mutation, facts[identityMatches[0]]),
				)
				continue
			}
			contradictory := false
			for _, index := range identityMatches {
				if facts[index].Hard &&
					!pipelineSealedActualSupports(facts[index].After, mutation.After) {
					contradictory = true
					break
				}
			}
			if contradictory {
				reasons = append(reasons, fmt.Sprintf(
					"%s[%s] has contradictory actual after values: %v",
					category.Name,
					mutation.StableID,
					pipelineSealedActualFactValues(facts, identityMatches),
				))
			}
			if strings.TrimSpace(mutation.Before) != "" {
				if len(beforeMatches) == 0 {
					reasons = append(reasons, fmt.Sprintf(
						"%s[%s] projected before=%q is not established by chapter-local state evidence",
						category.Name,
						mutation.StableID,
						truncatePipelineSealedText(mutation.Before, 100),
					))
				}
				for _, index := range identityMatches {
					if strings.TrimSpace(facts[index].Before) != "" &&
						!pipelineSealedActualSupports(facts[index].Before, mutation.Before) {
						reasons = append(reasons, fmt.Sprintf(
							"%s[%s] before contradiction: projected=%q actual=%q at %s",
							category.Name,
							mutation.StableID,
							truncatePipelineSealedText(mutation.Before, 100),
							truncatePipelineSealedText(facts[index].Before, 100),
							facts[index].Locator,
						))
					}
				}
			}

			chosen := facts[afterMatches[0]]
			locator := chosen.Locator
			if requirements.VisibleMutation[mutation.StableID] {
				bodyLocator := locatePipelineSealedBodyContract(
					chapterBody,
					pipelineSealedVisibleMutationContract(category.Name, mutation),
					facts,
				)
				if category.Name == "resource" && bodyLocator != "" {
					// A resource name plus transaction cause is not proof of its
					// terminal ownership/status. Locate the projected result
					// independently so prose that hands the item back cannot
					// pass merely because the server-side ledger says booked.
					afterLocator := locatePipelineSealedBodyContract(
						chapterBody,
						mutation.After,
						facts,
					)
					if afterLocator == "" {
						bodyLocator = ""
					} else {
						if contradiction := pipelineSealedVisibleResourceTerminalContradiction(
							chapterBody,
							mutation,
						); contradiction != "" {
							reasons = append(reasons, fmt.Sprintf(
								"resource[%s] terminal body transfer contradicts projected result %q: %s",
								mutation.StableID,
								truncatePipelineSealedText(mutation.After, 100),
								contradiction,
							))
						}
						if afterLocator != bodyLocator {
							bodyLocator += "+" + afterLocator
						}
					}
				}
				if bodyLocator == "" {
					reasons = append(reasons, fmt.Sprintf(
						"%s[%s] has ledger state but no locatable semantic body evidence for visible result %q",
						category.Name,
						mutation.StableID,
						truncatePipelineSealedText(mutation.After, 100),
					))
				} else {
					locator += "#" + bodyLocator
				}
			} else if offscreen := strings.TrimSpace(
				requirements.OffscreenEvidence[mutation.StableID],
			); offscreen != "" {
				locator += "#" + offscreen
			}
			pipelineSealedAppendActualMutation(&result.ActualDelta, category.Name, mutation)
			for i := range facts {
				if pipelineSealedActualFactsEquivalent(chosen, facts[i]) {
					matchedFacts[i] = true
				}
			}
			evidence = append(evidence, pipelineSealedActualEvidence{
				Category: category.Name,
				StableID: mutation.StableID,
				Locator:  locator,
				Before:   chosen.Before,
				After:    chosen.After,
			})
		}
	}

	for i, fact := range facts {
		if !fact.Hard || matchedFacts[i] {
			continue
		}
		reasons = append(reasons, fmt.Sprintf(
			"unplanned hard actual mutation: category=%s subject=%q object=%q field=%q after=%q locator=%s",
			fact.Category,
			fact.Subject,
			fact.Object,
			fact.Field,
			truncatePipelineSealedText(fact.After, 100),
			fact.Locator,
		))
		pipelineSealedAppendActualMutation(
			&result.ActualDelta,
			fact.Category,
			pipelineSealedExtraActualMutation(fact),
		)
	}

	result.ActualDelta = domain.NormalizeProjectedDeltaV2(result.ActualDelta)
	result.MismatchReasons = normalizePipelineSealedReasons(reasons)
	result.Evidence = normalizePipelineSealedEvidence(evidence)
	result.ObligationsSatisfied = compactProjectAllStrings(result.ObligationsSatisfied)
	if err := domain.ValidateProjectedDeltaV2(result.ActualDelta); err != nil {
		result.Complete = false
		result.MismatchReasons = normalizePipelineSealedReasons(append(
			result.MismatchReasons,
			"commit metadata cannot form a complete canonical actual delta: "+err.Error(),
		))
	} else {
		result.Complete = true
	}
	result.ProjectionMatch = result.Complete && len(result.MismatchReasons) == 0
	return result
}

func pipelineSealedVisibleMutationContract(
	category string,
	mutation domain.StateMutationV2,
) string {
	switch category {
	case "resource", "relationship", "character_state", "knowledge", "location":
		return strings.Join(compactProjectAllStrings([]string{
			mutation.Subject,
			mutation.Object,
			mutation.After,
			mutation.Cause,
		}), "；")
	default:
		return fallbackProjectAllText(mutation.After, mutation.Cause)
	}
}

// pipelineSealedVisibleResourceTerminalContradiction rejects a visible resource
// result that is established at one point in the chapter and then reversed by
// a later transfer away from the projected subject. The general semantic
// matcher intentionally remains unchanged: this narrow pass only recognizes
// explicit resource-transfer verbs after the first positive result span.
func pipelineSealedVisibleResourceTerminalContradiction(
	body string,
	mutation domain.StateMutationV2,
) string {
	segments := splitPipelineSealedBodySegments(body)
	if len(segments) == 0 {
		return ""
	}

	positiveIndex := -1
	for i, segment := range segments {
		if !pipelineSealedSemanticTextSupports(segment, mutation.After) ||
			pipelineSealedSegmentNegatesExpected(segment, mutation.After) {
			continue
		}
		if pipelineSealedResourceTransferAway(segment, mutation.Subject) {
			continue
		}
		positiveIndex = i
		break
	}

	// If the generic locator reached the body through an independently stored
	// evidence span rather than direct wording, scan the whole chapter. This is
	// fail-closed: an explicit transfer away is stronger than an opaque positive
	// locator whose segment position cannot be recovered here.
	scanFrom := 0
	lastResourceReference := -1
	if positiveIndex >= 0 {
		scanFrom = positiveIndex + 1
		lastResourceReference = positiveIndex
	}
	for i := scanFrom; i < len(segments); i++ {
		segment := strings.TrimSpace(segments[i])
		directReference := pipelineSealedResourceSegmentNamesObject(
			segment,
			mutation.Object,
		)
		pronounReference := lastResourceReference >= 0 &&
			i-lastResourceReference <= 2 &&
			pipelineSealedResourceSegmentUsesObjectPronoun(segment)
		if !directReference && !pronounReference {
			continue
		}
		if pipelineSealedResourceTransferAway(segment, mutation.Subject) {
			return "body:" + truncatePipelineSealedText(segment, 120)
		}
		lastResourceReference = i
	}
	return ""
}

func pipelineSealedResourceSegmentNamesObject(segment, object string) bool {
	segment = normalizePipelineSealedText(segment)
	object = normalizePipelineSealedText(object)
	return segment != "" && object != "" && strings.Contains(segment, object)
}

func pipelineSealedResourceSegmentUsesObjectPronoun(segment string) bool {
	segment = normalizePipelineSealedText(segment)
	for _, pronoun := range []string{
		"把它", "将它", "给它", "它又", "它被", "该物", "此物",
		"这张", "那张", "这份", "那份", "这件", "那件",
		"这把", "那把", "这笔", "那笔", "这个", "那个",
	} {
		if strings.Contains(segment, pronoun) {
			return true
		}
	}
	return false
}

func pipelineSealedResourceTransferAway(segment, projectedSubject string) bool {
	segment = normalizePipelineSealedText(segment)
	projectedSubject = normalizePipelineSealedText(projectedSubject)
	if segment == "" {
		return false
	}

	for _, marker := range []string{
		"交还", "归还", "退还", "返还", "还给", "交回",
		"交给", "转交", "移交", "递给", "交由",
	} {
		index := strings.Index(segment, marker)
		if index < 0 {
			continue
		}
		if pipelineSealedResourceTransferMarkerIsNegated(segment, marker, index) {
			continue
		}
		target := segment[index+len(marker):]
		if projectedSubject != "" && strings.Contains(target, projectedSubject) {
			return false
		}
		if strings.Contains(target, "自己") &&
			projectedSubject != "" &&
			strings.Contains(segment[:index], projectedSubject) {
			return false
		}
		return true
	}

	if letIndex := strings.Index(segment, "让"); letIndex >= 0 {
		if keepIndex := strings.Index(segment[letIndex+len("让"):], "保管"); keepIndex >= 0 {
			if pipelineSealedResourceTransferMarkerIsNegated(segment, "让", letIndex) {
				return false
			}
			target := segment[letIndex+len("让") : letIndex+len("让")+keepIndex]
			if projectedSubject != "" && strings.Contains(target, projectedSubject) {
				return false
			}
			if strings.Contains(target, "自己") &&
				projectedSubject != "" &&
				strings.Contains(segment[:letIndex], projectedSubject) {
				return false
			}
			return true
		}
	}

	for _, marker := range []string{
		"卖出", "卖给", "出售", "转卖", "丢弃", "丢掉", "扔掉", "遗弃",
	} {
		if index := strings.Index(segment, marker); index >= 0 &&
			!pipelineSealedResourceTransferMarkerIsNegated(segment, marker, index) {
			return true
		}
	}
	return false
}

func pipelineSealedResourceTransferMarkerIsNegated(
	segment, marker string,
	markerIndex int,
) bool {
	if markerIndex < 0 {
		markerIndex = strings.Index(segment, marker)
	}
	if markerIndex < 0 || markerIndex > len(segment) {
		return false
	}
	prefix := segment[:markerIndex]
	for _, negation := range []string{
		"没有", "并没有", "并未", "并不", "不曾", "从未",
		"不肯", "不愿", "拒绝", "尚未", "仍未", "未", "没",
		"不能", "不要", "不再",
	} {
		if strings.HasSuffix(prefix, negation) {
			return true
		}
		for _, objectMarker := range []string{"把", "将"} {
			if strings.Contains(prefix, negation+objectMarker) {
				return true
			}
		}
	}
	for _, phrase := range []string{
		"拒绝把", "拒绝将", "不肯把", "不肯将", "不愿把", "不愿将",
		"不能把", "不能将", "不要把", "不要将", "尚未把", "尚未将",
		"仍未把", "仍未将", "未把", "未将", "没把", "没将",
	} {
		if strings.Contains(prefix, phrase) {
			return true
		}
	}
	return false
}

type pipelineSealedProjectedCategory struct {
	Name      string
	Mutations []domain.StateMutationV2
}

func pipelineSealedProjectedCategories(delta domain.ProjectedDelta) []pipelineSealedProjectedCategory {
	return []pipelineSealedProjectedCategory{
		{Name: "timeline", Mutations: delta.Timeline},
		{Name: "character_state", Mutations: delta.CharacterState},
		{Name: "relationship", Mutations: delta.Relationships},
		{Name: "resource", Mutations: delta.Resources},
		{Name: "knowledge", Mutations: delta.Knowledge},
		{Name: "location", Mutations: delta.Locations},
		{Name: "foreshadow", Mutations: delta.Foreshadows},
		{Name: "obligation", Mutations: delta.Obligations},
	}
}

func collectPipelineSealedActualFacts(
	st *store.Store,
	candidate domain.ChapterWorldDelta,
) ([]pipelineSealedActualFact, error) {
	facts := make([]pipelineSealedActualFact, 0)
	add := func(fact pipelineSealedActualFact) {
		fact.Category = strings.TrimSpace(fact.Category)
		fact.Subject = strings.TrimSpace(fact.Subject)
		fact.Object = strings.TrimSpace(fact.Object)
		fact.Field = strings.TrimSpace(fact.Field)
		fact.Before = strings.TrimSpace(fact.Before)
		fact.After = strings.TrimSpace(fact.After)
		fact.Evidence = strings.TrimSpace(fact.Evidence)
		fact.Locator = strings.TrimSpace(fact.Locator)
		if fact.Category == "" || fact.Field == "" || fact.After == "" || fact.Locator == "" {
			return
		}
		facts = append(facts, fact)
	}
	for i, character := range candidate.CharacterDeltas {
		locator := fmt.Sprintf("chapter_world_delta.character_deltas[%d]", i)
		for _, field := range []struct {
			category string
			name     string
			value    string
		}{
			{"character_state", "state", character.Status},
			{"location", "location", character.Location},
			{"knowledge", "knowledge_boundary", character.KnowledgeBoundary},
			{"character_state", "death_state", character.DeathState},
			{"character_state", "personality", character.PersonalityDelta},
		} {
			if strings.TrimSpace(field.value) == "" {
				continue
			}
			add(pipelineSealedActualFact{
				Category: field.category,
				Subject:  character.Character,
				Field:    field.name,
				After:    field.value,
				Locator:  locator + "." + field.name,
				// CharacterDeltas are full-character snapshots. Their fields
				// can prove a projected terminal value, but an unmatched
				// baseline status/death/personality field is not by itself
				// evidence that this chapter introduced a new mutation.
				Hard: false,
			})
		}
	}
	for i, world := range candidate.WorldDeltas {
		kind := normalizePipelineSealedKind(world.Kind)
		locator := fmt.Sprintf("chapter_world_delta.world_deltas[%d]", i)
		fact := pipelineSealedActualFact{
			After:    world.Change,
			Evidence: world.Evidence,
			Locator:  locator,
			Hard:     true,
		}
		switch kind {
		case "timeline":
			fact.Category, fact.Subject, fact.Field = "timeline", "chapter", "outcome"
			// A committed chapter may retain several scene-granularity
			// timeline events for one projected chapter-level outcome.
			fact.Hard = false
		case "state":
			fact.Subject, fact.Field = splitPipelineSealedStateEntity(world.Entity)
			fact.Category = pipelineSealedCategoryForStateField(fact.Field)
			fact.Before, fact.After = splitPipelineSealedTransition(world.Change)
		case "relationship":
			fact.Category, fact.Field = "relationship", "relationship"
			fact.Subject, fact.Object = splitPipelineSealedPair(world.Entity)
		case "resource", "resource_booked", "resource_pending":
			fact.Category, fact.Field, fact.Object = "resource", "resource", world.Entity
		case "knowledge":
			fact.Category, fact.Subject, fact.Field = "knowledge", world.Entity, "knowledge_boundary"
		case "location":
			fact.Category, fact.Subject, fact.Field = "location", world.Entity, "location"
		case "foreshadow":
			fact.Category, fact.Object, fact.Field = "foreshadow", world.Entity, "evidence_return"
		case "obligation":
			fact.Category, fact.Subject, fact.Field = "obligation", world.Entity, "state"
		default:
			fact.Hard = false
		}
		add(fact)
	}

	timeline, err := st.World.LoadTimeline()
	if err != nil {
		return nil, fmt.Errorf("load actual timeline ledger: %w", err)
	}
	for i, event := range timeline {
		if event.Chapter != candidate.Chapter || strings.TrimSpace(event.Event) == "" {
			continue
		}
		add(pipelineSealedActualFact{
			Category: "timeline",
			Subject:  "chapter",
			Field:    "outcome",
			After:    event.Event,
			Locator:  fmt.Sprintf("timeline.json#chapter=%d,index=%d", candidate.Chapter, i),
			// Timeline entries are append-only narrative granularity, not
			// necessarily distinct formal state mutations.
			Hard: false,
		})
	}
	stateChanges, err := st.World.LoadStateChanges()
	if err != nil {
		return nil, fmt.Errorf("load actual state ledger: %w", err)
	}
	for i, change := range stateChanges {
		if change.Chapter != candidate.Chapter || strings.TrimSpace(change.NewValue) == "" {
			continue
		}
		category := pipelineSealedCategoryForStateField(change.Field)
		subject, object := change.Entity, ""
		if category == "relationship" {
			subject, object = splitPipelineSealedPair(change.Entity)
		}
		add(pipelineSealedActualFact{
			Category: category,
			Subject:  subject,
			Object:   object,
			Field:    pipelineSealedCanonicalField(category, change.Field),
			Before:   change.OldValue,
			After:    change.NewValue,
			Evidence: change.Reason,
			Locator:  fmt.Sprintf("meta/state_changes.json#chapter=%d,index=%d", candidate.Chapter, i),
			Hard:     true,
		})
	}
	relationships, err := st.World.LoadRelationships()
	if err != nil {
		return nil, fmt.Errorf("load actual relationship ledger: %w", err)
	}
	for i, relationship := range relationships {
		if relationship.Chapter != candidate.Chapter || strings.TrimSpace(relationship.Relation) == "" {
			continue
		}
		add(pipelineSealedActualFact{
			Category: "relationship",
			Subject:  relationship.CharacterA,
			Object:   relationship.CharacterB,
			Field:    "relationship",
			After:    relationship.Relation,
			Locator:  fmt.Sprintf("relationship_state.json#chapter=%d,index=%d", candidate.Chapter, i),
			Hard:     true,
		})
	}
	resourceLedger, err := st.ResourceLedger.Load()
	if err != nil {
		return nil, fmt.Errorf("load actual resource ledger: %w", err)
	}
	if resourceLedger != nil {
		for i, claim := range resourceLedger.Claims {
			if claim.Chapter != candidate.Chapter || strings.TrimSpace(claim.Name) == "" {
				continue
			}
			add(pipelineSealedActualFact{
				Category: "resource",
				Subject:  claim.Owner,
				Object:   claim.Name,
				Field:    "resource",
				After: strings.Join(compactProjectAllStrings([]string{
					claim.Status,
					claim.Risk,
					claim.Evidence,
				}), "；"),
				Evidence: claim.Evidence,
				Locator:  fmt.Sprintf("resource_ledger.json#chapter=%d,index=%d", candidate.Chapter, i),
				Hard:     true,
			})
		}
	}
	foreshadows, err := st.World.LoadForeshadowLedger()
	if err != nil {
		return nil, fmt.Errorf("load actual foreshadow ledger: %w", err)
	}
	for i, entry := range foreshadows {
		if entry.PlantedAt != candidate.Chapter && entry.ResolvedAt != candidate.Chapter {
			continue
		}
		add(pipelineSealedActualFact{
			Category: "foreshadow",
			Object:   entry.ID,
			Field:    "evidence_return",
			After: strings.Join(compactProjectAllStrings([]string{
				entry.Status,
				entry.Description,
			}), "；"),
			Locator: fmt.Sprintf("foreshadow_ledger.json#chapter=%d,index=%d", candidate.Chapter, i),
			Hard:    true,
		})
	}
	return facts, nil
}

func pipelineSealedActualIdentityMatches(
	category string,
	mutation domain.StateMutationV2,
	fact pipelineSealedActualFact,
) bool {
	if category != fact.Category ||
		!pipelineSealedIdentityEqual(
			pipelineSealedCanonicalField(category, mutation.Field),
			pipelineSealedCanonicalField(category, fact.Field),
		) {
		return false
	}
	switch category {
	case "timeline":
		return pipelineSealedIdentityEqual(mutation.Subject, "chapter") &&
			pipelineSealedIdentityEqual(fact.Subject, "chapter")
	case "relationship":
		if strings.TrimSpace(mutation.Subject) == "" || strings.TrimSpace(mutation.Object) == "" ||
			strings.TrimSpace(fact.Subject) == "" || strings.TrimSpace(fact.Object) == "" {
			return false
		}
		return (pipelineSealedIdentityEqual(mutation.Subject, fact.Subject) &&
			pipelineSealedIdentityEqual(mutation.Object, fact.Object)) ||
			(pipelineSealedIdentityEqual(mutation.Subject, fact.Object) &&
				pipelineSealedIdentityEqual(mutation.Object, fact.Subject))
	case "resource":
		if strings.TrimSpace(mutation.Object) == "" || strings.TrimSpace(fact.Object) == "" ||
			!pipelineSealedIdentityEqual(mutation.Object, fact.Object) {
			return false
		}
		if strings.TrimSpace(mutation.Subject) != "" && strings.TrimSpace(fact.Subject) != "" {
			return pipelineSealedIdentityEqual(mutation.Subject, fact.Subject)
		}
		return strings.TrimSpace(mutation.Subject) == ""
	case "foreshadow":
		return strings.TrimSpace(mutation.Object) != "" &&
			strings.TrimSpace(fact.Object) != "" &&
			pipelineSealedIdentityEqual(mutation.Object, fact.Object)
	default:
		return strings.TrimSpace(mutation.Subject) != "" &&
			strings.TrimSpace(fact.Subject) != "" &&
			pipelineSealedIdentityEqual(mutation.Subject, fact.Subject)
	}
}

func pipelineSealedMissingIdentityReason(category string, mutation domain.StateMutationV2) string {
	switch category {
	case "relationship":
		if strings.TrimSpace(mutation.Object) == "" {
			return fmt.Sprintf("relationship[%s] lacks the second character object; commit evidence cannot identify the pair", mutation.StableID)
		}
	case "resource":
		if strings.TrimSpace(mutation.Object) == "" {
			return fmt.Sprintf("resource[%s] lacks a stable resource object; commit evidence cannot identify the claim", mutation.StableID)
		}
	case "foreshadow":
		if strings.TrimSpace(mutation.Object) == "" {
			return fmt.Sprintf("foreshadow[%s] lacks a stable foreshadow object; commit evidence cannot identify the thread", mutation.StableID)
		}
	}
	return fmt.Sprintf(
		"%s[%s] has no structured actual evidence for subject=%q object=%q field=%q",
		category,
		mutation.StableID,
		mutation.Subject,
		mutation.Object,
		mutation.Field,
	)
}

func pipelineSealedAppendActualMutation(
	delta *domain.ProjectedDelta,
	category string,
	mutation domain.StateMutationV2,
) {
	if delta == nil {
		return
	}
	switch category {
	case "timeline":
		delta.Timeline = append(delta.Timeline, mutation)
	case "character_state":
		delta.CharacterState = append(delta.CharacterState, mutation)
	case "relationship":
		delta.Relationships = append(delta.Relationships, mutation)
	case "resource":
		delta.Resources = append(delta.Resources, mutation)
	case "knowledge":
		delta.Knowledge = append(delta.Knowledge, mutation)
	case "location":
		delta.Locations = append(delta.Locations, mutation)
	case "foreshadow":
		delta.Foreshadows = append(delta.Foreshadows, mutation)
	case "obligation":
		delta.Obligations = append(delta.Obligations, mutation)
	}
}

func pipelineSealedActualMutationFromFact(
	projected domain.StateMutationV2,
	fact pipelineSealedActualFact,
) domain.StateMutationV2 {
	actual := projected
	actual.Before = fact.Before
	actual.After = fact.After
	actual.Evidence = fact.Locator
	return actual
}

func pipelineSealedExtraActualMutation(fact pipelineSealedActualFact) domain.StateMutationV2 {
	hash, _ := domain.DeterministicPlanningHash(struct {
		Category string `json:"category"`
		Subject  string `json:"subject"`
		Object   string `json:"object"`
		Field    string `json:"field"`
		Before   string `json:"before"`
		After    string `json:"after"`
		Locator  string `json:"locator"`
	}{
		fact.Category,
		fact.Subject,
		fact.Object,
		fact.Field,
		fact.Before,
		fact.After,
		fact.Locator,
	})
	hash = strings.TrimPrefix(hash, domain.PlanningV2DigestPrefix)
	if len(hash) > 20 {
		hash = hash[:20]
	}
	operation := "update"
	if fact.Category == "timeline" || fact.Category == "foreshadow" {
		operation = "advance"
	}
	return domain.StateMutationV2{
		StableID:  "actual:" + hash,
		Subject:   fallbackProjectAllText(fact.Subject, fact.Object, fact.Category),
		Object:    fact.Object,
		Field:     fallbackProjectAllText(fact.Field, fact.Category),
		Operation: operation,
		Before:    fact.Before,
		After:     fact.After,
		Cause:     "independent commit metadata",
		Evidence:  fact.Locator,
	}
}

func pipelineSealedActualFactsEquivalent(
	left, right pipelineSealedActualFact,
) bool {
	if left.Category != right.Category ||
		!pipelineSealedIdentityEqual(left.Field, right.Field) {
		return false
	}
	switch left.Category {
	case "timeline":
		return pipelineSealedActualSupports(left.After, right.After) ||
			pipelineSealedActualSupports(right.After, left.After)
	case "relationship":
		pairEqual := (pipelineSealedIdentityEqual(left.Subject, right.Subject) &&
			pipelineSealedIdentityEqual(left.Object, right.Object)) ||
			(pipelineSealedIdentityEqual(left.Subject, right.Object) &&
				pipelineSealedIdentityEqual(left.Object, right.Subject))
		return pairEqual && (pipelineSealedActualSupports(left.After, right.After) ||
			pipelineSealedActualSupports(right.After, left.After))
	case "resource", "foreshadow":
		return pipelineSealedIdentityEqual(left.Object, right.Object) &&
			(pipelineSealedActualSupports(left.After, right.After) ||
				pipelineSealedActualSupports(right.After, left.After))
	default:
		return pipelineSealedIdentityEqual(left.Subject, right.Subject) &&
			(pipelineSealedActualSupports(left.After, right.After) ||
				pipelineSealedActualSupports(right.After, left.After))
	}
}

func classifyPipelineSealedMutationVisibility(
	bundle *domain.ProjectedChapterBundle,
	requirements *pipelineSealedActualRequirements,
) {
	if bundle == nil || requirements == nil {
		return
	}
	visibleCharacter := make(map[string]bool)
	protagonist := strings.TrimSpace(bundle.POVPlan.POVCharacterID)
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		visibleCharacter[name] = decision.VisibleToPOV || (protagonist != "" && name == protagonist)
	}
	contractContains := func(values []string, mutation domain.StateMutationV2) bool {
		for _, value := range values {
			if pipelineSealedSemanticTextSupports(value, mutation.After) ||
				pipelineSealedSemanticTextSupports(mutation.After, value) {
				return true
			}
		}
		return false
	}
	for _, category := range pipelineSealedProjectedCategories(bundle.ProjectedDelta) {
		for _, mutation := range category.Mutations {
			visible := true
			subject := strings.TrimSpace(mutation.Subject)
			switch category.Name {
			case "timeline":
				visible = true
			case "character_state":
				if known, ok := visibleCharacter[subject]; ok {
					visible = known
				}
			case "location":
				// Seeing a person through video or hearing them by phone does not
				// reveal their physical location. Location needs the POV's own
				// position, an on-scene spatial anchor, or an explicit transmitted
				// fact; VisibleToPOV alone is intentionally insufficient.
				visible = subject != "" && (subject == protagonist ||
					pipelineSealedCharacterPresentAtProjectedPOVLocation(bundle.POVPlan, mutation) ||
					pipelineSealedPOVExplicitlyKnowsMutation(bundle, mutation))
			case "knowledge":
				// A visible character's private knowledge is not itself visible.
				// The auto-derived KnowledgeChanges contract cannot circularly
				// prove prose visibility; require the POV's own boundary or an
				// explicit observable transmission instead.
				visible = subject != "" && (subject == protagonist ||
					pipelineSealedPOVExplicitlyKnowsMutation(bundle, mutation))
			case "relationship":
				visible = visibleCharacter[strings.TrimSpace(mutation.Subject)] ||
					visibleCharacter[strings.TrimSpace(mutation.Object)] ||
					contractContains(bundle.HardRenderContract.RelationshipChanges, mutation)
			case "resource":
				visible = contractContains(bundle.HardRenderContract.ResourceChanges, mutation) ||
					visibleCharacter[strings.TrimSpace(mutation.Subject)]
			case "foreshadow":
				visible = contractContains(bundle.HardRenderContract.ForeshadowChanges, mutation)
			case "obligation":
				if obligation, ok := requirements.Obligations[mutation.Subject]; ok {
					visible = obligation.Hardness == domain.ObligationHardV2
				}
			}
			requirements.VisibleMutation[mutation.StableID] = visible
			if !visible {
				requirements.OffscreenEvidence[mutation.StableID] = fmt.Sprintf(
					"sealed-offscreen-simulation:%s#%s",
					bundle.ChapterWorldSimulation.SimulationID,
					mutation.StableID,
				)
			}
		}
	}
}

func pipelineSealedCharacterPresentAtProjectedPOVLocation(
	plan domain.POVPlanV2,
	mutation domain.StateMutationV2,
) bool {
	subject := normalizePipelineSealedText(mutation.Subject)
	after := strings.TrimSpace(mutation.After)
	if subject == "" || after == "" {
		return false
	}
	for _, scene := range plan.Scenes {
		location := strings.TrimSpace(scene.Location)
		if location == "" || strings.EqualFold(location, "unknown") ||
			!pipelineSealedSafetyTextSupports(location, after) {
			continue
		}
		for _, actor := range scene.PresentActors {
			if normalizePipelineSealedText(actor) == subject {
				return true
			}
		}
	}
	return false
}

func pipelineSealedPOVExplicitlyKnowsMutation(
	bundle *domain.ProjectedChapterBundle,
	mutation domain.StateMutationV2,
) bool {
	if bundle == nil {
		return false
	}
	subject := normalizePipelineSealedText(mutation.Subject)
	after := strings.TrimSpace(mutation.After)
	if subject == "" || after == "" {
		return false
	}
	var texts []string
	for _, scene := range bundle.POVPlan.Scenes {
		texts = append(texts, scene.POVKnows...)
	}
	texts = append(texts, bundle.ChapterWorldSimulation.ProtagonistProjection.ObservableEffects...)
	for _, value := range texts {
		if strings.Contains(normalizePipelineSealedText(value), subject) &&
			pipelineSealedSafetyTextSupports(value, after) {
			return true
		}
	}
	return false
}

func pipelineSealedPOVBoundaryViolations(
	body string,
	bundle *domain.ProjectedChapterBundle,
) []string {
	if bundle == nil || strings.TrimSpace(body) == "" {
		return nil
	}
	var reasons []string
	for _, forbidden := range bundle.HardRenderContract.MustNotOccur {
		for _, probe := range pipelineSealedForbiddenContractProbes(forbidden) {
			if pipelineSealedForbiddenProbeEligible(probe) &&
				pipelineSealedBodyAffirmativelyContains(body, probe) {
				reasons = append(reasons, fmt.Sprintf(
					"hard forbidden move appears in body: %q",
					truncatePipelineSealedText(forbidden, 120),
				))
				break
			}
		}
	}
	for _, preserved := range bundle.HardRenderContract.MustPreserve {
		for _, clause := range splitPipelineSealedContractClauses(preserved) {
			if probe, negative := pipelineSealedNegativeContractProbe(clause); negative {
				if utf8.RuneCountInString(normalizePipelineSealedText(probe)) >= 4 &&
					pipelineSealedBodyAffirmativelyContains(body, probe) {
					reasons = append(reasons, fmt.Sprintf(
						"hard continuity contract was contradicted in body: %q",
						truncatePipelineSealedText(clause, 120),
					))
				}
				continue
			}
			if pipelineSealedBodyExplicitlyNegatesContract(body, clause) {
				reasons = append(reasons, fmt.Sprintf(
					"hard preserved fact was explicitly negated in body: %q",
					truncatePipelineSealedText(clause, 120),
				))
			}
		}
	}
	for _, reveal := range bundle.HardRenderContract.RevealBudget {
		for _, clause := range splitPipelineSealedContractClauses(reveal.Limit) {
			probe, negative := pipelineSealedNegativeContractProbe(clause)
			if !negative ||
				utf8.RuneCountInString(normalizePipelineSealedText(probe)) < 4 {
				reasons = append(reasons, fmt.Sprintf(
					"hard reveal budget is not mechanically enforceable because every clause needs an explicit forbidden fact with at least 4 content characters: %q",
					truncatePipelineSealedText(reveal.Limit, 120),
				))
				continue
			}
			if pipelineSealedBodyAffirmativelyContains(body, probe) {
				reasons = append(reasons, fmt.Sprintf(
					"hard reveal budget was exceeded in body: %q",
					truncatePipelineSealedText(reveal.Limit, 120),
				))
			}
		}
	}
	unknowns := append([]string(nil), bundle.POVPlan.Unknowns...)
	for _, scene := range bundle.POVPlan.Scenes {
		unknowns = append(unknowns, scene.POVDoesNotKnow...)
	}
	unknowns = append(unknowns, bundle.ChapterWorldSimulation.ProtagonistProjection.HiddenPressures...)
	for _, unknown := range compactProjectAllStrings(unknowns) {
		if utf8.RuneCountInString(normalizePipelineSealedText(unknown)) >= 6 &&
			pipelineSealedBodyLeaksUnknown(body, unknown) {
			reasons = append(reasons, fmt.Sprintf(
				"POV/reveal boundary leaked a sealed unknown into body: %q",
				truncatePipelineSealedText(unknown, 120),
			))
		}
	}
	protagonist := strings.TrimSpace(bundle.POVPlan.POVCharacterID)
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if decision.VisibleToPOV || name == "" || name == protagonist ||
			!pipelineSealedBodyContains(body, name) {
			continue
		}
		hiddenFacts := []string{
			decision.Decision,
			decision.Action,
			decision.ImmediateResult,
			decision.StateAfter,
		}
		for _, effect := range decision.ButterflyEffects {
			if effect.Visibility != "visible" {
				hiddenFacts = append(hiddenFacts, effect.Effect)
			}
		}
		for _, fact := range compactProjectAllStrings(hiddenFacts) {
			if utf8.RuneCountInString(normalizePipelineSealedText(fact)) >= 4 &&
				pipelineSealedBodySemanticallyContains(body, fact) {
				reasons = append(reasons, fmt.Sprintf(
					"body exposes offscreen state for %s before its visibility path: %q",
					name,
					truncatePipelineSealedText(fact, 100),
				))
				break
			}
		}
	}
	return normalizePipelineSealedReasons(reasons)
}

func pipelineSealedForbiddenProbeEligible(probe string) bool {
	probe = normalizePipelineSealedText(probe)
	if utf8.RuneCountInString(probe) >= 4 {
		return true
	}
	// Compact Chinese actions can be complete prohibitions even below the
	// general fuzzy-matching floor. They are matched by exact containment, not
	// bigram overlap, so admitting them does not weaken the fuzzy threshold.
	for _, action := range []string{
		"复合", "亲吻", "告白", "追捕", "搏斗", "下单", "出餐", "撤诉", "报警",
	} {
		if probe == action {
			return true
		}
	}
	return false
}

func pipelineSealedOffscreenObligationEvidence(
	bundle *domain.ProjectedChapterBundle,
	obligation domain.ObligationV2,
) string {
	if bundle == nil || strings.TrimSpace(obligation.Contract) == "" {
		return ""
	}
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		if decision.VisibleToPOV {
			continue
		}
		candidates := []string{
			decision.Decision,
			decision.Action,
			decision.ImmediateResult,
			decision.StateAfter,
		}
		for _, effect := range decision.ButterflyEffects {
			candidates = append(candidates, effect.Effect, effect.ProtagonistImpact)
		}
		for _, candidate := range compactProjectAllStrings(candidates) {
			if pipelineSealedSemanticTextSupports(candidate, obligation.Contract) ||
				pipelineSealedSemanticTextSupports(obligation.Contract, candidate) {
				return fmt.Sprintf(
					"sealed-offscreen-obligation:%s#%s",
					bundle.ChapterWorldSimulation.SimulationID,
					obligation.ID,
				)
			}
		}
	}
	return ""
}

func locatePipelineSealedBodyContract(
	body, contract string,
	facts []pipelineSealedActualFact,
) string {
	contract = strings.TrimSpace(contract)
	if contract == "" || strings.TrimSpace(body) == "" {
		return ""
	}
	// Outcome status contracts have polarity and terminal-state semantics that a
	// single fuzzy sentence match cannot preserve (for example, “两条都排除”
	// shares most of its text with “两条仍未排除”). Route these shapes through
	// the bounded status locator exclusively so its full-body contradiction check
	// remains authoritative.
	if _, outcomeStatus := parsePipelineSealedOutcomeStatusContract(contract); outcomeStatus {
		return locatePipelineSealedOutcomeStatusWindow(body, contract)
	}
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedSemanticTextSupports(segment, contract) &&
			!pipelineSealedSegmentNegatesExpected(segment, contract) {
			return "body:" + truncatePipelineSealedText(strings.TrimSpace(segment), 120)
		}
	}
	// Some hard beats deliberately describe an ordered causal unit instead of
	// wording that should appear in one sentence. Keep those slots separate:
	// a name reveal cannot also stand in for the strategy change or for the
	// later visible consequence merely because a wide text window shares four
	// common bigrams with the whole plan sentence.
	if locator := locatePipelineSealedOrderedNameConsequence(body, contract); locator != "" {
		return locator
	}
	// A public reputation reversal is often rendered as a small topology rather
	// than one summary sentence: comments accelerate, reposts spread, then a
	// named accusation becomes a headline or proof. Keep this fallback narrow so
	// ordinary timeline outcomes still use the existing segment/fact thresholds.
	if locator := locatePipelineSealedPublicStigmaWindow(body, contract); locator != "" {
		return locator
	}
	for _, fact := range facts {
		evidence := strings.TrimSpace(fact.Evidence)
		if evidence == "" || !pipelineSealedBodyContains(body, evidence) {
			continue
		}
		if pipelineSealedContractEvidenceOverlap(contract, evidence) {
			return fact.Locator + "#body:" + truncatePipelineSealedText(evidence, 100)
		}
	}
	return ""
}

type pipelineSealedBodyParagraphSpan struct {
	Text      string
	StartRune int
	EndRune   int
}

type pipelineSealedBodyParagraphWindow struct {
	Start int
	End   int
	Text  string
}

type pipelineSealedOrderedNameContract struct {
	Timing  string
	Subject string
	Target  string
}

type pipelineSealedPublicStigmaContract struct {
	Subject string
	Label   string
}

type pipelineSealedOutcomeStatusContract struct {
	Kind    string
	Subject string
}

const (
	pipelineSealedOutcomeStatusRisk              = "risk"
	pipelineSealedOutcomeStatusCandidates        = "candidates"
	pipelineSealedOutcomeStatusResolvedCandidate = "resolved-candidate"
)

// locatePipelineSealedOutcomeStatusWindow recognizes three result-level shapes
// that ordinary sentence matching cannot prove when prose distributes the
// decision and its visible status over nearby paragraphs:
//
//   - a named option is demoted to a high-risk choice;
//   - multiple candidate routes remain retained or pending.
//   - a concrete candidate becomes the uniquely confirmed entry after multiple
//     independent sources agree.
//
// It is not a general bag-of-words fallback. Risk/pending status stays bounded
// to two paragraphs; a resolved candidate may use five so prose can separate
// the entry, source checks and decision. Explicit terminal-state checks prevent
// an old candidate mention from proving a later incompatible outcome.
func locatePipelineSealedOutcomeStatusWindow(body, contract string) string {
	parsed, ok := parsePipelineSealedOutcomeStatusContract(contract)
	if !ok {
		return ""
	}
	paragraphs := splitPipelineSealedBodyParagraphSpans(body)
	if parsed.Kind == pipelineSealedOutcomeStatusResolvedCandidate {
		return locatePipelineSealedResolvedCandidateWindow(paragraphs, parsed)
	}
	fullBody := normalizePipelineSealedText(body)
	if parsed.Kind == pipelineSealedOutcomeStatusRisk &&
		pipelineSealedOutcomeRiskStatusContradicted(fullBody) {
		return ""
	}
	if parsed.Kind == pipelineSealedOutcomeStatusCandidates &&
		pipelineSealedOutcomeCandidateStatusContradicted(fullBody) {
		return ""
	}
	for start := 0; start < len(paragraphs); start++ {
		for end := start; end < len(paragraphs) && end < start+2; end++ {
			window := joinPipelineSealedParagraphWindow(paragraphs, start, end)
			normalized := normalizePipelineSealedText(window.Text)
			matched := false
			switch parsed.Kind {
			case pipelineSealedOutcomeStatusRisk:
				matched = pipelineSealedOutcomeRiskStatusMatches(normalized, parsed.Subject)
			case pipelineSealedOutcomeStatusCandidates:
				matched = pipelineSealedOutcomeCandidateStatusMatches(normalized)
			}
			if !matched {
				continue
			}
			guardStart := start - 1
			if guardStart < 0 {
				guardStart = 0
			}
			guardEnd := end + 1
			if guardEnd >= len(paragraphs) {
				guardEnd = len(paragraphs) - 1
			}
			guard := normalizePipelineSealedText(
				joinPipelineSealedParagraphWindow(paragraphs, guardStart, guardEnd).Text,
			)
			if parsed.Kind == pipelineSealedOutcomeStatusRisk &&
				pipelineSealedOutcomeRiskStatusContradicted(guard) {
				continue
			}
			if parsed.Kind == pipelineSealedOutcomeStatusCandidates &&
				pipelineSealedOutcomeCandidateStatusContradicted(guard) {
				continue
			}
			return fmt.Sprintf(
				"body:outcome-status:%s[%d-%d]=%s",
				parsed.Kind,
				window.Start+1,
				window.End+1,
				truncatePipelineSealedText(window.Text, 100),
			)
		}
	}
	return ""
}

func parsePipelineSealedOutcomeStatusContract(
	contract string,
) (pipelineSealedOutcomeStatusContract, bool) {
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return pipelineSealedOutcomeStatusContract{}, false
	}
	if pipelineSealedContainsAny(contract, []string{"唯一入口", "唯一安全入口"}) &&
		pipelineSealedContainsAny(contract, []string{"确认", "锁定", "变为", "成为"}) &&
		pipelineSealedOutcomeVerificationSourceCount(normalizePipelineSealedText(contract)) >= 2 {
		for _, predicate := range []string{
			"从候选路径变为", "从候选入口变为", "由候选路径变为", "由候选入口变为",
			"被共同确认为", "被确认为", "被共同确认", "被确认", "被锁定为", "被锁定",
			"变为", "成为",
		} {
			index := strings.Index(contract, predicate)
			if index <= 0 {
				continue
			}
			subject := strings.Trim(strings.TrimSpace(contract[:index]), "，,；;。:：‘’“”\"")
			for _, prefix := range []string{"但是", "但", "随后", "因此", "于是"} {
				subject = strings.TrimSpace(strings.TrimPrefix(subject, prefix))
			}
			if runes := utf8.RuneCountInString(subject); runes >= 4 && runes <= 24 {
				return pipelineSealedOutcomeStatusContract{
					Kind:    pipelineSealedOutcomeStatusResolvedCandidate,
					Subject: subject,
				}, true
			}
		}
	}
	if pipelineSealedContainsAny(contract, []string{"风险", "危险"}) {
		for _, predicate := range []string{
			"被明确降为", "被降为", "明确降为", "降为",
			"被列为", "列为", "被调为", "调为", "降级为",
		} {
			index := strings.Index(contract, predicate)
			if index <= 0 {
				continue
			}
			subject := strings.Trim(strings.TrimSpace(contract[:index]), "，,；;。:：‘’“”\"")
			for _, prefix := range []string{"但是", "但", "随后", "因此", "于是"} {
				subject = strings.TrimSpace(strings.TrimPrefix(subject, prefix))
			}
			if runes := utf8.RuneCountInString(subject); runes >= 2 && runes <= 16 {
				return pipelineSealedOutcomeStatusContract{
					Kind:    pipelineSealedOutcomeStatusRisk,
					Subject: subject,
				}, true
			}
		}
	}
	if pipelineSealedContainsAny(contract, []string{"两条", "两处", "两路", "两个", "双路"}) &&
		pipelineSealedContainsAny(contract, []string{"路径", "路线", "候选", "入口", "方案"}) &&
		pipelineSealedContainsAny(contract, []string{"未排除", "待核", "未定", "保留"}) {
		return pipelineSealedOutcomeStatusContract{
			Kind: pipelineSealedOutcomeStatusCandidates,
		}, true
	}
	return pipelineSealedOutcomeStatusContract{}, false
}

// locatePipelineSealedResolvedCandidateWindow binds the resolution predicate
// to the concrete entry in the resolving paragraph. Up to four immediately
// preceding paragraphs may supply independent source checks. Only statements
// after that resolution can undo it; an inherited two-candidate state before
// the decision is therefore allowed, while a later return to that state is not.
func locatePipelineSealedResolvedCandidateWindow(
	paragraphs []pipelineSealedBodyParagraphSpan,
	contract pipelineSealedOutcomeStatusContract,
) string {
	subject := normalizePipelineSealedText(contract.Subject)
	if subject == "" {
		return ""
	}
	for resolvedAt := 0; resolvedAt < len(paragraphs); resolvedAt++ {
		resolution := normalizePipelineSealedText(paragraphs[resolvedAt].Text)
		if pipelineSealedOutcomeResolvedCandidateContradicted(resolution) {
			continue
		}
		start := resolvedAt - 4
		if start < 0 {
			start = 0
		}
		window := joinPipelineSealedParagraphWindow(paragraphs, start, resolvedAt)
		normalized := normalizePipelineSealedText(window.Text)
		// A natural operational handoff often names the full door first, then
		// records the independent checks, and finally issues a short radio order
		// using only its identifier (for example, "下层 B17，按既定处置进入").
		// Bind that distributed form only when the same five-paragraph window has
		// the concrete entity, excludes its competing candidate, and ends in an
		// actual entry order. The ordinary explicit "确认为唯一入口" form remains
		// stricter and still requires the entity in its resolving paragraph.
		explicitResolution := pipelineSealedOutcomeSubjectMatches(resolution, subject) &&
			pipelineSealedOutcomeResolutionPredicate(resolution)
		distributedOperationalResolution :=
			pipelineSealedOutcomeSubjectMatches(normalized, subject) &&
				pipelineSealedOutcomeOperationalResolutionPredicate(resolution) &&
				pipelineSealedOutcomeAlternativeExcluded(normalized)
		if !explicitResolution && !distributedOperationalResolution {
			continue
		}
		if pipelineSealedOutcomeVerificationSourceCount(normalized) < 2 {
			continue
		}
		if resolvedAt+1 < len(paragraphs) {
			suffix := normalizePipelineSealedText(
				joinPipelineSealedParagraphWindow(paragraphs, resolvedAt+1, len(paragraphs)-1).Text,
			)
			if pipelineSealedOutcomeResolvedCandidateContradicted(suffix) {
				return ""
			}
		}
		return fmt.Sprintf(
			"body:outcome-status:%s[%d-%d]=%s",
			contract.Kind,
			window.Start+1,
			window.End+1,
			truncatePipelineSealedText(window.Text, 120),
		)
	}
	return ""
}

// pipelineSealedOutcomeSubjectMatches accepts the exact normalized facility
// name and the common form where a short door identifier is inserted directly
// before its type: "下层卸货坡道，B17 消防门". It does not permit prose words in
// that gap, so nearby mentions of another door cannot be fused into the entity.
func pipelineSealedOutcomeSubjectMatches(value, subject string) bool {
	value = normalizePipelineSealedText(value)
	subject = normalizePipelineSealedText(subject)
	if value == "" || subject == "" {
		return false
	}
	if strings.Contains(value, subject) {
		return true
	}
	for _, suffix := range []string{"消防门", "安全门", "通道门", "入口门"} {
		if !strings.HasSuffix(subject, suffix) {
			continue
		}
		prefix := strings.TrimSuffix(subject, suffix)
		if utf8.RuneCountInString(prefix) < 2 {
			continue
		}
		searchFrom := 0
		for searchFrom < len(value) {
			relative := strings.Index(value[searchFrom:], prefix)
			if relative < 0 {
				break
			}
			prefixEnd := searchFrom + relative + len(prefix)
			tail := value[prefixEnd:]
			suffixAt := strings.Index(tail, suffix)
			if suffixAt >= 0 {
				gap := tail[:suffixAt]
				if pipelineSealedOutcomeShortIdentifier(gap) {
					return true
				}
			}
			searchFrom = prefixEnd
		}
	}
	return false
}

func pipelineSealedOutcomeShortIdentifier(value string) bool {
	if value == "" {
		return true
	}
	if utf8.RuneCountInString(value) > 8 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("编号第号", r) {
			continue
		}
		return false
	}
	return true
}

func pipelineSealedOutcomeOperationalResolutionPredicate(value string) bool {
	return pipelineSealedContainsAny(value, []string{
		"按既定处置进入", "按处置方案进入", "按预案进入", "由此进入",
		"从该门进入", "从这扇门进入", "从此门进入", "处置人员进入",
	})
}

func pipelineSealedOutcomeAlternativeExcluded(value string) bool {
	hasExclusion := pipelineSealedContainsAny(value, []string{
		"不是", "排除", "已排除", "被排除", "依旧闭合", "仍然闭合", "仍闭合",
	})
	hasAlternative := pipelineSealedContainsAny(value, []string{
		"另一路", "另一候选", "另一个候选", "另一入口", "另一条路", "备选", "替代项",
		"上层", "下层", "东侧", "西侧", "北侧", "南侧",
	})
	return hasExclusion && hasAlternative
}

func pipelineSealedOutcomeVerificationSourceCount(value string) int {
	groups := [][]string{
		{"连续记录", "连续音轨", "音轨", "声轨", "服务端记录", "推流记录", "声音顺序"},
		{"门禁状态", "门禁记录", "门禁顺序", "门禁编号", "门禁"},
		{"公共监控", "监控画面", "外围监控", "公共摄像头", "摄像头"},
	}
	count := 0
	for _, group := range groups {
		if pipelineSealedContainsAny(value, group) {
			count++
		}
	}
	return count
}

func pipelineSealedOutcomeResolutionPredicate(value string) bool {
	return pipelineSealedContainsAny(value, []string{
		"唯一安全入口", "唯一入口", "共同确认", "确认为", "确认了", "确认是",
		"锁定为", "锁定了", "共同指向", "只剩", "收束到", "确定为",
	})
}

func pipelineSealedOutcomeResolvedCandidateContradicted(value string) bool {
	if pipelineSealedContainsAny(value, []string{
		"入口重新未定", "层位重新未定", "候选重新未定", "层位未定", "入口未定",
		"入口仍未确认", "层位仍未确认", "候选仍未确认", "入口尚未确认", "层位尚未确认",
		"无法确认入口", "不能确认入口", "未能确认入口", "入口继续待核", "入口恢复待核",
		"两条候选仍保留", "两路仍保留", "双候选仍保留", "两条仍保留", "两路仍然保留",
		"两条都留", "两路都留", "同时保留两条", "仍是两个候选", "仍有两个候选",
		"又变成两条候选", "重新保留双候选",
	}) {
		return true
	}
	// Bare "尚未确认" is not an entry contradiction: later rescue prose may
	// use it for a second suspect, victim condition, or another radio report.
	// Require the unresolved predicate to stay locally bound to route semantics.
	for _, predicate := range []string{"仍未确认", "尚未确认", "无法确认", "不能确认", "未能确认", "继续待核", "恢复待核", "重新未定"} {
		for from := 0; from < len(value); {
			relative := strings.Index(value[from:], predicate)
			if relative < 0 {
				break
			}
			at := from + relative
			left := pipelineSealedRuneTail(value[:at], 12)
			right := pipelineSealedRuneHead(value[at+len(predicate):], 12)
			if pipelineSealedContainsAny(left+right, []string{
				"入口", "候选", "路径", "路线", "层位", "门位", "消防门", "通道",
			}) {
				return true
			}
			from = at + len(predicate)
		}
	}
	return false
}

func pipelineSealedRuneTail(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
}

func pipelineSealedRuneHead(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func pipelineSealedOutcomeRiskStatusMatches(value, subject string) bool {
	subject = normalizePipelineSealedText(subject)
	if subject == "" || !strings.Contains(value, subject) {
		return false
	}
	if pipelineSealedOutcomeRiskStatusContradicted(value) {
		return false
	}
	hasRisk := pipelineSealedContainsAny(value, []string{
		"高风险", "风险最高", "危险最高", "最危险", "风险很高",
	})
	hasStatus := pipelineSealedContainsAny(value, []string{
		"降为", "降下来", "降级", "列为", "标为", "调低", "优先级降低",
		"高风险", "风险最高", "危险最高", "最危险",
	})
	return hasRisk && hasStatus
}

func pipelineSealedOutcomeRiskStatusContradicted(value string) bool {
	return pipelineSealedContainsAny(value, []string{
		"不是高风险", "并非高风险", "不算高风险", "风险不高", "风险最低",
		"不再是高风险", "降为低风险", "改为低风险", "解除风险",
	})
}

func pipelineSealedOutcomeCandidateStatusContradicted(value string) bool {
	return pipelineSealedContainsAny(value, []string{
		"唯一入口已锁定", "唯一入口已经锁定", "入口已锁定", "入口已经锁定",
		"唯一入口已确定", "唯一入口已经确定", "唯一入口已确认", "唯一入口已经确认",
		"只剩一条", "只留一条", "只保留一条", "排除另一条",
		"两条都排除", "两条均排除", "两路都排除", "两路均排除",
		"两个都排除", "两个均排除", "全部排除", "均被排除", "都被排除",
		"不再保留候选", "候选全部取消",
	})
}

func pipelineSealedOutcomeCandidateStatusMatches(value string) bool {
	if pipelineSealedOutcomeCandidateStatusContradicted(value) {
		return false
	}
	hasMultiple := pipelineSealedContainsAny(value, []string{
		"两条路径", "两条路线", "两条候选", "两处候选", "两个候选",
		"两路方案", "两路部署", "双路部署",
	}) || (pipelineSealedContainsAny(value, []string{"上层", "东侧"}) &&
		pipelineSealedContainsAny(value, []string{"下层", "坡道"}) &&
		pipelineSealedContainsAny(value, []string{"通道", "消防门", "路径", "路线"}))
	if !hasMultiple {
		return false
	}
	return pipelineSealedContainsAny(value, []string{
		"都留", "全留", "均保留", "同时保留", "继续保留", "仍然保留",
		"未排除", "没有排除", "尚未排除", "待核", "未定", "没定",
		"层位未定", "都描深", "均描深", "并排",
	})
}

// locatePipelineSealedPublicStigmaWindow proves a public reputational harm
// only when two independent features coexist in one bounded paragraph cluster:
// a propagation/comment topology and an accusation bound to the named subject.
// Private suspicion alone and unrelated viral traffic both remain insufficient.
func locatePipelineSealedPublicStigmaWindow(body, contract string) string {
	parsed, ok := parsePipelineSealedPublicStigmaContract(contract)
	if !ok {
		return ""
	}
	paragraphs := splitPipelineSealedBodyParagraphSpans(body)
	if len(paragraphs) == 0 {
		return ""
	}
	subject := normalizePipelineSealedText(parsed.Subject)
	for start := 0; start < len(paragraphs); start++ {
		// The subject and the concrete accusation must bind inside one paragraph;
		// otherwise a nearby mention of the subject could inherit somebody else's
		// viral accusation from the surrounding topology.
		for end := start; end < start+1; end++ {
			accusation := joinPipelineSealedParagraphWindow(paragraphs, start, end)
			normalized := normalizePipelineSealedText(accusation.Text)
			if !pipelineSealedPublicStigmaHasAccusation(normalized, subject, parsed.Label) ||
				pipelineSealedPublicStigmaCleared(accusation.Text) ||
				pipelineSealedSegmentNegatesExpected(accusation.Text, contract) {
				continue
			}
			topologyStart := start - 7
			if topologyStart < 0 {
				topologyStart = 0
			}
			topologyEnd := end + 2
			if topologyEnd >= len(paragraphs) {
				topologyEnd = len(paragraphs) - 1
			}
			topology := joinPipelineSealedParagraphWindow(paragraphs, topologyStart, topologyEnd)
			topologyText := normalizePipelineSealedText(topology.Text)
			if !pipelineSealedContainsAny(topologyText, []string{
				"评论区", "评论", "直播", "平台", "热搜", "页面", "帖子",
				"媒体", "话题", "观看人数", "播放数字", "围观",
			}) || !pipelineSealedContainsAny(topologyText, []string{
				"骤然加速", "加速", "翻倍", "暴涨", "激增", "接连", "刷屏",
				"扩散", "传开", "传播", "转载", "转发", "推上", "更多页面",
				"热度", "还在涨", "开始截取", "做成标题", "重新贴",
			}) {
				continue
			}
			return fmt.Sprintf(
				"body:public-stigma:topology[%d-%d]=%s;accusation[%d-%d]=%s",
				topology.Start+1,
				topology.End+1,
				truncatePipelineSealedText(topology.Text, 72),
				accusation.Start+1,
				accusation.End+1,
				truncatePipelineSealedText(accusation.Text, 72),
			)
		}
	}
	return ""
}

func parsePipelineSealedPublicStigmaContract(
	contract string,
) (pipelineSealedPublicStigmaContract, bool) {
	contract = strings.TrimSpace(contract)
	if !pipelineSealedContainsAny(contract, []string{
		"公开", "舆论", "评论", "转发", "传播", "网络", "平台", "直播",
		"媒体", "热搜", "围观",
	}) || !pipelineSealedContainsAny(contract, []string{
		"污名", "骂名", "嫌疑", "指控", "罪名", "名誉受损", "声誉受损",
		"被网暴", "泄密者", "骗子", "叛徒", "嫌疑人",
	}) {
		return pipelineSealedPublicStigmaContract{}, false
	}
	clauses := strings.FieldsFunc(contract, func(r rune) bool {
		switch r {
		case '，', ',', '；', ';', '。', '\n', '\r':
			return true
		default:
			return false
		}
	})
	actions := []string{
		"重新背负", "再次背负", "重新被推成", "再次被推成", "重新被指为",
		"背负", "被推成", "被指控为", "被指为", "被视为", "被当成",
		"遭到指控", "蒙上", "成为", "成了",
	}
	for _, clause := range clauses {
		for _, action := range actions {
			index := strings.Index(clause, action)
			if index < 0 {
				continue
			}
			subject := strings.Trim(strings.TrimSpace(clause[:index]), "‘’“”\"《》：: ")
			for _, prefix := range []string{"于是", "随后", "继而", "因此", "使", "让"} {
				subject = strings.TrimPrefix(subject, prefix)
			}
			if utf8.RuneCountInString(subject) < 2 || utf8.RuneCountInString(subject) > 8 {
				continue
			}
			label := strings.Trim(strings.TrimSpace(clause[index+len(action):]), "‘’“”\"《》：: ")
			for _, suffix := range []string{"污名", "骂名", "罪名", "标签", "嫌疑", "名声", "声誉"} {
				label = strings.TrimSuffix(label, suffix)
			}
			return pipelineSealedPublicStigmaContract{Subject: subject, Label: label}, true
		}
	}
	return pipelineSealedPublicStigmaContract{}, false
}

func pipelineSealedPublicStigmaHasAccusation(value, subject, label string) bool {
	markers := []string{
		"指认", "指控", "嫌疑", "污名", "骂名", "泄密", "造假", "罪名",
		"有罪", "叛徒", "骗子", "凶手", "同谋", "涉案", "偷走", "收钱",
		"出卖", "背叛", "持有原片", "带走原片", "早有问题", "早有嫌疑",
	}
	if label = normalizePipelineSealedText(label); label != "" {
		markers = append(markers, label)
	}
	// Bind the accusation to the named subject, rather than accepting an
	// accusation against somebody else followed by an unrelated subject mention.
	for searchAt := 0; searchAt < len(value); {
		relative := strings.Index(value[searchAt:], subject)
		if relative < 0 {
			break
		}
		subjectAt := searchAt + relative
		tail := value[subjectAt+len(subject):]
		for _, marker := range markers {
			marker = normalizePipelineSealedText(marker)
			markerAt := strings.Index(tail, marker)
			if markerAt >= 0 && utf8.RuneCountInString(tail[:markerAt]) <= 24 {
				return true
			}
		}
		searchAt = subjectAt + len(subject)
	}
	return false
}

func pipelineSealedPublicStigmaCleared(value string) bool {
	return pipelineSealedContainsAny(normalizePipelineSealedText(value), []string{
		"澄清", "辟谣", "洗清", "平反", "证明清白", "撤回指控", "撤销指控",
		"指控失去事实基础", "没有嫌疑", "不再怀疑", "无罪", "误会解除",
	})
}

// locatePipelineSealedOrderedNameConsequence handles the explicit contract
// shape "A names B and changes strategy; after the naming, show a separate
// visible consequence". It is intentionally narrow. Ordinary realization
// contracts continue through the existing semantic/fact matchers.
func locatePipelineSealedOrderedNameConsequence(body, contract string) string {
	parsed, ok := parsePipelineSealedOrderedNameContract(contract)
	if !ok {
		return ""
	}
	paragraphs := splitPipelineSealedBodyParagraphSpans(body)
	if len(paragraphs) == 0 {
		return ""
	}

	name, ok := locatePipelineSealedNameWindow(paragraphs, parsed.Subject, parsed.Target)
	if !ok || !pipelineSealedNameTimingMatches(name, paragraphs, parsed.Timing) {
		return ""
	}
	strategy, ok := locatePipelineSealedStrategyWindow(paragraphs, name.End+1)
	if !ok {
		return ""
	}
	consequence, ok := locatePipelineSealedConsequenceWindow(paragraphs, strategy.End+1)
	if !ok {
		return ""
	}
	return fmt.Sprintf(
		"body:ordered-name-consequence:name[%d-%d]=%s;strategy[%d-%d]=%s;consequence[%d-%d]=%s",
		name.Start+1,
		name.End+1,
		truncatePipelineSealedText(name.Text, 54),
		strategy.Start+1,
		strategy.End+1,
		truncatePipelineSealedText(strategy.Text, 54),
		consequence.Start+1,
		consequence.End+1,
		truncatePipelineSealedText(consequence.Text, 54),
	)
}

func parsePipelineSealedOrderedNameContract(
	contract string,
) (pipelineSealedOrderedNameContract, bool) {
	var parsed pipelineSealedOrderedNameContract
	parts := strings.FieldsFunc(contract, func(r rune) bool {
		return r == '；' || r == ';' || r == '\n' || r == '\r'
	})
	if len(parts) < 2 {
		return parsed, false
	}
	later := strings.Join(parts[1:], "；")
	if !pipelineSealedContainsAny(later, []string{"之后", "以后", "随后", "继而"}) ||
		!pipelineSealedContainsAny(later, []string{"后果", "结果", "变化", "反应"}) ||
		!pipelineSealedContainsAny(later, []string{"独立", "另", "新的", "新"}) {
		return parsed, false
	}

	first := strings.TrimSpace(parts[0])
	for _, comma := range []string{"，", ","} {
		if index := strings.Index(first, comma); index >= 0 {
			prefix := strings.TrimSpace(first[:index])
			if strings.Contains(prefix, "正文") || strings.Contains(prefix, "中点") {
				parsed.Timing = prefix
				first = strings.TrimSpace(first[index+len(comma):])
			}
		}
	}
	if !pipelineSealedContainsAny(first, []string{"改变策略", "调整策略", "改变控制", "调整控制", "改变安排", "调整安排"}) {
		return parsed, false
	}

	verb := ""
	verbIndex := -1
	for _, candidate := range []string{
		"清楚叫出", "明确叫出", "直接叫出", "当面叫出",
		"叫出", "喊出", "点名", "说出",
	} {
		if index := strings.Index(first, candidate); index >= 0 {
			verb, verbIndex = candidate, index
			break
		}
	}
	if verbIndex < 0 {
		return parsed, false
	}
	parsed.Subject = strings.TrimSpace(first[:verbIndex])
	tail := strings.TrimSpace(first[verbIndex+len(verb):])
	for _, boundary := range []string{"姓名", "名字", "的姓名", "的名字", "并", "且"} {
		if index := strings.Index(tail, boundary); index >= 0 {
			tail = strings.TrimSpace(tail[:index])
			break
		}
	}
	parsed.Target = strings.Trim(strings.TrimSpace(tail), "‘’“”\"《》")
	if utf8.RuneCountInString(parsed.Subject) < 2 ||
		utf8.RuneCountInString(parsed.Subject) > 8 ||
		utf8.RuneCountInString(parsed.Target) < 2 ||
		utf8.RuneCountInString(parsed.Target) > 8 {
		return pipelineSealedOrderedNameContract{}, false
	}
	return parsed, true
}

func splitPipelineSealedBodyParagraphSpans(body string) []pipelineSealedBodyParagraphSpan {
	lines := strings.SplitAfter(body, "\n")
	paragraphs := make([]pipelineSealedBodyParagraphSpan, 0, len(lines))
	runeOffset := 0
	for _, line := range lines {
		lineRunes := utf8.RuneCountInString(line)
		text := strings.TrimSpace(line)
		if text != "" {
			leading := utf8.RuneCountInString(line) -
				utf8.RuneCountInString(strings.TrimLeftFunc(line, unicode.IsSpace))
			paragraphs = append(paragraphs, pipelineSealedBodyParagraphSpan{
				Text:      text,
				StartRune: runeOffset + leading,
				EndRune:   runeOffset + lineRunes,
			})
		}
		runeOffset += lineRunes
	}
	return paragraphs
}

func locatePipelineSealedNameWindow(
	paragraphs []pipelineSealedBodyParagraphSpan,
	subject, target string,
) (pipelineSealedBodyParagraphWindow, bool) {
	for start := 0; start < len(paragraphs); start++ {
		for end := start; end < len(paragraphs) && end < start+3; end++ {
			window := joinPipelineSealedParagraphWindow(paragraphs, start, end)
			normalized := normalizePipelineSealedText(window.Text)
			if !strings.Contains(normalized, normalizePipelineSealedText(subject)) ||
				!strings.Contains(normalized, normalizePipelineSealedText(target)) ||
				!pipelineSealedContainsAny(normalized, []string{
					"叫出", "喊出", "点名", "说出", "开口", "姓名", "名字",
				}) || pipelineSealedSegmentNegatesExpected(window.Text, subject+target) {
				continue
			}
			return window, true
		}
	}
	return pipelineSealedBodyParagraphWindow{}, false
}

func locatePipelineSealedStrategyWindow(
	paragraphs []pipelineSealedBodyParagraphSpan,
	startAt int,
) (pipelineSealedBodyParagraphWindow, bool) {
	changeMarkers := []string{
		"改变", "调整", "变了", "变化", "改了", "重排", "重排", "撤回",
		"收短", "收紧", "封住", "封堵", "暂停", "拆开", "改向", "换成",
	}
	domainMarkers := []string{
		"策略", "控制", "方式", "安排", "处置", "路线", "节奏", "约束",
		"通路", "空隙", "方案", "方向", "顺序", "转移",
	}
	for start := startAt; start < len(paragraphs); start++ {
		for end := start; end < len(paragraphs) && end < start+2; end++ {
			window := joinPipelineSealedParagraphWindow(paragraphs, start, end)
			normalized := normalizePipelineSealedText(window.Text)
			if pipelineSealedContainsAny(normalized, changeMarkers) &&
				pipelineSealedContainsAny(normalized, domainMarkers) &&
				!pipelineSealedTextHasNegation(window.Text) {
				return window, true
			}
		}
	}
	return pipelineSealedBodyParagraphWindow{}, false
}

func locatePipelineSealedConsequenceWindow(
	paragraphs []pipelineSealedBodyParagraphSpan,
	startAt int,
) (pipelineSealedBodyParagraphWindow, bool) {
	actionMarkers := []string{
		"撞", "跌", "倒", "滑", "碎", "响", "敲", "推", "拉", "挣",
		"掉", "停住", "留下", "出现", "进入", "收进", "亮起", "熄灭",
		"流出", "裂开", "滚过", "移开", "制止",
	}
	visibleMarkers := []string{
		"看见", "可见", "画面", "镜头", "声音", "声响", "响", "声轨",
		"痕迹", "水痕", "餐碗", "碗", "灯", "门", "空隙", "滚轮", "动作",
	}
	for start := startAt; start < len(paragraphs); start++ {
		for end := start; end < len(paragraphs) && end < start+2; end++ {
			window := joinPipelineSealedParagraphWindow(paragraphs, start, end)
			normalized := normalizePipelineSealedText(window.Text)
			if pipelineSealedContainsAny(normalized, actionMarkers) &&
				pipelineSealedContainsAny(normalized, visibleMarkers) &&
				!pipelineSealedTextHasNegation(window.Text) {
				return window, true
			}
		}
	}
	return pipelineSealedBodyParagraphWindow{}, false
}

func joinPipelineSealedParagraphWindow(
	paragraphs []pipelineSealedBodyParagraphSpan,
	start, end int,
) pipelineSealedBodyParagraphWindow {
	texts := make([]string, 0, end-start+1)
	for index := start; index <= end; index++ {
		texts = append(texts, paragraphs[index].Text)
	}
	return pipelineSealedBodyParagraphWindow{
		Start: start,
		End:   end,
		Text:  strings.Join(texts, "\n"),
	}
}

func pipelineSealedNameTimingMatches(
	name pipelineSealedBodyParagraphWindow,
	paragraphs []pipelineSealedBodyParagraphSpan,
	timing string,
) bool {
	if strings.TrimSpace(timing) == "" || len(paragraphs) == 0 {
		return true
	}
	totalRunes := paragraphs[len(paragraphs)-1].EndRune
	if totalRunes <= 0 {
		return false
	}
	ratio := float64(paragraphs[name.Start].StartRune) / float64(totalRunes)
	if strings.Contains(timing, "不晚于中点") && ratio > 0.5 {
		return false
	}
	if target, ok := pipelineSealedApproximateBodyRatio(timing); ok {
		const tolerance = 0.05
		if ratio < target-tolerance || ratio > target+tolerance {
			return false
		}
	}
	return true
}

func pipelineSealedApproximateBodyRatio(value string) (float64, bool) {
	marker := strings.Index(value, "正文")
	percent := strings.Index(value, "%")
	if percent < 0 {
		percent = strings.Index(value, "％")
	}
	if marker < 0 || percent <= marker+len("正文") {
		return 0, false
	}
	digits := strings.TrimSpace(value[marker+len("正文") : percent])
	if digits == "" {
		return 0, false
	}
	number := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
		number = number*10 + int(r-'0')
	}
	if number <= 0 || number >= 100 {
		return 0, false
	}
	return float64(number) / 100, true
}

func pipelineSealedContainsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func pipelineSealedContractEvidenceOverlap(contract, evidence string) bool {
	left := pipelineSealedBigrams(contract)
	right := pipelineSealedBigrams(evidence)
	if len(left) == 0 || len(right) == 0 {
		return pipelineSealedActualSupports(contract, evidence) ||
			pipelineSealedActualSupports(evidence, contract)
	}
	overlap := 0
	for token := range left {
		if _, ok := right[token]; ok {
			overlap++
		}
	}
	minimum := len(left)
	if len(right) < minimum {
		minimum = len(right)
	}
	// The locator proves a concrete body span, not wording identity. Requiring
	// a quarter of all character bigrams made natural paraphrases fail and
	// rewarded copying the plan. A bounded one-sixth overlap still needs
	// multiple shared semantic anchors while allowing ordinary prose.
	required := (minimum + 5) / 6
	if required < 2 {
		required = 2
	}
	if required > 4 {
		required = 4
	}
	return overlap >= required
}

func pipelineSealedSemanticTextSupports(actual, expected string) bool {
	if pipelineSealedActualSupports(actual, expected) {
		return true
	}
	return pipelineSealedContractEvidenceOverlap(expected, actual)
}

func pipelineSealedBodySemanticallyContains(body, expected string) bool {
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedSafetyTextSupports(segment, expected) {
			return true
		}
	}
	return false
}

func pipelineSealedBodyAffirmativelyContains(body, expected string) bool {
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedSafetyTextSupports(segment, expected) &&
			!pipelineSealedSegmentNegatesExpected(segment, expected) {
			return true
		}
	}
	return false
}

func pipelineSealedSegmentNegatesExpected(segment, expected string) bool {
	if pipelineSealedTextHasNegation(expected) {
		return false
	}
	return pipelineSealedTextHasNegation(segment)
}

func pipelineSealedTextHasNegation(value string) bool {
	for _, marker := range []string{
		"没有", "没能", "没再", "没看", "未能", "未曾", "仍未",
		"并未", "并非", "不是", "不会", "不能", "不得", "不可", "不要",
		"不应", "不该", "不属于", "不曾", "不再", "拒绝", "放弃", "取消",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	// A bare 未 is productive Chinese negation (未确认、未洗白, etc.). Avoid
	// treating the common temporal noun 未来 as a polarity marker.
	return strings.Contains(strings.ReplaceAll(value, "未来", ""), "未")
}

func pipelineSealedBodyLeaksUnknown(body, expected string) bool {
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if !pipelineSealedSafetyTextSupports(segment, expected) {
			continue
		}
		if strings.Contains(segment, "不知道") ||
			strings.Contains(segment, "不清楚") ||
			strings.Contains(segment, "没看见") ||
			strings.Contains(segment, "未看见") ||
			strings.Contains(segment, "只知道") ||
			strings.Contains(segment, "不得而知") {
			continue
		}
		return true
	}
	return false
}

func stripPipelineSealedNegation(value string) string {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{
		"不得", "禁止", "不可", "不能", "不允许", "不要", "避免",
		"不提前给出", "不提前揭示", "不提前", "不解释", "不揭示",
		"不说明", "不公开", "不点破", "不交代", "不写成", "不写",
		"尚未", "仍未", "未曾", "未",
	} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimSpace(value)
}

// pipelineSealedForbiddenContractProbes turns a compound MustNotOccur entry
// into independently matchable negative clauses. Contract authors commonly
// join separate prohibitions with both commas and the Chinese enumeration
// comma; matching the whole entry lets one shared tail phrase impersonate all
// of them. A positive preface before the first explicit negative marker is
// explanatory context, not itself a forbidden fact.
func pipelineSealedForbiddenContractProbes(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	sentences := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '；', ';':
			return true
		default:
			return false
		}
	})
	hasExplicitNegative := false
	for _, sentence := range sentences {
		for _, clause := range strings.FieldsFunc(sentence, func(r rune) bool {
			return r == '，' || r == ',' || r == '、'
		}) {
			if _, negative := pipelineSealedNegativeContractProbe(clause); negative {
				hasExplicitNegative = true
				break
			}
		}
	}
	if !hasExplicitNegative {
		return compactProjectAllStrings([]string{stripPipelineSealedNegation(value)})
	}

	var probes []string
	for _, sentence := range sentences {
		clauses := strings.FieldsFunc(sentence, func(r rune) bool {
			return r == '，' || r == ','
		})
		negativeListActive := false
		for _, clause := range clauses {
			wholeProbe, wholeNegative := pipelineSealedNegativeContractProbe(clause)
			if !wholeNegative && pipelineSealedForbiddenPositiveBoundary(clause) {
				// A following positive/required result is not another member of
				// the preceding forbidden list (for example, “不得继续追捕，
				// 只能转入医疗”).
				negativeListActive = false
				continue
			}
			if wholeNegative {
				negativeListActive = true
			}
			wholeProbe = trimPipelineSealedClauseConnector(wholeProbe)
			if wholeNegative && (pipelineSealedForbiddenHasInstrumentalSharedTail(wholeProbe) ||
				pipelineSealedForbiddenHasSharedOrderPredicate(wholeProbe)) {
				probes = append(probes, wholeProbe)
				continue
			}
			for _, item := range strings.FieldsFunc(clause, func(r rune) bool { return r == '、' }) {
				probe, negative := pipelineSealedNegativeContractProbe(item)
				if negative {
					negativeListActive = true
				} else if !negativeListActive {
					// Ignore explanatory text before the first prohibition.
					continue
				}
				probe = trimPipelineSealedClauseConnector(probe)
				for _, alternative := range pipelineSealedForbiddenIndependentAlternatives(probe) {
					if alternative != "" {
						probes = append(probes, alternative)
					}
				}
			}
		}
	}
	return compactProjectAllStrings(probes)
}

func pipelineSealedForbiddenPositiveBoundary(value string) bool {
	value = trimPipelineSealedClauseConnector(value)
	for _, marker := range []string{
		"只能", "只可", "只允许", "必须", "须", "应当", "应该", "需要",
		"可以", "可由", "允许", "关系答案留到", "答案留到", "关系结果留到", "结果留到",
	} {
		if strings.HasPrefix(value, marker) || strings.HasPrefix(value, "本章"+marker) {
			return true
		}
	}
	return false
}

// pipelineSealedForbiddenHasSharedOrderPredicate preserves “颠倒 A、B、C 的
// 顺序” as one prohibition. A/B/C are required events; only changing their
// order is forbidden.
func pipelineSealedForbiddenHasSharedOrderPredicate(probe string) bool {
	probe = strings.TrimSpace(probe)
	if !strings.Contains(probe, "、") {
		return false
	}
	action := false
	for _, prefix := range []string{"颠倒", "打乱", "倒置", "错置", "逆转", "改变"} {
		if strings.HasPrefix(probe, prefix) {
			action = true
			break
		}
	}
	if !action {
		return false
	}
	return strings.Contains(probe, "顺序") || strings.Contains(probe, "次序") ||
		strings.Contains(probe, "时序") || strings.Contains(probe, "先后")
}

// Split “原谅或复合” and similar complete action alternatives, but keep
// shared-object grammar such as “把页面时间或观看端画面当作实时证明” intact.
func pipelineSealedForbiddenIndependentAlternatives(probe string) []string {
	probe = strings.TrimSpace(probe)
	if probe == "" {
		return nil
	}
	separator := ""
	for _, candidate := range []string{"或者", "或"} {
		if strings.Contains(probe, candidate) {
			separator = candidate
			break
		}
	}
	if separator == "" {
		return []string{probe}
	}
	parts := strings.Split(probe, separator)
	if len(parts) < 2 || !pipelineSealedSelfContainedForbiddenAction(parts[0]) {
		return []string{probe}
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimPipelineSealedClauseConnector(part)
		if part == "" {
			return []string{probe}
		}
		out = append(out, part)
	}
	return out
}

func pipelineSealedSelfContainedForbiddenAction(value string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{
		"把", "将", "用", "以", "拿", "借", "凭", "靠", "因", "由于",
		"通过", "根据", "为了", "为",
	} {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	for _, action := range []string{
		"原谅", "复合", "和好", "谅解", "承认", "营救", "下单", "出餐",
		"履约", "联系", "追踪", "定位", "定罪", "公开", "解释", "认定",
		"洗白", "离开", "进入", "揭示", "给出", "说明", "点破", "交代",
		"删除", "覆盖", "替代", "取代", "抹去", "免除", "撤诉", "报警",
		"控制", "纠正", "亲吻", "告白", "搏斗", "制服", "追捕",
	} {
		if strings.Contains(value, action) {
			return true
		}
	}
	return false
}

// pipelineSealedForbiddenHasInstrumentalSharedTail recognizes the narrow
// Chinese construction “用/以/拿/借/凭/靠/把 A、B 或 C 抵销 X”. The
// predicate after the enumeration governs every item, so the whole clause is
// one safety probe. Ordinary action lists remain independently matchable.
func pipelineSealedForbiddenHasInstrumentalSharedTail(probe string) bool {
	probe = strings.TrimSpace(probe)
	instrumental := false
	for _, prefix := range []string{"借助", "用", "以", "拿", "借", "凭", "靠", "把"} {
		if strings.HasPrefix(probe, prefix) {
			instrumental = true
			break
		}
	}
	enumeration := strings.Index(probe, "、")
	if !instrumental || enumeration < 0 {
		return false
	}
	for _, predicate := range []string{
		"抵销", "抵消", "洗白", "替代", "取代", "抹去", "冲抵", "冲销", "免除",
		"写进", "写入", "带入", "放进",
	} {
		if index := strings.Index(probe, predicate); index > enumeration && index+len(predicate) < len(probe) {
			return true
		}
	}
	return false
}

func trimPipelineSealedClauseConnector(value string) string {
	value = strings.TrimSpace(value)
	for {
		trimmed := value
		for _, prefix := range []string{"并且", "而且", "以及", "同时", "也", "且", "并", "又", "或", "而"} {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				break
			}
		}
		if trimmed == value {
			return value
		}
		value = trimmed
	}
}

func splitPipelineSealedContractClauses(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '；', '，', ',', ';':
			return true
		default:
			return false
		}
	})
	return compactProjectAllStrings(parts)
}

func pipelineSealedNegativeContractProbe(value string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, marker := range []string{
		"不提前给出", "不提前揭示", "不提前", "不解释", "不揭示",
		"不说明", "不公开", "不点破", "不交代", "不写成", "不写",
		"不允许", "不得", "禁止", "不可", "不能", "不要", "避免",
		"尚未", "仍未", "未曾",
	} {
		if index := strings.Index(value, marker); index >= 0 {
			probe := strings.TrimSpace(value[:index] + value[index+len(marker):])
			for _, scope := range []string{"本章", "当前章", "此章"} {
				probe = strings.TrimSpace(strings.TrimPrefix(probe, scope))
			}
			return probe, true
		}
	}
	return value, false
}

func pipelineSealedBodyExplicitlyNegatesContract(body, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" || pipelineSealedTextHasNegation(expected) {
		return false
	}
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedTextHasNegation(segment) &&
			pipelineSealedSafetyTextSupports(segment, expected) {
			return true
		}
	}
	return false
}

func pipelineSealedBigrams(value string) map[string]struct{} {
	runes := []rune(normalizePipelineSealedText(value))
	out := make(map[string]struct{})
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

// pipelineSealedSafetyTextSupports is deliberately stricter than the
// realization evidence matcher. Positive realization may use a handful of
// shared anchors because durable ledgers independently identify the mutation;
// a POV/forbidden/reveal rejection has no such identity proof and must not turn
// two common Chinese bigrams into a fabricated contradiction.
func pipelineSealedSafetyTextSupports(actual, expected string) bool {
	rawActual, rawExpected := actual, expected
	actual = normalizePipelineSealedText(actual)
	expected = normalizePipelineSealedText(expected)
	if actual == "" || expected == "" {
		return false
	}
	if !pipelineSealedSafetyPredicateAnchorPresent(rawActual, rawExpected) {
		return false
	}
	if actual == expected || strings.Contains(actual, expected) {
		return true
	}
	// Fuzzy overlap may recognize a concise paraphrase, but a short fragment
	// of the sealed fact cannot stand in for the whole forbidden assertion.
	// In particular, a time phrase such as "十八个月前" shares four consecutive
	// bigrams with a much longer prohibition without expressing its action.
	// Exact/full containment above remains authoritative; fuzzy candidates must
	// cover at least half of the expected fact before their anchors are scored.
	if utf8.RuneCountInString(actual)*2 < utf8.RuneCountInString(expected) {
		return false
	}
	left := pipelineSealedBigrams(actual)
	right := pipelineSealedBigrams(expected)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	overlap := 0
	for token := range left {
		if _, ok := right[token]; ok {
			overlap++
		}
	}
	minimum := len(left)
	if len(right) < minimum {
		minimum = len(right)
	}
	// Require at least four distinct anchors and two thirds of the shorter
	// phrase. This still catches concise paraphrases of a forbidden fact while
	// rejecting incidental name/object overlap with a long sealed contract.
	required := (2*minimum + 2) / 3
	if required < 4 {
		required = 4
	}
	return overlap >= required
}

// Shared-scope prohibitions are about the governing predicate, not the listed
// facts. Fuzzy overlap may recognize paraphrase only after that predicate is
// present; otherwise a correct recital of A/B/C would look like “颠倒 A/B/C”.
func pipelineSealedSafetyPredicateAnchorPresent(actual, expected string) bool {
	actual = normalizePipelineSealedText(actual)
	if pipelineSealedForbiddenHasSharedOrderPredicate(expected) {
		return pipelineSealedContainsAny(actual, []string{
			"颠倒", "打乱", "倒置", "错置", "逆序", "逆转", "改变顺序", "改变次序",
		})
	}
	if pipelineSealedForbiddenHasInstrumentalSharedTail(expected) {
		return pipelineSealedContainsAny(actual, []string{
			"抵销", "抵消", "洗白", "替代", "取代", "抹去", "冲抵", "冲销", "免除",
			"写进", "写入", "带入", "放进",
		})
	}
	return true
}

func pipelineSealedBodyContains(body, evidence string) bool {
	return pipelineSealedActualSupports(body, evidence)
}

func splitPipelineSealedBodySegments(body string) []string {
	return strings.FieldsFunc(body, func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '；', '，', ',', ';', '！', '？', '!', '?':
			return true
		default:
			return false
		}
	})
}

func pipelineSealedActualSupports(actual, expected string) bool {
	actual = normalizePipelineSealedText(actual)
	expected = normalizePipelineSealedText(expected)
	if actual == "" || expected == "" {
		return false
	}
	if actual == expected {
		return true
	}
	if utf8.RuneCountInString(expected) < 2 {
		return false
	}
	return strings.Contains(actual, expected)
}

func normalizePipelineSealedText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		switch r {
		case '→':
			b.WriteString("to")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pipelineSealedIdentityEqual(left, right string) bool {
	return normalizePipelineSealedText(left) == normalizePipelineSealedText(right)
}

func normalizePipelineSealedKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "-", "_")
	return kind
}

func splitPipelineSealedStateEntity(entity string) (string, string) {
	entity = strings.TrimSpace(entity)
	if index := strings.LastIndex(entity, "."); index > 0 && index < len(entity)-1 {
		return strings.TrimSpace(entity[:index]), strings.TrimSpace(entity[index+1:])
	}
	if index := strings.LastIndex(entity, ":"); index > 0 && index < len(entity)-1 {
		return strings.TrimSpace(entity[:index]), strings.TrimSpace(entity[index+1:])
	}
	return entity, "state"
}

func splitPipelineSealedTransition(value string) (string, string) {
	for _, separator := range []string{" -> ", "->", " → ", "→"} {
		if parts := strings.SplitN(value, separator, 2); len(parts) == 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	}
	return "", strings.TrimSpace(value)
}

func splitPipelineSealedPair(value string) (string, string) {
	for _, separator := range []string{"|", "—", "-", "/", "、"} {
		if parts := strings.SplitN(value, separator, 2); len(parts) == 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	}
	return "", ""
}

func pipelineSealedCategoryForStateField(field string) string {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "location":
		return "location"
	case "knowledge", "knowledge_boundary":
		return "knowledge"
	case "relationship", "trust", "debt":
		return "relationship"
	case "resource":
		return "resource"
	default:
		return "character_state"
	}
}

func pipelineSealedCanonicalField(category, field string) string {
	switch category {
	case "timeline":
		return "outcome"
	case "relationship":
		return "relationship"
	case "resource":
		return "resource"
	case "knowledge":
		return "knowledge_boundary"
	case "location":
		return "location"
	case "foreshadow":
		return "evidence_return"
	case "obligation":
		return "state"
	default:
		return strings.TrimSpace(field)
	}
}

func pipelineSealedActualFactValues(
	facts []pipelineSealedActualFact,
	indexes []int,
) []string {
	values := make([]string, 0, len(indexes))
	for _, index := range indexes {
		value := fmt.Sprintf("%s @ %s", facts[index].After, facts[index].Locator)
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func pipelineSealedActualSourceContains(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), target) {
			return true
		}
	}
	return false
}

func pipelineSealedHasConsumedObligation(values []domain.StateMutationV2) bool {
	for _, mutation := range values {
		if mutation.Operation == "consume" || mutation.Operation == "resolve" {
			return true
		}
	}
	return false
}

func samePipelineSealedActualJSON(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func normalizePipelineSealedReasons(values []string) []string {
	values = compactProjectAllStrings(values)
	sort.Strings(values)
	return values
}

func normalizePipelineSealedEvidence(
	values []pipelineSealedActualEvidence,
) []pipelineSealedActualEvidence {
	sort.Slice(values, func(i, j int) bool {
		left := values[i].Category + "\x00" + values[i].StableID + "\x00" + values[i].Locator
		right := values[j].Category + "\x00" + values[j].StableID + "\x00" + values[j].Locator
		return left < right
	})
	return values
}

func truncatePipelineSealedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
