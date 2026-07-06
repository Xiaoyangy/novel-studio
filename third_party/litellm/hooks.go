package litellm

import (
	"context"
	"time"
)

type CallMeta struct {
	CallID    string
	Provider  string
	Operation string
	Model     string
	Streaming bool
	StartedAt time.Time
	Duration  time.Duration
}

type Hook interface {
	BeforeRequest(context.Context, CallMeta, *Request)
	AfterResponse(context.Context, CallMeta, *Response, error)
	OnStreamEvent(context.Context, CallMeta, Event)
	OnStreamEnd(context.Context, CallMeta, error)
	OnWarning(context.Context, CallMeta, Warning)
}

type HookFuncs struct {
	BeforeRequestFunc func(context.Context, CallMeta, *Request)
	AfterResponseFunc func(context.Context, CallMeta, *Response, error)
	OnStreamEventFunc func(context.Context, CallMeta, Event)
	OnStreamEndFunc   func(context.Context, CallMeta, error)
	OnWarningFunc     func(context.Context, CallMeta, Warning)
}

func (h HookFuncs) BeforeRequest(ctx context.Context, meta CallMeta, req *Request) {
	if h.BeforeRequestFunc != nil {
		h.BeforeRequestFunc(ctx, meta, req)
	}
}

func (h HookFuncs) AfterResponse(ctx context.Context, meta CallMeta, resp *Response, err error) {
	if h.AfterResponseFunc != nil {
		h.AfterResponseFunc(ctx, meta, resp, err)
	}
}

func (h HookFuncs) OnStreamEvent(ctx context.Context, meta CallMeta, event Event) {
	if h.OnStreamEventFunc != nil {
		h.OnStreamEventFunc(ctx, meta, event)
	}
}

func (h HookFuncs) OnStreamEnd(ctx context.Context, meta CallMeta, err error) {
	if h.OnStreamEndFunc != nil {
		h.OnStreamEndFunc(ctx, meta, err)
	}
}

func (h HookFuncs) OnWarning(ctx context.Context, meta CallMeta, warning Warning) {
	if h.OnWarningFunc != nil {
		h.OnWarningFunc(ctx, meta, warning)
	}
}

func WithHook(h Hook) ClientOption {
	return func(c *Client) error {
		if h != nil {
			c.hooks = append(c.hooks, h)
		}
		return nil
	}
}

func WithHooks(hooks ...Hook) ClientOption {
	return func(c *Client) error {
		for _, h := range hooks {
			if h != nil {
				c.hooks = append(c.hooks, h)
			}
		}
		return nil
	}
}

func (c *Client) notifyBeforeRequest(ctx context.Context, meta CallMeta, req *Request) {
	for _, hook := range c.hooks {
		hook.BeforeRequest(ctx, meta, cloneRequest(*req))
	}
}

func (c *Client) notifyAfterResponse(ctx context.Context, meta CallMeta, resp *Response, err error) {
	for _, hook := range c.hooks {
		hook.AfterResponse(ctx, meta, cloneResponse(resp), err)
	}
	if resp != nil {
		for _, warning := range resp.Warnings {
			for _, hook := range c.hooks {
				hook.OnWarning(ctx, meta, warning)
			}
		}
	}
}

type hookedStream struct {
	ctx    context.Context
	meta   CallMeta
	hooks  []Hook
	inner  Stream
	closed bool
}

func newHookedStream(ctx context.Context, meta CallMeta, hooks []Hook, inner Stream) Stream {
	if len(hooks) == 0 || inner == nil {
		return inner
	}
	return &hookedStream{ctx: ctx, meta: meta, hooks: hooks, inner: inner}
}

func (s *hookedStream) Next() (Event, error) {
	event, err := s.inner.Next()
	if event != nil {
		for _, hook := range s.hooks {
			hookEvent := cloneEvent(event)
			hook.OnStreamEvent(s.ctx, s.meta, hookEvent)
			if warningEvent, ok := event.(WarningEvent); ok {
				hook.OnWarning(s.ctx, s.meta, warningEvent.Warning)
			}
		}
	}
	if err != nil {
		s.finish(err)
		return nil, err
	}
	if _, ok := event.(DoneEvent); ok {
		s.finish(nil)
	}
	return event, nil
}

func (s *hookedStream) Close() error {
	err := s.inner.Close()
	s.finish(err)
	return err
}

func (s *hookedStream) finish(err error) {
	if s.closed {
		return
	}
	s.closed = true
	for _, hook := range s.hooks {
		hook.OnStreamEnd(s.ctx, s.meta, err)
	}
}
