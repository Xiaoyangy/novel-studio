package main

// --simulate：分析 cwd 的 simulate/ 语料，合成仿写画像写到 meta/simulation_profile.json。
// --import-sim <path>：导入此前生成的画像（simulation_profile.v1）。
// 两者替代原 TUI 的 /simulate、/importsim 命令。

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/chenhongyang/novel-studio/internal/host/sim"
)

type simulateFlags struct {
	NoDiag bool
}

func parseSimulateFlags(name string, argv []string) (simulateFlags, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var flags simulateFlags
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法:\n")
		fmt.Fprintf(os.Stderr, "  novel-studio --simulate [--no-diag]                 # 分析 cwd 的 simulate/ 语料，合成仿写画像\n")
		fmt.Fprintf(os.Stderr, "  novel-studio --import-sim [--no-diag] <path>        # 导入此前生成的画像 JSON\n")
		fmt.Fprintf(os.Stderr, "\n选项：\n")
		fs.PrintDefaults()
	}
	fs.BoolVar(&flags.NoDiag, "no-diag", false, "跳过最后的 diag 报告")
	if err := fs.Parse(argv); err != nil {
		return flags, nil, err
	}
	return flags, fs.Args(), nil
}

// hasSimulateFlag / hasImportSimFlag 入口探测。
func hasSimulateFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--simulate" {
			return true
		}
	}
	return false
}

func hasImportSimFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--import-sim" {
			return true
		}
	}
	return false
}

func simulatePipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseSimulateFlags("simulate", []string{"--help"})
		return nil
	}
	flags, extra, err := parseSimulateFlags("simulate", args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--simulate 不接受额外参数：%v", extra)
	}

	eng, cleanup, err := newExistingHost(opts)
	if err != nil {
		return err
	}
	defer cleanup()

	ch, err := eng.Simulate(context.Background())
	if err != nil {
		return fmt.Errorf("启动仿写画像分析失败: %w", err)
	}
	if err := drainSimEvents(ch); err != nil {
		return err
	}
	if !flags.NoDiag {
		if err := runDiag(eng); err != nil {
			return fmt.Errorf("写 diag 失败: %w", err)
		}
	}
	return nil
}

func importSimPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseSimulateFlags("import-sim", []string{"--help"})
		return nil
	}
	flags, extra, err := parseSimulateFlags("import-sim", args)
	if err != nil {
		return err
	}
	if len(extra) != 1 {
		return fmt.Errorf("--import-sim 需要且只接受一个画像文件路径")
	}
	path := extra[0]

	eng, cleanup, err := newExistingHost(opts)
	if err != nil {
		return err
	}
	defer cleanup()

	ch, err := eng.ImportSimulationProfile(context.Background(), path)
	if err != nil {
		return fmt.Errorf("启动画像导入失败: %w", err)
	}
	if err := drainSimEvents(ch); err != nil {
		return err
	}
	if !flags.NoDiag {
		if err := runDiag(eng); err != nil {
			return fmt.Errorf("写 diag 失败: %w", err)
		}
	}
	return nil
}

// drainSimEvents 把仿写画像事件打到 stderr，遇 StageError 返回其错误。
func drainSimEvents(ch <-chan sim.Event) error {
	for ev := range ch {
		if ev.Stage == sim.StageError {
			if ev.Err != nil {
				return fmt.Errorf("%s: %w", ev.Message, ev.Err)
			}
			return fmt.Errorf("%s", ev.Message)
		}
		tag := string(ev.Stage)
		if ev.Total > 0 {
			tag = fmt.Sprintf("%s %d/%d", ev.Stage, ev.Current, ev.Total)
		}
		fmt.Fprintf(os.Stderr, "[sim] [%s] %s\n", tag, ev.Message)
	}
	return nil
}
