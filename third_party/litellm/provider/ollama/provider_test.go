package ollama

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

func TestNoAPIKeyRequiredAndThinkingMapping(t *testing.T) {
	var authorization string
	var body map[string]any
	p, err := New(compat.Config{
		BaseURL: "https://ollama.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			authorization = httpReq.Header.Get("Authorization")
			if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "high"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if authorization != "" {
		t.Fatalf("Authorization = %q, want empty", authorization)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("body = %#v", body)
	}
	testgolden.AssertJSON(t, "../../testdata/compat/ollama_request.golden.json", body)
}

func TestThinkingRequiresEffort(t *testing.T) {
	p, err := New(compat.Config{BaseURL: "https://ollama.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if err == nil || !strings.Contains(err.Error(), "effort is required") {
		t.Fatalf("expected thinking requirement error, got %v", err)
	}
}

func TestThinkingEffortMaxAndValidation(t *testing.T) {
	body := captureBody(t, &litellm.Request{
		Model:    "gpt-oss:20b",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "xhigh"},
	})
	if body["reasoning_effort"] != "max" {
		t.Fatalf("body = %#v", body)
	}

	p, err := New(compat.Config{BaseURL: "https://ollama.test", HTTPClient: roundTripFunc(nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "extreme"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported reasoning effort") {
		t.Fatalf("expected effort error, got %v", err)
	}
}

func TestResponseAndStreamReasoning(t *testing.T) {
	p, err := New(compat.Config{
		BaseURL: "https://ollama.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"thinking":"think","content":"ok"}}]}`)), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Chat(context.Background(), &litellm.Request{
		Model:    "qwen3",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Reasoning() != "think" || resp.Text() != "ok" {
		t.Fatalf("reasoning/text = %q/%q", resp.Reasoning(), resp.Text())
	}

	p, err = New(compat.Config{
		BaseURL: "https://ollama.test",
		HTTPClient: roundTripFunc(func(httpReq *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"choices":[{"index":0,"delta":{"thinking":"th"}}]}`,
				`data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				``,
			}, "\n"))), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New stream provider: %v", err)
	}
	stream, err := p.Stream(context.Background(), &litellm.Request{
		Model:    "qwen3",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	streamResp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if streamResp.Reasoning() != "th" || streamResp.Text() != "ok" {
		t.Fatalf("stream reasoning/text = %q/%q", streamResp.Reasoning(), streamResp.Text())
	}
}

func captureBody(t *testing.T, req *litellm.Request) map[string]any {
	t.Helper()
	var body map[string]any
	p, err := New(compat.Config{
		BaseURL: "https://ollama.test",
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
