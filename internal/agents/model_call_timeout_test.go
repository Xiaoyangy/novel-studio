package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

type deadlineProbeModel struct {
	remaining time.Duration
}

type neverCompletesStreamModel struct{}

func (*neverCompletesStreamModel) Generate(
	ctx context.Context,
	_ []agentcore.Message,
	_ []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*neverCompletesStreamModel) GenerateStream(
	context.Context,
	[]agentcore.Message,
	[]agentcore.ToolSpec,
	...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	return make(chan agentcore.StreamEvent), nil
}

func (*neverCompletesStreamModel) SupportsTools() bool { return true }

type immediateDeadlineTextModel struct{ err error }

func (m *immediateDeadlineTextModel) Generate(
	context.Context,
	[]agentcore.Message,
	[]agentcore.ToolSpec,
	...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	return nil, m.err
}

func (m *immediateDeadlineTextModel) GenerateStream(
	context.Context,
	[]agentcore.Message,
	[]agentcore.ToolSpec,
	...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	return nil, m.err
}

func (*immediateDeadlineTextModel) SupportsTools() bool { return true }

func (m *deadlineProbeModel) Generate(
	ctx context.Context,
	_ []agentcore.Message,
	_ []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		m.remaining = 0
	} else {
		m.remaining = time.Until(deadline)
	}
	return &agentcore.LLMResponse{}, nil
}

func (m *deadlineProbeModel) GenerateStream(
	ctx context.Context,
	_ []agentcore.Message,
	_ []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	_, err := m.Generate(ctx, nil, nil)
	out := make(chan agentcore.StreamEvent)
	close(out)
	return out, err
}

func (*deadlineProbeModel) SupportsTools() bool { return true }

func TestModelCallTimeoutBoundsRenderInvocationAndRespectsEarlierParent(t *testing.T) {
	probe := &deadlineProbeModel{}
	bounded := withModelCallTimeout(probe, "drafter", 4*time.Minute)
	if _, err := bounded.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if probe.remaining <= 0 || probe.remaining > 4*time.Minute {
		t.Fatalf("render call missing four-minute deadline: %s", probe.remaining)
	}

	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := bounded.Generate(parent, nil, nil); err != nil {
		t.Fatal(err)
	}
	if probe.remaining <= 0 || probe.remaining > 50*time.Millisecond {
		t.Fatalf("earlier parent deadline was widened: %s", probe.remaining)
	}
}

func TestModelCallTimeoutUsesTypedErrorOnlyForItsOwnDeadline(t *testing.T) {
	bounded := withModelCallTimeout(&neverCompletesStreamModel{}, "planner", 15*time.Millisecond)
	stream, err := bounded.GenerateStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for event := range stream {
		if event.Type == agentcore.StreamEventError {
			streamErr = event.Err
		}
	}
	if !IsModelCallTimeout(streamErr) || !errors.Is(streamErr, context.DeadlineExceeded) {
		t.Fatalf("own hard deadline did not retain typed/context identity: %T %v", streamErr, streamErr)
	}

	parent, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	parentBounded := withModelCallTimeout(&neverCompletesStreamModel{}, "planner", time.Second)
	stream, err = parentBounded.GenerateStream(parent, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	streamErr = nil
	for event := range stream {
		if event.Type == agentcore.StreamEventError {
			streamErr = event.Err
		}
	}
	if !errors.Is(streamErr, context.DeadlineExceeded) || IsModelCallTimeout(streamErr) {
		t.Fatalf("earlier parent deadline was misclassified as model-call timeout: %T %v", streamErr, streamErr)
	}

	providerErr := errors.New("provider said model call timeout but returned immediately")
	textOnly := withModelCallTimeout(&immediateDeadlineTextModel{err: providerErr}, "planner", time.Second)
	if _, err := textOnly.Generate(context.Background(), nil, nil); !errors.Is(err, providerErr) || IsModelCallTimeout(err) {
		t.Fatalf("timeout-shaped provider text acquired typed authority: %T %v", err, err)
	}
}
