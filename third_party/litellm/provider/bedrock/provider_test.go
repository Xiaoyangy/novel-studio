package bedrock

import (
	"bytes"
	"context"
	"encoding/binary"
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

func TestBuildRequestToolCacheAndThinking(t *testing.T) {
	provider := mustProvider(t)
	maxTokens := 4096
	temp := 1.0
	tool := mustTool(t, "lookup", "Lookup data.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
		"required": []string{"q"},
	})
	tool.Strict = litellm.StrictEnabled

	wire, err := provider.buildRequest(&litellm.Request{
		Model:       "anthropic.claude-sonnet-4-20250514-v1:0",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []litellm.Message{
			litellm.System("be concise"),
			litellm.User(litellm.Text("use tool")),
			litellm.Assistant(litellm.ToolUseBlock{
				ID:        "toolu_1",
				Name:      "lookup",
				Arguments: litellm.MustJSONRaw(map[string]any{"q": "x"}),
			}),
			litellm.ToolResultText("toolu_1", "result"),
		},
		Tools:    []litellm.Tool{tool},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "low"},
		Cache:    &litellm.CachePolicy{Retention: "1h"},
	})
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	testgolden.AssertJSON(t, "../../testdata/bedrock/request_converse_tools.golden.json", wire)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	jsonText := string(data)
	for _, want := range []string{
		`"system":[{"text":"be concise"},{"cachePoint":{"type":"default","ttl":"1h"}}]`,
		`"toolUse":{"toolUseId":"toolu_1","name":"lookup","input":{"q":"x"}}`,
		`"role":"user","content":[{"toolResult":{"toolUseId":"toolu_1","content":[{"text":"result"}]}}`,
		`"inputSchema":{"json":{"properties":{"q":{"type":"string"}},"required":["q"],"type":"object"}}`,
		`"strict":true`,
		`"thinking":{"budget_tokens":2048,"type":"enabled"}`,
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("wire JSON missing %s:\n%s", want, jsonText)
		}
	}
	if len(wire.ToolConfig.Tools) < 2 || wire.ToolConfig.Tools[len(wire.ToolConfig.Tools)-1].CachePoint == nil {
		t.Fatalf("expected cache point after tools: %+v", wire.ToolConfig.Tools)
	}
}

func TestBuildRequestRejectsThinkingWithoutBudgetOrEffort(t *testing.T) {
	provider := mustProvider(t)
	maxTokens := 4096
	_, err := provider.buildRequest(&litellm.Request{
		Model:     "anthropic.claude-sonnet-4-20250514-v1:0",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	})
	if err == nil || !strings.Contains(err.Error(), "budget_tokens or effort is required") {
		t.Fatalf("expected budget error, got %v", err)
	}
}

func TestBuildRequestMapsMaxThinkingEffort(t *testing.T) {
	provider := mustProvider(t)
	maxTokens := 65536
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "anthropic.claude-sonnet-4-20250514-v1:0",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "max"},
	})
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	thinking, ok := wire.AdditionalModelRequestFields["thinking"].(map[string]any)
	if !ok || thinking["budget_tokens"] != 32768 {
		t.Fatalf("thinking = %#v, want budget 32768", wire.AdditionalModelRequestFields["thinking"])
	}
}

func TestCapabilities(t *testing.T) {
	provider := mustProvider(t)
	caps := provider.Capabilities("anthropic.claude-sonnet-4-20250514-v1:0")
	if caps.Thinking.Supported != litellm.SupportYes || !caps.Thinking.SupportsEffort("max") {
		t.Fatalf("thinking caps = %+v", caps.Thinking)
	}
	if caps.Structured.JSONObject != litellm.SupportYes || caps.Structured.JSONSchema != litellm.SupportYes {
		t.Fatalf("structured caps = %+v", caps.Structured)
	}
	if caps.Structured.Strict != litellm.SupportNo {
		t.Fatalf("structured strict = %v, want no", caps.Structured.Strict)
	}
}

func TestChatRetriesAndSignsEachAttempt(t *testing.T) {
	var attempts int
	provider, err := New(Config{
		Region:      "us-west-2",
		BaseURL:     "https://bedrock-runtime.us-west-2.amazonaws.com",
		Credentials: StaticCredentials("AKID", "SECRET", ""),
		Retry:       &retry.Policy{MaxAttempts: 2, InitialDelay: 1},
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if !strings.Contains(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
				t.Fatalf("attempt %d missing signature: %s", attempts, req.Header.Get("Authorization"))
			}
			if attempts == 1 {
				return jsonResponse(http.StatusTooManyRequests, `{"error":"retry"}`), nil
			}
			return jsonResponse(http.StatusOK, `{
				"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},
				"stopReason":"end_turn"
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20240620-v1:0",
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
		Credentials: StaticCredentials("AKID", "SECRET", ""),
		HTTPClient:  roundTripFunc(nil),
		Transport:   roundTripperFunc(nil),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPClient and Transport are mutually exclusive") {
		t.Fatalf("expected HTTPClient/Transport error, got %v", err)
	}

	_, err = New(Config{
		Credentials: StaticCredentials("AKID", "SECRET", ""),
		HTTPClient:  roundTripFunc(nil),
		Retry:       retry.DefaultPolicy(),
	})
	if err == nil || !strings.Contains(err.Error(), "Retry cannot be used with a custom HTTPClient") {
		t.Fatalf("expected HTTPClient/Retry error, got %v", err)
	}
}

func TestBuildRequestRejectsThinkingWithoutMaxTokens(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&litellm.Request{
		Model:    "anthropic.claude-sonnet-4-20250514-v1:0",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "low"},
	})
	if err == nil || !strings.Contains(err.Error(), "max_tokens is required") {
		t.Fatalf("expected max_tokens error, got %v", err)
	}
}

func TestBuildRequestRejectsNonClaudeThinking(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&litellm.Request{
		Model:    "amazon.nova-pro-v1:0",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "low"},
	})
	if err == nil || !strings.Contains(err.Error(), "only supported for Claude") {
		t.Fatalf("expected non-Claude thinking error, got %v", err)
	}
}

func TestBuildRequestRejectsUnknownProviderOption(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&litellm.Request{
		Model:           "anthropic.claude-sonnet-4-20250514-v1:0",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("expected provider option error, got %v", err)
	}
}

func TestBuildRequestRejectsInvalidCacheRetention(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildRequest(&litellm.Request{
		Model:    "anthropic.claude-sonnet-4-20250514-v1:0",
		Messages: []litellm.Message{litellm.UserText("hi")},
		Cache:    &litellm.CachePolicy{Retention: "forever"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache retention") {
		t.Fatalf("expected cache retention error, got %v", err)
	}

	_, err = provider.buildRequest(&litellm.Request{
		Model:           "anthropic.claude-sonnet-4-20250514-v1:0",
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"cache_retention": "forever"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache retention") {
		t.Fatalf("expected provider option cache retention error, got %v", err)
	}
}

func TestBuildRequestRoundTripsReasoningBlockHistory(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildRequest(&litellm.Request{
		Model: "anthropic.claude-sonnet-4-20250514-v1:0",
		Messages: []litellm.Message{
			litellm.Assistant(litellm.ReasoningBlock{Text: "think", Signature: "sig"}),
		},
	})
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	block := wire.Messages[0].Content[0].ReasoningContent
	if block == nil || block.ReasoningText == nil || block.ReasoningText.Text != "think" || block.ReasoningText.Signature != "sig" {
		t.Fatalf("reasoning content = %#v", block)
	}
}

func TestChatSignsRequestAndConvertsResponse(t *testing.T) {
	var authHeader string
	var tokenHeader string
	provider, err := New(Config{
		Region:      "us-west-2",
		BaseURL:     "https://bedrock-runtime.us-west-2.amazonaws.com",
		Credentials: StaticCredentials("AKID", "SECRET", "SESSION"),
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authHeader = req.Header.Get("Authorization")
			tokenHeader = req.Header.Get("X-Amz-Security-Token")
			if req.URL.Path != "/model/anthropic.claude-3-5-sonnet-20240620-v1:0/converse" || req.URL.RawPath != "/model/anthropic.claude-3-5-sonnet-20240620-v1%3A0/converse" {
				t.Fatalf("unexpected path/rawPath: %s/%s", req.URL.Path, req.URL.RawPath)
			}
			return jsonResponse(http.StatusOK, `{
				"output":{"message":{"role":"assistant","content":[
					{"reasoningContent":{"reasoningText":{"text":"think","signature":"sig"}}},
					{"text":"hello"},
					{"toolUse":{"toolUseId":"toolu_1","name":"lookup","input":{"q":"x"}}}
				]}},
				"stopReason":"tool_use",
				"usage":{"inputTokens":5,"outputTokens":7,"totalTokens":12,"cacheReadInputTokens":2,"cacheWriteInputTokens":3}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20240620-v1:0",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if !strings.Contains(authHeader, "AWS4-HMAC-SHA256 Credential=AKID/") || !strings.Contains(authHeader, "/us-west-2/bedrock/aws4_request") {
		t.Fatalf("bad auth header: %s", authHeader)
	}
	if tokenHeader != "SESSION" {
		t.Fatalf("session token = %q", tokenHeader)
	}
	if resp.Text() != "hello" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Reasoning() != "think" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || reasoning.Signature != "sig" {
		t.Fatalf("reasoning block = %#v", resp.Blocks[0])
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 7 || resp.Usage.CacheReadTokens != 2 || resp.Usage.CacheWriteTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != litellm.FinishReasonToolCall {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestConvertResponseRejectsNil(t *testing.T) {
	_, err := convertResponse(nil, "anthropic.claude")
	if err == nil || !strings.Contains(err.Error(), "response cannot be nil") {
		t.Fatalf("expected nil response error, got %v", err)
	}
}

func TestStreamConvertsEventStreamToTypedEvents(t *testing.T) {
	provider, err := New(Config{
		Region:      "us-west-2",
		BaseURL:     "https://bedrock-runtime.us-west-2.amazonaws.com",
		Credentials: StaticCredentials("AKID", "SECRET", ""),
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/model/anthropic.claude-3-5-sonnet-20240620-v1:0/converse-stream" || req.URL.RawPath != "/model/anthropic.claude-3-5-sonnet-20240620-v1%3A0/converse-stream" {
				t.Fatalf("unexpected path/rawPath: %s/%s", req.URL.Path, req.URL.RawPath)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(testgolden.ReadFixture(t, "../../testdata/bedrock/eventstream.bin"))),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := provider.Stream(context.Background(), &litellm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20240620-v1:0",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hel" {
		t.Fatalf("text = %q", resp.Text())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 7 || resp.Usage.CacheReadTokens != 2 || resp.Usage.CacheWriteTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != litellm.FinishReasonToolCall {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestStreamExposesUnknownContentBlockAsProviderEvent(t *testing.T) {
	stream := newStream(&http.Response{
		Body: io.NopCloser(bytes.NewReader(eventStream(
			`{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"audio":{"bytes":"abc"}}}}`,
			`{"metadata":{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}}`,
		))),
	}, "anthropic.claude")

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	providerEvent, ok := event.(litellm.ProviderEvent)
	if !ok {
		t.Fatalf("event = %#v, want ProviderEvent", event)
	}
	if providerEvent.Name != "bedrock.contentBlockDelta" || !strings.Contains(string(providerEvent.Raw), "audio") {
		t.Fatalf("provider event = %#v", providerEvent)
	}
}

func TestStreamConvertsReasoningDelta(t *testing.T) {
	stream := newStream(&http.Response{
		Body: io.NopCloser(bytes.NewReader(eventStream(
			`{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"think","signature":"sig"}}}}`,
			`{"metadata":{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}}`,
		))),
	}, "anthropic.claude")
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Reasoning() != "think" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || reasoning.Signature != "sig" {
		t.Fatalf("reasoning block = %#v", resp.Blocks[0])
	}
}

func TestStreamConvertsExceptionEventsToErrors(t *testing.T) {
	stream := newStream(&http.Response{
		Body: io.NopCloser(bytes.NewReader(eventStream(
			`{"throttlingException":{"message":"too many requests"}}`,
		))),
	}, "anthropic.claude")
	_, err := stream.Next()
	if err == nil || !litellm.IsRateLimitError(err) || !strings.Contains(err.Error(), "too many requests") {
		t.Fatalf("expected rate limit stream error, got %v", err)
	}
}

func TestStreamRejectsEOFBeforeMetadata(t *testing.T) {
	stream := newStream(&http.Response{
		Body: io.NopCloser(bytes.NewReader(eventStream(
			`{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"text":"partial"}}}`,
		))),
	}, "anthropic.claude")
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "before metadata") || !litellm.IsProviderError(err) {
		t.Fatalf("expected truncated stream error, got %v", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func eventStream(payloads ...string) []byte {
	var out bytes.Buffer
	for _, payload := range payloads {
		data := []byte(payload)
		totalLength := uint32(16 + len(data))
		var prelude [12]byte
		binary.BigEndian.PutUint32(prelude[0:4], totalLength)
		binary.BigEndian.PutUint32(prelude[4:8], 0)
		out.Write(prelude[:])
		out.Write(data)
		out.Write([]byte{0, 0, 0, 0})
	}
	return out.Bytes()
}

func mustProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := New(Config{
		Region:      "us-east-1",
		Credentials: StaticCredentials("AKID", "SECRET", ""),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return provider
}

func mustTool(t *testing.T, name, description string, schema any) litellm.Tool {
	t.Helper()
	tool, err := litellm.NewTool(name, description, schema)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}
