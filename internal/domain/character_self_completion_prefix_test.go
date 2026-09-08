package domain_test

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func completionPolicyInput(t *testing.T, input domain.CharacterActivationInputSet, enabled bool) domain.CharacterActivationInputSet {
	t.Helper()
	input = historyV3Input(t, input, true)
	if enabled {
		input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterSelfCompletionViewPolicyV1)
	}
	var err error
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		if enabled {
			o.Sources = append(o.Sources, domain.CharacterSelfCompletionViewPolicyV1)
		}
		o.StimulusDigest = input.Stimulus.Digest
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationForSourcesV2(*input.Stimulus.PhysicalState, o.AgentID, o.Sources)
		continuationMust(t, err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		continuationMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID && input.Activation.Entries[j].State == domain.CharacterAgentActive {
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func TestSelfCompletionViewCannotChangeInsideVerifiedPrefix(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		context, _, input := v3Fixture(t)
		initial, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, context.Digest, *input.Stimulus.PhysicalState, 0, 4)
		continuationMust(t, err)
		prefix, err := domain.NewVerifiedCharacterActivationPrefix(initial)
		continuationMust(t, err)
		input = completionPolicyInput(t, v3RebindInput(t, input, initial, true), enabled)
		cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, historyV3Draft(t, prefix, input))
		continuationMust(t, err)
		_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
		continuationMust(t, err)
		prefix, _ = v3Assess(t, context, pending, "continue")
		changed := completionPolicyInput(t, v3NextInput(t, prefix), !enabled)
		if _, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, changed, changed.Observations[0].AgentID); err == nil || !strings.Contains(err.Error(), "self completion view policy") {
			t.Fatalf("mid-session completion policy switch accepted: %v", err)
		}
	}
}
