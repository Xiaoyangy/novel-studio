package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/eval"
	"github.com/chenhongyang/novel-studio/internal/rules"
	buildversion "github.com/chenhongyang/novel-studio/internal/version"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// headlessMode 记录本次是否 headless 启动，供 die 决定错误退出时是否暂停。
var headlessMode bool

func main() {
	// 子命令在常规 flag 解析之前拦截：eval 是离线评测 harness，参数体系独立。
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(eval.Command(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "service" {
		os.Exit(runServiceCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "skills" {
		os.Exit(runSkillsCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && (os.Args[1] == "list" || os.Args[1] == "novels") {
		os.Exit(runNovelsListCommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "reader-metrics" {
		opts, _, err := parseCLIOptions(nil)
		if err != nil {
			die("flags: %v", err)
		}
		if err := readerMetricsPipeline(opts, os.Args[2:]); err != nil {
			die("reader-metrics: %v", err)
		}
		return
	}

	opts, args, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		die("flags: %v", err)
	}
	// 顶层 --help：无子命令时打印顶层 usage；有子命令 token 时把 --help 留给子命令自己处理。
	if opts.Help && !hasAnySubcommand(args) {
		printTopUsage(os.Stdout)
		return
	}
	if opts.Version {
		buildversion.Print(os.Stdout, versionInfo())
		return
	}
	if opts.Update {
		if err := runSelfUpdate(opts.UpdateVersion); err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		return
	}
	headlessMode = opts.Headless

	// 注意：纯路由 token（不期望值的）需要从 argv 中剥离，否则 Go flag 包会把
	// 后面的 --from/--to/--budget 误当作该 string flag 的值（如 --review-existing
	// 是 fs.StringVar 注册的，下一个 token --from 会被吞成它的字符串值）。
	if hasPipelineFlag(args) {
		headlessMode = true
		if err := pipelinePipeline(opts, stripRoutingTokens(args, "--pipeline")); err != nil {
			die("pipeline: %v", err)
		}
		return
	}
	if hasReviewExistingFlag(args) {
		headlessMode = true
		reviewArgs := stripRoutingTokens(args, "--review-existing")
		if hasHelpToken(reviewArgs) {
			if err := reviewExistingPipeline(opts, reviewArgs); err != nil {
				die("review-existing: %v", err)
			}
			return
		}
		if err := runPipelineAlias(opts, []string{"review"}, "", map[string][]string{"review": reviewArgs}); err != nil {
			die("review-existing: %v", err)
		}
		return
	}
	if hasRewriteExistingFlag(args) {
		headlessMode = true
		rewriteArgs := stripRoutingTokens(args, "--rewrite-existing")
		if hasHelpToken(rewriteArgs) {
			if err := rewriteExistingPipeline(opts, rewriteArgs); err != nil {
				die("rewrite-existing: %v", err)
			}
			return
		}
		if err := runPipelineAlias(opts, []string{"rewrite"}, "", map[string][]string{"rewrite": rewriteArgs}); err != nil {
			die("rewrite-existing: %v", err)
		}
		return
	}
	if hasZeroInitFlag(args) {
		headlessMode = true
		if err := zeroInitPipeline(opts, stripRoutingTokens(args, "--zero-init")); err != nil {
			die("zero-init: %v", err)
		}
		return
	}
	if hasCheckFlag(args) {
		headlessMode = true
		if err := checkPipeline(opts, stripRoutingTokens(args, "--check")); err != nil {
			die("check: %v", err)
		}
		return
	}
	if hasDiagFlag(args) {
		headlessMode = true
		if err := diagPipeline(opts, stripRoutingTokens(args, "--diag")); err != nil {
			die("diag: %v", err)
		}
		return
	}
	if hasWritingAssetsFlag(args) {
		headlessMode = true
		if err := writingAssetsPipeline(opts, stripRoutingTokens(args, "--writing-assets")); err != nil {
			die("writing-assets: %v", err)
		}
		return
	}
	if hasRefreshProgressFlag(args) {
		headlessMode = true
		if err := refreshProgressPipeline(opts, stripRoutingTokens(args, "--refresh-progress")); err != nil {
			die("refresh-progress: %v", err)
		}
		return
	}
	if hasBuildRAGFlag(args) {
		headlessMode = true
		if err := buildRAGPipeline(opts, stripRoutingTokens(args, "--build-rag")); err != nil {
			die("build-rag: %v", err)
		}
		return
	}
	if hasSimulateFlag(args) {
		headlessMode = true
		if err := simulatePipeline(opts, stripRoutingTokens(args, "--simulate")); err != nil {
			die("simulate: %v", err)
		}
		return
	}
	if hasImportSimFlag(args) {
		headlessMode = true
		if err := importSimPipeline(opts, stripRoutingTokens(args, "--import-sim")); err != nil {
			die("import-sim: %v", err)
		}
		return
	}
	if hasSteerFlag(args) {
		headlessMode = true
		if err := steerPipeline(opts, stripRoutingTokens(args, "--steer")); err != nil {
			die("steer: %v", err)
		}
		return
	}
	if hasCocreateFlag(args) {
		headlessMode = true
		if err := cocreatePipeline(opts, stripRoutingTokens(args, "--cocreate")); err != nil {
			die("cocreate: %v", err)
		}
		return
	}

	// 首次引导
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		if opts.Headless {
			die("error: headless 模式不支持首次引导，请先在交互终端运行一次 novel-studio 完成配置，或手写配置文件")
		}
		setupCfg, err := bootstrap.RunSetup()
		if err != nil {
			die("setup: %v", err)
		}
		// 引导完成后使用生成的配置继续
		runWithConfig(setupCfg, opts, args)
		return
	}

	// 加载配置
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		die("config: %v", err)
	}

	runWithConfig(cfg, opts, args)
}

// die 统一处理致命错误退出：打印到 stderr、落盘到 ~/.novel-studio/last-error.log，
// 并在交互式终端（非 headless）下暂停等待回车——双击启动时控制台会随进程退出
// 立即关闭，不暂停的话错误一闪而过，正是 issue #37 里用户无从排查的根因。
func die(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	if path := bootstrap.WriteStartupError(msg); path != "" {
		fmt.Fprintf(os.Stderr, "（详细错误已记录到 %s）\n", path)
	}
	if !headlessMode && stdinIsTerminal() {
		fmt.Fprint(os.Stderr, "\n按回车键退出...")
		_, _ = fmt.Fscanln(os.Stdin)
	}
	os.Exit(1)
}

// stdinIsTerminal 判断标准输入是否连接到终端（字符设备）。双击启动 / 交互式终端
// 为 true；管道、重定向、CI 为 false。零依赖近似，足够区分要不要暂停。
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runWithConfig(_ bootstrap.Config, opts cliOptions, args []string) {
	rules.EnsureHomeRulesDir()

	if len(args) > 0 {
		die("error: 不支持命令行直接传入小说需求，请用 --pipeline --prompt <文本> 或对应子命令")
	}

	if opts.Headless {
		if err := pipelinePipeline(opts, nil); err != nil {
			die("error: %v", err)
		}
		return
	}
	if opts.Prompt != "" || opts.PromptFile != "" {
		die("error: --prompt/--prompt-file 需要配合 --pipeline 使用")
	}
	// 交互式 TUI 已移除：无子命令、非 headless 时打印用法供用户选择具体功能。
	printTopUsage(os.Stdout)
}

type cliOptions struct {
	ConfigPath    string
	Dir           string
	Headless      bool
	Prompt        string
	PromptFile    string
	Version       bool
	Update        bool
	UpdateVersion string
	Help          bool
}

// parseCLIOptions 提取 CLI flag，返回选项和剩余参数。
func parseCLIOptions(argv []string) (cliOptions, []string, error) {
	var opts cliOptions
	var args []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--version", "-v":
			opts.Version = true
		case "version":
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("version 不接受参数")
			}
			opts.Version = true
		case "--help", "-h", "help":
			// 不消费、留在 args 里给 flag 包识别（子命令 --help 触发 fs.Usage）。
			// opts.Help 只用于「无子命令时打顶层 usage」的路由判定。
			opts.Help = true
			args = append(args, argv[i])
		case "update":
			if opts.Update {
				return opts, nil, fmt.Errorf("update 只能指定一次")
			}
			opts.Update = true
			if i+1 < len(argv) {
				if strings.HasPrefix(argv[i+1], "-") {
					return opts, nil, fmt.Errorf("update 只接受一个可选版本参数")
				}
				opts.UpdateVersion = argv[i+1]
				i++
			}
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("update 只接受一个可选版本参数")
			}
		case "--config":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--config 缺少值")
			}
			opts.ConfigPath = argv[i+1]
			i++
		case "--dir":
			// 项目根目录：OutputDir（相对路径时）以它为基准解析，等价于 cd 过去再跑。
			// 子命令（--build-rag/--zero-init/--pipeline 等）由 loadCfgBundle 统一消费。
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--dir 缺少值")
			}
			opts.Dir = argv[i+1]
			i++
		case "--headless":
			opts.Headless = true
		case "--prompt":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt 缺少值")
			}
			opts.Prompt = argv[i+1]
			i++
		case "--prompt-file":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt-file 缺少值")
			}
			opts.PromptFile = argv[i+1]
			i++
		default:
			args = append(args, argv[i])
		}
	}
	if opts.Prompt != "" && opts.PromptFile != "" {
		return opts, nil, fmt.Errorf("--prompt 和 --prompt-file 不能同时使用")
	}
	if opts.Version && (opts.Update || opts.ConfigPath != "" || opts.Dir != "" || opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("version 不能与其他启动参数混用")
	}
	if opts.Update && (opts.ConfigPath != "" || opts.Dir != "" || opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("update 不能与其他启动参数混用")
	}
	return opts, args, nil
}

func versionInfo() buildversion.Info {
	return buildversion.Resolve(buildversion.Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
}

func runSelfUpdate(target string) error {
	info := versionInfo()
	result, err := buildversion.Update(context.Background(), buildversion.UpdateOptions{
		Repo:           "Xiaoyangy/novel-studio",
		BinaryName:     "novel-studio",
		TargetVersion:  target,
		CurrentVersion: info.Version,
	})
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Printf("novel-studio 已是最新版本 %s\n", result.Version)
		return nil
	}
	fmt.Printf("novel-studio 已更新到 %s\n", result.Version)
	fmt.Printf("安装位置：%s\n", result.Path)
	return nil
}

func loadPrompt(opts cliOptions) (string, error) {
	if opts.PromptFile == "" {
		return strings.TrimSpace(opts.Prompt), nil
	}

	var data []byte
	var err error
	if opts.PromptFile == "-" {
		data, err = os.ReadFile("/dev/stdin")
	} else {
		data, err = os.ReadFile(opts.PromptFile)
	}
	if err != nil {
		return "", fmt.Errorf("读取 prompt 失败: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// hasAnySubcommand 判断 argv 里是否含任一子命令入口 token。用于区分
// 「单纯 --help 想要顶层 usage」与「--diag --help 要子命令 usage」。
func hasAnySubcommand(argv []string) bool {
	for _, a := range argv {
		switch a {
		case "service", "skills", "--review-existing", "--rewrite-existing",
			"--check", "--diag", "--simulate", "--import-sim", "--steer",
			"--cocreate", "--pipeline", "--writing-assets", "--refresh-progress", "--build-rag", "--zero-init":
			return true
		}
	}
	return false
}

// hasHelpToken 判断 argv 里是否含 --help/-h/help。各子命令 pipeline 在
// 解析前调用，看到就触发 fs.Usage() 并 return nil，跳过 flag 包的
// 「help requested」错误退出（那种退出还会写 last-error.log + 暂停等回车）。
func hasHelpToken(argv []string) bool {
	for _, a := range argv {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}

// stripRoutingTokens 从 argv 中移除指定的路由 token。用于「纯路由标记」
// （不期望值的 flag）：Go flag 包会把它注册为 StringVar，下一个 flag token
// 会被吞成它的字符串值，导致后续 --from/--to 等全部被当 positional args。
// 在 main 路由到子命令之前先剥离路由 token，再交给 flag 包解析，flag 包
// 就只看到真正想要值的 --from 等。
func stripRoutingTokens(argv []string, tokens ...string) []string {
	skip := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		skip[t] = true
	}
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if skip[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// printTopUsage 打印顶层 usage。覆盖最常见的本地运行场景，避免用户去找 README。
func printTopUsage(w *os.File) {
	fmt.Fprintln(w, "novel-studio — AI 长篇小说创作引擎")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法:")
	fmt.Fprintln(w, "  novel-studio --pipeline --prompt <text>     # 可恢复流水线：写作→评审→重写→交付")
	fmt.Fprintln(w, "  novel-studio --pipeline --prompt-file p.md  # 从文件读 prompt 后进入流水线")
	fmt.Fprintln(w, "  novel-studio --cocreate                     # 多轮对话澄清需求，定稿创作指令")
	fmt.Fprintln(w, "  novel-studio --headless --prompt <text>     # 兼容别名：内部转为 --pipeline")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "功能子命令（无 TTY、CI / 远程可用）:")
	fmt.Fprintln(w, "  novel-studio --check                        # LLM 连通性自检（先确认能用再创作）")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages review     # 逐章 Editor 评审（不改原文）")
	fmt.Fprintln(w, "  novel-studio --pipeline --stages rewrite    # 按评审反馈逐章 Writer 重写")
	fmt.Fprintln(w, "  novel-studio --diag                        # 诊断当前项目产物")
	fmt.Fprintln(w, "  novel-studio --writing-assets list         # 查看/启停/组合/绑定/试写写法资产")
	fmt.Fprintln(w, "  novel-studio --writing-assets seed-defaults # 初始化本书基础写法资产")
	fmt.Fprintln(w, "  novel-studio --refresh-progress [--dir d]  # 回填章节推进/人物变化/下一章计划台账")
	fmt.Fprintln(w, "  novel-studio --build-rag [--dir d]         # 构建本书 RAG 索引并可探测召回")
	fmt.Fprintln(w, "  novel-studio --zero-init [--dir d]         # 新书第一章前的角色/关系/资源推演资产")
	fmt.Fprintln(w, "  novel-studio eval inspect --cases evals/cases/harness # Harness 检查既有项目产物")
	fmt.Fprintln(w, "  novel-studio --simulate [--no-diag]         # 分析 simulate/ 语料合成仿写画像")
	fmt.Fprintln(w, "  novel-studio --import-sim <profile.json>    # 导入此前生成的仿写画像（默认写 diag）")
	fmt.Fprintln(w, "  novel-studio --steer \"<指令>\"               # 排队一条干预，下次启动生效")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "其它:")
	fmt.Fprintln(w, "  novel-studio service start                  # 启动浏览器进度看板（长篇 output/novel + 短篇服务）")
	fmt.Fprintln(w, "  novel-studio service open                   # 手动打开小说项目进度看板")
	fmt.Fprintln(w, "  novel-studio service status                 # 检查看板服务 /api/health")
	fmt.Fprintln(w, "  novel-studio skills list                    # 列出内置 skills")
	fmt.Fprintln(w, "  novel-studio skills export --to <dir>       # 导出 skills 到项目目录")
	fmt.Fprintln(w, "  novel-studio --version                      # 打印版本信息")
	fmt.Fprintln(w, "  novel-studio update [version]               # 自我更新")
	fmt.Fprintln(w, "  novel-studio --config <path>                # 用指定配置文件启动")
	fmt.Fprintln(w, "  novel-studio --dir <project>                # 指定项目根目录（OutputDir 基准），免 cd")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "每个子命令的专属选项：")
	fmt.Fprintln(w, "  novel-studio service --help")
	fmt.Fprintln(w, "  novel-studio --pipeline --help")
	fmt.Fprintln(w, "  novel-studio --review-existing --help      # 兼容别名")
	fmt.Fprintln(w, "  novel-studio --rewrite-existing --help     # 兼容别名")
	fmt.Fprintln(w, "  novel-studio skills --help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "提示:")
	fmt.Fprintln(w, "  · 配置默认读 ~/.novel-studio/config.json（项目内可用 ./.novel-studio/config.json 覆盖）")
	fmt.Fprintln(w, "  · 首次启动会跑 setup 引导：选 Provider / 填 Key / 填 Base URL / 填模型")
	fmt.Fprintln(w, "  · 章节输出在 output/novel/chapters/*.md（可在配置里改 OutputDir）")
	fmt.Fprintln(w)
}
