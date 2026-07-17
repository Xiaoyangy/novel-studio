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
		if digestErr != nil || bundleDigestErr != nil || liveDigest != bundleDigest {
			preliminary = append(preliminary, "live promoted chapter plan differs from the sealed bundle")
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
			switch category.Name {
			case "timeline":
				visible = true
			case "character_state", "location":
				if known, ok := visibleCharacter[strings.TrimSpace(mutation.Subject)]; ok {
					visible = known
				}
			case "knowledge":
				if known, ok := visibleCharacter[strings.TrimSpace(mutation.Subject)]; ok {
					visible = known
				}
				visible = visible ||
					contractContains(bundle.HardRenderContract.KnowledgeChanges, mutation)
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

func pipelineSealedPOVBoundaryViolations(
	body string,
	bundle *domain.ProjectedChapterBundle,
) []string {
	if bundle == nil || strings.TrimSpace(body) == "" {
		return nil
	}
	var reasons []string
	for _, forbidden := range bundle.HardRenderContract.MustNotOccur {
		probe := stripPipelineSealedNegation(forbidden)
		if utf8.RuneCountInString(normalizePipelineSealedText(probe)) >= 4 &&
			pipelineSealedBodyAffirmativelyContains(body, probe) {
			reasons = append(reasons, fmt.Sprintf(
				"hard forbidden move appears in body: %q",
				truncatePipelineSealedText(forbidden, 120),
			))
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
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedSemanticTextSupports(segment, contract) &&
			!pipelineSealedSegmentNegatesExpected(segment, contract) {
			return "body:" + truncatePipelineSealedText(strings.TrimSpace(segment), 120)
		}
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
		if pipelineSealedSemanticTextSupports(segment, expected) {
			return true
		}
	}
	return false
}

func pipelineSealedBodyAffirmativelyContains(body, expected string) bool {
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if pipelineSealedSemanticTextSupports(segment, expected) &&
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
		"并未", "并非", "不是", "不属于", "不曾", "不再", "拒绝", "放弃", "取消",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func pipelineSealedBodyLeaksUnknown(body, expected string) bool {
	for _, segment := range splitPipelineSealedBodySegments(body) {
		if !pipelineSealedSemanticTextSupports(segment, expected) {
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
			pipelineSealedSemanticTextSupports(segment, expected) {
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
