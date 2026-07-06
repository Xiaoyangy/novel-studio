package rules

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawSource 是一个待归一化的原始来源（rules 文件的整段文本）。
//
// 砍 YAML 后，rules 文件就是普通自然语言提示词；归一化只需要原文，不再做 front matter 解析。
type RawSource struct {
	Label string     // 来源标签，进入 Snapshot.Sources（如 global:my-style.md）
	Kind  SourceKind // 优先级层级
	Text  string     // 文件原始内容
}

// RawFileSources 按 Global → Project 顺序枚举 rules 目录下的 .md 文件并返回原始文本。
//
// 与 readDirFromDisk 同样的扫描约定（顶层 .md、字典序、跳过隐藏文件），但不解析 YAML，
// 整段文本原样交给归一化器。System defaults / 启动 prompt / 运行中要求由 service 另行提供。
func RawFileSources(opts LoadOptions) []RawSource {
	var out []RawSource
	out = append(out, rawDir(opts.HomeRulesDir, SourceGlobal)...)
	out = append(out, rawDir(opts.ProjectRulesDir, SourceProject)...)
	return out
}

func rawDir(dir string, kind SourceKind) []RawSource {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录不存在是常态，静默跳过；但权限/路径其实是文件这类错误必须留痕——
		// 否则用户写了规则却完全没生效、零反馈，排查成本极高（见 known_rules_path_stale_readme）。
		if !os.IsNotExist(err) {
			slog.Warn("规则目录读取失败，已跳过", "module", "rules", "dir", dir, "err", err)
		}
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []RawSource
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("规则文件读取失败，已跳过", "module", "rules", "file", path, "err", err)
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		out = append(out, RawSource{
			Label: kind.String() + ":" + name,
			Kind:  kind,
			Text:  text,
		})
	}
	return out
}
