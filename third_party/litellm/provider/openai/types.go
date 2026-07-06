package openai

import "encoding/json"

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`

	MaxTokens           *int `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`

	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`

	N           *int  `json:"n,omitempty"`
	Logprobs    *bool `json:"logprobs,omitempty"`
	TopLogprobs *int  `json:"top_logprobs,omitempty"`
	Store       *bool `json:"store,omitempty"`
	Moderation  any   `json:"moderation,omitempty"`

	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`

	Stop []string `json:"stop,omitempty"`

	Tools      []tool `json:"tools,omitempty"`
	ToolChoice any    `json:"tool_choice,omitempty"`

	ResponseFormat *responseFormat `json:"response_format,omitempty"`

	PromptCacheKey       string `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string `json:"prompt_cache_retention,omitempty"`

	Prediction *prediction `json:"prediction,omitempty"`

	Metadata         map[string]string `json:"metadata,omitempty"`
	ServiceTier      string            `json:"service_tier,omitempty"`
	SafetyIdentifier string            `json:"safety_identifier,omitempty"`
	User             string            `json:"user,omitempty"`

	Modalities       []string `json:"modalities,omitempty"`
	Audio            any      `json:"audio,omitempty"`
	Verbosity        string   `json:"verbosity,omitempty"`
	WebSearchOptions any      `json:"web_search_options,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
	Seed              *int  `json:"seed,omitempty"`
}

type streamOptions struct {
	IncludeUsage       bool  `json:"include_usage,omitempty"`
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}

type prediction struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema"`
	Strict      *bool  `json:"strict,omitempty"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type tool struct {
	Type     string        `json:"type"`
	Function *toolFunction `json:"function,omitempty"`
}

type toolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
	Strict      *bool  `json:"strict,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolCallFunc `json:"function"`
}

type toolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCallDelta struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function *toolCallFuncDelta `json:"function,omitempty"`
}

type toolCallFuncDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
	Raw     json.RawMessage
}

type choice struct {
	Index            int               `json:"index"`
	Message          responseMessage   `json:"message"`
	Delta            delta             `json:"delta,omitempty"`
	ReasoningSummary *reasoningSummary `json:"reasoning_summary,omitempty"`
	FinishReason     string            `json:"finish_reason,omitempty"`
}

type responseMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	ToolCalls        []toolCall      `json:"tool_calls,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

type reasoningSummary struct {
	Text string `json:"text,omitempty"`
}

type delta struct {
	Content          string            `json:"content,omitempty"`
	ToolCalls        []toolCallDelta   `json:"tool_calls,omitempty"`
	ReasoningSummary *reasoningSummary `json:"reasoning_summary,omitempty"`
	Reasoning        string            `json:"reasoning,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
}

type usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type streamChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
}

type modelList struct {
	Data []modelInfo `json:"data"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}
