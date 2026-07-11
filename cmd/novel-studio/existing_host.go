package main

// 几个「对已有项目操作」的子命令（--diag / --simulate / --import-sim / --steer）
// 共享同一套 host 装配：检查配置 → 加载 → 起 host → 接日志。抽到这里避免重复。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

// loadCfgBundle 检查配置并加载 cfg + 资源包。多个子命令在起 host / 走 headless
// 之前都需要这两样，抽出来共用。
func loadCfgBundle(opts cliOptions) (bootstrap.Config, assets.Bundle, error) {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return bootstrap.Config{}, assets.Bundle{}, fmt.Errorf("尚未配置，请先在交互终端运行一次 novel-studio 完成配置引导，或手写配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return bootstrap.Config{}, assets.Bundle{}, fmt.Errorf("加载配置失败: %w", err)
	}
	// host.New 内部也会 FillDefaults（幂等），这里先填一遍让 cfg.OutputDir 等默认值
	// 对直接读 cfg 的调用方（如 --pipeline 解析 statePath）可见。
	// --dir 指定项目根时以它为基准解析相对 OutputDir，不再要求 cwd 必须是项目目录。
	if err := normalizeOutputAndRAGForInvocation(&cfg, opts.Dir, hasConfiguredRAGQdrantCollection(opts)); err != nil {
		return bootstrap.Config{}, assets.Bundle{}, err
	}
	rules.EnsureHomeRulesDir()
	// prompt 覆盖链（~/.novel-studio/prompts → ./.novel-studio/prompts）+ 指纹 manifest，
	// 回答"这次 run 用的是哪版 prompt"。
	bundle, provenance := assets.LoadWithOverrides(cfg.Style, assets.DefaultPromptOverrideDirs()...)
	assets.WritePromptManifest(cfg.OutputDir, provenance)
	return cfg, bundle, nil
}

// normalizeOutputAndRAGForInvocation keeps the runtime Qdrant collection tied
// to the final normalized output directory. FillDefaults may derive a
// collection before --dir is applied; leaving that value in place makes
// build-rag write one collection while pipeline/rag-ready query another.
func normalizeOutputAndRAGForInvocation(cfg *bootstrap.Config, baseDir string, collectionConfigured bool) error {
	if err := normalizeOutputDirForInvocation(cfg, baseDir); err != nil {
		return err
	}
	refreshAutoRAGCollectionForOutputDir(cfg, cfg.OutputDir, collectionConfigured)
	return nil
}

// normalizeOutputDirForInvocation turns cfg.OutputDir into an absolute path based
// on the command invocation directory. If the command is accidentally launched
// from output/novel, use that directory directly instead of creating
// output/novel/output/novel.
func normalizeOutputDirForInvocation(cfg *bootstrap.Config, baseDir string) error {
	cfg.FillDefaults()
	if filepath.IsAbs(cfg.OutputDir) {
		cfg.OutputDir = filepath.Clean(cfg.OutputDir)
		return nil
	}

	if strings.TrimSpace(baseDir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("获取当前目录失败: %w", err)
		}
		baseDir = wd
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("解析工作目录失败: %w", err)
	}
	absBase = filepath.Clean(absBase)
	outputRel := filepath.Clean(cfg.OutputDir)

	if collapsed, ok := collapseRepeatedOutputDir(absBase, outputRel); ok {
		cfg.OutputDir = collapsed
		return nil
	}
	if hasPathSuffix(absBase, outputRel) || looksLikeNovelOutputDir(absBase) {
		cfg.OutputDir = absBase
		return nil
	}
	cfg.OutputDir = filepath.Join(absBase, outputRel)
	return nil
}

func looksLikeNovelOutputDir(dir string) bool {
	for _, marker := range []string{
		filepath.Join("meta", "progress.json"),
		filepath.Join("meta", "pipeline.json"),
		"premise.md",
		"layered_outline.json",
		"timeline.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	if isDir(filepath.Join(dir, "chapters")) && isDir(filepath.Join(dir, "meta")) {
		return true
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func collapseRepeatedOutputDir(dir, outputRel string) (string, bool) {
	outputParts := pathParts(outputRel)
	if len(outputParts) == 0 {
		return dir, false
	}
	double := append(append([]string{}, outputParts...), outputParts...)
	collapsed := filepath.Clean(dir)
	changed := false
	for hasPathSuffixParts(collapsed, double) {
		for range outputParts {
			collapsed = filepath.Dir(collapsed)
		}
		changed = true
	}
	return collapsed, changed
}

func hasPathSuffix(path, suffix string) bool {
	return hasPathSuffixParts(path, pathParts(suffix))
}

func hasPathSuffixParts(path string, suffix []string) bool {
	if len(suffix) == 0 {
		return false
	}
	parts := pathParts(path)
	if len(parts) < len(suffix) {
		return false
	}
	parts = parts[len(parts)-len(suffix):]
	for i := range suffix {
		if parts[i] != suffix[i] {
			return false
		}
	}
	return true
}

func pathParts(path string) []string {
	path = filepath.ToSlash(filepath.Clean(path))
	raw := strings.Split(path, "/")
	parts := raw[:0]
	for _, part := range raw {
		if part == "" || part == "." {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

// newExistingHost 按当前配置在 cwd 的 OutputDir 上装配一个 host。
// 返回的 cleanup 同时关闭日志文件与 host，调用方 defer 即可。
func newExistingHost(opts cliOptions) (*host.Host, func(), error) {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return nil, nil, err
	}
	eng, err := host.New(cfg, bundle)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 host: %w", err)
	}
	closeLog := logger.SetupFile(eng.Dir(), "headless.log", false)
	cleanup := func() {
		closeLog()
		eng.Close()
	}
	return eng, cleanup, nil
}
