package agents

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const characterWorkContinuationPrompt = `
你可以自行决定是否在work_continuations中授权一个有明确有限总目标的单一work任务继续执行。until_target=true表示授权做到本人声明目标；或用max_effective_minutes授权本次提案之后的额外有效分钟上限（含本周期已执行部分），二者只能选一个。任务总需求本身不等于授权。出现本人收到的新信息、权限/可感资源变化、阻断、目标完成或额度耗尽时会再由你决定；这不是自动成功许可。复杂动作、位置改变、通信、阅读、测量或没有明确目标的任务不自动续做；不想授权可省略。`

func characterActivationPolicyForBoundary(boundary ProjectedArcBoundary) string {
	if boundary.CharacterActivationPolicy == domain.CharacterActivationCyclePolicyV3 {
		return domain.CharacterActivationCyclePolicyV3
	}
	if boundary.CharacterActivationPolicy == domain.CharacterActivationCyclePolicyV2 {
		return domain.CharacterActivationCyclePolicyV2
	}
	return domain.CharacterActivationCyclePolicy
}

func characterActivationPolicyForStimulus(stimulus domain.WorldStimulusPacket) string {
	if hasCharacterActivationPolicyV3(stimulus.Sources) {
		return domain.CharacterActivationCyclePolicyV3
	}
	if domain.HasCharacterSelfChronologyPolicyV1(stimulus.Sources) {
		return domain.CharacterActivationCyclePolicyV2
	}
	return domain.CharacterActivationCyclePolicy
}

func characterActivationProtocolForPolicy(policy string) string {
	if policy == domain.CharacterActivationCyclePolicyV3 {
		return characterActivationProtocolV3Digest()
	}
	if policy == domain.CharacterActivationCyclePolicy {
		return characterActivationProtocolDigest()
	}
	if policy != domain.CharacterActivationCyclePolicyV2 {
		return ""
	}
	policies := []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1,
		domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1}
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
	hash, err := domain.DeterministicPlanningHash(struct {
		Legacy, Policy, Chronology, Continuation, References, ReferencePrompt, ContinuationPrompt, ArbiterContinuationPrompt, CycleWire string
		SubmitSchema                                                                                                                    map[string]any
	}{characterActivationProtocolDigest(), policy, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1,
		modelinput.ScopedReferenceViewPolicy, scopedReferencePrompt, characterWorkContinuationPrompt, worldArbiterContinuationPromptV2, domain.CharacterActivationCycleV2Version, submit.Schema()})
	if err != nil {
		return ""
	}
	return "sha256:" + hash
}

func prepareCharacterActivationChronology(stimulus *domain.WorldStimulusPacket, session domain.CharacterActivationSession, policy, producer string) error {
	if stimulus.PhysicalState == nil {
		return fmt.Errorf("new activation policy lacks physical baseline")
	}
	before := *stimulus.PhysicalState
	prepared, err := domain.PrepareCharacterSelfChronologyStateV1(before)
	if err != nil {
		return err
	}
	if err := domain.ValidateCharacterSelfChronologyBaselineTransitionV1(before, prepared); err != nil {
		return err
	}
	root, err := domain.CharacterPhysicalRootForCycle(prepared)
	if err != nil || root != session.CurrentPhysicalRoot {
		return fmt.Errorf("chronology baseline differs from its frozen session root")
	}
	stimulus.PhysicalState = &prepared
	stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	if err != nil {
		return err
	}
	if domain.CharacterActivationUsesRoundSources(policy) {
		selected := characterActivationV3Policies()
		if policy == domain.CharacterActivationCyclePolicyV3 && producer == characterActivationProtocolV3LegacyDigest() {
			selected = characterActivationV3LegacyPolicies()
		}
		stimulus.Sources = compactAgentStrings(append(stimulus.Sources, selected...))
	} else {
		stimulus.Sources = compactAgentStrings(append(stimulus.Sources, domain.CharacterActivationCyclePolicyV2,
			domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1))
	}
	return nil
}

func validateCharacterActivationInputPolicy(input domain.CharacterActivationInputSet, session domain.CharacterActivationSession, policy string, producers ...string) error {
	if characterActivationPolicyForStimulus(input.Stimulus) != policy {
		return fmt.Errorf("stored activation input uses a different frozen execution policy")
	}
	if domain.CharacterActivationUsesVerifiedPrefix(policy) {
		if domain.CharacterActivationUsesRoundSources(policy) {
			if err := validateCharacterActivationProducerSource(input.Stimulus); err != nil {
				return err
			}
			if len(producers) > 1 || (len(producers) == 1 && characterActivationProtocolForStimulus(input.Stimulus) != CharacterActivationProtocolWithProducer(policy, producers[0])) {
				return fmt.Errorf("stored activation input differs from the generation's frozen producer")
			}
			selected := characterActivationV3Policies()
			if policy == domain.CharacterActivationCyclePolicyV3 && !domain.HasCharacterWorkContinuationHistoryPolicyV1(input.Stimulus.Sources) {
				selected = characterActivationV3LegacyPolicies()
			}
			for _, p := range selected {
				found := false
				for _, s := range input.Stimulus.Sources {
					found = found || s == p
				}
				if !found {
					return fmt.Errorf("v3 input lacks frozen policy %s", p)
				}
				for _, o := range input.Observations {
					present := false
					for _, s := range o.Sources {
						present = present || s == p
					}
					if !present {
						return fmt.Errorf("v3 observation lacks frozen source policy")
					}
				}
			}
		}
		if input.Stimulus.SelfEvaluationContext == nil || !domain.HasCharacterWorkContinuationPolicyV1(input.Stimulus.Sources) {
			return fmt.Errorf("new activation policy lacks its complete Host execution binding")
		}
		return domain.ValidateCharacterSelfEvaluationContextAgainstSessionV1(*input.Stimulus.SelfEvaluationContext, session)
	}
	return nil
}
