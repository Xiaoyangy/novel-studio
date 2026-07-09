package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
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

	// 保留已累积的 causal_simulation：planner 在重试/续跑时常再调一次 plan_structure，
	// 若每次清空会把之前 plan_details 攒下的字段全丢，导致 MiniMax 卡流后进度归零死循环。
	// 仅更新骨架，保住已有推演字段。
	existingSim := map[string]any{}
	if prev, err := t.store.Drafts.LoadChapterPlanPartial(chapter); err == nil && prev != nil {
		if sim, ok := prev["causal_simulation"].(map[string]any); ok {
			existingSim = sim
		}
	}
	partial := map[string]any{
		"structure":         structure,
		"causal_simulation": existingSim,
		"rewrite":           isRewritePlan,
		"updated_at":        time.Now().Format(time.RFC3339),
	}
	if err := validateProjectContaminationFree(t.store, "plan_structure", partial); err != nil {
		return nil, err
	}
	if issues := ChapterPlanIdentityIssues(t.store, chapter, partial); len(issues) > 0 {
		return nil, fmt.Errorf("第 %d 章计划身份锚点不合格：%s: %w", chapter, strings.Join(issues, "；"), errs.ErrToolPrecondition)
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
		// chapter / causal_simulation 均不设 schema-required：
		// - chapter 缺省用 in-progress 章推断；
		// - causal_simulation 缺省允许"仅 finalize"——把已累积的 partial 直接收口。
		//   否则 planner 想 finalize 已攒好的字段时被迫每次重发整个 causal_simulation，
		//   请求暴涨→MiniMax 卡流→永远收不了口（实测 05:51 死循环根因）。
		//   Execute 里 maps.Copy(merged, nil) 是安全空操作，finalize 校验会精确列出真缺的字段。
		schema.Property("chapter", schema.Int("章节号；缺省时用当前 in-progress 章。必须已有 plan_structure 中间态")),
		schema.Property("causal_simulation", causalSimulationSchema(false)),
		schema.Property("finalize", schema.Bool("最后一批传 true：合并全部批次后运行与 plan_chapter 相同的完整校验；校验失败会列出缺失字段，补批后可重试")),
	)
}

// inProgressChapter 从进度推断当前正在写作的章号：优先 in_progress_chapter，
// 回退到下一待写章。弱模型丢 chapter 参数时用它兜底。
func (t *PlanDetailsTool) inProgressChapter() int {
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil {
		return 0
	}
	if progress.InProgressChapter > 0 {
		return progress.InProgressChapter
	}
	return progress.NextChapter()
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
		a.Chapter = t.inProgressChapter()
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter 缺失且无法从进度推断当前章：请显式传 chapter: %w", errs.ErrToolArgs)
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
	if len(a.CausalSimulation) == 0 && !a.Finalize {
		return nil, fmt.Errorf("plan_details 空提交无效：必须提交非空 causal_simulation 补丁，不能只查看进度。当前缺口：%s。下一步只补最靠前的缺口分组: %w",
			strings.Join(planDetailsGapSummary(t.store, a.Chapter, partial, merged), "；"),
			errs.ErrToolArgs,
		)
	}
	mergeCausalSimulationPatch(merged, a.CausalSimulation)
	partial["causal_simulation"] = merged
	partial["updated_at"] = time.Now().Format(time.RFC3339)
	if err := validateProjectContaminationFree(t.store, "plan_details", partial); err != nil {
		return nil, err
	}
	if issues := ChapterPlanIdentityIssues(t.store, a.Chapter, partial); len(issues) > 0 {
		return nil, fmt.Errorf("第 %d 章计划身份锚点不合格：%s: %w", a.Chapter, strings.Join(issues, "；"), errs.ErrToolPrecondition)
	}
	if err := t.store.Drafts.SaveChapterPlanPartial(a.Chapter, partial); err != nil {
		return nil, fmt.Errorf("save chapter plan partial: %w: %w", errs.ErrStoreWrite, err)
	}

	if !a.Finalize {
		if result, finalized, err := t.autoFinalizePartialIfComplete(a.Chapter, partial, merged); finalized || err != nil {
			return result, err
		}
		return json.Marshal(map[string]any{
			"staged":              "details",
			"chapter":             a.Chapter,
			"fields_present":      sortedKeys(merged),
			"gap_summary":         planDetailsGapSummary(t.store, a.Chapter, partial, merged),
			"next_step":           "继续 plan_details 提交剩余字段；每次只补一个 recommended_batches 分组；全部就绪后最后一批再传 finalize=true；若字段已齐，本工具会自动收口为正式 plan",
			"recommended_batches": planDetailsRecommendedBatches(),
		})
	}

	return t.finalizePartial(a.Chapter, partial, merged)
}

func mergeCausalSimulationPatch(dst, patch map[string]any) {
	for key, value := range patch {
		if mergedArray, ok := mergeArrayObjectsByCharacter(dst[key], value); ok {
			dst[key] = mergedArray
			continue
		}
		dst[key] = value
	}
}

func mergeArrayObjectsByCharacter(existing, incoming any) ([]any, bool) {
	inItems, ok := incoming.([]any)
	if !ok {
		return nil, false
	}
	hasCharacter := false
	for _, item := range inItems {
		if m, ok := item.(map[string]any); ok {
			if s, _ := m["character"].(string); strings.TrimSpace(s) != "" {
				hasCharacter = true
				break
			}
		}
	}
	if !hasCharacter {
		return nil, false
	}
	out := []any{}
	index := map[string]int{}
	if exItems, ok := existing.([]any); ok {
		for _, item := range exItems {
			out = append(out, item)
			if m, ok := item.(map[string]any); ok {
				if s, _ := m["character"].(string); strings.TrimSpace(s) != "" {
					index[strings.TrimSpace(s)] = len(out) - 1
				}
			}
		}
	}
	for _, item := range inItems {
		m, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		name, _ := m["character"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			out = append(out, item)
			continue
		}
		if pos, ok := index[name]; ok {
			if old, ok := out[pos].(map[string]any); ok {
				next := map[string]any{}
				maps.Copy(next, old)
				maps.Copy(next, m)
				out[pos] = next
				continue
			}
		}
		index[name] = len(out)
		out = append(out, item)
	}
	return out, true
}

func (t *PlanDetailsTool) autoFinalizePartialIfComplete(chapter int, partial, merged map[string]any) (json.RawMessage, bool, error) {
	plan, err := chapterPlanFromPartial(chapter, partial, merged)
	if err != nil {
		return nil, false, nil
	}
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, chapter)
	if err != nil || skipped != nil {
		return skipped, err == nil && skipped != nil, err
	}
	if err := validateChapterPrewriteSimulation(t.store, plan, isRewritePlan); err != nil {
		return nil, false, nil
	}
	if hardIssues, _ := checkChapterPlanConsistency(t.store, plan); len(hardIssues) > 0 {
		return nil, false, nil
	}
	result, err := t.finalizePartial(chapter, partial, merged)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

func (t *PlanDetailsTool) finalizePartial(chapter int, partial, merged map[string]any) (json.RawMessage, error) {
	plan, err := chapterPlanFromPartial(chapter, partial, merged)
	if err != nil {
		return nil, fmt.Errorf("invalid merged plan: %w: %w", errs.ErrToolArgs, err)
	}
	// 重新核对门禁：分批期间队列/完成态可能已变化。
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, chapter)
	if err != nil || skipped != nil {
		return skipped, err
	}
	result, err := finalizeChapterPlan(t.store, plan, isRewritePlan)
	if err != nil {
		return nil, planDetailsFinalizeRepairError(chapter, merged, err)
	}
	if err := t.store.Drafts.DeleteChapterPlanPartial(chapter); err != nil {
		return nil, fmt.Errorf("cleanup chapter plan partial: %w: %w", errs.ErrStoreWrite, err)
	}
	return result, nil
}

func planDetailsFinalizeRepairError(chapter int, merged map[string]any, cause error) error {
	return fmt.Errorf("第 %d 章 plan_details finalize 未通过：%v。已保存字段：%s。修复协议：不要一次性重发所有字段，也不要立刻 finalize=true；下一轮只补 recommended_batches 中最靠前且未完成的一组，保留已保存字段，最后一组补完后再传 finalize=true。recommended_batches=%s: %w",
		chapter,
		cause,
		strings.Join(sortedKeys(merged), ", "),
		strings.Join(planDetailsRecommendedBatches(), " | "),
		errs.ErrToolPrecondition,
	)
}

func planDetailsRecommendedBatches() []string {
	return []string{
		"batch1_context_and_stage: context_sources + offscreen_character_stage；若 initial_state 已有，只补缺失 action_tendency 等小字段，不重发超大 initial_state",
		"batch2_scene_causality: environment_state + causal_beats + decision_points + outcome_shift",
		"batch3_voice_dialogue: voice_logic + dialogue_scene_blueprints",
		"batch4_character_emotion: character_arc_tests + emotional_logic + relationship_emotion_arcs + dormant_character_policy",
		"batch5_world_pressure: world_background_layers + information_asymmetry + hidden_rule_pressure + social_mood_rumors + ritual_calendar + structural_resources + cosmology_checks + conflict_web + narrative_tension_matrix",
	}
}

func planDetailsGapSummary(s *store.Store, chapter int, partial, merged map[string]any) []string {
	var gaps []string
	for _, field := range []string{
		"context_sources",
		"offscreen_character_stage",
		"environment_state",
		"causal_beats",
		"decision_points",
		"outcome_shift",
		"voice_logic",
		"dialogue_scene_blueprints",
		"character_arc_tests",
		"dormant_character_policy",
		"emotional_logic",
		"relationship_emotion_arcs",
		"world_background_layers",
		"information_asymmetry",
		"hidden_rule_pressure",
		"social_mood_rumors",
		"ritual_calendar",
		"structural_resources",
		"cosmology_checks",
		"conflict_web",
		"narrative_tension_matrix",
	} {
		if _, ok := merged[field]; !ok {
			gaps = append(gaps, "missing "+field)
		}
	}
	plan, err := chapterPlanFromPartial(chapter, partial, merged)
	if err == nil {
		required := requiredDossierCharacterNames(s, chapter)
		if missing := missingInitialStateCoverage(required, plan.CausalSimulation.InitialState); len(missing) > 0 {
			gaps = append(gaps, formatMissingCharacterCoverage("initial_state", missing))
		}
		for _, state := range plan.CausalSimulation.InitialState {
			if strings.TrimSpace(state.Character) != "" && strings.TrimSpace(state.ActionTendency) == "" {
				gaps = append(gaps, fmt.Sprintf("initial_state(%s).action_tendency", state.Character))
			}
		}
		if missing := missingArcTestCoverage(required, plan.CausalSimulation.CharacterArcTests); len(missing) > 0 {
			gaps = append(gaps, formatMissingCharacterCoverage("character_arc_tests", missing))
		}
		if missing := missingEmotionalLogicCoverage(required, plan.CausalSimulation.EmotionalLogic); len(missing) > 0 {
			gaps = append(gaps, formatMissingCharacterCoverage("emotional_logic", missing))
		}
	}
	if len(gaps) == 0 {
		return []string{"字段看似齐备；如 finalize 仍失败，请按错误中的具体子字段补最小补丁"}
	}
	return compactStrings(gaps)
}

// chapterPlanFromPartial 拼回完整 plan_chapter 参数，供显式 finalize 与自动收口共用。
func chapterPlanFromPartial(chapter int, partial, merged map[string]any) (domain.ChapterPlan, error) {
	structure, _ := partial["structure"].(map[string]any)
	if structure == nil {
		structure = map[string]any{}
	}
	full := make(map[string]any, len(structure)+1)
	maps.Copy(full, structure)
	full["chapter"] = chapter
	full["causal_simulation"] = merged
	raw, err := json.Marshal(full)
	if err != nil {
		return domain.ChapterPlan{}, fmt.Errorf("merge chapter plan: %w", err)
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		return domain.ChapterPlan{}, err
	}
	return plan, nil
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
