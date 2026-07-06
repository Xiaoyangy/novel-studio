package mimo

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
	maxTokens := 2048
	body := captureBody(t, &litellm.Request{
		Model:     "mimo-v2.5-pro",
		Messages:  []litellm.Message{litellm.UserText("hi")},
		MaxTokens: &maxTokens,
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	thinking := body["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v", thinking)
	}
	if body["max_completion_tokens"] != float64(2048) {
		t.Fatalf("max_completion_tokens = %#v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted: %#v", body)
	}
	testgolden.AssertJSON(t, "../../testdata/compat/mimo_request.golden.json", body)
}

func TestProviderOptionsAndStrictTools(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "mimo-v2-flash",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Tools: []litellm.Tool{
			mustTool(t, "lookup", litellm.StrictEnabled),
		},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionAudio: map[string]any{
				"format": "mp3",
			},
			ProviderOptionFrequencyPenalty: 0.3,
			ProviderOptionPresencePenalty:  0.2,
		},
	})
	if body["frequency_penalty"] != 0.3 || body["presence_penalty"] != 0.2 {
		t.Fatalf("penalties = %#v", body)
	}
	audio, ok := body["audio"].(map[string]any)
	if !ok || audio["format"] != "mp3" {
		t.Fatalf("audio = %#v", body["audio"])
	}
	fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("strict = %#v", fn["strict"])
	}
}

func TestRejectsUnknownProviderOptions(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:           "mimo-v2-flash",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported provider option "unknown"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsSamplingOverridesWithThinking(t *testing.T) {
	temp := 0.2
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:       "mimo-v2.5-pro",
		Messages:    []litellm.Message{litellm.UserText("hi")},
		Temperature: &temp,
		Thinking:    &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if err == nil || !strings.Contains(err.Error(), "temperature cannot be customized") {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsSamplingOverridesWithDefaultThinking(t *testing.T) {
	temp := 0.7
	topP := 0.8
	tests := []struct {
		name string
		req  *litellm.Request
		want string
	}{
		{
			name: "temperature",
			req: &litellm.Request{
				Model:       "mimo-v2.5-pro",
				Messages:    []litellm.Message{litellm.UserText("hi")},
				Temperature: &temp,
			},
			want: "temperature cannot be customized",
		},
		{
			name: "top_p",
			req: &litellm.Request{
				Model:    "mimo-v2.5-pro",
				Messages: []litellm.Message{litellm.UserText("hi")},
				TopP:     &topP,
			},
			want: "top_p cannot be customized",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(compat.Config{
				APIKey:  "key",
				BaseURL: "https://mimo.test",
				HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("request should not be sent")
					return nil, nil
				}),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			// mimo-v2.5-pro enables thinking by default. If sampling fields are
			// present in the normalized request, the provider cannot know whether
			// they came from the application or a caller default, so it must fail
			// loudly instead of silently dropping explicit input.
			_, err = p.Chat(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v", err)
			}
		})
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
				BaseURL: "https://mimo.test",
				HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("request should not be sent")
					return nil, nil
				}),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = p.Chat(context.Background(), &litellm.Request{
				Model:    "mimo-v2.5-pro",
				Messages: []litellm.Message{litellm.UserText("hi")},
				Thinking: tt.thinking,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRejectsThinkingForTTSModels(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "mimo-v2.5-tts",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking is not supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestResponseReasoningContent(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(`{
				"model":"mimo-v2.5-pro",
				"choices":[{"message":{"content":"ok","reasoning_content":"think"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":1},"prompt_tokens_details":{"cached_tokens":1}}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &litellm.Request{
		Model:    "mimo-v2.5-pro",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text() != "ok" || resp.Reasoning() != "think" {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
	if resp.Usage.ReasoningTokens != 1 || resp.Usage.CacheReadTokens != 1 || resp.Model != "mimo-v2.5-pro" {
		t.Fatalf("usage/model = %+v/%q", resp.Usage, resp.Model)
	}
}

func TestRoundTripsReasoningContentHistory(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model: "mimo-v2.5-pro",
		Messages: []litellm.Message{
			litellm.Assistant(
				litellm.ReasoningBlock{Text: "Need lookup."},
				litellm.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
			),
			litellm.ToolResultText("call_1", "ok"),
		},
	})
	messages := body["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "Need lookup." {
		t.Fatalf("assistant reasoning_content = %#v", assistant["reasoning_content"])
	}
	if _, ok := assistant["tool_calls"].([]any); !ok {
		t.Fatalf("assistant tool_calls = %#v", assistant["tool_calls"])
	}
}

func TestStreamReasoningContent(t *testing.T) {
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return streamResponse(strings.Join([]string{
				`data: {"model":"mimo-v2.5-pro","choices":[{"index":0,"delta":{"reasoning_content":"th"}}]}`,
				`data: {"model":"mimo-v2.5-pro","choices":[{"index":0,"delta":{"reasoning_content":"ink"}}]}`,
				`data: {"model":"mimo-v2.5-pro","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"completion_tokens_details":{"reasoning_tokens":1},"prompt_tokens_details":{"cached_tokens":1}}}`,
				`data: [DONE]`,
				``,
			}, "\n")), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := p.Stream(context.Background(), &litellm.Request{
		Model:    "mimo-v2.5-pro",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.Reasoning() != "think" || resp.Text() != "ok" {
		t.Fatalf("reasoning/text = %q/%q", resp.Reasoning(), resp.Text())
	}
	if _, ok := body["stream_options"]; ok {
		t.Fatalf("stream_options should be omitted for mimo: %#v", body)
	}
	if resp.Usage.ReasoningTokens != 1 || resp.Usage.CacheReadTokens != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://mimo.test",
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

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func streamResponse(body string) *http.Response {
	resp := jsonResponse(body)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp
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
