package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/retry"
)

const (
	defaultBaseURL   = "https://api.openai.com"
	defaultUserAgent = "litellm-go/0.1"

	APIChat      = "chat"
	APIResponses = "responses"
)

type Config struct {
	API               string
	APIKey            string
	APIKeyFunc        func(context.Context) (string, error)
	BaseURL           string
	HTTPClient        HTTPClient
	Transport         http.RoundTripper
	Retry             *retry.Policy
	StreamIdleTimeout time.Duration
	UserAgent         string
	Headers           map[string]string
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" && cfg.APIKeyFunc == nil {
		return nil, fmt.Errorf("openai: api key is required")
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("openai: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("openai: Retry cannot be used with a custom HTTPClient; use Transport or configure retry on the client")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		base := cfg.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		cfg.HTTPClient = &http.Client{Transport: retry.NewTransport(base, cfg.Retry)}
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	api, err := normalizeAPI(cfg.API)
	if err != nil {
		return nil, err
	}
	cfg.API = api
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string {
	return "openai"
}

func (p *Provider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	if p.cfg.API == APIResponses {
		return p.Responses(ctx, responsesRequestFromChat(req))
	}
	wire, err := p.buildRequest(req, false)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url("/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "read response failed", err)
	}
	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "openai: decode response", err)
	}
	out, err := convertResponse(&parsed, req)
	if err != nil {
		return nil, litellm.WrapError(err, p.Name())
	}
	litellm.CaptureRawResponse(req, out, data)
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	if p.cfg.API == APIResponses {
		return p.ResponsesStream(ctx, responsesRequestFromChat(req))
	}
	wire, err := p.buildRequest(req, true)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url("/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create stream request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "stream request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	return newStream(resp, req), nil
}

func normalizeAPI(api string) (string, error) {
	api = strings.ToLower(strings.TrimSpace(api))
	switch api {
	case "", APIChat:
		return APIChat, nil
	case APIResponses:
		return APIResponses, nil
	default:
		return "", fmt.Errorf("openai: api must be chat or responses, got %q", api)
	}
}

func (p *Provider) ListModels(ctx context.Context) ([]litellm.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url("/models"), nil)
	if err != nil {
		return nil, fmt.Errorf("openai: create models request: %w", err)
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "models request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	var payload modelList
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "openai: decode models response", err)
	}
	models := make([]litellm.ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, litellm.ModelInfo{
			ID:       item.ID,
			Name:     item.ID,
			Provider: p.Name(),
			Created:  item.Created,
		})
	}
	return models, nil
}

func (p *Provider) setHeaders(ctx context.Context, req *http.Request) error {
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		resolved, err := p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return fmt.Errorf("openai: resolve api key: %w", err)
		}
		key = resolved
	}
	if key == "" {
		return fmt.Errorf("openai: api key is required")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	for name, value := range p.cfg.Headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return fmt.Errorf("openai: header name cannot be empty")
		}
		if value == "" {
			continue
		}
		req.Header.Set(name, value)
	}
	return nil
}

func (p *Provider) url(path string) string {
	baseURL := strings.TrimRight(p.cfg.BaseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + path
	}
	return baseURL + "/v1" + path
}
