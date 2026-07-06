package litellm

import (
	"context"
	"testing"
)

type registryConfig struct {
	Name string
}

type registryProvider struct {
	name string
}

func (p *registryProvider) Name() string { return p.name }
func (p *registryProvider) Chat(context.Context, *Request) (*Response, error) {
	return &Response{}, nil
}
func (p *registryProvider) Stream(context.Context, *Request) (Stream, error) {
	return nil, nil
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register("x", TypedFactory(func(cfg registryConfig) (Provider, error) {
		return &registryProvider{name: cfg.Name}, nil
	}))
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	provider, err := reg.New("x", registryConfig{Name: "provider-x"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if provider.Name() != "provider-x" {
		t.Fatalf("provider name = %q", provider.Name())
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "x" {
		t.Fatalf("names = %#v", names)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	reg := NewRegistry()
	factory := func(any) (Provider, error) {
		return &registryProvider{name: "x"}, nil
	}
	if err := reg.Register("x", factory); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := reg.Register("x", factory); err == nil {
		t.Fatal("expected duplicate register error")
	}
}
