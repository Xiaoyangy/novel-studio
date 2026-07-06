package main

// 本文件实现「--import / --review-budget / --diag」三条 CLI 直通入口。
// 目的：让审核 + 优化工作流可以在没有 TTY 的环境（如 CI、远程 agent）里端到端跑通，
// 绕开 expect / Terminal 驱动的脆弱性。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/host/imp"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// hasImportFlag 判断 argv 中是否出现 --import 子流程入口。必须在 parseCLIOptions 之前调用，
// 因为 --import 不是原有 flag，会被原解析器当成 args 丢掉语义。
func hasImportFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--import" {
			return true
		}
	}
	return false
}

// importBudget 控制 import + review 全流程的硬时间预算。
type importBudget struct {
	// Import 阶段：LLM 反推 foundation + 逐章分析；按 5 分钟起步。
	Import time.Duration
	// Review 阶段：委派给 pipeline 的 review/rewrite 阶段；按需。
	Review time.Duration
	// Diag 阶段：纯静态分析，几乎不耗时。
	Diag time.Duration
}

func defaultImportBudget() importBudget {
	return importBudget{
		Import: 10 * time.Minute,
		Review: 8 * time.Minute,
		Diag:   30 * time.Second,
	}
}

// importFlags 通过 flag 包定义的子命令风格参数（与原 parseCLIOptions 的位置参数分离）。
type importFlags struct {
	Import       string        // --import <path>
	ReviewBudget time.Duration // --review-budget 8m
	NoReview     bool          // --no-review（只导入 + diag）
	NoDiag       bool          // --no-diag
}

// parseImportFlags 解析一条「--import」子流程内的所有子选项，剩余参数进 args。
// 用独立的 flag.FlagSet 而不污染主 parseCLIOptions，避免误伤 headless / update 等其他模式。
func parseImportFlags(argv []string) (importFlags, []string, error) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --import <novel.md> [--review-budget 8m] [--no-review] [--no-diag]\n\n")
		fmt.Fprintf(os.Stderr, "走完整 LLM 链路：切分 → 反推 foundation → 逐章分析 → 评审 → diag。\n")
		fmt.Fprintf(os.Stderr, "注意：--import 必须在最前，紧跟文件路径；其它 flag 顺序任意。\n\n选项：\n")
		fs.PrintDefaults()
	}
	f := importFlags{
		ReviewBudget: 8 * time.Minute,
	}
	fs.StringVar(&f.Import, "import", "", "导入外部小说文件路径（.md/.txt）")
	fs.DurationVar(&f.ReviewBudget, "review-budget", f.ReviewBudget, "导入后让 Coordinator 跑评审/改写的硬时间预算")
	fs.BoolVar(&f.NoReview, "no-review", false, "跳过 pipeline review/rewrite，只导入并写 diag")
	fs.BoolVar(&f.NoDiag, "no-diag", false, "跳过最后的 diag 报告")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// printImportUsage 主动打印 --import 子命令 usage。flag.FlagSet 的 Usage
// 只在 Parse 错误时被调用；缺参数走不到那里，所以手动复刻。
func printImportUsage(w *os.File) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: novel-studio --import <novel.md> [--review-budget 8m] [--no-review] [--no-diag]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "示例:")
	fmt.Fprintln(w, "  novel-studio --import ./novel.md")
	fmt.Fprintln(w, "  novel-studio --import ./novel.md --no-review --no-diag")
	fmt.Fprintln(w)
}

// runImportPipeline 是 --import 的端到端编排：导入 → 评审 → 诊断，全程无 TTY。
// 任何阶段失败都不 silent：每个阶段都有进度打印到 stderr，最后写到 meta/diag-export.md。
func runImportPipeline(opts cliOptions, args []string) error {
	// 短路：--import --help / -h / help 直接打 usage，不进 LLM、不写 last-error.log。
	// 注意 Go flag 包对「--import --help」会把 --help 当成 --import 的字符串值，
	// 不会触发 fs.Usage()；hasHelpToken 检测到后主动打 usage。
	if hasHelpToken(args) {
		printImportUsage(os.Stderr)
		return nil
	}
	// 不剥离 --import：flag 包已经把它注册为 fs.StringVar("import", ...)，
	// 自己会消费。剥离反而会让 flag 包看不到、变成 positional。
	flags, extra, err := parseImportFlags(args)
	if err != nil {
		printImportUsage(os.Stderr)
		return err
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "未识别的额外参数：%v\n\n", extra)
		printImportUsage(os.Stderr)
		return fmt.Errorf("--import 不接受额外参数")
	}
	if flags.Import == "" {
		printImportUsage(os.Stderr)
		return errors.New("--import 必须指定文件路径")
	}

	budget := defaultImportBudget()
	if flags.ReviewBudget > 0 {
		budget.Review = flags.ReviewBudget
	}

	// 配置加载与引导态检查。
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("首次启动需要先在交互终端运行一次 novel-studio 完成配置引导（headless / --import 不支持引导）")
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

	fmt.Fprintf(os.Stderr, "[import] 启动 · provider=%s model=%s out=%s\n", cfg.Provider, cfg.ModelName, cfg.OutputDir)

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return fmt.Errorf("初始化 host: %w", err)
	}
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()

	// === 阶段 1: 导入 ===
	importCtx, cancelImport := context.WithTimeout(context.Background(), budget.Import)
	defer cancelImport()
	impEvs, err := eng.ImportFrom(importCtx, imp.Options{SourcePath: flags.Import})
	if err != nil {
		return fmt.Errorf("启动导入失败: %w", err)
	}
	if err := drainImportEvents(impEvs, budget.Import); err != nil {
		return fmt.Errorf("导入失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[import] 导入完成 · 进入 %s 目录查看 chapters/\n", eng.Dir())

	// === 阶段 2: pipeline review/rewrite（可选） ===
	if !flags.NoReview && budget.Review > 0 {
		stageArgs := map[string][]string{
			"review":  {"--budget", budget.Review.String()},
			"rewrite": {"--budget", budget.Review.String()},
		}
		if err := runPipelineAlias(opts, []string{"review", "rewrite"}, "", stageArgs); err != nil {
			// 评审失败不算致命，落到 diag 让用户看。
			fmt.Fprintf(os.Stderr, "[review] 警告：%v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[review] 已跳过（--no-review）\n")
	}

	// === 阶段 3: diag 报告 ===
	if !flags.NoDiag {
		if err := runDiag(eng); err != nil {
			return fmt.Errorf("写 diag 失败: %w", err)
		}
	}
	return nil
}

// drainImportEvents 把导入事件以可读格式打到 stderr；遇 error / 超时立即返回。
func drainImportEvents(ch <-chan imp.Event, budget time.Duration) error {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if ev.Stage == imp.StageError {
				return ev.Err
			}
			tag := fmt.Sprintf("[%s]", ev.Stage)
			if ev.Total > 0 && ev.Stage == imp.StageChapter {
				tag = fmt.Sprintf("[chapter %d/%d]", ev.Current, ev.Total)
			}
			fmt.Fprintf(os.Stderr, "[import] %s %s\n", tag, ev.Message)
		case <-deadline.C:
			return fmt.Errorf("导入超时（%s）", budget)
		}
	}
}

// runDiag 调 diag.Diagnose 打印本地 Findings，再把脱敏导出落盘到
// output/novel/meta/diag-export.md。终端输出是给本机操作者看的；可分享报告仍使用 export。
func runDiag(eng *host.Host) error {
	st := store.NewStore(eng.Dir())
	rep, rc := diag.Diagnose(st)
	if _, err := os.Stderr.Write(diag.RenderCLIReport(rep)); err != nil {
		return err
	}
	path, err := diag.WriteExport(st, rep, rc)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[diag] 报告已写到 %s\n", path)
	return nil
}
