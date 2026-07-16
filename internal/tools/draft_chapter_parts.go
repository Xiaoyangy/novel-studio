package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// DraftChapterPartTool 写入章节正文分片，用于长章或上下文压力较高的渲染流程。
type DraftChapterPartTool struct {
	store *store.Store
}

func NewDraftChapterPartTool(store *store.Store) *DraftChapterPartTool {
	return &DraftChapterPartTool{store: store}
}

func (t *DraftChapterPartTool) Name() string { return "draft_chapter_part" }
func (t *DraftChapterPartTool) Description() string {
	return "写入章节正文分片到 drafts/NN.parts/part-XX.md。适合长章、上下文压力或多 agent 分段生成；全部分片完成后必须调用 merge_chapter_parts 合并为整章草稿"
}
func (t *DraftChapterPartTool) Label() string { return "写入章节分片" }

func (t *DraftChapterPartTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *DraftChapterPartTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *DraftChapterPartTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("part", schema.Int("分片序号，从 1 开始")).Required(),
		schema.Property("total_parts", schema.Int("本章计划分片总数")).Required(),
		schema.Property("title", schema.String("分片标题或场景名")).Required(),
		schema.Property("focus", schema.String("本分片承担的剧情/人物/信息功能")).Required(),
		schema.Property("content", schema.String("本分片正文，只写正常小说正文，不写清单或说明")).Required(),
	)
}

func (t *DraftChapterPartTool) StrictSchema() bool { return true }

func (t *DraftChapterPartTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		Part       int    `json:"part"`
		TotalParts int    `json:"total_parts"`
		Title      string `json:"title"`
		Focus      string `json:"focus"`
		Content    string `json:"content"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if err := guardPipelineProseExecution(t.store, a.Chapter, t.Name()); err != nil {
		return nil, err
	}
	if err := validateChapterPartArgs(a.Chapter, a.Part, a.TotalParts, a.Content); err != nil {
		return nil, err
	}
	if err := requireDraftPartRoute(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if err := ensureChapterDraftPartWritable(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if _, err := validateCurrentChapterRenderPlan(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if draft, err := t.store.Drafts.LoadDraft(a.Chapter); err != nil {
		return nil, fmt.Errorf("load draft before part write: %w: %w", errs.ErrStoreRead, err)
	} else if strings.TrimSpace(draft) != "" {
		idx, idxErr := t.store.Drafts.LoadDraftPartIndex(a.Chapter)
		if idxErr != nil {
			return nil, fmt.Errorf("load draft parts index: %w: %w", errs.ErrStoreRead, idxErr)
		}
		if idx == nil {
			return nil, fmt.Errorf("第 %d 章已有整章草稿，未检测到分片索引；请先 read_chapter(source=draft) 后 check_consistency/commit_chapter，或明确整章重写: %w", a.Chapter, errs.ErrToolPrecondition)
		}
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}
	index, item, err := t.store.Drafts.SaveDraftPart(a.Chapter, a.Part, a.TotalParts, strings.TrimSpace(a.Title), strings.TrimSpace(a.Focus), a.Content)
	if err != nil {
		return nil, fmt.Errorf("save draft part: %w: %w", errs.ErrStoreWrite, err)
	}
	if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(a.Chapter), "draft_part", item.ContentPath,
		"plan", "rerender-request", "draft_part",
	); err != nil {
		return nil, fmt.Errorf("checkpoint draft part: %w", err)
	}
	missing := missingDraftParts(index, a.TotalParts)
	return json.Marshal(map[string]any{
		"written":     true,
		"chapter":     a.Chapter,
		"part":        a.Part,
		"total_parts": a.TotalParts,
		"word_count":  item.RuneCount,
		"missing":     missing,
		"index_path":  fmt.Sprintf("drafts/%02d.parts/index.json", a.Chapter),
		"part_path":   item.ContentPath,
		"next_step":   draftPartNextStep(a.Chapter, len(missing) == 0),
	})
}

// MergeChapterPartsTool 将分片正文合成 drafts/NN.draft.md，交给原有整章门禁继续处理。
type MergeChapterPartsTool struct {
	store *store.Store
}

func NewMergeChapterPartsTool(store *store.Store) *MergeChapterPartsTool {
	return &MergeChapterPartsTool{store: store}
}

func (t *MergeChapterPartsTool) Name() string { return "merge_chapter_parts" }
func (t *MergeChapterPartsTool) Description() string {
	return "把 drafts/NN.parts/ 中的正文分片合并成 drafts/NN.draft.md。合并后必须 read_chapter(source=draft)、check_consistency、commit_chapter"
}
func (t *MergeChapterPartsTool) Label() string { return "合并章节分片" }

func (t *MergeChapterPartsTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *MergeChapterPartsTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *MergeChapterPartsTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("expected_parts", schema.Int("预期分片总数，必须与写入时 total_parts 一致")).Required(),
	)
}

func (t *MergeChapterPartsTool) StrictSchema() bool { return true }

func (t *MergeChapterPartsTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter       int                    `json:"chapter"`
		ExpectedParts int                    `json:"expected_parts"`
		Sampling      *domain.SamplingRecord `json:"sampling"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if err := guardPipelineProseExecution(t.store, a.Chapter, t.Name()); err != nil {
		return nil, err
	}
	if a.ExpectedParts <= 0 {
		return nil, fmt.Errorf("expected_parts must be > 0: %w", errs.ErrToolArgs)
	}
	if err := requireDraftPartRoute(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if err := ensureChapterDraftPartWritable(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if _, err := validateCurrentChapterRenderPlan(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if draftNeedsConsistencyCheck(t.store, a.Chapter) {
		return nil, fmt.Errorf("第 %d 章已有合并草稿且尚未执行 check_consistency，禁止重复 merge_chapter_parts；请先 read_chapter(source=draft)，再调用 check_consistency，若无硬伤直接 commit_chapter: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	indexBeforeMerge, err := t.store.Drafts.LoadDraftPartIndex(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load draft parts index before epoch gate: %w: %w", errs.ErrStoreRead, err)
	}
	if err := validateDraftPartsPlanEpoch(t.store, a.Chapter, indexBeforeMerge); err != nil {
		return nil, err
	}
	content, index, missing, err := t.store.Drafts.MergeDraftParts(a.Chapter, a.ExpectedParts)
	if err != nil {
		return nil, fmt.Errorf("merge draft parts: %w: %w", errs.ErrStoreRead, err)
	}
	if index == nil || len(index.Parts) == 0 {
		return nil, fmt.Errorf("第 %d 章没有分片草稿，不能合并: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if len(missing) > 0 {
		return json.Marshal(map[string]any{
			"merged":         false,
			"chapter":        a.Chapter,
			"expected_parts": a.ExpectedParts,
			"missing":        missing,
			"next_step":      fmt.Sprintf("先补齐第 %d 章缺失分片 %v，再重新调用 merge_chapter_parts", a.Chapter, missing),
		})
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("第 %d 章分片合并后为空: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if err := validateFictionProseTypography(content); err != nil {
		return nil, fmt.Errorf("第 %d 章分片合并正文格式门禁未通过: %w", a.Chapter, err)
	}
	if err := requireDraftHardFactAnchors(t.store, a.Chapter, content); err != nil {
		return nil, fmt.Errorf("第 %d 章 merge_chapter_parts 候选未通过 hard-fact anchor 门禁，真实草稿与 checkpoint 均未改变: %w", a.Chapter, err)
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}
	prior, err := t.store.Drafts.LoadDraft(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load prior draft before merge: %w: %w", errs.ErrStoreRead, err)
	}
	if err := beginDraftWriteIntent(t.store, a.Chapter, prior, content, "merge", a.Sampling); err != nil {
		return nil, fmt.Errorf("begin merged draft write: %w", err)
	}
	if err := t.store.Drafts.SaveDraft(a.Chapter, content); err != nil {
		return nil, fmt.Errorf("save merged draft: %w: %w", errs.ErrStoreWrite, err)
	}
	if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(a.Chapter), "draft",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		return nil, fmt.Errorf("checkpoint merged draft: %w", err)
	}
	analysis, err := NewDraftChapterTool(t.store).saveDraftAIVoice(a.Chapter, content, a.Sampling)
	if err != nil {
		return nil, err
	}
	wordContract := inspectChapterWordContract(t.store, content)
	aigcReport, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, content)
	rawAIGCGate := draftAIGCRawLocalGateResult(aigcReport, aigcGate)
	if err := checkpointDraftStructuralBlock(t.store, a.Chapter, content, aigcReport, aigcGate); err != nil {
		return nil, fmt.Errorf("checkpoint merged draft structural block: %w", err)
	}
	if !rawAIGCGate.Passed {
		if err := persistDraftAIGCRerenderRequirement(t.store, a.Chapter, content, aigcReport, aigcGate); err != nil {
			return nil, fmt.Errorf("persist merged draft AIGC rerender requirement: %w", err)
		}
	}
	localStructuralRerender := draftAIGCHasWholeTextStructuralBlock(content, aigcReport, aigcGate)
	nextStep := draftQualityGateNextStep(wordContract, aigcGate)
	if err := clearDraftWriteIntent(t.store.Dir(), a.Chapter); err != nil {
		return nil, fmt.Errorf("complete merged draft write: %w", err)
	}
	managedJudgePending, err := pipelineManagedCurrentDraftNeedsDeepSeekJudge(
		t.store, a.Chapter, reviewreport.BodySHA256(content),
	)
	if err != nil {
		return nil, fmt.Errorf("inspect current-hash DeepSeek gate after merge: %w: %w", err, errs.ErrStoreRead)
	}
	if localStructuralRerender {
		nextStep = "停止本次正文修改；合并后的精确整章哈希仍触发本地 whole-text/segment 结构阻断。分片不能绕过有界整章重渲染/重规划预算，立即把控制权交还外层 pipeline。"
	} else if managedJudgePending {
		nextStep = fmt.Sprintf("停止正文修改；合并后的 pipeline 新稿 drafts/%02d.draft.md 尚无可批准当前哈希的 DeepSeek 裸正文结论，立即把控制权交还外层 pipeline 复判；复判前禁止 check_consistency、edit 或 commit", a.Chapter)
	}
	stopForWholeDraftJudge := localStructuralRerender || managedJudgePending
	return json.Marshal(map[string]any{
		"merged":                             true,
		"chapter":                            a.Chapter,
		"parts":                              len(index.Parts),
		"word_count":                         utf8.RuneCountInString(content),
		"word_contract":                      wordContract,
		"aigc_gate":                          aigcGate,
		"aigc_raw_local_gate":                rawAIGCGate,
		"hard_gate_passed":                   wordContract.Passed && rawAIGCGate.Passed && !managedJudgePending,
		"external_rejudge_required":          stopForWholeDraftJudge,
		"external_rejudge_required_now":      managedJudgePending && !localStructuralRerender,
		"local_structural_rerender_required": localStructuralRerender,
		"stop_prose_modification":            stopForWholeDraftJudge,
		"ai_voice_score":                     analysis.Metrics.AIVoiceScore,
		"figurative_density":                 analysis.Metrics.FigurativeDensity,
		"dialogue_ratio":                     analysis.Metrics.DialogueRatio,
		"draft_path":                         fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"next_step":                          nextStep,
	})
}

// requireDraftPartRoute keeps long-chapter helpers from becoming a side door
// around exact-hash rerender/rejudge locks or an exhausted structural budget.
// Once any whole-draft state exists, the next replacement must use the single
// atomic draft_chapter(mode=write) path so named detector identities and write
// saga recovery remain intact.
func requireDraftPartRoute(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("invalid chapter %d: %w", chapter, errs.ErrToolArgs)
	}
	if err := NewDraftChapterTool(st).recoverDraftWriteIntent(chapter); err != nil {
		return fmt.Errorf("recover interrupted whole-draft write before part route: %w: %w", err, errs.ErrStoreWrite)
	}
	if escalation := InspectRenderOnlyReplanEscalation(st, chapter); escalation.Required {
		return fmt.Errorf("第 %d 章 render-only 已连续结构失败，分片不能绕过上限：%s；必须先重做 chapter_world_simulation 与 POV plan: %w", chapter, escalation.Reason, errs.ErrToolPrecondition)
	}
	if ExplicitRerenderRequestActive(st, chapter) || ReviewRequiresFreshDraft(st, chapter) {
		return fmt.Errorf("第 %d 章已有整章重渲染授权，禁止改走 draft_chapter_part/merge_chapter_parts；必须使用 draft_chapter(mode=write): %w", chapter, errs.ErrToolPrecondition)
	}
	inspection, err := InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return fmt.Errorf("读取第 %d 章草稿外审门禁: %w: %w", chapter, err, errs.ErrStoreRead)
	}
	if inspection.Status != DraftExternalGateNotRequired {
		return fmt.Errorf("第 %d 章处于草稿外审状态 %s，分片不能替代整章单次授权或同哈希复判；必须使用 draft_chapter(mode=write) 或等待外层复判: %w", chapter, inspection.Status, errs.ErrToolPrecondition)
	}
	return nil
}

func validateChapterPartArgs(chapter, part, totalParts int, content string) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if part <= 0 {
		return fmt.Errorf("part must be > 0: %w", errs.ErrToolArgs)
	}
	if totalParts <= 0 {
		return fmt.Errorf("total_parts must be > 0: %w", errs.ErrToolArgs)
	}
	if part > totalParts {
		return fmt.Errorf("part(%d) must be <= total_parts(%d): %w", part, totalParts, errs.ErrToolArgs)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content must not be empty: %w", errs.ErrToolArgs)
	}
	if err := validateFictionProseMetadataFree(content); err != nil {
		return fmt.Errorf("draft part content is not chapter prose: %w", err)
	}
	if err := validateFictionProseTypography(content); err != nil {
		return fmt.Errorf("draft part content is not chapter prose: %w", err)
	}
	return nil
}

func ensureChapterDraftPartWritable(st *store.Store, chapter int) error {
	if err := st.Progress.ValidateChapterWork(chapter); err != nil {
		return err
	}
	if err := EnsureChapterExpanded(st, chapter); err != nil {
		return err
	}
	if st.Progress.IsChapterCompleted(chapter) {
		progress, _ := st.Progress.Load()
		inRewriteQueue := progress != nil && slices.Contains(progress.PendingRewrites, chapter)
		if !inRewriteQueue {
			return fmt.Errorf("第 %d 章已提交完成，不能写入分片；需返工请先由 editor/reopen_book 入队: %w", chapter, errs.ErrToolPrecondition)
		}
	}
	return nil
}

func missingDraftParts(index *domain.ChapterDraftPartIndex, totalParts int) []int {
	if totalParts <= 0 {
		return nil
	}
	present := make(map[int]struct{}, len(index.Parts))
	for _, part := range index.Parts {
		present[part.Part] = struct{}{}
	}
	var missing []int
	for i := 1; i <= totalParts; i++ {
		if _, ok := present[i]; !ok {
			missing = append(missing, i)
		}
	}
	return missing
}

func draftPartNextStep(chapter int, complete bool) string {
	if complete {
		return fmt.Sprintf("第 %d 章所有分片已写入。下一步调用 merge_chapter_parts 合并整章草稿，再 read_chapter(source=draft)、check_consistency、commit_chapter。", chapter)
	}
	return fmt.Sprintf("继续补写第 %d 章剩余分片；不要直接 commit。全部分片完成后调用 merge_chapter_parts。", chapter)
}
