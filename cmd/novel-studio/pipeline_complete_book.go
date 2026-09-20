package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func pipelineCompleteBook(opts cliOptions, flags pipelineFlags) (returnErr error) {
	if flags.Start != 0 || flags.End != 0 {
		return fmt.Errorf("complete-book requires the complete frozen book; --from/--to are not allowed")
	}
	outputDir, release, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return err
	}
	defer releasePublishedOutlineAllStage(release, "complete-book", &returnErr)
	if outputDir == "" {
		return fmt.Errorf("complete-book requires a configured published book")
	}
	st := store.NewStore(outputDir)
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf("complete-book cannot run during active %s execution", lock.Mode)
	}
	return completePipelineBook(st)
}

// Uses only existing whole-book, accepted-arc and exact-body evidence. There
// is deliberately no model call, new global review, or new receipt protocol.
func completePipelineBook(st *store.Store) error {
	chapters, progress, err := validatePipelineCompleteBookSources(st)
	if err != nil {
		return err
	}
	manuscript, err := buildPipelineMergedManuscript(st, chapters)
	if err != nil {
		return err
	}
	path := filepath.Join(st.Dir(), "正文.md")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("complete-book refuses non-regular existing 正文.md")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, []byte(manuscript)) {
			return fmt.Errorf("existing 正文.md differs from verified chapters; retained unchanged, resolve the stale export explicitly")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err := st.Drafts.SaveMergedManuscript(manuscript); err != nil {
			return fmt.Errorf("write verified whole-book manuscript: %w", err)
		}
	}
	// The marker is last. A crash after the atomic manuscript write is safely
	// resumed by revalidating sources and the deterministic existing bytes.
	if progress.Phase != domain.PhaseComplete {
		if err := st.Progress.MarkComplete(); err != nil {
			return fmt.Errorf("mark verified book complete: %w", err)
		}
	}
	return nil
}

func validatePipelineCompleteBookSources(st *store.Store) ([]int, *domain.Progress, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("complete-book requires readable progress: %w", err)
	}
	if progress == nil {
		return nil, nil, fmt.Errorf("complete-book is missing progress")
	}
	if progress.Phase != domain.PhaseWriting && progress.Phase != domain.PhaseComplete {
		return nil, nil, fmt.Errorf("complete-book requires writing or complete phase")
	}
	if len(progress.PendingRewrites) > 0 || progress.InProgressChapter != 0 {
		return nil, nil, fmt.Errorf("complete-book refuses unfinished writing or pending rewrites")
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return nil, nil, err
	}
	if meta == nil || meta.PlanningTier != domain.PlanningTierLong {
		return nil, nil, fmt.Errorf("complete-book is the explicit long-form mechanical exit; short books must use finalize")
	}
	contract, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return nil, nil, fmt.Errorf("complete-book requires a verified frozen whole-book contract: %w", err)
	}
	if contract == nil {
		return nil, nil, fmt.Errorf("complete-book is missing its frozen whole-book contract")
	}
	if err := tools.RequireFrozenBookCompletion(st, progress); err != nil {
		return nil, nil, err
	}
	chapters, err := chapterNumbersFromFiles(filepath.Join(st.Dir(), "chapters"))
	if err != nil {
		return nil, nil, err
	}
	if len(chapters) != contract.TargetChapters {
		return nil, nil, fmt.Errorf("complete-book final chapter files do not match the frozen complete range")
	}
	for i, ch := range chapters {
		if ch != i+1 {
			return nil, nil, fmt.Errorf("complete-book final chapter files are not contiguous from 1")
		}
		if err := requirePipelineAcceptedExactReview(st.Dir(), ch); err != nil {
			return nil, nil, err
		}
		if issues := currentRegisteredExternalDeliveryIssues(st.Dir(), ch); len(issues) > 0 {
			return nil, nil, fmt.Errorf("chapter %d external delivery blockers: %v", ch, issues)
		}
	}
	if err := requirePipelineWholeBookArcChain(st, contract.TargetChapters); err != nil {
		return nil, nil, err
	}
	return chapters, progress, nil
}

func requirePipelineWholeBookArcChain(st *store.Store, last int) error {
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil {
		return fmt.Errorf("complete-book lacks active sealed generation: %w", err)
	}
	if active == nil {
		return fmt.Errorf("complete-book is missing active sealed generation")
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil {
		return fmt.Errorf("complete-book lacks realization cursor: %w", err)
	}
	if cursor == nil {
		return fmt.Errorf("complete-book is missing realization cursor")
	}
	if cursor.ActiveGenerationID != active.GenerationID || cursor.ActivePromotedChapter != 0 || cursor.LastAcceptedChapter != last || cursor.NextPromoteChapter != last+1 || len(cursor.BlockedByRewrites) > 0 {
		return fmt.Errorf("complete-book realization cursor is not fully accepted at the frozen book end")
	}
	seen := map[string]bool{}
	id, end := active.GenerationID, last
	var child *domain.PlanningGenerationV2
	for end > 0 {
		if id == "" || seen[id] {
			return fmt.Errorf("complete-book has a missing/cyclic sealed predecessor chain")
		}
		generation, err := projected.LoadSealedGeneration(id)
		if err != nil {
			return fmt.Errorf("complete-book load sealed predecessor %s: %w", id, err)
		}
		if generation == nil {
			return fmt.Errorf("complete-book is missing sealed predecessor %s", id)
		}
		if generation.LastProjectedChapter != end || generation.BookHorizonChapter != last {
			return fmt.Errorf("complete-book predecessor %s has a gap or foreign book horizon", id)
		}
		scope, err := locatePipelineArcScope(st, end)
		if err != nil {
			return err
		}
		if scope.LastChapter != end || generation.ScopeID != domain.DeriveArcCycleID(scope.Volume, scope.Arc, scope.FirstChapter, scope.LastChapter) {
			return fmt.Errorf("complete-book predecessor %s is not its complete logical arc", id)
		}
		proof, err := requirePipelineArcCompletion(st, generation)
		if err != nil {
			return err
		}
		if proof.FirstChapter != scope.FirstChapter || proof.LastChapter != scope.LastChapter {
			return fmt.Errorf("complete-book arc proof does not cover its whole logical scope")
		}
		outcome, err := projected.LoadActualOutcomeReceipt(id, end, proof.FinalOutcomeReceiptDigest)
		if err != nil {
			return fmt.Errorf("complete-book final actual outcome missing: %w", err)
		}
		if outcome == nil {
			return fmt.Errorf("complete-book is missing final actual outcome for %s chapter %d", id, end)
		}
		if child == nil {
			if cursor.LastOutcomeReceiptDigest != outcome.ReceiptDigest {
				return fmt.Errorf("complete-book terminal outcome differs from realization cursor")
			}
		} else if child.BaseCanonRoot != outcome.ActualCanonRoot || child.BaseStateRoot != outcome.ActualPostStateRoot {
			return fmt.Errorf("complete-book consecutive arcs do not share the accepted canon/state boundary")
		}
		first := generation
		ids := []string{id}
		if pipelineArcRequiresWindowAggregate(generation) {
			ids, _, err = validatePipelineArcWindowCompletionSources(st, generation)
			if err != nil {
				return err
			}
			first, err = projected.LoadSealedGeneration(ids[0])
			if err != nil {
				return fmt.Errorf("complete-book missing first window: %w", err)
			}
			if first == nil {
				return fmt.Errorf("complete-book is missing first window %s", ids[0])
			}
		}
		for _, windowID := range ids {
			if seen[windowID] {
				return fmt.Errorf("complete-book repeats a planning window")
			}
			seen[windowID] = true
		}
		if first.FirstProjectedChapter != scope.FirstChapter || first.BaseCanonChapter != scope.FirstChapter-1 {
			return fmt.Errorf("complete-book first arc window omits the original scope")
		}
		child, id, end = first, first.ParentGenerationID, scope.FirstChapter-1
	}
	if id != "" {
		// A canon-zero restart may retain a superseded, unaccepted planning
		// generation as parent. It is not an earlier accepted chapter/window.
		if seen[id] {
			return fmt.Errorf("complete-book canon-zero planning parent points back into accepted windows")
		}
		parent, err := projected.LoadSealedGeneration(id)
		if err != nil {
			return fmt.Errorf("complete-book canon-zero planning parent is not a verified sealed generation: %w", err)
		}
		if parent == nil {
			return fmt.Errorf("complete-book is missing canon-zero planning parent %s", id)
		}
		if parent.BaseCanonChapter != 0 {
			return fmt.Errorf("complete-book canon-zero parent cannot omit earlier accepted canon")
		}
	}
	return nil
}

func verifyPipelineCompleteBookStage(outputDir string, flags pipelineFlags, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	if flags.Start != 0 || flags.End != 0 {
		return evidence, fmt.Errorf("complete-book cannot verify a chapter subset")
	}
	st := store.NewStore(outputDir)
	chapters, progress, err := validatePipelineCompleteBookSources(st)
	if err != nil {
		return evidence, err
	}
	if progress.Phase != domain.PhaseComplete {
		return evidence, fmt.Errorf("complete-book has not closed the final phase")
	}
	if _, err := validatePipelineMergedManuscript(st, outputDir, chapters); err != nil {
		return evidence, err
	}
	evidence.Artifacts = []string{"正文.md", store.OutlineAllExecutionReceiptPath}
	for _, ch := range chapters {
		evidence.Artifacts = append(evidence.Artifacts, fmt.Sprintf("chapters/%02d.md", ch))
		evidence.Artifacts = append(evidence.Artifacts, inspectCurrentChapterReview(outputDir, ch).Artifacts...)
	}
	slices.Sort(evidence.Artifacts)
	evidence.Artifacts = slices.Compact(evidence.Artifacts)
	evidence.Message = fmt.Sprintf("mechanically completed %d accepted chapters; no global LLM review claimed", len(chapters))
	return evidence, nil
}
