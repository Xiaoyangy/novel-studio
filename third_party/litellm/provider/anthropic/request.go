package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/litellm"
)

type anthropicRequest struct {
	Model         string             `json:"model"`
	System        any                `json:"system,omitempty"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []anthropicMessage `json:"messages"`
	Stream        bool               `json:"stream,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	Tools         []anthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Thinking      *anthropicThinking `json:"thinking,omitempty"`
	Metadata      map[string]any     `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	Thinking     string                 `json:"thinking,omitempty"`
	Signature    string                 `json:"signature,omitempty"`
	Data         string                 `json:"data,omitempty"`
	ID           string                 `json:"id,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	// Input is any (not map[string]any): with a map type, omitempty drops the
	// field entirely when the map is empty, and Anthropic-compatible endpoints
	// such as MiniMax reject tool_use blocks without an input field (error 2013).
	// An interface is only omitted when nil, so an empty map still serializes
	// as "input": {}. Non-tool blocks never set Input and stay omitted.
	Input        any                    `json:"input,omitempty"`
	Content      any                    `json:"content,omitempty"`
	ToolName     string                 `json:"tool_name,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Strict      *bool          `json:"strict,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

const (
	ProviderOptionMetadata       = "metadata"
	ProviderOptionMetadataUserID = "metadata_user_id"
)

func (p *Provider) buildRequest(req *litellm.Request, stream bool) (*anthropicRequest, error) {
	if req.MaxTokens == nil {
		return nil, fmt.Errorf("anthropic: max_tokens is required")
	}
	if req.Temperature != nil && req.TopP != nil {
		return nil, fmt.Errorf("anthropic: temperature and top_p cannot both be set")
	}
	metadata, err := anthropicMetadata(req.ProviderOptions)
	if err != nil {
		return nil, err
	}
	out := &anthropicRequest{
		Model:         req.Model,
		MaxTokens:     *req.MaxTokens,
		Stream:        stream,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: append([]string(nil), req.Stop...),
		ToolChoice:    req.ToolChoice,
		Metadata:      metadata,
	}
	thinking, err := convertThinking(req.Thinking, *req.MaxTokens, req.Temperature, req.TopP, req.ToolChoice)
	if err != nil {
		return nil, err
	}
	out.Thinking = thinking
	if len(req.Tools) > 0 {
		out.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			converted, err := convertTool(tool)
			if err != nil {
				return nil, err
			}
			out.Tools = append(out.Tools, converted)
		}
	}
	system, messages, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	out.System = system
	out.Messages = messages
	return out, nil
}

func anthropicMetadata(options litellm.ProviderOptions) (map[string]any, error) {
	if len(options) == 0 {
		return nil, nil
	}
	for key := range options {
		switch key {
		case ProviderOptionMetadata, ProviderOptionMetadataUserID:
		default:
			return nil, fmt.Errorf("anthropic: unsupported provider option %q", key)
		}
	}
	var metadata map[string]any
	if raw, ok := options[ProviderOptionMetadata]; ok && raw != nil {
		switch value := raw.(type) {
		case map[string]any:
			metadata = make(map[string]any, len(value))
			for k, v := range value {
				if k == "" {
					return nil, fmt.Errorf("anthropic: metadata key cannot be empty")
				}
				metadata[k] = v
			}
		case map[string]string:
			metadata = make(map[string]any, len(value))
			for k, v := range value {
				if k == "" {
					return nil, fmt.Errorf("anthropic: metadata key cannot be empty")
				}
				metadata[k] = v
			}
		default:
			return nil, fmt.Errorf("anthropic: provider option %q must be object", "metadata")
		}
	}
	if raw, ok := options[ProviderOptionMetadataUserID]; ok && raw != nil {
		userID, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("anthropic: provider option %q must be string", "metadata_user_id")
		}
		if userID != "" {
			if metadata == nil {
				metadata = map[string]any{}
			}
			metadata["user_id"] = userID
		}
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	return metadata, nil
}

func convertThinking(thinking *litellm.Thinking, maxTokens int, temperature, topP *float64, toolChoice any) (*anthropicThinking, error) {
	if err := thinking.Validate(); err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if thinking == nil || thinking.Mode == litellm.ThinkingUnspecified {
		return nil, nil
	}
	if thinking.Mode == litellm.ThinkingDisabled {
		return &anthropicThinking{Type: "disabled"}, nil
	}
	if maxTokens < 1024 {
		return nil, fmt.Errorf("anthropic: thinking requires max_tokens >= 1024, got %d", maxTokens)
	}
	if temperature != nil && *temperature != 1 {
		return nil, fmt.Errorf("anthropic: temperature must be 1 when thinking is enabled, got %g", *temperature)
	}
	if topP != nil && (*topP < 0.95 || *topP > 1) {
		return nil, fmt.Errorf("anthropic: top_p must be between 0.95 and 1 when thinking is enabled, got %g", *topP)
	}
	if typ, ok := forcedThinkingToolChoice(toolChoice); ok {
		return nil, fmt.Errorf("anthropic: tool_choice %q is not supported when thinking is enabled", typ)
	}
	budget := thinking.BudgetTokens
	if budget == nil {
		if strings.TrimSpace(thinking.Effort) != "" {
			derived := effortToBudget(thinking.Effort)
			if derived == 0 {
				return nil, fmt.Errorf("anthropic: unknown thinking effort %q", thinking.Effort)
			}
			budget = &derived
		}
	}
	if budget == nil {
		return nil, fmt.Errorf("anthropic: thinking budget_tokens or effort is required")
	}
	if *budget < 1024 {
		return nil, fmt.Errorf("anthropic: thinking budget_tokens must be >= 1024, got %d", *budget)
	}
	if *budget >= maxTokens {
		return nil, fmt.Errorf("anthropic: thinking budget_tokens must be < max_tokens, got %d >= %d", *budget, maxTokens)
	}
	return &anthropicThinking{Type: "enabled", BudgetTokens: budget}, nil
}

func forcedThinkingToolChoice(choice any) (string, bool) {
	typ := toolChoiceType(choice)
	switch typ {
	case "", "auto", "none":
		return "", false
	default:
		return typ, true
	}
}

func toolChoiceType(choice any) string {
	switch v := choice.(type) {
	case nil:
		return ""
	case string:
		return v
	case map[string]any:
		typ, _ := v["type"].(string)
		return typ
	case map[string]string:
		return v["type"]
	case json.RawMessage:
		var decoded struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(v, &decoded); err == nil {
			return decoded.Type
		}
	}
	data, err := json.Marshal(choice)
	if err != nil {
		return ""
	}
	var decoded struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	return decoded.Type
}

func effortToBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal":
		return 1024
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	case "xhigh", "max":
		return 32768
	default:
		return 0
	}
}

func convertTool(tool litellm.Tool) (anthropicTool, error) {
	if tool.Name == "" {
		return anthropicTool{}, fmt.Errorf("anthropic: tool name is required")
	}
	var schema map[string]any
	if len(tool.Parameters) == 0 {
		schema = map[string]any{"type": "object"}
	} else if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		return anthropicTool{}, fmt.Errorf("anthropic: tool %q parameters must be object schema: %w", tool.Name, err)
	}
	out := anthropicTool{Name: tool.Name, Description: tool.Description, InputSchema: schema}
	switch tool.Strict {
	case litellm.StrictEnabled:
		out.Strict = litellm.Bool(true)
	case litellm.StrictDisabled:
		out.Strict = litellm.Bool(false)
	}
	return out, nil
}

func convertMessages(messages []litellm.Message) (any, []anthropicMessage, error) {
	var system []anthropicContent
	out := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		content, err := convertBlocks(msg.Blocks)
		if err != nil {
			return nil, nil, err
		}
		switch msg.Role {
		case litellm.RoleSystem:
			system = append(system, content...)
		case litellm.RoleAssistant:
			out = append(out, anthropicMessage{Role: "assistant", Content: content})
		case litellm.RoleUser:
			out = append(out, anthropicMessage{Role: "user", Content: content})
		case litellm.RoleTool:
			out = append(out, anthropicMessage{Role: "user", Content: content})
		default:
			return nil, nil, fmt.Errorf("anthropic: unsupported role %q", msg.Role)
		}
	}
	out = mergeAssistantMessages(out)
	if err := validateCacheOrder(system, out); err != nil {
		return nil, nil, err
	}
	return systemValue(system), out, nil
}

func validateCacheOrder(system []anthropicContent, messages []anthropicMessage) error {
	seenShort := false
	check := func(block anthropicContent) error {
		if block.CacheControl != nil {
			switch block.CacheControl.TTL {
			case litellm.CacheTTL1h:
				if seenShort {
					return fmt.Errorf("anthropic: 1h cache_control must appear before 5m cache_control")
				}
			case "", litellm.CacheTTL5m:
				seenShort = true
			}
		}
		return nil
	}
	var walk func([]anthropicContent) error
	walk = func(blocks []anthropicContent) error {
		for _, block := range blocks {
			if err := check(block); err != nil {
				return err
			}
			if nested, ok := block.Content.([]anthropicContent); ok {
				if err := walk(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(system); err != nil {
		return err
	}
	for _, msg := range messages {
		if err := walk(msg.Content); err != nil {
			return err
		}
	}
	return nil
}

func systemValue(system []anthropicContent) any {
	if len(system) == 0 {
		return nil
	}
	if len(system) == 1 && system[0].Type == "text" && system[0].CacheControl == nil {
		return system[0].Text
	}
	return system
}

func mergeAssistantMessages(messages []anthropicMessage) []anthropicMessage {
	if len(messages) <= 1 {
		return messages
	}
	out := make([]anthropicMessage, 0, len(messages))
	out = append(out, messages[0])
	for i := 1; i < len(messages); i++ {
		last := &out[len(out)-1]
		if last.Role == "assistant" && messages[i].Role == "assistant" {
			last.Content = append(last.Content, messages[i].Content...)
			continue
		}
		out = append(out, messages[i])
	}
	return out
}

func convertBlocks(blocks []litellm.Block) ([]anthropicContent, error) {
	out := make([]anthropicContent, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropicContent{Type: "text", Text: b.Text, CacheControl: cache})
		case litellm.ImageBlock:
			source, err := imageSource(b)
			if err != nil {
				return nil, err
			}
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropicContent{Type: "image", Source: source, CacheControl: cache})
		case litellm.ReasoningBlock:
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			if len(b.Redacted) > 0 {
				out = append(out, anthropicContent{Type: "redacted_thinking", Data: string(b.Redacted), CacheControl: cache})
			} else {
				out = append(out, anthropicContent{Type: "thinking", Thinking: b.Text, Signature: b.Signature, CacheControl: cache})
			}
		case litellm.ToolUseBlock:
			var input map[string]any
			if len(b.Arguments) > 0 {
				if err := json.Unmarshal(b.Arguments, &input); err != nil {
					return nil, fmt.Errorf("anthropic: tool use %q arguments must be object: %w", b.ID, err)
				}
			}
			if input == nil {
				input = map[string]any{}
			}
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropicContent{Type: "tool_use", ID: b.ID, Name: b.Name, Input: input, CacheControl: cache})
		case litellm.ToolResultBlock:
			content, err := convertToolResultContent(b.Content)
			if err != nil {
				return nil, err
			}
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropicContent{Type: "tool_result", ToolUseID: b.ToolUseID, Content: content, IsError: b.IsError, CacheControl: cache})
		case litellm.ToolReferenceBlock:
			cache, err := cacheControl(b.Cache)
			if err != nil {
				return nil, err
			}
			out = append(out, anthropicContent{Type: "tool_reference", ToolName: b.ToolName, CacheControl: cache})
		default:
			return nil, fmt.Errorf("anthropic: unsupported block %T", block)
		}
	}
	return out, nil
}

func convertToolResultContent(blocks []litellm.Block) (any, error) {
	if len(blocks) == 1 {
		if text, ok := blocks[0].(litellm.TextBlock); ok && text.Cache == nil {
			return text.Text, nil
		}
	}
	return convertBlocks(blocks)
}
