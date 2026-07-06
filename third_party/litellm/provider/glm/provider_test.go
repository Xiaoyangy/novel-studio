package glm

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

func TestThinkingVersionGates(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "MAX"},
	})
	if thinking := body["thinking"].(map[string]any); thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v", thinking)
	}
	if body["reasoning_effort"] != "max" {
		t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
	}
	testgolden.AssertJSON(t, "../../testdata/compat/glm_request.golden.json", body)

	body = captureBody(t, &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "HIGH"},
	})
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
	}

	err := chatErr(t, &litellm.Request{
		Model:    "glm-5.1",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "glm-5.2 or later") {
		t.Fatalf("expected reasoning_effort version error, got %v", err)
	}
}

func TestThinkingUnsupportedModelErrors(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://glm.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "glm-4",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "glm-4.5 or later") {
		t.Fatalf("expected unsupported thinking error, got %v", err)
	}
}

func TestThinkingEffortValidation(t *testing.T) {
	err := chatErr(t, &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "bad"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported reasoning_effort") {
		t.Fatalf("expected unsupported effort error, got %v", err)
	}
}

func TestProviderOptions(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionDoSample:   false,
			ProviderOptionRequestID:  "request-123",
			ProviderOptionToolStream: true,
			ProviderOptionUserID:     "user-123",
			ProviderOptionThinking: map[string]any{
				"clear_thinking": false,
			},
		},
	})
	if body["do_sample"] != false ||
		body["request_id"] != "request-123" ||
		body["tool_stream"] != true ||
		body["user_id"] != "user-123" {
		t.Fatalf("body = %#v", body)
	}
	thinking := body["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["clear_thinking"] != false {
		t.Fatalf("thinking = %#v", thinking)
	}
}

func TestProviderOptionsValidation(t *testing.T) {
	err := chatErr(t, &litellm.Request{
		Model:           "glm-5.2",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported provider option "unknown"`) {
		t.Fatalf("expected unknown option error, got %v", err)
	}

	err = chatErr(t, &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionThinking: map[string]any{"type": "disabled"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with Request.Thinking") {
		t.Fatalf("expected thinking conflict error, got %v", err)
	}
}

func TestReasoningContentRoundTrip(t *testing.T) {
	var capturedBody map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://glm.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(httpReq.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{
				"model":"glm-5.2",
				"choices":[{"message":{"content":"ok","reasoning_content":"think"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4}}
			}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Model != "glm-5.2" || resp.Text() != "ok" || resp.Reasoning() != "think" || resp.Usage.CacheReadTokens != 4 {
		t.Fatalf("response = model %q text %q reasoning %q usage %+v", resp.Model, resp.Text(), resp.Reasoning(), resp.Usage)
	}

	_, err = p.Chat(context.Background(), &litellm.Request{
		Model: "glm-5.2",
		Messages: []litellm.Message{
			litellm.Assistant(resp.Blocks...),
		},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionThinking: map[string]any{"clear_thinking": false},
		},
	})
	if err != nil {
		t.Fatalf("round-trip Chat: %v", err)
	}
	messages := capturedBody["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "think" || assistant["content"] != "ok" {
		t.Fatalf("assistant history = %#v", assistant)
	}
	thinking := capturedBody["thinking"].(map[string]any)
	if thinking["clear_thinking"] != false {
		t.Fatalf("thinking = %#v", thinking)
	}
}

func TestStreamReasoningContent(t *testing.T) {
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://glm.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(strings.Join([]string{
					`data: {"model":"glm-5.2","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
					`data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4}}}`,
					`data: [DONE]`,
					``,
				}, "\n"))),
				Header: make(http.Header),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := p.Stream(context.Background(), &litellm.Request{
		Model:    "glm-5.2",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.Model != "glm-5.2" || resp.Text() != "ok" || resp.Reasoning() != "think" || resp.Usage.CacheReadTokens != 4 {
		t.Fatalf("response = model %q text %q reasoning %q usage %+v", resp.Model, resp.Text(), resp.Reasoning(), resp.Usage)
	}
}

func TestJSONSchemaToPromptStillSendsJSONObjectFormat(t *testing.T) {
	format, err := litellm.NewResponseFormatJSONSchema("answer", "", map[string]any{
		"type":       "object",
		"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
	}, litellm.StrictDefault)
	if err != nil {
		t.Fatalf("NewResponseFormatJSONSchema: %v", err)
	}
	body := captureBody(t, &litellm.Request{
		Model:          "glm-4.5",
		Messages:       []litellm.Message{litellm.UserText("hi")},
		ResponseFormat: format,
	})
	responseFormat := body["response_format"].(map[string]any)
	if responseFormat["type"] != "json_object" {
		t.Fatalf("response_format = %#v", responseFormat)
	}
	messages := body["messages"].([]any)
	content := messageText(messages[0].(map[string]any)["content"])
	if !strings.Contains(content, "Return JSON matching schema answer") {
		t.Fatalf("schema prompt was not injected: %q", content)
	}
}

func messageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var out strings.Builder
		for _, item := range c {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, _ := part["text"].(string)
			out.WriteString(text)
		}
		return out.String()
	default:
		return ""
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://glm.test",
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

func chatErr(t *testing.T, req *litellm.Request) error {
	t.Helper()
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://glm.test",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent")
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), req)
	return err
}
