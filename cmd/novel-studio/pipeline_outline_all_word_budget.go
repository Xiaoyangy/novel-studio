package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var (
	pipelineWordScaleClausesRE     = regexp.MustCompile(`[，,；;。.!！?？\n\r]`)
	pipelineWordScaleUnitRE        = regexp.MustCompile(`(?i)([0-9一二三四五六七八九十百千万两零〇点]\s*(中文字|汉字|字)|\bwords?\b|总字数|全书字数|全文字数)`)
	pipelineChapterWordHintRE      = regexp.MustCompile(`(?i)(单章|每章|章均|每章节|章节字数|章节预算|章预算|per[- ]chapter|each chapter|[/／]\s*(章|chapter))`)
	pipelineExplicitBookWordHintRE = regexp.MustCompile(`(?i)(全书|全文|正文|总字数|全书字数|全文字数|目标字数|总篇幅|total words?|total word count)[约共为是需:：\s]*[0-9一二三四五六七八九十百千万两零〇点.]+\s*(中文字|汉字|字|words?)`)
)

// preparePipelineOutlineAllShortWordContract runs before the source snapshot
// and immutable outline-all receipt are created. A fixed chapter count plus
// the authoritative per-chapter range already defines a whole-book contract;
// persist that derivation once so planning, sealed rendering and delivery all
// resolve the same bounds. Published generations never reach this entrypoint.
func preparePipelineOutlineAllShortWordContract(
	st *store.Store,
	compass *domain.StoryCompass,
	target domain.BookScaleTarget,
) (domain.BookScaleTarget, bool, error) {
	meta, err := st.RunMeta.Load()
	if err != nil {
		return target, false, fmt.Errorf("outline-all load planning tier for word contract: %w", err)
	}
	if meta == nil || meta.PlanningTier != domain.PlanningTierShort {
		return target, false, nil
	}
	snapshot, err := st.UserRules.Load()
	if err != nil {
		return target, false, fmt.Errorf("outline-all load short chapter word contract: %w", err)
	}
	if snapshot == nil || snapshot.Status != rules.StatusReady || snapshot.Structured.ChapterWords == nil {
		return target, false, fmt.Errorf("outline-all short fiction requires ready user_rules.chapter_words before freezing its word contract")
	}
	chapterWords := snapshot.Structured.ChapterWords
	if chapterWords.Min <= 0 || chapterWords.Max < chapterWords.Min || target.TargetChapters <= 0 {
		return target, false, fmt.Errorf("outline-all short fiction has invalid chapter word bounds or chapter count")
	}
	if chapterWords.Max > int(^uint(0)>>1)/target.TargetChapters {
		return target, false, fmt.Errorf("outline-all short whole-book word contract overflows the supported integer range")
	}
	possibleMin := target.TargetChapters * chapterWords.Min
	possibleMax := target.TargetChapters * chapterWords.Max
	if target.MinWords > 0 && target.MaxWords >= target.MinWords {
		if target.MaxWords < possibleMin || target.MinWords > possibleMax {
			return target, false, fmt.Errorf(
				"outline-all explicit whole-book word range %d-%d conflicts with %d chapters × chapter_words %d-%d (feasible total %d-%d)",
				target.MinWords, target.MaxWords, target.TargetChapters, chapterWords.Min, chapterWords.Max, possibleMin, possibleMax,
			)
		}
		return target, false, nil
	}
	if target.Range.MinChapters != target.Range.MaxChapters {
		return target, false, fmt.Errorf("outline-all short fiction needs a fixed chapter count or an explicit whole-book word range before freezing")
	}
	if pipelineUnresolvedWholeBookWordHint(compass.EstimatedScale) {
		return target, false, fmt.Errorf("outline-all short estimated_scale contains an unparsed whole-book word constraint; express it as an explicit min-max word range")
	}
	normalized := *compass
	normalized.EstimatedScale = strings.TrimSpace(compass.EstimatedScale) +
		fmt.Sprintf("；全书%d-%d字", possibleMin, possibleMax)
	resolved, err := domain.ResolveBookScaleTarget(normalized.EstimatedScale, target.TargetVolumes, target.TargetChapters)
	if err != nil {
		return target, false, fmt.Errorf("outline-all derive short whole-book word contract: %w", err)
	}
	if resolved.MinWords != possibleMin || resolved.MaxWords != possibleMax || resolved.TargetChapters != target.TargetChapters {
		return target, false, fmt.Errorf("outline-all derived short whole-book word contract did not round-trip exactly")
	}
	if err := st.Outline.SaveCompass(normalized); err != nil {
		return target, false, fmt.Errorf("outline-all persist short whole-book word contract: %w", err)
	}
	*compass = normalized
	return resolved, true, nil
}

// Never replace an explicit-but-unparsed total (for example “全书7000字”)
// with an inferred range. Per-chapter annotations are safe to retain because
// the persisted structured rule, not prose parsing here, supplies the bounds.
func pipelineUnresolvedWholeBookWordHint(scale string) bool {
	// A per-chapter clause may also contain a separate total without a comma.
	// Do not let its chapter marker hide that explicit total.
	if pipelineExplicitBookWordHintRE.MatchString(scale) {
		return true
	}
	for _, clause := range pipelineWordScaleClausesRE.Split(scale, -1) {
		if pipelineWordScaleUnitRE.MatchString(clause) && !pipelineChapterWordHintRE.MatchString(clause) {
			return true
		}
	}
	return false
}
