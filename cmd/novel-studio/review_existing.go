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
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	editorReviewProtocolVersion      = "review-existing/editor/v6-style-contract"
	editorReviewUserPayloadKind      = "chapter_context_with_canonical_heading_and_style_contract/v3"
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
	StyleContractSHA256        string `json:"style_contract_sha256,omitempty"`
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
	Review         string
	CacheArtifact  *editorReviewCacheArtifact
	CacheHit       bool
	CachePersisted bool
	CacheLoadErr   error
	Err            error
	Elapsed        time.Duration
	ModelCalls     int
}

type deepseekAIJudgeBranchResult struct {
	Artifact       *deepseekAIJudgeArtifact
	CacheHit       bool
	CachePersisted bool
	CacheLoadErr   error
	Err            error
	Elapsed        time.Duration
	ModelCalls     int
}

func parseReviewFlags(argv []string) (reviewFlags, []string, error) {
	fs := flag.NewFlagSet("review-existing", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --review-existing [--budget 180s] [--from N] [--to N]\n\n")
		fmt.Fprintf(os.Stderr, "兼容别名：执行时会委派到 novel-studio --pipeline --stages review。\n")
		fmt.Fprintf(os.Stderr, "逐章调用 Editor LLM 出评审报告（不改原文）。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f reviewFlags
	f.Budget = 180 * time.Second
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
	return reviewExistingPipelineAtOutput(opts, args, "", false)
}

// reviewExistingPipelineAtOutput evaluates an exact isolated output tree.
// Sealed render candidates disable live RAG here: all inference inputs were
// frozen before rendering, and a rejected candidate must not write vectors or
// retrieval state into the canonical project's external index.
func reviewExistingPipelineAtOutput(
	opts cliOptions,
	args []string,
	exactOutputDir string,
	disableLiveRAG bool,
) error {
	return reviewExistingPipelineAtOutputWithPolicy(opts, args, exactOutputDir, disableLiveRAG, reviewExistingRunPolicy{})
}

type reviewExistingRunPolicy struct {
	// RequireCurrentDeepSeekCache is used by sealed post-commit recovery.  That
	// path already has an approved exact-body provider gate, so formal
	// revalidation may refresh Editor but must never pay for (or silently accept)
	// a second DeepSeek decision.
	RequireCurrentDeepSeekCache bool
}

func reviewExistingPipelineAtOutputWithPolicy(
	opts cliOptions,
	args []string,
	exactOutputDir string,
	disableLiveRAG bool,
	policy reviewExistingRunPolicy,
) error {
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
	if strings.TrimSpace(exactOutputDir) != "" {
		cfg.OutputDir = filepath.Clean(exactOutputDir)
		projDir = cfg.OutputDir
	} else {
		if err := normalizeOutputDirForInvocation(&cfg, projDir); err != nil {
			return err
		}
	}
	cfg.DisableLiveRAG = disableLiveRAG
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
	var ragVectorWriter rag.VectorWriter
	if !cfg.DisableLiveRAG {
		if embedder, enabled, err := bootstrap.NewRAGEmbedder(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] RAG embedding 初始化失败，仅写关键词索引：%v\n", err)
		} else if enabled {
			ragEmbedder = embedder
		}
		if writer, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] Qdrant 初始化失败，仅写本地 RAG：%v\n", err)
		} else if enabled {
			ragVectorWriter = writer
		}
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
	resolvedStyle := bundle.ResolveStyle(cfg.Style)

	successCount, failureCount := 0, 0
	for _, chNum := range selectedChapters {
		if err := st.ArcCycle().RequireChapterReviewArtifactsMutable(chNum); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] 跳过 ch%02d：%v\n", chNum, err)
			failureCount++
			continue
		}
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
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 机械门禁写入失败，已终止本章审核以避免沿用旧门禁：%v\n", chNum, err)
			failureCount++
			continue
		}
		chapterReviewContext, contextErr := buildEditorChapterReviewContextWithStyle(
			st,
			chNum,
			bundle.References,
			resolvedStyle.ID,
			resolvedStyle.Body,
		)
		if contextErr != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 审核合同构建失败，已在模型调用前终止本章：%v\n", chNum, contextErr)
			failureCount++
			continue
		}
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor(%s/%s) + DeepSeek(%s/%s) 并行评审中（预算 %s）...\n",
			chNum, editorProvider, editorName, reviewerProvider, reviewerName, flags.Budget)
		reviewBranchesStarted := time.Now()
		editorBranch, deepseekBranch := runReviewExistingBranchesConcurrently(
			func() editorReviewBranchResult {
				return loadOrGenerateEditorReview(
					eng.Dir(), model, editorProvider, editorName,
					premise, userRulesContext, chapterReviewContext,
					chNum, frozenBody, analysis, flags.Budget,
				)
			},
			func() deepseekAIJudgeBranchResult {
				if policy.RequireCurrentDeepSeekCache {
					started := time.Now()
					expected := newDeepSeekAIJudgeCachePolicy(reviewerSelection, chNum, frozenBody)
					cached, cacheErr := loadDeepSeekAIJudgeCache(eng.Dir(), expected)
					if cacheErr != nil {
						return deepseekAIJudgeBranchResult{Err: cacheErr, CacheLoadErr: cacheErr, Elapsed: time.Since(started)}
					}
					if cached == nil {
						cacheErr = fmt.Errorf("sealed formal revalidation requires current exact-body DeepSeek cache")
						return deepseekAIJudgeBranchResult{Err: cacheErr, Elapsed: time.Since(started)}
					}
					cached.ReviewerExplicit = reviewerSelection.Explicit
					cached.ModelSelection = reviewerSelection
					return deepseekAIJudgeBranchResult{Artifact: cached, CacheHit: true, Elapsed: time.Since(started)}
				}
				return loadOrGenerateDeepSeekAIJudge(
					eng.Dir(), reviewerModel, reviewerSelection,
					chNum, frozenBody, flags.Budget,
				)
			},
		)
		fmt.Fprintf(os.Stderr,
			"[review-existing:timing] ch%02d parallel_total=%s editor=%s(cache=%t calls=%d) deepseek=%s(cache=%t calls=%d)\n",
			chNum,
			time.Since(reviewBranchesStarted).Round(time.Millisecond),
			editorBranch.Elapsed.Round(time.Millisecond), editorBranch.CacheHit, editorBranch.ModelCalls,
			deepseekBranch.Elapsed.Round(time.Millisecond), deepseekBranch.CacheHit, deepseekBranch.ModelCalls,
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
		if editorBranch.Err == nil && !editorBranch.CacheHit && !editorBranch.CachePersisted {
			if err := saveEditorReviewCache(eng.Dir(), editorBranch.CacheArtifact); err != nil {
				cacheSaveErrors = append(cacheSaveErrors, "Editor: "+err.Error())
			}
		}
		if deepseekBranch.Err == nil && !deepseekBranch.CacheHit && !deepseekBranch.CachePersisted {
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
		entry, parseErr := structuredReviewFromMarkdown(chNum, review)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 输出合同校验失败，已在 SaveReview 前失败闭锁：%v\n", chNum, parseErr)
			failureCount++
			continue
		}
		entry.BodySHA256 = bodyHash
		if removed := sanitizeEditorReviewForProject(st, chNum, frozenBody, analysis, &entry); len(removed) > 0 {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 已移除 %d 条与正文机械事实或批准 plan 冲突的 Editor 建议：%s\n", chNum, len(removed), strings.Join(removed, "；"))
		}
		deepseekJudge := deepseekBranch.Artifact
		sanitizeDeepSeekAIJudgeForProject(st, deepseekJudge)
		if !deepseekJudge.AdviceComplete {
			// The model-facing request identity is unchanged. Calling the provider
			// again for the same body/cache key is both redundant and unbounded: the
			// same project sanitizer can reject the second response for the same
			// reason. Persist the exact result for audit and fail closed; a protocol
			// or prompt revision will naturally produce a new cache identity.
			if saveErr := saveDeepSeekAIJudge(eng.Dir(), deepseekJudge); saveErr != nil {
				fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek 净化后失败闭锁结果写入失败：%v\n", chNum, saveErr)
			}
			fmt.Fprintf(os.Stderr,
				"[review-existing] ch%02d DeepSeek 建议经项目门禁净化后不完整；同一 body/cache key 禁止重复回源，已保留精确结果并失败闭锁：%s\n",
				chNum, deepseekJudge.AdviceWarning,
			)
			failureCount++
			continue
		}
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
		externalJudge := deepSeekExternalAIJudge(deepseekJudge)
		plainExternalCalibrated := false
		if reviewreport.ApplyExternalCorroboration(mechanical, externalJudge) {
			if err := writeMechanicalGateForExistingChapter(st, mechanical); err != nil {
				fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 同哈希外判校准写入失败：%v\n", chNum, err)
				failureCount++
				continue
			}
			plainExternalCalibrated = true
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 本地概率代理已由同哈希 DeepSeek <4%% 校准为诊断项\n", chNum)
		}
		reconciledWarnings := false
		if reviewHasStructuralProseFailure(&entry, mechanical) {
			entry.Verdict = "rewrite"
			entry.AffectedChapters = []int{chNum}
		} else if deepseekJudge.Blocking {
			entry.Verdict = "rewrite"
			entry.AffectedChapters = []int{chNum}
		} else if reconcileWarningOnlyEditorReview(&entry, review, bodyHash, mechanical, analysis, deepseekJudge) {
			reconciledWarnings = true
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 警告已由同哈希机械门禁、AI voice 与裸文 Reviewer 三方校准为非阻断建议\n", chNum)
		}
		calibratedWithEditor := false
		if reconciledWarnings && !plainExternalCalibrated && mechanicalHasStructuralProseWarning(mechanical) &&
			reviewreport.ApplyExternalCorroborationWithEditor(mechanical, externalJudge, &entry) {
			if err := writeMechanicalGateForExistingChapter(st, mechanical); err != nil {
				fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 明确解除结构 warning 后的同哈希外判校准写入失败：%v\n", chNum, err)
				failureCount++
				continue
			}
			calibratedWithEditor = true
		}
		if calibratedWithEditor {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d Editor 已明确核验结构 warning；同哈希外判校准已写入，warning 保留为观察项\n", chNum)
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
			ExternalAIJudge: externalJudge,
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
		// The provenance sidecar is the commit marker for one complete review
		// set. Write it last: a crash after either model cache is saved, or while
		// any deterministic/final report is being replaced, must leave no fresh
		// provenance capable of blessing a mixed old/new artifact set.
		if err := persistReviewModelProvenance(
			st,
			chNum,
			bodyHash,
			editorBranch.CacheArtifact,
			&entry,
			deepseekBranch.Artifact,
		); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 模型/风格审核来源封存失败：%v\n", chNum, err)
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

func persistReviewModelProvenance(
	st *store.Store,
	chapter int,
	bodySHA256 string,
	editor *editorReviewCacheArtifact,
	finalEditor *domain.ReviewEntry,
	deepseek *deepseekAIJudgeArtifact,
) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(bodySHA256) == "" ||
		editor == nil || finalEditor == nil || deepseek == nil {
		return fmt.Errorf("review model provenance requires exact body, both cache artifacts and final reviews")
	}
	if editor.Chapter != chapter || deepseek.Chapter != chapter ||
		editor.CachePolicy.Chapter != chapter || deepseek.CachePolicy.Chapter != chapter ||
		editor.CachePolicy.BodySHA256 != bodySHA256 || deepseek.CachePolicy.BodySHA256 != bodySHA256 ||
		editor.CacheKey != reviewExistingCacheKey(editor.CachePolicy) ||
		deepseek.CacheKey != reviewExistingCacheKey(deepseek.CachePolicy) {
		return fmt.Errorf("review model cache identity does not match exact chapter body")
	}
	if err := validateReviewModelCachePolicies(
		chapter,
		bodySHA256,
		editor.CacheKey,
		editor.CachePolicy,
		deepseek.CacheKey,
		deepseek.CachePolicy,
		true,
	); err != nil {
		return fmt.Errorf("validate review model provenance policies: %w", err)
	}
	var persistedEditor domain.ReviewEntry
	persistedEditorRaw, err := os.ReadFile(filepath.Join(st.Dir(), "reviews", fmt.Sprintf("%02d.json", chapter)))
	if err != nil || json.Unmarshal(persistedEditorRaw, &persistedEditor) != nil || !reflect.DeepEqual(persistedEditor, *finalEditor) {
		return fmt.Errorf("persisted Editor final artifact does not match the completed review")
	}
	var persistedDeepSeek deepseekAIJudgeArtifact
	persistedDeepSeekRaw, err := os.ReadFile(filepath.Join(st.Dir(), "reviews", fmt.Sprintf("%02d_deepseek_ai_judge.json", chapter)))
	if err != nil || json.Unmarshal(persistedDeepSeekRaw, &persistedDeepSeek) != nil || !reflect.DeepEqual(persistedDeepSeek, *deepseek) {
		return fmt.Errorf("persisted DeepSeek final artifact does not match the completed review")
	}
	provenance := reviewModelProvenance{
		Version:             reviewModelProvenanceVersion,
		Chapter:             chapter,
		BodySHA256:          bodySHA256,
		EditorCacheKey:      editor.CacheKey,
		EditorCachePolicy:   editor.CachePolicy,
		DeepSeekCacheKey:    deepseek.CacheKey,
		DeepSeekCachePolicy: deepseek.CachePolicy,
	}
	requirement, err := loadReviewEffectiveStyleRequirement(st, chapter)
	if err != nil {
		return fmt.Errorf("validate effective style requirement for review provenance: %w", err)
	}
	if requirement.Required {
		_, receipt, archivePath, err := toolspkg.LoadBoundArchivedEffectiveRenderStyleContract(
			st, chapter, requirement.PlanDigest,
		)
		if err != nil {
			return fmt.Errorf("validate required effective style receipt for review provenance: %w", err)
		}
		if receipt == nil || receipt.ReceiptDigest != requirement.ReceiptDigest {
			return fmt.Errorf("required effective style receipt does not match candidate manifest")
		}
		if editor.CachePolicy.StyleContractSHA256 != receipt.StyleContractSHA256 {
			return fmt.Errorf("Editor request context did not consume the required effective style contract")
		}
		if receipt.PlanDigest != requirement.PlanDigest {
			return fmt.Errorf("immutable effective style receipt archive differs from active review identity")
		}
		artifactSHA, err := pipelineRequiredFileSHA(st.Dir(), archivePath)
		if err != nil {
			return fmt.Errorf("hash immutable effective style receipt archive: %w", err)
		}
		provenance.EffectiveStyleReceiptPath = archivePath
		provenance.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		provenance.EffectiveStyleArtifactSHA256 = artifactSHA
		provenance.StyleContractSHA256 = receipt.StyleContractSHA256
	}
	provenance.Artifacts = expectedReviewModelArtifactBindings(chapter, editor.CacheKey, deepseek.CacheKey)
	for i := range provenance.Artifacts {
		digest, err := reviewModelArtifactSHA256(st.Dir(), provenance.Artifacts[i].Path)
		if err != nil {
			return fmt.Errorf("bind final review artifact %s: %w", provenance.Artifacts[i].Path, err)
		}
		provenance.Artifacts[i].SHA256 = digest
	}
	if issues := validateReviewModelArtifactBindings(st.Dir(), provenance, &persistedDeepSeek); len(issues) > 0 {
		return fmt.Errorf("validate bound review artifact set: %s", strings.Join(issues, "; "))
	}
	raw, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(st.Dir(), "reviews", fmt.Sprintf("%02d_model_provenance.json", chapter))
	return atomicWriteRewriteFile(path, append(raw, '\n'), 0o644)
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
		ChapterReviewContextSHA256: reviewExistingSHA256(editorReviewChapterContextPayload(chapterReviewContext)),
		StyleContractSHA256:        editorReviewStyleContractSHA256(chapterReviewContext),
		AIVoiceContextSHA256:       reviewExistingSHA256(aiVoiceContext),
		UserPayloadKind:            editorReviewUserPayloadKind,
	}
}

func editorReviewStyleContractSHA256(chapterReviewContext string) string {
	var payload struct {
		StyleContract json.RawMessage `json:"style_contract"`
	}
	if json.Unmarshal([]byte(chapterReviewContext), &payload) != nil ||
		len(payload.StyleContract) == 0 || string(payload.StyleContract) == "null" {
		return ""
	}
	var contract any
	if json.Unmarshal(payload.StyleContract, &contract) != nil || contract == nil {
		return ""
	}
	canonical, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	return pipelineBytesSHA(canonical)
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
	started := time.Now()
	finish := func(result editorReviewBranchResult) editorReviewBranchResult {
		result.Elapsed = time.Since(started)
		return result
	}
	aiVoiceContext := editorAIVoiceReviewPayload(analysis, chapterBody)
	policy := newEditorReviewCachePolicy(
		provider, modelName, premise, userRules, chapterReviewContext,
		chapter, chapterBody, aiVoiceContext,
	)
	cached, loadErr := loadEditorReviewCache(projectDir, policy)
	if loadErr == nil && cached != nil {
		return finish(editorReviewBranchResult{
			Review:        cached.Markdown,
			CacheArtifact: cached,
			CacheHit:      true,
		})
	}
	key := reviewExistingCacheKey(policy)
	release, lockErr := acquireReviewCacheKeyLock(projectDir, editorReviewCacheBranch, key, budget)
	if lockErr != nil {
		return finish(editorReviewBranchResult{CacheLoadErr: loadErr, Err: lockErr})
	}
	defer func() { _ = release() }()

	// The winner may have populated the cache while this invocation waited.
	// Always re-read under the key lock before spending another model call.
	lockedCached, lockedLoadErr := loadEditorReviewCache(projectDir, policy)
	if lockedLoadErr == nil && lockedCached != nil {
		return finish(editorReviewBranchResult{
			Review:        lockedCached.Markdown,
			CacheArtifact: lockedCached,
			CacheHit:      true,
			CacheLoadErr:  loadErr,
		})
	}
	if lockedLoadErr != nil {
		loadErr = lockedLoadErr
	}
	remainingBudget, budgetErr := remainingReviewCacheBudget(started, budget)
	if budgetErr != nil {
		return finish(editorReviewBranchResult{CacheLoadErr: loadErr, Err: budgetErr})
	}

	review, err := callEditorOnChapter(
		model, premise, userRules, chapterReviewContext,
		chapter, chapterBody, analysis, remainingBudget,
	)
	if err != nil {
		return finish(editorReviewBranchResult{CacheLoadErr: loadErr, Err: err, ModelCalls: 1})
	}
	if _, err := structuredReviewFromMarkdown(chapter, review); err != nil {
		return finish(editorReviewBranchResult{
			Review:       review,
			CacheLoadErr: loadErr,
			Err:          fmt.Errorf("Editor 输出合同无效: %w", err),
			ModelCalls:   1,
		})
	}
	artifact := &editorReviewCacheArtifact{
		Chapter:     chapter,
		GeneratedAt: time.Now().Format(time.RFC3339),
		CacheKey:    reviewExistingCacheKey(policy),
		CachePolicy: policy,
		Markdown:    review,
	}
	if err := saveEditorReviewCache(projectDir, artifact); err != nil {
		return finish(editorReviewBranchResult{
			Review:        review,
			CacheArtifact: artifact,
			CacheLoadErr:  loadErr,
			Err:           fmt.Errorf("持久化 Editor 精确正文缓存: %w", err),
			ModelCalls:    1,
		})
	}
	return finish(editorReviewBranchResult{
		Review:         review,
		CacheArtifact:  artifact,
		CachePersisted: true,
		CacheLoadErr:   loadErr,
		ModelCalls:     1,
	})
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
	if _, err := structuredReviewFromMarkdown(expected.Chapter, artifact.Markdown); err != nil {
		return fmt.Errorf("markdown 输出合同无效: %w", err)
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
	violations = append(violations, toolspkg.ProjectContaminationViolations(st, body)...)
	if gatePercent := reviewExistingAIGCGatePercent(report); gatePercent >= deepseekAIJudgePassExclusive {
		severity := rules.SeverityWarning
		if gatePercent >= 35 {
			severity = rules.SeverityError
		}
		violations = append(violations, rules.Violation{
			Rule:      "aigc_ratio",
			Target:    report.Engine,
			Limit:     "<4%",
			Actual:    gatePercent,
			Deviation: gatePercent / 100,
			Severity:  severity,
		})
	}
	registeredRows, registeredErr := reviewreport.LatestRegisteredExternalDetections(
		st.Dir(), chapter, reviewreport.BodySHA256(body),
	)
	if registeredErr != nil {
		// User-platform sampling is optional production evidence. A damaged
		// sampling journal must still reject future registrations, but it cannot
		// prevent local/DeepSeek/Editor review of the chapter already on disk.
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 用户抽查日志不可读，已忽略该可选证据：%v\n", chapter, registeredErr)
		registeredRows = nil
	}
	blockingRegistered := make([]reviewreport.RegisteredExternalDetection, 0, len(registeredRows))
	for _, registeredExternal := range registeredRows {
		if registeredExternal.NormalizedScorePercent < deepseekAIJudgePassExclusive {
			continue
		}
		blockingRegistered = append(blockingRegistered, registeredExternal)
		severity := rules.SeverityWarning
		if registeredExternal.NormalizedScorePercent >= 35 {
			severity = rules.SeverityError
		}
		target := strings.TrimSpace(registeredExternal.Detector)
		if registeredExternal.Mode != "" {
			target += "/" + strings.TrimSpace(registeredExternal.Mode)
		}
		violations = append(violations, rules.Violation{
			Rule:      "external_aigc_ratio",
			Target:    target,
			Limit:     "<4%",
			Actual:    registeredExternal.NormalizedScorePercent,
			Deviation: registeredExternal.NormalizedScorePercent / 100,
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
	if err := writeMechanicalGateForExistingChapter(st, &payload); err != nil {
		return nil, err
	}
	for _, registeredExternal := range blockingRegistered {
		if err := toolspkg.SetRegisteredExternalRerenderRequirement(st.Dir(), registeredExternal); err != nil {
			return nil, fmt.Errorf("persist registered external rerender requirement: %w", err)
		}
		// Registered detector rows are immutable evidence, not causal plan/body
		// epochs. More than one detector can block the same body, so latest-only
		// idempotence would turn an unchanged A, B review into A, B, A, B on every
		// run. Historical digest idempotence preserves one checkpoint per exact
		// detector result; a genuinely new row still has a new semantic digest.
		if _, err := st.Checkpoints.Append(
			domain.ChapterScope(chapter), "registered-external-detection",
			"meta/external_detection_log.jsonl", reviewreport.RegisteredExternalDetectionDigest(registeredExternal),
		); err != nil {
			return nil, fmt.Errorf("checkpoint registered external detection: %w", err)
		}
	}
	return &payload, nil
}

func writeMechanicalGateForExistingChapter(st *store.Store, payload *reviewreport.MechanicalGatePayload) error {
	if st == nil || payload == nil || payload.Chapter <= 0 {
		return fmt.Errorf("mechanical gate payload is incomplete")
	}
	dir := filepath.Join(st.Dir(), "reviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d_ai_gate.json", payload.Chapter)), raw, 0o644)
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
	row, err := reviewreport.LatestRegisteredExternalDetection(
		root, chapter, reviewreport.BodySHA256(body), "", "",
	)
	if err != nil || row == nil {
		return externalAIGCDetection{}, false
	}
	return externalAIGCDetection{
		Chapter: row.Chapter, Detector: row.Detector, Mode: row.Mode,
		Score: row.Score, Verdict: row.Verdict, Note: row.Note,
		BodySHA256: row.BodySHA256, CheckedAt: row.CheckedAt,
		ScorePercent: row.NormalizedScorePercent,
	}, true
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

var reviewDimensionLabels = []string{
	"设定一致性",
	"角色行为",
	"节奏",
	"叙事连贯",
	"伏笔",
	"钩子",
	"审美品质",
	"AI 腔检测",
}

type formalEditorMarkdownContract struct {
	Rewrite    string
	Diagnosis  string
	Dimensions []domain.DimensionScore
}

func structuredReviewFromMarkdown(chapter int, md string) (domain.ReviewEntry, error) {
	contract, err := parseFormalEditorMarkdown(chapter, md)
	if err != nil {
		return domain.ReviewEntry{}, err
	}
	verdict := "accept"
	if contract.Rewrite == "是" {
		verdict = "rewrite"
	}
	entry := domain.ReviewEntry{
		Chapter:        chapter,
		Scope:          "chapter",
		Dimensions:     contract.Dimensions,
		Issues:         parseReviewIssues(md),
		ContractStatus: "met",
		Verdict:        verdict,
		Summary:        contract.Diagnosis,
	}
	if verdict == "polish" || verdict == "rewrite" {
		entry.AffectedChapters = []int{chapter}
	}
	return entry, nil
}

// validateFormalEditorMarkdown enforces the model-facing Markdown contract
// before an Editor response may be cached or converted into a ReviewEntry.
// This parser is deliberately fail-closed: an incomplete table must never be
// replaced with synthetic passing dimensions, and an explicit rewrite request
// must never be softened into an acceptance by downstream reconciliation.
func validateFormalEditorMarkdown(chapter int, md string) error {
	_, err := parseFormalEditorMarkdown(chapter, md)
	return err
}

func parseFormalEditorMarkdown(chapter int, md string) (formalEditorMarkdownContract, error) {
	var parsed formalEditorMarkdownContract
	if chapter <= 0 {
		return parsed, fmt.Errorf("chapter=%d 无效", chapter)
	}
	if strings.TrimSpace(md) == "" {
		return parsed, fmt.Errorf("Editor Markdown 为空")
	}

	titleRE := regexp.MustCompile(`^#\s+ch(\d+)\s+评审\s*$`)
	totalRE := regexp.MustCompile(`^##\s*总体评分\s*[：:]\s*(.*?)\s*$`)
	rewriteRE := regexp.MustCompile(`^##\s*是否需要改写\s*[：:]\s*(.*?)\s*$`)
	diagnosisRE := regexp.MustCompile(`^##\s*一句话诊断\s*[：:]\s*(.*?)\s*$`)
	dimensionSectionRE := regexp.MustCompile(`^##\s*八维打分\s*$`)
	issuesSectionRE := regexp.MustCompile(`^##\s*主要问题(?:\s*[（(].*[）)])?\s*$`)
	dimensionRowRE := regexp.MustCompile(`^\|\s*(\d+)\s+([^|]+)\|\s*([^|]*)\|\s*([^|]*)\|?\s*$`)
	overallScoreRE := regexp.MustCompile(`^(\d+)\s*/\s*40$`)

	var (
		titles, totals, rewrites, diagnoses int
		dimensionSections, issueSections    int
		titleChapter                        int
		totalValue, rewriteValue            string
		seenDimensions                      = make(map[int]struct{}, len(reviewDimensionNames))
		issueSectionHasBody                 bool
		insideIssues, insideDimensions      bool
		dimensions                          = make([]domain.DimensionScore, len(reviewDimensionNames))
	)
	for _, rawLine := range strings.Split(md, "\n") {
		line := strings.TrimSpace(rawLine)
		if m := titleRE.FindStringSubmatch(line); len(m) == 2 {
			titles++
			titleChapter, _ = strconv.Atoi(m[1])
		}
		if strings.HasPrefix(line, "##") && strings.Contains(line, "总体评分") {
			m := totalRE.FindStringSubmatch(line)
			if len(m) != 2 {
				return parsed, fmt.Errorf("总体评分标题格式无效")
			}
			totals++
			totalValue = normalizeEditorReviewScalar(m[1])
		}
		if strings.HasPrefix(line, "##") && strings.Contains(line, "是否需要改写") {
			m := rewriteRE.FindStringSubmatch(line)
			if len(m) != 2 {
				return parsed, fmt.Errorf("是否需要改写标题格式无效")
			}
			rewrites++
			rewriteValue = normalizeEditorReviewScalar(m[1])
		}
		if strings.HasPrefix(line, "##") && strings.Contains(line, "一句话诊断") {
			m := diagnosisRE.FindStringSubmatch(line)
			if len(m) != 2 || strings.TrimSpace(normalizeEditorReviewScalar(m[1])) == "" {
				return parsed, fmt.Errorf("一句话诊断缺失有效内容")
			}
			diagnoses++
			parsed.Diagnosis = normalizeEditorReviewScalar(m[1])
		}
		if dimensionSectionRE.MatchString(line) {
			dimensionSections++
			insideDimensions = true
			insideIssues = false
			continue
		}
		if issuesSectionRE.MatchString(line) {
			issueSections++
			insideDimensions = false
			insideIssues = true
			continue
		}
		if strings.HasPrefix(line, "## ") {
			insideDimensions = false
		}
		if insideIssues && strings.HasPrefix(line, "## ") {
			insideIssues = false
		}
		if insideIssues && line != "" && line != "```" && !strings.HasPrefix(line, "<!--") {
			issueSectionHasBody = true
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		m := dimensionRowRE.FindStringSubmatch(line)
		if !insideDimensions {
			if len(m) != 0 {
				return parsed, fmt.Errorf("八维打分行出现在八维打分章节之外: %q", line)
			}
			continue
		}
		if len(m) == 0 {
			// Header and separator rows are allowed. A row beginning with a
			// numeric cell, however, is a malformed dimension and must fail.
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "|"))
			if trimmed != "" && trimmed[0] >= '0' && trimmed[0] <= '9' {
				return parsed, fmt.Errorf("八维打分行格式无效: %q", line)
			}
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil || idx < 1 || idx > len(reviewDimensionNames) {
			return parsed, fmt.Errorf("八维序号 %q 无效", m[1])
		}
		if _, duplicate := seenDimensions[idx]; duplicate {
			return parsed, fmt.Errorf("八维序号 %d 重复", idx)
		}
		label := strings.Join(strings.Fields(stripMarkdownCellLimit(m[2], 0)), " ")
		if label != reviewDimensionLabels[idx-1] {
			return parsed, fmt.Errorf("第 %d 维标签=%q，必须是 %q", idx, label, reviewDimensionLabels[idx-1])
		}
		severity, err := strconv.Atoi(strings.TrimSpace(m[3]))
		if err != nil || severity < 0 || severity > 5 {
			return parsed, fmt.Errorf("第 %d 维分数 %q 无效，必须是 0-5 的整数", idx, strings.TrimSpace(m[3]))
		}
		comment := stripMarkdownCellLimit(m[4], 1600)
		if strings.TrimSpace(comment) == "" {
			return parsed, fmt.Errorf("第 %d 维缺少正文证据", idx)
		}
		score := 100 - severity*10
		verdict := "pass"
		if score < 60 {
			verdict = "fail"
		} else if score < 80 {
			verdict = "warning"
		}
		dimensions[idx-1] = domain.DimensionScore{
			Dimension: reviewDimensionNames[idx-1],
			Score:     score,
			Verdict:   verdict,
			Comment:   comment,
		}
		seenDimensions[idx] = struct{}{}
	}

	if titles != 1 {
		return parsed, fmt.Errorf("评审标题数量=%d，必须恰好为 1", titles)
	}
	if titleChapter != chapter {
		return parsed, fmt.Errorf("评审标题章号=%d，当前章号=%d", titleChapter, chapter)
	}
	if totals != 1 {
		return parsed, fmt.Errorf("总体评分字段数量=%d，必须恰好为 1", totals)
	}
	scoreMatch := overallScoreRE.FindStringSubmatch(totalValue)
	if len(scoreMatch) != 2 {
		return parsed, fmt.Errorf("总体评分 %q 无效，必须是 X / 40", totalValue)
	}
	overall, err := strconv.Atoi(scoreMatch[1])
	if err != nil || overall < 0 || overall > 40 {
		return parsed, fmt.Errorf("总体评分 %q 超出 0-40", totalValue)
	}
	if rewrites != 1 {
		return parsed, fmt.Errorf("是否需要改写字段数量=%d，必须恰好为 1", rewrites)
	}
	switch rewriteValue {
	case "是", "否", "可选":
		// All three are valid protocol values. Existing downstream calibration
		// continues to decide whether optional advice is actionable. An explicit
		// "是" remains authoritative and is converted to verdict=rewrite below;
		// it is never softened by otherwise high dimension scores.
	default:
		return parsed, fmt.Errorf("是否需要改写=%q 无效，只允许是/否/可选", rewriteValue)
	}
	if diagnoses != 1 {
		return parsed, fmt.Errorf("一句话诊断字段数量=%d，必须恰好为 1", diagnoses)
	}
	if dimensionSections != 1 {
		return parsed, fmt.Errorf("八维打分章节数量=%d，必须恰好为 1", dimensionSections)
	}
	if len(seenDimensions) != len(reviewDimensionNames) {
		return parsed, fmt.Errorf("八维打分不完整：解析到 %d/%d 维", len(seenDimensions), len(reviewDimensionNames))
	}
	if issueSections != 1 {
		return parsed, fmt.Errorf("主要问题章节数量=%d，必须恰好为 1", issueSections)
	}
	if !issueSectionHasBody {
		return parsed, fmt.Errorf("主要问题章节为空")
	}
	parsed.Rewrite = rewriteValue
	parsed.Dimensions = dimensions
	return parsed, nil
}

func normalizeEditorReviewScalar(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "**", ""))
	value = strings.Trim(value, "[]【】")
	return strings.TrimSpace(value)
}

func buildEditorChapterReviewContext(st *store.Store, chapter int) string {
	context, _ := buildEditorChapterReviewContextWithStyle(st, chapter, toolspkg.References{}, "", "")
	return context
}

func buildEditorChapterReviewContextWithStyle(
	st *store.Store,
	chapter int,
	refs toolspkg.References,
	style string,
	styleBody string,
) (string, error) {
	if st == nil || chapter <= 0 {
		return "", nil
	}
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return "", nil
	}
	renderContract := plan.Contract
	// The Drafter receives every hard outcome in full. The Editor has a
	// different job: verify visible results without turning an upstream action
	// recipe into a new prose obligation. Keep this projection local to review
	// context so it cannot weaken the prose-facing render packet.
	renderContract.RequiredBeats = editorReviewRequiredOutcomes(*plan)
	renderContract.ContinuityChecks = toolspkg.RenderContinuityChecks(*plan)
	styleContract, err := buildEditorReviewStyleContract(st, chapter, refs, style, styleBody)
	if err != nil {
		return "", err
	}
	payload := struct {
		StyleContract       json.RawMessage                  `json:"style_contract,omitempty"`
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
		TemporalPolicy      string                           `json:"temporal_policy"`
	}{
		StyleContract:       styleContract,
		Chapter:             plan.Chapter,
		Title:               plan.Title,
		Contract:            renderContract,
		WorldSimulationID:   plan.CausalSimulation.WorldSimulationID,
		ProtagonistDecision: plan.CausalSimulation.ProtagonistDecision,
		ReviewChecks:        plan.CausalSimulation.AntiAIPlan.ReviewChecks,
		TrendLanguage:       plan.CausalSimulation.TrendLanguage,
		Entertainment:       plan.CausalSimulation.EntertainmentPlan,
		EndingContract:      toolspkg.RenderEndingContract(*plan),
		RenderPolicy:        "contract.required_beats 已投影为结果级要求，continuity_checks 只保留事实连续性；审核结果是否成立，不要求正文复现上游执行配方、动作节拍、台词原句或指定末段物件。ending_consequence_contract 只核 consequence 与 next_chapter_pull，允许用更有吸引力的现场人物、动作或结果替换原计划镜头。style_contract 是 Drafter 同源的只读表达合同，只能审核叙述声音、距离、句法、节奏、意象、对白和跨章表层复读，绝不能反向改写冻结事件、人物决定、事实顺序或知识边界。逐项照抄 plan 本身属于审美问题。",
		TemporalPolicy:      "严格区分‘应下/约好次日八点之后’与‘到了次日八点之后’：前者表示人物先接受未来约定，后续动作仍可发生在当晚；只有合同明确写‘次日到场/复看完成后’才允许要求正文推进到次日。future/next_chapter 事件若被 forbidden_moves 保留为待发生事项，不得反向要求本章提前完成。",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// buildEditorReviewStyleContract extracts only the style contract from the
// same draft context surface used by Drafter. A same-chapter candidate manifest
// is authoritative even when review is invoked without the original render
// lease: v3 consumes its bound archive and explicit v1/v2 consumes only its
// frozen embedded style. Only a chapter with no owning candidate compiles the
// current local draft context. No planning field is returned or mutated.
func buildEditorReviewStyleContract(
	st *store.Store,
	chapter int,
	refs toolspkg.References,
	style string,
	styleBody string,
) (json.RawMessage, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, fmt.Errorf("load render execution while building Editor style contract: %w", err)
	}
	if lock != nil && lock.Mode == domain.PipelineExecutionRender {
		if lock.TargetChapter != chapter || strings.TrimSpace(lock.PlanDigest) == "" {
			return nil, fmt.Errorf("Editor sealed style contract does not match active render lease")
		}
	}
	requirement, requirementErr := loadReviewEffectiveStyleRequirement(st, chapter)
	if requirementErr != nil {
		return nil, fmt.Errorf("validate Editor style requirement: %w", requirementErr)
	}
	if requirement.Required {
		_, receipt, _, receiptErr := toolspkg.LoadBoundArchivedEffectiveRenderStyleContract(
			st, chapter, requirement.PlanDigest,
		)
		if receiptErr != nil {
			return nil, fmt.Errorf("load required effective sealed Editor style contract: %w", receiptErr)
		}
		return append(json.RawMessage(nil), receipt.StyleContract...), nil
	}
	if requirement.ManifestVersion == pipelineRenderCandidatePreviousManifestVersion ||
		requirement.ManifestVersion == pipelineRenderCandidateLegacyManifestVersion {
		// Legacy candidates did not have an effective-style receipt. Their only
		// safe review authority is the exact style contract already embedded in
		// the frozen bytes; current config must not be overlaid during recovery.
		raw, _, frozenErr := toolspkg.LoadFrozenDraftRenderContext(st, chapter, requirement.PlanDigest)
		if frozenErr != nil {
			return nil, fmt.Errorf("load legacy frozen Editor style contract: %w", frozenErr)
		}
		contract, frozenErr := toolspkg.ExtractRenderStyleContract(raw)
		if errors.Is(frozenErr, toolspkg.ErrRenderStyleContractMissing) {
			return nil, nil
		}
		if frozenErr != nil {
			return nil, fmt.Errorf("extract legacy frozen Editor style contract: %w", frozenErr)
		}
		return canonicalEditorStyleContract(contract)
	}
	if strings.TrimSpace(styleBody) == "" && strings.TrimSpace(refs.GenreStyleProfiles) == "" {
		return nil, nil
	}
	args, _ := json.Marshal(map[string]any{"chapter": chapter, "profile": "draft"})
	raw, err := toolspkg.NewContextTool(st, refs, style).
		WithConfiguredStyle(styleBody).
		Execute(context.Background(), args)
	if err != nil {
		return nil, fmt.Errorf("build current Editor style contract: %w", err)
	}
	contract, err := toolspkg.ExtractRenderStyleContract(raw)
	if errors.Is(err, toolspkg.ErrRenderStyleContractMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("extract current Editor style contract: %w", err)
	}
	return canonicalEditorStyleContract(contract)
}

func canonicalEditorStyleContract(contract map[string]any) (json.RawMessage, error) {
	if len(contract) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func editorReviewRequiredOutcomes(plan domain.ChapterPlan) []string {
	complete := toolspkg.RenderRequiredOutcomes(plan)
	out := make([]string, 0, len(complete))
	seen := make(map[string]bool, len(complete))
	for _, outcome := range complete {
		clauses := strings.FieldsFunc(outcome, func(r rune) bool {
			return r == '；' || r == ';' || r == '。' || r == '\n'
		})
		results := make([]string, 0, len(clauses))
		for _, clause := range clauses {
			clause = strings.TrimSpace(clause)
			if clause == "" || editorReviewProcessRecipeClause(clause) {
				continue
			}
			results = append(results, clause)
		}
		result := strings.Join(results, "；")
		if result == "" || seen[result] {
			continue
		}
		seen[result] = true
		out = append(out, result)
	}
	return out
}

func editorReviewProcessRecipeClause(clause string) bool {
	// Explicit result/boundary markers win over procedure vocabulary. For
	// example, "唯一一次失败复测" is a hard limitation even though it contains
	// the word "复测", while "用两至三个短动作验证边界" is only a staging recipe.
	for _, resultMarker := range []string{
		"必须", "不得", "只准", "只允许", "唯一", "至少", "不低于", "准确",
		"阻断", "拒绝", "完成", "成立", "到账", "解锁", "获得", "失去", "仍为",
	} {
		if strings.Contains(clause, resultMarker) {
			return false
		}
	}
	for _, recipeMarker := range []string{
		"短动作", "动作拍", "动作步骤", "操作步骤", "点击路径", "流程举例",
		"逐笔", "逐项核对", "逐项验证", "亲自到场复核", "验证边界",
	} {
		if strings.Contains(clause, recipeMarker) {
			return true
		}
	}
	hasExecutionNoun := strings.Contains(clause, "动作") || strings.Contains(clause, "点击") ||
		strings.Contains(clause, "操作") || strings.Contains(clause, "步骤") || strings.Contains(clause, "流程")
	hasVerificationVerb := strings.Contains(clause, "验证") || strings.Contains(clause, "复核") ||
		strings.Contains(clause, "核验") || strings.Contains(clause, "测试")
	return hasExecutionNoun && hasVerificationVerb
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
	removedDeferredCheckMisread := false
	removedPlanCauseChoiceMisread := false
	filtered := make([]domain.ConsistencyIssue, 0, len(entry.Issues))
	for _, issue := range entry.Issues {
		text := strings.Join([]string{issue.Description, issue.Evidence, issue.Suggestion}, "\n")
		switch {
		case reviewIssueIsExplicitlyNonActionable(issue):
			removed = append(removed, "明确无需返工的零行动项")
			continue
		case aiVoicePayloadPassed && reviewClaimsMissingAIVoicePayload(text):
			removed = append(removed, "AI 腔 JSON 已完整读取且阻断红旗为空")
			continue
		case analysis.Metrics.ProtagonistWaver && reviewClaimsMissingProtagonistWaver(text):
			removed = append(removed, "主角迟疑已由动作证据命中")
			continue
		case reviewClaimsSystemMessageOverpacked(text) && !hasViolation("system_message_overpacked"):
			removed = append(removed, "独立系统消息被错误拼接")
			continue
		case reviewRejectsApprovedTrendCarrier(st, text, body, plan):
			removed = append(removed, "热梗承载人违背批准 plan")
			continue
		case reviewDemandsAbsentOptionalTrend(text, body, plan):
			removed = append(removed, "可选热梗未使用不构成缺失")
			continue
		case reviewRejectsStandaloneChatEmoticon(text) && hasStandaloneSystemChatEmoticon(body):
			removed = append(removed, "独立系统私聊颜文字被误判为正式条款")
			continue
		case entry.ContractStatus == "met" && len(entry.ContractMisses) == 0 && reviewMisreadsDeferredCheckAfterFutureAppointment(text, body, plan):
			removed = append(removed, "八点约定后的主动查看已按批准时序落地，次日复看仍留待后章")
			removedDeferredCheckMisread = true
			continue
		case entry.ContractStatus == "met" && len(entry.ContractMisses) == 0 && reviewMisreadsPlanBackedCauseChoice(text, body, plan):
			removed = append(removed, "批准 plan 的因果选择已由正文顺序证据落地")
			removedPlanCauseChoiceMisread = true
			continue
		default:
			filtered = append(filtered, issue)
		}
	}
	entry.Issues = filtered
	if aiVoicePayloadPassed {
		if reviewClaimsMissingAIVoicePayload(entry.Summary) {
			entry.Summary = fmt.Sprintf("第 %d 章结果级合同已完成，机械 AI 腔阻断红旗为空；非阻断规划建议不触发返工。", chapter)
		}
		for i := range entry.Dimensions {
			dimension := &entry.Dimensions[i]
			if dimension.Dimension != "ai_voice_detection" || !reviewClaimsMissingAIVoicePayload(dimension.Comment) {
				continue
			}
			dimension.Score = max(dimension.Score, 90)
			dimension.Verdict = "pass"
			dimension.Comment = fmt.Sprintf(
				"机械 red flag JSON 已读取：比喻密度 %.4f、对话占比 %.1f%%、格言命中 %d 条、主角动摇=%t、阻断红旗=0、非阻断建议=%d。",
				analysis.Metrics.FigurativeDensity,
				analysis.Metrics.DialogueRatio*100,
				len(analysis.Metrics.AphorismHits),
				analysis.Metrics.ProtagonistWaver,
				len(analysis.RedFlags),
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
	switch {
	case removedDeferredCheckMisread && removedPlanCauseChoiceMisread:
		entry.Summary = fmt.Sprintf("第 %d 章已按批准合同完成未来约定后的主动查看顺序与计划内因果选择；其余意见按当前正文证据评估。", chapter)
	case removedDeferredCheckMisread:
		entry.Summary = fmt.Sprintf("第 %d 章已按批准合同完成未来约定后的主动查看顺序；其余意见按当前正文证据评估。", chapter)
	case removedPlanCauseChoiceMisread:
		entry.Summary = fmt.Sprintf("第 %d 章已按批准合同完成计划内因果选择；其余意见按当前正文证据评估。", chapter)
	}
	for i := range entry.Dimensions {
		dimension := &entry.Dimensions[i]
		if reviewDemandsAbsentOptionalTrend(dimension.Comment, body, plan) {
			dimension.Comment = removeReviewSentences(dimension.Comment, func(sentence string) bool {
				return reviewDemandsAbsentOptionalTrend(sentence, body, plan)
			})
			removed = append(removed, "可选热梗未使用不构成缺失")
		}
		contractEvidenceTrusted := entry.ContractStatus == "met" && len(entry.ContractMisses) == 0
		if contractEvidenceTrusted && reviewMisreadsDeferredCheckAfterFutureAppointment(dimension.Comment, body, plan) {
			dimension.Comment = removeReviewSentences(dimension.Comment, func(sentence string) bool {
				return reviewTextContainsAny(sentence, "结算", "系统反馈", "蓝色卡片") &&
					reviewFutureTimeRE.MatchString(sentence) &&
					reviewTextContainsAny(sentence, "移至", "挪到", "放到", "等到", "延至", "必须在", "需将")
			})
			dimension.Comment = appendReviewComment(dimension.Comment,
				"object_response_rhythm_flat warning：正文先落定未来复看约定，等人物对话结束后再由主角主动查看结算；未来复看没有被提前完成，此项无需改写。")
			dimension.Score = max(dimension.Score, 90)
			dimension.Verdict = "pass"
			removed = append(removed, "八点约定后的主动查看已按批准时序落地，次日复看仍留待后章")
		}
		if contractEvidenceTrusted && reviewMisreadsPlanBackedCauseChoice(dimension.Comment, body, plan) {
			dimension.Comment = removeReviewSentences(dimension.Comment, func(sentence string) bool {
				return reviewMisreadsPlanBackedCauseChoice(sentence, body, plan)
			})
			dimension.Comment = appendReviewComment(dimension.Comment,
				"批准 plan 的 causal beat 已在正文中按“原因动作→人物选择”的顺序落地；此项不再作为缺失返工。")
			dimension.Score = max(dimension.Score, 90)
			dimension.Verdict = "pass"
			removed = append(removed, "批准 plan 的因果选择已由正文顺序证据落地")
		}
	}
	// An explicit formal Editor rewrite disposition is authoritative. Project
	// sanitization may remove a contradictory/non-actionable issue, but it must
	// never silently turn the model's exact `是` into an acceptance.
	if entry.Verdict != "rewrite" && len(entry.Issues) == 0 && entry.ContractStatus == "met" && reviewDimensionsPass(entry.Dimensions) {
		entry.Verdict = "accept"
		entry.AffectedChapters = nil
	}
	return uniqueNonEmptyStrings(removed)
}

var (
	reviewFutureTimeRE = regexp.MustCompile(`(?:明早|明天|次日|明日|翌日)[^\n。！？]{0,24}(?:点|时|复看|再看|见|到)`)
	reviewClockTokenRE = regexp.MustCompile(`[零一二两三四五六七八九十百0-9]{1,4}点(?:半)?`)
)

func reviewMisreadsDeferredCheckAfterFutureAppointment(text, body string, plan *domain.ChapterPlan) bool {
	text = strings.TrimSpace(text)
	if text == "" || plan == nil {
		return false
	}
	planText := chapterPlanReviewText(plan)
	if !strings.Contains(planText, "结算") || !reviewFutureTimeRE.MatchString(planText) ||
		!reviewTextContainsAny(planText, "主动查看", "主动翻开", "主动点开") ||
		!reviewTextContainsAny(planText, "应下", "约好", "约定先落地") ||
		!reviewTextContainsAny(planText, "不得提前完成", "提前写", "尚未发生", "仍待确认", "留待后章") {
		return false
	}
	if !reviewTextContainsAny(text, "结算", "系统反馈", "蓝色卡片") ||
		!reviewFutureTimeRE.MatchString(text) ||
		!reviewTextContainsAny(text, "移至", "挪到", "放到", "等到", "延至", "必须在", "需将") {
		return false
	}

	appointmentLoc := reviewFutureTimeRE.FindStringIndex(body)
	if len(appointmentLoc) != 2 {
		return false
	}
	appointmentAt := appointmentLoc[0]
	appointmentText := body[appointmentLoc[0]:appointmentLoc[1]]
	clockToken := reviewClockTokenRE.FindString(appointmentText)
	acceptMarkers := []string{"我到", "我来", "准时到", "应下", "答应", "说定"}
	if clockToken != "" {
		acceptMarkers = append([]string{
			clockToken + "，我到", clockToken + "我到", clockToken + "，我来", clockToken + "我来",
			"我" + clockToken + "到", "我" + clockToken + "来",
		}, acceptMarkers...)
	}
	acceptedAt := firstTextIndexAfter(body, appointmentAt, acceptMarkers...)
	checkedAt := firstTextIndexAfter(body, acceptedAt, "主动点开", "主动查看", "才点开", "才查看")
	settledAt := firstTextIndexAfter(body, checkedAt, "结算完成", "支出结算")
	return acceptedAt > appointmentAt && checkedAt > acceptedAt && settledAt > checkedAt && settledAt-appointmentAt <= 1600
}

var reviewPlanEvidenceStopTokens = map[string]bool{
	"主角": true, "角色": true, "人物": true, "本章": true, "正文": true,
	"必须": true, "直接": true, "推动": true, "选择": true, "因果": true,
	"已经": true, "完成": true, "落地": true, "缺失": true, "没有": true,
	"未兑": true, "兑现": true, "要求": true, "通过": true, "当前": true,
}

// reviewMisreadsPlanBackedCauseChoice only clears a causal complaint when the
// approved plan contains an explicit causal beat and the body independently
// carries multiple plan-derived evidence tokens in cause -> choice order. It is
// deliberately conservative: prose phrases, scene names and character names
// are never embedded in the reviewer, and an unstructured body gets no waiver.
func reviewMisreadsPlanBackedCauseChoice(text, body string, plan *domain.ChapterPlan) bool {
	text = strings.TrimSpace(text)
	body = strings.TrimSpace(body)
	if text == "" || body == "" || plan == nil ||
		!reviewTextContainsAny(text, "缺失", "未兑现", "没有完成", "因果断裂", "未完成", "未发生", "未推动", "没有推动") {
		return false
	}
	for _, beat := range plan.CausalSimulation.CausalBeats {
		causeTokens := reviewPlanEvidenceTokens(beat.Cause)
		choiceTokens := reviewPlanEvidenceTokens(beat.CharacterChoice)
		causeTokens, choiceTokens = reviewDistinctEvidenceTokens(causeTokens, choiceTokens)
		if len(causeTokens) < 2 || len(choiceTokens) < 2 ||
			reviewEvidenceHitCount(text, causeTokens) == 0 || reviewEvidenceHitCount(text, choiceTokens) == 0 {
			continue
		}
		causeAt := reviewFirstEvidenceIndex(body, causeTokens)
		if causeAt < 0 {
			continue
		}
		choiceAt := reviewFirstEvidenceIndex(body[causeAt:], choiceTokens)
		if choiceAt < 0 {
			continue
		}
		choiceAt += causeAt
		if choiceAt <= causeAt || choiceAt-causeAt > 1600 {
			continue
		}
		windowEnd := causeAt + 1600
		if windowEnd > len(body) {
			windowEnd = len(body)
		}
		if reviewEvidenceHitCount(body[causeAt:choiceAt], causeTokens) < 2 ||
			reviewEvidenceHitCount(body[choiceAt:windowEnd], choiceTokens) < 2 {
			continue
		}
		return true
	}
	return false
}

func reviewPlanEvidenceTokens(text string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, token := range rag.TokenizeForBM25(text) {
		token = strings.TrimSpace(token)
		if token == "" || reviewPlanEvidenceStopTokens[token] || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func reviewDistinctEvidenceTokens(left, right []string) ([]string, []string) {
	leftSet := make(map[string]bool, len(left))
	rightSet := make(map[string]bool, len(right))
	for _, token := range left {
		leftSet[token] = true
	}
	for _, token := range right {
		rightSet[token] = true
	}
	leftOut := make([]string, 0, len(left))
	for _, token := range left {
		if !rightSet[token] {
			leftOut = append(leftOut, token)
		}
	}
	rightOut := make([]string, 0, len(right))
	for _, token := range right {
		if !leftSet[token] {
			rightOut = append(rightOut, token)
		}
	}
	return leftOut, rightOut
}

func reviewEvidenceHitCount(text string, tokens []string) int {
	count := 0
	for _, token := range tokens {
		if strings.Contains(text, token) {
			count++
		}
	}
	return count
}

func reviewFirstEvidenceIndex(text string, tokens []string) int {
	first := -1
	for _, token := range tokens {
		if at := strings.Index(text, token); at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	return first
}

func chapterPlanReviewText(plan *domain.ChapterPlan) string {
	if plan == nil {
		return ""
	}
	parts := []string{plan.Goal, plan.Conflict, plan.Hook}
	parts = append(parts, plan.Contract.RequiredBeats...)
	parts = append(parts, plan.Contract.ForbiddenMoves...)
	parts = append(parts, plan.Contract.ContinuityChecks...)
	parts = append(parts, plan.Contract.EvaluationFocus...)
	parts = append(parts, plan.CausalSimulation.AntiAIPlan.ReviewChecks...)
	parts = append(parts, plan.CausalSimulation.DecisionPoints...)
	parts = append(parts, plan.CausalSimulation.OutcomeShift...)
	for _, beat := range plan.CausalSimulation.CausalBeats {
		parts = append(parts, beat.Cause, beat.CharacterChoice, beat.WorldResponse, beat.StoryResult)
	}
	ending := plan.CausalSimulation.EndingContract
	parts = append(parts, ending.EndingMode, ending.ConcreteAnchor, ending.Consequence, ending.NextChapterPull, ending.WhyNotUI)
	parts = append(parts, ending.ForbiddenEndings...)
	return strings.Join(compactNonEmptyStrings(parts), "\n")
}

func firstTextIndex(text string, markers ...string) int {
	best := -1
	for _, marker := range markers {
		if marker = strings.TrimSpace(marker); marker == "" {
			continue
		}
		if idx := strings.Index(text, marker); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

func firstTextIndexAfter(text string, after int, markers ...string) int {
	if after < 0 || after >= len(text) {
		return -1
	}
	relative := firstTextIndex(text[after+1:], markers...)
	if relative < 0 {
		return -1
	}
	return after + 1 + relative
}

func reviewTextContainsAny(text string, markers ...string) bool {
	for _, marker := range markers {
		if marker != "" && strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func compactNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func removeReviewSentences(text string, shouldRemove func(string) bool) string {
	if strings.TrimSpace(text) == "" || shouldRemove == nil {
		return strings.TrimSpace(text)
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '!', '?', '\n', '\r':
			return true
		default:
			return false
		}
	})
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || shouldRemove(part) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "。")
}

func appendReviewComment(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return strings.TrimRight(base, "。；;，, ") + "。" + extra
}

func mechanicalHasStructuralProseWarning(mechanical *reviewreport.MechanicalGatePayload) bool {
	if mechanical == nil {
		return false
	}
	for _, violation := range mechanical.RuleViolations {
		if reviewreport.IsStructuralProseViolation(violation) {
			return true
		}
	}
	return false
}

func editorAIVoicePayloadPassed(chapter int, body string, analysis domain.AIVoiceAnalysis) bool {
	if analysis.Chapter != chapter || analysis.BodySHA256 == "" || analysis.BodySHA256 != reviewreport.BodySHA256(body) {
		return false
	}
	if analysis.Metrics.ParagraphCount <= 0 || analysis.Metrics.SentenceCount <= 0 {
		return false
	}
	for _, flag := range analysis.RedFlags {
		if domain.IsAdvisoryAIVoiceFlag(flag) {
			continue
		}
		return false
	}
	return strings.Contains(analysis.Label, "通过") || strings.Contains(analysis.Summary, "未发现硬性")
}

func reconcileWarningOnlyEditorReview(
	entry *domain.ReviewEntry,
	editorMarkdown string,
	bodyHash string,
	mechanical *reviewreport.MechanicalGatePayload,
	analysis domain.AIVoiceAnalysis,
	judge *deepseekAIJudgeArtifact,
) bool {
	if entry == nil || mechanical == nil || judge == nil || strings.TrimSpace(bodyHash) == "" {
		return false
	}
	if strings.TrimSpace(extractLine(editorMarkdown, "## 是否需要改写")) != "否" ||
		entry.ContractStatus != "met" || len(entry.ContractMisses) > 0 || !reviewDimensionsStrictlyPass(entry.Dimensions) ||
		judge.Blocking || deepseekJudgeBlocking(*judge) ||
		entry.Chapter <= 0 || entry.Chapter != mechanical.Chapter || entry.Chapter != analysis.Chapter || entry.Chapter != judge.Chapter ||
		strings.TrimSpace(entry.BodySHA256) != bodyHash || strings.TrimSpace(mechanical.BodySHA256) != bodyHash ||
		strings.TrimSpace(analysis.BodySHA256) != bodyHash || strings.TrimSpace(judge.BodySHA256) != bodyHash {
		return false
	}
	reviewreport.ApplyExternalCorroborationWithEditor(mechanical, deepSeekExternalAIJudge(judge), entry)
	if !strings.Contains(strings.ToLower(judge.Verdict), "human") ||
		reviewExistingAIGCGatePercent(mechanical.AIGCReport) >= deepseekAIJudgePassExclusive {
		return false
	}
	for _, flag := range analysis.RedFlags {
		switch strings.TrimSpace(flag.Severity) {
		case "critical":
			return false
		case "error":
			if !reviewreport.EditorExplicitlySupportsContextualDialogueRatioErrorClearance(entry, mechanical, &analysis, flag) {
				return false
			}
			continue
		}
		if reviewreport.IsBlockingAIVoiceFlagInAnalysis(flag, analysis) &&
			!reviewreport.EditorExplicitlySupportsAIVoiceWarningClearance(entry, mechanical, &analysis, flag) {
			return false
		}
	}
	for _, violation := range mechanical.RuleViolations {
		if reviewreport.IsDeterministicMechanicalViolation(violation) {
			return false
		}
		if reviewreport.IsStructuralProseViolation(violation) &&
			!reviewreport.EditorExplicitlySupportsStructuralProseWarningClearance(entry, mechanical, violation) {
			return false
		}
	}
	for _, issue := range entry.Issues {
		if issue.Severity != "warning" {
			return false
		}
		if reviewIssueIsStructuralProseFailure(issue) {
			return false
		}
	}
	entry.Issues = nil
	entry.Verdict = "accept"
	entry.AffectedChapters = nil
	entry.Summary = fmt.Sprintf("第 %d 章结果级合同、机械门禁、AI voice 与独立裸文 Reviewer 均通过；Editor 的 warning 级意见保留在原始报告中，不触发返工。", entry.Chapter)
	return true
}

func reviewDimensionsStrictlyPass(dimensions []domain.DimensionScore) bool {
	if len(dimensions) != len(reviewDimensionNames) {
		return false
	}
	seen := make(map[string]bool, len(reviewDimensionNames))
	for _, dimension := range dimensions {
		name := strings.TrimSpace(dimension.Dimension)
		known := false
		for _, want := range reviewDimensionNames {
			if name == want {
				known = true
				break
			}
		}
		if !known || seen[name] || dimension.Score < 80 || strings.TrimSpace(dimension.Verdict) != "pass" {
			return false
		}
		seen[name] = true
	}
	return true
}

func reviewIssueIsStructuralProseFailure(issue domain.ConsistencyIssue) bool {
	if reviewIssueIsExplicitlyNonActionable(issue) {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(issue.Type + " " + issue.Description + " " + issue.Evidence))
	for _, marker := range []string{
		"dialogue_conveyor_overuse", "dialogue_info_dump", "templated_dialogue_chain",
		"pov_interiority_thin", "state_clause_pile", "not_but_overuse",
		"对白传送带", "对话信息倾倒", "信息倾倒", "流程腔", "工作汇报",
		"项目复盘", "不像人", "不通顺", "ai 腔", "ai味", "ai 味",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func reviewIssueIsExplicitlyNonActionable(issue domain.ConsistencyIssue) bool {
	return isNonActionableReviewIssue(strings.Join([]string{
		issue.Description,
		issue.Evidence,
		issue.Suggestion,
	}, "\n"))
}

func reviewHasStructuralProseFailure(entry *domain.ReviewEntry, mechanical *reviewreport.MechanicalGatePayload) bool {
	if entry != nil {
		for _, issue := range entry.Issues {
			if reviewIssueIsStructuralProseFailure(issue) {
				return true
			}
		}
	}
	if mechanical != nil {
		for _, violation := range mechanical.RuleViolations {
			if reviewreport.IsStructuralProseViolation(violation) &&
				!reviewreport.EditorExplicitlySupportsStructuralProseWarningClearance(entry, mechanical, violation) {
				return true
			}
		}
	}
	return false
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

func reviewRejectsApprovedTrendCarrier(st *store.Store, text, body string, plan *domain.ChapterPlan) bool {
	if plan == nil {
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
		token := reviewTrendLanguageToken(item.Item)
		if token == "" || !strings.Contains(text, token) || !strings.Contains(body, token) {
			continue
		}
		for _, name := range reviewApprovedTrendCarrierNames(st, plan, item.CharacterCarrier) {
			if strings.Contains(text, name) && reviewCarrierUsesTrendToken(body, name, token) {
				return true
			}
		}
	}
	return false
}

func reviewApprovedTrendCarrierNames(st *store.Store, plan *domain.ChapterPlan, carrier string) []string {
	carrier = strings.TrimSpace(carrier)
	if carrier == "" {
		return nil
	}
	seen := map[string]bool{}
	var known []string
	addKnown := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] || !strings.Contains(carrier, name) {
			return
		}
		seen[name] = true
		known = append(known, name)
	}
	if st != nil {
		if characters, err := st.Characters.Load(); err == nil {
			for _, character := range characters {
				addKnown(character.Name)
			}
		}
	}
	if plan != nil {
		for _, state := range plan.CausalSimulation.InitialState {
			addKnown(state.Character)
		}
		for _, voice := range plan.CausalSimulation.VoiceLogic {
			addKnown(voice.Character)
		}
		for _, stage := range plan.CausalSimulation.OffscreenStage {
			addKnown(stage.Character)
		}
		for _, emotion := range plan.CausalSimulation.EmotionalLogic {
			addKnown(emotion.Character)
		}
		for _, visual := range plan.CausalSimulation.VisualDesign {
			addKnown(visual.Character)
		}
	}
	if len(known) > 0 {
		return known
	}
	// CharacterCarrier is itself a structured plan field. A short leading label
	// is accepted as a role/name fallback; free-form descriptions stay untrusted.
	labels := strings.FieldsFunc(carrier, func(r rune) bool {
		switch r {
		case '；', ';', '，', ',', '。', '！', '？', '!', '?', '：', ':', '\n', '\r':
			return true
		default:
			return false
		}
	})
	if len(labels) == 0 {
		return nil
	}
	label := strings.TrimSpace(labels[0])
	label = strings.TrimPrefix(label, "由")
	for _, suffix := range []string{"本人", "可选使用", "负责使用", "使用", "承载"} {
		label = strings.TrimSuffix(label, suffix)
	}
	label = strings.TrimSpace(label)
	if n := len([]rune(label)); n == 0 || n > 8 || strings.ContainsAny(label, " \t") {
		return nil
	}
	return []string{label}
}

func reviewCarrierUsesTrendToken(body, carrier, token string) bool {
	for carrierOffset := 0; carrierOffset < len(body); {
		carrierRel := strings.Index(body[carrierOffset:], carrier)
		if carrierRel < 0 {
			break
		}
		carrierAt := carrierOffset + carrierRel
		for tokenOffset := 0; tokenOffset < len(body); {
			tokenRel := strings.Index(body[tokenOffset:], token)
			if tokenRel < 0 {
				break
			}
			tokenAt := tokenOffset + tokenRel
			distance := tokenAt - carrierAt
			if distance < 0 {
				distance = -distance
			}
			if distance <= 360 {
				switch {
				case carrierAt <= tokenAt:
					window := body[carrierAt : tokenAt+len(token)]
					if strings.ContainsAny(window, "：:\"“‘「【") {
						return true
					}
				case tokenAt < carrierAt:
					window := body[tokenAt : carrierAt+len(carrier)]
					if strings.ContainsAny(window, "\"”’」】") {
						return true
					}
				}
			}
			tokenOffset = tokenAt + len(token)
		}
		carrierOffset = carrierAt + len(carrier)
	}
	return false
}

func reviewTrendLanguageToken(item string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(item), func(r rune) bool {
		switch r {
		case '`', '\'', '"', '“', '”', '‘', '’', '「', '」', '…', '，', ',', '。', '.', '!', '！', '?', '？', '；', ';', '：', ':', '\n', '\r':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}
	token := strings.TrimSpace(parts[0])
	if len([]rune(token)) > 24 {
		return ""
	}
	return token
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
		token := reviewTrendLanguageToken(item.Item)
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
		// 第八维会同时携带多个 structural warning 的 rule ID、正文证据和
		// 明确 clearance。沿用 issue 的 240 rune 上限会把尾部 clearance
		// 截掉，导致原始 Editor 已判“无需改写”，统一门禁却看不到证据。
		comment := stripMarkdownCellLimit(m[3], 1600)
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

func reviewVerdictFromMarkdown(md string, _ []domain.DimensionScore) string {
	line := normalizeEditorReviewScalar(extractLine(md, "## 是否需要改写"))
	if line == "是" {
		// An explicit Editor rewrite disposition is authoritative. Dimension
		// scores may explain its severity, but can never invert it to accept.
		return "rewrite"
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
	bare := strings.Trim(normalized, "（）()[]【】")
	nonAction := strings.HasPrefix(bare, "无严重问题") ||
		strings.HasPrefix(bare, "没有严重问题") ||
		strings.HasPrefix(bare, "无其他问题") ||
		strings.HasPrefix(bare, "没有其他问题")
	for _, marker := range []string{
		"无需整章返工", "无需返工", "不需要返工", "不需返工", "无需改写",
		"不触发返工", "不建议改写", "无需修改", "无需补充修改", "不需要修改", "可不修改",
		"不构成问题", "不构成当前章问题", "不构成强制改写", "不构成阻断", "非阻断",
		"不阻断本章通过", "不阻断当前章通过", "属可选优化", "可选优化", "实为优秀写作", "非必要",
	} {
		if strings.Contains(normalized, marker) {
			nonAction = true
			normalized = strings.ReplaceAll(normalized, marker, "")
		}
	}
	if !nonAction {
		return false
	}
	for _, marker := range []string{
		"仍需修改", "仍需改写", "仍需返工", "必须修改", "必须改写", "必须返工",
		"需要修改", "需要改写", "需要返工", "建议修改", "建议改写", "建议返工",
	} {
		if strings.Contains(normalized, marker) {
			return false
		}
	}
	return true
}

func stripMarkdownCell(s string) string {
	return stripMarkdownCellLimit(s, 240)
}

func stripMarkdownCellLimit(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "|")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.TrimSpace(s)
	if maxRunes > 0 && len([]rune(s)) > maxRunes {
		r := []rune(s)
		s = string(r[:maxRunes]) + "..."
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
7. **审美品质**：描写质感、对话区分度、用词质量、情感打动力；若用户消息的已批准上下文含 style_contract，必须用正文证据核对其中的 configured_style_rules 与 serial_style_memory。
8. **AI 腔检测**：必须读取用户消息里的 red flag JSON，检查比喻密度、对话占比、格言命中、主角动摇、章节功能和章末钩子均匀度。问题必须具体到段号/句号/风险来源。

章节文件首个非空行若符合“第N章 标题”（阿拉伯数字或中文数字均可），它是必须保留的规范章标题元数据，不是叙事段落，不属于第三人称限知 POV，也不计入开篇格言、段首句式、比喻密度或其他 AI 腔正文规则。正文第 1 段从标题后的首个非空段开始。你可以核对标题是否与已批准 plan 一致、评价标题吸引力，但不得把标题文字当作 opening_single_sentence_aphorism 等正文红旗，不得要求删除标题或把标题改成动作句。若 red flag JSON 的 evidence 恰好等于规范章标题，必须明确判为检测范围错位并清除该项，再从下一段审核正文。

若用户消息中的机械门禁列出 structural warning，而你根据当前正文判断它不阻断本章，必须在第 8 维证据的同一句中原样写出 rule ID、warning、正文里的有效打断或场景合理性证据，并明确写“无需改写”或“不触发返工”。只写中文别名、只说问题不大或只建议后续关注，不足以清除同哈希 warning；未逐条明确清除的 structural warning 继续阻断。

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
- style_contract 是 Drafter 同源的只读表达合同，只能评价叙述声音、距离、词汇、句法、节奏、意象、段落和对白质感。它不得被用来新增、删除、调序或改写冻结事件、人物决定、事实、因果、状态与知识边界；configured_style_rules 未要求逐条显性展示，serial_style_memory 也禁止机械同义替换。风格问题必须引用当前正文位置与可见效果。
- 若项目规则要求系统会聊天、吐槽、撑腰并始终支持主角，禁止建议把系统改成冷硬、静默、断联或纯任务机器人。
- 严格按 plan 的 temporal_policy 读时序。“应下/约好次日八点之后再查看”表示先把未来约定说定，再在当前场景查看；不等于必须推进到次日八点。只有合同明确写“次日到场/复看完成后”才可要求跳到次日；forbidden_moves 保留为后续章的事件绝不能被你提前到本章。
- 逐段朗读对白。角色轮流精准问答、句句完整、一次讲齐金额/时限/售后/责任，连续三段“某人做动作：台词”，或频繁用“答完/问完/说完后另一人才……”调度，均属于短剧腔和流程腔。系统必须回答主角刚问的具体问题；“钱没跑、陪你换条路、规矩不撤、先喘半口气”这类客服式空话直接判改写。
- “补测、核验、用途说明、临时固定、采购凭证、测试记录”等术语出现时，普通读者必须当场看懂哪里会坏、谁会吃亏、下一步做什么。每次换地点还要能回答上一场余波、主角为什么现在去、抵达后的首个阻力；只写锁屏、下楼、到了某地属于因果断裂。
- 热梗与颜文字是候选和使用上限，不是逐章门禁。正文没用不构成问题；用了才核对角色、语境和句法。“呱，……”若被选中，后面要接完整吐槽，不能写成“呱了一声”。生硬时要求删除，不得迁移到别处硬塞。
- red flag JSON 中 chapter_function_repetition 是面向下一章的非阻断规划建议。它不得降低当前章任何维度评分，不得写入当前章主要问题，不得把“否/通过”改成“可选/改写”；只有当前章原文本身已出现重复结构并具体损害阅读时，才能凭当前章证据另立问题。
- 不要复述章节内容，只指出问题。
- 引用原文必须用「」包裹，标明位置（章节序号 / 段落大意）。
- 不要寒暄、不要解释，直接进入评审。
`

// callEditorOnChapter 调用 Editor 模型出评审 markdown。
func callEditorOnChapter(model agentcore.ChatModel, premise, userRules, chapterReviewContext string, chNum int, chapterBody string, analysis domain.AIVoiceAnalysis, budget time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	redFlagJSON := editorAIVoiceReviewPayload(analysis, chapterBody)
	userMsg := fmt.Sprintf("## 项目用户规则（最高优先级）\n```json\n%s\n```\n\n## 本章已批准写前 plan 与同源只读 style_contract（合同事实，不得反向建议）\n%s\n\n## 故事前提（供参考）\n%s\n\n## AI 腔 red flag JSON（必须读取）\n```json\n%s\n```\n\n## 章节文件边界（硬约束）\n本章正文首个非空行是规范章标题元数据，必须保留；它不是叙事段落、不是 POV 句子，也不参与 opening_single_sentence_aphorism 等正文 AI 腔判断。正文段落编号从标题后的首个非空段开始；若 red flag evidence 等于标题，按检测范围错位清除，不得要求删改标题。\n\n## 本章正文（ch%02d）\n%s",
		truncateForContext(userRules, 6000), editorReviewChapterContextPayload(chapterReviewContext), truncateForContext(premise, 2000), redFlagJSON, chNum, chapterBody)

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

func editorReviewChapterContextPayload(raw string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return "```json\n" + truncateForContext(raw, 8000) + "\n```"
	}
	style := append(json.RawMessage(nil), fields["style_contract"]...)
	delete(fields, "style_contract")
	plan, err := json.Marshal(fields)
	if err != nil {
		return "```json\n" + truncateForContext(raw, 8000) + "\n```"
	}
	var b strings.Builder
	if len(style) > 0 && string(style) != "null" {
		b.WriteString("### 同源只读 style_contract（不可截断）\n```json\n")
		b.Write(style)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("### 已批准写前 plan / 结果合同（结构化预算 8000 bytes）\n```json\n")
	b.WriteString(truncateForContext(string(plan), 8000))
	b.WriteString("\n```")
	return b.String()
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
		line := strings.TrimSpace(ln)
		if !strings.HasPrefix(line, header) {
			continue
		}
		rest := strings.TrimPrefix(line, header)
		if rest != "" && !strings.ContainsRune(" ：:=", []rune(rest)[0]) {
			// `## 是否需要改写说明` and comment-prefixed shadow text are
			// not the exact protocol header.
			continue
		}
		rest = strings.TrimLeft(rest, " ：:=")
		rest = strings.TrimSpace(rest)
		if rest == "" && i+1 < len(sc) {
			rest = strings.TrimSpace(sc[i+1])
		}
		return rest
	}
	return "—"
}

// printReviewUsage 主动打印 --review-existing 子命令 usage。
func printReviewUsage(w *os.File) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: novel-studio --review-existing [--budget 180s] [--from N] [--to N]")
	fmt.Fprintln(w, "兼容别名：执行时会委派到 novel-studio --pipeline --stages review")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "示例:")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review                 # 评当前 output/novel/chapters/*.md")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review --from 1 --to 5")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review --budget 60s")
	fmt.Fprintln(w)
}
