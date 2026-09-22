package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
)

const characterHostLocationMetadataPromptV1 = `
Host地点定位元数据以本观察包私有的@loc_句柄表示；它只是位置同一性的引用，不是地点名称，也不表示你永远不知道地点。submit_character_decision.location原样使用顶层location的当前句柄，Host在原有起点校验前精确还原；行动、通信和文档自然语言不能写运输句柄，使用本人确已知道的名称或中性描述。本人实际收到的通信、实际读到的文书及自主意图原文仍有效，不因Host定位名隐藏而删除；收到一项说法不自动证明它为世界真相。此策略不要求你询问、等待、告知他人或采取任何特定行动。`

func characterActivationV3HostLocationPolicies() []string {
	return append(characterActivationV3MemoryTextPolicies(), domain.CharacterHostLocationMetadataPolicyV1)
}

func characterActivationProtocolV3HostLocationDigest() string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, Policy, Encoding, Prompt string }{
		characterActivationProtocolV3MemoryTextDigest(), domain.CharacterHostLocationMetadataPolicyV1,
		modelinput.ScopedLocationMetadataViewPolicyV1, characterHostLocationMetadataPromptV1,
	})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}
