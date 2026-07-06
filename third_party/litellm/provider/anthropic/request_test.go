package anthropic

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

func TestBuildRequestThinkingToolsCacheRoundTrip(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	temp := 1.0
	req := &litellm.Request{
		Model:       "claude-sonnet-4-5",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []litellm.Message{
			litellm.System("You are helpful."),
			litellm.User(
				litellm.TextBlock{
					Text: "Use the tool.",
					Cache: &litellm.CacheControl{
						Type: litellm.CacheTypeEphemeral,
						TTL:  litellm.CacheTTL1h,
					},
				},
			),
			litellm.Assistant(
				litellm.ReasoningBlock{Text: "I should call the tool.", Signature: "sig-thinking"},
				litellm.ToolUseBlock{ID: "toolu_1", Name: "lookup", Arguments: litellm.MustJSONRaw(map[string]any{"q": "x"})},
			),
			litellm.ToolResult("toolu_1",
				litellm.Text("result text"),
				litellm.ToolReferenceBlock{ToolName: "lookup"},
			),
			litellm.Assistant(litellm.Text("done")),
		},
		Tools: []litellm.Tool{
			mustTool(t, "lookup", "Lookup data.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q": map[string]any{"type": "string"},
				},
				"required": []string{"q"},
			}),
		},
		Thinking: &litellm.Thinking{
			Mode:   litellm.ThinkingEnabled,
			Effort: "low",
		},
	}
	wire, err := provider.buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	testgolden.AssertJSON(t, "../../testdata/anthropic/request_tools_cache.golden.json", wire)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	jsonText := string(data)
	for _, want := range []string{
		`"thinking":"I should call the tool."`,
		`"signature":"sig-thinking"`,
		`"type":"tool_use"`,
		`"tool_use_id":"toolu_1"`,
		`"type":"tool_reference"`,
		`"cache_control":{"type":"ephemeral","ttl":"1h"}`,
		`"budget_tokens":2048`,
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("wire JSON missing %s:\n%s", want, jsonText)
		}
	}
	if strings.Index(jsonText, `"type":"thinking"`) > strings.Index(jsonText, `"type":"tool_use"`) {
		t.Fatalf("thinking block must precede tool_use block:\n%s", jsonText)
	}
	if strings.Index(jsonText, `"type":"tool_use"`) > strings.Index(jsonText, `"type":"tool_result"`) {
		t.Fatalf("tool_use must precede tool_result:\n%s", jsonText)
	}
}

func TestBuildRequestUsesRedactedThinkingData(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.Assistant(litellm.ReasoningBlock{Redacted: []byte("opaque")}),
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	jsonText := string(data)
	if !strings.Contains(jsonText, `"type":"redacted_thinking"`) || !strings.Contains(jsonText, `"data":"opaque"`) {
		t.Fatalf("redacted thinking not encoded with data field:\n%s", jsonText)
	}
	if strings.Contains(jsonText, `"content":"opaque"`) {
		t.Fatalf("redacted thinking must not use content field:\n%s", jsonText)
	}
}

func TestBuildRequestRejectsInvalidBlockCache(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.User(litellm.TextBlock{
				Text:  "hi",
				Cache: &litellm.CacheControl{Type: litellm.CacheTypeEphemeral, TTL: "24h"},
			}),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported cache ttl") {
		t.Fatalf("expected cache ttl error, got %v", err)
	}

	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.User(litellm.TextBlock{
				Text:  "hi",
				Cache: &litellm.CacheControl{Type: "persistent"},
			}),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported cache type") {
		t.Fatalf("expected cache type error, got %v", err)
	}
}

func TestBuildRequestRejectsOneHourCacheAfterFiveMinuteCache(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.User(litellm.TextBlock{
				Text:  "short cache first",
				Cache: &litellm.CacheControl{Type: litellm.CacheTypeEphemeral, TTL: litellm.CacheTTL5m},
			}),
			litellm.User(litellm.TextBlock{
				Text:  "long cache later",
				Cache: &litellm.CacheControl{Type: litellm.CacheTypeEphemeral, TTL: litellm.CacheTTL1h},
			}),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "1h cache_control must appear before 5m") {
		t.Fatalf("expected cache order error, got %v", err)
	}
}

func TestBuildRequestAllowsOneHourCacheBeforeFiveMinuteCache(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.User(litellm.TextBlock{
				Text:  "long cache first",
				Cache: &litellm.CacheControl{Type: litellm.CacheTypeEphemeral, TTL: litellm.CacheTTL1h},
			}),
			litellm.User(litellm.TextBlock{
				Text:  "short cache later",
				Cache: &litellm.CacheControl{Type: litellm.CacheTypeEphemeral, TTL: litellm.CacheTTL5m},
			}),
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
}

func TestBuildRequestRejectsSilentThinkingBudgetDefault(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "budget_tokens or effort is required") {
		t.Fatalf("expected budget error, got %v", err)
	}
}

func TestBuildRequestRejectsUnknownThinkingEffort(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "extreme"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `unknown thinking effort "extreme"`) {
		t.Fatalf("expected unknown effort error, got %v", err)
	}
}

func TestBuildRequestMapsMaxThinkingEffort(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 65536
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "max"},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if wire.Thinking == nil || wire.Thinking.BudgetTokens == nil || *wire.Thinking.BudgetTokens != 32768 {
		t.Fatalf("thinking = %+v, want budget 32768", wire.Thinking)
	}
}

func TestBuildRequestRejectsThinkingBudgetEqualToMaxTokens(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 1024
	budget := 1024
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "budget_tokens must be < max_tokens") {
		t.Fatalf("expected budget/max_tokens error, got %v", err)
	}
}

func TestBuildRequestValidatesThinkingTopP(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 2048
	budget := 1024
	topP := 0.94
	_, err = provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		TopP:      &topP,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "top_p must be between 0.95 and 1") {
		t.Fatalf("expected top_p error, got %v", err)
	}

	topP = 0.95
	if _, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		TopP:      &topP,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		Thinking:  &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
	}, false); err != nil {
		t.Fatalf("buildRequest returned error for top_p 0.95: %v", err)
	}
}

func TestBuildRequestRejectsForcedToolChoiceWithThinking(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 2048
	budget := 1024
	_, err = provider.buildRequest(&litellm.Request{
		Model:      "claude",
		MaxTokens:  &maxTokens,
		Messages:   []litellm.Message{litellm.UserText("hi")},
		Thinking:   &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
		ToolChoice: map[string]any{"type": "tool", "name": "lookup"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `tool_choice "tool" is not supported`) {
		t.Fatalf("expected tool_choice error, got %v", err)
	}

	if _, err := provider.buildRequest(&litellm.Request{
		Model:      "claude",
		MaxTokens:  &maxTokens,
		Messages:   []litellm.Message{litellm.UserText("hi")},
		Thinking:   &litellm.Thinking{Mode: litellm.ThinkingEnabled, BudgetTokens: &budget},
		ToolChoice: map[string]any{"type": "none"},
	}, false); err != nil {
		t.Fatalf("buildRequest returned error for tool_choice none: %v", err)
	}
}

func TestBuildRequestRequiresMaxTokens(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.buildRequest(&litellm.Request{
		Model:    "claude",
		Messages: []litellm.Message{litellm.UserText("hi")},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "max_tokens is required") {
		t.Fatalf("expected max_tokens error, got %v", err)
	}
}

func TestChatReturnsStructuredValidationError(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "claude",
		Messages: []litellm.Message{litellm.UserText("hi")},
	})
	if err == nil || !litellm.IsValidationError(err) {
		t.Fatalf("expected structured validation error, got %v", err)
	}
}

func TestBuildRequestRejectsTemperatureAndTopP(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 1024
	temp := 0.7
	topP := 0.9
	_, err = provider.buildRequest(&litellm.Request{
		Model:       "claude",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
		Messages:    []litellm.Message{litellm.UserText("hi")},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "temperature and top_p cannot both be set") {
		t.Fatalf("expected temperature/top_p error, got %v", err)
	}
}

func TestBuildRequestKeepsTopPWhenTemperatureUnset(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 1024
	topP := 0.9
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		TopP:      &topP,
		Messages:  []litellm.Message{litellm.UserText("hi")},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if wire.TopP == nil || *wire.TopP != topP {
		t.Fatalf("top_p = %v, want %v", wire.TopP, topP)
	}
}

func TestBuildRequestProviderOptions(t *testing.T) {
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 1024
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			"metadata_user_id": "user-123",
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if wire.Metadata["user_id"] != "user-123" {
		t.Fatalf("metadata = %#v", wire.Metadata)
	}

	_, err = provider.buildRequest(&litellm.Request{
		Model:           "claude",
		MaxTokens:       &maxTokens,
		Messages:        []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{"unknown": true},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported provider option") {
		t.Fatalf("expected unsupported option error, got %v", err)
	}
}

func TestStreamConvertsSSEToTypedEvents(t *testing.T) {
	maxTokens := 2048
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Accept") != "text/event-stream" {
				t.Fatalf("Accept = %q, want text/event-stream", req.Header.Get("Accept"))
			}
			return streamResponse(testgolden.ReadFixtureString(t, "../../testdata/anthropic/messages_stream.sse")), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := provider.Stream(context.Background(), &litellm.Request{
		Model:     "claude-sonnet",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hello" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Reasoning() != "think" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	if len(resp.Blocks) == 0 {
		t.Fatalf("blocks empty")
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || reasoning.Signature != "sig-thinking" {
		t.Fatalf("reasoning block = %+v, want signature", resp.Blocks[0])
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 7 || resp.Usage.CacheReadTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != litellm.FinishReasonToolCall {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestChatSetsProviderHeaders(t *testing.T) {
	maxTokens := 2048
	provider, err := New(Config{
		APIKey:    "test-key",
		BaseURL:   "https://example.test",
		Beta:      "beta-name",
		UserAgent: "my-client/1.0",
		Headers:   map[string]string{"X-Custom-Client": "my-client"},
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("User-Agent"); got != "my-client/1.0" {
				t.Fatalf("User-Agent = %q", got)
			}
			if got := req.Header.Get("anthropic-beta"); got != "beta-name" {
				t.Fatalf("anthropic-beta = %q", got)
			}
			if got := req.Header.Get("X-Custom-Client"); got != "my-client" {
				t.Fatalf("X-Custom-Client = %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages:  []litellm.Message{litellm.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("text = %q", resp.Text())
	}
}

func TestConvertResponsePreservesRedactedThinkingData(t *testing.T) {
	resp, err := convertResponse(&anthropicResponse{
		Model: "claude",
		Content: []anthropicContent{
			{Type: "redacted_thinking", Data: "opaque"},
		},
	}, "fallback")
	if err != nil {
		t.Fatalf("convertResponse returned error: %v", err)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("blocks = %#v", resp.Blocks)
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || string(reasoning.Redacted) != "opaque" {
		t.Fatalf("reasoning block = %#v", resp.Blocks[0])
	}
}

func TestConvertResponseMapsThinkingTokenUsage(t *testing.T) {
	resp, err := convertResponse(&anthropicResponse{
		Model: "claude",
		Usage: anthropicUsage{
			InputTokens:  5,
			OutputTokens: 9,
			OutputTokensDetails: &anthropicOutputTokensDetails{
				ThinkingTokens: 4,
			},
		},
		Content: []anthropicContent{{Type: "text", Text: "ok"}},
	}, "fallback")
	if err != nil {
		t.Fatalf("convertResponse returned error: %v", err)
	}
	if resp.Usage.ReasoningTokens != 4 {
		t.Fatalf("reasoning tokens = %d, want 4", resp.Usage.ReasoningTokens)
	}
	if resp.Usage.OutputTokens != 9 || resp.Usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestConvertResponseRejectsUnsupportedContentType(t *testing.T) {
	_, err := convertResponse(&anthropicResponse{
		Model: "claude",
		Content: []anthropicContent{
			{Type: "server_tool_use"},
		},
	}, "fallback")
	if err == nil || !strings.Contains(err.Error(), "unsupported response content type") {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}

func TestConvertResponseRejectsNil(t *testing.T) {
	_, err := convertResponse(nil, "claude")
	if err == nil || !strings.Contains(err.Error(), "response cannot be nil") {
		t.Fatalf("expected nil response error, got %v", err)
	}
}

func TestResponseBlocksRoundTripBackIntoAssistantMessage(t *testing.T) {
	resp, err := convertResponse(&anthropicResponse{
		Model: "claude",
		Content: []anthropicContent{
			{Type: "thinking", Thinking: "Need lookup.", Signature: "sig-thinking"},
			{Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: map[string]any{"q": "x"}},
		},
	}, "fallback")
	if err != nil {
		t.Fatalf("convertResponse returned error: %v", err)
	}
	provider, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 4096
	wire, err := provider.buildRequest(&litellm.Request{
		Model:     "claude",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.Assistant(resp.Blocks...),
			litellm.ToolResultText("toolu_1", "result"),
		},
	}, false)
	if err != nil {
		t.Fatalf("buildRequest returned error: %v", err)
	}
	if len(wire.Messages) != 2 {
		t.Fatalf("messages = %#v", wire.Messages)
	}
	assistant := wire.Messages[0].Content
	if len(assistant) != 2 {
		t.Fatalf("assistant content = %#v", assistant)
	}
	if assistant[0].Type != "thinking" || assistant[0].Signature != "sig-thinking" {
		t.Fatalf("thinking block = %#v", assistant[0])
	}
	if assistant[1].Type != "tool_use" || assistant[1].ID != "toolu_1" || assistant[1].Name != "lookup" {
		t.Fatalf("tool_use block = %#v", assistant[1])
	}
	result := wire.Messages[1].Content[0]
	if result.Type != "tool_result" || result.ToolUseID != "toolu_1" {
		t.Fatalf("tool_result block = %#v", result)
	}
}

func TestStreamPreservesRedactedThinkingData(t *testing.T) {
	stream := newStream(streamResponse(strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")), &litellm.Request{Model: "claude"})
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("blocks = %#v", resp.Blocks)
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || string(reasoning.Redacted) != "opaque" {
		t.Fatalf("reasoning block = %#v", resp.Blocks[0])
	}
}

func TestStreamMapsThinkingTokenUsage(t *testing.T) {
	stream := newStream(streamResponse(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"model":"claude","usage":{"input_tokens":5}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9,"output_tokens_details":{"thinking_tokens":4}}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")), &litellm.Request{Model: "claude"})
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Usage.ReasoningTokens != 4 {
		t.Fatalf("reasoning tokens = %d, want 4", resp.Usage.ReasoningTokens)
	}
	if resp.Usage.OutputTokens != 9 || resp.Usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestStreamRejectsEOFBeforeMessageStop(t *testing.T) {
	stream := newStream(streamResponse(strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		``,
	}, "\n")), &litellm.Request{Model: "claude"})
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "before message_stop") || !litellm.IsProviderError(err) {
		t.Fatalf("expected truncated stream error, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewRejectsAmbiguousTransportConfig(t *testing.T) {
	_, err := New(Config{
		APIKey:     "test-key",
		HTTPClient: roundTripFunc(nil),
		Transport:  roundTripperFunc(nil),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPClient and Transport are mutually exclusive") {
		t.Fatalf("expected HTTPClient/Transport error, got %v", err)
	}

	_, err = New(Config{
		APIKey:     "test-key",
		HTTPClient: roundTripFunc(nil),
		Retry:      retry.DefaultPolicy(),
	})
	if err == nil || !strings.Contains(err.Error(), "Retry cannot be used with a custom HTTPClient") {
		t.Fatalf("expected HTTPClient/Retry error, got %v", err)
	}
}

func streamResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
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
