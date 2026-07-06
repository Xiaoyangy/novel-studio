package litellm

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ProviderFactory func(any) (Provider, error)

type Registry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]ProviderFactory)}
}

func (r *Registry) Register(name string, factory ProviderFactory) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	name = normalizeProviderName(name)
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("provider factory cannot be nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[name]; ok {
		return fmt.Errorf("provider %q is already registered", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *Registry) New(name string, config any) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	name = normalizeProviderName(name)
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	provider, err := factory(config)
	if err != nil {
		return nil, fmt.Errorf("%s provider: %w", name, err)
	}
	if provider == nil {
		return nil, fmt.Errorf("%s provider factory returned nil", name)
	}
	return provider, nil
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TypedFactory[T any](fn func(T) (Provider, error)) ProviderFactory {
	return func(config any) (Provider, error) {
		typed, ok := config.(T)
		if !ok {
			return nil, fmt.Errorf("invalid config type %T", config)
		}
		return fn(typed)
	}
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
