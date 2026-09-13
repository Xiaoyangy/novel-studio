package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const characterIncomingMaterialReadPromptV1 = `
条件来件阅读：若你自主选择在指定角色本轮实际送来材料且获准阅读后当场读，可在resource_reads填写incoming_delivery_from和本人实际阅读work的task_id；不需要猜未见材料的resource_id、文书类型或版本。该授权仅适用于本轮由指定人实际交来的唯一材料；没有来件或来件不唯一时不得假定读过。已知特定版本的阅读仍用artifact_reads。现场出示不等于交出保管权，实际阅读不等于签认或独立证实。若你选择向别人说明自己已知的材料名称，须在resource_reports明确传达本人可见名称；仅说“出示”不自动授予名称或内容。`

const worldArbiterIncomingMaterialReadPromptV1 = `
条件来件阅读裁决：原resource_reads含incoming_delivery_from及本人work.task_id时，若本轮确实从指定人向本人送达唯一且已获访问的材料，才可按真实材料类型处理。前态已存在、发送者已知并明确授权的版本化文书使用artifact_read_results；宿主绑定实际交付的resource_id/version_digest和原阅读任务，不要求角色在见到来件前猜版本。该路径不涵盖本轮才新建或修改的版本，不经unversioned received_facts泄露正文。resource_deliveries必须给真实delivered_at_day，核对授权、交付执行与位置，接收者实际阅读开始不得早于交付；不能用等待或无关工作充当阅读。无来件、多份匹配、未授权、时空不成立时不产生读结果，也不替角色选择一份或补提案。名称/单位/数量仍只来自原resource_reports实际发送字段，缺报告填received_fields=[]；机制ID不是交付证据。`

func characterActivationV3IncomingReadPolicies() []string {
	return append(characterActivationV3Policies(), domain.CharacterIncomingMaterialReadPolicyV1)
}

func characterActivationProtocolV3IncomingReadDigest() string {
	policies := append(characterActivationV3IncomingReadPolicies(), domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterPassiveReceptionPolicyV2)
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
	token, _ := domain.CharacterActivationCycleSourceToken("pg2_incoming_read_schema", 1, 1, "sha256:0000000000000000000000000000000000000000000000000000000000000000", "")
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: append(policies, token)}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	digest, err := domain.DeterministicPlanningHash(struct {
		Base, Policy, CharacterPrompt, ArbiterPrompt string
		Submit, Resolve                              map[string]any
	}{characterActivationProtocolV3Digest(), domain.CharacterIncomingMaterialReadPolicyV1, characterIncomingMaterialReadPromptV1, worldArbiterIncomingMaterialReadPromptV1, submit.Schema(), resolve.Schema()})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}
