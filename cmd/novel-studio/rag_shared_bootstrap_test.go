package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

func newSharedRAGBootstrapFixture(t *testing.T) bootstrap.Config {
	t.Helper()
	root := t.TempDir()
	lib := filepath.Join(root, "deconstruction-library", "writing-techniques")
	if err := os.MkdirAll(filepath.Join(lib, "weapons"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "weapons", "长剑.md"), []byte("# 长剑设计\n长剑的重量影响持握、挥动和恢复时间，铸剑材料必须约束耐用性。"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{OutputDir: filepath.Join(root, "runs", "fresh", "output", "novel"), RAG: bootstrap.RAGConfig{CraftLibrary: lib}}
	if err := store.NewStore(cfg.OutputDir).Init(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPipelineRAGLoadsSharedCatalogBeforeAnyFoundation(t *testing.T) {
	cfg := newSharedRAGBootstrapFixture(t)
	// No provider/service can be reached. A design-only index must return its
	// precise readiness condition before even attempting an embedding probe.
	cfg.RAG.Embedding = bootstrap.RAGEmbeddingConfig{Enabled: true, Provider: "direct", Model: "unreachable", BaseURL: "http://127.0.0.1:1"}
	if err := ensurePipelineRAGReady(cfg); !errors.Is(err, errNoRAGFactChunks) {
		t.Fatalf("got %v, want missing facts", err)
	}
	if err := ensureArchitectRAGReady(cfg); err != nil {
		t.Fatalf("shared-only fresh Architect bootstrap failed: %v", err)
	}
	st := store.NewStore(cfg.OutputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil || len(state.Chunks) == 0 {
		t.Fatalf("shared catalog absent: state=%+v err=%v", state, err)
	}
	for _, chunk := range state.Chunks {
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			t.Fatalf("shared material became fact: %+v", chunk)
		}
	}
	if vectors, err := st.RAG.LoadVectorStore(); err != nil || vectors != nil {
		t.Fatalf("design bootstrap created vectors: %+v %v", vectors, err)
	}
	raw, err := toolspkg.NewCraftRecallTool(st).Execute(context.Background(), json.RawMessage(`{"field":"weapon","topic":"长剑","chapter":0}`))
	if err != nil {
		t.Fatalf("Architect craft recall failed: %v", err)
	}
	var result struct {
		NoMaterial bool              `json:"no_material"`
		Hits       []json.RawMessage `json:"hits"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.NoMaterial || len(result.Hits) == 0 {
		t.Fatalf("shared source unavailable to Architect: %s", raw)
	}
}

func TestSharedRAGBootstrapIsIncrementalAndPreservesFactVectors(t *testing.T) {
	cfg := newSharedRAGBootstrapFixture(t)
	st := store.NewStore(cfg.OutputDir)
	fact := rag.NormalizeChunk(domain.RAGChunk{ID: "fact:rule", SourcePath: "project/world.json", SourceKind: "world", Text: "本书夜间关闭城门。"})
	index := domain.RAGIndexState{SchemaVersion: domain.CurrentRAGIndexSchemaVersion, Config: domain.RAGIndexConfig{Collection: "book-facts", VectorDimension: 2}, Chunks: []domain.RAGChunk{fact}, ChunkHashes: []string{fact.Hash}}
	if err := st.RAG.SaveIndexState(index); err != nil {
		t.Fatal(err)
	}
	vectors := domain.RAGVectorStore{Config: index.Config, Points: []domain.RAGVectorPoint{{ID: fact.ID, Hash: fact.Hash, Chunk: fact, Vector: []float32{1, 0}}}}
	if err := st.RAG.SaveVectorStore(vectors); err != nil {
		t.Fatal(err)
	}
	vectorPath := filepath.Join(cfg.OutputDir, "meta", "rag", "vector_store.json")
	vectorBefore, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(cfg.OutputDir, "meta", "rag", "index_state.json")
	first, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("unchanged readiness rewrote the index")
	}
	if err := os.WriteFile(filepath.Join(cfg.RAG.CraftLibrary, "weapons", "长枪.md"), []byte("长枪攻击距离与转向空间相互约束，不能无代价穿透护甲；狭窄街巷会进一步限制突刺后的回收动作。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
		t.Fatal(err)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Chunks) != 3 || state.Config != index.Config || !reflect.DeepEqual(state.Chunks[0], fact) {
		t.Fatalf("unexpected merged index: %+v", state)
	}
	after, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(vectorBefore) != string(after) {
		t.Fatal("shared imports changed fact vectors")
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
		t.Fatal(err)
	}
	state, err = st.RAG.LoadIndexState()
	if err != nil || len(state.Chunks) != 3 {
		t.Fatalf("import duplicated chunks: %+v %v", state, err)
	}
}

func TestSharedRAGBootstrapDoesNotInventIndexWithoutMaterial(t *testing.T) {
	for _, kind := range []string{"unconfigured", "empty"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			cfg := bootstrap.Config{OutputDir: filepath.Join(root, "fresh", "output", "novel")}
			if err := store.NewStore(cfg.OutputDir).Init(); err != nil {
				t.Fatal(err)
			}
			if kind == "empty" {
				cfg.RAG.CraftLibrary = filepath.Join(root, "writing-techniques")
				if err := os.MkdirAll(cfg.RAG.CraftLibrary, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensurePipelineRAGReady(cfg); !errors.Is(err, errNoRAGSourceFiles) {
				t.Fatalf("got %v, want no source files", err)
			}
			if state, err := store.NewStore(cfg.OutputDir).RAG.LoadIndexState(); err != nil || state != nil {
				t.Fatalf("invented index: %+v %v", state, err)
			}
		})
	}
}

func TestPipelineRAGIndexesFoundationAlongsideSharedCatalog(t *testing.T) {
	for _, priorCatalog := range []bool{false, true} {
		t.Run(fmt.Sprint(priorCatalog), func(t *testing.T) {
			cfg := newSharedRAGBootstrapFixture(t)
			if priorCatalog {
				if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(cfg.OutputDir, "premise.md"), []byte("# 新书设定\n主角必须在天亮前核对夜租账单，否则会失去店铺的使用权；所有线索都应当来自实际见闻。"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ensurePipelineRAGReady(cfg); err != nil {
				t.Fatal(err)
			}
			state, err := store.NewStore(cfg.OutputDir).RAG.LoadIndexState()
			if err != nil {
				t.Fatal(err)
			}
			facts, design := 0, 0
			for _, chunk := range state.Chunks {
				if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
					design++
				} else {
					facts++
				}
			}
			if facts == 0 || design == 0 {
				t.Fatalf("foundation/catalog missed: facts=%d design=%d", facts, design)
			}
		})
	}
}

func TestSharedRAGBootstrapPreservesExistingEmptyIndexFailure(t *testing.T) {
	cfg := newSharedRAGBootstrapFixture(t)
	st := store.NewStore(cfg.OutputDir)
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{SchemaVersion: domain.CurrentRAGIndexSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := ensureArchitectRAGReady(cfg); err == nil {
		t.Fatal("configured shared library hid an existing empty index")
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil || len(state.Chunks) != 0 {
		t.Fatalf("empty index evidence changed: %+v %v", state, err)
	}
}

func TestSharedRAGBootstrapRetriesEmptySourcesWithoutBlockingReady(t *testing.T) {
	cfg := newSharedRAGBootstrapFixture(t)
	shortPath := filepath.Join(cfg.RAG.CraftLibrary, "weapons", "待补充.md")
	if err := os.WriteFile(shortPath, []byte("太短"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.RAG.CraftLibrary, "weapons", "空文件.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.RAG.CraftLibrary, "weapons", "不可召回.md"), []byte("仅登记来自 data/reference-library/other-book.md 的来源备注，该片段包含禁入来源标记，不得作为本书写作素材。"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := ensureArchitectRAGReady(cfg); err != nil {
			t.Fatalf("empty library source blocked ready: %v", err)
		}
	}
	st := store.NewStore(cfg.OutputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil || len(state.Chunks) != 1 {
		t.Fatalf("unexpected initial catalog: %+v %v", state, err)
	}
	indexPath := filepath.Join(cfg.OutputDir, "meta", "rag", "index_state.json")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("no usable new content rewrote the catalog")
	}
	if err := os.WriteFile(shortPath, []byte("# 长枪的真实边界\n长枪攻击距离与转向空间相互约束，不能无代价穿透护甲；狭窄街巷会限制突刺后的回收动作。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureArchitectRAGReady(cfg); err != nil {
		t.Fatal(err)
	}
	state, err = st.RAG.LoadIndexState()
	if err != nil || len(state.Chunks) != 2 {
		t.Fatalf("newly usable source not imported: %+v %v", state, err)
	}
	if err := ensureArchitectRAGReady(cfg); err != nil {
		t.Fatalf("remaining empty file blocked subsequent ready: %v", err)
	}
}

func TestNoRAGChunksRemainsErrorForFactIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "premise.md")
	if err := os.WriteFile(path, []byte("太短"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildLocalRAGIndex(dir, []string{path}, 900, 600); !errors.Is(err, errNoRAGSourceChunks) {
		t.Fatalf("missing typed empty-content error: %v", err)
	}
	if err := ensureDefaultRAGIndex(dir); !errors.Is(err, errNoRAGSourceChunks) {
		t.Fatalf("fact index swallowed empty-content error: %v", err)
	}
}

func TestSharedRAGBootstrapPreservesSourceReadErrors(t *testing.T) {
	cfg := newSharedRAGBootstrapFixture(t)
	broken := filepath.Join(cfg.RAG.CraftLibrary, "weapons", "无法读取.md")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.md"), broken); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := ensureConfiguredSharedRAGIndex(cfg); err == nil || errors.Is(err, errNoRAGSourceChunks) {
		t.Fatalf("source read failure was hidden: %v", err)
	}
	state, err := store.NewStore(cfg.OutputDir).RAG.LoadIndexState()
	if err != nil || state != nil {
		t.Fatalf("failed source scan partially committed an index: %+v %v", state, err)
	}
}
