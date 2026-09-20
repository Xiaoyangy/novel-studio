package agents

import (
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const characterInitialSelfIntentPromptV1 = `
known_facts 中 initial_self_intent 是你在故事开局时本人的意图原文，作为历史自我记忆保留，不是必须完成的命令，也不证明任何外部事实或未来结果。current_goal 仍是你现在自主选择的目标；你可以根据本人实际经历与当前压力继续、修改或放弃原意图。不要把短期任务进度当成原意图必然已完成，也不必为了原意图默认别人合作。
resource_reads 两种参数不能混用：读取本人已知的普通既有资料，例如 {"resource_id":"本人当前可见的资料ID"}，不填 task_id 或 incoming_delivery_from；只有条件来件阅读才例如 {"incoming_delivery_from":"指定发送者实名","task_id":"本人原提案中实际阅读的work任务ID"}，此时不填 resource_id。后者引用的是接收者本人已声明的work，不是发送者的任务；没有真实来件或权限/工时不足不等于读过。已知版本化产物仍按原协议用 artifact_reads，不用普通资料读取绕过版本。`

func projectCharacterInitialSelfIntent(observation *domain.CharacterObservationPacket, profile characterAgentProfile) {
	if !domain.HasCharacterInitialSelfIntentPolicyV1(observation.Sources) || profile.Character.InitialState == nil {
		return
	}
	goal := strings.TrimSpace(profile.Character.InitialState.CurrentGoal)
	if goal == "" {
		return // Do not invent a historical intention from a task or outline.
	}
	observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("initial_self_intent", goal,
		"characters.json#"+profile.Record.AgentID+"/initial_state.current_goal", "private"))
}

func characterActivationV3InitialSelfIntentPolicies() []string {
	return append(characterActivationV3IncomingReadPolicies(), domain.CharacterInitialSelfIntentPolicyV1)
}

func characterActivationProtocolV3InitialSelfIntentDigest() string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, Policy, Prompt string }{
		characterActivationProtocolV3IncomingReadDigest(), domain.CharacterInitialSelfIntentPolicyV1, characterInitialSelfIntentPromptV1,
	})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}
