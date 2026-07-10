package rag

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const defaultRAGIOAttempts = 4

// IsTransientRAGError classifies transport failures that are safe to retry.
// EOF and truncated HTTP responses are common when a local llama-server or
// Qdrant process restarts between requests.
func IsTransientRAGError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"eof", "connection reset", "connection refused", "broken pipe",
		"server closed idle connection", "unexpected end of json input",
		"timeout", "temporarily unavailable",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly,
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryRAGIO(ctx context.Context, attempts int, fn func(attempt int) error) error {
	if attempts <= 0 {
		attempts = defaultRAGIOAttempts
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn(attempt)
		if lastErr == nil {
			return nil
		}
		if !IsTransientRAGError(lastErr) || attempt == attempts {
			return lastErr
		}
		if err := sleepRAGRetry(ctx, ragRetryBackoff(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

func sleepRAGRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ragRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<min(attempt-1, 4)) * 150 * time.Millisecond
	if d > 2*time.Second {
		return 2 * time.Second
	}
	return d
}
