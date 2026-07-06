package gemini

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
	queued           []response
	buffer           string
	done             bool
	model            string
	usage            litellm.Usage
	finish           litellm.FinishReason
	emittedOutput    bool
	nextToolIndex    int
	toolIndexByID    map[string]int
}

type promptFeedback struct {
	BlockReason   string         `json:"blockReason,omitempty"`
	SafetyRatings []safetyRating `json:"safetyRatings,omitempty"`
}

type streamPayload struct {
	Candidates     []candidate     `json:"candidates,omitempty"`
	UsageMetadata  *usageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *promptFeedback `json:"promptFeedback,omitempty"`
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
		toolIndexByID:    make(map[string]int),
	}
}

func (s *stream) Next() (litellm.Event, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	if len(s.queued) > 0 {
		next := s.queued[0]
		s.queued = s.queued[1:]
		return s.emit(next)
	}
	if s.done {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || line[0] == ':' {
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			line = strings.TrimSpace(data)
			if line == "" {
				continue
			}
		}
		if line == "[DONE]" {
			s.done = true
			return litellm.DoneEvent{FinishReason: s.finish, Provider: "gemini", Model: s.model}, nil
		}
		s.buffer += line
		parsed, err := s.parseBuffer()
		if err != nil {
			if isIncompleteJSON(err) {
				continue
			}
			return nil, err
		}
		if parsed == nil {
			continue
		}
		return s.emit(*parsed)
	}
	if err := s.scanner.Err(); err != nil {
		return nil, litellm.NewNetworkError("gemini", "stream read error", err)
	}
	if strings.TrimSpace(s.buffer) != "" {
		return nil, litellm.NewProviderError("gemini", litellm.ErrorTypeProvider, "gemini: incomplete JSON stream")
	}
	s.done = true
	return litellm.DoneEvent{FinishReason: s.finish, Provider: "gemini", Model: s.model}, nil
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) parseBuffer() (*response, error) {
	var single streamPayload
	if err := json.Unmarshal([]byte(s.buffer), &single); err == nil {
		s.buffer = ""
		return streamToResponse(single)
	} else {
		var list []streamPayload
		if arrErr := json.Unmarshal([]byte(s.buffer), &list); arrErr == nil {
			s.buffer = ""
			if len(list) == 0 {
				return nil, nil
			}
			for _, item := range list[1:] {
				converted, err := streamToResponse(item)
				if err != nil {
					return nil, err
				}
				if converted != nil {
					s.queued = append(s.queued, *converted)
				}
			}
			return streamToResponse(list[0])
		} else if isIncompleteJSON(err) || isIncompleteJSON(arrErr) {
			return nil, err
		} else {
			return nil, litellm.NewProviderErrorWithCause("gemini", litellm.ErrorTypeProvider, "gemini: parse stream chunk", err)
		}
	}
}

func streamToResponse(item streamPayload) (*response, error) {
	if item.PromptFeedback != nil {
		return nil, promptFeedbackError(item.PromptFeedback)
	}
	return &response{Candidates: item.Candidates, UsageMetadata: item.UsageMetadata}, nil
}

func promptFeedbackError(feedback *promptFeedback) error {
	message := "response ended without candidates"
	if feedback != nil && feedback.BlockReason != "" {
		message += ": prompt blocked: " + feedback.BlockReason
	}
	if feedback != nil {
		if ratings := formatSafetyRatings(feedback.SafetyRatings); ratings != "" {
			message += " (" + ratings + ")"
		}
	}
	return litellm.NewProviderError("gemini", litellm.ErrorTypeProvider, "gemini: "+message)
}

func (s *stream) emit(resp response) (litellm.Event, error) {
	events, err := s.events(resp)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return s.Next()
	}
	s.pending = append(s.pending, events[1:]...)
	return events[0], nil
}

func (s *stream) events(resp response) ([]litellm.Event, error) {
	events := make([]litellm.Event, 0)
	if resp.UsageMetadata != nil {
		s.usage = litellm.Usage{
			InputTokens:     resp.UsageMetadata.PromptTokenCount,
			OutputTokens:    resp.UsageMetadata.CandidatesTokenCount,
			ReasoningTokens: resp.UsageMetadata.ThoughtsTokenCount,
			TotalTokens:     resp.UsageMetadata.TotalTokenCount,
			CacheReadTokens: resp.UsageMetadata.CachedContentTokenCount,
			Provider:        "gemini",
			Model:           s.model,
		}
		events = append(events, litellm.UsageEvent{Usage: s.usage})
	}
	if len(resp.Candidates) == 0 {
		return events, nil
	}
	candidate := resp.Candidates[0]
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			if part.Thought != nil && *part.Thought {
				if s.includeReasoning {
					events = append(events, litellm.ReasoningDelta{Text: part.Text, Signature: part.ThoughtSignature})
					s.emittedOutput = true
				}
			} else {
				events = append(events, litellm.ContentDelta{Text: part.Text})
				s.emittedOutput = true
			}
		}
		if part.FunctionCall != nil {
			args, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				return nil, litellm.NewProviderErrorWithCause("gemini", litellm.ErrorTypeProvider, fmt.Sprintf("gemini: marshal function call %q arguments", part.FunctionCall.Name), err)
			}
			id := part.FunctionCall.ID
			var warning *litellm.Warning
			if id == "" {
				id = fmt.Sprintf("call_%d", generatedToolCallSeq.Add(1))
				w := generatedToolCallIDWarning(part.FunctionCall.Name, id)
				warning = &w
			}
			index := s.toolIndex(id)
			if warning != nil {
				events = append(events, litellm.WarningEvent{Warning: *warning})
			}
			events = append(events,
				litellm.ToolUseStart{ID: id, Name: part.FunctionCall.Name, Index: litellm.IntPtr(index), Signature: part.ThoughtSignature},
				litellm.ToolUseDelta{ID: id, Index: litellm.IntPtr(index), ArgumentsDelta: args, Signature: part.ThoughtSignature},
				litellm.ToolUseDone{ID: id, Index: litellm.IntPtr(index)},
			)
			s.emittedOutput = true
		}
	}
	if candidate.FinishReason != "" {
		finish := litellm.NormalizeFinishReason(candidate.FinishReason)
		if !s.emittedOutput && len(events) == 0 && finish != litellm.FinishReasonStop && finish != litellm.FinishReasonToolCall {
			return nil, candidateFinishError(candidate)
		}
		s.finish = finish
		events = append(events, litellm.DoneEvent{FinishReason: finish, Provider: "gemini", Model: s.model})
		s.done = true
	}
	return events, nil
}

func (s *stream) toolIndex(id string) int {
	if index, ok := s.toolIndexByID[id]; ok {
		return index
	}
	index := s.nextToolIndex
	s.toolIndexByID[id] = index
	s.nextToolIndex++
	return index
}

func candidateFinishError(candidate candidate) error {
	message := "stream finished before content"
	if candidate.FinishReason != "" {
		message += ": finish_reason=" + candidate.FinishReason
	}
	if candidate.FinishMessage != "" {
		message += ": " + candidate.FinishMessage
	}
	if ratings := formatSafetyRatings(candidate.SafetyRatings); ratings != "" {
		message += " (" + ratings + ")"
	}
	return litellm.NewProviderError("gemini", litellm.ErrorTypeProvider, "gemini: "+message)
}

func isIncompleteJSON(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unexpected end of JSON input") || strings.Contains(msg, "unexpected EOF")
}
