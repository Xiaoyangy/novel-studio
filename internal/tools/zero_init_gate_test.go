package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestZeroInitReadinessStateRejectsLegacySchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"ready":true,"generated_at":%q}`, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, reason := ZeroInitReadinessState(dir)
	if ok || !strings.Contains(reason, "schema_version=0") {
		t.Fatalf("ok=%v reason=%q, want legacy schema rejection", ok, reason)
	}
}

func TestZeroInitReadinessStateAcceptsCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(
		`{"schema_version":%d,"ready":true,"generated_at":%q}`,
		ZeroInitReadinessSchemaVersion,
		time.Now().Format(time.RFC3339),
	)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, reason := ZeroInitReadinessState(dir); !ok {
		t.Fatalf("current readiness rejected: %s", reason)
	}
}
