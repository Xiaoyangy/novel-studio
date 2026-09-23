package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type memoryTransportProbeModel struct {
	base          activationV3RuntimeModel
	st            *store.Store
	generation    string
	ownerID       string
	wantEncoded   bool
	tamper        bool
	rejected      bool
	validResponse *agentcore.LLMResponse
	wires         map[string][]byte
	views         map[string]modelinput.ScopedReferenceModelView
}

func (*memoryTransportProbeModel) SupportsTools() bool { return true }

func (m *memoryTransportProbeModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(specs) != 1 {
		return nil, fmt.Errorf("expected exactly one terminal tool")
	}
	if specs[0].Name == "resolve_chapter_world" {
		return nil, context.Canceled
	}
	if specs[0].Name != "submit_character_decision" {
		return nil, fmt.Errorf("unexpected tool %s", specs[0].Name)
	}
	var system string
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			system += message.TextContent()
		}
	}
	decodedMessages := append([]agentcore.Message(nil), messages...)
	var observation domain.CharacterObservationPacket
	for i, message := range messages {
		if message.Role != agentcore.RoleUser {
			continue
		}
		_, raw, ok := strings.Cut(message.TextContent(), "<character_observation_packet>\n")
		if !ok {
			continue
		}
		raw, _, ok = strings.Cut(raw, "\n</character_observation_packet>")
		if !ok {
			return nil, fmt.Errorf("truncated actor input")
		}
		view, err := modelinput.DecodeCharacterMemoryModelViewV1([]byte(raw))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(view.Body, &observation); err != nil {
			return nil, err
		}
		var envelope struct {
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			return nil, err
		}
		encoded := envelope.Encoding != ""
		if strings.Contains(system, modelinput.CharacterMemoryModelViewHelpV1) != encoded {
			return nil, fmt.Errorf("help did not match actual encoding")
		}
		if observation.Character == "甲" {
			if encoded != m.wantEncoded || strings.Contains(raw, "PEER_PRIVATE_MEMORY") {
				return nil, fmt.Errorf("encoding or owner privacy crossed frozen producer")
			}
		}
		m.wires[observation.Character] = []byte(raw)
		m.views[observation.Character] = view
		plain, err := json.Marshal(view)
		if err != nil {
			return nil, err
		}
		// The fake actor interprets the documented lossless transport. Only its
		// input is decoded; the real tool remains bound to the original codec.
		decodedMessages[i], err = modelinput.NewExactAgentPacketMessage(modelinput.KindCharacterObservation, strings.Replace(message.TextContent(), raw, string(plain), 1))
		if err != nil {
			return nil, err
		}
		break
	}
	if observation.Character == "甲" && m.validResponse != nil {
		toolRejected := false
		for _, message := range messages {
			toolRejected = toolRejected || message.Role == agentcore.RoleTool && message.Metadata["is_error"] == true
		}
		if !toolRejected {
			return nil, fmt.Errorf("tampered scoped argument was not rejected")
		}
		prefix, err := m.st.LoadVerifiedCharacterActivationPrefix(m.generation, 1)
		if err != nil {
			return nil, err
		}
		proofs, err := m.st.CharacterAgents.ForActivationCycle(prefix.Session())
		if err != nil {
			return nil, err
		}
		proposal, err := proofs.LoadProposal(m.generation, 1, 1, m.ownerID)
		if err != nil || proposal != nil {
			return nil, fmt.Errorf("invalid reference wrote a proposal: %v", err)
		}
		m.rejected = true
		result := m.validResponse
		m.validResponse = nil
		return result, nil
	}
	response, err := m.base.Generate(ctx, decodedMessages, specs, opts...)
	if err != nil || observation.Character != "甲" {
		return response, err
	}
	if len(observation.Memory) == 0 {
		return nil, fmt.Errorf("probe needs actual owner memory")
	}
	call := response.Message.ToolCalls()[0]
	var arguments map[string]any
	if err := json.Unmarshal(call.Args, &arguments); err != nil {
		return nil, err
	}
	memoryIndex := 0
	for i := range observation.Memory {
		if len(observation.Memory[i].Text) > len(observation.Memory[memoryIndex].Text) {
			memoryIndex = i
		}
	}
	refs := []string{observation.Memory[memoryIndex].ID}
	arguments["knowledge_refs"] = refs
	arguments["self_tasks"].([]any)[0].(map[string]any)["knowledge_refs"] = refs
	call.Args, err = json.Marshal(arguments)
	if err != nil {
		return nil, err
	}
	response.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
	if m.tamper && !m.rejected {
		valid := *response
		valid.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
		m.validResponse = &valid
		arguments["knowledge_refs"] = []string{"@ref999999"}
		call.Args, err = json.Marshal(arguments)
		if err != nil {
			return nil, err
		}
		response.Message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(call)}
	}
	return response, nil
}

func (m *memoryTransportProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestMemoryTextTransportActualDispatchKeepsOriginalToolAuthorityAndOldRecovery(t *testing.T) {
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		t.Run(producer, func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			cfg.CharacterAgents.FrozenActivationProducer, boundary.FrozenActivationProducer = producer, producer
			selectionMust(t, st.EnsureCharacterAgentCanon(0))
			registry, err := st.CharacterAgents.LoadRegistry()
			selectionMust(t, err)
			owner, _ := registry.Resolve("甲")
			peer, _ := registry.Resolve("乙")
			original := map[string]domain.CharacterAgentMemory{}
			for _, record := range []domain.CharacterAgentRecord{owner, peer} {
				memory, err := st.CharacterAgents.LoadCanonicalMemory(record.AgentID)
				selectionMust(t, err)
				text := "PEER_PRIVATE_MEMORY"
				count := 1
				if record.AgentID == owner.AgentID {
					text = strings.Repeat("本人当时完成了检查，但这不证明后来的世界状态，也不推定别人已经合作。", 16)
					count = 32
				}
				for i := 0; i < count; i++ {
					memory.Facts = append(memory.Facts, newCharacterMemoryFact(1, "pre_story", fmt.Sprintf("经历%d；%s", i, text), fmt.Sprintf("owner-source-%d", i), true))
				}
				selectionMust(t, st.CharacterAgents.SaveCanonicalMemory(*memory))
				saved, err := st.CharacterAgents.LoadCanonicalMemory(record.AgentID)
				selectionMust(t, err)
				original[record.AgentID] = *saved
			}
			const generation = "pg2_memory_transport_probe"
			encoded := producer == characterActivationProtocolV3MemoryTextDigest() || (producer == characterActivationProtocolV3HostLocationDigest() || producer == characterActivationProtocolV3AddressingDigest())
			model := &memoryTransportProbeModel{st: st, generation: generation, ownerID: owner.AgentID, wantEncoded: encoded, tamper: encoded, wires: map[string][]byte{}, views: map[string]modelinput.ScopedReferenceModelView{}}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "memory-transport", model)}
			_, err = runCharacterActivationChapter(t.Context(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("did not stop after actual proposals: %v", err)
			}
			if encoded && !model.rejected {
				t.Fatal("encoded actor never exercised original scoped argument rejection")
			}
			view, err := st.LoadCharacterArbitrationV3(generation, 1)
			selectionMust(t, err)
			prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
			selectionMust(t, err)
			proofs, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
			selectionMust(t, err)
			for _, observation := range view.Input().Observations {
				tampered := observation
				tampered.Sources = nil
				for _, source := range observation.Sources {
					if source != domain.CharacterMemoryTextTransportPolicyV1 {
						tampered.Sources = append(tampered.Sources, source)
					}
				}
				if !encoded {
					tampered.Sources = append(tampered.Sources, domain.CharacterMemoryTextTransportPolicyV1)
				}
				tampered, err = domain.FinalizeCharacterObservationPacket(tampered)
				selectionMust(t, err)
				if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(view.Input().Stimulus, tampered); err == nil {
					t.Fatal("re-signed observation switched frozen transport policy")
				}
				codec, err := modelinput.NewScopedArtifactReferenceCodecV1(modelinput.KindCharacterObservation, observation)
				selectionMust(t, err)
				if domain.HasCharacterCommunicationAddressingPolicyV1(observation.Sources) {
					codec, err = modelinput.WithScopedCommunicationReferencesV1(codec)
					selectionMust(t, err)
				}
				want := codec.ModelView()
				got := model.views[observation.Character]
				var wantBody, gotBody any
				selectionMust(t, json.Unmarshal(want.Body, &wantBody))
				selectionMust(t, json.Unmarshal(got.Body, &gotBody))
				if got.Binding != want.Binding || !reflect.DeepEqual(wantBody, gotBody) {
					t.Fatal("model transport changed bound owner data")
				}
				if !encoded || observation.Character != "甲" {
					plain, err := json.Marshal(want)
					selectionMust(t, err)
					if !bytes.Equal(plain, model.wires[observation.Character]) {
						t.Fatal("legacy/no-benefit view bytes changed")
					}
				}
				proposal, err := proofs.LoadProposal(generation, 1, 1, observation.AgentID)
				selectionMust(t, err)
				if proposal == nil {
					t.Fatal("actual scoped tool did not save proposal")
				}
				if observation.Character == "甲" {
					memoryIndex := 0
					for i := range observation.Memory {
						if len(observation.Memory[i].Text) > len(observation.Memory[memoryIndex].Text) {
							memoryIndex = i
						}
					}
					if !reflect.DeepEqual(proposal.KnowledgeRefs, []string{observation.Memory[memoryIndex].ID}) {
						t.Fatal("reconstructed long-memory handle was not restored by original bound codec")
					}
				}
			}
			for owner, before := range original {
				after, err := st.CharacterAgents.LoadCanonicalMemory(owner)
				selectionMust(t, err)
				if !reflect.DeepEqual(before, *after) {
					t.Fatal("model transport mutated canonical memory")
				}
			}
			// Reopening the Store reuses all actual saved owner proposals; a
			// historical producer must not re-encode or re-dispatch them.
			resumed := &memoryTransportProbeModel{st: store.NewStore(st.Dir()), generation: generation, ownerID: owner.AgentID, wantEncoded: encoded, wires: map[string][]byte{}, views: map[string]modelinput.ScopedReferenceModelView{}}
			models = &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "memory-transport", resumed)}
			_, err = runCharacterActivationChapter(t.Context(), cfg, resumed.st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			if !errors.Is(err, context.Canceled) || resumed.base.actorCalls != 0 {
				t.Fatalf("saved proposal recovery re-dispatched actors: %v", err)
			}
		})
	}
}

func TestMemoryTextTransportProducerPreservesBDD8AndSchema(t *testing.T) {
	if got := characterActivationProtocolV3InitialSelfIntentDigest(); got != "sha256:bdd8b0797a5c4f7648e3a825814cf079141f2be555f80e240b011f316b2cc8c3" {
		t.Fatal("BDD8 producer changed", got)
	}
	current := characterActivationProtocolV3MemoryTextDigest()
	if current == "" || current == characterActivationProtocolV3InitialSelfIntentDigest() || CharacterActivationProtocolWithProducer(domain.CharacterActivationCyclePolicyV3, current) != current {
		t.Fatal("historical transport producer is no longer recoverable")
	}
	fresh := characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV3)
	if fresh != characterActivationProtocolV3AddressingDigest() || fresh == current {
		t.Fatal("fresh producer did not advance while preserving transport history")
	}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(producer)
		if domain.HasCharacterMemoryTextTransportPolicyV1(policies) != (producer == current || producer == characterActivationProtocolV3HostLocationDigest() || producer == fresh) || characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != producer {
			t.Fatal("transport crossed frozen inventory")
		}
	}
	oldPolicies := append(characterActivationV3InitialSelfIntentPolicies(), domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
	newPolicies := append(characterActivationV3MemoryTextPolicies(), domain.CharacterSelfExperiencePolicyV2, domain.CharacterOperationalAvailabilityPolicyV1)
	oldTool := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: oldPolicies})
	newTool := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: newPolicies})
	if !reflect.DeepEqual(oldTool.Schema(), newTool.Schema()) {
		t.Fatal("transport changed actor tool wire schema")
	}
	oldArbiter := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: oldPolicies}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	newArbiter := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: newPolicies}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	if !reflect.DeepEqual(oldArbiter.Schema(), newArbiter.Schema()) {
		t.Fatal("transport changed world tool wire schema")
	}
}

func TestContinuationProducerRuntimeRecoversMemoryTextTransport(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "memory-text-transport", characterActivationProtocolV3MemoryTextDigest(), true, true)
}
