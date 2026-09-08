package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const characterWorkArtifactPromptV1 = `
当前时间以本次观察的时钟为准，工作完成度以实际self_experiences/task_progress及本人已知结果为准。current_goal和pressure保留开局或上次本人自述，其中旧的“还需几分钟”等估计不是本轮重新计时，也不能覆盖已经完成的实际工作；有新证据时由你独立调整决定。
实际产物协议：如果你需要写成或修改可供后来定位/读取的材料，必须在本人work任务中明确output_requests；仅在intended_action、通信或自由描述里说“写成了”不会产生文书实体。首次创建须声明实际材料投入；修改已知产物须绑定resource_id及expected_version_digest，不重复分配材料。内容声明只能来自本人已有事实/已知版本与本人明确的新陈述，不把本人陈述当独立核验。产物ID/版本由宿主生成，不自行猜新ID；后续通过本人artifact_views引用实际已知产物版本。source_refs、已有版本及既有claim_ids是引用，可用对应局部句柄；output_key和output_requests内新claim_id则是本人新声明的普通本地名字，不用@ref句柄。
阅读用artifact_reads，独立签认用artifact_signs；两者都须绑定本人self_tasks中实际work任务的task_id，任务resource_ids须包含该产物。unread版本摘要仅允许请求阅读，不代表已知正文；读取意图claim_ids可为空以请求整个授权版本。签认必须针对本人此前已经实际读到或亲自写入、且status=complete的已完成版本与有限声明范围，写正文不自动生成签名。向别人授权出示/交付须明确artifact_access和接收者/版本/权限，仍由世界裁判确认是否实际发生；别人获得访问权不等于读过。产物随本人移动须有明确carry，放下须有place，不预设交付、阅读或签认成功。`

const worldArbiterWorkArtifactPromptV1 = `
实际产物裁决：只能对原提案已有的output_requests，以对应实际self_executions.output_results裁决本段真实形成的正文产物及版本；这不包含独立签名。output_results的output_key和claim_ids是原请求里声明的本地名字，逐字复制，不改成运输句柄；created/updated的at_day必须等于对应本人实际self_execution.end_day，blocked须在本轮裁决窗口内。没有原请求、尚未执行、条件未满足或不具备实际材料/机制时，不得因自由文本说“写成”就补造对象；按真实条件阻断或不产出。宿主从原请求与实际执行来源生成稳定ID、已知版本和持有位置，并统一结算本轮材料；原已分配材料在继续写作时不重复扣除。
实际阅读只用artifact_read_results，独立签认只用artifact_signatures，分别绑定原artifact_reads/artifact_signs的版本、声明范围及对应work任务实际执行区间；不能用等待或无关工作代替。结果at_day必须是真实发生时刻，不因另一角色工作更久就延后；实际读取须列出非空的真实声明ID。文书不得通过旧resource_read received_facts绕过版本阅读；交付须匹配持有人原artifact_access，并在resource_deliveries绑定artifact_version_digest，交付不自动产生阅读结果。签名身份与摘要由宿主派生，不能替角色改签认范围；修改正文不使旧签名替新版背书。不能通过post_state任意新增世界对象、正文内容或别人知识。报告/请求/写成/交付/读取/独立核验必须分别有自己的实际证据。`

const worldArbiterRoundsPromptV3 = `
本轮坐标以current_arbitration为准，不取proposals里的最大round：续行原提案可能来自较早cycle的R2，其原round/digest/观察不得更改。intent_sources说明每个实际owner当前是fresh还是continuation，continuations只列本轮仍获授权者。R1不可行且需要修订时，保留原选择、给出受影响owner的冲突类型；非final或hard回执的story_time必须零推进、post_state保持原态，不提交self_executions/resource_settlements/deliveries/知识变化。R2只替换受影响owner的新P2，未受影响者原选择继续有效；最终所有owner在同一真实时段内共同裁决、世界资源只结算一次。硬合同不可能时如实infeasible而不伪造执行。角色只会得到宿主生成的本人约束类别，不会看到你的私有Feedback全文；请用准确conflict.kind标识问题。
陈述与核验边界：知情角色可以根据本人已有知识、目标和压力，自主选择合法陈述、承认本人经手事实、否认或保留信息；裁判不得仅因其透露的内容涉及作者秘密就阻止原提案中的真实通信。实际送达只让听者获得有来源的reported/待核陈述，不自动使陈述内容成为世界真相、独立证据或已核实结论；仍须按原通信及真实接收条件分别裁决，不能附带授予未读文书内容、访问权或未测数量。“不可靠突然自白解题”“证据取得前不得投放答案”等证据与知识要求，限制的是用陈述补齐核验结论或由作者越权灌入答案，不授权裁判禁止知情者说话、要求其改口或为叙事节奏强改意图。仍须拒绝角色使用本人未知的秘密、虚构接收、越过实际位置/时间/权限或其他真实物理与安全条件的结果。
硬不可能的证明边界：hard_contract_status=infeasible必须指出真实硬合同，以及在保留角色原选择后，哪项已成立的结果、资源、权限或期限冲突使该硬合同的可行履行路径被排除。当前提案尚未核验、一次行动受阻、软大纲的取证顺序或预定桥段落空，不等于当前弧硬合同已不可实现；不得仅因角色合法说话或不符合软情节就上报硬不可能。保留合法陈述与真实后果，让后续实际核验和规划处理；真实硬冲突仍须如实拒绝，不能为了继续生产伪造事实或放宽门禁。`

const characterResourceObservationTimePromptV1 = `
数值测量必须是本人明确的实际工作：resource_measurements.task_id绑定本人self_tasks中包含该resource_id的work任务。测量与随后耗用可以是不同任务；申请测量不等于测量成功，完成测量后才获得读数。后来耗用资源不会自动刷新本人旧读数或观察时刻。`

const worldArbiterResourceObservationTimePromptV1 = `
只提交变化：post_state必须给实际location；资源与感知未变化时省略resources/resource_updates，宿主继承原态。世界扣耗不是角色新知识，不要重填旧感知、刷新其章龄或把机制ID当测量证据。
数值测量按真实时刻裁决：新last_observed必须来自原resource_measurements绑定的work实际执行，observed_at_day等于该测量任务真实end_day，evidence_refs引用本人原proposal_digest。resource_settlements的start_day/end_day表示该项数量实际变化区间，每资源本轮仍只结算一次。只能从明确区间端点求精确读数，不能在区间内部猜测或线性插值。例如先测得4，随后耗光至0，本人上次读数仍是4，不是期末0。实际未测得则保留旧感知，不编造测量或为了校验补造任务。`

func hasCharacterActivationPolicyV3(sources []string) bool {
	for _, s := range sources {
		if s == domain.CharacterActivationCyclePolicyV3 {
			return true
		}
	}
	return false
}

func characterActivationV3Policies() []string {
	return append(characterActivationV3HistoryPolicies(), domain.CharacterSelfCompletionViewPolicyV1)
}

func characterActivationV3HistoryPolicies() []string {
	return append(characterActivationV3LegacyPolicies(), domain.CharacterWorkContinuationHistoryPolicyV1)
}

func characterActivationV3LegacyPolicies() []string {
	return []string{domain.CharacterActivationCyclePolicyV3, domain.CharacterArbitrationRoundSourcesPolicyV1,
		domain.CharacterWorkArtifactPolicyV1, domain.CharacterRevisionFeedbackPolicyV1,
		domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterResourceObservationTimePolicyV1}
}

func characterActivationProtocolV3Digest() string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, CompletionView, CompletionPrompt string }{characterActivationProtocolV3HistoryDigest(), domain.CharacterSelfCompletionViewPolicyV1, characterSelfCompletionViewPromptV1})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

// This exact producer remains executable for frozen b0513d-era generations.
func characterActivationProtocolV3HistoryDigest() string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, History, CarryAPIHelp string }{characterActivationProtocolV3LegacyDigest(), domain.CharacterWorkContinuationHistoryPolicyV1, characterCarryAPIHelpV1})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

// Keep the immediately preceding producer executable for its frozen sources.
func characterActivationProtocolV3LegacyDigest() string {
	policies := append(characterActivationV3LegacyPolicies(), domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterPassiveReceptionPolicyV2)
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
	token, _ := domain.CharacterActivationCycleSourceToken("pg2_v3_schema", 1, 1, "sha256:0000000000000000000000000000000000000000000000000000000000000000", "")
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: append(policies, token)}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	digest, err := domain.DeterministicPlanningHash(struct {
		Base, Policy, Cycle, Evidence, Ledger, SourceRounds, Artifacts, Feedback, ReferenceView, CharacterArtifacts, ArbiterArtifacts, ArbiterRounds string
		CharacterResourceTime, ArbiterResourceTime                                                                                                   string
		Submit, Resolve                                                                                                                              map[string]any
	}{characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV2), domain.CharacterActivationCyclePolicyV3, domain.CharacterActivationCycleV3Version, domain.CharacterActivationRoundEvidenceV3Version, domain.CharacterWorkContinuationLedgerV2Version, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterWorkArtifactPolicyV1, domain.CharacterRevisionFeedbackPolicyV1, modelinput.ScopedArtifactReferenceViewPolicyV1, characterWorkArtifactPromptV1, worldArbiterWorkArtifactPromptV1, worldArbiterRoundsPromptV3, characterResourceObservationTimePromptV1, worldArbiterResourceObservationTimePromptV1, submit.Schema(), resolve.Schema()})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

func activationPlanningPolicyV3Digest(legacy string) string {
	return activationPlanningPolicyV3ProducerDigest(legacy, characterActivationProtocolV3Digest())
}

func activationPlanningPolicyV3ProducerDigest(legacy, producer string) string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, Policy, Execution string }{legacy, domain.CharacterActivationCyclePolicyV3, producer})
	if err != nil {
		return ""
	}
	return digest
}
