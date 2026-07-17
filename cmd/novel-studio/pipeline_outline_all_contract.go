package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type pipelineOutlineAllOperationIntent struct {
	Version              string                         `json:"version"`
	AttemptID            string                         `json:"attempt_id"`
	Action               domain.OutlineAllPendingAction `json:"action"`
	BeforeLayeredDigest  string                         `json:"before_layered_digest"`
	BeforeVolumes        []domain.VolumeOutline         `json:"before_volumes"`
	ContextRoot          string                         `json:"context_root"`
	VisibleContextDigest string                         `json:"visible_context_digest"`
	VisibleContextBytes  int                            `json:"visible_context_bytes"`
	ModelIdentityDigest  string                         `json:"model_identity_digest"`
	PromptProtocolDigest string                         `json:"prompt_protocol_digest"`
	CreatedAt            string                         `json:"created_at"`
	IntentDigest         string                         `json:"intent_digest"`
}

type pipelineOutlineAllOperationReceipt struct {
	Version              string                         `json:"version"`
	AttemptID            string                         `json:"attempt_id"`
	Action               domain.OutlineAllPendingAction `json:"action"`
	IntentDigest         string                         `json:"intent_digest"`
	BeforeLayeredDigest  string                         `json:"before_layered_digest"`
	AfterLayeredDigest   string                         `json:"after_layered_digest"`
	DerivedFlatDigest    string                         `json:"derived_flat_digest"`
	ModelIdentityDigest  string                         `json:"model_identity_digest"`
	PromptProtocolDigest string                         `json:"prompt_protocol_digest"`
	ContextRoot          string                         `json:"context_root"`
	VisibleContextDigest string                         `json:"visible_context_digest"`
	VisibleContextBytes  int                            `json:"visible_context_bytes"`
	CompletedAt          string                         `json:"completed_at"`
	ReceiptDigest        string                         `json:"receipt_digest"`
}

func repairPipelineOutlineAllDerivedArtifacts(st *store.Store, volumes []domain.VolumeOutline) (string, error) {
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		return "", err
	}
	flat := domain.FlattenOutline(volumes)
	if err := st.Outline.SaveOutline(flat); err != nil {
		return "", err
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return "", fmt.Errorf("outline-all repair requires progress: %w", err)
	}
	progress.TotalChapters = domain.TotalChapters(volumes)
	if err := st.Progress.Save(progress); err != nil {
		return "", err
	}
	return domain.ComputeFlatOutlineDigest(flat)
}

func pipelineOutlineAllLayeredDigest(volumes []domain.VolumeOutline) string {
	digest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		return ""
	}
	return digest
}

func pipelineOutlineAllOperationIntentPath(operation int) string {
	return fmt.Sprintf("meta/planning/outline_all_operations/%04d.intent.json", operation)
}

func pipelineOutlineAllOperationReceiptPath(operation int) string {
	return fmt.Sprintf("meta/planning/outline_all_operations/%04d.receipt.json", operation)
}

func signPipelineOutlineAllOperationIntent(intent pipelineOutlineAllOperationIntent) (pipelineOutlineAllOperationIntent, error) {
	intent.IntentDigest = ""
	digest, err := pipelineProjectAllDigestE(intent)
	if err != nil {
		return intent, err
	}
	intent.IntentDigest = digest
	return intent, nil
}

func signPipelineOutlineAllOperationReceipt(receipt pipelineOutlineAllOperationReceipt) (pipelineOutlineAllOperationReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := pipelineProjectAllDigestE(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

func pipelineProjectAllDigestE(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return pipelineBytesSHA(raw), nil
}

func validatePipelineOutlineAllMutation(
	before, after []domain.VolumeOutline,
	action domain.OutlineAllPendingAction,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
) error {
	if err := domain.ValidateOutlineAllPendingAction(action); err != nil {
		return err
	}
	beforeTotal := domain.TotalChapters(before)
	afterTotal := domain.TotalChapters(after)
	switch action.Type {
	case domain.OutlineAllActionAppendVolume:
		if len(after) != len(before)+1 {
			return fmt.Errorf("append_volume changed volume count by %d, want exactly 1", len(after)-len(before))
		}
		for i := range before {
			if !reflect.DeepEqual(before[i], after[i]) {
				return fmt.Errorf("append_volume changed existing volume index=%d", before[i].Index)
			}
		}
		added := after[len(after)-1]
		if added.Index != action.ExpectedVolumeIndex || action.Volume != added.Index {
			return fmt.Errorf("append_volume added index=%d, intent=%d", added.Index, action.ExpectedVolumeIndex)
		}
		if len(added.Arcs) == 0 {
			return fmt.Errorf("append_volume added no arcs")
		}
		gotSpans := make([]int, 0, len(added.Arcs))
		for _, arc := range added.Arcs {
			if arc.IsExpanded() || arc.EstimatedChapters <= 0 || len(arc.ContractRefs) != 0 {
				return fmt.Errorf("append_volume must add reservation-only arcs")
			}
			gotSpans = append(gotSpans, arc.EstimatedChapters)
		}
		wantSpans, err := domain.RecommendedOutlineAllArcSpans(action.ExpectedChapterSpan)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(gotSpans, wantSpans) || action.ExpectedArcSpans != domain.FormatOutlineAllArcSpans(wantSpans) {
			return fmt.Errorf("append_volume arc spans=%v want deterministic %v", gotSpans, wantSpans)
		}
		if afterTotal-beforeTotal != action.ExpectedChapterSpan {
			return fmt.Errorf("append_volume span=%d, intent=%d", afterTotal-beforeTotal, action.ExpectedChapterSpan)
		}
		if domain.RealVolumeCount(after) > target.TargetVolumes || afterTotal > target.TargetChapters {
			return fmt.Errorf("append_volume exceeded frozen target")
		}
	case domain.OutlineAllActionMapContracts:
		if len(after) != len(before) || beforeTotal != afterTotal || afterTotal != action.ExpectedChapterSpan {
			return fmt.Errorf("map_contracts changed frozen volume/chapter span")
		}
		for vi := range before {
			if before[vi].Index != after[vi].Index || before[vi].Title != after[vi].Title ||
				before[vi].Theme != after[vi].Theme || len(before[vi].Arcs) != len(after[vi].Arcs) {
				return fmt.Errorf("map_contracts changed volume structure")
			}
			for ai := range before[vi].Arcs {
				oldArc, newArc := before[vi].Arcs[ai], after[vi].Arcs[ai]
				oldArc.ContractRefs = nil
				newArc.ContractRefs = nil
				if !reflect.DeepEqual(oldArc, newArc) {
					return fmt.Errorf("map_contracts changed V%dA%d beyond contract_refs", before[vi].Index, before[vi].Arcs[ai].Index)
				}
			}
		}
		if issues := domain.StoryContractSkeletonIssues(after, compass, true); len(issues) > 0 {
			return fmt.Errorf("map_contracts coverage invalid: %s", strings.Join(issues, "; "))
		}
	case domain.OutlineAllActionExpandArc, domain.OutlineAllActionReviseArc:
		if len(after) != len(before) || beforeTotal != afterTotal {
			return fmt.Errorf("%s changed frozen volume/chapter span", action.Type)
		}
		found := false
		for vi := range before {
			if before[vi].Index != after[vi].Index || before[vi].Title != after[vi].Title ||
				before[vi].Theme != after[vi].Theme || len(before[vi].Arcs) != len(after[vi].Arcs) {
				return fmt.Errorf("%s changed volume structure", action.Type)
			}
			for ai := range before[vi].Arcs {
				oldArc, newArc := before[vi].Arcs[ai], after[vi].Arcs[ai]
				isTarget := before[vi].Index == action.Volume && oldArc.Index == action.Arc
				if !isTarget {
					if !reflect.DeepEqual(oldArc, newArc) {
						return fmt.Errorf("%s changed non-target V%dA%d", action.Type, before[vi].Index, oldArc.Index)
					}
					continue
				}
				found = true
				if oldArc.Index != newArc.Index || oldArc.Title != newArc.Title || oldArc.Goal != newArc.Goal ||
					!reflect.DeepEqual(oldArc.ContractRefs, newArc.ContractRefs) ||
					oldArc.ChapterSpan() != newArc.ChapterSpan() || newArc.ChapterSpan() != action.ExpectedChapterSpan {
					return fmt.Errorf("%s changed target arc identity/reservation", action.Type)
				}
				switch action.Type {
				case domain.OutlineAllActionExpandArc:
					if oldArc.IsExpanded() || !newArc.IsExpanded() || len(newArc.Chapters) != action.ExpectedChapterSpan || newArc.EstimatedChapters != 0 {
						return fmt.Errorf("expand_arc did not replace the exact reservation")
					}
				case domain.OutlineAllActionReviseArc:
					if !oldArc.IsExpanded() || !newArc.IsExpanded() || len(oldArc.Chapters) != len(newArc.Chapters) || newArc.EstimatedChapters != 0 {
						return fmt.Errorf("revise_arc did not preserve the exact expanded span")
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("%s target V%dA%d not found", action.Type, action.Volume, action.Arc)
		}
	default:
		return fmt.Errorf("unsupported outline-all mutation %q", action.Type)
	}
	return nil
}

func validatePipelineOutlineAllFlatIdentity(st *store.Store, volumes []domain.VolumeOutline) error {
	flat := domain.FlattenOutline(volumes)
	stored, err := st.Outline.LoadOutline()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(flat, stored) {
		return fmt.Errorf("outline.json is not the exact flatten(layered_outline) identity")
	}
	fullyExpanded := true
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			if !arc.IsExpanded() {
				fullyExpanded = false
				break
			}
		}
		if !fullyExpanded {
			break
		}
	}
	previous := 0
	for i, entry := range flat {
		if entry.Chapter <= previous {
			return fmt.Errorf(
				"flat outline chapter sequence is not strictly increasing at index=%d chapter=%d previous=%d",
				i,
				entry.Chapter,
				previous,
			)
		}
		if fullyExpanded && entry.Chapter != i+1 {
			return fmt.Errorf("flat outline chapter sequence breaks at index=%d chapter=%d", i, entry.Chapter)
		}
		previous = entry.Chapter
	}
	return nil
}

func outlineAllNextStructuralAction(
	volumes []domain.VolumeOutline,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
) (domain.OutlineAllPendingAction, bool, error) {
	realVolumes := domain.RealVolumeCount(volumes)
	total := domain.TotalChapters(volumes)
	if realVolumes > target.TargetVolumes || total > target.TargetChapters {
		return domain.OutlineAllPendingAction{}, false, fmt.Errorf("outline exceeds frozen target")
	}
	if realVolumes < target.TargetVolumes {
		remainingVolumes := target.TargetVolumes - realVolumes
		remainingChapters := target.TargetChapters - total
		if remainingChapters < remainingVolumes {
			return domain.OutlineAllPendingAction{}, false, fmt.Errorf("frozen target cannot allocate at least one chapter per remaining volume")
		}
		span := remainingChapters / remainingVolumes
		if remainingChapters%remainingVolumes != 0 {
			span++
		}
		arcSpans, err := domain.RecommendedOutlineAllArcSpans(span)
		if err != nil {
			return domain.OutlineAllPendingAction{}, false, err
		}
		nextIndex := 1
		for _, volume := range volumes {
			if volume.Index >= nextIndex {
				nextIndex = volume.Index + 1
			}
		}
		return domain.OutlineAllPendingAction{
			Type: domain.OutlineAllActionAppendVolume, Volume: nextIndex,
			ExpectedVolumeIndex: nextIndex, ExpectedChapterSpan: span,
			ExpectedArcSpans: domain.FormatOutlineAllArcSpans(arcSpans),
			FinalSkeleton:    remainingVolumes == 1,
		}, true, nil
	}
	if total != target.TargetChapters {
		return domain.OutlineAllPendingAction{}, false, fmt.Errorf(
			"volume target reached with chapter total=%d, frozen target=%d; reservations cannot be renumbered",
			total, target.TargetChapters,
		)
	}
	if issues := domain.StoryContractSkeletonIssues(volumes, compass, true); len(issues) > 0 {
		return domain.OutlineAllPendingAction{
			Type:                domain.OutlineAllActionMapContracts,
			ExpectedChapterSpan: total,
			FinalSkeleton:       true,
		}, true, nil
	}
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			if !arc.IsExpanded() {
				return domain.OutlineAllPendingAction{
					Type: domain.OutlineAllActionExpandArc, Volume: volume.Index,
					Arc: arc.Index, ExpectedChapterSpan: arc.ChapterSpan(),
				}, true, nil
			}
		}
	}
	return domain.OutlineAllPendingAction{}, false, nil
}

func outlineAllNextRevisionAction(
	volumes []domain.VolumeOutline,
	_ domain.StoryCompass,
) (domain.OutlineAllPendingAction, bool) {
	for _, issue := range domain.OutlineChapterContractIssues(volumes) {
		if issue.Code != "arc_unexpanded" {
			if arc := findOutlineAllArc(volumes, issue.Volume, issue.Arc); arc != nil && arc.IsExpanded() {
				return domain.OutlineAllPendingAction{
					Type: domain.OutlineAllActionReviseArc, Volume: issue.Volume,
					Arc: issue.Arc, ExpectedChapterSpan: arc.ChapterSpan(),
				}, true
			}
		}
	}
	cursor := 1
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			start := cursor
			cursor += arc.ChapterSpan()
			if outlineAllArcContractRefsNeedRevision(arc, start) {
				return domain.OutlineAllPendingAction{
					Type: domain.OutlineAllActionReviseArc, Volume: volume.Index,
					Arc: arc.Index, ExpectedChapterSpan: arc.ChapterSpan(),
				}, true
			}
		}
	}
	return domain.OutlineAllPendingAction{}, false
}

func outlineAllArcContractRefsNeedRevision(arc domain.ArcOutline, globalStart int) bool {
	return len(domain.OutlineArcContractPayoffIssues(arc, globalStart)) > 0
}

func findOutlineAllArc(volumes []domain.VolumeOutline, volume, arc int) *domain.ArcOutline {
	for vi := range volumes {
		if volumes[vi].Index != volume {
			continue
		}
		for ai := range volumes[vi].Arcs {
			if volumes[vi].Arcs[ai].Index == arc {
				return &volumes[vi].Arcs[ai]
			}
		}
	}
	return nil
}

func validatePipelineOutlineAllFinal(
	st *store.Store,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
) ([]domain.VolumeOutline, error) {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	if domain.RealVolumeCount(volumes) != target.TargetVolumes ||
		domain.TotalChapters(volumes) != target.TargetChapters {
		return nil, fmt.Errorf("outline-all target mismatch: volumes=%d/%d chapters=%d/%d",
			domain.RealVolumeCount(volumes), target.TargetVolumes,
			domain.TotalChapters(volumes), target.TargetChapters)
	}
	if issues := domain.OutlineAllArcSpanIssues(volumes); len(issues) > 0 {
		return nil, fmt.Errorf("outline-all arc spans are not single-call expandable: %s", summarizeOutlineContractIssues(issues, 12))
	}
	if issues := domain.OutlineChapterContractIssues(volumes); len(issues) > 0 {
		return nil, fmt.Errorf("outline-all chapter contracts are not ready: %s", summarizeOutlineContractIssues(issues, 12))
	}
	if issues := domain.StoryContractSkeletonIssues(volumes, compass, true); len(issues) > 0 {
		return nil, fmt.Errorf("outline-all skeleton compass coverage invalid: %s", strings.Join(issues, "; "))
	}
	if missing := domain.MissingCompassCoverage(volumes, compass); len(missing) > 0 {
		return nil, fmt.Errorf("outline-all chapter payoff coverage invalid: %s", strings.Join(missing, "; "))
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, volumes); err != nil {
		return nil, err
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, fmt.Errorf("outline-all progress missing: %w", err)
	}
	if progress.TotalChapters != target.TargetChapters {
		return nil, fmt.Errorf("outline-all progress total=%d want %d", progress.TotalChapters, target.TargetChapters)
	}
	return volumes, nil
}

func summarizeOutlineContractIssues(issues []domain.OutlineContractIssue, limit int) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, fmt.Sprintf("V%dA%dC%d:%s", issue.Volume, issue.Arc, issue.Chapter, issue.Code))
	}
	sort.Strings(parts)
	if len(parts) > limit {
		parts = append(parts[:limit], fmt.Sprintf("...+%d", len(parts)-limit))
	}
	return strings.Join(parts, ", ")
}

func validatePipelineOutlineAllOperationChain(
	st *store.Store,
	receipt *domain.OutlineAllExecutionReceipt,
) error {
	if st == nil || receipt == nil {
		return fmt.Errorf("outline-all operation chain requires store and receipt")
	}
	previousAfter := ""
	if repair, err := loadPipelineOutlineRepairEvidence(
		st, receipt.AttemptID, receipt.SourceSnapshotRoot,
	); err != nil {
		return fmt.Errorf("outline-all operation 0 repair chain invalid: %w", err)
	} else if repair != nil {
		previousAfter = repair.AfterLayeredDigest
	}
	for operation := 1; operation <= receipt.CompletedActionCount; operation++ {
		var intent pipelineOutlineAllOperationIntent
		if err := readPipelinePlanningJSON(
			filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineAllOperationIntentPath(operation))),
			&intent,
		); err != nil {
			return fmt.Errorf("outline-all operation %d intent chain missing: %w", operation, err)
		}
		intentDigest := intent.IntentDigest
		validatedIntent, err := signPipelineOutlineAllOperationIntent(intent)
		if err != nil || validatedIntent.IntentDigest != intentDigest ||
			intent.Action.Operation != operation ||
			intent.AttemptID != receipt.AttemptID ||
			intent.ModelIdentityDigest != receipt.ModelIdentityDigest ||
			intent.PromptProtocolDigest != receipt.PromptProtocolDigest ||
			intent.ContextRoot != pipelineOutlineAllOperationContextRoot(receipt.FoundationContextRoot, intent.BeforeLayeredDigest) ||
			intent.VisibleContextDigest == "" || intent.VisibleContextBytes <= 0 ||
			intent.BeforeLayeredDigest != intent.Action.BeforeLayeredDigest ||
			pipelineOutlineAllLayeredDigest(intent.BeforeVolumes) != intent.BeforeLayeredDigest {
			return fmt.Errorf("outline-all operation %d intent chain invalid", operation)
		}
		if previousAfter != "" && intent.BeforeLayeredDigest != previousAfter {
			return fmt.Errorf("outline-all operation %d chain is discontinuous", operation)
		}
		var opReceipt pipelineOutlineAllOperationReceipt
		if err := readPipelinePlanningJSON(
			filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineAllOperationReceiptPath(operation))),
			&opReceipt,
		); err != nil {
			return fmt.Errorf("outline-all operation %d receipt chain missing: %w", operation, err)
		}
		receiptDigest := opReceipt.ReceiptDigest
		validatedReceipt, err := signPipelineOutlineAllOperationReceipt(opReceipt)
		if err != nil || validatedReceipt.ReceiptDigest != receiptDigest ||
			opReceipt.AttemptID != receipt.AttemptID ||
			!domain.OutlineAllPendingActionEqual(opReceipt.Action, intent.Action) ||
			opReceipt.IntentDigest != intent.IntentDigest ||
			opReceipt.ContextRoot != intent.ContextRoot ||
			opReceipt.VisibleContextDigest != intent.VisibleContextDigest ||
			opReceipt.VisibleContextBytes != intent.VisibleContextBytes ||
			opReceipt.BeforeLayeredDigest != intent.BeforeLayeredDigest ||
			opReceipt.ModelIdentityDigest != receipt.ModelIdentityDigest ||
			opReceipt.PromptProtocolDigest != receipt.PromptProtocolDigest ||
			opReceipt.AfterLayeredDigest == "" || opReceipt.DerivedFlatDigest == "" {
			return fmt.Errorf("outline-all operation %d receipt chain invalid", operation)
		}
		previousAfter = opReceipt.AfterLayeredDigest
	}
	current, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return err
	}
	currentDigest, err := domain.ComputeLayeredOutlineDigest(current)
	if err != nil {
		return err
	}
	if receipt.PendingAction == nil {
		if previousAfter != "" && currentDigest != previousAfter {
			return fmt.Errorf("outline-all candidate layered digest is not the completed operation chain tail")
		}
		return nil
	}
	if receipt.PendingAction.Operation != receipt.CompletedActionCount+1 {
		return fmt.Errorf("outline-all pending operation does not follow completed chain")
	}
	if previousAfter != "" && receipt.PendingAction.BeforeLayeredDigest != previousAfter {
		return fmt.Errorf("outline-all pending operation does not continue completed chain")
	}
	return nil
}
