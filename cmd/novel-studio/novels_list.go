package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// novelEntry 是 list 命令对一本小说的状态摘要。
type novelEntry struct {
	Name          string // data/runs/<Name>
	NovelDir      string // 实际 output/novel 目录（续跑 --dir 用）
	RunDir        string // data/runs/<Name>
	Stage         string // brainstorm / foundation / zero-init / writing / complete
	Phase         string
	Completed     int
	Total         int
	WordCount     int
	HasBrainstorm bool
	UpdatedAt     string
}

// runNovelsListCommand 实现 `novel-studio list [--dir <runs-root>]`：
// 扫描 data/runs/ 下所有小说，报告推进阶段与进度，供选择续写目标。
func runNovelsListCommand(argv []string) int {
	root := novelsRunsRoot(argv)
	entries := scanNovels(root)
	if len(entries) == 0 {
		fmt.Fprintf(os.Stdout, "在 %s 下未发现小说项目。用 `novel-studio --pipeline --new-novel --prompt \"<想法>\"` 从头脑风暴开始新建。\n", root)
		return 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].UpdatedAt > entries[j].UpdatedAt })
	fmt.Fprintf(os.Stdout, "进行中的小说（%s）：\n\n", root)
	for _, e := range entries {
		pct := 0
		if e.Total > 0 {
			pct = e.Completed * 100 / e.Total
		}
		bs := "无脑暴"
		if e.HasBrainstorm {
			bs = "有脑暴"
		}
		fmt.Fprintf(os.Stdout, "  %-16s  阶段=%-10s  章节 %d/%d (%d%%)  字数=%d  %s  更新=%s\n",
			e.Name, e.Stage, e.Completed, e.Total, pct, e.WordCount, bs, compactTime(e.UpdatedAt))
		if e.Stage == "待生成" {
			promptFile := filepath.Join(e.RunDir, "prompt.md")
			if nonEmptyRegular(promptFile) {
				fmt.Fprintf(os.Stdout, "        生成： novel-studio --pipeline --dir %s --prompt-file %s\n", e.RunDir, promptFile)
			} else {
				fmt.Fprintf(os.Stdout, "        生成： novel-studio --pipeline --new-novel --prompt \"<想法>\"（或据种子写任务书后 --prompt-file）\n")
			}
		} else {
			fmt.Fprintf(os.Stdout, "        续写： novel-studio --pipeline --dir %s\n", e.RunDir)
		}
	}
	return 0
}

func novelsRunsRoot(argv []string) string {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--dir" || argv[i] == "-dir" {
			return argv[i+1]
		}
	}
	return filepath.Join("data", "runs")
}

// scanNovels 扫描 runs 根下每个子目录，定位其 output/novel 并读取进度。
func scanNovels(root string) []novelEntry {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []novelEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		runDir := filepath.Join(root, d.Name())
		novelDir := resolveNovelDir(runDir)
		if novelDir == "" {
			continue
		}
		e := novelEntry{Name: d.Name(), RunDir: runDir, NovelDir: novelDir}
		e.HasBrainstorm = nonEmptyRegular(filepath.Join(runDir, "brainstorm.md")) ||
			nonEmptyRegular(filepath.Join(novelDir, "meta", "brainstorm.md"))
		st := store.NewStore(novelDir)
		if p, perr := st.Progress.Load(); perr == nil && p != nil {
			e.Phase = string(p.Phase)
			e.Completed = len(p.CompletedChapters)
			e.Total = p.TotalChapters
			e.WordCount = p.TotalWordCount
		}
		// Progress 无时间戳字段：用 progress.json 的 mtime 作为"最近更新"。
		if info, serr := os.Stat(filepath.Join(novelDir, "meta", "progress.json")); serr == nil {
			e.UpdatedAt = info.ModTime().Format("2006-01-02T15:04:05")
		}
		e.Stage = deriveStage(novelDir, e)
		out = append(out, e)
	}
	return out
}

// resolveNovelDir 在一个 run 目录里定位实际的 output/novel。
func resolveNovelDir(runDir string) string {
	for _, cand := range []string{
		filepath.Join(runDir, "output", "novel"),
		filepath.Join(runDir, "novel"),
		runDir,
	} {
		if nonEmptyRegular(filepath.Join(cand, "meta", "progress.json")) ||
			nonEmptyRegular(filepath.Join(cand, "premise.md")) ||
			nonEmptyRegular(filepath.Join(runDir, "brainstorm.md")) {
			return cand
		}
	}
	// 待生成项目：只有任务书/种子，产物尚未生成（如清空后等 GPT 重跑）。
	if nonEmptyRegular(filepath.Join(runDir, "prompt.md")) || hasSeedDir(runDir) {
		return filepath.Join(runDir, "output", "novel")
	}
	return ""
}

// hasSeedDir 判断 run 目录下是否有种子材料（legacy_seed_* 或 brainstorm.md）。
func hasSeedDir(runDir string) bool {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "legacy_seed") {
			return true
		}
	}
	return false
}

// deriveStage 从产物推断当前所处的流水线阶段。
func deriveStage(novelDir string, e novelEntry) string {
	if e.Phase == string(domain.PhaseComplete) {
		return "complete"
	}
	if e.Completed > 0 || e.Phase == "writing" {
		return "writing"
	}
	if nonEmptyRegular(filepath.Join(novelDir, "meta", "first_chapter_generation_readiness.json")) {
		return "zero-init"
	}
	if nonEmptyRegular(filepath.Join(novelDir, "premise.md")) {
		return "foundation"
	}
	if e.HasBrainstorm {
		return "brainstorm"
	}
	// 有任务书/种子但无产物：待生成（清空后等 provider 恢复重跑）。
	if nonEmptyRegular(filepath.Join(e.RunDir, "prompt.md")) || hasSeedDir(e.RunDir) {
		return "待生成"
	}
	return "empty"
}

func nonEmptyRegular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func compactTime(s string) string {
	if len(s) >= 16 {
		return strings.Replace(s[:16], "T", " ", 1)
	}
	return s
}

// loadBrainstormMeta 读取一本小说的 brainstorm.md（供 architect 消费的基础文件）。
func loadBrainstormMeta(novelDir, runDir string) string {
	for _, p := range []string{
		filepath.Join(runDir, "brainstorm.md"),
		filepath.Join(novelDir, "meta", "brainstorm.md"),
	} {
		if data, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			return string(data)
		}
	}
	return ""
}

var _ = json.Marshal // reserved for future --json output
