package litellm

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

type testProvider struct {
	name       string
	chatFunc   func(context.Context, *Request) (*Response, error)
	streamFunc func(context.Context, *Request) (Stream, error)
	lastReq    *Request
}

func (p *testProvider) Name() string { return p.name }

func (p *testProvider) Chat(ctx context.Context, req *Request) (*Response, error) {
	p.lastReq = req
	if p.chatFunc != nil {
		return p.chatFunc(ctx, req)
	}
	return &Response{Blocks: []Block{TextBlock{Text: "ok"}}}, nil
}

func (p *testProvider) Stream(ctx context.Context, req *Request) (Stream, error) {
	p.lastReq = req
	if p.streamFunc != nil {
		return p.streamFunc(ctx, req)
	}
	return &testStream{events: []Event{ContentDelta{Text: "ok"}, DoneEvent{FinishReason: FinishReasonStop, Provider: p.name, Model: req.Model}}}, nil
}

type testModelProvider struct {
	*testProvider
	listFunc func(context.Context) ([]ModelInfo, error)
}

func (p *testModelProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if p.listFunc != nil {
		return p.listFunc(ctx)
	}
	return []ModelInfo{{ID: "m", Provider: p.Name()}}, nil
}

type testStream struct {
	events []Event
	index  int
	closed bool
}

func (s *testStream) Next() (Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *testStream) Close() error {
	s.closed = true
	return nil
}

type blockingStream struct {
	ctx context.Context
}

func (s blockingStream) Next() (Event, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s blockingStream) Close() error {
	return nil
}

func TestClientDoesNotInjectDefaults(t *testing.T) {
	provider := &testProvider{name: "test"}
	client, err := New(provider)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if provider.lastReq.MaxTokens != nil || provider.lastReq.Temperature != nil || provider.lastReq.TopP != nil {
		t.Fatalf("unexpected defaults injected: %+v", provider.lastReq)
	}
}

func TestJSONRawReturnsMarshalError(t *testing.T) {
	_, err := JSONRaw(math.Inf(1))
	if err == nil {
		t.Fatalf("expected marshal error")
	}
}

func TestThinkingOptionsRequireExplicitMode(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking mode must be enabled or disabled") {
		t.Fatalf("expected thinking mode error, got %v", err)
	}
}

func TestThinkingDisabledRejectsOptions(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
		Thinking: &Thinking{Mode: ThinkingDisabled, Effort: "high"},
	})
	if err == nil || !strings.Contains(err.Error(), "thinking options cannot be set when thinking is disabled") {
		t.Fatalf("expected disabled thinking options error, got %v", err)
	}
}

func TestClientAppliesExplicitDefaults(t *testing.T) {
	maxTokens := 123
	temp := 0.4
	client, err := New(&testProvider{name: "test"}, WithDefaults(RequestDefaults{
		MaxTokens:   &maxTokens,
		Temperature: &temp,
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("response text = %q", resp.Text())
	}
}

func TestClientDeepClonesRequestForProvider(t *testing.T) {
	maxTokens := 10
	temp := 0.2
	topP := 0.9
	budget := 2048
	schema := Schema(`{"type":"object"}`)
	req := Request{
		Model:       "model",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
		Messages: []Message{
			User(TextBlock{
				Text: "hi",
				Annotations: []Annotation{
					{Type: "note", Extra: MustJSONRaw(map[string]any{"n": 1})},
				},
			}),
			Assistant(ToolUseBlock{
				ID:        "call_1",
				Name:      "tool",
				Arguments: MustJSONRaw(map[string]any{}),
				Cache:     &CacheControl{Type: CacheTypeEphemeral, TTL: CacheTTL1h},
			}),
			ToolResultText("call_1", "ok"),
		},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "tool",
			},
		},
		ResponseFormat: &ResponseFormat{
			Type: ResponseFormatJSONSchema,
			JSONSchema: &JSONSchema{
				Name:   "out",
				Schema: schema,
			},
		},
		Thinking: &Thinking{Mode: ThinkingEnabled, BudgetTokens: &budget},
		Cache:    &CachePolicy{Retention: CacheTTL1h, Placement: CachePlacementPrefix},
		ProviderOptions: ProviderOptions{
			"metadata": map[string]any{
				"tags":   []any{"a", "b"},
				"nested": map[string]any{"k": "v"},
			},
		},
	}
	provider := &testProvider{
		name: "test",
		chatFunc: func(ctx context.Context, cloned *Request) (*Response, error) {
			*cloned.MaxTokens = 99
			*cloned.Temperature = 1.5
			*cloned.TopP = 0.1
			cloned.ResponseFormat.JSONSchema.Schema[0] = '['
			*cloned.Thinking.BudgetTokens = 4096
			cloned.Cache.Retention = CacheTTL5m
			text := cloned.Messages[0].Blocks[0].(TextBlock)
			text.Annotations[0].Extra[0] = '['
			cloned.Messages[0].Blocks[0] = text
			tool := cloned.Messages[1].Blocks[0].(ToolUseBlock)
			tool.Arguments[0] = '['
			tool.Cache.TTL = CacheTTL5m
			cloned.Messages[1].Blocks[0] = tool
			choice := cloned.ToolChoice.(map[string]any)
			choice["type"] = "mutated"
			choice["function"].(map[string]any)["name"] = "mutated"
			metadata := cloned.ProviderOptions["metadata"].(map[string]any)
			metadata["tags"].([]any)[0] = "mutated"
			metadata["nested"].(map[string]any)["k"] = "mutated"
			return &Response{Blocks: []Block{Text("ok")}}, nil
		},
	}
	client, err := New(provider)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if *req.MaxTokens != 10 || *req.Temperature != 0.2 || *req.TopP != 0.9 {
		t.Fatalf("scalar pointers were mutated: %+v", req)
	}
	if string(req.ResponseFormat.JSONSchema.Schema) != `{"type":"object"}` {
		t.Fatalf("schema was mutated: %s", req.ResponseFormat.JSONSchema.Schema)
	}
	if *req.Thinking.BudgetTokens != 2048 || req.Cache.Retention != CacheTTL1h {
		t.Fatalf("thinking/cache mutated: %+v %+v", req.Thinking, req.Cache)
	}
	text := req.Messages[0].Blocks[0].(TextBlock)
	if string(text.Annotations[0].Extra) != `{"n":1}` {
		t.Fatalf("annotation extra mutated: %s", text.Annotations[0].Extra)
	}
	tool := req.Messages[1].Blocks[0].(ToolUseBlock)
	if string(tool.Arguments) != `{}` || tool.Cache.TTL != CacheTTL1h {
		t.Fatalf("tool block mutated: %#v", tool)
	}
	choice := req.ToolChoice.(map[string]any)
	if choice["type"] != "function" || choice["function"].(map[string]any)["name"] != "tool" {
		t.Fatalf("tool choice mutated: %#v", choice)
	}
	metadata := req.ProviderOptions["metadata"].(map[string]any)
	if metadata["tags"].([]any)[0] != "a" || metadata["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("provider options mutated: %#v", metadata)
	}
}

func TestValidateRejectsInvalidScalars(t *testing.T) {
	temp := 2.1
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:       "model",
		Messages:    []Message{UserText("hi")},
		Temperature: &temp,
	})
	if err == nil || !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestValidateRejectsInvalidCachePolicy(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
		Cache:    &CachePolicy{Retention: "forever", Placement: CachePlacementPrefix},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "cache retention") {
		t.Fatalf("expected cache retention validation error, got %v", err)
	}

	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
		Cache:    &CachePolicy{Retention: CacheTTL1h, Placement: CachePlacement("suffix")},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "cache placement") {
		t.Fatalf("expected cache placement validation error, got %v", err)
	}
}

func TestValidateRejectsInvalidBlockCacheControl(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model: "model",
		Messages: []Message{
			User(TextBlock{Text: "hi", Cache: &CacheControl{Type: "persistent"}}),
		},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "cache type") {
		t.Fatalf("expected cache type validation error, got %v", err)
	}

	_, err = client.Chat(context.Background(), Request{
		Model: "model",
		Messages: []Message{
			User(TextBlock{Text: "hi", Cache: &CacheControl{Type: CacheTypeEphemeral, TTL: "24h"}}),
		},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "cache ttl") {
		t.Fatalf("expected cache ttl validation error, got %v", err)
	}
}

func TestValidateRejectsInvalidUTF8Text(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model: "model",
		Messages: []Message{
			UserText(string([]byte{'h', 'i', 0xff})),
		},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("expected UTF-8 validation error, got %v", err)
	}
}

func TestValidateRejectsInvalidUTF8ProviderOptions(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "model",
		Messages: []Message{UserText("hi")},
		ProviderOptions: ProviderOptions{
			"metadata": map[string]any{
				"bad": string([]byte{0xff}),
			},
		},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("expected UTF-8 validation error, got %v", err)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	client, err := New(&testProvider{
		name: "test",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return blockingStream{ctx: ctx}, nil
		},
	}, WithStreamIdleTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil || !IsTimeoutError(err) || !IsStreamIdleError(err) {
		t.Fatalf("expected stream idle timeout, got %v", err)
	}
}

func TestStreamIdleTimeoutStopsAfterDoneEvent(t *testing.T) {
	client, err := New(&testProvider{
		name: "test",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &testStream{events: []Event{DoneEvent{FinishReason: FinishReasonStop}}}, nil
		},
	}, WithStreamIdleTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if _, ok := event.(DoneEvent); !ok {
		t.Fatalf("event = %#v, want DoneEvent", event)
	}
	time.Sleep(20 * time.Millisecond)
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after done, got %v", err)
	}
}

func TestClientHooks(t *testing.T) {
	var before, after int
	client, err := New(&testProvider{name: "hook"}, WithHook(HookFuncs{
		BeforeRequestFunc: func(ctx context.Context, meta CallMeta, req *Request) {
			before++
			if meta.Provider != "hook" || meta.Model != "m" {
				t.Fatalf("bad meta: %+v", meta)
			}
		},
		AfterResponseFunc: func(ctx context.Context, meta CallMeta, resp *Response, err error) {
			after++
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if resp == nil || resp.Text() != "ok" {
				t.Fatalf("bad hook response: %#v", resp)
			}
		},
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if before != 1 || after != 1 || resp.Text() != "ok" {
		t.Fatalf("hooks before=%d after=%d resp=%v", before, after, resp)
	}
}

func TestHooksCannotMutateProviderRequestOrReturnedResponse(t *testing.T) {
	provider := &testProvider{
		name: "hook",
		chatFunc: func(ctx context.Context, req *Request) (*Response, error) {
			if req.Model != "m" {
				t.Fatalf("provider saw mutated model %q", req.Model)
			}
			if got := req.Messages[0].Blocks[0].(TextBlock).Text; got != "hi" {
				t.Fatalf("provider saw mutated message %q", got)
			}
			return &Response{Blocks: []Block{Text("ok")}, Warnings: []Warning{{Code: "w"}}}, nil
		},
	}
	client, err := New(provider, WithHook(HookFuncs{
		BeforeRequestFunc: func(ctx context.Context, meta CallMeta, req *Request) {
			req.Model = "mutated"
			req.Messages[0].Blocks[0] = Text("mutated")
		},
		AfterResponseFunc: func(ctx context.Context, meta CallMeta, resp *Response, err error) {
			resp.Blocks[0] = Text("mutated")
			resp.Warnings[0].Code = "mutated"
		},
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Text() != "ok" {
		t.Fatalf("response text = %q, want ok", resp.Text())
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != "w" {
		t.Fatalf("warnings = %#v", resp.Warnings)
	}
}

func TestClientCaptureRawResponseOption(t *testing.T) {
	raw := []byte(`{"ok":true}`)
	client, err := New(&testProvider{
		name: "test",
		chatFunc: func(ctx context.Context, req *Request) (*Response, error) {
			resp := &Response{Blocks: []Block{Text("ok")}}
			CaptureRawResponse(req, resp, raw)
			return resp, nil
		},
	}, WithCaptureRawResponse(true))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if string(resp.Raw) != string(raw) {
		t.Fatalf("raw = %s, want %s", resp.Raw, raw)
	}
}

func TestClientDoesNotCaptureRawResponseByDefault(t *testing.T) {
	raw := []byte(`{"ok":true}`)
	client, err := New(&testProvider{
		name: "test",
		chatFunc: func(ctx context.Context, req *Request) (*Response, error) {
			resp := &Response{Blocks: []Block{Text("ok")}}
			CaptureRawResponse(req, resp, raw)
			return resp, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if len(resp.Raw) != 0 {
		t.Fatalf("raw should be empty by default: %s", resp.Raw)
	}
}

func TestClientRejectsNilResponseWithoutError(t *testing.T) {
	client, err := New(&testProvider{
		name: "test",
		chatFunc: func(context.Context, *Request) (*Response, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "nil response without error") {
		t.Fatalf("error = %v, want nil response contract", err)
	}
}

func TestClientAllowsMalformedToolArgumentsFromModel(t *testing.T) {
	client, err := New(&testProvider{
		name: "test",
		chatFunc: func(context.Context, *Request) (*Response, error) {
			return &Response{
				Provider:     "test",
				Model:        "m",
				FinishReason: FinishReasonToolCall,
				Blocks: []Block{
					ToolUseBlock{ID: "call_bad", Name: "lookup", Arguments: []byte(`{"q":`)},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
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
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != "tool_arguments_invalid" {
		t.Fatalf("warnings = %+v", resp.Warnings)
	}
}

func TestClientRejectsNilStreamWithoutError(t *testing.T) {
	client, err := New(&testProvider{
		name: "test",
		streamFunc: func(context.Context, *Request) (Stream, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "nil stream without error") {
		t.Fatalf("expected nil stream error, got %v", err)
	}
}

func TestClientWrapsProviderChatErrors(t *testing.T) {
	boom := errors.New("boom")
	client, err := New(&testProvider{
		name: "test",
		chatFunc: func(context.Context, *Request) (*Response, error) {
			return nil, boom
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err == nil || !IsProviderError(err) || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}
}

func TestClientWrapsProviderStreamStartErrors(t *testing.T) {
	boom := errors.New("boom")
	client, err := New(&testProvider{
		name: "test",
		streamFunc: func(context.Context, *Request) (Stream, error) {
			return nil, boom
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err == nil || !IsProviderError(err) || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped provider stream error, got %v", err)
	}
}

func TestWrapErrorClassifiesContextErrors(t *testing.T) {
	canceled := WrapError(context.Canceled, "test")
	if !IsNetworkError(canceled) || IsRetryableError(canceled) || !errors.Is(canceled, context.Canceled) {
		t.Fatalf("canceled error = %v", canceled)
	}
	deadline := WrapError(context.DeadlineExceeded, "test")
	if !IsTimeoutError(deadline) || IsRetryableError(deadline) || !errors.Is(deadline, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", deadline)
	}
}

func TestClientListModelsWrapsProviderErrors(t *testing.T) {
	boom := errors.New("boom")
	client, err := New(&testModelProvider{
		testProvider: &testProvider{name: "test"},
		listFunc: func(context.Context) ([]ModelInfo, error) {
			return nil, boom
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.ListModels(context.Background())
	if err == nil || !IsProviderError(err) || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped model list error, got %v", err)
	}
}

func TestCollect(t *testing.T) {
	stream := &testStream{events: []Event{
		ReasoningDelta{Text: "think"},
		ContentDelta{Text: "hel"},
		ContentDelta{Text: "lo"},
		ToolUseStart{ID: "call_1", Name: "tool"},
		ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"x":`)},
		ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`1}`)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}}
	resp, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hello" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Reasoning() != "think" {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	if resp.Provider != "test" || resp.Model != "m" {
		t.Fatalf("provider/model = %q/%q", resp.Provider, resp.Model)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || string(calls[0].Arguments) != `{"x":1}` {
		t.Fatalf("tool calls = %+v", calls)
	}
}

func TestValidateRejectsDirtyToolHistory(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(ToolUseBlock{ID: "call_1", Name: "tool", Arguments: MustJSONRaw(map[string]any{})}),
			UserText("next"),
		},
	})
	if err == nil || !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestValidateRejectsToolResultOutsideToolRole(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(
				ToolUseBlock{ID: "call_1", Name: "tool", Arguments: MustJSONRaw(map[string]any{})},
				ToolResultBlock{ToolUseID: "call_1", Content: []Block{Text("ok")}},
			),
		},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "tool result block requires tool role") {
		t.Fatalf("expected tool role validation error, got %v", err)
	}
}

func TestValidateRejectsTopLevelToolReference(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model:    "m",
		Messages: []Message{User(ToolReferenceBlock{ToolName: "lookup"})},
	})
	if err == nil || !IsValidationError(err) || !strings.Contains(err.Error(), "tool reference block is only valid inside tool result content") {
		t.Fatalf("expected tool reference validation error, got %v", err)
	}
}

func TestValidateAllowsToolReferenceInsideToolResult(t *testing.T) {
	client, err := New(&testProvider{name: "test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(ToolUseBlock{ID: "call_1", Name: "tool", Arguments: MustJSONRaw(map[string]any{})}),
			ToolResult("call_1", ToolReferenceBlock{ToolName: "lookup"}),
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
}

func TestMessageRepairIsExplicitAndWarns(t *testing.T) {
	provider := &testProvider{name: "test"}
	var hookWarnings []Warning
	client, err := New(provider,
		WithMessageRepair(RepairAll),
		WithHook(HookFuncs{
			OnWarningFunc: func(ctx context.Context, meta CallMeta, warning Warning) {
				hookWarnings = append(hookWarnings, warning)
			},
		}),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(ToolUseBlock{ID: "bad id!", Name: "tool", Arguments: MustJSONRaw(map[string]any{})}),
			UserText("next"),
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if len(resp.Warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2: %#v", len(resp.Warnings), resp.Warnings)
	}
	if len(hookWarnings) != 2 {
		t.Fatalf("hook warnings len = %d, want 2: %#v", len(hookWarnings), hookWarnings)
	}
	if got := provider.lastReq.Messages[0].Blocks[0].(ToolUseBlock).ID; got != "bad_id_" {
		t.Fatalf("tool use id = %q, want bad_id_", got)
	}
	if provider.lastReq.Messages[1].Role != RoleTool {
		t.Fatalf("messages[1].Role = %q, want tool", provider.lastReq.Messages[1].Role)
	}
	if provider.lastReq.Messages[2].Role != RoleUser {
		t.Fatalf("messages[2].Role = %q, want user", provider.lastReq.Messages[2].Role)
	}
	for _, warning := range resp.Warnings {
		if warning.Provider != "test" {
			t.Fatalf("warning provider = %q, want test", warning.Provider)
		}
	}
}

func TestMessageRepairSynthesizesMissingToolUseID(t *testing.T) {
	provider := &testProvider{name: "test"}
	client, err := New(provider, WithMessageRepair(RepairAll))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	resp, err := client.Chat(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(ToolUseBlock{Name: "tool", Arguments: MustJSONRaw(map[string]any{})}),
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if len(resp.Warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2: %#v", len(resp.Warnings), resp.Warnings)
	}
	generated := provider.lastReq.Messages[0].Blocks[0].(ToolUseBlock).ID
	if !strings.HasPrefix(generated, "call_") {
		t.Fatalf("generated id = %q, want call_ prefix", generated)
	}
	result := provider.lastReq.Messages[1].Blocks[0].(ToolResultBlock)
	if result.ToolUseID != generated || !result.IsError {
		t.Fatalf("synthetic result = %#v, generated=%q", result, generated)
	}
}

func TestMessageRepairInsertsMissingToolResultsInToolUseOrder(t *testing.T) {
	messages, warnings := repairMessages([]Message{
		Assistant(
			ToolUseBlock{ID: "call_1", Name: "first", Arguments: MustJSONRaw(map[string]any{})},
			ToolUseBlock{ID: "call_2", Name: "second", Arguments: MustJSONRaw(map[string]any{})},
		),
		UserText("next"),
	}, RepairInsertMissingToolResults)
	if len(warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2: %#v", len(warnings), warnings)
	}
	if len(messages) < 3 {
		t.Fatalf("messages = %#v", messages)
	}
	first, ok := messages[1].Blocks[0].(ToolResultBlock)
	if !ok || first.ToolUseID != "call_1" {
		t.Fatalf("first synthetic result = %#v", messages[1].Blocks[0])
	}
	second, ok := messages[2].Blocks[0].(ToolResultBlock)
	if !ok || second.ToolUseID != "call_2" {
		t.Fatalf("second synthetic result = %#v", messages[2].Blocks[0])
	}
}

func TestStreamEmitsRepairWarnings(t *testing.T) {
	client, err := New(&testProvider{name: "test"}, WithMessageRepair(RepairAll))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			Assistant(ToolUseBlock{ID: "bad id!", Name: "tool", Arguments: MustJSONRaw(map[string]any{})}),
			UserText("next"),
		},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	warning, ok := first.(WarningEvent)
	if !ok {
		t.Fatalf("first event = %#v, want WarningEvent", first)
	}
	if warning.Warning.Code != "message.tool_use_id_normalized" {
		t.Fatalf("warning code = %q", warning.Warning.Code)
	}
}

func TestStreamHookEndsOnError(t *testing.T) {
	boom := errors.New("boom")
	var endErr error
	client, err := New(&testProvider{
		name: "stream",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &testStreamWithError{events: []Event{ContentDelta{Text: "partial"}}, err: boom}, nil
		},
	}, WithHook(HookFuncs{
		OnStreamEndFunc: func(ctx context.Context, meta CallMeta, err error) {
			endErr = err
		},
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	_, err = Collect(stream)
	if !errors.Is(err, boom) {
		t.Fatalf("Collect err = %v, want %v", err, boom)
	}
	if !errors.Is(endErr, boom) {
		t.Fatalf("end err = %v, want %v", endErr, boom)
	}
}

func TestStreamWrapsRuntimeProviderErrors(t *testing.T) {
	boom := errors.New("boom")
	client, err := New(&testProvider{
		name: "stream",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &testStreamWithError{events: []Event{ContentDelta{Text: "partial"}}, err: boom}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	_, err = Collect(stream)
	if err == nil || !IsProviderError(err) || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped runtime stream error, got %v", err)
	}
}

func TestStreamHooksCannotMutateReturnedEvents(t *testing.T) {
	client, err := New(&testProvider{
		name: "stream",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &testStream{events: []Event{
				ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"q":"x"}`)},
				ProviderEvent{Name: "provider.event", Raw: []byte(`{"ok":true}`)},
				DoneEvent{FinishReason: FinishReasonStop, Provider: "stream", Model: req.Model},
			}}, nil
		},
	}, WithHook(HookFuncs{
		OnStreamEventFunc: func(ctx context.Context, meta CallMeta, event Event) {
			switch e := event.(type) {
			case ToolUseDelta:
				e.ArgumentsDelta[0] = '['
			case ProviderEvent:
				e.Raw[0] = '['
			}
		},
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	stream, err := client.Stream(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	toolEvent, err := stream.Next()
	if err != nil {
		t.Fatalf("Next tool event: %v", err)
	}
	toolDelta := toolEvent.(ToolUseDelta)
	if string(toolDelta.ArgumentsDelta) != `{"q":"x"}` {
		t.Fatalf("tool delta mutated: %s", toolDelta.ArgumentsDelta)
	}
	providerEvent, err := stream.Next()
	if err != nil {
		t.Fatalf("Next provider event: %v", err)
	}
	providerDelta := providerEvent.(ProviderEvent)
	if string(providerDelta.Raw) != `{"ok":true}` {
		t.Fatalf("provider event mutated: %s", providerDelta.Raw)
	}
}

func TestStreamTextReturnsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	client, err := New(&testProvider{
		name: "stream",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &closeErrStream{
				events: []Event{ContentDelta{Text: "ok"}, DoneEvent{FinishReason: FinishReasonStop, Provider: "stream", Model: req.Model}},
				err:    closeErr,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.StreamText(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}}, nil)
	if !errors.Is(err, closeErr) {
		t.Fatalf("StreamText err = %v, want close error", err)
	}
}

func TestStreamTextPreservesHandleErrorOverCloseError(t *testing.T) {
	boom := errors.New("boom")
	closeErr := errors.New("close failed")
	client, err := New(&testProvider{
		name: "stream",
		streamFunc: func(ctx context.Context, req *Request) (Stream, error) {
			return &closeErrStream{
				events: []Event{ContentDelta{Text: "partial"}},
				err:    closeErr,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = client.StreamText(context.Background(), Request{Model: "m", Messages: []Message{UserText("hi")}}, func(string) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("StreamText err = %v, want callback error", err)
	}
}

type testStreamWithError struct {
	events []Event
	index  int
	err    error
}

func (s *testStreamWithError) Next() (Event, error) {
	if s.index >= len(s.events) {
		return nil, s.err
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *testStreamWithError) Close() error { return nil }

type closeErrStream struct {
	events []Event
	index  int
	err    error
}

func (s *closeErrStream) Next() (Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *closeErrStream) Close() error { return s.err }
