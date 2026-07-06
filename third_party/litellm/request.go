package litellm

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Block interface {
	isBlock()
}

type Annotation struct {
	Type  string
	Text  string
	URL   string
	Extra json.RawMessage
}

type TextBlock struct {
	Text        string
	Annotations []Annotation
	Logprobs    json.RawMessage
	Cache       *CacheControl
}

type ImageBlock struct {
	URL     string
	Data    []byte
	MIME    string
	FileURI string
	Detail  string
	Cache   *CacheControl
}

type ReasoningBlock struct {
	Text      string
	Summary   bool
	Signature string
	Redacted  []byte
	Extra     json.RawMessage
	Cache     *CacheControl
}

type ToolUseBlock struct {
	ID        string
	Name      string
	Arguments json.RawMessage
	Signature string
	Extra     json.RawMessage
	Cache     *CacheControl
}

type ToolResultBlock struct {
	ToolUseID string
	Content   []Block
	IsError   bool
	Cache     *CacheControl
}

type ToolReferenceBlock struct {
	ToolName string
	Extra    json.RawMessage
	Cache    *CacheControl
}

func (TextBlock) isBlock()          {}
func (ImageBlock) isBlock()         {}
func (ReasoningBlock) isBlock()     {}
func (ToolUseBlock) isBlock()       {}
func (ToolResultBlock) isBlock()    {}
func (ToolReferenceBlock) isBlock() {}

type Message struct {
	Role   Role
	Blocks []Block
}

type CacheControl struct {
	Type string
	TTL  string
}

const (
	CacheTypeEphemeral = "ephemeral"
	CacheTTL5m         = "5m"
	CacheTTL1h         = "1h"
)

type Schema json.RawMessage

func SchemaFrom(v any) (Schema, error) {
	switch s := v.(type) {
	case nil:
		return nil, nil
	case Schema:
		return cloneBytes([]byte(s)), nil
	case json.RawMessage:
		if !json.Valid(s) {
			return nil, fmt.Errorf("schema must be valid JSON")
		}
		return Schema(cloneBytes(s)), nil
	case []byte:
		if !json.Valid(s) {
			return nil, fmt.Errorf("schema must be valid JSON")
		}
		return Schema(cloneBytes(s)), nil
	case string:
		b := []byte(s)
		if !json.Valid(b) {
			return nil, fmt.Errorf("schema must be valid JSON")
		}
		return Schema(cloneBytes(b)), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal schema: %w", err)
		}
		if !json.Valid(b) {
			return nil, fmt.Errorf("schema must be valid JSON")
		}
		return Schema(b), nil
	}
}

type StrictMode int

const (
	StrictDefault StrictMode = iota
	StrictEnabled
	StrictDisabled
)

type Tool struct {
	Name        string
	Description string
	Parameters  Schema
	Strict      StrictMode
}

func NewTool(name, description string, parameters any) (Tool, error) {
	schema, err := SchemaFrom(parameters)
	if err != nil {
		return Tool{}, err
	}
	return Tool{Name: name, Description: description, Parameters: schema}, nil
}

type ToolChoice any

type ResponseFormat struct {
	Type       ResponseFormatType
	JSONSchema *JSONSchema
}

type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONObject ResponseFormatType = "json_object"
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

type JSONSchema struct {
	Name        string
	Description string
	Schema      Schema
	Strict      StrictMode
}

type Thinking struct {
	Mode          ThinkingMode
	Effort        string
	BudgetTokens  *int
	IncludeOutput bool
}

func (t *Thinking) HasOptions() bool {
	return t != nil && (t.Effort != "" || t.BudgetTokens != nil || t.IncludeOutput)
}

func (t *Thinking) Validate() error {
	if t == nil {
		return nil
	}
	if !utf8.ValidString(t.Effort) {
		return NewError(ErrorTypeValidation, "thinking effort must be valid UTF-8")
	}
	if t.Mode == ThinkingUnspecified && t.HasOptions() {
		return NewError(ErrorTypeValidation, "thinking mode must be enabled or disabled when thinking options are set")
	}
	if t.Mode == ThinkingDisabled && t.HasOptions() {
		return NewError(ErrorTypeValidation, "thinking options cannot be set when thinking is disabled")
	}
	return nil
}

type ThinkingMode int

const (
	ThinkingUnspecified ThinkingMode = iota
	ThinkingDisabled
	ThinkingEnabled
)

type CachePolicy struct {
	Retention string
	Placement CachePlacement
}

type CachePlacement string

const (
	CachePlacementPrefix CachePlacement = "prefix"
)

type ProviderOptions map[string]any

type Request struct {
	Model    string
	Messages []Message

	MaxTokens   *int
	Temperature *float64
	TopP        *float64
	Stop        []string

	Tools      []Tool
	ToolChoice ToolChoice

	ResponseFormat *ResponseFormat
	Thinking       *Thinking
	Cache          *CachePolicy

	ProviderOptions ProviderOptions

	captureRawResponse bool
}

func (r *Request) CaptureRawResponse() bool {
	return r != nil && r.captureRawResponse
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
