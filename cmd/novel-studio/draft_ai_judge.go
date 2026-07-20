package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

type draftAIJudgeFlags struct {
	Chapter     int
	Start       int
	End         int
	Budget      time.Duration
	NoSediment  bool
	PrimaryOnly bool
}

func hasDraftAIJudgeFlag(argv []string) bool {
	for _, arg := range argv {
		if arg == "--draft-ai-judge" {
			return true
		}
	}
	return false
}

func parseDraftAIJudgeFlags(argv []string) (draftAIJudgeFlags, error) {
	fs := flag.NewFlagSet("draft-ai-judge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var flags draftAIJudgeFlags
	flags.Budget = 180 * time.Second
	fs.IntVar(&flags.Chapter, "chapter", 0, "只预审指定章草稿")
	fs.IntVar(&flags.Start, "from", 0, "起始章号（含）")
	fs.IntVar(&flags.End, "to", 0, "结束章号（含）")
	fs.DurationVar(&flags.Budget, "budget", flags.Budget, "每章外审墙钟预算")
	fs.BoolVar(&flags.NoSediment, "no-sediment", false, "不把本轮通用建议沉淀到 RAG")
	fs.BoolVar(&flags.PrimaryOnly, "primary-only", false, "只允许 reviewer 的主 provider/model，不自动切换 fallback")
	if err := fs.Parse(argv); err != nil {
		return flags, err
	}
	if len(fs.Args()) > 0 {
		return flags, fmt.Errorf("不接受额外参数: %v", fs.Args())
	}
	if flags.Chapter > 0 && (flags.Start > 0 || flags.End > 0) {
		return flags, fmt.Errorf("--chapter 不能与 --from/--to 同时使用")
	}
	if flags.Budget <= 0 {
		return flags, fmt.Errorf("--budget 必须大于 0")
	}
	return flags, nil
}

func draftAIJudgePipeline(opts cliOptions, argv []string) error {
	if hasHelpToken(argv) {
		fmt.Fprintln(os.Stdout, "用法: novel-studio --draft-ai-judge [--chapter N | --from N --to N] [--budget 180s]")
		fmt.Fprintln(os.Stdout, "读取 drafts/NN.draft.md，结果独立写入 reviews/drafts/，不会覆盖正式章节审核。")
		return nil
	}
	flags, err := parseDraftAIJudgeFlags(argv)
	if err != nil {
		return err
	}
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("首次启动需要先完成配置引导")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	projectDir := strings.TrimSpace(opts.Dir)
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	if err := normalizeOutputDirForInvocation(&cfg, projectDir); err != nil {
		return err
	}
	if flags.NoSediment {
		// Isolated sealed-render candidates may read only their frozen local
		// artifacts. Do not initialize or mutate the project's live vector index
		// merely to obtain an exact-body provider judgment.
		cfg.DisableLiveRAG = true
	}
	if flags.PrimaryOnly {
		cfg.DisableModelFailover = true
	}
	rules.EnsureHomeRulesDir()
	eng, err := host.New(cfg, assets.Load(cfg.Style))
	if err != nil {
		return fmt.Errorf("初始化 host: %w", err)
	}
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()

	chapters, err := draftChapterNumbers(filepath.Join(eng.Dir(), "drafts"))
	if err != nil {
		return err
	}
	chapters = selectDraftJudgeChapters(chapters, flags)
	if len(chapters) == 0 {
		return fmt.Errorf("指定范围没有 drafts/NN.draft.md")
	}

	st := store.NewStore(eng.Dir())
	reviewer := eng.Models().ForRole("reviewer")
	provider, model, explicit := eng.Models().CurrentSelection("reviewer")
	selection := deepseekAIJudgeModelSelection{Provider: provider, Model: model, Explicit: explicit}
	if !strings.EqualFold(strings.TrimSpace(provider), "deepseek") || !strings.Contains(strings.ToLower(strings.TrimSpace(model)), "deepseek") {
		return fmt.Errorf("草稿外判要求 reviewer 显式配置为 DeepSeek，当前为 %s/%s；已拒绝用其他模型冒充 DeepSeek", provider, model)
	}

	var embedder rag.Embedder
	var vectorWriter rag.VectorWriter
	if !flags.NoSediment {
		if value, enabled, loadErr := bootstrap.NewRAGEmbedder(cfg); loadErr == nil && enabled {
			embedder = value
		} else if loadErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] RAG embedding 初始化失败，仅写本地索引：%v\n", loadErr)
		}
		if value, enabled, loadErr := bootstrap.NewRAGQdrantClient(cfg, false); loadErr == nil && enabled {
			vectorWriter = value
		} else if loadErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] Qdrant 初始化失败，仅写本地索引：%v\n", loadErr)
		}
	}

	failed := 0
	for _, chapter := range chapters {
		path := filepath.Join(eng.Dir(), "drafts", fmt.Sprintf("%02d.draft.md", chapter))
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 读取失败：%v\n", chapter, readErr)
			failed++
			continue
		}
		body := string(raw)
		existingGate, gateErr := toolspkg.InspectDraftExternalGateWithStore(st, chapter)
		if gateErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 读取既有外部门禁失败：%v\n", chapter, gateErr)
			failed++
			continue
		}
		preserveRegisteredRetest := toolspkg.RequiresRegisteredExternalRetest(existingGate.Requirement)
		fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d DeepSeek(%s/%s) 裸正文预审中（预算 %s）...\n", chapter, provider, model, flags.Budget)
		result := loadOrGenerateDeepSeekAIJudge(eng.Dir(), reviewer, selection, chapter, body, flags.Budget)
		fmt.Fprintf(os.Stderr,
			"[draft-ai-judge:timing] ch%02d elapsed=%s cache=%t model_calls=%d\n",
			chapter, result.Elapsed.Round(time.Millisecond), result.CacheHit, result.ModelCalls,
		)
		if result.CacheLoadErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 缓存无效，已回源模型：%v\n", chapter, result.CacheLoadErr)
		}
		if result.Err != nil || result.Artifact == nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 外审失败：%v\n", chapter, result.Err)
			failed++
			continue
		}
		artifact := result.Artifact
		if !result.CacheHit {
			if saveErr := saveDeepSeekAIJudgeCache(eng.Dir(), artifact); saveErr != nil {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 缓存写入失败：%v\n", chapter, saveErr)
				failed++
				continue
			}
		}
		sanitizeDeepSeekAIJudgeForProject(st, artifact)
		if !artifact.AdviceComplete {
			// Sanitization is deterministic for the same project/body/protocol.
			// Re-calling DeepSeek under the identical cache identity cannot prove a
			// new fact and used to add another full provider budget. Persist the
			// fail-closed artifact; only a changed protocol/prompt may call again.
			if saveErr := saveDraftDeepSeekAIJudge(eng.Dir(), artifact); saveErr != nil {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 净化后不完整结果写入失败：%v\n", chapter, saveErr)
			} else {
				fmt.Fprintf(os.Stderr,
					"[draft-ai-judge] ch%02d 建议经项目门禁净化后不完整；同一 body/cache key 禁止重复回源，已保留精确结果并失败闭锁：%s\n",
					chapter, artifact.AdviceWarning,
				)
			}
			failed++
			continue
		}
		if saveErr := saveDraftDeepSeekAIJudge(eng.Dir(), artifact); saveErr != nil {
			fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 结果写入失败：%v\n", chapter, saveErr)
			failed++
			continue
		}
		if artifact.Blocking && !preserveRegisteredRetest {
			requirement := toolspkg.DraftExternalRerenderRequirement{
				Chapter:              chapter,
				EvaluatedBodySHA256:  artifact.BodySHA256,
				AIProbabilityPercent: artifact.AIProbabilityPercent,
				PassExclusivePercent: artifact.PassExclusivePercent,
				Summary:              artifact.Summary,
				Evidence:             artifact.Evidence,
				RevisionPlan:         artifact.RevisionPlan,
				AdviceComplete:       artifact.AdviceComplete,
			}
			if markerErr := toolspkg.SetDraftExternalRerenderRequirement(eng.Dir(), requirement); markerErr != nil {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 整章重渲染标记写入失败：%v\n", chapter, markerErr)
				failed++
				continue
			}
			if !artifact.AdviceComplete {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 阻断结果缺少完整可执行建议，已锁定草稿并要求重新外判\n", chapter)
				failed++
				continue
			}
		} else if !artifact.Blocking && !preserveRegisteredRetest {
			cleared, markerErr := clearDraftRerenderRequirementAfterPassingJudge(st, chapter)
			if markerErr != nil {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d 清理整章重渲染标记失败：%v\n", chapter, markerErr)
				failed++
				continue
			}
			if !cleared {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d DeepSeek 已通过，但当前精确哈希仍可复现本地 whole-text/segment 结构阻断；保留整章重渲染锁\n", chapter)
			}
		}
		if !flags.NoSediment && artifact.AdviceComplete {
			if ragErr := sedimentDraftDeepSeekAIJudgeRAG(context.Background(), st, embedder, vectorWriter, artifact); ragErr != nil {
				fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d RAG 沉淀失败：%v\n", chapter, ragErr)
				failed++
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "[draft-ai-judge] ch%02d ai=%d%% target=<%d%% blocking=%t advice_complete=%t\n", chapter, artifact.AIProbabilityPercent, deepseekAIJudgePassExclusive, artifact.Blocking, artifact.AdviceComplete)
	}
	if failed > 0 {
		return fmt.Errorf("草稿外审未全部完成：失败 %d 章", failed)
	}
	return nil
}

// clearDraftRerenderRequirementAfterPassingJudge only clears an independent-
// model marker when the current bytes are also free of deterministic local
// whole-text/segment blockers. A provider pass is corroboration, not authority
// to erase a reproducible structural failure or reset its bounded retry budget.
func clearDraftRerenderRequirementAfterPassingJudge(st *store.Store, chapter int) (bool, error) {
	if toolspkg.CurrentDraftHasLocalStructuralBlock(st, chapter) {
		return false, nil
	}
	if err := toolspkg.ClearDraftExternalRerenderRequirement(st.Dir(), chapter); err != nil {
		return false, err
	}
	return true, nil
}

func draftChapterNumbers(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取草稿目录 %s: %w", dir, err)
	}
	seen := make(map[int]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".draft.md") {
			continue
		}
		prefix := strings.TrimSuffix(entry.Name(), ".draft.md")
		chapter, parseErr := strconv.Atoi(prefix)
		if parseErr == nil && chapter > 0 {
			seen[chapter] = true
		}
	}
	chapters := make([]int, 0, len(seen))
	for chapter := range seen {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	return chapters, nil
}

func selectDraftJudgeChapters(chapters []int, flags draftAIJudgeFlags) []int {
	selected := make([]int, 0, len(chapters))
	for _, chapter := range chapters {
		if flags.Chapter > 0 && chapter != flags.Chapter {
			continue
		}
		if flags.Start > 0 && chapter < flags.Start {
			continue
		}
		if flags.End > 0 && chapter > flags.End {
			continue
		}
		selected = append(selected, chapter)
	}
	return selected
}

func saveDraftDeepSeekAIJudge(projectDir string, artifact *deepseekAIJudgeArtifact) error {
	if artifact == nil {
		return fmt.Errorf("DeepSeek 草稿预审 artifact 为空")
	}
	if err := validateDeepSeekAIJudgeArtifactIdentity(artifact, artifact.CachePolicy); err != nil {
		return err
	}
	dir := filepath.Join(projectDir, "reviews", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_deepseek_ai_judge.json", artifact.Chapter)), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_deepseek_ai_judge.md", artifact.Chapter)), []byte(renderDeepSeekAIJudgeMarkdown(*artifact)), 0o644)
}

func sedimentDraftDeepSeekAIJudgeRAG(
	ctx context.Context,
	st *store.Store,
	embedder rag.Embedder,
	vectorWriter rag.VectorWriter,
	artifact *deepseekAIJudgeArtifact,
) error {
	if artifact == nil || !artifact.AdviceComplete {
		return nil
	}
	chunks := deepseekAIJudgeRAGChunks(*artifact)
	for i := range chunks {
		chunks[i].ID = fmt.Sprintf("chapter:%03d:draft_deepseek_ai_judge", artifact.Chapter)
		chunks[i].SourcePath = fmt.Sprintf("reviews/drafts/%02d_deepseek_ai_judge.json", artifact.Chapter)
		chunks[i].Context = fmt.Sprintf("第 %d 章草稿 DeepSeek 裸正文预审", artifact.Chapter)
		chunks[i].Keywords = append(chunks[i].Keywords, "draft_preflight")
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = map[string]any{}
		}
		chunks[i].Metadata["draft_preflight"] = true
	}
	return toolspkg.UpsertRAGChunks(ctx, st, embedder, vectorWriter, chunks, domain.RAGIndexConfig{})
}
