package main

// --review-existing：对现有章节逐章调用 Editor LLM 出评审报告。
// 不走 Coordinator、不触发续写、不改原文。给"只想看诊断意见"的用户一个零破坏的入口。
//
// 输出：
//   output/novel/reviews/{ch:02d}.md   每章一份 markdown 评审
//   output/novel/meta/review-summary.md  汇总（每章 1-2 行评分 + 待修项数）

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	fs.DurationVar(&f.Budget, "budget", f.Budget, "每章 Editor 调用硬时间预算")
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

	// 找所有 chapters/
	chaptersDir := filepath.Join(eng.Dir(), "chapters")
	matches, err := filepath.Glob(filepath.Join(chaptersDir, "[0-9][0-9].md"))
	if err != nil {
		return fmt.Errorf("列章节失败: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("未在 %s 找到任何章节文件（*.md）。请先跑 --pipeline 写作阶段产出章节", chaptersDir)
	}

	// 确定起止
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
	fmt.Fprintf(os.Stderr, "[review-existing] 待评审章节：%d - %d（共 %d 章）\n", start, end, end-start+1)

	reviewsDir := filepath.Join(eng.Dir(), "reviews")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		return err
	}

	// ForRole 对未显式配置的角色回退到默认模型，始终返回可用实例。
	model := eng.Models().ForRole("editor")
	reviewerModel := eng.Models().ForRole("reviewer")
	reviewerProvider, reviewerName, reviewerExplicit := eng.Models().CurrentSelection("reviewer")
	reviewerSelection := deepseekAIJudgeModelSelection{
		Provider: reviewerProvider,
		Model:    reviewerName,
		Explicit: reviewerExplicit,
	}

	summaries := make([]string, 0, end-start+1)
	successCount, failureCount := 0, 0
	for chNum := start; chNum <= end; chNum++ {
		chPath := filepath.Join(chaptersDir, fmt.Sprintf("%02d.md", chNum))
		body, err := os.ReadFile(chPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] 跳过 ch%02d：%v\n", chNum, err)
			failureCount++
			continue
		}
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 评审中（预算 %s）...\n", chNum, flags.Budget)
		history, _ := st.AIVoice.LoadAllChapterMetrics()
		analysis := editrules.AnalyzeChapter(chNum, string(body), history)
		if err := st.AIVoice.SaveChapterMetrics(analysis.Metrics, false); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 指标写入失败：%v\n", chNum, err)
		}
		if err := st.AIVoice.SaveRedFlags(analysis); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d redflags 写入失败：%v\n", chNum, err)
		}
		mechanical, err := saveMechanicalGateForExistingChapter(st, chNum, string(body))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 机械门禁写入失败：%v\n", chNum, err)
		}
		review, err := callEditorOnChapter(model, premise, chNum, string(body), analysis, flags.Budget)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 评审失败：%v\n", chNum, err)
			summaries = append(summaries, fmt.Sprintf("- ch%02d: 评审失败 %v", chNum, err))
			failureCount++
			continue
		}
		entry := structuredReviewFromMarkdown(chNum, review)
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek 裸正文 AI 判定中（reviewer=%s/%s effort=max）...\n", chNum, reviewerProvider, reviewerName)
		deepseekJudge, err := runDeepSeekAIJudge(reviewerModel, reviewerSelection, chNum, string(body), flags.Budget)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek 裸正文 AI 判定失败：%v\n", chNum, err)
			summaries = append(summaries, fmt.Sprintf("- ch%02d: DeepSeek AI 判定失败 %v", chNum, err))
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
		outPath := filepath.Join(reviewsDir, fmt.Sprintf("%02d.md", chNum))
		if err := reviewreport.WriteUnifiedMarkdown(eng.Dir(), reviewreport.UnifiedMarkdownInput{
			Chapter:        chNum,
			Mechanical:     mechanical,
			AIVoice:        &analysis,
			Editor:         &entry,
			EditorMarkdown: review,
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
		if err := st.World.SaveReview(entry); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 结构化评审写入失败：%v\n", chNum, err)
		} else if _, _, err := st.WritingAssets.ApplyReviewFeedback(entry, entry.Verdict, ""); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d 写法历史反馈沉淀失败：%v\n", chNum, err)
		} else if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "review", fmt.Sprintf("reviews/%02d.json", chNum)); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d review checkpoint 写入失败：%v\n", chNum, err)
		} else if err := toolspkg.UpsertRAGChunks(context.Background(), st, ragEmbedder, ragVectorWriter, toolspkg.ReviewRAGChunks(entry, entry.Verdict, entry.AffectedChapters, ""), domain.RAGIndexConfig{}); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d RAG 评审沉淀失败：%v\n", chNum, err)
		} else if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chNum), "deepseek-ai-judge", fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chNum)); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定 checkpoint 写入失败：%v\n", chNum, err)
		} else if err := sedimentDeepSeekAIJudgeRAG(context.Background(), st, ragEmbedder, ragVectorWriter, deepseekJudge); err != nil {
			fmt.Fprintf(os.Stderr, "[review-existing] ch%02d DeepSeek AI 判定 RAG 沉淀失败：%v\n", chNum, err)
		}
		fmt.Fprintf(os.Stderr, "[review-existing] ch%02d → %s\n", chNum, outPath)
		summaryReview := review
		if unified, err := os.ReadFile(outPath); err == nil && strings.TrimSpace(string(unified)) != "" {
			summaryReview = string(unified)
		}
		// 提取统一报告口径作为 summary，避免原始 Editor 可选建议覆盖最终门禁结论。
		summaries = append(summaries, summarizeReview(chNum, summaryReview))
		successCount++
	}

	// 汇总
	summaryPath := filepath.Join(eng.Dir(), "meta", "review-summary.md")
	summary := "# review-summary\n\n" +
		"> 生成时间 " + time.Now().Format("2006-01-02 15:04:05") + "\n\n" +
		strings.Join(summaries, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(summaryPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(summaryPath, []byte(summary), 0o644); err != nil {
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

func saveMechanicalGateForExistingChapter(st *store.Store, chapter int, body string) (*reviewreport.MechanicalGatePayload, error) {
	report := aigc.Analyze(body)
	wordCount := utf8.RuneCountInString(body)
	violations := rules.Lint(body)
	structured := rules.SystemDefaults().Structured
	if snap, err := st.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	violations = append(violations, rules.Check(body, wordCount, structured)...)
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
	payload := reviewreport.MechanicalGatePayload{
		Chapter:        chapter,
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
		return "polish"
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
const editorSystemPrompt = `你是一位资深网文编辑，专门审稿"都市反转爽文 / 男频短篇 / 番茄爆款"。

## 你的任务
阅读用户提交的章节正文，按下面八个维度逐项评审，每项打分 0-5（0 完美，5 严重问题）。
最后给出 **总评 / 是否需要改写 / 主要问题**。

## 八个评审维度
1. **设定一致性**：人物性格 / 已知事实 / 能力边界有没有与前文矛盾？
2. **角色行为**：主角行为是否合乎人设？反派是否脸谱化过头？苏曼视角切得自然吗？
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
- 不要复述章节内容，只指出问题。
- 引用原文必须用「」包裹，标明位置（章节序号 / 段落大意）。
- 不要寒暄、不要解释，直接进入评审。
`

// callEditorOnChapter 调用 Editor 模型出评审 markdown。
func callEditorOnChapter(model agentcore.ChatModel, premise string, chNum int, chapterBody string, analysis domain.AIVoiceAnalysis, budget time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	redFlagJSON, _ := json.MarshalIndent(analysis, "", "  ")
	userMsg := fmt.Sprintf("## 故事前提（供参考）\n%s\n\n## AI 腔 red flag JSON（必须读取）\n```json\n%s\n```\n\n## 本章正文（ch%02d）\n%s",
		truncateForContext(premise, 2000), string(redFlagJSON), chNum, chapterBody)

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
