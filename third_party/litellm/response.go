package litellm

import "encoding/json"

type Response struct {
	Blocks []Block
	Usage  Usage

	Model    string
	Provider string

	FinishReason FinishReason
	Warnings     []Warning
	Raw          json.RawMessage
}

func CaptureRawResponse(req *Request, resp *Response, raw []byte) {
	if req == nil || resp == nil || !req.captureRawResponse {
		return
	}
	resp.Raw = json.RawMessage(cloneBytes(raw))
}

func (r *Response) Text() string {
	if r == nil {
		return ""
	}
	var out string
	for _, block := range r.Blocks {
		if text, ok := block.(TextBlock); ok {
			out += text.Text
		}
	}
	return out
}

func (r *Response) ToolCalls() []ToolUseBlock {
	if r == nil {
		return nil
	}
	var calls []ToolUseBlock
	for _, block := range r.Blocks {
		if call, ok := block.(ToolUseBlock); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func (r *Response) Reasoning() string {
	if r == nil {
		return ""
	}
	var out string
	for _, block := range r.Blocks {
		if reasoning, ok := block.(ReasoningBlock); ok {
			out += reasoning.Text
		}
	}
	return out
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	ReasoningTokens int

	CacheReadTokens  int
	CacheWriteTokens int

	Provider string
	Model    string
}

func (u Usage) HasTokens() bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.TotalTokens > 0 ||
		u.ReasoningTokens > 0 ||
		u.CacheReadTokens > 0 ||
		u.CacheWriteTokens > 0
}

func (u *Usage) StampModel(provider, model string) {
	if u == nil || !u.HasTokens() {
		return
	}
	if u.Provider == "" {
		u.Provider = provider
	}
	if u.Model == "" {
		u.Model = model
	}
}

type FinishReason string

const (
	FinishReasonStop     FinishReason = "stop"
	FinishReasonLength   FinishReason = "length"
	FinishReasonToolCall FinishReason = "tool_calls"
	FinishReasonError    FinishReason = "error"
	FinishReasonSafety   FinishReason = "safety"
)

func NormalizeFinishReason(raw string) FinishReason {
	switch raw {
	case "stop", "end_turn", "STOP", "stop_sequence", "pause_turn":
		return FinishReasonStop
	case "length", "max_tokens", "MAX_TOKENS":
		return FinishReasonLength
	case "tool_calls", "tool_use", "FUNCTION_CALLING":
		return FinishReasonToolCall
	case "completed":
		return FinishReasonStop
	case "incomplete":
		return FinishReasonLength
	case "safety", "SAFETY", "content_filter", "refusal", "RECITATION", "sensitive", "BLOCKLIST",
		"PROHIBITED_CONTENT", "SPII", "LANGUAGE", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT",
		"IMAGE_RECITATION":
		return FinishReasonSafety
	case "failed", "error", "cancelled", "canceled", "insufficient_system_resource", "network_error",
		"MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "OTHER",
		"IMAGE_OTHER", "NO_IMAGE", "MISSING_THOUGHT_SIGNATURE":
		return FinishReasonError
	case "":
		return ""
	default:
		return FinishReason(raw)
	}
}

type Warning struct {
	Code     string
	Provider string
	Message  string
}
