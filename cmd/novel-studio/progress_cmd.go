package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func hasRefreshProgressFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--refresh-progress" {
			return true
		}
	}
	return false
}

type refreshProgressFlags struct {
	Dir string
}

func parseRefreshProgressFlags(argv []string) (refreshProgressFlags, []string, error) {
	fs := flag.NewFlagSet("refresh-progress", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --refresh-progress [--dir <output/novel>]\n\n")
		fmt.Fprintf(os.Stderr, "根据已完成章节、摘要、时间线、角色状态、资源账本、大纲和评审指标刷新 meta/chapter_progress.json/md、meta/project_progress.json/md 与 meta/evolution_report.json/md。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f refreshProgressFlags
	fs.StringVar(&f.Dir, "dir", "", "小说输出目录；为空时使用配置中的 OutputDir")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func refreshProgressPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseRefreshProgressFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseRefreshProgressFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--refresh-progress 不接受额外参数：%v", extra)
	}

	dir := flags.Dir
	if dir == "" {
		cfg, _, err := loadCfgBundle(opts)
		if err != nil {
			return err
		}
		dir = cfg.OutputDir
	}
	st := store.NewStore(dir)
	ledger, err := st.RefreshChapterProgressLedger(0, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[refresh-progress] 已刷新 %d 章：%s\n", len(ledger.Entries), filepath.Join(dir, "meta", "chapter_progress.md"))
	fmt.Fprintf(os.Stderr, "[refresh-progress] 已刷新项目仪表盘：%s\n", filepath.Join(dir, "meta", "project_progress.md"))
	fmt.Fprintf(os.Stderr, "[refresh-progress] 已刷新自动进化报告：%s\n", filepath.Join(dir, "meta", "evolution_report.md"))
	fmt.Fprintf(os.Stdout, "%s\n", filepath.Join(dir, "meta", "chapter_progress.md"))
	return nil
}
