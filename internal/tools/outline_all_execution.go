package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func outlineAllExecutionModeActive(st *store.Store) (bool, error) {
	if st == nil {
		return false, nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return false, err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionOutlineAll {
		return false, nil
	}
	if lock.ProcessID != os.Getpid() {
		return false, fmt.Errorf("outline_all execution lock belongs to another process: %w", errs.ErrToolPrecondition)
	}
	return true, nil
}

// guardOutlineAllDynamicMaterialExecution makes the receipt-authorized
// mutation capability closed over its frozen prompt inputs. Live RAG/context
// retrieval and web research may be useful in ordinary Architect work, but
// they would make an outline-all operation depend on unreceipted state.
func guardOutlineAllDynamicMaterialExecution(st *store.Store, tool string) error {
	active, err := outlineAllExecutionModeActive(st)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	return fmt.Errorf("outline_all frozen-input execution rejects dynamic tool %s; use the content-addressed context embedded by the host: %w", tool, errs.ErrToolPrecondition)
}

func validateOutlineAllAppendVolumeContent(
	st *store.Store,
	volume domain.VolumeOutline,
) (bool, error) {
	active, err := outlineAllExecutionModeActive(st)
	if err != nil || !active {
		return active, err
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil || receipt.PendingAction == nil {
		return true, fmt.Errorf("outline_all append_volume lost its pending receipt: %w", errs.ErrToolPrecondition)
	}
	action := *receipt.PendingAction
	if action.Type != domain.OutlineAllActionAppendVolume ||
		volume.Index != action.ExpectedVolumeIndex {
		return true, fmt.Errorf("outline_all append_volume identity differs from pending action: %w", errs.ErrToolPrecondition)
	}
	span := 0
	gotSpans := make([]int, 0, len(volume.Arcs))
	for _, arc := range volume.Arcs {
		if arc.IsExpanded() || arc.EstimatedChapters <= 0 || len(arc.ContractRefs) != 0 {
			return true, fmt.Errorf("outline_all append_volume requires reservation-only arcs: %w", errs.ErrToolPrecondition)
		}
		span += arc.EstimatedChapters
		gotSpans = append(gotSpans, arc.EstimatedChapters)
	}
	if len(volume.Arcs) == 0 || span != action.ExpectedChapterSpan {
		return true, fmt.Errorf("outline_all append_volume span=%d want %d: %w", span, action.ExpectedChapterSpan, errs.ErrToolPrecondition)
	}
	wantSpans, err := domain.RecommendedOutlineAllArcSpans(action.ExpectedChapterSpan)
	if err != nil {
		return true, err
	}
	if !slices.Equal(gotSpans, wantSpans) || action.ExpectedArcSpans != domain.FormatOutlineAllArcSpans(wantSpans) {
		return true, fmt.Errorf("outline_all append_volume arc spans=%v want deterministic %v: %w", gotSpans, wantSpans, errs.ErrToolPrecondition)
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return true, err
	}
	after := append(append([]domain.VolumeOutline(nil), volumes...), volume)
	if domain.RealVolumeCount(after) > receipt.TargetVolumes ||
		domain.TotalChapters(after) > receipt.TargetChapters {
		return true, fmt.Errorf("outline_all append_volume exceeds frozen target: %w", errs.ErrToolPrecondition)
	}
	if action.FinalSkeleton &&
		(domain.RealVolumeCount(after) != receipt.TargetVolumes || domain.TotalChapters(after) != receipt.TargetChapters) {
		return true, fmt.Errorf("outline_all final skeleton does not land on the frozen target: %w", errs.ErrToolPrecondition)
	}
	return true, nil
}

func validateOutlineAllMapContractsContent(
	st *store.Store,
	assignments []domain.ArcContractAssignment,
) error {
	active, err := outlineAllExecutionModeActive(st)
	if err != nil || !active {
		return err
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil || receipt.PendingAction == nil ||
		receipt.PendingAction.Type != domain.OutlineAllActionMapContracts {
		return fmt.Errorf("outline_all map_contracts lost its pending receipt: %w", errs.ErrToolPrecondition)
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return err
	}
	if domain.RealVolumeCount(volumes) != receipt.TargetVolumes ||
		domain.TotalChapters(volumes) != receipt.TargetChapters {
		return fmt.Errorf("outline_all map_contracts requires the complete frozen skeleton: %w", errs.ErrToolPrecondition)
	}
	type key struct{ volume, arc int }
	provided := make(map[key][]domain.StoryContractRef, len(assignments))
	for _, assignment := range assignments {
		k := key{assignment.Volume, assignment.Arc}
		if _, exists := provided[k]; exists {
			return fmt.Errorf("outline_all map_contracts duplicate V%dA%d: %w", assignment.Volume, assignment.Arc, errs.ErrToolPrecondition)
		}
		provided[k] = assignment.ContractRefs
	}
	raw, _ := json.Marshal(volumes)
	var after []domain.VolumeOutline
	_ = json.Unmarshal(raw, &after)
	arcCount := 0
	for vi := range after {
		for ai := range after[vi].Arcs {
			arcCount++
			k := key{after[vi].Index, after[vi].Arcs[ai].Index}
			refs, ok := provided[k]
			if !ok {
				return fmt.Errorf("outline_all map_contracts missing V%dA%d: %w", k.volume, k.arc, errs.ErrToolPrecondition)
			}
			after[vi].Arcs[ai].ContractRefs = refs
			delete(provided, k)
		}
	}
	if len(assignments) != arcCount || len(provided) != 0 {
		return fmt.Errorf("outline_all map_contracts must cover the exact arc set: %w", errs.ErrToolPrecondition)
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil || compass == nil {
		return fmt.Errorf("outline_all map_contracts requires compass: %w", err)
	}
	if issues := domain.StoryContractSkeletonIssues(after, *compass, true); len(issues) > 0 {
		return fmt.Errorf("outline_all map_contracts invalid: %s: %w", strings.Join(issues, "; "), errs.ErrToolPrecondition)
	}
	return nil
}

func validateOutlineAllArcMutationContent(
	st *store.Store,
	kind domain.OutlineAllActionType,
	volume, arc int,
	chapters []domain.OutlineEntry,
) error {
	active, err := outlineAllExecutionModeActive(st)
	if err != nil || !active {
		return err
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(volumes)
	if err != nil {
		return err
	}
	var after []domain.VolumeOutline
	if err := json.Unmarshal(raw, &after); err != nil {
		return err
	}
	found := false
	cursor := 1
	for vi := range after {
		for ai := range after[vi].Arcs {
			target := &after[vi].Arcs[ai]
			start := cursor
			cursor += target.ChapterSpan()
			if after[vi].Index != volume || target.Index != arc {
				continue
			}
			found = true
			switch kind {
			case domain.OutlineAllActionExpandArc:
				if target.IsExpanded() || len(chapters) != target.EstimatedChapters {
					return fmt.Errorf("outline_all expand_arc must fill its exact reservation: %w", errs.ErrToolPrecondition)
				}
			case domain.OutlineAllActionReviseArc:
				if !target.IsExpanded() || len(chapters) != len(target.Chapters) {
					return fmt.Errorf("outline_all revise_arc must preserve its exact expanded span: %w", errs.ErrToolPrecondition)
				}
			default:
				return fmt.Errorf("outline_all unsupported arc mutation %q: %w", kind, errs.ErrToolPrecondition)
			}
			target.Chapters = chapters
			target.EstimatedChapters = 0
			if err := validateOutlineAllArcContractPayoffs(*target, start); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("outline_all target V%dA%d not found: %w", volume, arc, errs.ErrToolPrecondition)
	}
	if !domain.ArcChapterContractReady(after, volume, arc) {
		var targetIssues []domain.OutlineContractIssue
		for _, issue := range domain.OutlineChapterContractIssues(after) {
			if issue.Volume == volume && issue.Arc == arc {
				targetIssues = append(targetIssues, issue)
			}
		}
		return fmt.Errorf("outline_all V%dA%d chapter contract is not ready (%v): %w", volume, arc, targetIssues, errs.ErrToolPrecondition)
	}
	return nil
}

func validateOutlineAllArcContractPayoffs(arc domain.ArcOutline, globalStart int) error {
	want := make(map[string]domain.StoryContractRef, len(arc.ContractRefs))
	for _, ref := range arc.ContractRefs {
		want[ref.ID] = ref
	}
	seen := make(map[string]int, len(want))
	for offset, chapter := range arc.Chapters {
		chapterNo := globalStart + offset
		for _, ref := range chapter.ContractRefs {
			expected, ok := want[ref.ID]
			if !ok || ref != expected || ref.PlannedPayoffChapter != chapterNo {
				return fmt.Errorf("outline_all arc chapter has an unknown or misplaced contract ref %q: %w", ref.ID, errs.ErrToolPrecondition)
			}
			seen[ref.ID]++
		}
	}
	for id := range want {
		if seen[id] != 1 {
			return fmt.Errorf("outline_all arc contract ref %q payoff count=%d want 1: %w", id, seen[id], errs.ErrToolPrecondition)
		}
	}
	return nil
}

func guardOutlineAllFoundationType(st *store.Store, kind, scale string) error {
	if st == nil {
		return nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionOutlineAll {
		return nil
	}
	if lock.ProcessID != os.Getpid() {
		return fmt.Errorf("outline_all execution lock belongs to another process: %w", errs.ErrToolPrecondition)
	}
	if kind != string(domain.OutlineAllActionAppendVolume) &&
		kind != string(domain.OutlineAllActionMapContracts) &&
		kind != string(domain.OutlineAllActionExpandArc) &&
		kind != string(domain.OutlineAllActionReviseArc) {
		return fmt.Errorf(
			"outline_all execution lock rejects save_foundation type %q; only the exact pending append_volume/map_contracts/expand_arc/revise_arc mutation is allowed: %w",
			kind,
			errs.ErrToolPrecondition,
		)
	}
	if scale != "" {
		return fmt.Errorf("outline_all pending mutation cannot change planning scale: %w", errs.ErrToolPrecondition)
	}
	return nil
}

func guardOutlineAllFoundationMutation(
	st *store.Store,
	actual domain.OutlineAllPendingAction,
) error {
	if st == nil {
		return nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionOutlineAll {
		return nil
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return err
	}
	if receipt == nil || receipt.PendingAction == nil {
		return fmt.Errorf("outline_all save_foundation mutation has no pending receipt action: %w", errs.ErrToolPrecondition)
	}
	// Operation identity, the immutable before-root and final-skeleton flag are
	// host-issued fields rather than model arguments. The remaining fields come
	// from the decoded mutation payload and must match them exactly.
	actual.Operation = receipt.PendingAction.Operation
	actual.BeforeLayeredDigest = receipt.PendingAction.BeforeLayeredDigest
	actual.FinalSkeleton = receipt.PendingAction.FinalSkeleton
	if actual.Type == domain.OutlineAllActionAppendVolume {
		spans, spanErr := domain.RecommendedOutlineAllArcSpans(actual.ExpectedChapterSpan)
		if spanErr != nil {
			return spanErr
		}
		actual.ExpectedArcSpans = domain.FormatOutlineAllArcSpans(spans)
	}
	authorized, err := AuthorizeChapterZeroOutlineAllPendingAction(st, &actual)
	if err != nil {
		return err
	}
	if !authorized {
		return fmt.Errorf(
			"save_foundation mutation does not match the active outline_all pending action (type=%s volume=%d arc=%d span=%d): %w",
			actual.Type,
			actual.Volume,
			actual.Arc,
			actual.ExpectedChapterSpan,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}

// ChapterZeroOutlineAllWorldTickBypassAuthorized reports whether the active
// process holds the narrowly scoped capability for full-book foundation work
// before chapter 1 canon exists. A false result is intentionally silent so
// callers can apply the ordinary rolling world-tick gate unchanged.
func ChapterZeroOutlineAllWorldTickBypassAuthorized(
	st *store.Store,
	requested *domain.OutlineAllPendingAction,
) (bool, error) {
	return AuthorizeChapterZeroOutlineAllPendingAction(st, requested)
}

// AuthorizeChapterZeroOutlineAllPendingAction validates the actual requested
// structural mutation against the receipt capability. SaveFoundation and
// dispatch gates can share this exact type/volume/arc check.
func AuthorizeChapterZeroOutlineAllPendingAction(
	st *store.Store,
	requested *domain.OutlineAllPendingAction,
) (bool, error) {
	if st == nil || requested == nil {
		return false, nil
	}
	if err := domain.ValidateOutlineAllPendingAction(*requested); err != nil {
		return false, nil
	}
	writingMode, err := st.LoadWritingPipelineMode()
	if err != nil {
		return false, err
	}
	if writingMode == nil || writingMode.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
		return false, nil
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return false, err
	}
	if receipt == nil ||
		receipt.Status != domain.OutlineAllExecutionBuilding ||
		receipt.PendingAction == nil {
		return false, nil
	}
	candidateDir, err := filepath.Abs(st.Dir())
	if err != nil {
		return false, err
	}
	receiptCandidateDir, err := filepath.Abs(receipt.CandidateDir)
	if err != nil {
		return false, err
	}
	resolvedCandidateDir, err := filepath.EvalSymlinks(candidateDir)
	if err != nil {
		return false, err
	}
	resolvedReceiptDir, err := filepath.EvalSymlinks(receiptCandidateDir)
	if err != nil || filepath.Clean(resolvedReceiptDir) != filepath.Clean(resolvedCandidateDir) {
		return false, err
	}
	if !domain.OutlineAllPendingActionEqual(*receipt.PendingAction, *requested) {
		return false, nil
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, err
	}
	layeredDigest, err := domain.ComputeLayeredOutlineDigest(layered)
	if err != nil {
		return false, err
	}
	if layeredDigest != receipt.PendingAction.BeforeLayeredDigest {
		return false, nil
	}
	if receipt.WritingMode != writingMode.Mode ||
		receipt.WritingModeReceiptDigest != writingMode.ReceiptDigest {
		return false, nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return false, err
	}
	if lock == nil ||
		lock.Mode != domain.PipelineExecutionOutlineAll ||
		lock.ProcessID != os.Getpid() {
		return false, nil
	}
	if err := domain.ValidateOutlineAllExecutionLockBinding(*receipt, *lock); err != nil {
		return false, nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return false, err
	}
	if err := domain.ValidateOutlineAllChapterZeroProgress(progress, *receipt); err != nil {
		return false, nil
	}
	if err := st.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return false, nil
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		return false, err
	}
	if compass == nil {
		return false, nil
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(*compass)
	if err != nil {
		return false, err
	}
	if receipt.CompassDigest != compassDigest ||
		receipt.EstimatedScale != compass.EstimatedScale ||
		receipt.EndingDirection != compass.EndingDirection ||
		!slices.Equal(receipt.NonNegotiables, compass.NonNegotiables) {
		return false, nil
	}
	return true, nil
}
