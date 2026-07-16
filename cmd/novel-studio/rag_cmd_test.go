package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestChunksFromRAGFileBuildsMethodOnlySafeRewriteSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deconstruction-library", "review-calibration", "novel-craft-methodology", "11-章节技法.md")
	uniqueRaw := "独特原文前缀甲乙丙：某角色照着样例台词完整说出不应进入计划的句子。"
	text := "# 对白技法\n\n" + uniqueRaw + " 对白要用打断和潜台词改变信息释放与主动权，而不是整段说明。"
	chunks := chunksFromRAGFile(path, "", text, 500)
	if len(chunks) != 1 {
		t.Fatalf("chunks=%d, want 1", len(chunks))
	}
	chunk := chunks[0]
	if got := chunk.Metadata["summary_origin"]; got != rag.SummaryOriginDerivedMethodMetadata {
		t.Fatalf("summary_origin=%v", got)
	}
	if strings.Contains(chunk.Summary, uniqueRaw) || strings.HasPrefix(uniqueRaw, chunk.Summary) {
		t.Fatalf("safe rewrite summary copied raw benchmark prose: %q", chunk.Summary)
	}
	result := rag.NewCraftCatalog(chunks).RecallWithOptions(
		rag.CraftFieldDialogue, "对白 打断 潜台词 信息释放", 3,
		rag.CraftRecallOptions{Stage: rag.StagePlan, RequireRelevant: true, SafeRewrite: true},
	)
	if result.NoMaterial || len(result.Hits) != 1 || strings.Contains(result.Hits[0].Chunk.Summary, uniqueRaw) {
		t.Fatalf("indexed safe rewrite recall lost method card or leaked raw text: %+v", result)
	}
}

func TestChunksFromRAGFileDropsEmbeddedBinaryBase64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deconstruction-library", "novel_all", "01-教程方法论", "带图教程.md")
	binary := make([]byte, 4096)
	for i := range binary {
		binary[i] = byte(i % 256)
	}
	encoded := base64.StdEncoding.EncodeToString(binary)
	text := "# 正常方法\n\n场景必须由人物选择推动，观察、判断、动作和后果要真正改变下一步。\n\n" +
		"# 内嵌图片\n\n![图](data:image/png;base64," + encoded + ")"
	chunks := chunksFromRAGFile(path, "", text, 900)
	if len(chunks) != 1 || !strings.Contains(chunks[0].Text, "人物选择") {
		t.Fatalf("embedded binary was indexed or surrounding prose was lost: count=%d chunks=%+v", len(chunks), chunks)
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk.Text, encoded[:200]) {
			t.Fatal("base64 image payload survived RAG chunk filtering")
		}
	}
	// Long normal text and encoded *text* are not binary payloads.
	if ragChunkLooksEncodedBlob(strings.Repeat("methodology keeps human choices visible ", 30)) {
		t.Fatal("ordinary English prose was misclassified as an encoded blob")
	}
	if ragChunkLooksEncodedBlob(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("human readable method card "), 40))) {
		t.Fatal("base64-encoded textual evidence was misclassified as binary")
	}
}

func TestBuildLocalRAGIndexUsesCurrentProjectAndSkipsReferenceLibraries(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output", "novel")
	inputDir := filepath.Join(root, "input")
	refDir := filepath.Join(root, "拆文库", "豆瓣-言情", "参考", "样本")
	benchmarkDir := filepath.Join(root, "对标", "样本")
	mustWriteRAGTestFile(t, filepath.Join(root, "prompt.md"), "只写澄港本项目的数据审计故事，主线围绕药盒订单、照护数据和家庭压力推进，不允许外部参考库成为事实来源。")
	mustWriteRAGTestFile(t, filepath.Join(inputDir, "01_项目圣经.md"), "# 项目圣经\n\n许闻溪在澄港处理数据审计，人物关系与家庭照护压力必须持续回到行动。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "outline.md"), "# 大纲\n\n## 第 12 章：药盒订单\n\n邱梅药品配送订单被证明参与模型画像，许闻溪必须保全订单链路。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "book_world.json"), `{"summary":"澄港旧城、照护中心和药盒配送链互相牵连。"}`)
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "meta", "chapter_progress.json"), `{"next_plan":{"chapter":12,"core_event":"保全药盒订单证据"}}`)
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "meta", "project_progress.json"), `{"next_chapter_actions":["核对药盒订单的资源归属"]}`)
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "meta", "writing_assets.json"), `{"features":[{"id":"a","name":"证据物件推进","category":"structure","description":"让药盒订单承担新信息","enabled":true}]}`)
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "source_note.md"), "# 来源说明\n\n位置：novel-studio/拆文库/旧参考包\n\n这类外部来源痕迹不得进入写作 RAG。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "outline.full67.pre-dynamic-volume.md"), "# 旧备份\n\n这份旧文件不得进入 RAG。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "她的第二算法_前10章_20260703_pipeline.txt"), "旧交付文本不得进入 RAG。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "项目_pipeline_ch01_confirm.txt"), "导出确认包正文不得进入 RAG。")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "meta", "backups", "old", "01.md"), "# 旧正文备份\n\n这份旧正文备份不得进入 RAG。")
	mustWriteRAGTestFile(t, filepath.Join(refDir, "写作要素拆解.md"), "# 写作要素拆解\n\n每章应让关系张力落到具体选择，证据物件必须改变人物下一步行动。")
	mustWriteRAGTestFile(t, filepath.Join(benchmarkDir, "文风.md"), "# 对标文风\n\n这份对标参考不得进入 RAG。")

	result, err := buildLocalRAGIndex(outputDir, []string{root}, 240, 20)
	if err != nil {
		t.Fatalf("buildLocalRAGIndex returned error: %v", err)
	}
	if len(result.State.Chunks) == 0 {
		t.Fatal("expected chunks")
	}
	var hasPrompt, hasInput, hasOutline, hasBookWorld bool
	var hasLedger bool // 活动台账（chapter_progress/project_progress/writing_assets）按门禁不得入库
	for _, chunk := range result.State.Chunks {
		if strings.Contains(chunk.SourcePath, "full67") || strings.Contains(chunk.SourcePath, "前10章") ||
			strings.Contains(chunk.SourcePath, "pipeline_ch01_confirm") || strings.Contains(chunk.SourcePath, "meta/backups") {
			t.Fatalf("stale source should not be indexed: %s", chunk.SourcePath)
		}
		if strings.Contains(chunk.SourcePath, "拆文库") || strings.Contains(chunk.SourcePath, "对标") || chunk.SourceKind == "deconstruction" {
			t.Fatalf("reference source should not be indexed: %+v", chunk)
		}
		if strings.Contains(chunk.Text, "拆文库") || strings.Contains(chunk.Summary, "拆文库") {
			t.Fatalf("reference marker should not be indexed: %+v", chunk)
		}
		if strings.Contains(chunk.SourcePath, "prompt.md") {
			hasPrompt = true
		}
		if strings.Contains(chunk.SourcePath, "01_项目圣经.md") {
			hasInput = true
		}
		if strings.Contains(chunk.SourcePath, "outline.md") {
			hasOutline = true
		}
		if strings.Contains(chunk.SourcePath, "book_world.json") {
			hasBookWorld = true
		}
		if strings.Contains(chunk.SourcePath, "chapter_progress.json") ||
			strings.Contains(chunk.SourcePath, "project_progress.json") ||
			strings.Contains(chunk.SourcePath, "writing_assets.json") {
			hasLedger = true
		}
	}
	if !hasPrompt || !hasInput || !hasOutline {
		t.Fatalf("expected prompt, input and outline chunks, got prompt=%v input=%v outline=%v", hasPrompt, hasInput, hasOutline)
	}
	if !hasBookWorld {
		t.Fatalf("expected book_world chunks")
	}
	if hasLedger {
		t.Fatalf("活动台账不得入 RAG（每章刷新，召回走文件直读）：chapter_progress/project_progress/writing_assets")
	}
}

func TestBuildLocalRAGIndexRejectsExplicitReferenceSource(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output", "novel")
	refDir := filepath.Join(root, "拆文库", "豆瓣-言情", "参考", "样本")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "outline.md"), "# 大纲\n\n只索引当前项目。")
	mustWriteRAGTestFile(t, filepath.Join(refDir, "写作要素拆解.md"), "# 写作要素拆解\n\n不得进入 RAG。")

	_, err := buildLocalRAGIndex(outputDir, []string{refDir}, 240, 20)
	if err == nil || !strings.Contains(err.Error(), "不允许引用") {
		t.Fatalf("expected explicit reference source rejection, got %v", err)
	}
}

func TestRefreshAutoRAGCollectionForOutputDirRekeysAutoCollection(t *testing.T) {
	oldDir := filepath.Join(t.TempDir(), "output", "novel")
	newDir := filepath.Join(t.TempDir(), "data", "runs", "鬼城", "output", "novel")
	cfg := bootstrap.Config{OutputDir: oldDir}
	cfg.RAG.Qdrant.Collection = rag.CollectionName("novel_studio", oldDir)

	refreshAutoRAGCollectionForOutputDir(&cfg, newDir, false)

	want := rag.CollectionName("novel_studio", newDir)
	if cfg.OutputDir != newDir || cfg.RAG.Qdrant.Collection != want {
		t.Fatalf("OutputDir=%q Collection=%q, want %q %q", cfg.OutputDir, cfg.RAG.Qdrant.Collection, newDir, want)
	}
}

func TestRefreshAutoRAGCollectionForOutputDirRekeysStaleDefaultWhenUnconfigured(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: filepath.Join(t.TempDir(), "output", "novel")}
	cfg.RAG.Qdrant.Collection = "novel_studio_3cfd597a1235"
	newDir := filepath.Join(t.TempDir(), "data", "runs", "项目", "output", "novel")

	refreshAutoRAGCollectionForOutputDir(&cfg, newDir, false)

	want := rag.CollectionName("novel_studio", newDir)
	if cfg.OutputDir != newDir || cfg.RAG.Qdrant.Collection != want {
		t.Fatalf("OutputDir=%q Collection=%q, want %q %q", cfg.OutputDir, cfg.RAG.Qdrant.Collection, newDir, want)
	}
}

func TestRefreshAutoRAGCollectionForOutputDirPreservesExplicitCollection(t *testing.T) {
	cfg := bootstrap.Config{OutputDir: filepath.Join(t.TempDir(), "output", "novel")}
	cfg.RAG.Qdrant.Collection = "shared_manual_collection"
	newDir := filepath.Join(t.TempDir(), "data", "runs", "项目", "output", "novel")

	refreshAutoRAGCollectionForOutputDir(&cfg, newDir, true)

	if cfg.OutputDir != newDir || cfg.RAG.Qdrant.Collection != "shared_manual_collection" {
		t.Fatalf("OutputDir=%q Collection=%q", cfg.OutputDir, cfg.RAG.Qdrant.Collection)
	}
}

func TestParseBuildRAGFlagsBackfillsChaptersByDefault(t *testing.T) {
	flags, extra, err := parseBuildRAGFlags(nil)
	if err != nil {
		t.Fatalf("parseBuildRAGFlags: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("unexpected extra args: %v", extra)
	}
	if !flags.BackfillChapters {
		t.Fatal("build-rag should backfill chapter facts by default")
	}
}

func TestParseBuildRAGFlagsAllowsDisablingChapterBackfill(t *testing.T) {
	flags, extra, err := parseBuildRAGFlags([]string{"--backfill-chapters=false"})
	if err != nil {
		t.Fatalf("parseBuildRAGFlags: %v", err)
	}
	if len(extra) != 0 {
		t.Fatalf("unexpected extra args: %v", extra)
	}
	if flags.BackfillChapters {
		t.Fatal("expected --backfill-chapters=false to disable chapter backfill")
	}
}

func TestEffectiveRAGEmbeddingBuildConcurrencySerializesLocalGGUF(t *testing.T) {
	got := effectiveRAGEmbeddingBuildConcurrency(bootstrap.RAGEmbeddingConfig{
		LocalGGUF:        "models/embedding/Qwen3-Embedding-0.6B-Q8_0.gguf",
		BuildConcurrency: 4,
	})
	if got != 1 {
		t.Fatalf("local GGUF embedding should be serialized, got %d", got)
	}
	got = effectiveRAGEmbeddingBuildConcurrency(bootstrap.RAGEmbeddingConfig{BuildConcurrency: 4})
	if got != 4 {
		t.Fatalf("remote embedding concurrency should be preserved, got %d", got)
	}
}

func TestEnsureDefaultRAGIndexSanitizesExistingReferenceChunks(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output", "novel")
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{
			{
				ID:         "bad",
				SourcePath: "拆文库/样本/写作要素拆解.md",
				SourceKind: "deconstruction",
				Text:       "参考库内容",
			},
			{
				ID:         "good",
				SourcePath: "summaries/01.json",
				SourceKind: "chapter_summary_facts",
				Text:       "本书第一章事实",
			},
		},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	if err := ensureDefaultRAGIndex(outputDir); err != nil {
		t.Fatalf("ensureDefaultRAGIndex: %v", err)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if len(state.Chunks) != 1 || state.Chunks[0].ID != "good" {
		t.Fatalf("expected only project chunk to remain, got %+v", state.Chunks)
	}
}

func TestEnsureDefaultRAGIndexSanitizesExistingVectorStore(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output", "novel")
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "characters.json"), `[{"name":"许闻溪"}]`)
	goodChunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         "good",
		SourcePath: "summaries/01.json",
		SourceKind: "chapter_summary_facts",
		Text:       "许闻溪在发布会后拒绝空白确认单。",
	})
	badChunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         "bad",
		SourcePath: "summaries/00.json",
		SourceKind: "chapter_summary_facts",
		Text:       "许闻溪追查算法审计证据链。",
	})
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{goodChunk, badChunk},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := st.RAG.SaveVectorStore(domain.RAGVectorStore{
		Config: domain.RAGIndexConfig{VectorStore: "local_json"},
		Points: []domain.RAGVectorPoint{
			{ID: "good", Hash: goodChunk.Hash, Vector: []float32{0.1}, Chunk: goodChunk},
			{ID: "bad", Hash: badChunk.Hash, Vector: []float32{0.2}, Chunk: badChunk},
			{ID: "orphan", Hash: "not-in-index", Vector: []float32{0.3}, Chunk: domain.RAGChunk{ID: "orphan", Text: "旧索引孤点"}},
		},
	}); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}

	if err := ensureDefaultRAGIndex(outputDir); err != nil {
		t.Fatalf("ensureDefaultRAGIndex: %v", err)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if len(state.Chunks) != 1 || state.Chunks[0].ID != "good" {
		t.Fatalf("expected only clean index chunk, got %+v", state.Chunks)
	}
	vectorStore, err := st.RAG.LoadVectorStore()
	if err != nil {
		t.Fatalf("LoadVectorStore: %v", err)
	}
	if vectorStore == nil || len(vectorStore.Points) != 1 || vectorStore.Points[0].ID != "good" {
		t.Fatalf("expected only clean vector point, got %+v", vectorStore)
	}
}

func TestBackfillChapterRAGUpsertsGeneratedChapterSummaries(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output", "novel")
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "chapters", "01.md"), "# 第1章\n\n正文")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "chapters", "01.md.pre-rewrite.md"), "# 第1章旧备份\n\n不应被当作章节")
	mustWriteRAGTestFile(t, filepath.Join(outputDir, "chapters", "02.md"), "# 第2章\n\n正文")
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "江烬保全午夜欠费单。",
		Characters: []string{"江烬"},
		KeyEvents:  []string{"午夜欠费单"},
	}); err != nil {
		t.Fatalf("SaveSummary 1: %v", err)
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    2,
		Summary:    "周行舟小超市接入夜租交易。",
		Characters: []string{"周行舟"},
		KeyEvents:  []string{"夜租交易"},
	}); err != nil {
		t.Fatalf("SaveSummary 2: %v", err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		Config: domain.RAGIndexConfig{Collection: "local_keyword"},
		Chunks: []domain.RAGChunk{{
			ID:         "old",
			SourcePath: "summaries/01.json",
			SourceKind: "chapter_summary_facts",
			Text:       "旧事实",
		}},
	}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}

	n, err := backfillChapterRAG(outputDir, 1, 0)
	if err != nil {
		t.Fatalf("backfillChapterRAG: %v", err)
	}
	if n != 2 {
		t.Fatalf("backfilled chapters = %d, want 2", n)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	counts := map[string]int{}
	var foundUpdated bool
	for _, chunk := range state.Chunks {
		counts[chunk.SourcePath]++
		if chunk.SourcePath == "summaries/01.json" && strings.Contains(chunk.Text, "午夜欠费单") && !strings.Contains(chunk.Text, "旧事实") {
			foundUpdated = true
		}
	}
	if counts["summaries/01.json"] != 1 || counts["summaries/02.json"] != 1 || !foundUpdated {
		t.Fatalf("unexpected chapter rag chunks counts=%v foundUpdated=%v", counts, foundUpdated)
	}
	indexPath := filepath.Join(outputDir, "meta", "rag", "index_state.json")
	before, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := backfillChapterRAG(outputDir, 1, 0); err != nil {
		t.Fatalf("second backfillChapterRAG: %v", err)
	}
	after, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("unchanged chapter facts rewrote index: before=%s after=%s", before.ModTime(), after.ModTime())
	}
}

func TestPipelineRAGArtifactsReusableSkipsUnchangedFactEmbeddings(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config: domain.RAGIndexConfig{
			EmbeddingProvider: "codex",
			EmbeddingModel:    "qwen3-embedding-0.6b",
			VectorStore:       "qdrant",
			VectorDimension:   3,
			Collection:        "novel_test",
			QdrantURL:         "http://127.0.0.1:6333",
		},
		Chunks: []domain.RAGChunk{
			{ID: "fact", Hash: "fact-hash", SourceKind: "chapter_summary_facts", Text: "当前项目事实"},
			{ID: "craft", Hash: "craft-hash", SourceKind: "craft_technique", Text: "只走 BM25 的写作手法"},
		},
	}
	vectorStore := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "fact-hash", Vector: []float32{1, 2, 3}}},
	}
	ok, reason, count := pipelineRAGArtifactsReusable(
		state,
		vectorStore,
		bootstrap.RAGEmbeddingConfig{Provider: "codex", Model: "qwen3-embedding-0.6b"},
		bootstrap.RAGQdrantConfig{URL: "http://127.0.0.1:6333", Collection: "novel_test"},
		true,
	)
	if !ok || count != 1 {
		t.Fatalf("expected reusable fact embedding, ok=%v count=%d reason=%s", ok, count, reason)
	}
}

func TestPipelineRAGArtifactsReusableRejectsChangedChunkHash(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config: domain.RAGIndexConfig{
			EmbeddingProvider: "codex", EmbeddingModel: "embed", VectorStore: "qdrant",
			VectorDimension: 2, Collection: "novel_test", QdrantURL: "http://127.0.0.1:6333",
		},
		Chunks: []domain.RAGChunk{{ID: "fact", Hash: "new-hash", SourceKind: "chapter_summary_facts", Text: "new"}},
	}
	vectorStore := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "old-hash", Vector: []float32{1, 2}}},
	}
	ok, reason, _ := pipelineRAGArtifactsReusable(
		state,
		vectorStore,
		bootstrap.RAGEmbeddingConfig{Provider: "codex", Model: "embed"},
		bootstrap.RAGQdrantConfig{URL: "http://127.0.0.1:6333", Collection: "novel_test"},
		true,
	)
	if ok || !strings.Contains(reason, "过期 chunk hash") {
		t.Fatalf("changed hash must force rebuild, ok=%v reason=%s", ok, reason)
	}
}

func TestPipelineRAGLocalArtifactsReusableAcrossBackendChange(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config: domain.RAGIndexConfig{
			EmbeddingProvider: "codex", EmbeddingModel: "embed", VectorDimension: 2,
			VectorStore: "qdrant", Collection: "old_collection", QdrantURL: "http://127.0.0.1:6333",
		},
		Chunks: []domain.RAGChunk{{ID: "fact", Hash: "fact-hash", SourceKind: "chapter_summary_facts", Text: "fact"}},
	}
	vectorStore := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "fact-hash", Vector: []float32{1, 2}}},
	}
	localOK, reason, count := pipelineRAGLocalArtifactsReusable(state, vectorStore, bootstrap.RAGEmbeddingConfig{Provider: "codex", Model: "embed"})
	if !localOK || count != 1 {
		t.Fatalf("local vectors should remain reusable: ok=%v reason=%s count=%d", localOK, reason, count)
	}
	fullOK, _, _ := pipelineRAGArtifactsReusable(
		state, vectorStore,
		bootstrap.RAGEmbeddingConfig{Provider: "codex", Model: "embed"},
		bootstrap.RAGQdrantConfig{URL: "http://127.0.0.1:6333", Collection: "new_collection"}, true,
	)
	if fullOK {
		t.Fatal("backend metadata change must require Qdrant replay")
	}
}

func TestPipelineRAGIncrementalPlanEmbedsOnlyMissingHashes(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config: domain.RAGIndexConfig{
			EmbeddingProvider: "local", EmbeddingModel: "qwen", VectorDimension: 3,
			VectorStore: "qdrant", Collection: "novel_test",
		},
		Chunks: []domain.RAGChunk{
			{ID: "fact-a", Hash: "hash-a", SourcePath: "a.json", SourceKind: "chapter_summary_facts", Text: "A"},
			{ID: "fact-b", Hash: "hash-b", SourcePath: "b.json", SourceKind: "chapter_review_facts", Text: "B"},
			{ID: "craft", Hash: "hash-craft", SourcePath: "craft.md", SourceKind: "craft_technique", Text: "手法"},
		},
	}
	vectors := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact-a", Hash: "hash-a", Vector: []float32{1, 2, 3}}},
	}
	missing, expected, ok, reason := pipelineRAGIncrementalPlan(
		state, vectors, bootstrap.RAGEmbeddingConfig{Provider: "local", Model: "qwen"},
	)
	if !ok || expected != 2 || len(missing) != 1 || missing[0].Hash != "hash-b" {
		t.Fatalf("expected one missing fact hash: ok=%v expected=%d missing=%+v reason=%s", ok, expected, missing, reason)
	}
	update := domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact-b", Hash: "hash-b", Vector: []float32{3, 2, 1}}},
	}
	merged := mergePipelineRAGVectorPoints(vectors, update, state.Chunks)
	if len(merged.Points) != 2 || merged.Points[0].ID != "fact-a" || merged.Points[1].ID != "fact-b" {
		t.Fatalf("incremental merge should preserve valid points and add missing point: %+v", merged.Points)
	}
}

func TestPipelineRAGIncrementalPlanReplacesChangedHashWithoutFullRebuild(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config: domain.RAGIndexConfig{
			EmbeddingProvider: "local", EmbeddingModel: "qwen", VectorDimension: 3,
		},
		Chunks: []domain.RAGChunk{{ID: "fact", Hash: "new-hash", SourcePath: "chapter.json", SourceKind: "chapter_summary_facts", Text: "新事实"}},
	}
	vectors := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "old-hash", Vector: []float32{1, 2, 3}}},
	}
	missing, expected, ok, reason := pipelineRAGIncrementalPlan(
		state, vectors, bootstrap.RAGEmbeddingConfig{Provider: "local", Model: "qwen"},
	)
	if !ok || expected != 1 || len(missing) != 1 || missing[0].Hash != "new-hash" || !strings.Contains(reason, "stale=1") {
		t.Fatalf("changed hash should use incremental replacement: ok=%v expected=%d missing=%+v reason=%s", ok, expected, missing, reason)
	}
	update := domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "new-hash", Vector: []float32{3, 2, 1}}},
	}
	merged := mergePipelineRAGVectorPoints(vectors, update, state.Chunks)
	if len(merged.Points) != 1 || merged.Points[0].Hash != "new-hash" {
		t.Fatalf("changed hash should replace stale point: %+v", merged.Points)
	}
}

func TestPipelineRAGIncrementalPlanCanPruneStaleWithoutEmbedding(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config:        domain.RAGIndexConfig{EmbeddingProvider: "local", EmbeddingModel: "qwen", VectorDimension: 2},
		Chunks:        []domain.RAGChunk{{ID: "keep", Hash: "keep-hash", SourceKind: "chapter_summary_facts", Text: "保留"}},
	}
	vectors := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{
			{ID: "keep", Hash: "keep-hash", Vector: []float32{1, 2}},
			{ID: "stale", Hash: "stale-hash", Vector: []float32{2, 1}},
		},
	}
	missing, expected, ok, reason := pipelineRAGIncrementalPlan(
		state, vectors, bootstrap.RAGEmbeddingConfig{Provider: "local", Model: "qwen"},
	)
	if !ok || expected != 1 || len(missing) != 0 || !strings.Contains(reason, "stale=1") {
		t.Fatalf("stale-only plan should prune without embedding: ok=%v expected=%d missing=%+v reason=%s", ok, expected, missing, reason)
	}
}

func TestPipelineRAGIncrementalPlanPrunesDuplicateHashWithoutEmbedding(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config:        domain.RAGIndexConfig{EmbeddingProvider: "local", EmbeddingModel: "qwen", VectorDimension: 2},
		Chunks: []domain.RAGChunk{
			{ID: "formal-review", Hash: "same-hash", SourcePath: "reviews/02.json", SourceKind: "review", Text: "同一份审核"},
			{ID: "draft-review", Hash: "same-hash", SourcePath: "reviews/drafts/02.json", SourceKind: "review", Text: "同一份审核"},
		},
	}
	vectors := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{
			{ID: "formal-review", Hash: "same-hash", Vector: []float32{1, 2}},
			{ID: "draft-review", Hash: "same-hash", Vector: []float32{1, 2}},
		},
	}
	missing, expected, ok, reason := pipelineRAGIncrementalPlan(
		state, vectors, bootstrap.RAGEmbeddingConfig{Provider: "local", Model: "qwen"},
	)
	if !ok || expected != 1 || len(missing) != 0 || !strings.Contains(reason, "duplicate=1") {
		t.Fatalf("duplicate hash should prune without embedding: ok=%v expected=%d missing=%+v reason=%s", ok, expected, missing, reason)
	}
	merged := mergePipelineRAGVectorPoints(vectors, domain.RAGVectorStore{Config: state.Config}, state.Chunks)
	if len(merged.Points) != 1 || merged.Points[0].Hash != "same-hash" {
		t.Fatalf("duplicate merge should keep one hash: %+v", merged.Points)
	}
}

func TestPipelineRAGIncrementalPlanRejectsInvalidExistingVector(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Config:        domain.RAGIndexConfig{EmbeddingProvider: "local", EmbeddingModel: "qwen", VectorDimension: 2},
		Chunks:        []domain.RAGChunk{{ID: "fact", Hash: "hash", SourceKind: "chapter_summary_facts", Text: "事实"}},
	}
	vectors := &domain.RAGVectorStore{
		Config: state.Config,
		Points: []domain.RAGVectorPoint{{ID: "fact", Hash: "hash", Vector: []float32{1}}},
	}
	if _, _, ok, reason := pipelineRAGIncrementalPlan(state, vectors, bootstrap.RAGEmbeddingConfig{Provider: "local", Model: "qwen"}); ok || !strings.Contains(reason, "维度") {
		t.Fatalf("invalid existing vectors must force full rebuild: ok=%v reason=%s", ok, reason)
	}
}

func TestMigrateRAGIndexSchemaRehashesSemanticContent(t *testing.T) {
	state := &domain.RAGIndexState{
		SchemaVersion: 2,
		Chunks: []domain.RAGChunk{
			{
				ID: "stable-id", Hash: "legacy-hash", SourcePath: "outline.md", SourceKind: "planning",
				Summary: "青山县返乡经营", Text: "第一间门店开始试营业。", Metadata: map[string]any{"chapter": 1},
			},
			{
				ID: "legacy-design", Hash: "legacy-design-hash",
				SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/11-章节技法.md",
				SourceKind: rag.CalibrationSourceKind,
				Summary:    "独特原文前缀不应继续充当摘要。",
				Text:       "对白通过打断和潜台词改变主动权。",
				Metadata:   map[string]any{"craft_facet": string(rag.FacetDialogue), "usage_stage": "plan"},
			},
			{
				ID: "curated-design", Hash: "curated-design-hash",
				SourcePath: "deconstruction-library/review-calibration/novel-craft-methodology/12-人工卡.md",
				SourceKind: rag.CalibrationSourceKind,
				Summary:    "人工方法卡；动作=保留作者明确编排的观察与后果；验收=人工定义不被迁移覆盖",
				Text:       "这段原文即使含有打断和潜台词，也不能替换人工摘要。",
				Metadata: map[string]any{
					"craft_facet":    string(rag.FacetDialogue),
					"usage_stage":    "plan",
					"summary_origin": rag.SummaryOriginCuratedMethod,
				},
			},
			{
				ID:         "broad-benchmark",
				SourcePath: "deconstruction-library/benchmarks/某本小说/正文.md",
				SourceKind: rag.BenchmarkSourceKind,
				Summary:    "宽参考库摘要不能被 schema 升级伪装成自动方法卡。",
				Text:       "人物和故事原文只供显式拆解。",
				Metadata:   map[string]any{"craft_facet": string(rag.FacetBenchmark), "usage_stage": "plan"},
			},
		},
		ChunkHashes: []string{"legacy-hash", "legacy-design-hash", "curated-design-hash", "broad-benchmark-hash"},
	}
	broadBefore := rag.RehashChunk(state.Chunks[3])
	state.Chunks[3] = broadBefore
	state.ChunkHashes[3] = broadBefore.Hash
	if !migrateRAGIndexSchema(state) {
		t.Fatal("legacy state should migrate")
	}
	if state.SchemaVersion != domain.CurrentRAGIndexSchemaVersion || state.Chunks[0].Hash == "legacy-hash" {
		t.Fatalf("state was not rehashed: %+v", state)
	}
	if state.Chunks[0].ID != "stable-id" || len(state.ChunkHashes) != 4 {
		t.Fatalf("migration broke stable ID/hash list: %+v", state)
	}
	design := state.Chunks[1]
	if design.Metadata["summary_origin"] != rag.SummaryOriginDerivedMethodMetadata || strings.Contains(design.Summary, "独特原文前缀") {
		t.Fatalf("schema migration retained raw design summary: %+v", design)
	}
	curated := state.Chunks[2]
	if curated.Summary != "人工方法卡；动作=保留作者明确编排的观察与后果；验收=人工定义不被迁移覆盖" ||
		curated.Metadata["summary_origin"] != rag.SummaryOriginCuratedMethod {
		t.Fatalf("schema migration replaced curated method summary: %+v", curated)
	}
	broad := state.Chunks[3]
	if broad.Summary != "宽参考库摘要不能被 schema 升级伪装成自动方法卡。" || broad.Hash != broadBefore.Hash {
		t.Fatalf("schema migration rewrote an out-of-scope benchmark: %+v", broad)
	}
	if migrateRAGIndexSchema(state) {
		t.Fatal("current state migration must be idempotent")
	}
}

func TestEnsureDefaultRAGIndexRemapsCompatibleLegacyVectors(t *testing.T) {
	outputDir := t.TempDir()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	legacy := domain.RAGChunk{
		ID: "stable-id", Hash: "legacy-hash", SourcePath: "outline.md", SourceKind: "planning",
		Summary: "青山县返乡经营", Text: "第一间门店开始试营业。", Metadata: map[string]any{"chapter": 1},
	}
	cfg := domain.RAGIndexConfig{EmbeddingProvider: "codex", EmbeddingModel: "embed", VectorDimension: 2}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Config: cfg, Chunks: []domain.RAGChunk{legacy}, ChunkHashes: []string{legacy.Hash}}); err != nil {
		t.Fatalf("SaveIndexState: %v", err)
	}
	if err := st.RAG.SaveVectorStore(domain.RAGVectorStore{
		Config: cfg,
		Points: []domain.RAGVectorPoint{{ID: legacy.ID, Hash: legacy.Hash, Vector: []float32{1, 0}, Chunk: legacy}},
	}); err != nil {
		t.Fatalf("SaveVectorStore: %v", err)
	}
	if err := ensureDefaultRAGIndex(outputDir); err != nil {
		t.Fatalf("ensureDefaultRAGIndex: %v", err)
	}
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	vectors, err := st.RAG.LoadVectorStore()
	if err != nil {
		t.Fatalf("LoadVectorStore: %v", err)
	}
	if len(vectors.Points) != 1 || vectors.Points[0].Hash != state.Chunks[0].Hash || vectors.Points[0].Hash == legacy.Hash {
		t.Fatalf("compatible legacy vector was not remapped: state=%+v vectors=%+v", state, vectors)
	}
}

func TestMergePendingRAGStateReplacesSource(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	old := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "outline.md", SourceKind: "planning", Text: "旧大纲"})
	keep := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "world_rules.md", SourceKind: "world", Text: "保留规则"})
	replacement := rag.NormalizeChunk(domain.RAGChunk{SourcePath: "outline.md", SourceKind: "planning", Text: "青山县新大纲"})
	state := &domain.RAGIndexState{Chunks: []domain.RAGChunk{old, keep}}
	mergePendingRAGState(st, state, []domain.RAGChunk{replacement})
	if len(state.Chunks) != 2 || len(state.ChunkHashes) != 2 {
		t.Fatalf("unexpected merged state: %+v", state)
	}
	for _, chunk := range state.Chunks {
		if chunk.Hash == old.Hash {
			t.Fatalf("old source survived pending merge: %+v", state.Chunks)
		}
	}
}

func TestMergePendingRAGStatePrefersPendingChunkForDuplicateContentHash(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	draft := rag.NormalizeChunk(domain.RAGChunk{
		ID: "draft-review", SourcePath: "reviews/drafts/02.json", SourceKind: "review", Text: "同一份审核结论",
	})
	formal := draft
	formal.ID = "formal-review"
	formal.SourcePath = "reviews/02.json"
	state := &domain.RAGIndexState{Chunks: []domain.RAGChunk{draft}}
	mergePendingRAGState(st, state, []domain.RAGChunk{formal})
	if len(state.Chunks) != 1 || state.Chunks[0].ID != "formal-review" || len(state.ChunkHashes) != 1 {
		t.Fatalf("pending formal chunk should replace duplicate draft content: %+v", state.Chunks)
	}
}

func mustWriteRAGTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
