package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRepairCanonicalRAGDirectoryMigratesAndNormalizesLogs(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "book", "output", "novel")
	ragDir := filepath.Join(outputDir, "meta", "rag")
	if err := os.MkdirAll(ragDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := domain.RAGChunk{
		ID: "legacy-id", SourcePath: "book/output/novel/world_rules.md", SourceKind: "world",
		Hash: "legacy-hash", Text: "规则一旦成立就不能被弱召回覆盖。",
	}
	cfg := domain.RAGIndexConfig{EmbeddingModel: "local-test", EmbeddingProvider: "local", VectorDimension: 2, VectorStore: "local_json"}
	st := store.NewStore(outputDir)
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Config: cfg, Chunks: []domain.RAGChunk{legacy}, ChunkHashes: []string{"legacy-hash"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveVectorStore(domain.RAGVectorStore{Config: cfg, Points: []domain.RAGVectorPoint{{
		ID: legacy.ID, Hash: legacy.Hash, Vector: []float32{1, 0}, Chunk: legacy,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ragDir, "craft_recall_log.jsonl"), []byte(`{"event":1}{"event":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repair := repairCanonicalRAGDirectory(ragDir, "20260905T000000.000000000Z")
	if len(repair.Errors) != 0 || !repair.Changed || !repair.MigratedSchema || repair.NormalizedLogs != 1 {
		t.Fatalf("unexpected repair: %+v", repair)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if state.SchemaVersion != domain.CurrentRAGIndexSchemaVersion || len(state.Chunks) != 1 || state.Chunks[0].Hash == "legacy-hash" {
		t.Fatalf("state was not migrated: %+v", state)
	}
	vectors, err := st.RAG.LoadVectorStore()
	if err != nil || vectors == nil || len(vectors.Points) != 1 {
		t.Fatalf("LoadVectorStore: vectors=%+v err=%v", vectors, err)
	}
	if vectors.Points[0].Hash != state.Chunks[0].Hash || vectors.Points[0].Chunk.Hash != state.Chunks[0].Hash {
		t.Fatalf("vector was not remapped: point=%+v chunk=%+v", vectors.Points[0], state.Chunks[0])
	}
	logRaw, err := os.ReadFile(filepath.Join(ragDir, "craft_recall_log.jsonl"))
	if err != nil || string(logRaw) != "{\"event\":1}\n{\"event\":2}\n" {
		t.Fatalf("log was not normalized: %q err=%v", logRaw, err)
	}
	if _, err := os.Stat(filepath.Join(repair.Backup, "index_state.json")); err != nil {
		t.Fatalf("missing recovery backup: %v", err)
	}
	report, err := auditRAGTree(root)
	if err != nil || !report.CanonicalHealthy || !report.AllReadable {
		t.Fatalf("repaired tree is not healthy: %+v err=%v", report, err)
	}
	second := repairCanonicalRAGDirectory(ragDir, "20260905T000001.000000000Z")
	if len(second.Errors) != 0 || second.Changed || second.Backup != "" || second.NormalizedPoints != 0 {
		t.Fatalf("maintenance must be idempotent: %+v", second)
	}
}

func TestCompactRAGSnapshotsHardLinksOnlyIdenticalArtifacts(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "book", "output", "novel", "meta", "rag", "index_state.json")
	right := filepath.Join(root, "book", ".project-all", "g1", "output", "novel", "meta", "rag", "index_state.json")
	for _, path := range []string{left, right} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"schema_version":4,"chunks":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Unix(1_700_000_000, 123)
	for _, path := range []string{left, right} {
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	dry, err := compactRAGSnapshots(root, false)
	if err != nil || dry.RedundantFiles != 1 || dry.FilesLinked != 0 || dry.ReclaimableBytes == 0 {
		t.Fatalf("unexpected dry-run compaction: %+v err=%v", dry, err)
	}
	applied, err := compactRAGSnapshots(root, true)
	if err != nil || applied.FilesLinked != 1 || applied.BytesReclaimed == 0 {
		t.Fatalf("unexpected applied compaction: %+v err=%v", applied, err)
	}
	leftInfo, _ := os.Stat(left)
	rightInfo, _ := os.Stat(right)
	leftDev, leftIno := ragFileIdentity(leftInfo)
	rightDev, rightIno := ragFileIdentity(rightInfo)
	if leftDev != rightDev || leftIno != rightIno {
		t.Fatal("identical RAG artifacts were not hard-linked")
	}
}

func TestProjectWorkspaceRAGFilesUseSafeCopyOnWrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	ragPath := filepath.Join("meta", "rag", "index_state.json")
	for rel, content := range map[string]string{
		ragPath:        `{"schema_version":4,"chunks":[]}`,
		"outline.json": `[]`,
	} {
		path := filepath.Join(source, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyProjectAllWorkspace(source, target); err != nil {
		t.Fatal(err)
	}
	sourceRAG, _ := os.Stat(filepath.Join(source, ragPath))
	targetRAG, _ := os.Stat(filepath.Join(target, ragPath))
	_, sourceRAGIno := ragFileIdentity(sourceRAG)
	_, targetRAGIno := ragFileIdentity(targetRAG)
	if sourceRAGIno != targetRAGIno {
		t.Fatal("RAG snapshot should initially share an inode")
	}
	sourceOutline, _ := os.Stat(filepath.Join(source, "outline.json"))
	targetOutline, _ := os.Stat(filepath.Join(target, "outline.json"))
	_, sourceOutlineIno := ragFileIdentity(sourceOutline)
	_, targetOutlineIno := ragFileIdentity(targetOutline)
	if sourceOutlineIno == targetOutlineIno {
		t.Fatal("ordinary project artifacts must still receive fresh inodes")
	}

	targetStore := store.NewStore(target)
	chunk := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "world.md", SourceKind: "world", Text: "new target-only rule"})
	if err := targetStore.RAG.SaveIndexState(domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion, Chunks: []domain.RAGChunk{chunk}, ChunkHashes: []string{chunk.Hash},
	}); err != nil {
		t.Fatal(err)
	}
	sourceRaw, err := os.ReadFile(filepath.Join(source, ragPath))
	if err != nil || !strings.Contains(string(sourceRaw), `"chunks":[]`) {
		t.Fatalf("copy-on-write mutated source snapshot: %s err=%v", sourceRaw, err)
	}
	newTargetRAG, _ := os.Stat(filepath.Join(target, ragPath))
	_, newTargetIno := ragFileIdentity(newTargetRAG)
	if newTargetIno == sourceRAGIno {
		t.Fatal("atomic RAG write did not detach the target inode")
	}
}
