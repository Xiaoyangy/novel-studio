package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// renderContextPrimedModel closes the provider-independent first-token gap.
// The host loads the exact render-locked payload itself and appends a
// server-owned user envelope to a per-call message copy. Only after this
// envelope passes the full prose contract does the wrapper consume the
// one-shot provider permit and cross the downstream provider boundary.
type renderContextPrimedModel struct {
	base        agentcore.ChatModel
	store       *store.Store
	contextTool *tools.ContextTool
}

func withRenderContextPriming(
	base agentcore.ChatModel,
	st *store.Store,
	contextTool *tools.ContextTool,
) agentcore.ChatModel {
	if base == nil {
		return nil
	}
	return &renderContextPrimedModel{base: base, store: st, contextTool: contextTool}
}

func (m *renderContextPrimedModel) Generate(
	ctx context.Context,
	messages []agentcore.Message,
	toolSpecs []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	primed, chapter, err := m.prime(ctx, messages)
	if err != nil {
		return nil, err
	}
	if err := m.store.Runtime.ConsumePipelineRenderProsePermit(chapter); err != nil {
		return nil, fmt.Errorf("render context priming consume provider permit: %w", err)
	}
	return m.base.Generate(ctx, primed, toolSpecs, opts...)
}

func (m *renderContextPrimedModel) GenerateStream(
	ctx context.Context,
	messages []agentcore.Message,
	toolSpecs []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	primed, chapter, err := m.prime(ctx, messages)
	if err != nil {
		return nil, err
	}
	if err := m.store.Runtime.ConsumePipelineRenderProsePermit(chapter); err != nil {
		return nil, fmt.Errorf("render context priming consume provider permit: %w", err)
	}
	return m.base.GenerateStream(ctx, primed, toolSpecs, opts...)
}

func (m *renderContextPrimedModel) prime(
	ctx context.Context,
	messages []agentcore.Message,
) ([]agentcore.Message, int, error) {
	if m == nil || m.store == nil || m.contextTool == nil || m.base == nil {
		return nil, 0, fmt.Errorf("render context priming is not configured")
	}
	if err := m.store.Runtime.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return nil, 0, fmt.Errorf("render context priming validate candidate evidence tree: %w", err)
	}
	lock, err := m.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, 0, fmt.Errorf("render context priming load execution lock: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionRender ||
		lock.TargetChapter <= 0 || strings.TrimSpace(lock.PlanDigest) == "" {
		return nil, 0, fmt.Errorf("render context priming requires an exact active render lock")
	}
	args, err := json.Marshal(map[string]any{
		"chapter": lock.TargetChapter,
		"profile": "draft",
	})
	if err != nil {
		return nil, 0, fmt.Errorf("render context priming encode args: %w", err)
	}
	raw, err := m.contextTool.Execute(nonNilContext(ctx), args)
	if err != nil {
		return nil, 0, fmt.Errorf("render context priming load exact frozen context: %w", err)
	}
	if err := validatePrimedRenderContext(raw, lock.TargetChapter); err != nil {
		return nil, 0, fmt.Errorf("render context priming validate prose contract: %w", err)
	}
	envelope, identity, err := aigc.BuildProseRenderPrimingEnvelope(
		lock.TargetChapter,
		lock.PlanDigest,
		raw,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("render context priming build envelope: %w", err)
	}

	// Never mutate the agent's persisted message slice. Remove any older or
	// malformed copy of this server-owned protocol and append exactly one fresh
	// identity, preventing retries/turns from multiplying context.
	primed := make([]agentcore.Message, 0, len(messages)+1)
	for _, message := range messages {
		_, _, recognized, _ := aigc.ParseProseRenderPrimingEnvelope(message.TextContent())
		if recognized {
			continue
		}
		primed = append(primed, message)
	}
	primed = append(primed, agentcore.Message{
		Role:    agentcore.RoleUser,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(envelope)},
		Metadata: map[string]any{
			"server_owned":     true,
			"protocol_version": identity.ProtocolVersion,
			"chapter":          identity.Chapter,
			"plan_digest":      identity.PlanDigest,
			"payload_sha256":   identity.PayloadSHA256,
		},
	})
	return primed, lock.TargetChapter, nil
}

func validatePrimedRenderContext(raw json.RawMessage, chapter int) error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode draft context: %w", err)
	}
	if payload == nil || payload["_context_profile"] != "draft" {
		return fmt.Errorf("draft context profile is missing or invalid")
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
	if err != nil {
		return err
	}
	return aigc.ValidateProseRenderPacketV11(packet, chapter)
}

func (m *renderContextPrimedModel) SupportsTools() bool { return m.base.SupportsTools() }

func (m *renderContextPrimedModel) ProviderName() string {
	if provider, ok := m.base.(agentcore.ProviderNamer); ok {
		return provider.ProviderName()
	}
	return ""
}

func (m *renderContextPrimedModel) ModelName() string {
	if model, ok := m.base.(agentcore.ModelNamer); ok {
		return model.ModelName()
	}
	return ""
}

func (m *renderContextPrimedModel) Info() llm.ModelInfo {
	if info, ok := m.base.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info()
	}
	return llm.ModelInfo{}
}

func (m *renderContextPrimedModel) Capabilities() llm.Capabilities {
	if provider, ok := m.base.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}
