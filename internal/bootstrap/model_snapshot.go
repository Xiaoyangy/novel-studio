package bootstrap

import (
	"context"
	"fmt"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// ModelSnapshot pins a provider target and its routing identity together.
// Unlike ForRole, its model never follows a later default/role hot swap.
type ModelSnapshot struct {
	Model    agentcore.ChatModel
	Provider string
	Name     string
}

func (ms *ModelSet) SnapshotForRole(role string) (ModelSnapshot, error) {
	if ms == nil {
		return ModelSnapshot{}, fmt.Errorf("model set unavailable")
	}
	ms.selectionMu.RLock()
	defer ms.selectionMu.RUnlock()
	primary := ms.models[resolveRoleAlias(ms, role)]
	if primary == nil {
		primary = ms.Default
	}
	if primary == nil {
		return ModelSnapshot{}, fmt.Errorf("model role %s unavailable", role)
	}
	target := primary.snapshotTarget()
	if target.model == nil {
		return ModelSnapshot{}, fmt.Errorf("model role %s has no provider", role)
	}
	model := agentcore.ChatModel(&snapshotRoleModel{target: target, role: role, decorate: ms.getAttemptDecorator()})
	return ModelSnapshot{Model: model, Provider: target.provider, Name: target.name}, nil
}

type snapshotRoleModel struct {
	target   modelTarget
	role     string
	decorate ModelAttemptDecorator
}

func (m *snapshotRoleModel) attempt(ctx context.Context) agentcore.ChatModel {
	if m.decorate == nil {
		return m.target.model
	}
	return m.decorate(ctx, m.role, m.target.provider, m.target.name, m.target.model)
}
func (m *snapshotRoleModel) Generate(ctx context.Context, msg []agentcore.Message, spec []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.attempt(ctx).Generate(ctx, msg, spec, opts...)
}
func (m *snapshotRoleModel) GenerateStream(ctx context.Context, msg []agentcore.Message, spec []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return m.attempt(ctx).GenerateStream(ctx, msg, spec, opts...)
}
func (m *snapshotRoleModel) SupportsTools() bool { return m.target.model.SupportsTools() }
func (m *snapshotRoleModel) ProviderName() string {
	if provider, ok := m.target.model.(agentcore.ProviderNamer); ok {
		return provider.ProviderName()
	}
	return m.target.provider
}
func (m *snapshotRoleModel) Info() llm.ModelInfo {
	if provider, ok := m.target.model.(interface{ Info() llm.ModelInfo }); ok {
		return provider.Info()
	}
	return llm.ModelInfo{Name: m.target.name, Provider: m.target.provider}
}
func (m *snapshotRoleModel) Capabilities() llm.Capabilities {
	if provider, ok := m.target.model.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}
func (m *snapshotRoleModel) UsageAccountingBound() bool { return m.decorate != nil }
