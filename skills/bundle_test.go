package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestList(t *testing.T) {
	skills, err := List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(skills) < 35 {
		t.Fatalf("expected at least 35 story/fanqie/novel skills, got %d", len(skills))
	}
	for _, skill := range skills {
		if skill.Name == "" {
			t.Fatalf("skill has empty name: %+v", skill)
		}
		if skill.Description == "" {
			t.Fatalf("skill %s has empty description", skill.Name)
		}
	}
}

func TestExport(t *testing.T) {
	dir := t.TempDir()
	if err := Export(dir); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	protocol := filepath.Join(dir, "CONTEXT_PROTOCOL.md")
	if _, err := os.Stat(protocol); err != nil {
		t.Fatalf("expected exported context protocol %s: %v", protocol, err)
	}
	for _, name := range []string{"story", "story-setup", "story-long-write", "story-douban-long-write", "story-short-write", "fanqie-writing-flow", "fanqie-novel-template", "review", "deal-paper-summry"} {
		path := filepath.Join(dir, name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected exported skill file %s: %v", path, err)
		}
		for _, file := range []string{"CONTEXT.md", "context.json"} {
			path := filepath.Join(dir, name, file)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected exported context file %s: %v", path, err)
			}
		}
	}
	typoScan := filepath.Join(dir, "scripts", "typo_scan.py")
	if _, err := os.Stat(typoScan); err != nil {
		t.Fatalf("expected exported support script %s: %v", typoScan, err)
	}
	reviewSignal := filepath.Join(dir, "review", "scripts", "text_signals.py")
	if _, err := os.Stat(reviewSignal); err != nil {
		t.Fatalf("expected exported review support script %s: %v", reviewSignal, err)
	}
	playbook := filepath.Join(dir, "story-long-write", "references", "production-chain-playbook.md")
	if _, err := os.Stat(playbook); err != nil {
		t.Fatalf("expected exported production playbook %s: %v", playbook, err)
	}
	humanFeel := filepath.Join(dir, "story-review", "references", "human-feel-craft.md")
	if _, err := os.Stat(humanFeel); err != nil {
		t.Fatalf("expected exported human feel craft reference %s: %v", humanFeel, err)
	}
	writingDigest := filepath.Join(dir, "story-long-write", "references", "refer-writing-techniques-digest.md")
	if _, err := os.Stat(writingDigest); err != nil {
		t.Fatalf("expected exported refer writing techniques digest %s: %v", writingDigest, err)
	}
}

func TestReadPlan(t *testing.T) {
	plan, err := ReadPlan("story-long-write")
	if err != nil {
		t.Fatalf("ReadPlan() error = %v", err)
	}
	if plan.Skill.Name != "story-long-write" {
		t.Fatalf("unexpected skill: %+v", plan.Skill)
	}
	if plan.ProtocolPath != "skills/CONTEXT_PROTOCOL.md" {
		t.Fatalf("unexpected protocol path: %s", plan.ProtocolPath)
	}
	wantPrefix := []string{
		"skills/CONTEXT_PROTOCOL.md",
		"skills/story-long-write/SKILL.md",
		"skills/story-long-write/CONTEXT.md",
		"skills/story-long-write/context.json",
	}
	if len(plan.ReadOrder) < len(wantPrefix) {
		t.Fatalf("read order too short: %+v", plan.ReadOrder)
	}
	for i, want := range wantPrefix {
		if plan.ReadOrder[i] != want {
			t.Fatalf("read order[%d] = %s, want %s; full order: %+v", i, plan.ReadOrder[i], want, plan.ReadOrder)
		}
	}
	if len(plan.Manifest.ConditionalFiles) == 0 {
		t.Fatal("expected conditional files")
	}
	foundHumanFeel := false
	for _, entry := range plan.Manifest.ConditionalFiles {
		for _, path := range entry.Paths {
			if path == "references/human-feel-craft.md" {
				foundHumanFeel = true
			}
		}
	}
	if !foundHumanFeel {
		t.Fatal("expected human-feel-craft reference in conditional files")
	}
}

func TestReadPlans(t *testing.T) {
	plans, err := ReadPlans()
	if err != nil {
		t.Fatalf("ReadPlans() error = %v", err)
	}
	if len(plans) < 35 {
		t.Fatalf("expected at least 35 context plans, got %d", len(plans))
	}
	seen := make(map[string]bool, len(plans))
	for _, plan := range plans {
		if plan.Manifest.Skill == "" {
			t.Fatalf("empty manifest skill: %+v", plan)
		}
		if seen[plan.Manifest.Skill] {
			t.Fatalf("duplicate plan for %s", plan.Manifest.Skill)
		}
		seen[plan.Manifest.Skill] = true
		if !containsString(plan.ReadOrder, "skills/CONTEXT_PROTOCOL.md") {
			t.Fatalf("%s missing protocol in read order", plan.Manifest.Skill)
		}
	}
	if !seen["review"] || !seen["story-long-write"] {
		t.Fatalf("missing expected plans: %+v", seen)
	}
}

func TestReadBundleMaterializesReviewAuditSupport(t *testing.T) {
	bundle, err := ReadBundle("review", true)
	if err != nil {
		t.Fatalf("ReadBundle(review) error = %v", err)
	}
	for _, want := range []string{
		"quality/audit/README.md",
		"quality/audit/scripts/aigc_value.py",
		"quality/audit/references/signals-zh.md",
	} {
		file, ok := contextFileByPath(bundle.Files, want)
		if !ok {
			t.Fatalf("review context bundle missing %s", want)
		}
		if file.Content == "" {
			t.Fatalf("review context bundle file %s has empty content", want)
		}
		if !file.Conditional {
			t.Fatalf("expected %s to be marked conditional", want)
		}
	}
}

func TestReadBundleWithStateMaterializesExistingStateFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".skill-context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "追踪"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".skill-context", "story-long-write.md"), []byte("# state\n- stage: drafting\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "追踪", "上下文.md"), []byte("# tracking\n- chapter: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ReadBundleWithState("story-long-write", false, dir)
	if err != nil {
		t.Fatalf("ReadBundleWithState() error = %v", err)
	}
	for _, want := range []string{
		".skill-context/story-long-write.md",
		"追踪/上下文.md",
	} {
		file, ok := contextFileByPath(bundle.Files, want)
		if !ok {
			t.Fatalf("context bundle missing state file %s", want)
		}
		if !file.State {
			t.Fatalf("expected %s to be marked state", want)
		}
		if file.SourcePath == "" {
			t.Fatalf("expected %s to include source path", want)
		}
		if file.Content == "" {
			t.Fatalf("expected %s to include content", want)
		}
	}
	if !containsString(bundle.MissingStateFiles, "_progress.md") {
		t.Fatalf("expected missing _progress.md, got %+v", bundle.MissingStateFiles)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func contextFileByPath(files []ContextFile, want string) (ContextFile, bool) {
	for _, file := range files {
		if file.Path == want {
			return file, true
		}
	}
	return ContextFile{}, false
}
