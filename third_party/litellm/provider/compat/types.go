package compat

import "encoding/json"

type chatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

type choice struct {
	Index        int             `json:"index"`
	Message      message         `json:"message"`
	Delta        json.RawMessage `json:"delta,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

type message struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	Refusal          string          `json:"refusal,omitempty"`
	ToolCalls        []toolCall      `json:"tool_calls,omitempty"`
	ReasoningSummary any             `json:"reasoning_summary,omitempty"`
	ReasoningDetails any             `json:"reasoning_details,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningText    string          `json:"reasoning_text,omitempty"`
	Thinking         string          `json:"thinking,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	PromptCacheHitTokens    int                      `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens   int                      `json:"prompt_cache_miss_tokens,omitempty"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type streamChunk struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Choices []streamChoice  `json:"choices"`
	Usage   json.RawMessage `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int             `json:"index"`
	Delta        json.RawMessage `json:"delta"`
	FinishReason string          `json:"finish_reason"`
}

type modelList struct {
	Data []modelInfo `json:"data"`
}

type modelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Created       int64  `json:"created,omitempty"`
	ContextLength int    `json:"context_length,omitempty"`
}
