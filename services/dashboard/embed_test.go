package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeDashboard(t *testing.T) {
	base := t.TempDir()
	script, err := Materialize(base)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(script) != "server.py" {
		t.Fatalf("script=%q", script)
	}
	for _, path := range []string{script, filepath.Join(filepath.Dir(script), "static", "index.html")} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("materialized file %q: info=%v err=%v", path, info, err)
		}
	}

	// Re-materializing the same version is idempotent.
	again, err := Materialize(base)
	if err != nil {
		t.Fatal(err)
	}
	if again != script {
		t.Fatalf("second path=%q want=%q", again, script)
	}
}

func TestVersionLooksLikeHealthStamp(t *testing.T) {
	if got := Version(); len(got) != 16 || got == "unavailable" {
		t.Fatalf("Version()=%q", got)
	}
}
