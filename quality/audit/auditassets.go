package audit

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed README.md references scripts
var auditFS embed.FS

// ReadSupportFile returns a canonical audit support file embedded from
// quality/audit. It is used by skill context materialization without requiring
// the source tree to exist next to the built binary.
func ReadSupportFile(path string) ([]byte, error) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	path = strings.TrimPrefix(path, "quality/audit/")
	path = strings.TrimPrefix(path, "./")
	if path == "" {
		return nil, fmt.Errorf("audit support path is required")
	}
	data, err := auditFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// ExportReviewSupport copies the canonical audit scripts and references into an
// exported review skill. Source files remain owned by quality/audit.
func ExportReviewSupport(dest string) error {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fmt.Errorf("export destination is required")
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	if err := fs.WalkDir(auditFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return writeEmbeddedFile(path, target)
	}); err != nil {
		return err
	}
	return writeExportedReviewContext(root)
}

// ExportTypoScan writes the legacy top-level typo_scan.py support path for
// exported historical prompt packs without keeping a duplicate in source.
func ExportTypoScan(dest string) error {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fmt.Errorf("export destination is required")
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	return writeEmbeddedFile("scripts/typo_scan.py", root)
}

func writeEmbeddedFile(path, target string) error {
	data, err := auditFS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, data, exportMode(path)); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

func writeExportedReviewContext(root string) error {
	manifest := map[string]any{
		"skill":       "review",
		"entrypoint":  "SKILL.md",
		"always_read": []string{"SKILL.md", "CONTEXT.md", "context.json"},
		"required_files": []string{
			"README.md",
			"references/aigc-detection-current-notes.md",
			"references/signals-zh.md",
			"references/signals-en.md",
			"scripts/aigc_value.py",
			"scripts/text_signals.py",
			"scripts/paragraph_dup.py",
			"scripts/content_lint.py",
			"scripts/typo_scan.py",
		},
		"conditional_files": []map[string]any{},
		"state_files":       []string{".skill-context/review.md"},
		"output_contract": []string{
			"审核报告必须包含概率性结论、证据、脚本结果和可靠性说明。",
		},
		"compaction_resume": []string{
			"压缩或恢复后先读 review/CONTEXT.md，再读执行目录中的 .skill-context/review.md（若存在）。",
			"确认已读文件清单、当前阶段、输入/输出路径、硬约束和下一步后再继续。",
			"不要把长正文、完整参考库或多本书设定塞进主会话；这些内容必须落盘并按需读取。",
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(root, "context.json")
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write exported review context: %w", err)
	}
	return nil
}

func exportMode(path string) fs.FileMode {
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".py") {
		return 0o755
	}
	return 0o644
}
