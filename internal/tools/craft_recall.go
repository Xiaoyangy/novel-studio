package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// CraftRecallTool 写作手法库检索：字段绑定确定性 filter，BM25 子集内排序。
// 设计取料命中后必须实例化成本书事实（visual_design、character_kit、world_codex、props）；
// 草稿/重写阶段可检索写法方法、场景处理、对白摩擦、信息延迟和段落节奏。
// 边界：事实一致性仍走 novel_context；craft_recall 只提供"怎么写"的手法依据。
type CraftRecallTool struct {
	store         *store.Store
	mu            sync.Mutex
	chapterCounts map[int]int
	catalogState  *domain.RAGIndexState
	catalog       *rag.CraftCatalog
}

func NewCraftRecallTool(store *store.Store) *CraftRecallTool {
	return &CraftRecallTool{store: store}
}

func (t *CraftRecallTool) Name() string { return "craft_recall" }
func (t *CraftRecallTool) Description() string {
	return "检索写作手法库（外貌词库/武器/能力分级/法术/制度史料/科幻/世界构成/创作方法论/场景情境/对标拆文）。" +
		"每个设计字段绑定固定检索配方，命中与否是确定的；查不到会显式返回 no_material=true——" +
		"此时不得假装用到写法库，必须改用 dialogue/methodology/scene_situation 宽主题或 reference_pack.references 兜底。" +
		"设计取料结果必须实例化为本书事实（visual_design/character_kit/world_codex）后才能进入正文；" +
		"草稿/重写取手法时只迁移结构、节奏、对白摩擦、信息延迟和段落功能，不改写本书事实；查角色现状请用 novel_context。"
}
func (t *CraftRecallTool) Label() string { return "手法库检索" }

func (t *CraftRecallTool) ReadOnly(_ json.RawMessage) bool        { return false } // 写审计日志
func (t *CraftRecallTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *CraftRecallTool) Schema() map[string]any {
	fields := rag.CraftFieldNames()
	descriptions := make([]string, 0, len(fields))
	for _, f := range fields {
		descriptions = append(descriptions, f+"="+rag.CraftRecipeDescription(rag.CraftDesignField(f)))
	}
	return schema.Object(
		schema.Property("field", schema.Enum("设计字段（决定检索配方）："+strings.Join(descriptions, "；"), fields...)).Required(),
		schema.Property("topic", schema.String("在命中子集内排序用的主题词，例如“长剑 淬火”“眼睛 冷峻”；为空取该类目代表性材料")),
		schema.Property("chapter", schema.Int("本次设计服务的章节号；零章初始化传 0")),
		schema.Property("limit", schema.Int("返回条数，默认 5，最大 12")),
	)
}

func (t *CraftRecallTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if err := guardOutlineAllDynamicMaterialExecution(t.store, t.Name()); err != nil {
		return nil, err
	}
	var a struct {
		Field   string `json:"field"`
		Topic   string `json:"topic"`
		Chapter int    `json:"chapter"`
		Limit   int    `json:"limit"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Limit <= 0 {
		a.Limit = 5
	}
	if a.Limit > 12 {
		a.Limit = 12
	}
	effectiveChapter := t.effectiveChapter(a.Chapter)
	if effectiveChapter > 0 {
		if partial, _ := t.store.Drafts.LoadChapterPlanPartial(effectiveChapter); partial != nil {
			return nil, fmt.Errorf("第 %d 章已进入 staged plan repair，禁止 craft_recall；请按 novel_context 的 gap_summary 直接补全世界模拟或 plan_details: %w", effectiveChapter, errs.ErrToolPrecondition)
		}
		if partial, _ := t.store.LoadChapterWorldSimulationPartial(effectiveChapter); partial != nil {
			return nil, fmt.Errorf("第 %d 章全角色世界推演已分批开始，禁止中途 craft_recall 扩张上下文；请继续 simulate_chapter_world 直到 finalize: %w", effectiveChapter, errs.ErrToolPrecondition)
		}
	}
	callCount := t.recordChapterCall(effectiveChapter)
	if effectiveChapter > 0 && callCount > 3 {
		entry := map[string]any{
			"at":                time.Now().Format(time.RFC3339),
			"field":             a.Field,
			"topic":             a.Topic,
			"chapter":           effectiveChapter,
			"requested_chapter": a.Chapter,
			"hit_count":         0,
			"no_material":       true,
			"budget_exhausted":  true,
			"call_count":        callCount,
		}
		_ = t.store.RAG.AppendCraftRecallLog(entry)
		return json.Marshal(map[string]any{
			"field":             a.Field,
			"topic":             a.Topic,
			"chapter":           effectiveChapter,
			"requested_chapter": a.Chapter,
			"no_material":       true,
			"budget_exhausted":  true,
			"call_count":        callCount,
			"hits":              []any{},
			"next_step":         fmt.Sprintf("第 %d 章 craft_recall 已调用 %d 次，超过写前推演预算。禁止继续检索；必须立即调用 plan_structure/plan_details 或 plan_chapter 落盘计划，把已召回材料写入 material_source/context_sources。", effectiveChapter, callCount),
			"method_scope":      "craft_recall 已达到同章预算；继续检索会造成规划 loop。",
		})
	}
	state, err := t.store.RAG.LoadIndexStateReadOnly()
	if err != nil || state == nil || len(state.Chunks) == 0 {
		return nil, fmt.Errorf("RAG 索引不存在或为空；先运行 novel-studio --build-rag --add-source <writing-techniques 路径>: %w", errs.ErrToolPrecondition)
	}
	chunks, filtered := t.filterCrossProjectCraftChunks(state.Chunks)
	result := t.craftCatalogFor(state, chunks).Recall(rag.CraftDesignField(a.Field), a.Topic, a.Limit)

	type hitPayload struct {
		SourcePath string  `json:"source_path"`
		Summary    string  `json:"summary"`
		Text       string  `json:"text"`
		Score      float64 `json:"score"`
	}
	hits := make([]hitPayload, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, hitPayload{
			SourcePath: hit.Chunk.SourcePath,
			Summary:    hit.Chunk.Summary,
			Text:       truncateRunes(hit.Chunk.Text, 1200),
			Score:      hit.Score,
		})
	}
	// 审计留痕：每次设计检索（含 no_material）都是可回放事件。
	_ = t.store.RAG.AppendCraftRecallLog(map[string]any{
		"at":                time.Now().Format(time.RFC3339),
		"field":             a.Field,
		"topic":             a.Topic,
		"chapter":           effectiveChapter,
		"requested_chapter": a.Chapter,
		"call_count":        callCount,
		"hit_count":         len(hits),
		"no_material":       result.NoMaterial,
		"filtered":          filtered,
	})

	nextStep := "若是设计取料，把选用素材实例化为本书事实：外貌进 visual_design（记 material_source=来源路径）、武器/装备/技能进 character_kit、体系分级进 world_codex；若是写法取料，只迁移场景结构、对白摩擦、信息延迟、句式节奏和留存手法，不改写本书事实。"
	if rag.IsBenchmarkField(rag.CraftDesignField(a.Field)) {
		nextStep = "对标素材：**只可迁移手法、结构、节奏与模板骨架**，禁止照搬情节、人名、地名与专有设定；" +
			"选用的手法须在 causal_simulation.external_reference_plan 登记 source_refs 与 transformation_rule，" +
			"草稿/重写阶段则登记为写法来源，转成 reader_retention_plan 的 surface/latent/reveal/cut 筛选与段落功能变化。"
	}
	if result.NoMaterial {
		nextStep = "本字段无可用素材：对应产物字段必须显式写 material_source=no_material；草稿/重写阶段请立刻改用 dialogue、methodology 或 scene_situation 做宽主题检索（主题含 小说场景/留存/冲突/对白/人物刻画/情感叙事/情绪弧线/动机反应/信息延迟/句长变化/AI检测）。若同章同类问题已循环两轮以上，或宽主题仍无料，必须先 web_research 查写作资料、检测平台口径或现实支架，并沉淀为 meta/writing-techniques、meta/web_reference_brief 或 review RAG 规则后再改；仍无料时用 reference_pack.references 兜底并声明 method_source=fallback_reference_pack。不允许假装引用了素材，也不允许凭感觉硬联想。"
	}
	return json.Marshal(map[string]any{
		"field":             a.Field,
		"topic":             a.Topic,
		"chapter":           effectiveChapter,
		"requested_chapter": a.Chapter,
		"call_count":        callCount,
		"no_material":       result.NoMaterial,
		"hits":              hits,
		"next_step":         nextStep,
		"method_scope":      "craft_recall 提供写法/手法/结构参考；本书事实、角色现状、资源状态仍以 novel_context 为准",
	})
}

func (t *CraftRecallTool) effectiveChapter(requested int) int {
	if target, ok := pendingRewriteTarget(t.store); ok {
		return target
	}
	if requested > 0 {
		return requested
	}
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil {
		return requested
	}
	if progress.InProgressChapter > 0 {
		return progress.InProgressChapter
	}
	if progress.CurrentChapter > 0 {
		return progress.CurrentChapter
	}
	if next := progress.NextChapter(); next > 0 {
		return next
	}
	return requested
}

func (t *CraftRecallTool) recordChapterCall(chapter int) int {
	if chapter <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.chapterCounts == nil {
		t.chapterCounts = make(map[int]int)
	}
	t.chapterCounts[chapter]++
	return t.chapterCounts[chapter]
}

func (t *CraftRecallTool) craftCatalogFor(state *domain.RAGIndexState, chunks []domain.RAGChunk) *rag.CraftCatalog {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.catalog != nil && t.catalogState == state {
		return t.catalog
	}
	t.catalog = rag.NewCraftCatalog(chunks)
	t.catalogState = state
	return t.catalog
}

func (t *CraftRecallTool) filterCrossProjectCraftChunks(chunks []domain.RAGChunk) ([]domain.RAGChunk, int) {
	terms := projectContaminationTerms(t.store)
	if len(terms) == 0 {
		return chunks, 0
	}
	filtered := make([]domain.RAGChunk, 0, len(chunks))
	dropped := 0
	for _, chunk := range chunks {
		text := chunk.SourcePath + "\n" + chunk.Context + "\n" + chunk.Summary + "\n" + chunk.Text
		if containsProjectContaminationTerm(terms, text) {
			dropped++
			continue
		}
		filtered = append(filtered, chunk)
	}
	return filtered, dropped
}
