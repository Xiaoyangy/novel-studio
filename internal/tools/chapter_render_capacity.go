package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// validateProjectAllRenderCapacity is deliberately scoped to the formal
// projection pass. Legacy/manual plans remain readable, while every chapter
// sealed by project-all must prove that its scene spine can carry the exact
// user-configured prose range without filler.
func validateProjectAllRenderCapacity(s *store.Store, plan domain.ChapterPlan) error {
	if s == nil {
		return nil
	}
	lock, err := s.Runtime.LoadPipelineExecution()
	if err != nil {
		return err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll {
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "plan render_capacity finalize"); err != nil {
		return err
	}

	snapshot, err := s.UserRules.Load()
	if err != nil {
		return fmt.Errorf("第 %d 章读取 user_rules.chapter_words 失败: %w", plan.Chapter, err)
	}
	if snapshot == nil || snapshot.Structured.ChapterWords == nil {
		return fmt.Errorf(
			"第 %d 章 project-all 缺少 user_rules.chapter_words，无法验证 render_capacity 总预算: %w",
			plan.Chapter,
			errs.ErrToolPrecondition,
		)
	}
	rangeRule := snapshot.Structured.ChapterWords
	if plan.CausalSimulation.RenderCapacity == nil {
		return fmt.Errorf(
			"第 %d 章 project-all 缺少 causal_simulation.render_capacity；必须提供 3-6 个可写场景单元与反注水策略，并让总 target_runes 落在 user_rules.chapter_words=%d-%d: %w",
			plan.Chapter,
			rangeRule.Min,
			rangeRule.Max,
			errs.ErrToolPrecondition,
		)
	}
	if err := plan.CausalSimulation.RenderCapacity.Validate(rangeRule.Min, rangeRule.Max); err != nil {
		return fmt.Errorf(
			"第 %d 章 project-all render_capacity 不可封存：%v: %w",
			plan.Chapter,
			err,
			errs.ErrToolPrecondition,
		)
	}
	if err := validateProjectAllBookRenderBudget(s, plan, rangeRule.Min, rangeRule.Max); err != nil {
		return err
	}
	return nil
}

func validateProjectAllBookRenderBudget(
	s *store.Store,
	plan domain.ChapterPlan,
	chapterMin, chapterMax int,
) error {
	receipt, err := s.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil || receipt.TargetWords <= 0 || receipt.TargetChapters <= 0 {
		return err
	}
	target, err := domain.ResolveBookScaleTarget(
		receipt.EstimatedScale,
		receipt.TargetVolumes,
		receipt.TargetChapters,
	)
	if err != nil {
		return fmt.Errorf("第 %d 章解析 outline-all 全书字数合同失败: %w", plan.Chapter, err)
	}
	if target.MinWords <= 0 || target.MaxWords <= 0 || plan.Chapter > target.TargetChapters {
		return nil
	}
	priorRunes := 0
	progress, progressErr := s.Progress.Load()
	if progressErr != nil {
		return progressErr
	}
	for chapter := 1; chapter < plan.Chapter; chapter++ {
		if progress != nil && progress.ChapterWordCounts[chapter] > 0 {
			priorRunes += progress.ChapterWordCounts[chapter]
			continue
		}
		priorPlan, loadErr := s.Drafts.LoadChapterPlan(chapter)
		if loadErr != nil {
			return loadErr
		}
		if priorPlan == nil || priorPlan.CausalSimulation.RenderCapacity == nil {
			return fmt.Errorf(
				"第 %d 章全书字数预算缺少前章 %d 的 accepted body 或正式 render_capacity: %w",
				plan.Chapter,
				chapter,
				errs.ErrToolPrecondition,
			)
		}
		priorRunes += priorPlan.CausalSimulation.RenderCapacity.TotalTargetRunes
	}
	allowedMin, allowedMax := projectAllCurrentChapterCapacityBounds(
		chapterMin,
		chapterMax,
		target,
		plan.Chapter,
		priorRunes,
	)
	current := plan.CausalSimulation.RenderCapacity.TotalTargetRunes
	if allowedMin > allowedMax || current < allowedMin || current > allowedMax {
		return fmt.Errorf(
			"第 %d 章 project-all render_capacity=%d 破坏全书%d-%d字预算；此前累计%d，本章必须落在%d-%d，目标总量%d（约%d/章）: %w",
			plan.Chapter,
			current,
			target.MinWords,
			target.MaxWords,
			priorRunes,
			allowedMin,
			allowedMax,
			target.TargetWords,
			target.TargetWordsPerChapter,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}

func projectAllCurrentChapterCapacityBounds(
	chapterMin, chapterMax int,
	target domain.BookScaleTarget,
	chapter, priorRunes int,
) (int, int) {
	remaining := target.TargetChapters - chapter
	minAllowed := chapterMin
	maxAllowed := chapterMax
	if feasibleMin := target.MinWords - priorRunes - remaining*chapterMax; feasibleMin > minAllowed {
		minAllowed = feasibleMin
	}
	if feasibleMax := target.MaxWords - priorRunes - remaining*chapterMin; feasibleMax < maxAllowed {
		maxAllowed = feasibleMax
	}
	// Short fiction benefits from a cumulative midpoint envelope: it keeps the
	// opening from spending the whole budget and forcing visibly starved final
	// chapters. Longer books retain only the feasibility guard above so natural
	// chapter variation can average out over many arcs.
	if target.TargetChapters <= 32 && target.TargetWords > 0 {
		targetCumulative := (target.TargetWords*chapter + target.TargetChapters/2) /
			target.TargetChapters
		tolerance := target.TargetWordsPerChapter / 10
		if tolerance < 100 {
			tolerance = 100
		}
		if centeredMin := targetCumulative - tolerance - priorRunes; centeredMin > minAllowed {
			minAllowed = centeredMin
		}
		if centeredMax := targetCumulative + tolerance - priorRunes; centeredMax < maxAllowed {
			maxAllowed = centeredMax
		}
	}
	return minAllowed, maxAllowed
}
