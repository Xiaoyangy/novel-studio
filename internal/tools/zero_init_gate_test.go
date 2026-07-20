package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestZeroInitReadinessStateRejectsLegacySchema(t *testing.T) {
	dir := t.TempDir()
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
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
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	writeZeroInitTestReadiness(t, dir, time.Now().UTC().Truncate(time.Second))

	if ok, reason := ZeroInitReadinessState(dir); !ok {
		t.Fatalf("current readiness rejected: %s", reason)
	}
}

func TestZeroInitReadinessUsesLayeredOutlineAsFreshnessAuthority(t *testing.T) {
	dir := t.TempDir()
	generatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	layered := `[{"index":1,"title":"第一卷","theme":"开局","arcs":[{"index":1,"title":"第一弧","goal":"立住冲突","chapters":[{"chapter":1,"title":"第一章","core_event":"开局","hook":"后果","scenes":["现场"]}]}]}]`
	if err := os.WriteFile(filepath.Join(dir, "layered_outline.json"), []byte(layered), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := generatedAt.Add(-10 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, zeroInitUserRulesDependency), before, before); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "layered_outline.json"), before, before); err != nil {
		t.Fatal(err)
	}
	after := generatedAt.Add(10 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "outline.json"), after, after); err != nil {
		t.Fatal(err)
	}
	writeZeroInitTestReadiness(t, dir, generatedAt)
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

func TestZeroInitReadinessSkeletonLayeredOutlineDoesNotHideFlatDigest(t *testing.T) {
	dir := t.TempDir()
	generatedAt := time.Now().UTC().Truncate(time.Second)
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	skeleton := `[{"index":1,"title":"第一卷","theme":"开局","arcs":[{"index":1,"title":"第一弧","goal":"待展开","estimated_chapters":12,"chapters":[]}]}]`
	if err := os.WriteFile(filepath.Join(dir, "layered_outline.json"), []byte(skeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	outlinePath := filepath.Join(dir, "outline.json")
	if err := os.WriteFile(outlinePath, []byte(`[{"chapter":1,"title":"A"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	stableAt := generatedAt.Add(-time.Second)
	for _, path := range []string{
		filepath.Join(dir, zeroInitUserRulesDependency),
		filepath.Join(dir, "layered_outline.json"),
		outlinePath,
	} {
		if err := os.Chtimes(path, stableAt, stableAt); err != nil {
			t.Fatal(err)
		}
	}
	writeZeroInitTestReadiness(t, dir, generatedAt)
	if err := os.WriteFile(outlinePath, []byte(`[{"chapter":1,"title":"B"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(outlinePath, stableAt, stableAt); err != nil {
		t.Fatal(err)
	}
	if ok, reason := ZeroInitReadinessState(dir); ok || !strings.Contains(reason, "outline.json 内容摘要") {
		t.Fatalf("ok=%v reason=%q, skeleton layered outline must not mask flat authority drift", ok, reason)
	}
}

func TestZeroInitReadinessExpiresWhenUserRulesChange(t *testing.T) {
	dir := t.TempDir()
	generatedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	userRulesPath := filepath.Join(dir, zeroInitUserRulesDependency)
	initialAt := generatedAt.Add(-10 * time.Second)
	if err := os.Chtimes(userRulesPath, initialAt, initialAt); err != nil {
		t.Fatal(err)
	}
	writeZeroInitTestReadiness(t, dir, generatedAt)
	changedAt := generatedAt.Add(zeroInitFreshnessGrace + time.Second)
	if err := os.Chtimes(userRulesPath, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	ok, reason := ZeroInitReadinessState(dir)
	if ok || !strings.Contains(reason, "meta/user_rules.json") {
		t.Fatalf("ok=%v reason=%q, want user rules freshness rejection", ok, reason)
	}
}

func TestZeroInitReadinessRejectsUserRulesContentChangeWithSameMtime(t *testing.T) {
	dir := t.TempDir()
	generatedAt := time.Now().UTC().Truncate(time.Second)
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	stableAt := generatedAt.Add(-time.Second)
	path := filepath.Join(dir, zeroInitUserRulesDependency)
	if err := os.Chtimes(path, stableAt, stableAt); err != nil {
		t.Fatal(err)
	}
	writeZeroInitTestReadiness(t, dir, generatedAt)

	writeZeroInitTestUserRules(t, dir, 2100, 3300)
	if err := os.Chtimes(path, stableAt, stableAt); err != nil {
		t.Fatal(err)
	}
	ok, reason := ZeroInitReadinessState(dir)
	if ok || !strings.Contains(reason, "内容摘要") {
		t.Fatalf("ok=%v reason=%q, want digest mismatch despite identical mtime", ok, reason)
	}
}

func TestZeroInitReadinessRejectsDeletedUserRules(t *testing.T) {
	dir := t.TempDir()
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	writeZeroInitTestReadiness(t, dir, time.Now().UTC().Truncate(time.Second))
	if err := os.Remove(filepath.Join(dir, zeroInitUserRulesDependency)); err != nil {
		t.Fatal(err)
	}
	ok, reason := ZeroInitReadinessState(dir)
	if ok || !strings.Contains(reason, "不存在") {
		t.Fatalf("ok=%v reason=%q, want deleted user_rules rejection", ok, reason)
	}
}

func TestZeroInitReadinessRejectsInvalidUserRulesChapterWords(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing", body: `{"structured":{"genre":"现实题材"}}`, want: "缺少 structured.chapter_words"},
		{name: "nonpositive", body: `{"structured":{"chapter_words":{"min":0,"max":3300}}}`, want: "chapter_words 非法"},
		{name: "reversed", body: `{"structured":{"chapter_words":{"min":3400,"max":3300}}}`, want: "chapter_words 非法"},
		{name: "malformed", body: `{"structured":{"chapter_words":`, want: "无法解析"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, zeroInitUserRulesDependency)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			writeZeroInitRawReadiness(t, dir, time.Now().UTC().Truncate(time.Second), map[string]string{
				zeroInitUserRulesDependency: "sha256:" + strings.Repeat("0", 64),
			})
			ok, reason := ZeroInitReadinessState(dir)
			if ok || !strings.Contains(reason, tt.want) {
				t.Fatalf("ok=%v reason=%q, want %q", ok, reason, tt.want)
			}
		})
	}
}

func TestZeroInitReadinessRejectsCurrentSchemaWithoutDependencyReceipt(t *testing.T) {
	dir := t.TempDir()
	writeZeroInitTestUserRules(t, dir, 2000, 3300)
	path := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	data := fmt.Sprintf(
		`{"schema_version":%d,"ready":true,"generated_at":%q}`,
		ZeroInitReadinessSchemaVersion,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := ZeroInitReadinessState(dir)
	if ok || !strings.Contains(reason, "foundation_dependencies") {
		t.Fatalf("ok=%v reason=%q, want missing dependency receipt rejection", ok, reason)
	}
}

func writeZeroInitTestUserRules(t *testing.T, dir string, minWords, maxWords int) {
	t.Helper()
	path := filepath.Join(dir, zeroInitUserRulesDependency)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"version":1,"status":"ready","structured":{"chapter_words":{"min":%d,"max":%d}},"preferences":"","sources":["test"],"uncertain":[]}`,
		minWords,
		maxWords,
	)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZeroInitTestReadiness(t *testing.T, dir string, generatedAt time.Time) {
	t.Helper()
	dependencies, err := CaptureZeroInitFoundationDependencies(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeZeroInitRawReadiness(t, dir, generatedAt, dependencies)
}

func writeZeroInitRawReadiness(t *testing.T, dir string, generatedAt time.Time, dependencies map[string]string) {
	t.Helper()
	path := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := struct {
		SchemaVersion          int               `json:"schema_version"`
		Ready                  bool              `json:"ready"`
		GeneratedAt            string            `json:"generated_at"`
		FoundationDependencies map[string]string `json:"foundation_dependencies,omitempty"`
	}{
		SchemaVersion:          ZeroInitReadinessSchemaVersion,
		Ready:                  true,
		GeneratedAt:            generatedAt.Format(time.RFC3339),
		FoundationDependencies: dependencies,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
