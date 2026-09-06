package agents

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// Observe provider failures before AgentLoop can replace them with ctx.Err().
// This also catches requests consumed by its internal retry path; observing the
// later EventError/EventRetry remains safe because the typed report is deduped.
type directUsageModel struct {
	agentcore.ChatModel
	usage      *directAgentUsage
	beforeCall func(string) error
	skipCall   func(string) error
	auditScope string
}

func (m *directUsageModel) UsageAccountingBound() bool { return m.auditScope != "" }

type directUsageAgentKey struct{}

func WithDirectUsageAgent(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, directUsageAgentKey{}, name)
}
func DirectUsageAgent(ctx context.Context, fallback string) string {
	if name, _ := ctx.Value(directUsageAgentKey{}).(string); strings.TrimSpace(name) != "" {
		return name
	}
	return fallback
}

type usageAgentContextModel struct {
	agentcore.ChatModel
	agentName string
}

func (m *usageAgentContextModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.ChatModel.Generate(WithDirectUsageAgent(ctx, m.agentName), messages, specs, opts...)
}
func (m *usageAgentContextModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return m.ChatModel.GenerateStream(WithDirectUsageAgent(ctx, m.agentName), messages, specs, opts...)
}
func (m *usageAgentContextModel) UsageAccountingBound() bool {
	bound, ok := m.ChatModel.(interface{ UsageAccountingBound() bool })
	return ok && bound.UsageAccountingBound()
}
func (m *usageAgentContextModel) ProviderName() string {
	if p, ok := m.ChatModel.(agentcore.ProviderNamer); ok {
		return p.ProviderName()
	}
	return ""
}
func (m *usageAgentContextModel) Info() llm.ModelInfo {
	if p, ok := m.ChatModel.(interface{ Info() llm.ModelInfo }); ok {
		return p.Info()
	}
	return llm.ModelInfo{}
}
func (m *usageAgentContextModel) Capabilities() llm.Capabilities {
	if p, ok := m.ChatModel.(llm.CapabilityProvider); ok {
		return p.Capabilities()
	}
	return llm.Capabilities{}
}
func (m *usageAgentContextModel) SupportsTools() bool { return m.ChatModel.SupportsTools() }

// WithDirectUsageAgentModel labels a provider-observed model without adding
// another billing wrapper; useful for grounded judges sharing a model role.
func WithDirectUsageAgentModel(model agentcore.ChatModel, agentName string) agentcore.ChatModel {
	return &usageAgentContextModel{ChatModel: model, agentName: agentName}
}

// NewAuditedUsageModel observes one already-selected provider attempt. Callers
// must place it inside failover/sampling, not around their combined result.
func NewAuditedUsageModel(ctx context.Context, model agentcore.ChatModel, agentName, provider, name, scope string, record UsageRecorder, hooks DirectUsageLifecycle) agentcore.ChatModel {
	if existing, ok := model.(*directUsageModel); ok && existing.auditScope == scope {
		return existing
	}
	agentName = DirectUsageAgent(ctx, agentName)
	if named, ok := model.(agentcore.ProviderNamer); ok && named.ProviderName() != "" {
		provider = named.ProviderName()
	}
	if info, ok := model.(interface{ Info() llm.ModelInfo }); ok {
		actual := info.Info()
		if actual.Name != "" {
			name = actual.Name
		}
		if actual.Provider != "" {
			provider = actual.Provider
		}
	}
	usage := &directAgentUsage{resolved: directAgentModelIdentity{ChatModel: model, Provider: provider, Name: name}, agentName: agentName, generationID: hooks.GenerationID, audited: true, record: func(msg agentcore.AgentMessage) {
		if record != nil {
			record(agentName, msg)
		}
	}}
	wrapped := &directUsageModel{ChatModel: model, usage: usage, auditScope: scope, skipCall: hooks.SkipCall}
	if hooks.StartCall != nil {
		wrapped.beforeCall = func(id string) error { return hooks.StartCall(id, agentName) }
	}
	return wrapped
}

type DirectUsageLifecycle struct {
	StartCall func(id, agentName string) error
	SkipCall  func(id string) error
	// Host-owned identity travels with the response into the numeric session
	// receipt; it is not model context or a prompt protocol field.
	GenerationID string
}
type directUsageLifecycleKey struct{}

// WithDirectUsageLifecycle installs host-owned accounting hooks only. It does
// not add model context, tools, capabilities or prompt protocol material.
func WithDirectUsageLifecycle(ctx context.Context, hooks DirectUsageLifecycle) context.Context {
	return context.WithValue(ctx, directUsageLifecycleKey{}, hooks)
}

func attachDirectUsageLifecycle(ctx context.Context, model *directUsageModel, agentName string) {
	hooks, _ := ctx.Value(directUsageLifecycleKey{}).(DirectUsageLifecycle)
	model.skipCall = hooks.SkipCall
	if hooks.StartCall != nil {
		model.beforeCall = func(id string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return hooks.StartCall(id, agentName)
		}
	}
}

func (m *directUsageModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := m.startCall()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, m.skipUnstarted(id))
	}
	response, err := m.ChatModel.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return response, errors.Join(err, m.finishError(err, id))
	}
	if err == nil && response != nil {
		copy := *response
		copy.Message, err = m.finishResponse(copy.Message, id)
		response = &copy
	} else if id != "" {
		err = fmt.Errorf("provider returned no response")
		m.unknownCall(id)
	}
	return response, err
}

func (m *directUsageModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := m.startCall()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, m.skipUnstarted(id))
	}
	stream, err := m.ChatModel.GenerateStream(ctx, messages, specs, opts...)
	if err != nil {
		return nil, errors.Join(err, m.finishError(err, id))
	}
	if stream == nil && id != "" {
		m.unknownCall(id)
		return nil, fmt.Errorf("provider returned no stream")
	}
	observed := make(chan agentcore.StreamEvent)
	go func() {
		defer close(observed)
		finished := false
		defer func() {
			if !finished && id != "" {
				m.unknownCall(id)
			}
		}()
		for event := range stream {
			if event.Type == agentcore.StreamEventError {
				event.Err = errors.Join(event.Err, m.finishError(event.Err, id))
				finished = true
			}
			if event.Type == agentcore.StreamEventDone {
				event.Message, event.Err = m.finishResponse(event.Message, id)
				if event.Err != nil {
					event.Type = agentcore.StreamEventError
				}
				finished = true
			}
			select {
			case observed <- event:
			case <-ctx.Done():
			}
			if event.Type == agentcore.StreamEventError || event.Type == agentcore.StreamEventDone {
				return
			}
		}
	}()
	return observed, nil
}

func (m *directUsageModel) finishResponse(message agentcore.Message, id string) (agentcore.Message, error) {
	// This is a positive local-provider receipt of no subprocess execution,
	// not an inference from absent/zero tokens. Never bill cache-only work or
	// give a later OnMessage an ID that could reopen the skipped request.
	if m.ProviderName() == "codex-cli" && message.Metadata["codex_usage_source"] == "none" {
		calls, ok := message.Metadata["codex_exec_calls"].(int)
		u := message.Usage
		if ok && calls == 0 && u != nil && u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0 && u.TotalTokens == 0 && (u.Cost == nil || u.Cost.Total == 0) {
			metadata := make(map[string]any, len(message.Metadata))
			for key, value := range message.Metadata {
				if key != "usage_audit_id" {
					metadata[key] = value
				}
			}
			message.Metadata = metadata
			return message, m.skipUnstarted(id)
		}
	}
	return m.observeResponse(message, id), nil
}

func (m *directUsageModel) observeResponse(message agentcore.Message, ids ...string) agentcore.Message {
	metadata := make(map[string]any, len(message.Metadata)+1)
	for key, value := range message.Metadata {
		metadata[key] = value
	}
	id := ""
	if len(ids) > 0 {
		id = ids[0]
	}
	if id == "" {
		id = "direct_usage_" + rand.Text()
	}
	metadata["usage_audit_id"] = id
	message.Metadata = metadata
	message = m.usage.withIdentity(message).(agentcore.Message)
	m.usage.message(message)
	return message
}

func (m *directUsageModel) startCall() (string, error) {
	if m.beforeCall == nil {
		return "", nil
	}
	id := "direct_usage_" + rand.Text()
	if err := m.beforeCall(id); err != nil {
		// Start may have appended its WAL intent before a snapshot error.
		// No provider was invoked, so make a best-effort skipped closure.
		_ = m.skipUnstarted(id)
		return "", err
	}
	return id, nil
}

func (m *directUsageModel) skipUnstarted(id string) error {
	if id != "" && m.skipCall != nil {
		return m.skipCall(id)
	}
	return nil
}

func (m *directUsageModel) finishError(err error, id string) error {
	if m.usage.observeError(err, id) {
		return nil
	}
	if id == "" {
		return nil
	} // Existing outline-all behavior remains unchanged.
	// Codex's typed usage error is produced for every executed CLI failure;
	// an unwrapped setup/validation error is known not to have executed it.
	if err != nil && m.ProviderName() == "codex-cli" && m.skipCall != nil {
		return m.skipCall(id)
	}
	m.unknownCall(id)
	return nil
}

func (m *directUsageModel) unknownCall(id string) {
	m.usage.message(agentcore.Message{Role: agentcore.RoleAssistant, Metadata: map[string]any{"usage_audit_id": id, "codex_usage_source": "unknown", "usage_accounting_gap": "provider attempt ended without a readable usage receipt", "codex_usage_breakdown": []*agentcore.Usage{nil}, "codex_usage_breakdown_complete": true}})
}

func (m *directUsageModel) ModelName() string    { return m.usage.resolved.Name }
func (m *directUsageModel) ProviderName() string { return m.usage.resolved.Provider }
func (m *directUsageModel) Info() llm.ModelInfo {
	if provider, ok := m.ChatModel.(interface{ Info() llm.ModelInfo }); ok {
		return provider.Info()
	}
	return llm.ModelInfo{Name: m.ModelName(), Provider: m.ProviderName()}
}
func (m *directUsageModel) Capabilities() llm.Capabilities {
	if provider, ok := m.ChatModel.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}

type directUsageError interface {
	error
	LLMUsage() (*agentcore.Usage, string)
}

// Keep provider accounting independent of Host so this direct runner can also
// be used without constructing a Coordinator. The observer receives the same
// request boundaries that are persisted in the replayable session log.
type directAgentUsage struct {
	mu           sync.Mutex
	resolved     directAgentModelIdentity
	record       func(agentcore.AgentMessage)
	seen         map[directUsageError]bool
	seenMessages map[string]bool
	agentName    string
	generationID string
	audited      bool
}

func (u *directAgentUsage) message(msg agentcore.AgentMessage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if message, ok := msg.(agentcore.Message); ok {
		if id, _ := message.Metadata["usage_audit_id"].(string); id != "" {
			if u.seenMessages[id] {
				return
			}
			if u.seenMessages == nil {
				u.seenMessages = make(map[string]bool)
			}
			u.seenMessages[id] = true
		}
	}
	u.record(u.withIdentity(msg))
}

func (u *directAgentUsage) withIdentity(msg agentcore.AgentMessage) agentcore.AgentMessage {
	m, ok := msg.(agentcore.Message)
	if !ok {
		return msg
	}
	if u.agentName != "" {
		metadata := make(map[string]any, len(m.Metadata)+1)
		for key, value := range m.Metadata {
			metadata[key] = value
		}
		metadata["usage_audit_agent"] = u.agentName
		if u.audited {
			metadata["stage"] = u.agentName
			metadata["generation_id"] = u.generationID
		}
		m.Metadata = metadata
	}
	if m.Usage == nil {
		return m
	}
	usage := *m.Usage
	if strings.TrimSpace(usage.Model) == "" {
		usage.Model = u.resolved.Name
	}
	if strings.TrimSpace(usage.Provider) == "" {
		usage.Provider = u.resolved.Provider
	}
	m.Usage = &usage
	return m
}

func (u *directAgentUsage) observeError(err error, ids ...string) bool {
	var report directUsageError
	if !errors.As(err, &report) {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	id := ""
	if len(ids) > 0 {
		id = ids[0]
	}
	if reflect.TypeOf(report).Comparable() {
		if u.seen[report] && id == "" {
			return true
		}
		if u.seen == nil {
			u.seen = make(map[directUsageError]bool)
		}
		u.seen[report] = true
	}
	if id == "" {
		id = "direct_usage_" + rand.Text()
	}
	if u.seenMessages[id] {
		return true
	}
	if u.seenMessages == nil {
		u.seenMessages = map[string]bool{}
	}
	u.seenMessages[id] = true
	usage, source := report.LLMUsage()
	metadata := map[string]any{"codex_usage_source": source, "direct_usage_error": true, "usage_audit_id": id}
	if detailed, ok := report.(interface {
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}); ok {
		calls, complete := detailed.LLMUsageBreakdown()
		metadata["codex_usage_breakdown"] = calls
		metadata["codex_usage_breakdown_complete"] = complete
	} else if source == "partial" || source == "unknown" {
		// A known subtotal is still billable, but never implies the absent
		// requests cost zero. Preserve that blind spot through session replay.
		metadata["codex_usage_breakdown"] = []*agentcore.Usage{usage, nil}
		metadata["codex_usage_breakdown_complete"] = true
	}
	if usage == nil {
		usage = &agentcore.Usage{}
		if _, exists := metadata["codex_usage_breakdown"]; !exists {
			metadata["codex_usage_breakdown"] = []*agentcore.Usage{nil}
			metadata["codex_usage_breakdown_complete"] = true
		}
	}
	if (source == "partial" || source == "unknown") && usage.Cost != nil {
		// Any reported cost here covers only the known requests. Price the
		// breakdown (including its reported subtotals) so missing calls remain
		// visible instead of treating the aggregate cost as a complete bill.
		partial := *usage
		partial.Cost = nil
		usage = &partial
	}
	u.record(u.withIdentity(agentcore.Message{Role: agentcore.RoleAssistant, Usage: usage, Metadata: metadata}))
	return true
}

type directAgentModelIdentity struct {
	ChatModel agentcore.ChatModel
	Provider  string
	Name      string
}

// Preserve the outline-all helper API while sharing its provider/error meter.
type outlineAllUsageModel = directUsageModel
type outlineAllOperationUsage = directAgentUsage
