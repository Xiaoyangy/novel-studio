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
	store         *store.Store
	proofs        *store.CharacterAgentStore
	observation   domain.CharacterObservationPacket
	arbitrationV3 *store.CharacterArbitrationV3
}

func NewSubmitCharacterDecisionTool(st *store.Store, observation domain.CharacterObservationPacket) *SubmitCharacterDecisionTool {
	t := &SubmitCharacterDecisionTool{store: st, observation: observation}
	if st != nil {
		t.proofs = st.CharacterAgents
	}
	return t
}

func (t *SubmitCharacterDecisionTool) Name() string                         { return "submit_character_decision" }
func (t *SubmitCharacterDecisionTool) Label() string                        { return "提交角色决定" }
func (t *SubmitCharacterDecisionTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *SubmitCharacterDecisionTool) ConcurrencySafe(json.RawMessage) bool { return true }
func (t *SubmitCharacterDecisionTool) Description() string {
	return "为当前绑定角色提交一次独立决定。只能引用 observation 中存在的 fact id 和公开 mechanism id；不能选择角色、章节或 generation。成功后立即停止。"
}

func (t *SubmitCharacterDecisionTool) Schema() map[string]any {
	result := schema.Object(
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
		schema.Property("mechanism_refs", schema.Array("本次行动实际使用的 public_mechanisms id；普通行动可为空", schema.String("mechanism id"))),
		schema.Property("resource_claims", schema.Array("会占用、消耗或竞争的资源", schema.String(""))),
		schema.Property("constraints", schema.Array("角色自己必须遵守的边界", schema.String(""))),
		schema.Property("contingencies", schema.Array("失败或受阻时的备选反应", schema.String(""))),
		schema.Property("expected_consequences", schema.Array("角色从自身视角预期的后果", schema.String(""))),
	)
	if t.observation.Version == domain.CharacterObservationV2Version {
		properties := result["properties"].(map[string]any)
		properties["location"] = schema.String("当前观察包的行动起点，必须逐字等于 observation.location；不得填打算前往的目的地")
		properties["resource_estimates"] = schema.Array("只基于本人resource_views及knowledge_refs形成的行动后数量估计；允许错误，不是世界真值", schema.Object(
			schema.Property("resource_id", schema.String("本人resource_views中可见的稳定resource_id")).Required(),
			schema.Property("estimate_min", schema.Number("本人估计下界，非负")).Required(),
			schema.Property("estimate_max", schema.Number("本人估计上界，须不小于下界；相同上下界仍只表示估计")).Required(),
			schema.Property("evidence_refs", schema.Array("本人观察中可引用的fact/resource evidence id", schema.String(""))).Required(),
		))
		properties["resource_measurements"] = schema.Array("明确属于本人选择的测量行动；缺测量请求不能把世界余额当已观测数", schema.Object(
			schema.Property("resource_id", schema.String("待测的既有可见resource_id")).Required(),
			schema.Property("mechanism_ref", schema.String("本人public_mechanisms中实际拟使用、且列入mechanism_refs的测量机制id")).Required(),
		))
		if domain.HasCharacterResourceObservationTimePolicyV1(t.observation.Sources) {
			measurement := properties["resource_measurements"].(map[string]any)["items"].(map[string]any)
			measurement["properties"].(map[string]any)["task_id"] = schema.String("本人self_tasks中承载此次测量的work任务ID；该任务须列出相同resource_id，实际读数时点由其真实执行end_day确定")
			measurement["required"] = append(measurement["required"].([]string), "task_id")
		}
		properties["resource_reports"] = schema.Array("本人选择发给既有角色的数量报告/凭据标示；接收者将其作为未核实reported信息", schema.Object(
			schema.Property("resource_id", schema.String("本人可见的resource_id")).Required(),
			schema.Property("to_character", schema.String("明确接收角色实名")).Required(),
			schema.Property("amount", physicalNullableNumberSchema("所报告或出示的数值，可与世界实际余额不同；仅定性交接/名称告知时null")).Required(),
			schema.Property("perceived_name", schema.String("可选：本人resource_view中的已知名称，明确告诉接收者；不得携带作者态私有名称")),
			schema.Property("perceived_unit", schema.String("可选：本人resource_view中已知且明确告诉接收者的单位，不得从世界真值推定")),
			schema.Property("evidence_refs", schema.Array("本人可引用的报告来源fact/resource evidence id", schema.String(""))).Required(),
		))
	}
	if t.observation.Version == domain.CharacterObservationV2Version {
		properties := result["properties"].(map[string]any)
		properties["communications"] = characterCommunicationsSchema()
		properties["resource_reads"] = characterResourceReadsSchema()
		if domain.HasCharacterIncomingMaterialReadPolicyV1(t.observation.Sources) {
			properties["resource_reads"] = characterIncomingMaterialReadsSchemaV1()
		}
		if domain.HasCharacterSelfExperiencePolicyV2(t.observation.Sources) {
			properties["self_tasks"] = characterSelfTasksSchema(domain.HasCharacterOperationalAvailabilityPolicyV1(t.observation.Sources))
			if domain.HasCharacterSurfaceInspectionPolicyV1(t.observation.Sources) {
				tasks := properties["self_tasks"].(map[string]any)
				tasks["items"].(map[string]any)["properties"].(map[string]any)["observation_requests"] = characterSurfaceInspectionRequestsSchemaV1()
			}
			if domain.HasCharacterWorkArtifactPolicyV1(t.observation.Sources) {
				tasks := properties["self_tasks"].(map[string]any)
				tasks["items"].(map[string]any)["properties"].(map[string]any)["output_requests"] = characterWorkOutputRequestsSchema()
				properties["artifact_reads"] = characterArtifactReadsSchema(false)
				properties["artifact_signs"] = characterArtifactSignsSchema(false)
				properties["artifact_access"] = characterArtifactAccessSchema()
			}
			result["required"] = append(result["required"].([]string), "self_tasks")
		}
		if domain.HasCharacterWorkContinuationPolicyV1(t.observation.Sources) {
			properties["work_continuations"] = schema.Array("可选：本人明确授权有限work继续执行，until_target与max_effective_minutes二选一；任务需求不是默认授权", schema.Object(
				schema.Property("task_id", schema.String("本提案已有的有限work任务ID")).Required(),
				schema.Property("until_target", schema.Bool("true明确授权做到本人声明的有限总目标")),
				schema.Property("max_effective_minutes", schema.Number("从本提案开始的额外有效分钟授权上限，含本周期已执行部分")),
			))
		}
	}
	return result
}

func (t *SubmitCharacterDecisionTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.store == nil || t.proofs == nil {
		return nil, fmt.Errorf("character decision store is unavailable")
	}
	var input struct {
		Time                 string                                            `json:"time"`
		Location             string                                            `json:"location"`
		CurrentGoal          string                                            `json:"current_goal"`
		Pressure             string                                            `json:"pressure"`
		Resources            []string                                          `json:"resources"`
		AvailableOptions     []string                                          `json:"available_options"`
		RejectedOptions      []string                                          `json:"rejected_options"`
		Decision             string                                            `json:"decision"`
		DecisionReason       string                                            `json:"decision_reason"`
		IntendedAction       string                                            `json:"intended_action"`
		ActionDuration       string                                            `json:"action_duration"`
		KnowledgeRefs        []string                                          `json:"knowledge_refs"`
		MechanismRefs        []string                                          `json:"mechanism_refs"`
		ResourceClaims       []string                                          `json:"resource_claims"`
		Constraints          []string                                          `json:"constraints"`
		Contingencies        []string                                          `json:"contingencies"`
		ExpectedConsequences []string                                          `json:"expected_consequences"`
		ResourceEstimates    []domain.ResourceEstimateV2                       `json:"resource_estimates"`
		ResourceMeasurements []domain.ResourceMeasurementV2                    `json:"resource_measurements"`
		ResourceReports      []domain.ResourceReportV2                         `json:"resource_reports"`
		Communications       []domain.CharacterCommunicationV2                 `json:"communications"`
		ResourceReads        []domain.ResourceReadRequestV2                    `json:"resource_reads"`
		SelfTasks            []domain.CharacterSelfTaskV2                      `json:"self_tasks"`
		WorkContinuations    []domain.CharacterWorkContinuationAuthorizationV1 `json:"work_continuations"`
		ArtifactReads        []domain.CharacterArtifactReadVersionV1           `json:"artifact_reads"`
		ArtifactSigns        []domain.CharacterArtifactSignIntentV1            `json:"artifact_signs"`
		ArtifactAccess       []domain.CharacterArtifactAccessIntentV1          `json:"artifact_access"`
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
		MechanismRefs:        input.MechanismRefs,
		ResourceClaims:       input.ResourceClaims,
		Constraints:          input.Constraints,
		Contingencies:        input.Contingencies,
		ExpectedConsequences: input.ExpectedConsequences,
		ResourceEstimates:    input.ResourceEstimates,
		ResourceMeasurements: input.ResourceMeasurements,
		ResourceReports:      input.ResourceReports,
		Communications:       input.Communications,
		ResourceReads:        input.ResourceReads,
		SelfTasks:            input.SelfTasks,
		WorkContinuations:    input.WorkContinuations,
		ArtifactReads:        input.ArtifactReads,
		ArtifactSigns:        input.ArtifactSigns,
		ArtifactAccess:       input.ArtifactAccess,
		SubmittedAt:          time.Now().UTC().Format(time.RFC3339Nano),
	}
	finalized, err := domain.FinalizeCharacterDecisionProposal(proposal, t.observation)
	if err != nil {
		return nil, fmt.Errorf("character decision rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	if t.arbitrationV3 != nil {
		if err := domain.ValidateCharacterOperationalObservationSourcesV1(finalized, t.arbitrationV3.Input().Stimulus); err != nil {
			return nil, fmt.Errorf("character decision rejected: %w: %w", err, errs.ErrToolPrecondition)
		}
	}
	save := func(value domain.CharacterDecisionProposal) error { return t.proofs.SaveProposal(value, t.observation) }
	if t.arbitrationV3 != nil {
		save = t.arbitrationV3.SaveProposal
	}
	if err := save(finalized); err != nil {
		// A retry gets a new host timestamp even when the model submits the
		// same decision. Reuse only a fully verified, semantically identical
		// stored proposal, preserving its original digest and submission time.
		existing, loadErr := t.proofs.LoadProposal(finalized.GenerationID, finalized.Chapter, finalized.Round, finalized.AgentID)
		if loadErr != nil {
			return nil, loadErr
		}
		if existing == nil {
			return nil, err
		}
		finalized.SubmittedAt = existing.SubmittedAt
		retry, finalizeErr := domain.FinalizeCharacterDecisionProposal(finalized, t.observation)
		if finalizeErr != nil || retry.Digest != existing.Digest {
			return nil, err
		}
		finalized = *existing
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
	cycleProofs       *store.CharacterAgentStore
	continuationProof *store.CharacterContinuationArbitration
	arbitrationV3     *store.CharacterArbitrationV3
	roundSourcesV3    *domain.VerifiedCharacterArbitrationSourcesV1
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
	return "裁决当前绑定的全部独立角色提案。decision 和 intended_action 必须逐字复制提案；依据 operational_world、mechanisms 和 counterfactual_tests 决定顺序、可行性、完成度、结果和蝴蝶效应。存在未解决冲突时返回最小反馈，最终轮仍未解决会拒绝。"
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
		schema.Property("mechanism_refs", schema.Array("实际用于裁决的 world_stimulus.mechanisms id，包含角色未知的隐秘机制", schema.String("mechanism id"))),
		schema.Property("visible_to_pov", schema.Bool("POV 是否直接可见")),
		schema.Property("butterfly_effects", schema.Array("下游影响，至少一个", effect)).Required(),
		schema.Property("conflict_ids", schema.Array("关联冲突 id", schema.String(""))),
	)
	if t.stimulus.Version == domain.WorldStimulusPacketV2Version {
		selfPolicy := domain.HasCharacterSelfExperiencePolicyV2(t.stimulus.Sources)
		resolution["properties"].(map[string]any)["post_state"] = characterPhysicalPostStateSchema(selfPolicy, domain.HasCharacterResourceObservationTimePolicyV1(t.stimulus.Sources))
		resolution["required"] = append(resolution["required"].([]string), "post_state")
		if selfPolicy {
			resolution["properties"].(map[string]any)["self_executions"] = characterSelfExecutionsSchema(domain.HasCharacterOperationalAvailabilityPolicyV1(t.stimulus.Sources))
			if domain.HasCharacterSurfaceInspectionPolicyV1(t.stimulus.Sources) {
				executions := resolution["properties"].(map[string]any)["self_executions"].(map[string]any)
				executions["items"].(map[string]any)["properties"].(map[string]any)["observation_results"] = characterSurfaceInspectionResultsSchemaV1()
			}
			if domain.HasCharacterWorkArtifactPolicyV1(t.stimulus.Sources) {
				properties := resolution["properties"].(map[string]any)
				executions := properties["self_executions"].(map[string]any)
				executions["items"].(map[string]any)["properties"].(map[string]any)["output_results"] = characterWorkOutputResultsSchema()
				properties["artifact_read_results"] = characterArtifactReadsSchema(true)
				properties["artifact_signatures"] = characterArtifactSignsSchema(true)
			}
			resolution["required"] = append(resolution["required"].([]string), "self_executions")
		}
	}
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
	storyTime := schema.Property("story_time", schema.Object(
		schema.Property("chapter", schema.Int("必须等于当前绑定章节")).Required(),
		schema.Property("start_day", schema.Number("实际开始的故事日，必须等于 world_stimulus.story_clock.current_day；禁止按平均章速估算")).Required(),
		schema.Property("end_day", schema.Number("裁决后的实际结束故事日，可为分钟/秒对应的小数；不得早于 start_day 或超出 duration_days_max")).Required(),
	))
	if t.stimulus.StoryClock != nil || domain.HasCharacterSelfExperiencePolicyV2(t.stimulus.Sources) || domain.HasCharacterPassiveReceptionPolicyV2(t.stimulus.Sources) {
		storyTime = storyTime.Required()
	}
	result := schema.Object(
		schema.Property("time_window", schema.String("本章现实时间窗口")).Required(),
		storyTime,
		schema.Property("resolutions", schema.Array("每个活跃角色恰好一条裁决", resolution)).Required(),
		schema.Property("conflicts", schema.Array("检测到的冲突", conflict)),
		schema.Property("hard_contract_status", schema.Enum("角色意图与不可协商硬合同是否仍可共同实现", "feasible", "infeasible")).Required(),
		schema.Property("hard_contract_conflicts", schema.Array("不可实现时逐条引用冲突的硬合同；feasible 时为空", schema.String(""))),
		schema.Property("protagonist_projection", projection).Required(),
		schema.Property("finalized", schema.Bool("无未解决冲突时传 true")).Required(),
	)
	if t.stimulus.Version == domain.WorldStimulusPacketV2Version {
		result["properties"].(map[string]any)["resource_settlements"] = schema.Array("全世界按resource_id结算一次；共享引用不重复扣款；未变化资源可省略，未知前量不能算出精确后量", schema.Object(
			schema.Property("resource_id", schema.String("physical_state.resources的全局唯一id")).Required(),
			schema.Property("before", physicalNullableNumberSchema("必须等于宿主真实前量；未知为null")).Required(),
			schema.Property("delta", physicalNullableNumberSchema("实际净变化；已知before必须填写，after=before+delta；变化也未知时null")).Required(),
			schema.Property("after", physicalNullableNumberSchema("真实后量，非负；未知before时仍为null")).Required(),
			schema.Property("evidence_refs", schema.Array("实际行为/机制来源，不能用StateAfter文本作数量来源", schema.String(""))).Required(),
		))
		result["required"] = append(result["required"].([]string), "resource_settlements")
		if domain.HasCharacterResourceObservationTimePolicyV1(t.stimulus.Sources) {
			settlements := result["properties"].(map[string]any)["resource_settlements"].(map[string]any)
			settlements["description"] = "每个resource_id至多一条实际变化区间；非零变化必须给start_day/end_day，未变资源可省略。共享资源只结算一次；正区间内部不插值猜测读数，起止端点分别对应before/after；瞬时变化的该时点取after"
			fields := settlements["items"].(map[string]any)["properties"].(map[string]any)
			fields["start_day"] = schema.Number("实际资源变化开始时点，非零变化必填；须在story_time内且不晚于end_day，相同表示瞬时变化，不把测量前后耗用混为同时发生")
			fields["end_day"] = schema.Number("实际资源变化结束时点，非零变化必填；须在story_time内，after只代表此时及其后尚未再变化的余额")
			fields["before"] = physicalNullableNumberSchema("实际变化区间起点的真实余额，须等于宿主前量；未知为null")
			fields["after"] = physicalNullableNumberSchema("实际变化区间终点的真实余额，after=before+delta；不得拿此值替代较早测量时点的读数")
		}
	}
	if t.stimulus.Version == domain.WorldStimulusPacketV2Version {
		result["properties"].(map[string]any)["resource_deliveries"] = characterResourceDeliveriesSchema()
		if domain.HasCharacterIncomingMaterialReadPolicyV1(t.stimulus.Sources) {
			result["properties"].(map[string]any)["resource_deliveries"] = characterIncomingMaterialDeliveriesSchemaV1()
		}
		if domain.HasCharacterWorkArtifactPolicyV1(t.stimulus.Sources) {
			deliveries := result["properties"].(map[string]any)["resource_deliveries"].(map[string]any)
			deliveries["items"].(map[string]any)["properties"].(map[string]any)["artifact_version_digest"] = schema.String("产物出示/交付必填发送者artifact_access明确授权的正文版本；普通资源省略")
		}
		if domain.HasCharacterPassiveReceptionPolicyV2(t.stimulus.Sources) {
			result["properties"].(map[string]any)["passive_receptions"] = characterPassiveReceptionsSchema()
		}
	}
	return t.characterActivationArbitrationSchema(result)
}

func (t *ResolveChapterWorldTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.store == nil || len(t.proposals) == 0 {
		return nil, fmt.Errorf("world arbiter has no bound proposals")
	}
	if t.cycleProofs == nil {
		for _, source := range t.stimulus.Sources {
			if strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
				return nil, fmt.Errorf("activation cycle requires its host-bound arbitration tool; cannot publish a single-cycle chapter")
			}
		}
	}
	var input struct {
		TimeWindow            string                               `json:"time_window"`
		StoryTime             *arbitrationStoryTimeInput           `json:"story_time"`
		Resolutions           []arbitrationResolutionInput         `json:"resolutions"`
		Conflicts             []domain.WorldArbitrationConflict    `json:"conflicts"`
		HardContractStatus    string                               `json:"hard_contract_status"`
		HardContractConflicts []string                             `json:"hard_contract_conflicts"`
		ProtagonistProjection domain.ProtagonistDecisionProjection `json:"protagonist_projection"`
		Finalized             bool                                 `json:"finalized"`
		ResourceSettlements   []domain.ResourceSettlementV2        `json:"resource_settlements"`
		ResourceDeliveries    []domain.ResourceDeliveryV2          `json:"resource_deliveries"`
		PassiveReceptions     []domain.CharacterPassiveReceptionV2 `json:"passive_receptions"`
	}
	if err := unmarshalToolArgs(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	storyTime, err := input.StoryTime.schedule()
	if err != nil {
		return nil, fmt.Errorf("world arbitration rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	resolutions, err := normalizeArbitrationResolutions(input.Resolutions, t.stimulus, t.proposals)
	if err != nil {
		return nil, fmt.Errorf("world arbitration post-state rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	round := t.proposals[0].Round
	proposalDigests := make([]string, 0, len(t.proposals))
	for _, proposal := range t.proposals {
		if proposal.Round > round {
			round = proposal.Round
		}
		proposalDigests = append(proposalDigests, proposal.Digest)
	}
	if t.roundSourcesV3 != nil {
		// A continued P2 can belong to a previous cycle. Its original Round
		// must not become this cycle's arbitration round.
		round = t.roundSourcesV3.CurrentRound()
	}
	receipt := domain.WorldArbitrationReceipt{
		Version:               domain.WorldArbitrationReceiptVersion,
		GenerationID:          t.stimulus.GenerationID,
		Chapter:               t.stimulus.Chapter,
		Round:                 round,
		StoryTime:             storyTime,
		StimulusDigest:        t.stimulus.Digest,
		ActivationDigest:      t.activation.Digest,
		ProposalDigests:       proposalDigests,
		Resolutions:           resolutions,
		Conflicts:             input.Conflicts,
		HardContractStatus:    input.HardContractStatus,
		HardContractConflicts: input.HardContractConflicts,
		ProtagonistProjection: input.ProtagonistProjection,
		Finalized:             input.Finalized,
		GeneratedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		ResourceSettlements:   input.ResourceSettlements,
		ResourceDeliveries:    input.ResourceDeliveries,
		PassiveReceptions:     input.PassiveReceptions,
	}
	if t.stimulus.Version == domain.WorldStimulusPacketV2Version {
		receipt.Version = domain.WorldArbitrationReceiptV2Version
	}
	receipt = t.normalizeContinuationSettlementEvidence(receipt)
	if err := domain.PrecheckWorldArbitrationStaticReferencesV2(receipt, t.stimulus, t.proposals); err != nil {
		return nil, fmt.Errorf("world arbitration rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	finalizedReceipt, err := t.finalizeBoundArbitration(receipt)
	if err != nil {
		return nil, fmt.Errorf("world arbitration rejected: %w: %w", err, errs.ErrToolPrecondition)
	}
	if t.cycleProofs != nil {
		return t.persistActivationArbitration(finalizedReceipt)
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

	var physicalStates []domain.WorldPhysicalStateV2
	if finalizedReceipt.Version == domain.WorldArbitrationReceiptV2Version {
		physical, physicalErr := domain.ApplyArbitrationPhysicalStateV2(finalizedReceipt, t.stimulus, t.proposals...)
		if physicalErr != nil {
			return nil, physicalErr
		}
		physicalStates = append(physicalStates, physical)
	}
	decisions, err := finalizedReceipt.CharacterDecisions(t.proposals, physicalStates...)
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
		StoryTime:             finalizedReceipt.StoryTime,
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
	if finalizedReceipt.Version == domain.WorldArbitrationReceiptV2Version {
		simulation.PhysicalState = &physicalStates[0]
		simulation.CharacterAgentProtocol.Version = domain.CharacterAgentDecisionProtocolV2Version
	}
	if simulation.TimeWindow == "" || t.stimulus.StoryClock != nil {
		if simulation.StoryTime != nil {
			simulation.TimeWindow = fmt.Sprintf("故事开始后 %.12g–%.12g 分钟", simulation.StoryTime.StartDay*1440, simulation.StoryTime.EndDay*1440)
		} else {
			simulation.TimeWindow = t.stimulus.TimeWindow
		}
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
	if simulation.CharacterActivation != nil {
		_, err := loadCurrentCharacterActivationEvidence(st, simulation)
		return err
	}
	if len(simulation.CharacterDecisionTrace) > 0 {
		return fmt.Errorf("unbound chapter activation decision trace")
	}
	if simulation.Version < 2 || simulation.CharacterAgentProtocol == nil {
		return fmt.Errorf("missing character-agent protocol receipt")
	}
	receipt := simulation.CharacterAgentProtocol
	if (receipt.Version != domain.CharacterAgentDecisionProtocolVersion && receipt.Version != domain.CharacterAgentDecisionProtocolV2Version) || receipt.RegistryRoot == "" || receipt.StimulusDigest == "" || receipt.ActivationDigest == "" || receipt.ArbitrationRound <= 0 || receipt.ArbitrationDigest == "" || receipt.ProtocolDigest == "" {
		return fmt.Errorf("character-agent protocol receipt is incomplete")
	}
	registry, err := st.CharacterAgents.LoadRegistrySnapshot(simulation.GenerationID, simulation.Chapter)
	if err == nil && registry == nil {
		if promoted, promotionErr := validatePromotedCharacterSimulation(st, simulation); promotionErr != nil {
			return promotionErr
		} else if promoted {
			return nil
		}
	}
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
	if !domain.SameStoryTime(simulation.StoryTime, arbitration.StoryTime) {
		return fmt.Errorf("simulation story_time differs from arbitration")
	}
	var physicalStates []domain.WorldPhysicalStateV2
	if receipt.Version == domain.CharacterAgentDecisionProtocolV2Version {
		if stimulus.Version != domain.WorldStimulusPacketV2Version || arbitration.Version != domain.WorldArbitrationReceiptV2Version || simulation.PhysicalState == nil {
			return fmt.Errorf("v2 simulation lacks its bound physical-state protocol")
		}
		physical, err := domain.ApplyArbitrationPhysicalStateV2(*arbitration, *stimulus, proposals...)
		if err != nil {
			return err
		}
		left, _ := json.Marshal(physical)
		right, _ := json.Marshal(simulation.PhysicalState)
		if string(left) != string(right) {
			return fmt.Errorf("simulation physical state differs from arbitration")
		}
		physicalStates = append(physicalStates, physical)
	} else if simulation.PhysicalState != nil || stimulus.Version == domain.WorldStimulusPacketV2Version || arbitration.Version == domain.WorldArbitrationReceiptV2Version {
		return fmt.Errorf("v1 simulation cannot mix v2 physical-state artifacts")
	}
	if err := domain.ValidateStoryTimeForClock(simulation.Chapter, simulation.StoryTime, stimulus.StoryClock); err != nil {
		return fmt.Errorf("simulation story_time: %w", err)
	}
	decisions, err := arbitration.CharacterDecisions(proposals, physicalStates...)
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
