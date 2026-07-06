package main

// Task 077：读者数据登记回路（仿外部检测登记模式：人工触发、只登记不抓取）。
// 用法: novel-studio reader-metrics log --chapter 3 --platform fanqie \
//        --read-through 0.42 --comments 3 --note "第3章弃读点疑似在中段"

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type readerMetricRow struct {
	Chapter     int     `json:"chapter"`
	Platform    string  `json:"platform,omitempty"`
	ReadThrough float64 `json:"read_through,omitempty"` // 追读/读完率 [0,1]
	Comments    int     `json:"comments,omitempty"`
	Collect     int     `json:"collect,omitempty"`
	Note        string  `json:"note,omitempty"`
	LoggedAt    string  `json:"logged_at"`
}

func readerMetricsPipeline(opts cliOptions, args []string) error {
	if len(args) == 0 || args[0] != "log" {
		fmt.Fprintln(os.Stderr, "用法: novel-studio reader-metrics log --chapter N [--platform fanqie] [--read-through 0.42] [--comments 3] [--collect 12] [--note ...] [--dir <output/novel>]")
		return fmt.Errorf("reader-metrics 仅支持 log 子命令")
	}
	fs := flag.NewFlagSet("reader-metrics", flag.ContinueOnError)
	var row readerMetricRow
	var dir string
	fs.IntVar(&row.Chapter, "chapter", 0, "章节号（必填）")
	fs.StringVar(&row.Platform, "platform", "", "平台名，如 fanqie/qidian")
	fs.Float64Var(&row.ReadThrough, "read-through", 0, "追读/读完率 0-1")
	fs.IntVar(&row.Comments, "comments", 0, "评论数")
	fs.IntVar(&row.Collect, "collect", 0, "收藏/追更数")
	fs.StringVar(&row.Note, "note", "", "备注（如疑似弃读位置）")
	fs.StringVar(&dir, "dir", "", "小说输出目录；为空时使用配置 OutputDir")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if row.Chapter <= 0 {
		return fmt.Errorf("--chapter 必填且 > 0")
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if dir == "" {
		dir = cfg.OutputDir
	}
	row.LoggedAt = time.Now().Format(time.RFC3339)
	path := filepath.Join(dir, "meta", "reader_metrics.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[reader-metrics] 已登记：ch%d platform=%s read_through=%.2f → %s\n", row.Chapter, row.Platform, row.ReadThrough, path)
	return nil
}
