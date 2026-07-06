package main

// --diag：对当前项目（cwd 的 OutputDir）的产物做诊断，终端打印本地 Findings，
// 并写出脱敏报告 meta/diag-export.md。
// 替代原 TUI 的 /diag 命令。复用 runDiag（import 流程末尾用的同一函数）。

import (
	"flag"
	"fmt"
	"os"
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
