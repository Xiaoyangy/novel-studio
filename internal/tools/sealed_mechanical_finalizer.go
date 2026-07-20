package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// SealedMechanicalFinalizeDisposition describes whether the Host-only sealed
// finalizer consumed the current draft. The ordinary Drafter finalizer remains
// responsible for every state that can require prose judgment or mutation.
type SealedMechanicalFinalizeDisposition string

const (
	SealedMechanicalFinalizeNotApplicable SealedMechanicalFinalizeDisposition = "not_applicable"
	SealedMechanicalFinalizeNeedsAgent    SealedMechanicalFinalizeDisposition = "needs_agent"
	SealedMechanicalFinalizeCommitted     SealedMechanicalFinalizeDisposition = "committed"
)

// SealedMechanicalFinalizeResult is deliberately small: the outer pipeline
// only needs to know whether it should return, fall back to the existing Agent,
// or report a fail-closed execution error.
type SealedMechanicalFinalizeResult struct {
	Disposition SealedMechanicalFinalizeDisposition `json:"disposition"`
	Chapter     int                                 `json:"chapter"`
	BodySHA256  string                              `json:"body_sha256,omitempty"`
	Reason      string                              `json:"reason,omitempty"`
}

// FinalizeSealedDraftMechanically closes the exact-body sealed_v2 happy path
// without another LLM turn. It does not replace any gate: check_consistency and
// commit_chapter are executed through their normal tool implementations, and
// commit_chapter therefore revalidates the render lease, frozen plan, body
// checkpoint, DeepSeek approval, hard facts, title, word contract and AIGC
// gates before applying the server-owned projected control plane.
//
// The fast path is intentionally limited to an initial, already-approved
// chapter. Missing drafts and semantic/edit states return NotApplicable so the
// existing router keeps its historical behavior. Errors after the exact-body
// path becomes applicable are returned fail-closed because commit_chapter is a
// durable saga and must never be followed by a speculative LLM retry in the
// same turn.
func FinalizeSealedDraftMechanically(
	ctx context.Context,
	st *store.Store,
	chapter int,
) (SealedMechanicalFinalizeResult, error) {
	result := SealedMechanicalFinalizeResult{
		Disposition: SealedMechanicalFinalizeNotApplicable,
		Chapter:     chapter,
	}
	if st == nil || chapter <= 0 {
		return result, fmt.Errorf("sealed mechanical finalizer: invalid chapter %d or nil store", chapter)
	}
	if !sealedCommitSchemaUsesServerControlPlane(st) {
		result.Reason = "active execution is not the exact sealed_v2 render lease"
		return result, nil
	}
	sealedMode, err := sealedTwoPassModeActive(st)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer load writing mode: %w", err)
	}
	if !sealedMode || !pipelineWritingManaged(st) {
		result.Reason = "sealed_v2 writing mode or managed pipeline identity is missing"
		return result, nil
	}

	progress, err := st.Progress.Load()
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer load progress: %w", err)
	}
	if progress == nil || st.Progress.IsChapterCompleted(chapter) ||
		sealedMechanicalFinalizeRewriteState(progress, chapter) {
		result.Reason = "chapter is completed, queued for rewrite, or not in the initial writing flow"
		return result, nil
	}
	pendingCommit, err := st.Signals.LoadPendingCommit()
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer load pending commit: %w", err)
	}
	if pendingCommit != nil {
		result.Reason = "an existing commit saga must use its normal recovery route"
		return result, nil
	}

	body, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer load draft: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		result.Reason = "no current draft"
		return result, nil
	}
	result.BodySHA256 = reviewreport.BodySHA256(body)

	// A body on disk without its exact checkpoint is an interrupted write, not
	// an invitation to let another actor guess whether the bytes are complete.
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer current body checkpoint: %w", err)
	}
	if want := "sha256:" + result.BodySHA256; bodyCheckpoint.Digest != want {
		return result, fmt.Errorf(
			"sealed mechanical finalizer body checkpoint digest=%q, want %q",
			bodyCheckpoint.Digest,
			want,
		)
	}
	if ExplicitRerenderRequestActive(st, chapter) {
		result.Reason = "an explicit rerender request has not been consumed by a newer body"
		return result, nil
	}

	inspection, err := InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer inspect exact-body external gate: %w", err)
	}
	if !sealedMechanicalFinalizeExternalGateEligible(inspection, result.BodySHA256) {
		result.Reason = sealedMechanicalFinalizeExternalGateReason(inspection)
		return result, nil
	}

	checkArgs, err := json.Marshal(struct {
		Chapter int `json:"chapter"`
	}{Chapter: chapter})
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer encode consistency args: %w", err)
	}
	checkedRaw, err := NewCheckConsistencyTool(st).Execute(ctx, checkArgs)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer check_consistency: %w", err)
	}
	var checked struct {
		HardGateViolations []string                    `json:"hard_gate_violations"`
		Receipt            DraftHardConsistencyReceipt `json:"hard_consistency_receipt"`
	}
	if err := json.Unmarshal(checkedRaw, &checked); err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer decode consistency result: %w", err)
	}
	if !checked.Receipt.Passed || checked.Receipt.BodySHA256 != result.BodySHA256 ||
		len(checked.Receipt.HardGateViolations) > 0 || len(checked.HardGateViolations) > 0 {
		result.Disposition = SealedMechanicalFinalizeNeedsAgent
		result.Reason = fmt.Sprintf(
			"exact-body consistency requires Agent work (passed=%t, violations=%d)",
			checked.Receipt.Passed,
			max(len(checked.Receipt.HardGateViolations), len(checked.HardGateViolations)),
		)
		return result, nil
	}

	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer load chapter plan: %w", err)
	}
	if plan == nil || plan.Chapter != chapter {
		return result, fmt.Errorf("sealed mechanical finalizer missing exact chapter %d plan", chapter)
	}
	commitArgs := commitChapterArgs{
		Chapter: chapter,
		Summary: deterministicSealedCommitSummary(*plan),
	}
	if plan.CausalSimulation.LiteraryRendering != nil {
		commitArgs.POV = strings.TrimSpace(plan.CausalSimulation.LiteraryRendering.Focalizer)
	}
	commitRaw, err := json.Marshal(commitArgs)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer encode commit args: %w", err)
	}
	committedRaw, err := NewCommitChapterTool(st).Execute(ctx, commitRaw)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer commit_chapter: %w", err)
	}
	var committed domain.CommitResult
	if err := json.Unmarshal(committedRaw, &committed); err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer decode commit result: %w", err)
	}
	if !committed.Committed || committed.Chapter != chapter {
		return result, fmt.Errorf(
			"sealed mechanical finalizer commit returned committed=%t chapter=%d, want chapter=%d",
			committed.Committed,
			committed.Chapter,
			chapter,
		)
	}
	finalBody, err := st.Drafts.LoadChapterText(chapter)
	if err != nil {
		return result, fmt.Errorf("sealed mechanical finalizer verify final body: %w", err)
	}
	if reviewreport.BodySHA256(finalBody) != result.BodySHA256 || !st.Progress.IsChapterCompleted(chapter) {
		return result, fmt.Errorf("sealed mechanical finalizer commit did not persist the exact body/progress identity")
	}

	result.Disposition = SealedMechanicalFinalizeCommitted
	result.Reason = "exact-body gates passed; Host consistency+commit completed"
	return result, nil
}

func sealedMechanicalFinalizeRewriteState(progress *domain.Progress, chapter int) bool {
	if progress == nil {
		return true
	}
	for _, pending := range progress.PendingRewrites {
		if pending == chapter {
			return true
		}
	}
	switch progress.Flow {
	case domain.FlowReviewing, domain.FlowRewriting, domain.FlowPolishing:
		return true
	default:
		return false
	}
}

func sealedMechanicalFinalizeExternalGateEligible(
	inspection DraftExternalGateInspection,
	bodySHA256 string,
) bool {
	bodySHA256 = strings.TrimSpace(bodySHA256)
	return bodySHA256 != "" &&
		inspection.Status == DraftExternalGateApproved &&
		inspection.ArtifactExists &&
		strings.TrimSpace(inspection.CurrentBodySHA256) == bodySHA256 &&
		strings.TrimSpace(inspection.EvaluatedBodySHA256) == bodySHA256 &&
		!inspection.LocalSoftEditPending &&
		!inspection.LocalSoftEditBeforeJudge &&
		!inspection.LocalSoftEditConsumed &&
		!inspection.LocalSoftEditFailedClosed &&
		!inspection.RequiresRegisteredRetest &&
		!RequiresRegisteredExternalRetest(inspection.Requirement)
}

func sealedMechanicalFinalizeExternalGateReason(inspection DraftExternalGateInspection) string {
	switch {
	case inspection.LocalSoftEditFailedClosed:
		return "bounded local-soft edit failed closed"
	case inspection.LocalSoftEditPending:
		return "bounded local-soft edit is pending"
	case inspection.LocalSoftEditBeforeJudge || inspection.LocalSoftEditConsumed:
		return "draft belongs to a local-soft edit recovery path"
	case inspection.RequiresRegisteredRetest || RequiresRegisteredExternalRetest(inspection.Requirement):
		return "registered external retest is pending"
	case !inspection.ArtifactExists:
		return "exact-body DeepSeek artifact is missing"
	default:
		return fmt.Sprintf("exact-body external gate status=%s is not mechanically finalizable", inspection.Status)
	}
}

func deterministicSealedCommitSummary(plan domain.ChapterPlan) string {
	parts := RenderRequiredOutcomes(plan)
	if len(parts) == 0 {
		parts = append(parts, plan.CausalSimulation.OutcomeShift...)
	}
	if len(compactStrings(parts)) == 0 {
		parts = []string{plan.Goal}
	}
	parts = compactStrings(parts)
	if len(parts) == 0 {
		parts = []string{fmt.Sprintf("第%d章按冻结计划完成既定推进。", plan.Chapter)}
	}
	return truncateRunes(strings.Join(parts, "；"), 197)
}
