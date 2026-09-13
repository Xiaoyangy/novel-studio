package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

func TestSelfCompletionViewProducerPreservesBothHistoricalIdentities(t *testing.T) {
	if got := characterActivationProtocolV3LegacyDigest(); got != "sha256:3d9cce9681fd3e0978d6eb7e728c8b9e5fca3075bd82a091ffe050106d79370e" {
		t.Fatal("oldest producer changed", got)
	}
	if got := characterActivationProtocolV3HistoryDigest(); got != "sha256:b0513dc83d43f3b52964ecafe0355abfa677dfd829988c276009ec281db9a296" {
		t.Fatal("full-owner producer changed", got)
	}
	want, err := domain.DeterministicPlanningHash(struct{ Base, CompletionView, CompletionPrompt string }{characterActivationProtocolV3HistoryDigest(), domain.CharacterSelfCompletionViewPolicyV1, characterSelfCompletionViewPromptV1})
	selectionMust(t, err)
	if characterActivationProtocolV3CompletionDigest() != "sha256:"+want {
		t.Fatal("new producer does not bind its selector and prompt")
	}
	seen := map[string]bool{}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		if producer == "" || seen[producer] {
			t.Fatal("producer candidates are not distinct")
		}
		seen[producer] = true
		sources := characterActivationV3PoliciesForProducer(producer)
		if CharacterActivationProtocolWithProducer(domain.CharacterActivationCyclePolicyV3, producer) != producer || characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: sources}) != producer {
			t.Fatal("frozen marker inventory selected another producer")
		}
	}
	if len(seen) != 5 {
		t.Fatal("lost executable historical producer")
	}
}

type completionViewPromptModel struct {
	activationV3RuntimeModel
	want bool
	seen atomic.Int32
}

func (m *completionViewPromptModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var system string
	for _, msg := range messages {
		if msg.Role == agentcore.RoleSystem {
			system += msg.TextContent()
		}
	}
	owner := len(specs) == 1 && specs[0].Name == "submit_character_decision"
	if strings.Contains(system, characterSelfCompletionViewPromptV1) != (owner && m.want) {
		return nil, fmt.Errorf("completion view help crossed producer/role boundary")
	}
	if owner {
		m.seen.Add(1)
		return nil, context.Canceled
	}
	return m.activationV3RuntimeModel.Generate(ctx, messages, specs, opts...)
}
func (m *completionViewPromptModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}
func TestSelfCompletionViewPromptOnlyReachesNewProducerOwners(t *testing.T) {
	for i, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			st, cfg, boundary := activationV3RuntimeFixture(t)
			cfg.CharacterAgents.FrozenActivationProducer = producer
			boundary.FrozenActivationProducer = producer
			// Stop at the first actual owner request after checking its prompt;
			// full persisted recovery is exercised by the three-producer test.
			model := &completionViewPromptModel{want: producer == characterActivationProtocolV3Digest() || producer == characterActivationProtocolV3IncomingReadDigest() || producer == characterActivationProtocolV3CompletionDigest()}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "completion-view", model)}
			if _, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_completion_prompt", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if model.seen.Load() == 0 {
				t.Fatal("test did not inspect an actual owner request")
			}
		})
	}
}
