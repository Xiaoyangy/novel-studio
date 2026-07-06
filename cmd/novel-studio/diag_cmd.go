package main

// --diag：对当前项目（cwd 的 OutputDir）的产物做诊断，终端打印本地 Findings，
// 并写出脱敏报告 meta/diag-export.md。
// 替代原 TUI 的 /diag 命令。runDiag 同时被 --simulate / --import-sim 复用。

import (
	"flag"
	"fmt"
	"os"

	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func parseDiagFlags(argv []string) ([]string, error) {
	fs := flag.NewFlagSet("diag", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --diag\n\n")
		fmt.Fprintf(os.Stderr, "诊断当前项目（cwd 的 output 产物），终端打印本地 Findings，并写出脱敏报告 meta/diag-export.md。\n")
	}
	if err := fs.Parse(argv); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// hasDiagFlag 判断 argv 中是否含 --diag 入口。
func hasDiagFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--diag" {
			return true
		}
	}
	return false
}

func diagPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _ = parseDiagFlags([]string{"--help"})
		return nil
	}
	extra, err := parseDiagFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--diag 不接受额外参数：%v", extra)
	}

	eng, cleanup, err := newExistingHost(opts)
	if err != nil {
		return err
	}
	defer cleanup()

	return runDiag(eng)
}

// runDiag 调 diag.Diagnose 打印本地 Findings，再把脱敏导出落盘到
// output/novel/meta/diag-export.md。终端输出是给本机操作者看的。
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
