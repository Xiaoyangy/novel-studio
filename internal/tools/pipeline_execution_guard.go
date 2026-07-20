package tools

import (
	"fmt"
	"os"

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
	sealedMode, err := sealedTwoPassModeActive(st)
	if err != nil {
		return fmt.Errorf("%s 读取 writing pipeline mode: %w: %w", tool, err, errs.ErrStoreRead)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil {
		if sealedMode {
			return fmt.Errorf(
				"项目已启用 sealed_two_pass_v2；%s 不得脱离 project-all execution lock 规划第 %d 章: %w",
				tool,
				chapter,
				errs.ErrToolPrecondition,
			)
		}
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, tool); err != nil {
		return err
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
	if lock.Mode == domain.PipelineExecutionFoundation || lock.Mode == domain.PipelineExecutionWorldTick || lock.Mode == domain.PipelineExecutionOutlineAll {
		return fmt.Errorf(
			"%s execution lock 正在准备全书基础（owner=%s）；%s 试图提前规划第 %d 章，已拒绝: %w",
			lock.Mode,
			lock.Owner,
			tool,
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	if sealedMode && lock.Mode != domain.PipelineExecutionProjectAll {
		return fmt.Errorf(
			"项目已启用 sealed_two_pass_v2；%s 只允许在 project-all lock 内规划，当前 mode=%s: %w",
			tool,
			lock.Mode,
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
	if st == nil {
		return nil
	}
	sealedMode, err := sealedTwoPassModeActive(st)
	if err != nil {
		return fmt.Errorf("%s 读取 writing pipeline mode: %w: %w", tool, err, errs.ErrStoreRead)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil {
		if sealedMode {
			return fmt.Errorf(
				"项目已启用 sealed_two_pass_v2；%s 不得绕过 sealed generation 改写全局规划或正史: %w",
				tool,
				errs.ErrToolPrecondition,
			)
		}
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, tool); err != nil {
		return err
	}
	switch lock.Mode {
	case domain.PipelineExecutionFoundation:
		switch tool {
		case "save_foundation", "save_world_tick", "save_user_rules":
			return nil
		default:
			return fmt.Errorf(
				"foundation execution lock 只允许基础设定工具；%s 不得借此改写 progress、章节摘要或既有正史: %w",
				tool,
				errs.ErrToolPrecondition,
			)
		}
	case domain.PipelineExecutionWorldTick:
		if tool == "save_world_tick" {
			return nil
		}
		return fmt.Errorf(
			"world_tick execution lock 只允许 save_world_tick；%s 不得修改 user rules、foundation、progress、摘要或正文: %w",
			tool,
			errs.ErrToolPrecondition,
		)
	case domain.PipelineExecutionOutlineAll:
		if tool == "save_foundation" {
			return nil
		}
		return fmt.Errorf(
			"outline_all execution lock 只允许 receipt pending_action 精确授权的 save_foundation mutation；%s 不得修改 world_tick、user rules、progress、摘要或正文: %w",
			tool,
			errs.ErrToolPrecondition,
		)
	case domain.PipelineExecutionRender:
		return fmt.Errorf(
			"render execution lock 正在只渲染第 %d 章（owner=%s, plan_digest=%s）；%s 试图改写全局配置、规划或世界状态，已拒绝。render 阶段不得修改 user rules、progress、foundation、弧边界或 world tick: %w",
			lock.TargetChapter,
			lock.Owner,
			lock.PlanDigest,
			tool,
			errs.ErrToolPrecondition,
		)
	case domain.PipelineExecutionPreplan, domain.PipelineExecutionProjectAll:
		return fmt.Errorf(
			"planning execution lock 正在只推演第 %d 章（owner=%s）；%s 试图改写全局配置或正史状态，已拒绝。preplan/plan 阶段不得修改 user rules、progress、foundation、弧边界或 world tick: %w",
			lock.TargetChapter,
			lock.Owner,
			tool,
			errs.ErrToolPrecondition,
		)
	default:
		return nil
	}
}

// guardPipelineProseExecution blocks prose mutations during preplanning. In a
// render lease it additionally proves that the exact formal-plan digest still
// matches the digest captured when the lease was acquired.
func guardPipelineProseExecution(st *store.Store, chapter int, tool string) error {
	sealedMode, err := sealedTwoPassModeActive(st)
	if err != nil {
		return fmt.Errorf("%s 读取 writing pipeline mode: %w: %w", tool, err, errs.ErrStoreRead)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("%s 读取 pipeline execution lock: %w: %w", tool, err, errs.ErrStoreRead)
	}
	if lock == nil {
		if sealedMode {
			return fmt.Errorf(
				"项目已启用 sealed_two_pass_v2；%s 不得脱离 promote/render execution lock 改写第 %d 章正文: %w",
				tool,
				chapter,
				errs.ErrToolPrecondition,
			)
		}
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, tool); err != nil {
		return err
	}
	if lock.Mode == domain.PipelineExecutionFoundation || lock.Mode == domain.PipelineExecutionWorldTick || lock.Mode == domain.PipelineExecutionOutlineAll {
		return fmt.Errorf(
			"%s execution lock 正在准备第 %d 章之前的全书基础（owner=%s）；%s 试图改写第 %d 章正文，已拒绝。基础阶段不得生成、编辑、合并或提交任何正文: %w",
			lock.Mode,
			lock.TargetChapter,
			lock.Owner,
			tool,
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	if lock.Mode == domain.PipelineExecutionPreplan || lock.Mode == domain.PipelineExecutionProjectAll {
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

func requireCurrentPipelineExecutionProcess(
	lock *domain.PipelineExecutionLock,
	tool string,
) error {
	if lock == nil {
		return nil
	}
	if lock.ProcessID != os.Getpid() {
		return fmt.Errorf(
			"pipeline execution lock 属于另一个进程（owner=%s pid=%d current_pid=%d）；%s 不得借用该执行能力: %w",
			lock.Owner,
			lock.ProcessID,
			os.Getpid(),
			tool,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}

func sealedTwoPassModeActive(st *store.Store) (bool, error) {
	if st == nil {
		return false, nil
	}
	receipt, err := st.LoadWritingPipelineMode()
	if err != nil || receipt == nil {
		return false, err
	}
	return receipt.Mode == domain.WritingPipelineModeSealedTwoPassV2, nil
}
