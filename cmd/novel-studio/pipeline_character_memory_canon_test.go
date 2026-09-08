package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCharacterMemoryCanonOverridesMatchExactPublicationWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	progress := &domain.Progress{}
	projectAllCmdTestWriteFile(t, filepath.Join(dir, "characters.json"), `{"identity":"unchanged"}`)
	registryPath := "meta/character_agents/registry.json"
	memoryPath := "meta/character_agents/memory/ca_example.json"
	oldRegistry, newRegistry := `{"registry":"before"}`, `{"registry":"after"}`
	newMemory := `{"memory":"after"}`
	projectAllCmdTestWriteFile(t, filepath.Join(dir, registryPath), oldRegistry)
	beforeRoot, err := pipelineCanonRoot(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	directoryBefore, err := store.DirectoryContentRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]string{registryPath: pipelineBytesSHA([]byte(newRegistry)), memoryPath: pipelineBytesSHA([]byte(newMemory))}
	wantAfter, err := pipelineCanonRootWithCharacterMemoryOverrides(dir, progress, after)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := store.DirectoryContentRoot(dir); err != nil || current != directoryBefore {
		t.Fatalf("dry-run canonical root wrote files: %v", err)
	}
	projectAllCmdTestWriteFile(t, filepath.Join(dir, registryPath), newRegistry)
	projectAllCmdTestWriteFile(t, filepath.Join(dir, memoryPath), newMemory)
	gotAfter, err := pipelineCanonRoot(dir, progress)
	if err != nil || gotAfter != wantAfter || gotAfter == beforeRoot {
		t.Fatalf("predicted and actual roots disagree: got=%s want=%s err=%v", gotAfter, wantAfter, err)
	}
	before := map[string]string{registryPath: pipelineBytesSHA([]byte(oldRegistry)), memoryPath: ""}
	reconstructedBefore, err := pipelineCanonRootWithCharacterMemoryOverrides(dir, progress, before)
	if err != nil || reconstructedBefore != beforeRoot {
		t.Fatalf("cannot verify exact predecessor root: got=%s want=%s err=%v", reconstructedBefore, beforeRoot, err)
	}
	projectAllCmdTestWriteFile(t, filepath.Join(dir, "characters.json"), `{"identity":"tampered"}`)
	if reconstructedBefore, err = pipelineCanonRootWithCharacterMemoryOverrides(dir, progress, before); err != nil || reconstructedBefore == beforeRoot {
		t.Fatalf("memory transition hid unrelated canon drift: root=%s err=%v", reconstructedBefore, err)
	}
}

func TestCharacterMemoryCanonOverridesRejectPathsDigestsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	for _, entry := range []struct{ path, digest string }{
		{"characters.json", ""}, {"meta/character_agents/projected/g/memory.json", ""},
		{"meta/character_agents/memory/../../characters.json", ""},
		{"meta/character_agents/memory/ca/a.json", ""}, {"/meta/character_agents/registry.json", ""},
		{"meta/character_agents/registry.json", "not-a-digest"},
	} {
		if _, err := pipelineCanonRootWithCharacterMemoryOverrides(dir, &domain.Progress{}, map[string]string{entry.path: entry.digest}); err == nil {
			t.Errorf("accepted invalid memory override: %+v", entry)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta/character_agents/memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "meta/character_agents/memory/ca_link.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineCanonRootWithCharacterMemoryOverrides(dir, &domain.Progress{}, map[string]string{"meta/character_agents/memory/ca_link.json": ""}); err == nil {
		t.Fatal("memory override hid a symlink")
	}
}
