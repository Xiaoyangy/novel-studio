package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWithOverridesPrecedence(t *testing.T) {
	global := t.TempDir()
	project := t.TempDir()

	// 全局覆盖 writer；项目级覆盖 writer + editor（项目级应胜出）。
	if err := os.WriteFile(filepath.Join(global, "writer.md"), []byte("全局版 writer prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "writer.md"), []byte("项目版 writer prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "editor.md"), []byte("项目版 editor prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 空文件应回退内置。
	if err := os.WriteFile(filepath.Join(project, "coordinator.md"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, provenance := LoadWithOverrides("default", global, project)

	if !strings.HasPrefix(bundle.Prompts.Writer, "项目版 writer prompt") {
		t.Fatalf("项目级覆盖应胜出，实际开头: %.30s", bundle.Prompts.Writer)
	}
	if !strings.Contains(bundle.Prompts.Writer, "仿写画像") {
		t.Fatal("覆盖版必须走 WithSimulationGuidance 同一包装路径")
	}
	if !strings.HasPrefix(bundle.Prompts.Editor, "项目版 editor prompt") {
		t.Fatal("editor 项目级覆盖未生效")
	}

	baseline := Load("default")
	if bundle.Prompts.Coordinator != baseline.Prompts.Coordinator {
		t.Fatal("空覆盖文件应回退内置 coordinator")
	}

	bySource := map[string]string{}
	byFinger := map[string]string{}
	for _, p := range provenance {
		bySource[p.Name] = p.Source
		byFinger[p.Name] = p.Fingerprint
		if len(p.Fingerprint) != 12 {
			t.Fatalf("指纹应为 12 位: %+v", p)
		}
	}
	if bySource["writer.md"] != "override:"+project {
		t.Fatalf("writer 来源应为项目级覆盖: %s", bySource["writer.md"])
	}
	if bySource["coordinator.md"] != "builtin" {
		t.Fatalf("coordinator 来源应回退 builtin: %s", bySource["coordinator.md"])
	}
	if byFinger["writer.md"] == byFinger["architect-long.md"] {
		t.Fatal("不同内容指纹不应相同")
	}

	// 无覆盖目录时与 Load 完全等价，全部 builtin。
	plain, prov2 := LoadWithOverrides("default")
	if plain.Prompts.Writer != baseline.Prompts.Writer {
		t.Fatal("无覆盖时应与 Load 等价")
	}
	for _, p := range prov2 {
		if p.Source != "builtin" {
			t.Fatalf("无覆盖时来源应全为 builtin: %+v", p)
		}
	}
}

func TestWritePromptManifest(t *testing.T) {
	dir := t.TempDir()
	_, provenance := LoadWithOverrides("default")
	WritePromptManifest(dir, provenance)
	data, err := os.ReadFile(filepath.Join(dir, "meta", "prompt_manifest.json"))
	if err != nil {
		t.Fatalf("manifest 未写入: %v", err)
	}
	var back []PromptProvenance
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("manifest 非法 JSON: %v", err)
	}
	if len(back) != 5 {
		t.Fatalf("manifest 应含 5 个核心 prompt，实际 %d", len(back))
	}
}
