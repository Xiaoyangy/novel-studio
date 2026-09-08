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
	"github.com/voocel/agentcore"
)

func TestCharacterCarryAPIHelpMatchesExistingOwnerTaskContract(t *testing.T) {
	target := 5.0
	o := domain.CharacterObservationPacket{
		Version:    domain.CharacterObservationV2Version,
		Sources:    []string{domain.CharacterSelfExperiencePolicyV2},
		KnownFacts: []domain.CharacterAgentFact{{ID: "owner-known", Text: "本人知道所持物品及目的地"}},
		ResourceViews: []domain.CharacterResourceViewV2{
			{ResourceID: "res_0000000000000001"},
			{ResourceID: "res_0000000000000002"},
		},
		SelfExperiences: []domain.CharacterSelfExperienceV2{
			{ID: "old-carry-a", TaskID: "carry-a", Kind: "carry", Status: "not_started"},
			{ID: "old-carry-b", TaskID: "carry-b", Kind: "carry", Status: "not_started"},
		},
		TaskProgress: []domain.CharacterTaskProgressV2{{TaskID: "read", Action: "本人查阅既有材料", Unit: "minute", Target: &target, State: "not_started"}},
	}
	p := domain.CharacterDecisionProposal{SelfTasks: []domain.CharacterSelfTaskV2{
		{TaskID: "carry-together", Kind: "carry", Action: "本人将两件物品同程携至同一实际终点", ResourceIDs: []string{o.ResourceViews[0].ResourceID, o.ResourceViews[1].ResourceID}, KnowledgeRefs: []string{"owner-known", "old-carry-a", "old-carry-b"}},
		{TaskID: "read", Kind: "work", Action: "本人查阅既有材料", ProgressUnit: "minute", ProgressTarget: &target, KnowledgeRefs: []string{"owner-known"}},
	}}
	before, err := json.Marshal([]any{o, p})
	selectionMust(t, err)
	selectionMust(t, domain.ValidateCharacterSelfTaskIntentV2(p, o))
	if domain.HasCharacterSharedIntervalCarryPolicyV1(o.Sources) {
		t.Fatal("single-task multi-item help requires no new physical policy")
	}
	after, err := json.Marshal([]any{o, p})
	selectionMust(t, err)
	if string(before) != string(after) {
		t.Fatal("checking the current owner declaration rewrote historical experiences or the proposal")
	}
	for _, tc := range []struct {
		name string
		edit func(*domain.CharacterDecisionProposal)
	}{
		{"existing work remains fixed", func(p *domain.CharacterDecisionProposal) { p.SelfTasks[1].Action = "改写已有工作" }},
		{"unseen object remains forbidden", func(p *domain.CharacterDecisionProposal) {
			p.SelfTasks[0].ResourceIDs = append(p.SelfTasks[0].ResourceIDs, "res_0000000000000003")
		}},
		{"duplicate task remains forbidden", func(p *domain.CharacterDecisionProposal) { p.SelfTasks[0].TaskID = p.SelfTasks[1].TaskID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var candidate domain.CharacterDecisionProposal
			raw, err := json.Marshal(p)
			selectionMust(t, err)
			selectionMust(t, json.Unmarshal(raw, &candidate))
			tc.edit(&candidate)
			if err := domain.ValidateCharacterSelfTaskIntentV2(candidate, o); err == nil {
				t.Fatal("API help weakened the existing owner intent contract")
			}
		})
	}
}

type carryAPIHelpPromptModel struct {
	activationV3RuntimeModel
	wantHelp bool
	checked  atomic.Int32
	revised  atomic.Int32
}

func (m *carryAPIHelpPromptModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var system string
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			system += message.TextContent()
		}
	}
	character := len(specs) == 1 && specs[0].Name == "submit_character_decision"
	if strings.Contains(system, characterCarryAPIHelpV1) != (character && m.wantHelp) {
		return nil, fmt.Errorf("carry API help crossed its frozen producer or role boundary")
	}
	if character {
		m.checked.Add(1)
		for _, message := range messages {
			if strings.Contains(message.TextContent(), "UNEXECUTED_PEER_SECRET") || strings.Contains(message.TextContent(), "AUTHOR_PRIVATE_SECRET") {
				return nil, fmt.Errorf("carry help exposed private arbiter text to an owner")
			}
			if message.Role != agentcore.RoleUser {
				continue
			}
			_, packet, ok := strings.Cut(message.TextContent(), "<character_observation_packet>\n")
			if !ok {
				continue
			}
			packet, _, ok = strings.Cut(packet, "\n</character_observation_packet>")
			if !ok {
				return nil, fmt.Errorf("owner packet was truncated")
			}
			var view modelinput.ScopedReferenceModelView
			var observation domain.CharacterObservationPacket
			if err := json.Unmarshal([]byte(packet), &view); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(view.Body, &observation); err != nil {
				return nil, err
			}
			if observation.Round == 2 {
				if len(observation.ConflictFeedback) == 0 {
					return nil, fmt.Errorf("owner revision lost its safe constraint")
				}
				m.revised.Add(1)
				// This test ends after observing the actual R2 prompt. Full
				// recovery/cache coverage lives in the producer recovery test.
				return nil, context.Canceled
			}
		}
	}
	return m.activationV3RuntimeModel.Generate(ctx, messages, specs, opts...)
}

func (m *carryAPIHelpPromptModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func TestCharacterCarryAPIHelpOnlyReachesNewProducerOwner(t *testing.T) {
	if got := characterActivationProtocolV3LegacyDigest(); got != "sha256:3d9cce9681fd3e0978d6eb7e728c8b9e5fca3075bd82a091ffe050106d79370e" {
		t.Fatalf("carry help changed the existing production identity: %s", got)
	}
	want, err := domain.DeterministicPlanningHash(struct{ Base, History, CarryAPIHelp string }{
		characterActivationProtocolV3LegacyDigest(), domain.CharacterWorkContinuationHistoryPolicyV1, characterCarryAPIHelpV1,
	})
	selectionMust(t, err)
	if characterActivationProtocolV3HistoryDigest() != "sha256:"+want {
		t.Fatal("new producer did not bind the complete fixed carry API help")
	}
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			producer := characterActivationProtocolV3LegacyDigest()
			if enabled {
				producer = characterActivationProtocolV3Digest()
			}
			cfg.CharacterAgents.FrozenActivationProducer = producer
			model := &carryAPIHelpPromptModel{wantHelp: enabled}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "carry-api-help", model)}
			const generation = "pg2_carry_api_help"
			_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected to stop only after observing the actual owner revision: %v", err)
			}
			view, err := st.LoadCharacterArbitrationV3(generation, 1)
			selectionMust(t, err)
			if view == nil || view.ProtocolDigest() != producer || model.checked.Load() < 2 || model.revised.Load() == 0 {
				t.Fatal("real runner did not exercise frozen producer and owner revision prompts")
			}
			prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
			selectionMust(t, err)
			if prefix == nil || len(prefix.Steps()) != 1 {
				t.Fatal("test failed to persist the real predecessor before owner revision")
			}
			for _, step := range prefix.Steps() {
				input := step.Input()
				if domain.HasCharacterSharedIntervalCarryPolicyV1(input.Stimulus.Sources) {
					t.Fatal("API help silently enabled multi-task shared-interval physics")
				}
			}
		})
	}
}
