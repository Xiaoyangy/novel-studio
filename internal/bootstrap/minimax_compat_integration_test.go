package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/minimax"
)

type minimaxFixtureHTTPClient func(*http.Request) (*http.Response, error)

func (f minimaxFixtureHTTPClient) Do(req *http.Request) (*http.Response, error) { return f(req) }

// Exercise the application's actual adapter, not just the nested provider
// package. All reasoning strings below are synthetic fixtures, never a trace.
func TestMiniMaxLongStreamAndToolHistoryThroughApplicationAdapter(t *testing.T) {
	const fragments = 64
	const argument = `{"message":"hello","count":2}`
	wantThinking := strings.Repeat("fixture-", fragments)
	var body strings.Builder
	emit := func(delta map[string]any, finish any) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&body, "data: %s\n\n", raw)
	}
	for i := 0; i < fragments; i++ {
		emit(map[string]any{"reasoning_content": "fixture-", "reasoning_details": []any{map[string]any{"type": "reasoning.text", "text": "fixture-"}}}, nil)
	}
	emit(map[string]any{"content": "好"}, nil)
	emit(map[string]any{"content": "好"}, nil)
	for start := 0; start < len(argument); start += 7 {
		end := min(start+7, len(argument))
		function := map[string]any{"arguments": argument[start:end]}
		call := map[string]any{"index": 0, "function": function}
		if start == 0 {
			call["id"], call["type"], function["name"] = "call_fixture", "function", "fixture_tool"
		}
		emit(map[string]any{"tool_calls": []any{call}}, nil)
	}
	emit(map[string]any{}, "tool_calls")
	body.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8,\"total_tokens\":20}}\n\n")
	var requests atomic.Int32
	provider, err := minimax.New(minimax.Config{APIKey: "synthetic-test-key", BaseURL: "https://api.minimax.cn/v1", HTTPClient: minimaxFixtureHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "api.minimax.cn" || req.URL.Path != "/v1/chat/completions" {
			return nil, fmt.Errorf("unexpected fixture route")
		}
		var payload struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role      string            `json:"role"`
				Content   any               `json:"content"`
				Reasoning json.RawMessage   `json:"reasoning_content"`
				Details   json.RawMessage   `json:"reasoning_details"`
				Calls     []json.RawMessage `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		switch requests.Add(1) {
		case 1:
			if !payload.Stream {
				return nil, fmt.Errorf("first call must stream")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}, nil
		case 2:
			if payload.Stream || len(payload.Messages) != 3 || payload.Messages[1].Role != "assistant" || payload.Messages[2].Role != "tool" {
				return nil, fmt.Errorf("tool history was not retained")
			}
			var thinking string
			if err := json.Unmarshal(payload.Messages[1].Reasoning, &thinking); err != nil || thinking != wantThinking {
				return nil, fmt.Errorf("application's textual reasoning history must remain an exact string in reasoning_content")
			}
			// agentcore v1.7.8 represents thinking history as text. It must never
			// be sent as a string in the provider's array-valued details field.
			if len(payload.Messages[1].Details) != 0 || len(payload.Messages[1].Calls) != 1 || payload.Messages[1].Content != "好好" {
				return nil, fmt.Errorf("history type or repeated text changed")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}, nil
		default:
			return nil, fmt.Errorf("unexpected extra model request")
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	client, err := litellm.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	model := NewSwappableModel("minimax", "MiniMax-M3", llm.NewLiteLLMAdapter("MiniMax-M3", client))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messages := []agentcore.Message{{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("Run the fixture tool.")}}}
	specs := []agentcore.ToolSpec{{Name: "fixture_tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"}}, "required": []string{"message", "count"}}}}
	events, err := model.GenerateStream(ctx, messages, specs)
	if err != nil {
		t.Fatal(err)
	}
	var final agentcore.Message
	done := 0
	for event := range events {
		if event.Type == agentcore.StreamEventError {
			t.Fatalf("long stream failed: %v", event.Err)
		}
		if event.Type == agentcore.StreamEventDone {
			final, done = event.Message, done+1
		}
	}
	if done != 1 || final.TextContent() != "好好" || final.Usage == nil || final.Usage.TotalTokens != 20 {
		t.Fatal("stream did not finish exactly once with exact text and trailing usage")
	}
	calls := final.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_fixture" || calls[0].Name != "fixture_tool" || string(calls[0].Args) != argument {
		t.Fatal("streamed tool arguments were not reconstructed exactly")
	}
	messages = append(messages, final, agentcore.ToolResultMsg("call_fixture", json.RawMessage(`"done"`), false))
	response, err := model.Generate(ctx, messages, specs)
	if err != nil {
		t.Fatalf("history continuation failed: %v", err)
	}
	if response.Message.TextContent() != "ok" || requests.Load() != 2 {
		t.Fatal("continuation did not complete with exactly two requests")
	}
}

func TestMiniMaxLateStreamErrorCannotTriggerApplicationTool(t *testing.T) {
	wire := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fixture-call\",\"function\":{\"name\":\"fixture_tool\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"error\":{\"message\":\"synthetic late failure\"}}\n\n"
	provider, err := minimax.New(minimax.Config{APIKey: "synthetic-test-key", BaseURL: "https://api.minimax.cn/v1", HTTPClient: minimaxFixtureHTTPClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	client, err := litellm.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	model := NewSwappableModel("minimax", "MiniMax-M3", llm.NewLiteLLMAdapter("MiniMax-M3", client))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := model.GenerateStream(ctx, []agentcore.Message{{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("fixture")}}}, []agentcore.ToolSpec{{Name: "fixture_tool", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for event := range events {
		switch event.Type {
		case agentcore.StreamEventToolCallEnd, agentcore.StreamEventDone:
			t.Fatal("failed stream could dispatch a completed tool or finish successfully")
		case agentcore.StreamEventError:
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("expected one explicit failure, got %d", failures)
	}
}
