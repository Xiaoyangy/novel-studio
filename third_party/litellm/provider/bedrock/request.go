package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/litellm"
)

const ProviderOptionCacheRetention = "cache_retention"

func (p *Provider) buildRequest(req *litellm.Request) (*request, error) {
	if len(req.ProviderOptions) > 0 {
		if err := validateProviderOptions(req.ProviderOptions); err != nil {
			return nil, err
		}
	}
	out := &request{}
	if err := convertMessages(out, req.Messages); err != nil {
		return nil, err
	}
	inference := convertInference(req)
	out.InferenceConfig = inference
	if err := applyThinking(out, req); err != nil {
		return nil, err
	}
	output, err := convertOutputConfig(req.ResponseFormat)
	if err != nil {
		return nil, err
	}
	out.OutputConfig = output
	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out.ToolConfig = &toolConfig{Tools: tools, ToolChoice: req.ToolChoice}
	}
	cp, err := cachePointFromRequest(req)
	if err != nil {
		return nil, err
	}
	if cp != nil {
		applyCachePoints(out, cp)
	}
	return out, nil
}

func validateProviderOptions(options litellm.ProviderOptions) error {
	for key, value := range options {
		switch key {
		case ProviderOptionCacheRetention:
			if _, ok := value.(string); !ok {
				return fmt.Errorf("bedrock: provider option %q must be string", key)
			}
		default:
			return fmt.Errorf("bedrock: unsupported provider option %q", key)
		}
	}
	return nil
}

func convertMessages(out *request, messages []litellm.Message) error {
	for i, msg := range messages {
		switch msg.Role {
		case litellm.RoleSystem:
			blocks, err := convertSystemBlocks(msg.Blocks)
			if err != nil {
				return fmt.Errorf("bedrock: messages[%d]: %w", i, err)
			}
			out.System = append(out.System, blocks...)
		case litellm.RoleUser:
			blocks, err := convertContentBlocks(msg.Blocks)
			if err != nil {
				return fmt.Errorf("bedrock: messages[%d]: %w", i, err)
			}
			out.Messages = append(out.Messages, message{Role: "user", Content: blocks})
		case litellm.RoleAssistant:
			blocks, err := convertAssistantBlocks(msg.Blocks)
			if err != nil {
				return fmt.Errorf("bedrock: messages[%d]: %w", i, err)
			}
			out.Messages = append(out.Messages, message{Role: "assistant", Content: blocks})
		case litellm.RoleTool:
			blocks, err := convertToolResultBlocks(msg.Blocks)
			if err != nil {
				return fmt.Errorf("bedrock: messages[%d]: %w", i, err)
			}
			out.Messages = append(out.Messages, message{Role: "user", Content: blocks})
		default:
			return fmt.Errorf("bedrock: unsupported role %q", msg.Role)
		}
	}
	return nil
}

func convertSystemBlocks(blocks []litellm.Block) ([]systemContent, error) {
	out := make([]systemContent, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			out = append(out, systemContent{Text: b.Text})
		default:
			return nil, fmt.Errorf("system only supports text blocks, got %T", block)
		}
	}
	return out, nil
}

func convertContentBlocks(blocks []litellm.Block) ([]content, error) {
	out := make([]content, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			out = append(out, content{Text: b.Text})
		case litellm.ImageBlock:
			image, err := convertImage(b)
			if err != nil {
				return nil, err
			}
			out = append(out, content{Image: image})
		default:
			return nil, fmt.Errorf("unsupported user block %T", block)
		}
	}
	return out, nil
}

func convertAssistantBlocks(blocks []litellm.Block) ([]content, error) {
	out := make([]content, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			out = append(out, content{Text: b.Text})
		case litellm.ReasoningBlock:
			out = append(out, convertReasoningContent(b))
		case litellm.ToolUseBlock:
			var input any = map[string]any{}
			if len(b.Arguments) > 0 {
				if err := json.Unmarshal(b.Arguments, &input); err != nil {
					return nil, fmt.Errorf("tool use %q arguments must be JSON: %w", b.ID, err)
				}
			}
			out = append(out, content{ToolUse: &toolUse{ToolUseID: b.ID, Name: b.Name, Input: input}})
		default:
			return nil, fmt.Errorf("unsupported assistant block %T", block)
		}
	}
	return out, nil
}

func convertReasoningContent(block litellm.ReasoningBlock) content {
	if len(block.Redacted) > 0 {
		return content{ReasoningContent: &reasoningContent{RedactedContent: append([]byte(nil), block.Redacted...)}}
	}
	return content{ReasoningContent: &reasoningContent{ReasoningText: &reasoningText{
		Text:      block.Text,
		Signature: block.Signature,
	}}}
}

func convertToolResultBlocks(blocks []litellm.Block) ([]content, error) {
	out := make([]content, 0, len(blocks))
	for _, block := range blocks {
		result, ok := block.(litellm.ToolResultBlock)
		if !ok {
			return nil, fmt.Errorf("tool role only supports ToolResultBlock, got %T", block)
		}
		children, err := convertToolResultContent(result.Content)
		if err != nil {
			return nil, err
		}
		tr := &toolResult{ToolUseID: result.ToolUseID, Content: children}
		if result.IsError {
			tr.Status = "error"
		}
		out = append(out, content{ToolResult: tr})
	}
	return out, nil
}

func convertToolResultContent(blocks []litellm.Block) ([]content, error) {
	out := make([]content, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			out = append(out, content{Text: b.Text})
		default:
			return nil, fmt.Errorf("Bedrock tool result only supports text blocks, got %T", block)
		}
	}
	return out, nil
}

func convertImage(block litellm.ImageBlock) (*image, error) {
	var format string
	var data []byte
	switch {
	case len(block.Data) > 0:
		if block.MIME == "" {
			return nil, fmt.Errorf("inline image MIME is required")
		}
		format = strings.TrimPrefix(block.MIME, "image/")
		data = block.Data
	case block.URL != "":
		mime, encoded, ok := parseDataURL(block.URL)
		if !ok {
			return nil, fmt.Errorf("Bedrock image URL must be a data URL or inline data")
		}
		format = strings.TrimPrefix(mime, "image/")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("Bedrock image data URL must be base64: %w", err)
		}
		data = decoded
	default:
		return nil, fmt.Errorf("image requires inline data or data URL")
	}
	if format == "" {
		return nil, fmt.Errorf("image MIME must identify a format")
	}
	return &image{Format: format, Source: imageSource{Bytes: base64.StdEncoding.EncodeToString(data)}}, nil
}

func convertInference(req *litellm.Request) *inferenceConfig {
	if req.MaxTokens == nil && req.Temperature == nil && req.TopP == nil && len(req.Stop) == 0 {
		return nil
	}
	out := &inferenceConfig{Temperature: req.Temperature, TopP: req.TopP, StopSequences: append([]string(nil), req.Stop...)}
	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}
	return out
}

func applyThinking(out *request, req *litellm.Request) error {
	if err := req.Thinking.Validate(); err != nil {
		return fmt.Errorf("bedrock: %w", err)
	}
	if req.Thinking == nil || req.Thinking.Mode == litellm.ThinkingUnspecified {
		return nil
	}
	if !strings.Contains(strings.ToLower(req.Model), "claude") {
		return fmt.Errorf("bedrock: thinking is only supported for Claude models")
	}
	thinking, err := anthropicThinking(req.Thinking, req.MaxTokens, req.Temperature)
	if err != nil {
		return err
	}
	if out.AdditionalModelRequestFields == nil {
		out.AdditionalModelRequestFields = map[string]any{}
	}
	out.AdditionalModelRequestFields["thinking"] = thinking
	if req.Thinking.Mode == litellm.ThinkingEnabled {
		if out.InferenceConfig == nil {
			out.InferenceConfig = &inferenceConfig{}
		}
		if req.MaxTokens == nil {
			return fmt.Errorf("bedrock: max_tokens is required when thinking is enabled")
		}
	}
	return nil
}

func anthropicThinking(thinking *litellm.Thinking, maxTokens *int, temperature *float64) (map[string]any, error) {
	if thinking.Mode == litellm.ThinkingDisabled {
		return map[string]any{"type": "disabled"}, nil
	}
	if maxTokens == nil {
		return nil, fmt.Errorf("bedrock: max_tokens is required when thinking is enabled")
	}
	resolvedMax := *maxTokens
	if resolvedMax < 1024 {
		return nil, fmt.Errorf("bedrock: thinking requires max_tokens >= 1024, got %d", resolvedMax)
	}
	if temperature != nil && *temperature != 1 {
		return nil, fmt.Errorf("bedrock: temperature must be 1 when thinking is enabled, got %g", *temperature)
	}
	budget := thinking.BudgetTokens
	if budget == nil && thinking.Effort != "" {
		derived := effortToBudget(thinking.Effort)
		if derived == 0 {
			return nil, fmt.Errorf("bedrock: unknown thinking effort %q", thinking.Effort)
		}
		budget = &derived
	}
	if budget == nil {
		return nil, fmt.Errorf("bedrock: thinking budget_tokens or effort is required")
	}
	if *budget < 1024 {
		return nil, fmt.Errorf("bedrock: thinking budget_tokens must be >= 1024, got %d", *budget)
	}
	if *budget > resolvedMax {
		return nil, fmt.Errorf("bedrock: thinking budget_tokens must be <= max_tokens, got %d > %d", *budget, resolvedMax)
	}
	return map[string]any{"type": "enabled", "budget_tokens": *budget}, nil
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

func convertTools(tools []litellm.Tool) ([]tool, error) {
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		var schema any = map[string]any{"type": "object"}
		if len(t.Parameters) > 0 {
			var decoded any
			if err := json.Unmarshal(t.Parameters, &decoded); err != nil {
				return nil, fmt.Errorf("bedrock: tool %q parameters must be valid JSON: %w", t.Name, err)
			}
			schema = decoded
		}
		var strict *bool
		if t.Strict == litellm.StrictEnabled {
			strict = litellm.Bool(true)
		} else if t.Strict == litellm.StrictDisabled {
			strict = litellm.Bool(false)
		}
		out = append(out, tool{ToolSpec: &toolSpec{
			Name:        t.Name,
			Description: t.Description,
			Strict:      strict,
			InputSchema: map[string]any{"json": schema},
		}})
	}
	return out, nil
}

func convertOutputConfig(format *litellm.ResponseFormat) (*outputConfig, error) {
	if format == nil || format.Type == litellm.ResponseFormatText {
		return nil, nil
	}
	var schema any
	name := ""
	description := ""
	switch format.Type {
	case litellm.ResponseFormatJSONObject:
		name = "json_object"
		description = "Generic JSON object response"
		schema = map[string]any{"type": "object", "additionalProperties": true}
	case litellm.ResponseFormatJSONSchema:
		if format.JSONSchema == nil {
			return nil, fmt.Errorf("bedrock: json schema response format requires schema")
		}
		name = format.JSONSchema.Name
		description = format.JSONSchema.Description
		if err := json.Unmarshal(format.JSONSchema.Schema, &schema); err != nil {
			return nil, fmt.Errorf("bedrock: response schema must be valid JSON: %w", err)
		}
	default:
		return nil, fmt.Errorf("bedrock: unsupported response format %q", format.Type)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal response schema: %w", err)
	}
	return &outputConfig{TextFormat: &textFormat{Type: "json_schema", Structure: textFormatStructure{JSONSchema: jsonSchema{
		Name:        name,
		Description: description,
		Schema:      string(data),
	}}}}, nil
}

func cachePointFromRequest(req *litellm.Request) (*cachePoint, error) {
	retention := ""
	if req.Cache != nil {
		if req.Cache.Placement != "" && req.Cache.Placement != litellm.CachePlacementPrefix {
			return nil, fmt.Errorf("bedrock: unsupported cache placement %q", req.Cache.Placement)
		}
		retention = req.Cache.Retention
	}
	if raw, ok := req.ProviderOptions[ProviderOptionCacheRetention].(string); ok && raw != "" {
		retention = raw
	}
	switch strings.ToLower(strings.TrimSpace(retention)) {
	case "", "none":
		return nil, nil
	case "long", "1h":
		return &cachePoint{Type: "default", TTL: "1h"}, nil
	case "short", "5m":
		return &cachePoint{Type: "default"}, nil
	default:
		return nil, fmt.Errorf("bedrock: unsupported cache retention %q", retention)
	}
}

func applyCachePoints(req *request, cp *cachePoint) {
	if len(req.System) > 0 {
		req.System = append(req.System, systemContent{CachePoint: cp})
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" && len(req.Messages[i].Content) > 0 {
			req.Messages[i].Content = append(req.Messages[i].Content, content{CachePoint: cp})
			break
		}
	}
	if req.ToolConfig != nil && len(req.ToolConfig.Tools) > 0 {
		req.ToolConfig.Tools = append(req.ToolConfig.Tools, tool{CachePoint: cp})
	}
}

func parseDataURL(url string) (mimeType, data string, ok bool) {
	rest, found := strings.CutPrefix(url, "data:")
	if !found {
		return "", "", false
	}
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return "", "", false
	}
	mimeType = rest[:semi]
	if data, ok = strings.CutPrefix(rest[semi+1:], "base64,"); ok {
		return mimeType, data, true
	}
	return "", "", false
}
