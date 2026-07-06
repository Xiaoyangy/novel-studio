package litellm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"
)

func validateRequest(req *Request) error {
	if req == nil {
		return NewError(ErrorTypeValidation, "request cannot be nil")
	}
	if req.Model == "" {
		return NewError(ErrorTypeValidation, "model cannot be empty")
	}
	if !utf8.ValidString(req.Model) {
		return NewError(ErrorTypeValidation, "model must be valid UTF-8")
	}
	if len(req.Messages) == 0 {
		return NewError(ErrorTypeValidation, "messages cannot be empty")
	}
	for i, stop := range req.Stop {
		if !utf8.ValidString(stop) {
			return NewError(ErrorTypeValidation, fmt.Sprintf("stop[%d] must be valid UTF-8", i))
		}
	}
	if req.MaxTokens != nil && *req.MaxTokens <= 0 {
		return NewError(ErrorTypeValidation, "max_tokens must be positive")
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return NewError(ErrorTypeValidation, "temperature must be between 0 and 2")
	}
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return NewError(ErrorTypeValidation, "top_p must be between 0 and 1")
	}
	if err := validateCachePolicy(req.Cache); err != nil {
		return err
	}
	if err := validateMessages(req.Messages); err != nil {
		return err
	}
	for _, tool := range req.Tools {
		if tool.Name == "" {
			return NewError(ErrorTypeValidation, "tool name cannot be empty")
		}
		if !utf8.ValidString(tool.Name) {
			return NewError(ErrorTypeValidation, "tool name must be valid UTF-8")
		}
		if !utf8.ValidString(tool.Description) {
			return NewError(ErrorTypeValidation, fmt.Sprintf("tool %q description must be valid UTF-8", tool.Name))
		}
		if len(tool.Parameters) > 0 && !json.Valid(tool.Parameters) {
			return NewError(ErrorTypeValidation, fmt.Sprintf("tool %q parameters must be valid JSON", tool.Name))
		}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == ResponseFormatJSONSchema {
		if req.ResponseFormat.JSONSchema == nil {
			return NewError(ErrorTypeValidation, "json schema response format requires schema")
		}
		if req.ResponseFormat.JSONSchema.Name == "" {
			return NewError(ErrorTypeValidation, "json schema response format requires name")
		}
		if !utf8.ValidString(req.ResponseFormat.JSONSchema.Name) {
			return NewError(ErrorTypeValidation, "json schema response format name must be valid UTF-8")
		}
		if !utf8.ValidString(req.ResponseFormat.JSONSchema.Description) {
			return NewError(ErrorTypeValidation, "json schema response format description must be valid UTF-8")
		}
		if len(req.ResponseFormat.JSONSchema.Schema) > 0 && !json.Valid(req.ResponseFormat.JSONSchema.Schema) {
			return NewError(ErrorTypeValidation, "json schema response format schema must be valid JSON")
		}
	}
	if err := validateThinking(req.Thinking); err != nil {
		return err
	}
	if err := validateAnyUTF8(req.ToolChoice, "tool choice"); err != nil {
		return err
	}
	if err := validateProviderOptionsUTF8(req.ProviderOptions); err != nil {
		return err
	}
	return nil
}

func validateMessages(messages []Message) error {
	seenToolUses := make(map[string]bool)
	openToolUses := make(map[string]bool)
	for i, msg := range messages {
		switch msg.Role {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: invalid role %q", i, msg.Role))
		}
		for _, block := range msg.Blocks {
			switch b := block.(type) {
			case TextBlock:
				if !utf8.ValidString(b.Text) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: text block must be valid UTF-8", i))
				}
				if err := validateCacheControl(i, b.Cache); err != nil {
					return err
				}
			case ImageBlock:
				if err := validateImageUTF8(i, b); err != nil {
					return err
				}
				if err := validateCacheControl(i, b.Cache); err != nil {
					return err
				}
			case ReasoningBlock:
				if msg.Role != RoleAssistant {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: reasoning block requires assistant role", i))
				}
				if !utf8.ValidString(b.Text) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: reasoning block text must be valid UTF-8", i))
				}
				if !utf8.ValidString(b.Signature) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: reasoning block signature must be valid UTF-8", i))
				}
				if err := validateCacheControl(i, b.Cache); err != nil {
					return err
				}
			case ToolReferenceBlock:
				return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool reference block is only valid inside tool result content", i))
			case ToolUseBlock:
				if msg.Role != RoleAssistant {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use block requires assistant role", i))
				}
				if b.ID == "" {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use missing id", i))
				}
				if !validToolUseID(b.ID) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use id %q is invalid", i, b.ID))
				}
				if b.Name == "" {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use %q missing name", i, b.ID))
				}
				if !utf8.ValidString(b.ID) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use id must be valid UTF-8", i))
				}
				if !utf8.ValidString(b.Name) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use %q name must be valid UTF-8", i, b.ID))
				}
				if !utf8.ValidString(b.Signature) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use %q signature must be valid UTF-8", i, b.ID))
				}
				if err := validateCacheControl(i, b.Cache); err != nil {
					return err
				}
				if len(b.Arguments) == 0 || !json.Valid(b.Arguments) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool use %q arguments must be valid JSON", i, b.ID))
				}
				if seenToolUses[b.ID] {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: duplicate tool use id %q", i, b.ID))
				}
				seenToolUses[b.ID] = true
				openToolUses[b.ID] = true
			case ToolResultBlock:
				if msg.Role != RoleTool {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result block requires tool role", i))
				}
				if b.ToolUseID == "" {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result missing tool use id", i))
				}
				if !validToolUseID(b.ToolUseID) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result id %q is invalid", i, b.ToolUseID))
				}
				if !utf8.ValidString(b.ToolUseID) {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result id must be valid UTF-8", i))
				}
				if err := validateCacheControl(i, b.Cache); err != nil {
					return err
				}
				if err := validateToolResultContent(i, b.Content); err != nil {
					return err
				}
				if !seenToolUses[b.ToolUseID] {
					return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result references unknown tool use %q", i, b.ToolUseID))
				}
				delete(openToolUses, b.ToolUseID)
			default:
				return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: unsupported block %T", i, block))
			}
		}
		if msg.Role == RoleUser && len(openToolUses) > 0 {
			return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: user message follows unresolved tool use", i))
		}
	}
	if len(openToolUses) > 0 {
		return NewError(ErrorTypeValidation, "messages: unresolved tool use at end of history")
	}
	return nil
}

func validateCachePolicy(cache *CachePolicy) error {
	if cache == nil {
		return nil
	}
	switch cache.Placement {
	case "", CachePlacementPrefix:
	default:
		return NewError(ErrorTypeValidation, fmt.Sprintf("cache placement %q is not supported", cache.Placement))
	}
	switch cache.Retention {
	case "", "none", "short", "long", CacheTTL5m, CacheTTL1h:
		return nil
	default:
		return NewError(ErrorTypeValidation, fmt.Sprintf("cache retention %q is not supported", cache.Retention))
	}
}

func validateCacheControl(messageIndex int, cache *CacheControl) error {
	if cache == nil {
		return nil
	}
	switch cache.Type {
	case "", CacheTypeEphemeral:
	default:
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: cache type %q is not supported", messageIndex, cache.Type))
	}
	switch cache.TTL {
	case "", CacheTTL5m, CacheTTL1h:
		return nil
	default:
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: cache ttl %q is not supported", messageIndex, cache.TTL))
	}
}

func validateToolResultContent(messageIndex int, blocks []Block) error {
	for _, block := range blocks {
		switch b := block.(type) {
		case TextBlock:
			if !utf8.ValidString(b.Text) {
				return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool result text block must be valid UTF-8", messageIndex))
			}
			if err := validateCacheControl(messageIndex, b.Cache); err != nil {
				return err
			}
		case ImageBlock:
			if err := validateImageUTF8(messageIndex, b); err != nil {
				return err
			}
			if err := validateCacheControl(messageIndex, b.Cache); err != nil {
				return err
			}
		case ToolReferenceBlock:
			if b.ToolName == "" {
				return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool reference missing tool name", messageIndex))
			}
			if !utf8.ValidString(b.ToolName) {
				return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: tool reference name must be valid UTF-8", messageIndex))
			}
			if err := validateCacheControl(messageIndex, b.Cache); err != nil {
				return err
			}
		default:
			return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: unsupported tool result content block %T", messageIndex, block))
		}
	}
	return nil
}

func validateImageUTF8(messageIndex int, block ImageBlock) error {
	if !utf8.ValidString(block.URL) {
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: image URL must be valid UTF-8", messageIndex))
	}
	if !utf8.ValidString(block.MIME) {
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: image MIME must be valid UTF-8", messageIndex))
	}
	if !utf8.ValidString(block.FileURI) {
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: image file URI must be valid UTF-8", messageIndex))
	}
	if !utf8.ValidString(block.Detail) {
		return NewError(ErrorTypeValidation, fmt.Sprintf("messages[%d]: image detail must be valid UTF-8", messageIndex))
	}
	return nil
}

func validateProviderOptionsUTF8(options ProviderOptions) error {
	for key, value := range options {
		if !utf8.ValidString(key) {
			return NewError(ErrorTypeValidation, "provider option key must be valid UTF-8")
		}
		if err := validateAnyUTF8(value, "provider option "+key); err != nil {
			return err
		}
	}
	return nil
}

func validateThinking(thinking *Thinking) error {
	return thinking.Validate()
}

func validateAnyUTF8(value any, path string) error {
	switch v := value.(type) {
	case string:
		if !utf8.ValidString(v) {
			return NewError(ErrorTypeValidation, path+" must be valid UTF-8")
		}
	case []string:
		for i, item := range v {
			if !utf8.ValidString(item) {
				return NewError(ErrorTypeValidation, fmt.Sprintf("%s[%d] must be valid UTF-8", path, i))
			}
		}
	case []any:
		for i, item := range v {
			if err := validateAnyUTF8(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			if !utf8.ValidString(key) {
				return NewError(ErrorTypeValidation, path+" key must be valid UTF-8")
			}
			if err := validateAnyUTF8(item, path+"."+key); err != nil {
				return err
			}
		}
	case ProviderOptions:
		return validateProviderOptionsUTF8(v)
	}
	return nil
}

func validateResponse(resp *Response, provider, model string) error {
	if resp == nil {
		err := NewProviderError(provider, ErrorTypeInternal, "provider returned nil response without error")
		err.Model = model
		return err
	}
	resolvedProvider := resp.Provider
	if resolvedProvider == "" {
		resolvedProvider = provider
	}
	resolvedModel := resp.Model
	if resolvedModel == "" {
		resolvedModel = model
	}
	if resolvedProvider == "" {
		err := NewProviderError(provider, ErrorTypeInternal, "response missing provider")
		err.Model = resolvedModel
		return err
	}
	if resolvedModel == "" {
		err := NewProviderError(resolvedProvider, ErrorTypeInternal, "response missing model")
		err.Model = model
		return err
	}
	for _, block := range resp.Blocks {
		if tool, ok := block.(ToolUseBlock); ok {
			if tool.ID == "" {
				return NewProviderError(resolvedProvider, ErrorTypeValidation, "tool use missing id")
			}
			if tool.Name == "" {
				return NewProviderError(resolvedProvider, ErrorTypeValidation, fmt.Sprintf("tool use %q missing name", tool.ID))
			}
		}
	}
	return nil
}

func finalizeResponse(resp *Response, provider, model string) {
	if resp.Provider == "" {
		resp.Provider = provider
	}
	if resp.Model == "" {
		resp.Model = model
	}
	appendMalformedToolArgumentWarnings(resp)
	resp.Usage.StampModel(resp.Provider, resp.Model)
}

func appendMalformedToolArgumentWarnings(resp *Response) {
	if resp == nil {
		return
	}
	for _, block := range resp.Blocks {
		tool, ok := block.(ToolUseBlock)
		if !ok || len(tool.Arguments) == 0 || json.Valid(tool.Arguments) {
			continue
		}
		var probe any
		parseErr := json.Unmarshal(tool.Arguments, &probe)
		resp.Warnings = append(resp.Warnings, Warning{
			Code:     "tool_arguments_invalid",
			Provider: resp.Provider,
			Message:  fmt.Sprintf("tool use %q returned malformed JSON arguments: %v", tool.ID, parseErr),
		})
	}
}

func cloneRequest(req Request) *Request {
	out := req
	out.MaxTokens = cloneIntPtr(req.MaxTokens)
	out.Temperature = cloneFloat64Ptr(req.Temperature)
	out.TopP = cloneFloat64Ptr(req.TopP)
	out.Messages = cloneMessages(req.Messages)
	out.Stop = append([]string(nil), req.Stop...)
	out.Tools = cloneTools(req.Tools)
	out.ToolChoice = cloneAny(req.ToolChoice)
	out.ResponseFormat = cloneResponseFormat(req.ResponseFormat)
	out.Thinking = cloneThinking(req.Thinking)
	out.Cache = cloneCachePolicy(req.Cache)
	if req.ProviderOptions != nil {
		out.ProviderOptions = make(ProviderOptions, len(req.ProviderOptions))
		for k, v := range req.ProviderOptions {
			out.ProviderOptions[k] = cloneAny(v)
		}
	}
	return &out
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i, msg := range messages {
		out[i] = Message{Role: msg.Role, Blocks: cloneBlocks(msg.Blocks)}
	}
	return out
}

func cloneBlocks(blocks []Block) []Block {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]Block, len(blocks))
	for i, block := range blocks {
		out[i] = cloneBlock(block)
	}
	return out
}

func cloneBlock(block Block) Block {
	switch b := block.(type) {
	case TextBlock:
		b.Logprobs = cloneBytes(b.Logprobs)
		b.Annotations = append([]Annotation(nil), b.Annotations...)
		for i := range b.Annotations {
			b.Annotations[i].Extra = cloneBytes(b.Annotations[i].Extra)
		}
		b.Cache = cloneCacheControl(b.Cache)
		return b
	case ImageBlock:
		b.Data = cloneBytes(b.Data)
		b.Cache = cloneCacheControl(b.Cache)
		return b
	case ReasoningBlock:
		b.Redacted = cloneBytes(b.Redacted)
		b.Extra = cloneBytes(b.Extra)
		b.Cache = cloneCacheControl(b.Cache)
		return b
	case ToolUseBlock:
		b.Arguments = cloneBytes(b.Arguments)
		b.Extra = cloneBytes(b.Extra)
		b.Cache = cloneCacheControl(b.Cache)
		return b
	case ToolResultBlock:
		b.Content = cloneBlocks(b.Content)
		b.Cache = cloneCacheControl(b.Cache)
		return b
	case ToolReferenceBlock:
		b.Extra = cloneBytes(b.Extra)
		b.Cache = cloneCacheControl(b.Cache)
		return b
	default:
		return block
	}
}

func cloneResponse(resp *Response) *Response {
	if resp == nil {
		return nil
	}
	out := *resp
	out.Blocks = cloneBlocks(resp.Blocks)
	out.Warnings = append([]Warning(nil), resp.Warnings...)
	out.Raw = cloneBytes(resp.Raw)
	return &out
}

func cloneAny(v any) any {
	if v == nil {
		return nil
	}
	return cloneValue(reflect.ValueOf(v)).Interface()
}

func cloneValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cloned := cloneValue(v.Elem())
		out := reflect.New(v.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		elem := cloneValue(v.Elem())
		out := reflect.New(v.Type().Elem())
		if elem.IsValid() {
			out.Elem().Set(elem)
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := cloneValue(iter.Key())
			value := cloneValue(iter.Value())
			out.SetMapIndex(key, value)
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i)))
		}
		return out
	default:
		return v
	}
}

func cloneTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		out[i].Parameters = Schema(cloneBytes(tool.Parameters))
	}
	return out
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func cloneResponseFormat(format *ResponseFormat) *ResponseFormat {
	if format == nil {
		return nil
	}
	out := *format
	if format.JSONSchema != nil {
		schema := *format.JSONSchema
		schema.Schema = Schema(cloneBytes(format.JSONSchema.Schema))
		out.JSONSchema = &schema
	}
	return &out
}

func cloneThinking(thinking *Thinking) *Thinking {
	if thinking == nil {
		return nil
	}
	out := *thinking
	out.BudgetTokens = cloneIntPtr(thinking.BudgetTokens)
	return &out
}

func cloneCachePolicy(cache *CachePolicy) *CachePolicy {
	if cache == nil {
		return nil
	}
	out := *cache
	return &out
}

func cloneCacheControl(cache *CacheControl) *CacheControl {
	if cache == nil {
		return nil
	}
	out := *cache
	return &out
}
