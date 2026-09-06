package llmcodex

import (
	"bytes"
	"encoding/json"

	"github.com/voocel/agentcore"
)

type codexUsageContextKey struct{}

// UsageError preserves metering from a Generate that failed after one or more
// CLI subprocesses ran. Callers can errors.As to interface {
// LLMUsage() (*agentcore.Usage, string) } without depending on this concrete type.
// The source describes completeness: unknown has nil usage, while partial has
// only the known subtotal and must not be presented as the complete call cost.
type UsageError struct {
	cause             error
	usage             *agentcore.Usage
	source            string
	breakdown         []*agentcore.Usage
	breakdownComplete bool
}

func (e *UsageError) Error() string { return e.cause.Error() }
func (e *UsageError) Unwrap() error { return e.cause }

func (e *UsageError) LLMUsage() (*agentcore.Usage, string) {
	return cloneCodexUsage(e.usage), e.source
}

// LLMUsageBreakdown exposes per-CLI counts for pricing. Combining several
// short requests before pricing can incorrectly trigger long-context rates.
// A nil entry is an executed CLI whose usage is unknown; complete is false
// only if the bounded list was truncated, never a claim that nil usage is zero.
func (e *UsageError) LLMUsageBreakdown() ([]*agentcore.Usage, bool) {
	return cloneCodexUsageBreakdown(e.breakdown), e.breakdownComplete
}

const maxCodexUsageBreakdownCalls = 16

type codexUsageAccumulator struct {
	total                            agentcore.Usage
	reported, estimated, unavailable int
	breakdown                        []*agentcore.Usage
	breakdownTruncated               bool
}

func (a *codexUsageAccumulator) add(usage *agentcore.Usage, source string) {
	a.total.Add(usage)
	if len(a.breakdown) < maxCodexUsageBreakdownCalls {
		a.breakdown = append(a.breakdown, cloneCodexUsage(usage))
	} else {
		a.breakdownTruncated = true
	}
	switch source {
	case "reported":
		a.reported++
	case "estimated":
		a.estimated++
	default:
		a.unavailable++
	}
}

func (a *codexUsageAccumulator) source() string {
	source := "none"
	switch {
	case a.unavailable > 0 && a.reported == 0 && a.estimated == 0:
		source = "unknown"
	case a.unavailable > 0:
		source = "partial"
	case a.reported > 0 && a.estimated > 0:
		source = "mixed"
	case a.reported > 0:
		source = "reported"
	case a.estimated > 0:
		source = "estimated"
	}
	return source
}

func (a *codexUsageAccumulator) wrapError(cause error) error {
	if cause == nil || a.reported+a.estimated+a.unavailable == 0 {
		return cause
	}
	var usage *agentcore.Usage
	if a.reported+a.estimated > 0 {
		total := a.total
		usage = &total
	}
	return &UsageError{cause: cause, usage: usage, source: a.source(), breakdown: cloneCodexUsageBreakdown(a.breakdown), breakdownComplete: !a.breakdownTruncated}
}

func (a *codexUsageAccumulator) apply(message *agentcore.Message) {
	usage := a.total
	message.Usage = &usage
	if message.Metadata == nil {
		message.Metadata = make(map[string]any)
	}
	message.Metadata["codex_usage_source"] = a.source()
	message.Metadata["codex_exec_calls"] = a.reported + a.estimated + a.unavailable
	message.Metadata["codex_reported_usage_calls"] = a.reported
	message.Metadata["codex_estimated_usage_calls"] = a.estimated
	message.Metadata["codex_unavailable_usage_calls"] = a.unavailable
	message.Metadata["codex_usage_breakdown"] = cloneCodexUsageBreakdown(a.breakdown)
	message.Metadata["codex_usage_breakdown_complete"] = !a.breakdownTruncated
}

func cloneCodexUsage(usage *agentcore.Usage) *agentcore.Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	if copy.Cost != nil {
		cost := *copy.Cost
		copy.Cost = &cost
	}
	return &copy
}

func cloneCodexUsageBreakdown(calls []*agentcore.Usage) []*agentcore.Usage {
	if calls == nil {
		return nil
	}
	result := make([]*agentcore.Usage, len(calls))
	for i, usage := range calls {
		result[i] = cloneCodexUsage(usage)
	}
	return result
}

const maxCodexUsageEventBytes = 64 << 10

// codexUsageEventWriter consumes JSONL without retaining the event stream.
// Large reasoning/tool/message lines are discarded through their newline;
// unlike Scanner's token limit, this does not lose a later usage event.
type codexUsageEventWriter struct {
	line               []byte
	discarding         bool
	usage              agentcore.Usage
	completed, dropped int
	invalid            bool
}

func (w *codexUsageEventWriter) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		part := data
		if newline >= 0 {
			part = data[:newline]
		}
		if !w.discarding {
			if len(w.line)+len(part) > maxCodexUsageEventBytes {
				clear(w.line)
				w.line = w.line[:0]
				w.discarding = true
				w.dropped++
			} else {
				w.line = append(w.line, part...)
			}
		}
		if newline < 0 {
			break
		}
		w.finish()
		data = data[newline+1:]
	}
	return n, nil
}

func (w *codexUsageEventWriter) finish() {
	if !w.discarding && len(w.line) > 0 {
		w.consume(w.line)
	}
	clear(w.line)
	w.line = w.line[:0]
	w.discarding = false
}

func (w *codexUsageEventWriter) consume(line []byte) {
	var event struct {
		Type  string          `json:"type"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type != "turn.completed" {
		return
	}
	var u struct {
		Input     *int `json:"input_tokens"`
		Output    *int `json:"output_tokens"`
		CacheRead int  `json:"cached_input_tokens"`
	}
	if json.Unmarshal(event.Usage, &u) != nil || u.Input == nil || u.Output == nil || *u.Input < 0 || *u.Output < 0 || u.CacheRead < 0 || u.CacheRead > *u.Input {
		w.invalid = true
		return
	}
	maxInt := int(^uint(0) >> 1)
	if *u.Input > maxInt-*u.Output || w.usage.Input > maxInt-*u.Input || w.usage.Output > maxInt-*u.Output || w.usage.CacheRead > maxInt-u.CacheRead || w.usage.TotalTokens > maxInt-(*u.Input+*u.Output) {
		w.invalid = true
		return
	}
	w.usage.Add(&agentcore.Usage{Input: *u.Input, Output: *u.Output, CacheRead: u.CacheRead, TotalTokens: *u.Input + *u.Output})
	w.completed++
}

// Only a small stderr diagnostic tail is needed for CLI errors. Its size is
// independent of stream duration and it never receives JSON stdout events.
type codexDiagnosticTail struct{ data []byte }

const maxCodexDiagnosticBytes = 8 << 10

func (w *codexDiagnosticTail) Write(data []byte) (int, error) {
	n := len(data)
	if n >= maxCodexDiagnosticBytes {
		w.data = append(w.data[:0], data[n-maxCodexDiagnosticBytes:]...)
		return n, nil
	}
	if overflow := len(w.data) + n - maxCodexDiagnosticBytes; overflow > 0 {
		copy(w.data, w.data[overflow:])
		w.data = w.data[:len(w.data)-overflow]
	}
	w.data = append(w.data, data...)
	return n, nil
}

func (w *codexDiagnosticTail) String() string { return string(w.data) }
