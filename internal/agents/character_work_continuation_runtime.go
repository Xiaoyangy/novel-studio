package agents

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const worldArbiterContinuationPromptV2 = `
continuations是宿主从原始角色明确授权和实际执行账本派生的本轮续行凭据，不是本轮新选择。对应proposals保留原始意图和原始观察摘要，不得重写或伪造新提案。只裁决本轮当前时刻之后的实际work区间，既往区间已经发生，不重做、不重复扣资源。每个owner本轮有效工作不得超过其authorized_remaining；任务目标不是默认成功或设备整体合格，遇阻断应如实裁决blocked。授权只覆盖原先唯一的原地work，不增加移动、通信、测量、文档读取或观察请求；其他角色本轮新决定与续行共同使用同一个世界时钟和资源余额。`

func prepareCharacterWorkContinuationSelection(st *store.Store, inputs *characterAgentChapterInputs, frozen *domain.CharacterActivationInputSet, session domain.CharacterActivationSession) error {
	if !domain.HasCharacterSelfChronologyPolicyV1(inputs.Stimulus.Sources) || !domain.HasCharacterWorkContinuationPolicyV1(inputs.Stimulus.Sources) {
		return nil
	}
	if frozen == nil {
		return fmt.Errorf("continuation selection requires complete immutable current inputs")
	}
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
	if err != nil {
		return err
	}
	if prefix == nil || prefix.Session().Digest != session.Digest {
		return fmt.Errorf("continuation selection lacks its exact verified execution prefix")
	}
	ledgers := map[string]domain.CharacterWorkContinuationLedgerV1{}
	for _, entry := range frozen.Activation.Entries {
		if ledger, ok := prefix.ContinuationLedger(entry.AgentID); ok {
			ledgers[entry.AgentID] = ledger
		}
	}
	selection, err := SelectCharacterWorkContinuations(*frozen, ledgers, prefix.ContinuationBoundaries()...)
	if err != nil {
		return err
	}
	inputs.ContinuationSelection = &selection
	return nil
}
