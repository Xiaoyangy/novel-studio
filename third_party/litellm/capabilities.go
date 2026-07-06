package litellm

type Support int

const (
	SupportUnknown Support = iota
	SupportNo
	SupportYes
	SupportPartial
)

type CapabilityProvider interface {
	Capabilities(model string) Capabilities
}

type Capabilities struct {
	Provider string
	Model    string

	Thinking   ThinkingCapabilities
	Reasoning  ReasoningCapabilities
	Tools      ToolCapabilities
	Structured StructuredCapabilities
	Media      MediaCapabilities
	Cache      CacheCapabilities
	Streaming  StreamingCapabilities
	Usage      UsageCapabilities
}

type ThinkingCapabilities struct {
	Supported     Support
	Disable       Support
	Efforts       []string
	BudgetTokens  Support
	IncludeOutput Support
	Notes         []string
}

func (c ThinkingCapabilities) SupportsEffort(effort string) bool {
	return containsString(c.Efforts, effort)
}

type ReasoningCapabilities struct {
	Blocks          Support
	StreamingDeltas Support
	ReasoningTokens Support
}

type ToolCapabilities struct {
	Calls               Support
	ParallelCalls       Support
	StrictSchema        Support
	Choice              Support
	MultimodalResults   Support
	RequiresAdjacency   bool
	RoundTripSignatures Support
	HostedProviderTools Support
}

type StructuredCapabilities struct {
	JSONObject Support
	JSONSchema Support
	Strict     Support
	PromptOnly bool
}

type MediaCapabilities struct {
	ImageURL    Support
	ImageBytes  Support
	FileURI     Support
	ImageDetail Support
}

type CacheCapabilities struct {
	Block         Support
	RequestPolicy Support
	PromptKey     Support
	Retention     Support
	UsageRead     Support
	UsageWrite    Support
}

type StreamingCapabilities struct {
	Supported       Support
	Usage           Support
	ReasoningDeltas Support
	ToolCallDeltas  Support
	NativeResponses Support
	IdleTimeout     Support
}

type UsageCapabilities struct {
	InputTokens      Support
	OutputTokens     Support
	TotalTokens      Support
	ReasoningTokens  Support
	CacheReadTokens  Support
	CacheWriteTokens Support
}

func GetCapabilities(provider Provider, model string) Capabilities {
	if provider == nil {
		return Capabilities{Model: model}
	}
	if cp, ok := provider.(CapabilityProvider); ok {
		caps := cp.Capabilities(model)
		if caps.Provider == "" {
			caps.Provider = provider.Name()
		}
		if caps.Model == "" {
			caps.Model = model
		}
		return caps
	}
	return Capabilities{
		Provider: provider.Name(),
		Model:    model,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func PortableThinkingEfforts() []string {
	return []string{"minimal", "low", "medium", "high", "xhigh", "max"}
}
