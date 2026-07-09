package main

// --rewrite-existing：按 Editor 评审反馈逐章用 Writer LLM 做局部修订。
// 流程：
//   1. 读 chapters/{N}.md（原文）+ reviews/{N}.md（评审）
//   2. 拼上下文：premise + characters + 本章 outline entry + 项目连续性资料 + 项目设计/写法口径
//   3. 调用 Writer LLM 在原文基础上出局部修订版
//   4. 覆盖写回 chapters/{N}.md
//   5. （可选）--also-review 跑一遍重审验证
//
// 关键约束：保留故事圣经的所有伏笔、人物声音、章节情绪曲线，不引入新设定。

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
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

type rewriteFlags struct {
	RewriteExisting string        // --rewrite-existing <path>
	Budget          time.Duration // --budget 每章硬时间（默认 180s，Writer 输出长）
	Start, End      int           // --from / --to
	Role            string        // --role 模型角色：writer / coordinator，默认 writer
	MaxRounds       int           // --max-rounds 红旗复审闭环最多轮数
	PolishWarnings  bool          // --polish-warnings 将黄旗择优打磨也送入 Writer
	BriefOnly       bool          // --brief-only 只刷新 rewrite brief，不调用 Writer
}

type rewritePatchBounds struct {
	Original   int
	Min        int
	Max        int
	ProjectMin int
	ProjectMax int
	Source     string
}

const (
	rewriteMinFinalWords       = 2300
	rewriteMaxNetGrowthPercent = 15
	maxRewriteWriterAttempts   = 5
)

type rewriteCallOptions struct {
	LengthBounds  rewritePatchBounds
	Attempt       int
	RetryFeedback string
	Compression   bool
}

type patchRewriteValidationError struct {
	Kind    string
	Message string
}

func (e *patchRewriteValidationError) Error() string {
	return e.Message
}

const patchRewriteLengthError = "length"

func parseRewriteFlags(argv []string) (rewriteFlags, []string, error) {
	fs := flag.NewFlagSet("rewrite-existing", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --rewrite-existing [--budget 180s] [--from N] [--to N] [--role writer]\n\n")
		fmt.Fprintf(os.Stderr, "兼容别名：执行时会委派到 novel-studio --pipeline --stages rewrite。\n")
		fmt.Fprintf(os.Stderr, "按 Editor 评审反馈逐章用 Writer LLM 重写 chapters/*.md。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f rewriteFlags
	f.Budget = 180 * time.Second
	fs.StringVar(&f.RewriteExisting, "rewrite-existing", "", "项目根目录（缺省当前目录）")
	fs.DurationVar(&f.Budget, "budget", f.Budget, "每章 Writer 调用硬时间预算")
	fs.IntVar(&f.Start, "from", 0, "起始章号（含），0=自动")
	fs.IntVar(&f.End, "to", 0, "结束章号（含），0=自动")
	fs.StringVar(&f.Role, "role", "writer", "调用的模型角色：writer / coordinator")
	fs.IntVar(&f.MaxRounds, "max-rounds", 3, "红旗重写-复审闭环最多轮数；0/负数按 1 轮处理")
	fs.BoolVar(&f.PolishWarnings, "polish-warnings", false, "无红旗但存在黄旗时，也按质量优先原则择优打磨")
	fs.BoolVar(&f.BriefOnly, "brief-only", false, "只刷新 reviews/{N}_rewrite_brief.md，不调用 Writer、不改正文")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func hasRewriteExistingFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--rewrite-existing" {
			return true
		}
	}
	return false
}

func rewriteExistingPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		printRewriteUsage(os.Stderr)
		return nil
	}
	flags, extra, err := parseRewriteFlags(args)
	if err != nil {
		printRewriteUsage(os.Stderr)
		return err
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "未识别的额外参数：%v\n\n", extra)
		printRewriteUsage(os.Stderr)
		return fmt.Errorf("--rewrite-existing 不接受额外参数")
	}

	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("首次启动需要先在交互终端运行一次 novel-studio 完成配置引导，或手写配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	projDir := flags.RewriteExisting
	if projDir == "" {
		projDir = opts.Dir // --pipeline --dir <项目> 经此传入
	}
	if projDir == "" {
		projDir, _ = os.Getwd()
	}
	if err := normalizeOutputDirForInvocation(&cfg, projDir); err != nil {
		return err
	}
	rules.EnsureHomeRulesDir()
	bundle := assets.Load(cfg.Style)
	fmt.Fprintf(os.Stderr, "[rewrite-existing] 工作目录: %s\n", projDir)

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return fmt.Errorf("初始化 host: %w", err)
	}
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()

	st := store.NewStore(eng.Dir())
	if err := ensurePipelineRAGReady(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[rewrite-existing] 写前 RAG 检查失败，降级使用本地上下文继续：%v\n", err)
	}
	var ragEmbedder rag.Embedder
	if embedder, enabled, err := bootstrap.NewRAGEmbedder(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[rewrite-existing] RAG embedding 初始化失败，仅写关键词索引：%v\n", err)
	} else if enabled {
		ragEmbedder = embedder
	}
	var ragVectorWriter rag.VectorWriter
	if writer, enabled, err := bootstrap.NewRAGQdrantClient(cfg, false); err != nil {
		fmt.Fprintf(os.Stderr, "[rewrite-existing] Qdrant 初始化失败，仅写本地 RAG：%v\n", err)
	} else if enabled {
		ragVectorWriter = writer
	}
	premise, _ := st.Outline.LoadPremise()
	characters, _ := st.Characters.Load()
	volumes, _ := st.Outline.LoadLayeredOutline()

	// ForRole 对未显式配置的角色回退到默认模型，始终返回可用实例。
	model := eng.Models().ForRole(flags.Role)

	chaptersDir := filepath.Join(eng.Dir(), "chapters")
	reviewsDir := filepath.Join(eng.Dir(), "reviews")

	matches, _ := filepath.Glob(filepath.Join(chaptersDir, "[0-9][0-9].md"))
	if len(matches) == 0 {
		return fmt.Errorf("未在 %s 找到任何章节", chaptersDir)
	}

	start, end := flags.Start, flags.End
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = len(matches)
	}
	if end > len(matches) {
		end = len(matches)
	}

	fmt.Fprintf(os.Stderr, "[rewrite-existing] 待重写章节：%d - %d（共 %d 章）\n", start, end, end-start+1)

	maxRounds := flags.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 1
	}
	totalRewritten := 0
	totalBriefs := 0
	for round := 1; round <= maxRounds; round++ {
		successCount, failureCount, skippedCount := 0, 0, 0
		for chNum := start; chNum <= end; chNum++ {
			chapterRel := filepath.ToSlash(filepath.Join("chapters", fmt.Sprintf("%02d.md", chNum)))
			briefRel := filepath.ToSlash(filepath.Join("reviews", fmt.Sprintf("%02d_rewrite_brief.md", chNum)))
			chapterPath := filepath.Join(chaptersDir, fmt.Sprintf("%02d.md", chNum))
			original, err := os.ReadFile(chapterPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 读原文失败：%v\n", chNum, err)
				failureCount++
				continue
			}
			reviewPath := filepath.Join(reviewsDir, fmt.Sprintf("%02d.md", chNum))
			reviewBody, _ := os.ReadFile(reviewPath)
			plan := buildRevisionPlan(eng.Dir(), chNum, string(original), string(reviewBody))
			if err := writeRevisionBrief(eng.Dir(), plan); err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 写 rewrite brief 失败：%v\n", chNum, err)
			} else {
				totalBriefs++
			}
			if flags.BriefOnly {
				if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "rewrite-brief-only", briefRel); err != nil {
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d brief-only checkpoint 失败：%v\n", chNum, err)
				}
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d brief 已刷新，跳过 Writer\n", chNum)
				skippedCount++
				continue
			}
			shouldRewrite := plan.HasRed || (flags.PolishWarnings && plan.HasYellow)
			if !shouldRewrite {
				if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "rewrite-not-needed", chapterRel); err != nil {
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d rewrite-not-needed checkpoint 失败：%v\n", chNum, err)
				}
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 无红旗，跳过自动重写（黄旗仅作择优建议）\n", chNum)
				skippedCount++
				continue
			}

			mode := "红旗重写"
			if !plan.HasRed {
				mode = "黄旗择优打磨"
			}
			fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 第 %d 轮%s中（预算 %s）...\n", chNum, round, mode, flags.Budget)
			outlineEntry := lookupOutlineEntry(volumes, chNum)
			bounds := rewritePatchBoundsFor(eng.Dir(), string(original))
			var newText string
			var retryFeedback string
			compressionRetry := false
			rewriteOK := false
			for attempt := 1; attempt <= maxRewriteWriterAttempts; attempt++ {
				newText, err = callWriterRewrite(model, eng.Dir(), bundle.References, cfg.Style, premise, characters, outlineEntry, chNum, string(original), plan.Brief, flags.Budget, rewriteCallOptions{
					LengthBounds:  bounds,
					Attempt:       attempt,
					RetryFeedback: retryFeedback,
					Compression:   bounds.Original > bounds.Max || compressionRetry,
				})
				if err != nil {
					if attempt < maxRewriteWriterAttempts {
						fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d Writer 调用失败，重试一次：%v\n", chNum, err)
						retryFeedback = rewriteModelRetryFeedback(err, bounds)
						continue
					}
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 重写失败：%v\n", chNum, err)
					break
				}
				if normalized, changed := normalizeRewriteParagraphingForGate(newText); changed {
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 已归并普通孤句段，写回前重新校验\n", chNum)
					newText = normalized
				}
				if err := validatePatchRewriteWithBounds(string(original), newText, bounds); err != nil {
					if isPatchRewriteLengthError(err) && attempt < maxRewriteWriterAttempts {
						fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 字数未通过，按 %d-%d 字重试一次：%v\n", chNum, bounds.Min, bounds.Max, err)
						retryFeedback = rewriteLengthRetryFeedback(err, bounds)
						compressionRetry = true
						continue
					}
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 拒绝写回：%v\n", chNum, err)
					break
				}
				if err := validateRewritePreflight(st, chNum, newText, flags.PolishWarnings); err != nil {
					if attempt < maxRewriteWriterAttempts {
						fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 写回前门禁未通过，重试一次：%v\n", chNum, err)
						retryFeedback = rewritePreflightRetryFeedback(err, bounds)
						continue
					}
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 拒绝写回：%v\n", chNum, err)
					break
				}
				rewriteOK = true
				break
			}
			if !rewriteOK {
				failureCount++
				continue
			}
			backupPath := chapterPath + ".pre-rewrite.md"
			if _, err := os.Stat(backupPath); os.IsNotExist(err) {
				if err := os.WriteFile(backupPath, original, 0o644); err != nil {
					fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 备份失败：%v\n", chNum, err)
				}
			}
			if err := os.WriteFile(chapterPath, []byte(newText), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 写回失败：%v\n", chNum, err)
				failureCount++
				continue
			}
			if err := syncRewriteProgressWordCount(st, chNum, rewriteWordCount(newText)); err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 同步进度字数失败：%v\n", chNum, err)
				failureCount++
				continue
			}
			if err := toolspkg.UpsertRAGChunks(context.Background(), st, ragEmbedder, ragVectorWriter, rewriteExistingRAGChunks(chNum, plan.Brief, newText), domain.RAGIndexConfig{}); err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d RAG 重写沉淀失败：%v\n", chNum, err)
			}
			if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "rewrite-existing", chapterRel); err != nil {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d rewrite-existing checkpoint 失败：%v\n", chNum, err)
			}
			fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d → %d 字（备份在 %s）\n", chNum, rewriteWordCount(newText), backupPath)
			successCount++
			totalRewritten++
		}

		if failureCount > 0 {
			return fmt.Errorf("第 %d 轮重写未全部完成：成功 %d 章，跳过 %d 章，失败 %d 章", round, successCount, skippedCount, failureCount)
		}
		if successCount == 0 {
			if flags.BriefOnly {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] brief-only 完成，刷新 %d 份 brief\n", totalBriefs)
				return nil
			}
			fmt.Fprintf(os.Stderr, "[rewrite-existing] 无章节需要重写/打磨；跳过 %d 章\n", skippedCount)
			return nil
		}

		fmt.Fprintf(os.Stderr, "[rewrite-existing] 第 %d 轮完成，开始复审红旗...\n", round)
		if err := reviewExistingPipeline(opts, rewriteLoopReviewArgs(flags)); err != nil {
			return fmt.Errorf("第 %d 轮重写后复审失败: %w", round, err)
		}
		includeYellow := flags.PolishWarnings
		blocking, err := revisionChaptersNeedingWork(eng.Dir(), start, end, includeYellow)
		if err != nil {
			return err
		}
		if len(blocking) == 0 {
			if err := completeResolvedRewrites(eng.Dir(), start, end, includeYellow); err != nil {
				return err
			}
			if includeYellow {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] 复审无红旗/黄旗，重写闭环完成\n")
			} else {
				fmt.Fprintf(os.Stderr, "[rewrite-existing] 复审无红旗，重写闭环完成\n")
			}
			return nil
		}
		blockingLabel := "红旗"
		if includeYellow {
			blockingLabel = "红旗/黄旗"
		}
		fmt.Fprintf(os.Stderr, "[rewrite-existing] 复审仍有%s章节：%s\n", blockingLabel, formatChapterList(blocking))
		if round == maxRounds {
			return fmt.Errorf("达到最大闭环轮数 %d 后仍有%s章节：%s", maxRounds, blockingLabel, formatChapterList(blocking))
		}
	}
	if totalRewritten == 0 {
		fmt.Fprintln(os.Stderr, "[rewrite-existing] 未发现红旗，无需重写")
	}
	fmt.Fprintf(os.Stderr, "[rewrite-existing] 完成\n")
	return nil
}

func completeResolvedRewrites(projectDir string, start, end int, includeYellow ...bool) error {
	requireNoYellow := len(includeYellow) > 0 && includeYellow[0]
	st := store.NewStore(projectDir)
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("读取 progress 失败: %w", err)
	}
	if progress == nil || len(progress.PendingRewrites) == 0 {
		return nil
	}
	pending := make(map[int]struct{}, len(progress.PendingRewrites))
	for _, ch := range progress.PendingRewrites {
		pending[ch] = struct{}{}
	}
	for ch := start; ch <= end; ch++ {
		if _, ok := pending[ch]; !ok {
			continue
		}
		chapterPath := filepath.Join(projectDir, "chapters", fmt.Sprintf("%02d.md", ch))
		body, err := os.ReadFile(chapterPath)
		if err != nil {
			return fmt.Errorf("读取第 %d 章用于清理 pending_rewrites 失败: %w", ch, err)
		}
		plan := buildRevisionPlan(projectDir, ch, string(body), "")
		if plan.HasRed {
			continue
		}
		if requireNoYellow && plan.HasYellow {
			continue
		}
		var reviewEntry domain.ReviewEntry
		if !readJSONIfExists(filepath.Join(projectDir, "reviews", fmt.Sprintf("%02d.json", ch)), &reviewEntry) {
			continue
		}
		if reviewEntry.Verdict != "accept" {
			continue
		}
		if err := st.Progress.CompleteRewrite(ch); err != nil {
			return fmt.Errorf("清理第 %d 章 pending_rewrites 失败: %w", ch, err)
		}
		fmt.Fprintf(os.Stderr, "[rewrite-existing] ch%02d 复审 accept，已清理 pending_rewrites\n", ch)
	}
	return nil
}

func rewriteExistingRAGChunks(chapter int, brief, rewritten string) []domain.RAGChunk {
	text := strings.TrimSpace(strings.Join([]string{
		fmt.Sprintf("# 第 %d 章旁路重写沉淀", chapter),
		"重写 brief：\n" + strings.TrimSpace(brief),
		fmt.Sprintf("重写后字数：%d", rewriteWordCount(rewritten)),
		"重写后开头：\n" + truncateForContext(strings.TrimSpace(rewritten), 240),
	}, "\n\n"))
	if text == "" {
		return nil
	}
	return []domain.RAGChunk{rag.NormalizeChunk(domain.RAGChunk{
		ID:         fmt.Sprintf("chapter:%03d:rewrite_existing", chapter),
		SourcePath: fmt.Sprintf("meta/rag/rewrite_existing/%02d.md", chapter),
		SourceKind: "chapter_rewrite",
		Facet:      "review",
		Context:    fmt.Sprintf("第 %d 章旁路重写 | rewrite-existing", chapter),
		Text:       text,
		Summary:    fmt.Sprintf("第 %d 章旁路重写完成，字数 %d。", chapter, rewriteWordCount(rewritten)),
		Keywords:   []string{"rewrite", "polish", fmt.Sprintf("chapter_%03d", chapter)},
		Metadata: map[string]any{
			"source":  "rewrite_existing",
			"chapter": chapter,
		},
	})}
}

// lookupOutlineEntry 从分层大纲里找本章的 outline entry，给 Writer 提供情绪标签。
func lookupOutlineEntry(volumes []domain.VolumeOutline, chapter int) *domain.OutlineEntry {
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for i := range a.Chapters {
				if a.Chapters[i].Chapter == chapter {
					return &a.Chapters[i]
				}
			}
		}
	}
	return nil
}

// rewriteSystemPrompt Writer 重写模式下的系统提示。
const rewriteSystemPrompt = `你是中文长篇小说资深主笔。原章节正文 AI 味重、被审核打回，你的任务是**依据本章大纲与评审反馈重新渲染本章正文**：剧情事件、因果链、伏笔、章末钩子、设定与权限边界都保留，但**句式、节奏、段落结构、用词要大幅重写，绝不沿用原文的句法节奏**（原文的节奏正是 AI 味来源）。把原文当作"要发生什么"的剧情参考，而不是要保留的文字。

## 硬性约束
1. **只服从当前项目**：人物名、组织名、事件、时间点、权限边界、证据状态和章节标题都以用户给出的当前项目材料、原文剧情、本章大纲和评审反馈为准。不得继承其他书、旧归档、示例项目或过期 prompt 的专属设定。
2. **保持原文叙事身份**：原文是第几人称、什么视角、谁的主视角，就保持该视角。不要把第三人称改成第一人称，也不要换主角。
3. **不乱加设定**：可以补匿名窗口人员、现场阻力、动作和物件摩擦；不能新增会改变主线事实的人名、组织、合同、授权、证据或能力。
4. **保留核心骨架、重写文字**：本章关键事件、因果链、伏笔、章末钩子和已入账资源必须保留；但承载它们的句子、段落、节奏要重新写，不是在原句上改字。
5. **事实和权限边界优先**：待确认就是待确认，看到不等于拿到，线索不等于责任坐实。不得让角色越权取得原文没有取得的材料。
6. **字数以当前项目为准**：保持原文字数量级，按评审要求适度增删；不要套用其他项目的固定字数模板。
7. **格式=正常小说，不是 Markdown**：首行是纯文本章节标题（如「第一章 讲稿第一句」，不要写 # 或任何 Markdown 符号）；段落之间空一行；全程禁止 # * - > 反引号 等标记。
8. **内容质量第一**：审核通过性不能压过小说质量。AI率目标不高于 5%。禁止为了检测引入注水、乱码、OCR 脏码、随机汉字、冷僻词堆砌、无信息清单、拟声长串或刻意错别字。
9. **反 AI 味硬要求（这是本次返工的核心）**：句长长短交替（不要整章中短句同一节奏）；用字多样、不整章复述同一具象名词（换称/代指/部件名，拉高 ttr）；段首不重复（不要连续多段以同一主语起句）；单句成段全章≤4 且绝不连续；对白断续、有隐瞒、被追问才挤出下一条信息（禁止一口气罗列清单/姓名+房号+背景的信息倾倒）；抽象判断后必落到动作、物件、感官或对白后果；配角对白要有主动误解/打断/拒绝/讨价的冲突。
10. **消除 AI 味优先于最小修订**：本次是 AI 味返工，原文整章节奏就是病因——允许并要求整章重新渲染句子与段落节拍。只有剧情/设定/因果不许推倒，文字层面放手重写，不要为"最小改动"而保留原文的 AI 味句法。
11. **高质锚点可留其意不留其形**：原文里真正有信息/关系/规则功能的段落（关键对白、人物选择、章末压力段），保留它承载的事实与功能，但用更自然的文字重新写出来；空对仗、拼接残留、视角硬伤必须改掉。
12. **术语不外溢**：不得把审核建议里的概念词直接写进正文。普通叙述和普通人对话少用"标的"；不得新增"可识别标的""可追索标的""可识别、可追索的标的"这类硬流程腔表达。需要表达时改成角色能说的话。
13. **AI味阻断项必须实改**：rewrite brief 中的 AI 味、段首/短句、动作/物件响应、高风险维度不是建议项。必须逐条在正文里处理，不能只解释、不能跳过，也不能用新增系统提示词糊过去。
14. **人味对白和标点必须实改**：普通人物对白不要像合同、广告词或对仗口号。讲价、互怼、求救要有省略、停顿、关系算盘和生活口气；正文与对白不要滥用分号，除童谣、咒词或故意念条款外，对话里原则上不用分号。
15. **纸面材料要像真实载体**：记录、邮件、系统提示、备忘录不要写成"页面写着：A；B；C；D"。优先用标题、栏位、遮挡、涂改、补字、角色逐行读到或被打断的方式呈现。
16. **结构感必须拆散**：遇到并列清单、连续顺序词、工整解释链、连续"X得发Y"、相邻重复对话点、视线/身体方位冲突时必须局部实改。不要为了"人味"随机加错别字；要用划掉、补字、读不全、卡壳、沉默、关系压力和视角合理化来修。
17. **作者画像与专业信息**：默认叙述者背后是 30 岁左右、有文学素养的程序员。她懂工具、流程、权限和系统边界，但正文不能像技术文档；专业名词不必逐个解释，必须通过界面痕迹、权限卡点、同事误判、生活动作和后果让非专业读者跟上。
18. **对白标签降密**：连续双人对白能靠声口、上一句问题、动作位置和关系压力辨认时，省略部分"他说/她问/他答"；三人以上只在必要处补标签。禁止"人物：台词"剧本格式。动作 beat 只保留会改变局面、遮掩信息、暴露情绪、打断台词或触发规则的动作。

## 优化要点
1. 按评审反馈逐项修复硬伤，优先处理设定矛盾、角色失真、AI腔、重复说明和弱钩子。
2. 把解释性总结藏进动作、对话、物件和选择后果里；少用作者替读者划重点。
3. 配角阻力要现实：推诿、误解、自保、权限不够、流程卡住都可以，但不要脸谱化。
4. 对话必须改变信息、关系或行动条件；不要让角色替章节功能报幕。
5. 章末钩子落在新的行动压力、证据变化、选择代价或物件状态上，不用抽象金句。
6. 主角判断来自现场可见事实；专业同盟可以提醒边界，但不能替主角开挂解决。
7. 黄旗打磨时只能做局部补丁，不得扩写整章；若建议会破坏原文读感，保留原文并让后续章节吸收。
8. 审核建议里的例句不是正文模板。重写时只吸收原则：不齐整、可复核、有现场动作；不要把建议原句直接照搬成新套路。
8. 段首轮换入口：同一页不要连续用主角名/他/物件显字起手。可用环境声、对话、宾语前置、邻居反应、动作结果进入；但不要为了变化硬造新意象。
9. 短句只保留在压迫、反转、停顿、视线落点处。普通说明、连续动作和信息复盘要合并成有呼吸的中句，避免一段一短句的机械节拍。
10. 动作 beat 必须承担功能：改变空间、暴露情绪、制造阻力、打断对话或触发规则。纯粹的"看/停/写/拿/按"填充动作要删、合并或交给对话和环境承接。
11. 物件回应不能等距确认主角行为。连续显字、震动、变色、弹提示要改成延迟、静默、误判、缺席或只在关键选择后发生一次。
12. 高风险维度处理方式：困惑度/TTR 低时，不堆冷僻词，改为增加具体声口和场景功能差异；结构指纹高时，打散同构段落和段首；突发性低时，混合长短句和未完成动作，而不是随机碎句。
13. 分号处理：保留角色故意念条款或正式文件里的必要分号；普通叙述和对白里的分号要优先改成句号、逗号或换行。若同一句出现三个以上分号，除非是受保护的正式载体，否则必须拆开。
14. 条款/单据处理：纸面或系统文本先给读者看见载体状态，再让角色反应。栏位信息分行呈现，不要一次性报幕；可以让页面漏一项、遮一项、被角色划掉一项，使规则从场景里长出来。
15. 人味对白处理：把"漂亮但不像人说"的句子改成角色会说的话。朋友之间可以抬杠、算账、嘴硬、半开玩笑，但不能整齐到像标语。
16. **黄旗打磨净增限制**：允许为了修人物、冲突和事实回收增补文字，但整章最终字数不得低于2300，且相对原文净增不得超过15%。删除不设比例限制。补配角对话、现场误判或行动代价时，同步删掉原有解释、重复物件响应、无功能短句或抽象比喻，避免只加不减。

## 输出格式
只输出新章节正文。不要解释、不要分析、不要建议、不要标题外的额外内容。
`

const compressionRewriteSystemPrompt = `你是中文长篇小说定稿编辑，当前任务是"超字章节的压缩型返工"。

你必须以用户给出的原章节为底稿，只做删、并、替换和极少量必要补句。不要重写成新章，不要新增完整场景，不要扩写世界观，不要新增有名字的人物、组织、合同、授权、证据或能力。

核心优先级：
1. 先把正文压进用户给出的字数窗口；如果爽点、对话和氛围发生冲突，先删重复氛围和解释性复盘。
2. 保留章节因果链、主视角、标题、已成立规则和关键物件。
3. 红旗修复以替换原有段落完成：开场压力/章末钩子优先替换信息倾倒段、流程压力段和结尾段，不额外加一整个新场景。
4. 删除重复灯光、屏幕、纸页、手机响应；合并连续短句；把"没有/没/停在半空/迟疑"类动作压到一两处。
5. 对话只保留能改变信息、关系或行动条件的句子；不要为了提高占比硬加闲聊。
6. 如果上一版因扩写被拒绝，这一版必须遵守"最终不少于2300、净增不超过15%"：补一个冲突句就删掉至少一句解释/异象/复盘；无法兼顾全部黄旗时，只修最影响 AI 味的 1-2 处。

输出格式：只输出压缩后的完整章节正文。不要解释、不要分析、不要列修改点。`

const rewriteDesignContextLimit = 70000

// callWriterRewrite 调 Writer 模型，按 review 重写本章正文。
func callWriterRewrite(
	model agentcore.ChatModel,
	projectDir string,
	refs toolspkg.References,
	style string,
	premise string,
	characters []domain.Character,
	outline *domain.OutlineEntry,
	chNum int,
	original string,
	review string,
	budget time.Duration,
	options rewriteCallOptions,
) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	// 拼上下文（限长，避免撑爆 token）
	charLines := make([]string, 0, len(characters))
	for _, c := range characters {
		if c.Role == "protagonist" || c.Role == "antagonist" {
			charLines = append(charLines, fmt.Sprintf("- %s（%s）：%s", c.Name, c.Role, truncateForContext(c.Description, 200)))
		}
	}
	charCtx := strings.Join(charLines, "\n")

	outlineCtx := ""
	if outline != nil {
		outlineCtx = fmt.Sprintf("- 本章标题：%s\n- 本章核心事件：%s\n- 本章钩子：%s\n- 场景锚点：%s",
			outline.Title,
			outline.CoreEvent,
			outline.Hook,
			strings.Join(outline.Scenes, "；"),
		)
	}

	reviewCtx := "（无评审）"
	if strings.TrimSpace(review) != "" {
		reviewCtx = truncateForContext(review, 2500)
	}
	preserveCtx := preserveAnchorContext(original)
	lengthCtx := rewriteLengthInstruction(options.LengthBounds, options.RetryFeedback)

	if options.Compression {
		userMsg := fmt.Sprintf(`## 故事前提
%s

## 核心人物（仅主角 / 反派）
%s

## 本章大纲
%s

## 本次写回字数窗口
%s

## 原文必须保留的高质锚点
%s

## 原章节正文（ch%02d）
%s

## 只吸收这些审核结论
%s

请对 ch%02d 做压缩型返工：先把正文压进字数窗口，再用替换信息倾倒段、流程压力段和结尾段的方式完成必要红旗修复。不要新增完整场景，不要扩写新人物对白，不要把评审术语写进正文；如果只是黄旗打磨，最终不少于2300字，净增不得超过15%%。`,
			truncateForContext(premise, 1200),
			charCtx,
			outlineCtx,
			lengthCtx,
			preserveCtx,
			chNum,
			truncateForContext(original, 9000),
			truncateForContext(reviewCtx, 1800),
			chNum,
		)
		userMsg = sanitizeRewritePromptText(userMsg)
		resp, err := model.Generate(ctx,
			[]agentcore.Message{
				{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: compressionRewriteSystemPrompt}}},
				{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: userMsg}}},
			},
			nil,
		)
		if err != nil {
			return "", err
		}
		text := strings.TrimSpace(resp.Message.TextContent())
		if text == "" {
			return "", fmt.Errorf("writer 返回空响应")
		}
		text = strings.TrimPrefix(text, "```markdown")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		return strings.TrimSpace(text), nil
	}

	writerCtx := writerNovelContext(projectDir, refs, style, chNum)
	if strings.TrimSpace(writerCtx) == "" {
		writerCtx = projectContinuityContext(projectDir)
	}
	designCtx := projectDesignContext(projectDir, chNum)

	userMsg := fmt.Sprintf(`## 故事前提
%s

## 核心人物（仅主角 / 反派）
%s

## 本章大纲
%s

## 项目设计与黄金三章/写作手法口径
%s

## 直接写作同源上下文（novel_context chapter=%02d）
%s

## 原文里要保留的剧情/规则功能（保留其承载的事实与功能，用全新的自然文字重写，不要照抄原句）
%s

## 本次写回字数窗口
%s

## 原章节正文（ch%02d）——只作剧情参考，其句式节奏是被打回的病因，不要沿用
%s

## 汇总审核建议 / rewrite brief（红旗必须修，黄旗择优修）
%s

请依据本章大纲与 rewrite brief，把 ch%02d **整章重新渲染成自然人类文笔的小说正文**，用来替换 AI 味重的原文。保留原文的剧情事件、因果链、伏笔、章末钩子与设定权限；但句子、段落、节奏、用词全部重写，绝不沿用原文的句法节奏。逐条落实反 AI 味硬要求（句长突发、用字多样拉高 ttr、段首不重复、单句成段≤4、对白断续无信息倾倒、抽象判断落到动作物件）。正常小说格式，不要 Markdown。不要把评审建议里的"可识别/可追索/标的"等概念词直接写进正文。`,
		truncateForContext(premise, 1500),
		charCtx,
		outlineCtx,
		truncateForContext(designCtx, rewriteDesignContextLimit),
		chNum,
		truncateForContext(writerCtx, 60000),
		preserveCtx,
		lengthCtx,
		chNum,
		truncateForContext(original, 12000),
		reviewCtx,
		chNum,
	)
	userMsg = sanitizeRewritePromptText(userMsg)

	resp, err := model.Generate(ctx,
		[]agentcore.Message{
			{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: rewriteSystemPrompt}}},
			{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: userMsg}}},
		},
		nil,
	)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(resp.Message.TextContent())
	if text == "" {
		return "", fmt.Errorf("writer 返回空响应")
	}
	// 兜底：剥除可能的 markdown 围栏
	text = strings.TrimPrefix(text, "```markdown")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text), nil
}

func projectDesignContext(projectDir string, chapter int) string {
	type contextFile struct {
		Title string
		Rel   string
		Limit int
	}
	coreFiles := []contextFile{
		{Title: "写作手法历史反馈", Rel: "写作手法_历史反馈沉淀.md", Limit: 4200},
		{Title: "大纲与写作流程", Rel: "大纲与写作流程_工程版.md", Limit: 2600},
		{Title: "世界规则", Rel: "world_rules.md", Limit: 3000},
		{Title: "时间线", Rel: "timeline.md", Limit: 4800},
		{Title: "动态分卷大纲", Rel: "layered_outline.md", Limit: 7200},
		{Title: "伏笔账本", Rel: "foreshadow_ledger.md", Limit: 3200},
		{Title: "角色档案", Rel: "characters.md", Limit: 4200},
		{Title: "角色设计", Rel: "角色.md", Limit: 4600},
		{Title: "人物关系状态", Rel: "relationship_state.md", Limit: 2800},
		{Title: "人物连续性", Rel: filepath.Join("meta", "character_continuity.md"), Limit: 3600},
		{Title: "故事罗盘", Rel: filepath.Join("meta", "compass.json"), Limit: 2200},
		{Title: "风格规则", Rel: filepath.Join("meta", "style_rules.json"), Limit: 2200},
		{Title: "作者声口与对白规避规则", Rel: filepath.Join("meta", "author_voice_profile.md"), Limit: 2600},
	}
	ledgerFiles := []contextFile{
		{Title: "章节推进台账", Rel: filepath.Join("meta", "chapter_progress.md"), Limit: 6500},
		{Title: "前100章动态推演台账", Rel: filepath.Join("meta", "first_100_projection.md"), Limit: 5200},
		{Title: "资源账本", Rel: filepath.Join("meta", "resource_ledger.md"), Limit: 4200},
		{Title: "写法资产库", Rel: filepath.Join("meta", "writing_assets.md"), Limit: 4200},
		{Title: "项目进度", Rel: filepath.Join("meta", "project_progress.md"), Limit: 3200},
	}
	var parts []string
	appendFiles := func(files []contextFile) {
		for _, file := range files {
			body, err := os.ReadFile(filepath.Join(projectDir, file.Rel))
			if err != nil || strings.TrimSpace(string(body)) == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("### %s（%s）\n%s", file.Title, filepath.ToSlash(file.Rel), truncateForContext(string(body), file.Limit)))
		}
	}
	appendFiles(coreFiles)
	parts = append(parts, dynamicProjectContext(projectDir, chapter)...)
	appendFiles(ledgerFiles)
	if len(parts) == 0 {
		return "（未找到项目设计补充文件；以本章大纲、原文和 novel_context 为准。）"
	}
	return strings.Join(parts, "\n\n")
}

func dynamicProjectContext(projectDir string, chapter int) []string {
	var parts []string
	addRel := func(title, rel string, limit int) {
		body, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
		if err != nil || strings.TrimSpace(string(body)) == "" {
			return
		}
		parts = append(parts, fmt.Sprintf("### %s（%s）\n%s", title, rel, truncateForContext(string(body), limit)))
	}

	for _, rel := range summaryContextFiles(projectDir, chapter) {
		addRel("章节摘要", rel, 2500)
	}
	if rel := latestFileRel(projectDir, "meta/sampling", ".json"); rel != "" {
		addRel("sampling 采样记录", rel, 2500)
	}
	if rel := latestFileRel(projectDir, "meta/snapshots", ".json"); rel != "" {
		addRel("snapshot 角色/弧线快照", rel, 4500)
	}
	if rel := latestFileRel(projectDir, "summaries", ".json"); rel != "" && !containsString(summaryContextFiles(projectDir, chapter), rel) {
		addRel("最新章节摘要", rel, 2500)
	}
	return parts
}

func summaryContextFiles(projectDir string, chapter int) []string {
	var rels []string
	add := func(ch int) {
		if ch <= 0 {
			return
		}
		rel := fmt.Sprintf("summaries/%02d.json", ch)
		if _, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(rel))); err == nil {
			rels = appendUnique(rels, rel)
		}
	}
	add(chapter - 1)
	add(chapter)
	add(chapter + 1)
	for _, rel := range []string{"summaries/arc-v01a01.json", "summaries/arc-v01a02.json"} {
		if _, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(rel))); err == nil {
			rels = appendUnique(rels, rel)
		}
	}
	return rels
}

func latestFileRel(projectDir, relDir, ext string) string {
	dir := filepath.Join(projectDir, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || (ext != "" && filepath.Ext(entry.Name()) != ext) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return filepath.ToSlash(filepath.Join(relDir, names[len(names)-1]))
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func preserveAnchorContext(original string) string {
	anchors := preserveAnchors(original)
	if len(anchors) == 0 {
		return "- 无自动锚点；仍须保持原文骨架和声口。"
	}
	var b strings.Builder
	b.WriteString("以下原文段落视为高质锚点，默认原样保留；不要为了降 AI 味改写成说明文：\n")
	for _, anchor := range anchors {
		fmt.Fprintf(&b, "- %s\n", truncateForContext(anchor, 900))
	}
	return b.String()
}

func preserveAnchors(original string) []string {
	paragraphs := splitTextParagraphs(original)
	var anchors []string
	for _, para := range paragraphs {
		if anchor := protectedRuleRhymeAnchor(para); anchor != "" {
			anchors = append(anchors, anchor)
			if len(anchors) >= 6 {
				break
			}
			continue
		}
		score := 0
		if strings.Contains(para, "儿歌") || strings.Contains(para, "童谣") || strings.Contains(para, "唱") {
			score += 3
		}
		if strings.Count(para, "；") >= 3 {
			score += 2
		}
		if strings.Count(para, "“") > 0 && len([]rune(para)) >= 30 {
			score++
		}
		for _, marker := range []string{"门认名", "名字来", "账也来", "1704快开", "别念了", "哭腔很低"} {
			if strings.Contains(para, marker) {
				score += 2
			}
		}
		if score >= 3 {
			anchors = append(anchors, para)
			if len(anchors) >= 6 {
				break
			}
		}
	}
	return anchors
}

func protectedRuleRhymeAnchor(paragraph string) string {
	start := strings.Index(paragraph, "门开门开")
	if start < 0 {
		return ""
	}
	endMarker := "账认门，门认人"
	end := strings.Index(paragraph[start:], endMarker)
	if end < 0 {
		return ""
	}
	end = start + end + len(endMarker)
	return paragraph[start:end]
}

func splitTextParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var paragraphs []string
	for _, part := range strings.Split(text, "\n\n") {
		part = strings.TrimSpace(part)
		if part != "" {
			paragraphs = append(paragraphs, part)
		}
	}
	if len(paragraphs) > 0 {
		return paragraphs
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	return paragraphs
}

func rewriteWordCount(text string) int {
	return domain.WordCount(text)
}

func rewritePatchBoundsFor(projectDir, original string) rewritePatchBounds {
	origCount := rewriteWordCount(original)
	if origCount < 1 {
		origCount = 1
	}
	maxCount := (origCount*(100+rewriteMaxNetGrowthPercent) + 99) / 100
	minCount := rewriteMinFinalWords
	if maxCount < minCount {
		maxCount = minCount
	}
	bounds := rewritePatchBounds{
		Original: origCount,
		Min:      minCount,
		Max:      maxCount,
		Source:   fmt.Sprintf("rewrite 改写窗口：最终不少于%d字，净增不超过%d%%，删除不设比例限制", rewriteMinFinalWords, rewriteMaxNetGrowthPercent),
	}

	var snap rules.Snapshot
	if projectDir != "" && readJSONIfExists(filepath.Join(projectDir, "meta", "user_rules.json"), &snap) && snap.Structured.ChapterWords != nil {
		wr := snap.Structured.ChapterWords
		if wr.Min > 0 && wr.Max > 0 && wr.Min <= wr.Max {
			bounds.ProjectMin = wr.Min
			bounds.ProjectMax = wr.Max
			bounds.Source = fmt.Sprintf("rewrite 改写窗口：最终不少于%d字，净增不超过%d%%；user_rules.chapter_words=%d-%d 为硬边界，写回上限取两者更严格者", rewriteMinFinalWords, rewriteMaxNetGrowthPercent, wr.Min, wr.Max)
			if wr.Min > bounds.Min {
				bounds.Min = wr.Min
			}
			if wr.Max < bounds.Max {
				bounds.Max = wr.Max
			}
			if bounds.Min > bounds.Max {
				bounds.Max = bounds.Min
			}
		}
	}
	return bounds
}

func rewriteLengthInstruction(bounds rewritePatchBounds, retryFeedback string) string {
	if bounds.Original <= 0 {
		return "- 未能统计原文字数；仍须保持原文字数量级，只做局部修订。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- 原文可统计字数：%d\n", bounds.Original)
	fmt.Fprintf(&b, "- 允许写回区间：%d-%d 字\n", bounds.Min, bounds.Max)
	fmt.Fprintf(&b, "- Rewrite 硬规则：最终整体文章字数不少于 %d；相对原文净增不超过 %d%%；删除不限制，但不能删断因果、声口、伏笔和章末钩子。\n", rewriteMinFinalWords, rewriteMaxNetGrowthPercent)
	if bounds.ProjectMin > 0 || bounds.ProjectMax > 0 {
		fmt.Fprintf(&b, "- 本书章节字数规则：%d-%d 字；本轮 rewrite 按项目章节规则与 15%% 净增上限的更严格口径校验，超过项目上限会被拒绝写回。\n", bounds.ProjectMin, bounds.ProjectMax)
	}
	fmt.Fprintf(&b, "- 窗口来源：%s\n", bounds.Source)
	b.WriteString("- 如果需要补对白、交易动作或钩子，必须同步删掉重复说明、空短句、等距物件响应和无功能比喻；净增超过15%或低于2300都会被 pipeline 拒绝写回。\n")
	if bounds.Original > bounds.Max {
		over := bounds.Original - bounds.Max
		targetMax := rewriteRetryTargetMax(bounds)
		fmt.Fprintf(&b, "- 当前原文已超过上限 %d 字，本轮属于压缩型返工：必须净删至少 %d 字，建议成稿不超过 %d 字。\n", over, over+120, targetMax)
		b.WriteString("- 修复红旗时以替换现有段落为主，不新增完整场景；开场压力、人物选择和章末钩子优先在现有段落内完成。\n")
		b.WriteString("- 优先删除：重复灯光/屏幕/纸页/手机响应、流程复盘、同义迟疑动作、无功能比喻、可合并的单句短段。\n")
	}
	if feedback := strings.TrimSpace(retryFeedback); feedback != "" {
		fmt.Fprintf(&b, "- 上一版写回失败：%s\n", feedback)
		b.WriteString("- 这次优先压缩：保留红旗修复和章末钩子，删解释性复盘、重复比喻、同义短句和无信息动作。\n")
	}
	return strings.TrimSpace(b.String())
}

func rewriteLengthRetryFeedback(err error, bounds rewritePatchBounds) string {
	targetMax := rewriteRetryTargetMax(bounds)
	return fmt.Sprintf("%s；请把正文压回 %d-%d 字，内部目标不超过 %d 字，最终绝对不能超过 %d 字。只保留必要红旗修复。", sanitizeRewritePromptText(fmt.Sprint(err)), bounds.Min, bounds.Max, targetMax, bounds.Max)
}

func rewriteModelRetryFeedback(err error, bounds rewritePatchBounds) string {
	targetMax := rewriteRetryTargetMax(bounds)
	return fmt.Sprintf("上一轮模型调用失败：%s。请重新输出完整章节，遵守 %d-%d 字窗口，内部目标不超过 %d 字。", sanitizeRewritePromptText(fmt.Sprint(err)), bounds.Min, bounds.Max, targetMax)
}

func rewritePreflightRetryFeedback(err error, bounds rewritePatchBounds) string {
	targetMax := rewriteRetryTargetMax(bounds)
	return fmt.Sprintf("上一版写回前门禁失败：%s。请直接删改命中词和机械节奏，遵守 %d-%d 字窗口，内部目标不超过 %d 字；普通说明和动作并段，单句自然段最多保留 4 处。", sanitizeRewritePromptText(fmt.Sprint(err)), bounds.Min, bounds.Max, targetMax)
}

func sanitizeRewritePromptText(s string) string {
	return strings.ToValidUTF8(s, "�")
}

func rewriteRetryTargetMax(bounds rewritePatchBounds) int {
	if bounds.Max <= bounds.Min {
		return bounds.Max
	}
	margin := (bounds.Max - bounds.Min) / 5
	if margin < 120 {
		margin = 120
	}
	if margin > 260 {
		margin = 260
	}
	target := bounds.Max - margin
	if target < bounds.Min {
		return bounds.Max
	}
	return target
}

func isPatchRewriteLengthError(err error) bool {
	if v, ok := err.(*patchRewriteValidationError); ok {
		return v.Kind == patchRewriteLengthError
	}
	return false
}

func validatePatchRewriteWithBounds(original, rewritten string, bounds rewritePatchBounds) error {
	original = strings.TrimSpace(original)
	rewritten = strings.TrimSpace(rewritten)
	if original == "" || rewritten == "" {
		return fmt.Errorf("原文或新稿为空")
	}
	origCount := rewriteWordCount(original)
	newCount := rewriteWordCount(rewritten)
	minCount := bounds.Min
	maxCount := bounds.Max
	if minCount < 1 {
		bounds = rewritePatchBoundsFor("", original)
		minCount = bounds.Min
		maxCount = bounds.Max
	}
	if newCount < minCount || newCount > maxCount {
		return &patchRewriteValidationError{
			Kind:    patchRewriteLengthError,
			Message: fmt.Sprintf("新稿字数偏离原文过大：原文=%d，新稿=%d，允许=%d-%d；rewrite 只能局部修订", origCount, newCount, minCount, maxCount),
		}
	}
	// 注：AI 味返工是整章重新渲染（保留剧情不保留原句），不再强制新稿包含原文的
	// 逐字"高质锚点"——原文的句法节奏正是被打回的病因，要求逐字保留会锁死重写。
	// 剧情/设定的保留由 rewrite 系统提示 + 后续一致性对账把关。
	for _, term := range []string{"可识别标的", "可追索标的", "可识别、可追索的标的", "可识别、可追索", "可识别、可追溯"} {
		if !strings.Contains(original, term) && strings.Contains(rewritten, term) {
			return fmt.Errorf("新稿引入审核/审计腔术语 %q；应保留角色口语或项目既有表达", term)
		}
	}
	if !strings.Contains(original, "标的") && strings.Contains(rewritten, "标的") {
		return fmt.Errorf("新稿新增普通正文不应外溢的审计术语 \"标的\"")
	}
	return nil
}

func validateRewritePreflight(st *store.Store, chapter int, rewritten string, includeYellowAIVoice ...bool) error {
	var failures []rules.Violation
	failures = append(failures, toolspkg.SecondAlgorithmProjectContaminationViolations(st, rewritten)...)
	for _, v := range rules.Lint(rewritten) {
		if immediateRewritePreflightFailure(v) {
			failures = append(failures, v)
		}
	}
	if len(includeYellowAIVoice) > 0 && includeYellowAIVoice[0] {
		analysis := editrules.AnalyzeChapter(chapter, rewritten, nil)
		for _, flag := range analysis.RedFlags {
			if flag.Severity != "warning" && flag.Severity != "error" && flag.Severity != "critical" {
				continue
			}
			target := flag.Suggestion
			if flag.Rule == "supporting_dialogue_ratio" {
				need := int((flag.Limit-flag.Actual)*float64(rewriteWordCount(rewritten))) + 40
				if need < 120 {
					need = 120
				}
				target = fmt.Sprintf("对话占比 %.2f 低于 %.2f；至少净增约 %d 字引号内配角主动话轮，并同步删等量说明", flag.Actual, flag.Limit, need)
			}
			failures = append(failures, rules.Violation{
				Rule:     "ai_voice:" + flag.Rule,
				Target:   target,
				Actual:   flag.Actual,
				Limit:    flag.Limit,
				Severity: rules.SeverityWarning,
			})
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("rewrite 写回前门禁未通过：%s", summarizeRewritePreflightViolations(failures))
}

func immediateRewritePreflightFailure(v rules.Violation) bool {
	switch v.Rule {
	case "project_contamination", "deprecated_story_engine":
		return true
	default:
		return reviewreport.IsBlockingMechanicalViolation(v)
	}
}

func summarizeRewritePreflightViolations(vs []rules.Violation) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		item := v.Rule
		if target := strings.TrimSpace(v.Target); target != "" {
			item += "=" + truncateForContext(target, 80)
		}
		if actual := fmt.Sprint(v.Actual); strings.TrimSpace(actual) != "" {
			item += fmt.Sprintf("(actual=%v", v.Actual)
			if v.Limit != nil {
				item += fmt.Sprintf(", limit=%v", v.Limit)
			}
			item += ")"
		}
		parts = append(parts, item)
		if len(parts) >= 8 {
			break
		}
	}
	return strings.Join(parts, "；")
}

var rewriteSentenceBoundaryRe = regexp.MustCompile(`[。！？!?]+`)

func normalizeRewriteParagraphingForGate(text string) (string, bool) {
	paragraphs := splitRewriteParagraphs(text)
	if rewriteIsolatedParagraphCount(paragraphs) <= 4 {
		return strings.TrimSpace(text), false
	}
	changed := false
	for pass := 0; pass < 4 && rewriteIsolatedParagraphCount(paragraphs) > 4; pass++ {
		merged := false
		for i := 0; i < len(paragraphs) && rewriteIsolatedParagraphCount(paragraphs) > 4; i++ {
			if !rewriteShouldMergeIsolatedParagraph(i, paragraphs[i]) {
				continue
			}
			if i > 0 && !rewriteIsPlainChapterTitle(paragraphs[i-1]) && rewriteParagraphRuneCount(paragraphs[i-1])+rewriteParagraphRuneCount(paragraphs[i]) <= 280 {
				paragraphs[i-1] = joinRewriteParagraphs(paragraphs[i-1], paragraphs[i])
				paragraphs = append(paragraphs[:i], paragraphs[i+1:]...)
				changed = true
				merged = true
				i--
				continue
			}
			if i+1 < len(paragraphs) && rewriteParagraphRuneCount(paragraphs[i])+rewriteParagraphRuneCount(paragraphs[i+1]) <= 280 {
				paragraphs[i+1] = joinRewriteParagraphs(paragraphs[i], paragraphs[i+1])
				paragraphs = append(paragraphs[:i], paragraphs[i+1:]...)
				changed = true
				merged = true
				i--
			}
		}
		if !merged {
			break
		}
	}
	if !changed {
		return strings.TrimSpace(text), false
	}
	return strings.Join(paragraphs, "\n\n"), true
}

func splitRewriteParagraphs(text string) []string {
	var paragraphs []string
	var buf []string
	flush := func() {
		p := strings.TrimSpace(strings.Join(buf, "\n"))
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
		buf = nil
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		buf = append(buf, strings.TrimSpace(line))
	}
	flush()
	return paragraphs
}

func rewriteIsolatedParagraphCount(paragraphs []string) int {
	count := 0
	for i, paragraph := range paragraphs {
		if rewriteShouldMergeIsolatedParagraph(i, paragraph) {
			count++
		}
	}
	return count
}

func rewriteShouldMergeIsolatedParagraph(index int, paragraph string) bool {
	p := strings.TrimSpace(paragraph)
	if p == "" || (index == 0 && rewriteIsPlainChapterTitle(p)) || rewriteIsDialogueOnlyParagraph(p) {
		return false
	}
	return rewriteParagraphSentenceCount(p) == 1
}

func rewriteParagraphSentenceCount(paragraph string) int {
	count := 0
	for _, part := range rewriteSentenceBoundaryRe.Split(paragraph, -1) {
		if countCJKRunes(part) > 0 {
			count++
		}
	}
	return count
}

func rewriteIsPlainChapterTitle(p string) bool {
	p = strings.TrimSpace(p)
	if countCJKRunes(p) > 24 {
		return false
	}
	return (strings.HasPrefix(p, "第") && strings.Contains(p, "章")) ||
		(strings.HasPrefix(p, "Chapter ") && len([]rune(p)) <= 24)
}

func rewriteIsDialogueOnlyParagraph(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || !(strings.HasPrefix(p, "“") || strings.HasPrefix(p, "\"") || strings.HasPrefix(p, "「")) {
		return false
	}
	return strings.HasSuffix(p, "”") || strings.HasSuffix(p, "\"") || strings.HasSuffix(p, "」")
}

func joinRewriteParagraphs(a, b string) string {
	return strings.TrimSpace(a) + strings.TrimSpace(b)
}

func rewriteParagraphRuneCount(p string) int {
	return len([]rune(strings.TrimSpace(p)))
}

func countCJKRunes(s string) int {
	count := 0
	for _, r := range s {
		if r >= '\u4e00' && r <= '\u9fff' {
			count++
		}
	}
	return count
}

func writerNovelContext(projectDir string, refs toolspkg.References, style string, chapter int) string {
	st := store.NewStore(projectDir)
	tool := toolspkg.NewContextTool(st, refs, style)
	payload := []byte(fmt.Sprintf(`{"chapter":%d}`, chapter))
	raw, err := tool.Execute(context.Background(), payload)
	if err != nil || len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func projectContinuityContext(projectDir string) string {
	type contextFile struct {
		Title string
		Rel   string
		Limit int
	}
	files := []contextFile{
		{Title: "时间线", Rel: "timeline.md", Limit: 5000},
		{Title: "世界规则", Rel: "world_rules.md", Limit: 3000},
		{Title: "章节推进与故事线", Rel: filepath.Join("meta", "chapter_progress.md"), Limit: 6500},
		{Title: "人物连续性", Rel: filepath.Join("meta", "character_continuity.md"), Limit: 4500},
		{Title: "伏笔账本", Rel: "foreshadow_ledger.md", Limit: 3500},
		{Title: "资源账本", Rel: filepath.Join("meta", "resource_ledger.md"), Limit: 3500},
		{Title: "用户规则", Rel: filepath.Join("meta", "user_rules.json"), Limit: 2500},
		{Title: "故事罗盘", Rel: filepath.Join("meta", "compass.json"), Limit: 2500},
	}

	var parts []string
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join(projectDir, file.Rel))
		if err != nil || strings.TrimSpace(string(body)) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("### %s（%s）\n%s", file.Title, filepath.ToSlash(file.Rel), truncateForContext(string(body), file.Limit)))
	}
	if len(parts) == 0 {
		return "（未找到额外连续性资料；仅使用故事前提、本章大纲、原文和审核建议。）"
	}
	return strings.Join(parts, "\n\n")
}

// printRewriteUsage 主动打印 --rewrite-existing 子命令 usage。
func printRewriteUsage(w *os.File) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: novel-studio --rewrite-existing [--budget 180s] [--from N] [--to N] [--role writer] [--brief-only]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "示例:")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages rewrite                       # 改写全部 chapters/*.md")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages rewrite --from 1 --to 5       # 改写 1-5 章")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages rewrite --budget 120s         # 缩短每章预算")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages rewrite --polish-warnings     # 黄旗也按质量优先原则择优打磨")
	fmt.Fprintln(w, "  novel-studio --rewrite-existing --from 1 --to 1 --brief-only   # 只刷新 rewrite brief，不改正文")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "前提: 必须先 --pipeline --stages review 生成 reviews/{N}.md。")
	fmt.Fprintln(w)
}
