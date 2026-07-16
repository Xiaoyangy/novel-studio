package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
	agentcoretools "github.com/voocel/agentcore/tools"
)

// EditChapterTool 对章节草稿做定点字符串替换，适用于打磨场景。
// 相比 draft_chapter 整章重写，token 节省 10x+。
//
// 落盘契约：只改 drafts/{ch:02d}.draft.md，禁止直接改 chapters/（终稿由 commit_chapter 独占）。
// Seed 语义：drafts 不存在但 chapters 有 → 自动把 chapters 复制到 drafts 作为起点。
// 归属检查：章节已完成时必须在 PendingRewrites 队列中，否则拒绝。
//
// 本工具是 agentcore.EditTool 的薄封装，找-换逻辑（多级容错匹配、diff 输出、行尾/BOM 保留）
// 全部复用上游实现。
type EditChapterTool struct {
	store *store.Store

	// afterDraftWrite is nil in production. Tests use it to simulate a process
	// exit after the atomic Markdown replace but before the edit checkpoint, so
	// the write-intent recovery path is exercised at the real crash boundary.
	afterDraftWrite func() error
}

func NewEditChapterTool(s *store.Store) *EditChapterTool {
	return &EditChapterTool{store: s}
}

func (t *EditChapterTool) Name() string  { return "edit_chapter" }
func (t *EditChapterTool) Label() string { return "编辑章节" }

// ReadOnly 明确声明写工具（配合 ConcurrencySafeTool 防止被并发调度）。
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe 显式禁止并发：同章节多次 edit_chapter 并行会读-改-写竞态，
// 即使不同章节并行也会穿插 checkpoint 顺序。统一串行最稳。
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription 供 UI/日志展示当前工具的活动描述。
func (t *EditChapterTool) ActivityDescription(_ json.RawMessage) string { return "编辑章节草稿" }

func (t *EditChapterTool) Description() string {
	return "对章节草稿做定点字符串替换（打磨场景首选，比 draft_chapter 整章重写省 token）。" +
		"找到 old_string 并替换为 new_string，要求精确匹配且唯一（多处匹配需 replace_all=true）。" +
		"写入 drafts/{ch}.draft.md；drafts 不存在时自动从 chapters 播种。" +
		"章节已完成且不在 PendingRewrites 队列中时拒绝执行。若外部草稿审核要求结构级重渲染，本工具会拒绝，必须先用 draft_chapter(mode=write) 整章覆盖。若仅 DeepSeek 当前哈希已通过，或仍有本地非结构性门禁，本工具只允许改一处，落盘后必须立即停笔并复判新哈希；若当前精确哈希已通过 requirement 的全部注册 detector/mode，则正文冻结，本工具拒绝修改。"
}

func (t *EditChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("old_string", schema.String("要替换的原文精确片段，多行需包含换行；不加 replace_all 时必须在草稿中唯一出现")).Required(),
		schema.Property("new_string", schema.String("替换后的新文本")).Required(),
		schema.Property("replace_all", schema.Bool("替换所有匹配（默认 false）")),
	)
}

func (t *EditChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.OldString == "" {
		return nil, fmt.Errorf("old_string 不能为空: %w", errs.ErrToolArgs)
	}
	if a.OldString == a.NewString {
		return nil, fmt.Errorf("old_string 与 new_string 相同，无需修改: %w", errs.ErrToolArgs)
	}
	externalGateBefore, err := InspectDraftExternalGateWithStore(t.store, a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("读取草稿外审门禁: %w: %w", err, errs.ErrStoreRead)
	}
	if err := draftExternalGateEditPrecondition(a.Chapter, externalGateBefore); err != nil {
		return nil, err
	}
	if escalation := InspectRenderOnlyReplanEscalation(t.store, a.Chapter); escalation.Required {
		return nil, fmt.Errorf("第 %d 章同一因果计划下的整章结构失败已达到上限：%s；禁止用 edit_chapter 绕过重规划，必须先重新完成 chapter_world_simulation 与 POV plan: %w",
			a.Chapter, escalation.Reason, errs.ErrToolPrecondition)
	}

	// 归属检查：已完成章节必须在重写队列中，避免污染终稿
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		progress, _ := t.store.Progress.Load()
		if progress == nil || !slices.Contains(progress.PendingRewrites, a.Chapter) {
			return nil, fmt.Errorf("第 %d 章已完成且不在 PendingRewrites 队列中，不能编辑；需修改请先由 editor 评审触发重写/打磨: %w", a.Chapter, errs.ErrToolPrecondition)
		}
	}
	if _, err := validateCurrentChapterRenderPlan(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if err := validateCurrentPlanBodyEpoch(t.store, a.Chapter); err != nil {
		return nil, err
	}

	prior, base, err := t.loadEditBase(a.Chapter)
	if err != nil {
		return nil, err
	}

	// 先在隔离副本上复用 agentcore.EditTool 的完整匹配语义，得到精确候选
	// bytes；随后才能在真实文件写入前持久化 candidate hash intent。
	subArgs, _ := json.Marshal(map[string]any{
		"path":        fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"file_path":   fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"old_text":    a.OldString,
		"old_string":  a.OldString,
		"new_text":    a.NewString,
		"new_string":  a.NewString,
		"replace_all": a.ReplaceAll,
	})
	candidate, result, err := prepareChapterEditCandidate(ctx, a.Chapter, base, subArgs)
	if err != nil {
		return nil, fmt.Errorf("apply edit: %w: %w", errs.ErrToolPrecondition, err)
	}
	if err := validateFictionProseTypography(candidate); err != nil {
		return nil, fmt.Errorf("edited draft content is not valid chapter prose: %w", err)
	}
	if err := requireDraftHardFactAnchors(t.store, a.Chapter, candidate); err != nil {
		return nil, fmt.Errorf("第 %d 章 edit_chapter 候选未通过 hard-fact anchor 门禁，真实草稿与 checkpoint 均未改变: %w", a.Chapter, err)
	}
	if err := beginDraftWriteIntent(t.store, a.Chapter, prior, candidate, "edit", nil); err != nil {
		return nil, fmt.Errorf("begin edit write: %w: %w", err, errs.ErrStoreWrite)
	}
	if err := t.store.Drafts.SaveDraft(a.Chapter, candidate); err != nil {
		return nil, fmt.Errorf("save edited draft: %w: %w", err, errs.ErrStoreWrite)
	}
	if t.afterDraftWrite != nil {
		if err := t.afterDraftWrite(); err != nil {
			return nil, fmt.Errorf("after edited draft write: %w: %w", err, errs.ErrStoreWrite)
		}
	}

	if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(a.Chapter), "edit",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		return nil, fmt.Errorf("checkpoint edit: %w: %w", errs.ErrStoreWrite, err)
	}
	currentBody, err := t.store.Drafts.LoadDraft(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load edited draft for whole-text gate: %w: %w", errs.ErrStoreRead, err)
	}
	aigcReport, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, currentBody)
	if err := checkpointDraftStructuralBlock(t.store, a.Chapter, currentBody, aigcReport, aigcGate); err != nil {
		return nil, fmt.Errorf("checkpoint edited draft structural block: %w: %w", errs.ErrStoreWrite, err)
	}
	localStructuralRerender := draftAIGCHasWholeTextStructuralBlock(currentBody, aigcReport, aigcGate)
	if localStructuralRerender {
		if err := persistDraftAIGCRerenderRequirement(t.store, a.Chapter, currentBody, aigcReport, aigcGate); err != nil {
			return nil, fmt.Errorf("persist edited draft AIGC rerender requirement: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := clearDraftWriteIntent(t.store.Dir(), a.Chapter); err != nil {
		return nil, fmt.Errorf("complete edit write: %w: %w", err, errs.ErrStoreWrite)
	}

	// 附加指引：让 writer 知道后续步骤，避免遗漏 check_consistency / commit_chapter
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["message"] = fmt.Sprintf("Successfully replaced text in drafts/%02d.draft.md.", a.Chapter)
	passthrough["aigc_gate"] = aigcGate
	if localStructuralRerender {
		passthrough["external_rejudge_required"] = true
		passthrough["external_rejudge_required_now"] = false
		passthrough["local_structural_rerender_required"] = true
		passthrough["registered_external_retest_deferred"] = RequiresRegisteredExternalRetest(externalGateBefore.Requirement)
		passthrough["stop_prose_modification"] = true
		passthrough["next_step"] = "立即停止正文修改并把控制权交还外层 pipeline；edit 后的精确整章哈希触发本地 whole-text/segment 结构阻断，不能继续局部打补丁。外层将执行有界整章重渲染或重规划；已有注册 detector/mode 复测义务保持不变。"
	} else if externalGateBefore.Status == DraftExternalGateApproved || externalGateBefore.LocalSoftEditPending {
		passthrough["external_rejudge_required"] = true
		passthrough["external_rejudge_required_now"] = true
		passthrough["registered_external_retest_deferred"] = RequiresRegisteredExternalRetest(externalGateBefore.Requirement)
		passthrough["stop_prose_modification"] = true
		passthrough["next_step"] = "edit 已使当前哈希的 DeepSeek 通过结论失效：立即停止正文修改并把控制权交还外层 pipeline；禁止再次 edit_chapter、check_consistency 或 commit_chapter，先复判新哈希；注册 detector/mode 复测义务继续暂缓到本地门禁与 DeepSeek 都通过后"
	} else {
		passthrough["next_step"] = "edit 已落盘。仍有硬伤可再次 edit_chapter；否则 check_consistency 后 commit_chapter"
	}
	return json.Marshal(passthrough)
}

func draftExternalGateEditPrecondition(chapter int, inspection DraftExternalGateInspection) error {
	if draftCurrentHashNamedPassFrozen(inspection) {
		return fmt.Errorf("第 %d 章当前草稿精确哈希已通过全部注册 detector/mode 的严格 <4%% 复测，正文已冻结；禁止 edit_chapter 改变送检载荷，只允许继续 check_consistency/commit_chapter。若确需换稿，必须先产生新的整章重渲染授权或新的阻断要求: %w", chapter, errs.ErrToolPrecondition)
	}
	switch inspection.Status {
	case DraftExternalGateRerenderAuthorized:
		return fmt.Errorf("%s: %w", draftExternalRerenderInstruction(inspection.Requirement), errs.ErrToolPrecondition)
	case DraftExternalGateAdviceIncomplete:
		return fmt.Errorf("第 %d 章外判建议不完整，禁止局部编辑；先重新外判: %w", chapter, errs.ErrToolPrecondition)
	case DraftExternalGateRejudgePending:
		if !inspection.LocalSoftEditPending {
			return fmt.Errorf("第 %d 章当前草稿哈希尚未外判，禁止继续局部编辑；先停止正文修改并复判: %w", chapter, errs.ErrToolPrecondition)
		}
	}
	return nil
}

// loadEditBase keeps seeding inside the same write intent as the edit. A crash
// can therefore never leave an uncheckpointed final->draft copy behind.
func (t *EditChapterTool) loadEditBase(chapter int) (prior, base string, err error) {
	draft, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return "", "", fmt.Errorf("load draft: %w: %w", errs.ErrStoreRead, err)
	}
	if draft != "" {
		return draft, draft, nil
	}
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return "", "", fmt.Errorf("load chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if text == "" {
		return "", "", fmt.Errorf("第 %d 章无草稿也无终稿，请先调 draft_chapter(mode=write, chapter=%d) 创建初稿: %w", chapter, chapter, errs.ErrToolPrecondition)
	}
	return "", text, nil
}

func prepareChapterEditCandidate(ctx context.Context, chapter int, base string, args json.RawMessage) (string, json.RawMessage, error) {
	tmpDir, err := os.MkdirTemp("", "novel-studio-edit-preview-*")
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	rel := filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter))
	path := filepath.Join(tmpDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		return "", nil, err
	}
	// Host contexts may carry a live cwd override. Pin the preview to an
	// absolute temporary path so that ambient cwd can never redirect this
	// pre-intent computation back onto the real project draft.
	var isolatedArgs map[string]any
	if err := json.Unmarshal(args, &isolatedArgs); err != nil {
		return "", nil, err
	}
	isolatedArgs["path"] = path
	isolatedArgs["file_path"] = path
	rawArgs, err := json.Marshal(isolatedArgs)
	if err != nil {
		return "", nil, err
	}
	result, err := agentcoretools.NewEdit(tmpDir, nil).Execute(ctx, rawArgs)
	if err != nil {
		return "", nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	return string(raw), result, nil
}
