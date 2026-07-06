package litellm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Event interface {
	isEvent()
}

type ContentDelta struct {
	Text         string
	OutputIndex  *int
	ContentIndex *int
}

type RefusalDelta struct {
	Text         string
	OutputIndex  *int
	ContentIndex *int
}

type ReasoningDelta struct {
	Text      string
	Summary   bool
	Signature string
	Redacted  []byte
	Extra     json.RawMessage
	ExtraFull bool
	Index     *int
}

type ToolUseStart struct {
	ID          string
	Name        string
	Index       *int
	OutputIndex *int
	ItemID      string
	Signature   string
}

type ToolUseDelta struct {
	ID             string
	Index          *int
	OutputIndex    *int
	ItemID         string
	ArgumentsDelta []byte
	Signature      string
}

type ToolUseDone struct {
	ID          string
	Index       *int
	OutputIndex *int
	ItemID      string
}

type UsageEvent struct {
	Usage Usage
}

type WarningEvent struct {
	Warning Warning
}

type DoneEvent struct {
	FinishReason FinishReason
	Provider     string
	Model        string
}

type ErrorEvent struct {
	Err error
}

type ProviderEvent struct {
	Name string
	Raw  json.RawMessage
}

func (ContentDelta) isEvent()   {}
func (RefusalDelta) isEvent()   {}
func (ReasoningDelta) isEvent() {}
func (ToolUseStart) isEvent()   {}
func (ToolUseDelta) isEvent()   {}
func (ToolUseDone) isEvent()    {}
func (UsageEvent) isEvent()     {}
func (WarningEvent) isEvent()   {}
func (DoneEvent) isEvent()      {}
func (ErrorEvent) isEvent()     {}
func (ProviderEvent) isEvent()  {}

func cloneEvent(event Event) Event {
	switch e := event.(type) {
	case ContentDelta:
		return e
	case RefusalDelta:
		return e
	case ReasoningDelta:
		e.Redacted = cloneBytes(e.Redacted)
		e.Extra = cloneBytes(e.Extra)
		return e
	case ToolUseStart:
		return e
	case ToolUseDelta:
		e.ArgumentsDelta = cloneBytes(e.ArgumentsDelta)
		return e
	case ToolUseDone:
		return e
	case UsageEvent:
		return e
	case WarningEvent:
		return e
	case DoneEvent:
		return e
	case ErrorEvent:
		return e
	case ProviderEvent:
		e.Raw = cloneBytes(e.Raw)
		return e
	default:
		return event
	}
}

type Stream interface {
	Next() (Event, error)
	Close() error
}

type providerErrorStream struct {
	provider string
	inner    Stream
}

func wrapProviderStreamErrors(provider string, stream Stream) Stream {
	if stream == nil {
		return nil
	}
	return &providerErrorStream{provider: provider, inner: stream}
}

func (s *providerErrorStream) Next() (Event, error) {
	event, err := s.inner.Next()
	if err != nil && !errors.Is(err, io.EOF) {
		return event, WrapError(err, s.provider)
	}
	return event, err
}

func (s *providerErrorStream) Close() error {
	err := s.inner.Close()
	if err != nil {
		return WrapError(err, s.provider)
	}
	return nil
}

type warningPrefixStream struct {
	warnings []Warning
	index    int
	inner    Stream
}

func prependWarningEvents(stream Stream, warnings []Warning) Stream {
	if len(warnings) == 0 || stream == nil {
		return stream
	}
	copied := append([]Warning(nil), warnings...)
	return &warningPrefixStream{warnings: copied, inner: stream}
}

func (s *warningPrefixStream) Next() (Event, error) {
	if s.index < len(s.warnings) {
		warning := s.warnings[s.index]
		s.index++
		return WarningEvent{Warning: warning}, nil
	}
	return s.inner.Next()
}

func (s *warningPrefixStream) Close() error {
	return s.inner.Close()
}

// Collect consumes the stream and returns the aggregated Response. It errors if
// the stream ends before a DoneEvent.
func Collect(stream Stream) (*Response, error) {
	return Handle(stream, nil)
}

// Handle consumes the stream, invoking fn for each event as it arrives, and
// returns the aggregated Response. It is the real-time counterpart to Collect;
// a nil fn behaves exactly like Collect. If fn returns an error, Handle stops
// and returns it. The caller still owns Close.
func Handle(stream Stream, fn func(Event) error) (*Response, error) {
	if stream == nil {
		return nil, fmt.Errorf("stream cannot be nil")
	}
	collector := newEventCollector()
	for {
		event, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("stream ended before Done event: %w", err)
			}
			return nil, err
		}
		if event == nil {
			return nil, fmt.Errorf("stream returned nil event without error")
		}
		if fn != nil {
			if err := fn(event); err != nil {
				return nil, err
			}
		}
		done, err := collector.Apply(event)
		if err != nil {
			return nil, err
		}
		if done {
			resp := collector.Response()
			if err := validateResponse(resp, resp.Provider, resp.Model); err != nil {
				return nil, err
			}
			return resp, nil
		}
	}
}

// HandleText consumes the stream, invoking fn for each text content delta, and
// returns the aggregated Response. It is the simplest path for streaming answer
// text; reasoning and tool events are still aggregated into the Response but are
// not passed to fn.
func HandleText(stream Stream, fn func(string) error) (*Response, error) {
	return Handle(stream, func(event Event) error {
		if delta, ok := event.(ContentDelta); ok && delta.Text != "" && fn != nil {
			return fn(delta.Text)
		}
		return nil
	})
}

// StreamHandler routes streamed deltas to per-category callbacks. Unset
// callbacks are skipped; every event is still aggregated into the returned
// Response. For full event fidelity (tool-call streaming, provider events), use
// Handle or the raw Stream.
type StreamHandler struct {
	Content   func(string) error
	Reasoning func(string) error
}

// HandleWith consumes the stream, dispatching content and reasoning deltas to
// the handler's callbacks, and returns the aggregated Response.
func HandleWith(stream Stream, handler StreamHandler) (*Response, error) {
	return Handle(stream, func(event Event) error {
		switch e := event.(type) {
		case ContentDelta:
			if handler.Content != nil && e.Text != "" {
				return handler.Content(e.Text)
			}
		case ReasoningDelta:
			if handler.Reasoning != nil && e.Text != "" {
				return handler.Reasoning(e.Text)
			}
		}
		return nil
	})
}

type EventCollector struct {
	blocks      []Block
	toolIndexes map[string]int
	usage       Usage
	finish      FinishReason
	provider    string
	model       string
	warnings    []Warning
	tools       *ToolUseAccumulator
}

func newEventCollector() *EventCollector {
	return &EventCollector{
		toolIndexes: make(map[string]int),
		tools:       NewToolUseAccumulator(),
	}
}

func (c *EventCollector) Apply(event Event) (bool, error) {
	switch e := event.(type) {
	case ContentDelta:
		c.appendContent(e.Text)
	case RefusalDelta:
		c.appendContent(e.Text)
	case ReasoningDelta:
		c.appendReasoning(e)
	case ToolUseStart:
		key, tool, err := c.tools.Start(e)
		if err != nil {
			return false, err
		}
		c.appendTool(key, tool)
	case ToolUseDelta:
		key, tool, err := c.tools.Delta(e)
		if err != nil {
			return false, err
		}
		c.appendTool(key, tool)
	case ToolUseDone:
		key, tool, err := c.tools.Done(e)
		if err != nil {
			return false, err
		}
		if warning := normalizeInvalidToolArguments(tool); warning != nil {
			c.appendWarning(*warning)
		}
		c.appendTool(key, tool)
	case UsageEvent:
		c.usage = e.Usage
		if e.Usage.Provider != "" {
			c.provider = e.Usage.Provider
		}
		if e.Usage.Model != "" {
			c.model = e.Usage.Model
		}
	case WarningEvent:
		c.appendWarning(e.Warning)
	case ErrorEvent:
		if e.Err == nil {
			return false, fmt.Errorf("stream error event missing error")
		}
		return false, e.Err
	case ProviderEvent:
		// Provider-native events are observable by stream consumers. The core
		// collector ignores them unless they are promoted to typed events.
	case DoneEvent:
		c.finish = e.FinishReason
		if e.Provider != "" {
			c.provider = e.Provider
		}
		if e.Model != "" {
			c.model = e.Model
		}
		c.normalizeToolArguments()
		return true, nil
	default:
		return false, fmt.Errorf("unknown stream event %T", event)
	}
	return false, nil
}

func (c *EventCollector) appendWarning(w Warning) {
	if w.Provider == "" {
		w.Provider = c.provider
	}
	c.warnings = append(c.warnings, w)
}

func (c *EventCollector) normalizeToolArguments() {
	for i, block := range c.blocks {
		tool, ok := block.(ToolUseBlock)
		if !ok {
			continue
		}
		if warning := normalizeInvalidToolArguments(&tool); warning != nil {
			c.blocks[i] = tool
			c.appendWarning(*warning)
		}
	}
}

func normalizeInvalidToolArguments(tool *ToolUseBlock) *Warning {
	if tool == nil || len(tool.Arguments) == 0 || json.Valid(tool.Arguments) {
		return nil
	}
	var probe any
	parseErr := json.Unmarshal(tool.Arguments, &probe)
	return &Warning{
		Code:     "stream.tool_arguments_invalid",
		Message:  fmt.Sprintf("tool use %q returned malformed JSON arguments: %v", tool.ID, parseErr),
		Provider: "",
		// Keep raw arguments out of Warning to avoid leaking large or sensitive
		// payloads through observability hooks. Consumers that need raw deltas
		// can observe ToolUseDelta events directly.
	}
}

func (c *EventCollector) Response() *Response {
	resp := &Response{
		Blocks:       c.cloneBlocks(),
		Usage:        c.usage,
		Model:        c.model,
		Provider:     c.provider,
		FinishReason: c.finish,
		Warnings:     append([]Warning(nil), c.warnings...),
	}
	resp.Usage.StampModel(resp.Provider, resp.Model)
	return resp
}

func (c *EventCollector) appendContent(text string) {
	if text == "" {
		return
	}
	if len(c.blocks) > 0 {
		if block, ok := c.blocks[len(c.blocks)-1].(TextBlock); ok {
			block.Text += text
			c.blocks[len(c.blocks)-1] = block
			return
		}
	}
	c.blocks = append(c.blocks, TextBlock{Text: text})
}

func (c *EventCollector) appendReasoning(delta ReasoningDelta) {
	if delta.Text == "" && delta.Signature == "" && len(delta.Redacted) == 0 && len(delta.Extra) == 0 {
		return
	}
	if len(delta.Redacted) > 0 {
		c.blocks = append(c.blocks, ReasoningBlock{
			Signature: delta.Signature,
			Redacted:  cloneBytes(delta.Redacted),
		})
		return
	}
	if len(c.blocks) > 0 {
		if block, ok := c.blocks[len(c.blocks)-1].(ReasoningBlock); ok && len(block.Redacted) == 0 && block.Summary == delta.Summary {
			block.Text += delta.Text
			if delta.Signature != "" {
				block.Signature = delta.Signature
			}
			block.Extra = mergeReasoningExtra(block.Extra, delta)
			c.blocks[len(c.blocks)-1] = block
			return
		}
	}
	c.blocks = append(c.blocks, ReasoningBlock{Text: delta.Text, Summary: delta.Summary, Signature: delta.Signature, Extra: cloneBytes(delta.Extra)})
}

func mergeReasoningExtra(current json.RawMessage, delta ReasoningDelta) json.RawMessage {
	if len(delta.Extra) == 0 {
		return current
	}
	if delta.ExtraFull {
		return cloneBytes(delta.Extra)
	}
	if len(current) == 0 {
		return cloneBytes(delta.Extra)
	}
	var currentItems, deltaItems []json.RawMessage
	if json.Unmarshal(current, &currentItems) == nil && json.Unmarshal(delta.Extra, &deltaItems) == nil {
		merged := make([]json.RawMessage, 0, len(currentItems)+len(deltaItems))
		merged = append(merged, currentItems...)
		merged = append(merged, deltaItems...)
		if data, err := json.Marshal(merged); err == nil {
			return data
		}
	}
	return cloneBytes(delta.Extra)
}

func (c *EventCollector) appendTool(key string, tool *ToolUseBlock) {
	if key == "" || tool == nil {
		return
	}
	if index, ok := c.toolIndexes[key]; ok {
		c.blocks[index] = cloneToolUseBlock(*tool)
		return
	}
	c.toolIndexes[key] = len(c.blocks)
	c.blocks = append(c.blocks, cloneToolUseBlock(*tool))
}

func (c *EventCollector) cloneBlocks() []Block {
	if len(c.blocks) == 0 {
		return nil
	}
	out := make([]Block, len(c.blocks))
	for i, block := range c.blocks {
		switch b := block.(type) {
		case TextBlock:
			out[i] = b
		case ReasoningBlock:
			out[i] = b
		case ToolUseBlock:
			out[i] = cloneToolUseBlock(b)
		default:
			out[i] = block
		}
	}
	return out
}

func cloneToolUseBlock(block ToolUseBlock) ToolUseBlock {
	block.Arguments = json.RawMessage(cloneBytes(block.Arguments))
	block.Extra = json.RawMessage(cloneBytes(block.Extra))
	return block
}

type ToolUseAccumulator struct {
	order   []string
	byKey   map[string]*ToolUseBlock
	aliases map[string]string
}

func NewToolUseAccumulator() *ToolUseAccumulator {
	return &ToolUseAccumulator{
		byKey:   make(map[string]*ToolUseBlock),
		aliases: make(map[string]string),
	}
}

func (a *ToolUseAccumulator) Start(start ToolUseStart) (string, *ToolUseBlock, error) {
	key, tool, err := a.ensureFor(start.ID, start.Index, start.OutputIndex, start.ItemID)
	if err != nil {
		return "", nil, fmt.Errorf("tool use start: %w", err)
	}
	if start.ID != "" {
		tool.ID = start.ID
	}
	if start.Name != "" {
		tool.Name = start.Name
	}
	if start.Signature != "" {
		tool.Signature = start.Signature
	}
	return key, tool, nil
}

func (a *ToolUseAccumulator) Delta(delta ToolUseDelta) (string, *ToolUseBlock, error) {
	key, tool, err := a.ensureFor(delta.ID, delta.Index, delta.OutputIndex, delta.ItemID)
	if err != nil {
		return "", nil, fmt.Errorf("tool use delta: %w", err)
	}
	if delta.ID != "" {
		tool.ID = delta.ID
	}
	if delta.Signature != "" {
		tool.Signature = delta.Signature
	}
	if len(delta.ArgumentsDelta) > 0 {
		tool.Arguments = append(tool.Arguments, delta.ArgumentsDelta...)
	}
	return key, tool, nil
}

func (a *ToolUseAccumulator) Done(done ToolUseDone) (string, *ToolUseBlock, error) {
	key, tool, err := a.findFor(done.ID, done.Index, done.OutputIndex, done.ItemID)
	if err != nil {
		return "", nil, fmt.Errorf("tool use done: %w", err)
	}
	if done.ID != "" {
		tool.ID = done.ID
	}
	// A tool call with no streamed argument deltas (an argument-less call)
	// normalizes to an empty JSON object, keeping the block valid JSON for
	// response validation and replay rather than dangling as empty bytes.
	if len(tool.Arguments) == 0 {
		tool.Arguments = json.RawMessage("{}")
	}
	return key, tool, nil
}

func (a *ToolUseAccumulator) ensureFor(id string, index, outputIndex *int, itemID string) (string, *ToolUseBlock, error) {
	keys := toolUseKeys(id, index, outputIndex, itemID)
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("tool use missing id and index")
	}
	primary := keys[0]
	if key, tool := a.lookup(keys); tool != nil {
		a.aliasAll(key, keys)
		return key, tool, nil
	}
	tool := &ToolUseBlock{}
	a.byKey[primary] = tool
	a.order = append(a.order, primary)
	a.aliasAll(primary, keys)
	return primary, tool, nil
}

func (a *ToolUseAccumulator) findFor(id string, index, outputIndex *int, itemID string) (string, *ToolUseBlock, error) {
	keys := toolUseKeys(id, index, outputIndex, itemID)
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("tool use missing id and index")
	}
	if key, tool := a.lookup(keys); tool != nil {
		a.aliasAll(key, keys)
		return key, tool, nil
	}
	return "", nil, fmt.Errorf("tool use done references unknown tool use")
}

func (a *ToolUseAccumulator) lookup(keys []string) (string, *ToolUseBlock) {
	for _, key := range keys {
		if canonical := a.aliases[key]; canonical != "" {
			if tool := a.byKey[canonical]; tool != nil {
				return canonical, tool
			}
		}
		if tool := a.byKey[key]; tool != nil {
			return key, tool
		}
	}
	return "", nil
}

func (a *ToolUseAccumulator) aliasAll(canonical string, keys []string) {
	for _, key := range keys {
		a.aliases[key] = canonical
	}
}

func toolUseKeys(id string, index, outputIndex *int, itemID string) []string {
	keys := make([]string, 0, 4)
	if id != "" {
		keys = append(keys, "id:"+id)
	}
	if itemID != "" {
		keys = append(keys, "item:"+itemID)
	}
	if index != nil && outputIndex != nil {
		keys = append(keys, fmt.Sprintf("output:%d/index:%d", *outputIndex, *index))
		return keys
	}
	if index != nil {
		keys = append(keys, fmt.Sprintf("index:%d", *index))
	}
	if outputIndex != nil {
		keys = append(keys, fmt.Sprintf("output:%d", *outputIndex))
	}
	return keys
}
