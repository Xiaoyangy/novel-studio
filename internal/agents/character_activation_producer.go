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
		return []string{current, characterActivationProtocolV3CompletionDigest(), characterActivationProtocolV3HistoryDigest(), characterActivationProtocolV3LegacyDigest()}
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
	if policy == domain.CharacterActivationCyclePolicyV3 {
		if domain.HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources) && (!domain.HasCharacterWorkContinuationHistoryPolicyV1(stimulus.Sources) || !domain.HasCharacterSelfCompletionViewPolicyV1(stimulus.Sources)) {
			return "" // No older producer can acquire only the new wire marker.
		}
		if !domain.HasCharacterWorkContinuationHistoryPolicyV1(stimulus.Sources) {
			return characterActivationProtocolV3LegacyDigest()
		}
		if !domain.HasCharacterSelfCompletionViewPolicyV1(stimulus.Sources) {
			return characterActivationProtocolV3HistoryDigest()
		}
		if !domain.HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources) {
			return characterActivationProtocolV3CompletionDigest()
		}
	}
	return characterActivationProtocolForPolicy(policy)
}

func characterActivationV3PoliciesForProducer(producer string) []string {
	switch producer {
	case characterActivationProtocolV3LegacyDigest():
		return characterActivationV3LegacyPolicies()
	case characterActivationProtocolV3HistoryDigest():
		return characterActivationV3HistoryPolicies()
	case characterActivationProtocolV3CompletionDigest():
		return characterActivationV3CompletionPolicies()
	case characterActivationProtocolV3Digest():
		return characterActivationV3Policies()
	}
	return nil
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
