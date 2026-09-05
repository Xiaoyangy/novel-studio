package rag

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLocalQdrantCommandKeepsRuntimeArtifactsInStorage(t *testing.T) {
	storage := filepath.Join(t.TempDir(), "qdrant-storage")
	cfg := QdrantServiceConfig{URL: "http://127.0.0.1:7333", StorageDir: storage}
	cmd := newLocalQdrantCommand("qdrant", cfg)
	if cmd.Dir != storage {
		t.Fatalf("command dir=%q want=%q", cmd.Dir, storage)
	}
	if !slices.Contains(cmd.Env, "QDRANT__STORAGE__STORAGE_PATH="+storage) {
		t.Fatalf("storage env missing from command: %v", cmd.Env)
	}
}
