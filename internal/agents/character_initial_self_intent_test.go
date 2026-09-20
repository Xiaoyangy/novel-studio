package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestInitialSelfIntentProducerPreservesHistoricalSchemaAndInventories(t *testing.T) {
	current, old := characterActivationProtocolV3InitialSelfIntentDigest(), characterActivationProtocolV3IncomingReadDigest()
	if current == "" || current == old || characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV3) != current {
		t.Fatal("opening-intent history must select a distinct fresh producer")
	}
	if old != "sha256:2556d28206cb95b06153a3249b0fc1c233bfbc52650c8729ff1d124084a78651" {
		t.Fatal("immediately preceding frozen producer changed", old)
	}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(producer)
		if domain.HasCharacterInitialSelfIntentPolicyV1(policies) != (producer == current) || characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != producer {
			t.Fatal("initial intention crossed its executable source inventory")
		}
		if producer != current {
			mixed := append(policies, domain.CharacterInitialSelfIntentPolicyV1)
			if producer != old && characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: mixed}) != "" {
				t.Fatal("incomplete historical inventory acquired new producer")
			}
		}
	}
	policies := append(characterActivationV3InitialSelfIntentPolicies(), domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
	oldPolicies := append(characterActivationV3IncomingReadPolicies(), domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
	for _, schemaPair := range [][2]map[string]any{
		{tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: oldPolicies}).Schema(), tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies}).Schema()},
		{tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: oldPolicies}, domain.CharacterAgentActivation{}, nil, old, nil, 1).Schema(), tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: policies}, domain.CharacterAgentActivation{}, nil, current, nil, 1).Schema()},
	} {
		if !reflect.DeepEqual(schemaPair[0], schemaPair[1]) {
			t.Fatal("private intention projection changed existing action schema")
		}
	}
}

func TestInitialSelfIntentProjectionIsPrivateHistoricalAndOptional(t *testing.T) {
	profile := characterAgentProfile{Record: domain.CharacterAgentRecord{AgentID: "owner"}, Character: domain.Character{Name: "本人", InitialState: &domain.CharacterInitialState{CurrentGoal: ownerOriginalGoalProbe}}}
	for _, chapter := range []int{1, 2, 200} {
		for _, enabled := range []bool{false, true} {
			observation := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, AgentID: "owner", Chapter: chapter, CurrentGoal: ownerCurrentGoalProbe}
			if enabled {
				observation.Sources = characterActivationV3InitialSelfIntentPolicies()
			}
			projectCharacterInitialSelfIntent(&observation, profile)
			projectCharacterObservationSourcesV2(&observation)
			if observation.CurrentGoal != ownerCurrentGoalProbe || (len(observation.KnownFacts) == 1) != enabled {
				t.Fatal("historical intention changed current intent or legacy projection")
			}
			if enabled {
				fact := observation.KnownFacts[0]
				if fact.Kind != "initial_self_intent" || fact.Text != ownerOriginalGoalProbe || fact.Visibility != "private" || !domain.IsCharacterSourceRefV2(fact.Source) {
					t.Fatal("history is not the exact owner's private, source-projected initial goal")
				}
			}
		}
	}
	profile.Character.InitialState.CurrentGoal = ""
	profile.Character.InitialState.CurrentAction = futureGoalProbe
	observation := domain.CharacterObservationPacket{Sources: characterActivationV3InitialSelfIntentPolicies()}
	projectCharacterInitialSelfIntent(&observation, profile)
	if len(observation.KnownFacts) != 0 {
		t.Fatal("task fallback invented an opening intention")
	}
}

func TestInitialSelfIntentPromptExplainsReadBranchesWithoutChangingSchema(t *testing.T) {
	for _, sample := range []string{
		`{"resource_id":"本人当前可见的资料ID"}`,
		`{"incoming_delivery_from":"指定发送者实名","task_id":"本人原提案中实际阅读的work任务ID"}`,
	} {
		if !strings.Contains(characterInitialSelfIntentPromptV1, sample) {
			t.Fatal("new-producer help lost its exact parameter example")
		}
		var fields map[string]string
		if err := json.Unmarshal([]byte(sample), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["resource_id"] != "" && (fields["task_id"] != "" || fields["incoming_delivery_from"] != "") {
			t.Fatal("known-document example contradicts original request contract")
		}
	}
	for _, phrase := range []string{"不是必须完成的命令", "继续、修改或放弃原意图", "接收者本人", "artifact_reads"} {
		if !strings.Contains(characterInitialSelfIntentPromptV1, phrase) {
			t.Fatal("new help lost autonomy or existing read authorization boundary")
		}
	}
}

func TestContinuationProducerRuntimeRecoversInitialSelfIntent(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "initial-self-intent", characterActivationProtocolV3InitialSelfIntentDigest(), true, true)
}
