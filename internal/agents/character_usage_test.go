package agents

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/models"
	"github.com/voocel/agentcore"
)

type characterUsageTestModel struct {
	characterConcurrencyProbeModel
	name     string
	usage    *agentcore.Usage
	metadata map[string]any
	failure  error
}

func (m *characterUsageTestModel) ModelName() string    { return m.name }
func (m *characterUsageTestModel) ProviderName() string { return "codex-cli" }
func (m *characterUsageTestModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.failure != nil {
		return nil, m.failure
	}
	response := m.response(messages)
	response.Message.Usage = m.usage
	response.Message.Metadata = m.metadata
	return response, nil
}
func (m *characterUsageTestModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message, StopReason: response.Message.StopReason}
	close(events)
	return events, nil
}

func TestCharacterUsagePricesActualModelAndPerCLIRequestBeforeAggregation(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 1)
	model := &characterUsageTestModel{
		name:  "claude-sonnet-4", // Actual response identity must take precedence.
		usage: &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 300_000},
		metadata: map[string]any{
			"codex_usage_breakdown_complete": true,
			"codex_usage_breakdown":          []*agentcore.Usage{{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000}, {Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000}},
		},
	}
	if err := runOneCharacterAgent(context.Background(), bootstrap.Config{}, st, model, observations[ids[0]]); err != nil {
		t.Fatal(err)
	}
	rows, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing successful sidecar usage: %+v %v", rows, err)
	}
	row := rows[0]
	if row.CostUSD != 3 || row.CostSource != "estimated" || row.Model != "gpt-6-astra" || row.Provider != "codex-cli" ||
		row.Attempts != 2 || row.UnpricedCalls != 0 || row.Status != "success" || row.UsageID == "" || row.Input != 300_000 {
		t.Fatalf("sidecar used aggregate/context/configured-model pricing: %+v", row)
	}
}

func TestCharacterUsageReportedCostFallbackAndUnknownAreDistinct(t *testing.T) {
	model := &characterUsageTestModel{name: "gpt-6-astra"}
	for _, reported := range []float64{0, 0.25} {
		var summary characterAgentLoopUsage
		summary.observe(model, &agentcore.Usage{Input: 300_000, Cost: &agentcore.Cost{Total: reported}}, nil)
		summary.finish(model, 1)
		if summary.CostUSD != reported || summary.CostSource != "reported" || summary.Model != "gpt-6-astra" {
			t.Fatalf("provider cost did not override fallback: %+v", summary)
		}
	}
	var fallback characterAgentLoopUsage
	fallback.observe(model, &agentcore.Usage{Input: 100_000, Output: 1000, CacheRead: 80_000}, nil)
	want := 20_000*10.0/1e6 + 80_000*1.0/1e6 + 1000*50.0/1e6
	if math.Abs(fallback.CostUSD-want) > 1e-12 || fallback.CostSource != models.CostSourceEstimated {
		t.Fatalf("known-model cache-aware fallback failed: %+v", fallback)
	}
	var unknown characterAgentLoopUsage
	unknown.observe(model, &agentcore.Usage{Model: "unregistered-custom-model", Input: 100}, nil)
	if unknown.CostSource != "unknown" || unknown.CostUSD != 0 || unknown.UnpricedCalls != 1 || unknown.Model != "unregistered-custom-model" {
		t.Fatalf("unknown model was billed as configured model or measured free usage: %+v", unknown)
	}
	var noBoundaries characterAgentLoopUsage
	noBoundaries.observe(model, &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 300_000}, nil)
	if noBoundaries.CostSource != "unknown" || noBoundaries.CostUSD != 0 || noBoundaries.Input != 300_000 {
		t.Fatalf("CLI aggregate without request boundaries received a guessed long-context tier: %+v", noBoundaries)
	}
}

type characterUsageTestError struct {
	usage  *agentcore.Usage
	calls  []*agentcore.Usage
	source string
}

func (*characterUsageTestError) Error() string                          { return "simulated model cancellation" }
func (*characterUsageTestError) Unwrap() error                          { return context.Canceled }
func (e *characterUsageTestError) LLMUsage() (*agentcore.Usage, string) { return e.usage, e.source }
func (e *characterUsageTestError) LLMUsageBreakdown() ([]*agentcore.Usage, bool) {
	return e.calls, true
}

func TestCharacterUsageFailurePersistsPartialUsageAndUniqueAttempts(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 1)
	partial := &agentcore.Usage{Provider: "codex-cli", Model: "gpt-6-astra", Input: 150_000}
	failure := &characterUsageTestError{usage: partial, calls: []*agentcore.Usage{partial, nil}, source: "partial"}
	model := &characterUsageTestModel{name: "gpt-6-astra", failure: failure}
	for range 2 {
		if err := runOneCharacterAgent(context.Background(), bootstrap.Config{}, st, model, observations[ids[0]]); !errors.Is(err, context.Canceled) {
			t.Fatalf("failure cause lost: %v", err)
		}
	}
	rows, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(rows) != 2 {
		t.Fatalf("failed actual attempts lost their usage: %+v %v", rows, err)
	}
	if rows[0].UsageID == "" || rows[0].UsageID == rows[1].UsageID {
		t.Fatal("identical failed retries share an audit identity")
	}
	for _, row := range rows {
		if row.CostUSD != 1.5 || row.CostSource != "unknown" || row.UnpricedCalls != 1 || row.Input != 150_000 ||
			row.Attempts != 2 || row.Status != "canceled" || row.ErrorCategory != "context_canceled" {
			t.Fatalf("partial failure was reported as full or free usage: %+v", row)
		}
	}
	if proposal, err := st.CharacterAgents.LoadProposal(observations[ids[0]].GenerationID, 1, 1, ids[0]); err != nil || proposal != nil {
		t.Fatal("failed provider created a character decision")
	}
	var deduplicated characterAgentLoopUsage
	deduplicated.observeError(model, failure)
	deduplicated.observeError(model, fmt.Errorf("retry wrapper: %w", failure))
	if deduplicated.Input != 150_000 || deduplicated.Attempts != 2 {
		t.Fatalf("the same typed failure was counted twice: %+v", deduplicated)
	}
}

func TestCharacterUsageFailureWithoutTokenReceiptIsUnknown(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 1)
	model := &characterUsageTestModel{name: "gpt-6-astra", failure: &characterUsageTestError{calls: []*agentcore.Usage{nil}, source: "unknown"}}
	if err := runOneCharacterAgent(context.Background(), bootstrap.Config{}, st, model, observations[ids[0]]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(rows) != 1 || rows[0].Input != 0 || rows[0].CostSource != "unknown" || rows[0].UnpricedCalls != 1 {
		t.Fatalf("unknown failure was omitted or presented as measured zero: %+v %v", rows, err)
	}
}

func TestCharacterUsageCodexPreExecutionFailureDoesNotInventUsage(t *testing.T) {
	st, observations, ids := seedCharacterRound(t, 1)
	model := &characterUsageTestModel{name: "gpt-6-astra", failure: context.Canceled}
	if err := runOneCharacterAgent(context.Background(), bootstrap.Config{}, st, model, observations[ids[0]]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if rows, err := st.CharacterAgents.LoadUsage(); err != nil || len(rows) != 0 {
		t.Fatalf("Codex pre-execution failure fabricated a model usage record: %+v %v", rows, err)
	}
}
