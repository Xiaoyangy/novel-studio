package agents

import (
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestContinuationHistoryPolicyHasNewProducerIdentity(t *testing.T) {
	// Captured from the pre-change executable's frozen pg2_ced2a2a78a6ae60143f0a907
	// stimulus producer metadata only; no private observations are copied here.
	if got := characterActivationProtocolV3LegacyDigest(); got != "sha256:3d9cce9681fd3e0978d6eb7e728c8b9e5fca3075bd82a091ffe050106d79370e" {
		t.Fatalf("immediately preceding real producer changed: %s", got)
	}
	policies := characterActivationV3Policies()
	if !domain.HasCharacterWorkContinuationHistoryPolicyV1(policies) {
		t.Fatal("fresh V3 generation did not select full owner history")
	}
	o := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, AgentID: "owner", Sources: policies}
	projectCharacterObservationSourcesV2(&o)
	if !domain.HasCharacterWorkContinuationHistoryPolicyV1(o.Sources) {
		t.Fatal("owner source projection hid the protocol marker")
	}
	// Freeze the exact pre-history producer formula independently of the new
	// marker. Tool wire schemas have not changed; this host policy must still
	// change the generation identity, rather than reinterpreting old grants.
	oldPolicies := characterActivationV3LegacyPolicies()
	oldPolicies = append(oldPolicies, domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterPassiveReceptionPolicyV2)
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: oldPolicies})
	token, err := domain.CharacterActivationCycleSourceToken("pg2_v3_schema", 1, 1, "sha256:0000000000000000000000000000000000000000000000000000000000000000", "")
	if err != nil {
		t.Fatal(err)
	}
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: append(oldPolicies, token)}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	newPolicies := append(append([]string(nil), oldPolicies...), domain.CharacterWorkContinuationHistoryPolicyV1)
	newSubmit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: newPolicies})
	newResolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &domain.WorldPhysicalStateV2{}, StoryClock: &domain.StoryClockContext{}, Sources: append(newPolicies, token)}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	if !reflect.DeepEqual(submit.Schema(), newSubmit.Schema()) || !reflect.DeepEqual(resolve.Schema(), newResolve.Schema()) {
		t.Fatal("host information-root policy changed the model tool wire contract")
	}
	old, err := domain.DeterministicPlanningHash(struct {
		Base, Policy, Cycle, Evidence, Ledger, SourceRounds, Artifacts, Feedback, ReferenceView, CharacterArtifacts, ArbiterArtifacts, ArbiterRounds string
		CharacterResourceTime, ArbiterResourceTime                                                                                                   string
		Submit, Resolve                                                                                                                              map[string]any
	}{characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV2), domain.CharacterActivationCyclePolicyV3, domain.CharacterActivationCycleV3Version, domain.CharacterActivationRoundEvidenceV3Version, domain.CharacterWorkContinuationLedgerV2Version, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterWorkArtifactPolicyV1, domain.CharacterRevisionFeedbackPolicyV1, modelinput.ScopedArtifactReferenceViewPolicyV1, characterWorkArtifactPromptV1, worldArbiterWorkArtifactPromptV1, worldArbiterRoundsPromptV3, characterResourceObservationTimePromptV1, worldArbiterResourceObservationTimePromptV1, submit.Schema(), resolve.Schema()})
	if err != nil {
		t.Fatal(err)
	}
	got := characterActivationProtocolV3Digest()
	if got == "" || got == "sha256:"+old || got != characterActivationProtocolV3Digest() {
		t.Fatalf("continuation policy did not deterministically change V3 producer identity: %s", got)
	}
	for _, policy := range []string{domain.CharacterActivationCyclePolicy, domain.CharacterActivationCyclePolicyV2} {
		if characterActivationProtocolForPolicy(policy) == got {
			t.Fatal("new host continuation strategy reused a legacy identity")
		}
	}
}
