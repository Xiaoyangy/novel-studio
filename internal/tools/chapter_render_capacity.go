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
	return nil
}
