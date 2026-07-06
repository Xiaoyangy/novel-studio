package bedrock

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type signingTransport struct {
	credentials CredentialsProvider
	region      string
	base        http.RoundTripper
}

func SigningTransport(credentials CredentialsProvider, region string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &signingTransport{credentials: credentials, region: region, base: base}
}

func (t *signingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.credentials == nil {
		return nil, fmt.Errorf("bedrock: credentials provider is required")
	}
	payload, signedReq, err := replayableRequest(req)
	if err != nil {
		return nil, err
	}
	credentials, err := t.credentials.Credentials(req.Context())
	if err != nil {
		return nil, fmt.Errorf("bedrock: resolve credentials: %w", err)
	}
	if credentials.Region == "" {
		credentials.Region = t.region
	}
	if credentials.Region == "" {
		credentials.Region = defaultRegion
	}
	if credentials.AccessKeyID == "" {
		return nil, fmt.Errorf("bedrock: access key id is required")
	}
	if credentials.SecretAccessKey == "" {
		return nil, fmt.Errorf("bedrock: secret access key is required")
	}
	if err := signRequest(signedReq, payload, credentials); err != nil {
		return nil, fmt.Errorf("bedrock: sign request: %w", err)
	}
	return t.base.RoundTrip(signedReq)
}

func replayableRequest(req *http.Request) ([]byte, *http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil {
		return nil, clone, nil
	}
	defer req.Body.Close()
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, nil, err
		}
		defer body.Close()
		payload, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, err
		}
		clone.Body = io.NopCloser(bytes.NewReader(payload))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
		clone.ContentLength = int64(len(payload))
		return payload, clone, nil
	}
	payload, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, nil, err
	}
	clone.Body = io.NopCloser(bytes.NewReader(payload))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	clone.ContentLength = int64(len(payload))
	return payload, clone, nil
}

func signRequest(req *http.Request, payload []byte, credentials Credentials) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Date", amzDate)
	if credentials.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", credentials.SessionToken)
	}

	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	canonicalURI := awsEscapePath(req.URL.EscapedPath(), false)
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := req.URL.RawQuery

	signedHeaders := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if credentials.SessionToken != "" {
		signedHeaders = append(signedHeaders, "x-amz-security-token")
	}
	sort.Strings(signedHeaders)

	var canonicalHeaders strings.Builder
	for _, h := range signedHeaders {
		value := req.Header.Get(h)
		if h == "host" {
			value = req.URL.Host
		}
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteString("\n")
	}

	signedHeadersString := strings.Join(signedHeaders, ";")
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeadersString,
		payloadHash,
	}, "\n")

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := dateStamp + "/" + credentials.Region + "/bedrock/aws4_request"
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := signatureKey(credentials.SecretAccessKey, dateStamp, credentials.Region, "bedrock")
	signature := hmacSHA256Hex(signingKey, []byte(stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, credentials.AccessKeyID, credentialScope, signedHeadersString, signature))
	return nil
}
func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacSHA256Hex(key, data []byte) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func signatureKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func awsEscapePath(path string, encodeSlash bool) string {
	var out strings.Builder
	out.Grow(len(path))
	for i := 0; i < len(path); i++ {
		c := path[i]
		if isAWSPathUnreserved(c) || (c == '/' && !encodeSlash) {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(upperHex[c>>4])
		out.WriteByte(upperHex[c&0x0f])
	}
	return out.String()
}

func isAWSPathUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' ||
		c == '.' ||
		c == '_' ||
		c == '~'
}

const upperHex = "0123456789ABCDEF"
