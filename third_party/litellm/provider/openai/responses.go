package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/voocel/litellm"
)

type ResponsesRequest struct {
	Model    string
	Messages []litellm.Message
	Input    any

	Instructions       string
	Conversation       any
	PreviousResponseID string

	ContextManagement []map[string]any
	MaxOutputTokens   *int
	MaxToolCalls      *int
	Include           []string
	TopLogprobs       *int

	Temperature *float64
	TopP        *float64

	ResponseFormat *litellm.ResponseFormat
	TextVerbosity  string
	Truncation     string

	Tools             []litellm.Tool
	OpenAITools       []ResponsesTool
	ToolChoice        any
	ParallelToolCalls *bool

	ReasoningEffort  string
	ReasoningSummary string
	Thinking         *litellm.Thinking

	PromptCacheKey       string
	PromptCacheRetention string
	Metadata             map[string]string
	SafetyIdentifier     string

	ServiceTier   string
	Background    *bool
	Store         *bool
	StreamOptions *ResponsesStreamOptions

	Prompt map[string]any

	CaptureRawResponse bool

	providerOptions   map[string]any
	unsupportedFields []string
}

type ResponsesTool map[string]any

type ResponsesStreamOptions struct {
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}

func responsesRequestFromChat(req *litellm.Request) *ResponsesRequest {
	if req == nil {
		return nil
	}
	out := &ResponsesRequest{
		Model:              req.Model,
		Messages:           append([]litellm.Message(nil), req.Messages...),
		MaxOutputTokens:    req.MaxTokens,
		Temperature:        req.Temperature,
		TopP:               req.TopP,
		Tools:              append([]litellm.Tool(nil), req.Tools...),
		ToolChoice:         req.ToolChoice,
		ResponseFormat:     req.ResponseFormat,
		Thinking:           req.Thinking,
		CaptureRawResponse: req.CaptureRawResponse(),
	}
	if len(req.ProviderOptions) > 0 {
		out.providerOptions = cloneMapAny(map[string]any(req.ProviderOptions))
	}
	if len(req.Stop) > 0 {
		out.unsupportedFields = append(out.unsupportedFields, "stop")
	}
	if req.Cache != nil {
		out.unsupportedFields = append(out.unsupportedFields, "cache")
	}
	return out
}

type responsesRequest struct {
	Model string `json:"model"`
	Input any    `json:"input,omitempty"`

	Instructions       string `json:"instructions,omitempty"`
	Conversation       any    `json:"conversation,omitempty"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`

	ContextManagement []map[string]any        `json:"context_management,omitempty"`
	MaxOutputTokens   *int                    `json:"max_output_tokens,omitempty"`
	MaxToolCalls      *int                    `json:"max_tool_calls,omitempty"`
	Include           []string                `json:"include,omitempty"`
	TopLogprobs       *int                    `json:"top_logprobs,omitempty"`
	Stream            *bool                   `json:"stream,omitempty"`
	StreamOptions     *ResponsesStreamOptions `json:"stream_options,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`

	Text       *responsesText      `json:"text,omitempty"`
	Truncation string              `json:"truncation,omitempty"`
	Tools      []responsesToolWire `json:"tools,omitempty"`

	ToolChoice        any   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	Reasoning *responsesReasoning `json:"reasoning,omitempty"`

	PromptCacheKey       string `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string `json:"prompt_cache_retention,omitempty"`

	Metadata         map[string]string `json:"metadata,omitempty"`
	SafetyIdentifier string            `json:"safety_identifier,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`
	Background  *bool  `json:"background,omitempty"`
	Store       *bool  `json:"store,omitempty"`

	Prompt map[string]any `json:"prompt,omitempty"`
}

type responsesText struct {
	Format    *responsesTextFormat `json:"format,omitempty"`
	Verbosity string               `json:"verbosity,omitempty"`
}

type responsesTextFormat struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
	Strict      *bool  `json:"strict,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesToolWire struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  any            `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
	Raw         map[string]any `json:"-"`
}

func (t responsesToolWire) MarshalJSON() ([]byte, error) {
	if t.Raw != nil {
		return json.Marshal(t.Raw)
	}
	body := map[string]any{"type": t.Type}
	if t.Name != "" {
		body["name"] = t.Name
	}
	if t.Description != "" {
		body["description"] = t.Description
	}
	if t.Parameters != nil {
		body["parameters"] = t.Parameters
	}
	if t.Strict != nil {
		body["strict"] = *t.Strict
	}
	return json.Marshal(body)
}

type responsesInputItem struct {
	Type             string                 `json:"type"`
	Role             string                 `json:"role,omitempty"`
	Content          []responsesContentItem `json:"content,omitempty"`
	ID               string                 `json:"id,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
	Output           string                 `json:"output,omitempty"`
	Status           string                 `json:"status,omitempty"`
	Summary          []responsesSummaryItem `json:"summary,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage        `json:"-"`
}

func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return append([]byte(nil), i.Raw...), nil
	}
	type alias responsesInputItem
	return json.Marshal(alias(i))
}

type responsesContentItem struct {
	Type        string                   `json:"type"`
	Text        string                   `json:"text,omitempty"`
	ImageURL    *responsesImageURL       `json:"image_url,omitempty"`
	Annotations []map[string]interface{} `json:"annotations,omitempty"`
	Logprobs    []map[string]interface{} `json:"logprobs,omitempty"`
}

type responsesImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type responsesResponse struct {
	ID         string                `json:"id"`
	Model      string                `json:"model"`
	Status     string                `json:"status,omitempty"`
	OutputText string                `json:"output_text"`
	Output     []responsesOutputItem `json:"output"`
	Usage      responsesUsage        `json:"usage"`
	Error      *responsesError       `json:"error,omitempty"`
}

type responsesCompletedEvent struct {
	Response struct {
		Model  string         `json:"model"`
		Status string         `json:"status,omitempty"`
		Usage  responsesUsage `json:"usage"`
	} `json:"response"`
	SequenceNumber int `json:"sequence_number,omitempty"`
}

type responsesErrorEvent struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type responsesError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type responsesOutputItem struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type"`
	Role             string                 `json:"role,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
	Content          []responsesContentItem `json:"content,omitempty"`
	Summary          []responsesSummaryItem `json:"summary,omitempty"`
	Status           string                 `json:"status,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage        `json:"-"`
}

func (i *responsesOutputItem) UnmarshalJSON(data []byte) error {
	type alias responsesOutputItem
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*i = responsesOutputItem(out)
	i.Raw = append(json.RawMessage(nil), data...)
	return nil
}

type responsesSummaryItem struct {
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens         int                           `json:"input_tokens"`
	OutputTokens        int                           `json:"output_tokens"`
	TotalTokens         int                           `json:"total_tokens"`
	InputTokensDetails  *responsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type responsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type responsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

func (p *Provider) Responses(ctx context.Context, req *ResponsesRequest) (*litellm.Response, error) {
	wire, err := p.buildResponsesRequest(req, false)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeInternal, "openai: marshal responses request", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url("/responses"), bytes.NewReader(body))
	if err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeInternal, "openai: create responses request", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "responses request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "read responses response failed", err)
	}
	var parsed responsesResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "openai: decode responses response", err)
	}
	if parsed.Error != nil {
		return nil, litellm.NewProviderError(p.Name(), litellm.ErrorTypeProvider, fmt.Sprintf("openai: responses error: [%s] %s", parsed.Error.Code, parsed.Error.Message))
	}
	out, err := convertResponsesResponse(&parsed, req.Model)
	if err != nil {
		return nil, litellm.WrapError(err, p.Name())
	}
	if req.CaptureRawResponse {
		out.Raw = append(json.RawMessage(nil), data...)
	}
	return out, nil
}

func (p *Provider) ResponsesStream(ctx context.Context, req *ResponsesRequest) (litellm.Stream, error) {
	wire, err := p.buildResponsesRequest(req, true)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeInternal, "openai: marshal responses stream request", err)
	}
	streamCtx := ctx
	var cancel context.CancelFunc
	if p.cfg.StreamIdleTimeout > 0 {
		streamCtx, cancel = context.WithCancel(ctx)
	}
	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, p.url("/responses"), bytes.NewReader(body))
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeInternal, "openai: create responses stream request", err)
	}
	if err := p.setHeaders(streamCtx, httpReq); err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, litellm.NewNetworkError(p.Name(), "responses stream request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	stream := litellm.Stream(newResponsesStream(resp, req.Model))
	return litellm.WithStreamIdleWatchdog(stream, cancel, p.cfg.StreamIdleTimeout, p.Name()), nil
}

func (p *Provider) buildResponsesRequest(req *ResponsesRequest, stream bool) (*responsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("openai: responses request cannot be nil")
	}
	effective := *req
	if err := applyResponsesProviderOptions(&effective, stream); err != nil {
		return nil, err
	}
	if err := validateResponsesRequest(&effective, stream); err != nil {
		return nil, err
	}
	out := &responsesRequest{
		Model:                effective.Model,
		Conversation:         effective.Conversation,
		PreviousResponseID:   effective.PreviousResponseID,
		ContextManagement:    cloneMapAnySlice(effective.ContextManagement),
		MaxOutputTokens:      effective.MaxOutputTokens,
		MaxToolCalls:         effective.MaxToolCalls,
		Include:              append([]string(nil), effective.Include...),
		TopLogprobs:          effective.TopLogprobs,
		Temperature:          effective.Temperature,
		TopP:                 effective.TopP,
		Truncation:           effective.Truncation,
		ToolChoice:           effective.ToolChoice,
		ParallelToolCalls:    effective.ParallelToolCalls,
		PromptCacheKey:       effective.PromptCacheKey,
		PromptCacheRetention: effective.PromptCacheRetention,
		Metadata:             cloneStringMap(effective.Metadata),
		SafetyIdentifier:     effective.SafetyIdentifier,
		ServiceTier:          effective.ServiceTier,
		Background:           effective.Background,
		Store:                effective.Store,
		StreamOptions:        cloneResponsesStreamOptions(effective.StreamOptions),
		Prompt:               cloneMapAny(effective.Prompt),
	}
	if stream {
		out.Stream = litellm.Bool(true)
	}
	out.Input = cloneAny(effective.Input)
	if len(effective.Messages) > 0 {
		instructions, messages, err := responsesInstructions(effective.Messages)
		if err != nil {
			return nil, err
		}
		if effective.Instructions != "" {
			instructions = strings.TrimSpace(strings.Join([]string{effective.Instructions, instructions}, "\n"))
		}
		out.Instructions = instructions
		if out.Input != nil && len(messages) > 0 {
			return nil, fmt.Errorf("openai: responses request cannot set Input and non-system Messages together")
		}
		if out.Input == nil {
			if text, ok := responsesInputString(messages); ok {
				out.Input = text
			} else {
				items, err := responsesInputItems(messages)
				if err != nil {
					return nil, err
				}
				if len(items) > 0 {
					out.Input = items
				}
			}
		}
	} else {
		out.Instructions = effective.Instructions
	}
	text, err := p.responsesText(&effective)
	if err != nil {
		return nil, err
	}
	out.Text = text
	reasoning, err := responsesReasoningConfig(&effective)
	if err != nil {
		return nil, err
	}
	out.Reasoning = reasoning
	tools, err := responsesTools(effective.Tools, effective.OpenAITools)
	if err != nil {
		return nil, err
	}
	out.Tools = tools
	return out, nil
}

func validateResponsesRequest(req *ResponsesRequest, stream bool) error {
	if req.Model == "" {
		return fmt.Errorf("openai: model is required for responses request")
	}
	if len(req.unsupportedFields) > 0 {
		return fmt.Errorf("openai: responses API does not support request field(s): %s", strings.Join(req.unsupportedFields, ", "))
	}
	if req.Conversation != nil && req.PreviousResponseID != "" {
		return fmt.Errorf("openai: conversation and previous_response_id are mutually exclusive")
	}
	if req.StreamOptions != nil && !stream {
		return fmt.Errorf("openai: responses stream_options requires stream request")
	}
	if err := validateOneOf("text_verbosity", req.TextVerbosity, "low", "medium", "high"); err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	if err := validateOneOf("truncation", req.Truncation, "auto", "disabled"); err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	if err := validateOneOf("reasoning_effort", req.ReasoningEffort, "none", "low", "medium", "high", "xhigh"); err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	if err := validateOneOf("reasoning_summary", req.ReasoningSummary, "auto", "concise", "detailed"); err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	if err := validateOneOf("service_tier", req.ServiceTier, "auto", "default", "flex", "priority"); err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	if err := validatePromptCacheRetention(req.PromptCacheRetention); err != nil {
		return err
	}
	for i, tool := range req.OpenAITools {
		toolType, ok := tool["type"].(string)
		if !ok || strings.TrimSpace(toolType) == "" {
			return fmt.Errorf("openai: openai_tools[%d].type is required", i)
		}
	}
	return nil
}

func applyResponsesProviderOptions(req *ResponsesRequest, stream bool) error {
	if req == nil {
		return nil
	}
	for key := range req.providerOptions {
		if _, ok := providerOptionKeys[key]; !ok {
			return fmt.Errorf("openai: unsupported provider option %q", key)
		}
	}
	for key, value := range req.providerOptions {
		switch key {
		case ProviderOptionStore:
			v, err := optionBool(key, value)
			if err != nil {
				return err
			}
			req.Store = &v
		case ProviderOptionStreamOptions:
			v, err := optionResponsesStreamOptions(key, value)
			if err != nil {
				return err
			}
			if !stream {
				return fmt.Errorf("openai: provider option %q requires stream request", key)
			}
			req.StreamOptions = v
		case ProviderOptionPromptCacheKey:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.PromptCacheKey = v
		case ProviderOptionPromptCacheRetention:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			if err := validatePromptCacheRetention(v); err != nil {
				return err
			}
			req.PromptCacheRetention = v
		case ProviderOptionMetadata:
			v, err := optionStringMap(key, value)
			if err != nil {
				return err
			}
			req.Metadata = v
		case ProviderOptionServiceTier:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.ServiceTier = v
		case ProviderOptionSafetyIdentifier:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.SafetyIdentifier = v
		case ProviderOptionVerbosity:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.TextVerbosity = v
		case ProviderOptionParallelToolCalls:
			v, err := optionBool(key, value)
			if err != nil {
				return err
			}
			req.ParallelToolCalls = &v
		case ProviderOptionTopLogprobs:
			v, err := optionInt(key, value)
			if err != nil {
				return err
			}
			req.TopLogprobs = &v
		default:
			return fmt.Errorf("openai: provider option %q is only supported with chat completions API", key)
		}
	}
	return nil
}

func validateOneOf(field, value string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, ", "), value)
}

func responsesInstructions(messages []litellm.Message) (string, []litellm.Message, error) {
	var parts []string
	filtered := make([]litellm.Message, 0, len(messages))
	for i, msg := range messages {
		if msg.Role == litellm.RoleSystem {
			text, err := textOnlyBlocks(msg.Blocks)
			if err != nil {
				return "", nil, fmt.Errorf("openai: responses system message[%d]: %w", i, err)
			}
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
			continue
		}
		filtered = append(filtered, msg)
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), filtered, nil
}

func responsesInputString(messages []litellm.Message) (string, bool) {
	if len(messages) != 1 || messages[0].Role != litellm.RoleUser {
		return "", false
	}
	if len(messages[0].Blocks) != 1 {
		return "", false
	}
	text, ok := messages[0].Blocks[0].(litellm.TextBlock)
	return text.Text, ok && text.Text != ""
}

func responsesInputItems(messages []litellm.Message) ([]responsesInputItem, error) {
	items := make([]responsesInputItem, 0, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case litellm.RoleUser:
			content, err := responsesContent(msg.Blocks, "input_text")
			if err != nil {
				return nil, fmt.Errorf("openai: responses messages[%d]: %w", i, err)
			}
			if len(content) > 0 {
				items = append(items, responsesInputItem{Type: "message", Role: "user", Content: content})
			}
		case litellm.RoleAssistant:
			for _, block := range msg.Blocks {
				switch b := block.(type) {
				case litellm.TextBlock, litellm.ImageBlock:
					content, err := responsesContent([]litellm.Block{block}, "output_text")
					if err != nil {
						return nil, fmt.Errorf("openai: responses messages[%d]: %w", i, err)
					}
					if len(content) > 0 {
						items = append(items, responsesInputItem{Type: "message", Role: "assistant", Content: content})
					}
				case litellm.ReasoningBlock:
					item, err := responsesReasoningInputItem(b)
					if err != nil {
						return nil, fmt.Errorf("openai: responses messages[%d]: %w", i, err)
					}
					items = append(items, item)
				case litellm.ToolUseBlock:
					items = append(items, responsesInputItem{
						Type:      "function_call",
						CallID:    b.ID,
						Name:      b.Name,
						Arguments: string(b.Arguments),
					})
				default:
					return nil, fmt.Errorf("openai: responses messages[%d]: unsupported block %T", i, block)
				}
			}
		case litellm.RoleTool:
			for _, block := range msg.Blocks {
				result, ok := block.(litellm.ToolResultBlock)
				if !ok {
					return nil, fmt.Errorf("tool role only supports ToolResultBlock, got %T", block)
				}
				output, err := textOnlyBlocks(result.Content)
				if err != nil {
					return nil, fmt.Errorf("tool result %q: %w", result.ToolUseID, err)
				}
				items = append(items, responsesInputItem{
					Type:   "function_call_output",
					CallID: result.ToolUseID,
					Output: output,
				})
			}
		default:
			return nil, fmt.Errorf("unsupported role %q", msg.Role)
		}
	}
	return items, nil
}

func responsesContent(blocks []litellm.Block, textType string) ([]responsesContentItem, error) {
	items := make([]responsesContentItem, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if b.Text != "" {
				items = append(items, responsesContentItem{Type: textType, Text: b.Text})
			}
		case litellm.ImageBlock:
			url, err := imageURLValue(b)
			if err != nil {
				return nil, err
			}
			items = append(items, responsesContentItem{Type: "input_image", ImageURL: &responsesImageURL{URL: url, Detail: b.Detail}})
		case litellm.ToolUseBlock:
			continue
		case litellm.ReasoningBlock:
			return nil, fmt.Errorf("OpenAI Responses does not accept reasoning blocks in message history")
		default:
			return nil, fmt.Errorf("unsupported block %T", block)
		}
	}
	return items, nil
}

func responsesReasoningInputItem(block litellm.ReasoningBlock) (responsesInputItem, error) {
	if len(block.Redacted) > 0 || block.Signature != "" {
		return responsesInputItem{}, fmt.Errorf("OpenAI Responses reasoning blocks only support text summary or provider extra state")
	}
	if len(block.Extra) > 0 {
		var item responsesInputItem
		if err := json.Unmarshal(block.Extra, &item); err != nil {
			return responsesInputItem{}, fmt.Errorf("OpenAI Responses reasoning extra must be valid reasoning item JSON: %w", err)
		}
		if item.Type != "reasoning" {
			return responsesInputItem{}, fmt.Errorf("OpenAI Responses reasoning extra type must be reasoning")
		}
		item.Raw = append(json.RawMessage(nil), block.Extra...)
		return item, nil
	}
	item := responsesInputItem{Type: "reasoning"}
	if block.Text != "" {
		item.Summary = []responsesSummaryItem{{Text: block.Text}}
	}
	return item, nil
}

func textOnlyBlocks(blocks []litellm.Block) (string, error) {
	var out strings.Builder
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(b.Text)
		default:
			return "", fmt.Errorf("only text blocks are supported, got %T", block)
		}
	}
	return out.String(), nil
}

func (p *Provider) responsesText(req *ResponsesRequest) (*responsesText, error) {
	if req.ResponseFormat == nil && req.TextVerbosity == "" {
		return nil, nil
	}
	out := &responsesText{Verbosity: req.TextVerbosity}
	if req.ResponseFormat == nil {
		return out, nil
	}
	format := &responsesTextFormat{Type: string(req.ResponseFormat.Type)}
	if req.ResponseFormat.Type == litellm.ResponseFormatJSONSchema {
		if req.ResponseFormat.JSONSchema == nil {
			return nil, fmt.Errorf("openai: json schema response format requires schema")
		}
		var schema any
		if len(req.ResponseFormat.JSONSchema.Schema) > 0 {
			if err := json.Unmarshal(req.ResponseFormat.JSONSchema.Schema, &schema); err != nil {
				return nil, fmt.Errorf("openai: response schema must be valid JSON: %w", err)
			}
		}
		var strict *bool
		switch req.ResponseFormat.JSONSchema.Strict {
		case litellm.StrictEnabled:
			normalised, err := normalizeStrictSchema(schema)
			if err != nil {
				return nil, fmt.Errorf("openai: response strict schema invalid: %w", err)
			}
			schema = normalised
			strict = litellm.Bool(true)
		case litellm.StrictDisabled:
			strict = litellm.Bool(false)
		}
		format.Name = req.ResponseFormat.JSONSchema.Name
		format.Description = req.ResponseFormat.JSONSchema.Description
		format.Schema = schema
		format.Strict = strict
	}
	out.Format = format
	return out, nil
}

func responsesReasoningConfig(req *ResponsesRequest) (*responsesReasoning, error) {
	if err := req.Thinking.Validate(); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	if req.ReasoningEffort == "" && req.ReasoningSummary == "" && (req.Thinking == nil || req.Thinking.Mode == litellm.ThinkingUnspecified) {
		return nil, nil
	}
	out := &responsesReasoning{Effort: req.ReasoningEffort, Summary: req.ReasoningSummary}
	if out.Effort != "" && req.Thinking != nil && req.Thinking.Mode == litellm.ThinkingDisabled {
		return nil, fmt.Errorf("openai: thinking disabled conflicts with reasoning_effort")
	}
	if req.Thinking != nil {
		switch req.Thinking.Mode {
		case litellm.ThinkingDisabled:
			out.Effort = "none"
		case litellm.ThinkingEnabled:
			if out.Effort == "" {
				out.Effort = req.Thinking.Effort
			}
			if out.Summary == "" && req.Thinking.IncludeOutput {
				out.Summary = "auto"
			}
			if out.Effort == "" && out.Summary == "" {
				return nil, fmt.Errorf("openai: thinking effort, summary, or include_output is required")
			}
		}
	}
	if out.Effort == "" && out.Summary == "" {
		return nil, nil
	}
	if err := validateOneOf("reasoning_effort", out.Effort, "none", "low", "medium", "high", "xhigh"); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	return out, nil
}

func responsesTools(tools []litellm.Tool, hosted []ResponsesTool) ([]responsesToolWire, error) {
	out := make([]responsesToolWire, 0, len(tools)+len(hosted))
	for _, t := range tools {
		var params any = map[string]any{"type": "object"}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &params); err != nil {
				return nil, fmt.Errorf("openai: tool %q parameters must be valid JSON: %w", t.Name, err)
			}
		}
		var strict *bool
		switch t.Strict {
		case litellm.StrictEnabled:
			normalised, err := normalizeStrictSchema(params)
			if err != nil {
				return nil, fmt.Errorf("openai: tool %q strict schema invalid: %w", t.Name, err)
			}
			params = normalised
			strict = litellm.Bool(true)
		case litellm.StrictDisabled:
			strict = litellm.Bool(false)
		}
		out = append(out, responsesToolWire{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
			Strict:      strict,
		})
	}
	for _, tool := range hosted {
		raw := cloneMapAny(map[string]any(tool))
		out = append(out, responsesToolWire{Raw: raw})
	}
	return out, nil
}

func convertResponsesResponse(resp *responsesResponse, fallbackModel string) (*litellm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("openai: responses response cannot be nil")
	}
	out := &litellm.Response{
		Model:        resp.Model,
		Provider:     "openai",
		FinishReason: litellm.NormalizeFinishReason(resp.Status),
		Usage: litellm.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.TotalTokens,
			Provider:     "openai",
			Model:        resp.Model,
		},
	}
	if out.Model == "" {
		out.Model = fallbackModel
		out.Usage.Model = fallbackModel
	}
	if resp.Usage.InputTokensDetails != nil {
		out.Usage.CacheReadTokens = resp.Usage.InputTokensDetails.CachedTokens
	}
	if resp.Usage.OutputTokensDetails != nil {
		out.Usage.ReasoningTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
	}
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			blocks, err := responsesOutputBlocks(item.Content)
			if err != nil {
				return nil, err
			}
			out.Blocks = append(out.Blocks, blocks...)
		case "function_call", "tool_call":
			id := item.CallID
			if id == "" {
				id = item.ID
			}
			if !json.Valid([]byte(item.Arguments)) {
				return nil, fmt.Errorf("openai: tool call %q arguments are not valid JSON", id)
			}
			out.Blocks = append(out.Blocks, litellm.ToolUseBlock{
				ID:        id,
				Name:      item.Name,
				Arguments: json.RawMessage(item.Arguments),
			})
			out.FinishReason = litellm.FinishReasonToolCall
		case "reasoning":
			if text := reasoningSummaryText(item.Summary); text != "" {
				out.Blocks = append(out.Blocks, litellm.ReasoningBlock{
					Text:    text,
					Summary: true,
					Extra:   responsesReasoningExtra(item),
				})
			} else if len(item.Raw) > 0 || item.EncryptedContent != "" || item.ID != "" {
				out.Blocks = append(out.Blocks, litellm.ReasoningBlock{
					Summary: true,
					Extra:   responsesReasoningExtra(item),
				})
			}
		default:
			return nil, fmt.Errorf("openai: unsupported responses output item type %q", item.Type)
		}
	}
	if len(out.Blocks) == 0 && resp.OutputText != "" {
		out.Blocks = append(out.Blocks, litellm.Text(resp.OutputText))
	}
	return out, nil
}

func responsesReasoningExtra(item responsesOutputItem) json.RawMessage {
	if len(item.Raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), item.Raw...)
}

type responsesStream struct {
	resp         *http.Response
	scanner      *bufio.Scanner
	pending      []litellm.Event
	done         bool
	model        string
	currentEvent string
	toolSeen     map[string]bool
	toolIDs      map[string]string
	lastSequence int
}

func newResponsesStream(resp *http.Response, model string) *responsesStream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &responsesStream{
		resp:     resp,
		scanner:  scanner,
		model:    model,
		toolSeen: make(map[string]bool),
		toolIDs:  make(map[string]string),
	}
}

func (s *responsesStream) Next() (litellm.Event, error) {
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
		if event, ok := strings.CutPrefix(line, "event: "); ok {
			s.currentEvent = event
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
			return litellm.DoneEvent{Provider: "openai", Model: s.model}, nil
		}
		eventName := s.currentEvent
		s.currentEvent = ""
		if eventName == "" {
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(data), &peek) == nil {
				eventName = peek.Type
			}
		}
		events, err := s.events(eventName, json.RawMessage(data))
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
		return nil, litellm.NewNetworkError("openai", "responses stream read error", err)
	}
	s.done = true
	return nil, litellm.NewProviderError("openai", litellm.ErrorTypeProvider, "openai: responses stream ended before response.completed")
}

func (s *responsesStream) Close() error {
	return s.resp.Body.Close()
}

func responsesStreamParseError(message string, cause error) error {
	return litellm.NewProviderErrorWithCause("openai", litellm.ErrorTypeProvider, message, cause)
}

func (s *responsesStream) events(name string, raw json.RawMessage) ([]litellm.Event, error) {
	switch name {
	case "response.output_text.delta":
		var delta struct {
			Delta        string `json:"delta"`
			OutputIndex  *int   `json:"output_index,omitempty"`
			ContentIndex *int   `json:"content_index,omitempty"`
			Sequence     int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &delta); err != nil {
			return nil, responsesStreamParseError("openai: parse responses output delta", err)
		}
		if !s.shouldEmit(delta.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.ContentDelta{Text: delta.Delta, OutputIndex: delta.OutputIndex, ContentIndex: delta.ContentIndex}}, nil
	case "response.refusal.delta":
		var delta struct {
			Delta        string `json:"delta"`
			OutputIndex  *int   `json:"output_index,omitempty"`
			ContentIndex *int   `json:"content_index,omitempty"`
			Sequence     int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &delta); err != nil {
			return nil, responsesStreamParseError("openai: parse responses refusal delta", err)
		}
		if !s.shouldEmit(delta.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.RefusalDelta{Text: delta.Delta, OutputIndex: delta.OutputIndex, ContentIndex: delta.ContentIndex}}, nil
	case "response.refusal.done":
		var done struct {
			Refusal      string `json:"refusal"`
			OutputIndex  *int   `json:"output_index,omitempty"`
			ContentIndex *int   `json:"content_index,omitempty"`
			Sequence     int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &done); err != nil {
			return nil, responsesStreamParseError("openai: parse responses refusal done", err)
		}
		if done.Refusal == "" || !s.shouldEmit(done.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.RefusalDelta{Text: done.Refusal, OutputIndex: done.OutputIndex, ContentIndex: done.ContentIndex}}, nil
	case "response.reasoning_text.delta":
		var delta struct {
			Delta    string `json:"delta"`
			Sequence int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &delta); err != nil {
			return nil, responsesStreamParseError("openai: parse responses reasoning delta", err)
		}
		if !s.shouldEmit(delta.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.ReasoningDelta{Text: delta.Delta}}, nil
	case "response.reasoning_summary_text.delta":
		var delta struct {
			Delta    string `json:"delta"`
			Sequence int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &delta); err != nil {
			return nil, responsesStreamParseError("openai: parse responses reasoning summary delta", err)
		}
		if !s.shouldEmit(delta.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.ReasoningDelta{Text: delta.Delta, Summary: true}}, nil
	case "response.output_item.added":
		var item struct {
			Item struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Name   string `json:"name,omitempty"`
				CallID string `json:"call_id,omitempty"`
			} `json:"item"`
			OutputIndex *int `json:"output_index,omitempty"`
			Sequence    int  `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, responsesStreamParseError("openai: parse responses output item added", err)
		}
		if !s.shouldEmit(item.Sequence) {
			return nil, nil
		}
		if item.Item.Type == "function_call" {
			id := toolID(item.Item.ID, item.Item.CallID)
			if item.Item.ID != "" && id != "" {
				s.toolIDs[item.Item.ID] = id
			}
			return []litellm.Event{litellm.ToolUseStart{
				ID:          id,
				Name:        item.Item.Name,
				OutputIndex: item.OutputIndex,
				ItemID:      item.Item.ID,
			}}, nil
		}
		return []litellm.Event{litellm.ProviderEvent{Name: name, Raw: raw}}, nil
	case "response.function_call_arguments.delta":
		var delta struct {
			Delta       string `json:"delta"`
			ItemID      string `json:"item_id"`
			OutputIndex *int   `json:"output_index,omitempty"`
			Sequence    int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &delta); err != nil {
			return nil, responsesStreamParseError("openai: parse responses tool delta", err)
		}
		if !s.shouldEmit(delta.Sequence) {
			return nil, nil
		}
		if delta.ItemID != "" {
			s.toolSeen[delta.ItemID] = true
		}
		id := s.toolID(delta.ItemID)
		return []litellm.Event{litellm.ToolUseDelta{
			ID:             id,
			OutputIndex:    delta.OutputIndex,
			ItemID:         delta.ItemID,
			ArgumentsDelta: []byte(delta.Delta),
		}}, nil
	case "response.function_call_arguments.done":
		var done struct {
			Name        string `json:"name"`
			Arguments   string `json:"arguments"`
			ItemID      string `json:"item_id"`
			OutputIndex *int   `json:"output_index,omitempty"`
			Sequence    int    `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &done); err != nil {
			return nil, responsesStreamParseError("openai: parse responses tool done", err)
		}
		if !s.shouldEmit(done.Sequence) {
			return nil, nil
		}
		if !s.toolSeen[done.ItemID] && done.Arguments != "" {
			id := s.toolID(done.ItemID)
			return []litellm.Event{
				litellm.ToolUseStart{ID: id, Name: done.Name, OutputIndex: done.OutputIndex, ItemID: done.ItemID},
				litellm.ToolUseDelta{ID: id, OutputIndex: done.OutputIndex, ItemID: done.ItemID, ArgumentsDelta: []byte(done.Arguments)},
				litellm.ToolUseDone{ID: id, OutputIndex: done.OutputIndex, ItemID: done.ItemID},
			}, nil
		}
		return []litellm.Event{litellm.ToolUseDone{ID: s.toolID(done.ItemID), OutputIndex: done.OutputIndex, ItemID: done.ItemID}}, nil
	case "response.completed":
		var completed responsesCompletedEvent
		if err := json.Unmarshal(raw, &completed); err != nil {
			return nil, responsesStreamParseError("openai: parse responses completed", err)
		}
		if !s.shouldEmit(completed.SequenceNumber) {
			return nil, nil
		}
		if completed.Response.Model != "" {
			s.model = completed.Response.Model
		}
		s.done = true
		return []litellm.Event{
			litellm.UsageEvent{Usage: responsesUsageToUsage(completed.Response.Usage, s.model)},
			litellm.DoneEvent{FinishReason: litellm.NormalizeFinishReason(completed.Response.Status), Provider: "openai", Model: s.model},
		}, nil
	case "response.incomplete":
		var incomplete responsesCompletedEvent
		if err := json.Unmarshal(raw, &incomplete); err != nil {
			return nil, responsesStreamParseError("openai: parse responses incomplete", err)
		}
		if !s.shouldEmit(incomplete.SequenceNumber) {
			return nil, nil
		}
		if incomplete.Response.Model != "" {
			s.model = incomplete.Response.Model
		}
		s.done = true
		return []litellm.Event{
			litellm.UsageEvent{Usage: responsesUsageToUsage(incomplete.Response.Usage, s.model)},
			litellm.DoneEvent{FinishReason: litellm.FinishReasonLength, Provider: "openai", Model: s.model},
		}, nil
	case "response.failed":
		var failed struct {
			Response struct {
				Error responsesError `json:"error"`
			} `json:"response"`
			Sequence int `json:"sequence_number,omitempty"`
		}
		if err := json.Unmarshal(raw, &failed); err != nil {
			return nil, responsesStreamParseError("openai: parse responses failed", err)
		}
		if !s.shouldEmit(failed.Sequence) {
			return nil, nil
		}
		return []litellm.Event{litellm.ErrorEvent{Err: litellm.NewProviderError("openai", litellm.ErrorTypeProvider, fmt.Sprintf("openai: response failed: [%s] %s", failed.Response.Error.Code, failed.Response.Error.Message))}}, nil
	case "error":
		var responseErr responsesErrorEvent
		if err := json.Unmarshal(raw, &responseErr); err != nil {
			return nil, responsesStreamParseError("openai: parse responses error", err)
		}
		return []litellm.Event{litellm.ErrorEvent{Err: litellm.NewProviderError("openai", litellm.ErrorTypeProvider, "openai: stream error: "+responseErr.Error.Message)}}, nil
	case "response.created", "response.in_progress", "response.queued",
		"response.output_item.done",
		"response.content_part.added", "response.content_part.done",
		"response.output_text.done",
		"response.reasoning_text.done", "response.reasoning_summary_text.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.file_search_call.in_progress", "response.file_search_call.searching", "response.file_search_call.completed",
		"response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting", "response.code_interpreter_call.completed",
		"response.code_interpreter_call.code.delta", "response.code_interpreter_call.code.done",
		"response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed",
		"response.image_generation_call.in_progress", "response.image_generation_call.generating", "response.image_generation_call.partial_image", "response.image_generation_call.completed",
		"response.mcp_call.in_progress", "response.mcp_call.completed", "response.mcp_call.failed",
		"response.mcp_call_arguments.delta", "response.mcp_call_arguments.done",
		"response.mcp_list_tools.in_progress", "response.mcp_list_tools.completed", "response.mcp_list_tools.failed":
		return []litellm.Event{litellm.ProviderEvent{Name: name, Raw: raw}}, nil
	default:
		if name != "" {
			return []litellm.Event{litellm.ProviderEvent{Name: name, Raw: raw}}, nil
		}
		return nil, litellm.NewProviderError("openai", litellm.ErrorTypeProvider, "openai: responses stream event missing type")
	}
}

func (s *responsesStream) shouldEmit(sequence int) bool {
	if sequence == 0 {
		return true
	}
	if sequence <= s.lastSequence {
		return false
	}
	s.lastSequence = sequence
	return true
}

func (s *responsesStream) toolID(itemID string) string {
	if id := s.toolIDs[itemID]; id != "" {
		return id
	}
	return itemID
}

func responsesUsageToUsage(u responsesUsage, model string) litellm.Usage {
	out := litellm.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
		Provider:     "openai",
		Model:        model,
	}
	if u.InputTokensDetails != nil {
		out.CacheReadTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		out.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return out
}

func toolID(id, callID string) string {
	if callID != "" {
		return callID
	}
	return id
}

func responsesOutputBlocks(items []responsesContentItem) ([]litellm.Block, error) {
	blocks := make([]litellm.Block, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "output_text", "text":
			if item.Text != "" {
				annotations, err := responseAnnotations(item)
				if err != nil {
					return nil, err
				}
				logprobs, err := marshalRaw(item.Logprobs)
				if err != nil {
					return nil, fmt.Errorf("openai: marshal response logprobs: %w", err)
				}
				blocks = append(blocks, litellm.TextBlock{Text: item.Text, Annotations: annotations, Logprobs: logprobs})
			}
		case "image", "image_url", "output_image":
			if item.ImageURL != nil && item.ImageURL.URL != "" {
				blocks = append(blocks, litellm.ImageBlock{URL: item.ImageURL.URL, Detail: item.ImageURL.Detail})
			}
		case "refusal":
			if item.Text != "" {
				blocks = append(blocks, litellm.TextBlock{Text: item.Text})
			}
		default:
			return nil, fmt.Errorf("openai: unsupported responses content item type %q", item.Type)
		}
	}
	return blocks, nil
}

func responseAnnotations(item responsesContentItem) ([]litellm.Annotation, error) {
	if len(item.Annotations) == 0 {
		return nil, nil
	}
	out := make([]litellm.Annotation, 0, len(item.Annotations))
	for _, raw := range item.Annotations {
		extra, err := marshalRaw(raw)
		if err != nil {
			return nil, fmt.Errorf("openai: marshal response annotation: %w", err)
		}
		ann := litellm.Annotation{Extra: extra}
		ann.Type, _ = raw["type"].(string)
		ann.Text, _ = raw["text"].(string)
		ann.URL, _ = raw["url"].(string)
		out = append(out, ann)
	}
	return out, nil
}

func reasoningSummaryText(items []responsesSummaryItem) string {
	var out strings.Builder
	for _, item := range items {
		if item.Text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(item.Text)
	}
	return out.String()
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneMapAny(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneMapAnySlice(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, len(in))
	for i, item := range in {
		out[i] = cloneMapAny(item)
	}
	return out
}

func cloneResponsesStreamOptions(in *ResponsesStreamOptions) *ResponsesStreamOptions {
	if in == nil {
		return nil
	}
	out := *in
	if in.IncludeObfuscation != nil {
		value := *in.IncludeObfuscation
		out.IncludeObfuscation = &value
	}
	return &out
}

func cloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMapAny(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneAny(item)
		}
		return out
	case []string:
		return append([]string(nil), v...)
	case []int:
		return append([]int(nil), v...)
	case []float64:
		return append([]float64(nil), v...)
	case []bool:
		return append([]bool(nil), v...)
	default:
		return value
	}
}
