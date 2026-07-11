package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/rag"
)

func TestNormalizeOutputAndRAGRekeysCollectionAfterDirOverride(t *testing.T) {
	runRoot := t.TempDir()
	cfg := bootstrap.Config{}
	cfg.FillDefaults()
	stale := cfg.RAG.Qdrant.Collection

	if err := normalizeOutputAndRAGForInvocation(&cfg, runRoot, false); err != nil {
		t.Fatal(err)
	}

	wantDir := filepath.Join(runRoot, "output", "novel")
	wantCollection := rag.CollectionName("novel_studio", wantDir)
	if cfg.OutputDir != wantDir || cfg.RAG.Qdrant.Collection != wantCollection {
		t.Fatalf("OutputDir=%q Collection=%q, want %q %q (stale=%q)",
			cfg.OutputDir, cfg.RAG.Qdrant.Collection, wantDir, wantCollection, stale)
	}
}

func TestNormalizeOutputAndRAGPreservesExplicitCollection(t *testing.T) {
	runRoot := t.TempDir()
	cfg := bootstrap.Config{}
	cfg.FillDefaults()
	cfg.RAG.Qdrant.Collection = "shared_manual_collection"

	if err := normalizeOutputAndRAGForInvocation(&cfg, runRoot, true); err != nil {
		t.Fatal(err)
	}
	if cfg.RAG.Qdrant.Collection != "shared_manual_collection" {
		t.Fatalf("explicit collection changed to %q", cfg.RAG.Qdrant.Collection)
	}
}

func TestNormalizeOutputDirFromRunRoot(t *testing.T) {
	runRoot := t.TempDir()
	cfg := bootstrap.Config{}

	if err := normalizeOutputDirForInvocation(&cfg, runRoot); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(runRoot, "output", "novel")
	if cfg.OutputDir != want {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, want)
	}
}

func TestNormalizeOutputDirFromOutputNovel(t *testing.T) {
	runRoot := t.TempDir()
	outputDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{}

	if err := normalizeOutputDirForInvocation(&cfg, outputDir); err != nil {
		t.Fatal(err)
	}

	if cfg.OutputDir != outputDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, outputDir)
	}
}

func TestNormalizeOutputDirCollapsesNestedOutputNovel(t *testing.T) {
	runRoot := t.TempDir()
	outputDir := filepath.Join(runRoot, "output", "novel")
	nested := filepath.Join(outputDir, "output", "novel")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{}

	if err := normalizeOutputDirForInvocation(&cfg, nested); err != nil {
		t.Fatal(err)
	}

	if cfg.OutputDir != outputDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, outputDir)
	}
}

func TestNormalizeOutputDirRecognizesExistingNovelArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputDir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{}

	if err := normalizeOutputDirForInvocation(&cfg, outputDir); err != nil {
		t.Fatal(err)
	}

	if cfg.OutputDir != outputDir {
		t.Fatalf("OutputDir = %q, want %q", cfg.OutputDir, outputDir)
	}
}
