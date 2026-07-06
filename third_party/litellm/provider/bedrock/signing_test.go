package bedrock

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/voocel/litellm/retry"
)

func TestSignRequestSetsSigV4Headers(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-west-2.amazonaws.com/model/anthropic.claude/converse", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	err = signRequest(req, []byte(`{}`), Credentials{
		AccessKeyID:     "AKID",
		SecretAccessKey: "SECRET",
		SessionToken:    "SESSION",
		Region:          "us-west-2",
	})
	if err != nil {
		t.Fatalf("signRequest: %v", err)
	}
	if req.Header.Get("X-Amz-Security-Token") != "SESSION" {
		t.Fatalf("session token = %q", req.Header.Get("X-Amz-Security-Token"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Fatalf("payload hash = %q", req.Header.Get("X-Amz-Content-Sha256"))
	}
	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256 Credential=AKID/",
		"/us-west-2/bedrock/aws4_request",
		"SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token",
		"Signature=",
	} {
		if !strings.Contains(auth, want) {
			t.Fatalf("authorization header missing %q: %s", want, auth)
		}
	}
}

func TestAWSCanonicalPathEscapesBedrockModelID(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-west-2.amazonaws.com/model/anthropic.claude-3-5-sonnet-20240620-v1%3A0/converse", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	canonicalURI := awsEscapePath(req.URL.EscapedPath(), false)
	if canonicalURI != "/model/anthropic.claude-3-5-sonnet-20240620-v1%253A0/converse" {
		t.Fatalf("canonicalURI = %q", canonicalURI)
	}
}

func TestRuntimeURLEscapesModelIDAsSinglePathSegment(t *testing.T) {
	endpoint, rawPath, err := runtimeEndpoint("https://bedrock-runtime.us-west-2.amazonaws.com", "arn:aws:bedrock:us-west-2:123456789012:prompt/PROMPT12345:1", "converse")
	if err != nil {
		t.Fatalf("runtimeEndpoint: %v", err)
	}
	want := "https://bedrock-runtime.us-west-2.amazonaws.com/model/arn%3Aaws%3Abedrock%3Aus-west-2%3A123456789012%3Aprompt%2FPROMPT12345%3A1/converse"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
	wantRawPath := "/model/arn%3Aaws%3Abedrock%3Aus-west-2%3A123456789012%3Aprompt%2FPROMPT12345%3A1/converse"
	if rawPath != wantRawPath {
		t.Fatalf("rawPath = %q, want %q", rawPath, wantRawPath)
	}
}

func TestSigningTransportSignsEachRetryAttempt(t *testing.T) {
	credentials := &countingCredentials{
		credentials: Credentials{
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
			Region:          "us-west-2",
		},
	}
	var attempts int
	var authHeaders []string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		auth := req.Header.Get("Authorization")
		if !strings.Contains(auth, "AWS4-HMAC-SHA256 Credential=AKID/") {
			t.Fatalf("missing auth on attempt %d: %s", attempts, auth)
		}
		authHeaders = append(authHeaders, auth)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != `{}` {
			t.Fatalf("body = %q", body)
		}
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("retry"))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	transport := retry.NewTransport(SigningTransport(credentials, "us-west-2", base), &retry.Policy{
		MaxAttempts:  2,
		InitialDelay: time.Nanosecond,
	})
	req, err := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-west-2.amazonaws.com/model/anthropic.claude/converse", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if credentials.calls != 2 {
		t.Fatalf("credential calls = %d, want 2", credentials.calls)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("auth headers = %d, want 2", len(authHeaders))
	}
}

func TestSigningTransportClosesOriginalRequestBody(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader(`{}`)}
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if _, err := io.ReadAll(req.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		req.Body.Close()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	transport := SigningTransport(StaticCredentials("AKID", "SECRET", ""), "us-west-2", base)
	req, err := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-west-2.amazonaws.com/model/anthropic.claude/converse", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if !body.closed {
		t.Fatal("expected original request body to be closed")
	}
}

type countingCredentials struct {
	credentials Credentials
	calls       int
}

func (c *countingCredentials) Credentials(context.Context) (Credentials, error) {
	c.calls++
	return c.credentials, nil
}

type trackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
