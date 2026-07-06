package main

// --cocreate：共创规划。与 AI 多轮对话澄清需求，逐轮累积出一份"整本书创作指令"草稿，
// 定稿后落盘 meta/cocreate-prompt.txt，可直接 --start 进入创作。替代原 TUI 的"共创规划"。
//
// 与一次性 --headless --prompt 的区别：共创是 stdin 多轮 REPL，AI 主动追问 + 给引导建议，
// 适合"还没想清楚写什么"的冷启动；最终收敛成的草稿等价于一条高质量 --prompt。

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/entry/startup"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/utils"
)

type cocreateFlags struct {
	Start bool
}

func parseCocreateFlags(argv []string) (cocreateFlags, []string, error) {
	fs := flag.NewFlagSet("cocreate", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --cocreate [\"初始想法\"] [--start]\n\n")
		fmt.Fprintf(os.Stderr, "与 AI 多轮对话澄清需求，定稿一份创作指令；--start 定稿后直接进入创作。\n\n")
		fmt.Fprintf(os.Stderr, "对话中可用命令：/done 定稿  /draft 看当前草稿  /quit 放弃\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f cocreateFlags
	fs.BoolVar(&f.Start, "start", false, "定稿后立即进入创作（等价于把草稿喂给 --headless --prompt）")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasCocreateFlag 判断 argv 中是否含 --cocreate 入口。
func hasCocreateFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--cocreate" {
			return true
		}
	}
	return false
}

func cocreatePipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseCocreateFlags([]string{"--help"})
		return nil
	}
	flags, rest, err := parseCocreateFlags(args)
	if err != nil {
		return err
	}
	initial := strings.TrimSpace(strings.Join(rest, " "))

	draft, err := runCoCreate(opts, initial)
	if err != nil {
		return err
	}

	if !flags.Start {
		fmt.Fprintln(os.Stderr, "[cocreate] 未指定 --start；可稍后用 novel-studio --pipeline --prompt-file <上面的文件> 创作")
		return nil
	}

	fmt.Fprintln(os.Stderr, "\n[cocreate] 定稿完成，进入流水线…")
	return runPipelineAlias(opts, defaultPipelineStages, draft, nil)
}

// runCoCreate 跑完整轮共创 REPL，返回定稿的创作指令草稿，并落盘到 meta/cocreate-prompt.txt。
func runCoCreate(opts cliOptions, initial string) (string, error) {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return "", err
	}
	eng, err := host.New(cfg, bundle)
	if err != nil {
		return "", fmt.Errorf("初始化 host: %w", err)
	}
	closeLog := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer closeLog()
	defer eng.Close()

	reader := bufio.NewReader(os.Stdin)
	if initial == "" {
		fmt.Fprintln(os.Stderr, "共创规划：先用一句话说说你想写什么（题材 / 主角 / 大概方向都行）")
		fmt.Fprint(os.Stderr, "❯ ")
		line, _ := reader.ReadString('\n')
		initial = utils.CleanInputLine(line)
		if initial == "" {
			return "", fmt.Errorf("需要一个初始创作想法才能开始共创")
		}
	}
	fmt.Fprintln(os.Stderr, "\n开始共创对话。回复推进澄清；输入 /done 定稿，/draft 看当前草稿，/quit 放弃。")

	session := startup.NewCoCreateSession(initial)
	for {
		fmt.Fprintln(os.Stderr, "\n（AI 思考中…）")
		reply, err := eng.CoCreateStream(context.Background(), session.History(), func(kind, text string) {})
		if err != nil {
			return "", fmt.Errorf("共创对话失败: %w", err)
		}
		session.ApplyReply(reply)
		printCoCreateTurn(reply, session)

		fmt.Fprint(os.Stderr, "\n你 ❯ ")
		line, rerr := reader.ReadString('\n')
		cmd := utils.CleanInputLine(line)
		// EOF（管道结束）：能定稿就定稿，否则放弃。
		if rerr != nil && cmd == "" {
			if session.CanStart() {
				return finalizeCoCreate(eng, session)
			}
			return "", fmt.Errorf("输入结束但草稿尚未成形，已放弃")
		}

		switch cmd {
		case "/quit":
			return "", fmt.Errorf("已放弃共创")
		case "/draft":
			printCoCreateDraft(session)
		case "/done", "":
			if session.CanStart() {
				return finalizeCoCreate(eng, session)
			}
			fmt.Fprintln(os.Stderr, "草稿还没成形，再聊几句把方向说清楚（或继续回复 AI 的问题）。")
		default:
			session.AppendUser(cmd)
		}
	}
}

func finalizeCoCreate(eng *host.Host, session *startup.CoCreateSession) (string, error) {
	draft := strings.TrimSpace(session.DraftPrompt())
	path := filepath.Join(eng.Dir(), "meta", "cocreate-prompt.txt")
	if err := os.WriteFile(path, []byte(draft+"\n"), 0o644); err != nil {
		// 落盘失败不致命：草稿仍可返回给调用方使用。
		fmt.Fprintf(os.Stderr, "[cocreate] 警告：保存草稿失败：%v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "\n[cocreate] 创作指令已定稿，保存到 %s\n", path)
	}
	return draft, nil
}

func printCoCreateTurn(reply host.CoCreateReply, session *startup.CoCreateSession) {
	if msg := strings.TrimSpace(reply.Message); msg != "" {
		fmt.Fprintf(os.Stderr, "\nAI: %s\n", msg)
	}
	if sugg := session.Suggestions(); len(sugg) > 0 {
		fmt.Fprintln(os.Stderr, "\n引导建议（可直接照着回，或自己说）：")
		for i, s := range sugg {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, s)
		}
	}
	printCoCreateDraft(session)
	if session.Ready() {
		fmt.Fprintln(os.Stderr, "\n（AI 认为方向已清晰，可输入 /done 定稿进入创作）")
	}
}

func printCoCreateDraft(session *startup.CoCreateSession) {
	draft := strings.TrimSpace(session.DraftPrompt())
	if draft == "" {
		return
	}
	fmt.Fprintln(os.Stderr, "\n—— 当前创作指令草稿 ——")
	fmt.Fprintln(os.Stderr, draft)
	fmt.Fprintln(os.Stderr, "————————————————————")
}
