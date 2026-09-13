package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func incomingReadItemsProperties(t *testing.T, value map[string]any, field string) map[string]any {
	t.Helper()
	return value["properties"].(map[string]any)[field].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
}

func TestIncomingMaterialReadProducerKeepsHistoricalWireBoundaries(t *testing.T) {
	current := characterActivationProtocolV3IncomingReadDigest()
	if current == "" || current == characterActivationProtocolV3Digest() || characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV3) != current {
		t.Fatal("new default must bind a distinct executable producer")
	}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(producer)
		want := producer == current
		if domain.HasCharacterIncomingMaterialReadPolicyV1(policies) != want || characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != producer {
			t.Fatal("incoming read crossed frozen source inventory")
		}
		policies = append(policies, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
		submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
		resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: policies}, domain.CharacterAgentActivation{}, nil, producer, nil, 1)
		_, hasTask := incomingReadItemsProperties(t, submit.Schema(), "resource_reads")["task_id"]
		_, hasTime := incomingReadItemsProperties(t, resolve.Schema(), "resource_deliveries")["delivered_at_day"]
		if hasTask != want || hasTime != want {
			t.Fatal("conditional version binding appeared in a historical schema")
		}
	}
	for _, old := range [][]string{characterActivationV3LegacyPolicies(), characterActivationV3HistoryPolicies(), characterActivationV3CompletionPolicies()} {
		if got := characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: append(old, domain.CharacterIncomingMaterialReadPolicyV1)}); got != "" {
			t.Fatal("partial marker inventory acquired a new producer")
		}
	}
}

type incomingReadPromptProbe struct {
	want  bool
	calls int
}

func (*incomingReadPromptProbe) SupportsTools() bool { return true }

func (m *incomingReadPromptProbe) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_character_decision" {
		return nil, fmt.Errorf("incoming read probe received extra capabilities")
	}
	var system string
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			system += message.TextContent()
		}
	}
	if strings.Contains(system, characterIncomingMaterialReadPromptV1) != m.want {
		return nil, fmt.Errorf("incoming read prompt crossed frozen producer boundary")
	}
	parameters := specs[0].Parameters.(map[string]any)
	props := parameters["properties"].(map[string]any)["resource_reads"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, exists := props["task_id"]; exists != m.want {
		return nil, fmt.Errorf("model-visible incoming read schema differs from prompt")
	}
	return nil, context.Canceled
}

func (m *incomingReadPromptProbe) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	_, err := m.Generate(ctx, messages, specs, opts...)
	return nil, err
}

func TestIncomingMaterialReadActualActorDispatchUsesOnlyFrozenHelp(t *testing.T) {
	for i, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			cfg.CharacterAgents.FrozenActivationProducer, boundary.FrozenActivationProducer = producer, producer
			model := &incomingReadPromptProbe{want: producer == characterActivationProtocolV3IncomingReadDigest()}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "incoming-read-help", model)}
			_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_incoming_help", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			if !errors.Is(err, context.Canceled) || model.calls != 1 {
				t.Fatalf("did not reach exactly one actor request: %v calls=%d", err, model.calls)
			}
			stimulus := domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: characterActivationV3PoliciesForProducer(producer)}
			tool := tools.NewResolveChapterWorldTool(nil, stimulus, domain.CharacterAgentActivation{}, nil, producer, nil, 1)
			prompt, _, _, err := prepareCharacterArbitrationRequest(characterAgentChapterInputs{Stimulus: stimulus}, nil, tool)
			selectionMust(t, err)
			if strings.Contains(prompt, worldArbiterIncomingMaterialReadPromptV1) != model.want {
				t.Fatal("arbiter incoming-read help crossed producer boundary")
			}
		})
	}
}

func TestContinuationProducerRuntimeRecoversFrozenIncomingMaterialRead(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "incoming-material-read", characterActivationProtocolV3IncomingReadDigest(), true, true)
}
