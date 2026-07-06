package grok

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

func TestReasoningEffort(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "grok-4.3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if body["reasoning_effort"] != "high" {
		t.Fatalf("body = %#v", body)
	}
	testgolden.AssertJSON(t, "../../testdata/compat/grok_request.golden.json", body)
}

func TestThinkingDisabled(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "grok-4.3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingDisabled},
	})
	if body["reasoning_effort"] != "none" {
		t.Fatalf("body = %#v", body)
	}
}

func TestReasoningEffortRejectsUnsupportedModel(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "grok-4",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "grok-4.3") {
		t.Fatalf("expected model support error, got %v", err)
	}
}

func TestThinkingRequiresEffort(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "grok-4.3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if err == nil || !strings.Contains(err.Error(), "effort is required") {
		t.Fatalf("expected thinking requirement error, got %v", err)
	}
}

func TestRejectsUnsupportedReasoningEffort(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "grok-4.3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "max"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported reasoning_effort") {
		t.Fatalf("expected effort error, got %v", err)
	}
}

func TestRejectsStopForReasoningModel(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "grok-4.3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Stop:     []string{"END"},
	})
	if err == nil || !strings.Contains(err.Error(), "stop is not supported") {
		t.Fatalf("expected stop error, got %v", err)
	}
}

func TestRejectsUnsupportedReasoningProviderOptions(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil), AllowUnknownProviderOptions: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:           "grok-4.3",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"presence_penalty": 0.2},
	})
	if err == nil || !strings.Contains(err.Error(), "presence_penalty") {
		t.Fatalf("expected provider option error, got %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	p, err := New(compat.Config{APIKey: "key", BaseURL: "https://grok.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := p.Capabilities("grok-4.3")
	if caps.Thinking.Supported != litellm.SupportYes || !caps.Thinking.SupportsEffort("high") || caps.Thinking.SupportsEffort("max") {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if alias := p.Capabilities("grok-latest"); alias.Thinking.Supported != litellm.SupportYes {
		t.Fatalf("alias thinking caps = %+v", alias.Thinking)
	}
	caps = p.Capabilities("grok-4")
	if caps.Thinking.Supported != litellm.SupportNo || caps.Thinking.SupportsEffort("high") {
		t.Fatalf("unsupported model caps = %+v", caps.Thinking)
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		APIKey:  "key",
		BaseURL: "https://grok.test",
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
