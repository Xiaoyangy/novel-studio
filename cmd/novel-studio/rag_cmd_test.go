package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

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
