package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

// Both parent and child are deterministic local models. The child is invoked
// by agentcore's real subagent dispatcher, never a replacement FuncTool.
type singleModeProbeModel struct {
	args  json.RawMessage
	calls atomic.Int32
}

func (m *singleModeProbeModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	call := m.calls.Add(1)
	message := agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop, Content: []agentcore.ContentBlock{agentcore.TextBlock("single child completed")}}
	if m.args != nil && call == 1 {
		message.Content = []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "single-mode-call", Name: "subagent", Args: m.args})}
		message.StopReason = agentcore.StopReasonToolUse
	}
	return &agentcore.LLMResponse{Message: message}, nil
}

func (m *singleModeProbeModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message}
	close(events)
	return events, nil
}

func (*singleModeProbeModel) SupportsTools() bool { return true }

const singleModeTask = " 保留中文、换行\n与 <exact> 任务内容 "

func singleModeArgs(fields string) json.RawMessage {
	task, _ := json.Marshal(singleModeTask)
	return json.RawMessage(`{"agent":"architect_long","task":` + string(task) + fields + `}`)
}

func runSingleModeProbe(t *testing.T, args json.RawMessage, allow bool) {
	t.Helper()
	originalArgs := append(json.RawMessage(nil), args...)
	parent, child := &singleModeProbeModel{args: args}, &singleModeProbeModel{}
	var dispatched atomic.Value
	type dispatch struct{ agent, task string }
	base := subagent.New(subagent.Config{Name: "architect_long", Description: "local probe", Model: child, MaxTurns: 1, OnMessage: func(agent, task string, _ agentcore.AgentMessage) {
		dispatched.Store(dispatch{agent, task})
	}})
	tool := singleSubagentTool{Tool: base}
	baseSchema, _ := json.Marshal(base.Schema())
	toolSchema, _ := json.Marshal(tool.Schema())
	afterSchema, _ := json.Marshal(base.Schema())
	if !bytes.Equal(baseSchema, afterSchema) || bytes.Equal(baseSchema, toolSchema) || tool.Label() != base.Label() {
		t.Fatal("single-mode schema mutated the underlying tool or lost its label")
	}
	boundaries, ended := 0, 0
	var result json.RawMessage
	for event := range agentcore.AgentLoop(context.Background(), []agentcore.AgentMessage{agentcore.UserMsg("dispatch one local task")}, agentcore.AgentContext{Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{
		Model: parent, MaxTurns: 2,
		ToolGate: combineToolGates(singleSubagentModeGate(), func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
			// Later gates must still receive the exact task contract.
			var params struct{ Agent, Task string }
			if err := json.Unmarshal(req.Call.Args, &params); err != nil || params.Agent != "architect_long" || params.Task != singleModeTask {
				return &agentcore.GateDecision{Allowed: false, Reason: "wrong single task"}, nil
			}
			return nil, nil
		}),
		Middlewares: []agentcore.ToolMiddleware{flowBoundaryMiddleware(func(string) { boundaries++ })},
	}) {
		if event.Type == agentcore.EventToolExecEnd && event.Tool == "subagent" {
			ended++
			result = event.Result
			if event.IsError == allow {
				t.Fatalf("tool outcome: allowed=%v error=%v args=%s result=%s", allow, event.IsError, args, event.Result)
			}
		}
	}
	want := int32(0)
	if allow {
		want = 1
		var got struct{ Output string }
		if err := json.Unmarshal(result, &got); err != nil || dispatched.Load() != (dispatch{"architect_long", singleModeTask}) || got.Output != "single child completed" {
			t.Fatalf("real dispatcher did not run exact single mode: result=%s err=%v", result, err)
		}
	}
	if ended != 1 || child.calls.Load() != want || boundaries != int(want) {
		t.Fatalf("unexpected dispatch counts: end=%d child=%d boundaries=%d want=%d", ended, child.calls.Load(), boundaries, want)
	}
	if !bytes.Equal(args, originalArgs) {
		t.Fatal("normalization mutated the original model tool-call buffer")
	}
}

func TestSingleSubagentEmptyOptionalModesReachRealSynchronousDispatcher(t *testing.T) {
	for _, key := range []string{"tasks", "chain", "team_name"} {
		for index, empty := range []string{`[]`, " [ \n\t ] ", `{}`, " { \n\t } ", `""`, " \n null \t "} {
			t.Run(fmt.Sprintf("%s/%d", key, index), func(t *testing.T) {
				runSingleModeProbe(t, singleModeArgs(`,"`+key+`":`+empty), true)
			})
		}
	}
	for name, fields := range map[string]string{
		"absent":             "",
		"reported-issue":     `,"tasks":[],"team_name":""`,
		"all-empty":          `,"tasks":{},"chain":"","team_name":[],"background":false`,
		"null-background":    `,"tasks":null,"chain":null,"team_name":null,"background":null`,
		"case-alias-empties": `,"Tasks":{},"CHAIN":[],"TEAM_NAME":""`,
	} {
		t.Run(name, func(t *testing.T) { runSingleModeProbe(t, singleModeArgs(fields), true) })
	}
}

func TestSingleSubagentNonemptyAndInvalidModesNeverDispatch(t *testing.T) {
	for _, key := range []string{"tasks", "chain", "team_name"} {
		for index, value := range []string{`false`, `true`, `0`, `1`, `" "`, `"[]"`, `"{}"`, `"null"`, `[null]`, `[""]`, `[{}]`, `[[]]`, `{"unused":null}`, `[{"agent":"architect_long","task":"parallel"}]`} {
			t.Run(fmt.Sprintf("%s/%d", key, index), func(t *testing.T) {
				runSingleModeProbe(t, singleModeArgs(`,"`+key+`":`+value), false)
			})
		}
	}
	for name, args := range map[string]json.RawMessage{
		"background":         singleModeArgs(`,"background":true`),
		"background-number":  singleModeArgs(`,"background":0`),
		"real-parallel":      json.RawMessage(`{"tasks":[{"agent":"architect_long","task":"a"},{"agent":"architect_long","task":"b"}]}`),
		"real-chain":         json.RawMessage(`{"chain":[{"agent":"architect_long","task":"a"},{"agent":"architect_long","task":"{previous}"}]}`),
		"case-alias":         singleModeArgs(`,"Tasks":[{"agent":"architect_long","task":"a"}]`),
		"unicode-case-alias": singleModeArgs(`,"ta\u017fks":[{"agent":"architect_long","task":"a"}]`),
		"conflicting-alias":  singleModeArgs(`,"tasks":[],"Tasks":[{"agent":"architect_long","task":"a"}]`),
		"null-object":        json.RawMessage(`null`),
		"invalid-json":       json.RawMessage(`{"tasks":[}`),
		"missing-agent":      json.RawMessage(`{"task":"a","tasks":[]}`),
		"missing-task":       json.RawMessage(`{"agent":"architect_long","chain":{}}`),
		"empty-task":         json.RawMessage(`{"agent":"architect_long","task":"","team_name":null}`),
	} {
		t.Run(name, func(t *testing.T) { runSingleModeProbe(t, args, false) })
	}
}

func TestSingleSubagentNormalizationIsExactAndDefensive(t *testing.T) {
	args := singleModeArgs(`,"tasks":{ },"chain":[ ],"team_name":"","background":false,"description":"unchanged"`)
	normalized, err := normalizeSingleSubagentArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(normalized, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"agent": "architect_long", "task": singleModeTask, "background": false, "description": "unchanged"}) {
		t.Fatalf("normalization changed unrelated fields or retained modes: %v", got)
	}
	for _, invalid := range []json.RawMessage{nil, json.RawMessage(" "), json.RawMessage(`false`), json.RawMessage(`0`), json.RawMessage(`[null]`), json.RawMessage(`{"x":null}`)} {
		if emptySingleSubagentModeValue(invalid) {
			t.Fatalf("invalid/nonempty mode was classified as empty: %q", invalid)
		}
	}
	child := &singleModeProbeModel{}
	tool := singleSubagentTool{Tool: subagent.New(subagent.Config{Name: "architect_long", Model: child, MaxTurns: 1})}
	if _, err := tool.Execute(context.Background(), singleModeArgs(`,"tasks":[{}]`)); err == nil || child.calls.Load() != 0 {
		t.Fatal("direct execution bypassed the single-writer restriction")
	}
	for _, invalid := range []string{`{"agent":"architect_long","tasks":[]}`, `{"task":"a","chain":{}}`, `{"agent":"architect_long","task":"","team_name":null}`} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(invalid)); err == nil || child.calls.Load() != 0 {
			t.Fatal("empty optional modes manufactured a missing agent/task")
		}
	}
	if decision, err := singleSubagentModeGate()(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{Name: "other", Args: json.RawMessage(`{"tasks":[{}]}`)}}); err != nil || decision != nil {
		t.Fatal("subagent gate changed another tool's arguments")
	}
	clean := singleModeArgs("")
	untouched, err := normalizeSingleSubagentArgs(clean)
	if err != nil || !bytes.Equal(clean, untouched) || strings.Contains(string(untouched), `"tasks"`) {
		t.Fatal("already valid single call was needlessly rewritten")
	}
}
