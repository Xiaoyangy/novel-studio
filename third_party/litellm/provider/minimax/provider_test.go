package minimax

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

func TestThinkingAndMaxCompletionTokens(t *testing.T) {
	maxTokens := 128
	body := captureBody(t, &litellm.Request{
		Model:     "MiniMax-M3",
		Messages:  []litellm.Message{litellm.UserText("hi")},
		MaxTokens: &maxTokens,
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if body["max_completion_tokens"] != float64(128) {
		t.Fatalf("max_completion_tokens = %#v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted: %#v", body)
	}
	if thinking := body["thinking"].(map[string]any); thinking["type"] != "adaptive" {
		t.Fatalf("thinking = %#v", thinking)
	}
	if body["reasoning_split"] != true {
		t.Fatalf("reasoning_split = %#v", body["reasoning_split"])
	}
	testgolden.AssertJSON(t, "../../testdata/compat/minimax_request.golden.json", body)
}

func TestDisabledThinking(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "MiniMax-M3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	if thinking := body["thinking"].(map[string]any); thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v", thinking)
	}
	if _, ok := body["reasoning_split"]; ok {
		t.Fatalf("reasoning_split should be omitted: %#v", body)
	}
}

func TestDefaultReasoningSplit(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "MiniMax-M3",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if body["reasoning_split"] != true {
		t.Fatalf("reasoning_split = %#v", body["reasoning_split"])
	}
	if _, ok := body["thinking"]; ok {
		t.Fatalf("thinking should be omitted when unspecified: %#v", body)
	}
}

func TestM2CannotDisableThinking(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://minimax.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "MiniMax-M2.7",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking cannot be disabled for M2.x") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsUnsupportedThinkingControls(t *testing.T) {
	budget := 1024
	tests := []struct {
		name     string
		thinking *litellm.Thinking
		want     string
	}{
		{
			name:     "effort",
			thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
			want:     "thinking effort is not supported",
		},
		{
			name:     "budget",
			thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
			want:     "thinking budget_tokens is not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(compat.Config{
				APIKey:  "key",
				BaseURL: "https://minimax.test",
				HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("request should not be sent")
					return nil, nil
				}),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = p.Chat(context.Background(), &litellm.Request{
				Model:    "MiniMax-M3",
				Messages: []litellm.Message{litellm.UserText("hi")},
				Thinking: tt.thinking,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestProviderOptions(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "MiniMax-M3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionServiceTier: "PRIORITY",
		},
	})
	if body["service_tier"] != "priority" {
		t.Fatalf("service_tier = %#v", body["service_tier"])
	}
}

func TestProviderOptionsValidation(t *testing.T) {
	tests := []struct {
		name    string
		options litellm.ProviderOptions
		want    string
	}{
		{
			name:    "unknown",
			options: litellm.ProviderOptions{"unknown": true},
			want:    `unsupported provider option "unknown"`,
		},
		{
			name:    "service_tier_type",
			options: litellm.ProviderOptions{ProviderOptionServiceTier: 1},
			want:    `provider option "service_tier" must be string`,
		},
		{
			name:    "service_tier_value",
			options: litellm.ProviderOptions{ProviderOptionServiceTier: "fast"},
			want:    `provider option "service_tier" must be standard or priority`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(compat.Config{
				APIKey:  "key",
				BaseURL: "https://minimax.test",
				HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("request should not be sent")
					return nil, nil
				}),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = p.Chat(context.Background(), &litellm.Request{
				Model:           "MiniMax-M3",
				Messages:        []litellm.Message{litellm.UserText("hi")},
				ProviderOptions: tt.options,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func TestResponseReasoningContentFallback(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://minimax.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"model":"MiniMax-M3",
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
		Model:    "MiniMax-M3",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text() != "ok" || resp.Reasoning() != "think" {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
}

func TestCapabilities(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://minimax.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := p.Capabilities("MiniMax-M3")
	if caps.Thinking.Supported != litellm.SupportYes || caps.Thinking.Disable != litellm.SupportPartial {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if caps.Thinking.SupportsEffort("high") || caps.Thinking.BudgetTokens != litellm.SupportNo {
		t.Fatalf("minimax should not advertise effort or budget controls: %+v", caps.Thinking)
	}

	caps = p.Capabilities("MiniMax-M2")
	if caps.Thinking.Disable != litellm.SupportNo {
		t.Fatalf("M2 disable support = %v, want no", caps.Thinking.Disable)
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://minimax.test",
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
