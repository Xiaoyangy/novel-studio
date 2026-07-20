package tools

import (
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var planPreservingRenderRules = map[string]bool{
	"aigc_ratio":                    true,
	"abstract_system_reassurance":   true,
	"dialogue_conveyor_overuse":     true,
	"dialogue_semicolon_formality":  true,
	"dramatic_negation_overuse":     true,
	"isolated_sentence_overuse":     true,
	"micro_action_overuse":          true,
	"not_but_overuse":               true,
	"object_response_overuse":       true,
	"object_response_rhythm_flat":   true,
	"paragraph_start_repetition":    true,
	"pov_interiority_thin":          true,
	"semicolon_overuse":             true,
	"state_clause_pile":             true,
	"stiff_trade_dialogue":          true,
	"system_message_inline":         true,
	"system_message_overpacked":     true,
	"templated_dialogue_chain":      true,
	"too_many_isolated_short_lines": true,
}

// RenderOnlyReviewAllowsPlanReuse identifies a formal exact-body review whose
// failures are confined to prose expression, paragraphing, dialogue
// presentation or AI-voice symptoms. It deliberately requires the chapter
// contract and every causal/character dimension to pass, so callers may reuse
// the sealed simulation and POV plan without turning factual failures into a
// render-only escape hatch.
func RenderOnlyReviewAllowsPlanReuse(st *store.Store, chapter int) bool {
	if st == nil || chapter <= 0 {
		return false
	}
	body, err := st.Drafts.LoadChapterText(chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return false
	}
	bodyHash := reviewreport.BodySHA256(body)
	review, err := st.World.LoadReview(chapter)
	if err != nil || review == nil || review.BodySHA256 != bodyHash || review.ContractStatus != "met" || len(review.ContractMisses) > 0 {
		return false
	}
	for _, dimension := range review.Dimensions {
		switch dimension.Dimension {
		case "consistency", "character", "pacing", "continuity", "foreshadow", "hook":
			if dimension.Verdict != "pass" {
				return false
			}
		}
	}
	for _, issue := range review.Issues {
		if issue.Type != "aesthetic" && issue.Type != "ai_voice_detection" {
			return false
		}
	}

	gate, _, err := reviewreport.LoadMechanicalGate(st.Dir(), chapter)
	if err != nil || gate == nil || gate.BodySHA256 != bodyHash || len(gate.RuleViolations) == 0 {
		return false
	}
	blocking := review.Verdict == "rewrite" || review.Verdict == "polish"
	if !blocking {
		if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil &&
			progress.Flow == domain.FlowPolishing && slices.Contains(progress.PendingRewrites, chapter) {
			blocking = true
		}
	}
	for _, violation := range gate.RuleViolations {
		if !planPreservingRenderRules[violation.Rule] {
			return false
		}
		if violation.Severity == rules.SeverityError {
			blocking = true
		}
	}
	return blocking
}

type expressionOnlyReviewReusePhase uint8

const (
	expressionOnlyReviewReuseNone expressionOnlyReviewReusePhase = iota
	expressionOnlyReviewReuseSeed
	expressionOnlyReviewReuseReplacement
	expressionOnlyReviewReuseRejectedReplacement
)

// expressionOnlyReviewPendingJudgePlanReuse is a review-only capability. It
// proves that a replacement hash still belongs to the exact sealed plan whose
// formal review rejected only expression, but it deliberately says nothing
// about whether another prose write is authorized. The outer pipeline uses it
// only for deterministic hard-fact inspection before judging that exact hash.
// Write tools continue to require expressionOnlyReviewPlanReusePhase below.
func expressionOnlyReviewPendingJudgePlanReuse(
	st *store.Store,
	chapter int,
	plan domain.ChapterPlan,
) bool {
	if !RenderOnlyReviewAllowsPlanReuse(st, chapter) ||
		InspectRenderOnlyReplanEscalation(st, chapter).Required {
		return false
	}
	if _, exact := exactSealedV2FrozenBundleForPlan(st, chapter, plan); !exact {
		return false
	}
	final, finalErr := st.Drafts.LoadChapterText(chapter)
	draft, draftErr := st.Drafts.LoadDraft(chapter)
	if finalErr != nil || draftErr != nil || strings.TrimSpace(final) == "" ||
		strings.TrimSpace(draft) == "" || draft == final ||
		!BlockingReviewRejectsBody(st, chapter, final) {
		return false
	}
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil || bodyCheckpoint == nil {
		return false
	}
	planCheckpoint, err := CurrentChapterPlanCheckpoint(st, chapter)
	return err == nil && planCheckpoint != nil && bodyCheckpoint.Seq > planCheckpoint.Seq
}

// expressionOnlyReviewPlanReusePhase turns the semantic classification above
// into a bounded execution capability. The rejected exact-body seed may be
// replaced once. After that write, the old review no longer authorizes prose;
// the same frozen plan becomes usable again only when the replacement has an
// exact draft checkpoint and a strict current-hash DeepSeek pass.
func expressionOnlyReviewPlanReusePhase(
	st *store.Store,
	chapter int,
	plan domain.ChapterPlan,
) expressionOnlyReviewReusePhase {
	if !RenderOnlyReviewAllowsPlanReuse(st, chapter) ||
		InspectRenderOnlyReplanEscalation(st, chapter).Required {
		return expressionOnlyReviewReuseNone
	}
	if _, exact := exactSealedV2FrozenBundleForPlan(st, chapter, plan); !exact {
		return expressionOnlyReviewReuseNone
	}
	if ReviewRequiresFreshDraft(st, chapter) {
		return expressionOnlyReviewReuseSeed
	}

	final, finalErr := st.Drafts.LoadChapterText(chapter)
	draft, draftErr := st.Drafts.LoadDraft(chapter)
	if finalErr != nil || draftErr != nil || strings.TrimSpace(final) == "" ||
		strings.TrimSpace(draft) == "" || draft == final ||
		!BlockingReviewRejectsBody(st, chapter, final) {
		return expressionOnlyReviewReuseNone
	}
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil || bodyCheckpoint == nil {
		return expressionOnlyReviewReuseNone
	}
	planCheckpoint, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil || planCheckpoint == nil || bodyCheckpoint.Seq <= planCheckpoint.Seq {
		return expressionOnlyReviewReuseNone
	}
	// A strict current-hash blocking judgment grants exactly one further whole-
	// chapter render. This is distinct from the unjudged replacement capability
	// above: Inspector binds the advice and one-shot requirement to the exact
	// current bytes, and draft_chapter consumes it by producing a new hash.
	inspection, inspectErr := InspectDraftExternalGateWithStore(st, chapter)
	if inspectErr == nil && inspection.Status == DraftExternalGateRerenderAuthorized &&
		inspection.Requirement != nil && inspection.Requirement.AdviceComplete &&
		len(inspection.Requirement.RevisionPlan) > 0 &&
		strings.TrimSpace(inspection.CurrentBodySHA256) == reviewreport.BodySHA256(draft) &&
		strings.TrimSpace(inspection.Requirement.EvaluatedBodySHA256) == inspection.CurrentBodySHA256 {
		return expressionOnlyReviewReuseRejectedReplacement
	}
	status, err := loadDraftExternalJudgeStatus(st.Dir(), chapter)
	if err != nil || !expressionOnlyReviewReplacementReadyForFinalizer(
		status,
		reviewreport.BodySHA256(draft),
		inspection,
		inspectErr,
	) {
		return expressionOnlyReviewReuseNone
	}
	// This phase is a finalizer/check capability, not a second prose-render
	// authorization. The exact current hash has passed DeepSeek, so
	// check_consistency must be able to run the local hard gate even when
	// Inspector already reports LocalSoftEditPending. draft_chapter,
	// edit_chapter and commit_chapter each enforce that external/local state
	// independently: a soft failure permits one edit, a structural failure
	// permits one whole render, and neither state permits commit.
	return expressionOnlyReviewReuseReplacement
}

func expressionOnlyReviewReplacementReadyForFinalizer(
	status *draftExternalJudgeStatus,
	draftSHA string,
	inspection DraftExternalGateInspection,
	inspectErr error,
) bool {
	if inspectErr != nil || status == nil || !status.AdviceComplete || status.Blocking ||
		status.PassExclusivePercent <= 0 ||
		status.AIProbabilityPercent >= status.PassExclusivePercent ||
		strings.TrimSpace(status.BodySHA256) != strings.TrimSpace(draftSHA) ||
		strings.TrimSpace(inspection.CurrentBodySHA256) != strings.TrimSpace(draftSHA) ||
		strings.TrimSpace(inspection.EvaluatedBodySHA256) != strings.TrimSpace(draftSHA) {
		return false
	}
	return inspection.Status == DraftExternalGateApproved ||
		(inspection.Status == DraftExternalGateRejudgePending && inspection.LocalSoftEditPending)
}
