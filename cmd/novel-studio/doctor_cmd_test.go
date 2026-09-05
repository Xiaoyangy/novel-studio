package main

import (
	"os"
	"path/filepath"
	"testing"

	buildversion "github.com/chenhongyang/novel-studio/internal/version"
)

func TestDoctorWithExplicitValidConfig(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(`{
  "provider": "ollama",
  "model": "qwen3:8b",
  "providers": {"ollama": {"base_url": "http://127.0.0.1:11434/v1"}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := buildDoctorReport(doctorOptions{ConfigPath: config, Dir: dir}, buildversion.Info{Version: "v1.0.0", Commit: "abcdef"})
	if !report.OK {
		t.Fatalf("report=%+v", report)
	}
	assertDoctorStatus(t, report, "config", "pass")
	assertDoctorStatus(t, report, "workspace", "pass")
}

func TestDoctorReportsMissingExplicitConfig(t *testing.T) {
	dir := t.TempDir()
	report := buildDoctorReport(doctorOptions{
		ConfigPath: filepath.Join(dir, "missing.json"),
		Dir:        dir,
	}, buildversion.Info{Version: "dev"})
	if report.OK {
		t.Fatalf("report unexpectedly OK: %+v", report)
	}
	assertDoctorStatus(t, report, "config", "fail")
}

func TestCheckWritableDirectoryRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkWritableDirectory(path); err == nil {
		t.Fatal("expected non-directory error")
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess(1, 25, 4, 1, 25, 5) {
		t.Fatal("1.25.4 should be too old")
	}
	if versionLess(1, 27, 1, 1, 25, 5) {
		t.Fatal("1.27.1 should be supported")
	}
}

func assertDoctorStatus(t *testing.T, report doctorReport, name, want string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != want {
				t.Fatalf("%s status=%q want=%q (%s)", name, check.Status, want, check.Detail)
			}
			return
		}
	}
	t.Fatalf("check %q missing from %+v", name, report.Checks)
}
