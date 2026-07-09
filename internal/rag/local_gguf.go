package rag

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Task 071（batch-10）：真语义 embedding 本地落地。模型放在项目内
// （models/embedding/Qwen3-Embedding-0.6B-Q8_0.gguf），由 llama-server 以
// OpenAI 兼容 /v1/embeddings 提供服务，本文件负责按需拉起与健康等待。
//
// Qwen3-Embedding-0.6B 要点（HF 官方 GGUF 卡）：
//   - 输出 1024 维（MRL 支持 32-1024），pooling 必须 last
//   - 输入末尾需手动补 <|endoftext|>（llama-server 不套模板）
//   - llama-server 不支持 --embd-normalize，客户端做 L2 归一化
//     （本地 vector_store 用 cosine 对归一不敏感，但 Qdrant collection
//     建成 Dot 时敏感——统一归一化最稳）

// LocalGGUFConfig 本地 GGUF embedding 服务配置。
type LocalGGUFConfig struct {
	GGUFPath string        // 模型文件路径（项目内）
	Port     int           // 默认 18434
	CtxSize  int           // 默认 8192
	Timeout  time.Duration // 单次 embed 超时
}

func (c *LocalGGUFConfig) fillDefaults() {
	if c.Port <= 0 {
		c.Port = 18434
	}
	if c.CtxSize <= 0 {
		c.CtxSize = 8192
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
}

func (c LocalGGUFConfig) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", c.Port)
}

// EnsureLocalGGUFServer 确保本地 embedding 服务可用：健康则直接返回；
// 否则用 llama-server 拉起（detached，模型加载最多等 120s）。
func EnsureLocalGGUFServer(ctx context.Context, cfg LocalGGUFConfig) error {
	cfg.fillDefaults()
	if localGGUFHealthy(cfg.baseURL()) {
		return nil
	}
	if _, err := os.Stat(cfg.GGUFPath); err != nil {
		return fmt.Errorf("本地 embedding 模型不存在: %s（见 models/embedding/README.md 下载说明）", cfg.GGUFPath)
	}
	bin, err := exec.LookPath("llama-server")
	if err != nil {
		return fmt.Errorf("未找到 llama-server（brew install llama.cpp）: %w", err)
	}
	cmd := exec.Command(bin,
		"-m", cfg.GGUFPath,
		"--embedding",
		"--pooling", "last",
		"-c", fmt.Sprint(cfg.CtxSize),
		"-ub", fmt.Sprint(cfg.CtxSize),
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(cfg.Port),
	)
	detachQdrantCommand(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("拉起 llama-server 失败: %w", err)
	}
	// detach：进程由系统接管，随用随在；健康检查决定可用性。
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
		if localGGUFHealthy(cfg.baseURL()) {
			return nil
		}
	}
	return fmt.Errorf("llama-server 启动超时（模型加载 >120s）: %s", cfg.GGUFPath)
}

func localGGUFHealthy(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// localGGUFEmbedder 包装 OpenAIEmbedder：输入补 <|endoftext|>，输出 L2 归一化。
type localGGUFEmbedder struct {
	inner *OpenAIEmbedder
	cfg   LocalGGUFConfig
	model string
	mu    sync.Mutex
}

// NewLocalGGUFEmbedder 构建指向本地 llama-server 的 embedder。
func NewLocalGGUFEmbedder(cfg LocalGGUFConfig, model string) (Embedder, error) {
	cfg.fillDefaults()
	if strings.TrimSpace(model) == "" {
		model = "qwen3-embedding-0.6b"
	}
	inner, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: cfg.baseURL() + "/v1",
		Model:   model,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return &localGGUFEmbedder{inner: inner, cfg: cfg, model: model}, nil
}

func (e *localGGUFEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("rag embedding input is empty")
	}
	if !strings.HasSuffix(text, "<|endoftext|>") {
		text += "<|endoftext|>"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	vec, err := e.inner.Embed(ctx, text)
	if err == nil {
		l2Normalize(vec)
		return vec, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	firstErr := err
	if err := EnsureLocalGGUFServer(ctx, e.cfg); err != nil {
		return nil, fmt.Errorf("local gguf embedding failed and restart failed: %w (original: %v)", err, firstErr)
	}
	inner, err := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: e.cfg.baseURL() + "/v1",
		Model:   e.model,
		Timeout: e.cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("rebuild local gguf embedder: %w (original: %v)", err, firstErr)
	}
	e.inner = inner
	vec, err = e.inner.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("local gguf embedding failed after restart: %w (original: %v)", err, firstErr)
	}
	l2Normalize(vec)
	return vec, nil
}

func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
}
