package host

import (
	"context"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
)

func TestEnsureHostRAGSkipsServiceWhenLiveRAGDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := bootstrap.Config{
		OutputDir:      t.TempDir(),
		DisableLiveRAG: true,
		RAG: bootstrap.RAGConfig{
			Qdrant: bootstrap.RAGQdrantConfig{
				Enabled: true,
				URL:     "http://127.0.0.1:1",
			},
		},
	}

	enabled, err := ensureHostRAG(ctx, cfg)
	if err != nil {
		t.Fatalf("frozen render attempted to initialize live RAG: %v", err)
	}
	if enabled {
		t.Fatal("frozen render reported live RAG enabled")
	}
}
