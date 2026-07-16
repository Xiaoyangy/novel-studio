package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// guardPipelinePlanningExecution prevents any planning mutation while a
// render-only invocation is active. The target chapter identifies the prose
// being rendered, but the mode boundary is process-wide: falling through to a
// planner for another chapter would still violate the promise that render
// never plans.
func guardPipelinePlanningExecution(st *store.Store, chapter int, tool string) error {
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil {
		return nil
	}
	if lock.Mode == domain.PipelineExecutionRender {
		return fmt.Errorf(
			"render execution lock 正在只渲染第 %d 章（owner=%s, plan_digest=%s）；%s 试图规划第 %d 章，已拒绝。render 阶段不得推演或改写任何正式 plan: %w",
			lock.TargetChapter,
			lock.Owner,
			lock.PlanDigest,
			tool,
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}

// guardPipelineGlobalPlanningExecution covers planning mutations that are not
// chapter-scoped, such as foundation expansion and off-screen world ticks.
// A render-only lease must reject these too; otherwise an Architect dispatch
// could change the outline/world boundary after the formal plan was frozen.
func guardPipelineGlobalPlanningExecution(st *store.Store, tool string) error {
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionRender {
		return nil
	}
	return fmt.Errorf(
		"render execution lock 正在只渲染第 %d 章（owner=%s, plan_digest=%s）；%s 试图改写全局规划或世界状态，已拒绝。render 阶段不得修改 foundation、弧边界或 world tick: %w",
		lock.TargetChapter,
		lock.Owner,
		lock.PlanDigest,
		tool,
		errs.ErrToolPrecondition,
	)
}

// guardPipelineProseExecution blocks prose mutations during preplanning. In a
// render lease it additionally proves that the exact formal-plan digest still
// matches the digest captured when the lease was acquired.
func guardPipelineProseExecution(st *store.Store, chapter int, tool string) error {
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil {
		return nil
	}
	if lock.Mode == domain.PipelineExecutionPreplan {
		return fmt.Errorf(
			"preplan execution lock 正在推演第 %d 章（owner=%s）；%s 试图改写第 %d 章正文，已拒绝。推演阶段不得生成、编辑、合并或提交任何正文: %w",
			lock.TargetChapter,
			lock.Owner,
			tool,
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	if lock.Mode == domain.PipelineExecutionRender && lock.TargetChapter != chapter {
		return fmt.Errorf(
			"render execution lock 只授权第 %d 章（owner=%s）；%s 试图改写第 %d 章，已拒绝: %w",
			lock.TargetChapter,
			lock.Owner,
			tool,
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	planCheckpoint, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("第 %d 章 render execution lock 无法验证正式 plan: %w", chapter, err)
	}
	if planCheckpoint.Digest != lock.PlanDigest {
		return fmt.Errorf(
			"第 %d 章 render execution lock 的 plan_digest=%s，与当前正式 plan digest=%s 不一致；禁止 %s，必须重新建立 render lock: %w",
			chapter,
			lock.PlanDigest,
			planCheckpoint.Digest,
			tool,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}
