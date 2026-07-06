package litellm

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var ErrStreamIdle = errors.New("stream idle timeout")

func IsStreamIdleError(err error) bool {
	return errors.Is(err, ErrStreamIdle)
}

type streamIdleWatchdog struct {
	inner    Stream
	cancel   context.CancelFunc
	timeout  time.Duration
	provider string

	timer   *time.Timer
	aborted atomic.Bool
	stopped atomic.Bool
}

func newStreamIdleWatchdog(inner Stream, cancel context.CancelFunc, timeout time.Duration, provider string) Stream {
	if inner == nil || timeout <= 0 {
		return inner
	}
	w := &streamIdleWatchdog{
		inner:    inner,
		cancel:   cancel,
		timeout:  timeout,
		provider: provider,
	}
	w.timer = time.AfterFunc(timeout, w.fire)
	return w
}

// WithStreamIdleWatchdog wraps inner with a per-event idle timeout. It returns
// inner unchanged when timeout <= 0 or inner is nil.
func WithStreamIdleWatchdog(inner Stream, cancel context.CancelFunc, timeout time.Duration, provider string) Stream {
	return newStreamIdleWatchdog(inner, cancel, timeout, provider)
}

func (w *streamIdleWatchdog) Next() (Event, error) {
	event, err := w.inner.Next()
	if err != nil {
		if w.aborted.Load() {
			return nil, w.idleError()
		}
		w.stop()
		return nil, err
	}
	if _, ok := event.(DoneEvent); ok {
		w.stop()
	} else {
		w.reset()
	}
	return event, nil
}

func (w *streamIdleWatchdog) Close() error {
	w.stop()
	return w.inner.Close()
}

func (w *streamIdleWatchdog) fire() {
	if w.stopped.Load() {
		return
	}
	if !w.aborted.CompareAndSwap(false, true) {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *streamIdleWatchdog) reset() {
	if w.aborted.Load() || w.stopped.Load() || w.timer == nil {
		return
	}
	w.timer.Reset(w.timeout)
}

func (w *streamIdleWatchdog) stop() {
	if !w.stopped.CompareAndSwap(false, true) {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	if w.cancel != nil {
		w.cancel()
	}
}

func (w *streamIdleWatchdog) idleError() error {
	return &LiteLLMError{
		Type:      ErrorTypeTimeout,
		Provider:  w.provider,
		Message:   fmt.Sprintf("stream idle timeout: no event received for %s", w.timeout),
		Retryable: true,
		Cause:     ErrStreamIdle,
	}
}
