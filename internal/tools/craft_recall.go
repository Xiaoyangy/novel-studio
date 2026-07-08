package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	store *store.Store
}

func NewCraftRecallTool(store *store.Store) *CraftRecallTool {
	return &CraftRecallTool{store: store}
}

func (t *CraftRecallTool) Name() string { return "craft_recall" }
func (t *CraftRecallTool) Description() string {
	return "检索写作手法库（外貌词库/武器/能力分级/法术/制度史料/科幻/世界构成/创作方法论/场景情境/对标拆文）。" +
		"每个设计字段绑定固定检索配方，命中与否是确定的；查不到会显式返回 no_material=true——" +
		"此时不得假装用到写法库，必须改用 methodology/scene_situation 宽主题或 reference_pack.references 兜底。" +
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
	state, err := t.store.RAG.LoadIndexState()
	if err != nil || state == nil || len(state.Chunks) == 0 {
		return nil, fmt.Errorf("RAG 索引不存在或为空；先运行 novel-studio --build-rag --add-source <writing-techniques 路径>: %w", errs.ErrToolPrecondition)
	}
	result := rag.CraftRecall(state.Chunks, rag.CraftDesignField(a.Field), a.Topic, a.Limit)

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
		"at":          time.Now().Format(time.RFC3339),
		"field":       a.Field,
		"topic":       a.Topic,
		"chapter":     a.Chapter,
		"hit_count":   len(hits),
		"no_material": result.NoMaterial,
	})

	nextStep := "若是设计取料，把选用素材实例化为本书事实：外貌进 visual_design（记 material_source=来源路径）、武器/装备/技能进 character_kit、体系分级进 world_codex；若是写法取料，只迁移场景结构、对白摩擦、信息延迟、句式节奏和留存手法，不改写本书事实。"
	if rag.IsBenchmarkField(rag.CraftDesignField(a.Field)) {
		nextStep = "对标素材：**只可迁移手法、结构、节奏与模板骨架**，禁止照搬情节、人名、地名与专有设定；" +
			"选用的手法须在 causal_simulation.external_reference_plan 登记 source_refs 与 transformation_rule，" +
			"草稿/重写阶段则登记为写法来源，转成 reader_retention_plan 的 surface/latent/reveal/cut 筛选与段落功能变化。"
	}
	if result.NoMaterial {
		nextStep = "本字段无可用素材：对应产物字段必须显式写 material_source=no_material；草稿/重写阶段请立刻改用 methodology 或 scene_situation 做宽主题检索（主题含 小说场景/留存/冲突/对白/信息延迟/句长变化/AI检测），仍无料时用 reference_pack.references 兜底并声明 method_source=fallback_reference_pack；不允许假装引用了素材。"
	}
	return json.Marshal(map[string]any{
		"field":        a.Field,
		"topic":        a.Topic,
		"no_material":  result.NoMaterial,
		"hits":         hits,
		"next_step":    nextStep,
		"method_scope": "craft_recall 提供写法/手法/结构参考；本书事实、角色现状、资源状态仍以 novel_context 为准",
	})
}
