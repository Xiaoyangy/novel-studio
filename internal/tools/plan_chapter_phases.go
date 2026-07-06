package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// 两阶段章节规划：plan_structure 先落 5 个核心字段和章节契约，plan_details 分批
// 提交 causal_simulation 并在最后一批 finalize。单发的 plan_chapter 保持不变，
// 两条路径在 finalizeChapterPlan 汇合，校验口径完全一致。

// PlanStructureTool 两阶段规划第 1 步：提交章节骨架。
type PlanStructureTool struct {
	store *store.Store
}

func NewPlanStructureTool(store *store.Store) *PlanStructureTool {
	return &PlanStructureTool{store: store}
}

func (t *PlanStructureTool) Name() string { return "plan_structure" }
func (t *PlanStructureTool) Description() string {
	return "章节推演第 1 阶段：先提交 5 个核心字段（chapter/title/goal/conflict/hook）与章节契约，" +
		"落成 drafts/NN.plan.partial.json。之后用 plan_details 分批提交 causal_simulation，" +
		"最后一批传 finalize=true 完成校验。单次输出压力大的长章推荐走这条两阶段路径；" +
		"也可以继续用 plan_chapter 一次性提交全部字段。"
}
func (t *PlanStructureTool) Label() string { return "规划章节骨架" }

func (t *PlanStructureTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanStructureTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanStructureTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("title", schema.String("章节标题")).Required(),
		schema.Property("goal", schema.String("本章目标")).Required(),
		schema.Property("conflict", schema.String("核心冲突")).Required(),
		schema.Property("hook", schema.String("章末钩子")).Required(),
		schema.Property("emotion_arc", schema.String("情绪曲线")),
		schema.Property("notes", schema.String("自由备忘；写明本章承接的历史数据、大纲、动态台账、资源/人物连续性、写法资产或 RAG 召回依据")),
		schema.Property("required_beats", schema.Array("本章必须完成的推进项", schema.String(""))),
		schema.Property("forbidden_moves", schema.Array("本章明确不能发生的推进", schema.String(""))),
		schema.Property("continuity_checks", schema.Array("本章需特别核对的连续性点", schema.String(""))),
		schema.Property("evaluation_focus", schema.Array("Editor 重点检查项", schema.String(""))),
		schema.Property("emotion_target", schema.String("可选：本章希望读者主要感受到的情绪")),
		schema.Property("payoff_points", schema.Array("可选：关键章希望回应的情节点或兑现点", schema.String(""))),
		schema.Property("hook_goal", schema.String("可选：章末希望驱动的追读欲望或悬念目标")),
		schema.Property("scene_anchors", schema.Array("可选：本章要反复使用并承担信息、关系或代价的现场物件、痕迹、动作证据", schema.String(""))),
	)
}

func (t *PlanStructureTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var structure map[string]any
	if err := unmarshalToolArgs(args, &structure); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	chapter := intFromAny(structure["chapter"])
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, chapter)
	if err != nil || skipped != nil {
		return skipped, err
	}
	for _, field := range []string{"title", "goal", "conflict", "hook"} {
		if s, _ := structure[field].(string); s == "" {
			return nil, fmt.Errorf("plan_structure 缺少核心字段 %s: %w", field, errs.ErrToolArgs)
		}
	}
	delete(structure, "causal_simulation") // 细节只走 plan_details，避免两处口径漂移

	partial := map[string]any{
		"structure":         structure,
		"causal_simulation": map[string]any{},
		"rewrite":           isRewritePlan,
		"updated_at":        time.Now().Format(time.RFC3339),
	}
	if err := t.store.Drafts.SaveChapterPlanPartial(chapter, partial); err != nil {
		return nil, fmt.Errorf("save chapter plan partial: %w: %w", errs.ErrStoreWrite, err)
	}
	return json.Marshal(map[string]any{
		"staged":  "structure",
		"chapter": chapter,
		"rewrite": isRewritePlan,
		"next_step": "分批调用 plan_details(chapter, causal_simulation={...}) 提交推演细节；" +
			"每批只带一部分字段即可（例如先 initial_state/offscreen_character_stage，再对白/世界层，最后计划类字段），" +
			"最后一批传 finalize=true 触发完整校验并落成正式 plan",
	})
}

// PlanDetailsTool 两阶段规划第 2 步：分批合并 causal_simulation，finalize 时收口。
type PlanDetailsTool struct {
	store *store.Store
}

func NewPlanDetailsTool(store *store.Store) *PlanDetailsTool {
	return &PlanDetailsTool{store: store}
}

func (t *PlanDetailsTool) Name() string { return "plan_details" }
func (t *PlanDetailsTool) Description() string {
	return "章节推演第 2 阶段：向 plan_structure 建立的中间态分批合并 causal_simulation 字段，" +
		"同名字段后批覆盖前批。最后一批传 finalize=true：合并结果按 plan_chapter 同一口径完整校验，" +
		"通过后写 drafts/NN.plan.json、置章节 in_progress 并记 checkpoint。"
}
func (t *PlanDetailsTool) Label() string { return "补充章节推演" }

func (t *PlanDetailsTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanDetailsTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanDetailsTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号；必须已有 plan_structure 中间态")).Required(),
		schema.Property("causal_simulation", causalSimulationSchema(false)).Required(),
		schema.Property("finalize", schema.Bool("最后一批传 true：合并全部批次后运行与 plan_chapter 相同的完整校验；校验失败会列出缺失字段，补批后可重试")),
	)
}

func (t *PlanDetailsTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter          int            `json:"chapter"`
		CausalSimulation map[string]any `json:"causal_simulation"`
		Finalize         bool           `json:"finalize"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	partial, err := t.store.Drafts.LoadChapterPlanPartial(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter plan partial: %w: %w", errs.ErrStoreRead, err)
	}
	if partial == nil {
		return nil, fmt.Errorf("第 %d 章没有两阶段规划中间态：请先调用 plan_structure 提交核心字段，或改用 plan_chapter 一次性提交: %w", a.Chapter, errs.ErrToolPrecondition)
	}

	merged, _ := partial["causal_simulation"].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, a.CausalSimulation)
	partial["causal_simulation"] = merged
	partial["updated_at"] = time.Now().Format(time.RFC3339)
	if err := t.store.Drafts.SaveChapterPlanPartial(a.Chapter, partial); err != nil {
		return nil, fmt.Errorf("save chapter plan partial: %w: %w", errs.ErrStoreWrite, err)
	}

	if !a.Finalize {
		return json.Marshal(map[string]any{
			"staged":         "details",
			"chapter":        a.Chapter,
			"fields_present": sortedKeys(merged),
			"next_step":      "继续 plan_details 提交剩余字段；全部就绪后最后一批传 finalize=true",
		})
	}

	// finalize：拼回完整 plan_chapter 参数并走同一条校验/落盘路径。
	structure, _ := partial["structure"].(map[string]any)
	if structure == nil {
		structure = map[string]any{}
	}
	full := make(map[string]any, len(structure)+1)
	maps.Copy(full, structure)
	full["chapter"] = a.Chapter
	full["causal_simulation"] = merged
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("merge chapter plan: %w", err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid merged plan: %w: %w", errs.ErrToolArgs, err)
	}
	// 重新核对门禁：分批期间队列/完成态可能已变化。
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, a.Chapter)
	if err != nil || skipped != nil {
		return skipped, err
	}
	result, err := finalizeChapterPlan(t.store, plan, isRewritePlan)
	if err != nil {
		return nil, err
	}
	if err := t.store.Drafts.DeleteChapterPlanPartial(a.Chapter); err != nil {
		return nil, fmt.Errorf("cleanup chapter plan partial: %w: %w", errs.ErrStoreWrite, err)
	}
	return result, nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
