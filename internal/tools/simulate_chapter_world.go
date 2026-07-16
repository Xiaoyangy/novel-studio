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
	return "在章节 plan 之前推进单一世界中的全部实名角色。通常每个角色都按自己的目标、压力、资源和知识边界选择行动，写明决定理由，并携带至少一个会改变世界或主角选项的蝴蝶效应。例外：simulation_character_authority 中 blocking=true 的角色不要手抄长 JSON，直接把实名放入 authority_contract_characters；工具会在服务端逐字段物化并校验对应 hold_baseline_contract 或 rewrite_source_only_contract，避免引号、长句和知识锁在传输中被改写。novel_context 若返回 chapter_pipeline_instruction，必须逐条服从其硬约束并把 source_token 原样写入 sources。character_decisions 与 authority_contract_characters 合计每批最多8名，最后 finalize=true；若 chapter_world_simulation.status=ready_to_finalize，则只传 chapter 和 finalize=true，禁止重复提交任何已校验字段。完成后 POV plan 只能引用返回的 simulation_id 和 protagonist_projection，正文不得直接泄露 hidden/delayed 信息。空补丁会被拒绝。"
}

func (t *SimulateChapterWorldTool) Schema() map[string]any {
	effect := schema.Object(
		schema.Property("effect", schema.String("该决定引发的状态变化")).Required(),
		schema.Property("targets", schema.Array("受影响角色、关系、资源或势力", schema.String(""))),
		schema.Property("transmission_path", schema.String("影响如何传播；不传播到主角也要说明阻断路径")).Required(),
		schema.Property("arrival_chapter", schema.Int("最早影响主角或世界局面的章号，不得早于当前章")).Required(),
		schema.Property("visibility", schema.Enum("主视角可见性", "visible", "delayed", "hidden")).Required(),
		schema.Property("protagonist_impact", schema.String("如何改变主角可见事实、压力、资源或可选项；无即时接触时写明延迟影响；hold_baseline 合同例外，必须原样写 none")).Required(),
	)
	decision := schema.Object(
		schema.Property("character", schema.String("characters.json 中的角色实名")).Required(),
		schema.Property("time", schema.String("本章共同时间线中的时点")),
		schema.Property("location", schema.String("角色所在位置")).Required(),
		schema.Property("current_goal", schema.String("角色自己的当前目标")).Required(),
		schema.Property("pressure", schema.String("推动或限制其选择的压力")).Required(),
		schema.Property("resources", schema.Array("可使用或受限的资源", schema.String(""))),
		schema.Property("knowledge_boundary", schema.String("角色知道、不知道和不能提前知道什么")).Required(),
		schema.Property("available_options", schema.Array("按当前信息真正可选的行动，至少两个；等待或拒绝也可是选项；hold_baseline 合同必须原样复制固定两项", schema.String(""))).Required(),
		schema.Property("decision", schema.String("角色最终选择")).Required(),
		schema.Property("decision_reason", schema.String("为何此人此刻会这样选，必须锚定目标、压力、资源、关系或误判；blocking 角色例外，必须照抄对应 authority contract")).Required(),
		schema.Property("action", schema.String("选择转成的实际行动")).Required(),
		schema.Property("action_duration", schema.String("现实耗时，例如十分钟、两天、三周；复杂项目不得用一章默认完成；hold_baseline 合同例外，必须原样写 not_applicable")).Required(),
		schema.Property("completion_state", schema.Enum("本章结束时完成度", "instant", "started", "in_progress", "completed", "blocked")).Required(),
		schema.Property("immediate_result", schema.String("行动的即时世界反馈")).Required(),
		schema.Property("state_after", schema.String("行动后的状态或下一倾向")).Required(),
		schema.Property("visible_to_pov", schema.Bool("本章主视角是否直接看见")),
		schema.Property("butterfly_effects", schema.Array("该决定携带的下游影响，至少一个；hold_baseline 合同的唯一 effect 是 transmission_blocked no-op 哨兵", effect)).Required(),
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
		schema.Property("authority_contract_characters", schema.Array("本批 blocking 角色实名；工具在服务端从 simulation_character_authority 物化 exact contract，不要再把这些角色放进 character_decisions；与 character_decisions 合计最多8名", schema.String("角色实名"))),
		schema.Property("character_decisions", schema.Array("本批非 blocking 角色决定；每批与 authority_contract_characters 合计最多8名，剩余角色下次补；already_present 禁止重发", decision)),
		schema.Property("protagonist_projection", projection),
		schema.Property("rewrite_fact_coverage", schema.Array("仅返工章需要：逐条证明保留事实已进入本轮世界模拟", rewriteCoverage)),
		schema.Property("sources", schema.Array("本次推演依据的 tick、角色档案、台账、大纲和规则；存在 chapter_pipeline_instruction 时必须原样包含其 source_token", schema.String(""))),
		schema.Property("finalize", schema.Bool("全角色覆盖后传 true，生成正式 simulation_id")),
	)
}

func (t *SimulateChapterWorldTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter               int                                  `json:"chapter"`
		TimeWindow            string                               `json:"time_window"`
		AuthorityCharacters   []string                             `json:"authority_contract_characters"`
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
	if len(a.CharacterDecisions)+len(a.AuthorityCharacters) > chapterWorldSimulationBatchLimit {
		return nil, fmt.Errorf("simulate_chapter_world 单批最多提交%d名角色，当前 character_decisions=%d authority_contract_characters=%d；请按 gaps 分批: %w", chapterWorldSimulationBatchLimit, len(a.CharacterDecisions), len(a.AuthorityCharacters), errs.ErrToolArgs)
	}
	if skipped, _, err := ensureChapterPlannable(t.store, a.Chapter); err != nil || skipped != nil {
		return skipped, err
	}
	// A valid finalized simulation is immutable for its exact rewrite source and
	// current cast. Planner may defensively call this tool again; return the
	// existing projection instead of opening a second partial that can overwrite
	// already completed all-character work.
	forceStructuralResimulation := InspectRenderOnlyReplanEscalation(t.store, a.Chapter).Required
	if finalized, loadErr := t.store.LoadChapterWorldSimulation(a.Chapter); loadErr != nil {
		return nil, fmt.Errorf("load finalized chapter simulation: %w", loadErr)
	} else if !forceStructuralResimulation && finalized != nil && len(chapterWorldSimulationGaps(t.store, *finalized)) == 0 {
		if err := ensureChapterWorldSimulationCheckpoint(t.store, a.Chapter); err != nil {
			return nil, fmt.Errorf("checkpoint reused chapter world simulation: %w", err)
		}
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
	restartedShell := false
	if forceStructuralResimulation && partial != nil && !chapterWorldSimulationHasCausalWork(*partial) &&
		(len(a.CharacterDecisions) > 0 || len(a.AuthorityCharacters) > 0 || len(a.RewriteFactCoverage) > 0 || protagonistProjectionHasCausalWork(a.ProtagonistProjection)) {
		// A context failure can leave a time-window/source-only shell before any
		// causal evidence was authored. It is not resumable work: carrying its
		// ungrounded provenance into the replacement epoch is worse than starting
		// clean. This reset is deliberately limited to structural escalation and
		// never discards a character decision, fact coverage or POV projection.
		partial = &domain.ChapterWorldSimulation{Version: 1, Chapter: a.Chapter}
		restartedShell = true
	}
	if partial == nil {
		partial = &domain.ChapterWorldSimulation{Version: 1, Chapter: a.Chapter}
	}
	materialized, materializeErr := materializeSimulationAuthorityContracts(t.store, a.Chapter, a.AuthorityCharacters)
	if materializeErr != nil {
		return nil, fmt.Errorf("simulate_chapter_world authority_contract_characters: %w: %w", materializeErr, errs.ErrToolPrecondition)
	}
	if len(materialized) > 0 {
		directNames := make(map[string]bool, len(a.CharacterDecisions))
		for _, decision := range canonicalizeCharacterWorldDecisions(t.store, a.CharacterDecisions) {
			directNames[strings.TrimSpace(decision.Character)] = true
		}
		for _, decision := range materialized {
			if directNames[strings.TrimSpace(decision.Character)] {
				return nil, fmt.Errorf("角色 %s 同时出现在 character_decisions 与 authority_contract_characters，禁止双重提交: %w", decision.Character, errs.ErrToolArgs)
			}
		}
		a.CharacterDecisions = append(a.CharacterDecisions, materialized...)
	}
	partial.CharacterDecisions = canonicalizeCharacterWorldDecisions(t.store, partial.CharacterDecisions)
	a.CharacterDecisions = canonicalizeCharacterWorldDecisions(t.store, a.CharacterDecisions)
	if err := validateIncomingSimulationCharacterAuthority(t.store, a.Chapter, a.CharacterDecisions); err != nil {
		return nil, fmt.Errorf("simulate_chapter_world authority guard: %w: %w", err, errs.ErrToolPrecondition)
	}
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
		var coverageErr error
		a.RewriteFactCoverage, coverageErr = canonicalizeIncomingRewriteFactCoverage(rewriteSource.PreserveFacts, a.RewriteFactCoverage)
		if coverageErr != nil {
			return nil, fmt.Errorf("simulate_chapter_world rewrite_fact_coverage guard: %w: %w", coverageErr, errs.ErrToolPrecondition)
		}
	}
	if err := validateIncomingSimulationSemanticInvariants(t.store, a.Chapter, a.CharacterDecisions, a.ProtagonistProjection, rewriteSource); err != nil {
		return nil, fmt.Errorf("simulate_chapter_world semantic guard: %w: %w", err, errs.ErrToolPrecondition)
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
		nextStep := "继续分批调用 simulate_chapter_world，每批只补 gaps 中最多8名角色；补齐后单独提交 protagonist_projection 并传 finalize=true。禁止空提交，不要开始 plan_structure。"
		if len(gaps) == 0 {
			nextStep = "所有字段已通过校验；下一次只传 chapter 和 finalize=true 原子转正式，不得重发任何角色、投影、覆盖、来源或时间窗口。"
		}
		result := map[string]any{
			"staged":             "chapter_world_simulation",
			"chapter":            a.Chapter,
			"characters_present": characterDecisionNames(partial.CharacterDecisions),
			"gaps":               gaps,
			"next_step":          nextStep,
		}
		if restartedShell {
			result["partial_restarted"] = true
			result["restart_reason"] = "structural escalation 下的无因果证据空壳已丢弃"
		}
		return json.Marshal(result)
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
	if err := ensureChapterWorldSimulationCheckpoint(t.store, a.Chapter); err != nil {
		return nil, fmt.Errorf("checkpoint chapter world simulation: %w", err)
	}
	result := map[string]any{
		"simulated":              true,
		"chapter":                a.Chapter,
		"simulation_id":          partial.SimulationID,
		"protagonist_projection": partial.ProtagonistProjection,
		"next_step":              "基于 protagonist_projection 调用 plan_structure，再用 plan_details 写入 world_simulation_id 和 protagonist_decision；正文只渲染主角可见信息。",
	}
	if restartedShell {
		result["partial_restarted"] = true
	}
	return json.Marshal(result)
}

func materializeSimulationAuthorityContracts(st *store.Store, chapter int, names []string) ([]domain.CharacterWorldDecision, error) {
	if st == nil || chapter <= 0 || len(names) == 0 {
		return nil, nil
	}
	canonical := canonicalCharacterIdentityMap(st)
	authority := buildSimulationCharacterAuthority(st, chapter)
	byName := make(map[string]simulationCharacterAuthority, len(authority))
	for _, entry := range authority {
		byName[entry.Character] = entry
	}
	seen := make(map[string]bool, len(names))
	decisions := make([]domain.CharacterWorldDecision, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if resolved := canonical[name]; resolved != "" {
			name = resolved
		}
		if name == "" {
			return nil, fmt.Errorf("存在空角色名")
		}
		if seen[name] {
			return nil, fmt.Errorf("同批重复角色：%s", name)
		}
		seen[name] = true
		entry, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("角色 %s 不在 simulation_character_authority 名册", name)
		}
		if entry.SimulationStatus == "already_present" {
			return nil, fmt.Errorf("角色 %s 已为 already_present，禁止重发", name)
		}
		var decision domain.CharacterWorldDecision
		switch entry.AuthorityMode {
		case "rewrite_source_only":
			decision = rewriteSourceOnlySentinelDecision(name, chapter, entry.RewriteSourceEvidence)
		case "hold_baseline":
			decision = holdBaselineSentinelDecision(name, chapter)
		default:
			return nil, fmt.Errorf("角色 %s 的 authority_mode=%s 不是可物化 blocking contract；请在 character_decisions 中基于权威事实推演", name, entry.AuthorityMode)
		}
		decision.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(decision.KnowledgeBoundary, entry.RequiredKnowledgeBoundary)
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func validateIncomingSimulationSemanticInvariants(st *store.Store, chapter int, decisions []domain.CharacterWorldDecision, projection domain.ProtagonistDecisionProjection, rewriteSource *domain.ChapterRewriteSource) error {
	if len(decisions) == 0 && !protagonistProjectionHasCausalWork(projection) {
		return nil
	}
	present := make(map[string]domain.CharacterWorldDecision, len(decisions))
	for _, decision := range decisions {
		present[strings.TrimSpace(decision.Character)] = decision
	}
	var violations []string
	if rewriteSource != nil {
		violations = append(violations, chapterWorldSimulationPreserveInvariantGaps(st, chapter, present, rewriteSource.PreserveFacts)...)
		violations = append(violations, chapterWorldSimulationProjectionInvariantGaps(projection, rewriteSource.PreserveFacts)...)
	}
	for _, decision := range decisions {
		violations = append(violations, simulationDecisionKnowledgeGaps(decision)...)
	}
	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("角色候选项/知识边界与硬事实冲突：%s", strings.Join(compactStrings(violations), "；"))
}

func chapterWorldSimulationHasCausalWork(sim domain.ChapterWorldSimulation) bool {
	return len(sim.CharacterDecisions) > 0 || len(sim.RewriteFactCoverage) > 0 ||
		protagonistProjectionHasCausalWork(sim.ProtagonistProjection)
}

func protagonistProjectionHasCausalWork(projection domain.ProtagonistDecisionProjection) bool {
	// normalizeProtagonistProjection fills the protagonist identity on every
	// staged save. That name alone is bookkeeping, not authored causal work.
	return len(projection.ObservableEffects) > 0 || len(projection.HiddenPressures) > 0 || len(projection.AvailableOptions) > 0 ||
		strings.TrimSpace(projection.ChosenDecision) != "" || strings.TrimSpace(projection.DecisionReason) != "" ||
		len(projection.PlanConstraints) > 0 || len(projection.CausalChain) > 0
}

func ensureChapterWorldSimulationCheckpoint(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("chapter world simulation checkpoint requires a store and chapter")
	}
	_, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter),
		"chapter_world_simulation",
		fmt.Sprintf("meta/chapter_simulations/%03d.json", chapter),
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	return err
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
		index[rewriteFactIdentity(coverage.Fact)] = i
	}
	for _, coverage := range incoming {
		fact := strings.TrimSpace(coverage.Fact)
		if fact == "" {
			continue
		}
		coverage.Fact = fact
		coverage.SimulationEvidence = compactStrings(coverage.SimulationEvidence)
		identity := rewriteFactIdentity(fact)
		if i, ok := index[identity]; ok {
			// Keep the first spelling as the durable canonical form. Models often
			// round-trip Chinese curly quotes as corner or ASCII quotes; that is a
			// typography change, not a different protected fact.
			coverage.Fact = out[i].Fact
			out[i] = coverage
			continue
		}
		index[identity] = len(out)
		out = append(out, coverage)
	}
	return out
}

// canonicalizeIncomingRewriteFactCoverage makes a coverage submission atomic:
// every fact must identify one exact preserve_facts item before any part of the
// call is staged. Accepted quote-glyph variants are written back with the
// canonical brief spelling, so a failed paraphrase cannot linger beside a
// later exact retry and bloat planning context.
func canonicalizeIncomingRewriteFactCoverage(expected []string, incoming []domain.ChapterRewriteFactCoverage) ([]domain.ChapterRewriteFactCoverage, error) {
	if len(incoming) == 0 {
		return nil, nil
	}
	canonical := make(map[string]string, len(expected))
	for _, fact := range expected {
		if identity := rewriteFactIdentity(fact); identity != "" {
			canonical[identity] = strings.TrimSpace(fact)
		}
	}
	var invalid []string
	var normalized []domain.ChapterRewriteFactCoverage
	for i, item := range incoming {
		identity := rewriteFactIdentity(item.Fact)
		fact, ok := canonical[identity]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("[%d]=%q", i, firstRunes(strings.TrimSpace(item.Fact), 48)))
			continue
		}
		item.Fact = fact
		item.SimulationEvidence = compactStrings(item.SimulationEvidence)
		normalized = mergeRewriteFactCoverage(normalized, []domain.ChapterRewriteFactCoverage{item})
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("fact 必须逐字来自 rewrite_source.preserve_facts，禁止删引号、换标点或概括；非法项：%s", strings.Join(invalid, "、"))
	}
	return normalized, nil
}

func rewriteFactCoverageIntegrityGaps(expected []string, actual []domain.ChapterRewriteFactCoverage) []string {
	expectedSet := make(map[string]bool, len(expected))
	for _, fact := range expected {
		expectedSet[rewriteFactIdentity(fact)] = true
	}
	seen := make(map[string]bool, len(actual))
	var gaps []string
	for i, item := range actual {
		identity := rewriteFactIdentity(item.Fact)
		if !expectedSet[identity] {
			gaps = append(gaps, fmt.Sprintf("rewrite_fact_coverage unexpected[%d]: %s", i, firstRunes(strings.TrimSpace(item.Fact), 64)))
			continue
		}
		if seen[identity] {
			gaps = append(gaps, fmt.Sprintf("rewrite_fact_coverage duplicate[%d]: %s", i, firstRunes(strings.TrimSpace(item.Fact), 64)))
			continue
		}
		seen[identity] = true
	}
	return gaps
}

func chapterWorldSimulationGaps(s *store.Store, sim domain.ChapterWorldSimulation) []string {
	var gaps []string
	sim.CharacterDecisions = canonicalizeCharacterWorldDecisions(s, sim.CharacterDecisions)
	if gap := storedSimulationCharacterAuthorityGap(s, sim); gap != "" {
		gaps = append(gaps, gap)
	}
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
		gaps = append(gaps, simulationDecisionKnowledgeGaps(decision)...)
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
		gaps = append(gaps, rewriteFactCoverageIntegrityGaps(expected.PreserveFacts, sim.RewriteFactCoverage)...)
		for _, item := range sim.RewriteFactCoverage {
			coverage[rewriteFactIdentity(item.Fact)] = item
		}
		for _, fact := range expected.PreserveFacts {
			item, ok := coverage[rewriteFactIdentity(fact)]
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
		gaps = append(gaps, chapterWorldSimulationPreserveInvariantGaps(s, sim.Chapter, present, expected.PreserveFacts)...)
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
	if expected, _, _, err := loadChapterRewriteSource(s, sim.Chapter); err == nil && expected != nil {
		gaps = append(gaps, chapterWorldSimulationProjectionInvariantGaps(p, expected.PreserveFacts)...)
	}
	if gap := chapterPipelineInstructionGap(s, sim); gap != "" {
		gaps = append(gaps, gap)
	}
	gaps = append(gaps, chapterWorldSimulationQuantityGaps(s, sim)...)
	return compactStrings(gaps)
}

func storedSimulationCharacterAuthorityGap(s *store.Store, sim domain.ChapterWorldSimulation) string {
	// Incoming batches are validated before staging. A finalized artifact can
	// outlive a stronger contract implementation, though; without revalidation,
	// an old rewrite_source_only sentence (for example one missing its closing
	// quote) would remain "ready" forever. Partials deliberately skip this path
	// because their already-present actors are immutable within the current
	// epoch and were checked on ingress.
	if s == nil || strings.TrimSpace(sim.SimulationID) == "" || len(sim.CharacterDecisions) == 0 {
		return ""
	}
	if err := validateIncomingSimulationCharacterAuthority(s, sim.Chapter, sim.CharacterDecisions); err != nil {
		return "stored simulation authority contract invalid: " + err.Error()
	}
	return ""
}

type simulationDecisionTextField struct {
	Path string
	Text string
}

func chapterWorldSimulationProjectionInvariantGaps(projection domain.ProtagonistDecisionProjection, facts []string) []string {
	if !protagonistProjectionHasCausalWork(projection) {
		return nil
	}
	fields := simulationProjectionTextFields(projection)
	var gaps []string
	for _, fact := range facts {
		if strings.Contains(fact, "任何字段") && strings.Contains(fact, "不通过") {
			for _, quoted := range chineseQuotedSegments(fact) {
				if !containsSimulationAny(quoted, "要求", "命令", "纠偏") || !containsSimulationCorrectionOutcome(quoted) {
					continue
				}
				actor := constraintActorName(quoted)
				for _, field := range fields {
					if exactPreserveFactText(field.Text, facts) {
						continue
					}
					if affirmedNamedActorControlOutcome(field.Text, actor) {
						gaps = append(gaps, "preserve_fact invariant violated: protagonist_projection."+field.Path+" contains forbidden actor-causes-correction path")
					}
				}
			}
		}
		for _, forbidden := range preserveFactForbiddenProjectionPhrases(fact) {
			for _, field := range fields {
				if containsAffirmedForbiddenProjectionPhrase(field.Text, forbidden) {
					gaps = append(gaps, fmt.Sprintf("preserve_fact invariant violated: protagonist_projection.%s contains forbidden projection claim %q", field.Path, forbidden))
				}
			}
		}
	}
	return compactStrings(gaps)
}

func constraintActorName(quoted string) string {
	end := len(quoted)
	for _, control := range []string{"要求", "命令", "纠偏", "让", "叫", "示意", "催促"} {
		if at := strings.Index(quoted, control); at >= 0 && at < end {
			end = at
		}
	}
	return strings.Trim(strings.TrimSpace(quoted[:end]), "，,、/：:")
}

func affirmedNamedActorControlOutcome(text, actor string) bool {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return false
	}
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', ';', '，', ',', '\n':
			return true
		default:
			return false
		}
	}) {
		if containsSimulationAny(clause, "不通过", "不得写成", "不能写成", "禁止写成") {
			continue
		}
		searchFrom := 0
		for searchFrom < len(clause) {
			rel := strings.Index(clause[searchFrom:], actor)
			if rel < 0 {
				break
			}
			at := searchFrom + rel
			if affirmedActorControlOutcome(clause[at:]) {
				return true
			}
			searchFrom = at + len(actor)
		}
	}
	return false
}

func exactPreserveFactText(text string, facts []string) bool {
	identity := rewriteFactIdentity(text)
	if identity == "" {
		return false
	}
	for _, fact := range facts {
		if identity == rewriteFactIdentity(fact) {
			return true
		}
	}
	return false
}

func simulationProjectionTextFields(projection domain.ProtagonistDecisionProjection) []simulationDecisionTextField {
	fields := []simulationDecisionTextField{
		{Path: "protagonist", Text: projection.Protagonist},
		{Path: "chosen_decision", Text: projection.ChosenDecision},
		{Path: "decision_reason", Text: projection.DecisionReason},
	}
	appendValues := func(path string, values []string) {
		for i, value := range values {
			fields = append(fields, simulationDecisionTextField{Path: fmt.Sprintf("%s[%d]", path, i), Text: value})
		}
	}
	appendValues("observable_effects", projection.ObservableEffects)
	appendValues("hidden_pressures", projection.HiddenPressures)
	appendValues("available_options", projection.AvailableOptions)
	appendValues("plan_constraints", projection.PlanConstraints)
	appendValues("causal_chain", projection.CausalChain)
	return fields
}

func preserveFactForbiddenProjectionPhrases(fact string) []string {
	var phrases []string
	if at := strings.Index(fact, "均不得写"); at >= 0 {
		phrases = append(phrases, splitForbiddenProjectionPhrases(fact[at+len("均不得写"):])...)
	}
	if strings.Contains(fact, "不得埋") || strings.Contains(fact, "不得留下") {
		for _, quoted := range chineseQuotedSegments(fact) {
			if strings.TrimSpace(quoted) != "" {
				phrases = append(phrases, quoted)
			}
		}
	}
	if strings.Contains(fact, "不得据此生成") && strings.Contains(fact, "主动回拨") {
		// "回电" is the ordinary prose synonym models prefer.  Treating only
		// the literal "主动回拨" as forbidden lets an available option silently
		// schedule the same off-screen action under a different word.
		phrases = append(phrases, "回拨")
	}
	return compactStrings(phrases)
}

func splitForbiddenProjectionPhrases(text string) []string {
	if at := strings.IndexAny(text, "。；;\n"); at >= 0 {
		text = text[:at]
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '、' || r == '或'
	})
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.TrimPrefix(parts[i], "任何"))
	}
	return compactStrings(parts)
}

func containsAffirmedForbiddenProjectionPhrase(text, forbidden string) bool {
	canonicalText := canonicalProjectionConstraintText(text)
	canonicalForbidden := canonicalProjectionConstraintText(forbidden)
	if canonicalForbidden == "" {
		return false
	}
	searchFrom := 0
	for searchFrom < len(canonicalText) {
		rel := strings.Index(canonicalText[searchFrom:], canonicalForbidden)
		if rel < 0 {
			break
		}
		at := searchFrom + rel
		if !containsSimulationAny(projectionClaimLocalPrefix(canonicalText[:at]), "不得", "不能", "未", "尚未", "没有", "并未", "禁止", "避免", "不可", "不允许") {
			return true
		}
		searchFrom = at + len(canonicalForbidden)
	}
	return false
}

func projectionClaimLocalPrefix(prefix string) string {
	start := 0
	for _, separator := range []string{"，", ",", "；", ";", "。", "但", "却", "然而", "不过", "实际", "事实上"} {
		if at := strings.LastIndex(prefix, separator); at >= 0 && at+len(separator) > start {
			start = at + len(separator)
		}
	}
	return prefix[start:]
}

func canonicalProjectionConstraintText(text string) string {
	return strings.NewReplacer(
		"主动回拨", "回拨", "回电话", "回拨", "回电", "回拨",
		"已经", "已", "將", "将", " ", "", "\t", "", "“", "", "”", "", "「", "", "」", "", "'", "", "\"", "",
	).Replace(strings.TrimSpace(text))
}

// chapterWorldSimulationPreserveInvariantGaps enforces the narrow but
// important class of rewrite facts that explicitly says a named actor must not
// cause an outcome in *any* simulation field.  Available options are authored
// counterfactuals, yet they still reach the Planner; leaving a forbidden causal
// option there can anchor the next stage even when the chosen decision is
// correct.  The fact itself remains the source of truth: no project name or
// chapter-specific action is compiled into this guard.
func chapterWorldSimulationPreserveInvariantGaps(s *store.Store, chapter int, present map[string]domain.CharacterWorldDecision, facts []string) []string {
	var gaps []string
	var knownNames []string
	if s != nil {
		knownNames = requiredDossierCharacterNames(s, chapter)
	}
	// The caller's present map is authoritative for imported aliases and for
	// chapters whose required roster is intentionally sparse.
	for name := range present {
		knownNames = appendUniqueString(knownNames, name)
	}
	for _, fact := range facts {
		if strings.Contains(fact, "任何字段") && strings.Contains(fact, "不通过") {
			for _, quoted := range chineseQuotedSegments(fact) {
				actor := namedActorInConstraint(quoted, knownNames)
				decision, ok := present[actor]
				if !ok || !containsSimulationAny(quoted, "要求", "命令", "纠偏") || !containsSimulationAny(quoted, "断电", "退线", "返工") {
					continue
				}
				for _, field := range simulationDecisionTextFields(decision) {
					if exactPreserveFactText(field.Text, facts) {
						continue
					}
					if affirmedActorControlOutcome(field.Text) {
						gaps = append(gaps, fmt.Sprintf("preserve_fact invariant violated: character_decisions(%s).%s contains forbidden actor-causes-correction option", actor, field.Path))
					}
				}
			}
		}
		for _, forbidden := range preserveFactForbiddenProjectionPhrases(fact) {
			if forbidden != "回拨" {
				continue
			}
			for name, decision := range present {
				for _, field := range simulationDecisionTextFields(decision) {
					if containsAffirmedForbiddenProjectionPhrase(field.Text, forbidden) {
						gaps = append(gaps, fmt.Sprintf("preserve_fact invariant violated: character_decisions(%s).%s contains forbidden callback claim %q", name, field.Path, forbidden))
					}
				}
			}
		}
	}
	return compactStrings(gaps)
}

func chineseQuotedSegments(text string) []string {
	pairs := map[rune]rune{'“': '”', '「': '」', '『': '』'}
	var result []string
	var close rune
	var current strings.Builder
	for _, r := range text {
		if close == 0 {
			if expected, ok := pairs[r]; ok {
				close = expected
				current.Reset()
			}
			continue
		}
		if r == close {
			if value := strings.TrimSpace(current.String()); value != "" {
				result = append(result, value)
			}
			close = 0
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	return result
}

func namedActorInConstraint(text string, names []string) string {
	best := ""
	bestAt := len(text) + 1
	for _, name := range compactStrings(names) {
		if at := strings.Index(text, name); at >= 0 && (at < bestAt || at == bestAt && len(name) > len(best)) {
			best, bestAt = name, at
		}
	}
	return best
}

func containsSimulationAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func simulationDecisionTextFields(decision domain.CharacterWorldDecision) []simulationDecisionTextField {
	fields := []simulationDecisionTextField{
		{Path: "time", Text: decision.Time}, {Path: "location", Text: decision.Location},
		{Path: "current_goal", Text: decision.CurrentGoal}, {Path: "pressure", Text: decision.Pressure},
		{Path: "knowledge_boundary", Text: decision.KnowledgeBoundary}, {Path: "decision", Text: decision.Decision},
		{Path: "decision_reason", Text: decision.DecisionReason}, {Path: "action", Text: decision.Action},
		{Path: "action_duration", Text: decision.ActionDuration}, {Path: "immediate_result", Text: decision.ImmediateResult},
		{Path: "state_after", Text: decision.StateAfter},
	}
	for i, value := range decision.Resources {
		fields = append(fields, simulationDecisionTextField{Path: fmt.Sprintf("resources[%d]", i), Text: value})
	}
	for i, value := range decision.AvailableOptions {
		fields = append(fields, simulationDecisionTextField{Path: fmt.Sprintf("available_options[%d]", i), Text: value})
	}
	for i, effect := range decision.ButterflyEffects {
		prefix := fmt.Sprintf("butterfly_effects[%d]", i)
		fields = append(fields,
			simulationDecisionTextField{Path: prefix + ".effect", Text: effect.Effect},
			simulationDecisionTextField{Path: prefix + ".transmission_path", Text: effect.TransmissionPath},
			simulationDecisionTextField{Path: prefix + ".protagonist_impact", Text: effect.ProtagonistImpact},
		)
		for j, target := range effect.Targets {
			fields = append(fields, simulationDecisionTextField{Path: fmt.Sprintf("%s.targets[%d]", prefix, j), Text: target})
		}
	}
	return fields
}

func affirmedActorControlOutcome(text string) bool {
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', ';', '，', ',', '\n':
			return true
		default:
			return false
		}
	}) {
		if !containsSimulationCorrectionOutcome(clause) {
			continue
		}
		if containsSimulationAny(clause, "不能写", "不得写", "禁止写", "不可写") {
			continue
		}
		// A direct modal order ("线必须退回") is the same forbidden causal
		// move even when the model omits the reporting verb "要求".  Completed
		// state and future boundary wording remain valid.
		if affirmedModalCorrectionOutcome(clause) {
			return true
		}
		if affirmedDelayedCorrectionOutcome(clause) {
			return true
		}
		for _, control := range []string{"要求", "命令", "纠偏", "让", "叫", "示意", "催促"} {
			searchFrom := 0
			for searchFrom < len(clause) {
				rel := strings.Index(clause[searchFrom:], control)
				if rel < 0 {
					break
				}
				at := searchFrom + rel
				suffix := clause[at+len(control):]
				prefix := clause[:at]
				if containsSimulationCorrectionOutcome(suffix) && !simulationCorrectionIsObservedState(suffix) &&
					!containsSimulationAny(lastRunes(prefix, 8), "不能写", "不得", "禁止", "避免", "不需要", "无需", "无须", "并非", "不是", "而非") {
					return true
				}
				searchFrom = at + len(control)
			}
		}
	}
	return false
}

func affirmedModalCorrectionOutcome(clause string) bool {
	for _, modal := range []string{"必须", "立即", "马上"} {
		searchFrom := 0
		for searchFrom < len(clause) {
			rel := strings.Index(clause[searchFrom:], modal)
			if rel < 0 {
				break
			}
			at := searchFrom + rel
			before := lastRunes(clause[:at], 14)
			after := firstRunes(clause[at+len(modal):], 14)
			local := before + modal + after
			if containsSimulationCorrectionOutcome(local) &&
				!containsSimulationAny(before, "不能写", "不得写", "禁止写", "避免", "不需要", "无需", "无须", "今后", "以后") &&
				!strings.Contains(after, "保持") {
				return true
			}
			searchFrom = at + len(modal)
		}
	}
	return false
}

func affirmedDelayedCorrectionOutcome(clause string) bool {
	if !containsSimulationCorrectionOutcome(clause) {
		return false
	}
	for _, marker := range []string{"这才", "后才", "随后"} {
		if at := strings.Index(clause, marker); at >= 0 {
			suffix := clause[at+len(marker):]
			if containsSimulationCorrectionOutcome(suffix) {
				return !simulationCorrectionIsObservedState(suffix)
			}
		}
	}
	preCompleted := containsSimulationAny(clause,
		"已完成", "已经完成", "早已断电", "早已退线", "早已收线", "已断电", "已退线", "已收线", "已退回", "已经断电", "已经退回")
	observing := containsSimulationAny(clause, "检查", "确认", "看见", "看到", "发现", "复核", "核对")
	if preCompleted && observing {
		return false
	}
	if containsSimulationAny(clause, "才断电", "才退线", "才收线", "才返工", "才让", "才叫", "才要求") {
		return true
	}
	if observing {
		return false
	}
	return containsSimulationAny(clause, "到场后", "到场才", "抵达后", "随后", "指出问题后")
}

func simulationCorrectionIsObservedState(text string) bool {
	outcomeAt := simulationCorrectionOutcomeAt(text)
	if outcomeAt < 0 {
		return false
	}
	observationAt := -1
	observationLen := 0
	for _, verb := range []string{"检查", "确认", "看见", "看到", "发现", "复核", "核对"} {
		if at := strings.Index(text, verb); at >= 0 && (observationAt < 0 || at < observationAt) {
			observationAt, observationLen = at, len(verb)
		}
	}
	if observationAt < 0 || observationAt >= outcomeAt {
		return false
	}
	between := text[observationAt+observationLen : outcomeAt]
	return !strings.Contains(between, "后")
}

func simulationCorrectionOutcomeAt(text string) int {
	best := -1
	for _, outcome := range []string{"断电", "退线", "收线", "返工"} {
		if at := strings.Index(text, outcome); at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	for _, retreat := range []string{"退回", "收回", "撤回"} {
		if at := strings.Index(text, retreat); at >= 0 && strings.Contains(text[:at], "线") && (best < 0 || at < best) {
			best = at
		}
	}
	return best
}

func containsSimulationCorrectionOutcome(text string) bool {
	return containsSimulationAny(text, "断电", "退线", "收线", "返工") ||
		(strings.Contains(text, "线") && containsSimulationAny(text, "退回", "收回", "撤回"))
}

func simulationDecisionKnowledgeGaps(decision domain.CharacterWorldDecision) []string {
	boundary := strings.TrimSpace(decision.KnowledgeBoundary)
	if boundary == "" || !strings.Contains(boundary, "不知道") {
		return nil
	}
	fields := simulationDecisionTextFields(decision)
	var gaps []string
	for _, unknownClause := range simulationUnknownBoundaryClauses(boundary) {
		at := strings.Index(unknownClause, "不知道")
		unknown := canonicalUnknownClaim(unknownClause[at+len("不知道"):])
		if len([]rune(unknown)) < 4 {
			continue
		}
		for _, field := range fields {
			if field.Path == "knowledge_boundary" {
				continue
			}
			for _, candidate := range splitSimulationClauses(field.Text) {
				canonical := canonicalUnknownClaim(candidate)
				matchAt := simulationUnknownClaimMatchAt(canonical, unknown)
				if matchAt < 0 || simulationClauseNegatesClaim(canonical, matchAt) {
					continue
				}
				if simulationCandidateShowsKnowledgeAcquisition(unknownClause, candidate, unknown) {
					continue
				}
				gaps = append(gaps, fmt.Sprintf("character_decisions(%s).%s contradicts knowledge_boundary unknown claim %q", decision.Character, field.Path, unknown))
				break
			}
		}
	}
	return compactStrings(gaps)
}

func simulationUnknownBoundaryClauses(boundary string) []string {
	var result []string
	for _, clause := range splitSimulationClauses(boundary) {
		searchFrom := 0
		for searchFrom < len(clause) {
			rel := strings.Index(clause[searchFrom:], "不知道")
			if rel < 0 {
				break
			}
			start := searchFrom + rel
			end := len(clause)
			if next := strings.Index(clause[start+len("不知道"):], "不知道"); next >= 0 {
				end = start + len("不知道") + next
			}
			segment := strings.TrimSpace(clause[start:end])
			for _, suffix := range []string{"，也", ",也", "，还", ",还", "也", "还"} {
				segment = strings.TrimSuffix(segment, suffix)
			}
			if segment != "" {
				result = append(result, segment)
			}
			searchFrom = end
		}
	}
	return result
}

func simulationCandidateShowsKnowledgeAcquisition(boundaryClause, candidate, unknown string) bool {
	// Strong evidence channels may legitimately turn an earlier unknown into a
	// known fact within the same chapter. The evidence-bearing prefix must itself
	// contain this exact claim; an unrelated ticket, sight or quoted sentence may
	// not launder an inference after "因此/所以/推断/可能".
	evidence := simulationKnowledgeEvidencePrefix(candidate)
	directEvidence := simulationDirectKnowledgeEvidenceClause(evidence)
	if directEvidence == "" || simulationUnknownClaimMatchAt(canonicalUnknownClaim(directEvidence), unknown) < 0 {
		return false
	}
	if containsSimulationAny(directEvidence,
		"亲眼看见", "亲眼看到", "检查后确认", "检查确认后", "核对后确认", "核对确认后",
		"票据显示", "记录显示", "明确告知", "被明确告知", "明确说", "回答说",
	) {
		return true
	}
	// When the boundary itself pins the discovery to arrival, require the
	// candidate to repeat both the arrival and an observable discovery verb.
	boundaryAllowsArrivalDiscovery := containsSimulationAny(boundaryClause, "到场时才发现", "到场后才发现", "抵达时才发现", "抵达后才发现")
	return boundaryAllowsArrivalDiscovery &&
		containsSimulationAny(directEvidence, "到场时", "到场后", "抵达时", "抵达后") &&
		containsSimulationAny(directEvidence, "看见", "看到", "发现", "检查", "确认")
}

func simulationDirectKnowledgeEvidenceClause(evidence string) string {
	markers := []string{
		"亲眼看见", "亲眼看到", "检查后确认", "检查确认后", "核对后确认", "核对确认后",
		"票据显示", "记录显示", "明确告知", "被明确告知", "明确说", "回答说",
		"到场时", "到场后", "抵达时", "抵达后",
	}
	markerAt := -1
	for _, marker := range markers {
		if at := strings.Index(evidence, marker); at >= 0 && (markerAt < 0 || at < markerAt) {
			markerAt = at
		}
	}
	if markerAt < 0 {
		return ""
	}
	start := 0
	for _, separator := range []string{"，", ",", "；", ";", "。", "！", "？", "\n"} {
		if at := strings.LastIndex(evidence[:markerAt], separator); at >= 0 && at+len(separator) > start {
			start = at + len(separator)
		}
	}
	end := len(evidence)
	for _, separator := range []string{"，", ",", "；", ";", "。", "！", "？", "\n"} {
		if rel := strings.Index(evidence[start:], separator); rel >= 0 && start+rel < end {
			end = start + rel
		}
	}
	return strings.TrimSpace(evidence[start:end])
}

func simulationKnowledgeEvidencePrefix(candidate string) string {
	end := len(candidate)
	for _, marker := range []string{"因此", "所以", "于是", "从而", "进而", "推断", "猜测", "怀疑", "可能", "这才知道", "因而知道"} {
		if at := strings.Index(candidate, marker); at >= 0 && at < end {
			end = at
		}
	}
	return strings.TrimSpace(candidate[:end])
}

func simulationUnknownClaimMatchAt(candidate, unknown string) int {
	if at := strings.Index(candidate, unknown); at >= 0 {
		return at
	}
	// A later clause may insert another learned predicate between the subject
	// and location ("林澈回青山县且在夜市").  Preserve the same subject+place
	// match instead of letting that conjunction hide the contradiction.
	for _, pivot := range []string{"回", "在", "要", "有", "是"} {
		if at := strings.Index(unknown, pivot); at >= len("林") {
			subject, predicate := unknown[:at], unknown[at:]
			if subjectAt := strings.Index(candidate, subject); subjectAt >= 0 && strings.Contains(candidate[subjectAt+len(subject):], predicate) {
				return subjectAt
			}
		}
	}
	return -1
}

func splitSimulationClauses(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '；', ';', '\n':
			return true
		default:
			return false
		}
	})
}

func canonicalUnknownClaim(text string) string {
	text = strings.TrimSpace(text)
	if at := strings.IndexAny(text, "（("); at >= 0 {
		text = text[:at]
	}
	text = strings.NewReplacer(
		"今天", "", "当晚", "", "目前", "", "当前", "", "已经", "", "已", "", "正在", "", "尚在", "",
		"做的事", "", "发生的事", "", "一带", "", "附近", "", "了", "",
		"，", "", ",", "", "：", "", ":", "", "——", "", "—", "", " ", "", "\t", "",
	).Replace(text)
	return strings.TrimSpace(text)
}

func simulationClauseNegatesClaim(clause string, matchAt int) bool {
	prefix := clause[:matchAt]
	return containsSimulationAny(lastRunes(prefix, 12), "不知道", "不知", "尚未", "还未", "没有", "不能", "不得", "禁止", "无法", "无从", "并非", "不是")
}

func lastRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[len(runes)-limit:])
}

func firstRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
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

// ChapterWorldSimulationStatus exposes the pre-plan boundary to the Host
// router without leaking simulation implementation details into flow logic.
func ChapterWorldSimulationStatus(s *store.Store, chapter int) (required, ready bool, gaps []string) {
	if s == nil || chapter <= 0 || !chapterWorldSimulationRequired(s) {
		return false, true, nil
	}
	if partial, err := s.LoadChapterWorldSimulationPartial(chapter); err != nil {
		return true, false, []string{err.Error()}
	} else if partial != nil {
		return true, false, chapterWorldSimulationGaps(s, *partial)
	}
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return true, false, []string{err.Error()}
	}
	if sim == nil {
		return true, false, []string{"missing chapter world simulation"}
	}
	gaps = chapterWorldSimulationGaps(s, *sim)
	return true, len(gaps) == 0, gaps
}

func ensureChapterWorldSimulationReadyForPlanning(s *store.Store, chapter int) (*domain.ChapterWorldSimulation, error) {
	if !chapterWorldSimulationRequired(s) {
		// A cast change can make a previously staged simulation optional. Planning
		// is the write-side recovery point: discard that stale partial so the
		// shared prose guard does not deadlock on an artifact no simulator will
		// ever be routed to finish.
		if partial, err := s.LoadChapterWorldSimulationPartial(chapter); err != nil {
			return nil, err
		} else if partial != nil {
			if err := s.DeleteChapterWorldSimulationPartial(chapter); err != nil {
				return nil, err
			}
		}
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
	if instruction, loadErr := loadChapterPipelineInstruction(s, plan.Chapter); loadErr != nil {
		return fmt.Errorf("第 %d 章 pipeline instruction 无法校验: %w", plan.Chapter, loadErr)
	} else if instruction != nil && !contextSourcesContain(causal.ContextSources, instruction.Token) {
		return fmt.Errorf("第 %d 章 context_sources 必须绑定当前 chapter_pipeline_instruction=%s: %w", plan.Chapter, instruction.Token, errs.ErrToolPrecondition)
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
