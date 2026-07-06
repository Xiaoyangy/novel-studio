package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/retry"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultUserAgent = "litellm-go/0.1"
)

type Config struct {
	APIKey     string
	APIKeyFunc func(context.Context) (string, error)
	BaseURL    string
	HTTPClient HTTPClient
	Transport  http.RoundTripper
	Retry      *retry.Policy
	Version    string
	Beta       string
	UserAgent  string
	Headers    map[string]string
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" && cfg.APIKeyFunc == nil {
		return nil, fmt.Errorf("anthropic: api key is required")
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("anthropic: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("anthropic: Retry cannot be used with a custom HTTPClient; use Transport or configure retry on the client")
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
	if cfg.Version == "" {
		cfg.Version = "2023-06-01"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string {
	return "anthropic"
}

func (p *Provider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	wire, err := p.buildRequest(req, false)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
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
	var parsed anthropicResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "anthropic: decode response", err)
	}
	out, err := convertResponse(&parsed, req.Model)
	if err != nil {
		return nil, litellm.WrapError(err, p.Name())
	}
	litellm.CaptureRawResponse(req, out, data)
	return out, nil
}

func (p *Provider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	wire, err := p.buildRequest(req, true)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if err := p.setHeaders(ctx, httpReq); err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
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

func (p *Provider) setHeaders(ctx context.Context, req *http.Request) error {
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		resolved, err := p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return fmt.Errorf("anthropic: resolve api key: %w", err)
		}
		key = resolved
	}
	if key == "" {
		return fmt.Errorf("anthropic: api key is required")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", p.cfg.Version)
	if p.cfg.Beta != "" {
		req.Header.Set("anthropic-beta", p.cfg.Beta)
	}
	for name, value := range p.cfg.Headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return fmt.Errorf("anthropic: header name cannot be empty")
		}
		if value == "" {
			continue
		}
		req.Header.Set(name, value)
	}
	return nil
}

func cacheControl(cache *litellm.CacheControl) (*anthropicCacheControl, error) {
	if cache == nil {
		return nil, nil
	}
	cc := &anthropicCacheControl{Type: cache.Type}
	if cc.Type == "" {
		cc.Type = litellm.CacheTypeEphemeral
	}
	if cc.Type != litellm.CacheTypeEphemeral {
		return nil, fmt.Errorf("anthropic: unsupported cache type %q", cache.Type)
	}
	if cache.TTL != "" && cache.TTL != litellm.CacheTTL5m {
		if cache.TTL != litellm.CacheTTL1h {
			return nil, fmt.Errorf("anthropic: unsupported cache ttl %q", cache.TTL)
		}
		cc.TTL = cache.TTL
	}
	return cc, nil
}

func imageSource(block litellm.ImageBlock) (*anthropicImageSource, error) {
	switch {
	case block.URL != "":
		return &anthropicImageSource{Type: "url", URL: block.URL}, nil
	case len(block.Data) > 0:
		if block.MIME == "" {
			return nil, fmt.Errorf("anthropic: inline image MIME is required")
		}
		return &anthropicImageSource{
			Type:      "base64",
			MediaType: block.MIME,
			Data:      base64.StdEncoding.EncodeToString(block.Data),
		}, nil
	default:
		return nil, fmt.Errorf("anthropic: image requires URL or data")
	}
}
