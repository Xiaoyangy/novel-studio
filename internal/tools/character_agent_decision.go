package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SubmitCharacterDecisionTool is bound to exactly one redacted observation.
// Identity, chapter, generation, round and observation digest are supplied by
// the host and cannot be selected or rewritten by the character model.
type SubmitCharacterDecisionTool struct {
	store       *store.Store
	observation domain.CharacterObservationPacket
}

func NewSubmitCharacterDecisionTool(st *store.Store, observation domain.CharacterObservationPacket) *SubmitCharacterDecisionTool {
	return &SubmitCharacterDecisionTool{store: st, observation: observation}
}

func (t *SubmitCharacterDecisionTool) Name() string                         { return "submit_character_decision" }
func (t *SubmitCharacterDecisionTool) Label() string                        { return "提交角色决定" }
func (t *SubmitCharacterDecisionTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *SubmitCharacterDecisionTool) ConcurrencySafe(json.RawMessage) bool { return true }
func (t *SubmitCharacterDecisionTool) Description() string {
	return "为当前绑定角色提交一次独立决定。只能引用 observation 中存在的 fact id；不能选择角色、章节或 generation。成功后立即停止。"
}

func (t *SubmitCharacterDecisionTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("time", schema.String("行动时点")),
		schema.Property("location", schema.String("当前地点")).Required(),
		schema.Property("current_goal", schema.String("角色自己的当前目标")).Required(),
		schema.Property("pressure", schema.String("当前压力")).Required(),
		schema.Property("resources", schema.Array("实际可用资源", schema.String(""))),
		schema.Property("available_options", schema.Array("当下真实可选行动，至少两个", schema.String(""))).Required(),
		schema.Property("rejected_options", schema.Array("拒绝的选项", schema.String(""))),
		schema.Property("decision", schema.String("最终选择")).Required(),
		schema.Property("decision_reason", schema.String("基于角色目标、压力和已知事实的简明理由；不要输出思维链")).Required(),
		schema.Property("intended_action", schema.String("选择转化成的具体行动")).Required(),
		schema.Property("action_duration", schema.String("现实耗时")).Required(),
		schema.Property("knowledge_refs", schema.Array("本次决定实际使用的 observation fact id，只能原样引用", schema.String("fact id"))).Required(),
		schema.Property("resource_claims", schema.Array("会占用、消耗或竞争的资源", schema.String(""))),
		schema.Property("constraints", schema.Array("角色自己必须遵守的边界", schema.String(""))),
		schema.Property("contingencies", schema.Array("失败或受阻时的备选反应", schema.String(""))),
		schema.Property("expected_consequences", schema.Array("角色从自身视角预期的后果", schema.String(""))),
	)
}

func (t *SubmitCharacterDecisionTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return nil, fmt.Errorf("character decision store is unavailable")
	}
	var input struct {
		Time                 string   `json:"time"`
		Location             string   `json:"location"`
		CurrentGoal          string   `json:"current_goal"`
		Pressure             string   `json:"pressure"`
		Resources            []string `json:"resources"`
		AvailableOptions     []string `json:"available_options"`
		RejectedOptions      []string `json:"rejected_options"`
		Decision             string   `json:"decision"`
		DecisionReason       string   `json:"decision_reason"`
		IntendedAction       string   `json:"intended_action"`
		ActionDuration       string   `json:"action_duration"`
		KnowledgeRefs        []string `json:"knowledge_refs"`
		ResourceClaims       []string `json:"resource_claims"`
		Constraints          []string `json:"constraints"`
		Contingencies        []string `json:"contingencies"`
		ExpectedConsequences []string `json:"expected_consequences"`
	}
	if err := unmarshalToolArgs(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	proposal := domain.CharacterDecisionProposal{
		Version:              domain.CharacterDecisionProposalVersion,
		GenerationID:         t.observation.GenerationID,
		Chapter:              t.observation.Chapter,
		Round:                t.observation.Round,
		AgentID:              t.observation.AgentID,
		Character:            t.observation.Character,
		ObservationDigest:    t.observation.Digest,
		Time:                 strings.TrimSpace(input.Time),
		Location:             strings.TrimSpace(input.Location),
		CurrentGoal:          strings.TrimSpace(input.CurrentGoal),
		Pressure:             strings.TrimSpace(input.Pressure),
		Resources:            input.Resources,
		AvailableOptions:     input.AvailableOptions,
		RejectedOptions:      input.RejectedOptions,
		Decision:             strings.TrimSpace(input.Decision),
		DecisionReason:       strings.TrimSpace(input.DecisionReason),
		IntendedAction:       strings.TrimSpace(input.IntendedAction),
		ActionDuration:       strings.TrimSpace(input.ActionDuration),
		KnowledgeRefs:        input.KnowledgeRefs,
		ResourceClaims:       input.ResourceClaims,
		Constraints:          input.Constraints,
		Contingencies:        input.Contingencies,
		ExpectedConsequences: input.ExpectedConsequences,
		SubmittedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	finalized, err := domain.FinalizeCharacterDecisionProposal(proposal, t.observation)
	if err != nil {
		return nil, fmt.Errorf("character decision rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	if err := t.store.CharacterAgents.SaveProposal(finalized, t.observation); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"submitted":          true,
		"agent_id":           finalized.AgentID,
		"character":          finalized.Character,
		"chapter":            finalized.Chapter,
		"round":              finalized.Round,
		"proposal_digest":    finalized.Digest,
		"observation_digest": finalized.ObservationDigest,
	})
}

// SubmitCharacterAgentSuccessorPlanTool is bound to the immutable constraints
// of a failed arbitration. The Architect may replace only title/core event/
// hook/scenes for the remaining soft slots; chapter numbers and contract refs
// are restored by the host.
type SubmitCharacterAgentSuccessorPlanTool struct {
	store    *store.Store
	base     domain.CharacterAgentSuccessorPlan
	original map[int]domain.OutlineEntry
}

func NewSubmitCharacterAgentSuccessorPlanTool(st *store.Store, base domain.CharacterAgentSuccessorPlan, original []domain.OutlineEntry) *SubmitCharacterAgentSuccessorPlanTool {
	byChapter := make(map[int]domain.OutlineEntry, len(original))
	for _, entry := range original {
		byChapter[entry.Chapter] = entry
	}
	return &SubmitCharacterAgentSuccessorPlanTool{store: st, base: base, original: byChapter}
}

func (t *SubmitCharacterAgentSuccessorPlanTool) Name() string {
	return "submit_character_agent_successor_plan"
}
func (t *SubmitCharacterAgentSuccessorPlanTool) Label() string {
	return "提交角色冲突后继规划"
}
func (t *SubmitCharacterAgentSuccessorPlanTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *SubmitCharacterAgentSuccessorPlanTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *SubmitCharacterAgentSuccessorPlanTool) Description() string {
	return "在不改结局、篇幅、硬合同、章节号、既有正史和角色选择的前提下，重排冲突章至当前弧末的软大纲。"
}

func (t *SubmitCharacterAgentSuccessorPlanTool) Schema() map[string]any {
	chapter := schema.Object(
		schema.Property("chapter", schema.Int("原章节号，不得改变")).Required(),
		schema.Property("title", schema.String("修订后的软标题")).Required(),
		schema.Property("core_event", schema.String("服从角色真实选择的具体核心事件")).Required(),
		schema.Property("hook", schema.String("不预定未来角色选择的章节钩子")).Required(),
		schema.Property("scenes", schema.Array("具体场景安排", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("architect_summary", schema.String("简述如何保留硬合同并重排软事件，不输出思维链")).Required(),
		schema.Property("revised_chapters", schema.Array("从冲突章到当前弧末的完整连续软章位", chapter)).Required(),
	)
}

func (t *SubmitCharacterAgentSuccessorPlanTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return nil, fmt.Errorf("character-agent successor store is unavailable")
	}
	var input struct {
		ArchitectSummary string `json:"architect_summary"`
		RevisedChapters  []struct {
			Chapter   int      `json:"chapter"`
			Title     string   `json:"title"`
			CoreEvent string   `json:"core_event"`
			Hook      string   `json:"hook"`
			Scenes    []string `json:"scenes"`
		} `json:"revised_chapters"`
	}
	if err := unmarshalToolArgs(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	plan := t.base
	plan.ArchitectSummary = strings.TrimSpace(input.ArchitectSummary)
	plan.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	plan.RevisedChapters = make([]domain.OutlineEntry, 0, len(input.RevisedChapters))
	for _, revision := range input.RevisedChapters {
		original, ok := t.original[revision.Chapter]
		if !ok {
			return nil, fmt.Errorf("successor plan cannot add or renumber chapter %d: %w", revision.Chapter, errs.ErrToolPrecondition)
		}
		plan.RevisedChapters = append(plan.RevisedChapters, domain.OutlineEntry{
			Chapter: revision.Chapter, Title: strings.TrimSpace(revision.Title), CoreEvent: strings.TrimSpace(revision.CoreEvent),
			Hook: strings.TrimSpace(revision.Hook), Scenes: revision.Scenes,
			ContractRefs: append([]domain.StoryContractRef(nil), original.ContractRefs...),
		})
	}
	finalized, err := domain.FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil {
		return nil, fmt.Errorf("character-agent successor plan rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	if err := t.store.CharacterAgents.SaveSuccessorPlan(finalized, true); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"submitted": true, "parent_generation_id": finalized.ParentGenerationID,
		"trigger_chapter": finalized.TriggerChapter, "plan_digest": finalized.Digest,
	})
}

// ResolveChapterWorldTool is bound to one complete round of independent
// proposals. It rejects any attempt to rewrite a character's decision or
// intended action and materializes the backward-compatible simulation only
// after every active character has a resolution.
type ResolveChapterWorldTool struct {
	store             *store.Store
	stimulus          domain.WorldStimulusPacket
	activation        domain.CharacterAgentActivation
	proposals         []domain.CharacterDecisionProposal
	protocolDigest    string
	sources           []string
	maxRevisionRounds int
}

func NewResolveChapterWorldTool(st *store.Store, stimulus domain.WorldStimulusPacket, activation domain.CharacterAgentActivation, proposals []domain.CharacterDecisionProposal, protocolDigest string, sources []string, maxRevisionRounds int) *ResolveChapterWorldTool {
	return &ResolveChapterWorldTool{
		store: st, stimulus: stimulus, activation: activation,
		proposals:      append([]domain.CharacterDecisionProposal(nil), proposals...),
		protocolDigest: strings.TrimSpace(protocolDigest),
		sources:        append([]string(nil), sources...), maxRevisionRounds: maxRevisionRounds,
	}
}

func (t *ResolveChapterWorldTool) Name() string                         { return "resolve_chapter_world" }
func (t *ResolveChapterWorldTool) Label() string                        { return "世界裁决" }
func (t *ResolveChapterWorldTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ResolveChapterWorldTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ResolveChapterWorldTool) Description() string {
	return "裁决当前绑定的全部独立角色提案。decision 和 intended_action 必须逐字复制提案；只能决定顺序、可行性、完成度、结果和蝴蝶效应。存在未解决冲突时返回最小反馈，最终轮仍未解决会拒绝。"
}

func (t *ResolveChapterWorldTool) Schema() map[string]any {
	effect := schema.Object(
		schema.Property("effect", schema.String("世界状态变化")).Required(),
		schema.Property("targets", schema.Array("受影响对象", schema.String(""))),
		schema.Property("transmission_path", schema.String("传播路径")).Required(),
		schema.Property("arrival_chapter", schema.Int("最早抵达章节")).Required(),
		schema.Property("visibility", schema.Enum("POV 可见性", "visible", "delayed", "hidden")).Required(),
		schema.Property("protagonist_impact", schema.String("对主角选项或压力的影响")).Required(),
	)
	resolution := schema.Object(
		schema.Property("agent_id", schema.String("提案中的 agent_id")).Required(),
		schema.Property("character", schema.String("提案中的角色实名")).Required(),
		schema.Property("proposal_digest", schema.String("提案摘要")).Required(),
		schema.Property("decision", schema.String("逐字复制提案 decision")).Required(),
		schema.Property("intended_action", schema.String("逐字复制提案 intended_action")).Required(),
		schema.Property("action_order", schema.Int("行动顺序，从1开始")).Required(),
		schema.Property("outcome", schema.Enum("可行性结果", "success", "partial", "blocked")).Required(),
		schema.Property("completion_state", schema.Enum("本章末完成度", "instant", "started", "in_progress", "completed", "blocked")).Required(),
		schema.Property("immediate_result", schema.String("即时世界反馈")).Required(),
		schema.Property("state_after", schema.String("行动后状态")).Required(),
		schema.Property("visible_to_pov", schema.Bool("POV 是否直接可见")),
		schema.Property("butterfly_effects", schema.Array("下游影响，至少一个", effect)).Required(),
		schema.Property("conflict_ids", schema.Array("关联冲突 id", schema.String(""))),
	)
	conflict := schema.Object(
		schema.Property("id", schema.String("冲突 id")).Required(),
		schema.Property("kind", schema.Enum("冲突类型", "time", "location", "resource", "rule", "knowledge", "collision")).Required(),
		schema.Property("affected_agent_ids", schema.Array("受影响角色 Agent", schema.String(""))).Required(),
		schema.Property("feedback", schema.String("只包含冲突本身的最小反馈，不泄露其他角色私有信息")).Required(),
		schema.Property("resolved", schema.Bool("冲突是否已经解决")).Required(),
	)
	projection := schema.Object(
		schema.Property("protagonist", schema.String("主视角角色")).Required(),
		schema.Property("observable_effects", schema.Array("主角可感知影响", schema.String(""))).Required(),
		schema.Property("hidden_pressures", schema.Array("已发生但主角未知的压力", schema.String(""))).Required(),
		schema.Property("available_options", schema.Array("主角决定时真实可用选项", schema.String(""))).Required(),
		schema.Property("chosen_decision", schema.String("必须等于主角提案 decision")).Required(),
		schema.Property("decision_reason", schema.String("基于可见证据的简明理由")).Required(),
		schema.Property("plan_constraints", schema.Array("POV 规划边界", schema.String(""))).Required(),
		schema.Property("causal_chain", schema.Array("决定汇聚链", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("time_window", schema.String("本章现实时间窗口")).Required(),
		schema.Property("resolutions", schema.Array("每个活跃角色恰好一条裁决", resolution)).Required(),
		schema.Property("conflicts", schema.Array("检测到的冲突", conflict)),
		schema.Property("hard_contract_status", schema.Enum("角色意图与不可协商硬合同是否仍可共同实现", "feasible", "infeasible")).Required(),
		schema.Property("hard_contract_conflicts", schema.Array("不可实现时逐条引用冲突的硬合同；feasible 时为空", schema.String(""))),
		schema.Property("protagonist_projection", projection).Required(),
		schema.Property("finalized", schema.Bool("无未解决冲突时传 true")).Required(),
	)
}

func (t *ResolveChapterWorldTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.store == nil || len(t.proposals) == 0 {
		return nil, fmt.Errorf("world arbiter has no bound proposals")
	}
	var input struct {
		TimeWindow            string                               `json:"time_window"`
		Resolutions           []domain.CharacterDecisionResolution `json:"resolutions"`
		Conflicts             []domain.WorldArbitrationConflict    `json:"conflicts"`
		HardContractStatus    string                               `json:"hard_contract_status"`
		HardContractConflicts []string                             `json:"hard_contract_conflicts"`
		ProtagonistProjection domain.ProtagonistDecisionProjection `json:"protagonist_projection"`
		Finalized             bool                                 `json:"finalized"`
	}
	if err := unmarshalToolArgs(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	round := t.proposals[0].Round
	proposalDigests := make([]string, 0, len(t.proposals))
	for _, proposal := range t.proposals {
		if proposal.Round > round {
			round = proposal.Round
		}
		proposalDigests = append(proposalDigests, proposal.Digest)
	}
	receipt := domain.WorldArbitrationReceipt{
		Version:               domain.WorldArbitrationReceiptVersion,
		GenerationID:          t.stimulus.GenerationID,
		Chapter:               t.stimulus.Chapter,
		Round:                 round,
		StimulusDigest:        t.stimulus.Digest,
		ActivationDigest:      t.activation.Digest,
		ProposalDigests:       proposalDigests,
		Resolutions:           input.Resolutions,
		Conflicts:             input.Conflicts,
		HardContractStatus:    input.HardContractStatus,
		HardContractConflicts: input.HardContractConflicts,
		ProtagonistProjection: input.ProtagonistProjection,
		Finalized:             input.Finalized,
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	finalizedReceipt, err := domain.FinalizeWorldArbitrationReceipt(receipt, t.stimulus, t.activation, t.proposals, t.maxRevisionRounds)
	if err != nil {
		return nil, fmt.Errorf("world arbitration rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	if err := t.store.CharacterAgents.SaveArbitration(finalizedReceipt, t.stimulus, t.activation, t.proposals, t.maxRevisionRounds); err != nil {
		return nil, err
	}
	if !finalizedReceipt.Finalized {
		affected := make([]string, 0)
		for _, conflict := range finalizedReceipt.Conflicts {
			if !conflict.Resolved {
				affected = append(affected, conflict.AffectedAgentIDs...)
			}
		}
		return json.Marshal(map[string]any{
			"resolved": false, "chapter": finalizedReceipt.Chapter, "round": round,
			"arbitration_digest": finalizedReceipt.Digest,
			"affected_agent_ids": compactStrings(affected),
		})
	}

	decisions, err := finalizedReceipt.CharacterDecisions(t.proposals)
	if err != nil {
		return nil, err
	}
	if err := validateIncomingSimulationSemanticInvariants(t.store, finalizedReceipt.Chapter, decisions, finalizedReceipt.ProtagonistProjection, nil); err != nil {
		return nil, fmt.Errorf("resolved character decisions are invalid: %w", err)
	}
	observationDigests := make([]string, 0, len(t.proposals))
	memoryRoots := make([]string, 0, len(t.proposals))
	for _, proposal := range t.proposals {
		observationDigests = append(observationDigests, proposal.ObservationDigest)
		if observation, loadErr := t.store.CharacterAgents.LoadObservation(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID); loadErr == nil && observation != nil && observation.MemoryRoot != "" {
			memoryRoots = append(memoryRoots, observation.MemoryRoot)
		}
	}
	simulation := domain.ChapterWorldSimulation{
		Version:               2,
		Chapter:               finalizedReceipt.Chapter,
		GenerationID:          finalizedReceipt.GenerationID,
		TimeWindow:            strings.TrimSpace(input.TimeWindow),
		CharacterDecisions:    decisions,
		ProtagonistProjection: finalizedReceipt.ProtagonistProjection,
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		Sources:               compactStrings(append(append([]string(nil), t.stimulus.Sources...), t.sources...)),
		CharacterAgentProtocol: &domain.CharacterAgentProtocolReceipt{
			Version:            domain.CharacterAgentDecisionProtocolVersion,
			RegistryRoot:       t.activation.RegistryRoot,
			StimulusDigest:     t.stimulus.Digest,
			ActivationDigest:   t.activation.Digest,
			ObservationDigests: compactStrings(observationDigests),
			ProposalDigests:    compactStrings(proposalDigests),
			ArbitrationRound:   round,
			ArbitrationDigest:  finalizedReceipt.Digest,
			MemoryRoots:        compactStrings(memoryRoots),
			ProtocolDigest:     t.protocolDigest,
		},
	}
	if simulation.TimeWindow == "" {
		simulation.TimeWindow = t.stimulus.TimeWindow
	}
	if tick, loadErr := t.store.WorldSim.LoadTick(); loadErr == nil && tick != nil {
		simulation.BaseTickID = tick.TickID
	}
	if simulation.TimeWindow == "" || simulation.CharacterAgentProtocol.ProtocolDigest == "" {
		return nil, fmt.Errorf("world arbitration lacks time window or protocol digest: %w", errs.ErrToolPrecondition)
	}
	if _, projectAllToken, loadErr := loadProjectAllStateForExecution(t.store, simulation.Chapter); loadErr != nil {
		return nil, loadErr
	} else if projectAllToken != "" {
		if !projectAllStateSourcesContain(simulation.Sources, projectAllToken) {
			return nil, fmt.Errorf("character-agent simulation lacks project-all state source: %w", errs.ErrToolPrecondition)
		}
		if err := consumePlanningContextAccessReceipt(t.store, simulation.Chapter, domain.PlanningContextAccessSimulate, simulation.Sources); err != nil {
			return nil, fmt.Errorf("character-agent simulation lacks consumed context receipt: %w", err)
		}
	}
	simulation.SimulationID = chapterWorldSimulationID(simulation)
	if err := validateStoredCharacterAgentProtocol(t.store, simulation); err != nil {
		return nil, fmt.Errorf("character-agent protocol verification: %w", err)
	}
	if err := t.store.SaveChapterWorldSimulation(simulation); err != nil {
		return nil, err
	}
	if err := t.store.Drafts.DeleteChapterPlanPartial(simulation.Chapter); err != nil {
		return nil, err
	}
	if err := t.store.DeleteChapterWorldSimulationPartial(simulation.Chapter); err != nil {
		return nil, err
	}
	if err := ensureChapterWorldSimulationCheckpoint(t.store, simulation.Chapter); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"resolved": true, "simulated": true, "chapter": simulation.Chapter,
		"round": round, "simulation_id": simulation.SimulationID,
		"arbitration_digest":     finalizedReceipt.Digest,
		"protagonist_projection": simulation.ProtagonistProjection,
	})
}

func validateStoredCharacterAgentProtocol(st *store.Store, simulation domain.ChapterWorldSimulation) error {
	if simulation.Version < 2 || simulation.CharacterAgentProtocol == nil {
		return fmt.Errorf("missing character-agent protocol receipt")
	}
	receipt := simulation.CharacterAgentProtocol
	if receipt.Version != domain.CharacterAgentDecisionProtocolVersion || receipt.RegistryRoot == "" || receipt.StimulusDigest == "" || receipt.ActivationDigest == "" || receipt.ArbitrationRound <= 0 || receipt.ArbitrationDigest == "" || receipt.ProtocolDigest == "" {
		return fmt.Errorf("character-agent protocol receipt is incomplete")
	}
	registry, err := st.CharacterAgents.LoadRegistrySnapshot(simulation.GenerationID, simulation.Chapter)
	if err != nil || registry == nil || registry.RegistryRoot != receipt.RegistryRoot {
		return fmt.Errorf("character-agent registry root mismatch: %w", err)
	}
	stimulus, err := st.CharacterAgents.LoadStimulus(simulation.GenerationID, simulation.Chapter)
	if err != nil || stimulus == nil || stimulus.Digest != receipt.StimulusDigest {
		return fmt.Errorf("world stimulus digest mismatch: %w", err)
	}
	activation, err := st.CharacterAgents.LoadActivation(simulation.GenerationID, simulation.Chapter)
	if err != nil || activation == nil || activation.Digest != receipt.ActivationDigest {
		return fmt.Errorf("character activation digest mismatch: %w", err)
	}
	ids := make([]string, 0)
	for _, entry := range activation.Entries {
		if entry.State == domain.CharacterAgentActive {
			ids = append(ids, entry.AgentID)
		}
	}
	proposals, err := st.CharacterAgents.LoadLatestProposals(simulation.GenerationID, simulation.Chapter, receipt.ArbitrationRound, ids)
	if err != nil || len(proposals) != len(ids) {
		return fmt.Errorf("character proposal set is incomplete: %w", err)
	}
	proposalDigests := make([]string, 0, len(proposals))
	observationDigests := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		proposalDigests = append(proposalDigests, proposal.Digest)
		observationDigests = append(observationDigests, proposal.ObservationDigest)
	}
	if !sameStringSet(proposalDigests, receipt.ProposalDigests) || !sameStringSet(observationDigests, receipt.ObservationDigests) {
		return fmt.Errorf("character proposal/observation digest set mismatch")
	}
	arbitration, err := st.CharacterAgents.LoadArbitration(simulation.GenerationID, simulation.Chapter, receipt.ArbitrationRound)
	if err != nil || arbitration == nil || !arbitration.Finalized || arbitration.Digest != receipt.ArbitrationDigest {
		return fmt.Errorf("world arbitration digest mismatch: %w", err)
	}
	decisions, err := arbitration.CharacterDecisions(proposals)
	if err != nil {
		return err
	}
	left, _ := json.Marshal(decisions)
	right, _ := json.Marshal(simulation.CharacterDecisions)
	if string(left) != string(right) || simulation.ProtagonistProjection.ChosenDecision != arbitration.ProtagonistProjection.ChosenDecision {
		return fmt.Errorf("simulation projection differs from arbitration")
	}
	if simulation.SimulationID != "" && simulation.SimulationID != chapterWorldSimulationID(simulation) {
		return fmt.Errorf("character-agent simulation id mismatch")
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	left = compactStrings(left)
	right = compactStrings(right)
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		seen[value]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
