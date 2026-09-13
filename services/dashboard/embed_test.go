package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var expectedDashboardFiles = []string{
	"server.py", "static/index.html", "static/broadcast.html",
	"static/broadcast.css", "static/broadcast.js",
}

func TestMaterializeDashboard(t *testing.T) {
	base := t.TempDir()
	script, err := Materialize(base)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(script) != "server.py" {
		t.Fatalf("script=%q", script)
	}
	if filepath.Base(filepath.Dir(script)) != Version() {
		t.Fatal("dashboard assets were not materialized under their content version")
	}
	for _, name := range expectedDashboardFiles {
		path := filepath.Join(filepath.Dir(script), filepath.FromSlash(name))
		want, err := bundled.ReadFile(name)
		if err != nil || len(want) == 0 {
			t.Fatalf("missing/empty embedded file %q: %v", name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("materialized file %q differs from embedded bytes: %v", path, err)
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
	// A partial or changed runtime extraction is repaired from the embedded
	// release, never mistaken for a separately versioned set of static files.
	css := filepath.Join(filepath.Dir(script), "static", "broadcast.css")
	if err := os.WriteFile(css, []byte("interrupted extraction"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(base); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(css)
	want, _ := bundled.ReadFile("static/broadcast.css")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("broadcast asset was not repaired during materialization")
	}
}

func TestVersionLooksLikeHealthStamp(t *testing.T) {
	if got := Version(); len(got) != 16 || got == "unavailable" {
		t.Fatalf("Version()=%q", got)
	}
	h := sha256.New()
	for _, name := range expectedDashboardFiles {
		data, err := bundled.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = h.Write(data)
	}
	if want := hex.EncodeToString(h.Sum(nil))[:16]; Version() != want {
		t.Fatal("dashboard version omitted assets or changed the shared hash order")
	}
}

func TestBundledPagesLinkOnlyToPackagedBroadcastAssets(t *testing.T) {
	index, err := bundled.ReadFile("static/index.html")
	if err != nil || !strings.Contains(string(index), `href="/broadcast"`) {
		t.Fatal("existing dashboard has no broadcast entry")
	}
	page, err := bundled.ReadFile("static/broadcast.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{`href="/broadcast.css"`, `src="/broadcast.js"`} {
		if !strings.Contains(string(page), reference) {
			t.Fatalf("broadcast page missing packaged asset reference %s", reference)
		}
	}
}

func TestMaterializedDashboardVersionMatchesPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to compare the dashboard runtime stamp")
	}
	script, err := Materialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// run_path does not execute the __main__ server entry point or bind a port.
	output, err := exec.CommandContext(ctx, python, "-B", "-c", "import runpy,sys; print(runpy.run_path(sys.argv[1])['SERVICE_VERSION'])", script).CombinedOutput()
	if err != nil {
		t.Fatalf("load materialized Python version: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != Version() {
		t.Fatalf("Python version=%q embedded version=%q", got, Version())
	}
}
