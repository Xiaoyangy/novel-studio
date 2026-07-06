package qwen

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

func TestThinkingBudget(t *testing.T) {
	budget := 4096
	maxTokens := 8192
	body := captureBody(t, &litellm.Request{
		Model:     "qwen3.7-plus",
		Messages:  []litellm.Message{litellm.UserText("hi")},
		MaxTokens: &maxTokens,
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
	})
	if body["enable_thinking"] != true || body["thinking_budget"] != float64(4096) {
		t.Fatalf("body = %#v", body)
	}
	if body["max_completion_tokens"] != float64(8192) {
		t.Fatalf("max_completion_tokens = %#v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted: %#v", body)
	}
	testgolden.AssertJSON(t, "../../testdata/compat/qwen_request.golden.json", body)
}

func TestThinkingDisabled(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "qwen3.7-plus",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	if body["enable_thinking"] != false {
		t.Fatalf("body = %#v", body)
	}
}

func TestProviderOptions(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "qwen3.7-plus",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionTopK:              50,
			ProviderOptionRepetitionPenalty: 1.05,
			ProviderOptionPresencePenalty:   0.2,
			ProviderOptionPreserveThinking:  true,
			ProviderOptionToolStream:        true,
			ProviderOptionEnableSearch:      true,
			ProviderOptionSearchOptions: map[string]any{
				"forced_search": true,
			},
			ProviderOptionSeed:              1234,
			ProviderOptionLogprobs:          true,
			ProviderOptionTopLogprobs:       3,
			ProviderOptionParallelToolCalls: false,
		},
	})
	if body["top_k"] != float64(50) ||
		body["repetition_penalty"] != 1.05 ||
		body["presence_penalty"] != 0.2 ||
		body["preserve_thinking"] != true ||
		body["tool_stream"] != true ||
		body["enable_search"] != true ||
		body["seed"] != float64(1234) ||
		body["logprobs"] != true ||
		body["top_logprobs"] != float64(3) ||
		body["parallel_tool_calls"] != false {
		t.Fatalf("body = %#v", body)
	}
	options, ok := body["search_options"].(map[string]any)
	if !ok || options["forced_search"] != true {
		t.Fatalf("search_options = %#v", body["search_options"])
	}
}

func TestRejectsUnknownProviderOptions(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://qwen.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:           "qwen3.7-plus",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported provider option "unknown"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsThinkingEffort(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://qwen.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3.7-plus",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking effort is not supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://qwen.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := p.Capabilities("qwen3-max")
	if caps.Thinking.Supported != litellm.SupportYes || caps.Thinking.BudgetTokens != litellm.SupportYes {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if caps.Thinking.SupportsEffort("high") {
		t.Fatalf("qwen should not advertise effort support: %+v", caps.Thinking)
	}
}

func TestResponseReasoningContent(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://qwen.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"choices":[{"message":{"content":"ok","reasoning_content":"think"},"finish_reason":"stop"}]
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3.7-plus",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text() != "ok" || resp.Reasoning() != "think" {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://qwen.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return body
}
