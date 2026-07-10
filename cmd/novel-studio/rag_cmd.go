package main

import (
	"context"
	"crypto/sha256"
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

func hasRAGReadyFlag(argv []string) bool {
	for _, arg := range argv {
		if arg == "--rag-ready" {
			return true
		}
	}
	return false
}

func ragReadyPipeline(opts cliOptions, args []string) error {
	fs := flag.NewFlagSet("rag-ready", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: novel-studio --rag-ready [--dir <output/novel>]")
		fmt.Fprintln(os.Stderr, "只执行 RAG schema 迁移、pending 回放、向量复用与 Qdrant 恢复，不启动写作。")
		fs.PrintDefaults()
	}
	var dir string
	fs.StringVar(&dir, "dir", "", "小说输出目录；为空时使用配置中的 output_dir")
	if hasHelpToken(args) {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("--rag-ready 不接受额外参数：%v", fs.Args())
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil && strings.TrimSpace(dir) == "" {
		return err
	}
	if strings.TrimSpace(dir) != "" {
		absDir, absErr := filepath.Abs(dir)
		if absErr != nil {
			return absErr
		}
		refreshAutoRAGCollectionForOutputDir(&cfg, absDir, hasConfiguredRAGQdrantCollection(opts))
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return fmt.Errorf("读取 RAG index 状态失败: %w", err)
	}
	if state == nil {
		return fmt.Errorf("读取 RAG index 状态失败: index_state 不存在")
	}
	vectorStore, err := st.RAG.LoadVectorStore()
	if err != nil {
		return err
	}
	pending, err := st.RAG.LoadPendingUpserts()
	if err != nil {
		return err
	}
	pendingCount := 0
	if pending != nil {
		pendingCount = len(pending.Chunks)
	}
	if pendingCount != 0 {
		return fmt.Errorf("RAG 就绪后仍有 pending chunks=%d", pendingCount)
	}
	factHashes := make(map[string]struct{})
	for _, chunk := range state.Chunks {
		if !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			factHashes[rag.NormalizeChunk(chunk).Hash] = struct{}{}
		}
	}
	report := map[string]any{
		"ready":            true,
		"schema_version":   state.SchemaVersion,
		"chunks":           len(state.Chunks),
		"fact_chunks":      len(factHashes),
		"pending_chunks":   pendingCount,
		"collection":       state.Config.Collection,
		"vector_store":     state.Config.VectorStore,
		"vector_dimension": state.Config.VectorDimension,
	}
	if vectorStore != nil {
		report["local_vector_points"] = len(vectorStore.Points)
	}
	if _, enabled := bootstrap.ResolveRAGQdrantConfig(cfg); enabled {
		client, clientEnabled, clientErr := bootstrap.NewRAGQdrantClient(cfg, false)
		if clientErr != nil {
			return clientErr
		}
		if clientEnabled {
			countCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			count, countErr := client.Count(countCtx, true)
			if countErr != nil {
				return countErr
			}
			report["qdrant_points"] = count
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func parseBuildRAGFlags(argv []string) (buildRAGFlags, []string, error) {
	fs := flag.NewFlagSet("build-rag", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --build-rag [--dir <output/novel>] [--source <path> ...] [--probe-chapter N]\n\n")
		fmt.Fprintf(os.Stderr, "构建本书本地 RAG 索引，写入 meta/rag/index_state.json/md。默认只索引当前项目的 prompt.md、brainstorm.md、input/*.md 与 output/novel 关键设定/账本文件；拒绝拆解库(deconstruction-library)、对标库和外部参考库。\n\n选项：\n")
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
	if pending, err := st.RAG.LoadPendingUpserts(); err != nil {
		return err
	} else if pending != nil && len(pending.Chunks) > 0 {
		mergePendingRAGState(st, &result.State, pending.Chunks)
		if err := st.RAG.SaveIndexState(result.State); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[build-rag] 已合并待回填 RAG chunks=%d\n", len(pending.Chunks))
	}
	if removed, err := sanitizeExistingRAGVectorStore(st, &result.State); err != nil {
		return err
	} else if removed > 0 {
		fmt.Fprintf(os.Stderr, "[build-rag] 已移除旧 RAG 向量点=%d\n", removed)
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
		if err := st.RAG.SaveVectorStore(vectorStore); err != nil {
			return err
		}
		if err := st.RAG.SaveIndexState(result.State); err != nil {
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
	if err := st.RAG.ClearPendingUpserts(); err != nil {
		return err
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
		migrated := migrateRAGIndexSchema(state)
		beforeSanitizedDigest := state.SanitizedDigest
		removed := sanitizeRAGIndexState(st, state)
		sanitized := state.SanitizedDigest != beforeSanitizedDigest
		vectorRemoved, vectorErr := sanitizeExistingRAGVectorStore(st, state)
		if vectorErr != nil {
			return vectorErr
		}
		if removed > 0 || migrated || sanitized {
			if len(state.Chunks) > 0 {
				if err := st.RAG.SaveIndexState(*state); err != nil {
					return err
				}
				if migrated {
					fmt.Fprintf(os.Stderr, "[build-rag] RAG 索引 schema 已迁移到 v%d，旧向量将按新语义 hash 重建\n", domain.CurrentRAGIndexSchemaVersion)
				}
				if removed > 0 {
					fmt.Fprintf(os.Stderr, "[build-rag] 已移除旧 RAG 参考库 chunks=%d\n", removed)
				}
				if vectorRemoved > 0 {
					fmt.Fprintf(os.Stderr, "[build-rag] 已移除旧 RAG 向量点=%d\n", vectorRemoved)
				}
				return nil
			}
			fmt.Fprintf(os.Stderr, "[build-rag] 旧 RAG 索引仅含参考库 chunks=%d，重新构建当前项目索引\n", removed)
		} else if vectorRemoved > 0 {
			fmt.Fprintf(os.Stderr, "[build-rag] 已移除旧 RAG 向量点=%d\n", vectorRemoved)
			return nil
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

func migrateRAGIndexSchema(state *domain.RAGIndexState) bool {
	if state == nil || state.SchemaVersion >= domain.CurrentRAGIndexSchemaVersion {
		return false
	}
	for index, chunk := range state.Chunks {
		state.Chunks[index] = rag.RehashChunk(chunk)
	}
	state.ChunkHashes = rebuildRAGChunkHashList(state.Chunks)
	state.SchemaVersion = domain.CurrentRAGIndexSchemaVersion
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	return true
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
	if _, err := backfillChapterRAG(cfg.OutputDir, 1, 0); err != nil {
		return fmt.Errorf("回填已生成章节 RAG 事实: %w", err)
	}
	st := store.NewStore(cfg.OutputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return err
	}
	if state == nil || len(state.Chunks) == 0 {
		return fmt.Errorf("RAG index 为空，无法构建 Qdrant 向量索引")
	}
	pending, err := st.RAG.LoadPendingUpserts()
	if err != nil {
		return err
	}
	pendingApplied := pending != nil && len(pending.Chunks) > 0
	if pendingApplied {
		mergePendingRAGState(st, state, pending.Chunks)
		fmt.Fprintf(os.Stderr, "[build-rag] 检测到待回填 RAG chunks=%d，纳入本次恢复\n", len(pending.Chunks))
	}
	if _, enabled := bootstrap.ResolveRAGEmbeddingConfig(cfg, bootstrap.RAGEmbeddingConfig{}); !enabled {
		if pendingApplied {
			if err := st.RAG.SaveIndexState(*state); err != nil {
				return err
			}
			return st.RAG.ClearPendingUpserts()
		}
		return nil
	}
	embCfg, _ := bootstrap.ResolveRAGEmbeddingConfig(cfg, bootstrap.RAGEmbeddingConfig{})
	qdrantCfg, qdrantEnabled := bootstrap.ResolveRAGQdrantConfig(cfg)
	existingVectorStore, vectorErr := st.RAG.LoadVectorStore()
	if vectorErr != nil {
		return vectorErr
	}
	localReusable, localReason, expectedPoints := pipelineRAGLocalArtifactsReusable(state, existingVectorStore, embCfg)
	if localReusable {
		if !qdrantEnabled {
			if err := persistPipelineRAGBackendConfig(st, state, existingVectorStore, embCfg, nil); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[build-rag] 复用现有本地 RAG 向量：fact_chunks=%d（%s）\n", expectedPoints, localReason)
			return st.RAG.ClearPendingUpserts()
		}
		qdrantCtx, cancelQdrant := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancelQdrant()
		if _, err := bootstrap.EnsureRAGQdrant(qdrantCtx, cfg); err != nil {
			return fmt.Errorf("恢复 RAG Qdrant 服务: %w", err)
		}
		client, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false)
		if err != nil {
			return fmt.Errorf("初始化 RAG Qdrant 复用客户端: %w", err)
		}
		if !enabled {
			return fmt.Errorf("初始化 RAG Qdrant 复用客户端: Qdrant 未启用")
		}
		remoteReady := false
		if err := client.EnsureCollection(qdrantCtx, existingVectorStore.Config.VectorDimension); err == nil {
			if count, countErr := client.Count(qdrantCtx, true); countErr == nil && count == expectedPoints {
				remoteReady = true
			}
		}
		if !remoteReady {
			if err := restoreQdrantFromLocalVectorStore(qdrantCtx, cfg, existingVectorStore, expectedPoints); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[build-rag] Qdrant 已从本地向量恢复：points=%d collection=%s（未重新 embedding）\n", expectedPoints, qdrantCfg.Collection)
		} else {
			fmt.Fprintf(os.Stderr, "[build-rag] 复用现有 RAG 向量：fact_chunks=%d qdrant_points=%d collection=%s\n", expectedPoints, expectedPoints, qdrantCfg.Collection)
		}
		if err := persistPipelineRAGBackendConfig(st, state, existingVectorStore, embCfg, client); err != nil {
			return err
		}
		return st.RAG.ClearPendingUpserts()
	}
	missingChunks, incrementalExpected, incrementalOK, incrementalReason := pipelineRAGIncrementalPlan(state, existingVectorStore, embCfg)
	if incrementalOK {
		incrementalCtx, cancelIncremental := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancelIncremental()
		embedded, err := reconcilePipelineRAGIncrementally(
			incrementalCtx, cfg, st, state, existingVectorStore, embCfg, missingChunks, incrementalExpected, qdrantEnabled,
		)
		if err != nil {
			return fmt.Errorf("增量恢复 RAG 向量: %w", err)
		}
		if err := st.RAG.ClearPendingUpserts(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[build-rag] 增量补齐 RAG 向量：missing=%d embedded=%d reused=%d total=%d（%s）\n",
			len(missingChunks), embedded, incrementalExpected-len(missingChunks), incrementalExpected, incrementalReason)
		return nil
	}
	fmt.Fprintf(os.Stderr, "[build-rag] 现有本地向量不可复用：%s；执行 embedding 重建\n", localReason)
	result, vectorStore, err := buildRAGEmbeddings(context.Background(), cfg, bootstrap.RAGEmbeddingConfig{}, state.Chunks)
	if err != nil {
		return err
	}
	if err := st.RAG.SaveVectorStore(vectorStore); err != nil {
		return err
	}
	if err := st.RAG.SaveIndexState(result.State); err != nil {
		return err
	}
	if err := st.RAG.ClearPendingUpserts(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[build-rag] 写作前 RAG 检查完成：chunks=%d embeddings=%d vector_points=%d collection=%s\n", len(result.State.Chunks), result.Embedded, result.Written, result.State.Config.Collection)
	return nil
}

func pipelineRAGIncrementalPlan(
	state *domain.RAGIndexState,
	vectorStore *domain.RAGVectorStore,
	embCfg bootstrap.RAGEmbeddingConfig,
) ([]domain.RAGChunk, int, bool, string) {
	if state == nil || vectorStore == nil {
		return nil, 0, false, "缺少 index_state 或 vector_store"
	}
	if state.SchemaVersion != domain.CurrentRAGIndexSchemaVersion {
		return nil, 0, false, "RAG index schema 需要迁移"
	}
	stateCfg := state.Config
	vectorCfg := vectorStore.Config
	if !strings.EqualFold(strings.TrimSpace(stateCfg.EmbeddingProvider), strings.TrimSpace(embCfg.Provider)) ||
		strings.TrimSpace(stateCfg.EmbeddingModel) != strings.TrimSpace(embCfg.Model) {
		return nil, 0, false, "embedding provider/model 已变化"
	}
	if stateCfg.VectorDimension <= 0 || vectorCfg.VectorDimension != stateCfg.VectorDimension ||
		vectorCfg.EmbeddingProvider != stateCfg.EmbeddingProvider || vectorCfg.EmbeddingModel != stateCfg.EmbeddingModel {
		return nil, 0, false, "本地向量配置或维度不一致"
	}
	desired := make(map[string]domain.RAGChunk)
	for _, chunk := range state.Chunks {
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			continue
		}
		chunk = rag.NormalizeChunk(chunk)
		if chunk.Hash != "" {
			desired[chunk.Hash] = chunk
		}
	}
	if len(desired) == 0 {
		return nil, 0, false, "事实层 chunk 为空"
	}
	present := make(map[string]struct{}, len(vectorStore.Points))
	for _, point := range vectorStore.Points {
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = strings.TrimSpace(point.Chunk.Hash)
		}
		if _, ok := desired[hash]; !ok {
			return nil, len(desired), false, "本地向量仍含过期 chunk hash"
		}
		if _, duplicate := present[hash]; duplicate {
			return nil, len(desired), false, "本地向量含重复 chunk hash"
		}
		if len(point.Vector) != stateCfg.VectorDimension {
			return nil, len(desired), false, "本地向量维度不一致"
		}
		if err := rag.ValidateVector(point.Vector); err != nil {
			return nil, len(desired), false, "本地向量包含无效数值"
		}
		present[hash] = struct{}{}
	}
	missing := make([]domain.RAGChunk, 0, len(desired)-len(present))
	for hash, chunk := range desired {
		if _, ok := present[hash]; !ok {
			missing = append(missing, chunk)
		}
	}
	if len(missing) == 0 {
		return nil, len(desired), false, "没有缺失 hash，需按其他不一致原因处理"
	}
	sort.SliceStable(missing, func(i, j int) bool { return missing[i].ID < missing[j].ID })
	return missing, len(desired), true, "旧向量均有效，仅缺失 chunk hash"
}

func reconcilePipelineRAGIncrementally(
	ctx context.Context,
	cfg bootstrap.Config,
	st *store.Store,
	state *domain.RAGIndexState,
	existing *domain.RAGVectorStore,
	embCfg bootstrap.RAGEmbeddingConfig,
	missing []domain.RAGChunk,
	expected int,
	qdrantEnabled bool,
) (int, error) {
	if len(missing) == 0 {
		return 0, nil
	}
	embedder, enabled, err := bootstrap.NewRAGEmbedder(cfg)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, fmt.Errorf("RAG embedding 未启用")
	}
	buildConcurrency := embCfg.BuildConcurrency
	if buildConcurrency <= 0 {
		buildConcurrency = 2
	}
	embCfg.BuildConcurrency = effectiveRAGEmbeddingBuildConcurrency(embCfg)
	if strings.TrimSpace(embCfg.LocalGGUF) != "" && buildConcurrency != embCfg.BuildConcurrency {
		fmt.Fprintf(os.Stderr, "[build-rag] 本地 GGUF 增量 embedding 自动串行化：configured=%d effective=%d（避免 llama-server 并发 EOF）\n", buildConcurrency, embCfg.BuildConcurrency)
	}
	indexCfg := existing.Config
	indexCfg.EmbeddingProvider = embCfg.Provider
	indexCfg.EmbeddingModel = embCfg.Model
	indexCfg.EmbeddingConcurrency = embCfg.BuildConcurrency
	if indexCfg.QdrantWriteConcurrency <= 0 {
		indexCfg.QdrantWriteConcurrency = embCfg.BuildConcurrency
	}
	if indexCfg.VectorBatchSize <= 0 {
		indexCfg.VectorBatchSize = 32
	}
	memory := rag.NewMemoryVectorWriter()
	result, err := rag.BuildIndex(ctx, missing, nil, indexCfg, embedder, memory)
	if err != nil {
		return 0, err
	}
	update := memory.VectorStore(result.State.Config)
	merged := mergePipelineRAGVectorPoints(existing, update, state.Chunks)
	if len(merged.Points) != expected {
		return 0, fmt.Errorf("增量合并后本地向量点数不一致: got=%d want=%d", len(merged.Points), expected)
	}
	var qdrantClient *rag.QdrantClient
	if qdrantEnabled {
		if _, err := bootstrap.EnsureRAGQdrant(ctx, cfg); err != nil {
			return 0, err
		}
		client, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false)
		if err != nil {
			return 0, err
		}
		if !enabled {
			return 0, fmt.Errorf("Qdrant 未启用")
		}
		qdrantClient = client
		merged.Config.VectorStore = "qdrant"
		merged.Config.Collection = client.Collection()
		merged.Config.QdrantURL = client.URL()
	} else {
		merged.Config.VectorStore = "local_json"
		merged.Config.Collection = "local_vector"
		merged.Config.QdrantURL = ""
	}
	state.Config = merged.Config
	state.ChunkHashes = rebuildRAGChunkHashList(state.Chunks)
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := st.RAG.SaveVectorStore(merged); err != nil {
		return 0, err
	}
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return 0, err
	}
	if qdrantClient != nil {
		points := make([]rag.VectorPoint, 0, len(update.Points))
		for _, point := range update.Points {
			points = append(points, rag.VectorPoint{ID: point.ID, Vector: point.Vector, Payload: point.Payload, Chunk: point.Chunk})
		}
		remoteErr := qdrantClient.EnsureCollection(ctx, merged.Config.VectorDimension)
		if remoteErr == nil {
			_, remoteErr = rag.WriteVectorPoints(ctx, qdrantClient, points, merged.Config)
		}
		if remoteErr == nil {
			if count, countErr := qdrantClient.Count(ctx, true); countErr != nil {
				remoteErr = countErr
			} else if count != expected {
				remoteErr = fmt.Errorf("Qdrant 增量写入后点数不一致: got=%d want=%d", count, expected)
			}
		}
		if remoteErr != nil {
			if restoreErr := restoreQdrantFromLocalVectorStore(ctx, cfg, &merged, expected); restoreErr != nil {
				return 0, fmt.Errorf("Qdrant 增量写入失败 (%v)，本地全量重放也失败: %w", remoteErr, restoreErr)
			}
			fmt.Fprintf(os.Stderr, "[build-rag] Qdrant 增量点数异常，已从本地向量重放：points=%d（未重新 embedding）\n", expected)
		}
	}
	return result.Embedded, nil
}

func mergePipelineRAGVectorPoints(base *domain.RAGVectorStore, update domain.RAGVectorStore, desiredChunks []domain.RAGChunk) domain.RAGVectorStore {
	desired := make(map[string]struct{})
	for _, chunk := range desiredChunks {
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			continue
		}
		chunk = rag.NormalizeChunk(chunk)
		if chunk.Hash != "" {
			desired[chunk.Hash] = struct{}{}
		}
	}
	replaceIDs := make(map[string]struct{}, len(update.Points))
	replaceHashes := make(map[string]struct{}, len(update.Points))
	for _, point := range update.Points {
		replaceIDs[point.ID] = struct{}{}
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = strings.TrimSpace(point.Chunk.Hash)
		}
		replaceHashes[hash] = struct{}{}
	}
	merged := domain.RAGVectorStore{Config: update.Config, UpdatedAt: time.Now().Format(time.RFC3339)}
	if base != nil {
		merged.Config = base.Config
		for _, point := range base.Points {
			hash := strings.TrimSpace(point.Hash)
			if hash == "" {
				hash = strings.TrimSpace(point.Chunk.Hash)
			}
			if _, keep := desired[hash]; !keep {
				continue
			}
			if _, replace := replaceIDs[point.ID]; replace {
				continue
			}
			if _, replace := replaceHashes[hash]; replace {
				continue
			}
			merged.Points = append(merged.Points, point)
		}
	}
	merged.Points = append(merged.Points, update.Points...)
	sort.SliceStable(merged.Points, func(i, j int) bool { return merged.Points[i].ID < merged.Points[j].ID })
	return merged
}

func mergePendingRAGState(st *store.Store, state *domain.RAGIndexState, pending []domain.RAGChunk) {
	if state == nil || len(pending) == 0 {
		return
	}
	sources := make(map[string]struct{})
	normalized := make([]domain.RAGChunk, 0, len(pending))
	seen := make(map[string]struct{})
	for _, chunk := range pending {
		chunk = rag.NormalizeChunk(chunk)
		if chunk.SourcePath == "" || chunk.Hash == "" || rag.IsForbiddenChunk(chunk) || ragChunkHasProjectContamination(st, chunk) {
			continue
		}
		if _, duplicate := seen[chunk.Hash]; duplicate {
			continue
		}
		seen[chunk.Hash] = struct{}{}
		sources[chunk.SourcePath] = struct{}{}
		normalized = append(normalized, chunk)
	}
	merged := make([]domain.RAGChunk, 0, len(state.Chunks)+len(normalized))
	for _, chunk := range state.Chunks {
		if _, replace := sources[chunk.SourcePath]; replace {
			continue
		}
		merged = append(merged, rag.NormalizeChunk(chunk))
	}
	merged = append(merged, normalized...)
	state.Chunks = merged
	state.ChunkHashes = rebuildRAGChunkHashList(merged)
	state.SanitizedDigest = ragIndexSanitizationDigest(state)
	state.SchemaVersion = domain.CurrentRAGIndexSchemaVersion
	state.UpdatedAt = time.Now().Format(time.RFC3339)
}

func pipelineRAGArtifactsReusable(
	state *domain.RAGIndexState,
	vectorStore *domain.RAGVectorStore,
	embCfg bootstrap.RAGEmbeddingConfig,
	qdrantCfg bootstrap.RAGQdrantConfig,
	qdrantEnabled bool,
) (bool, string, int) {
	localReusable, reason, expected := pipelineRAGLocalArtifactsReusable(state, vectorStore, embCfg)
	if !localReusable {
		return false, reason, expected
	}
	cfg := state.Config
	if qdrantEnabled {
		if cfg.VectorStore != "qdrant" || strings.TrimSpace(cfg.Collection) != strings.TrimSpace(qdrantCfg.Collection) ||
			strings.TrimRight(strings.TrimSpace(cfg.QdrantURL), "/") != strings.TrimRight(strings.TrimSpace(qdrantCfg.URL), "/") {
			return false, "Qdrant collection 或 URL 已变化", expected
		}
	} else if cfg.VectorStore != "local_json" {
		return false, "向量后端配置已变化", expected
	}
	return true, "chunk/model/dimension/vector points 一致", expected
}

func pipelineRAGLocalArtifactsReusable(
	state *domain.RAGIndexState,
	vectorStore *domain.RAGVectorStore,
	embCfg bootstrap.RAGEmbeddingConfig,
) (bool, string, int) {
	if state == nil || vectorStore == nil {
		return false, "缺少 index_state 或 vector_store", 0
	}
	if state.SchemaVersion != domain.CurrentRAGIndexSchemaVersion {
		return false, "RAG index schema 需要迁移", 0
	}
	factHashes := map[string]struct{}{}
	for _, chunk := range state.Chunks {
		if rag.IsDesignOnlySourceKind(chunk.SourceKind) {
			continue
		}
		chunk = rag.NormalizeChunk(chunk)
		if strings.TrimSpace(chunk.Hash) != "" {
			factHashes[chunk.Hash] = struct{}{}
		}
	}
	expected := len(factHashes)
	if expected == 0 {
		return false, "事实层 chunk 为空", 0
	}
	cfg := state.Config
	vectorCfg := vectorStore.Config
	if !strings.EqualFold(strings.TrimSpace(cfg.EmbeddingProvider), strings.TrimSpace(embCfg.Provider)) ||
		strings.TrimSpace(cfg.EmbeddingModel) != strings.TrimSpace(embCfg.Model) {
		return false, "embedding provider/model 已变化", expected
	}
	if cfg.VectorDimension <= 0 || vectorCfg.VectorDimension != cfg.VectorDimension {
		return false, "向量维度缺失或不一致", expected
	}
	if vectorCfg.EmbeddingProvider != cfg.EmbeddingProvider || vectorCfg.EmbeddingModel != cfg.EmbeddingModel {
		return false, "index_state 与 vector_store 的模型不一致", expected
	}
	if len(vectorStore.Points) != expected {
		return false, fmt.Sprintf("本地向量点数=%d，期望=%d", len(vectorStore.Points), expected), expected
	}
	seen := make(map[string]struct{}, len(vectorStore.Points))
	for _, point := range vectorStore.Points {
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = strings.TrimSpace(point.Chunk.Hash)
		}
		if _, ok := factHashes[hash]; !ok {
			return false, "本地向量包含过期 chunk hash", expected
		}
		if _, duplicate := seen[hash]; duplicate {
			return false, "本地向量含重复 chunk hash", expected
		}
		if len(point.Vector) != cfg.VectorDimension {
			return false, "本地向量维度与索引配置不一致", expected
		}
		if err := rag.ValidateVector(point.Vector); err != nil {
			return false, "本地向量包含无效数值", expected
		}
		seen[hash] = struct{}{}
	}
	return true, "chunk/model/dimension/vector points 一致", expected
}

func restoreQdrantFromLocalVectorStore(ctx context.Context, cfg bootstrap.Config, vectorStore *domain.RAGVectorStore, expected int) error {
	if vectorStore == nil || len(vectorStore.Points) == 0 {
		return fmt.Errorf("无法恢复 Qdrant：本地向量为空")
	}
	client, enabled, err := bootstrap.NewRAGQdrantClient(cfg, true)
	if err != nil {
		return fmt.Errorf("初始化 Qdrant 恢复客户端: %w", err)
	}
	if !enabled {
		return fmt.Errorf("初始化 Qdrant 恢复客户端: Qdrant 未启用")
	}
	points := make([]rag.VectorPoint, 0, len(vectorStore.Points))
	for _, point := range vectorStore.Points {
		points = append(points, rag.VectorPoint{ID: point.ID, Vector: point.Vector, Payload: point.Payload, Chunk: point.Chunk})
	}
	writeCfg := vectorStore.Config
	if writeCfg.VectorBatchSize <= 0 {
		writeCfg.VectorBatchSize = 32
	}
	if writeCfg.QdrantWriteConcurrency <= 0 {
		writeCfg.QdrantWriteConcurrency = 2
	}
	if _, err := rag.WriteVectorPoints(ctx, client, points, writeCfg); err != nil {
		return fmt.Errorf("从本地向量恢复 Qdrant: %w", err)
	}
	count, err := client.Count(ctx, true)
	if err != nil {
		return fmt.Errorf("验证恢复后的 Qdrant 点数: %w", err)
	}
	if count != expected {
		return fmt.Errorf("恢复后的 Qdrant 点数不一致: got=%d want=%d", count, expected)
	}
	return nil
}

func persistPipelineRAGBackendConfig(st *store.Store, state *domain.RAGIndexState, vectorStore *domain.RAGVectorStore, embCfg bootstrap.RAGEmbeddingConfig, client *rag.QdrantClient) error {
	if state == nil || vectorStore == nil {
		return fmt.Errorf("RAG 后端配置持久化缺少索引或向量状态")
	}
	cfg := vectorStore.Config
	cfg.EmbeddingProvider = embCfg.Provider
	cfg.EmbeddingModel = embCfg.Model
	if cfg.VectorBatchSize <= 0 {
		cfg.VectorBatchSize = 32
	}
	if client == nil {
		cfg.VectorStore = "local_json"
		cfg.Collection = "local_vector"
		cfg.QdrantURL = ""
	} else {
		cfg.VectorStore = "qdrant"
		cfg.Collection = client.Collection()
		cfg.QdrantURL = client.URL()
	}
	if state.Config == cfg && vectorStore.Config == cfg {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	vectorStore.Config = cfg
	vectorStore.UpdatedAt = now
	state.Config = cfg
	state.UpdatedAt = now
	if err := st.RAG.SaveVectorStore(*vectorStore); err != nil {
		return err
	}
	return st.RAG.SaveIndexState(*state)
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
			SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
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
	configuredConcurrency := embCfg.BuildConcurrency
	embCfg.BuildConcurrency = effectiveRAGEmbeddingBuildConcurrency(embCfg)
	if strings.TrimSpace(embCfg.LocalGGUF) != "" && configuredConcurrency != embCfg.BuildConcurrency {
		fmt.Fprintf(os.Stderr, "[build-rag] 本地 GGUF embedding 自动串行化：configured=%d effective=%d（避免 llama-server 并发 EOF，不更换模型）\n", configuredConcurrency, embCfg.BuildConcurrency)
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
		VectorBatchSize:        32,
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
	if len(factChunks) == 0 {
		return rag.IndexResult{}, domain.RAGVectorStore{}, fmt.Errorf("RAG 事实层 chunk 为空，不能构建语义向量")
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

func sanitizeExistingRAGVectorStore(st *store.Store, state *domain.RAGIndexState) (int, error) {
	vectorStore, err := st.RAG.LoadVectorStore()
	if err != nil {
		return 0, err
	}
	if vectorStore == nil {
		return 0, nil
	}
	removed, remapped := sanitizeRAGVectorStore(st, vectorStore, state)
	if removed == 0 && remapped == 0 {
		return 0, nil
	}
	if err := st.RAG.SaveVectorStore(*vectorStore); err != nil {
		return 0, err
	}
	return removed, nil
}

func sanitizeRAGVectorStore(st *store.Store, vectorStore *domain.RAGVectorStore, state *domain.RAGIndexState) (int, int) {
	if vectorStore == nil {
		return 0, 0
	}
	allowed := map[string]struct{}{}
	allowedByID := map[string]domain.RAGChunk{}
	if state != nil {
		for _, chunk := range state.Chunks {
			chunk = rag.NormalizeChunk(chunk)
			if chunk.Hash != "" && !rag.IsDesignOnlySourceKind(chunk.SourceKind) {
				allowed[chunk.Hash] = struct{}{}
				if chunk.ID != "" {
					allowedByID[chunk.ID] = chunk
				}
			}
		}
	}
	filtered := vectorStore.Points[:0]
	removed := 0
	remapped := 0
	for _, point := range vectorStore.Points {
		chunk := rag.NormalizeChunk(point.Chunk)
		hash := strings.TrimSpace(point.Hash)
		if hash == "" {
			hash = chunk.Hash
		}
		if state != nil {
			if _, ok := allowed[hash]; !ok {
				candidate, candidateOK := allowedByID[point.ID]
				if !candidateOK {
					candidate, candidateOK = allowedByID[chunk.ID]
				}
				if !candidateOK || rag.RehashChunk(chunk).Hash != candidate.Hash {
					removed++
					continue
				}
				hash = candidate.Hash
				chunk = candidate
				point.ID = candidate.ID
				remapped++
			}
		}
		if rag.IsForbiddenChunk(chunk) || rag.IsDesignOnlySourceKind(chunk.SourceKind) || ragChunkHasProjectContamination(st, chunk) || rag.ValidateVector(point.Vector) != nil {
			removed++
			continue
		}
		if vectorStore.Config.VectorDimension > 0 && len(point.Vector) != vectorStore.Config.VectorDimension {
			removed++
			continue
		}
		point.Hash = hash
		point.Chunk = chunk
		filtered = append(filtered, point)
	}
	vectorStore.Points = filtered
	if removed > 0 || remapped > 0 {
		vectorStore.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return removed, remapped
}

func effectiveRAGEmbeddingBuildConcurrency(embCfg bootstrap.RAGEmbeddingConfig) int {
	concurrency := embCfg.BuildConcurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	if strings.TrimSpace(embCfg.LocalGGUF) != "" && concurrency > 1 {
		return 1
	}
	return concurrency
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
	addIfExists(filepath.Join(projectRoot, "brainstorm.md"))
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
		// 写作手法库（writing-techniques）、对标素材库（novel_all）与显式校准库
		// （review-calibration）是跨书共享资产，允许来自项目目录外；其余来源仍必须在项目内，
		// 防止误扫外部正文库。
		if !isPathWithin(abs, projectRoot) && !rag.IsCraftTechniquePath(abs) && !rag.IsBenchmarkLibraryPath(abs) && !rag.IsCalibrationPath(abs) {
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
			// 设计库（craft/benchmark/review-calibration）再打内容级细分面 + 可用阶段标签：
			// 目录分类太粗，按文件名+正文内容判 craft_facet，并派生 usage_stage
			// （architect/plan/writing/review），让各阶段按内容取料而非只按目录。
			if rag.IsCraftTechniquePath(path) || rag.IsBenchmarkLibraryPath(path) || rag.IsCalibrationPath(path) {
				facet := rag.CraftContentFacet(path, part)
				metadata["craft_facet"] = string(facet)
				metadata["usage_stage"] = strings.Join(rag.UsageStagesForFacet(facet), ",")
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
	case rag.IsCalibrationPath(clean):
		// 审核校准库（review-calibration）：AI 检测校准 + 人工文笔样本，服务 review/writing。
		return rag.CalibrationSourceKind
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

func sanitizeRAGIndexState(st *store.Store, state *domain.RAGIndexState) int {
	if state == nil {
		return 0
	}
	if digest := ragIndexSanitizationDigest(state); digest != "" && state.SanitizedDigest == digest {
		return 0
	}
	filtered := state.Chunks[:0]
	removed := 0
	for _, chunk := range state.Chunks {
		if rag.IsForbiddenChunk(chunk) || ragChunkHasProjectContamination(st, chunk) {
			removed++
			continue
		}
		filtered = append(filtered, rag.NormalizeChunk(chunk))
	}
	state.Chunks = filtered
	state.ChunkHashes = rebuildLocalRAGChunkHashes(filtered)
	state.SanitizedDigest = ragIndexSanitizationDigest(state)
	if removed > 0 {
		state.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return removed
}

const ragSanitizationPolicyVersion = "project-contamination-v1"

func ragIndexSanitizationDigest(state *domain.RAGIndexState) string {
	if state == nil || len(state.ChunkHashes) == 0 {
		return ""
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(ragSanitizationPolicyVersion))
	for _, hash := range state.ChunkHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(hash))
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil))
}

func ragChunkHasProjectContamination(st *store.Store, chunk domain.RAGChunk) bool {
	if st == nil {
		return false
	}
	metadata := ""
	if len(chunk.Metadata) > 0 {
		if data, err := json.Marshal(chunk.Metadata); err == nil {
			metadata = string(data)
		}
	}
	text := strings.Join([]string{chunk.SourcePath, chunk.Text, chunk.Summary, chunk.Context, strings.Join(chunk.Keywords, " "), metadata}, "\n")
	return len(toolspkg.SecondAlgorithmProjectContaminationViolations(st, text)) > 0
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
		return 0, nil
	}
	st := store.NewStore(outputDir)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return 0, err
	}
	if state == nil {
		state = &domain.RAGIndexState{SchemaVersion: domain.CurrentRAGIndexSchemaVersion, Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	beforeSanitizedDigest := state.SanitizedDigest
	changed := sanitizeRAGIndexState(st, state) > 0 || state.SanitizedDigest != beforeSanitizedDigest
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
		sourceCount := 0
		same := 0
		for _, existing := range state.Chunks {
			if existing.SourcePath == sourcePath {
				sourceCount++
				if rag.NormalizeChunk(existing).Hash == chunk.Hash {
					same++
				}
			}
		}
		if sourceCount == 1 && same == 1 {
			continue
		}
		filtered := make([]domain.RAGChunk, 0, len(state.Chunks)+1)
		for _, existing := range state.Chunks {
			if existing.SourcePath == sourcePath {
				continue
			}
			filtered = append(filtered, existing)
		}
		state.Chunks = append(filtered, chunk)
		changed = true
	}
	if !changed {
		return len(chapters), nil
	}
	state.ChunkHashes = rebuildLocalRAGChunkHashes(state.Chunks)
	state.SanitizedDigest = ragIndexSanitizationDigest(state)
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return 0, err
	}
	return len(chapters), nil
}

func discoverGeneratedChapters(outputDir string, start, end int) ([]int, error) {
	entries, err := os.ReadDir(filepath.Join(outputDir, "chapters"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
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

// appendConfiguredSharedLibraries 把配置的共享库（写作手法库 + 对标素材库 + 显式校准库）
// 追加进索引来源（去重）。让 --build-rag / --zero-init 的重建默认保留这些 chunk，复用不靠记忆。
func appendConfiguredSharedLibraries(sources []string, cfg bootstrap.Config) []string {
	sources = appendSharedLibrary(sources, cfg.RAG.CraftLibrary, "rag.craft_library", rag.IsCraftTechniquePath)
	sources = appendSharedLibrary(sources, cfg.RAG.BenchmarkLibrary, "rag.benchmark_library", rag.IsBenchmarkLibraryPath)
	sources = appendSharedLibrary(sources, cfg.RAG.CalibrationLibrary, "rag.calibration_library", rag.IsCalibrationPath)
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
