package bootstrap

import (
	"context"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// ModelAttemptDecorator is process-local instrumentation, not model routing or
// prompt configuration. Each invocation receives one fixed provider target.
type ModelAttemptDecorator func(context.Context, string, string, string, agentcore.ChatModel) agentcore.ChatModel

func (ms *ModelSet) SetAttemptDecorator(decorator ModelAttemptDecorator) {
	ms.decoratorMu.Lock()
	defer ms.decoratorMu.Unlock()
	ms.attemptDecorator = decorator
}
func (ms *ModelSet) getAttemptDecorator() ModelAttemptDecorator {
	ms.decoratorMu.RLock()
	defer ms.decoratorMu.RUnlock()
	return ms.attemptDecorator
}

func (ms *ModelSet) observeRole(role string, primary *SwappableModel) agentcore.ChatModel {
	decorator := ms.getAttemptDecorator()
	if decorator == nil {
		return primary
	}
	return &observedRoleModel{primary: primary, role: role, decorate: decorator}
}

// ForDefaultPurpose retains the configured default model even when the named
// purpose has an explicit role override; only its accounting label changes.
func (ms *ModelSet) ForDefaultPurpose(purpose string) agentcore.ChatModel {
	return ms.observeRole(purpose, ms.Default)
}

func (m *SwappableModel) snapshotTarget() modelTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return modelTarget{provider: m.provider, name: m.name, model: m.SwappableModel.Current()}
}

type observedRoleModel struct {
	primary  *SwappableModel
	role     string
	decorate ModelAttemptDecorator
}

func (m *observedRoleModel) UsageAccountingBound() bool { return true }
func (m *observedRoleModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	t := m.primary.snapshotTarget()
	return m.decorate(ctx, m.role, t.provider, t.name, t.model).Generate(ctx, messages, specs, opts...)
}
func (m *observedRoleModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	t := m.primary.snapshotTarget()
	return m.decorate(ctx, m.role, t.provider, t.name, t.model).GenerateStream(ctx, messages, specs, opts...)
}
func (m *observedRoleModel) SupportsTools() bool            { return m.primary.SupportsTools() }
func (m *observedRoleModel) ProviderName() string           { return m.primary.ProviderName() }
func (m *observedRoleModel) Info() llm.ModelInfo            { return m.primary.Info() }
func (m *observedRoleModel) Capabilities() llm.Capabilities { return m.primary.Capabilities() }

func (m *failoverModel) attemptModel(ctx context.Context, target modelTarget) agentcore.ChatModel {
	if m.decorate == nil {
		return target.model
	}
	return m.decorate(ctx, m.accountingRole, target.provider, target.name, target.model)
}
func (m *failoverModel) UsageAccountingBound() bool { return m.decorate != nil }
