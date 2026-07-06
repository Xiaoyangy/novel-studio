package bedrock

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"

	"github.com/voocel/litellm"
)

type stream struct {
	reader    *bufio.Reader
	response  *http.Response
	model     string
	pending   []litellm.Event
	done      bool
	finish    litellm.FinishReason
	toolNames map[int]string
	toolIDs   map[int]string
}

func newStream(resp *http.Response, model string) *stream {
	return &stream{
		reader:    bufio.NewReader(resp.Body),
		response:  resp,
		model:     model,
		toolNames: make(map[int]string),
		toolIDs:   make(map[int]string),
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
	for {
		payload, err := s.readEventStreamMessage()
		if err != nil {
			if err == io.EOF {
				s.done = true
				return nil, litellm.NewProviderError("bedrock", litellm.ErrorTypeProvider, "bedrock: stream ended before metadata")
			}
			return nil, litellm.NewNetworkError("bedrock", "read stream", err)
		}
		if len(payload) == 0 {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, litellm.NewProviderErrorWithCause("bedrock", litellm.ErrorTypeProvider, "bedrock: parse stream event", err)
		}
		events, err := s.events(event)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
}

func (s *stream) Close() error {
	return s.response.Body.Close()
}

func (s *stream) events(event map[string]json.RawMessage) ([]litellm.Event, error) {
	if data, ok := event["contentBlockStart"]; ok {
		return s.contentBlockStart(data)
	}
	if data, ok := event["contentBlockDelta"]; ok {
		return s.contentBlockDelta(data)
	}
	if data, ok := event["contentBlockStop"]; ok {
		return s.contentBlockStop(data)
	}
	if data, ok := event["messageStop"]; ok {
		var stop struct {
			StopReason string `json:"stopReason"`
		}
		if err := json.Unmarshal(data, &stop); err != nil {
			return nil, bedrockStreamProviderError("bedrock: parse messageStop", err)
		}
		s.finish = litellm.NormalizeFinishReason(stop.StopReason)
		return nil, nil
	}
	if data, ok := event["metadata"]; ok {
		var meta struct {
			Usage usage `json:"usage"`
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, litellm.NewProviderErrorWithCause("bedrock", litellm.ErrorTypeProvider, "bedrock: parse metadata", err)
		}
		s.done = true
		usage := litellm.Usage{
			InputTokens:      meta.Usage.InputTokens + meta.Usage.CacheReadInputTokens,
			OutputTokens:     meta.Usage.OutputTokens,
			TotalTokens:      meta.Usage.TotalTokens,
			CacheReadTokens:  meta.Usage.CacheReadInputTokens,
			CacheWriteTokens: meta.Usage.CacheWriteInputTokens,
			Provider:         "bedrock",
			Model:            s.model,
		}
		return []litellm.Event{
			litellm.UsageEvent{Usage: usage},
			litellm.DoneEvent{FinishReason: s.finish, Provider: "bedrock", Model: s.model},
		}, nil
	}
	if err := s.streamException(event); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, bedrockStreamProviderError("bedrock: marshal unknown stream event", err)
	}
	return []litellm.Event{bedrockProviderEvent("bedrock.event", raw)}, nil
}

func (s *stream) contentBlockStart(data json.RawMessage) ([]litellm.Event, error) {
	var start struct {
		ContentBlockIndex int `json:"contentBlockIndex"`
		Start             struct {
			ToolUse *struct {
				ToolUseID string `json:"toolUseId"`
				Name      string `json:"name"`
			} `json:"toolUse"`
		} `json:"start"`
	}
	if err := json.Unmarshal(data, &start); err != nil {
		return nil, bedrockStreamProviderError("bedrock: parse contentBlockStart", err)
	}
	if start.Start.ToolUse == nil {
		return []litellm.Event{bedrockProviderEvent("bedrock.contentBlockStart", data)}, nil
	}
	s.toolIDs[start.ContentBlockIndex] = start.Start.ToolUse.ToolUseID
	s.toolNames[start.ContentBlockIndex] = start.Start.ToolUse.Name
	return []litellm.Event{litellm.ToolUseStart{
		ID:    start.Start.ToolUse.ToolUseID,
		Name:  start.Start.ToolUse.Name,
		Index: litellm.IntPtr(start.ContentBlockIndex),
	}}, nil
}

func (s *stream) contentBlockDelta(data json.RawMessage) ([]litellm.Event, error) {
	var delta struct {
		ContentBlockIndex int `json:"contentBlockIndex"`
		Delta             struct {
			Text             string `json:"text"`
			ReasoningContent *struct {
				Text            string `json:"text"`
				Signature       string `json:"signature"`
				RedactedContent []byte `json:"redactedContent"`
			} `json:"reasoningContent"`
			ToolUse *struct {
				Input string `json:"input"`
			} `json:"toolUse"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &delta); err != nil {
		return nil, bedrockStreamProviderError("bedrock: parse contentBlockDelta", err)
	}
	if delta.Delta.Text != "" {
		return []litellm.Event{litellm.ContentDelta{Text: delta.Delta.Text, ContentIndex: litellm.IntPtr(delta.ContentBlockIndex)}}, nil
	}
	if delta.Delta.ReasoningContent != nil {
		return []litellm.Event{litellm.ReasoningDelta{
			Text:      delta.Delta.ReasoningContent.Text,
			Signature: delta.Delta.ReasoningContent.Signature,
			Redacted:  append([]byte(nil), delta.Delta.ReasoningContent.RedactedContent...),
			Index:     litellm.IntPtr(delta.ContentBlockIndex),
		}}, nil
	}
	if delta.Delta.ToolUse != nil && delta.Delta.ToolUse.Input != "" {
		return []litellm.Event{litellm.ToolUseDelta{
			ID:             s.toolIDs[delta.ContentBlockIndex],
			Index:          litellm.IntPtr(delta.ContentBlockIndex),
			ArgumentsDelta: []byte(delta.Delta.ToolUse.Input),
		}}, nil
	}
	return []litellm.Event{bedrockProviderEvent("bedrock.contentBlockDelta", data)}, nil
}

func (s *stream) contentBlockStop(data json.RawMessage) ([]litellm.Event, error) {
	var stop struct {
		ContentBlockIndex int `json:"contentBlockIndex"`
	}
	if err := json.Unmarshal(data, &stop); err != nil {
		return nil, bedrockStreamProviderError("bedrock: parse contentBlockStop", err)
	}
	id := s.toolIDs[stop.ContentBlockIndex]
	if id == "" {
		return nil, nil
	}
	delete(s.toolIDs, stop.ContentBlockIndex)
	delete(s.toolNames, stop.ContentBlockIndex)
	return []litellm.Event{litellm.ToolUseDone{ID: id, Index: litellm.IntPtr(stop.ContentBlockIndex)}}, nil
}

func (s *stream) streamException(event map[string]json.RawMessage) error {
	for name, raw := range event {
		switch name {
		case "throttlingException":
			return bedrockStreamError(litellm.ErrorTypeRateLimit, name, raw)
		case "validationException":
			return bedrockStreamError(litellm.ErrorTypeValidation, name, raw)
		case "serviceUnavailableException":
			return bedrockStreamError(litellm.ErrorTypeOverloaded, name, raw)
		case "internalServerException", "modelStreamErrorException":
			return bedrockStreamError(litellm.ErrorTypeProvider, name, raw)
		}
	}
	return nil
}

func bedrockStreamError(errorType litellm.ErrorType, name string, raw json.RawMessage) error {
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := payload.Message
	if message == "" {
		message = name
	}
	return litellm.NewProviderError("bedrock", errorType, "bedrock: stream "+message)
}

func bedrockProviderEvent(name string, raw json.RawMessage) litellm.ProviderEvent {
	return litellm.ProviderEvent{Name: name, Raw: append(json.RawMessage(nil), raw...)}
}

func bedrockStreamProviderError(message string, cause error) error {
	return litellm.NewProviderErrorWithCause("bedrock", litellm.ErrorTypeProvider, message, cause)
}
