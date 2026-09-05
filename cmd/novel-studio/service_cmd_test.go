package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	dashboardassets "github.com/chenhongyang/novel-studio/services/dashboard"
)

// currentDashboardVersion must hash server.py + static/index.html exactly the way
// the Python /api/health stamp does, so a stale instance from another checkout is
// detected by a version mismatch instead of being accepted as current.
func TestCurrentDashboardVersionMatchesPythonStamp(t *testing.T) {
	dir := t.TempDir()
	static := filepath.Join(dir, "static")
	if err := os.MkdirAll(static, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "server.py")
	serverBytes := []byte("print('server')\n")
	indexBytes := []byte("<html>index</html>\n")
	if err := os.WriteFile(script, serverBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "index.html"), indexBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	h := sha256.New()
	h.Write(serverBytes)
	h.Write(indexBytes)
	want := hex.EncodeToString(h.Sum(nil))[:16]

	if got := currentDashboardVersion(script); got != want {
		t.Fatalf("currentDashboardVersion=%q want=%q", got, want)
	}
}

func TestEmbeddedDashboardVersionMatchesLauncher(t *testing.T) {
	script, err := dashboardassets.Materialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := currentDashboardVersion(script), dashboardassets.Version(); got != want {
		t.Fatalf("currentDashboardVersion=%q want=%q", got, want)
	}
}

func TestRunsDirForNovelOutput(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "data", "runs")
	output := filepath.Join(runs, "book", "output", "novel")
	if got := runsDirForNovelOutput(output); got != runs {
		t.Fatalf("runsDirForNovelOutput=%q want=%q", got, runs)
	}
	if got := runsDirForNovelOutput(filepath.Join(t.TempDir(), "elsewhere")); got != "" {
		t.Fatalf("unexpected runs dir %q", got)
	}
}

func TestRecognizesSourceAndEmbeddedDashboardCommands(t *testing.T) {
	for _, command := range []string{
		"python3 /repo/services/dashboard/server.py --port 8765",
		"python3 /home/me/.novel-studio/runtime/dashboard/abc/server.py --port 8765",
	} {
		if !isNovelStudioDashboardCommand(command) {
			t.Fatalf("did not recognize %q", command)
		}
	}
	if isNovelStudioDashboardCommand("python3 /tmp/server.py") {
		t.Fatal("recognized unrelated Python server")
	}
}

// A change to either file must change the version so `service start` replaces the
// running board.
func TestCurrentDashboardVersionChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	static := filepath.Join(dir, "static")
	if err := os.MkdirAll(static, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "server.py")
	index := filepath.Join(static, "index.html")
	if err := os.WriteFile(script, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("index-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	v1 := currentDashboardVersion(script)

	if err := os.WriteFile(index, []byte("index-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v2 := currentDashboardVersion(script); v1 == v2 {
		t.Fatalf("version did not change after editing index.html: %q", v1)
	}
}

// Missing files must yield an empty version so the launcher avoids needless churn
// rather than force-restarting on an uncomputable local hash.
func TestCurrentDashboardVersionEmptyWhenFilesMissing(t *testing.T) {
	if got := currentDashboardVersion(filepath.Join(t.TempDir(), "absent.py")); got != "" {
		t.Fatalf("expected empty version for missing files, got %q", got)
	}
}
