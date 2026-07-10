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

const (
	planStructureWorldSimulationKey = "_world_simulation_id"
	planStructureRewriteSHAKey      = "_rewrite_source_sha256"
)

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
	worldSimulation, err := ensureChapterWorldSimulationReadyForPlanning(t.store, chapter)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"title", "goal", "conflict", "hook"} {
		if s, _ := structure[field].(string); s == "" {
			return nil, fmt.Errorf("plan_structure 缺少核心字段 %s: %w", field, errs.ErrToolArgs)
		}
	}
	applyOutlineAnchorsToStructure(t.store, chapter, structure)
	if err := applyRewriteAnchorsToStructure(t.store, chapter, structure); err != nil {
		return nil, err
	}
	bindPlanStructureToSources(t.store, chapter, structure, worldSimulation)
	delete(structure, "causal_simulation") // 细节只走 plan_details，避免两处口径漂移

	// 保留已累积的 causal_simulation：planner 在重试/续跑时常再调一次 plan_structure，
	// 若每次清空会把之前 plan_details 攒下的字段全丢，导致 MiniMax 卡流后进度归零死循环。
	// 仅更新骨架，保住已有推演字段。
	existingSim := map[string]any{}
	if prev, err := t.store.Drafts.LoadChapterPlanPartial(chapter); err == nil && prev != nil {
		if sim, ok := prev["causal_simulation"].(map[string]any); ok && planStructureBoundToSources(t.store, chapter, prev, worldSimulation) {
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

func applyRewriteAnchorsToStructure(s *store.Store, chapter int, structure map[string]any) error {
	source, _, _, err := loadChapterRewriteSource(s, chapter)
	if err != nil || source == nil {
		return err
	}
	required := stringSliceFromAny(structure["required_beats"])
	for _, fact := range source.PreserveFacts {
		required = appendUniqueString(required, fact)
	}
	if len(required) > 0 {
		structure["required_beats"] = required
	}
	checks := stringSliceFromAny(structure["continuity_checks"])
	checks = appendUniqueString(checks, fmt.Sprintf("局部返工必须继承 %s 的事实链；源正文 sha256=%s，允许改写表达，不得改动事件顺序、金额、地点、角色出场、结果、伏笔与章末钩子。", source.BodyPath, source.BodySHA256))
	structure["continuity_checks"] = checks
	return nil
}

func bindPlanStructureToSources(s *store.Store, chapter int, structure map[string]any, simulation *domain.ChapterWorldSimulation) {
	if simulation != nil {
		structure[planStructureWorldSimulationKey] = simulation.SimulationID
	}
	if source, _, _, err := loadChapterRewriteSource(s, chapter); err == nil && source != nil {
		structure[planStructureRewriteSHAKey] = source.BodySHA256
	}
}

func planStructureBoundToSources(s *store.Store, chapter int, partial map[string]any, simulation *domain.ChapterWorldSimulation) bool {
	structure, _ := partial["structure"].(map[string]any)
	if structure == nil {
		return false
	}
	if simulation != nil {
		got, _ := structure[planStructureWorldSimulationKey].(string)
		if strings.TrimSpace(got) != strings.TrimSpace(simulation.SimulationID) {
			return false
		}
	}
	if source, _, _, err := loadChapterRewriteSource(s, chapter); err != nil {
		return false
	} else if source != nil {
		got, _ := structure[planStructureRewriteSHAKey].(string)
		if strings.TrimSpace(got) != source.BodySHA256 {
			return false
		}
	}
	return true
}

func applyOutlineAnchorsToStructure(s *store.Store, chapter int, structure map[string]any) {
	if s == nil || chapter <= 0 || structure == nil {
		return
	}
	entry, err := s.Outline.GetChapterOutline(chapter)
	if err != nil || entry == nil {
		return
	}
	if title := strings.TrimSpace(entry.Title); title != "" {
		structure["title"] = title
	}
	required := stringSliceFromAny(structure["required_beats"])
	if event := strings.TrimSpace(entry.CoreEvent); event != "" {
		// 大纲核心事件决定本章“要完成什么”。允许 Planner 自由设计冲突、场景和
		// 因果，但不能把后续章项目拿来替换当前章目标。
		structure["goal"] = "完整兑现本章大纲核心事件：" + event
		required = appendUniqueString(required, "必须完整兑现大纲核心事件："+event)
	}
	if hook := strings.TrimSpace(entry.Hook); hook != "" {
		// 章末钩子是章节边界。过去仅追加 required beat，会出现标题仍是本章、
		// 正文却一路写到数章后的“成熟钩子”；这里直接钉住正式 hook。
		structure["hook"] = hook
		required = appendUniqueString(required, "必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末："+hook)
	}
	if len(required) > 0 {
		structure["required_beats"] = required
	}
}

func stringSliceFromAny(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
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
	return inProgressChapterOf(t.store)
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
	worldSimulation, err := ensureChapterWorldSimulationReadyForPlanning(t.store, a.Chapter)
	if err != nil {
		return nil, err
	}
	if !planStructureBoundToSources(t.store, a.Chapter, partial, worldSimulation) {
		return nil, fmt.Errorf("第 %d 章 plan_structure 不是由当前 world_simulation/rewrite_source 生成；请先重新调用 plan_structure，再提交 plan_details: %w", a.Chapter, errs.ErrToolPrecondition)
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
	applyPlanDetailsSourceAnchors(t.store, a.Chapter, merged, worldSimulation)
	normalizations := normalizePartialVisibleCharacterScope(t.store, a.Chapter, merged)
	partial["causal_simulation"] = merged
	if len(normalizations) > 0 {
		partial["scope_normalizations"] = append(stringSliceFromAny(partial["scope_normalizations"]), normalizations...)
	}
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
		response := map[string]any{
			"staged":              "details",
			"chapter":             a.Chapter,
			"fields_present":      sortedKeys(merged),
			"gap_summary":         planDetailsGapSummary(t.store, a.Chapter, partial, merged),
			"next_step":           "继续 plan_details 提交剩余字段；每次只补一个 recommended_batches 分组；全部就绪后最后一批再传 finalize=true；若字段已齐，本工具会自动收口为正式 plan",
			"recommended_batches": planDetailsRecommendedBatches(),
		}
		if len(normalizations) > 0 {
			response["scope_normalizations"] = normalizations
		}
		return json.Marshal(response)
	}

	return t.finalizePartial(a.Chapter, partial, merged)
}

func normalizePartialVisibleCharacterScope(s *store.Store, chapter int, merged map[string]any) []string {
	items, ok := merged["offscreen_character_stage"].([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, name := range chapterOutlineCharacterNames(s, chapter) {
		allowed[strings.TrimSpace(name)] = struct{}{}
	}
	known := knownCharacterNameSet(s)
	var changes []string
	for index, item := range items {
		stage, ok := item.(map[string]any)
		if !ok || stage["visible_in_chapter"] != true {
			continue
		}
		name, _ := stage["character"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		if _, ok := known[name]; !ok {
			continue
		}
		stage["visible_in_chapter"] = false
		changes = append(changes, fmt.Sprintf("offscreen_character_stage[%d] %s 未获本章大纲授权，已自动改为 visible_in_chapter=false", index, name))
	}
	return changes
}

func mergeCausalSimulationPatch(dst, patch map[string]any) {
	for key, value := range patch {
		if key == "context_sources" {
			if mergedStrings, ok := mergeUniqueStringArrays(dst[key], value); ok {
				dst[key] = mergedStrings
				continue
			}
		}
		if key == "review_refinement" {
			if mergedMap, ok := mergeReviewRefinementPatch(dst[key], value); ok {
				dst[key] = mergedMap
				continue
			}
		}
		if mergedArray, ok := mergeArrayObjectsByCharacter(dst[key], value); ok {
			dst[key] = mergedArray
			continue
		}
		dst[key] = value
	}
}

func mergeReviewRefinementPatch(existing, incoming any) (map[string]any, bool) {
	incomingMap, ok := incoming.(map[string]any)
	if !ok {
		return nil, false
	}
	merged := map[string]any{}
	if existingMap, ok := existing.(map[string]any); ok {
		maps.Copy(merged, existingMap)
	}
	for key, value := range incomingMap {
		if key == "trigger_sources" || key == "preserve_constraints" {
			if stringsMerged, ok := mergeUniqueStringArrays(merged[key], value); ok {
				merged[key] = stringsMerged
				continue
			}
		}
		merged[key] = value
	}
	return merged, true
}

func mergeUniqueStringArrays(existing, incoming any) ([]any, bool) {
	incomingStrings := stringSliceFromAny(incoming)
	if incomingStrings == nil {
		return nil, false
	}
	merged := stringSliceFromAny(existing)
	for _, value := range incomingStrings {
		merged = appendUniqueString(merged, value)
	}
	out := make([]any, 0, len(merged))
	for _, value := range merged {
		out = append(out, value)
	}
	return out, true
}

func applyPlanDetailsSourceAnchors(st *store.Store, chapter int, merged map[string]any, simulation *domain.ChapterWorldSimulation) {
	if st == nil || chapter <= 0 || merged == nil {
		return
	}
	contextSources := stringSliceFromAny(merged["context_sources"])
	if simulation != nil {
		merged["world_simulation_id"] = simulation.SimulationID
		merged["protagonist_decision"] = simulation.ProtagonistProjection.ChosenDecision
		contextSources = appendUniqueString(contextSources, "chapter_world_simulation:"+simulation.SimulationID)
	}
	if source, _, _, err := loadChapterRewriteSource(st, chapter); err == nil && source != nil {
		contextSources = appendUniqueString(contextSources, rewriteSourceToken(source))
		contextSources = appendUniqueString(contextSources, rewriteBriefToken(source))
		refinement, _ := merged["review_refinement"].(map[string]any)
		if refinement == nil {
			refinement = map[string]any{}
		}
		preserve := stringSliceFromAny(refinement["preserve_constraints"])
		for _, fact := range source.PreserveFacts {
			preserve = appendUniqueString(preserve, fact)
		}
		refinement["preserve_constraints"] = preserve
		merged["review_refinement"] = refinement
	}
	if len(contextSources) > 0 {
		merged["context_sources"] = contextSources
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
		"batch1_causal_foundation: world_simulation_id + protagonist_decision + project_promise + chapter_function + context_sources + initial_state + environment_state + causal_beats + decision_points + outcome_shift（initial_state 只需覆盖主角）",
		"batch2_voice_and_entertainment: voice_logic + dialogue_scene_blueprints + emotional_logic + anti_ai_execution_plan + reader_entertainment_plan；显式要求热梗时同时补 trend_language_plan",
		"batch3_reader_contract: reader_reward_plan + reader_retention_plan + ending_consequence_contract；第一章长篇项目同时补 longform_opening；返工章同时补 review_refinement",
	}
}

func planDetailsGapSummary(s *store.Store, chapter int, partial, merged map[string]any) []string {
	var gaps []string
	if chapterWorldSimulationRequired(s) {
		for _, field := range []string{"world_simulation_id", "protagonist_decision"} {
			if _, ok := merged[field]; !ok {
				gaps = append(gaps, "missing "+field)
			}
		}
	}
	for _, field := range []string{
		"project_promise",
		"chapter_function",
		"context_sources",
		"initial_state",
		"environment_state",
		"causal_beats",
		"decision_points",
		"outcome_shift",
		"voice_logic",
		"dialogue_scene_blueprints",
		"emotional_logic",
		"anti_ai_execution_plan",
		"reader_reward_plan",
		"reader_retention_plan",
		"ending_consequence_contract",
	} {
		if _, ok := merged[field]; !ok {
			gaps = append(gaps, "missing "+field)
		}
	}
	attraction := attractionRequirementsForChapter(s, chapter)
	if attraction.Trend {
		if _, ok := merged["trend_language_plan"]; !ok {
			gaps = append(gaps, "missing trend_language_plan")
		}
	}
	if attraction.Entertainment {
		if _, ok := merged["reader_entertainment_plan"]; !ok {
			gaps = append(gaps, "missing reader_entertainment_plan")
		}
	}
	if attraction.Longform {
		if _, ok := merged["longform_opening"]; !ok {
			gaps = append(gaps, "missing longform_opening")
		}
	}
	if rewrite, _ := partial["rewrite"].(bool); rewrite {
		if _, ok := merged["review_refinement"]; !ok {
			gaps = append(gaps, "missing review_refinement")
		}
	}
	plan, err := chapterPlanFromPartial(chapter, partial, merged)
	if err == nil {
		protagonist := inferCommitProtagonist(s)
		protagonistOnly := compactStrings([]string{protagonist})
		if missing := missingInitialStateCoverage(protagonistOnly, plan.CausalSimulation.InitialState); len(missing) > 0 {
			gaps = append(gaps, formatMissingCharacterCoverage("initial_state", missing))
		}
		for _, state := range plan.CausalSimulation.InitialState {
			if strings.TrimSpace(state.Character) != "" && strings.TrimSpace(state.ActionTendency) == "" {
				gaps = append(gaps, fmt.Sprintf("initial_state(%s).action_tendency", state.Character))
			}
		}
		if missing := missingEmotionalLogicCoverage(protagonistOnly, plan.CausalSimulation.EmotionalLogic); len(missing) > 0 {
			gaps = append(gaps, formatMissingCharacterCoverage("emotional_logic", missing))
		}
		if protagonist != "" && !voiceLogicContainsCharacter(plan.CausalSimulation.VoiceLogic, protagonist) {
			gaps = append(gaps, formatMissingCharacterCoverage("voice_logic", []string{protagonist}))
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
