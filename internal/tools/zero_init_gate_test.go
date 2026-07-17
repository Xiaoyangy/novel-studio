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

func TestZeroInitReadinessUsesLayeredOutlineAsFreshnessAuthority(t *testing.T) {
	dir := t.TempDir()
	generatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	readinessPath := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	if err := os.MkdirAll(filepath.Dir(readinessPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(
		`{"schema_version":%d,"ready":true,"generated_at":%q}`,
		ZeroInitReadinessSchemaVersion,
		generatedAt.Format(time.RFC3339),
	)
	if err := os.WriteFile(readinessPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"layered_outline.json", "outline.json"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(`[]`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := generatedAt.Add(-10 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "layered_outline.json"), before, before); err != nil {
		t.Fatal(err)
	}
	after := generatedAt.Add(10 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "outline.json"), after, after); err != nil {
		t.Fatal(err)
	}
	if ok, reason := ZeroInitReadinessState(dir); !ok {
		t.Fatalf("derived flat outline invalidated zero-init readiness: %s", reason)
	}
	if err := os.Chtimes(filepath.Join(dir, "layered_outline.json"), after, after); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ZeroInitReadinessState(dir); ok {
		t.Fatal("authored layered outline change did not invalidate zero-init readiness")
	}
}
