package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// pipelineArcScope is the immutable transaction boundary for formal planning.
// outline-all may freeze the whole book, but project/seal/realize are only
// allowed to operate on this one contiguous arc.
type pipelineArcScope struct {
	Volume          int    `json:"volume"`
	Arc             int    `json:"arc"`
	Title           string `json:"title"`
	Goal            string `json:"goal"`
	FirstChapter    int    `json:"first_chapter"`
	LastChapter     int    `json:"last_chapter"`
	BookLastChapter int    `json:"book_last_chapter"`
}

func requirePipelineArcChapterAcceptances(
	outputDir string,
	st *store.Store,
	generation *domain.PlanningGenerationV2,
) error {
	if st == nil || generation == nil {
		return fmt.Errorf("arc acceptance validation requires store and generation")
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("load progress for arc acceptance: %w", err)
	}
	if progress.LatestCompleted() != generation.LastProjectedChapter {
		return fmt.Errorf(
			"arc generation %s canon ends at %d, want %d",
			generation.GenerationID,
			progress.LatestCompleted(),
			generation.LastProjectedChapter,
		)
	}
	for chapter := generation.FirstProjectedChapter; chapter <= generation.LastProjectedChapter; chapter++ {
		if !slices.Contains(progress.CompletedChapters, chapter) {
			return fmt.Errorf("arc generation %s chapter %d is not committed", generation.GenerationID, chapter)
		}
		if err := requirePipelineAcceptedExactReview(outputDir, chapter); err != nil {
			return fmt.Errorf("arc generation %s chapter %d exact-body review: %w", generation.GenerationID, chapter, err)
		}
	}
	if err := st.ArcCycle().ValidateArcCycle(generation.GenerationID); err != nil {
		return fmt.Errorf("arc generation %s immutable chapter acceptance chain: %w", generation.GenerationID, err)
	}
	acceptances, err := st.ArcCycle().ListChapterAcceptanceReceipts(generation.GenerationID)
	if err != nil {
		return err
	}
	if len(acceptances) != generation.ExpectedChapterCount {
		return fmt.Errorf(
			"arc generation %s has %d/%d immutable chapter acceptance receipts",
			generation.GenerationID,
			len(acceptances),
			generation.ExpectedChapterCount,
		)
	}
	return requirePipelineCompletedArcBoundary(st, generation.LastProjectedChapter)
}

func (s pipelineArcScope) Validate() error {
	if s.Volume <= 0 || s.Arc <= 0 {
		return fmt.Errorf("arc scope requires positive volume and arc")
	}
	if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Goal) == "" {
		return fmt.Errorf("V%dA%d arc scope requires title and goal", s.Volume, s.Arc)
	}
	if s.FirstChapter <= 0 || s.LastChapter < s.FirstChapter ||
		s.BookLastChapter < s.LastChapter {
		return fmt.Errorf(
			"V%dA%d arc scope has invalid chapter range %d..%d/book=%d",
			s.Volume,
			s.Arc,
			s.FirstChapter,
			s.LastChapter,
			s.BookLastChapter,
		)
	}
	return nil
}

func locatePipelineArcScope(st *store.Store, chapter int) (pipelineArcScope, error) {
	var result pipelineArcScope
	if st == nil || chapter <= 0 {
		return result, fmt.Errorf("locate arc scope requires store and chapter")
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return result, fmt.Errorf("load layered outline for chapter %d: %w", chapter, err)
	}
	bookLast := domain.TotalChapters(volumes)
	cursor := 1
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			span := arc.ChapterSpan()
			first, last := cursor, cursor+span-1
			cursor += span
			if chapter < first || chapter > last {
				continue
			}
			if !arc.IsExpanded() || len(arc.Chapters) != span {
				return result, fmt.Errorf(
					"chapter %d belongs to V%dA%d, but that arc is not fully expanded",
					chapter,
					volume.Index,
					arc.Index,
				)
			}
			result = pipelineArcScope{
				Volume:          volume.Index,
				Arc:             arc.Index,
				Title:           strings.TrimSpace(arc.Title),
				Goal:            strings.TrimSpace(arc.Goal),
				FirstChapter:    first,
				LastChapter:     last,
				BookLastChapter: bookLast,
			}
			return result, result.Validate()
		}
	}
	return result, fmt.Errorf("chapter %d is outside the frozen layered outline", chapter)
}

func requirePipelineArcStart(st *store.Store, baseChapter int) (pipelineArcScope, error) {
	next := baseChapter + 1
	scope, err := locatePipelineArcScope(st, next)
	if err != nil {
		return scope, err
	}
	if scope.FirstChapter != next {
		return scope, fmt.Errorf(
			"formal arc planning may only start at an arc boundary: canon ends at %d, next chapter %d is inside V%dA%d (%d..%d)",
			baseChapter,
			next,
			scope.Volume,
			scope.Arc,
			scope.FirstChapter,
			scope.LastChapter,
		)
	}
	return scope, nil
}

func requirePipelineCompletedArcBoundary(st *store.Store, chapter int) error {
	if chapter == 0 {
		return nil
	}
	if st == nil {
		return fmt.Errorf("arc boundary validation requires store")
	}
	boundary, err := st.Outline.CheckArcBoundary(chapter)
	if err != nil {
		return err
	}
	if boundary == nil || !boundary.IsArcEnd {
		return fmt.Errorf("chapter %d is not an arc end; next arc planning is locked", chapter)
	}
	return nil
}

func requirePipelinePreviousArcFullyRealized(st *store.Store, baseChapter int) error {
	if st == nil {
		return fmt.Errorf("previous arc realization validation requires store")
	}
	if baseChapter == 0 {
		if active, err := st.ProjectedV2().LoadActiveGeneration(); err != nil {
			return err
		} else if active != nil {
			return fmt.Errorf("chapter-zero planning cannot reuse active generation %s", active.GenerationID)
		}
		return nil
	}
	if err := requirePipelineCompletedArcBoundary(st, baseChapter); err != nil {
		return err
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil {
		return err
	}
	if active == nil {
		return fmt.Errorf("chapter %d is an arc end but has no active sealed arc generation", baseChapter)
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return fmt.Errorf("load previous sealed arc generation: %w", err)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return fmt.Errorf("load previous arc realization cursor: %w", err)
	}
	if generation.LastProjectedChapter != baseChapter ||
		cursor.ActiveGenerationID != generation.GenerationID ||
		cursor.ActivePromotedChapter != 0 ||
		cursor.LastAcceptedChapter != baseChapter ||
		cursor.NextPromoteChapter != baseChapter+1 ||
		strings.TrimSpace(cursor.LastOutcomeReceiptDigest) == "" {
		return fmt.Errorf(
			"previous arc generation %s is not fully realized at chapter %d",
			generation.GenerationID,
			baseChapter,
		)
	}
	completion, err := requirePipelineArcCompletion(st, generation)
	if err != nil {
		return fmt.Errorf("previous arc generation %s has no valid completion proof: %w", generation.GenerationID, err)
	}
	if completion.FinalOutcomeReceiptDigest != cursor.LastOutcomeReceiptDigest {
		return fmt.Errorf("previous arc generation %s completion does not bind the realization cursor", generation.GenerationID)
	}
	return nil
}
