package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const characterCommunicationAddressingPromptV1 = `
定向通信不要求知道对方实名：communications中to_character、recipient_hint、reply_to_received_fact_id严格三选一。本人确知实名时可用原to_character；不知道实名但实际感知到特定对象时，用自己的称呼/描述recipient_hint并引用本人已有knowledge_refs，不猜作者实名。回复本人known_facts里已经收到的communication时，用该事实ID作reply_to_received_fact_id并同时列入knowledge_refs；不可用别人的事件、记忆摘要或尚未收到的未来回复。非实名conditional_response只允许回复前态已经收到的精确事件，condition_kind须匹配且condition_from_character留空；不能按Host隐藏实名匹配匿名来源，也不能为尚未收到的消息预作条件回复。收到说法不等于证实内容。新寻址不把私人意图变成广播、不保证听到/接听/回答；目标不明或不唯一可能未送达。你仍独立决定说什么、是否说、是否回答，不能为了工程检查自行预设介绍姓名或别人合作。`

const worldArbiterCommunicationAddressingPromptV1 = `
非实名定向通信：原recipient_hint是角色自己的可感知称呼，不是广播。只根据原通信knowledge_refs支持的本人感知和本轮实际场景解析唯一真实对象；不得仅凭作者知道谁在故事里就替角色选择未感知对象，不得将歧义称呼随意匹配一人。reply_to_received_fact_id只指本人前态实际收到的communication，其原FromAgentID是唯一回复对象；不能把回复改送给别的人。原提案的mode/hint/text/意图均不改写。
仅已实际送达的新模式通信填写communication_receptions，精确绑定原proposal/id、唯一接收者、送达故事日、实际in_person/mechanism通道和原knowledge_refs依据，recipient_resolution只能在确已唯一识别时写unique。未识别、不唯一、受阻或尚未送达时不填该送达行，可用原冲突反馈说明；不伪造接听、回复、同意或未来得知。活跃和休眠接收者均由Host从原text/kind派生收到事实，不在post_state.received_facts手填新模式收到文本。named通信保留原active/passive协议。发送/收到不代表知道对方规范姓名；本人实际自我介绍/姓名陈述原文保留，不附加作者姓名。原时间、实际位置、信道、机制、权限和资源条件一律继续校验。`

func characterActivationV3AddressingPolicies() []string {
	return append(characterActivationV3HostLocationPolicies(), domain.CharacterCommunicationAddressingPolicyV1)
}

func characterActivationProtocolV3AddressingDigest() string {
	policies := append(characterActivationV3AddressingPolicies(), domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterPassiveReceptionPolicyV2)
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: policies}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	hash, err := domain.DeterministicPlanningHash(struct {
		Base, Policy, References, ActorPrompt, ArbiterPrompt string
		Submit, Resolve                                      map[string]any
	}{characterActivationProtocolV3HostLocationDigest(), domain.CharacterCommunicationAddressingPolicyV1, modelinput.ScopedCommunicationReferenceViewPolicyV1, characterCommunicationAddressingPromptV1, worldArbiterCommunicationAddressingPromptV1, submit.Schema(), resolve.Schema()})
	if err != nil {
		return ""
	}
	return "sha256:" + hash
}
