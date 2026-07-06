package openai

import (
	"bufio"
	"encoding/json"
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
	toolIDs          map[int]string
	finish           litellm.FinishReason
}

func newStream(resp *http.Response, req *litellm.Request) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &stream{
		resp:             resp,
		scanner:          scanner,
		includeReasoning: thinkingEnabled(req),
		model:            req.Model,
		toolIDs:          make(map[int]string),
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
		if line == "" || line[0] == ':' {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if trimmed, found := strings.CutPrefix(line, "data:"); found {
				data = strings.TrimSpace(trimmed)
				ok = true
			}
		}
		if !ok {
			continue
		}
		if data == "[DONE]" {
			s.done = true
			return litellm.DoneEvent{FinishReason: s.finish, Provider: "openai", Model: s.model}, nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, litellm.NewProviderErrorWithCause("openai", litellm.ErrorTypeProvider, "openai: parse stream chunk", err)
		}
		if chunk.Model != "" {
			s.model = chunk.Model
		}
		events := s.events(chunk)
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, litellm.NewNetworkError("openai", "stream read error", err)
	}
	s.done = true
	return nil, litellm.NewProviderError("openai", litellm.ErrorTypeProvider, "openai: stream ended before [DONE]")
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) events(chunk streamChunk) []litellm.Event {
	events := make([]litellm.Event, 0, 4)
	if chunk.Usage != nil {
		events = append(events, litellm.UsageEvent{Usage: convertUsage(chunk.Usage, s.model)})
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			events = append(events, litellm.ContentDelta{
				Text:        choice.Delta.Content,
				OutputIndex: litellm.IntPtr(choice.Index),
			})
		}
		if s.includeReasoning {
			if text, summary := extractDeltaReasoning(choice.Delta); text != "" {
				events = append(events, litellm.ReasoningDelta{
					Text:    text,
					Summary: summary,
					Index:   litellm.IntPtr(choice.Index),
				})
			}
		}
		for _, call := range choice.Delta.ToolCalls {
			index := call.Index
			id := call.ID
			if id != "" {
				s.toolIDs[index] = id
			} else {
				id = s.toolIDs[index]
			}
			if call.ID != "" || (call.Function != nil && call.Function.Name != "") {
				start := litellm.ToolUseStart{
					ID:    id,
					Name:  "",
					Index: &index,
				}
				if call.Function != nil {
					start.Name = call.Function.Name
				}
				events = append(events, start)
			}
			if call.Function != nil && call.Function.Arguments != "" {
				events = append(events, litellm.ToolUseDelta{
					ID:             id,
					Index:          &index,
					ArgumentsDelta: []byte(call.Function.Arguments),
				})
			}
		}
		if choice.FinishReason != "" {
			s.finish = litellm.NormalizeFinishReason(choice.FinishReason)
		}
	}
	return events
}
