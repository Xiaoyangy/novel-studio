package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/voocel/litellm"
)

const thoughtSignaturePlaceholder = "skip_thought_signature_validator"

const (
	ProviderOptionSafetySettings = "safety_settings"
	ProviderOptionTopK           = "top_k"
	ProviderOptionCandidateCount = "candidate_count"
)

func (p *Provider) buildRequest(req *litellm.Request) (*request, error) {
	out := &request{}
	contents, system, err := convertMessages(req.Model, req.Messages)
	if err != nil {
		return nil, err
	}
	out.Contents = contents
	if len(system) > 0 {
		out.SystemInstruction = &content{Parts: system}
	}
	generation, err := convertGenerationConfig(req)
	if err != nil {
		return nil, err
	}
	out.GenerationConfig = generation
	if err := applyProviderOptions(out, req.ProviderOptions); err != nil {
		return nil, err
	}
	if len(req.Tools) > 0 {
		converted, err := convertTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out.Tools = converted
		out.ToolConfig = convertToolChoice(req.ToolChoice)
	}
	return out, nil
}

func applyProviderOptions(out *request, options litellm.ProviderOptions) error {
	for key, value := range options {
		switch key {
		case ProviderOptionSafetySettings:
			settings, err := safetySettings(value)
			if err != nil {
				return err
			}
			out.SafetySettings = settings
		case ProviderOptionTopK:
			topK, err := intOption("gemini", key, value)
			if err != nil {
				return err
			}
			if out.GenerationConfig == nil {
				out.GenerationConfig = &generationConfig{}
			}
			out.GenerationConfig.TopK = &topK
		case ProviderOptionCandidateCount:
			count, err := intOption("gemini", key, value)
			if err != nil {
				return err
			}
			if out.GenerationConfig == nil {
				out.GenerationConfig = &generationConfig{}
			}
			out.GenerationConfig.CandidateCount = &count
		default:
			return fmt.Errorf("gemini: unsupported provider option %q", key)
		}
	}
	return nil
}

func safetySettings(raw any) ([]safetySetting, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal safety settings: %w", err)
	}
	var settings []safetySetting
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("gemini: provider option %q must be safety setting array: %w", ProviderOptionSafetySettings, err)
	}
	for i, setting := range settings {
		if setting.Category == "" || setting.Threshold == "" {
			return nil, fmt.Errorf("gemini: safety_settings[%d] requires category and threshold", i)
		}
	}
	return settings, nil
}

func intOption(provider, key string, value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("%s: provider option %q must be integer", provider, key)
		}
		return int(v), nil
	case json.Number:
		n, err := strconv.Atoi(string(v))
		if err != nil {
			return 0, fmt.Errorf("%s: provider option %q must be integer", provider, key)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s: provider option %q must be integer", provider, key)
	}
}

func convertMessages(model string, messages []litellm.Message) ([]content, []part, error) {
	out := make([]content, 0, len(messages))
	system := make([]part, 0)
	callNames := make(map[string]string)
	for i, msg := range messages {
		switch msg.Role {
		case litellm.RoleSystem:
			parts, err := convertContentBlocks(msg.Blocks)
			if err != nil {
				return nil, nil, fmt.Errorf("gemini: messages[%d]: %w", i, err)
			}
			system = append(system, parts...)
		case litellm.RoleUser:
			parts, err := convertContentBlocks(msg.Blocks)
			if err != nil {
				return nil, nil, fmt.Errorf("gemini: messages[%d]: %w", i, err)
			}
			if len(parts) > 0 {
				out = append(out, content{Role: "user", Parts: parts})
			}
		case litellm.RoleAssistant:
			parts, err := convertAssistantBlocks(model, msg.Blocks, callNames)
			if err != nil {
				return nil, nil, fmt.Errorf("gemini: messages[%d]: %w", i, err)
			}
			if len(parts) > 0 {
				out = append(out, content{Role: "model", Parts: parts})
			}
		case litellm.RoleTool:
			parts, err := convertToolResultBlocks(msg.Blocks, callNames)
			if err != nil {
				return nil, nil, fmt.Errorf("gemini: messages[%d]: %w", i, err)
			}
			if len(parts) > 0 {
				out = append(out, content{Role: "user", Parts: parts})
			}
		default:
			return nil, nil, fmt.Errorf("gemini: unsupported role %q", msg.Role)
		}
	}
	return out, system, nil
}

func convertContentBlocks(blocks []litellm.Block) ([]part, error) {
	out := make([]part, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if b.Text != "" {
				out = append(out, part{Text: b.Text})
			}
		case litellm.ImageBlock:
			converted, err := convertImage(b)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		default:
			return nil, fmt.Errorf("unsupported block %T", block)
		}
	}
	return out, nil
}

func convertAssistantBlocks(model string, blocks []litellm.Block, callNames map[string]string) ([]part, error) {
	out := make([]part, 0, len(blocks))
	callIndex := 0
	for _, block := range blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			if b.Text != "" {
				out = append(out, part{Text: b.Text})
			}
		case litellm.ReasoningBlock:
			if b.Text != "" {
				out = append(out, part{Text: b.Text, Thought: litellm.Bool(true), ThoughtSignature: b.Signature})
			}
		case litellm.ToolUseBlock:
			var args map[string]any
			if err := json.Unmarshal(b.Arguments, &args); err != nil {
				return nil, fmt.Errorf("tool use %q arguments must be JSON object: %w", b.ID, err)
			}
			callNames[b.ID] = b.Name
			converted := part{
				FunctionCall: &functionCall{
					ID:   b.ID,
					Name: b.Name,
					Args: args,
				},
			}
			switch {
			case b.Signature != "":
				converted.ThoughtSignature = b.Signature
			case callIndex == 0 && usesThinkingLevel(model):
				converted.ThoughtSignature = thoughtSignaturePlaceholder
			}
			callIndex++
			out = append(out, converted)
		default:
			return nil, fmt.Errorf("unsupported assistant block %T", block)
		}
	}
	return out, nil
}

func convertToolResultBlocks(blocks []litellm.Block, callNames map[string]string) ([]part, error) {
	out := make([]part, 0, len(blocks))
	for _, block := range blocks {
		result, ok := block.(litellm.ToolResultBlock)
		if !ok {
			return nil, fmt.Errorf("tool role only supports ToolResultBlock, got %T", block)
		}
		name := callNames[result.ToolUseID]
		if name == "" {
			return nil, fmt.Errorf("tool result %q has no preceding tool use name", result.ToolUseID)
		}
		response, err := toolResultObject(result)
		if err != nil {
			return nil, err
		}
		out = append(out, part{FunctionResponse: &functionResponse{
			ID:       result.ToolUseID,
			Name:     name,
			Response: response,
		}})
	}
	return out, nil
}

func toolResultObject(result litellm.ToolResultBlock) (map[string]any, error) {
	if len(result.Content) == 0 {
		if result.IsError {
			return map[string]any{"error": "tool execution failed"}, nil
		}
		return map[string]any{}, nil
	}
	if len(result.Content) != 1 {
		return nil, fmt.Errorf("Gemini tool result only supports a single text block")
	}
	text, ok := result.Content[0].(litellm.TextBlock)
	if !ok {
		return nil, fmt.Errorf("Gemini tool result only supports text content, got %T", result.Content[0])
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(text.Text), &obj); err == nil && obj != nil {
		return obj, nil
	}
	key := "result"
	if result.IsError {
		key = "error"
	}
	return map[string]any{key: text.Text}, nil
}

func convertImage(block litellm.ImageBlock) (part, error) {
	switch {
	case len(block.Data) > 0:
		if block.MIME == "" {
			return part{}, fmt.Errorf("inline image MIME is required")
		}
		return part{InlineData: &inlineData{
			MimeType: block.MIME,
			Data:     base64.StdEncoding.EncodeToString(block.Data),
		}}, nil
	case block.URL != "":
		if mime, data, ok := parseDataURL(block.URL); ok {
			return part{InlineData: &inlineData{MimeType: mime, Data: data}}, nil
		}
		return part{FileData: &fileData{MimeType: inferMimeType(block.URL), FileURI: block.URL}}, nil
	case block.FileURI != "":
		return part{FileData: &fileData{MimeType: block.MIME, FileURI: block.FileURI}}, nil
	default:
		return part{}, fmt.Errorf("image requires URL, data, or file URI")
	}
}

func convertGenerationConfig(req *litellm.Request) (*generationConfig, error) {
	if req.MaxTokens == nil && req.Temperature == nil && req.TopP == nil && len(req.Stop) == 0 && req.ResponseFormat == nil && req.Thinking == nil {
		return nil, nil
	}
	out := &generationConfig{
		Temperature:     req.Temperature,
		MaxOutputTokens: req.MaxTokens,
		TopP:            req.TopP,
		StopSequences:   append([]string(nil), req.Stop...),
	}
	if err := req.Thinking.Validate(); err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	if req.Thinking != nil && req.Thinking.Mode != litellm.ThinkingUnspecified {
		includeThoughts := req.Thinking.Mode == litellm.ThinkingEnabled
		tc := &thinkingConfig{IncludeThoughts: &includeThoughts}
		if includeThoughts {
			effort := thinkingEffort(req.Thinking)
			if usesThinkingLevel(req.Model) {
				if effort != "" {
					tc.ThinkingLevel = effort
				} else {
					tc.ThinkingBudget = req.Thinking.BudgetTokens
				}
			} else {
				tc.ThinkingBudget = req.Thinking.BudgetTokens
				if tc.ThinkingBudget == nil && effort != "" {
					budget := effortToBudget(effort)
					if budget == 0 {
						return nil, fmt.Errorf("gemini: unknown thinking effort %q", effort)
					}
					tc.ThinkingBudget = &budget
				}
			}
		}
		out.ThinkingConfig = tc
	}
	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case litellm.ResponseFormatJSONObject:
			out.ResponseMimeType = "application/json"
		case litellm.ResponseFormatJSONSchema:
			if req.ResponseFormat.JSONSchema == nil {
				return nil, fmt.Errorf("gemini: json schema response format requires schema")
			}
			var schema any
			if err := json.Unmarshal(req.ResponseFormat.JSONSchema.Schema, &schema); err != nil {
				return nil, fmt.Errorf("gemini: response schema must be valid JSON: %w", err)
			}
			out.ResponseMimeType = "application/json"
			out.ResponseSchema = schema
		case litellm.ResponseFormatText:
		default:
			return nil, fmt.Errorf("gemini: unsupported response format %q", req.ResponseFormat.Type)
		}
	}
	return out, nil
}

func convertTools(tools []litellm.Tool) ([]tool, error) {
	out := tool{FunctionDeclarations: make([]functionDeclaration, 0, len(tools))}
	for _, t := range tools {
		if t.Strict == litellm.StrictEnabled {
			return nil, fmt.Errorf("gemini: strict tool calling is not supported")
		}
		var params map[string]any
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &params); err != nil {
				return nil, fmt.Errorf("gemini: tool %q parameters must be object schema: %w", t.Name, err)
			}
		}
		out.FunctionDeclarations = append(out.FunctionDeclarations, functionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return []tool{out}, nil
}

func convertToolChoice(choice any) *toolConfig {
	switch v := choice.(type) {
	case nil:
		return nil
	case string:
		mode := strings.ToUpper(v)
		if mode == "REQUIRED" {
			mode = "ANY"
		}
		if mode == "AUTO" || mode == "ANY" || mode == "NONE" {
			return &toolConfig{FunctionCallingConfig: &functionCallingConfig{Mode: mode}}
		}
	case map[string]any:
		rawType, _ := v["type"].(string)
		switch rawType {
		case "auto":
			return &toolConfig{FunctionCallingConfig: &functionCallingConfig{Mode: "AUTO"}}
		case "any", "required":
			return &toolConfig{FunctionCallingConfig: &functionCallingConfig{Mode: "ANY"}}
		case "none":
			return &toolConfig{FunctionCallingConfig: &functionCallingConfig{Mode: "NONE"}}
		case "function", "tool":
			name, _ := v["name"].(string)
			if name != "" {
				return &toolConfig{FunctionCallingConfig: &functionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{name}}}
			}
		}
	}
	return nil
}

func usesThinkingLevel(model string) bool {
	_, after, ok := strings.Cut(strings.ToLower(model), "gemini-")
	if !ok {
		return false
	}
	var major int
	fmt.Sscanf(after, "%d", &major)
	return major >= 3
}

func thinkingEffort(thinking *litellm.Thinking) string {
	if thinking == nil {
		return ""
	}
	return thinking.Effort
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

func inferMimeType(url string) string {
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	url = strings.ToLower(url)
	switch {
	case strings.HasSuffix(url, ".jpg"), strings.HasSuffix(url, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(url, ".png"):
		return "image/png"
	case strings.HasSuffix(url, ".gif"):
		return "image/gif"
	case strings.HasSuffix(url, ".webp"):
		return "image/webp"
	case strings.HasSuffix(url, ".heic"):
		return "image/heic"
	case strings.HasSuffix(url, ".heif"):
		return "image/heif"
	case strings.HasSuffix(url, ".bmp"):
		return "image/bmp"
	default:
		return ""
	}
}
