package compat

import "github.com/voocel/litellm"

// NewClient builds the provider from cfg and spec and wraps it in a ready
// *litellm.Client. It is a convenience for custom OpenAI-compatible endpoints.
// It calls New(cfg, spec) and then litellm.New(provider, opts...).
func NewClient(cfg Config, spec Spec, opts ...litellm.ClientOption) (*litellm.Client, error) {
	p, err := New(cfg, spec)
	if err != nil {
		return nil, err
	}
	return litellm.New(p, opts...)
}
