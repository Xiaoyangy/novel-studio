package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// Billing is book history, not disposable generation state. usage.json keeps
// audit offsets and dedupe IDs, so removing its WAL would break recovery (or
// tempt a reset that double-counts previously paid calls). Leave the regular
// audit and lock files byte-for-byte intact; remove only other runtime entries
// in the already archived rebase candidate, without following directory links.
func resetPipelineRebaseRuntimePreservingUsage(outputDir string) error {
	meta := filepath.Join(outputDir, "meta")
	root := filepath.Join(meta, "runtime")
	for _, path := range []string{meta, root} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("rebase runtime cleanup requires a real directory: %s", path)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var remove []string
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.Name() == filepath.Base(store.UsageAuditPath) || entry.Name() == "usage_audit.lock" {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("rebase usage audit must be a regular file: %s", path)
			}
			continue
		}
		remove = append(remove, path)
	}
	for _, path := range remove {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("rebase runtime cleanup: %w", err)
		}
	}
	return nil
}
