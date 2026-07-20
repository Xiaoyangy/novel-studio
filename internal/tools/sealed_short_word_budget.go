package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// SealedShortChapterWordBounds is the effective exact-body range for the
// current isolated short-fiction render candidate. The range is narrower than
// user_rules.chapter_words when the accepted cumulative prose would otherwise
// drift away from the frozen whole-book word contract.
type SealedShortChapterWordBounds struct {
	Active                bool `json:"active"`
	Chapter               int  `json:"chapter"`
	Min                   int  `json:"min"`
	Max                   int  `json:"max"`
	ChapterMin            int  `json:"chapter_min"`
	ChapterMax            int  `json:"chapter_max"`
	PriorAcceptedChapters int  `json:"prior_accepted_chapters"`
	PriorAcceptedRunes    int  `json:"prior_accepted_runes"`
	BookMin               int  `json:"book_min"`
	BookMax               int  `json:"book_max"`
	TargetWords           int  `json:"target_words"`
	TargetChapters        int  `json:"target_chapters"`
}

// attachSealedShortRenderWordBudget applies the accepted-prose word budget to
// the in-memory copy returned to a frozen Drafter. The published frozen render
// context remains byte-for-byte unchanged: accepted chapter lengths do not
// exist yet when project-all builds every chapter bundle, so this deterministic
// execution overlay has to be derived at render time.
func (t *ContextTool) attachSealedShortRenderWordBudget(
	raw json.RawMessage,
	chapter int,
) (json.RawMessage, error) {
	if t == nil || t.store == nil {
		return raw, nil
	}
	bounds, err := InspectSealedShortChapterWordBounds(t.store, chapter)
	if err != nil {
		return nil, err
	}
	if !bounds.Active {
		return raw, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode frozen render context for sealed short word budget: %w", err)
	}
	packet, ok := sealedShortRenderPacket(payload)
	if !ok {
		return nil, fmt.Errorf(
			"第 %d 章 sealed 短篇冻结正文上下文缺少 render_packet，无法注入动态字数合同: %w",
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	targetMin, targetMax := sealedShortSubmissionTargetRange(bounds.Min, bounds.Max)
	packet["word_budget"] = map[string]any{
		"unit":                  "unicode_characters_including_title",
		"hard_min":              bounds.Min,
		"hard_max":              bounds.Max,
		"submission_target_min": targetMin,
		"submission_target_max": targetMax,
		"exact_boundary":        true,
	}
	return json.Marshal(payload)
}

func sealedShortRenderPacket(payload map[string]any) (map[string]any, bool) {
	if packet, ok := payload["render_packet"].(map[string]any); ok {
		return packet, true
	}
	for _, sectionName := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		section, ok := payload[sectionName].(map[string]any)
		if !ok {
			continue
		}
		if packet, ok := section["render_packet"].(map[string]any); ok {
			return packet, true
		}
	}
	return nil, false
}

// Keep the generation target in the middle-to-upper part of the effective
// interval. This leaves protection against the common under-count while
// retaining one quarter of the interval as upper headroom.
func sealedShortSubmissionTargetRange(minWords, maxWords int) (int, int) {
	if minWords <= 0 || maxWords <= minWords {
		return minWords, maxWords
	}
	span := maxWords - minWords
	targetMin := minWords + (span+1)/2
	targetMax := minWords + (3*span)/4
	if targetMax < targetMin {
		targetMax = targetMin
	}
	return targetMin, targetMax
}

// InspectSealedShortChapterWordBounds activates only for an isolated sealed-v2
// render candidate in short planning mode. It deliberately uses the accepted
// chapter bodies (and verifies their recorded progress counts) rather than the
// plan's intended render capacity, so the commit boundary closes actual prose
// liveness instead of merely validating an estimate.
func InspectSealedShortChapterWordBounds(
	st *store.Store,
	chapter int,
) (SealedShortChapterWordBounds, error) {
	result := SealedShortChapterWordBounds{Chapter: chapter}
	if st == nil || chapter <= 0 {
		return result, nil
	}

	meta, err := st.RunMeta.Load()
	if err != nil {
		return result, fmt.Errorf("load run metadata for sealed short word budget: %w", err)
	}
	if meta == nil || meta.PlanningTier != domain.PlanningTierShort {
		return result, nil
	}

	manifest, err := activeToolRenderCandidateManifest(st, chapter)
	if err != nil {
		return result, fmt.Errorf("validate sealed short render candidate identity: %w", err)
	}
	if manifest == nil {
		return result, nil
	}

	markerRaw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath)))
	if err != nil {
		return result, fmt.Errorf("read sealed short frozen plan marker: %w", err)
	}
	var marker sealedV2FrozenPlanMarker
	if err := json.Unmarshal(markerRaw, &marker); err != nil {
		return result, fmt.Errorf("decode sealed short frozen plan marker: %w", err)
	}
	if marker.Version != "pipeline-planning.v1" ||
		marker.ProjectionBinding != sealedV2ProjectionBinding ||
		marker.Chapter != chapter ||
		marker.PlanDigest != manifest.PlanDigest ||
		marker.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		marker.PlanningGenerationID != manifest.GenerationID ||
		strings.TrimSpace(marker.RenderContextSHA256) == "" ||
		strings.TrimSpace(marker.ProjectedPlanSHA256) == "" ||
		marker.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		marker.PromotionReceiptDigest != manifest.PromotionReceiptDigest {
		return result, fmt.Errorf("sealed short render candidate and frozen plan identity differ: %w", errs.ErrToolPrecondition)
	}
	return inspectShortChapterWordBoundsFromAcceptedProse(st, chapter)
}

// InspectShortChapterWordBoundsFromAcceptedProse is the read-only planning-side
// counterpart of InspectSealedShortChapterWordBounds. It does not require an
// isolated render_candidate manifest, so a sealed convergence controller can
// tell Planner the exact render_capacity interval while it is still operating
// on the live planning tree. Callers remain responsible for proving their own
// sealed planning authority before using the result.
func InspectShortChapterWordBoundsFromAcceptedProse(
	st *store.Store,
	chapter int,
) (SealedShortChapterWordBounds, error) {
	result := SealedShortChapterWordBounds{Chapter: chapter}
	if st == nil || chapter <= 0 {
		return result, nil
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return result, fmt.Errorf("load run metadata for short accepted-prose word budget: %w", err)
	}
	if meta == nil || meta.PlanningTier != domain.PlanningTierShort {
		return result, nil
	}
	return inspectShortChapterWordBoundsFromAcceptedProse(st, chapter)
}

func inspectShortChapterWordBoundsFromAcceptedProse(
	st *store.Store,
	chapter int,
) (SealedShortChapterWordBounds, error) {
	result := SealedShortChapterWordBounds{Chapter: chapter}
	snapshot, err := st.UserRules.Load()
	if err != nil {
		return result, fmt.Errorf("load user_rules.chapter_words for sealed short word budget: %w", err)
	}
	if snapshot == nil || snapshot.Structured.ChapterWords == nil ||
		snapshot.Structured.ChapterWords.Min <= 0 ||
		snapshot.Structured.ChapterWords.Max < snapshot.Structured.ChapterWords.Min {
		return result, fmt.Errorf("sealed short render candidate lacks a valid user_rules.chapter_words range: %w", errs.ErrToolPrecondition)
	}
	rule := snapshot.Structured.ChapterWords

	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return result, fmt.Errorf("load sealed short whole-book word contract: %w", err)
	}
	if receipt == nil {
		return result, fmt.Errorf("sealed short render candidate lacks its outline-all word contract: %w", errs.ErrToolPrecondition)
	}
	target, err := domain.ResolveBookScaleTarget(
		receipt.EstimatedScale,
		receipt.TargetVolumes,
		receipt.TargetChapters,
	)
	if err != nil {
		return result, fmt.Errorf("resolve sealed short whole-book word contract: %w", err)
	}
	if target.MinWords <= 0 || target.MaxWords < target.MinWords ||
		target.TargetWords <= 0 || target.TargetChapters <= 0 ||
		chapter > target.TargetChapters {
		return result, fmt.Errorf("sealed short whole-book word contract is incomplete for chapter %d: %w", chapter, errs.ErrToolPrecondition)
	}

	progress, err := st.Progress.Load()
	if err != nil {
		return result, fmt.Errorf("load accepted prose progress for sealed short word budget: %w", err)
	}
	if progress == nil {
		return result, fmt.Errorf("sealed short render candidate lacks accepted prose progress: %w", errs.ErrToolPrecondition)
	}
	completed := make(map[int]bool, len(progress.CompletedChapters))
	for _, acceptedChapter := range progress.CompletedChapters {
		completed[acceptedChapter] = true
	}

	priorRunes := 0
	for acceptedChapter := 1; acceptedChapter < chapter; acceptedChapter++ {
		if !completed[acceptedChapter] {
			return result, fmt.Errorf(
				"第 %d 章 sealed 短篇累计字数门禁缺少已验收前章 %d: %w",
				chapter,
				acceptedChapter,
				errs.ErrToolPrecondition,
			)
		}
		body, err := st.Drafts.LoadChapterText(acceptedChapter)
		if err != nil {
			return result, fmt.Errorf("load accepted chapter %d body for sealed short word budget: %w", acceptedChapter, err)
		}
		if strings.TrimSpace(body) == "" {
			return result, fmt.Errorf(
				"第 %d 章 sealed 短篇累计字数门禁缺少已验收前章 %d 正文: %w",
				chapter,
				acceptedChapter,
				errs.ErrToolPrecondition,
			)
		}
		actual := utf8.RuneCountInString(body)
		recorded := progress.ChapterWordCounts[acceptedChapter]
		if recorded <= 0 || recorded != actual {
			return result, fmt.Errorf(
				"第 %d 章 sealed 短篇累计字数门禁发现前章 %d 实际/进度字数漂移：正文=%d progress=%d: %w",
				chapter,
				acceptedChapter,
				actual,
				recorded,
				errs.ErrToolPrecondition,
			)
		}
		priorRunes += actual
	}

	minWords, maxWords := projectAllCurrentChapterCapacityBounds(
		rule.Min,
		rule.Max,
		target,
		chapter,
		priorRunes,
	)
	if minWords > maxWords {
		return result, fmt.Errorf(
			"第 %d 章 sealed 短篇累计字数已无可行区间：此前%d章已验收累计%d字，计算区间%d-%d，全书合同%d-%d字: %w",
			chapter,
			chapter-1,
			priorRunes,
			minWords,
			maxWords,
			target.MinWords,
			target.MaxWords,
			errs.ErrToolPrecondition,
		)
	}

	result.Active = true
	result.Min = minWords
	result.Max = maxWords
	result.ChapterMin = rule.Min
	result.ChapterMax = rule.Max
	result.PriorAcceptedChapters = chapter - 1
	result.PriorAcceptedRunes = priorRunes
	result.BookMin = target.MinWords
	result.BookMax = target.MaxWords
	result.TargetWords = target.TargetWords
	result.TargetChapters = target.TargetChapters
	return result, nil
}
