package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/internal/testgolden"
)

func TestResponsesBuildRequestMapsCoreFields(t *testing.T) {
	provider := mustProvider(t)
	maxOutputTokens := 1200
	maxToolCalls := 4
	topLogprobs := 2
	parallelToolCalls := true
	store := true
	background := false

	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.1",
		Messages: []litellm.Message{
			litellm.System("Follow the contract."),
			litellm.UserText("Search and summarize."),
		},
		Instructions:         "Be concise.",
		PreviousResponseID:   "resp_previous",
		ContextManagement:    []map[string]any{{"type": "compaction", "compact_threshold": 0.75}},
		MaxOutputTokens:      &maxOutputTokens,
		MaxToolCalls:         &maxToolCalls,
		Include:              []string{"reasoning.encrypted_content"},
		TopLogprobs:          &topLogprobs,
		TextVerbosity:        "low",
		Truncation:           "auto",
		OpenAITools:          []ResponsesTool{{"type": "web_search_preview"}},
		ParallelToolCalls:    &parallelToolCalls,
		ReasoningEffort:      "xhigh",
		ReasoningSummary:     "auto",
		PromptCacheKey:       "workflow-v1",
		PromptCacheRetention: "24h",
		Metadata:             map[string]string{"tenant": "acme"},
		SafetyIdentifier:     "user-123",
		ServiceTier:          "flex",
		Store:                &store,
		Background:           &background,
		Prompt:               map[string]any{"id": "pmpt_123"},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}

	if wire.Instructions != "Be concise.\nFollow the contract." {
		t.Fatalf("instructions = %q", wire.Instructions)
	}
	if wire.Input != "Search and summarize." {
		t.Fatalf("input = %#v", wire.Input)
	}
	if wire.PreviousResponseID != "resp_previous" {
		t.Fatalf("previous_response_id = %q", wire.PreviousResponseID)
	}
	if len(wire.ContextManagement) != 1 || wire.ContextManagement[0]["type"] != "compaction" {
		t.Fatalf("context_management = %#v", wire.ContextManagement)
	}
	if wire.MaxOutputTokens == nil || *wire.MaxOutputTokens != maxOutputTokens {
		t.Fatalf("max_output_tokens = %v", wire.MaxOutputTokens)
	}
	if wire.Text == nil || wire.Text.Verbosity != "low" {
		t.Fatalf("text = %#v", wire.Text)
	}
	if wire.Reasoning == nil || wire.Reasoning.Effort != "xhigh" || wire.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning = %#v", wire.Reasoning)
	}
	tools, err := json.Marshal(wire.Tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if string(tools) != `[{"type":"web_search_preview"}]` {
		t.Fatalf("tools = %s", tools)
	}
	if wire.Prompt["id"] != "pmpt_123" || wire.Metadata["tenant"] != "acme" || wire.ServiceTier != "flex" || wire.Store == nil || !*wire.Store {
		t.Fatalf("wire = %#v", wire)
	}
}

func TestResponsesAcceptsNativeInputWithoutMessages(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.1",
		Input: []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "hello"},
				},
			},
		},
		Instructions: "Be concise.",
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if wire.Instructions != "Be concise." {
		t.Fatalf("instructions = %q", wire.Instructions)
	}
	items, ok := wire.Input.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("input = %#v", wire.Input)
	}
}

func TestResponsesAcceptsPromptOnlyRequest(t *testing.T) {
	provider := mustProvider(t)
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.1",
		Prompt: map[string]any{
			"id": "pmpt_123",
			"variables": map[string]any{
				"topic": "cache",
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if wire.Input != nil {
		t.Fatalf("input = %#v, want nil", wire.Input)
	}
	if wire.Prompt["id"] != "pmpt_123" {
		t.Fatalf("prompt = %#v", wire.Prompt)
	}
}

func TestResponsesRejectsInputWithNonSystemMessages(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.1",
		Input: "hello",
		Messages: []litellm.Message{
			litellm.UserText("also hello"),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "cannot set Input and non-system Messages") {
		t.Fatalf("expected input/messages conflict error, got %v", err)
	}
}

func TestResponsesStreamOptions(t *testing.T) {
	provider := mustProvider(t)
	includeObfuscation := false
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		StreamOptions: &ResponsesStreamOptions{
			IncludeObfuscation: &includeObfuscation,
		},
	}, true)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if wire.StreamOptions == nil || wire.StreamOptions.IncludeObfuscation == nil || *wire.StreamOptions.IncludeObfuscation {
		t.Fatalf("stream_options = %#v", wire.StreamOptions)
	}

	_, err = provider.buildResponsesRequest(&ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		StreamOptions: &ResponsesStreamOptions{
			IncludeObfuscation: &includeObfuscation,
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "stream_options requires stream request") {
		t.Fatalf("expected non-stream stream_options error, got %v", err)
	}
}

func TestResponsesRejectsMinimalReasoningEffort(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:           "gpt-5.1",
		Messages:        []litellm.Message{litellm.UserText("hello")},
		ReasoningEffort: "minimal",
	}, false)
	if err == nil || !strings.Contains(err.Error(), `reasoning_effort must be one of none, low, medium, high, xhigh`) {
		t.Fatalf("expected minimal reasoning_effort error, got %v", err)
	}
}

func TestResponsesInputItemsPreserveToolTurns(t *testing.T) {
	items, err := responsesInputItems([]litellm.Message{
		litellm.UserText("weather?"),
		litellm.Assistant(
			litellm.Text("let me check"),
			litellm.ToolUseBlock{ID: "call_1", Name: "get_weather", Arguments: litellm.MustJSONRaw(map[string]any{"city": "Paris"})},
		),
		litellm.ToolResultText("call_1", `{"temp":"15C"}`),
	})
	if err != nil {
		t.Fatalf("responsesInputItems: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("items len = %d, want 4: %+v", len(items), items)
	}
	if items[0].Type != "message" || items[0].Role != "user" || items[0].Content[0].Type != "input_text" {
		t.Fatalf("user item = %+v", items[0])
	}
	if items[1].Type != "message" || items[1].Role != "assistant" || items[1].Content[0].Type != "output_text" {
		t.Fatalf("assistant item = %+v", items[1])
	}
	if items[2].Type != "function_call" || items[2].CallID != "call_1" || items[2].Name != "get_weather" {
		t.Fatalf("function call item = %+v", items[2])
	}
	if items[3].Type != "function_call_output" || items[3].CallID != "call_1" || items[3].Output == "" {
		t.Fatalf("function output item = %+v", items[3])
	}
}

func TestResponsesTextFormatUsesResponsesJSONSchemaShape(t *testing.T) {
	provider := mustProvider(t)
	format, err := litellm.NewResponseFormatJSONSchema("answer", "Answer shape.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required": []string{"answer"},
	}, litellm.StrictEnabled)
	if err != nil {
		t.Fatalf("NewResponseFormatJSONSchema: %v", err)
	}
	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:          "gpt-5.1",
		Messages:       []litellm.Message{litellm.UserText("hello")},
		ResponseFormat: format,
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v", body["text"])
	}
	formatBody, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format = %#v", text["format"])
	}
	if _, ok := formatBody["json_schema"]; ok {
		t.Fatalf("responses text.format must not use chat response_format nesting: %s", data)
	}
	if formatBody["type"] != "json_schema" || formatBody["name"] != "answer" || formatBody["description"] != "Answer shape." || formatBody["strict"] != true {
		t.Fatalf("format = %#v", formatBody)
	}
	schema, ok := formatBody["schema"].(map[string]any)
	if !ok || schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema = %#v", formatBody["schema"])
	}
}

func TestResponsesBuildRequestDeepClonesNativeMaps(t *testing.T) {
	provider := mustProvider(t)
	req := &ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		OpenAITools: []ResponsesTool{{
			"type": "web_search_preview",
			"filters": map[string]any{
				"domains": []any{"example.com"},
			},
		}},
		Prompt: map[string]any{
			"id": "pmpt_123",
			"variables": map[string]any{
				"tags": []any{"a", "b"},
			},
		},
	}
	wire, err := provider.buildResponsesRequest(req, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	wire.Prompt["variables"].(map[string]any)["tags"].([]any)[0] = "mutated"
	wire.Tools[0].Raw["filters"].(map[string]any)["domains"].([]any)[0] = "mutated.test"

	if req.Prompt["variables"].(map[string]any)["tags"].([]any)[0] != "a" {
		t.Fatalf("prompt mutated: %#v", req.Prompt)
	}
	if req.OpenAITools[0]["filters"].(map[string]any)["domains"].([]any)[0] != "example.com" {
		t.Fatalf("openai tool mutated: %#v", req.OpenAITools)
	}
}

func TestResponsesInputEncodesReasoningHistory(t *testing.T) {
	items, err := responsesInputItems([]litellm.Message{
		litellm.Assistant(litellm.ReasoningBlock{Text: "think", Summary: true}),
	})
	if err != nil {
		t.Fatalf("responsesInputItems: %v", err)
	}
	if len(items) != 1 || items[0].Type != "reasoning" || len(items[0].Summary) != 1 || items[0].Summary[0].Text != "think" {
		t.Fatalf("items = %#v", items)
	}
}

func TestResponsesInputRejectsOpaqueReasoningHistory(t *testing.T) {
	_, err := responsesInputItems([]litellm.Message{
		litellm.Assistant(litellm.ReasoningBlock{Text: "think", Signature: "sig"}),
	})
	if err == nil || !strings.Contains(err.Error(), "only support text summary or provider extra state") {
		t.Fatalf("expected opaque reasoning history error, got %v", err)
	}
}

func TestResponsesRejectsNonTextSystemInstructions(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model: "gpt-5.1",
		Messages: []litellm.Message{
			{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.ImageURL("https://example.test/a.png")}},
			litellm.UserText("hello"),
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "system message") || !strings.Contains(err.Error(), "only text blocks") {
		t.Fatalf("expected non-text system error, got %v", err)
	}
}

func TestResponsesInputRejectsNonTextToolResultOutput(t *testing.T) {
	_, err := responsesInputItems([]litellm.Message{
		litellm.Assistant(litellm.ToolUseBlock{ID: "call_1", Name: "lookup", Arguments: litellm.MustJSONRaw(map[string]any{})}),
		litellm.ToolResult("call_1", litellm.ImageURL("https://example.test/a.png")),
	})
	if err == nil || !strings.Contains(err.Error(), "tool result") || !strings.Contains(err.Error(), "only text blocks") {
		t.Fatalf("expected non-text tool result error, got %v", err)
	}
}

func TestResponsesToolsDefaultLeavesSchemaUnchanged(t *testing.T) {
	tool, err := litellm.NewTool("lookup", "Lookup.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
		"default": map[string]any{"q": "x"},
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	converted, err := responsesTools([]litellm.Tool{tool}, nil)
	if err != nil {
		t.Fatalf("responsesTools: %v", err)
	}
	if len(converted) != 1 || converted[0].Strict != nil {
		t.Fatalf("tool strict = %#v", converted)
	}
	params := converted[0].Parameters.(map[string]any)
	if _, ok := params["additionalProperties"]; ok {
		t.Fatalf("default mode should not add additionalProperties: %#v", params)
	}
	if got, ok := params["default"]; !ok {
		t.Fatalf("default mode should preserve schema fields: %#v", params)
	} else if got.(map[string]any)["q"] != "x" {
		t.Fatalf("default = %#v", got)
	}
}

func TestResponsesToolsStrictEnabledNormalizesSchema(t *testing.T) {
	tool, err := litellm.NewTool("lookup", "Lookup.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string", "default": "x"},
		},
		"required": []string{"q"},
		"default":  map[string]any{"q": "x"},
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	tool.Strict = litellm.StrictEnabled

	converted, err := responsesTools([]litellm.Tool{tool}, nil)
	if err != nil {
		t.Fatalf("responsesTools: %v", err)
	}
	if len(converted) != 1 || converted[0].Strict == nil || !*converted[0].Strict {
		t.Fatalf("tool strict = %#v", converted)
	}
	params := converted[0].Parameters.(map[string]any)
	if params["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v", params["additionalProperties"])
	}
	if _, ok := params["default"]; ok {
		t.Fatalf("default should be removed from strict schema: %#v", params)
	}
	props := params["properties"].(map[string]any)
	q := props["q"].(map[string]any)
	if _, ok := q["default"]; ok {
		t.Fatalf("nested default should be removed from strict schema: %#v", q)
	}
}

func TestResponsesToolsStrictEnabledRejectsSchemaMissingRequired(t *testing.T) {
	tool, err := litellm.NewTool("lookup", "Lookup.", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	tool.Strict = litellm.StrictEnabled

	_, err = responsesTools([]litellm.Tool{tool}, nil)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected strict schema error, got %v", err)
	}
}

func TestResponsesRejectsConversationWithPreviousResponseID(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:              "gpt-5.1",
		Messages:           []litellm.Message{litellm.UserText("hello")},
		Conversation:       "conv_123",
		PreviousResponseID: "resp_123",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got %v", err)
	}
}

func TestResponsesRejectsInvalidPromptCacheRetention(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:                "gpt-5.1",
		Messages:             []litellm.Message{litellm.UserText("hello")},
		PromptCacheRetention: "forever",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "prompt_cache_retention") {
		t.Fatalf("expected prompt cache retention error, got %v", err)
	}
}

func TestResponsesReturnsStructuredValidationError(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.Responses(context.Background(), &ResponsesRequest{
		Model:              "gpt-5.1",
		Messages:           []litellm.Message{litellm.UserText("hello")},
		Conversation:       "conv_123",
		PreviousResponseID: "resp_123",
	})
	if err == nil || !litellm.IsValidationError(err) {
		t.Fatalf("expected structured validation error, got %v", err)
	}
}

func TestResponsesAPIChatRoutesToResponsesEndpoint(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]any
	provider, err := New(Config{
		API:     APIResponses,
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedPath = req.URL.Path
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{
				"id":"resp_123",
				"model":"gpt-5.1",
				"status":"completed",
				"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],
				"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	maxTokens := 128
	parallelToolCalls := false
	resp, err := provider.Chat(context.Background(), &litellm.Request{
		Model:     "gpt-5.1",
		MaxTokens: &maxTokens,
		Messages: []litellm.Message{
			litellm.System("Follow instructions."),
			litellm.UserText("hi"),
		},
		Tools:    []litellm.Tool{mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "medium"},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionStore:             true,
			ProviderOptionMetadata:          map[string]any{"tenant": "acme"},
			ProviderOptionParallelToolCalls: parallelToolCalls,
			ProviderOptionVerbosity:         "low",
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("Text = %q", resp.Text())
	}
	if capturedPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", capturedPath)
	}
	if capturedBody["input"] != "hi" || capturedBody["instructions"] != "Follow instructions." {
		t.Fatalf("input/instructions = %#v/%#v", capturedBody["input"], capturedBody["instructions"])
	}
	if capturedBody["max_output_tokens"] != float64(maxTokens) {
		t.Fatalf("max_output_tokens = %#v", capturedBody["max_output_tokens"])
	}
	reasoning := capturedBody["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	text := capturedBody["text"].(map[string]any)
	if text["verbosity"] != "low" {
		t.Fatalf("text = %#v", text)
	}
	if capturedBody["store"] != true || capturedBody["parallel_tool_calls"] != false {
		t.Fatalf("store/parallel_tool_calls = %#v/%#v", capturedBody["store"], capturedBody["parallel_tool_calls"])
	}
}

func TestResponsesAPIStreamRoutesToResponsesEndpoint(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]any
	provider, err := New(Config{
		API:     APIResponses,
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedPath = req.URL.Path
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return streamResponse(strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"o"}`,
				`data: {"type":"response.output_text.delta","delta":"k"}`,
				`data: {"type":"response.completed","response":{"model":"gpt-5.1","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
				"",
			}, "\n\n")), nil
		}),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	includeObfuscation := false
	stream, err := provider.Stream(context.Background(), &litellm.Request{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hi")},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionStreamOptions: map[string]any{"include_obfuscation": includeObfuscation},
		},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("Text = %q", resp.Text())
	}
	if capturedPath != "/v1/responses" || capturedBody["stream"] != true {
		t.Fatalf("path/stream = %q/%#v", capturedPath, capturedBody["stream"])
	}
	streamOptions := capturedBody["stream_options"].(map[string]any)
	if streamOptions["include_obfuscation"] != false {
		t.Fatalf("stream_options = %#v", streamOptions)
	}
}

func TestResponsesAPIRejectsChatOnlyProviderOption(t *testing.T) {
	provider, err := New(Config{API: APIResponses, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		ProviderOptions: litellm.ProviderOptions{
			ProviderOptionFrequencyPenalty: 0.2,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "only supported with chat completions API") || !litellm.IsValidationError(err) {
		t.Fatalf("expected chat-only provider option validation error, got %v", err)
	}
}

func TestResponsesAPIRejectsUnsupportedRequestField(t *testing.T) {
	provider, err := New(Config{API: APIResponses, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		Stop:     []string{"END"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support request field") || !litellm.IsValidationError(err) {
		t.Fatalf("expected unsupported request field validation error, got %v", err)
	}
}

func TestResponsesAPIRejectsMinimalThinkingEffort(t *testing.T) {
	provider, err := New(Config{API: APIResponses, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = provider.Chat(context.Background(), &litellm.Request{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, Effort: "minimal"},
	})
	if err == nil || !strings.Contains(err.Error(), "reasoning_effort must be one of none, low, medium, high, xhigh") || !litellm.IsValidationError(err) {
		t.Fatalf("expected minimal reasoning_effort validation error, got %v", err)
	}
}

func TestResponsesThinkingRequiresExplicitMapping(t *testing.T) {
	provider := mustProvider(t)
	_, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "thinking effort, summary, or include_output is required") {
		t.Fatalf("expected thinking mapping error, got %v", err)
	}

	wire, err := provider.buildResponsesRequest(&ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
		Thinking: &litellm.Thinking{Mode: litellm.ThinkingEnabled, IncludeOutput: true},
	}, false)
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if wire.Reasoning == nil || wire.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning = %#v", wire.Reasoning)
	}
}

func TestResponsesConvertsOutputBlocks(t *testing.T) {
	resp, err := convertResponsesResponse(&responsesResponse{
		Model:  "gpt-5.1",
		Status: "completed",
		Output: []responsesOutputItem{
			{Type: "reasoning", Summary: []responsesSummaryItem{{Text: "think"}}},
			{Type: "message", Content: []responsesContentItem{{Type: "output_text", Text: "answer"}}},
			{Type: "function_call", ID: "fc_1", CallID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`},
		},
		Usage: responsesUsage{
			InputTokens:         3,
			OutputTokens:        4,
			TotalTokens:         7,
			InputTokensDetails:  &responsesInputTokensDetails{CachedTokens: 2},
			OutputTokensDetails: &responsesOutputTokensDetails{ReasoningTokens: 1},
		},
	}, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	if resp.Reasoning() != "think" || resp.Text() != "answer" {
		t.Fatalf("reasoning/text = %q/%q", resp.Reasoning(), resp.Text())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.FinishReason != litellm.FinishReasonToolCall {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 4 || resp.Usage.CacheReadTokens != 2 || resp.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestResponsesReasoningItemRoundTripsWithEncryptedContent(t *testing.T) {
	resp, err := convertResponsesResponse(&responsesResponse{
		Model:  "gpt-5.1",
		Status: "completed",
		Output: []responsesOutputItem{
			{
				ID:               "rs_1",
				Type:             "reasoning",
				Status:           "completed",
				EncryptedContent: "enc_123",
				Summary:          []responsesSummaryItem{{Text: "think"}},
				Raw:              json.RawMessage(`{"id":"rs_1","type":"reasoning","status":"completed","encrypted_content":"enc_123","summary":[{"text":"think"}]}`),
			},
			{Type: "function_call", ID: "fc_1", CallID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`},
		},
	}, "")
	if err != nil {
		t.Fatalf("convertResponsesResponse: %v", err)
	}
	reasoning, ok := resp.Blocks[0].(litellm.ReasoningBlock)
	if !ok || reasoning.Text != "think" || !reasoning.Summary || !strings.Contains(string(reasoning.Extra), `"encrypted_content":"enc_123"`) {
		t.Fatalf("reasoning block = %#v", resp.Blocks[0])
	}

	items, err := responsesInputItems([]litellm.Message{litellm.Assistant(resp.Blocks...)})
	if err != nil {
		t.Fatalf("responsesInputItems: %v", err)
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	if !strings.Contains(string(data), `"encrypted_content":"enc_123"`) || !strings.Contains(string(data), `"call_id":"call_1"`) {
		t.Fatalf("round-trip items lost provider state: %s", data)
	}
}

func TestConvertResponsesResponseRejectsNil(t *testing.T) {
	_, err := convertResponsesResponse(nil, "gpt-5.1")
	if err == nil || !strings.Contains(err.Error(), "responses response cannot be nil") {
		t.Fatalf("expected nil responses response error, got %v", err)
	}
}

func TestResponsesRejectsInvalidToolCallArguments(t *testing.T) {
	_, err := convertResponsesResponse(&responsesResponse{
		Model: "gpt-5.1",
		Output: []responsesOutputItem{
			{Type: "function_call", ID: "fc_1", CallID: "call_1", Name: "lookup", Arguments: `{"q":`},
		},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "arguments are not valid JSON") {
		t.Fatalf("expected invalid arguments error, got %v", err)
	}
}

func TestResponsesRejectsUnsupportedOutputItem(t *testing.T) {
	_, err := convertResponsesResponse(&responsesResponse{
		Model: "gpt-5.1",
		Output: []responsesOutputItem{
			{Type: "computer_call", ID: "item_1"},
		},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported responses output item type") {
		t.Fatalf("expected unsupported output item error, got %v", err)
	}
}

func TestResponsesRejectsUnsupportedContentItem(t *testing.T) {
	_, err := convertResponsesResponse(&responsesResponse{
		Model: "gpt-5.1",
		Output: []responsesOutputItem{
			{Type: "message", Content: []responsesContentItem{{Type: "audio", Text: "sound"}}},
		},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported responses content item type") {
		t.Fatalf("expected unsupported content item error, got %v", err)
	}
}

func TestResponsesSendsRequestToEndpoint(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]any
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			capturedPath = req.URL.Path
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"model":"gpt-5.1",
					"status":"completed",
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],
					"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
				}`)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := provider.Responses(context.Background(), &ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	if capturedPath != "/v1/responses" {
		t.Fatalf("path = %q", capturedPath)
	}
	if capturedBody["input"] != "hello" {
		t.Fatalf("body = %#v", capturedBody)
	}
	if resp.Text() != "ok" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Raw) != 0 {
		t.Fatalf("raw should be empty by default: %s", resp.Raw)
	}
}

func TestResponsesCanCaptureRawResponse(t *testing.T) {
	const body = `{"model":"gpt-5.1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := provider.Responses(context.Background(), &ResponsesRequest{
		Model:              "gpt-5.1",
		Messages:           []litellm.Message{litellm.UserText("hello")},
		CaptureRawResponse: true,
	})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	if string(resp.Raw) != body {
		t.Fatalf("raw = %s, want %s", resp.Raw, body)
	}
}

func TestResponsesStreamCollectsTypedEvents(t *testing.T) {
	provider, err := New(Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body["stream"] != true {
				t.Fatalf("stream flag = %#v", body["stream"])
			}
			return streamResponse(testgolden.ReadFixtureString(t, "../../testdata/openai/responses_stream.sse")), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.ResponsesStream(context.Background(), &ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("ResponsesStream: %v", err)
	}
	var sawProviderEvent bool
	events := make([]litellm.Event, 0)
	for {
		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		events = append(events, event)
		if providerEvent, ok := event.(litellm.ProviderEvent); ok && providerEvent.Name == "response.code_interpreter_call.in_progress" {
			sawProviderEvent = true
		}
		if _, ok := event.(litellm.DoneEvent); ok {
			break
		}
	}
	if !sawProviderEvent {
		t.Fatalf("expected hosted tool lifecycle ProviderEvent, got %#v", events)
	}
	stream = &sliceStream{events: events}
	resp, err := litellm.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.Text() != "hel" || resp.Reasoning() != "think " {
		t.Fatalf("text/reasoning = %q/%q", resp.Text(), resp.Reasoning())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 2 || resp.Usage.OutputTokens != 3 || resp.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != litellm.FinishReasonStop {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
}

func TestResponsesStreamIdleTimeout(t *testing.T) {
	provider, err := New(Config{
		APIKey:            "test-key",
		BaseURL:           "https://example.test",
		StreamIdleTimeout: 10 * time.Millisecond,
		HTTPClient: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: &contextBlockingBody{
					ctx: req.Context(),
					prefix: []byte(strings.Join([]string{
						`event: response.output_text.delta`,
						`data: {"type":"response.output_text.delta","delta":"hi"}`,
						``,
						``,
					}, "\n")),
				},
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.ResponsesStream(context.Background(), &ResponsesRequest{
		Model:    "gpt-5.1",
		Messages: []litellm.Message{litellm.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("ResponsesStream: %v", err)
	}
	defer stream.Close()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if delta, ok := event.(litellm.ContentDelta); !ok || delta.Text != "hi" {
		t.Fatalf("event = %#v, want content delta hi", event)
	}
	_, err = stream.Next()
	if err == nil || !litellm.IsTimeoutError(err) || !litellm.IsStreamIdleError(err) {
		t.Fatalf("expected stream idle timeout, got %v", err)
	}
}

func TestResponsesStreamErrorEventSurfaces(t *testing.T) {
	stream := newResponsesStream(streamResponse(strings.Join([]string{
		`event: error`,
		`data: {"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`,
		``,
	}, "\n")), "gpt-5.1")
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "bad request") || !litellm.IsProviderError(err) {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestResponsesStreamFailedEventSurfacesStructuredError(t *testing.T) {
	stream := newResponsesStream(streamResponse(strings.Join([]string{
		`event: response.failed`,
		`data: {"type":"response.failed","response":{"error":{"code":"server_error","message":"failed"}}}`,
		``,
	}, "\n")), "gpt-5.1")
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "failed") || !litellm.IsProviderError(err) {
		t.Fatalf("expected structured failed event error, got %v", err)
	}
}

func TestResponsesStreamRejectsEOFBeforeCompleted(t *testing.T) {
	stream := newResponsesStream(streamResponse(strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
	}, "\n")), "gpt-5.1")
	_, err := litellm.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "before response.completed") || !litellm.IsProviderError(err) {
		t.Fatalf("expected truncated responses stream error, got %v", err)
	}
}

func TestResponsesStreamRejectsDataWithoutEventType(t *testing.T) {
	stream := newResponsesStream(streamResponse(strings.Join([]string{
		`data: {"delta":"orphan"}`,
		``,
	}, "\n")), "gpt-5.1")
	_, err := stream.Next()
	if err == nil || !strings.Contains(err.Error(), "event missing type") || !litellm.IsProviderError(err) {
		t.Fatalf("expected missing type error, got %v", err)
	}
}

type sliceStream struct {
	events []litellm.Event
	index  int
}

func (s *sliceStream) Next() (litellm.Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *sliceStream) Close() error {
	return nil
}

type contextBlockingBody struct {
	ctx    context.Context
	prefix []byte
	offset int
}

func (b *contextBlockingBody) Read(p []byte) (int, error) {
	if b.offset < len(b.prefix) {
		n := copy(p, b.prefix[b.offset:])
		b.offset += n
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextBlockingBody) Close() error {
	return nil
}
