package agents

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
)

func characterActivationV3MemoryTextPolicies() []string {
	return append(characterActivationV3InitialSelfIntentPolicies(), domain.CharacterMemoryTextTransportPolicyV1)
}

func characterActivationProtocolV3MemoryTextDigest() string {
	digest, err := domain.DeterministicPlanningHash(struct{ Base, Policy, Encoding, Help string }{
		characterActivationProtocolV3InitialSelfIntentDigest(), domain.CharacterMemoryTextTransportPolicyV1,
		modelinput.CharacterMemoryModelViewPolicyV1, modelinput.CharacterMemoryModelViewHelpV1,
	})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}
