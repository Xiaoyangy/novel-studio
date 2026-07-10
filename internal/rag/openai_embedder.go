package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEmbeddingMaxAttempts = 6

type OpenAIEmbedderConfig struct {
	APIKey    string
	BaseURL   string
	Model     string
	UserAgent string
	Headers   map[string]string
	Timeout   time.Duration
	// DisableKeepAlives is useful for local llama-server processes that may be
	// restarted in place while an old pooled socket still exists.
	DisableKeepAlives bool
}

type OpenAIEmbedder struct {
	cfg    OpenAIEmbedderConfig
	client *http.Client
}

func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) (*OpenAIEmbedder, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("rag embedding model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = cfg.DisableKeepAlives
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = minDuration(cfg.Timeout, 30*time.Second)
	return &OpenAIEmbedder{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, Transport: transport}}, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("rag embedding input is empty")
	}
	body := map[string]any{
		"model": e.cfg.Model,
		"input": text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var lastErr error
	attemptsUsed := 0
	for attempt := 1; attempt <= defaultEmbeddingMaxAttempts; attempt++ {
		attemptsUsed = attempt
		vec, err := e.embedOnce(ctx, data)
		if err == nil {
			return vec, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !isRetryableEmbeddingError(err) || attempt == defaultEmbeddingMaxAttempts {
			break
		}
		e.closeIdleConnections()
		if err := sleepEmbeddingRetry(ctx, embeddingRetryBackoff(attempt)); err != nil {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("rag embedding request failed after %d attempts: %w", attemptsUsed, lastErr)
	}
	return nil, fmt.Errorf("rag embedding request failed")
}

func (e *OpenAIEmbedder) embedOnce(ctx context.Context, data []byte) ([]float32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.BaseURL+"/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Close = e.cfg.DisableKeepAlives
	if e.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	}
	if e.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", e.cfg.UserAgent)
	}
	for k, v := range e.cfg.Headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, retryableEmbeddingError{err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, retryableEmbeddingError{err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("status=%d body=%s", resp.StatusCode, truncateForError(string(raw), 300))
		if isRetryableEmbeddingStatus(resp.StatusCode) {
			return nil, retryableEmbeddingError{err: err}
		}
		return nil, err
	}
	var payload struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error any `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		parseErr := fmt.Errorf("parse embedding response: %w", err)
		if IsTransientRAGError(parseErr) {
			return nil, retryableEmbeddingError{err: parseErr}
		}
		return nil, parseErr
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Embedding) == 0 {
		return nil, retryableEmbeddingError{err: fmt.Errorf("embedding response missing vector")}
	}
	vector := payload.Data[0].Embedding
	if err := ValidateVector(vector); err != nil {
		return nil, fmt.Errorf("embedding response contains invalid vector: %w", err)
	}
	return vector, nil
}

type retryableEmbeddingError struct {
	err error
}

func (e retryableEmbeddingError) Error() string { return e.err.Error() }
func (e retryableEmbeddingError) Unwrap() error { return e.err }

func isRetryableEmbeddingError(err error) bool {
	var retryable retryableEmbeddingError
	return errors.As(err, &retryable) || IsTransientRAGError(err)
}

func isRetryableEmbeddingStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func sleepEmbeddingRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func embeddingRetryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 200 * time.Millisecond
	}
	d := time.Duration(1<<min(attempt-1, 4)) * 200 * time.Millisecond
	if d > 3*time.Second {
		return 3 * time.Second
	}
	return d
}

func (e *OpenAIEmbedder) closeIdleConnections() {
	if closer, ok := e.client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func truncateForError(text string, limit int) string {
	rs := []rune(strings.TrimSpace(text))
	if len(rs) <= limit {
		return string(rs)
	}
	return string(rs[:limit]) + "..."
}
