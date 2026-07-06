package version

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseURL(t *testing.T) {
	cases := map[string]string{
		"":       "https://api.github.com/repos/chenhongyang/novel-studio/releases/latest",
		"latest": "https://api.github.com/repos/chenhongyang/novel-studio/releases/latest",
		"1.2.3":  "https://api.github.com/repos/chenhongyang/novel-studio/releases/tags/v1.2.3",
		"v1.2.3": "https://api.github.com/repos/chenhongyang/novel-studio/releases/tags/v1.2.3",
	}
	for target, want := range cases {
		if got := releaseURL("chenhongyang/novel-studio", target); got != want {
			t.Fatalf("releaseURL(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestSelectAsset(t *testing.T) {
	suffix, err := assetSuffix()
	if err != nil {
		t.Skip(err)
	}
	rel := &release{
		TagName: "v1.2.3",
		Assets: []releaseAsset{
			{Name: "novel-studio_v1.2.3_Windows_x86_64.zip", BrowserDownloadURL: "wrong"},
			{Name: "novel-studio_v1.2.3" + suffix, BrowserDownloadURL: "right"},
		},
	}
	asset, err := selectAsset(rel, "novel-studio")
	if err != nil {
		t.Fatalf("selectAsset: %v", err)
	}
	if asset.BrowserDownloadURL != "right" {
		t.Fatalf("asset = %+v", asset)
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "novel-studio")
	src := filepath.Join(dir, "new")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := replaceExecutable(dst, src)
	if err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}
	realDst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got != realDst {
		t.Fatalf("path = %q, want %q", got, realDst)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if _, err := os.Stat(dst + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup should be removed, err=%v", err)
	}
}
