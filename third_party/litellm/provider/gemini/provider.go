package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/retry"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com"

type Config struct {
	APIKey     string
	APIKeyFunc func(context.Context) (string, error)
	BaseURL    string
	HTTPClient HTTPClient
	Transport  http.RoundTripper
	Retry      *retry.Policy
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" && cfg.APIKeyFunc == nil {
		return nil, fmt.Errorf("gemini: api key is required")
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("gemini: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("gemini: Retry cannot be used with a custom HTTPClient; use Transport or configure retry on the client")
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
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string {
	return "gemini"
}

func (p *Provider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	wire, err := p.buildRequest(req)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}
	url, err := p.url(ctx, req.Model, "generateContent")
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
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
	var parsed response
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "gemini: decode response", err)
	}
	out, err := convertResponse(&parsed, req)
	if err != nil {
		return nil, litellm.WrapError(err, p.Name())
	}
	litellm.CaptureRawResponse(req, out, data)
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	wire, err := p.buildRequest(req)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal stream request: %w", err)
	}
	url, err := p.url(ctx, req.Model, "streamGenerateContent")
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	url += "&alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
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

func (p *Provider) ListModels(ctx context.Context) ([]litellm.ModelInfo, error) {
	url, err := p.modelsURL(ctx)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: create models request: %w", err)
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
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "gemini: decode models response", err)
	}
	models := make([]litellm.ModelInfo, 0, len(payload.Models))
	for _, item := range payload.Models {
		id := strings.TrimPrefix(item.Name, "models/")
		name := item.DisplayName
		if name == "" {
			name = id
		}
		models = append(models, litellm.ModelInfo{
			ID:               id,
			Name:             name,
			Provider:         p.Name(),
			Description:      item.Description,
			InputTokenLimit:  item.InputTokenLimit,
			OutputTokenLimit: item.OutputTokenLimit,
		})
	}
	return models, nil
}

func (p *Provider) url(ctx context.Context, model, method string) (string, error) {
	baseURL := strings.TrimRight(p.cfg.BaseURL, "/")
	key, err := p.apiKey(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/v1beta/models/%s:%s?key=%s", baseURL, model, method, key), nil
}

func (p *Provider) modelsURL(ctx context.Context) (string, error) {
	baseURL := strings.TrimRight(p.cfg.BaseURL, "/")
	key, err := p.apiKey(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/v1beta/models?key=%s", baseURL, key), nil
}

func (p *Provider) apiKey(ctx context.Context) (string, error) {
	if p.cfg.APIKeyFunc == nil {
		if p.cfg.APIKey == "" {
			return "", fmt.Errorf("gemini: api key is required")
		}
		return p.cfg.APIKey, nil
	}
	key, err := p.cfg.APIKeyFunc(ctx)
	if err != nil {
		return "", fmt.Errorf("gemini: resolve api key: %w", err)
	}
	if key == "" {
		return "", fmt.Errorf("gemini: api key is required")
	}
	return key, nil
}
