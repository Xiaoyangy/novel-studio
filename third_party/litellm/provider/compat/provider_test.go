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
	"github.com/voocel/litellm/retry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestChatBuildsRequestAndConvertsResponse(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("Authorization = %q", got)
			}
			if req.URL.String() != "https://compat.example/v1/chat/completions" {
				t.Fatalf("url = %s", req.URL.String())
			}
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, testgolden.ReadFixtureString(t, "../../testdata/compat/chat_response.json")), nil
		}),
	}, Spec{
		Name: "testcompat",
		Auth: AuthSpec{APIKeyRequired: true},
		Request: RequestSpec{
			SupportsJSONSchema: true,
			AllowedProviderOptions: map[string]struct{}{
				"extra_body": {},
			},
		},
		Response: ResponseSpec{
			ModelFromResponse:         true,
			HasCompletionTokenDetails: true,
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	tool := mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model: "m",
		Messages: []litellm.Message{
			litellm.User(litellm.Text("hi"), litellm.ImageURL("https://example.test/image.png")),
			litellm.Assistant(litellm.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: litellm.MustJSONRaw(map[string]any{"q": "x"})}),
			litellm.ToolResultText("call_1", "ok"),
		},
		Tools:           []litellm.Tool{tool},
		ProviderOptions: litellm.ProviderOptions{"extra_body": true},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capturedBody["model"] != "m" || capturedBody["extra_body"] != true {
		t.Fatalf("captured body = %#v", capturedBody)
	}
	if _, ok := capturedBody["messages"].([]any); !ok {
		t.Fatalf("messages not encoded as array: %#v", capturedBody["messages"])
	}
	if resp.Model != "provider-model" || resp.Text() != "hello" || resp.Reasoning() != "think" {
		t.Fatalf("response model/text/reasoning = %q/%q/%q", resp.Model, resp.Text(), resp.Reasoning())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestChatRetriesWhenRetryPolicyIsConfigured(t *testing.T) {
	var attempts int
	provider, err := New(Config{
		BaseURL: "https://compat.example/v1",
		Retry:   &retry.Policy{MaxAttempts: 2, InitialDelay: 1},
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return jsonResponse(http.StatusTooManyRequests, `{"error":"retry"}`), nil
			}
			return jsonResponse(http.StatusOK, `{
				"model":"m",
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]
			}`), nil
		}),
	}, Spec{Name: "retrycompat"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if attempts != 2 || resp.Text() != "ok" {
		t.Fatalf("attempts/text = %d/%q", attempts, resp.Text())
	}
}

func TestNewRejectsAmbiguousTransportConfig(t *testing.T) {
	_, err := New(Config{
		BaseURL:    "https://compat.example/v1",
		HTTPClient: roundTripFunc(nil),
		Transport:  roundTripperFunc(nil),
	}, Spec{Name: "testcompat"})
	if err == nil || !strings.Contains(err.Error(), "HTTPClient and Transport are mutually exclusive") {
		t.Fatalf("expected HTTPClient/Transport error, got %v", err)
	}

	_, err = New(Config{
		BaseURL:    "https://compat.example/v1",
		HTTPClient: roundTripFunc(nil),
		Retry:      retry.DefaultPolicy(),
	}, Spec{Name: "testcompat"})
	if err == nil || !strings.Contains(err.Error(), "Retry cannot be used with a custom HTTPClient") {
		t.Fatalf("expected HTTPClient/Retry error, got %v", err)
	}
}

func TestRejectsUnknownProviderOptionByDefault(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:           "m",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
}

func TestConfigCanAllowUnknownProviderOptions(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{Name: "passthrough"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	data, _, err := provider.buildRequest(&litellm.Request{
		Model:           "m",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"min_p": 0.05},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if !strings.Contains(string(data), `"min_p":0.05`) {
		t.Fatalf("body missing passthrough option: %s", data)
	}
}

func TestConfigAllowsUnknownProviderOptionsWithoutBypassingKnownMapper(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{
		Name: "mapped",
		Request: RequestSpec{
			AllowedProviderOptions: map[string]struct{}{"known": {}},
			ProviderOptions: func(options litellm.ProviderOptions, body map[string]any, _ *litellm.Request) error {
				for key, value := range options {
					if key != "known" {
						t.Fatalf("mapper saw unknown option %q", key)
					}
					body["mapped_known"] = value
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	data, _, err := provider.buildRequest(&litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			"known": "typed",
			"min_p": 0.05,
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"mapped_known":"typed"`, `"min_p":0.05`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestConfigAllowedUnknownProviderOptionsRejectGeneratedFieldConflict(t *testing.T) {
	provider, err := New(Config{
		BaseURL:                     "https://compat.example",
		HTTPClient:                  roundTripFunc(nil),
		AllowUnknownProviderOptions: true,
	}, Spec{
		Name: "mapped",
		Request: RequestSpec{
			AllowedProviderOptions: map[string]struct{}{"known": {}},
			ProviderOptions: func(options litellm.ProviderOptions, body map[string]any, _ *litellm.Request) error {
				body["known"] = options["known"]
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, _, err = provider.buildRequest(&litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			"known":    "typed",
			"messages": []any{"override"},
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "messages" conflicts with generated request field`) {
		t.Fatalf("expected generated field conflict, got %v", err)
	}
}

func TestProviderOptionsRejectGeneratedFieldConflict(t *testing.T) {
	provider, err := New(Config{
		BaseURL:    "https://compat.example",
		HTTPClient: roundTripFunc(nil),
	}, Spec{
		Name: "passthrough",
		Request: RequestSpec{
			AllowUnknownProviderOptions: true,
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, _, err = provider.buildRequest(&litellm.Request{
		Model:           "m",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"model": "override"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `provider option "model" conflicts with generated request field`) {
		t.Fatalf("expected generated field conflict, got %v", err)
	}
}

func TestChatReturnsStructuredValidationError(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:           "m",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !litellm.IsValidationError(err) {
		t.Fatalf("expected structured validation error, got %v", err)
	}
}

func TestRejectsStopSequencesAboveProviderLimit(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{
		Name:    "glm",
		Request: RequestSpec{MaxStopSequences: 1},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Stop:     []string{"a", "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "stop supports at most 1 sequence") {
		t.Fatalf("expected stop limit error, got %v", err)
	}
}

func TestReasoningBlockHistoryRequiresProviderMapping(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "think"})},
	})
	if err == nil || !strings.Contains(err.Error(), "ReasoningBlock history is not supported") {
		t.Fatalf("expected reasoning history error, got %v", err)
	}
}

func TestReasoningBlockExtraRoundTripsThroughConfiguredField(t *testing.T) {
	var capturedBody map[string]any
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
		}),
	}, Spec{
		Name: "minimax",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_details"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model: "m",
		Messages: []litellm.Message{
			litellm.Assistant(litellm.ReasoningBlock{
				Text:  "step 1",
				Extra: json.RawMessage(`[{"type":"reasoning.text","text":"step 1"}]`),
			}),
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	details := messages[0].(map[string]any)["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["text"] != "step 1" {
		t.Fatalf("reasoning_details = %#v", details)
	}
}

func TestResponseReasoningBlocksRoundTripThroughConfiguredField(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{
					"message":{
						"content":"ok",
						"reasoning_details":[{"type":"reasoning.text","text":"step 1"}]
					}
				}]
			}`), nil
		}),
	}, Spec{
		Name: "minimax",
		Response: ResponseSpec{
			ReasoningFields: []string{"reasoning_details"},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	var capturedBody map[string]any
	provider.cfg.HTTPClient = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
	})
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.Assistant(resp.Blocks...)},
	})
	if err != nil {
		t.Fatalf("round-trip Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	details := messages[0].(map[string]any)["reasoning_details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["text"] != "step 1" {
		t.Fatalf("reasoning_details = %#v", details)
	}
}

func TestAssistantToolCallsCanEmitEmptyContentWhenConfigured(t *testing.T) {
	tests := []struct {
		name        string
		spec        Spec
		wantContent bool
	}{
		{name: "default_omits_empty_content", spec: Spec{Name: "compat"}},
		{name: "configured_emits_empty_content", spec: Spec{
			Name: "deepseek",
			Request: RequestSpec{
				EmitEmptyAssistantContentWithToolCalls: true,
			},
		}, wantContent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody map[string]any
			provider, err := New(Config{
				BaseURL: "https://compat.example",
				HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
						t.Fatalf("decode body: %v", err)
					}
					return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`), nil
				}),
			}, tt.spec)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			_, err = provider.Chat(context.Background(), &litellm.Request{
				Model: "m",
				Messages: []litellm.Message{
					litellm.Assistant(litellm.ToolUseBlock{
						ID:        "call_1",
						Name:      "lookup",
						Arguments: json.RawMessage(`{"q":"weather"}`),
					}),
				},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			messages := capturedBody["messages"].([]any)
			assistant := messages[0].(map[string]any)
			_, hasContent := assistant["content"]
			if hasContent != tt.wantContent {
				t.Fatalf("has content = %v, want %v; assistant=%#v", hasContent, tt.wantContent, assistant)
			}
			if tt.wantContent && assistant["content"] != "" {
				t.Fatalf("content = %#v, want empty string", assistant["content"])
			}
		})
	}
}

func TestChatConvertsPromptTokensDetailsCachedTokens(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
				"usage":{
					"prompt_tokens":10,
					"completion_tokens":2,
					"total_tokens":12,
					"prompt_tokens_details":{"cached_tokens":7}
				}
			}`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Usage.CacheReadTokens != 7 {
		t.Fatalf("cache read tokens = %d, want 7", resp.Usage.CacheReadTokens)
	}
}

func TestChatConvertsRefusalMessageAndContentPart(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "message_refusal",
			body: `{"choices":[{"message":{"content":null,"refusal":"I can't help."},"finish_reason":"content_filter"}]}`,
			want: "I can't help.",
		},
		{
			name: "content_part_refusal",
			body: `{"choices":[{"message":{"content":[{"type":"refusal","refusal":"I can't help."}]},"finish_reason":"content_filter"}]}`,
			want: "I can't help.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := New(Config{
				BaseURL: "https://compat.example",
				HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, tt.body), nil
				}),
			}, Spec{Name: "strict"})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			resp, err := provider.Chat(context.Background(), &litellm.Request{
				Model:    "m",
				Messages: []litellm.Message{litellm.UserText("hi")},
			})
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if resp.Text() != tt.want {
				t.Fatalf("text = %q, want %q", resp.Text(), tt.want)
			}
		})
	}
}

func TestChatRejectsUnsupportedResponseContent(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{
					"message":{"content":[{"type":"audio","url":"https://example.test/a.wav"}]},
					"finish_reason":"stop"
				}]
			}`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content part type") {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}

func TestChatPreservesInvalidToolCallArguments(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
				"choices":[{
					"message":{"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"lookup","arguments":"{\"q\":"}
					}]},
					"finish_reason":"tool_calls"
				}]
			}`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(calls))
	}
	if got := string(calls[0].Arguments); got != `{"q":` {
		t.Fatalf("arguments = %q, want raw malformed args", got)
	}
}

func TestChatDecodeErrorIsStructuredProviderError(t *testing.T) {
	provider, err := New(Config{
		BaseURL: "https://compat.example",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"choices":[`), nil
		}),
	}, Spec{Name: "strict"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "decode response") || !litellm.IsProviderError(err) {
		t.Fatalf("expected structured decode provider error, got %v", err)
	}
}

func TestThinkingMapperMustEmitFields(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://compat.example", HTTPClient: roundTripFunc(nil)}, Spec{
		Name: "empty-thinking",
		Request: RequestSpec{
			Thinking: func(*litellm.Thinking, string) (map[string]any, error) {
				return nil, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "m",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking mapper produced no fields") {
		t.Fatalf("expected empty thinking mapper error, got %v", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mustTool(t *testing.T, name, description string, schema any) litellm.Tool {
	t.Helper()
	tool, err := litellm.NewTool(name, description, schema)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}
