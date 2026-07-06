package rag

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	return &QdrantClient{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}, nil
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
	if c.ensured && c.vectorDim == vectorSize {
		return nil
	}
	if c.cfg.ResetCollection {
		if err := c.deleteCollection(ctx); err != nil {
			return err
		}
		if err := c.createCollection(ctx, vectorSize); err != nil {
			return err
		}
		c.ensured = true
		c.vectorDim = vectorSize
		return nil
	}
	if err := c.doJSON(ctx, http.MethodGet, c.collectionPath(), nil, nil, http.StatusOK); err == nil {
		c.ensured = true
		c.vectorDim = vectorSize
		return nil
	}
	if err := c.createCollection(ctx, vectorSize); err != nil {
		return err
	}
	c.ensured = true
	c.vectorDim = vectorSize
	return nil
}

func (c *QdrantClient) Write(ctx context.Context, point VectorPoint) error {
	if len(point.Vector) == 0 {
		return fmt.Errorf("qdrant point vector is empty")
	}
	chunk := NormalizeChunk(point.Chunk)
	if chunk.ID == "" {
		return fmt.Errorf("qdrant point chunk id is empty")
	}
	if err := c.EnsureCollection(ctx, len(point.Vector)); err != nil {
		return err
	}
	payload := chunkPayload(chunk)
	payload["chunk_id"] = chunk.ID
	payload["chunk"] = chunk
	body := map[string]any{
		"points": []map[string]any{{
			"id":      qdrantPointID(chunk.ID),
			"vector":  point.Vector,
			"payload": payload,
		}},
	}
	return c.doJSON(ctx, http.MethodPut, c.collectionPath()+"/points?wait=true", body, nil, http.StatusOK)
}

func (c *QdrantClient) Search(ctx context.Context, vector []float32, limit int) ([]VectorSearchHit, error) {
	if len(vector) == 0 || limit <= 0 {
		return nil, nil
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
	resp, err := c.doRaw(ctx, http.MethodDelete, c.collectionPath(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant delete collection failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (c *QdrantClient) createCollection(ctx context.Context, vectorSize int) error {
	body := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": c.cfg.Distance,
		},
	}
	return c.doJSON(ctx, http.MethodPut, c.collectionPath(), body, nil, http.StatusOK)
}

func (c *QdrantClient) collectionPath() string {
	return "/collections/" + url.PathEscape(c.cfg.Collection)
}

func (c *QdrantClient) doJSON(ctx context.Context, method, path string, body any, out any, okStatuses ...int) error {
	resp, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if !statusOK(resp.StatusCode, okStatuses...) {
		return fmt.Errorf("qdrant request failed: method=%s path=%s status=%d body=%s", method, path, resp.StatusCode, truncateForError(string(raw), 300))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse qdrant response: %w", err)
	}
	return nil
}

func (c *QdrantClient) doRaw(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("api-key", c.cfg.APIKey)
	}
	return c.client.Do(req)
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
