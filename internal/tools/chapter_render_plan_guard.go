package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// currentChapterRenderPlanGuard is the shared write-side plan gate for whole
// drafts, draft parts, local edits, merges and rewrite commit. It closes the
// gaps where a tool could otherwise write first and discover a stale world,
// quantity, attraction or craft receipt only at a later stage.
type currentChapterRenderPlanGuard struct {
	Plan       *domain.ChapterPlan
	IsRewrite  bool
	RenderOnly bool
}

func validateCurrentChapterRenderPlan(st *store.Store, chapter int) (currentChapterRenderPlanGuard, error) {
	guard := currentChapterRenderPlanGuard{}
	if st == nil || chapter <= 0 {
		return guard, fmt.Errorf("invalid chapter %d: %w", chapter, errs.ErrToolArgs)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return guard, fmt.Errorf("load progress for chapter plan gate: %w: %w", errs.ErrStoreRead, err)
	}
	guard.IsRewrite = progress != nil && slices.Contains(progress.PendingRewrites, chapter)
	guard.RenderOnly = guard.IsRewrite && RenderOnlyRerenderReady(st, chapter)
	if partial, err := st.Drafts.LoadChapterPlanPartial(chapter); err != nil {
		return guard, fmt.Errorf("load staged chapter plan: %w: %w", errs.ErrStoreRead, err)
	} else if partial != nil {
		return guard, fmt.Errorf("第 %d 章仍有 plan.partial，staged plan 是当前唯一真相；必须先完成 plan_details/finalize，禁止沿旧正式 plan 写入、编辑、合并或提交正文: %w",
			chapter, errs.ErrToolPrecondition)
	}
	if partial, err := st.LoadChapterWorldSimulationPartial(chapter); err != nil {
		return guard, fmt.Errorf("load staged world simulation: %w: %w", errs.ErrStoreRead, err)
	} else if partial != nil {
		return guard, fmt.Errorf("第 %d 章仍有 chapter_world_simulation partial；必须先完成当前世界推演，禁止沿旧正式 simulation/plan 写入、编辑、合并或提交正文: %w",
			chapter, errs.ErrToolPrecondition)
	}

	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return guard, fmt.Errorf("load chapter plan: %w: %w", errs.ErrStoreRead, err)
	}
	guard.Plan = plan
	// Any pipeline-managed writing phase is strictly plan-first. Projects outside
	// the pipeline keep the legacy/import compatibility path; causal rewrites and
	// review-derived craft repairs remain strict regardless of that marker.
	requirePlan := pipelineWritingManaged(st)
	requirePlan = requirePlan || (guard.IsRewrite && activeRewriteCraftReceiptRequired(st, chapter))
	if plan == nil {
		if requirePlan {
			return guard, fmt.Errorf("第 %d 章处于 pipeline 写作/返工阶段，必须先完成当前 world simulation 与正式 chapter plan，不能在缺少计划时写入、编辑、合并或提交正文: %w",
				chapter, errs.ErrToolPrecondition)
		}
		return guard, nil
	}
	latestPlanCheckpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "plan")
	if requirePlan || latestPlanCheckpoint != nil {
		if _, err := CurrentChapterPlanCausalCheckpoint(st, chapter); err != nil {
			return guard, fmt.Errorf("第 %d 章正式 plan 未与当前 finalize checkpoint 强绑定，禁止写入、编辑、合并或提交正文: %w",
				chapter, err)
		}
	}
	if err := ValidateRAGFactPlanCurrent(st, *plan); err != nil {
		return guard, fmt.Errorf("第 %d 章正文写入前普通事实 RAG receipt 复验失败：%w", chapter, err)
	}
	if guard.RenderOnly {
		if err := ValidateReusableCausalPlanForRerender(st, chapter); err != nil {
			return guard, fmt.Errorf("第 %d 章显式 render-only 复用门禁失败: %w", chapter, err)
		}
	} else if err := validateChapterPrewriteSimulation(st, *plan, guard.IsRewrite); err != nil {
		return guard, err
	}
	if guard.IsRewrite {
		if err := validateRewriteCraftConsumption(st, *plan); err != nil {
			return guard, fmt.Errorf("第 %d 章正文写入前 craft receipt 复验失败：%w", chapter, err)
		}
	}
	return guard, nil
}

// ValidateCurrentChapterRenderPlanForExecution exposes the complete render
// freshness gate to outer pipeline orchestration without exposing its internal
// receipt bundle. Split plan/render mode must freeze only a plan that has
// consumed the current simulation, rewrite source, selected RAG facts and
// chapter-scoped constraints.
func ValidateCurrentChapterRenderPlanForExecution(st *store.Store, chapter int) error {
	_, err := validateCurrentChapterRenderPlan(st, chapter)
	return err
}

func pipelineWritingManaged(st *store.Store) bool {
	if st == nil {
		return false
	}
	_, err := os.Stat(filepath.Join(st.Dir(), "meta", "pipeline.json"))
	return err == nil
}

// validateCurrentPlanBodyEpoch proves that prose being edited or committed was
// produced after the latest finalized plan (or explicit rerender request).
// Re-running consistency over an old draft cannot advance this causal epoch.
func validateCurrentPlanBodyEpoch(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("invalid chapter %d: %w", chapter, errs.ErrToolArgs)
	}
	formalPlan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return fmt.Errorf("load chapter plan for body epoch: %w: %w", errs.ErrStoreRead, err)
	}
	scope := domain.ChapterScope(chapter)
	latestBodyEvent := int64(0)
	for _, step := range []string{"draft", "edit"} {
		if cp := st.Checkpoints.LatestByStep(scope, step); cp != nil && cp.Seq > latestBodyEvent {
			latestBodyEvent = cp.Seq
		}
	}
	strict := pipelineWritingManaged(st) || activeRewriteCraftReceiptRequired(st, chapter)
	boundary := int64(0)
	if formalPlan != nil {
		latestPlanCheckpoint := st.Checkpoints.LatestByStep(scope, "plan")
		if latestPlanCheckpoint == nil && !strict {
			return nil // legacy/import formal plan predating checkpoint journaling.
		}
		plan, planErr := CurrentChapterPlanCheckpoint(st, chapter)
		if planErr != nil {
			return fmt.Errorf("第 %d 章正文 epoch 无法绑定当前正式 plan: %w", chapter, planErr)
		}
		boundary = plan.Seq
		if request := st.Checkpoints.LatestByStep(scope, "rerender-request"); request != nil && request.Seq > boundary {
			boundary = request.Seq
		}
	}
	if latestBodyEvent == 0 && !strict {
		return nil // legacy/import prose predating checkpoint journaling.
	}
	body, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("第 %d 章正文 epoch 无法绑定当前 draft artifact: %w", chapter, err)
	}
	if formalPlan == nil {
		return nil // exact current body is enough for a plan-less legacy/import.
	}
	if body.Seq <= boundary {
		return fmt.Errorf("第 %d 章当前草稿属于旧 plan/rerender epoch（latest_body_seq=%d, required_after=%d）；必须按当前正式 plan 重新渲染正文，不能仅重跑 consistency 后提交: %w",
			chapter, body.Seq, boundary, errs.ErrToolPrecondition)
	}
	return nil
}

func validateAppendBaseCurrentPlanEpoch(st *store.Store, chapter int) error {
	draft, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load append base draft: %w: %w", errs.ErrStoreRead, err)
	}
	if len(draft) == 0 {
		return nil
	}
	if err := validateCurrentPlanBodyEpoch(st, chapter); err != nil {
		return fmt.Errorf("第 %d 章 append 的现有草稿属于旧 plan/rerender epoch；必须用 mode=write 按当前计划整章覆盖，不能靠追加少量新文本刷新 epoch: %w", chapter, err)
	}
	return nil
}

func validateDraftPartsPlanEpoch(st *store.Store, chapter int, index *domain.ChapterDraftPartIndex) error {
	if st == nil || index == nil || len(index.Parts) == 0 {
		return nil
	}
	formalPlan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return fmt.Errorf("load chapter plan for draft-part epoch: %w: %w", errs.ErrStoreRead, err)
	}
	if formalPlan == nil {
		return nil
	}
	scope := domain.ChapterScope(chapter)
	latestPlanCheckpoint := st.Checkpoints.LatestByStep(scope, "plan")
	strict := pipelineWritingManaged(st) || activeRewriteCraftReceiptRequired(st, chapter)
	if latestPlanCheckpoint == nil && !strict {
		return nil // legacy/import formal plan predating checkpoint journaling.
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return fmt.Errorf("第 %d 章分片 epoch 无法绑定当前正式 plan: %w", chapter, err)
	}
	boundary := plan.Seq
	if request := st.Checkpoints.LatestByStep(scope, "rerender-request"); request != nil && request.Seq > boundary {
		boundary = request.Seq
	}
	latestByArtifact := map[string]domain.Checkpoint{}
	for _, cp := range st.Checkpoints.All() {
		if !cp.Scope.Matches(scope) || cp.Step != "draft_part" || cp.Seq <= latestByArtifact[cp.Artifact].Seq {
			continue
		}
		latestByArtifact[cp.Artifact] = cp
	}
	for _, part := range index.Parts {
		cp := latestByArtifact[part.ContentPath]
		if cp.Seq <= boundary {
			return fmt.Errorf("第 %d 章分片 %d 属于旧 plan/rerender epoch（part_seq=%d, required_after=%d）；请按当前 plan 重写该分片后再合并: %w",
				chapter, part.Part, cp.Seq, boundary, errs.ErrToolPrecondition)
		}
		raw, readErr := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(part.ContentPath)))
		if readErr != nil {
			return fmt.Errorf("读取第 %d 章分片 %d artifact %s 失败: %w: %w", chapter, part.Part, part.ContentPath, errs.ErrStoreRead, readErr)
		}
		sum := sha256.Sum256(raw)
		wantDigest := fmt.Sprintf("sha256:%x", sum)
		if cp.Artifact != part.ContentPath || cp.Digest != wantDigest {
			return fmt.Errorf("第 %d 章分片 %d 与当前 draft_part checkpoint 不匹配（artifact=%q, checkpoint=%s, current=%s）；可能是分片写入后 checkpoint 追加失败: %w",
				chapter, part.Part, cp.Artifact, cp.Digest, wantDigest, errs.ErrToolPrecondition)
		}
	}
	return nil
}
