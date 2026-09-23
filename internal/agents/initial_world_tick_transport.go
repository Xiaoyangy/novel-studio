package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type initialWorldTickTransport struct {
	agentcore.ChatModel
	store         *store.Store
	initialError  error
	retryFeedback string
}

func withInitialWorldTickTransport(model agentcore.ChatModel, st *store.Store, retryFeedback ...string) agentcore.ChatModel {
	_, active, err := tools.InitialWorldTickExactContext(st)
	if !active && err == nil {
		return model
	}
	feedback := ""
	if len(retryFeedback) > 0 {
		feedback = retryFeedback[0]
	}
	return &initialWorldTickTransport{ChatModel: model, store: st, initialError: err, retryFeedback: feedback}
}

func (m *initialWorldTickTransport) messages(messages []agentcore.Message) ([]agentcore.Message, error) {
	if m.initialError != nil {
		return nil, m.initialError
	}
	exact, active, err := tools.InitialWorldTickExactContext(m.store)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("initial world_tick source lease ended; no provider call")
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(m.store)
	if err != nil {
		return nil, err
	}
	result := make([]agentcore.Message, 0, len(messages)+1)
	dispatchFound := false
	sourceAdded := false
	for _, message := range messages {
		if message.Role == agentcore.RoleUser && strings.Contains(message.TextContent(), contract.Block) {
			packet, err := modelinput.NewExactAgentPacketMessage(modelinput.KindInitialWorldTick, message.TextContent())
			if err != nil {
				return nil, err
			}
			result = append(result, packet)
			dispatchFound = true
			continue
		}
		if message.Role == agentcore.RoleTool && message.Metadata["tool_name"] == "novel_context" && message.Metadata["is_error"] != true {
			if message.TextContent() != string(exact) {
				return nil, fmt.Errorf("initial world_tick successful novel_context source is stale, partial or unverified; no provider call")
			}
			// Keep the tool response/call pairing. The complete verified bytes are
			// transported once as host user data, never as system instructions.
			copy := message
			copy.Content = []agentcore.ContentBlock{agentcore.TextBlock(`{"exact_author_source":"complete source follows as host data; no new permissions"}`)}
			result = append(result, copy)
			if !sourceAdded {
				packet, err := modelinput.NewExactAgentPacketMessage(modelinput.KindInitialWorldTick, string(exact))
				if err != nil {
					return nil, err
				}
				result = append(result, packet)
				sourceAdded = true
			}
			continue
		}
		result = append(result, message)
	}
	if !dispatchFound {
		return nil, fmt.Errorf("initial world_tick Architect input lacks the complete current dispatch contract; no provider call")
	}
	if m.retryFeedback != "" {
		found := false
		for _, message := range result {
			if message.Role == agentcore.RoleUser && strings.Contains(message.TextContent(), m.retryFeedback) {
				found = true
				break
			}
		}
		if !found {
			packet, err := modelinput.NewExactAgentPacketMessage(modelinput.KindInitialWorldTick, m.retryFeedback)
			if err != nil {
				return nil, err
			}
			result = append(result, packet)
		}
	}
	return result, nil
}

func (m *initialWorldTickTransport) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	transformed, err := m.messages(messages)
	if err != nil {
		return nil, err
	}
	return m.ChatModel.Generate(ctx, transformed, specs, opts...)
}
func (m *initialWorldTickTransport) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	transformed, err := m.messages(messages)
	if err != nil {
		return nil, err
	}
	return m.ChatModel.GenerateStream(ctx, transformed, specs, opts...)
}
func (m *initialWorldTickTransport) ProviderName() string {
	if p, ok := m.ChatModel.(agentcore.ProviderNamer); ok {
		return p.ProviderName()
	}
	return ""
}
func (m *initialWorldTickTransport) ModelName() string {
	if p, ok := m.ChatModel.(interface{ ModelName() string }); ok {
		return p.ModelName()
	}
	return ""
}
func (m *initialWorldTickTransport) Info() llm.ModelInfo {
	if p, ok := m.ChatModel.(interface{ Info() llm.ModelInfo }); ok {
		return p.Info()
	}
	return llm.ModelInfo{Name: m.ModelName(), Provider: m.ProviderName()}
}
func (m *initialWorldTickTransport) Capabilities() llm.Capabilities {
	if p, ok := m.ChatModel.(llm.CapabilityProvider); ok {
		return p.Capabilities()
	}
	return llm.Capabilities{}
}
func (m *initialWorldTickTransport) UsageAccountingBound() bool {
	p, ok := m.ChatModel.(interface{ UsageAccountingBound() bool })
	return ok && p.UsageAccountingBound()
}

func (m *initialWorldTickTransport) ExactAgentContextWindow() int {
	if p, ok := m.ChatModel.(interface{ ExactAgentContextWindow() int }); ok {
		return p.ExactAgentContextWindow()
	}
	return 0
}

// Compile-time assertion: source transport never provides a mutation capability.
var _ agentcore.ChatModel = (*initialWorldTickTransport)(nil)
