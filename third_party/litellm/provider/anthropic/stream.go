package anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/voocel/litellm"
)

type stream struct {
	resp             *http.Response
	scanner          *bufio.Scanner
	includeReasoning bool
	pending          []litellm.Event
	done             bool
	model            string
	usage            litellm.Usage
	finish           litellm.FinishReason
	toolIDs          map[int]string
	toolNames        map[int]string
}

type streamChunk struct {
	Type         string          `json:"type"`
	Index        int             `json:"index,omitempty"`
	Delta        *streamDelta    `json:"delta,omitempty"`
	Usage        *anthropicUsage `json:"usage,omitempty"`
	Message      *streamMessage  `json:"message,omitempty"`
	Error        *streamError    `json:"error,omitempty"`
	ContentBlock *struct {
		Type      string         `json:"type"`
		ID        string         `json:"id,omitempty"`
		Name      string         `json:"name,omitempty"`
		Input     map[string]any `json:"input,omitempty"`
		Thinking  string         `json:"thinking,omitempty"`
		Signature string         `json:"signature,omitempty"`
		Data      string         `json:"data,omitempty"`
	} `json:"content_block,omitempty"`
}

type streamMessage struct {
	ID    string          `json:"id,omitempty"`
	Model string          `json:"model,omitempty"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type streamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

func newStream(resp *http.Response, req *litellm.Request) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &stream{
		resp:             resp,
		scanner:          scanner,
		includeReasoning: req == nil || req.Thinking == nil || req.Thinking.Mode != litellm.ThinkingDisabled,
		model:            req.Model,
		toolIDs:          make(map[int]string),
		toolNames:        make(map[int]string),
	}
}

func (s *stream) Next() (litellm.Event, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	if s.done {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" || strings.HasPrefix(line, "event: ") || line[0] == ':' {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if trimmed, found := strings.CutPrefix(line, "data:"); found {
				data = strings.TrimSpace(trimmed)
				ok = true
			}
		}
		if !ok || data == "" {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, litellm.NewProviderErrorWithCause("anthropic", litellm.ErrorTypeProvider, "anthropic: parse stream chunk", err)
		}
		events, err := s.events(chunk, json.RawMessage(data))
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, litellm.NewNetworkError("anthropic", "stream read error", err)
	}
	s.done = true
	return nil, litellm.NewProviderError("anthropic", litellm.ErrorTypeProvider, "anthropic: stream ended before message_stop")
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) events(chunk streamChunk, raw json.RawMessage) ([]litellm.Event, error) {
	switch chunk.Type {
	case "message_start":
		if chunk.Message != nil {
			if chunk.Message.Model != "" {
				s.model = chunk.Message.Model
			}
			if chunk.Message.Usage != nil {
				s.usage = convertStreamUsage(chunk.Message.Usage, s.model)
				return []litellm.Event{litellm.UsageEvent{Usage: s.usage}}, nil
			}
		}
	case "message_delta":
		if chunk.Usage != nil {
			s.mergeUsage(chunk.Usage)
		}
		if chunk.Delta != nil && chunk.Delta.StopReason != "" {
			s.finish = litellm.NormalizeFinishReason(chunk.Delta.StopReason)
		}
		if s.usage.HasTokens() {
			return []litellm.Event{litellm.UsageEvent{Usage: s.usage}}, nil
		}
	case "message_stop":
		s.done = true
		return []litellm.Event{litellm.DoneEvent{FinishReason: s.finish, Provider: "anthropic", Model: s.model}}, nil
	case "content_block_start":
		if chunk.ContentBlock == nil {
			return nil, nil
		}
		switch chunk.ContentBlock.Type {
		case "tool_use":
			s.toolIDs[chunk.Index] = chunk.ContentBlock.ID
			s.toolNames[chunk.Index] = chunk.ContentBlock.Name
			return []litellm.Event{litellm.ToolUseStart{
				ID:        chunk.ContentBlock.ID,
				Name:      chunk.ContentBlock.Name,
				Index:     litellm.IntPtr(chunk.Index),
				Signature: chunk.ContentBlock.Signature,
			}}, nil
		case "thinking":
			if !s.includeReasoning {
				return nil, nil
			}
			if chunk.ContentBlock.Thinking == "" && chunk.ContentBlock.Signature == "" {
				return nil, nil
			}
			return []litellm.Event{litellm.ReasoningDelta{
				Text:      chunk.ContentBlock.Thinking,
				Signature: chunk.ContentBlock.Signature,
				Index:     litellm.IntPtr(chunk.Index),
			}}, nil
		case "redacted_thinking":
			if !s.includeReasoning {
				return nil, nil
			}
			if chunk.ContentBlock.Data == "" {
				return nil, nil
			}
			return []litellm.Event{litellm.ReasoningDelta{
				Redacted: []byte(chunk.ContentBlock.Data),
				Index:    litellm.IntPtr(chunk.Index),
			}}, nil
		case "text":
			return nil, nil
		default:
			return []litellm.Event{litellm.ProviderEvent{Name: chunk.Type + "." + chunk.ContentBlock.Type, Raw: raw}}, nil
		}
	case "content_block_delta":
		if chunk.Delta == nil {
			return nil, nil
		}
		switch chunk.Delta.Type {
		case "text_delta":
			return []litellm.Event{litellm.ContentDelta{Text: chunk.Delta.Text, ContentIndex: litellm.IntPtr(chunk.Index)}}, nil
		case "thinking_delta":
			if !s.includeReasoning {
				return nil, nil
			}
			return []litellm.Event{litellm.ReasoningDelta{Text: chunk.Delta.Thinking, Index: litellm.IntPtr(chunk.Index)}}, nil
		case "signature_delta":
			if !s.includeReasoning {
				return nil, nil
			}
			return []litellm.Event{litellm.ReasoningDelta{Signature: chunk.Delta.Signature, Index: litellm.IntPtr(chunk.Index)}}, nil
		case "input_json_delta":
			return []litellm.Event{litellm.ToolUseDelta{
				ID:             s.toolIDs[chunk.Index],
				Index:          litellm.IntPtr(chunk.Index),
				ArgumentsDelta: []byte(chunk.Delta.PartialJSON),
			}}, nil
		default:
			return []litellm.Event{litellm.ProviderEvent{Name: chunk.Type + "." + chunk.Delta.Type, Raw: raw}}, nil
		}
	case "content_block_stop":
		if id := s.toolIDs[chunk.Index]; id != "" {
			return []litellm.Event{litellm.ToolUseDone{ID: id, Index: litellm.IntPtr(chunk.Index)}}, nil
		}
	case "ping":
		return nil, nil
	case "error":
		if chunk.Error != nil {
			return nil, litellm.NewProviderError("anthropic", litellm.ErrorTypeProvider, fmt.Sprintf("anthropic: stream error: [%s] %s", chunk.Error.Type, chunk.Error.Message))
		}
		return nil, litellm.NewProviderError("anthropic", litellm.ErrorTypeProvider, "anthropic: unknown stream error")
	default:
		return []litellm.Event{litellm.ProviderEvent{Name: chunk.Type, Raw: raw}}, nil
	}
	return nil, nil
}

func convertStreamUsage(u *anthropicUsage, model string) litellm.Usage {
	if u == nil {
		return litellm.Usage{}
	}
	return litellm.Usage{
		InputTokens:      u.InputTokens + u.CacheReadInputTokens,
		OutputTokens:     u.OutputTokens,
		TotalTokens:      u.InputTokens + u.CacheReadInputTokens + u.OutputTokens,
		ReasoningTokens:  u.reasoningTokens(),
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		Provider:         "anthropic",
		Model:            model,
	}
}

func (s *stream) mergeUsage(u *anthropicUsage) {
	next := convertStreamUsage(u, s.model)
	if next.InputTokens > 0 || u.CacheReadInputTokens > 0 {
		s.usage.InputTokens = next.InputTokens
		s.usage.CacheReadTokens = next.CacheReadTokens
	}
	if next.OutputTokens > 0 {
		s.usage.OutputTokens = next.OutputTokens
	}
	if next.ReasoningTokens > 0 {
		s.usage.ReasoningTokens = next.ReasoningTokens
	}
	if next.CacheWriteTokens > 0 {
		s.usage.CacheWriteTokens = next.CacheWriteTokens
	}
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
	s.usage.Provider = "anthropic"
	s.usage.Model = s.model
}
