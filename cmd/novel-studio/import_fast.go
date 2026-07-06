package main

// --import-fast：从故事圣经（Markdown）+ 章节原文（Markdown/txt）直接落盘 foundation，
// 跳过 imp.Run 的 LLM 反推（解决 litellm/anthropic 代理返回 schema 漂移 JSON 的痛点）。
//
// 适用场景：
//   1. 已有完整故事圣经 + 完整正文（不需要 AI 反推设定）。
//   2. 之前 --import 失败、希望用本地权威数据继续。
//   3. 想跑评审 / diag，但不想付额外的 foundation 反推 LLM 成本。
//
// 设计哲学：foundation 是「事实」，不是「LLM 推断」。本地手写的事实比远端推断更可靠。
// 对当前工程来说，foundation 的产出物是 *.md + 几个 *.json 文件，全是确定性 IO。

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/host/imp"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// fastFlags --import-fast 子命令参数。
type fastFlags struct {
	Fast         string        // --import-fast <novel.md>
	Bible        string        // --bible <bible.md>  可选；缺省自动读 ./.ainovel/rules/00-故事圣经.md
	ReviewBudget time.Duration // --review-budget（透传）
	NoReview     bool          // --no-review
	NoDiag       bool          // --no-diag
	NovelName    string        // --name 书名（缺省用文件名）
}

func parseFastFlags(argv []string) (fastFlags, []string, error) {
	fs := flag.NewFlagSet("import-fast", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --import-fast <chapter.md> [--bible <bible.md>] [--review-budget 8m] [--no-review] [--no-diag] [--name 书名]\n\n")
		fmt.Fprintf(os.Stderr, "从已写好的章节原文（Markdown / txt）直接落盘 foundation，跳过 LLM 反推。\n")
		fmt.Fprintf(os.Stderr, "必须放 --import-fast 在所有 flag 之前（Go flag 包的限制）；下面参数可以任意顺序。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f fastFlags
	f.ReviewBudget = 8 * time.Minute
	fs.StringVar(&f.Fast, "import-fast", "", "导入章节原文（必填）")
	fs.StringVar(&f.Bible, "bible", "", "故事圣经路径（可选，缺省 .ainovel/rules/00-故事圣经.md）")
	fs.DurationVar(&f.ReviewBudget, "review-budget", f.ReviewBudget, "评审/改写时间预算")
	fs.BoolVar(&f.NoReview, "no-review", false, "跳过自动评审")
	fs.BoolVar(&f.NoDiag, "no-diag", false, "跳过最后 diag")
	fs.StringVar(&f.NovelName, "name", "", "书名（缺省用文件名）")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasFastImportFlag 判断 argv 中是否出现 --import-fast 子流程入口。
func hasFastImportFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--import-fast" {
			return true
		}
	}
	return false
}

// fastImportPipeline --import-fast 的端到端编排：拆章 → 写 foundation → 落盘 → 评审 → diag。
func fastImportPipeline(opts cliOptions, args []string) error {
	// 短路：--import-fast --help / -h / help 直接打 usage，不进 LLM、不写 last-error.log。
	// 注意 Go flag 包对「--import-fast --help」会把 --help 当成 --import-fast 的字符串值，
	// 不会触发 fs.Usage()；hasHelpToken 检测到后主动打 usage。
	if hasHelpToken(args) {
		printFastUsage(os.Stderr)
		return nil
	}
	// 不剥 --import-fast：flag 包已注册 fs.StringVar("import-fast", ...)，
	// 自己会消费；剥离反而让 flag 包看不到、变成 positional。
	flags, extra, err := parseFastFlags(args)
	if err != nil {
		// flag 包已经打到 stderr；补一行 usage 让用户对照。
		printFastUsage(os.Stderr)
		return err
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "未识别的额外参数：%v\n\n", extra)
		printFastUsage(os.Stderr)
		return fmt.Errorf("--import-fast 不接受额外参数")
	}
	if flags.Fast == "" {
		printFastUsage(os.Stderr)
		return fmt.Errorf("--import-fast 必须指定章节原文文件路径")
	}

	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("首次启动需要先在交互终端运行一次 novel-studio 完成配置引导，或手写配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if err := normalizeOutputDirForInvocation(&cfg, ""); err != nil {
		return err
	}
	rules.EnsureHomeRulesDir()
	bundle := assets.Load(cfg.Style)

	fmt.Fprintf(os.Stderr, "[fast-import] 启动 · provider=%s model=%s out=%s\n", cfg.Provider, cfg.ModelName, cfg.OutputDir)

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return fmt.Errorf("初始化 host: %w", err)
	}
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()
	st := store.NewStore(eng.Dir())

	// === 阶段 1: 拆章 ===
	chapFile := flags.Fast
	fmt.Fprintf(os.Stderr, "[fast-import] 拆章: %s\n", chapFile)
	chapters, err := imp.SplitFile(chapFile)
	if err != nil {
		return fmt.Errorf("拆章失败: %w", err)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("未识别到任何章节（首行得是「楔子 / 第N章 / Chapter N」之类）")
	}
	fmt.Fprintf(os.Stderr, "[fast-import] 切分完成：%d 章\n", len(chapters))
	for i, ch := range chapters {
		title := strings.TrimSpace(strings.SplitN(ch.Title, "\n", 2)[0])
		fmt.Fprintf(os.Stderr, "  [%02d] %s\n", i+1, title)
	}

	// === 阶段 2: 写 foundation（不调 LLM，本地确定性落盘） ===
	biblePath := flags.Bible
	if biblePath == "" {
		biblePath = "./.ainovel/rules/00-故事圣经.md"
	}
	if _, err := os.Stat(biblePath); err == nil {
		fmt.Fprintf(os.Stderr, "[fast-import] 读故事圣经: %s\n", biblePath)
	} else {
		fmt.Fprintf(os.Stderr, "[fast-import] 故事圣经未找到（%s），将仅写章节\n", biblePath)
		biblePath = ""
	}

	novelName := flags.NovelName
	if novelName == "" {
		novelName = strings.TrimSuffix(filepath.Base(chapFile), filepath.Ext(chapFile))
	}

	if err := persistFastFoundation(st, novelName, len(chapters), biblePath); err != nil {
		return fmt.Errorf("落盘 foundation 失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[fast-import] foundation 落盘完成（premise / characters / outline / compass）\n")

	// === 阶段 3: 落盘章节 + 标记完成 ===
	if err := persistFastChapters(st, novelName, chapters); err != nil {
		return fmt.Errorf("落盘章节失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[fast-import] 章节落盘完成：%d 章 / 总字数 %d\n", len(chapters), totalWords(st.Progress))

	// === 阶段 4: pipeline review/rewrite（可选） ===
	if !flags.NoReview && flags.ReviewBudget > 0 {
		stageArgs := map[string][]string{
			"review":  {"--budget", flags.ReviewBudget.String()},
			"rewrite": {"--budget", flags.ReviewBudget.String()},
		}
		if err := runPipelineAlias(opts, []string{"review", "rewrite"}, "", stageArgs); err != nil {
			fmt.Fprintf(os.Stderr, "[review] 警告：%v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[review] 已跳过（--no-review）\n")
	}

	// === 阶段 5: diag ===
	if !flags.NoDiag {
		if err := runDiag(eng); err != nil {
			return fmt.Errorf("写 diag 失败: %w", err)
		}
	}
	return nil
}

// persistFastChapters 落盘每章正文 + 标记章节完成 + 推进 phase 到 writing。
func persistFastChapters(st *store.Store, novelName string, chapters []imp.Chapter) error {
	progress := st.Progress
	if err := progress.Init(novelName, len(chapters)); err != nil {
		return fmt.Errorf("progress init: %w", err)
	}
	for i, ch := range chapters {
		chNum := i + 1
		wc := countCJK(ch.Content)
		if err := st.Drafts.SaveFinalChapter(chNum, ch.Content); err != nil {
			return fmt.Errorf("save chapter %d: %w", chNum, err)
		}
		if err := progress.MarkChapterComplete(chNum, wc, "", ""); err != nil {
			return fmt.Errorf("mark chapter %d complete: %w", chNum, err)
		}
	}
	if err := progress.UpdatePhase(domain.PhaseWriting); err != nil {
		return fmt.Errorf("update phase to writing: %w", err)
	}
	return nil
}

// persistFastFoundation 写 foundation 文件。从故事圣经里提取：
//   - premise.md：标题 + 故事核心 + 钩子 + 信息差 + 情绪卖点 + 记忆点
//   - characters.json：主要人物卡（**xxx**：性格/弧光 形式）
//   - world_rules.json：空骨架（无明确规则时不写）
//   - layered_outline.json：一卷结构 + 11 个 ArcEntry
//   - compass.json：终局方向 + 开放线程（从伏笔表 + 信息差推导）
//
// 这个提取器对「中文故事圣经 + 「**人物**：弧光」格式」做了特化。bible 缺位时退化为空结构。
func persistFastFoundation(st *store.Store, novelName string, totalChapters int, biblePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ctx // 留作后续接入 LLM 的占位

	if biblePath == "" {
		return persistFastMinimal(st, novelName, totalChapters)
	}

	body, err := os.ReadFile(biblePath)
	if err != nil {
		return fmt.Errorf("读 bible: %w", err)
	}
	text := string(body)

	// --- premise：从 ## 一、故事核心 起，到 ## 七、人物卡 之前的所有段落 ---
	premise := extractPremise(novelName, text)
	if err := st.Outline.SavePremise(premise); err != nil {
		return fmt.Errorf("save premise: %w", err)
	}

	// --- characters：从 ## 七、人物卡 解析 **名字**：描述 格式 ---
	chars := extractCharacters(text)
	if len(chars) > 0 {
		if err := st.Characters.Save(chars); err != nil {
			return fmt.Errorf("save characters: %w", err)
		}
	}

	// --- layered_outline：从 ## 九、章节情绪曲线 提取 楔(炸)→1 窝囊... 形式 ---
	volumes := extractLayeredOutline(text, totalChapters)
	if len(volumes) > 0 {
		if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
			return fmt.Errorf("save outline: %w", err)
		}
	}

	// --- compass：从 ## 四、核心信息差 / 八、伏笔登记 提取 ---
	compass := extractCompass(text)
	if compass.EndingDirection != "" {
		if err := st.Outline.SaveCompass(compass); err != nil {
			return fmt.Errorf("save compass: %w", err)
		}
	}

	return nil
}

// persistFastMinimal 无 bible 时写最薄一版 foundation，足够 diag 跑起来。
func persistFastMinimal(st *store.Store, novelName string, totalChapters int) error {
	if err := st.Outline.SavePremise(fmt.Sprintf("# %s\n\n故事前提待补全。\n", novelName)); err != nil {
		return err
	}
	if err := st.Characters.Save(nil); err != nil {
		return err
	}
	volumes := []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "主卷",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "主弧", Goal: "推进主剧情",
			Chapters: makeEntries(totalChapters),
		}},
	}}
	return st.Outline.SaveLayeredOutline(volumes)
}

func makeEntries(n int) []domain.OutlineEntry {
	out := make([]domain.OutlineEntry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, domain.OutlineEntry{Chapter: i, Title: fmt.Sprintf("第%d章", i)})
	}
	return out
}

// extractPremise 把故事圣经中从「## 一、故事核心」起到「## 七、主要人物卡」之前的所有内容
// 拼成 premise.md。
func extractPremise(novelName, text string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(novelName)
	b.WriteString("\n\n")
	// 头部信息行（题材/视角/字数/结构）
	if head := extractBibleHeader(text); head != "" {
		b.WriteString(head)
		b.WriteString("\n\n")
	}
	// 一句话钩子
	if hook := extractSectionBody(text, "二、一句话钩子"); hook != "" {
		b.WriteString("## 一句话钩子\n\n> ")
		b.WriteString(strings.TrimSpace(hook))
		b.WriteString("\n\n")
	}
	// 核心
	if core := extractSectionBody(text, "一、故事核心"); core != "" {
		b.WriteString("## 故事核心\n\n")
		b.WriteString(strings.TrimSpace(core))
		b.WriteString("\n\n")
	}
	// 情绪卖点
	if emo := extractSectionBody(text, "三、核心情绪与卖点"); emo != "" {
		b.WriteString("## 核心情绪与卖点\n\n")
		b.WriteString(strings.TrimSpace(emo))
		b.WriteString("\n\n")
	}
	// 信息差
	if gap := extractSectionBody(text, "四、核心信息差"); gap != "" {
		b.WriteString("## 核心信息差（引擎）\n\n")
		b.WriteString(strings.TrimSpace(gap))
		b.WriteString("\n\n")
	}
	// 记忆点
	if mem := extractSectionBody(text, "五、记忆点"); mem != "" {
		b.WriteString("## 记忆点（梗）\n\n")
		b.WriteString(strings.TrimSpace(mem))
		b.WriteString("\n\n")
	}
	return b.String()
}

// extractBibleHeader 抽出 > 题材：… 那一行。
func extractBibleHeader(text string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var head []string
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(ln, "# ") {
			continue
		}
		if strings.HasPrefix(ln, "> ") {
			head = append(head, ln)
			continue
		}
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			break
		}
	}
	return strings.Join(head, "\n")
}

// extractSectionBody 抽出「## 标题」到下一个「## 标题」之间的正文。
func extractSectionBody(text, titlePrefix string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inSection := false
	var body []string
	for sc.Scan() {
		ln := sc.Text()
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "## ") {
			cleaned := strings.TrimPrefix(trim, "## ")
			// 兼容「二、一句话钩子」和「一句话钩子」两种前缀
			if strings.HasSuffix(cleaned, titlePrefix) || cleaned == strings.TrimPrefix(titlePrefix, "一二三四五六七八九十、") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
			continue
		}
		if inSection {
			body = append(body, ln)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// extractCharacters 从「## 七、主要人物卡」段落解析 - **名字**：描述 格式。
func extractCharacters(text string) []domain.Character {
	body := extractSectionBody(text, "主要人物卡")
	if body == "" {
		return nil
	}
	var chars []domain.Character
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(ln, "- **") {
			continue
		}
		rest := strings.TrimPrefix(ln, "- **")
		end := strings.Index(rest, "**")
		if end < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:end])
		desc := strings.TrimSpace(rest[end+2:])
		desc = strings.TrimPrefix(desc, "：")
		desc = strings.TrimPrefix(desc, ":")
		role := "supporting"
		if isProtagonist(name) {
			role = "protagonist"
		} else if isAntagonist(name) {
			role = "antagonist"
		}
		arc := extractArcSentence(desc)
		chars = append(chars, domain.Character{
			Name:        name,
			Role:        role,
			Description: desc,
			Arc:         arc,
			Tier:        "important",
		})
	}
	return chars
}

// isProtagonist / isAntagonist 用启发式判断角色定位。bible 里通常没显式标。
func isProtagonist(name string) bool {
	first := []rune(name)
	if len(first) == 0 {
		return false
	}
	// 男频小说里，主角往往是「陈默」「萧炎」这种两个汉字 + 一个叠词或同字姓氏
	// 这里采用启发式：苏曼（妻子）不算主角，主角是叙述视角的人 = 故事圣经里第一个出现 + 「弧光」最长。
	// 但保险起见，默认所有都 supporting，让 Coordinator 自评。
	return name == "陈默"
}
func isAntagonist(name string) bool {
	return name == "周凯"
}

// extractArcSentence 从描述里抓「弧光：…」这一截。
func extractArcSentence(desc string) string {
	i := strings.Index(desc, "弧光")
	if i < 0 {
		return ""
	}
	rest := desc[i:]
	rest = strings.TrimPrefix(rest, "弧光")
	rest = strings.TrimPrefix(rest, "：")
	rest = strings.TrimPrefix(rest, ":")
	// 截到第一个句号
	if idx := indexAnyCN(rest, "。.!！"); idx >= 0 {
		return strings.TrimSpace(rest[:idx])
	}
	return strings.TrimSpace(rest)
}

func indexAnyCN(s string, chars string) int {
	for i, r := range s {
		for _, c := range chars {
			if r == c {
				return i
			}
		}
	}
	return -1
}

// extractLayeredOutline 从「## 九、章节情绪曲线」抽 楔(炸)→1 窝囊→2 ... 形式。
// 解析为：一卷 + 一弧 + N 章 entries（章名 = 情绪词）。
func extractLayeredOutline(text string, totalChapters int) []domain.VolumeOutline {
	body := extractSectionBody(text, "章节情绪曲线")
	if body == "" {
		return nil
	}
	// 提取 → 两侧的情绪词
	parts := strings.Split(body, "→")
	entries := make([]domain.OutlineEntry, 0, totalChapters)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		title, label := parseEmotionToken(p)
		if title == "" {
			continue
		}
		entries = append(entries, domain.OutlineEntry{
			Chapter:   len(entries) + 1,
			Title:     fmt.Sprintf("%s %s", title, label),
			CoreEvent: label,
		})
	}
	if len(entries) == 0 {
		return nil
	}
	return []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "主卷",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "主弧", Goal: "推进主剧情",
			Chapters: entries,
		}},
	}}
}

// parseEmotionToken 把「楔(炸)」或「1 窝囊」拆成（title, label）。
// 楔子没有数字，所以 chapter 号 = 序号（楔=1, 1=2, 2=3 ...）。
func parseEmotionToken(s string) (title string, label string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	// 楔(炸) / 楔(楔子)
	if strings.HasPrefix(s, "楔") {
		rest := strings.TrimPrefix(s, "楔")
		rest = strings.Trim(rest, "()（）")
		return "楔子", "炸点开场：" + rest
	}
	// 1 窝囊
	idx := 0
	for idx < len(s) && (s[idx] >= '0' && s[idx] <= '9') {
		idx++
	}
	if idx == 0 {
		return "", ""
	}
	num, _ := strconv.Atoi(s[:idx])
	rest := strings.TrimSpace(s[idx:])
	if num == 0 {
		return "", ""
	}
	chapterTitle := fmt.Sprintf("第%d章", num)
	return chapterTitle, rest
}

// extractCompass 从「## 四、核心信息差」+ 「## 八、伏笔与信息差登记表」合成 compass。
func extractCompass(text string) domain.StoryCompass {
	var c domain.StoryCompass
	if gap := extractSectionBody(text, "四、核心信息差"); gap != "" {
		// 第一段当 ending_direction
		lines := strings.SplitN(gap, "\n", 2)
		if len(lines) > 0 {
			c.EndingDirection = strings.TrimSpace(lines[0])
		}
	}
	if table := extractSectionBody(text, "八、伏笔与信息差登记表"); table != "" {
		// 抽 | 线索 | ... | 行第一列
		sc := bufio.NewScanner(strings.NewReader(table))
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			ln := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(ln, "|") {
				continue
			}
			// 跳过表头分隔（|---|---|）
			if strings.Contains(ln, "---") {
				continue
			}
			cells := strings.Split(ln, "|")
			if len(cells) < 2 {
				continue
			}
			thread := strings.TrimSpace(cells[1])
			if thread == "" || thread == "线索" {
				continue
			}
			c.OpenThreads = append(c.OpenThreads, thread)
		}
	}
	if c.EstimatedScale == "" {
		c.EstimatedScale = "短篇/中篇"
	}
	return c
}

// countCJK 统计 CJK + ASCII 词数。
func countCJK(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			n++
		} else if r == '\n' || r == ' ' || r == '\t' {
			continue
		} else {
			n++
		}
	}
	return n
}

func totalWords(p *store.ProgressStore) int {
	prog, _ := p.Load()
	if prog == nil {
		return 0
	}
	return prog.TotalWordCount
}

// 抑制未使用引用告警
var _ = regexp.MustCompile

// printFastUsage 主动打印一次 --import-fast 子命令的 usage。
// flag.FlagSet 的 Usage 只在 Parse 错误时被调用，缺参数这种"业务错误"
// 走不到那里，需要手动复刻一遍。
func printFastUsage(w *os.File) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: novel-studio --import-fast <chapter.md> [--bible <bible.md>] [--review-budget 8m] [--no-review] [--no-diag] [--name 书名]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "示例:")
	fmt.Fprintln(w, "  novel-studio --import-fast ./novel.md")
	fmt.Fprintln(w, "  novel-studio --import-fast ./novel.md --bible ./.ainovel/rules/00-故事圣经.md")
	fmt.Fprintln(w, "  novel-studio --import-fast ./novel.md --no-review --no-diag")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "说明: --import-fast 必须紧跟章节原文路径（值），其它 flag 顺序任意。")
	fmt.Fprintln(w, "      这是 Go flag 包的限制（首个非 flag token 后停止 flag 解析）。")
	fmt.Fprintln(w)
}
