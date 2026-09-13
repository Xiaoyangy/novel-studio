// Package dashboard embeds the zero-dependency Python dashboard so release
// binaries can run `novel-studio service open` without a source checkout.
package dashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

//go:embed server.py static/index.html static/broadcast.html static/broadcast.css static/broadcast.js
var bundled embed.FS

// Keep this order identical to Python _dashboard_version and the CLI launcher.
// One inventory covers both version identity and release materialization.
var bundledFiles = [...]string{
	"server.py", "static/index.html", "static/broadcast.html",
	"static/broadcast.css", "static/broadcast.js",
}

var (
	versionOnce sync.Once
	version     string
)

// Version returns the same content identity exposed by /api/health.
func Version() string {
	versionOnce.Do(func() {
		h := sha256.New()
		for _, name := range bundledFiles {
			data, err := bundled.ReadFile(name)
			if err != nil {
				version = "unavailable"
				return
			}
			_, _ = h.Write(data)
		}
		version = hex.EncodeToString(h.Sum(nil))[:16]
	})
	return version
}

// Materialize writes the embedded dashboard to a versioned runtime directory
// and returns server.py. Writes are atomic so concurrent CLI starts cannot
// leave a partially extracted dashboard behind.
func Materialize(baseDir string) (string, error) {
	if baseDir == "" {
		return "", fmt.Errorf("dashboard runtime directory is unavailable")
	}
	root := filepath.Join(baseDir, Version())
	for _, name := range bundledFiles {
		data, err := bundled.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read embedded dashboard %s: %w", name, err)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := writeAtomicIfChanged(target, data, 0o644); err != nil {
			return "", fmt.Errorf("materialize dashboard %s: %w", name, err)
		}
	}
	return filepath.Join(root, "server.py"), nil
}

func writeAtomicIfChanged(target string, data []byte, mode fs.FileMode) error {
	if current, err := os.ReadFile(target); err == nil && string(current) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".dashboard-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	installed := false
	defer func() {
		_ = tmp.Close()
		if !installed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	installed = true
	return nil
}
