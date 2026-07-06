package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

type sourceList []string

func (s *sourceList) String() string { return strings.Join(*s, ",") }
func (s *sourceList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v != "" {
		*s = append(*s, v)
	}
	return nil
}

type buildRAGFlags struct {
	Dir                string
	Sources            sourceList
	AddSources         sourceList
	MaxChunk           int
	MaxFiles           int
	ProbeChapter       int
	BackfillChapters   bool
	BackfillStart      int
	BackfillEnd        int
	WithEmbeddings     bool
	EmbeddingProvider  string
	EmbeddingModel     string
	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingAPIKeyEnv string
}

func hasBuildRAGFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--build-rag" {
			return true
		}
	}
	return false
}

func parseBuildRAGFlags(argv []string) (buildRAGFlags, []string, error) {
	fs := flag.NewFlagSet("build-rag", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --build-rag [--dir <output/novel>] [--source <path> ...] [--probe-chapter N]\n\n")
		fmt.Fprintf(os.Stderr, "构建本书本地 RAG 索引，写入 meta/rag/index_state.json/md。默认只索引当前项目的 prompt.md、input/*.md 与 output/novel 关键设定/账本文件；拒绝拆解库(deconstruction-library)、对标库和外部参考库。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f buildRAGFlags
	f.MaxChunk = 900
	f.MaxFiles = 2000
	f.BackfillChapters = true
	fs.StringVar(&f.Dir, "dir", "", "小说输出目录；为空时使用配置中的 OutputDir")
	fs.Var(&f.Sources, "source", "索引来源文件或目录；可重复。指定后**替换**默认项目来源")
	fs.Var(&f.AddSources, "add-source", "在默认项目来源基础上**追加**的来源文件或目录；可重复（如写作手法库）")
	fs.IntVar(&f.MaxChunk, "max-chunk-runes", f.MaxChunk, "单个 RAG chunk 的最大字符数")
	fs.IntVar(&f.MaxFiles, "max-files", f.MaxFiles, "最多索引文件数，避免误扫正文库")
	fs.BoolVar(&f.BackfillChapters, "backfill-chapters", f.BackfillChapters, "构建后按章节号从 summaries/ 与 chapters/ 回填已完成章节事实包（可设为 false 仅重建项目资料索引）")
	fs.IntVar(&f.BackfillStart, "backfill-start", 1, "章节回填起始章号")
	fs.IntVar(&f.BackfillEnd, "backfill-end", 0, "章节回填结束章号；0 表示自动发现全部已生成章节")
	fs.IntVar(&f.ProbeChapter, "probe-chapter", 0, "构建后立即用 novel_context 探测指定章节的 RAG 召回并写 retrieval_trace")
	fs.BoolVar(&f.WithEmbeddings, "with-embeddings", false, "同时调用 OpenAI 兼容 embeddings 接口，写入本机 Qdrant，并落 meta/rag/vector_store.json 作为 fallback")
	fs.StringVar(&f.EmbeddingProvider, "embedding-provider", "", "embedding provider；为空时使用 rag.embedding.provider 或顶层 provider")
	fs.StringVar(&f.EmbeddingModel, "embedding-model", "", "embedding 模型；为空时使用 rag.embedding.model 或 text-embedding-3-small")
	fs.StringVar(&f.EmbeddingBaseURL, "embedding-base-url", "", "embedding OpenAI 兼容 base_url；为空时继承 provider.base_url")
	fs.StringVar(&f.EmbeddingAPIKey, "embedding-api-key", "", "embedding API key；为空时继承 provider.api_key 或 --embedding-api-key-env")
	fs.StringVar(&f.EmbeddingAPIKeyEnv, "embedding-api-key-env", "", "从环境变量读取 embedding API key")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func buildRAGPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseBuildRAGFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseBuildRAGFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--build-rag 不接受额外参数：%v", extra)
	}
	if flags.MaxChunk < 200 {
		return fmt.Errorf("--max-chunk-runes 不能小于 200")
	}
	if flags.MaxFiles <= 0 {
		return fmt.Errorf("--max-files 必须大于 0")
	}

	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil && flags.Dir == "" {
		return err
	}
	dir := flags.Dir
	if dir == "" {
		dir = cfg.OutputDir
	}
	if dir == "" {
		dir = "output/novel"
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	refreshAutoRAGCollectionForOutputDir(&cfg, absDir, hasConfiguredRAGQdrantCollection(opts))
	sources := append([]string(nil), flags.Sources...)
	if len(sources) == 0 {
		sources = discoverDefaultRAGSources(absDir)
	}
	sources = append(sources, flags.AddSources...)
	sources = appendConfiguredSharedLibraries(sources, cfg)
	result, err := buildLocalRAGIndex(absDir, sources, flags.MaxChunk, flags.MaxFiles)
	if err != nil {
		return err
	}
	st := store.NewStore(absDir)
	if err := st.RAG.SaveIndexState(result.State); err != nil {
		return err
	}
	backfilled := 0
	if flags.BackfillChapters {
		backfilled, err = backfillChapterRAG(absDir, flags.BackfillStart, flags.BackfillEnd)
		if err != nil {
			return err
		}
		if state, err := st.RAG.LoadIndexState(); err == nil && state != nil {
			result.State = *state
		} else if err != nil {
			return err
		}
	}
	vectorEmbedded := 0
	vectorWritten := 0
	vectorEnabled := false
	embeddingOverride := buildRAGEmbeddingOverride(flags)
	if _, enabled := bootstrap.ResolveRAGEmbeddingConfig(cfg, embeddingOverride); enabled {
		vectorEnabled = true
		embeddingResult, vectorStore, err := buildRAGEmbeddings(context.Background(), cfg, embeddingOverride, result.State.Chunks)
		if err != nil {
			return err
		}
		result.State = embeddingResult.State
		vectorEmbedded = embeddingResult.Embedded
		vectorWritten = embeddingResult.Written
		if err := st.RAG.SaveIndexState(result.State); err != nil {
			return err
		}
		if err := st.RAG.SaveVectorStore(vectorStore); err != nil {
			return err
		}
	}
	resetRAGTrace(absDir)
	fmt.Fprintf(os.Stderr, "[build-rag] 已写入 %s\n", filepath.Join(absDir, "meta", "rag", "index_state.json"))
	fmt.Fprintf(os.Stderr, "[build-rag] 来源文件 %d 个，chunks=%d，跳过重复=%d\n", result.Files, len(result.State.Chunks), result.SkippedDup)
	if flags.BackfillChapters {
		fmt.Fprintf(os.Stderr, "[build-rag] 章节事实回填 %d 章\n", backfilled)
	}
	if vectorEnabled {
		fmt.Fprintf(os.Stderr, "[build-rag] embeddings=%d，vector_points=%d，vector_store=%s，collection=%s，fallback=%s\n",
			vectorEmbedded, vectorWritten, result.State.Config.VectorStore, result.State.Config.Collection, filepath.Join(absDir, "meta", "rag", "vector_store.json"))
	}
	fmt.Fprintf(os.Stdout, "%s\n", filepath.Join(absDir, "meta", "rag", "index_state.md"))

	if flags.ProbeChapter > 0 {
		if cfg.Style == "" {
			cfg.Style = "default"
		}
		if bundle.References.RAGWritingGuidelines == "" {
			bundle = assets.Load(cfg.Style)
		}
		embedder, _, _ := bootstrap.NewRAGEmbedderWithOverride(cfg, embeddingOverride)
		var searcher rag.VectorSearcher
		if qdrantClient, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false); err == nil && enabled {
			searcher = qdrantClient
		}
		count, err := probeRAGRecall(absDir, cfg.Style, bundle.References, flags.ProbeChapter, embedder, searcher)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[build-rag] probe chapter %d rag_recall=%d\n", flags.ProbeChapter, count)
		if count == 0 {
			return fmt.Errorf("probe chapter %d 未召回 RAG；索引已生成，但当前章节查询未命中", flags.ProbeChapter)
		}
	}
	return nil
}

func refreshAutoRAGCollectionForOutputDir(cfg *bootstrap.Config, outputDir string, collectionConfigured bool) {
	cfg.OutputDir = outputDir
	if !collectionConfigured {
		cfg.RAG.Qdrant.Collection = rag.CollectionName("novel_studio", outputDir)
	}
}

func hasConfiguredRAGQdrantCollection(opts cliOptions) bool {
	paths := []string{
		bootstrap.DefaultConfigPath(),
		filepath.Join(".novel-studio", "config.json"),
		opts.ConfigPath,
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		cfg, err := bootstrap.LoadConfigFile(path)
		if err != nil {
			continue
		}
		if strings.TrimSpace(cfg.RAG.Qdrant.Collection) != "" {
			return true
		}
	}
	return false
}

func ensureDefaultRAGIndex(outputDir string) error {
	st := store.NewStore(outputDir)
	if state, err := st.RAG.LoadIndexState(); err == nil && state != nil && len(state.Chunks) > 0 {
		removed := sanitizeRAGIndexState(state)
		if removed > 0 {
			if len(state.Chunks) > 0 {
				if err := st.RAG.SaveIndexState(*state); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "[build-rag] 已移除旧 RAG 参考库 chunks=%d\n", removed)
				return nil
			}
			fmt.Fprintf(os.Stderr, "[build-rag] 旧 RAG 索引仅含参考库 chunks=%d，重新构建当前项目索引\n", removed)
		} else {
			return nil
		}
	} else if err != nil {
		return err
	}
	sources := discoverDefaultRAGSources(outputDir)
	result, err := buildLocalRAGIndex(outputDir, sources, 900, 600)
	if err != nil {
		return err
	}
	if err := st.RAG.SaveIndexState(result.State); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[build-rag] 自动构建 RAG：来源文件 %d 个，chunks=%d\n", result.Files, len(result.State.Chunks))
	return nil
}

func ensurePipelineRAGReady(cfg bootstrap.Config) error {
	if strings.TrimSpace(cfg.OutputDir) == "" {
		cfg.OutputDir = filepath.Join("output", "novel")
	}
	if abs, err := filepath.Abs(cfg.OutputDir); err == nil {
		cfg.OutputDir = abs
	}
	if err := ensureDefaultRAGIndex(cfg.OutputDir); err != nil {
		return err
	}
	if _, enabled := bootstrap.ResolveRAGEmbeddingConfig(cfg, bootstrap.RAGEmbeddingConfig{}); !enabled {
		return nil
	}
	st := store.NewStore(cfg.OutputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return err
	}
	if state == nil || len(state.Chunks) == 0 {
		return fmt.Errorf("RAG index 为空，无法构建 Qdrant 向量索引")
	}
	result, vectorStore, err := buildRAGEmbeddings(context.Background(), cfg, bootstrap.RAGEmbeddingConfig{}, state.Chunks)
	if err != nil {
		return err
	}
	if err := st.RAG.SaveIndexState(result.State); err != nil {
		return err
	}
	if err := st.RAG.SaveVectorStore(vectorStore); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[build-rag] 写作前 RAG 检查完成：chunks=%d embeddings=%d vector_points=%d collection=%s\n", len(result.State.Chunks), result.Embedded, result.Written, result.State.Config.Collection)
	return nil
}

type localRAGBuildResult struct {
	State      domain.RAGIndexState
	Files      int
	SkippedDup int
}

func buildLocalRAGIndex(outputDir string, rawSources []string, maxChunkRunes, maxFiles int) (localRAGBuildResult, error) {
	outputDir = cleanAbsRAGPath(outputDir)
	files, err := collectRAGSourceFiles(outputDir, rawSources, maxFiles)
	if err != nil {
		return localRAGBuildResult{}, err
	}
	if len(files) == 0 {
		return localRAGBuildResult{}, fmt.Errorf("未找到可索引的 RAG 来源文件")
	}
	var chunks []domain.RAGChunk
	seen := map[string]struct{}{}
	skipped := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return localRAGBuildResult{}, fmt.Errorf("读取 RAG 来源失败 %s: %w", path, err)
		}
		for _, chunk := range chunksFromRAGFile(path, outputDir, string(data), maxChunkRunes) {
			chunk = rag.NormalizeChunk(chunk)
			if rag.IsForbiddenChunk(chunk) {
				continue
			}
			if chunk.Hash == "" {
				continue
			}
			if _, ok := seen[chunk.Hash]; ok {
				skipped++
				continue
			}
			seen[chunk.Hash] = struct{}{}
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) == 0 {
		return localRAGBuildResult{}, fmt.Errorf("RAG 来源文件存在，但没有切出有效 chunk")
	}
	hashes := make([]string, 0, len(seen))
	for h := range seen {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	return localRAGBuildResult{
		Files:      len(files),
		SkippedDup: skipped,
		State: domain.RAGIndexState{
			Config: domain.RAGIndexConfig{
				EmbeddingConcurrency:   0,
				QdrantWriteConcurrency: 0,
				Collection:             "local_keyword",
			},
			Chunks:      chunks,
			ChunkHashes: hashes,
			UpdatedAt:   time.Now().Format(time.RFC3339),
		},
	}, nil
}

func buildRAGEmbeddingOverride(flags buildRAGFlags) bootstrap.RAGEmbeddingConfig {
	enabled := flags.WithEmbeddings ||
		flags.EmbeddingProvider != "" ||
		flags.EmbeddingModel != "" ||
		flags.EmbeddingBaseURL != "" ||
		flags.EmbeddingAPIKey != "" ||
		flags.EmbeddingAPIKeyEnv != ""
	return bootstrap.RAGEmbeddingConfig{
		Enabled:   enabled,
		Provider:  flags.EmbeddingProvider,
		Model:     flags.EmbeddingModel,
		BaseURL:   flags.EmbeddingBaseURL,
		APIKey:    flags.EmbeddingAPIKey,
		APIKeyEnv: flags.EmbeddingAPIKeyEnv,
	}
}

func buildRAGEmbeddings(ctx context.Context, cfg bootstrap.Config, override bootstrap.RAGEmbeddingConfig, chunks []domain.RAGChunk) (rag.IndexResult, domain.RAGVectorStore, error) {
	embedder, enabled, err := bootstrap.NewRAGEmbedderWithOverride(cfg, override)
	if err != nil {
		return rag.IndexResult{}, domain.RAGVectorStore{}, err
	}
	if !enabled {
		return rag.IndexResult{}, domain.RAGVectorStore{}, fmt.Errorf("RAG embedding 未启用")
	}
	embCfg, _ := bootstrap.ResolveRAGEmbeddingConfig(cfg, override)
	if embCfg.BuildConcurrency <= 0 {
		embCfg.BuildConcurrency = 2
	}
	// Task 071：维度探针——切换模型前后可对比；哈希伪向量时提示升级为真语义模型。
	if probe, err := embedder.Embed(ctx, "维度探针"); err == nil {
		fmt.Fprintf(os.Stderr, "[build-rag] embedder=%s dims=%d（本次全量构建会重建 Qdrant collection 与 vector_store.json，维度变更自动生效）\n", embCfg.Model, len(probe))
		if strings.HasPrefix(strings.ToLower(embCfg.Model), "local-") {
			fmt.Fprintln(os.Stderr, "[build-rag] ⚠ 当前为哈希伪向量（离线兜底），长程语义召回必然偏弱：建议配置 rag.embedding.local_gguf 使用项目内 Qwen3-Embedding-0.6B")
		}
	} else {
		return rag.IndexResult{}, domain.RAGVectorStore{}, fmt.Errorf("embedding 维度探针失败（端点不可用？）: %w", err)
	}
	qdrantEnabled, err := bootstrap.EnsureRAGQdrant(ctx, cfg)
	if err != nil {
		return rag.IndexResult{}, domain.RAGVectorStore{}, err
	}
	var qdrantWriter rag.VectorWriter
	vectorStoreName := "local_json"
	collection := "local_vector"
	qdrantURL := ""
	if qdrantEnabled {
		client, _, err := bootstrap.NewRAGQdrantClient(cfg, true)
		if err != nil {
			return rag.IndexResult{}, domain.RAGVectorStore{}, err
		}
		qdrantWriter = client
		vectorStoreName = "qdrant"
		collection = client.Collection()
		qdrantURL = client.URL()
	}
	indexCfg := domain.RAGIndexConfig{
		EmbeddingConcurrency:   embCfg.BuildConcurrency,
		QdrantWriteConcurrency: embCfg.BuildConcurrency,
		Collection:             collection,
		EmbeddingProvider:      embCfg.Provider,
		EmbeddingModel:         embCfg.Model,
		VectorStore:            vectorStoreName,
		QdrantURL:              qdrantURL,
	}
	memory := rag.NewMemoryVectorWriter()
	writer := rag.VectorWriter(memory)
	if qdrantWriter != nil {
		writer = rag.NewTeeVectorWriter(memory, qdrantWriter)
	}
	// 设计库（手法/对标）只走 craft_recall 的 BM25 确定性路由，从不做向量检索；
	// 跳过它们的 embedding 把重建成本从小时级降回分钟级（1.7 万设计 chunk vs 数百事实 chunk）。
	factChunks := make([]domain.RAGChunk, 0, len(chunks))
	designOnly := 0
	for _, chunk := range chunks {
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			designOnly++
			continue
		}
		factChunks = append(factChunks, chunk)
	}
	if designOnly > 0 {
		fmt.Fprintf(os.Stderr, "[build-rag] 设计库 chunk=%d 仅入词法索引（craft_recall/BM25），跳过 embedding；事实层 embedding=%d"+"\n", designOnly, len(factChunks))
	}
	result, err := rag.BuildIndex(ctx, factChunks, nil, indexCfg, embedder, writer)
	if err != nil {
		return rag.IndexResult{}, domain.RAGVectorStore{}, err
	}
	// index_state 仍持有全量 chunk（含设计库），只有向量层是事实子集。
	result.State.Chunks = chunks
	result.State.ChunkHashes = rebuildRAGChunkHashList(chunks)
	vectorStore := memory.VectorStore(result.State.Config)
	result.State.Config = vectorStore.Config
	return result, vectorStore, nil
}

func discoverDefaultRAGSources(outputDir string) []string {
	outputDir = cleanAbsRAGPath(outputDir)
	projectRoot := ragProjectRoot(outputDir)
	var sources []string
	addIfExists := func(path string) {
		if path == "" {
			return
		}
		if _, err := os.Stat(path); err == nil && !containsPath(sources, path) {
			sources = append(sources, path)
		}
	}
	addIfExists(filepath.Join(projectRoot, "prompt.md"))
	addIfExists(filepath.Join(projectRoot, "input"))
	addIfExists(outputDir)
	return sources
}

func collectRAGSourceFiles(outputDir string, rawSources []string, maxFiles int) ([]string, error) {
	outputDir = cleanAbsRAGPath(outputDir)
	projectRoot := ragProjectRoot(outputDir)
	var files []string
	for _, src := range rawSources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		abs, err := filepath.Abs(src)
		if err != nil {
			return nil, err
		}
		if rag.IsForbiddenSourcePath(abs) {
			return nil, fmt.Errorf("RAG source 不允许引用拆解库(deconstruction-library)、对标库或外部参考库: %s", src)
		}
		// 写作手法库（writing-techniques）与对标素材库（novel_all）是跨书共享资产，
		// 允许来自项目目录外；其余来源仍必须在项目内，防止误扫外部正文库。
		if !isPathWithin(abs, projectRoot) && !rag.IsCraftTechniquePath(abs) && !rag.IsBenchmarkLibraryPath(abs) {
			return nil, fmt.Errorf("RAG source 必须位于当前项目目录 %s 内: %s", projectRoot, src)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("RAG source 不存在 %s: %w", src, err)
		}
		if !info.IsDir() {
			if shouldIndexRAGFile(abs, outputDir) {
				files = append(files, abs)
			}
			continue
		}
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != abs && shouldSkipRAGDir(path, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if shouldIndexRAGFile(path, outputDir) {
				files = append(files, path)
				if len(files) > maxFiles {
					return fmt.Errorf("RAG 来源文件超过 --max-files=%d，请缩小 --source", maxFiles)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	out := files[:0]
	seen := map[string]struct{}{}
	for _, f := range files {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out, nil
}

func shouldSkipRAGDir(path, name string) bool {
	switch name {
	case ".git", "node_modules", ".tmp", ".novel-studio", "backups", "chapters", "drafts", "reviews", "reviews_ai", "sessions", "runtime", "rag", "skills", "source_project", "experiments", "拆文库", "deconstruction-library", "对标", "reference-library", "reference_library", "generated-output":
		return true
	}
	clean := filepath.ToSlash(path)
	if rag.IsForbiddenSourcePath(clean) {
		return true
	}
	return strings.Contains(clean, "/meta/backups") || strings.Contains(clean, "/meta/sessions") || strings.Contains(clean, "/meta/runtime")
}

func shouldIndexRAGFile(path, outputDir string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" && ext != ".txt" && ext != ".json" {
		return false
	}
	base := filepath.Base(path)
	clean := filepath.ToSlash(path)
	lowerClean := strings.ToLower(clean)
	if rag.IsForbiddenSourcePath(clean) {
		return false
	}
	if containsAnyString(lowerClean,
		".full67.", ".front10.", ".pre-dynamic", ".pre-full", ".pre-rewrite",
		"final-verification", "continuation-audit", "前10章", "前10章_", "前10章专项",
		"包装提交", "review与写作流程", "pipeline执行覆盖", "cleared_history",
	) {
		return false
	}
	if strings.Contains(clean, "/reviews_ai/") || strings.Contains(clean, "/reviews/") ||
		strings.Contains(clean, "/drafts/") || strings.Contains(clean, "/chapters/") ||
		strings.Contains(clean, "/experiments/") || strings.Contains(clean, "/meta/rag/") || strings.Contains(clean, "/meta/sessions/") ||
		strings.Contains(clean, "/meta/runtime/") || strings.Contains(lowerClean, "/meta/quarantine") ||
		strings.Contains(lowerClean, "/summaries/") {
		return false
	}
	// 活动台账与流程/审计文档：每章刷新或纯过程记录，入库只会制造陈旧副本，
	// 召回一律走 novel_context 文件直读；种子版（*.initial.* / initial_*）仍可入库。
	lowerBase := strings.ToLower(base)
	if !strings.Contains(lowerBase, "initial") {
		for _, marker := range []string{
			"timeline.", "resource_ledger.", "state_changes.", "writing_assets.",
			"relationship_state.", "foreshadow_ledger.", "chapter_progress.",
			"character_continuity.", "project_progress.", "evolution_report.",
			"delivery_log.", "diag-export", "review-summary", "readiness",
			"gap_audit", "optimization", "replan",
		} {
			if strings.Contains(lowerBase, marker) {
				return false
			}
		}
	}
	if outputDir != "" {
		if rel, err := filepath.Rel(outputDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			if ext == ".txt" {
				return false
			}
			if ext == ".json" {
				switch base {
				case "outline.json", "layered_outline.json", "characters.json",
					"world_rules.json", "book_world.json", "world_codex.json", "compass.json":
					return true
				default:
					return false
				}
			}
		}
	}
	return true
}

func chunksFromRAGFile(path, outputDir, text string, maxChunkRunes int) []domain.RAGChunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	rel := displayRAGSourcePath(path, outputDir)
	sections := splitRAGSections(text)
	var chunks []domain.RAGChunk
	for _, sec := range sections {
		for _, part := range splitRAGText(sec.Text, maxChunkRunes) {
			part = strings.TrimSpace(part)
			if len([]rune(part)) < 30 {
				continue
			}
			if rag.MentionsForbiddenSourceMarker(part) {
				continue
			}
			idx := len(chunks) + 1
			metadata := map[string]any{
				"heading":     sec.Heading,
				"chunk_index": idx,
			}
			if category, subcategory := rag.CraftCategory(path); category != "" {
				metadata["craft_category"] = category
				if subcategory != "" {
					metadata["craft_subcategory"] = subcategory
				}
			} else if category := rag.BenchmarkCategory(path); category != "" {
				metadata["craft_category"] = category
			}
			chunks = append(chunks, domain.RAGChunk{
				ID:         fmt.Sprintf("local:%x:%03d", stableHash(rel), idx),
				SourcePath: rel,
				SourceKind: inferRAGSourceKind(path),
				Facet:      inferRAGFacet(path, sec.Heading),
				Context:    buildRAGContext(path, rel, sec.Heading),
				Text:       part,
				Summary:    summarizeRAGText(part, 120),
				Metadata:   metadata,
			})
		}
	}
	return chunks
}

type ragSection struct {
	Heading string
	Text    string
}

func splitRAGSections(text string) []ragSection {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var sections []ragSection
	heading := ""
	var b strings.Builder
	flush := func() {
		body := strings.TrimSpace(b.String())
		if body != "" {
			sections = append(sections, ragSection{Heading: heading, Text: body})
		}
		b.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && len([]rune(trimmed)) <= 80 {
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	if len(sections) == 0 {
		return []ragSection{{Text: text}}
	}
	return sections
}

func splitRAGText(text string, maxRunes int) []string {
	if len([]rune(text)) <= maxRunes {
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	var parts []string
	var b strings.Builder
	for _, para := range paras {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if len([]rune(para)) > maxRunes {
			if strings.TrimSpace(b.String()) != "" {
				parts = append(parts, strings.TrimSpace(b.String()))
				b.Reset()
			}
			rs := []rune(para)
			for start := 0; start < len(rs); start += maxRunes {
				end := start + maxRunes
				if end > len(rs) {
					end = len(rs)
				}
				parts = append(parts, string(rs[start:end]))
			}
			continue
		}
		candidate := strings.TrimSpace(b.String())
		if candidate != "" {
			candidate += "\n\n"
		}
		candidate += para
		if len([]rune(candidate)) > maxRunes {
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(para)
	}
	if strings.TrimSpace(b.String()) != "" {
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	return parts
}

func inferRAGSourceKind(path string) string {
	clean := filepath.ToSlash(path)
	switch {
	case rag.IsCraftTechniquePath(clean):
		// 写作手法库：craft 参考，与本书事实（note/knowledge）区分开，召回痕迹可审计。
		return "craft_technique"
	case rag.IsBenchmarkLibraryPath(clean):
		// 对标素材库（novel_all）：只服务设计时刻，只可迁移手法/结构。
		return rag.BenchmarkSourceKind
	case filepath.Base(clean) == "prompt.md":
		return "note"
	case strings.Contains(clean, "/input/"):
		return "note"
	case strings.Contains(clean, "/output/novel/"):
		return "note"
	default:
		return "knowledge"
	}
}

func inferRAGFacet(path, heading string) string {
	if rag.IsCraftTechniquePath(path) {
		return "craft"
	}
	if rag.IsBenchmarkLibraryPath(path) {
		return "benchmark"
	}
	s := strings.ToLower(filepath.ToSlash(path) + " " + heading)
	switch {
	case containsAnyString(s, "角色", "人物", "character", "cast"):
		return "character"
	case containsAnyString(s, "大纲", "结构", "分卷", "outline", "chapter", "章节", "arc", "volume"):
		return "plot"
	case containsAnyString(s, "世界", "规则", "world", "设定"):
		return "world"
	case containsAnyString(s, "写作", "技法", "拆解", "审美", "风格", "craft"):
		return "craft"
	case containsAnyString(s, "时间", "证据", "资源", "线索", "timeline", "ledger", "progress"):
		return "evidence"
	default:
		return "project"
	}
}

func buildRAGContext(path, rel, heading string) string {
	parts := []string{rel}
	if heading != "" {
		parts = append(parts, heading)
	}
	return strings.Join(parts, " | ")
}

func summarizeRAGText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	rs := []rune(text)
	if len(rs) <= limit {
		return text
	}
	return string(rs[:limit]) + "..."
}

func displayRAGSourcePath(path, outputDir string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if outputDir != "" {
		if rel, err := filepath.Rel(outputDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(filepath.Join("output/novel", rel))
		}
	}
	return filepath.ToSlash(path)
}

func containsPath(items []string, path string) bool {
	abs, _ := filepath.Abs(path)
	for _, item := range items {
		itemAbs, _ := filepath.Abs(item)
		if itemAbs == abs {
			return true
		}
	}
	return false
}

func containsAnyString(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func cleanAbsRAGPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func ragProjectRoot(outputDir string) string {
	outputDir = cleanAbsRAGPath(outputDir)
	if filepath.Base(outputDir) == "novel" && filepath.Base(filepath.Dir(outputDir)) == "output" {
		return filepath.Dir(filepath.Dir(outputDir))
	}
	return outputDir
}

func isPathWithin(path, root string) bool {
	path = cleanAbsRAGPath(path)
	root = cleanAbsRAGPath(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sanitizeRAGIndexState(state *domain.RAGIndexState) int {
	if state == nil {
		return 0
	}
	filtered := state.Chunks[:0]
	removed := 0
	for _, chunk := range state.Chunks {
		if rag.IsForbiddenChunk(chunk) {
			removed++
			continue
		}
		filtered = append(filtered, rag.NormalizeChunk(chunk))
	}
	state.Chunks = filtered
	state.ChunkHashes = rebuildLocalRAGChunkHashes(filtered)
	if removed > 0 {
		state.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return removed
}

func resetRAGTrace(outputDir string) {
	_ = os.Remove(filepath.Join(outputDir, "meta", "rag", "retrieval_trace.jsonl"))
}

func backfillChapterRAG(outputDir string, start, end int) (int, error) {
	if start <= 0 {
		start = 1
	}
	chapters, err := discoverGeneratedChapters(outputDir, start, end)
	if err != nil {
		return 0, err
	}
	if len(chapters) == 0 {
		return 0, fmt.Errorf("未发现可回填的章节文件")
	}
	st := store.NewStore(outputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return 0, err
	}
	if state == nil {
		state = &domain.RAGIndexState{Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	if strings.TrimSpace(state.Config.Collection) == "" {
		state.Config.Collection = "local_keyword"
	}
	for _, chapter := range chapters {
		sum, err := st.Summaries.LoadSummary(chapter)
		if err != nil {
			return 0, fmt.Errorf("读取第 %d 章摘要失败: %w", chapter, err)
		}
		if sum == nil {
			return 0, fmt.Errorf("第 %d 章缺少 summaries/%02d.json，无法沉淀章节事实包", chapter, chapter)
		}
		chunk := rag.NormalizeChunk(chunkFromChapterSummary(*sum))
		sourcePath := chapterRAGSourcePath(chapter)
		filtered := state.Chunks[:0]
		for _, existing := range state.Chunks {
			if existing.SourcePath == sourcePath {
				continue
			}
			filtered = append(filtered, existing)
		}
		state.Chunks = append(filtered, chunk)
	}
	state.ChunkHashes = rebuildLocalRAGChunkHashes(state.Chunks)
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return 0, err
	}
	return len(chapters), nil
}

func discoverGeneratedChapters(outputDir string, start, end int) ([]int, error) {
	entries, err := os.ReadDir(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return nil, err
	}
	var chapters []int
	seen := map[int]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".md")
		chapter, err := strconv.Atoi(stem)
		if err != nil {
			continue
		}
		if chapter < start {
			continue
		}
		if end > 0 && chapter > end {
			continue
		}
		if _, ok := seen[chapter]; ok {
			continue
		}
		seen[chapter] = struct{}{}
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	return chapters, nil
}

func chunkFromChapterSummary(sum domain.ChapterSummary) domain.RAGChunk {
	text := chapterSummaryRAGText(sum)
	keywords := append([]string(nil), sum.Characters...)
	keywords = append(keywords, sum.KeyEvents...)
	return domain.RAGChunk{
		ID:         fmt.Sprintf("chapter:%03d:commit_facts", sum.Chapter),
		SourcePath: chapterRAGSourcePath(sum.Chapter),
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    fmt.Sprintf("第 %d 章终稿沉淀 | backfill_chapters", sum.Chapter),
		Text:       text,
		Summary:    summarizeRAGText(sum.Summary, 120),
		Keywords:   keywords,
		Metadata: map[string]any{
			"chapter":        sum.Chapter,
			"opening_device": sum.OpeningDevice,
			"ending_device":  sum.EndingDevice,
			"source":         "backfill_chapters",
		},
	}
}

func chapterSummaryRAGText(sum domain.ChapterSummary) string {
	var b strings.Builder
	writeLine := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			fmt.Fprintf(&b, "%s：%s\n", label, value)
		}
	}
	fmt.Fprintf(&b, "# 第 %d 章终稿沉淀\n", sum.Chapter)
	writeLine("摘要", sum.Summary)
	if len(sum.Characters) > 0 {
		writeLine("出场人物", strings.Join(cleanRAGStrings(sum.Characters), "；"))
	}
	if len(sum.KeyEvents) > 0 {
		writeLine("关键事件", strings.Join(cleanRAGStrings(sum.KeyEvents), "；"))
	}
	writeLine("开头装置", sum.OpeningDevice)
	writeLine("结尾装置", sum.EndingDevice)
	return strings.TrimSpace(b.String())
}

func chapterRAGSourcePath(chapter int) string {
	return fmt.Sprintf("summaries/%02d.json", chapter)
}

func rebuildLocalRAGChunkHashes(chunks []domain.RAGChunk) []string {
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.Hash != "" {
			seen[chunk.Hash] = struct{}{}
		}
	}
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}

func cleanRAGStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func stableHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func probeRAGRecall(outputDir, style string, refs toolspkg.References, chapter int, embedder rag.Embedder, searcher rag.VectorSearcher) (int, error) {
	st := store.NewStore(outputDir)
	tool := toolspkg.NewContextTool(st, refs, style)
	if embedder != nil {
		tool.WithRAGEmbedder(embedder)
	}
	if searcher != nil {
		tool.WithRAGVectorSearcher(searcher)
	}
	raw, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"chapter":%d}`, chapter)))
	if err != nil {
		return 0, err
	}
	var payload struct {
		Selected struct {
			RAG []domain.RecallItem `json:"rag_recall"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, err
	}
	return len(payload.Selected.RAG), nil
}

// appendConfiguredSharedLibraries 把配置的共享库（写作手法库 + 对标素材库）追加进
// 索引来源（去重）。让 --build-rag / --zero-init 的重建默认保留这些 chunk，复用不靠记忆。
func appendConfiguredSharedLibraries(sources []string, cfg bootstrap.Config) []string {
	sources = appendSharedLibrary(sources, cfg.RAG.CraftLibrary, "rag.craft_library", rag.IsCraftTechniquePath)
	sources = appendSharedLibrary(sources, cfg.RAG.BenchmarkLibrary, "rag.benchmark_library", rag.IsBenchmarkLibraryPath)
	return sources
}

func appendSharedLibrary(sources []string, lib, label string, allowed func(string) bool) []string {
	lib = strings.TrimSpace(lib)
	if lib == "" {
		return sources
	}
	abs, err := filepath.Abs(lib)
	if err != nil || !allowed(abs) {
		fmt.Fprintf(os.Stderr, "[build-rag] 忽略 %s=%q：路径未命中对应白名单\n", label, lib)
		return sources
	}
	if _, statErr := os.Stat(abs); statErr != nil {
		fmt.Fprintf(os.Stderr, "[build-rag] 忽略 %s=%q：%v\n", label, lib, statErr)
		return sources
	}
	for _, existing := range sources {
		if existingAbs, err := filepath.Abs(existing); err == nil && existingAbs == abs {
			return sources
		}
	}
	return append(sources, abs)
}

// rebuildRAGChunkHashList 生成排序去重的 chunk hash 列表（index_state.chunk_hashes 口径）。
func rebuildRAGChunkHashList(chunks []domain.RAGChunk) []string {
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.Hash != "" {
			seen[chunk.Hash] = struct{}{}
		}
	}
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}
