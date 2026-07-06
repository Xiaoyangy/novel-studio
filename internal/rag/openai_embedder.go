package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIEmbedderConfig struct {
	APIKey    string
	BaseURL   string
	Model     string
	UserAgent string
	Headers   map[string]string
	Timeout   time.Duration
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
	return &OpenAIEmbedder{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.BaseURL+"/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rag embedding request failed: status=%d body=%s", resp.StatusCode, truncateForError(string(raw), 300))
	}
	var payload struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error any `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding response missing vector")
	}
	return payload.Data[0].Embedding, nil
}

func truncateForError(text string, limit int) string {
	rs := []rune(strings.TrimSpace(text))
	if len(rs) <= limit {
		return string(rs)
	}
	return string(rs[:limit]) + "..."
}
