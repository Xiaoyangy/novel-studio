package retry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTransportRetriesCompleteRetryableResponses(t *testing.T) {
	var attempts int
	transport := NewTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return response(http.StatusTooManyRequests, "slow down"), nil
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != `{"ok":true}` {
			t.Fatalf("body = %q", body)
		}
		return response(http.StatusOK, "ok"), nil
	}), &Policy{MaxAttempts: 2, InitialDelay: time.Nanosecond})

	req, err := http.NewRequest(http.MethodPost, "https://example.test", bytes.NewReader([]byte(`{"ok":true}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 2 || resp.StatusCode != http.StatusOK {
		t.Fatalf("attempts/status = %d/%d", attempts, resp.StatusCode)
	}
}

func TestTransportDoesNotRetryNetworkErrors(t *testing.T) {
	boom := errors.New("boom")
	var attempts int
	transport := NewTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return nil, boom
	}), &Policy{MaxAttempts: 3, InitialDelay: time.Nanosecond})

	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = transport.RoundTrip(req)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestTransportRetryAfterRespectsContext(t *testing.T) {
	var attempts int
	transport := NewTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		resp := response(http.StatusTooManyRequests, "slow down")
		resp.Header.Set("Retry-After", "30")
		return resp, nil
	}), &Policy{MaxAttempts: 2, InitialDelay: time.Hour, RespectRetryAfter: true})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = transport.RoundTrip(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestTransportRejectsNonReplayableBodyOnRetry(t *testing.T) {
	var attempts int
	transport := NewTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return response(http.StatusServiceUnavailable, "retry"), nil
	}), &Policy{MaxAttempts: 2, InitialDelay: time.Nanosecond})

	req, err := http.NewRequest(http.MethodPost, "https://example.test", io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = transport.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "not replayable") {
		t.Fatalf("expected non-replayable body error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
