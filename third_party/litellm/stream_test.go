package litellm

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCollectPreservesBlockOrder(t *testing.T) {
	stream := &eventSliceStream{events: []Event{
		ContentDelta{Text: "first "},
		ReasoningDelta{Text: "think", Signature: "sig"},
		ContentDelta{Text: "second "},
		ToolUseStart{ID: "call_1", Name: "lookup"},
		ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"q":"x"}`)},
		ContentDelta{Text: "third"},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test-provider", Model: "test-model"},
	}}

	resp, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resp.Blocks) != 5 {
		t.Fatalf("blocks len = %d, want 5: %#v", len(resp.Blocks), resp.Blocks)
	}
	if block, ok := resp.Blocks[0].(TextBlock); !ok || block.Text != "first " {
		t.Fatalf("blocks[0] = %#v", resp.Blocks[0])
	}
	if block, ok := resp.Blocks[1].(ReasoningBlock); !ok || block.Text != "think" || block.Signature != "sig" {
		t.Fatalf("blocks[1] = %#v", resp.Blocks[1])
	}
	if block, ok := resp.Blocks[2].(TextBlock); !ok || block.Text != "second " {
		t.Fatalf("blocks[2] = %#v", resp.Blocks[2])
	}
	if block, ok := resp.Blocks[3].(ToolUseBlock); !ok || block.ID != "call_1" || block.Name != "lookup" || string(block.Arguments) != `{"q":"x"}` {
		t.Fatalf("blocks[3] = %#v", resp.Blocks[3])
	}
	if block, ok := resp.Blocks[4].(TextBlock); !ok || block.Text != "third" {
		t.Fatalf("blocks[4] = %#v", resp.Blocks[4])
	}
	if resp.Provider != "test-provider" || resp.Model != "test-model" {
		t.Fatalf("provider/model = %q/%q", resp.Provider, resp.Model)
	}
}

func TestCollectMergesToolUseWhenStableIDArrivesAfterIndex(t *testing.T) {
	stream := &eventSliceStream{events: []Event{
		ToolUseStart{Name: "lookup", Index: IntPtr(0)},
		ToolUseDelta{Index: IntPtr(0), ArgumentsDelta: []byte(`{"q":`)},
		ToolUseDelta{ID: "call_1", Index: IntPtr(0), ArgumentsDelta: []byte(`"x"}`)},
		ToolUseDone{ID: "call_1", Index: IntPtr(0)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}}
	resp, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool call = %#v", calls[0])
	}
}

func TestCollectSeparatesToolUseByOutputAndIndex(t *testing.T) {
	stream := &eventSliceStream{events: []Event{
		ToolUseStart{ID: "call_a", Name: "first", Index: IntPtr(0), OutputIndex: IntPtr(0)},
		ToolUseDelta{ID: "call_a", Index: IntPtr(0), OutputIndex: IntPtr(0), ArgumentsDelta: []byte(`{"a":1}`)},
		ToolUseStart{ID: "call_b", Name: "second", Index: IntPtr(0), OutputIndex: IntPtr(1)},
		ToolUseDelta{ID: "call_b", Index: IntPtr(0), OutputIndex: IntPtr(1), ArgumentsDelta: []byte(`{"b":2}`)},
		ToolUseDone{ID: "call_a", Index: IntPtr(0), OutputIndex: IntPtr(0)},
		ToolUseDone{ID: "call_b", Index: IntPtr(0), OutputIndex: IntPtr(1)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}}
	resp, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("tool calls len = %d, want 2: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_a" || calls[0].Name != "first" || string(calls[0].Arguments) != `{"a":1}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call_b" || calls[1].Name != "second" || string(calls[1].Arguments) != `{"b":2}` {
		t.Fatalf("second call = %#v", calls[1])
	}
}

func TestCollectRejectsNilEventWithoutError(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{nil}})
	if err == nil || err.Error() != "stream returned nil event without error" {
		t.Fatalf("expected nil event error, got %v", err)
	}
}

func TestCollectRequiresDoneOrError(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{ContentDelta{Text: "partial"}}})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF when stream ends without Done or error, got %v", err)
	}
}

func TestCollectNormalizesInvalidToolArguments(t *testing.T) {
	resp, err := Collect(&eventSliceStream{events: []Event{
		ToolUseStart{ID: "call_1", Name: "lookup"},
		ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"q":`)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}})
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(calls))
	}
	if got := string(calls[0].Arguments); got != `{"q":` {
		t.Fatalf("arguments = %q, want raw malformed args", got)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != "stream.tool_arguments_invalid" {
		t.Fatalf("warnings = %+v", resp.Warnings)
	}
}

func TestCollectRejectsMissingProviderOrModel(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{
		ContentDelta{Text: "ok"},
		DoneEvent{FinishReason: FinishReasonStop, Model: "m"},
	}})
	if err == nil || !strings.Contains(err.Error(), "missing provider") {
		t.Fatalf("expected missing provider error, got %v", err)
	}

	_, err = Collect(&eventSliceStream{events: []Event{
		ContentDelta{Text: "ok"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test"},
	}})
	if err == nil || !strings.Contains(err.Error(), "missing model") {
		t.Fatalf("expected missing model error, got %v", err)
	}
}

func TestCollectRejectsToolCallMissingID(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{
		ToolUseStart{Name: "lookup", Index: IntPtr(0)},
		ToolUseDelta{Index: IntPtr(0), ArgumentsDelta: []byte(`{"q":"x"}`)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}})
	if err == nil || err.Error() == "" {
		t.Fatalf("expected missing tool id error, got %v", err)
	}
}

func TestCollectRejectsToolCallMissingName(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{
		ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"q":"x"}`)},
		DoneEvent{FinishReason: FinishReasonToolCall, Provider: "test", Model: "m"},
	}})
	if err == nil || err.Error() == "" {
		t.Fatalf("expected missing tool name error, got %v", err)
	}
}

func TestCollectRejectsToolDoneMissingIDAndIndex(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{
		ToolUseDone{},
	}})
	if err == nil || !strings.Contains(err.Error(), "missing id and index") {
		t.Fatalf("expected missing tool done id/index error, got %v", err)
	}
}

func TestCollectRejectsToolDoneUnknownToolUse(t *testing.T) {
	_, err := Collect(&eventSliceStream{events: []Event{
		ToolUseDone{ID: "call_missing"},
	}})
	if err == nil || !strings.Contains(err.Error(), "references unknown tool use") {
		t.Fatalf("expected unknown tool done error, got %v", err)
	}
	if strings.Contains(err.Error(), "missing id and index") {
		t.Fatalf("unknown tool use must not be reported as missing id/index: %v", err)
	}
}

func TestHandleInvokesCallbackPerEventAndAggregates(t *testing.T) {
	stream := &eventSliceStream{events: []Event{
		ContentDelta{Text: "a"},
		ReasoningDelta{Text: "r"},
		ContentDelta{Text: "b"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test", Model: "m"},
	}}
	var seen int
	resp, err := Handle(stream, func(event Event) error {
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if seen != 4 {
		t.Fatalf("callback invoked %d times, want 4", seen)
	}
	if resp.Text() != "ab" {
		t.Fatalf("aggregated text = %q, want %q", resp.Text(), "ab")
	}
}

func TestCollectStampsUsageProviderAndModelFromDoneEvent(t *testing.T) {
	resp, err := Collect(&eventSliceStream{events: []Event{
		ContentDelta{Text: "ok"},
		UsageEvent{Usage: Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test-provider", Model: "test-model"},
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.Usage.Provider != "test-provider" || resp.Usage.Model != "test-model" {
		t.Fatalf("usage provider/model = %q/%q", resp.Usage.Provider, resp.Usage.Model)
	}
}

func TestHandleStopsOnCallbackError(t *testing.T) {
	boom := errors.New("boom")
	var seen int
	_, err := Handle(&eventSliceStream{events: []Event{
		ContentDelta{Text: "a"},
		ContentDelta{Text: "b"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test", Model: "m"},
	}}, func(event Event) error {
		seen++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if seen != 1 {
		t.Fatalf("callback invoked %d times, want 1 (stop on first error)", seen)
	}
}

func TestHandleTextReceivesOnlyContentDeltas(t *testing.T) {
	var text strings.Builder
	resp, err := HandleText(&eventSliceStream{events: []Event{
		ReasoningDelta{Text: "ignored"},
		ContentDelta{Text: "hello "},
		ContentDelta{Text: "world"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test", Model: "m"},
	}}, func(s string) error {
		text.WriteString(s)
		return nil
	})
	if err != nil {
		t.Fatalf("HandleText: %v", err)
	}
	if text.String() != "hello world" {
		t.Fatalf("streamed text = %q, want %q", text.String(), "hello world")
	}
	if resp.Reasoning() != "ignored" {
		t.Fatalf("reasoning still aggregated, got %q", resp.Reasoning())
	}
}

func TestHandleWithSplitsReasoningAndContent(t *testing.T) {
	var reasoning, content strings.Builder
	resp, err := HandleWith(&eventSliceStream{events: []Event{
		ReasoningDelta{Text: "think "},
		ContentDelta{Text: "ans"},
		ReasoningDelta{Text: "more"},
		ContentDelta{Text: "wer"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test", Model: "m"},
	}}, StreamHandler{
		Reasoning: func(s string) error { reasoning.WriteString(s); return nil },
		Content:   func(s string) error { content.WriteString(s); return nil },
	})
	if err != nil {
		t.Fatalf("HandleWith: %v", err)
	}
	if reasoning.String() != "think more" {
		t.Fatalf("reasoning = %q, want %q", reasoning.String(), "think more")
	}
	if content.String() != "answer" {
		t.Fatalf("content = %q, want %q", content.String(), "answer")
	}
	if resp.Text() != "answer" {
		t.Fatalf("aggregated text = %q", resp.Text())
	}
}

func TestHandleWithNilCallbacksAreSkipped(t *testing.T) {
	// Only Content is set; reasoning deltas must not panic and are still aggregated.
	var content strings.Builder
	resp, err := HandleWith(&eventSliceStream{events: []Event{
		ReasoningDelta{Text: "r"},
		ContentDelta{Text: "c"},
		DoneEvent{FinishReason: FinishReasonStop, Provider: "test", Model: "m"},
	}}, StreamHandler{
		Content: func(s string) error { content.WriteString(s); return nil },
	})
	if err != nil {
		t.Fatalf("HandleWith: %v", err)
	}
	if content.String() != "c" || resp.Reasoning() != "r" {
		t.Fatalf("content=%q reasoning=%q", content.String(), resp.Reasoning())
	}
}

type eventSliceStream struct {
	events []Event
	index  int
}

func (s *eventSliceStream) Next() (Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *eventSliceStream) Close() error {
	return nil
}
