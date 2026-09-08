package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestSurfaceInspectionProducerPreservesThreeHistoricalIdentities(t *testing.T) {
	for name, value := range map[string]struct{ got, want string }{
		"legacy":     {characterActivationProtocolV3LegacyDigest(), "sha256:3d9cce9681fd3e0978d6eb7e728c8b9e5fca3075bd82a091ffe050106d79370e"},
		"full-owner": {characterActivationProtocolV3HistoryDigest(), "sha256:b0513dc83d43f3b52964ecafe0355abfa677dfd829988c276009ec281db9a296"},
		"completion": {characterActivationProtocolV3CompletionDigest(), "sha256:19eca2f61d4e47dc07692f7898575a2f6c645a911e5b6a98b5097f874788f0d5"},
	} {
		if value.got != value.want {
			t.Fatalf("%s frozen producer changed: %s", name, value.got)
		}
	}
	seen := map[string]bool{}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(producer)
		if producer == "" || seen[producer] || characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != producer {
			t.Fatal("producer candidates are not distinct exact executable inventories")
		}
		seen[producer] = true
		if domain.HasCharacterSurfaceInspectionPolicyV1(policies) != (producer == characterActivationProtocolV3Digest()) {
			t.Fatal("old producer gained or current producer lost surface capability")
		}
		policies = append(policies, domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
		submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: policies})
		resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: policies}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
		for _, contract := range []map[string]any{submit.Schema(), resolve.Schema()} {
			raw, _ := json.Marshal(contract)
			if strings.Contains(string(raw), `"surface"`) != (producer == characterActivationProtocolV3Digest()) {
				t.Fatal("surface wire schema crossed the frozen producer boundary")
			}
		}
	}
	if len(seen) != 4 {
		t.Fatal("missing historical executable producer")
	}
	for _, old := range [][]string{characterActivationV3LegacyPolicies(), characterActivationV3HistoryPolicies()} {
		if got := characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: append(old, domain.CharacterSurfaceInspectionPolicyV1)}); got != "" {
			t.Fatal("incomplete old inventory could acquire new wire marker")
		}
	}
}

type surfacePromptModel struct {
	want bool
	seen atomic.Int32
}

func (*surfacePromptModel) SupportsTools() bool { return true }
func (m *surfacePromptModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 || specs[0].Name != "submit_character_decision" {
		return nil, fmt.Errorf("surface prompt test only expects the first owner call")
	}
	var system string
	var observation domain.CharacterObservationPacket
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			system += message.TextContent()
		}
		if message.Role != agentcore.RoleUser {
			continue
		}
		_, raw, found := strings.Cut(message.TextContent(), "<character_observation_packet>\n")
		if !found {
			continue
		}
		raw, _, closed := strings.Cut(raw, "\n</character_observation_packet>")
		if !closed {
			return nil, fmt.Errorf("truncated surface model view")
		}
		var view modelinput.ScopedReferenceModelView
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(view.Body, &observation); err != nil {
			return nil, err
		}
	}
	if strings.Contains(system, characterSurfaceInspectionPromptV1) != m.want || domain.HasCharacterSurfaceInspectionPolicyV1(observation.Sources) != m.want {
		return nil, fmt.Errorf("surface prompt or marker crossed producer boundary")
	}
	if len(observation.ResourceViews) != 1 || (len(observation.ResourceViews[0].InspectableSurfaces) > 0) != m.want {
		return nil, fmt.Errorf("new surface capability view leaked into an old producer or vanished")
	}
	m.seen.Add(1)
	return nil, context.Canceled
}
func (m *surfacePromptModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(stream)
	return stream, nil
}

func TestSurfaceInspectionPromptAndOwnerCapabilityAreProducerBound(t *testing.T) {
	for i, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			characters, err := st.Characters.Load()
			selectionMust(t, err)
			for i := range characters {
				characters[i].InitialState.ResourceBalances = []domain.InitialCharacterResourceV2{{ResourceID: "res_7d683751779f4393", Name: "原有F07", PerceivedName: "F07", PerceivedLabel: "F07", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, InspectableSurfaces: []string{"seal_exterior"}, ReadableFacts: []domain.ResourceReadableFactV2{{ID: "manifest", Text: "外清单只是历史文本"}}}}
			}
			selectionMust(t, st.Characters.Save(characters))
			cfg.CharacterAgents.FrozenActivationProducer, boundary.FrozenActivationProducer = producer, producer
			model := &surfacePromptModel{want: producer == characterActivationProtocolV3Digest()}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "surface-view", model)}
			_, err = runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_surface_view", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			if !errors.Is(err, context.Canceled) || model.seen.Load() != 1 {
				t.Fatalf("did not reach exactly one inspected owner request: %v seen=%d", err, model.seen.Load())
			}
			policies := characterActivationV3PoliciesForProducer(producer)
			stimulus := domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: policies}
			tool := tools.NewResolveChapterWorldTool(nil, stimulus, domain.CharacterAgentActivation{}, nil, producer, nil, 1)
			prompt, _, _, err := prepareCharacterArbitrationRequest(characterAgentChapterInputs{Stimulus: stimulus}, nil, tool)
			selectionMust(t, err)
			if strings.Contains(prompt, worldArbiterSurfaceInspectionPromptV1) != model.want {
				t.Fatal("surface arbiter instructions crossed producer boundary")
			}
		})
	}
}
