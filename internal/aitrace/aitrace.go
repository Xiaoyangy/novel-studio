// Package aitrace 提供 LLM 调用级横切 trace：每次带 usage 的调用一行 JSONL，
// 落盘 meta/runtime/llm_calls.jsonl。字段名对齐 OpenTelemetry GenAI 语义约定
// （gen_ai.*，2026 初 client span 出实验期），将来可直接喂 OTel 后端；本地 CLI
// 不引 OTel SDK、不建 span 树。
//
// 合宪定位：纯观察层。Emit 永不阻塞主流程、写失败只记 slog；与 meta/usage.json
// （累计量）和 meta/sessions/*.jsonl（消息级）互补，不替代。
package aitrace

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record 一次 LLM 调用的事实。采不到的字段留零值，不为凑字段侵入内核。
type Record struct {
	Timestamp    time.Time `json:"timestamp"`
	Agent        string    `json:"agent"` // coordinator / writer / architect / editor
	Provider     string    `json:"gen_ai.provider.name,omitempty"`
	Model        string    `json:"gen_ai.request.model,omitempty"`
	InputTokens  int       `json:"gen_ai.usage.input_tokens,omitempty"`
	OutputTokens int       `json:"gen_ai.usage.output_tokens,omitempty"`
	CacheRead    int       `json:"cache_read_tokens,omitempty"`
	CacheWrite   int       `json:"cache_write_tokens,omitempty"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	FinishReason string    `json:"gen_ai.response.finish_reasons,omitempty"`
	Fallback     bool      `json:"fallback,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Sink append-only JSONL 写入器。并发安全；nil 安全（Emit noop）。
type Sink struct {
	mu   sync.Mutex
	path string
	// warned 写失败只 warn 一次，避免磁盘只读等场景刷屏。
	warned bool
}

// NewSink 创建指向 path 的 sink；目录按需创建（延迟到首次 Emit）。
func NewSink(path string) *Sink {
	return &Sink{path: path}
}

// Emit 追加一条 trace。永不阻塞调用方语义：无重试、失败只记一次 slog。
func (s *Sink) Emit(r Record) {
	if s == nil || s.path == "" {
		return
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	data, err := json.Marshal(r)
	if err != nil {
		s.warnOnce("llm trace 序列化失败", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		s.warnOnce("llm trace 目录创建失败", err)
		return
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.warnOnce("llm trace 打开失败", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		s.warnOnce("llm trace 写入失败", err)
	}
}

func (s *Sink) warnOnce(msg string, err error) {
	if s.warned {
		return
	}
	s.warned = true
	slog.Warn(msg, "module", "aitrace", "path", s.path, "err", err)
}
