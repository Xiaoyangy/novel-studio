package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// PlanChapterTool 保存章节构思，Agent 自主决定规划粒度。
type PlanChapterTool struct {
	store *store.Store
}

func NewPlanChapterTool(store *store.Store) *PlanChapterTool {
	return &PlanChapterTool{store: store}
}

func (t *PlanChapterTool) Name() string { return "plan_chapter" }
func (t *PlanChapterTool) Description() string {
	return "保存章节写作构思。必须基于 novel_context 的 simulation_restart_policy、world_foundation、character_dossiers、current_chapter_outline、future_outline_window、progression_snapshot.next_plan、project_progress、character_continuity、character_stage_records、chapter_world_deltas、resource_audit、writing_engine、user_rules、reference_pack.references、prewrite_storycraft_plan、RAG trace、web_reference_brief 和必要的网络检索推导本章任务；若存在推演重启策略，旧章节/旧资源/旧人物经历只能当背景种子，不能作为新 canon 事实。causal_simulation 只能补充角色/世界因果推演、人物 voice_logic、dialogue_scene_blueprints、写作规范执行、人物弧测试、情感逻辑、关系/恋爱情感弧、视觉设计、读者奖励阶梯、读者留存筛选、证据回收链、章末后果契约、休眠角色策略、现实支撑计划、外部资料转化、热梗使用预算、全角色同时间线行动和审核失败后的 review_refinement，不能替代原章节契约、大纲和进度台账；计划是素材池与边界，不是正文清单，必须明确哪些只留台账、哪些延后揭示、哪些压缩不写；禁止凭空开章。Agent 自主决定规划粒度，不强制场景拆分"
}
func (t *PlanChapterTool) Label() string { return "规划章节" }

// 写工具，禁止并发。
func (t *PlanChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanChapterTool) Schema() map[string]any {
	return schema.Object(
		// 核心字段不设 schema-required：弱模型在超大 plan / 压缩后常丢字段触发
		// harness InputValidationError 死循环；且已有两阶段 partial 时这些字段来自
		// plan_structure。放行到 Execute 由 partial 合并或核心字段校验兜底。
		schema.Property("chapter", schema.Int("章节号；缺省用当前 in-progress 章")),
		schema.Property("title", schema.String("章节标题")),
		schema.Property("goal", schema.String("本章目标")),
		schema.Property("conflict", schema.String("核心冲突")),
		schema.Property("hook", schema.String("章末钩子")),
		schema.Property("emotion_arc", schema.String("情绪曲线")),
		schema.Property("notes", schema.String("自由备忘；写明本章承接的历史数据、大纲、动态台账、资源/人物连续性、写法资产或 RAG 召回依据")),
		schema.Property("required_beats", schema.Array("本章正文必须让读者看见的 2-4 个结果级变化；能并成一项就并，不写离屏台账、点击、验证次数、动作拍、台词原句或流程步骤", schema.String(""))),
		schema.Property("forbidden_moves", schema.Array("本章明确不能发生的推进", schema.String(""))),
		schema.Property("continuity_checks", schema.Array("本章需特别核对的连续性点", schema.String(""))),
		schema.Property("evaluation_focus", schema.Array("Editor 重点检查项", schema.String(""))),
		schema.Property("emotion_target", schema.String("可选：本章希望读者主要感受到的情绪")),
		schema.Property("payoff_points", schema.Array("可选：关键章希望回应的情节点或兑现点", schema.String(""))),
		schema.Property("hook_goal", schema.String("可选：章末希望驱动的追读欲望或悬念目标")),
		schema.Property("scene_anchors", schema.Array("可选：本章要反复使用并承担信息、关系或代价的现场物件、痕迹、动作证据", schema.String(""))),
		schema.Property("causal_simulation", causalSimulationSchema(true)),
	)
}

func (t *PlanChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	// 与两阶段互通：若该章已有 plan_details 建立的中间态（partial），把本次 plan_chapter
	// 的字段合并进 partial 后按同一口径 finalize。这样即使 planner 在单发/两阶段之间
	// 混用（弱模型在压缩后常"忘了"自己在走两阶段），也能用已累积的字段收口，
	// 而不是要求单发一次性给全导致"缺字段"+超大请求。
	if merged, handled, err := t.tryFinalizeFromPartial(args); handled {
		return merged, err
	}
	plan, err := decodeChapterPlanArgs(args)
	if err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if plan.Chapter <= 0 {
		plan.Chapter = inProgressChapterOf(t.store)
	}
	// 无 partial 的单发路径：核心字段缺失时给明确指引（schema 已不强制）。
	if strings.TrimSpace(plan.Goal) == "" || strings.TrimSpace(plan.Conflict) == "" || strings.TrimSpace(plan.Hook) == "" {
		return nil, fmt.Errorf("plan_chapter 单发需给出 goal/conflict/hook（或改用两阶段 plan_structure+plan_details）: %w", errs.ErrToolArgs)
	}
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, plan.Chapter)
	if err != nil || skipped != nil {
		return skipped, err
	}
	return finalizeChapterPlan(t.store, plan, isRewritePlan)
}

// inProgressChapterOf 从进度推断当前正在推演的章号（in_progress 优先，回退下一待写章）。
func inProgressChapterOf(s *store.Store) int {
	if target, ok := pendingRewriteTarget(s); ok {
		return target
	}
	progress, err := s.Progress.Load()
	if err != nil || progress == nil {
		return 0
	}
	if progress.InProgressChapter > 0 {
		return progress.InProgressChapter
	}
	return progress.NextChapter()
}

// tryFinalizeFromPartial 若目标章已有两阶段 partial，则把本次 plan_chapter 的字段并入
// partial 后 finalize，实现单发/两阶段互通。返回 (结果, 是否已处理, err)。
// 无 partial 时返回 handled=false，走普通单发路径。
func (t *PlanChapterTool) tryFinalizeFromPartial(args json.RawMessage) (json.RawMessage, bool, error) {
	var callMap map[string]any
	if err := unmarshalToolArgs(args, &callMap); err != nil {
		return nil, false, nil
	}
	chapter := intFromAny(callMap["chapter"])
	if chapter <= 0 {
		chapter = inProgressChapterOf(t.store)
	}
	if chapter <= 0 {
		return nil, false, nil
	}
	partial, err := t.store.Drafts.LoadChapterPlanPartial(chapter)
	if err != nil || partial == nil {
		return nil, false, nil
	}
	structure, _ := partial["structure"].(map[string]any)
	if structure == nil {
		structure = map[string]any{}
	}
	for k, v := range callMap {
		if k != "causal_simulation" {
			structure[k] = v // 本次单发的顶层字段覆盖 partial 中的骨架字段
		}
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	if cs, ok := callMap["causal_simulation"].(map[string]any); ok {
		maps.Copy(merged, cs)
	}
	full := make(map[string]any, len(structure)+2)
	maps.Copy(full, structure)
	full["chapter"] = chapter
	full["causal_simulation"] = merged
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, false, nil
	}
	plan, err := decodeChapterPlanArgs(raw)
	if err != nil {
		return nil, true, fmt.Errorf("invalid merged plan: %w: %w", errs.ErrToolArgs, err)
	}
	skipped, isRewritePlan, err := ensureChapterPlannable(t.store, chapter)
	if err != nil || skipped != nil {
		return skipped, true, err
	}
	result, err := finalizeChapterPlan(t.store, plan, isRewritePlan)
	if err != nil {
		return nil, true, err
	}
	_ = t.store.Drafts.DeleteChapterPlanPartial(chapter)
	return result, true, nil
}

// ensureChapterPlannable 跑规划前置门禁：重启口径、完成态、队列、弧扩展。
// 已完成章节返回 skipped 响应；plan_chapter 与 plan_structure 共用。
func ensureChapterPlannable(s *store.Store, chapter int) (skipped json.RawMessage, isRewritePlan bool, err error) {
	if chapter <= 0 {
		return nil, false, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, false, err
	}
	if err := ensureSimulationRestartProgressReady(s, progress); err != nil {
		return nil, false, err
	}
	isRewritePlan = progress != nil && slices.Contains(progress.PendingRewrites, chapter)
	if s.Progress.IsChapterCompleted(chapter) && !isRewritePlan {
		resp, mErr := json.Marshal(map[string]any{
			"chapter":   chapter,
			"skipped":   true,
			"completed": true,
			"reason":    fmt.Sprintf("第 %d 章已提交完成，不能重新规划", chapter),
		})
		return resp, isRewritePlan, mErr
	}
	if err := s.Progress.ValidateChapterWork(chapter); err != nil {
		return nil, isRewritePlan, err
	}
	if err := EnsureChapterExpanded(s, chapter); err != nil {
		return nil, isRewritePlan, err
	}
	return nil, isRewritePlan, nil
}

// finalizeChapterPlan 完整校验并落盘章节计划：写 plan 文件、置章节 in_progress、
// 记 checkpoint、透出事件编织冲突。plan_chapter 单发与 plan_details finalize 共用。
func finalizeChapterPlan(s *store.Store, plan domain.ChapterPlan, isRewritePlan bool) (json.RawMessage, error) {
	applyOutlineAnchorsToPlan(s, &plan)
	if err := applyRewriteAnchorsToPlan(s, &plan); err != nil {
		return nil, err
	}
	normalizeChapterAttractionPlan(s, &plan)
	plan.Contract.RequiredBeats = compactStrings(plan.Contract.RequiredBeats)
	if len(plan.Contract.RequiredBeats) > 4 {
		return nil, fmt.Errorf("第 %d 章 required_beats=%d，正文显性结果最多 4 项；请把同一选择、兑现或关系变化合并，离屏角色决定留在 causal_simulation: %w",
			plan.Chapter, len(plan.Contract.RequiredBeats), errs.ErrToolPrecondition)
	}
	if err := validateLeanPOVPlan(plan); err != nil {
		return nil, err
	}
	if err := validateChapterPrewriteSimulation(s, plan, isRewritePlan); err != nil {
		return nil, err
	}
	if issues := ChapterPlanIdentityIssues(s, plan.Chapter, plan); len(issues) > 0 {
		return nil, fmt.Errorf("第 %d 章计划身份锚点不合格：%s: %w", plan.Chapter, strings.Join(issues, "；"), errs.ErrToolPrecondition)
	}
	if err := validateProjectContaminationFinal(s, "chapter plan", plan); err != nil {
		return nil, err
	}

	// plan 收尾一致性检查：计划是正文的唯一范围依据，收尾前先与本书既定事实对齐。
	// hard 矛盾挡下 finalize 让 planner 修正；warn 疑点透出并落盘供 drafter 正文阶段核对。
	hardIssues, warnIssues := checkChapterPlanConsistency(s, plan)
	if len(hardIssues) > 0 {
		return nil, fmt.Errorf("第 %d 章计划一致性检查未过，请修正后重新收尾计划：\n- %s: %w",
			plan.Chapter, strings.Join(hardIssues, "\n- "), errs.ErrToolPrecondition)
	}

	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		return nil, fmt.Errorf("save chapter plan: %w", err)
	}
	if len(warnIssues) > 0 {
		_ = s.Drafts.SaveChapterPlanConsistencyWarnings(plan.Chapter, warnIssues)
	}
	if !isRewritePlan {
		if err := s.Progress.StartChapter(plan.Chapter); err != nil {
			return nil, fmt.Errorf("mark chapter in progress: %w", err)
		}
	}

	if _, err := s.Checkpoints.AppendArtifact(
		domain.ChapterScope(plan.Chapter), "plan",
		fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint chapter plan: %w", err)
	}

	nextStep := "立即调用 draft_chapter(chapter=本章节号, content=完整正文字符串) 写入正文，不要重复规划同一章"
	if isRewritePlan {
		nextStep = "这是待返工章节的新推演计划；先读取 rewrite_brief/审阅结论和原终稿，再按问题范围选择 edit_chapter 或 draft_chapter 覆盖重写，完成后必须 check_consistency 和 commit_chapter"
	}
	result := map[string]any{
		"planned":   true,
		"chapter":   plan.Chapter,
		"rewrite":   isRewritePlan,
		"next_step": nextStep,
	}
	if len(warnIssues) > 0 {
		result["consistency_warnings"] = warnIssues
		result["consistency_warnings_usage"] = "计划一致性疑点：正文阶段 drafter 会在 check_consistency 里看到这些点并须逐一核对；如是笔误请现在就改计划"
	}
	// Task 078：plan 与事件编织表冲突时透出警告（软约束：改排要在 notes 说明理由）。
	if weave, err := s.WorldSim.LoadEventWeave(); err == nil && weave != nil {
		if conflicts := weave.PlanWeaveConflicts(plan.Chapter, plan.AdvanceEvents); len(conflicts) > 0 {
			result["weave_conflicts"] = conflicts
		}
	}
	return json.Marshal(result)
}

func applyRewriteAnchorsToPlan(s *store.Store, plan *domain.ChapterPlan) error {
	if plan == nil {
		return nil
	}
	source, _, _, err := loadChapterRewriteSource(s, plan.Chapter)
	if err != nil || source == nil {
		return err
	}
	plan.Contract.ContinuityChecks = appendUniqueString(plan.Contract.ContinuityChecks,
		fmt.Sprintf("局部返工源正文 %s 的 sha256 必须保持为 %s；若正文源已变化，本计划作废并重新推演。preserve_constraints 保护世界事实，不要求复刻旧场景、旧顺序或全部出场人物。", source.BodyPath, source.BodySHA256))
	return nil
}

func applyOutlineAnchorsToPlan(s *store.Store, plan *domain.ChapterPlan) {
	if s == nil || plan == nil || plan.Chapter <= 0 {
		return
	}
	entry, err := s.Outline.GetChapterOutline(plan.Chapter)
	if err != nil || entry == nil {
		return
	}
	if title := strings.TrimSpace(entry.Title); title != "" {
		plan.Title = title
	}
	if event := strings.TrimSpace(entry.CoreEvent); event != "" {
		plan.Goal = "完整兑现本章大纲核心事件：" + event
	}
	if decision := strings.TrimSpace(plan.CausalSimulation.ProtagonistDecision); decision != "" &&
		strings.TrimSpace(plan.CausalSimulation.WorldSimulationID) != "" {
		plan.Goal = "落实本轮世界模拟后的主角选择：" + decision
	}
	if hook := strings.TrimSpace(entry.Hook); hook != "" {
		plan.Hook = hook
	}
}

func appendUniqueString(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(items, value) {
		return items
	}
	return append(items, value)
}

func ensureSimulationRestartProgressReady(s *store.Store, progress *domain.Progress) error {
	policy, err := s.LoadSimulationRestartPolicy()
	if err != nil || policy == nil || !policy.Active {
		return err
	}
	want := strings.TrimSpace(policy.GenerationID)
	got := ""
	if progress != nil {
		got = strings.TrimSpace(progress.GenerationID)
	}
	if want == "" || got == want {
		return nil
	}
	return fmt.Errorf("当前项目处于推演重启模式 generation_id=%s，但 meta/progress.json 仍属于旧活动线 generation_id=%q；旧章节/旧资源只能作背景种子。请先运行 --zero-init --reset-simulation-state 切换活动进度，再从第1章重新推演: %w", want, got, errs.ErrToolPrecondition)
}

func decodeChapterPlanArgs(args json.RawMessage) (domain.ChapterPlan, error) {
	var a struct {
		Chapter          int                            `json:"chapter"`
		Title            string                         `json:"title"`
		Goal             string                         `json:"goal"`
		Conflict         string                         `json:"conflict"`
		Hook             string                         `json:"hook"`
		EmotionArc       string                         `json:"emotion_arc"`
		Notes            string                         `json:"notes"`
		RequiredBeats    []string                       `json:"required_beats"`
		ForbiddenMoves   []string                       `json:"forbidden_moves"`
		ContinuityChecks []string                       `json:"continuity_checks"`
		EvaluationFocus  []string                       `json:"evaluation_focus"`
		EmotionTarget    string                         `json:"emotion_target"`
		PayoffPoints     []string                       `json:"payoff_points"`
		HookGoal         string                         `json:"hook_goal"`
		SceneAnchors     []string                       `json:"scene_anchors"`
		CausalSimulation domain.ChapterCausalSimulation `json:"causal_simulation"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		// 形状回退：LLM 把数组写成对象/标量时按目标类型纠正后重试（causal_beats、
		// risk_signals 等 []T 字段的高频失误），仍失败才把原始错误交回让 LLM 自修。
		cleaned := json.RawMessage(stripJSONWrapping(string(args)))
		if coerced, changed := coerceJSONShape(cleaned, reflect.TypeOf(a)); changed {
			if err2 := json.Unmarshal(coerced, &a); err2 != nil {
				// 形状纠正后仍失败：回传纠正后的错误（更准确地指向真正未修复的字段），
				// 而不是纠正前的首个错误，避免把 LLM 引向已被自动修复的字段。
				return domain.ChapterPlan{}, err2
			}
		} else {
			return domain.ChapterPlan{}, err
		}
	}

	return domain.ChapterPlan{
		Chapter:    a.Chapter,
		Title:      a.Title,
		Goal:       a.Goal,
		Conflict:   a.Conflict,
		Hook:       a.Hook,
		EmotionArc: a.EmotionArc,
		Notes:      a.Notes,
		Contract: domain.ChapterContract{
			RequiredBeats:    a.RequiredBeats,
			ForbiddenMoves:   a.ForbiddenMoves,
			ContinuityChecks: a.ContinuityChecks,
			EvaluationFocus:  a.EvaluationFocus,
			EmotionTarget:    a.EmotionTarget,
			PayoffPoints:     a.PayoffPoints,
			HookGoal:         a.HookGoal,
			SceneAnchors:     a.SceneAnchors,
		},
		CausalSimulation: a.CausalSimulation,
	}, nil
}

// ChapterPlanIdentityIssues catches template-like planning before it reaches
// draft rendering. Any project with a character roster gets the guard; tests
// and empty scaffolds without characters remain unaffected.
func ChapterPlanIdentityIssues(s *store.Store, chapter int, payload any) []string {
	if !chapterIdentityGuardActive(s) {
		return nil
	}
	var issues []string
	names := knownCharacterNameSet(s)
	// The companion system is an explicitly requested speaking entity, not a
	// human cast member. Keep it out of characters.json while allowing its
	// voice plan only for projects whose user rules actually request one.
	if planSystemEntityAllowed(s) {
		names["系统"] = struct{}{}
	}
	required := requiredDossierCharacterNames(s, chapter)
	if len(required) == 0 {
		for name := range names {
			required = append(required, name)
		}
	}
	text := strings.Join(flattenPlanStrings(payload), "\n")
	for _, placeholder := range []string{
		"既定主角", "既定地点", "本章触发事件",
		"current_chapter_outline 规定", "current_chapter_outline 指定",
	} {
		if strings.Contains(text, placeholder) {
			issues = append(issues, fmt.Sprintf("计划仍含模板占位 %q，必须改写为当前章大纲中的人物、地点、物件和事件", placeholder))
		}
	}
	if len(required) > 0 && !containsAnyLiteral(text, required) {
		issues = append(issues, fmt.Sprintf("计划文本未出现本章角色实名（需要至少命中：%s），不能用“主角/关键对话对象/后台关联者”占位", strings.Join(required, "、")))
	}
	if entry, err := s.Outline.GetChapterOutline(chapter); err == nil && entry != nil {
		expected := strings.TrimSpace(entry.Title)
		actual := planPayloadTitle(payload)
		if expected != "" && actual != expected {
			issues = append(issues, fmt.Sprintf("计划标题 %q 与大纲标题 %q 不一致，必须原样使用大纲标题", actual, expected))
		}
	}
	if bad := characterFieldIdentityIssues(payload, names, inferCommitProtagonist(s), projectHasFemaleProtagonist(s)); len(bad) > 0 {
		issues = append(issues, bad...)
	}
	if rewriteTarget, ok := pendingRewriteTarget(s); ok && rewriteTarget == chapter {
		issues = append(issues, visibleCharacterOutlineScopeIssues(s, chapter, payload)...)
	}
	return compactStrings(issues)
}

func planSystemEntityAllowed(s *store.Store) bool {
	if s == nil {
		return false
	}
	snapshot, err := s.UserRules.Load()
	if err != nil || snapshot == nil {
		return false
	}
	source := snapshot.Structured.Genre + "\n" + snapshot.Preferences
	if domain.SystemCompanionVoiceRequested(source) {
		return true
	}
	for _, rel := range []string{"world_rules.json", filepath.Join("meta", "world_foundation.json")} {
		raw, readErr := os.ReadFile(filepath.Join(s.Dir(), rel))
		if readErr == nil && domain.SystemCompanionVoiceRequested(string(raw)) {
			return true
		}
	}
	return false
}

func visibleCharacterOutlineScopeIssues(s *store.Store, chapter int, payload any) []string {
	entry, err := s.Outline.GetChapterOutline(chapter)
	if err != nil || entry == nil {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, name := range chapterOutlineCharacterNames(s, chapter) {
		allowed[name] = struct{}{}
	}
	chars, _ := s.Characters.Load()
	roles := make(map[string]string, len(chars))
	for _, character := range chars {
		roles[character.Name] = character.Role
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	sim, _ := root["causal_simulation"].(map[string]any)
	if sim == nil {
		return nil
	}
	stages, _ := sim["offscreen_character_stage"].([]any)
	var issues []string
	for index, item := range stages {
		stage, _ := item.(map[string]any)
		if stage == nil || stage["visible_in_chapter"] != true {
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
		role := strings.TrimSpace(roles[name])
		if role == "" {
			role = "未登记职责"
		}
		issues = append(issues, fmt.Sprintf(
			"causal_simulation.offscreen_character_stage[%d] 将 %s 标为本章可见，但 current_chapter_outline 未授权该角色出场，且其职责为 %q；改为 visible_in_chapter=false，或使用本章大纲明确的人物",
			index, name, role,
		))
	}
	return issues
}

func chapterIdentityGuardActive(s *store.Store) bool {
	chars, err := s.Characters.Load()
	return err == nil && len(chars) > 0
}

func planPayloadTitle(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	if title, _ := root["title"].(string); strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	if structure, _ := root["structure"].(map[string]any); structure != nil {
		title, _ := structure["title"].(string)
		return strings.TrimSpace(title)
	}
	return ""
}

func projectHasFemaleProtagonist(s *store.Store) bool {
	data, err := os.ReadFile(filepath.Join(s.Dir(), "premise.md"))
	if err != nil {
		return false
	}
	text := string(data)
	return strings.Contains(text, "女频") ||
		strings.Contains(text, "女性主角") ||
		strings.Contains(text, "主角为女性") ||
		strings.Contains(text, "主角是女性")
}

func knownCharacterNameSet(s *store.Store) map[string]struct{} {
	names := map[string]struct{}{}
	chars, err := s.Characters.Load()
	if err != nil {
		return names
	}
	for _, c := range chars {
		name := strings.TrimSpace(c.Name)
		if name != "" {
			names[name] = struct{}{}
		}
		for _, alias := range c.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				names[alias] = struct{}{}
			}
		}
	}
	if cast, err := s.Cast.RecentActive(200); err == nil {
		for _, entry := range cast {
			if name := strings.TrimSpace(entry.Name); name != "" {
				names[name] = struct{}{}
			}
			for _, alias := range entry.Aliases {
				if alias = strings.TrimSpace(alias); alias != "" {
					names[alias] = struct{}{}
				}
			}
		}
	}
	return names
}

func flattenPlanStrings(v any) []string {
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch vv := x.(type) {
		case string:
			if s := strings.TrimSpace(vv); s != "" {
				out = append(out, s)
			}
		case map[string]any:
			for _, item := range vv {
				walk(item)
			}
		case []any:
			for _, item := range vv {
				walk(item)
			}
		default:
			raw, err := json.Marshal(vv)
			if err != nil || len(raw) == 0 || string(raw) == "null" {
				return
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err == nil && decoded != nil && !reflect.DeepEqual(decoded, vv) {
				walk(decoded)
			}
		}
	}
	walk(v)
	return out
}

func containsAnyLiteral(text string, needles []string) bool {
	for _, needle := range needles {
		if needle = strings.TrimSpace(needle); needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func characterFieldIdentityIssues(v any, names map[string]struct{}, protagonist string, femaleProtagonist bool) []string {
	var issues []string
	var walk func(string, any)
	walk = func(path string, x any) {
		switch vv := x.(type) {
		case map[string]any:
			if rawName, ok := vv["character"].(string); ok {
				name := strings.TrimSpace(rawName)
				if name != "" && name != "none" {
					switch {
					case isGenericCharacterPlaceholder(name):
						issues = append(issues, fmt.Sprintf("%s.character=%q 是模板占位，必须改为 characters.json 里的角色实名", path, name))
					case isAllowedNonCastPlanEntity(path, name, names):
						// A companion system needs a voice card, but is not a social
						// actor and must stay out of the all-character world simulation.
					case len(names) > 0:
						if _, ok := names[name]; !ok {
							issues = append(issues, fmt.Sprintf("%s.character=%q 不在角色册中，计划阶段不能用泛称或临时身份代替实名", path, name))
						}
					}
					if femaleProtagonist && protagonist != "" && name == protagonist && hasLikelyMalePronoun(vv) {
						issues = append(issues, fmt.Sprintf("%s 以女性主角 %s 为主体却出现疑似男性代词，请改成“她”或角色实名", path, protagonist))
					}
				}
			}
			for key, item := range vv {
				next := key
				if path != "" {
					next = path + "." + key
				}
				walk(next, item)
			}
		case []any:
			for i, item := range vv {
				walk(fmt.Sprintf("%s[%d]", path, i), item)
			}
		default:
			raw, err := json.Marshal(vv)
			if err != nil || len(raw) == 0 || string(raw) == "null" {
				return
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err == nil && decoded != nil && !reflect.DeepEqual(decoded, vv) {
				walk(path, decoded)
			}
		}
	}
	walk("plan", v)
	return compactStrings(issues)
}

func isAllowedNonCastPlanEntity(path, name string, names map[string]struct{}) bool {
	_, companionRequested := names["系统"]
	return companionRequested && strings.Contains(path, ".voice_logic[") && strings.Contains(name, "系统")
}

func isGenericCharacterPlaceholder(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	exact := map[string]struct{}{
		"主角":      {},
		"女主":      {},
		"男主":      {},
		"主人公":     {},
		"关键对话对象":  {},
		"制度资源控制者": {},
		"关键对话对象/制度资源控制者": {},
		"后台关联者":          {},
		"休眠关键角色":         {},
		"后台关联者/休眠关键角色":   {},
		"媒介/第三方":         {},
	}
	if _, ok := exact[name]; ok {
		return true
	}
	return strings.Contains(name, "关键对话对象") ||
		strings.Contains(name, "后台关联者") ||
		strings.Contains(name, "休眠关键角色") ||
		strings.Contains(name, "制度资源控制者")
}

func hasLikelyMalePronoun(v any) bool {
	text := strings.Join(flattenPlanStrings(v), "\n")
	for _, pattern := range []string{
		"他必须", "他会", "他在", "他先", "他不能", "他的", "他不", "他把",
		"他从", "他没有", "他已经", "他想", "他需要", "他站", "他按", "他删",
		"他发现", "他拿", "他问", "他接受", "他最", "按他的", "让他",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func compactStrings(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func validateChapterPrewriteSimulation(s *store.Store, plan domain.ChapterPlan, rewrite bool) error {
	sim := plan.CausalSimulation
	if !hasChapterCausalSimulation(sim) {
		return fmt.Errorf("第 %d 章缺少写前 causal_simulation：正文写作必须先推演前文/时间线/卷弧/未来窗口、角色状态、资源账本和 AI 味风险: %w", plan.Chapter, errs.ErrToolPrecondition)
	}
	if err := validateChapterWorldSimulationReference(s, plan); err != nil {
		return err
	}

	var missing []string
	require := func(ok bool, field string) {
		if !ok {
			missing = append(missing, field)
		}
	}
	hasSource := func(needles ...string) bool {
		return contextSourcesContain(sim.ContextSources, needles...)
	}
	restartPolicy, restartErr := s.LoadSimulationRestartPolicy()
	restartActive := restartErr == nil && restartPolicy != nil && restartPolicy.Active

	require(strings.TrimSpace(sim.ProjectPromise) != "", "causal_simulation.project_promise")
	require(strings.TrimSpace(sim.ChapterFunction) != "", "causal_simulation.chapter_function")
	require(len(sim.ContextSources) > 0, "causal_simulation.context_sources")
	require(len(sim.InitialState) > 0, "causal_simulation.initial_state")
	require(len(sim.CausalBeats) > 0, "causal_simulation.causal_beats")
	require(len(sim.DecisionPoints) > 0, "causal_simulation.decision_points")
	require(len(sim.OutcomeShift) > 0, "causal_simulation.outcome_shift")
	require(len(sim.VoiceLogic) > 0, "causal_simulation.voice_logic")
	if len(sim.TrendLanguage) > 0 {
		require(domain.CompleteTrendLanguagePlan(sim.TrendLanguage), "causal_simulation.trend_language_plan")
		if problems := domain.TrendLanguagePlanProblems(sim.TrendLanguage); len(problems) > 0 {
			require(false, "causal_simulation.trend_language_plan(semantic_usage: "+strings.Join(problems, " | ")+")")
		}
	}
	if len(sim.EntertainmentPlan.HumorBeats) > 0 || strings.TrimSpace(sim.EntertainmentPlan.OpeningBeat) != "" {
		require(domain.CompleteReaderEntertainmentPlan(sim.EntertainmentPlan), "causal_simulation.reader_entertainment_plan")
	}
	if len(sim.AntiAIPlan.RiskSignals) > 0 || len(sim.AntiAIPlan.CounterMoves) > 0 {
		require(hasFocusedAntiAIExecutionPlan(sim.AntiAIPlan), "causal_simulation.anti_ai_execution_plan")
	}
	if attractionRequirementsForChapter(s, plan.Chapter).SystemCompanion &&
		(len(sim.EntertainmentPlan.HumorBeats) > 0 || len(sim.AntiAIPlan.RiskSignals) > 0) {
		if problems := domain.SystemCompanionPlanProblems(sim); len(problems) > 0 {
			require(false, "causal_simulation.reader_entertainment_plan(system_companion_voice: 必须写系统接话/吐槽/解闷且始终支持主角；同时从anti_ai_execution_plan和forbidden_comedy删除反向句；当前问题="+strings.Join(problems, " | ")+")")
		}
	}
	// 轻松大众题材不把资料/装备/视觉/读者奖励矩阵设为硬卡点；这些字段存在时仍校验来源，
	// 缺失交给 Editor/AI 味审核和正文阶段处理，避免第一章计划被方法论表格拖死。
	for i, vd := range sim.VisualDesign {
		require(strings.TrimSpace(vd.MaterialSource) != "", fmt.Sprintf("causal_simulation.visual_design[%d].material_source", i))
	}
	for i, kit := range sim.CharacterKit {
		prefix := fmt.Sprintf("causal_simulation.character_kit[%d]", i)
		require(strings.TrimSpace(kit.Character) != "", prefix+".character")
		require(strings.TrimSpace(kit.CodexCompliance) != "", prefix+".codex_compliance")
		if kit.FirstAppearance {
			require(strings.TrimSpace(kit.AppearanceRef) != "", prefix+".appearance_ref(first_appearance)")
		}
		for j, item := range kit.Weapons {
			require(strings.TrimSpace(item.MaterialSource) != "", fmt.Sprintf("%s.weapons[%d].material_source", prefix, j))
		}
		for j, item := range kit.Equipment {
			require(strings.TrimSpace(item.MaterialSource) != "", fmt.Sprintf("%s.equipment[%d].material_source", prefix, j))
		}
		for j, item := range kit.Skills {
			require(strings.TrimSpace(item.MaterialSource) != "", fmt.Sprintf("%s.skills[%d].material_source", prefix, j))
		}
		for j, ability := range kit.Abilities {
			aprefix := fmt.Sprintf("%s.abilities[%d]", prefix, j)
			require(strings.TrimSpace(ability.Name) != "", aprefix+".name")
			require(strings.TrimSpace(ability.CodexTier) != "", aprefix+".codex_tier")
			require(strings.TrimSpace(ability.CurrentLevel) != "", aprefix+".current_level")
			require(strings.TrimSpace(ability.UsageScope) != "", aprefix+".usage_scope")
			require(strings.TrimSpace(ability.MaterialSource) != "", aprefix+".material_source")
		}
	}
	if rewrite {
		require(hasReviewRefinementLoop(sim.ReviewRefinement), "causal_simulation.review_refinement")
		require(hasSource("rewrite_brief", "review"), "causal_simulation.context_sources(rewrite_brief/review)")
		rewriteSource, _, _, rewriteErr := loadChapterRewriteSource(s, plan.Chapter)
		if rewriteErr != nil {
			return rewriteErr
		}
		if rewriteSource != nil {
			require(hasSource(rewriteSourceToken(rewriteSource)), "causal_simulation.context_sources("+rewriteSourceToken(rewriteSource)+")")
			require(hasSource(rewriteBriefToken(rewriteSource)), "causal_simulation.context_sources("+rewriteBriefToken(rewriteSource)+")")
			for i, fact := range rewriteSource.PreserveFacts {
				require(factCoveredByConstraints(fact, sim.ReviewRefinement.PreserveConstraints),
					fmt.Sprintf("causal_simulation.review_refinement.preserve_constraints[%d]=%s", i, fact))
			}
		}
	} else {
		if restartActive {
			require(hasSource("simulation_restart_policy", "restart_policy", "generation_id"), "causal_simulation.context_sources(simulation_restart_policy)")
		}
		require(hasSource("world_foundation", "world_iron_law", "past_timeline"), "causal_simulation.context_sources(world_foundation)")
		require(hasSource("character_dossiers", "character_dossier", "role_dossier"), "causal_simulation.context_sources(character_dossiers)")
		require(hasSource("current_chapter_outline", "outline"), "causal_simulation.context_sources(current_chapter_outline/outline)")
		require(hasSource("progression", "chapter_progress", "chapter_contract"), "causal_simulation.context_sources(progression/chapter_contract)")
	}

	for i, state := range sim.InitialState {
		prefix := fmt.Sprintf("causal_simulation.initial_state[%d]", i)
		require(state.Character != "", prefix+".character")
		require(state.CurrentGoal != "", prefix+".current_goal")
		require(state.Pressure != "", prefix+".pressure")
		require(state.ActionTendency != "", prefix+".action_tendency")
	}
	for i, stage := range sim.OffscreenStage {
		prefix := fmt.Sprintf("causal_simulation.offscreen_character_stage[%d]", i)
		require(stage.Character != "", prefix+".character")
		require(stage.Location != "", prefix+".location")
		require(stage.CurrentAction != "", prefix+".current_action")
		require(stage.Decision != "", prefix+".decision")
		require(stage.KnowledgeBoundary != "", prefix+".knowledge_boundary")
		require(stage.TimelineConsistency != "", prefix+".timeline_consistency")
	}
	protagonist := inferCommitProtagonist(s)
	protagonistOnly := compactStrings([]string{protagonist})
	if missingCharacters := missingInitialStateCoverage(protagonistOnly, sim.InitialState); len(missingCharacters) > 0 {
		missing = append(missing, formatMissingCharacterCoverage("causal_simulation.initial_state", missingCharacters))
	}
	if missingCharacters := missingEmotionalLogicCoverage(protagonistOnly, sim.EmotionalLogic); len(missingCharacters) > 0 {
		missing = append(missing, formatMissingCharacterCoverage("causal_simulation.emotional_logic", missingCharacters))
	}
	if protagonist != "" && !voiceLogicContainsCharacter(sim.VoiceLogic, protagonist) {
		missing = append(missing, formatMissingCharacterCoverage("causal_simulation.voice_logic", []string{protagonist}))
	}
	for i, arc := range sim.CharacterArcTests {
		prefix := fmt.Sprintf("causal_simulation.character_arc_tests[%d]", i)
		require(arc.Character != "", prefix+".character")
		require(arc.Want != "", prefix+".want")
		require(arc.CoreLie != "", prefix+".core_lie")
		require(arc.Need != "", prefix+".need")
		require(arc.Truth != "", prefix+".truth")
		require(arc.PressureTest != "", prefix+".pressure_test")
		require(arc.FirstMistake != "", prefix+".first_mistake")
		require(arc.CorrectionSignal != "", prefix+".correction_signal")
		require(arc.ChapterEvidence != "", prefix+".chapter_evidence")
	}
	for i, voice := range sim.VoiceLogic {
		prefix := fmt.Sprintf("causal_simulation.voice_logic[%d]", i)
		require(voice.Character != "", prefix+".character")
		require(voice.SceneObjective != "", prefix+".scene_objective")
		require(voice.HiddenSubtext != "", prefix+".hidden_subtext")
		require(voice.KnowledgeBoundary != "", prefix+".knowledge_boundary")
		require(voice.DictionAndRhythm != "", prefix+".diction_and_rhythm")
		require(len(voice.DialogueFunctions) > 0, prefix+".dialogue_functions")
		require(len(voice.ForbiddenMoves) > 0, prefix+".forbidden_moves")
	}
	for i, blueprint := range sim.DialogueBlueprints {
		prefix := fmt.Sprintf("causal_simulation.dialogue_scene_blueprints[%d]", i)
		require(blueprint.SceneID != "", prefix+".scene_id")
		require(blueprint.DialogueMode != "", prefix+".dialogue_mode")
		require(blueprint.ScenePressure != "", prefix+".scene_pressure")
		require(blueprint.RelationshipFrame != "", prefix+".relationship_frame")
		require(blueprint.LocationAnchor != "", prefix+".location_anchor")
		require(blueprint.DialogueObjective != "", prefix+".dialogue_objective")
		require(len(blueprint.TurnProgression) > 0, prefix+".turn_progression")
		require(blueprint.ExitBeat != "", prefix+".exit_beat")
		for j, turn := range blueprint.TurnProgression {
			turnPrefix := fmt.Sprintf("%s.turn_progression[%d]", prefix, j)
			require(turn.Speaker != "", turnPrefix+".speaker")
			require(turn.SurfaceLineFunction != "", turnPrefix+".surface_line_function")
			require(turn.NewInformation != "" || turn.PowerMove != "", turnPrefix+".new_information/power_move")
			require(turn.NextPressure != "", turnPrefix+".next_pressure")
		}
	}
	for i, chain := range sim.EvidenceChains {
		prefix := fmt.Sprintf("causal_simulation.evidence_return_chains[%d]", i)
		require(chain.OffscreenCharacter != "", prefix+".offscreen_character")
		require(chain.Event != "", prefix+".event")
		require(chain.Evidence != "", prefix+".evidence")
		require(chain.ProtagonistAccess != "", prefix+".protagonist_access")
		require(chain.ReturnTiming != "", prefix+".return_timing")
		require(chain.DistortionOrMisread != "", prefix+".distortion_or_misread")
	}
	for i, dormant := range sim.DormantPolicy {
		prefix := fmt.Sprintf("causal_simulation.dormant_character_policy[%d]", i)
		require(dormant.Character != "", prefix+".character")
		require(dormant.Status != "", prefix+".status")
		require(dormant.Location != "", prefix+".location")
		require(dormant.NoChangeReason != "", prefix+".no_change_reason")
		require(dormant.TriggerCondition != "", prefix+".trigger_condition")
		require(dormant.KnowledgeBoundary != "", prefix+".knowledge_boundary")
		require(dormant.NextCheck != "", prefix+".next_check")
	}
	for i, support := range sim.RealitySupport {
		prefix := fmt.Sprintf("causal_simulation.reality_support_plan[%d]", i)
		require(support.Domain != "", prefix+".domain")
		require(support.SourceRef != "", prefix+".source_ref")
		require(support.UsableDetail != "", prefix+".usable_detail")
		require(support.TransformedAs != "", prefix+".transformed_as")
		require(support.ChapterUse != "", prefix+".chapter_use")
		require(len(support.ForbiddenDirectUse) > 0, prefix+".forbidden_direct_use")
	}
	for i, emo := range sim.EmotionalLogic {
		prefix := fmt.Sprintf("causal_simulation.emotional_logic[%d]", i)
		require(emo.Character != "", prefix+".character")
		require(emo.ImmediateState != "", prefix+".immediate_state")
		require(emo.PrimaryEmotion != "", prefix+".primary_emotion")
		require(emo.EmotionalTrigger != "", prefix+".emotional_trigger")
		require(emo.GoalAppraisal != "", prefix+".goal_appraisal")
		require(emo.RegulationStrategy != "", prefix+".regulation_strategy")
		require(emo.EmotionLedAction != "", prefix+".emotion_led_action")
		require(len(emo.EvidenceInScene) > 0, prefix+".evidence_in_scene")
	}
	for i, arc := range sim.RelationshipArcs {
		prefix := fmt.Sprintf("causal_simulation.relationship_emotion_arcs[%d]", i)
		require(len(arc.Pair) >= 2, prefix+".pair")
		require(arc.RelationshipType != "", prefix+".relationship_type")
		require(arc.CurrentBond != "", prefix+".current_bond")
		require(arc.EmotionalWant != "", prefix+".emotional_want")
		require(arc.Fear != "", prefix+".fear")
		require(arc.PowerBalance != "", prefix+".power_balance")
		require(arc.IntimacyStage != "", prefix+".intimacy_stage")
		require(arc.TrustDebt != "", prefix+".trust_debt")
		require(arc.ConflictTrigger != "", prefix+".conflict_trigger")
		require(arc.AttachmentOrLoveLanguage != "", prefix+".attachment_or_love_language")
		require(arc.Boundary != "", prefix+".boundary")
		require(arc.RomancePotential != "", prefix+".romance_potential")
		require(arc.NextEmotionalBeat != "", prefix+".next_emotional_beat")
		require(arc.ProtagonistKnowledgeBoundary != "", prefix+".protagonist_knowledge_boundary")
	}
	for i, visual := range sim.VisualDesign {
		prefix := fmt.Sprintf("causal_simulation.visual_design[%d]", i)
		require(visual.Character != "", prefix+".character")
		require(visual.Silhouette != "", prefix+".silhouette")
		require(visual.FaceAndHair != "", prefix+".face_and_hair")
		require(visual.ClothingStyle != "", prefix+".clothing_style")
		require(visual.ColorPalette != "", prefix+".color_palette")
		require(visual.BodyLanguage != "", prefix+".body_language")
		require(visual.SignatureObject != "", prefix+".signature_object")
		require(visual.FirstImpression != "", prefix+".first_impression")
		require(visual.StatusWear != "", prefix+".status_wear")
		require(visual.ChangeRule != "", prefix+".change_rule")
		require(visual.SceneUse != "", prefix+".scene_use")
		require(len(visual.DoNotUse) > 0, prefix+".do_not_use")
	}
	for i, info := range sim.InformationLedger {
		prefix := fmt.Sprintf("causal_simulation.information_asymmetry[%d]", i)
		require(info.Subject != "", prefix+".subject")
		require(len(info.ReaderKnows) > 0, prefix+".reader_knows")
		require(len(info.ProtagonistKnows) > 0, prefix+".protagonist_knows")
		require(len(info.CharacterKnows) > 0, prefix+".character_knows")
		require(len(info.CharacterMistakes) > 0, prefix+".character_mistakes")
		require(len(info.HiddenFromReader) > 0, prefix+".hidden_from_reader")
		require(info.RevealCondition != "", prefix+".reveal_condition")
		require(info.TensionFunction != "", prefix+".tension_function")
	}
	for i, hidden := range sim.HiddenRules {
		prefix := fmt.Sprintf("causal_simulation.hidden_rule_pressure[%d]", i)
		require(hidden.Domain != "", prefix+".domain")
		require(hidden.VisibleRule != "", prefix+".visible_rule")
		require(hidden.HiddenRule != "", prefix+".hidden_rule")
		require(hidden.CulturalNorm != "", prefix+".cultural_norm")
		require(hidden.WhoBenefits != "", prefix+".who_benefits")
		require(hidden.WhoPays != "", prefix+".who_pays")
		require(hidden.ViolationCost != "", prefix+".violation_cost")
		require(hidden.SceneEvidence != "", prefix+".scene_evidence")
	}
	for i, rumor := range sim.SocialMoodRumors {
		prefix := fmt.Sprintf("causal_simulation.social_mood_rumors[%d]", i)
		require(rumor.Group != "", prefix+".group")
		require(rumor.Mood != "", prefix+".mood")
		require(rumor.Rumor != "", prefix+".rumor")
		require(rumor.Source != "", prefix+".source")
		require(rumor.SpreadPath != "", prefix+".spread_path")
		require(rumor.Reliability != "", prefix+".reliability")
		require(rumor.BehaviorEffect != "", prefix+".behavior_effect")
		require(rumor.ProtagonistAccess != "", prefix+".protagonist_access")
	}
	for i, window := range sim.RitualCalendar {
		prefix := fmt.Sprintf("causal_simulation.ritual_calendar[%d]", i)
		require(window.Time != "", prefix+".time")
		require(window.CalendarType != "", prefix+".calendar_type")
		require(window.RitualOrDeadline != "", prefix+".ritual_or_deadline")
		require(window.SocialMeaning != "", prefix+".social_meaning")
		require(window.PracticalConstraint != "", prefix+".practical_constraint")
		require(window.EmotionalCharge != "", prefix+".emotional_charge")
		require(window.MissedCost != "", prefix+".missed_cost")
		require(window.SceneUse != "", prefix+".scene_use")
	}
	for i, resource := range sim.StructuralResources {
		prefix := fmt.Sprintf("causal_simulation.structural_resources[%d]", i)
		require(resource.Resource != "", prefix+".resource")
		require(resource.Controller != "", prefix+".controller")
		require(resource.ScarcityReason != "", prefix+".scarcity_reason")
		require(resource.AccessRule != "", prefix+".access_rule")
		require(resource.BlackMarketOrInformalPath != "", prefix+".black_market_or_informal_path")
		require(resource.PriceOrCost != "", prefix+".price_or_cost")
		require(resource.PowerEffect != "", prefix+".power_effect")
		require(resource.ChapterPressure != "", prefix+".chapter_pressure")
	}
	for i, check := range sim.CosmologyChecks {
		prefix := fmt.Sprintf("causal_simulation.cosmology_checks[%d]", i)
		require(check.Layer != "", prefix+".layer")
		require(check.Rule != "", prefix+".rule")
		require(check.Cost != "", prefix+".cost")
		require(check.Boundary != "", prefix+".boundary")
		require(check.ExceptionCondition != "", prefix+".exception_condition")
		require(check.Evidence != "", prefix+".evidence")
		require(check.FailureMode != "", prefix+".failure_mode")
	}
	for i, conflict := range sim.ConflictWeb {
		prefix := fmt.Sprintf("causal_simulation.conflict_web[%d]", i)
		require(len(conflict.Parties) >= 2, prefix+".parties")
		require(conflict.ConflictType != "", prefix+".conflict_type")
		require(conflict.OpenGoal != "", prefix+".open_goal")
		require(conflict.HiddenAgenda != "", prefix+".hidden_agenda")
		require(conflict.ResourceStake != "", prefix+".resource_stake")
		require(conflict.InformationGap != "", prefix+".information_gap")
		require(conflict.TimePressure != "", prefix+".time_pressure")
		require(conflict.CurrentBalance != "", prefix+".current_balance")
		require(conflict.Destabilizer != "", prefix+".destabilizer")
		require(conflict.NextEscalation != "", prefix+".next_escalation")
	}
	if len(missing) > 0 {
		return fmt.Errorf("第 %d 章写前推演不完整，缺少：%s: %w", plan.Chapter, strings.Join(missing, ", "), errs.ErrToolPrecondition)
	}
	return nil
}

// validateLeanPOVPlan applies only when Planner finalizes a new plan. Drafter
// may render an older, otherwise valid plan because the render packet already
// projects legacy detail down to the same compact surface. Revalidating these
// cardinality limits at draft time would strand approved plans after upgrades.
func validateLeanPOVPlan(plan domain.ChapterPlan) error {
	sim := plan.CausalSimulation
	type bound struct {
		field string
		got   int
		max   int
	}
	for _, item := range []bound{
		{field: "initial_state", got: len(sim.InitialState), max: 2},
		{field: "causal_beats", got: len(sim.CausalBeats), max: 4},
		{field: "decision_points", got: len(sim.DecisionPoints), max: 4},
		{field: "outcome_shift", got: len(sim.OutcomeShift), max: 4},
		{field: "voice_logic", got: len(sim.VoiceLogic), max: 4},
	} {
		if item.got > item.max {
			return fmt.Errorf("第 %d 章 causal_simulation.%s=%d，POV plan 最多 %d 项；请合并重复分析，全角色细节留在 chapter_world_simulation: %w",
				plan.Chapter, item.field, item.got, item.max, errs.ErrToolPrecondition)
		}
	}
	return nil
}

// dialogueAudienceAbsent 判断 audience_presence.present 是否表示无第三方在场。
func dialogueAudienceAbsent(present string) bool {
	p := strings.ToLower(strings.TrimSpace(present))
	return p == "" || p == "none" || p == "no" || strings.HasPrefix(p, "无")
}

func contextSourcesContain(sources []string, needles ...string) bool {
	for _, source := range sources {
		source = strings.ToLower(source)
		for _, needle := range needles {
			if strings.Contains(source, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func hasCollectedExternalReference(refs []domain.ExternalReferencePlan) bool {
	for _, ref := range refs {
		sourceType := strings.ToLower(strings.TrimSpace(ref.SourceType))
		if sourceType == "" || sourceType == "none" || sourceType == "not_applicable" || strings.Contains(sourceType, "_or_") {
			continue
		}
		retrievedAt := strings.ToLower(strings.TrimSpace(ref.RetrievedAt))
		sourceRefs := strings.ToLower(strings.Join(ref.SourceRefs, " "))
		if len(ref.SourceRefs) == 0 || strings.TrimSpace(ref.RetrievedAt) == "" ||
			strings.EqualFold(strings.TrimSpace(ref.RetrievedAt), "unknown") ||
			strings.Contains(retrievedAt, "zero-init") ||
			strings.Contains(retrievedAt, "未实时") ||
			strings.Contains(retrievedAt, "需要刷新") ||
			strings.Contains(sourceRefs, "if present") {
			continue
		}
		if strings.TrimSpace(ref.FreshnessRequirement) == "" ||
			strings.TrimSpace(ref.TransformationRule) == "" {
			continue
		}
		return true
	}
	return false
}

func hasWorldBackgroundLayers(layers domain.WorldBackgroundLayersPlan) bool {
	return strings.TrimSpace(layers.PhysicalSpace) != "" &&
		strings.TrimSpace(layers.TimeLayer) != "" &&
		strings.TrimSpace(layers.SocialInstitution) != "" &&
		strings.TrimSpace(layers.CulturalNorm) != "" &&
		strings.TrimSpace(layers.RelationshipNetwork) != "" &&
		strings.TrimSpace(layers.EconomicResource) != "" &&
		strings.TrimSpace(layers.ConflictTension) != "" &&
		strings.TrimSpace(layers.SocialMood) != "" &&
		strings.TrimSpace(layers.CosmologyMetaRule) != "" &&
		strings.TrimSpace(layers.NarrativeMeta) != "" &&
		strings.TrimSpace(layers.EventActivation) != ""
}

func hasNarrativeTensionMatrix(matrix domain.NarrativeTensionMatrix) bool {
	return strings.TrimSpace(matrix.StabilityTurbulence) != "" &&
		strings.TrimSpace(matrix.ExplicitHiddenRules) != "" &&
		strings.TrimSpace(matrix.InformationGap) != "" &&
		strings.TrimSpace(matrix.TimePressurePreparation) != "" &&
		strings.TrimSpace(matrix.WhyEventNow) != "" &&
		strings.TrimSpace(matrix.ReaderQuestion) != "" &&
		strings.TrimSpace(matrix.POVBoundary) != ""
}

func hasFocusedAntiAIExecutionPlan(plan domain.AntiAIExecutionPlan) bool {
	return len(plan.RiskSignals) > 0 &&
		len(plan.CounterMoves) > 0 &&
		strings.TrimSpace(plan.SentenceRhythmPolicy) != "" &&
		strings.TrimSpace(plan.DialogueFunctionPlan) != "" &&
		len(plan.ReviewChecks) > 0
}

func voiceLogicContainsCharacter(records []domain.CharacterVoiceLogic, character string) bool {
	character = strings.TrimSpace(character)
	for _, record := range records {
		if strings.TrimSpace(record.Character) == character {
			return true
		}
	}
	return false
}

func hasReaderRewardPlan(plan domain.ReaderRewardPlan) bool {
	return strings.TrimSpace(plan.FirstChapterSmallWin) != "" &&
		strings.TrimSpace(plan.NewDebtOrCost) != "" &&
		strings.TrimSpace(plan.PayoffVisibility) != "" &&
		len(plan.ForbiddenRewardPatterns) > 0
}

func hasReaderRetentionPlan(plan domain.ReaderRetentionPlan) bool {
	if len(plan.SurfaceBeats) == 0 || len(plan.CutOrCompress) == 0 || len(plan.PageTurnQuestions) == 0 {
		return false
	}
	for _, beat := range plan.SurfaceBeats {
		if strings.TrimSpace(beat.MustShow) != "" &&
			strings.TrimSpace(beat.ReaderPayoff) != "" &&
			strings.TrimSpace(beat.SceneVehicle) != "" {
			return true
		}
	}
	return false
}

func hasEndingConsequenceContract(contract domain.EndingConsequenceContract) bool {
	return strings.TrimSpace(contract.EndingMode) != "" &&
		strings.TrimSpace(contract.ConcreteAnchor) != "" &&
		strings.TrimSpace(contract.Consequence) != "" &&
		strings.TrimSpace(contract.NextChapterPull) != "" &&
		len(contract.ForbiddenEndings) > 0
}

// causalSimulationSchema 的分批路径只暴露正文真正依赖的紧凑契约，完整性由
// finalize 的服务端校验兜底。单发路径暂保留兼容 schema，旧调用不会失效。
func causalSimulationSchema(strict bool) map[string]any {
	if strict {
		return legacyCausalSimulationSchema(true)
	}
	return focusedCausalSimulationSchema()
}

func focusedCausalSimulationSchema() map[string]any {
	initialState := schema.Object(
		schema.Property("character", schema.String("角色实名")).Required(),
		schema.Property("current_goal", schema.String("开章时的具体目标")).Required(),
		schema.Property("pressure", schema.String("当前外部或内部压力")).Required(),
		schema.Property("action_tendency", schema.String("按当前信息最可能先做什么")).Required(),
		schema.Property("likely_action", schema.String("本章最可能采取的行动")),
		schema.Property("private_boundary", schema.String("不能说或不能越过的边界")),
		schema.Property("resources", schema.Array("可用资源", schema.String(""))),
		schema.Property("misbeliefs", schema.Array("误判或未知", schema.String(""))),
		schema.Property("skill_limits", schema.Array("当前能力边界", schema.String(""))),
		schema.Property("plausible_mistakes", schema.Array("合理会犯的错误", schema.String(""))),
	)
	offscreenStage := schema.Object(
		schema.Property("character", schema.String("角色实名")).Required(),
		schema.Property("location", schema.String("本章时段的位置")).Required(),
		schema.Property("current_action", schema.String("此刻行动")).Required(),
		schema.Property("decision", schema.String("本章选择")).Required(),
		schema.Property("knowledge_boundary", schema.String("知道和不知道什么")).Required(),
		schema.Property("timeline_consistency", schema.String("与主线时间如何同步")).Required(),
		schema.Property("visible_in_chapter", schema.Bool("是否直接出现在正文")),
		schema.Property("status", schema.String("当前状态")),
		schema.Property("pressure", schema.String("当前压力")),
		schema.Property("transport", schema.String("移动方式；未移动可写原地")),
		schema.Property("travel_time", schema.String("现实尺度耗时")),
	)
	environmentState := schema.Object(
		schema.Property("place", schema.String("地点或物件")).Required(),
		schema.Property("visible_state", schema.String("读者可见状态")).Required(),
		schema.Property("information_carried", schema.String("承载的信息")),
		schema.Property("pressure_applied", schema.String("对选择施加的压力")).Required(),
		schema.Property("expected_change", schema.String("章末变化")).Required(),
	)
	causalBeat := schema.Object(
		schema.Property("cause", schema.String("前置事实")).Required(),
		schema.Property("character_choice", schema.String("角色选择")).Required(),
		schema.Property("world_response", schema.String("环境、关系或规则反馈")).Required(),
		schema.Property("story_result", schema.String("状态后果")).Required(),
	)
	voiceLogic := schema.Object(
		schema.Property("character", schema.String("角色实名")).Required(),
		schema.Property("scene_objective", schema.String("对话中要达成的目的")).Required(),
		schema.Property("hidden_subtext", schema.String("未明说的内容")).Required(),
		schema.Property("knowledge_boundary", schema.String("台词不能越过的信息边界")).Required(),
		schema.Property("diction_and_rhythm", schema.String("词汇和节奏特征")).Required(),
		schema.Property("silence_or_action_beat", schema.String("沉默或动作承接")).Required(),
		schema.Property("dialogue_functions", schema.Array("对白承担的功能", schema.String(""))).Required(),
		schema.Property("forbidden_moves", schema.Array("禁用声口和表达", schema.String(""))).Required(),
	)
	dialogueTurn := schema.Object(
		schema.Property("speaker", schema.String("说话人")).Required(),
		schema.Property("surface_line_function", schema.String("表层台词功能")).Required(),
		schema.Property("hidden_subtext", schema.String("潜台词")),
		schema.Property("new_information", schema.String("新增信息")),
		schema.Property("power_move", schema.String("权力变化")),
		schema.Property("action_beat", schema.String("动作或停顿")).Required(),
		schema.Property("next_pressure", schema.String("下一压力点")).Required(),
	)
	dialogueBlueprint := schema.Object(
		schema.Property("scene_id", schema.String("场景标识")).Required(),
		schema.Property("dialogue_mode", schema.String("对话模式")).Required(),
		schema.Property("scene_pressure", schema.String("场景压力")).Required(),
		schema.Property("relationship_frame", schema.String("关系和权力位置")).Required(),
		schema.Property("location_anchor", schema.String("现场锚点")).Required(),
		schema.Property("dialogue_objective", schema.String("对白必须完成的剧情功能")).Required(),
		schema.Property("turn_progression", schema.Array("至少一轮改变信息或压力的对白", dialogueTurn)).Required(),
		schema.Property("exit_beat", schema.String("退出对白的现场后果")).Required(),
	)
	emotionalLogic := schema.Object(
		schema.Property("character", schema.String("角色实名")).Required(),
		schema.Property("immediate_state", schema.String("即时状态")).Required(),
		schema.Property("primary_emotion", schema.String("主要情绪")).Required(),
		schema.Property("emotional_trigger", schema.String("触发事件")).Required(),
		schema.Property("goal_appraisal", schema.String("对目标的影响判断")).Required(),
		schema.Property("regulation_strategy", schema.String("角色如何压住或处理情绪")).Required(),
		schema.Property("emotion_led_action", schema.String("情绪推动的可见行动")).Required(),
		schema.Property("evidence_in_scene", schema.Array("正文可见证据", schema.String(""))).Required(),
	)
	antiAI := schema.Object(
		schema.Property("risk_signals", schema.Array("本章高风险 AI 味", schema.String(""))).Required(),
		schema.Property("counter_moves", schema.Array("对应阻断动作", schema.String(""))).Required(),
		schema.Property("sentence_rhythm_policy", schema.String("句长和段落节奏")).Required(),
		schema.Property("object_response_budget", schema.String("屏幕、物件回应预算")),
		schema.Property("dialogue_function_plan", schema.String("对白功能分配")).Required(),
		schema.Property("review_checks", schema.Array("提交前检查项", schema.String(""))).Required(),
	)
	trendLanguage := schema.Object(
		schema.Property("item", schema.String("本章会原样落进人物对白/系统交流/群聊反应的一条具体短梗；若项目 web_reference_brief 有本章热梗落点，只能从该小节选择，不得擅自换梗")).Required(),
		schema.Property("source_context", schema.String("必须明确写 meta/web_reference_brief.md 或项目联网简报的具体条目；无简报时才可写当轮 web_research 来源")).Required(),
		schema.Property("character_carrier", schema.String("明确到角色或媒介；不得写旁白")).Required(),
		schema.Property("scene_function", schema.String("误会、社死、关系反应或轻喜剧反噬中的具体功能")).Required(),
		schema.Property("usage_budget", schema.String("本章次数预算，通常1-2处且禁止梗串")).Required(),
		schema.Property("forbidden_usage", schema.String("明确旁白、关键判断、硬煽情和章末禁用")).Required(),
	)
	entertainmentPlan := schema.Object(
		schema.Property("opening_beat", schema.String("前200字内的具体尴尬、冲突、误会或反转；写清谁做什么以及现场反应")).Required(),
		schema.Property("humor_beats", schema.Array("至少2个不同机制的喜剧节拍，每个写清铺垫、承载角色和反应后果；不能都靠热梗", schema.String(""))).Required(),
		schema.Property("immediate_payoffs", schema.Array("至少2个本章页面可见的即时兑现：到账、打脸、关系偏转、结果反噬或新权限", schema.String(""))).Required(),
		schema.Property("procedure_compression", schema.String("列明哪些流程一笔带过，以及保留的冲突/笑点/关系变化；不得把经营写成教程")).Required(),
		schema.Property("companion_voice_beat", schema.String("系统、搭档或朋友如何用有性格的短回应陪主角推进；用户定义系统会交流解闷时，必须明确系统如何接话/吐槽并始终支持主角，禁止反向写成不接话")).Required(),
		schema.Property("forbidden_comedy", schema.Array("本章喜剧禁区：降智、梗串、旁白热词、拿严肃情绪硬抖包袱等", schema.String(""))).Required(),
	)
	longRangePromise := schema.Object(
		schema.Property("promise", schema.String("长线承诺")).Required(),
		schema.Property("first_chapter_seed", schema.String("第一章可见种子")).Required(),
		schema.Property("payoff_horizon", schema.String("兑现区间")).Required(),
	)
	longformOpening := schema.Object(
		schema.Property("target_reader", schema.String("核心读者与消费期待")).Required(),
		schema.Property("opening_hook", schema.String("第一章最短追读理由")).Required(),
		schema.Property("serial_engine", schema.String("支撑长篇连载的升级发动机")).Required(),
		schema.Property("reader_reward_loop", schema.Array("反复兑现的奖励类型", schema.String(""))).Required(),
		schema.Property("long_range_promises", schema.Array("长线承诺与回收周期", longRangePromise)).Required(),
		schema.Property("reveal_budget", schema.Array("第一章克制不解释的内容", schema.String(""))).Required(),
		schema.Property("first_chapter_proof", schema.Array("第一章证明连载可持续的页面证据", schema.String(""))).Required(),
		schema.Property("retention_risks", schema.Array("第一章流失风险与规避动作", schema.String(""))).Required(),
	)
	rewardPlan := schema.Object(
		schema.Property("chapter_window", schema.String("兑现窗口")),
		schema.Property("first_chapter_small_win", schema.String("本章可见小胜")).Required(),
		schema.Property("new_debt_or_cost", schema.String("小胜后的新代价")).Required(),
		schema.Property("payoff_visibility", schema.String("读者如何看见兑现")).Required(),
		schema.Property("traffic_risk", schema.String("可能导致流失的风险")),
		schema.Property("forbidden_reward_patterns", schema.Array("禁用的虚假爽点", schema.String(""))).Required(),
	)
	retentionBeat := schema.Object(
		schema.Property("plan_source", schema.String("对应计划来源")),
		schema.Property("must_show", schema.String("若选中此候选拍，正文展示的事件；不代表所有候选都必须写")).Required(),
		schema.Property("reader_payoff", schema.String("读者获得什么")).Required(),
		schema.Property("scene_vehicle", schema.String("用什么场景承载")).Required(),
		schema.Property("proof_on_page", schema.String("页面证据")),
	)
	retentionPlan := schema.Object(
		schema.Property("surface_beats", schema.Array("2-4 个正文候选节拍；只选足以完成 required_beats 的最少部分", retentionBeat)).Required(),
		schema.Property("latent_context", schema.Array("只留后台的上下文", schema.String(""))),
		schema.Property("reveal_budget", schema.Array("延后解释的内容", schema.String(""))),
		schema.Property("cut_or_compress", schema.Array("删除或压缩内容", schema.String(""))).Required(),
		schema.Property("page_turn_questions", schema.Array("带入下一章的问题", schema.String(""))).Required(),
	)
	endingContract := schema.Object(
		schema.Property("ending_mode", schema.String("章末模式")).Required(),
		schema.Property("concrete_anchor", schema.String("章末具体物件、动作或提示")).Required(),
		schema.Property("consequence", schema.String("已发生且不能撤销的后果")).Required(),
		schema.Property("next_chapter_pull", schema.String("与下一章的直接承接")).Required(),
		schema.Property("why_not_ui", schema.String("为何不是空 UI 提示")),
		schema.Property("forbidden_endings", schema.Array("禁用收尾", schema.String(""))).Required(),
	)
	reviewRefinement := schema.Object(
		schema.Property("trigger_sources", schema.Array("审核或 rewrite brief 来源", schema.String(""))).Required(),
		schema.Property("failure_modes", schema.Array("本轮失败类型", schema.String(""))).Required(),
		schema.Property("localized_targets", schema.Array("局部修复目标", schema.String(""))).Required(),
		schema.Property("preserve_constraints", schema.Array("必须保留内容", schema.String(""))).Required(),
		schema.Property("replanning_moves", schema.Array("重规划动作", schema.String(""))).Required(),
		schema.Property("acceptance_checks", schema.Array("验收问题", schema.String(""))).Required(),
		schema.Property("stop_condition", schema.String("停止继续整章重写的条件")).Required(),
		schema.Property("iteration_limit", schema.Int("同类失败最多迭代次数")).Required(),
	)
	return schema.Object(
		schema.Property("world_simulation_id", schema.String("simulate_chapter_world finalize 返回的稳定 ID")),
		schema.Property("protagonist_decision", schema.String("全角色世界模拟投影出的主角选择，必须原样引用")),
		schema.Property("project_promise", schema.String("本章承接的整本书核心承诺")),
		schema.Property("chapter_function", schema.String("本章在全书中的功能")),
		schema.Property("context_sources", schema.Array("本次实际使用的上下文来源", schema.String(""))),
		schema.Property("initial_state", schema.Array("最多2项：主角，必要时加当章关系核心", initialState)),
		schema.Property("offscreen_character_stage", schema.Array("仅写本章相关人物；非本章角色不必填", offscreenStage)),
		schema.Property("environment_state", schema.Array("现场环境和物件状态", environmentState)),
		schema.Property("causal_beats", schema.Array("最多4项结果级触发、选择、反馈、后果", causalBeat)),
		schema.Property("decision_points", schema.Array("最多4个必须落成的主角选择", schema.String(""))),
		schema.Property("outcome_shift", schema.Array("最多4项章末状态变化", schema.String(""))),
		schema.Property("voice_logic", schema.Array("最多4张：主角、关系核心、系统、一名关键配角", voiceLogic)),
		schema.Property("dialogue_scene_blueprints", schema.Array("可选；默认省略，勿重复世界模拟或预写对白", dialogueBlueprint)),
		schema.Property("emotional_logic", schema.Array("可选；默认由主角选择承载，不逐项填心理矩阵", emotionalLogic)),
		schema.Property("anti_ai_execution_plan", antiAI),
		schema.Property("trend_language_plan", schema.Array("可选热梗上限；默认省略，不把梗变成硬台词", trendLanguage)),
		schema.Property("reader_entertainment_plan", entertainmentPlan),
		schema.Property("longform_opening", longformOpening),
		schema.Property("reader_reward_plan", rewardPlan),
		schema.Property("reader_retention_plan", retentionPlan),
		schema.Property("ending_consequence_contract", endingContract),
		schema.Property("review_refinement", reviewRefinement),
		schema.Property("world_rules_in_force", schema.Array("本章实际施压的规则", schema.String(""))),
		schema.Property("information_gaps", schema.Array("信息差和未授权内容", schema.String(""))),
		schema.Property("scene_constraints", schema.Array("视角、解释和场景限制", schema.String(""))),
	)
}

// legacyCausalSimulationSchema 保留旧单发 plan_chapter 的完整输入契约。
func legacyCausalSimulationSchema(strict bool) map[string]any {
	req := func(p schema.Prop) schema.Prop {
		if strict {
			return p.Required()
		}
		return p
	}
	knowledgeLedger := schema.Object(
		schema.Property("known_facts", schema.Array("此角色此刻已经能确认的事实，必须有前文、台账或本章可见证据", schema.String(""))),
		schema.Property("unknown_facts", schema.Array("此角色还不知道、不能提前知道或只能等待验证的信息", schema.String(""))),
		schema.Property("suspicions", schema.Array("此角色有迹象怀疑但尚未证实的判断", schema.String(""))),
		schema.Property("false_beliefs", schema.Array("此角色当前相信但可能错误的判断", schema.String(""))),
		schema.Property("evidence_seen", schema.Array("支撑其判断的可见证据、动作、台账、物件或他人说法", schema.String(""))),
		schema.Property("confidence", schema.String("此角色对当前判断的置信度，例如 low/medium/high 或具体说明")),
		schema.Property("source_chapter", schema.Int("主要知识来源章节；没有明确来源时可省略")),
		schema.Property("forbidden_knowledge", schema.Array("本章台词和行动绝不能越过的信息边界", schema.String(""))),
	)
	decisionFrame := schema.Object(
		schema.Property("available_options", schema.Array("此角色按当前信息量能看见的可选行动", schema.String(""))),
		schema.Property("rejected_options", schema.Array("此角色会拒绝的选择及理由，例如风险过高、证据不足、会越界承诺", schema.String(""))),
		schema.Property("decision_rule", schema.String("此角色做选择时的稳定判断标准，例如先核验证据再交易")),
		schema.Property("tradeoff", schema.String("本章选择需要权衡的收益、代价、关系或风险")),
		schema.Property("cost_paid", schema.String("选择后愿意付出的代价或已经付出的代价")),
		schema.Property("risk_accepted", schema.String("此角色主动接受或暂时容忍的风险")),
		schema.Property("expected_gain", schema.String("此角色希望通过选择得到什么")),
		schema.Property("minimum_evidence_required", schema.String("让此角色采取行动前最低需要看到什么证据")),
	)
	relationshipContract := schema.Object(
		schema.Property("counterpart", schema.String("关系对象")),
		schema.Property("trust", schema.String("当前信任度、信任来源或不信任来源")),
		schema.Property("debt", schema.String("债务、亏欠、救命账、承诺账或资源账")),
		schema.Property("leverage", schema.String("此关系中可交易、可威胁或可交换的筹码")),
		schema.Property("promise", schema.String("已经许下或被要求履行的承诺")),
		schema.Property("shared_secret", schema.String("共同秘密或双方都不能公开的信息")),
		schema.Property("betrayal_record", schema.String("欺骗、背叛、试探或隐瞒记录")),
		schema.Property("dependency", schema.String("彼此依赖点")),
		schema.Property("fear_source", schema.String("此关系中的恐惧来源")),
		schema.Property("alliance_status", schema.String("同盟状态，例如临时合作/互不信任/绑定/破裂")),
		schema.Property("betrayal_threshold", schema.String("什么情况下此人会反咬、退出或拒绝帮助")),
		schema.Property("help_condition", schema.String("此人愿意帮助对方的条件")),
		schema.Property("source_chapter", schema.Int("主要关系证据来源章节；没有明确来源时可省略")),
	)
	emotionAppraisal := schema.Object(
		schema.Property("trigger_event", schema.String("引发情绪的具体事件、物件、台词或规则反馈")),
		schema.Property("goal_impact", schema.String("该事件如何阻碍或推动此角色当前目标")),
		schema.Property("threat_to_value", schema.String("该事件威胁到此角色哪个价值、责任、尊严、秘密或生存边界")),
		schema.Property("visible_expression", schema.String("读者能看见的情绪表现：动作、停顿、语速、选择变化")),
		schema.Property("suppressed_expression", schema.String("此角色压住没说、没做或故意转移的反应")),
		schema.Property("coping_strategy", schema.String("此角色处理情绪的习惯方式，例如报账式确认、讥讽、沉默、求助")),
		schema.Property("action_pressure", schema.String("情绪如何改变其下一步行动压力")),
		schema.Property("relationship_effect", schema.String("情绪如何改变本章关系姿态")),
	)
	arcAxis := schema.Object(
		schema.Property("want", schema.String("此角色当前外在想要什么")),
		schema.Property("need", schema.String("此角色长期真正需要学会或承认什么")),
		schema.Property("wound_or_ghost", schema.String("塑造其反应方式的旧伤、创伤、职业阴影或历史欠账")),
		schema.Property("core_lie", schema.String("此角色当前相信并会限制他的核心错误信念")),
		schema.Property("value_axis", schema.String("此角色长期价值冲突轴，例如责任/自保、交易/信任")),
		schema.Property("arc_stage", schema.String("当前弧线阶段，例如起点、防御、试探、动摇、突破、倒退")),
		schema.Property("pressure_test", schema.String("本章如何测试这条弧线")),
		schema.Property("growth_signal", schema.String("本章若成长，会出现什么可见信号")),
		schema.Property("regression_signal", schema.String("本章若倒退，会出现什么可见信号")),
	)
	characterState := schema.Object(
		schema.Property("character", schema.String("角色名")),
		schema.Property("current_goal", schema.String("本章开局此角色想要什么")),
		schema.Property("pressure", schema.String("正在压迫此角色的外部/内部压力")),
		schema.Property("resources", schema.Array("此角色当前可用或正在牵制他的资源、债务、凭证、伤势、权限", schema.String(""))),
		schema.Property("relationship_forces", schema.Array("此角色当前被哪些关系、信任、债务、欺骗记录或利益绑定牵引", schema.String(""))),
		schema.Property("secrets", schema.Array("此角色当前隐瞒、未公开、不能暴露或尚未被他人知道的内容", schema.String(""))),
		schema.Property("misbeliefs", schema.Array("此角色当前误判、不知道或被错误信息影响的内容", schema.String(""))),
		schema.Property("private_boundary", schema.String("此角色暂时不能说、不能暴露或不能越过的边界")),
		schema.Property("action_tendency", schema.String("按此角色长期经验和当前系统状态，遇到风险时通常先做什么")),
		schema.Property("likely_action", schema.String("按此角色性格和信息量最可能采取的行动")),
		schema.Property("state_delta_to_track", schema.Array("本章结束后必须回填的状态变化候选：目标/压力/资源/关系/秘密暴露度/误判/行动倾向等", schema.String(""))),
		schema.Property("competence_stage", schema.String("此刻能力/认知阶段；禁止把开局角色写成最终状态或全知全能")).Required(),
		schema.Property("skill_limits", schema.Array("此角色当前能力边界、不会做/做不好的事、不能稳定判断的事", schema.String(""))).Required(),
		schema.Property("plausible_mistakes", schema.Array("按当前压力和误判会合理犯的错、迟疑、错判或过度反应", schema.String(""))).Required(),
		schema.Property("correction_triggers", schema.Array("什么证据、代价或他人行动会让角色修正判断并成长", schema.String(""))).Required(),
		schema.Property("knowledge_ledger", knowledgeLedger).Required(),
		schema.Property("decision_frame", decisionFrame).Required(),
		schema.Property("relationship_contract", schema.Array("此角色和关键关系对象之间的信任、债务、筹码、承诺和背叛阈值；没有关键关系时传空数组", relationshipContract)).Required(),
		schema.Property("emotion_appraisal", emotionAppraisal).Required(),
		schema.Property("arc_axis", arcAxis).Required(),
	)
	characterVoiceLogic := schema.Object(
		schema.Property("character", schema.String("角色名")),
		schema.Property("personality_source", schema.String("该说话逻辑来自哪些人物卡、关系状态、前文声口或本章压力")),
		schema.Property("speech_principle", schema.String("此角色在本章说话时的底层逻辑，例如先证据后判断、先躲避后求救")),
		schema.Property("scene_objective", schema.String("此角色在本章关键对话里想达成什么具体目的")),
		schema.Property("hidden_subtext", schema.String("此角色没有明说但正在隐瞒、试探、转嫁、索取或保护的内容")),
		schema.Property("knowledge_boundary", schema.String("此角色此时知道/不知道什么；不能让台词越过信息边界")),
		schema.Property("relationship_stance", schema.String("此角色面对对手/亲友/组织时的权力位置、亲疏和情绪姿态")),
		schema.Property("diction_and_rhythm", schema.String("词汇、句长、停顿、口头禅、职业语域和情绪节奏")),
		schema.Property("sentence_length", schema.String("本章句长偏好：短句/中句/断句/急促改口/职业性长句的使用边界")),
		schema.Property("punctuation_style", schema.String("标点风格：问号、顿号、省略、破折或句号的使用边界；避免统一 AI 节奏")),
		schema.Property("line_break_style", schema.String("断行风格：什么情绪、停顿、动作或信息需要独立成段")),
		schema.Property("subtext_strategy", schema.String("潜台词策略：想要、害怕、隐瞒或试探如何藏进条件、反问、动作或沉默")),
		schema.Property("silence_or_action_beat", schema.String("可选预算：只有会改变权力、遮掩信息、打断台词或影响现场结果时才安排沉默/动作；禁止给每句对白机械配动作")),
		schema.Property("voice_contrast", schema.String("与主角/同场角色相比，此人声口如何区分：词汇、句长、反应速度、信息处理习惯")),
		schema.Property("action_beat_policy", schema.String("对白之间应如何用动作、沉默、误判、物件反应承载潜台词")),
		schema.Property("dialogue_functions", schema.Array("本章对白必须承担的功能：推进冲突、暴露性格、交换/隐藏信息、改变关系、埋伏笔等", schema.String(""))),
		schema.Property("typical_moves", schema.Array("本章允许出现的典型话术动作、句长、语气和潜台词", schema.String(""))),
		schema.Property("forbidden_moves", schema.Array("本章不应出现的台词逻辑或口吻偏移", schema.String(""))),
		schema.Property("dialogue_test", schema.Array("写完正文后用于自检声口是否成立的短问题", schema.String(""))),
	)
	dialogueTurnDesign := schema.Object(
		schema.Property("speaker", schema.String("这一轮说话人或沉默承载者")).Required(),
		schema.Property("surface_line_function", schema.String("表层台词功能：问候/催促/试探/拒绝/报错/让步/下命令等")).Required(),
		schema.Property("hidden_subtext", schema.String("这句话没明说的恐惧、索取、隐瞒、试探或权力动作")).Required(),
		schema.Property("new_information", schema.String("这一轮给读者或角色新增的具体信息；没有新增时说明由动作/沉默替代")),
		schema.Property("power_move", schema.String("权力变化：谁在压迫、退让、转移责任、求助或抢回主动权")),
		schema.Property("action_beat", schema.String("可选。仅记录会改变权力、遮掩信息、打断话头或影响现场结果的动作；普通轮次留空，禁止为了字段完整给每句台词配动作")),
		schema.Property("next_pressure", schema.String("这一轮之后推进到的下一个压力点")).Required(),
	)
	dialogueAudiencePresence := schema.Object(
		schema.Property("present", schema.String("第三方观众：none 或具体在场者（围观宾客、下属、孩子、记录者、直播观众）；一旦有观众，双方的话有一半是演给旁人看的")).Required(),
		schema.Property("performance_for", schema.String("双方各自演给谁看、想在观众面前保住或摧毁什么（体面、权威、关系、证词）；present 为 none 时可写 none")),
		schema.Property("audience_effect", schema.String("观众反应如何反过来改变对话走向：起哄、沉默、倒戈、传播、记录在案；present 为 none 时可写 none")),
	)
	dialogueInfoAsymmetry := schema.Object(
		schema.Property("pov_knows", schema.String("POV 此刻掌握、可以打出去的信息或筹码")),
		schema.Property("pov_lacks", schema.String("POV 缺失并因此会误读、误判的信息")).Required(),
		schema.Property("other_holds", schema.String("对手掌握而 POV 不知道的底牌、议程或事实")).Required(),
		schema.Property("reader_position", schema.String("读者信息位：reader_ahead=读者比 POV 知道得多(戏剧反讽，替角色捏汗)、reader_level=同步、reader_behind=读者知道得更少(悬念)")).Required(),
		schema.Property("asymmetry_play", schema.String("信息差在本场如何被利用、暴露或加深；哪一回合信息差收窄或扩大")).Required(),
	)
	dialogueValueShift := schema.Object(
		schema.Property("value", schema.String("本场押上的价值：信任、安全、希望、亲密、控制权、名誉、归属等")).Required(),
		schema.Property("opening_charge", schema.String("开场极性(正/负)加一句现场证据，例如“负：他站在门外不敢敲”")).Required(),
		schema.Property("turn_trigger", schema.String("触发翻转的具体台词、动作或信息；对应 turn_progression 里的某一回合")).Required(),
		schema.Property("closing_charge", schema.String("收场极性；必须与开场不同——没有价值翻转的对话场应该删除或并入他场")).Required(),
	)
	dialoguePowerTrajectory := schema.Object(
		schema.Property("opening_holder", schema.String("开场谁占上风，凭什么：信息、地位、时间压力、情感筹码、观众支持")).Required(),
		schema.Property("flip_beat", schema.String("权力第一次易手发生在哪一回合、由什么触发；好的对话戏权力至少易手一次")).Required(),
		schema.Property("closing_holder", schema.String("收场谁占上风；允许翻回开场一方，但必须经过易手")).Required(),
	)
	dialogueObjectiveTactic := schema.Object(
		schema.Property("character", schema.String("执行策略的角色")).Required(),
		schema.Property("faction", schema.String("多人议事场：此角色所属派系或临时联盟；双人场可省略")),
		schema.Property("immediate_objective", schema.String("此角色在这一场/这一拍真正想立刻拿到什么")).Required(),
		schema.Property("tactic", schema.String("他采用的说话策略：试探、压价、求救、威胁、撒谎、转移话题、沉默、玩笑、顺从等")).Required(),
		schema.Property("counter_tactic", schema.String("对方如何抵消、误读、反压或绕开这个策略")).Required(),
		schema.Property("emotional_leak", schema.String("情绪如何从语速、断句、称呼、动作、停顿或偏题里漏出来")).Required(),
		schema.Property("turn_result", schema.String("这一策略造成的信息、关系、权力或情绪位移")).Required(),
	)
	dialogueSceneBlueprint := schema.Object(
		schema.Property("scene_id", schema.String("对白场景唯一标识，例如 opening-dialogue-entry")).Required(),
		schema.Property("dialogue_mode", schema.String("本场对话模式。防御/压力类：pressure_negotiation(压力谈判)、interrogation(审问套话)、plea_for_help(求助转嫁)、logistics_under_stress(压力下汇报)、avoidance(回避冷处理)、silence_pressure(沉默压迫)、status_report(带潜台词汇报)、coercion_blackmail(威胁敲诈，与谈判的区别是不给对方留选择)。进攻/揭示类：reveal_showdown(摊牌揭露，信息倾泻而非抽取)、public_confrontation(公开对峙/打脸，必填 audience_presence)、recruitment_temptation(招揽劝降诱惑，条件必须真的动人)、mutual_probing(双向试探，权力对称双方都藏都钓)。关系转折类：confession(告白告解)、banter_masking_fear(互怼调情)、misunderstanding_escalation(误会升级)、rupture(决裂，出口必须是回不去了的具象物)、reconciliation_apology(和解道歉，禁止直说对不起我错了)、breaking_bad_news(传达坏消息，写说的人怎么拖延、听的人怎么拒绝理解)、farewell(告别)。情感纵深类：mentorship_teaching(教导传承，exposition_budget 从严)、uninhibited_truth(失控真言：醉酒/重伤/濒死滤镜脱落)、mundane_talk_bearing_weight(日常闲话承重，表面聊琐事底下全是没说出口的事)。结构特殊类：group_council(多人议事，必填 participants/faction/coalition_shift)、overheard(旁听偷听，pov_role 必须是 eavesdropper/bystander，POV 无法回应只能误读)、mediated_exchange(隔媒介对话，必填 medium，动作拍改为媒介拍)。也可用项目自定义模式")).Required(),
		schema.Property("mode_reason", schema.String("为什么本章此处必须采用该模式，而不是对白先入场/审问/闲聊/告白等其他模式")).Required(),
		schema.Property("scene_pressure", schema.String("本场主要压力：时间、资源、关系、身份、危险、羞耻、误会、制度、身体状态或世界规则")).Required(),
		schema.Property("emotional_temperature", schema.String("情绪温度与变化：冷处理、惊恐、压怒、尴尬、暧昧、防御、崩溃、麻木等")).Required(),
		schema.Property("relationship_frame", schema.String("双方关系框架和权力差：陌生/上下级/债主债务/亲密/敌对/互相需要/信息不对称")).Required(),
		schema.Property("medium", schema.String("对话媒介：face_to_face、phone、text_message、letter、through_door、intermediary(借第三人传话)等；非面对面时没有身体动作拍，改用媒介拍——已读不回、打字又删掉、信纸折痕、门后脚步")).Required(),
		schema.Property("pov_role", schema.String("POV 在本场的位置：participant(参与者，默认)、eavesdropper(偷听者)、bystander(旁观者)；偷听/旁观场 POV 无法回应，只能误读、错过追问机会")),
		schema.Property("participants", schema.Array("三人以上对话时列出全部说话方或派系；双人场可省略", schema.String(""))),
		schema.Property("audience_presence", dialogueAudiencePresence).Required(),
		schema.Property("information_asymmetry", dialogueInfoAsymmetry).Required(),
		schema.Property("value_shift", dialogueValueShift).Required(),
		schema.Property("power_trajectory", dialoguePowerTrajectory).Required(),
		schema.Property("address_shift", schema.String("称谓漂移设计：双方称呼如何随压力变化（陛下→你、师兄→直呼其名、敬称脱落/加回），称谓变化本身是一条潜台词线")),
		schema.Property("coalition_shift", schema.String("多人议事场必填：派系联盟在哪一回合、因何翻转，谁倒戈、谁沉默弃权")),
		schema.Property("opening_strategy", schema.String("开场策略：dialogue_first、action_first、silence_first、object_first、misunderstanding_first、memory_then_interruption、environmental_voice 等；不能每场固定对白先行")).Required(),
		schema.Property("first_spoken_moment", schema.String("第一句真正说出口的时机和原因；若不是开篇第一句，说明前面由动作/物件/沉默承载什么")).Required(),
		schema.Property("entry_line", schema.String("若采用 dialogue_first/voice_first，用于把读者直接拉进场的一句台词/广播/制度话术/门外声音；其他模式可写 delayed/none 并说明原因")),
		schema.Property("entry_speaker", schema.String("第一句或第一声的发出者；可以是角色、群体、制度载体或环境声源")),
		schema.Property("location_anchor", schema.String("一句短定场：地点、空间压迫、时辰、可见物件或出口关系")).Required(),
		schema.Property("pov_state", schema.String("主视角人物当下的身体/心理迟滞、误判、恐惧或自欺；禁止立刻全懂")).Required(),
		schema.Property("inner_question", schema.String("贴近主视角的信息缺口或自问；第三人称项目可写成短念头，但不得切成全知解释")).Required(),
		schema.Property("memory_bridge", schema.String("只补当前对白必需的身份、关系、前一幕或处境，明确说明信息量预算")).Required(),
		schema.Property("identity_grounding", schema.String("通过称谓、职位、外貌、关系债或权力差说明对方是谁")).Required(),
		schema.Property("dialogue_objective", schema.String("这场对白必须完成的剧情功能：任务分配、交易试探、关系改向、威胁落地或伏笔投放")).Required(),
		schema.Property("interlocutor_agenda", schema.String("对话对手/制度载体真正想得到或隐藏什么")).Required(),
		schema.Property("protagonist_response_strategy", schema.String("主角按当前信息量如何应对：误按、沉默、追问、敷衍、拒绝、绕开称呼等")).Required(),
		schema.Property("objective_tactics", schema.Array("角色目标与策略链；必须说明不同角色如何用不同说话策略追求目标", dialogueObjectiveTactic)).Required(),
		schema.Property("turn_progression", schema.Array("至少两轮对白/动作推进，每轮必须改变信息、压力或权力位置", dialogueTurnDesign)).Required(),
		schema.Property("directness_policy", schema.String("直说/绕说比例：哪些话必须直说，哪些只能靠潜台词、动作或误会呈现")).Required(),
		schema.Property("subtext_source", schema.String("潜台词来源：关系旧账、创伤、文化规范、利益冲突、羞耻、保护欲、偏见或信息差")).Required(),
		schema.Property("escalation_pattern", schema.String("升级方式：yes-but、no-and、三次试探、误会加深、让步换代价、沉默压迫、情绪失控等")).Required(),
		schema.Property("beat_density", schema.String("动作/沉默/环境拍密度：高压场多短拍，亲密场留空，信息场避免每句一动作")).Required(),
		schema.Property("silence_policy", schema.String("何处该沉默、无人接话、答非所问或让物件不回应，用于承载情绪和潜台词")).Required(),
		schema.Property("info_release_policy", schema.String("信息释放策略：直接给、被误读、被打断、对方只给半句、事后才理解等")).Required(),
		schema.Property("exposition_budget", schema.String("背景信息预算：允许补什么、最多补多少、禁止一次灌完整设定")).Required(),
		schema.Property("subtext_and_power_shift", schema.String("这场对白从开头到退出时，潜台词和权力位置如何变化")).Required(),
		schema.Property("exit_beat", schema.String("退出对白的具体现场拍：动作、物件状态、空间变化或未完成选择；禁止突然声响式万能钩子")).Required(),
		schema.Property("do_not_use", schema.Array("禁止使用的模板：照抄样本、解释设定、菜单选项、全知判断、漂亮金句等", schema.String(""))).Required(),
	)
	crowdRole := schema.Object(
		schema.Property("group_name", schema.String("群体/职能名，例如调查队其余队员、围观租户、后勤组三人")),
		schema.Property("count", schema.Int("人数；不确定时填估计数")),
		schema.Property("scene_function", schema.String("本章存在功能：证明团队规模、提供反应、制造众目睽睽、承接恐怖样本、后勤搬运等")),
		schema.Property("reaction_policy", schema.String("他们如何反应：只用集体动作/短反应/沉默/退后/互看，不抢关键判断")),
		schema.Property("voice_budget", schema.String("台词预算，例如0句、1句群声、最多2句短反应；不得用来解释设定")),
		schema.Property("naming_policy", schema.String("命名策略：默认不命名；需要区分时用职能称呼，不引入无用正式名")),
		schema.Property("continuity_policy", schema.String("连续性策略：默认不进长期角色台账；若携带新信息、做关键选择或建立关系债务，必须升级为关键角色")),
		schema.Property("exit_condition", schema.String("何时退场或回到背景，避免持续占用后续章节空间")),
	)
	reviewRefinement := schema.Object(
		schema.Property("trigger_sources", schema.Array("触发重推演的审核来源，例如 rewrite_brief.review_summary、issues、contract_misses、mechanical_gate、reviews/NN.md", schema.String(""))),
		schema.Property("failure_modes", schema.Array("审核归纳出的失败类型，例如人物声口偏移、因果跳切、契约遗漏、AI 腔、RAG 越权", schema.String(""))),
		schema.Property("localized_targets", schema.Array("需要重写/打磨的局部目标：段落、场景、台词组、物件承载、章尾钩子等", schema.String(""))),
		schema.Property("preserve_constraints", schema.Array("返工时必须保留的资产：已通过的剧情事实、伏笔、私人道具、好句子、已入账资源等", schema.String(""))),
		schema.Property("replanning_moves", schema.Array("根据审核结论重建推演的动作：重排因果、补动作拍、改声口、收窄 RAG、替换钩子等", schema.String(""))),
		schema.Property("acceptance_checks", schema.Array("本轮返工完成后必须满足的验收问题", schema.String(""))),
		schema.Property("stop_condition", schema.String("什么时候停止继续整章重写，改用局部 edit、人工确认或上游大纲调整")),
		schema.Property("iteration_limit", schema.Int("同一失败原因允许连续重推演/重写的上限，避免无效循环")),
	)
	writingNormApplication := schema.Object(
		schema.Property("source", schema.String("规范来源，例如 writing_engine.active_rules、user_rules、anti_ai_tone、human_feel_craft、writing_techniques_digest、web_reference_guidelines")).Required(),
		schema.Property("rule_focus", schema.Array("本章实际要执行的具体规则，不要只写资料名", schema.String(""))).Required(),
		schema.Property("chapter_application", schema.String("本章如何把该规范转成场景、对白、物件、节奏或审核自检")).Required(),
		schema.Property("proof_targets", schema.Array("正文里可检查该规范是否落实的证据位置或表现", schema.String(""))).Required(),
		schema.Property("failure_risk", schema.String("若没执行会导致的具体失败模式，例如AI腔、解释段、角色声口偏移、资料堆砌")).Required(),
	)
	antiAIExecutionPlan := schema.Object(
		schema.Property("risk_signals", schema.Array("本章最容易出现的 AI 味风险信号，如整齐清单、解释段、同型短句、金句收尾、物件即时回应过密", schema.String(""))).Required(),
		schema.Property("counter_moves", schema.Array("写作时阻断风险的动作：改成对白摩擦、物件证据、误判、沉默、关系代价、场景后果等", schema.String(""))).Required(),
		schema.Property("sentence_rhythm_policy", schema.String("句长、段落功能、标点和抽象/具象切换策略")).Required(),
		schema.Property("object_response_budget", schema.String("屏幕、纸面、门牌、灯光等物件回应的预算、间距和禁用方式")).Required(),
		schema.Property("dialogue_function_plan", schema.String("对白如何分配信息交换、隐藏、拒绝、试探、关系变化；禁止把对白写成设定讲解")).Required(),
		schema.Property("review_checks", schema.Array("正文提交前必须自查的 AI 味/机械门禁问题", schema.String(""))).Required(),
	)
	externalReferencePlan := schema.Object(
		schema.Property("query_or_need", schema.String("本章需要的外部资料、行业流程、当代生活细节或网络语境；没有使用时说明不用的理由")).Required(),
		schema.Property("source_type", schema.String("资料类型：web_search、official、news、platform_trend、project_web_reference_brief、RAG、local_reference 等")).Required(),
		schema.Property("source_refs", schema.Array("来源链接、检索词、reference_pack 路径或 RAG trace id", schema.String(""))).Required(),
		schema.Property("retrieved_at", schema.String("检索或简报日期；没有实时检索时写项目简报日期/未知并说明需要刷新")).Required(),
		schema.Property("freshness_requirement", schema.String("时效要求：最新/近90天/年度盘点/稳定常识/无需最新，并说明原因")).Required(),
		schema.Property("usable_details", schema.Array("可转化成正文的细节：物件、界面、流程、口语、空间、价格、制度压力", schema.String(""))).Required(),
		schema.Property("transformation_rule", schema.String("如何从资料转成小说场景，禁止网页摘要式搬运")).Required(),
		schema.Property("do_not_use", schema.Array("不使用的资料、过时梗、低可信内容、版权风险或破坏本书语气的内容", schema.String(""))).Required(),
	)
	trendLanguagePlan := schema.Object(
		schema.Property("item", schema.String("热梗/流行语/网络口头禅或其弱化形态；不用时写 none")).Required(),
		schema.Property("source_context", schema.String("该表达来自哪个平台语境、年龄层、社交场景或项目 web_reference_brief 条目")).Required(),
		schema.Property("character_carrier", schema.String("由哪个角色、群体、手机屏幕、群聊、外放视频或环境噪声承载；默认不由旁白承载")).Required(),
		schema.Property("scene_function", schema.String("它在场景中的功能：误判、嘈杂、时代纹理、关系摩擦、诱导确认、反讽等")).Required(),
		schema.Property("usage_budget", schema.String("使用预算，例如0次、最多1句半截、最多2处群体反应；禁止梗串")).Required(),
		schema.Property("forbidden_usage", schema.String("禁止用法：主角金句、旁白解释、章末钩子、规则条款、过时梗硬贴等")).Required(),
	)
	readerEntertainmentPlan := schema.Object(
		schema.Property("opening_beat", schema.String("前200字内的具体尴尬、冲突、误会或反转；写清谁做什么以及现场反应")).Required(),
		schema.Property("humor_beats", schema.Array("至少2个不同机制的喜剧节拍，每个写清铺垫、承载角色和反应后果；不能都靠热梗", schema.String(""))).Required(),
		schema.Property("immediate_payoffs", schema.Array("至少2个本章页面可见的即时兑现：到账、打脸、关系偏转、结果反噬或新权限", schema.String(""))).Required(),
		schema.Property("procedure_compression", schema.String("哪些流程压缩，以及保留的冲突、笑点或关系变化")).Required(),
		schema.Property("companion_voice_beat", schema.String("系统、搭档或朋友的性格化短回应；无此类角色时写替代反应")).Required(),
		schema.Property("forbidden_comedy", schema.Array("喜剧禁区：降智、梗串、旁白热词、硬抖包袱等", schema.String(""))).Required(),
	)
	groundingDetail := schema.Object(
		schema.Property("detail", schema.String("从外部资料或当代生活观察提炼出的具体细节")).Required(),
		schema.Property("source_ref", schema.String("来源引用、简报条目或 RAG trace")).Required(),
		schema.Property("transformed_as", schema.String("在正文中转成什么：账单、群聊、外放声、物件、客服话术、排队细节、角色动作等")).Required(),
		schema.Property("scene_anchor", schema.String("绑定到哪个 scene_anchor、环境信号或因果节拍")).Required(),
	)
	characterStageRecord := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("time", schema.String("故事内时间点或相对时间")),
		schema.Property("location", schema.String("该角色此刻所处位置")).Required(),
		schema.Property("status", schema.String("此刻状态：存活/受伤/失踪/异化/死亡/待确认等")),
		schema.Property("environment", schema.String("该角色正在承受的现场环境、规则压力或社会压力")).Required(),
		schema.Property("current_action", schema.String("该角色此刻正在做什么；正文可不展示，但时间线必须成立")).Required(),
		schema.Property("pressure", schema.String("推动此角色行动的具体压力")).Required(),
		schema.Property("decision", schema.String("此阶段做出的选择或暂时不做的选择")).Required(),
		schema.Property("mistake_or_misbelief", schema.String("合理误判、错误操作、信息缺口或过度反应")),
		schema.Property("knowledge_boundary", schema.String("此角色此刻知道/不知道什么，后续出场不能越界")).Required(),
		schema.Property("visible_in_chapter", schema.Bool("本章正文是否直接展示")),
		schema.Property("evidence", schema.String("正文或台账中支撑该记录的证据")),
		schema.Property("transport", schema.String("交通工具/移动方式；不能默认瞬移，若未移动写原地/被困/无")),
		schema.Property("travel_time", schema.String("按 book_world 或现实距离估算的移动耗时；未移动也写为什么为0")),
		schema.Property("meeting_constraint", schema.String("本章能否与主角相见、为何不能随叫随到、需要什么交通/凭证/能力")),
		schema.Property("personality_delta", schema.String("本章经历造成的性格、信任、恐惧、价值取向或决策习惯变化")),
		schema.Property("death_state", schema.String("若死亡/失踪/异化/重伤，记录确认程度；否则写存活/未确认/无")),
		schema.Property("protagonist_notice", schema.String("该状态何时、通过谁或什么证据传回主角；若主角已知也说明证据")),
		schema.Property("timeline_consistency", schema.String("如何与主线时间线同步，避免后续突然出现")).Required(),
		schema.Property("next_potential", schema.String("后续可回归时携带的新压力、新信息或新误判")),
		schema.Property("tags", schema.Array("检索标签", schema.String(""))),
	)
	environmentSignal := schema.Object(
		schema.Property("place", schema.String("地点、空间或关键环境单元")),
		schema.Property("visible_state", schema.String("读者能看见/听见/摸到的环境状态")),
		schema.Property("information_carried", schema.String("该环境状态承载的新信息、证据、规则提示或伏笔")),
		schema.Property("pressure_applied", schema.String("该环境对角色选择施加的压力或限制")),
		schema.Property("expected_change", schema.String("章末相较开章，这个环境/物件/空间应发生的状态变化")),
	)
	longRangePromise := schema.Object(
		schema.Property("promise", schema.String("百万字长线承诺，例如资产扩张、亲情营救、审计追债、终局命题等")),
		schema.Property("first_chapter_seed", schema.String("第一章里负责埋下该承诺的具体物件、动作、台词、环境变化或信息缺口")),
		schema.Property("payoff_horizon", schema.String("预计回收/升级区间，例如3章内、第一卷、30-50章、第二卷、终局")),
	)
	longformOpening := schema.Object(
		schema.Property("target_reader", schema.String("本书百万字连载要服务的核心读者与消费期待")),
		schema.Property("opening_hook", schema.String("第一章读者继续读的最短理由：异常、危机、爽点预告或未完成选择")),
		schema.Property("serial_engine", schema.String("能支撑百万字连续推进的发动机：资源/敌人/地图/规则/关系如何不断升级")),
		schema.Property("reader_reward_loop", schema.Array("读者在3章、10章、30章、每卷会反复获得的奖励类型", schema.String(""))),
		schema.Property("long_range_promises", schema.Array("第一章必须埋下的长线承诺及回收周期", longRangePromise)),
		schema.Property("reveal_budget", schema.Array("第一章必须克制不解释的内容，只露证据或问题，不提前给答案", schema.String(""))),
		schema.Property("first_chapter_proof", schema.Array("第一章证明百万字设计可持续的证据：主角方法、世界扩展口、长期敌人/账单/资产/关系线等", schema.String(""))),
		schema.Property("retention_risks", schema.Array("第一章最容易导致读者流失的风险和规避方式", schema.String(""))),
	)
	characterArcTest := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("want", schema.String("此角色眼前外在想要什么")).Required(),
		schema.Property("core_lie", schema.String("此角色当前相信的错误信念、误判或自欺")).Required(),
		schema.Property("need", schema.String("此角色长期真正需要学会/承认什么")).Required(),
		schema.Property("truth", schema.String("本章后或小弧后会逼近的真实认知")).Required(),
		schema.Property("pressure_test", schema.String("本章如何测试 Want/Lie/Need/Truth")).Required(),
		schema.Property("first_mistake", schema.String("本章按当前压力和信息差会犯的具体错、迟疑、误按、误判或自欺")).Required(),
		schema.Property("correction_signal", schema.String("什么可见证据、代价或他人行动会触发修正")).Required(),
		schema.Property("chapter_evidence", schema.String("正文里证明这条弧线测试发生的物件、台词、选择或后果")).Required(),
	)
	readerRewardStep := schema.Object(
		schema.Property("chapter", schema.Int("预计兑现章节；第一章计划至少给 1-5 章窗口")),
		schema.Property("reward", schema.String("该章给读者的具体奖励：小胜、证据、关系变化、资源变化、地图打开等")).Required(),
		schema.Property("cost", schema.String("该奖励对应的新债务、新风险、新敌意或新误解")).Required(),
		schema.Property("hook", schema.String("该奖励如何形成下一章追读")).Required(),
	)
	readerRewardPlan := schema.Object(
		schema.Property("chapter_window", schema.String("奖励规划窗口，例如 1-5 或 1-4")).Required(),
		schema.Property("first_chapter_small_win", schema.String("第一章必须给出的具体小胜/暂缓/证据确认，不能只给危机")).Required(),
		schema.Property("new_debt_or_cost", schema.String("小胜之后立刻留下的代价、债务、尾巴或更深问题")).Required(),
		schema.Property("payoff_visibility", schema.String("读者能在正文哪里看见奖励已经兑现，而不是作者说了算")).Required(),
		schema.Property("traffic_risk", schema.String("若奖励不足会导致的流失风险和本章规避方式")).Required(),
		schema.Property("reward_ladder", schema.Array("未来 3-4 章的奖励/代价阶梯", readerRewardStep)).Required(),
		schema.Property("forbidden_reward_patterns", schema.Array("禁止的奖励方式，如只摆按钮、只承诺无限额度、只解释设定", schema.String(""))),
	)
	retentionSurfaceBeat := schema.Object(
		schema.Property("plan_source", schema.String("来自哪个计划字段或章节契约，例如 required_beats[0]/dialogue_scene_blueprints/security-review")).Required(),
		schema.Property("must_show", schema.String("若 Drafter 选择这一候选拍，读者实际看见的动作、对白、物件变化、选择或后果；不是解释性摘要。字段名为兼容旧数据保留，不代表所有候选都必须写")).Required(),
		schema.Property("reader_payoff", schema.String("这一拍给读者的即时收益：确认、悬念、误判、爽点、关系变化、规则代价等")).Required(),
		schema.Property("scene_vehicle", schema.String("用哪个场景/物件/冲突载体承载，禁止只写成计划说明")).Required(),
		schema.Property("proof_on_page", schema.String("正文里可核对的页面证据，例如某句打断、某个动作、某个物件状态变化")).Required(),
		schema.Property("function_shift", schema.String("这一拍在段落功能上制造的换挡：事故/争执/沉默/证据迟到/生活打断/后果入账等，避免全章同一叙述曲线")),
	)
	readerRetentionPlan := schema.Object(
		schema.Property("surface_beats", schema.Array("从全量计划中筛出的 2-4 个页面候选节拍；Drafter 只选足以完成 required_beats 的最少部分，禁止全部照抄", retentionSurfaceBeat)).Required(),
		schema.Property("latent_context", schema.Array("只留在台账/角色逻辑里的内容：可约束行为，但本章不显性解释、不让旁白摊开", schema.String(""))).Required(),
		schema.Property("reveal_budget", schema.Array("本章必须延后、只露半截或只通过证据暗示的信息；避免把大纲答案一次讲完", schema.String(""))).Required(),
		schema.Property("cut_or_compress", schema.Array("若正文出现会变成结构化清单、说明书或 AI 味的计划材料；应删除、合并进动作或压成半句", schema.String(""))).Required(),
		schema.Property("page_turn_questions", schema.Array("读者读完本章会想继续看的具体问题；必须落到人/物/代价/选择，不写抽象金句", schema.String(""))).Required(),
	)
	evidenceReturnChain := schema.Object(
		schema.Property("offscreen_character", schema.String("离屏/后台事件涉及的角色或群体")).Required(),
		schema.Property("event", schema.String("他/他们在主角视角外实际经历或推动的事件")).Required(),
		schema.Property("evidence", schema.String("这个事件以后怎样以账单、物件、消息、尸体、证词、收据、位置变化等形式回到主角视角")).Required(),
		schema.Property("protagonist_access", schema.String("主角通过什么合法路径得知：通信、亲见、证据传回、能力授权、第三方目击")).Required(),
		schema.Property("return_timing", schema.String("预计在哪章、哪个场景或什么条件下回收")).Required(),
		schema.Property("distortion_or_misread", schema.String("传回信息可能被谁误读、遮挡、延迟或篡改")).Required(),
		schema.Property("chapter_to_resolve", schema.Int("预计回收章节；未知时填最近可检查章节")),
	)
	endingContract := schema.Object(
		schema.Property("ending_mode", schema.String("章末形态：具体后果、物件变化、关系位移、未完成动作、账单落地等")).Required(),
		schema.Property("concrete_anchor", schema.String("章末落点绑定的具体物件、动作、位置、鞋尖、账单、门牌、通信等")).Required(),
		schema.Property("consequence", schema.String("章末相较开章已经发生的不可撤销或待付代价")).Required(),
		schema.Property("next_chapter_pull", schema.String("下一章必须承接的具体方向，不是抽象悬念")).Required(),
		schema.Property("why_not_ui", schema.String("为什么不是给读者看的标准菜单/按钮/机械提示；若出现界面必须说明其诡异性和代价未知")).Required(),
		schema.Property("forbidden_endings", schema.Array("禁用章末：UI选项展示、突然一声响、金句问号、无代价无限爽等", schema.String(""))).Required(),
	)
	dormantPolicy := schema.Object(
		schema.Property("character", schema.String("暂不直接出场或仅后台推进的角色")).Required(),
		schema.Property("status", schema.String("本章时间线上的状态：工作中、被困、未知、休眠、死亡待确认等")).Required(),
		schema.Property("location", schema.String("此角色本章所在位置或不能确定位置的原因")).Required(),
		schema.Property("no_change_reason", schema.String("若本章确实没有推进，为什么静止合理；不能只写未出场")).Required(),
		schema.Property("trigger_condition", schema.String("什么事件会让此角色进入正文、被主角得知或状态改变")).Required(),
		schema.Property("knowledge_boundary", schema.String("主角此刻知道/不知道此角色什么状态")).Required(),
		schema.Property("next_check", schema.String("后续在哪章、哪条线或什么台账里重新检查")).Required(),
	)
	realitySupport := schema.Object(
		schema.Property("domain", schema.String("现实支撑领域：居住/物业/支付/职业/交通/医疗/学校/平台/社交语境等")).Required(),
		schema.Property("source_ref", schema.String("项目 web_reference_brief、RAG、检索链接、官方资料或本地资料来源")).Required(),
		schema.Property("usable_detail", schema.String("可转成小说的具体细节")).Required(),
		schema.Property("transformed_as", schema.String("在正文中如何变形为角色可见的动作、界面、物件、话术或时间成本")).Required(),
		schema.Property("chapter_use", schema.String("本章在哪个场景/角色/交易/路线里使用")).Required(),
		schema.Property("forbidden_direct_use", schema.Array("不能直接搬用的真实名称、敏感热点、网页摘要、过时梗等", schema.String(""))).Required(),
	)
	emotionalLogic := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("physiological_state", schema.String("生理/本能状态：饥饿、疲惫、疼痛、安全感、欲望、能量水平等")).Required(),
		schema.Property("immediate_state", schema.String("即时状态：凌晨/刚受惊/刚吃饱/刚失去某物/注意力焦点/最近强化历史")).Required(),
		schema.Property("baseline_mood", schema.String("本章开局情绪底色")).Required(),
		schema.Property("primary_emotion", schema.String("原始情绪：恐惧、愤怒、爱、悲伤、厌恶、惊讶等")).Required(),
		schema.Property("composite_emotion", schema.String("复合情绪：羞耻、嫉妒、内疚、自豪、羡慕、怜悯等")).Required(),
		schema.Property("emotional_trigger", schema.String("触发情绪变化的具体物件、台词、动作或关系变化")).Required(),
		schema.Property("goal_appraisal", schema.String("角色如何评价该事件对目标/预期/边界的影响")).Required(),
		schema.Property("boundary_threat", schema.String("该事件威胁了什么边界：安全、尊严、身份、关系、资源、身体等")).Required(),
		schema.Property("regulation_strategy", schema.String("情绪调节方式：压抑、转移、合理化、求助、攻击、沉默等")).Required(),
		schema.Property("defense_mechanism", schema.String("防御机制：否认、投射、置换、合理化、反向形成、压抑等")).Required(),
		schema.Property("cognitive_bias", schema.String("认知偏差/启发式：损失厌恶、锚定、确认偏差、现状偏见、可用性启发等")).Required(),
		schema.Property("approach_avoidance", schema.String("趋近 vs 回避张力：想靠近什么、怕失去/遇见什么")).Required(),
		schema.Property("short_long_term_tension", schema.String("短期 vs 长期张力")).Required(),
		schema.Property("self_relationship_tension", schema.String("自我需求 vs 关系/社会期待张力")).Required(),
		schema.Property("conscious_reason", schema.String("角色嘴上/意识里给自己的行动理由")).Required(),
		schema.Property("hidden_reason", schema.String("更深层真实原因：创伤、羞耻、爱、恐惧、意义需求或潜意识重复")).Required(),
		schema.Property("meaning_need", schema.String("意义/目的层：他想证明自己是谁、守住什么人生叙事、对抗什么死亡/虚无焦虑")).Required(),
		schema.Property("metacognition", schema.String("元认知/自控：他能否意识到自己正在冲动、能否反着来")).Required(),
		schema.Property("emotion_led_action", schema.String("本章由情绪主导推出的具体行动，不是事件硬推")).Required(),
		schema.Property("event_completion_role", schema.String("该情绪如何推动、扭曲或完成本章事件")).Required(),
		schema.Property("evidence_in_scene", schema.Array("正文里能看见情绪驱动的证据：动作、沉默、语速、错判、选择、物件使用", schema.String(""))).Required(),
	)
	relationshipEmotionArc := schema.Object(
		schema.Property("pair", schema.Array("关系双方；亲情/合作/敌对/恋爱潜势都可记录", schema.String(""))).Required(),
		schema.Property("relationship_type", schema.String("关系类型：亲情、合作、邻居、敌对、债务、暧昧、恋爱、旧识等")).Required(),
		schema.Property("current_bond", schema.String("当前情感连接或裂缝")).Required(),
		schema.Property("emotional_want", schema.String("双方或其中一方真正想从关系里得到什么")).Required(),
		schema.Property("fear", schema.String("这段关系最怕发生什么")).Required(),
		schema.Property("power_balance", schema.String("权力/地位/资源/信息不对等")).Required(),
		schema.Property("intimacy_stage", schema.String("亲密阶段：陌生、试探、互利、信任萌芽、吸引、暧昧、承诺、破裂等")).Required(),
		schema.Property("trust_debt", schema.String("信任、亏欠、承诺、筹码或背叛记录")).Required(),
		schema.Property("conflict_trigger", schema.String("本章让关系推进或冲突的情绪触发点")).Required(),
		schema.Property("attachment_or_love_language", schema.String("依恋/表达方式：照顾、确认、物质支持、身体距离、语言安抚、边界尊重等")).Required(),
		schema.Property("boundary", schema.String("本章不能越过的关系边界")).Required(),
		schema.Property("romance_potential", schema.String("恋爱潜势或明确无恋爱：吸引来源、阻碍、禁忌、节奏；没有就说明 none 和原因")).Required(),
		schema.Property("next_emotional_beat", schema.String("下一次关系推进应带来的情绪变化")).Required(),
		schema.Property("protagonist_knowledge_boundary", schema.String("主角此刻是否知道这段关系信息，不知道则说明传回路径")).Required(),
	)
	visualDesign := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("silhouette", schema.String("轮廓/形状语言：圆/方/尖/塌/挺/窄/厚等给读者的第一印象")).Required(),
		schema.Property("face_and_hair", schema.String("长相、脸部特征、发型、发质、修整程度")).Required(),
		schema.Property("clothing_style", schema.String("穿衣风格：材质、剪裁、颜色、职业/阶层/生活状态信息")).Required(),
		schema.Property("color_palette", schema.String("颜色与视觉情绪")).Required(),
		schema.Property("body_language", schema.String("站姿、手部习惯、视线、走路、缩/撑/靠/避")).Required(),
		schema.Property("signature_object", schema.String("标志物：包、伞、鞋、戒指、卡、账本、工具、气味等")).Required(),
		schema.Property("first_impression", schema.String("读者第一次看见他应感到什么")).Required(),
		schema.Property("status_wear", schema.String("本章状态如何改变外观：雨水、灰、血、皱、破、异化、疲惫")).Required(),
		schema.Property("change_rule", schema.String("后续随成长、堕落、受伤、恋爱或权力变化，外观如何变化")).Required(),
		schema.Property("scene_use", schema.String("本章哪一处会用外观推动识别、情绪或关系，而不是纯描写")).Required(),
		schema.Property("do_not_use", schema.Array("禁用空泛外貌词、现代真实品牌、与世界不合的服装或同质化描写", schema.String(""))).Required(),
		schema.Property("material_source", schema.String("素材来源声明：craft_recall 命中的 source_path、book_facts（沿用本书已实例化事实）或 no_material（检索无料、自行设计并说明依据）")).Required(),
	)
	characterKitItem := schema.Object(
		schema.Property("name", schema.String("名称")).Required(),
		schema.Property("category", schema.String("类别：长剑/符箓/义体/账本/防具等")),
		schema.Property("description", schema.String("形制、来历、可见特征")),
		schema.Property("material_source", schema.String("素材来源：craft_recall source_path / book_facts / no_material")).Required(),
		schema.Property("evidence", schema.String("首次出场章节或台账证据")),
	)
	characterKitAbility := schema.Object(
		schema.Property("name", schema.String("能力名")).Required(),
		schema.Property("codex_tier", schema.String("对应 world_codex.ability_tiers 的分级名；无法典时写 uncodexed 并在 codex_compliance 说明")).Required(),
		schema.Property("current_level", schema.String("当前等级/熟练度")).Required(),
		schema.Property("usage_scope", schema.String("本章允许使用范围；不得越级")).Required(),
		schema.Property("cost", schema.String("使用代价")),
		schema.Property("upgrade_trigger", schema.String("升级触发条件")),
		schema.Property("material_source", schema.String("素材来源：craft_recall source_path / book_facts / no_material")).Required(),
		schema.Property("evidence", schema.String("能力现状的正文/台账证据")),
	)
	characterKit := schema.Object(
		schema.Property("character", schema.String("角色名")).Required(),
		schema.Property("first_appearance", schema.Bool("是否本章首次出场；首次出场必须先 craft_recall 取料或显式 no_material")),
		schema.Property("appearance_ref", schema.String("外貌引用：visual_design 条目或 dossier；首次出场角色必填")),
		schema.Property("weapons", schema.Array("武器", characterKitItem)),
		schema.Property("equipment", schema.Array("装备/法宝/道具", characterKitItem)),
		schema.Property("skills", schema.Array("技能/法术/手艺", characterKitItem)),
		schema.Property("abilities", schema.Array("能力条目：对齐 world_codex 分级与当前卷上限", characterKitAbility)),
		schema.Property("codex_compliance", schema.String("合规声明：未越过 world_codex/当前卷上限的依据；无法典项目说明当前约束来源")).Required(),
	)
	worldBackgroundLayers := schema.Object(
		schema.Property("physical_space", schema.String("物理/空间层：地理位置、地形、气候、光照、出口、封闭/开阔结构、距离和物理法则如何影响权力动态")).Required(),
		schema.Property("time_layer", schema.String("时间层：时刻、日期/季节、历史时间点、倒计时、纪念日、仪式窗口如何改变选择")).Required(),
		schema.Property("social_institution", schema.String("社会/制度层：政治、法律、阶级、经济、教育、医疗、治安、官僚等显规则如何施压")).Required(),
		schema.Property("cultural_norm", schema.String("文化/规范层：礼俗、禁忌、羞耻、方言、审美、官方/民间叙事和潜规则")).Required(),
		schema.Property("relationship_network", schema.String("关系/网络层：血缘、师承、雇佣、债务、盟友/敌对、中立和信息渠道")).Required(),
		schema.Property("economic_resource", schema.String("经济/资源层：货币、价格、物流、信用、稀缺资源和谁掌握它")).Required(),
		schema.Property("conflict_tension", schema.String("冲突/张力层：政治矛盾、隐藏暗流、历史遗留问题、个体/集体冲突和即时危险")).Required(),
		schema.Property("social_mood", schema.String("氛围/情绪层：社会情绪、谣言、集体恐慌/麻木、异兆和预兆")).Required(),
		schema.Property("cosmology_meta_rule", schema.String("元背景/宇宙观层：魔法/诡异/科技/因果/轮回/平行世界规则、代价和边界")).Required(),
		schema.Property("narrative_meta", schema.String("叙事层：读者知道什么、角色知道什么、隐藏什么、POV 可信度和本章节奏职责")).Required(),
		schema.Property("event_activation", schema.String("这些层如何共同激活本章事件：不能只是装饰，必须说明哪一层撕开稳定系统")).Required(),
	)
	informationAsymmetry := schema.Object(
		schema.Property("subject", schema.String("信息差对象：某条规则、关系、资源、谣言、身份或阴谋")).Required(),
		schema.Property("reader_knows", schema.Array("读者此刻知道或被允许推测的信息", schema.String(""))).Required(),
		schema.Property("protagonist_knows", schema.Array("主角此刻经由亲见/通信/证据能确认的信息", schema.String(""))).Required(),
		schema.Property("character_knows", schema.Array("相关角色或势力此刻知道的信息", schema.String(""))).Required(),
		schema.Property("character_mistakes", schema.Array("相关角色误以为知道、误判或被诱导相信的内容", schema.String(""))).Required(),
		schema.Property("character_pretends", schema.Array("谁假装不知道/假装知道了什么", schema.String(""))).Required(),
		schema.Property("hidden_from_reader", schema.Array("暂时不能向读者揭示的内容", schema.String(""))).Required(),
		schema.Property("reveal_condition", schema.String("何时、通过什么证据或代价揭示")).Required(),
		schema.Property("tension_function", schema.String("此信息差在本章制造悬疑、反转、误判、关系压力或读者期待的功能")).Required(),
	)
	hiddenRulePressure := schema.Object(
		schema.Property("domain", schema.String("潜规则所在领域：江湖、朝廷、物业、账单、婚恋、商贸、学校、帮派、阴司等")).Required(),
		schema.Property("visible_rule", schema.String("表面规则/官方说法")).Required(),
		schema.Property("hidden_rule", schema.String("真正决定行为的潜规则")).Required(),
		schema.Property("cultural_norm", schema.String("支撑潜规则的文化规范、禁忌、羞耻或默契")).Required(),
		schema.Property("who_benefits", schema.String("谁从潜规则受益")).Required(),
		schema.Property("who_pays", schema.String("谁为潜规则付代价")).Required(),
		schema.Property("violation_cost", schema.String("违反潜规则的代价：失去资源、名誉、保护、资格、生命等")).Required(),
		schema.Property("scene_evidence", schema.String("正文里能看见潜规则的证据：称呼、站位、交易、沉默、手续、物价、眼神等")).Required(),
	)
	socialMoodRumor := schema.Object(
		schema.Property("group", schema.String("传播或承受情绪的群体：住户、商人、宗门弟子、市民、官吏、患者等")).Required(),
		schema.Property("mood", schema.String("当前社会情绪：恐慌、麻木、狂热、怨恨、侥幸、看热闹等")).Required(),
		schema.Property("rumor", schema.String("街头巷尾/群聊/茶馆/内网正在传什么")).Required(),
		schema.Property("source", schema.String("流言来源：目击、官方公告、黑市、社群、神谕、失真转述等")).Required(),
		schema.Property("spread_path", schema.String("传播路径：楼道、群聊、摊贩、驿站、学堂、平台、酒馆等")).Required(),
		schema.Property("reliability", schema.String("可信度、偏差或被操纵程度")).Required(),
		schema.Property("behavior_effect", schema.String("该情绪/流言如何改变群体或角色行为")).Required(),
		schema.Property("protagonist_access", schema.String("主角通过什么渠道接触或暂时接触不到它")).Required(),
	)
	ritualCalendarWindow := schema.Object(
		schema.Property("time", schema.String("故事内时刻/日期/季节/倒计时/纪念日")).Required(),
		schema.Property("calendar_type", schema.String("时间类型：日常时辰、节日、仪式、忌日、月相、潮汐、账单日、考试日、封城日等")).Required(),
		schema.Property("ritual_or_deadline", schema.String("本章实际存在的仪式、礼俗、deadline 或窗口")).Required(),
		schema.Property("social_meaning", schema.String("它在社会/文化/关系中的含义")).Required(),
		schema.Property("practical_constraint", schema.String("它带来的现实限制：关门、涨价、封路、禁忌、执法、资源短缺等")).Required(),
		schema.Property("emotional_charge", schema.String("它对角色的情感压力：忌日、生日、承诺、羞耻、恐惧、纪念等")).Required(),
		schema.Property("missed_cost", schema.String("错过窗口的代价")).Required(),
		schema.Property("scene_use", schema.String("本章在哪个场景使用这个时间窗口")).Required(),
	)
	structuralResource := schema.Object(
		schema.Property("resource", schema.String("结构性资源：盐铁、漕运、印刷、兵器、药品、信号、账本、门牌、人才、交通、能源、灵气等")).Required(),
		schema.Property("controller", schema.String("谁控制资源")).Required(),
		schema.Property("scarcity_reason", schema.String("为什么稀缺：地理、制度、封锁、灾变、垄断、污染、技术限制等")).Required(),
		schema.Property("access_rule", schema.String("正式准入规则")).Required(),
		schema.Property("black_market_or_informal_path", schema.String("非正式路径、潜规则或黑市路径；没有则说明 none 和原因")).Required(),
		schema.Property("price_or_cost", schema.String("价格、对价、风险或关系成本")).Required(),
		schema.Property("power_effect", schema.String("该资源如何生成权力，而不是只作为物品")).Required(),
		schema.Property("chapter_pressure", schema.String("本章它如何对角色选择施压")).Required(),
	)
	cosmologyCheck := schema.Object(
		schema.Property("layer", schema.String("元背景层：魔法、诡异规则、科技、神祇、因果、轮回、灵界、AI 等")).Required(),
		schema.Property("rule", schema.String("规则本身")).Required(),
		schema.Property("cost", schema.String("使用/违背/触发的代价")).Required(),
		schema.Property("boundary", schema.String("规则边界：不能做什么，什么情况下失效")).Required(),
		schema.Property("exception_condition", schema.String("例外条件；没有例外写 none，不得临场开挂")).Required(),
		schema.Property("evidence", schema.String("正文或台账中能看见的证据")).Required(),
		schema.Property("failure_mode", schema.String("若不遵守这条规则，本章会出现的崩坏方式")).Required(),
	)
	conflictWebNode := schema.Object(
		schema.Property("parties", schema.Array("冲突参与方：角色、势力、群体、制度、资源控制者", schema.String(""))).Required(),
		schema.Property("conflict_type", schema.String("冲突类型：资源、身份、权力、债务、信仰、亲密、历史仇恨、信息封锁等")).Required(),
		schema.Property("open_goal", schema.String("公开目标")).Required(),
		schema.Property("hidden_agenda", schema.String("隐藏目标或不能明说的算盘")).Required(),
		schema.Property("resource_stake", schema.String("争夺或押上的资源")).Required(),
		schema.Property("information_gap", schema.String("谁知道/不知道/误以为知道什么")).Required(),
		schema.Property("time_pressure", schema.String("本章倒计时或窗口压力")).Required(),
		schema.Property("current_balance", schema.String("冲突开始前的稳定状态")).Required(),
		schema.Property("destabilizer", schema.String("本章打破平衡的因素")).Required(),
		schema.Property("next_escalation", schema.String("后续 3-4 章可升级的方向")).Required(),
	)
	narrativeTensionMatrix := schema.Object(
		schema.Property("stability_turbulence", schema.String("稳定 vs 动荡：原本稳定系统是什么，被什么打破，主角是催化剂还是被打破者")).Required(),
		schema.Property("explicit_hidden_rules", schema.String("显规则 vs 潜规则：表面如何说，背后真正按什么运行")).Required(),
		schema.Property("information_gap", schema.String("信息差结构：角色/主角/读者各自知道、误判、假装不知道什么")).Required(),
		schema.Property("time_pressure_preparation", schema.String("时间压力 vs 准备时间：倒计时、窗口、未准备好的人如何制造张力")).Required(),
		schema.Property("why_event_now", schema.String("为什么这件事必须在本章此刻发生")).Required(),
		schema.Property("reader_question", schema.String("本章希望读者带到下一章的具体问题")).Required(),
		schema.Property("pov_boundary", schema.String("POV 信息边界：正文不能越过哪些主角可见证据")).Required(),
	)
	causalBeat := schema.Object(
		schema.Property("cause", schema.String("触发原因或前置事实")),
		schema.Property("character_choice", schema.String("角色基于欲望、压力和信息差做出的选择")),
		schema.Property("world_response", schema.String("世界规则、组织制度或环境对选择的即时反馈")),
		schema.Property("story_result", schema.String("该反馈带来的剧情后果或状态位移")),
	)
	causalSimulation := schema.Object(
		schema.Property("world_simulation_id", schema.String("simulate_chapter_world finalize 返回的稳定 ID")),
		schema.Property("protagonist_decision", schema.String("全角色世界模拟投影出的主角选择，必须原样引用")),
		schema.Property("project_promise", schema.String("本章承接的整本书核心承诺，例如女性夺回解释权、可见事实回收、升级爽点等")),
		schema.Property("chapter_function", schema.String("本章在全书/卷/弧中的功能；第一章必须写明开局承诺、核心问题和主角初始选择")),
		schema.Property("context_sources", schema.Array("本次推演实际使用的上下文来源；若存在重启策略必须列出 simulation_restart_policy，并至少列出 world_foundation、character_dossiers、current_chapter_outline、future_outline_window、chapter_contract/progression_snapshot、characters、world_rules/book_world、recent_summaries/previous_tail、character_continuity/character_stage_records/chapter_world_deltas、resource_audit/foreshadow/relationship_state、prewrite_storycraft_plan、user_rules/writing_engine、reference_pack.references、selected_memory.rag_recall、web_reference_brief/web_search 中实际可见的项", schema.String(""))),
		req(schema.Property("writing_norms_applied", schema.Array("本章写作规范执行计划：必须覆盖 writing_engine/user_rules/anti_ai_tone/human_feel_craft/writing_techniques_digest/web_reference_guidelines/longform_ai_detector 中实际可见且相关的规则", writingNormApplication))),
		req(schema.Property("anti_ai_execution_plan", antiAIExecutionPlan)),
		req(schema.Property("external_reference_plan", schema.Array("外部资料、网络检索、项目 web_reference_brief 和 RAG 召回如何进入正文；不用网络资料也要说明不用原因", externalReferencePlan))),
		req(schema.Property("trend_language_plan", schema.Array("热梗/流行语的受控使用计划；不用时写 item=none 并说明禁用原因", trendLanguagePlan))),
		req(schema.Property("reader_entertainment_plan", readerEntertainmentPlan)),
		req(schema.Property("grounding_details", schema.Array("由外部资料转化出的具体生活/制度/物件锚点", groundingDetail))),
		req(schema.Property("offscreen_character_stage", schema.Array("本章所有关键角色在正文内外的同时间线行动、误判和决策；主角、关键配角、短期会回归人物都要覆盖", characterStageRecord))),
		schema.Property("longform_opening", longformOpening),
		req(schema.Property("character_arc_tests", schema.Array("人物 Want/Lie/Need/Truth、本章合理犯错和纠错触发；主角必须覆盖，关键配角按章节压力覆盖", characterArcTest))),
		req(schema.Property("reader_reward_plan", readerRewardPlan)),
		req(schema.Property("reader_retention_plan", readerRetentionPlan)),
		req(schema.Property("evidence_return_chains", schema.Array("主角视角外事件如何以证据回到主线，保证梗、伏笔和配角线可回收", evidenceReturnChain))),
		req(schema.Property("ending_consequence_contract", endingContract)),
		req(schema.Property("dormant_character_policy", schema.Array("未出场/休眠/暂不推进角色的最小状态和后续检查；没有休眠角色也要写 none 占位", dormantPolicy))),
		req(schema.Property("reality_support_plan", schema.Array("现实资料如何支撑职业、居住、交通、支付、交易、生活细节和网络语境", realitySupport))),
		req(schema.Property("emotional_logic", schema.Array("每个关键角色本章的身体状态、情绪评估、创伤/防御/偏差/意义/元认知，以及情绪如何完成事件", emotionalLogic))),
		req(schema.Property("relationship_emotion_arcs", schema.Array("亲情、合作、敌对、债务、恋爱潜势等关系的情绪推进、亲密阶段、信任债和边界", relationshipEmotionArc))),
		req(schema.Property("visual_design", schema.Array("角色外貌、发型、穿衣、轮廓、色彩、标志物和状态磨损；用于避免角色空白纸", visualDesign))),
		req(schema.Property("character_kit", schema.Array("本章关键角色的武器/装备/技能/能力套件；素材来源与法典合规必须声明", characterKit))),
		req(schema.Property("world_background_layers", worldBackgroundLayers)),
		req(schema.Property("information_asymmetry", schema.Array("角色/主角/读者之间的信息差结构；用于防止突然全知和支撑悬疑/反转", informationAsymmetry))),
		req(schema.Property("hidden_rule_pressure", schema.Array("潜规则、文化规范和违反成本；用于让世界不只是制度条款", hiddenRulePressure))),
		req(schema.Property("social_mood_rumors", schema.Array("社会情绪和流言流；用于让城市/人群随时间线变化", socialMoodRumor))),
		req(schema.Property("ritual_calendar", schema.Array("节日/纪念日/deadline/仪式/账单日等时间窗口", ritualCalendarWindow))),
		req(schema.Property("structural_resources", schema.Array("结构性稀缺资源与控制权；权力来源必须可追踪", structuralResource))),
		req(schema.Property("cosmology_checks", schema.Array("元背景/宇宙观规则、代价、边界、例外条件和失败模式", cosmologyCheck))),
		req(schema.Property("conflict_web", schema.Array("多角色/多势力矛盾网，保证长篇冲突不散", conflictWebNode))),
		req(schema.Property("narrative_tension_matrix", narrativeTensionMatrix)),
		schema.Property("initial_state", schema.Array("关键角色开章状态", characterState)),
		schema.Property("voice_logic", schema.Array("关键角色本章说话逻辑：证据来源、常用话术、禁用偏移和对话自检", characterVoiceLogic)),
		schema.Property("dialogue_scene_blueprints", schema.Array("关键对白场景蓝图：按场景压力、情绪温度、关系权力和角色目标选择对话模式；对白先入场只是一种 opening_strategy", dialogueSceneBlueprint)),
		schema.Property("crowd_roles", schema.Array("捧场/凑数/群体角色的轻量设计；不作为完整人物动力学，除非升级为关键角色", crowdRole)),
		schema.Property("review_refinement", reviewRefinement),
		schema.Property("environment_state", schema.Array("环境信息性规划：地点/物件/空间如何承载信息、规则压力和状态变化", environmentSignal)),
		schema.Property("world_rules_in_force", schema.Array("本章会实际施压的世界/制度规则", schema.String(""))),
		schema.Property("information_gaps", schema.Array("信息差、误解、隐瞒和未授权内容", schema.String(""))),
		schema.Property("causal_beats", schema.Array("触发 -> 选择 -> 反馈 -> 后果的因果节拍", causalBeat)),
		schema.Property("decision_points", schema.Array("本章必须落成选择的节点", schema.String(""))),
		schema.Property("outcome_shift", schema.Array("章末相较开章必须改变的状态", schema.String(""))),
		schema.Property("scene_constraints", schema.Array("写作限制：视角、证据边界、不能提前解释的内容", schema.String(""))),
	)
	return causalSimulation
}
