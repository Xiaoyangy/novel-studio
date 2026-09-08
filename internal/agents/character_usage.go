package agents

import (
	"context"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/models"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type characterUsageModel struct {
	agentcore.ChatModel
	started atomic.Int32
}

func (m *characterUsageModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.started.Add(1)
	response, err := m.ChatModel.Generate(ctx, messages, specs, opts...)
	m.excludePreExecutionFailure(err)
	return response, err
}

func (m *characterUsageModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.started.Add(1)
	events, err := m.ChatModel.GenerateStream(ctx, messages, specs, opts...)
	m.excludePreExecutionFailure(err)
	return events, err
}

func (m *characterUsageModel) excludePreExecutionFailure(err error) {
	// The Codex adapter wraps every failure after a CLI execution in LLMUsage,
	// including unknown-token executions. An unwrapped setup/validation error
	// therefore must not be reported as an executed or billable model attempt.
	if err != nil && m.ProviderName() == "codex-cli" {
		var usageError characterUsageError
		if !errors.As(err, &usageError) {
			m.started.Add(-1)
		}
	}
}

func (m *characterUsageModel) ModelName() string { return characterUsageModelName(m.ChatModel) }
func (m *characterUsageModel) ProviderName() string {
	if provider, ok := m.ChatModel.(agentcore.ProviderNamer); ok {
		return provider.ProviderName()
	}
	return ""
}
func (m *characterUsageModel) Info() llm.ModelInfo {
	if provider, ok := m.ChatModel.(interface{ Info() llm.ModelInfo }); ok {
		return provider.Info()
	}
	return llm.ModelInfo{Name: m.ModelName(), Provider: m.ProviderName()}
}
func (m *characterUsageModel) Capabilities() llm.Capabilities {
	if provider, ok := m.ChatModel.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}

func characterUsageModelName(model agentcore.ChatModel) string {
	if named, ok := model.(agentcore.ModelNamer); ok && strings.TrimSpace(named.ModelName()) != "" {
		return strings.TrimSpace(named.ModelName())
	}
	return strings.TrimSpace(bootstrap.ModelName(model))
}

type characterAgentLoopUsage struct {
	agentcore.Usage
	CostUSD       float64
	CostSource    string
	Models        []string
	Attempts      int
	UnpricedCalls int
	outerCalls    int
	seenErrors    map[characterUsageError]bool
}

type characterUsageError interface {
	error
	LLMUsage() (*agentcore.Usage, string)
}

func (u *characterAgentLoopUsage) unknown() {
	u.CostSource = models.CostSourceUnknown
	u.UnpricedCalls++
}

func (u *characterAgentLoopUsage) identity(model agentcore.ChatModel, usage *agentcore.Usage) (string, string) {
	provider, name := "", ""
	if usage != nil {
		provider, name = strings.TrimSpace(usage.Provider), strings.TrimSpace(usage.Model)
	}
	configured := characterUsageModelName(model)
	if name == "" {
		name = configured
	}
	if provider == "" && name == configured {
		if named, ok := model.(agentcore.ProviderNamer); ok {
			provider = strings.TrimSpace(named.ProviderName())
		}
	}
	identity := name
	if provider != "" && name != "" {
		identity = provider + "/" + name
	}
	if identity != "" {
		if len(u.Models) == 0 {
			u.Provider, u.Model = provider, name
		} else {
			if u.Provider != provider {
				u.Provider = "mixed"
			}
			if u.Model != name {
				u.Model = "mixed"
			}
		}
		u.Models = compactAgentStrings(append(u.Models, identity))
	}
	return provider, name
}

func (u *characterAgentLoopUsage) price(model agentcore.ChatModel, usage *agentcore.Usage) {
	if usage == nil {
		u.unknown()
		return
	}
	_, name := u.identity(model, usage)
	entry, _ := models.DefaultRegistry().Resolve(name)
	var reported *float64
	if usage.Cost != nil {
		reported = &usage.Cost.Total
	}
	quote := models.ResolveTokenCost(models.TokenUsage{Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite}, entry, reported)
	u.CostUSD += quote.USD
	if quote.Source == models.CostSourceUnknown {
		u.unknown()
	} else if u.CostSource == "" || (u.CostSource == models.CostSourceReported && quote.Source == models.CostSourceEstimated) {
		u.CostSource = quote.Source
	}
}

func (u *characterAgentLoopUsage) observe(model agentcore.ChatModel, usage *agentcore.Usage, metadata map[string]any) {
	u.outerCalls++
	u.Usage.Add(usage)
	u.identity(model, usage)
	if raw, exists := metadata["codex_usage_breakdown"]; exists && (usage == nil || usage.Cost == nil) {
		calls, err := models.DecodeTokenUsageBreakdown(raw)
		complete, _ := metadata["codex_usage_breakdown_complete"].(bool)
		if err != nil || len(calls) == 0 {
			u.Attempts++
			u.unknown()
			return
		}
		u.Attempts += len(calls)
		for _, call := range calls {
			if call == nil {
				u.unknown()
				continue
			}
			part := &agentcore.Usage{Provider: call.Provider, Model: call.Model, Input: call.Input, Output: call.Output, CacheRead: call.CacheRead, CacheWrite: call.CacheWrite}
			if usage != nil {
				if part.Model == "" {
					part.Model = usage.Model
				}
				if part.Provider == "" {
					part.Provider = usage.Provider
				}
			}
			if call.Cost != nil {
				part.Cost = &agentcore.Cost{Total: call.Cost.Total}
			}
			u.price(model, part)
		}
		if !complete {
			u.Attempts++ // at least one omitted request; its cost remains unknown
			u.unknown()
		}
		return
	}
	u.Attempts++
	if usage != nil && usage.Cost == nil {
		provider, name := u.identity(model, usage)
		if entry, ok := models.DefaultRegistry().Resolve(name); ok && provider == "codex-cli" &&
			models.TokenUsageCrossesPricingTier(models.TokenUsage{Input: usage.Input, CacheWrite: usage.CacheWrite}, *entry) {
			u.unknown()
			return
		}
	}
	u.price(model, usage)
}

func (u *characterAgentLoopUsage) observeError(model agentcore.ChatModel, err error) {
	var report characterUsageError
	if !errors.As(err, &report) {
		return
	}
	if reflect.TypeOf(report).Comparable() {
		if u.seenErrors[report] {
			return
		}
		if u.seenErrors == nil {
			u.seenErrors = make(map[characterUsageError]bool)
		}
		u.seenErrors[report] = true
	}
	usage, source := report.LLMUsage()
	metadata := map[string]any{}
	if detailed, ok := report.(interface {
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}); ok {
		calls, complete := detailed.LLMUsageBreakdown()
		metadata["codex_usage_breakdown"] = calls
		metadata["codex_usage_breakdown_complete"] = complete
	}
	u.observe(model, usage, metadata)
	if (source == "partial" || source == "unknown") && u.CostSource != models.CostSourceUnknown {
		u.unknown()
	}
}

func (u *characterAgentLoopUsage) finish(model agentcore.ChatModel, started int) {
	for range max(0, started-u.outerCalls) {
		u.Attempts++
		u.unknown()
	}
	if len(u.Models) == 0 {
		u.identity(model, nil)
	}
	if u.CostSource == "" {
		u.CostSource = models.CostSourceUnknown
	}
}

func appendCharacterLoopUsage(st *store.Store, record domain.CharacterAgentUsage, usage characterAgentLoopUsage, runErr error, observers ...func(domain.CharacterAgentUsage) error) error {
	if usage.Attempts == 0 {
		return nil
	}
	if record.UsageID == "" {
		record.UsageID = "ca_usage_" + rand.Text()
	}
	record.Input, record.Output, record.CacheRead, record.CacheWrite = usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite
	record.CostUSD, record.CostSource = usage.CostUSD, usage.CostSource
	record.Model, record.Provider = usage.Model, usage.Provider
	if len(usage.Models) > 1 {
		record.Models = append([]string(nil), usage.Models...)
	}
	record.Attempts, record.UnpricedCalls = usage.Attempts, usage.UnpricedCalls
	record.Status = "success"
	if runErr != nil {
		record.Status, record.ErrorCategory = "failed", "model_or_submission"
		if errors.Is(runErr, context.Canceled) {
			record.Status, record.ErrorCategory = "canceled", "context_canceled"
		} else if errors.Is(runErr, context.DeadlineExceeded) {
			record.ErrorCategory = "deadline_exceeded"
		}
	}
	if err := st.CharacterAgents.AppendUsage(record); err != nil {
		return err
	}
	for _, observer := range observers {
		if observer != nil {
			if err := observer(record); err != nil {
				return err
			}
		}
	}
	return nil
}
