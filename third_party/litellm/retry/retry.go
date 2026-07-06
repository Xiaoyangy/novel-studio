// Package retry provides explicit opt-in HTTP retry transports for providers.
//
// The default SDK behavior is no retry. Provider configs expose Retry for the
// simple path; this package is also available for advanced transport composition.
package retry

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

type Policy struct {
	MaxAttempts       int
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	Multiplier        float64
	Jitter            bool
	RespectRetryAfter bool
}

// DefaultPolicy returns a conservative retry policy for complete retryable HTTP
// responses. It does not retry network write/read errors because a POST may
// already have been processed by the provider.
func DefaultPolicy() *Policy {
	return &Policy{
		MaxAttempts:       3,
		InitialDelay:      200 * time.Millisecond,
		MaxDelay:          2 * time.Second,
		Multiplier:        2,
		Jitter:            true,
		RespectRetryAfter: true,
	}
}

// NewTransport wraps base with retry behavior. A nil policy, MaxAttempts <= 1,
// or nil base keeps behavior simple: no retry or http.DefaultTransport.
func NewTransport(base http.RoundTripper, policy *Policy) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if policy == nil || policy.MaxAttempts <= 1 {
		return base
	}
	resolved := normalizePolicy(*policy)
	return &Transport{Base: base, Policy: resolved}
}

// NewHTTPClient returns a shallow copy of base whose Transport is wrapped by
// NewTransport. Provider Config.Retry is the preferred user-facing API.
func NewHTTPClient(base *http.Client, policy *Policy) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	out := *base
	out.Transport = NewTransport(base.Transport, policy)
	return &out
}

// Transport retries complete 429/5xx/529 responses according to Policy.
// It requires replayable request bodies for retries.
type Transport struct {
	Base   http.RoundTripper
	Policy Policy
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	policy := normalizePolicy(t.Policy)
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		attemptReq, err := requestForAttempt(req, attempt)
		if err != nil {
			return nil, err
		}
		resp, err := base.RoundTrip(attemptReq)
		if err != nil {
			return nil, err
		}
		if !isRetryableStatus(resp.StatusCode) || attempt == policy.MaxAttempts {
			return resp, nil
		}
		if req.Body != nil && req.GetBody == nil {
			if err := drainAndCloseResponse(resp); err != nil {
				return nil, err
			}
			return nil, errors.New("retry: request body is not replayable")
		}

		delay := policy.delay(attempt, resp)
		if err := drainAndCloseResponse(resp); err != nil {
			return nil, err
		}
		if err := sleep(req.Context(), delay); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("retry: exhausted attempts without response")
}

func requestForAttempt(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 1 {
		return req, nil
	}
	if req.Body != nil && req.GetBody == nil {
		return nil, errors.New("retry: request body is not replayable")
	}
	cloned := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		cloned.Body = body
	}
	return cloned, nil
}

func normalizePolicy(policy Policy) Policy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	if policy.InitialDelay <= 0 {
		policy.InitialDelay = 200 * time.Millisecond
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = 2 * time.Second
	}
	if policy.Multiplier <= 0 {
		policy.Multiplier = 2
	}
	return policy
}

func (p Policy) delay(attempt int, resp *http.Response) time.Duration {
	if p.RespectRetryAfter {
		if retryAfter := parseRetryAfter(resp); retryAfter > 0 {
			return retryAfter
		}
	}
	delay := p.InitialDelay
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * p.Multiplier)
		if delay >= p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if delay > p.MaxDelay {
		delay = p.MaxDelay
	}
	if p.Jitter && delay > 0 {
		spread := float64(delay) * 0.25
		delay = time.Duration(float64(delay) + spread*(2*rand.Float64()-1))
		if delay < 0 {
			delay = 0
		}
	}
	return delay
}

func isRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		529:
		return true
	default:
		return false
	}
}

func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	value := resp.Header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func drainAndCloseResponse(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	_, err := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return err
	}
	return closeErr
}
