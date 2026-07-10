package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SimulateChapterWorldTool persists the all-character simulation that must
// precede a POV chapter plan. Calls are staged so a large cast can be submitted
// in small batches without losing prior decisions.
type SimulateChapterWorldTool struct {
	store *store.Store
}

const chapterWorldSimulationBatchLimit = 8

func NewSimulateChapterWorldTool(store *store.Store) *SimulateChapterWorldTool {
	return &SimulateChapterWorldTool{store: store}
}

func (t *SimulateChapterWorldTool) Name() string                           { return "simulate_chapter_world" }
func (t *SimulateChapterWorldTool) Label() string                          { return "全角色世界推演" }
func (t *SimulateChapterWorldTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SimulateChapterWorldTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SimulateChapterWorldTool) Description() string {
	return "在章节 plan 之前推进单一世界中的全部实名角色。每个角色都必须按自己的目标、压力、资源和知识边界选择行动，写明决定理由，并携带至少一个会改变世界或主角选项的蝴蝶效应。必须分批提交 character_decisions，每批最多8名角色，最后 finalize=true；完成后 POV plan 只能引用返回的 simulation_id 和 protagonist_projection，正文不得直接泄露 hidden/delayed 信息。空补丁会被拒绝。"
}

func (t *SimulateChapterWorldTool) Schema() map[string]any {
	effect := schema.Object(
		schema.Property("effect", schema.String("该决定引发的状态变化")).Required(),
		schema.Property("targets", schema.Array("受影响角色、关系、资源或势力", schema.String(""))),
		schema.Property("transmission_path", schema.String("影响如何传播；不传播到主角也要说明阻断路径")).Required(),
		schema.Property("arrival_chapter", schema.Int("最早影响主角或世界局面的章号，不得早于当前章")).Required(),
		schema.Property("visibility", schema.Enum("主视角可见性", "visible", "delayed", "hidden")).Required(),
		schema.Property("protagonist_impact", schema.String("如何改变主角可见事实、压力、资源或可选项；无即时接触时写明延迟影响")).Required(),
	)
	decision := schema.Object(
		schema.Property("character", schema.String("characters.json 中的角色实名")).Required(),
		schema.Property("time", schema.String("本章共同时间线中的时点")),
		schema.Property("location", schema.String("角色所在位置")).Required(),
		schema.Property("current_goal", schema.String("角色自己的当前目标")).Required(),
		schema.Property("pressure", schema.String("推动或限制其选择的压力")).Required(),
		schema.Property("resources", schema.Array("可使用或受限的资源", schema.String(""))),
		schema.Property("knowledge_boundary", schema.String("角色知道、不知道和不能提前知道什么")).Required(),
		schema.Property("available_options", schema.Array("按当前信息真正可选的行动，至少两个；等待或拒绝也可是选项", schema.String(""))).Required(),
		schema.Property("decision", schema.String("角色最终选择")).Required(),
		schema.Property("decision_reason", schema.String("为何此人此刻会这样选，必须锚定目标、压力、资源、关系或误判")).Required(),
		schema.Property("action", schema.String("选择转成的实际行动")).Required(),
		schema.Property("action_duration", schema.String("现实耗时，例如十分钟、两天、三周；复杂项目不得用一章默认完成")).Required(),
		schema.Property("completion_state", schema.Enum("本章结束时完成度", "instant", "started", "in_progress", "completed", "blocked")).Required(),
		schema.Property("immediate_result", schema.String("行动的即时世界反馈")).Required(),
		schema.Property("state_after", schema.String("行动后的状态或下一倾向")).Required(),
		schema.Property("visible_to_pov", schema.Bool("本章主视角是否直接看见")),
		schema.Property("butterfly_effects", schema.Array("该决定携带的下游影响，至少一个", effect)).Required(),
	)
	projection := schema.Object(
		schema.Property("protagonist", schema.String("主视角角色实名")).Required(),
		schema.Property("observable_effects", schema.Array("本章主角可依法感知的影响", schema.String(""))).Required(),
		schema.Property("hidden_pressures", schema.Array("已在世界发生但主角暂时不知道的压力", schema.String(""))).Required(),
		schema.Property("available_options", schema.Array("世界推演后主角真正拥有的选项", schema.String(""))).Required(),
		schema.Property("chosen_decision", schema.String("主角最终选择")).Required(),
		schema.Property("decision_reason", schema.String("主角基于可见证据和自身目标作此选择的理由")).Required(),
		schema.Property("plan_constraints", schema.Array("POV plan 必须遵守的信息和因果边界", schema.String(""))).Required(),
		schema.Property("causal_chain", schema.Array("全角色决定如何汇聚为主角选择的因果链", schema.String(""))).Required(),
	)
	rewriteCoverage := schema.Object(
		schema.Property("fact", schema.String("rewrite_source.chapter.preserve_facts 中必须原样抄录的一条事实")).Required(),
		schema.Property("simulation_evidence", schema.Array("该事实由哪些角色决定、行动、结果和蝴蝶效应承接；至少一项", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号；缺省用当前章")),
		schema.Property("time_window", schema.String("本章覆盖的现实故事时间窗口；复杂项目按真实耗时跨章推进")),
		schema.Property("character_decisions", schema.Array("本批角色决定；同名角色后批覆盖前批；每批最多8名，剩余角色下次补", decision)),
		schema.Property("protagonist_projection", projection),
		schema.Property("rewrite_fact_coverage", schema.Array("仅返工章需要：逐条证明保留事实已进入本轮世界模拟", rewriteCoverage)),
		schema.Property("sources", schema.Array("本次推演依据的 tick、角色档案、台账、大纲和规则", schema.String(""))),
		schema.Property("finalize", schema.Bool("全角色覆盖后传 true，生成正式 simulation_id")),
	)
}

func (t *SimulateChapterWorldTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter               int                                  `json:"chapter"`
		TimeWindow            string                               `json:"time_window"`
		CharacterDecisions    []domain.CharacterWorldDecision      `json:"character_decisions"`
		ProtagonistProjection domain.ProtagonistDecisionProjection `json:"protagonist_projection"`
		RewriteFactCoverage   []domain.ChapterRewriteFactCoverage  `json:"rewrite_fact_coverage"`
		Sources               []string                             `json:"sources"`
		Finalize              bool                                 `json:"finalize"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		a.Chapter = inProgressChapterOf(t.store)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter 缺失且无法推断当前章: %w", errs.ErrToolArgs)
	}
	if len(a.CharacterDecisions) > chapterWorldSimulationBatchLimit {
		return nil, fmt.Errorf("simulate_chapter_world 单批最多提交%d名角色，当前=%d；请按 gaps 分成多批，避免大 JSON 在模型末端丢成空参数: %w", chapterWorldSimulationBatchLimit, len(a.CharacterDecisions), errs.ErrToolArgs)
	}
	if skipped, _, err := ensureChapterPlannable(t.store, a.Chapter); err != nil || skipped != nil {
		return skipped, err
	}
	// A valid finalized simulation is immutable for its exact rewrite source and
	// current cast. Planner may defensively call this tool again; return the
	// existing projection instead of opening a second partial that can overwrite
	// already completed all-character work.
	if finalized, loadErr := t.store.LoadChapterWorldSimulation(a.Chapter); loadErr != nil {
		return nil, fmt.Errorf("load finalized chapter simulation: %w", loadErr)
	} else if finalized != nil && len(chapterWorldSimulationGaps(t.store, *finalized)) == 0 {
		if err := t.store.DeleteChapterWorldSimulationPartial(a.Chapter); err != nil {
			return nil, fmt.Errorf("cleanup redundant chapter simulation partial: %w", err)
		}
		if meta, metaErr := t.store.RunMeta.Load(); metaErr == nil && meta != nil &&
			strings.HasPrefix(strings.TrimSpace(meta.PendingSteer), "Pipeline world-simulation repair") {
			if err := t.store.RunMeta.ClearPendingSteer(); err != nil {
				return nil, fmt.Errorf("clear redundant world simulation repair steer: %w", err)
			}
		}
		return json.Marshal(map[string]any{
			"simulated":              true,
			"reused":                 true,
			"chapter":                a.Chapter,
			"simulation_id":          finalized.SimulationID,
			"protagonist_projection": finalized.ProtagonistProjection,
			"next_step":              "正式 world simulation 已与当前角色册及 rewrite source 对齐；不得重新推演。立即基于 protagonist_projection 调用 plan_structure。",
		})
	}

	partial, err := t.store.LoadChapterWorldSimulationPartial(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter simulation partial: %w", err)
	}
	if partial == nil {
		partial = &domain.ChapterWorldSimulation{Version: 1, Chapter: a.Chapter}
	}
	partial.CharacterDecisions = canonicalizeCharacterWorldDecisions(t.store, partial.CharacterDecisions)
	a.CharacterDecisions = canonicalizeCharacterWorldDecisions(t.store, a.CharacterDecisions)
	if strings.TrimSpace(a.TimeWindow) == "" && len(a.CharacterDecisions) == 0 &&
		strings.TrimSpace(a.ProtagonistProjection.Protagonist) == "" && len(a.RewriteFactCoverage) == 0 &&
		len(a.Sources) == 0 && !a.Finalize {
		return nil, fmt.Errorf("simulate_chapter_world 空提交无效：必须补1-%d名 character_decisions、rewrite_fact_coverage、protagonist_projection 或 finalize。当前缺口：%s: %w", chapterWorldSimulationBatchLimit,
			strings.Join(chapterWorldSimulationGaps(t.store, *partial), "；"), errs.ErrToolArgs)
	}
	rewriteSource, _, _, rewriteErr := loadChapterRewriteSource(t.store, a.Chapter)
	if rewriteErr != nil {
		return nil, rewriteErr
	}
	if rewriteSource != nil {
		partial.RewriteSource = rewriteSource
		partial.Sources = appendUniqueString(partial.Sources, rewriteSourceToken(rewriteSource))
		partial.Sources = appendUniqueString(partial.Sources, rewriteBriefToken(rewriteSource))
	}
	if strings.TrimSpace(a.TimeWindow) != "" {
		partial.TimeWindow = strings.TrimSpace(a.TimeWindow)
	}
	partial.CharacterDecisions = mergeCharacterWorldDecisions(partial.CharacterDecisions, a.CharacterDecisions)
	partial.RewriteFactCoverage = mergeRewriteFactCoverage(partial.RewriteFactCoverage, a.RewriteFactCoverage)
	if strings.TrimSpace(a.ProtagonistProjection.Protagonist) != "" {
		partial.ProtagonistProjection = a.ProtagonistProjection
	}
	normalizeProtagonistProjection(t.store, partial)
	for _, source := range a.Sources {
		partial.Sources = appendUniqueString(partial.Sources, source)
	}
	if progress, loadErr := t.store.Progress.Load(); loadErr == nil && progress != nil {
		partial.GenerationID = progress.GenerationID
	}
	if tick, loadErr := t.store.WorldSim.LoadTick(); loadErr == nil && tick != nil {
		partial.BaseTickID = tick.TickID
	}
	partial.GeneratedAt = time.Now().Format(time.RFC3339)
	if err := t.store.SaveChapterWorldSimulationPartial(*partial); err != nil {
		return nil, fmt.Errorf("save chapter simulation partial: %w", err)
	}

	gaps := chapterWorldSimulationGaps(t.store, *partial)
	if !a.Finalize {
		return json.Marshal(map[string]any{
			"staged":             "chapter_world_simulation",
			"chapter":            a.Chapter,
			"characters_present": characterDecisionNames(partial.CharacterDecisions),
			"gaps":               gaps,
			"next_step":          "继续分批调用 simulate_chapter_world，每批只补 gaps 中最多8名角色；补齐后单独提交 protagonist_projection 并传 finalize=true。禁止空提交，不要开始 plan_structure。",
		})
	}
	if len(gaps) > 0 {
		return nil, fmt.Errorf("第 %d 章全角色世界推演未完成：%s: %w", a.Chapter, strings.Join(gaps, "；"), errs.ErrToolPrecondition)
	}
	partial.SimulationID = chapterWorldSimulationID(*partial)
	if err := t.store.SaveChapterWorldSimulation(*partial); err != nil {
		return nil, fmt.Errorf("save chapter world simulation: %w", err)
	}
	// A POV plan partial is version-bound to the simulation it projected. Once
	// a new simulation finalizes, keeping that partial invites plan_details to
	// patch an obsolete skeleton. Force the next Router turn through structure.
	if err := t.store.Drafts.DeleteChapterPlanPartial(a.Chapter); err != nil {
		return nil, fmt.Errorf("invalidate stale chapter plan partial: %w", err)
	}
	if meta, loadErr := t.store.RunMeta.Load(); loadErr == nil && meta != nil &&
		strings.HasPrefix(strings.TrimSpace(meta.PendingSteer), "Pipeline world-simulation repair") {
		if err := t.store.RunMeta.ClearPendingSteer(); err != nil {
			return nil, fmt.Errorf("clear completed world simulation repair steer: %w", err)
		}
	}
	if err := t.store.DeleteChapterWorldSimulationPartial(a.Chapter); err != nil {
		return nil, fmt.Errorf("cleanup chapter simulation partial: %w", err)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.ChapterScope(a.Chapter), "chapter_world_simulation", fmt.Sprintf("meta/chapter_simulations/%03d.json", a.Chapter)); err != nil {
		return nil, fmt.Errorf("checkpoint chapter world simulation: %w", err)
	}
	return json.Marshal(map[string]any{
		"simulated":              true,
		"chapter":                a.Chapter,
		"simulation_id":          partial.SimulationID,
		"protagonist_projection": partial.ProtagonistProjection,
		"next_step":              "基于 protagonist_projection 调用 plan_structure，再用 plan_details 写入 world_simulation_id 和 protagonist_decision；正文只渲染主角可见信息。",
	})
}

func normalizeProtagonistProjection(st *store.Store, simulation *domain.ChapterWorldSimulation) {
	if st == nil || simulation == nil {
		return
	}
	protagonist := strings.TrimSpace(inferCommitProtagonist(st))
	if protagonist == "" {
		return
	}
	projection := &simulation.ProtagonistProjection
	if strings.TrimSpace(projection.Protagonist) == "" {
		projection.Protagonist = protagonist
	}
	if strings.TrimSpace(projection.Protagonist) != protagonist {
		return
	}
	for _, decision := range simulation.CharacterDecisions {
		if strings.TrimSpace(decision.Character) != protagonist || strings.TrimSpace(decision.Decision) == "" {
			continue
		}
		projection.ChosenDecision = strings.TrimSpace(decision.Decision)
		return
	}
}

func mergeCharacterWorldDecisions(existing, incoming []domain.CharacterWorldDecision) []domain.CharacterWorldDecision {
	out := append([]domain.CharacterWorldDecision(nil), existing...)
	index := map[string]int{}
	for i, decision := range out {
		index[strings.TrimSpace(decision.Character)] = i
	}
	for _, decision := range incoming {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		decision.Character = name
		if i, ok := index[name]; ok {
			out[i] = decision
			continue
		}
		index[name] = len(out)
		out = append(out, decision)
	}
	return out
}

func canonicalizeCharacterWorldDecisions(s *store.Store, decisions []domain.CharacterWorldDecision) []domain.CharacterWorldDecision {
	canonical := canonicalCharacterIdentityMap(s)
	normalized := make([]domain.CharacterWorldDecision, 0, len(decisions))
	for _, decision := range decisions {
		name := strings.TrimSpace(decision.Character)
		if resolved := canonical[name]; resolved != "" {
			name = resolved
		}
		decision.Character = name
		normalized = mergeCharacterWorldDecisions(normalized, []domain.CharacterWorldDecision{decision})
	}
	return normalized
}

func characterDecisionNames(decisions []domain.CharacterWorldDecision) []string {
	names := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		if name := strings.TrimSpace(decision.Character); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func mergeRewriteFactCoverage(existing, incoming []domain.ChapterRewriteFactCoverage) []domain.ChapterRewriteFactCoverage {
	out := append([]domain.ChapterRewriteFactCoverage(nil), existing...)
	index := map[string]int{}
	for i, coverage := range out {
		index[strings.TrimSpace(coverage.Fact)] = i
	}
	for _, coverage := range incoming {
		fact := strings.TrimSpace(coverage.Fact)
		if fact == "" {
			continue
		}
		coverage.Fact = fact
		coverage.SimulationEvidence = compactStrings(coverage.SimulationEvidence)
		if i, ok := index[fact]; ok {
			out[i] = coverage
			continue
		}
		index[fact] = len(out)
		out = append(out, coverage)
	}
	return out
}

func chapterWorldSimulationGaps(s *store.Store, sim domain.ChapterWorldSimulation) []string {
	var gaps []string
	sim.CharacterDecisions = canonicalizeCharacterWorldDecisions(s, sim.CharacterDecisions)
	if strings.TrimSpace(sim.TimeWindow) == "" {
		gaps = append(gaps, "missing time_window")
	}
	present := map[string]domain.CharacterWorldDecision{}
	for _, decision := range sim.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		if _, duplicate := present[name]; duplicate {
			gaps = append(gaps, "duplicate character decision: "+name)
		}
		present[name] = decision
		prefix := "character_decisions(" + name + ")"
		if strings.TrimSpace(decision.Location) == "" || strings.TrimSpace(decision.CurrentGoal) == "" ||
			strings.TrimSpace(decision.Pressure) == "" || strings.TrimSpace(decision.KnowledgeBoundary) == "" ||
			len(decision.AvailableOptions) < 2 || strings.TrimSpace(decision.Decision) == "" ||
			strings.TrimSpace(decision.DecisionReason) == "" || strings.TrimSpace(decision.Action) == "" ||
			strings.TrimSpace(decision.ActionDuration) == "" || !slices.Contains([]string{"instant", "started", "in_progress", "completed", "blocked"}, decision.CompletionState) ||
			strings.TrimSpace(decision.ImmediateResult) == "" || strings.TrimSpace(decision.StateAfter) == "" {
			gaps = append(gaps, prefix+" missing decision chain")
		}
		if len(decision.ButterflyEffects) == 0 {
			gaps = append(gaps, prefix+" missing butterfly_effects")
		}
		for i, effect := range decision.ButterflyEffects {
			if strings.TrimSpace(effect.Effect) == "" || strings.TrimSpace(effect.TransmissionPath) == "" ||
				effect.ArrivalChapter < sim.Chapter || !slices.Contains([]string{"visible", "delayed", "hidden"}, effect.Visibility) ||
				strings.TrimSpace(effect.ProtagonistImpact) == "" {
				gaps = append(gaps, fmt.Sprintf("%s.butterfly_effects[%d] incomplete", prefix, i))
			}
		}
	}
	for _, name := range requiredDossierCharacterNames(s, sim.Chapter) {
		if _, ok := present[name]; !ok {
			gaps = append(gaps, "missing character decision: "+name)
		}
	}
	if expected, body, _, err := loadChapterRewriteSource(s, sim.Chapter); err != nil {
		gaps = append(gaps, "rewrite source unavailable: "+err.Error())
	} else if expected != nil {
		if !rewriteSourceEqual(sim.RewriteSource, expected) {
			gaps = append(gaps, "rewrite_source does not match current committed body and rewrite brief")
		}
		coverage := make(map[string]domain.ChapterRewriteFactCoverage, len(sim.RewriteFactCoverage))
		for _, item := range sim.RewriteFactCoverage {
			coverage[strings.TrimSpace(item.Fact)] = item
		}
		for _, fact := range expected.PreserveFacts {
			item, ok := coverage[strings.TrimSpace(fact)]
			if !ok || len(compactStrings(item.SimulationEvidence)) == 0 {
				gaps = append(gaps, "rewrite_fact_coverage missing: "+fact)
			}
		}
		for _, name := range rewriteVisibleCharacterNames(s, body, expected.PreserveFacts) {
			decision, ok := present[name]
			if !ok || !decision.VisibleToPOV {
				gaps = append(gaps, "rewrite-visible character must remain visible_to_pov: "+name)
			}
		}
	}
	p := sim.ProtagonistProjection
	protagonist := inferCommitProtagonist(s)
	if strings.TrimSpace(p.Protagonist) != protagonist || len(p.ObservableEffects) == 0 || len(p.HiddenPressures) == 0 ||
		len(p.AvailableOptions) < 2 || strings.TrimSpace(p.ChosenDecision) == "" || strings.TrimSpace(p.DecisionReason) == "" ||
		len(p.PlanConstraints) == 0 || len(p.CausalChain) == 0 {
		gaps = append(gaps, "incomplete protagonist_projection")
	}
	if decision, ok := present[protagonist]; ok && strings.TrimSpace(p.ChosenDecision) != "" && strings.TrimSpace(decision.Decision) != strings.TrimSpace(p.ChosenDecision) {
		gaps = append(gaps, "protagonist_projection.chosen_decision must equal protagonist character decision")
	}
	return compactStrings(gaps)
}

func chapterWorldSimulationID(sim domain.ChapterWorldSimulation) string {
	clone := sim
	clone.SimulationID = ""
	clone.GeneratedAt = ""
	raw, _ := json.Marshal(clone)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("ch%03d-%s", sim.Chapter, hex.EncodeToString(sum[:6]))
}

func chapterWorldSimulationRequired(s *store.Store) bool {
	cast, err := s.WorldSim.LoadSimulationCast()
	return err == nil && len(cast.Assignments) > 0
}

func ensureChapterWorldSimulationReadyForPlanning(s *store.Store, chapter int) (*domain.ChapterWorldSimulation, error) {
	if !chapterWorldSimulationRequired(s) {
		return nil, nil
	}
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return nil, err
	}
	if sim == nil {
		return nil, fmt.Errorf("第 %d 章必须先完成单世界全角色推演：调用 simulate_chapter_world 分批覆盖所有实名角色并 finalize，之后才能规划 POV 章节: %w", chapter, errs.ErrToolPrecondition)
	}
	if gaps := chapterWorldSimulationGaps(s, *sim); len(gaps) > 0 {
		return nil, fmt.Errorf("第 %d 章全角色世界推演不完整：%s: %w", chapter, strings.Join(gaps, "；"), errs.ErrToolPrecondition)
	}
	return sim, nil
}

func validateChapterWorldSimulationReference(s *store.Store, plan domain.ChapterPlan) error {
	if !chapterWorldSimulationRequired(s) {
		return nil
	}
	sim, err := ensureChapterWorldSimulationReadyForPlanning(s, plan.Chapter)
	if err != nil {
		return err
	}
	causal := plan.CausalSimulation
	if strings.TrimSpace(causal.WorldSimulationID) != strings.TrimSpace(sim.SimulationID) {
		return fmt.Errorf("第 %d 章 POV plan 必须引用本轮 world_simulation_id=%s，当前为 %q: %w", plan.Chapter, sim.SimulationID, causal.WorldSimulationID, errs.ErrToolPrecondition)
	}
	if strings.TrimSpace(causal.ProtagonistDecision) != strings.TrimSpace(sim.ProtagonistProjection.ChosenDecision) {
		return fmt.Errorf("第 %d 章 protagonist_decision 必须等于世界模拟投影的主角选择 %q，当前为 %q: %w", plan.Chapter, sim.ProtagonistProjection.ChosenDecision, causal.ProtagonistDecision, errs.ErrToolPrecondition)
	}
	if !contextSourcesContain(causal.ContextSources, sim.SimulationID) || !contextSourcesContain(causal.ContextSources, "chapter_world_simulation") {
		return fmt.Errorf("第 %d 章 context_sources 必须记录 chapter_world_simulation:%s: %w", plan.Chapter, sim.SimulationID, errs.ErrToolPrecondition)
	}
	return nil
}

// MigrateLegacyPlanStageToChapterSimulation moves the old all-character stage
// out of a POV plan partial. It preserves authored choices as an incomplete
// simulation seed; the Writer must still supply reasons, options, and effects.
func MigrateLegacyPlanStageToChapterSimulation(s *store.Store, chapter int, partial map[string]any) (bool, error) {
	if partial == nil {
		return false, nil
	}
	if final, err := s.LoadChapterWorldSimulation(chapter); err != nil || final != nil {
		return false, err
	}
	merged, _ := partial["causal_simulation"].(map[string]any)
	if merged == nil {
		return false, nil
	}
	legacyRaw, err := json.Marshal(merged)
	if err != nil {
		return false, err
	}
	legacyArchive := map[string]any{}
	if err := json.Unmarshal(legacyRaw, &legacyArchive); err != nil {
		return false, err
	}
	rawStage, ok := merged["offscreen_character_stage"]
	if !ok {
		return false, nil
	}
	raw, err := json.Marshal(rawStage)
	if err != nil {
		return false, err
	}
	var stages []domain.CharacterStageRecord
	if err := json.Unmarshal(raw, &stages); err != nil {
		return false, err
	}
	seed, err := s.LoadChapterWorldSimulationPartial(chapter)
	if err != nil {
		return false, err
	}
	if seed == nil {
		seed = &domain.ChapterWorldSimulation{Version: 1, Chapter: chapter, Sources: []string{"migrated from drafts plan offscreen_character_stage"}}
	}
	var incoming []domain.CharacterWorldDecision
	for _, stage := range stages {
		incoming = append(incoming, domain.CharacterWorldDecision{
			Character:         stage.Character,
			Time:              stage.Time,
			Location:          stage.Location,
			Pressure:          stage.Pressure,
			KnowledgeBoundary: stage.KnowledgeBoundary,
			Decision:          stage.Decision,
			Action:            stage.CurrentAction,
			ImmediateResult:   stage.Evidence,
			StateAfter:        stage.Status,
			VisibleToPOV:      stage.VisibleInChapter,
		})
	}
	seed.CharacterDecisions = mergeCharacterWorldDecisions(seed.CharacterDecisions, canonicalizeCharacterWorldDecisions(s, incoming))
	if err := s.SaveChapterWorldSimulationPartial(*seed); err != nil {
		return false, err
	}
	partial["legacy_causal_simulation_archive"] = legacyArchive
	// All plan-side causal fields must be regenerated from the finalized world
	// simulation. Otherwise a plan can cite the new simulation ID while silently
	// rendering beats authored against the legacy all-character stage.
	partial["causal_simulation"] = map[string]any{}
	partial["world_simulation_migration"] = map[string]any{
		"chapter": chapter,
		"records": len(stages),
		"status":  "legacy causal archived; character stages moved to chapter simulation partial; active POV causal must be rebuilt from finalized simulation",
	}
	if err := s.Drafts.SaveChapterPlanPartial(chapter, partial); err != nil {
		return false, err
	}
	return true, nil
}
