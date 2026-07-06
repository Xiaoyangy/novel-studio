package main

// --export：把已完成章节合并导出为 TXT / EPUB。只读操作，写作中途也可随时拿成品。
// 替代原 TUI 的 /export 命令；参数语义保持一致（格式由输出后缀推断）。

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/host/exp"
)

type exportFlags struct {
	Out          string
	From, To     int
	Overwrite    bool
	EvidencePack bool
}

func parseExportFlags(argv []string) (exportFlags, []string, error) {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --export [--out <路径>] [--from N] [--to N] [--overwrite]\n\n")
		fmt.Fprintf(os.Stderr, "合并已完成章节导出。格式由 --out 后缀决定（.txt / .epub），缺省 TXT 写到 {novelDir}/{书名}.txt。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f exportFlags
	fs.StringVar(&f.Out, "out", "", "输出文件路径（后缀 .txt/.epub 决定格式）")
	fs.IntVar(&f.From, "from", 0, "起始章号（含），0 = 第 1 章")
	fs.IntVar(&f.To, "to", 0, "结束章号（含），0 = 最后一章")
	fs.BoolVar(&f.Overwrite, "overwrite", false, "目标文件已存在时覆盖")
	fs.BoolVar(&f.EvidencePack, "evidence-pack", false, "同时打包人工创作过程证据链（reviews/ai_gate/返工diff/外部检测登记/prompt指纹）供平台申诉使用")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasExportFlag 判断 argv 中是否含 --export 入口。
func hasExportFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--export" {
			return true
		}
	}
	return false
}

func exportPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseExportFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseExportFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--export 不接受额外参数：%v", extra)
	}

	eng, cleanup, err := newExistingHost(opts)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := context.Background()
	res, err := eng.Export(ctx, exp.Options{
		OutPath:   flags.Out,
		From:      flags.From,
		To:        flags.To,
		Overwrite: flags.Overwrite,
	})
	if err != nil {
		return fmt.Errorf("导出失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[export] 已写出 %s（%d 章 / %d 字节）\n", res.Path, res.Chapters, res.Bytes)
	if len(res.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "[export] 跳过未完成章节：%v\n", res.Skipped)
	}
	if flags.EvidencePack {
		if err := writeEvidencePack(filepath.Dir(res.Path)); err != nil {
			fmt.Fprintf(os.Stderr, "[export] ⚠ 证据链打包失败（不影响导出）: %v\n", err)
		}
	}

	return nil
}

// writeEvidencePack Task 070：打包"人工创作过程证据链"——对应平台"重证据、一次复审
// 机会"的申诉约束。汇集 reviews/（八维评审+机械门禁 ai_gate）、返工前后对照
// （.pre-rewrite）、外部检测登记、prompt 指纹清单，生成 evidence-pack/README.md 索引。
func writeEvidencePack(outputDir string) error {
	packDir := filepath.Join(outputDir, "evidence-pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# 人工创作过程证据链\n\n本包证明每章经过机械门禁 + 八维 LLM 评审 + 返工闭环，非无审核批量生成。\n\n")
	count := func(glob string) int { m, _ := filepath.Glob(filepath.Join(outputDir, glob)); return len(m) }
	fmt.Fprintf(&b, "- 章级统一审核报告 reviews/NN.md：%d 份\n", count("reviews/[0-9][0-9].md"))
	fmt.Fprintf(&b, "- 机械门禁事实 reviews/NN_ai_gate.json：%d 份（含 aigc blended 分、lexicon_version）\n", count("reviews/*_ai_gate.json"))
	fmt.Fprintf(&b, "- 审核历史归档 reviews/NN.history.jsonl：%d 份（多轮评审可追溯）\n", count("reviews/*.history.jsonl"))
	fmt.Fprintf(&b, "- 返工前后对照 chapters/*.pre-rewrite.md：%d 份\n", count("chapters/*.pre-rewrite*"))
	fmt.Fprintf(&b, "- 外部检测登记 meta/external_detection_log.jsonl：%d 条\n", countLines(filepath.Join(outputDir, "meta", "external_detection_log.jsonl")))
	b.WriteString("- prompt 指纹：meta/prompt_manifest.json（每次运行的提示词来源与 sha256）\n")
	b.WriteString("\n上述文件均在 output 目录原位，本索引仅汇总；申诉时按平台要求截取对应章节的完整链条。\n")
	return os.WriteFile(filepath.Join(packDir, "README.md"), []byte(b.String()), 0o644)
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
