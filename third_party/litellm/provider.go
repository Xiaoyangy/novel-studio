package litellm

import "context"

type Provider interface {
	Name() string
	Chat(context.Context, *Request) (*Response, error)
	Stream(context.Context, *Request) (Stream, error)
}

type ModelLister interface {
	ListModels(context.Context) ([]ModelInfo, error)
}

type ModelInfo struct {
	ID               string
	Name             string
	Provider         string
	Description      string
	ContextLength    int
	InputTokenLimit  int
	OutputTokenLimit int
	Created          int64

	SupportsTools    bool
	SupportsVision   bool
	SupportsThinking bool
}
