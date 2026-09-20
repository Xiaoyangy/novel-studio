package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Only host-frozen whole-book contracts add this mechanical terminal gate.
// Legacy open-ended projects keep their existing completion/review behavior.
// This proves delivered scope and current body reviews, not narrative closure.
func requireFrozenBookCompletion(st *store.Store, progress *domain.Progress, requestedScale ...domain.PlanningTier) error {
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return fmt.Errorf("load frozen whole-book completion contract: %w", err)
	}
	if receipt == nil {
		return nil
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return fmt.Errorf("load book completion review policy: %w", err)
	}
	if len(requestedScale) > 0 && requestedScale[0] != "" && (meta == nil || meta.PlanningTier != requestedScale[0]) {
		return fmt.Errorf("全书完结不能更改已冻结项目的审阅级别: %w", errs.ErrToolPrecondition)
	}
	if receipt.Status != domain.OutlineAllExecutionComplete {
		return fmt.Errorf("全书规划合同尚未完成，不能宣告全书完结: %w", errs.ErrToolPrecondition)
	}
	if progress == nil || len(progress.CompletedChapters) != receipt.TargetChapters {
		return fmt.Errorf("全书冻结合同要求完整交付第 1-%d 章，当前已完成章数不足或越界: %w", receipt.TargetChapters, errs.ErrToolPrecondition)
	}
	seen := make(map[int]bool, len(progress.CompletedChapters))
	for _, chapter := range progress.CompletedChapters {
		if chapter < 1 || chapter > receipt.TargetChapters || seen[chapter] {
			return fmt.Errorf("全书已完成章序含重复或越界章节 %d，须精确覆盖第 1-%d 章: %w", chapter, receipt.TargetChapters, errs.ErrToolPrecondition)
		}
		seen[chapter] = true
	}
	scale, err := domain.ResolveBookScaleTarget(receipt.EstimatedScale, receipt.TargetVolumes, receipt.TargetChapters)
	if err != nil {
		return fmt.Errorf("resolve frozen whole-book completion bounds: %w", err)
	}
	actualWords := 0
	for chapter := 1; chapter <= receipt.TargetChapters; chapter++ {
		if !seen[chapter] {
			return fmt.Errorf("全书冻结合同缺少已完成章节 %d: %w", chapter, errs.ErrToolPrecondition)
		}
		body, err := st.Drafts.LoadChapterText(chapter)
		if err != nil {
			return fmt.Errorf("read final chapter %d for book completion: %w", chapter, err)
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("第 %d 章缺少实际终稿正文，不能宣告全书完结: %w", chapter, errs.ErrToolPrecondition)
		}
		if !st.World.HasAcceptedChapterReview(chapter) {
			return fmt.Errorf("第 %d 章当前正文未通过章级审阅，不能宣告全书完结: %w", chapter, errs.ErrToolPrecondition)
		}
		actualWords += domain.WordCount(body)
	}
	// TargetWords is a planning midpoint, not a newly invented exact limit.
	// Only the explicit frozen whole-book range constrains delivered length.
	if scale.MinWords > 0 && (actualWords < scale.MinWords || actualWords > scale.MaxWords) {
		return fmt.Errorf("全书实际终稿累计 %d 字，不符合冻结合同 %d-%d 字，不能宣告全书完结: %w", actualWords, scale.MinWords, scale.MaxWords, errs.ErrToolPrecondition)
	}
	actualProgress := *progress
	actualProgress.TotalWordCount = actualWords
	if domain.RequiresFinalGlobalReview(&actualProgress, meta) && !st.World.HasAcceptedGlobalReview(receipt.TargetChapters) {
		return fmt.Errorf("当前全文尚未通过要求的 global 终审，不能宣告全书完结: %w", errs.ErrToolPrecondition)
	}
	return nil
}
