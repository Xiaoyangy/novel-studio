package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/retry"
)

const defaultRegion = "us-east-1"

type Config struct {
	Region              string
	BaseURL             string
	ControlPlaneBaseURL string
	Credentials         CredentialsProvider
	HTTPClient          HTTPClient
	Transport           http.RoundTripper
	Retry               *retry.Policy
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

type CredentialsProvider interface {
	Credentials(context.Context) (Credentials, error)
}

type staticCredentials struct {
	credentials Credentials
}

func StaticCredentials(accessKeyID, secretAccessKey, sessionToken string) CredentialsProvider {
	return staticCredentials{credentials: Credentials{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
	}}
}

func (p staticCredentials) Credentials(context.Context) (Credentials, error) {
	return p.credentials, nil
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	if cfg.Credentials == nil {
		return nil, fmt.Errorf("bedrock: credentials provider is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", cfg.Region)
	}
	if cfg.ControlPlaneBaseURL == "" {
		cfg.ControlPlaneBaseURL = fmt.Sprintf("https://bedrock.%s.amazonaws.com", cfg.Region)
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("bedrock: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.HTTPClient != nil && cfg.Retry != nil {
		return nil, fmt.Errorf("bedrock: Retry cannot be used with a custom HTTPClient; use Transport so Bedrock can retry above SigV4 signing")
	}
	base := cfg.Transport
	if base == nil {
		if cfg.HTTPClient != nil {
			base = clientTransport{client: cfg.HTTPClient}
		} else {
			base = http.DefaultTransport
		}
	}
	signed := SigningTransport(cfg.Credentials, cfg.Region, base)
	cfg.HTTPClient = &http.Client{Transport: retry.NewTransport(signed, cfg.Retry)}
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (litellm.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string {
	return "bedrock"
}

func (p *Provider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	wire, err := p.buildRequest(req)
	if err != nil {
		return nil, litellm.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal request: %w", err)
	}
	endpoint, rawPath, err := runtimeEndpoint(p.cfg.BaseURL, req.Model, "converse")
	if err != nil {
		return nil, fmt.Errorf("bedrock: create endpoint: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: create request: %w", err)
	}
	httpReq.URL.RawPath = rawPath
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
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "bedrock: decode response", err)
	}
	out, err := convertResponse(&parsed, req.Model)
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
		return nil, fmt.Errorf("bedrock: marshal stream request: %w", err)
	}
	endpoint, rawPath, err := runtimeEndpoint(p.cfg.BaseURL, req.Model, "converse-stream")
	if err != nil {
		return nil, fmt.Errorf("bedrock: create stream endpoint: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: create stream request: %w", err)
	}
	httpReq.URL.RawPath = rawPath
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, litellm.NewNetworkError(p.Name(), "stream request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, litellm.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	return newStream(resp, req.Model), nil
}

func (p *Provider) ListModels(ctx context.Context) ([]litellm.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.ControlPlaneBaseURL, "/")+"/foundation-models", nil)
	if err != nil {
		return nil, fmt.Errorf("bedrock: create models request: %w", err)
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
		return nil, litellm.NewProviderErrorWithCause(p.Name(), litellm.ErrorTypeProvider, "bedrock: decode models response", err)
	}
	models := make([]litellm.ModelInfo, 0, len(payload.ModelSummaries))
	for _, item := range payload.ModelSummaries {
		name := item.ModelName
		if name == "" {
			name = item.ModelID
		}
		models = append(models, litellm.ModelInfo{
			ID:               item.ModelID,
			Name:             name,
			Provider:         item.ProviderName,
			InputTokenLimit:  item.InputTokenLimit,
			OutputTokenLimit: item.OutputTokenLimit,
		})
	}
	return models, nil
}

type clientTransport struct {
	client HTTPClient
}

func (t clientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

func runtimeEndpoint(baseURL, model, operation string) (string, string, error) {
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", "", err
	}
	pathPrefix := strings.TrimRight(endpoint.Path, "/")
	rawPrefix := strings.TrimRight(endpoint.EscapedPath(), "/")
	path := pathPrefix + "/model/" + model + "/" + operation
	rawPath := rawPrefix + "/model/" + awsEscapePath(model, true) + "/" + operation
	endpoint.Path = path
	endpoint.RawPath = rawPath
	return endpoint.String(), rawPath, nil
}
