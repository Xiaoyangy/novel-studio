package minimax

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/voocel/litellm"
)

// Synthetic protocol data only. This server rejects string reasoning_details
// exactly at the real HTTP boundary, then checks both tool-history turns.
func TestReasoningHistoryMultiTurnHTTPPreservesDetailsAndText(t *testing.T) {
	details := []any{map[string]any{"type": "reasoning.text", "text": "synthetic detail A", "index": float64(0), "signature": "synthetic-opaque-state", "future": map[string]any{"keep": true}}}
	canonicalText := "synthetic detail A"
	jsonLookingText := `[{"synthetic":"still text, not opaque state"}]`
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		n := calls.Add(1)
		fail := func(message string) {
			t.Error(message)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"reasoning history shape mismatch"}}`))
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || len(body.Tools) != 1 {
			fail("expected the real chat endpoint with the declared tool")
			return
		}
		for _, message := range body.Messages {
			if value, exists := message["reasoning_details"]; exists {
				if _, ok := value.([]any); !ok {
					fail("HTTP request sent non-array reasoning_details")
					return
				}
			}
		}
		if n >= 2 {
			if len(body.Messages) < 3 || !reflect.DeepEqual(body.Messages[1]["reasoning_details"], details) || body.Messages[2]["role"] != "tool" || body.Messages[2]["tool_call_id"] != "tool_a" {
				fail("first assistant reasoning/tool history was lost")
				return
			}
			if _, duplicated := body.Messages[1]["reasoning_content"]; duplicated {
				fail("identical reasoning text was sent twice")
				return
			}
		}
		if n == 3 {
			if len(body.Messages) != 5 || body.Messages[3]["reasoning_content"] != jsonLookingText || body.Messages[4]["tool_call_id"] != "tool_b" {
				fail("text-only second assistant history was misrouted")
				return
			}
			if _, exists := body.Messages[3]["reasoning_details"]; exists {
				fail("JSON-looking text became array history")
				return
			}
		}
		message := map[string]any{"role": "assistant", "content": ""}
		finish := "tool_calls"
		switch n {
		case 1:
			message["reasoning_content"], message["reasoning_details"] = canonicalText, details
			message["tool_calls"] = []any{map[string]any{"id": "tool_a", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"q":"synthetic-a"}`}}}
		case 2:
			message["reasoning_content"] = jsonLookingText
			message["tool_calls"] = []any{map[string]any{"id": "tool_b", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"q":"synthetic-b"}`}}}
		case 3:
			message["content"], finish = "synthetic complete", "stop"
		default:
			fail("unexpected additional HTTP request")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "MiniMax-M3", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
	}))
	defer server.Close()
	provider, err := New(Config{APIKey: "synthetic-test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	messages := []litellm.Message{litellm.UserText("synthetic tool-history test")}
	tool := litellm.Tool{Name: "lookup", Description: "Synthetic local lookup", Parameters: litellm.Schema(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)}
	for turn := 0; turn < 3; turn++ {
		response, err := provider.Chat(context.Background(), &litellm.Request{Model: "MiniMax-M3", Messages: messages, Tools: []litellm.Tool{tool}})
		if err != nil {
			t.Fatalf("HTTP turn %d: %v", turn+1, err)
		}
		if turn == 0 {
			reasoning, ok := response.Blocks[0].(litellm.ReasoningBlock)
			if !ok || reasoning.Text != canonicalText {
				t.Fatal("legacy reasoning-details projection changed")
			}
			var extra any
			if err := json.Unmarshal(reasoning.Extra, &extra); err != nil || !reflect.DeepEqual(extra, details) {
				t.Fatal("structured response details lost opaque fields")
			}
		}
		if turn < 2 {
			toolCalls := response.ToolCalls()
			if len(toolCalls) != 1 || toolCalls[0].Name != "lookup" || toolCalls[0].ID != fmt.Sprintf("tool_%c", 'a'+turn) {
				t.Fatal("response lost the actual tool call")
			}
			messages = append(messages, litellm.Assistant(response.Blocks...))
			messages = append(messages, litellm.Message{Role: litellm.RoleTool, Blocks: []litellm.Block{litellm.ToolResultBlock{ToolUseID: toolCalls[0].ID, Content: []litellm.Block{litellm.TextBlock{Text: "synthetic lookup result"}}}}})
		} else if response.Text() != "synthetic complete" {
			t.Fatal("multi-turn tool exchange did not finish")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("test did not exercise all real HTTP history requests")
	}
}

func TestReasoningHistoryMalformedExtraNeverReachesHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	provider, err := New(Config{APIKey: "synthetic-test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []json.RawMessage{json.RawMessage(`"[{\"synthetic\":true}]"`), json.RawMessage(`{"synthetic":true}`), json.RawMessage(`null`)} {
		_, err := provider.Chat(context.Background(), &litellm.Request{Model: "MiniMax-M3", Messages: []litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "synthetic", Extra: extra})}})
		if err == nil {
			t.Fatal("invalid array-shaped state was accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid reasoning_details escaped local validation")
	}
}
