package deepseek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/internal/testgolden"
	"github.com/voocel/litellm/provider/compat"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestThinkingAndStrictTools(t *testing.T) {
	body, _ := captureBody(t, compat.Config{APIKey: "key", BaseURL: "https://api.deepseek.com/beta"}, &litellm.Request{
		Model:    "deepseek-reasoner",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "xhigh"},
		Tools: []litellm.Tool{
			mustTool(t, "lookup", litellm.StrictEnabled),
		},
	})
	testgolden.AssertJSON(t, "../../testdata/compat/deepseek_request.golden.json", body)
	if thinking := body["thinking"].(map[string]any); thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v", thinking)
	}
	if body["reasoning_effort"] != "max" {
		t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
	}
	strict := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["strict"]
	if strict != true {
		t.Fatalf("strict = %#v, want true", strict)
	}
}

func TestThinkingUsesEffort(t *testing.T) {
	body, _ := captureBody(t, compat.Config{APIKey: "key", BaseURL: "https://api.deepseek.com/beta"}, &litellm.Request{
		Model:    "deepseek-reasoner",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "max"},
	})
	if body["reasoning_effort"] != "max" {
		t.Fatalf("reasoning_effort = %#v, want max", body["reasoning_effort"])
	}
}

func TestThinkingLowMediumFoldWarning(t *testing.T) {
	_, resp := captureBody(t, compat.Config{APIKey: "key", BaseURL: "https://api.deepseek.com/beta"}, &litellm.Request{
		Model:    "deepseek-reasoner",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "minimal"},
	})
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings len = %d, want 1: %#v", len(resp.Warnings), resp.Warnings)
	}
	warning := resp.Warnings[0]
	if warning.Code != "deepseek.thinking_effort_folded" || warning.Provider != "deepseek" {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestStrictToolsOmittedOutsideBeta(t *testing.T) {
	body, resp := captureBody(t, compat.Config{APIKey: "key"}, &litellm.Request{
		Model:    "deepseek-chat",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Tools: []litellm.Tool{
			mustTool(t, "lookup", litellm.StrictEnabled),
		},
	})
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if _, ok := fn["strict"]; ok {
		t.Fatalf("strict should be omitted outside beta: %#v", fn)
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("warnings len = %d, want 1: %#v", len(resp.Warnings), resp.Warnings)
	}
	warning := resp.Warnings[0]
	if warning.Code != "request.strict_tool_omitted" || warning.Provider != "deepseek" {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestProviderOptions(t *testing.T) {
	body, _ := captureBody(t, compat.Config{APIKey: "key"}, &litellm.Request{
		Model:    "deepseek-v4-flash",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionLogprobs:    true,
			ProviderOptionTopLogprobs: 5,
			ProviderOptionUserID:      "user-123",
		},
	})
	if body["logprobs"] != true ||
		body["top_logprobs"] != float64(5) ||
		body["user_id"] != "user-123" {
		t.Fatalf("body = %#v", body)
	}
}

func TestRejectsUnknownProviderOptions(t *testing.T) {
	p, err := New(compat.Config{
		APIKey: "key",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:           "deepseek-v4-flash",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported provider option "unknown"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsUnknownThinkingEffort(t *testing.T) {
	p, err := New(compat.Config{
		APIKey: "key",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "deepseek-v4-flash",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "extreme"},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported thinking effort "extreme"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRoundTripsThinkingToolCallAssistantMessage(t *testing.T) {
	var capturedBody map[string]any
	p, err := New(compat.Config{
		APIKey: "key",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model: "deepseek-v4-flash",
		Messages: []litellm.Message{
			litellm.Assistant(
				litellm.ReasoningBlock{Text: "Need the tool."},
				litellm.ToolUseBlock{
					ID:        "call_1",
					Name:      "lookup",
					Arguments: json.RawMessage(`{"q":"weather"}`),
				},
			),
			litellm.ToolResultText("call_1", "sunny"),
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["content"] != "" {
		t.Fatalf("assistant content = %#v, want empty string", assistant["content"])
	}
	if assistant["reasoning_content"] != "Need the tool." {
		t.Fatalf("assistant reasoning_content = %#v", assistant["reasoning_content"])
	}
	if _, ok := assistant["tool_calls"].([]any); !ok {
		t.Fatalf("assistant tool_calls = %#v", assistant["tool_calls"])
	}
}

func captureBody(t *testing.T, cfg compat.Config, req *litellm.Request) (map[string]any, *litellm.Response) {
	t.Helper()
	var body map[string]any
	cfg.HTTPClient = roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return jsonResponse(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return body, resp
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mustTool(t *testing.T, name string, strict litellm.StrictMode) litellm.Tool {
	t.Helper()
	tool, err := litellm.NewTool(name, "Lookup.", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	tool.Strict = strict
	return tool
}
