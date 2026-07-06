package litellm

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type Client struct {
	provider           Provider
	hooks              []Hook
	defaults           *RequestDefaults
	repair             MessageRepairPolicy
	captureRawResponse bool
	streamIdleTimeout  time.Duration
}

type RequestDefaults struct {
	MaxTokens   *int
	Temperature *float64
	TopP        *float64
}

type ClientOption func(*Client) error

func New(provider Provider, opts ...ClientOption) (*Client, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider cannot be nil")
	}
	client := &Client{provider: provider}
	for _, opt := range opts {
		if err := opt(client); err != nil {
			return nil, fmt.Errorf("apply client option: %w", err)
		}
	}
	return client, nil
}

func WithDefaults(defaults RequestDefaults) ClientOption {
	return func(c *Client) error {
		c.defaults = &defaults
		return nil
	}
}

func WithMessageRepair(policies ...MessageRepairPolicy) ClientOption {
	return func(c *Client) error {
		var policy MessageRepairPolicy
		for _, p := range policies {
			policy |= p
		}
		c.repair = policy
		return nil
	}
}

func WithCaptureRawResponse(enabled bool) ClientOption {
	return func(c *Client) error {
		c.captureRawResponse = enabled
		return nil
	}
}

func WithStreamIdleTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) error {
		if timeout < 0 {
			return fmt.Errorf("stream idle timeout cannot be negative")
		}
		c.streamIdleTimeout = timeout
		return nil
	}
}

func (c *Client) ProviderName() string {
	if c == nil || c.provider == nil {
		return ""
	}
	return c.provider.Name()
}

func (c *Client) Capabilities(model string) Capabilities {
	if c == nil {
		return Capabilities{Model: model}
	}
	return GetCapabilities(c.provider, model)
}

func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	prepared, warnings, err := c.prepareRequest(req)
	if err != nil {
		return nil, err
	}
	stampWarnings(warnings, c.ProviderName())
	meta := c.newCallMeta("chat", prepared.Model, false)
	c.notifyBeforeRequest(ctx, meta, prepared)
	start := meta.StartedAt
	resp, err := c.provider.Chat(ctx, prepared)
	if err != nil {
		err = WrapError(err, c.provider.Name())
	}
	if err == nil {
		err = validateResponse(resp, c.provider.Name(), prepared.Model)
	}
	if resp != nil {
		resp.Warnings = append(warnings, resp.Warnings...)
		finalizeResponse(resp, c.provider.Name(), prepared.Model)
	}
	meta.Duration = time.Since(start)
	c.notifyAfterResponse(ctx, meta, resp, err)
	return resp, err
}

func (c *Client) Stream(ctx context.Context, req Request) (Stream, error) {
	prepared, warnings, err := c.prepareRequest(req)
	if err != nil {
		return nil, err
	}
	streamCtx := ctx
	var cancel context.CancelFunc
	if c.streamIdleTimeout > 0 {
		streamCtx, cancel = context.WithCancel(ctx)
	}
	stampWarnings(warnings, c.ProviderName())
	meta := c.newCallMeta("stream", prepared.Model, true)
	c.notifyBeforeRequest(streamCtx, meta, prepared)
	start := meta.StartedAt
	stream, err := c.provider.Stream(streamCtx, prepared)
	if err != nil {
		err = WrapError(err, c.provider.Name())
	}
	meta.Duration = time.Since(start)
	c.notifyAfterResponse(streamCtx, meta, nil, err)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if stream == nil {
		if cancel != nil {
			cancel()
		}
		err := NewProviderError(c.provider.Name(), ErrorTypeInternal, "provider returned nil stream without error")
		return nil, err
	}
	stream = wrapProviderStreamErrors(c.provider.Name(), stream)
	stream = newStreamIdleWatchdog(stream, cancel, c.streamIdleTimeout, c.provider.Name())
	stream = prependWarningEvents(stream, warnings)
	return newHookedStream(streamCtx, meta, c.hooks, stream), nil
}

// StreamText opens a stream for req and invokes fn for each text content delta,
// returning the aggregated Response. It is the simplest way to stream answer
// text to a UI or writer. It creates and closes the stream for you; use
// Client.Stream directly when you need the raw event stream.
func (c *Client) StreamText(ctx context.Context, req Request, fn func(string) error) (resp *Response, err error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stream.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return HandleText(stream, fn)
}

// StreamWith opens a stream for req and dispatches its deltas to handler's
// callbacks, returning the aggregated Response. Use it to stream reasoning and
// answer text separately without a type switch. For full event fidelity, use
// Client.Stream with Handle.
func (c *Client) StreamWith(ctx context.Context, req Request, handler StreamHandler) (resp *Response, err error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stream.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return HandleWith(stream, handler)
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if c == nil || c.provider == nil {
		return nil, NewError(ErrorTypeValidation, "client has no provider")
	}
	lister, ok := c.provider.(ModelLister)
	if !ok {
		return nil, NewProviderError(c.provider.Name(), ErrorTypeValidation, fmt.Sprintf("%s provider does not support model listing", c.provider.Name()))
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, WrapError(err, c.provider.Name())
	}
	return models, nil
}

func (c *Client) prepareRequest(req Request) (*Request, []Warning, error) {
	prepared := cloneRequest(req)
	if c.defaults != nil {
		applyDefaults(prepared, *c.defaults)
	}
	prepared.captureRawResponse = c.captureRawResponse
	warnings, err := repairRequest(prepared, c.repair)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRequest(prepared); err != nil {
		return nil, nil, err
	}
	return prepared, warnings, nil
}

func (c *Client) newCallMeta(operation, model string, streaming bool) CallMeta {
	return CallMeta{
		CallID:    fmt.Sprintf("call_%d", callIDSeq.Add(1)),
		Provider:  c.ProviderName(),
		Operation: operation,
		Model:     model,
		Streaming: streaming,
		StartedAt: time.Now(),
	}
}

var callIDSeq atomic.Uint64

func applyDefaults(req *Request, defaults RequestDefaults) {
	if req.MaxTokens == nil && defaults.MaxTokens != nil {
		req.MaxTokens = IntPtr(*defaults.MaxTokens)
	}
	if req.Temperature == nil && defaults.Temperature != nil {
		req.Temperature = Float64Ptr(*defaults.Temperature)
	}
	if req.TopP == nil && defaults.TopP != nil {
		req.TopP = Float64Ptr(*defaults.TopP)
	}
}
