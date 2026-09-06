package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type outlineAllTestUsageError struct {
	usage  *agentcore.Usage
	calls  []*agentcore.Usage
	source string
}

func (*outlineAllTestUsageError) Error() string                          { return "fake model failed after execution" }
func (e *outlineAllTestUsageError) LLMUsage() (*agentcore.Usage, string) { return e.usage, e.source }
func (e *outlineAllTestUsageError) LLMUsageBreakdown() ([]*agentcore.Usage, bool) {
	return e.calls, true
}

type outlineAllFailingUsageModel struct {
	err    error
	cancel context.CancelFunc
}

func (m *outlineAllFailingUsageModel) SupportsTools() bool { return true }
func (m *outlineAllFailingUsageModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, m.err
}
func (m *outlineAllFailingUsageModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	if m.cancel != nil {
		m.cancel()
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: m.err}
	close(events)
	return events, nil
}

func TestOutlineAllUsageDirectModelSuccessAndFailure(t *testing.T) {
	for _, mode := range []string{"success", "failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			failed := mode != "success"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			usage := &agentcore.Usage{Input: 300_000, Output: 20}
			calls := []*agentcore.Usage{{Input: 150_000, Output: 10}, {Input: 150_000, Output: 10}}
			response := outlineAllOperationToolUseResponse("save-usage")
			response.Usage = usage
			response.Metadata = map[string]any{"codex_usage_breakdown": calls, "codex_usage_breakdown_complete": true}
			var model agentcore.ChatModel = &outlineAllOperationCaptureModel{response: response}
			var failure *outlineAllTestUsageError
			if failed {
				failure = &outlineAllTestUsageError{usage: usage, calls: calls, source: "reported"}
				failing := &outlineAllFailingUsageModel{err: failure}
				if mode == "canceled" {
					failing.cancel = cancel
				}
				model = failing
			}
			saveTool := agentcore.NewFuncTool("save_foundation", "test", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
			})
			var observed []agentcore.Message
			err := runOutlineAllOperationWithModel(ctx, bootstrap.Config{}, assets.Bundle{}, st, outlineAllOperationTask(t, 1, 1, 1, 1),
				outlineAllOperationModel{ChatModel: model, Provider: "codex-cli", Name: "gpt-6-astra"}, saveTool,
				func(name string, msg agentcore.AgentMessage) {
					if name != "architect_outline_all" {
						t.Errorf("unexpected agent %q", name)
					}
					if m, ok := msg.(agentcore.Message); ok && m.Usage != nil {
						observed = append(observed, m)
					}
				})
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if mode == "failure" && !errors.Is(err, failure) {
				t.Fatalf("lost original failure: %v", err)
			}
			if !failed && err != nil {
				t.Fatal(err)
			}
			if len(observed) != 1 {
				t.Fatalf("recorded %d usage messages, want 1", len(observed))
			}
			msg := observed[0]
			if msg.Usage.Provider != "codex-cli" || msg.Usage.Model != "gpt-6-astra" || msg.Usage.Input != 300_000 {
				t.Fatalf("incorrect usage identity: %+v", msg.Usage)
			}
			if got, ok := msg.Metadata["codex_usage_breakdown"].([]*agentcore.Usage); !ok || len(got) != 2 {
				t.Fatalf("lost request breakdown: %#v", msg.Metadata)
			}
			if usage.Model != "" || usage.Provider != "" {
				t.Fatalf("observer modified provider response: %+v", usage)
			}
		})
	}
}

func TestOutlineAllUsageErrorDedupAndUnknownPreserved(t *testing.T) {
	var observed []agentcore.Message
	tracker := outlineAllOperationUsage{resolved: outlineAllOperationModel{Provider: "codex-cli", Name: "gpt-6-astra"}, record: func(msg agentcore.AgentMessage) { observed = append(observed, msg.(agentcore.Message)) }}
	err := &outlineAllTestUsageError{source: "unknown", calls: []*agentcore.Usage{nil}}
	tracker.observeError(err)
	tracker.observeError(fmt.Errorf("retry wrapper: %w", err))
	tracker.observeError(errors.New("pre-execution failure"))
	if len(observed) != 1 {
		t.Fatalf("duplicate error accounting: %d", len(observed))
	}
	msg := observed[0]
	if msg.Usage == nil || msg.Metadata["codex_usage_source"] != "unknown" {
		t.Fatalf("unknown usage is not replayable: %+v", msg)
	}
	calls := msg.Metadata["codex_usage_breakdown"].([]*agentcore.Usage)
	if len(calls) != 1 || calls[0] != nil {
		t.Fatalf("unknown call became zero-cost usage: %+v", calls)
	}
	actual := agentcore.Message{Usage: &agentcore.Usage{Provider: "actual-provider", Model: "actual-model", Input: 1}}
	tracker.message(actual)
	if got := observed[1].Usage; got.Provider != "actual-provider" || got.Model != "actual-model" {
		t.Fatalf("overwrote actual model with configured model: %+v", got)
	}
}
