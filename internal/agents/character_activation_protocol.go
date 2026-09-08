package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const projectAllActivationPlannerBoundary = `
当前是完整章内多周期结果。必须按character_decision_trace消费所有已经裁决的事件，保持每次原始选择、意图与时间顺序；兼容character_decisions及protagonist_projection.chosen_decision只代表最近一次选择，不能覆盖早期选择。final_physical_state是最终状态，不授予早期角色尚未获得的信息。raw输入/记忆及完整self历史不会重复展示，宿主仍持有并验证完整源；不得将未展示等同未发生。不要续推世界或替角色新增实质行动；材料不足应由章内执行/就绪流程解决，Planner只组织已发生的真实内容。`

const worldArbiterPassiveReceptionPromptV2 = `
章内被动接收协议：休眠角色本轮没有提案或resolution，不得虚构其选择、回应或移动。若发送者的某条原始communications确实送达该角色，使用passive_receptions引用原通信及准确接收者、送达时间和通道；宿主按原文入账，下一周期才让接收者独立决定。发送意图/条件未满足/预计传递不等于送达；无实际送达就不填。当面传递必须符合双方实际位置，跨地点中途抵达未有可验证位置时应在实际抵达时点收束周期。远程传递必须使用既有、发送者明确拟用且裁决实际使用的世界机制，遵守它的前置、时延和成本；不能凭空创造通信渠道。此回执只授予收到的通信内容，不授予文档读取、资源权限或对报告内容的真实性确认。`

const worldArbiterActivationProjectionPromptV1 = `
章内单周期 POV 边界：本轮只裁决 active 角色真实提交的提案，应省略 protagonist_projection。全书/本章主角可以休眠，不能为填写投影而要求其提交提案、虚构其选择或把活跃配角改称主角。完整周期链结束后，由宿主使用冻结的章 POV 和该角色在较早或当前周期的实际选择汇总整章投影；本轮被动收到信息不等于本轮作出选择。若仍提供非空投影，必须完整绑定本轮确有的提案，不能借用前轮提案冒充本轮选择。`

const characterOperationalAvailabilityPromptV1 = `
局部操作性观察：若你选择实际检查已知、有访问权、无计量数量的资源对某个具体用途是否可用，可在work任务声明observation_requests，并明确拟用的既有公开机制。不要把数量、文档内容或整项检查验收塞入这种请求；测量和读取仍使用各自专用协议。本人operational_observations是已执行请求在注明时刻的局部观察，可引用其id，不是当前持续可用承诺、容量或整体任务完成证明；purpose只是你当时的检查意图，其中的假设不自动成立。`

const worldArbiterOperationalAvailabilityPromptV1 = `
局部操作性裁决：对已实际执行的self_tasks.observation_requests，在对应self_executions.observation_results逐项返回available/unavailable/inconclusive和实际观察时刻，并在该resolution列出实际使用的mechanism_ref；未执行不得产出观察。结果仅限角色对该非计量资源和具体用途在当时的局部观察，必须满足实际位置、世界机制的条件、耗时及成本；证据不足填inconclusive。禁止据此确认数量/文档内容/隐藏事实/整项检查合格，禁止修改访问权或将结果写进任意post_state字段。宿主从原任务和执行时间确定性派生本人记录，后续周期才能读取。`

// The cycle protocol is separate from historical one-shot v2, whose digest
// must not change merely because the new opt-in execution path gains a tool.
func characterActivationProtocolDigest() string {
	// A schema-only content-addressed token selects the same cycle schema as
	// runtime. Its value is not published as evidence or used to authorize IO.
	token, err := domain.CharacterActivationCycleSourceToken("pg2_cycle_schema", 1, 1, "sha256:0000000000000000000000000000000000000000000000000000000000000000", "")
	if err != nil {
		return ""
	}
	policies := []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterPassiveReceptionPolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, token}
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: policies}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
	digest, err := domain.DeterministicPlanningHash(struct {
		Base, Cycle, Inputs, Observation, Passive, Memory, Prompt, Projection string
		Operations, CharacterOperations, ArbiterOperations                    string
		Resolve, Submit                                                       map[string]any
	}{CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolV2Version), domain.CharacterActivationCyclePolicy, domain.CharacterActivationInputSetVersion, domain.CharacterObservationCyclePolicy, domain.CharacterPassiveReceptionPolicyV2, "cycle-memory.private-outcome+passive-sent.v1", worldArbiterPassiveReceptionPromptV2, worldArbiterActivationProjectionPromptV1, domain.CharacterOperationalAvailabilityPolicyV1, characterOperationalAvailabilityPromptV1, worldArbiterOperationalAvailabilityPromptV1, resolve.Schema(), submit.Schema()})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

// New-generation identity includes cycle semantics and bounds. Legacy
// single-cycle planners retain their exact historical digest.
func ProjectAllPlanningProtocolWithActivation(plannerPrompt, protocol string, maxCycles int, policies ...string) string {
	policy := domain.CharacterActivationCyclePolicy
	if len(policies) > 1 {
		return ""
	}
	if len(policies) == 1 && policies[0] != "" {
		policy = policies[0]
	}
	if !domain.IsCharacterActivationPolicy(policy) {
		return ""
	}
	return ProjectAllPlanningProtocolWithActivationProducer(plannerPrompt, protocol, maxCycles, policy, "")
}

// producer is a host-only choice recovered from an exact generation identity.
func ProjectAllPlanningProtocolWithActivationProducer(plannerPrompt, protocol string, maxCycles int, policy, producer string) string {
	if policy == "" {
		policy = domain.CharacterActivationCyclePolicy
	}
	if !domain.IsCharacterActivationPolicy(policy) {
		return ""
	}
	selected := CharacterActivationProtocolWithProducer(policy, producer)
	if selected == "" {
		return ""
	}
	base := ProjectAllPlanningProtocolDigest(plannerPrompt, protocol)
	if protocol != domain.CharacterAgentDecisionProtocolV2Version || maxCycles <= 1 {
		return base
	}
	tool := &submitCharacterReadinessTool{}
	digest, err := domain.DeterministicPlanningHash(struct {
		Base, Activation, Readiness, Grounding, PlannerView, PlannerBoundary, Successor string
		ReadinessSchema                                                                 map[string]any
		MaxCycles                                                                       int
	}{base, characterActivationProtocolDigest(), characterReadinessPrompt, activationGroundingPrompt, domain.CharacterActivationPlannerViewPolicy, projectAllActivationPlannerBoundary, characterAgentSuccessorArchitectPrompt + ";readiness-bound-successor.v1", tool.Schema(), maxCycles})
	if err != nil {
		return ""
	}
	if policy != domain.CharacterActivationCyclePolicy {
		// Canonical evidence stays complete. These independently versioned
		// policies bind how a new generation displays and evaluates that data;
		// the absent/v1 branch above retains its exact historical identity.
		if policy == domain.CharacterActivationCyclePolicyV3 {
			return activationPlanningPolicyV3ProducerDigest(activationPlanningPolicyV2Digest(digest), selected)
		}
		return activationPlanningPolicyV2Digest(digest)
	}
	return digest
}

func activationPlanningPolicyV2Digest(legacy string) string {
	digest, err := domain.DeterministicPlanningHash(struct {
		Base, Cycle, Activation, Chronology, Continuation, References, ReadinessView, ReadinessSchema, ReadinessPrompt, ReferencePrompt string
		GroupedSchema                                                                                                                   map[string]any
	}{legacy, domain.CharacterActivationCyclePolicyV2, characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV2), domain.CharacterSelfChronologyPolicyV1,
		domain.CharacterWorkContinuationPolicyV1, modelinput.ScopedReferenceViewPolicy,
		domain.CharacterReadinessModelViewPolicyV1, domain.CharacterReadinessGroupedSchemaPolicyV1, characterGroupedReadinessPrompt, scopedReferencePrompt,
		domain.CharacterReadinessGroupedVerdictSchemaV1()})
	if err != nil {
		return ""
	}
	return digest
}
