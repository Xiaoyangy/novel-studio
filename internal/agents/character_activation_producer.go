package agents

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Candidates are executable implementations, not arbitrary historical hashes.
// Recovery must match the complete generation dependency root before choosing.
func CharacterActivationProducerCandidates(policy string) []string {
	current := characterActivationProtocolForPolicy(policy)
	if policy == domain.CharacterActivationCyclePolicyV3 {
		return []string{current, characterActivationProtocolV3LegacyDigest()}
	}
	return []string{current}
}

func CharacterActivationProtocolWithProducer(policy, producer string) string {
	if producer == "" {
		return characterActivationProtocolForPolicy(policy)
	}
	for _, candidate := range CharacterActivationProducerCandidates(policy) {
		if producer == candidate {
			return producer
		}
	}
	return ""
}

func characterActivationProtocolForStimulus(stimulus domain.WorldStimulusPacket) string {
	policy := characterActivationPolicyForStimulus(stimulus)
	if policy == domain.CharacterActivationCyclePolicyV3 && !domain.HasCharacterWorkContinuationHistoryPolicyV1(stimulus.Sources) {
		return characterActivationProtocolV3LegacyDigest()
	}
	return characterActivationProtocolForPolicy(policy)
}

func validateCharacterActivationProducerSource(stimulus domain.WorldStimulusPacket) error {
	want := characterActivationProtocolForStimulus(stimulus)
	count := 0
	for _, source := range stimulus.Sources {
		if strings.HasPrefix(source, "character-agent-protocol:") {
			count++
			if source != "character-agent-protocol:"+want {
				return fmt.Errorf("activation producer differs from its frozen source policy")
			}
		}
	}
	if count != 1 || want == "" {
		return fmt.Errorf("activation lacks an exact executable producer source")
	}
	return nil
}
