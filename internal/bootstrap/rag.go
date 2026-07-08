package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/rag"
)

func NewRAGEmbedder(cfg Config) (rag.Embedder, bool, error) {
	return NewRAGEmbedderWithOverride(cfg, RAGEmbeddingConfig{})
}

func NewRAGQdrantClient(cfg Config, resetCollection bool) (*rag.QdrantClient, bool, error) {
	qc, enabled := ResolveRAGQdrantConfig(cfg)
	if !enabled {
		return nil, false, nil
	}
	client, err := rag.NewQdrantClient(rag.QdrantClientConfig{
		URL:             qc.URL,
		APIKey:          resolveQdrantAPIKey(qc),
		Collection:      qc.Collection,
		Timeout:         time.Duration(qc.TimeoutSeconds) * time.Second,
		ResetCollection: resetCollection,
	})
	if err != nil {
		return nil, true, fmt.Errorf("初始化 Qdrant 失败: %w", err)
	}
	return client, true, nil
}

func EnsureRAGQdrant(ctx context.Context, cfg Config) (bool, error) {
	qc, enabled := ResolveRAGQdrantConfig(cfg)
	if !enabled {
		return false, nil
	}
	err := rag.EnsureLocalQdrant(ctx, rag.QdrantServiceConfig{
		URL:           qc.URL,
		APIKey:        resolveQdrantAPIKey(qc),
		AutoStart:     qc.AutoStart,
		BinaryPath:    qc.BinaryPath,
		DockerImage:   qc.DockerImage,
		ContainerName: qc.ContainerName,
		StorageDir:    qc.StorageDir,
		Timeout:       time.Duration(qc.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return true, err
	}
	return true, nil
}

func NewRAGEmbedderWithOverride(cfg Config, override RAGEmbeddingConfig) (rag.Embedder, bool, error) {
	emb, enabled := ResolveRAGEmbeddingConfig(cfg, override)
	if !enabled {
		return nil, false, nil
	}
	pc := cfg.Providers[emb.Provider]
	apiKey := strings.TrimSpace(emb.APIKey)
	if emb.APIKeyEnv != "" {
		if envValue := strings.TrimSpace(os.Getenv(emb.APIKeyEnv)); envValue != "" {
			apiKey = envValue
		}
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(pc.APIKey)
	}
	baseURL := strings.TrimSpace(emb.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(pc.BaseURL)
	}
	timeout := time.Duration(emb.TimeoutSeconds) * time.Second
	// Task 071：项目内 GGUF 本地语义模型优先（Qwen3-Embedding-0.6B，llama-server 自启动）——
	// 必须先于 provider=="local" 的哈希兜底判断，否则旧配置残留的 provider 会吞掉真模型。
	if gguf := strings.TrimSpace(emb.LocalGGUF); gguf != "" {
		lcfg := rag.LocalGGUFConfig{GGUFPath: gguf, Port: emb.LocalPort, Timeout: timeout}
		if err := rag.EnsureLocalGGUFServer(context.Background(), lcfg); err != nil {
			return nil, true, fmt.Errorf("本地 embedding 服务不可用: %w", err)
		}
		embedder, err := rag.NewLocalGGUFEmbedder(lcfg, emb.Model)
		if err != nil {
			return nil, true, fmt.Errorf("初始化本地 embedding 失败: %w", err)
		}
		return embedder, true, nil
	}
	if strings.EqualFold(emb.Provider, "local") || strings.HasPrefix(strings.ToLower(emb.Model), "local-") {
		return rag.NewLocalHashEmbedder(emb.Model), true, nil
	}
	headers, userAgent := providerHTTPExtras(pc)
	embedder, err := rag.NewOpenAIEmbedder(rag.OpenAIEmbedderConfig{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		Model:     emb.Model,
		UserAgent: userAgent,
		Headers:   headers,
		Timeout:   timeout,
	})
	if err != nil {
		return nil, true, fmt.Errorf("初始化 RAG embedding 失败: %w", err)
	}
	return embedder, true, nil
}

func ResolveRAGEmbeddingConfig(cfg Config, override RAGEmbeddingConfig) (RAGEmbeddingConfig, bool) {
	cfg.FillDefaults()
	emb := mergeRAGEmbeddingConfig(cfg.RAG.Embedding, override)
	if !emb.Enabled {
		return emb, false
	}
	if emb.Provider == "" {
		emb.Provider = cfg.Provider
	}
	if emb.Provider == "" {
		emb.Provider = "direct"
	}
	if emb.Model == "" {
		emb.Model = "text-embedding-3-small"
	}
	if emb.BuildConcurrency <= 0 {
		emb.BuildConcurrency = 2
	}
	if emb.TimeoutSeconds <= 0 {
		emb.TimeoutSeconds = 60
	}
	return emb, true
}

func ResolveRAGQdrantConfig(cfg Config) (RAGQdrantConfig, bool) {
	cfg.FillDefaults()
	qc := cfg.RAG.Qdrant
	enabled := qc.Enabled || cfg.RAG.Embedding.Enabled
	if !enabled {
		return qc, false
	}
	if qc.URL == "" {
		qc.URL = "http://127.0.0.1:6333"
	}
	if qc.Collection == "" {
		qc.Collection = ragCollectionName(cfg.OutputDir)
	}
	if qc.DockerImage == "" {
		qc.DockerImage = "qdrant/qdrant:latest"
	}
	if qc.ContainerName == "" {
		qc.ContainerName = "novel-studio-qdrant"
	}
	if qc.TimeoutSeconds <= 0 {
		qc.TimeoutSeconds = 30
	}
	qc.AutoStart = true
	return qc, true
}

func ragCollectionName(outputDir string) string {
	// 规范化为绝对路径再派生 collection 名：否则同一物理目录被不同代码路径以相对/绝对/
	// 带斜杠等不同字符串引用时会 hash 出不同 collection（曾导致 pipeline 与 build-rag 用了
	// 两个 collection、rewrite 写入时 404）。
	if abs, err := filepath.Abs(outputDir); err == nil {
		outputDir = abs
	}
	return rag.CollectionName("novel_studio", filepath.Clean(outputDir))
}

func resolveQdrantAPIKey(qc RAGQdrantConfig) string {
	apiKey := strings.TrimSpace(qc.APIKey)
	if qc.APIKeyEnv != "" {
		if envValue := strings.TrimSpace(os.Getenv(qc.APIKeyEnv)); envValue != "" {
			apiKey = envValue
		}
	}
	return apiKey
}

func mergeRAGEmbeddingConfig(base, overlay RAGEmbeddingConfig) RAGEmbeddingConfig {
	if overlay.Enabled {
		base.Enabled = true
	}
	if overlay.Provider != "" {
		base.Provider = overlay.Provider
	}
	if overlay.Model != "" {
		base.Model = overlay.Model
	}
	if overlay.APIKey != "" {
		base.APIKey = overlay.APIKey
	}
	if overlay.APIKeyEnv != "" {
		base.APIKeyEnv = overlay.APIKeyEnv
	}
	if overlay.BaseURL != "" {
		base.BaseURL = overlay.BaseURL
	}
	if overlay.TimeoutSeconds > 0 {
		base.TimeoutSeconds = overlay.TimeoutSeconds
	}
	if overlay.BuildConcurrency > 0 {
		base.BuildConcurrency = overlay.BuildConcurrency
	}
	if overlay.SearchConcurrency > 0 {
		base.SearchConcurrency = overlay.SearchConcurrency
	}
	return base
}

func providerHTTPExtras(pc ProviderConfig) (map[string]string, string) {
	headers := map[string]string{}
	var userAgent string
	if ua, ok := pc.Extra["user_agent"].(string); ok {
		userAgent = ua
	}
	switch raw := pc.Extra["headers"].(type) {
	case map[string]any:
		for k, v := range raw {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	case map[string]string:
		for k, v := range raw {
			headers[k] = v
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	return headers, userAgent
}
