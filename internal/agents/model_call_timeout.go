package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// modelCallTimeout applies a deadline to one role invocation while preserving
// the underlying model's optional metadata/capability interfaces. It is used
// only by the frozen-render execution path; broad planning/review calls retain
// their own larger budgets.
type modelCallTimeout struct {
	base    agentcore.ChatModel
	role    string
	timeout time.Duration
}

// ModelCallTimeoutError is emitted only when the deadline installed by
// modelCallTimeout itself expires. Provider errors which merely contain words
// such as "timeout", and an earlier parent-context deadline, deliberately do
// not acquire this type. Callers may therefore authorize a narrowly-scoped
// zero-side-effect replacement without relying on error strings.
type ModelCallTimeoutError struct {
	Role    string
	Timeout time.Duration
	Cause   error
}

func (e *ModelCallTimeoutError) Error() string {
	if e == nil {
		return "model call timeout"
	}
	return fmt.Sprintf("model call %s exceeded hard timeout %s: %v", e.Role, e.Timeout, e.Cause)
}

func (e *ModelCallTimeoutError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return context.DeadlineExceeded
	}
	return e.Cause
}

// IsModelCallTimeout reports the typed wrapper above through arbitrary %w
// layers. It intentionally performs no string matching.
func IsModelCallTimeout(err error) bool {
	var timeoutErr *ModelCallTimeoutError
	return errors.As(err, &timeoutErr)
}

func withModelCallTimeout(base agentcore.ChatModel, role string, timeout time.Duration) agentcore.ChatModel {
	if base == nil || timeout <= 0 {
		return base
	}
	return &modelCallTimeout{base: base, role: role, timeout: timeout}
}

func (m *modelCallTimeout) Generate(
	ctx context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	bounded, cancel := context.WithTimeout(nonNilContext(ctx), m.timeout)
	defer cancel()
	started := time.Now()
	resp, err := m.base.Generate(bounded, messages, tools, opts...)
	err = m.classifyTimeout(ctx, bounded, err)
	m.logFinish(started, err)
	return resp, err
}

func (m *modelCallTimeout) GenerateStream(
	ctx context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	bounded, cancel := context.WithTimeout(nonNilContext(ctx), m.timeout)
	started := time.Now()
	source, err := m.base.GenerateStream(bounded, messages, tools, opts...)
	if err != nil {
		err = m.classifyTimeout(ctx, bounded, err)
		cancel()
		m.logFinish(started, err)
		return nil, err
	}
	out := make(chan agentcore.StreamEvent, 16)
	go func() {
		defer close(out)
		defer cancel()
		var finalErr error
		for {
			select {
			case event, ok := <-source:
				if !ok {
					m.logFinish(started, finalErr)
					return
				}
				if event.Type == agentcore.StreamEventError {
					event.Err = m.classifyTimeout(ctx, bounded, event.Err)
					finalErr = event.Err
				}
				out <- event
			case <-bounded.Done():
				finalErr = m.classifyTimeout(ctx, bounded, bounded.Err())
				out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: finalErr}
				m.logFinish(started, finalErr)
				return
			}
		}
	}()
	return out, nil
}

func (m *modelCallTimeout) classifyTimeout(parent, bounded context.Context, err error) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) ||
		bounded == nil || !errors.Is(bounded.Err(), context.DeadlineExceeded) ||
		(parent != nil && parent.Err() != nil) {
		return err
	}
	return &ModelCallTimeoutError{Role: m.role, Timeout: m.timeout, Cause: context.DeadlineExceeded}
}

func (m *modelCallTimeout) SupportsTools() bool { return m.base.SupportsTools() }

func (m *modelCallTimeout) ProviderName() string {
	if provider, ok := m.base.(agentcore.ProviderNamer); ok {
		return provider.ProviderName()
	}
	return ""
}

func (m *modelCallTimeout) Info() llm.ModelInfo {
	if info, ok := m.base.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info()
	}
	return llm.ModelInfo{}
}

func (m *modelCallTimeout) Capabilities() llm.Capabilities {
	if provider, ok := m.base.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}

func (m *modelCallTimeout) logFinish(started time.Time, err error) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	slog.Info("render role model call finished",
		"module", "agent",
		"role", m.role,
		"status", status,
		"elapsed_ms", time.Since(started).Milliseconds(),
		"call_timeout_ms", m.timeout.Milliseconds(),
		"err", err,
	)
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
