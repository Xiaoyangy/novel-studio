package rag

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

type VectorSearcher interface {
	Search(ctx context.Context, vector []float32, limit int) ([]VectorSearchHit, error)
}

type SourcePathDeleter interface {
	DeleteSourcePath(ctx context.Context, sourcePath string) error
}

type QdrantClientConfig struct {
	URL             string
	APIKey          string
	Collection      string
	Distance        string
	Timeout         time.Duration
	ResetCollection bool
}

type QdrantClient struct {
	cfg       QdrantClientConfig
	client    *http.Client
	ensureMu  sync.Mutex
	ensured   bool
	vectorDim int
}

func NewQdrantClient(cfg QdrantClientConfig) (*QdrantClient, error) {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.Collection = strings.TrimSpace(cfg.Collection)
	cfg.Distance = strings.TrimSpace(cfg.Distance)
	if cfg.URL == "" {
		cfg.URL = "http://127.0.0.1:6333"
	}
	if cfg.Collection == "" {
		return nil, fmt.Errorf("qdrant collection is required")
	}
	if cfg.Distance == "" {
		cfg.Distance = "Cosine"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	responseHeaderTimeout := 15 * time.Second
	if cfg.Timeout < responseHeaderTimeout {
		responseHeaderTimeout = cfg.Timeout
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	return &QdrantClient{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, Transport: transport}}, nil
}

func (c *QdrantClient) Collection() string { return c.cfg.Collection }
func (c *QdrantClient) URL() string        { return c.cfg.URL }

func (c *QdrantClient) Health(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodGet, "/collections", nil, nil, http.StatusOK)
}

func (c *QdrantClient) EnsureCollection(ctx context.Context, vectorSize int) error {
	if vectorSize <= 0 {
		return fmt.Errorf("qdrant vector size must be > 0")
	}
	c.ensureMu.Lock()
	defer c.ensureMu.Unlock()
	if c.ensured {
		if c.vectorDim != vectorSize {
			return fmt.Errorf("qdrant collection %s vector dimension mismatch: existing=%d requested=%d", c.cfg.Collection, c.vectorDim, vectorSize)
		}
		return nil
	}
	if c.cfg.ResetCollection {
		if err := c.deleteCollection(ctx); err != nil {
			return err
		}
		if err := c.createCollection(ctx, vectorSize); err != nil {
			if verifyErr := c.verifyCollectionDimension(ctx, vectorSize); verifyErr != nil {
				return fmt.Errorf("create qdrant collection: %w (verification: %v)", err, verifyErr)
			}
		}
		c.ensured = true
		c.vectorDim = vectorSize
		return nil
	}
	info, err := c.collectionInfo(ctx)
	if err == nil {
		if info.VectorSize <= 0 {
			return fmt.Errorf("qdrant collection %s did not report vector size", c.cfg.Collection)
		}
		if info.VectorSize != vectorSize {
			return fmt.Errorf("qdrant collection %s vector dimension mismatch: existing=%d requested=%d", c.cfg.Collection, info.VectorSize, vectorSize)
		}
		c.ensured = true
		c.vectorDim = info.VectorSize
		return nil
	}
	if !isQdrantHTTPStatus(err, http.StatusNotFound) {
		return fmt.Errorf("inspect qdrant collection %s: %w", c.cfg.Collection, err)
	}
	if err = c.createCollection(ctx, vectorSize); err != nil {
		if verifyErr := c.verifyCollectionDimension(ctx, vectorSize); verifyErr != nil {
			return fmt.Errorf("create qdrant collection: %w (verification: %v)", err, verifyErr)
		}
	}
	c.ensured = true
	c.vectorDim = vectorSize
	return nil
}

func (c *QdrantClient) Write(ctx context.Context, point VectorPoint) error {
	return c.WriteBatch(ctx, []VectorPoint{point})
}

func (c *QdrantClient) WriteBatch(ctx context.Context, points []VectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	vectorSize := len(points[0].Vector)
	if vectorSize == 0 {
		return fmt.Errorf("qdrant point vector is empty")
	}
	encoded := make([]map[string]any, 0, len(points))
	for _, point := range points {
		if len(point.Vector) != vectorSize {
			return fmt.Errorf("qdrant batch vector dimension drift: got=%d want=%d", len(point.Vector), vectorSize)
		}
		if err := ValidateVector(point.Vector); err != nil {
			return fmt.Errorf("qdrant point vector is invalid: %w", err)
		}
		chunk := NormalizeChunk(point.Chunk)
		if chunk.ID == "" {
			return fmt.Errorf("qdrant point chunk id is empty")
		}
		payload := chunkPayload(chunk)
		payload["chunk_id"] = chunk.ID
		payload["chunk"] = chunk
		encoded = append(encoded, map[string]any{
			"id": qdrantPointID(chunk.ID), "vector": point.Vector, "payload": payload,
		})
	}
	if err := c.EnsureCollection(ctx, vectorSize); err != nil {
		return err
	}
	body := map[string]any{"points": encoded}
	for recovery := 0; recovery < 2; recovery++ {
		err := c.doJSON(ctx, http.MethodPut, c.collectionPath()+"/points?wait=true", body, nil, http.StatusOK)
		if err == nil {
			return nil
		}
		if recovery == 0 && isQdrantHTTPStatus(err, http.StatusNotFound) {
			c.invalidateCollection()
			if ensureErr := c.EnsureCollection(ctx, vectorSize); ensureErr != nil {
				return fmt.Errorf("recreate missing qdrant collection: %w", ensureErr)
			}
			continue
		}
		return err
	}
	return fmt.Errorf("qdrant batch write failed")
}

func (c *QdrantClient) Search(ctx context.Context, vector []float32, limit int) ([]VectorSearchHit, error) {
	if len(vector) == 0 || limit <= 0 {
		return nil, nil
	}
	if err := ValidateVector(vector); err != nil {
		return nil, fmt.Errorf("qdrant query vector is invalid: %w", err)
	}
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}
	var out struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.collectionPath()+"/points/search", body, &out, http.StatusOK); err != nil {
		return nil, err
	}
	hits := make([]VectorSearchHit, 0, len(out.Result))
	for _, item := range out.Result {
		chunk := chunkFromQdrantPayload(item.Payload)
		if chunk.ID == "" || IsForbiddenChunk(chunk) {
			continue
		}
		hits = append(hits, VectorSearchHit{
			Point: domain.RAGVectorPoint{
				ID:      chunk.ID,
				Hash:    chunk.Hash,
				Payload: item.Payload,
				Chunk:   NormalizeChunk(chunk),
			},
			Score: item.Score,
		})
	}
	return hits, nil
}

func (c *QdrantClient) Count(ctx context.Context, exact bool) (int, error) {
	body := map[string]any{"exact": exact}
	var out struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.collectionPath()+"/points/count", body, &out, http.StatusOK); err != nil {
		return 0, err
	}
	return out.Result.Count, nil
}

func (c *QdrantClient) DeleteSourcePath(ctx context.Context, sourcePath string) error {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil
	}
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{{
				"key": "source_path",
				"match": map[string]any{
					"value": sourcePath,
				},
			}},
		},
	}
	return c.doJSON(ctx, http.MethodPost, c.collectionPath()+"/points/delete?wait=true", body, nil, http.StatusOK)
}

func (c *QdrantClient) deleteCollection(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodDelete, c.collectionPath(), nil, nil, http.StatusOK, http.StatusNotFound)
}

func (c *QdrantClient) createCollection(ctx context.Context, vectorSize int) error {
	body := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": c.cfg.Distance,
		},
	}
	return c.doJSON(ctx, http.MethodPut, c.collectionPath(), body, nil, http.StatusOK, http.StatusCreated)
}

type qdrantCollectionInfo struct {
	VectorSize int
}

func (c *QdrantClient) collectionInfo(ctx context.Context) (qdrantCollectionInfo, error) {
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.collectionPath(), nil, &out, http.StatusOK); err != nil {
		return qdrantCollectionInfo{}, err
	}
	return qdrantCollectionInfo{VectorSize: out.Result.Config.Params.Vectors.Size}, nil
}

func (c *QdrantClient) verifyCollectionDimension(ctx context.Context, vectorSize int) error {
	info, err := c.collectionInfo(ctx)
	if err != nil {
		return err
	}
	if info.VectorSize != vectorSize {
		return fmt.Errorf("vector dimension mismatch: existing=%d requested=%d", info.VectorSize, vectorSize)
	}
	return nil
}

func (c *QdrantClient) invalidateCollection() {
	c.ensureMu.Lock()
	c.ensured = false
	c.vectorDim = 0
	c.ensureMu.Unlock()
}

func (c *QdrantClient) collectionPath() string {
	return "/collections/" + url.PathEscape(c.cfg.Collection)
}

func (c *QdrantClient) doJSON(ctx context.Context, method, path string, body any, out any, okStatuses ...int) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	var lastErr error
	for attempt := 1; attempt <= defaultRAGIOAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var reader io.Reader
		if data != nil {
			reader = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, reader)
		if err != nil {
			return err
		}
		if data != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.cfg.APIKey != "" {
			req.Header.Set("api-key", c.cfg.APIKey)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("qdrant transport: %w", err)
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			closeErr := resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read qdrant response: %w", readErr)
			} else if closeErr != nil && IsTransientRAGError(closeErr) {
				lastErr = fmt.Errorf("close qdrant response: %w", closeErr)
			} else if !statusOK(resp.StatusCode, okStatuses...) {
				lastErr = qdrantHTTPError{method: method, path: path, status: resp.StatusCode, body: truncateForError(string(raw), 300)}
				if !isRetryableHTTPStatus(resp.StatusCode) {
					return lastErr
				}
			} else if out != nil {
				if len(raw) == 0 {
					lastErr = fmt.Errorf("parse qdrant response: %w", io.ErrUnexpectedEOF)
				} else if err := json.Unmarshal(raw, out); err != nil {
					lastErr = fmt.Errorf("parse qdrant response: %w", err)
				} else {
					return nil
				}
			} else if len(raw) == 0 && resp.StatusCode != http.StatusNoContent {
				lastErr = fmt.Errorf("parse qdrant response: %w", io.ErrUnexpectedEOF)
			} else if len(raw) > 0 && !json.Valid(raw) {
				var syntax any
				parseErr := json.Unmarshal(raw, &syntax)
				lastErr = fmt.Errorf("parse qdrant response: %w", parseErr)
			} else {
				return nil
			}
		}
		if attempt == defaultRAGIOAttempts || (!IsTransientRAGError(lastErr) && !isRetryableQdrantError(lastErr)) {
			return lastErr
		}
		c.closeIdleConnections()
		if err := sleepRAGRetry(ctx, ragRetryBackoff(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

type qdrantHTTPError struct {
	method string
	path   string
	status int
	body   string
}

func (e qdrantHTTPError) Error() string {
	return fmt.Sprintf("qdrant request failed: method=%s path=%s status=%d body=%s", e.method, e.path, e.status, e.body)
}

func isQdrantHTTPStatus(err error, status int) bool {
	var httpErr qdrantHTTPError
	return errors.As(err, &httpErr) && httpErr.status == status
}

func isRetryableQdrantError(err error) bool {
	var httpErr qdrantHTTPError
	return errors.As(err, &httpErr) && isRetryableHTTPStatus(httpErr.status)
}

func (c *QdrantClient) closeIdleConnections() {
	if closer, ok := c.client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func statusOK(status int, okStatuses ...int) bool {
	if len(okStatuses) == 0 {
		return status >= 200 && status < 300
	}
	for _, ok := range okStatuses {
		if status == ok {
			return true
		}
	}
	return false
}

func chunkFromQdrantPayload(payload map[string]any) domain.RAGChunk {
	if payload == nil {
		return domain.RAGChunk{}
	}
	if raw, ok := payload["chunk"]; ok {
		data, err := json.Marshal(raw)
		if err == nil {
			var chunk domain.RAGChunk
			if json.Unmarshal(data, &chunk) == nil {
				return NormalizeChunk(chunk)
			}
		}
	}
	chunk := domain.RAGChunk{
		ID:         stringFromPayload(payload, "chunk_id"),
		SourcePath: stringFromPayload(payload, "source_path"),
		SourceKind: stringFromPayload(payload, "source_kind"),
		Facet:      stringFromPayload(payload, "facet"),
		ParentID:   stringFromPayload(payload, "parent_id"),
		Hash:       stringFromPayload(payload, "hash"),
		Context:    stringFromPayload(payload, "context"),
		Summary:    stringFromPayload(payload, "summary"),
	}
	return NormalizeChunk(chunk)
}

func stringFromPayload(payload map[string]any, key string) string {
	switch v := payload[key].(type) {
	case string:
		return v
	default:
		return ""
	}
}

func qdrantPointID(id string) string {
	sum := sha1.Sum([]byte("novel-studio-rag\x00" + id))
	b := append([]byte(nil), sum[:16]...)
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}

var collectionUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func CollectionName(prefix, seed string) string {
	prefix = strings.Trim(collectionUnsafe.ReplaceAllString(prefix, "_"), "_")
	if prefix == "" {
		prefix = "novel_studio"
	}
	seed = strings.TrimSpace(seed)
	if seed == "" {
		seed = "default"
	}
	sum := sha1.Sum([]byte(seed))
	return strings.ToLower(prefix + "_" + hex.EncodeToString(sum[:])[:12])
}
