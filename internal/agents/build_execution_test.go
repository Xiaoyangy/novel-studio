package agents

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/agentcore"
)

func TestSingleSubagentModeGateRejectsSharedStateParallelism(t *testing.T) {
	gate := singleSubagentModeGate()
	for _, args := range []string{
		`{"tasks":[{"agent":"writer","task":"a"}]}`,
		`{"chain":[{"agent":"writer","task":"a"}]}`,
		`{"agent":"writer","task":"a","background":true}`,
		`{"agent":"writer","task":"a","team_name":"writers"}`,
	} {
		decision, err := gate(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{Name: "subagent", Args: json.RawMessage(args)}})
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || decision.Allowed {
			t.Fatalf("expected mode to be rejected: %s", args)
		}
	}
}

func TestSingleSubagentModeGateAllowsSynchronousSingleTask(t *testing.T) {
	decision, err := singleSubagentModeGate()(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent",
		Args: json.RawMessage(`{"agent":"writer","task":"写第 1 章"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if decision != nil {
		t.Fatalf("single task should pass through, got %+v", decision)
	}
}
