package compat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/internal/testgolden"
)

func TestStreamCumulativeReasoningAndToolDeltas(t *testing.T) {
	stream := streamFromSSE(t,
		testgolden.ReadFixtureString(t, "../../testdata/compat/minimax_stream.sse"),
		Spec{
			Name: "minimax",
			Stream: StreamSpec{
				ReasoningFields:     []string{"reasoning_content"},
				ReasoningCumulative: true,
				ContentCumulative:   true,
			},
		},
		&litellm.Request{Model: "minimax-text-01", Messages: []litellm.Message{litellm.UserText("hi")}},
	)
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Reasoning() != "ab" || resp.Text() != "hi" {
		t.Fatalf("reasoning/text = %q/%q", resp.Reasoning(), resp.Text())
	}
	if len(resp.Blocks) == 0 {
		t.Fatalf("blocks is empty")
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok {
		t.Fatalf("first block = %T", resp.Blocks[0])
	}
	var details []map[string]any
	if err := json.Unmarshal(reasoning.Extra, &details); err != nil {
		t.Fatalf("reasoning extra: %v", err)
	}
	if len(details) != 1 || details[0]["text"] != "ab" {
		t.Fatalf("reasoning details = %#v", details)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 1 || resp.Usage.OutputTokens != 2 || resp.FinishReason != litellm.FinishReasonToolCall {
		t.Fatalf("usage/finish = %+v/%q", resp.Usage, resp.FinishReason)
	}
}

func TestStreamCumulativeContentRejectsRewrite(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"abc"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"ax"}}]}`,
		``,
	}, "\n"), Spec{Name: "minimax", Stream: StreamSpec{ContentCumulative: true}}, nil)
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "cumulative content stream changed") {
		t.Fatalf("expected cumulative content error, got %v", err)
	}
}

func TestStreamCumulativeContentCanDependOnThinking(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"h"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"i"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{
		Name: "minimax",
		Stream: StreamSpec{
			ContentCumulative:          true,
			ContentCumulativeCondition: "thinking_enabled",
		},
		Request: RequestSpec{
			Thinking: func(*litellm.Thinking, string) (map[string]any, error) {
				return map[string]any{"thinking": map[string]any{"type": "disabled"}}, nil
			},
		},
	}, &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hi" {
		t.Fatalf("text = %q", resp.Text())
	}
}

func TestStreamConvertsRefusalAndCachedTokens(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"refusal":"no"}}]}`,
		`data: {"choices":[{"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":6}}}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "strict"}, nil)
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "no" || resp.Usage.CacheReadTokens != 6 || resp.FinishReason != litellm.FinishReasonSafety {
		t.Fatalf("response = text %q usage %+v finish %q", resp.Text(), resp.Usage, resp.FinishReason)
	}
}

func TestStreamPrependsStrictToolOmittedWarning(t *testing.T) {
	tool := mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})
	tool.Strict = litellm.StrictEnabled
	stream := streamFromSSE(t, "data: [DONE]\n\n", Spec{
		Name:     "strictless",
		Features: FeatureSpec{StrictTools: StrictToolsOmit},
	}, &litellm.Request{Model: "m", Messages: []litellm.Message{litellm.UserText("hi")}, Tools: []litellm.Tool{tool}})
	defer stream.Close()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	warning, ok := event.(litellm.WarningEvent)
	if !ok {
		t.Fatalf("first event = %#v, want WarningEvent", event)
	}
	if warning.Warning.Code != "request.strict_tool_omitted" || warning.Warning.Provider != "strictless" {
		t.Fatalf("warning = %#v", warning.Warning)
	}
}

func TestStreamCumulativeReasoningRejectsRewrite(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"abc"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"ax"}}]}`,
		``,
	}, "\n"), Spec{Name: "minimax", Stream: StreamSpec{ReasoningFields: []string{"reasoning_content"}, ReasoningCumulative: true}}, nil)
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "cumulative reasoning stream changed") {
		t.Fatalf("expected cumulative reasoning error, got %v", err)
	}
}

// TestStreamMergesToolCallChunksIntoOneStart guards the OpenAI streaming
// protocol: id/name arrive only on the opening chunk, later chunks stream
// argument deltas without them. A backfilled id must not re-emit ToolUseStart,
// which downstream consumers turn into duplicate empty-named tool calls.
func TestStreamMergesToolCallChunksIntoOneStart(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "mimo"}, &litellm.Request{Model: "mimo-v2.5", Messages: []litellm.Message{litellm.UserText("hi")}})
	var starts []litellm.ToolUseStart
	var dones []litellm.ToolUseDone
	var args strings.Builder
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := ev.(type) {
		case litellm.ToolUseStart:
			starts = append(starts, e)
		case litellm.ToolUseDelta:
			args.Write(e.ArgumentsDelta)
		case litellm.ToolUseDone:
			dones = append(dones, e)
		}
	}
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 ToolUseStart, got %d: %+v", len(starts), starts)
	}
	if starts[0].ID != "call_1" || starts[0].Name != "lookup" {
		t.Fatalf("ToolUseStart = %+v", starts[0])
	}
	if args.String() != `{"q":"x"}` {
		t.Fatalf("aggregated arguments = %q", args.String())
	}
	if len(dones) != 1 || dones[0].ID != "call_1" {
		t.Fatalf("expected exactly 1 ToolUseDone for call_1, got %+v", dones)
	}
}

// TestStreamClosesArglessToolCall reproduces an argument-less tool call: the
// model opens a call and finishes without streaming any arguments. The stream
// must still emit ToolUseDone so the consumer can finalize the call.
func TestStreamClosesArglessToolCall(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"novel_context"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "mimo"}, &litellm.Request{Model: "mimo-v2.5", Messages: []litellm.Message{litellm.UserText("hi")}})
	var starts, dones int
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := ev.(type) {
		case litellm.ToolUseStart:
			starts++
		case litellm.ToolUseDone:
			if e.ID != "call_9" {
				t.Fatalf("ToolUseDone id = %q", e.ID)
			}
			dones++
		}
	}
	if starts != 1 || dones != 1 {
		t.Fatalf("expected 1 start and 1 done, got start=%d done=%d", starts, dones)
	}
}

func TestStreamSeparatesToolCallsByChoiceIndex(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"first","arguments":"{\"a\":1}"}}]}},{"index":1,"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"name":"second","arguments":"{\"b\":2}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[{"index":1,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("tool calls len = %d, want 2: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_a" || calls[0].Name != "first" || string(calls[0].Arguments) != `{"a":1}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call_b" || calls[1].Name != "second" || string(calls[1].Arguments) != `{"b":2}` {
		t.Fatalf("second call = %#v", calls[1])
	}
}

func TestStreamRejectsMalformedToolCall(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "non_object",
			body: `data: {"choices":[{"index":0,"delta":{"tool_calls":["bad"]}}]}`,
			want: "tool_call must be an object",
		},
		{
			name: "non_string_arguments",
			body: `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":123}}]}}]}`,
			want: "arguments must be string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := streamFromSSE(t, tt.body, Spec{Name: "strict"}, nil)
			_, err := stream.Next()
			if err == nil || !strings.Contains(err.Error(), tt.want) || !litellm.IsProviderError(err) {
				t.Fatalf("expected malformed tool call provider error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestStreamRejectsEOFBeforeDoneSentinel(t *testing.T) {
	stream := streamFromSSE(t, `data: {"choices":[{"delta":{"content":"partial"}}]}`, Spec{Name: "strict"}, nil)
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") || !litellm.IsProviderError(err) {
		t.Fatalf("expected truncated stream error, got %v", err)
	}
}

func streamFromSSE(t *testing.T, body string, spec Spec, req *litellm.Request) litellm.Stream {
	t.Helper()
	provider, err := New(Config{
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return streamResponse(body), nil
		}),
	}, spec)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if req == nil {
		req = &litellm.Request{Model: "m", Messages: []litellm.Message{litellm.UserText("hi")}}
	}
	stream, err := provider.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	return stream
}

func streamResponse(body string) *http.Response {
	resp := jsonResponse(http.StatusOK, body)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp
}
