package testutil

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// CharacterActivationV3Inputs constructs actual public-domain inputs, not a
// serialized verification badge. Callers own their temporary Store setup.
func CharacterActivationV3Inputs(t *testing.T) (domain.CharacterActivationSession, domain.CharacterActivationInputSet) {
	t.Helper()
	input := CharacterActivationInputs(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	physical, err := domain.PrepareCharacterSelfChronologyStateV1(*input.Stimulus.PhysicalState)
	must(err)
	input.Stimulus.PhysicalState = &physical
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, input.Stimulus.Chapter, "sha256:"+strings.Repeat("a", 64), physical, input.Stimulus.StoryClock.CurrentDay, 8)
	must(err)
	policies := []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, 1, session.ChapterContextDigest, "")
	must(err)
	input.Stimulus.Sources = append(append([]string(nil), policies...), token)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	must(err)
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	must(err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.Sources = append([]string(nil), policies...)
		o.StimulusDigest = input.Stimulus.Digest
		o.CycleContext, err = domain.NewCharacterObservationCycleContext(session)
		must(err)
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
		must(err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, o.AgentID)
		must(err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		must(err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID {
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	must(err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	must(err)
	return session, input
}
