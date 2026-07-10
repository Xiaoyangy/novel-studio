package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
)

func TestBrainstormKickoffJournalReusesExactInputAndArtifact(t *testing.T) {
	runsRoot := t.TempDir()
	projectDir := filepath.Join(runsRoot, "测试书")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "brainstorm.md"), []byte("完整脑爆素材"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := brainstormKickoffKey(bootstrap.Config{Style: "default", Provider: "openai", ModelName: "gpt-5.6-sol"}, assets.Load("default"), "县城神豪")
	if err := saveBrainstormKickoff(runsRoot, key, projectDir); err != nil {
		t.Fatal(err)
	}
	got, ok := loadBrainstormKickoff(runsRoot, key)
	if !ok || got != projectDir {
		t.Fatalf("kickoff cache miss: got=%q ok=%v", got, ok)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "brainstorm.md"), []byte("正文被改动"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadBrainstormKickoff(runsRoot, key); ok {
		t.Fatal("artifact drift must invalidate brainstorm kickoff cache")
	}
}

func TestBrainstormKickoffKeyChangesWithIdeaOrPrompt(t *testing.T) {
	cfg := bootstrap.Config{Style: "default", Provider: "openai", ModelName: "gpt-5.6-sol"}
	bundle := assets.Load("default")
	a := brainstormKickoffKey(cfg, bundle, "县城神豪")
	b := brainstormKickoffKey(cfg, bundle, "返乡经营")
	bundle.Prompts.Brainstorm += "\n新协议"
	c := brainstormKickoffKey(cfg, bundle, "县城神豪")
	if a == b || a == c {
		t.Fatalf("kickoff key must bind idea and prompt: %s %s %s", a, b, c)
	}
}
