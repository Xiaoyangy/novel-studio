package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestArchitectCanBootstrapBeforeRAGSourcesExist(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output", "novel")
	st := store.NewStore(output)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{OutputDir: output}
	if err := ensureArchitectRAGReady(cfg); err != nil {
		t.Fatalf("new project must reach Architect before it can have foundation: %v", err)
	}
	if err := ensurePipelineRAGReady(cfg); err == nil {
		t.Fatal("planning readiness must still reject missing RAG")
	}
	if index, err := st.RAG.LoadIndexState(); err != nil || index != nil {
		t.Fatalf("bootstrap must not invent an empty ready index: %+v %v", index, err)
	}
	if err := os.WriteFile(filepath.Join(output, "premise.md"), []byte(strings.Repeat("林澄在雨夜渡口发现油料账本被修改，必须在停航前保全证据。", 8)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureArchitectRAGReady(cfg); err != nil {
		t.Fatalf("foundation should be indexed on the next readiness check: %v", err)
	}
	if index, err := st.RAG.LoadIndexState(); err != nil || index == nil || len(index.Chunks) == 0 {
		t.Fatalf("foundation did not enter real index: %+v %v", index, err)
	}
}

func TestArchitectBootstrapDoesNotHideExistingOrBrokenEvidence(t *testing.T) {
	for name, pathAndContent := range map[string][2]string{
		"bad index":              {"meta/rag/index_state.json", "{"},
		"empty existing index":   {"meta/rag/index_state.json", `{ "schema_version": 3, "chunks": [] }`},
		"bad vectors":            {"meta/rag/vector_store.json", "{"},
		"empty existing vectors": {"meta/rag/vector_store.json", `{ "points": [] }`},
		"accepted chapter":       {"chapters/100.md", "# 第一百章\n这是已经生成的正文。"},
		"bad source":             {"premise.md", "   "},
	} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output", "novel")
			path := filepath.Join(output, pathAndContent[0])
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(pathAndContent[1]), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ensureArchitectRAGReady(bootstrap.Config{OutputDir: output}); err == nil {
				t.Fatal("existing/broken evidence was treated as a new empty project")
			}
		})
	}
}

func TestChapterNumbersIncludeLongBooksWithoutAliases(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.md", "09.md", "99.md", "100.md", "101.md", "1000.md", "00.md", "1.md", "001.md", "+10.md", "10-copy.md", "正文.md", "102.md.bak"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("正文"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "103.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := chapterNumbersFromFiles(dir)
	want := []int{1, 9, 99, 100, 101, 1000}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("chapterNumbersFromFiles = %v, %v; want %v", got, err, want)
	}
}
