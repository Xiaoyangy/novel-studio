package compat

import (
	"context"
	"net/http"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/retry"
)

type Config struct {
	APIKey     string
	APIKeyFunc func(context.Context) (string, error)
	BaseURL    string
	HTTPClient HTTPClient
	Transport  http.RoundTripper
	Retry      *retry.Policy
	UserAgent  string
	Headers    map[string]string

	// AllowUnknownProviderOptions copies unknown request ProviderOptions into
	// the JSON body. The default remains strict; applications that expose an
	// explicit extra_body escape hatch can opt in.
	AllowUnknownProviderOptions bool
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Spec struct {
	Name     string
	Endpoint EndpointSpec
	Auth     AuthSpec
	Headers  HeaderSpec
	Request  RequestSpec
	Response ResponseSpec
	Stream   StreamSpec
	Features FeatureSpec

	Capabilities CapabilityMapper
}

type EndpointSpec struct {
	BaseURL    string
	ChatPath   string
	ModelsPath string
}

type AuthSpec struct {
	APIKeyRequired bool
}

type HeaderSpec struct {
	Extra  map[string]string
	Stream map[string]string
}

type RequestSpec struct {
	MaxTokensField                         string
	OmitStop                               bool
	MaxStopSequences                       int
	EmitEmptyAssistantContentWithToolCalls bool

	SupportsJSONSchema bool
	JSONSchemaToPrompt bool

	Thinking        ThinkingMapper
	ResponseFormat  ResponseFormatMapper
	CleanSchema     SchemaMapper
	ProviderOptions ProviderOptionsMapper
	Warnings        WarningMapper
	Messages        MessageMapper
	Tools           ToolMapper

	AllowUnknownProviderOptions bool
	AllowedProviderOptions      map[string]struct{}
}

type ResponseSpec struct {
	ModelFromResponse         bool
	ContentAsInterface        bool
	ReasoningFields           []string
	HasCompletionTokenDetails bool
	HasCacheTokens            bool
}

type StreamSpec struct {
	DataPrefix string

	ReasoningFields     []string
	ReasoningCondition  string
	ReasoningCumulative bool

	ContentFields              []string
	ContentCumulative          bool
	ContentCumulativeCondition string
	DoneSentinel               string

	OmitStreamOptions bool
}

type FeatureSpec struct {
	StrictTools    StrictToolMode
	APIKeyRequired bool
}

type StrictToolMode int

const (
	StrictToolsOmit StrictToolMode = iota
	StrictToolsForward
	StrictToolsRequireAll
)

type ThinkingMapper func(*litellm.Thinking, string) (map[string]any, error)
type CapabilityMapper func(model string, base litellm.Capabilities) litellm.Capabilities
type ResponseFormatMapper func(*litellm.ResponseFormat) (any, error)
type SchemaMapper func(litellm.Schema) (any, error)
type ProviderOptionsMapper func(litellm.ProviderOptions, map[string]any, *litellm.Request) error
type WarningMapper func(*litellm.Request) []litellm.Warning
type MessageMapper func([]litellm.Message) (any, error)
type ToolMapper func([]litellm.Tool) (any, error)

func (s Spec) providerName() string {
	if s.Name != "" {
		return s.Name
	}
	return "compat"
}

func (s Spec) chatPath() string {
	if s.Endpoint.ChatPath != "" {
		return s.Endpoint.ChatPath
	}
	return "/chat/completions"
}

func (s Spec) modelsPath() string {
	if s.Endpoint.ModelsPath != "" {
		return s.Endpoint.ModelsPath
	}
	return "/models"
}

func (s Spec) maxTokensField() string {
	if s.Request.MaxTokensField != "" {
		return s.Request.MaxTokensField
	}
	return "max_tokens"
}

func (s Spec) dataPrefix() string {
	if s.Stream.DataPrefix != "" {
		return s.Stream.DataPrefix
	}
	return "data: "
}

func (s Spec) doneSentinel() string {
	if s.Stream.DoneSentinel != "" {
		return s.Stream.DoneSentinel
	}
	return "[DONE]"
}

func (s Spec) apiKeyRequired() bool {
	if s.Features.APIKeyRequired {
		return true
	}
	return s.Auth.APIKeyRequired
}
