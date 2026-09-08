package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/voocel/agentcore"
)

type directLifecycleModel struct {
	agentcore.ChatModel
	provider          string
	calls             int
	failure           error
	onCall            func()
	closeWithoutUsage bool
	cacheOnly         bool
}

func (m *directLifecycleModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	stream := make(chan agentcore.StreamEvent, 1)
	if !m.closeWithoutUsage {
		stream <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message}
	}
	close(stream)
	return stream, nil
}

func (m *directLifecycleModel) ProviderName() string { return m.provider }
func (m *directLifecycleModel) ModelName() string    { return "lifecycle-test" }
func (m *directLifecycleModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if m.onCall != nil {
		m.onCall()
	}
	if m.failure != nil {
		return nil, m.failure
	}
	if m.cacheOnly {
		return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{}, Metadata: map[string]any{"codex_exec_calls": 0, "codex_usage_source": "none"}}}, nil
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{Input: 10, Output: 2}}}, nil
}

func TestAuditedDirectUsageSkipsOnlyExplicitUnexecutedCacheReceipt(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		starts, skips, records := 0, 0, 0
		model := NewAuditedUsageModel(context.Background(), &directLifecycleModel{provider: "codex-cli", cacheOnly: true}, "drafter", "codex-cli", "test", "host-test", func(string, agentcore.AgentMessage) { records++ }, DirectUsageLifecycle{StartCall: func(string, string) error { starts++; return nil }, SkipCall: func(string) error { skips++; return nil }})
		if streaming {
			stream, err := model.GenerateStream(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for event := range stream {
				if event.Err != nil {
					t.Fatal(event.Err)
				}
				if event.Message.Metadata["usage_audit_id"] != nil {
					t.Fatal("skipped request leaked billable ID")
				}
			}
		} else {
			response, err := model.Generate(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.Message.Metadata["usage_audit_id"] != nil {
				t.Fatal("skipped request leaked billable ID")
			}
		}
		if starts != 1 || skips != 1 || records != 0 {
			t.Fatalf("cached request was charged: %d/%d/%d", starts, skips, records)
		}
	}
}

func TestProjectedDirectUsageLifecycleBindsStartedIDToReportedResponse(t *testing.T) {
	started := ""
	records := []agentcore.Message{}
	hooks := ProjectedPlanningAccounting{StartCall: func(id, role string) error {
		if role != "project_all_planner" {
			t.Fatal(role)
		}
		started = id
		return nil
	}, RecordUsage: func(_ string, raw agentcore.AgentMessage) { records = append(records, raw.(agentcore.Message)) }}
	ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, hooks)
	base := &directLifecycleModel{onCall: func() {
		if started == "" {
			t.Fatal("provider started before durable call identity hook")
		}
	}}
	model, observer := projectedAccountingModel(ctx, base, "project_all_planner", "")
	response, err := model.Generate(ctx, []agentcore.Message{agentcore.UserMsg("PRIVATE PROMPT")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observer(response.Message)
	if base.calls != 1 || len(records) != 1 || records[0].Metadata["usage_audit_id"] != started {
		t.Fatalf("call identity was lost or charged twice: calls=%d records=%+v", base.calls, records)
	}
}

func TestProjectedDirectUsageLifecycleDoesNotPriceUnstartedRequests(t *testing.T) {
	for _, mode := range []string{"context", "start-failure", "codex-setup"} {
		t.Run(mode, func(t *testing.T) {
			starts, skips, records := 0, 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hooks := ProjectedPlanningAccounting{StartCall: func(string, string) error {
				starts++
				if mode == "start-failure" {
					return errors.New("WAL refused")
				}
				return nil
			}, SkipCall: func(string) error { skips++; return nil }, RecordUsage: func(string, agentcore.AgentMessage) { records++ }}
			ctx = context.WithValue(ctx, projectedPlanningAccountingKey{}, hooks)
			base := &directLifecycleModel{provider: "codex-cli"}
			if mode == "context" {
				cancel()
			}
			if mode == "codex-setup" {
				base.failure = errors.New("invalid local setup")
			}
			model, _ := projectedAccountingModel(ctx, base, "project_all_planner", "")
			if _, err := model.Generate(ctx, nil, nil); err == nil {
				t.Fatal("expected setup/cancellation error")
			}
			if records != 0 {
				t.Fatal("an unstarted request was priced or marked as a usage failure")
			}
			if mode == "context" && (starts != 0 || base.calls != 0) {
				t.Fatal("pre-canceled request reached provider/start WAL")
			}
			if mode == "start-failure" && base.calls != 0 {
				t.Fatal("provider started without durable start")
			}
			if mode == "codex-setup" && (starts != 1 || skips != 1) {
				t.Fatal("known unexecuted Codex request did not close as skipped")
			}
		})
	}
}

func TestProjectedDirectUsageLifecycleBindsTypedFailureToStartedID(t *testing.T) {
	var id string
	var messages []agentcore.Message
	part := &agentcore.Usage{Input: 23, Output: 4}
	failure := &characterUsageTestError{usage: part, source: "partial", calls: []*agentcore.Usage{part, nil}}
	ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{StartCall: func(call, _ string) error { id = call; return nil }, RecordUsage: func(_ string, raw agentcore.AgentMessage) { messages = append(messages, raw.(agentcore.Message)) }})
	base := &directLifecycleModel{provider: "codex-cli", failure: failure}
	model, _ := projectedAccountingModel(ctx, base, "world_arbiter", "")
	if _, err := model.Generate(ctx, nil, nil); err == nil {
		t.Fatal("typed failure lost")
	}
	model.(*directUsageModel).usage.observeError(failure)
	if len(messages) != 1 || messages[0].Metadata["usage_audit_id"] != id || messages[0].Usage.Input != 23 {
		t.Fatalf("typed failure lost subtotal or duplicated observation: %+v", messages)
	}
}

func TestProjectedDirectUsageStreamingLifecycleClosesReportedOrUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		var started string
		var records []agentcore.Message
		ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{StartCall: func(id, _ string) error { started = id; return nil }, RecordUsage: func(_ string, raw agentcore.AgentMessage) { records = append(records, raw.(agentcore.Message)) }})
		base := &directLifecycleModel{closeWithoutUsage: unknown}
		model, observer := projectedAccountingModel(ctx, base, "project_all_planner", "")
		stream, err := model.GenerateStream(ctx, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for event := range stream {
			if event.Type == agentcore.StreamEventDone {
				observer(event.Message)
			}
		}
		if len(records) != 1 || records[0].Metadata["usage_audit_id"] != started {
			t.Fatalf("stream lost exact started closure: %+v", records)
		}
		if unknown && records[0].Metadata["codex_usage_source"] != "unknown" {
			t.Fatal("missing stream receipt became a measured zero")
		}
	}
}
