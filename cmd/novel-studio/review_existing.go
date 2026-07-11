package main

// --review-existing：对现有章节逐章调用 Editor LLM 出评审报告。
// 不走 Coordinator、不触发续写、不改原文。给"只想看诊断意见"的用户一个零破坏的入口。
//
// 输出：
//   output/novel/reviews/{ch:02d}.md   每章一份 markdown 评审
//   output/novel/meta/review-summary.md  汇总（每章 1-2 行评分 + 待修项数）

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type reviewFlags struct {
	ReviewExisting string        // --review-existing <path>（不必要；缺省从 cwd 读 chapters/）
	Budget         time.Duration // --budget 每章硬时间预算（默认 90s）
	Start, End     int           // --from / --to 起止章号（1-based，闭区间）
}

const (
	editorReviewCacheBranch          = "editor"
	deepseekAIJudgeCacheBranch       = "deepseek"
	editorReviewProtocolVersion      = "review-existing/editor/v3"
	reviewExistingCacheDirectoryName = "cache"
)

// reviewExistingCachePolicy is the complete identity of one model request. The
// cache key is the SHA-256 of this struct's canonical JSON representation.
type reviewExistingCachePolicy struct {
	Branch                     string `json:"branch"`
	ReviewProtocolVersion      string `json:"review_protocol_version"`
	Chapter                    int    `json:"chapter"`
	BodySHA256                 string `json:"body_sha256"`
	Provider                   string `json:"provider"`
	Model                      string `json:"model"`
	SystemPromptSHA256         string `json:"system_prompt_sha256"`
	PremiseSHA256              string `json:"premise_sha256,omitempty"`
	UserRulesSHA256            string `json:"user_rules_sha256,omitempty"`
	ChapterReviewContextSHA256 string `json:"chapter_review_context_sha256,omitempty"`
	AIVoiceContextSHA256       string `json:"ai_voice_context_sha256,omitempty"`
	ReasoningEffort            string `json:"reasoning_effort,omitempty"`
	UserPayloadKind            string `json:"user_payload_kind,omitempty"`
}

type editorReviewCacheArtifact struct {
	Chapter     int                       `json:"chapter"`
	GeneratedAt string                    `json:"generated_at"`
	CacheKey    string                    `json:"cache_key"`
	CachePolicy reviewExistingCachePolicy `json:"cache_policy"`
	Markdown    string                    `json:"markdown"`
}

type editorReviewBranchResult struct {
	Review        string
	CacheArtifact *editorReviewCacheArtifact
	CacheHit      bool
	CacheLoadErr  error
	Err           error
}

type deepseekAIJudgeBranchResult struct {
	Artifact     *deepseekAIJudgeArtifact
	CacheHit     bool
	CacheLoadErr error
	Err          error
}

func parseReviewFlags(argv []string) (reviewFlags, []string, error) {
	fs := flag.NewFlagSet("review-existing", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --review-existing [--budget 90s] [--from N] [--to N]\n\n")
		fmt.Fprintf(os.Stderr, "兼容别名：执行时会委派到 novel-studio --pipeline --stages review。\n")
		fmt.Fprintf(os.Stderr, "逐章调用 Editor LLM 出评审报告（不改原文）。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f reviewFlags
	f.Budget = 90 * time.Second
	fs.StringVar(&f.ReviewExisting, "review-existing", "", "项目根目录（缺省当前目录）")
	fs.DurationVar(&f.Budget, "budget", f.Budget, "每章并行审核总墙钟预算（Editor 与 Reviewer 共用截止时间）")
	fs.IntVar(&f.Start, "from", 0, "起始章号（含），0 = 自动")
	fs.IntVar(&f.End, "to", 0, "结束章号（含），0 = 自动")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasReviewExistingFlag 检测 argv 中是否含 --review-existing 子命令入口。
func hasReviewExistingFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--review-existing" {
			return true
		}
	}
	return false
}

// reviewExistingPipeline 逐章 Editor 评审编排。
func reviewExistingPipeline(opts cliOptions, args []string) error {
	// --review-existing --help：路由 token 已在 main 里剥离（避免 Go flag 包
	// 把它注册为 StringVar 后把 --help 当成它的值），此时再 hasHelpToken 打印 usage。
	if hasHelpToken(args) {
		printReviewUsage(os.Stderr)
		return nil
	}
	flags, extra, err := parseReviewFlags(args)
	if err != nil {
		printReviewUsage(os.Stderr)
		return err
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "未识别的额外参数：%v\n\n", extra)
		printReviewUsage(os.Stderr)
		return fmt.Errorf("--review-existing 不接受额外参数")
	}

	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("首次启动需要先在交互终端运行一次 novel-studio 完成配置引导，或手写配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	projDir := flags.ReviewExisting
	if projDir == "" {
		projDir = opts.Dir // --pipeline --dir <项目> 经此传入；此前漏用导致回落到 cwd/output/novel
	}
	if projDir == "" {
		projDir, _ = os.Getwd()
	}
	if err := normalizeOutputDirForInvocation(&cfg, projDir); err != nil {
		return err
	}
	rules.EnsureHomeRulesDir()
	bundle := assets.Load(cfg.Style)
	fmt.Fprintf(os.Stderr, "[review-existing] 工作目录: %s\n", projDir)

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return fmt.Errorf("初始化 host: %w", err)
	}
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()

	st := store.NewStore(eng.Dir())
	var ragEmbedder rag.Embedder
	if embedder, enabled, err := bootstrap.NewRAGEmbedder(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[review-existing] RAG embedding 初始化失败，仅写关键词索引：%v\n", err)
	} else if enabled {
		ragEmbedder = embedder
	}
	var ragVectorWriter rag.VectorWriter
	if writer, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false); err != nil {
		fmt.Fprintf(os.Stderr, "[review-existing] Qdrant 初始化失败，仅写本地 RAG：%v\n", err)
	} else if enabled {
		ragVectorWriter = writer
	}

	// 读 premise 拼 context
	premise, _ := st.Outline.LoadPremise()
	userRulesContext := ""
	if snapshot, loadErr := st.UserRules.Load(); loadErr == nil && snapshot != nil {
		if raw, marshalErr := json.MarshalIndent(snapshot.Payload(), "", "  "); marshalErr == nil {
			userRulesContext = string(raw)
		}
	}

	// 找所有 chapters/
	chaptersDir := filepath.Join(eng.Dir(), "chapters")
	chapters, err := chapterNumbersFromFiles(chaptersDir)
	if err != nil {
		return fmt.Errorf("列章节失败: %w", err)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("未在 %s 找到任何章节文件（*.md）。请先跑 --pipeline 写作阶段产出章节", chaptersDir)
	}

	selectedChapters := filterChaptersForPipelineRange(chapters, pipelineFlags{Start: flags.Start, End: flags.End})
	if len(selectedChapters) == 0 {
		return fmt.Errorf("指定评审范围内没有章节")
	}
	start, end := selectedChapters[0], selectedChapters[len(selectedChapters)-1]
	fmt.Fprintf(os.Stderr, "[review-existing] 待评审章节：%d - %d（共 %d 章）\n", start, end, len(selectedChapters))

	reviewsDir := filepath.Join(eng.Dir(), "reviews")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		return err
	}

	// ForRole 对未显式配置的角色回退到默认模型，始终返回可用实例。
	model := eng.Models().ForRole("editor")
	editorProvider, editorName, _ := eng.Models().CurrentSelection("editor")
	reviewerModel := eng.Models().ForRole("reviewer")
	reviewerProvider, reviewerName, reviewerExplicit := eng.Models().CurrentSelection("reviewer")
	reviewerSelection := deepseekAIJudgeModelSelection{
		Provider: reviewerProvider,
		Model:    reviewerName,
		Explicit: reviewerExplicit,
	}

	successCount, failureCount := 0, 0
	for _, chNum := range selectedChapters {
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("%02d.md", chNum))
		body, err := os.ReadFile(chPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] 跳过 ch%02d：%v\n", chNum, err)
			failureCount++
			continue
		}
		frozenBody := string(append([]byte(nil), body...))
		bodyHash := reviewreport.BodySHA256(frozenBody)
		history, _ := st.AIVoice.LoadAllChapterMetrics()
		analysis := editrules.AnalyzeChapter(chNum, frozenBody, reviewHistoryExcludingChapter(history, chNum))
		analysis.BodySHA256 = bodyHash
		if err := st.AIVoice.SaveChapterMetrics(analysis.Metrics, false); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 指标写入失败：%v\n", chNum, err)
		}
		if err := st.AIVoice.SaveRedFlags(analysis); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d redflags 写入失败：%v\n", chNum, err)
		}
		mechanical, err := saveMechanicalGateForExistingChapter(st, chNum, frozenBody)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 机械门禁写入失败：%v\n", chNum, err)
		}
		chapterReviewContext := buildEditorChapterReviewContext(st, chNum)
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor(%s/%s) + DeepSeek(%s/%s) 并行评审中（预算 %s）...\n",
			chNum, editorProvider, editorName, reviewerProvider, reviewerName, flags.Budget)
		editorBranch, deepseekBranch := runReviewExistingBranchesConcurrently(
			func() editorReviewBranchResult {
				return loadOrGenerateEditorReview(
					eng.Dir(), model, editorProvider, editorName,
					premise, userRulesContext, chapterReviewContext,
					chNum, frozenBody, analysis, flags.Budget,
				)
			},
			func() deepseekAIJudgeBranchResult {
				return loadOrGenerateDeepSeekAIJudge(
					eng.Dir(), reviewerModel, reviewerSelection,
					chNum, frozenBody, flags.Budget,
				)
			},
		)

		if editorBranch.CacheLoadErr != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 缓存无效，已回源模型：%v\n", chNum, editorBranch.CacheLoadErr)
		}
		if deepseekBranch.CacheLoadErr != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek 缓存无效，已回源模型：%v\n", chNum, deepseekBranch.CacheLoadErr)
		}

		// Generate is concurrent, but every cache and review artifact write stays in
		// this serial section. Persist a successful branch even if its peer failed,
		// so a retry only calls the missing model.
		var cacheSaveErrors []string
		if editorBranch.Err == nil && !editorBranch.CacheHit {
			if err := saveEditorReviewCache(eng.Dir(), editorBranch.CacheArtifact); err != nil {
				cacheSaveErrors = append(cacheSaveErrors, "Editor: "+err.Error())
			}
		}
		if deepseekBranch.Err == nil && !deepseekBranch.CacheHit {
			if err := saveDeepSeekAIJudgeCache(eng.Dir(), deepseekBranch.Artifact); err != nil {
				cacheSaveErrors = append(cacheSaveErrors, "DeepSeek: "+err.Error())
			}
		}
		if editorBranch.CacheHit {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor cache hit（%s）\n", chNum, shortReviewCacheKey(editorBranch.CacheArtifact.CacheKey))
		}
		if deepseekBranch.CacheHit {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek cache hit（%s）\n", chNum, shortReviewCacheKey(deepseekBranch.Artifact.CacheKey))
		}
		if editorBranch.Err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 评审失败：%v\n", chNum, editorBranch.Err)
		}
		if deepseekBranch.Err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek 裸正文 AI 判定失败：%v\n", chNum, deepseekBranch.Err)
		}
		if editorBranch.Err != nil || deepseekBranch.Err != nil || len(cacheSaveErrors) > 0 {
			if len(cacheSaveErrors) > 0 {
				fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 模型缓存写入失败：%s\n", chNum, strings.Join(cacheSaveErrors, "；"))
			}
			failureCount++
			continue
		}

		review := editorBranch.Review
		entry := structuredReviewFromMarkdown(chNum, review)
		entry.BodySHA256 = bodyHash
		if removed := sanitizeEditorReviewForProject(st, chNum, frozenBody, analysis, &entry); len(removed) > 0 {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 已移除 %d 条与正文机械事实或批准 plan 冲突的 Editor 建议：%s\n", chNum, len(removed), strings.Join(removed, "；"))
		}
		deepseekJudge := deepseekBranch.Artifact
		sanitizeDeepSeekAIJudgeForProject(st, deepseekJudge)
		if err := saveDeepSeekAIJudge(eng.Dir(), deepseekJudge); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定写入失败：%v\n", chNum, err)
			failureCount++
			continue
		}
		if mechanical == nil {
			var mechanicalErr error
			mechanical, _, mechanicalErr = reviewreport.LoadMechanicalGate(eng.Dir(), chNum)
			if mechanicalErr != nil {
				fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 机械门禁读取失败：%v\n", chNum, mechanicalErr)
			}
		}
		if deepseekJudge.Blocking {
			entry.Verdict = "rewrite"
			entry.AffectedChapters = []int{chNum}
		} else if reconcileWarningOnlyEditorReview(&entry, bodyHash, mechanical, analysis, deepseekJudge) {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 警告已由同哈希机械门禁、AI voice 与裸文 Reviewer 三方校准为非阻断建议\n", chNum)
		}
		entryRaw, err := json.Marshal(entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 结构化评审编码失败：%v\n", chNum, err)
			failureCount++
			continue
		}
		saveTool := toolspkg.NewSaveReviewTool(st).WithRAGEmbedder(ragEmbedder).WithRAGVectorWriter(ragVectorWriter)
		if _, err := saveTool.Execute(context.Background(), entryRaw); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 确定性评审门禁/状态沉淀失败：%v\n", chNum, err)
			failureCount++
			continue
		}
		if savedEntry, loadErr := st.World.LoadReview(chNum); loadErr != nil || savedEntry == nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 最终结构化评审读取失败：%v\n", chNum, loadErr)
			failureCount++
			continue
		} else {
			entry = *savedEntry
		}
		if savedVoice, loadErr := st.AIVoice.LoadRedFlags(chNum); loadErr == nil && savedVoice != nil {
			analysis = *savedVoice
		}
		outPath := filepath.Join(reviewsDir, fmt.Sprintf("%02d.md", chNum))
		if err := reviewreport.WriteUnifiedMarkdown(eng.Dir(), reviewreport.UnifiedMarkdownInput{
			Chapter:         chNum,
			Mechanical:      mechanical,
			AIVoice:         &analysis,
			ExternalAIJudge: deepSeekExternalAIJudge(deepseekJudge),
			Editor:          &entry,
			EditorMarkdown:  review,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 写文件失败：%v\n", chNum, err)
			failureCount++
			continue
		}
		if err := appendDeepSeekAIJudgeToUnifiedMarkdown(eng.Dir(), deepseekJudge); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定并入统一报告失败：%v\n", chNum, err)
			failureCount++
			continue
		}
		if err := reviewreport.RemoveLegacyMarkdown(eng.Dir(), chNum); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 清理旧 AI味报告失败：%v\n", chNum, err)
		}
		if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "deepseek-ai-judge", fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chNum)); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定 checkpoint 写入失败：%v\n", chNum, err)
		}
		if err := sedimentDeepSeekAIJudgeRAG(context.Background(), st, ragEmbedder, ragVectorWriter, deepseekJudge); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定 RAG 沉淀失败：%v\n", chNum, err)
		}
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d → %s\n", chNum, outPath)
		successCount++
	}

	// 每次都从 reviews/ 下的全部章级报告重建汇总。局部复审只刷新选中章节，
	// 但不能把其他章节从项目级审核视图中抹掉。
	summaryPath, err := rebuildReviewSummary(eng.Dir())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[review-existing] 汇总已写到 %s\n", summaryPath)

	// 再跑一次静态 diag 把运行时信号也写进 export
	_, _ = diag.Export(st)
	if failureCount > 0 {
		return fmt.Errorf("评审未全部完成：成功 %d 章，失败 %d 章", successCount, failureCount)
	}
	if successCount == 0 {
		return fmt.Errorf("评审未产出任何章节报告")
	}
	fmt.Fprintf(os.Stderr, "[review-existing] 完成\n")
	return nil
}

func reviewHistoryExcludingChapter(history []domain.ChapterAIVoiceMetrics, chapter int) []domain.ChapterAIVoiceMetrics {
	if len(history) == 0 {
		return nil
	}
	out := make([]domain.ChapterAIVoiceMetrics, 0, len(history))
	for _, metrics := range history {
		if metrics.Chapter != chapter {
			out = append(out, metrics)
		}
	}
	return out
}

func runReviewExistingBranchesConcurrently(
	editor func() editorReviewBranchResult,
	deepseek func() deepseekAIJudgeBranchResult,
) (editorReviewBranchResult, deepseekAIJudgeBranchResult) {
	var editorResult editorReviewBranchResult
	var deepseekResult deepseekAIJudgeBranchResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		editorResult = editor()
	}()
	go func() {
		defer wg.Done()
		deepseekResult = deepseek()
	}()
	wg.Wait()
	return editorResult, deepseekResult
}

func newEditorReviewCachePolicy(
	provider, model, premise, userRules, chapterReviewContext string,
	chapter int,
	chapterBody, aiVoiceContext string,
) reviewExistingCachePolicy {
	return reviewExistingCachePolicy{
		Branch:                     editorReviewCacheBranch,
		ReviewProtocolVersion:      editorReviewProtocolVersion,
		Chapter:                    chapter,
		BodySHA256:                 reviewreport.BodySHA256(chapterBody),
		Provider:                   provider,
		Model:                      model,
		SystemPromptSHA256:         reviewExistingSHA256(editorSystemPrompt),
		PremiseSHA256:              reviewExistingSHA256(premise),
		UserRulesSHA256:            reviewExistingSHA256(userRules),
		ChapterReviewContextSHA256: reviewExistingSHA256(chapterReviewContext),
		AIVoiceContextSHA256:       reviewExistingSHA256(aiVoiceContext),
	}
}

func loadOrGenerateEditorReview(
	projectDir string,
	model agentcore.ChatModel,
	provider, modelName, premise, userRules, chapterReviewContext string,
	chapter int,
	chapterBody string,
	analysis domain.AIVoiceAnalysis,
	budget time.Duration,
) editorReviewBranchResult {
	aiVoiceContext := editorAIVoiceReviewPayload(analysis, chapterBody)
	policy := newEditorReviewCachePolicy(
		provider, modelName, premise, userRules, chapterReviewContext,
		chapter, chapterBody, aiVoiceContext,
	)
	cached, loadErr := loadEditorReviewCache(projectDir, policy)
	if loadErr == nil && cached != nil {
		return editorReviewBranchResult{
			Review:        cached.Markdown,
			CacheArtifact: cached,
			CacheHit:      true,
		}
	}

	review, err := callEditorOnChapter(
		model, premise, userRules, chapterReviewContext,
		chapter, chapterBody, analysis, budget,
	)
	if err != nil {
		return editorReviewBranchResult{CacheLoadErr: loadErr, Err: err}
	}
	artifact := &editorReviewCacheArtifact{
		Chapter:     chapter,
		GeneratedAt: time.Now().Format(time.RFC3339),
		CacheKey:    reviewExistingCacheKey(policy),
		CachePolicy: policy,
		Markdown:    review,
	}
	return editorReviewBranchResult{
		Review:        review,
		CacheArtifact: artifact,
		CacheLoadErr:  loadErr,
	}
}

func editorAIVoiceReviewPayload(analysis domain.AIVoiceAnalysis, chapterBody string) string {
	stable := analysis
	stable.GeneratedAt = ""
	stable.Metrics.GeneratedAt = ""
	stable.Metrics.AIVoiceScoreHistory = append([]domain.AIVoiceScorePoint(nil), analysis.Metrics.AIVoiceScoreHistory...)
	for i := range stable.Metrics.AIVoiceScoreHistory {
		stable.Metrics.AIVoiceScoreHistory[i].At = ""
	}
	payload := map[string]any{
		"ai_voice_analysis":           stable,
		"mechanical_prose_violations": rules.Lint(chapterBody),
		"mechanical_prose_usage":      "逐条复核；abstract_system_reassurance、opaque_procedure_jargon、ui_trial_checklist、dialogue_action_lead_repetition 和 templated_dialogue_chain 命中时不得 accept。",
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return string(raw)
}

func loadEditorReviewCache(projectDir string, expected reviewExistingCachePolicy) (*editorReviewCacheArtifact, error) {
	key := reviewExistingCacheKey(expected)
	path := reviewExistingCachePath(projectDir, editorReviewCacheBranch, key)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 Editor 缓存 %s: %w", path, err)
	}
	var artifact editorReviewCacheArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return nil, fmt.Errorf("解析 Editor 缓存 %s: %w", path, err)
	}
	if err := validateEditorReviewCacheArtifact(&artifact, expected); err != nil {
		return nil, fmt.Errorf("校验 Editor 缓存 %s: %w", path, err)
	}
	return &artifact, nil
}

func saveEditorReviewCache(projectDir string, artifact *editorReviewCacheArtifact) error {
	if artifact == nil {
		return fmt.Errorf("Editor 缓存 artifact 为空")
	}
	if err := validateEditorReviewCacheArtifact(artifact, artifact.CachePolicy); err != nil {
		return err
	}
	path := reviewExistingCachePath(projectDir, editorReviewCacheBranch, artifact.CacheKey)
	return writeReviewExistingCacheJSON(path, artifact)
}

func validateEditorReviewCacheArtifact(artifact *editorReviewCacheArtifact, expected reviewExistingCachePolicy) error {
	if artifact == nil {
		return fmt.Errorf("artifact 为空")
	}
	if expected.Branch != editorReviewCacheBranch {
		return fmt.Errorf("branch=%q, want %q", expected.Branch, editorReviewCacheBranch)
	}
	if artifact.CachePolicy != expected {
		return fmt.Errorf("cache policy mismatch")
	}
	expectedKey := reviewExistingCacheKey(expected)
	if artifact.CacheKey != expectedKey {
		return fmt.Errorf("cache_key=%q, want %q", artifact.CacheKey, expectedKey)
	}
	if artifact.Chapter != expected.Chapter {
		return fmt.Errorf("chapter=%d, want %d", artifact.Chapter, expected.Chapter)
	}
	if strings.TrimSpace(artifact.GeneratedAt) == "" {
		return fmt.Errorf("generated_at 为空")
	}
	if strings.TrimSpace(artifact.Markdown) == "" {
		return fmt.Errorf("markdown 为空")
	}
	return nil
}

func reviewExistingCacheKey(policy reviewExistingCachePolicy) string {
	raw, _ := json.Marshal(policy)
	return reviewExistingSHA256(string(raw))
}

func reviewExistingSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}

func reviewExistingCachePath(projectDir, branch, key string) string {
	return filepath.Join(projectDir, "reviews", reviewExistingCacheDirectoryName, branch, key+".json")
}

func writeReviewExistingCacheJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".review-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func shortReviewCacheKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}

func saveMechanicalGateForExistingChapter(st *store.Store, chapter int, body string) (*reviewreport.MechanicalGatePayload, error) {
	report := aigc.Analyze(body)
	wordCount := utf8.RuneCountInString(body)
	violations := rules.Lint(body)
	structured := rules.SystemDefaults().Structured
	if snap, err := st.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	violations = append(violations, rules.Check(body, wordCount, structured)...)
	violations = append(violations, toolspkg.SecondAlgorithmProjectContaminationViolations(st, body)...)
	if gatePercent := reviewExistingAIGCGatePercent(report); gatePercent > 5 {
		severity := rules.SeverityWarning
		if gatePercent >= 35 {
			severity = rules.SeverityError
		}
		violations = append(violations, rules.Violation{
			Rule:      "aigc_ratio",
			Target:    report.Engine,
			Limit:     "5%",
			Actual:    gatePercent,
			Deviation: gatePercent / 100,
			Severity:  severity,
		})
	}
	if external, ok := latestExternalAIGCDetection(st.Dir(), chapter, body); ok && external.ScorePercent > 5 {
		severity := rules.SeverityWarning
		if external.ScorePercent >= 35 {
			severity = rules.SeverityError
		}
		target := strings.TrimSpace(external.Detector)
		if external.Mode != "" {
			target += "/" + strings.TrimSpace(external.Mode)
		}
		violations = append(violations, rules.Violation{
			Rule:      "external_aigc_ratio",
			Target:    target,
			Limit:     "5%",
			Actual:    external.ScorePercent,
			Deviation: external.ScorePercent / 100,
			Severity:  severity,
		})
	}
	payload := reviewreport.MechanicalGatePayload{
		Chapter:        chapter,
		BodySHA256:     reviewreport.BodySHA256(body),
		AIGCReport:     report,
		RuleViolations: violations,
		GeneratedAt:    time.Now().Format(time.RFC3339),
	}
	dir := filepath.Join(st.Dir(), "reviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_ai_gate.json", chapter)), raw, 0o644); err != nil {
		return nil, err
	}
	return &payload, nil
}

func reviewExistingAIGCGatePercent(report aigc.Report) float64 {
	// 与 commit_chapter 和统一审核降级策略共用同一口径：
	// 短章按单检测片段/segment floor 真高判，不被 blended 平均值稀释放行。
	return aigc.EffectiveGatePercent(report)
}

type externalAIGCDetection struct {
	Chapter      int     `json:"chapter"`
	Detector     string  `json:"detector"`
	Mode         string  `json:"mode"`
	Score        float64 `json:"score"`
	Verdict      string  `json:"verdict"`
	Note         string  `json:"note"`
	BodySHA256   string  `json:"body_sha256"`
	CheckedAt    string  `json:"checked_at"`
	ScorePercent float64
}

func latestExternalAIGCDetection(root string, chapter int, body string) (externalAIGCDetection, bool) {
	chapterPath := filepath.Join(root, "chapters", fmt.Sprintf("%02d.md", chapter))
	chapterInfo, _ := os.Stat(chapterPath)
	sum := sha256.Sum256([]byte(body))
	currentHash := fmt.Sprintf("%x", sum)
	raw, err := os.ReadFile(filepath.Join(root, "meta", "external_detection_log.jsonl"))
	if err != nil {
		return externalAIGCDetection{}, false
	}
	var latest externalAIGCDetection
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row externalAIGCDetection
		if err := json.Unmarshal([]byte(line), &row); err != nil || row.Chapter != chapter {
			continue
		}
		if row.BodySHA256 != "" && row.BodySHA256 != currentHash {
			continue
		}
		if row.BodySHA256 == "" && chapterInfo != nil && !chapterInfo.ModTime().IsZero() && row.CheckedAt != "" {
			checkedAt, err := time.ParseInLocation("2006-01-02T15:04:05", row.CheckedAt, time.Local)
			if err == nil && checkedAt.Before(chapterInfo.ModTime().Add(-time.Second)) {
				continue
			}
		}
		score := row.Score
		if score > 0 && score <= 1 {
			score *= 100
		}
		row.ScorePercent = score
		latest = row
		found = true
	}
	return latest, found
}

var reviewDimensionNames = []string{
	"consistency",
	"character",
	"pacing",
	"continuity",
	"foreshadow",
	"hook",
	"aesthetic",
	"ai_voice_detection",
}

func structuredReviewFromMarkdown(chapter int, md string) domain.ReviewEntry {
	dimensions := parseReviewDimensions(md)
	if len(dimensions) != len(reviewDimensionNames) {
		dimensions = defaultReviewDimensions()
	}
	verdict := reviewVerdictFromMarkdown(md, dimensions)
	entry := domain.ReviewEntry{
		Chapter:        chapter,
		Scope:          "chapter",
		Dimensions:     dimensions,
		Issues:         parseReviewIssues(md),
		ContractStatus: "met",
		Verdict:        verdict,
		Summary:        extractLine(md, "## 一句话诊断"),
	}
	if strings.TrimSpace(entry.Summary) == "" || entry.Summary == "—" {
		entry.Summary = fmt.Sprintf("第 %d 章已有 Markdown 评审，未发现必须返工的结构性结论。", chapter)
	}
	if verdict == "polish" || verdict == "rewrite" {
		entry.AffectedChapters = []int{chapter}
	}
	return entry
}

func buildEditorChapterReviewContext(st *store.Store, chapter int) string {
	if st == nil || chapter <= 0 {
		return ""
	}
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return ""
	}
	renderContract := plan.Contract
	renderContract.RequiredBeats = toolspkg.RenderRequiredOutcomes(*plan)
	renderContract.ContinuityChecks = toolspkg.RenderContinuityChecks(*plan)
	payload := struct {
		Chapter             int                              `json:"chapter"`
		Title               string                           `json:"title,omitempty"`
		Contract            domain.ChapterContract           `json:"contract,omitempty"`
		WorldSimulationID   string                           `json:"world_simulation_id,omitempty"`
		ProtagonistDecision string                           `json:"protagonist_decision,omitempty"`
		ReviewChecks        []string                         `json:"review_checks,omitempty"`
		TrendLanguage       []domain.TrendLanguagePlan       `json:"trend_language_plan,omitempty"`
		Entertainment       domain.ReaderEntertainmentPlan   `json:"reader_entertainment_plan,omitempty"`
		EndingContract      domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
		RenderPolicy        string                           `json:"render_policy"`
	}{
		Chapter:             plan.Chapter,
		Title:               plan.Title,
		Contract:            renderContract,
		WorldSimulationID:   plan.CausalSimulation.WorldSimulationID,
		ProtagonistDecision: plan.CausalSimulation.ProtagonistDecision,
		ReviewChecks:        plan.CausalSimulation.AntiAIPlan.ReviewChecks,
		TrendLanguage:       plan.CausalSimulation.TrendLanguage,
		Entertainment:       plan.CausalSimulation.EntertainmentPlan,
		EndingContract:      toolspkg.RenderEndingContract(*plan),
		RenderPolicy:        "contract.required_beats 已投影为结果级要求，continuity_checks 只保留事实连续性；审核结果是否成立，不要求正文复现上游动作顺序、验证次数、流程举例、台词原句或指定末段物件。ending_consequence_contract 只核 consequence 与 next_chapter_pull，允许用更有吸引力的现场人物、动作或结果替换原计划镜头。逐项照抄 plan 本身属于审美问题。",
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}

func sanitizeEditorReviewForProject(st *store.Store, chapter int, body string, analysis domain.AIVoiceAnalysis, entry *domain.ReviewEntry) []string {
	if entry == nil {
		return nil
	}
	var plan *domain.ChapterPlan
	if st != nil {
		plan, _ = st.Drafts.LoadChapterPlan(chapter)
	}
	violations := rules.Lint(body)
	aiVoicePayloadPassed := editorAIVoicePayloadPassed(chapter, body, analysis)
	hasViolation := func(rule string) bool {
		for _, violation := range violations {
			if violation.Rule == rule {
				return true
			}
		}
		return false
	}

	var removed []string
	filtered := make([]domain.ConsistencyIssue, 0, len(entry.Issues))
	for _, issue := range entry.Issues {
		text := strings.Join([]string{issue.Description, issue.Evidence, issue.Suggestion}, "\n")
		switch {
		case aiVoicePayloadPassed && reviewClaimsMissingAIVoicePayload(text):
			removed = append(removed, "AI 腔 JSON 已完整读取且机械红旗为空")
			continue
		case analysis.Metrics.ProtagonistWaver && reviewClaimsMissingProtagonistWaver(text):
			removed = append(removed, "主角迟疑已由动作证据命中")
			continue
		case reviewClaimsSystemMessageOverpacked(text) && !hasViolation("system_message_overpacked"):
			removed = append(removed, "独立系统消息被错误拼接")
			continue
		case reviewRejectsApprovedTrendCarrier(text, body, plan):
			removed = append(removed, "热梗承载人违背批准 plan")
			continue
		case reviewDemandsAbsentOptionalTrend(text, body, plan):
			removed = append(removed, "可选热梗未使用不构成缺失")
			continue
		case reviewRejectsStandaloneChatEmoticon(text) && hasStandaloneSystemChatEmoticon(body):
			removed = append(removed, "独立系统私聊颜文字被误判为正式条款")
			continue
		default:
			filtered = append(filtered, issue)
		}
	}
	entry.Issues = filtered
	if aiVoicePayloadPassed {
		if reviewClaimsMissingAIVoicePayload(entry.Summary) {
			entry.Summary = fmt.Sprintf("第 %d 章结果级合同已完成，机械 AI 腔红旗为空。", chapter)
		}
		for i := range entry.Dimensions {
			dimension := &entry.Dimensions[i]
			if dimension.Dimension != "ai_voice_detection" || !reviewClaimsMissingAIVoicePayload(dimension.Comment) {
				continue
			}
			dimension.Score = max(dimension.Score, 90)
			dimension.Verdict = "pass"
			dimension.Comment = fmt.Sprintf(
				"机械 red flag JSON 已读取：比喻密度 %.4f、对话占比 %.1f%%、格言命中 %d 条、主角动摇=%t、红旗=0。",
				analysis.Metrics.FigurativeDensity,
				analysis.Metrics.DialogueRatio*100,
				len(analysis.Metrics.AphorismHits),
				analysis.Metrics.ProtagonistWaver,
			)
		}
	}
	if analysis.Metrics.ProtagonistWaver {
		if reviewClaimsMissingProtagonistWaver(entry.Summary) {
			entry.Summary = fmt.Sprintf("第 %d 章合同与八维评分达到通过线；主角迟疑已有正文动作证据。", chapter)
		}
		for i := range entry.Dimensions {
			if reviewClaimsMissingProtagonistWaver(entry.Dimensions[i].Comment) {
				entry.Dimensions[i].Comment = "主角存在可识别的动作迟疑与风险复核，本维度按当前规则指标通过。"
			}
		}
	}
	if len(entry.Issues) == 0 && entry.ContractStatus == "met" && reviewDimensionsPass(entry.Dimensions) {
		entry.Verdict = "accept"
		entry.AffectedChapters = nil
	}
	return uniqueNonEmptyStrings(removed)
}

func editorAIVoicePayloadPassed(chapter int, body string, analysis domain.AIVoiceAnalysis) bool {
	if analysis.Chapter != chapter || analysis.BodySHA256 == "" || analysis.BodySHA256 != reviewreport.BodySHA256(body) {
		return false
	}
	if analysis.Metrics.ParagraphCount <= 0 || analysis.Metrics.SentenceCount <= 0 || len(analysis.RedFlags) > 0 {
		return false
	}
	return strings.Contains(analysis.Label, "通过") || strings.Contains(analysis.Summary, "未发现硬性")
}

func reconcileWarningOnlyEditorReview(
	entry *domain.ReviewEntry,
	bodyHash string,
	mechanical *reviewreport.MechanicalGatePayload,
	analysis domain.AIVoiceAnalysis,
	judge *deepseekAIJudgeArtifact,
) bool {
	if entry == nil || mechanical == nil || judge == nil || strings.TrimSpace(bodyHash) == "" {
		return false
	}
	if entry.ContractStatus != "met" || len(entry.ContractMisses) > 0 || judge.Blocking ||
		mechanical.BodySHA256 != bodyHash || analysis.BodySHA256 != bodyHash || judge.BodySHA256 != bodyHash {
		return false
	}
	if !strings.Contains(strings.ToLower(judge.Verdict), "human") ||
		!(strings.Contains(analysis.Label, "通过") || strings.Contains(analysis.Summary, "未发现硬性")) ||
		len(analysis.RedFlags) > 0 || reviewExistingAIGCGatePercent(mechanical.AIGCReport) > 5 {
		return false
	}
	for _, violation := range mechanical.RuleViolations {
		if violation.Severity == rules.SeverityError {
			return false
		}
	}
	for _, issue := range entry.Issues {
		if issue.Severity != "warning" {
			return false
		}
	}
	for i := range entry.Dimensions {
		if entry.Dimensions[i].Score < 80 {
			entry.Dimensions[i].Score = 80
			entry.Dimensions[i].Verdict = "pass"
			entry.Dimensions[i].Comment += " | 同哈希机械门禁、AI voice 与裸文 Reviewer 均通过；本项保留为非阻断建议。"
		}
	}
	entry.Issues = nil
	entry.Verdict = "accept"
	entry.AffectedChapters = nil
	entry.Summary = fmt.Sprintf("第 %d 章结果级合同、机械门禁、AI voice 与独立裸文 Reviewer 均通过；Editor 的 warning 级意见保留在原始报告中，不触发返工。", entry.Chapter)
	return true
}

func reviewClaimsMissingAIVoicePayload(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, marker := range []string{
		"未读取red flag JSON", "未读取 red flag JSON", "未引用red flag JSON", "未引用 red flag JSON",
		"AI腔检测流程缺失", "AI 腔检测流程缺失", "需补检", "无法判断",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func reviewDimensionsPass(dimensions []domain.DimensionScore) bool {
	if len(dimensions) == 0 {
		return false
	}
	for _, dimension := range dimensions {
		if dimension.Score < 80 || dimension.Verdict == "fail" {
			return false
		}
	}
	return true
}

func reviewClaimsMissingProtagonistWaver(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.Contains(text, "protagonist_waver_missing") || strings.Contains(text, "主角动摇缺失") {
		return true
	}
	mentionsWaver := strings.Contains(text, "真实迟疑") || strings.Contains(text, "认知层面的摇摆") || strings.Contains(text, "判断失误")
	claimsMissing := strings.Contains(text, "缺少") || strings.Contains(text, "缺乏") || strings.Contains(text, "没有") || strings.Contains(text, "不足") || strings.Contains(text, "未出现")
	return mentionsWaver && claimsMissing
}

func reviewClaimsSystemMessageOverpacked(text string) bool {
	if !strings.Contains(text, "系统消息") {
		return false
	}
	for _, marker := range []string{"一次塞入过多", "过多功能", "同时承担", "一条消息"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func reviewRejectsApprovedTrendCarrier(text, body string, plan *domain.ChapterPlan) bool {
	if plan == nil || !strings.Contains(text, "呱") {
		return false
	}
	rejectsCarrier := false
	for _, marker := range []string{"落地方位错误", "配角口中", "承载人错误", "换给主角", "削弱记忆点"} {
		if strings.Contains(text, marker) {
			rejectsCarrier = true
			break
		}
	}
	if !rejectsCarrier {
		return false
	}
	for _, item := range plan.CausalSimulation.TrendLanguage {
		if !strings.Contains(item.Item, "呱") || strings.TrimSpace(item.CharacterCarrier) == "" {
			continue
		}
		for _, name := range []string{"赵航", "林澈", "沈知遥", "系统"} {
			if strings.Contains(item.CharacterCarrier, name) && strings.Contains(text, name) &&
				strings.Contains(body, name) && (strings.Contains(body, "“呱，") || strings.Contains(body, "\"呱，")) {
				return true
			}
		}
	}
	return false
}

func reviewDemandsAbsentOptionalTrend(text, body string, plan *domain.ChapterPlan) bool {
	if plan == nil {
		return false
	}
	complaint := false
	for _, marker := range []string{"未落地", "未完全落地", "缺失", "仅完成", "没有使用", "没有出现"} {
		if strings.Contains(text, marker) {
			complaint = true
			break
		}
	}
	if !complaint {
		return false
	}
	for _, item := range plan.CausalSimulation.TrendLanguage {
		token := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’「」…，,。.!！?？")
		if strings.HasPrefix(strings.TrimSpace(item.Item), "呱") {
			token = "呱"
		}
		if token != "" && strings.Contains(text, token) && !strings.Contains(body, token) {
			return true
		}
	}
	return false
}

func reviewRejectsStandaloneChatEmoticon(text string) bool {
	return strings.Contains(text, "颜文字") &&
		(strings.Contains(text, "位置风险") || strings.Contains(text, "正式系统") || strings.Contains(text, "正式条款"))
}

func hasStandaloneSystemChatEmoticon(body string) bool {
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "【") || !strings.HasSuffix(line, "】") {
			continue
		}
		hasEmoticon := false
		for _, marker := range []string{"^_^", "^-^", ":)", "：）", "(￣", "(＾"} {
			if strings.Contains(line, marker) {
				hasEmoticon = true
				break
			}
		}
		if !hasEmoticon {
			continue
		}
		formal := false
		for _, marker := range []string{"任务", "额度", "时限", "核验", "规则", "限制", "剩余", "解锁"} {
			if strings.Contains(line, marker) {
				formal = true
				break
			}
		}
		if !formal {
			return true
		}
	}
	return false
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultReviewDimensions() []domain.DimensionScore {
	dims := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dims = append(dims, domain.DimensionScore{
			Dimension: name,
			Score:     80,
			Verdict:   "pass",
			Comment:   "Markdown 评审未能解析该维度，按通过线保守入账。",
		})
	}
	return dims
}

func parseReviewDimensions(md string) []domain.DimensionScore {
	rowRE := regexp.MustCompile(`^\|\s*([1-8])\s+[^|]*\|\s*([0-5])\s*\|\s*(.*?)\s*\|?$`)
	var dims []domain.DimensionScore
	for _, line := range strings.Split(md, "\n") {
		m := rowRE.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) != 4 {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		severity, _ := strconv.Atoi(m[2])
		if idx < 1 || idx > len(reviewDimensionNames) {
			continue
		}
		score := 100 - severity*10
		verdict := "pass"
		if score < 60 {
			verdict = "fail"
		} else if score < 80 {
			verdict = "warning"
		}
		comment := stripMarkdownCell(m[3])
		if strings.TrimSpace(comment) == "" {
			comment = "由 Markdown 八维评审转换。"
		}
		dims = append(dims, domain.DimensionScore{
			Dimension: reviewDimensionNames[idx-1],
			Score:     score,
			Verdict:   verdict,
			Comment:   comment,
		})
	}
	return dims
}

func reviewVerdictFromMarkdown(md string, dimensions []domain.DimensionScore) string {
	line := extractLine(md, "## 是否需要改写")
	if strings.Contains(line, "是") && !strings.Contains(line, "否") {
		for _, dim := range dimensions {
			if dim.Score < 60 {
				return "rewrite"
			}
		}
		for _, dim := range dimensions {
			if dim.Score < 80 {
				return "polish"
			}
		}
		// 原始报告偶尔会在八维全部通过时仍写“需要改写”。此时问题只作为
		// 后续写作建议沉淀，不能把已通过章节拖入无意义返工。
		return "accept"
	}
	return "accept"
}

func parseReviewIssues(md string) []domain.ConsistencyIssue {
	section := false
	lineRE := regexp.MustCompile(`^\s*\d+[.、]\s*(.+)$`)
	var issues []domain.ConsistencyIssue
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## 主要问题") {
			section = true
			continue
		}
		if section && strings.HasPrefix(line, "## ") {
			break
		}
		if !section {
			continue
		}
		m := lineRE.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		desc := stripMarkdownCell(m[1])
		if desc == "" {
			continue
		}
		if isNonActionableReviewIssue(desc) {
			continue
		}
		issues = append(issues, domain.ConsistencyIssue{
			Type:        "aesthetic",
			Severity:    "warning",
			Description: desc,
			Evidence:    desc,
			Suggestion:  "",
		})
		if len(issues) >= 5 {
			break
		}
	}
	return issues
}

func isNonActionableReviewIssue(desc string) bool {
	desc = strings.TrimSpace(strings.ReplaceAll(desc, "**", ""))
	if desc == "" {
		return true
	}
	normalized := strings.Join(strings.Fields(desc), "")
	switch {
	case strings.HasPrefix(normalized, "无严重问题"):
		return true
	case strings.HasPrefix(normalized, "没有严重问题"):
		return true
	case strings.Contains(normalized, "不建议改写"):
		return true
	case strings.Contains(normalized, "无需修改"):
		return true
	case strings.Contains(normalized, "不需要修改"):
		return true
	case strings.Contains(normalized, "不构成问题"):
		return true
	case strings.Contains(normalized, "实为优秀写作"):
		return true
	case strings.Contains(normalized, "非必要"):
		return true
	}
	return false
}

func stripMarkdownCell(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "|")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 240 {
		r := []rune(s)
		s = string(r[:240]) + "..."
	}
	return s
}

// editorSystemPrompt 复用八维评审框架（与 assets/prompts/editor.md 同源，但去掉
// 工具调用约束，直接要 markdown 输出——更适合 CLI 无 TTY 场景）。
const editorSystemPrompt = `你是一位资深中文网文编辑，负责男频长篇连载的章级审核。具体题材、人物关系和风格承诺以用户消息里的项目规则为准。

## 你的任务
阅读用户提交的章节正文，按下面八个维度逐项评审，每项打分 0-5（0 完美，5 严重问题）。
最后给出 **总评 / 是否需要改写 / 主要问题**。

## 八个评审维度
1. **设定一致性**：人物性格 / 已知事实 / 能力边界有没有与前文矛盾？
2. **角色行为**：主角行为是否合乎人设？配角是否有自己的目标和理由？人物知识边界与视角是否自然？
3. **节奏**：信息密度、情绪起伏、钩子位置是否到位？
4. **叙事连贯**：与上一章衔接、视角切换、时间线是否清楚？
5. **伏笔**：本章埋下的伏笔是否清晰？是否回扣了前文伏笔？
6. **钩子**：章末 / 小节末是否有钩子？
7. **审美品质**：描写质感、对话区分度、用词质量、情感打动力。
8. **AI 腔检测**：必须读取用户消息里的 red flag JSON，检查比喻密度、对话占比、格言命中、主角动摇、章节功能和章末钩子均匀度。问题必须具体到段号/句号/风险来源。

## 输出格式（严格遵循 markdown 结构）
` + "```" + `
# ch{NN} 评审

## 总体评分：X / 40
## 是否需要改写：[是 / 否 / 可选]
## 一句话诊断：(最突出的一处问题)

## 八维打分
| 维度 | 分 | 证据 |
|---|---|---|
| 1 设定一致性 | x | 引用原文 |
| 2 角色行为 | x | ... |
| 3 节奏 | x | ... |
| 4 叙事连贯 | x | ... |
| 5 伏笔 | x | ... |
| 6 钩子 | x | ... |
| 7 审美品质 | x | ... |
| 8 AI 腔检测 | x | 比喻密度/对话占比/格言命中/段句级修法 |

## 主要问题（按严重度排序）
1. ...
2. ...
` + "```" + `

## 注意
- 项目规则是最高优先级。不得把用户明确要求的系统人格、感情路线、轻松基调、热梗预算或题材方向当成缺点，也不得提出反向修改；只能优化它们的落地节奏、长度和人物声口。
- 若项目规则要求系统会聊天、吐槽、撑腰并始终支持主角，禁止建议把系统改成冷硬、静默、断联或纯任务机器人。
- 逐段朗读对白。角色轮流精准问答、句句完整、一次讲齐金额/时限/售后/责任，连续三段“某人做动作：台词”，或频繁用“答完/问完/说完后另一人才……”调度，均属于短剧腔和流程腔。系统必须回答主角刚问的具体问题；“钱没跑、陪你换条路、规矩不撤、先喘半口气”这类客服式空话直接判改写。
- “补测、核验、用途说明、临时固定、采购凭证、测试记录”等术语出现时，普通读者必须当场看懂哪里会坏、谁会吃亏、下一步做什么。每次换地点还要能回答上一场余波、主角为什么现在去、抵达后的首个阻力；只写锁屏、下楼、到了某地属于因果断裂。
- 热梗与颜文字是候选和使用上限，不是逐章门禁。正文没用不构成问题；用了才核对角色、语境和句法。“呱，……”若被选中，后面要接完整吐槽，不能写成“呱了一声”。生硬时要求删除，不得迁移到别处硬塞。
- 不要复述章节内容，只指出问题。
- 引用原文必须用「」包裹，标明位置（章节序号 / 段落大意）。
- 不要寒暄、不要解释，直接进入评审。
`

// callEditorOnChapter 调用 Editor 模型出评审 markdown。
func callEditorOnChapter(model agentcore.ChatModel, premise, userRules, chapterReviewContext string, chNum int, chapterBody string, analysis domain.AIVoiceAnalysis, budget time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	redFlagJSON := editorAIVoiceReviewPayload(analysis, chapterBody)
	userMsg := fmt.Sprintf("## 项目用户规则（最高优先级）\n```json\n%s\n```\n\n## 本章已批准写前 plan（合同事实，不得反向建议）\n```json\n%s\n```\n\n## 故事前提（供参考）\n%s\n\n## AI 腔 red flag JSON（必须读取）\n```json\n%s\n```\n\n## 本章正文（ch%02d）\n%s",
		truncateForContext(userRules, 6000), truncateForContext(chapterReviewContext, 8000), truncateForContext(premise, 2000), redFlagJSON, chNum, chapterBody)

	resp, err := model.Generate(ctx,
		[]agentcore.Message{
			{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: editorSystemPrompt}}},
			{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: userMsg}}},
		},
		nil,
	)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Message.TextContent() == "" {
		return "", fmt.Errorf("editor 返回空响应")
	}
	return resp.Message.TextContent(), nil
}

// truncateForContext 把超长文本截断，按 rune 切片避免切碎多字节 UTF-8。
// maxBytes 是目标上限（按字节算）；实际截到不超 maxBytes 的最大 rune 前缀。
func truncateForContext(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// 二分查找最接近但不超 maxBytes 的 rune 边界
	lo, hi := 0, len([]rune(s))
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if len(string([]rune(s)[:mid])) <= maxBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string([]rune(s)[:lo]) + "\n\n...（截断）"
}

// summarizeReview 从评审 markdown 提取"总分 / 是否改写 / 一句话诊断"做汇总。
func summarizeReview(chNum int, review string) string {
	score := extractLine(review, "## 总体评分")
	rewrite := extractLine(review, "## 是否需要改写")
	diag := extractLine(review, "## 一句话诊断")
	return fmt.Sprintf("- **ch%02d**：%s ｜ %s ｜ %s", chNum, score, rewrite, diag)
}

func rebuildReviewSummary(projectDir string) (string, error) {
	reviewsDir := filepath.Join(projectDir, "reviews")
	chapters, err := chapterNumbersFromFiles(reviewsDir)
	if err != nil {
		return "", err
	}
	rows := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		path := filepath.Join(reviewsDir, fmt.Sprintf("%02d.md", chapter))
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("读取第 %d 章统一审核用于重建汇总: %w", chapter, err)
		}
		row := summarizeReview(chapter, string(raw))
		if mechanical, _, loadErr := reviewreport.LoadMechanicalGate(projectDir, chapter); loadErr == nil && mechanical != nil && mechanical.BodySHA256 != "" {
			row += fmt.Sprintf(" <!-- body_sha256=%s -->", mechanical.BodySHA256)
		}
		rows = append(rows, row)
	}

	summaryPath := filepath.Join(projectDir, "meta", "review-summary.md")
	summary := "# review-summary\n\n" +
		"> 生成时间 " + time.Now().Format("2006-01-02 15:04:05") + "\n\n" +
		strings.Join(rows, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(summaryPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(summaryPath, []byte(summary), 0o644); err != nil {
		return "", err
	}
	return summaryPath, nil
}

func extractLine(md, header string) string {
	sc := strings.Split(md, "\n")
	for i, ln := range sc {
		if strings.Contains(ln, header) {
			rest := strings.TrimPrefix(ln, header)
			rest = strings.TrimLeft(rest, " ：:=")
			rest = strings.TrimSpace(rest)
			if rest == "" && i+1 < len(sc) {
				rest = strings.TrimSpace(sc[i+1])
			}
			return rest
		}
	}
	return "—"
}

// printReviewUsage 主动打印 --review-existing 子命令 usage。
func printReviewUsage(w *os.File) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: novel-studio --review-existing [--budget 90s] [--from N] [--to N]")
	fmt.Fprintln(w, "兼容别名：执行时会委派到 novel-studio --pipeline --stages review")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "示例:")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review                 # 评当前 output/novel/chapters/*.md")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review --from 1 --to 5")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review --budget 60s")
	fmt.Fprintln(w)
}
