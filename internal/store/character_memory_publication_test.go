package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCharacterMemoryPublicationExactBeforeAfterAndThirdState(t *testing.T) {
	io := newIO(t.TempDir())
	files := []CharacterMemoryPublicationFile{
		{Path: characterAgentMemoryPath("ca_one"), BeforeExists: false, After: []byte("new memory")},
		{Path: characterAgentRegistryPath(), BeforeExists: true, Before: []byte("old registry"), After: []byte("new registry")},
	}
	if err := io.WriteFileUnlocked(files[1].Path, files[1].Before); err != nil {
		t.Fatal(err)
	}
	if complete, err := inspectCharacterMemoryPublicationFiles(io, files); err != nil || complete {
		t.Fatalf("before state: complete=%v err=%v", complete, err)
	}
	if err := io.WriteFileUnlocked(files[0].Path, files[0].After); err != nil {
		t.Fatal(err)
	}
	if complete, err := inspectCharacterMemoryPublicationFiles(io, files); err != nil || complete {
		t.Fatalf("partial exact publication must be recoverable: complete=%v err=%v", complete, err)
	}
	if err := io.WriteFileUnlocked(files[1].Path, files[1].After); err != nil {
		t.Fatal(err)
	}
	if complete, err := inspectCharacterMemoryPublicationFiles(io, files); err != nil || !complete {
		t.Fatalf("after state: complete=%v err=%v", complete, err)
	}
	if err := io.WriteFileUnlocked(files[1].Path, []byte("unknown corruption")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectCharacterMemoryPublicationFiles(io, files); err == nil || !strings.Contains(err.Error(), "exact before and after") {
		t.Fatalf("third state should fail closed: %v", err)
	}
	got, err := io.ReadFileUnlocked(files[1].Path)
	if err != nil || !bytes.Equal(got, []byte("unknown corruption")) {
		t.Fatal("inspection modified a corrupt target")
	}
}

func TestCharacterMemoryPublicationTargetsAndSymlinksAreRejected(t *testing.T) {
	for _, path := range []string{"meta/usage.json", "meta/character_agents/memory/../registry.json", "meta/character_agents/memory/ca_x/nested.json", "meta/character_agents/projected/g/memory/ca_x.json", "/tmp/registry.json"} {
		if err := validateCharacterMemoryTarget(path); err == nil {
			t.Errorf("unsafe target allowed: %q", path)
		}
	}
	for _, path := range []string{characterAgentRegistryPath(), characterAgentMemoryPath("ca_0123")} {
		if err := validateCharacterMemoryTarget(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, symlinkParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "target", true: "parent"}[symlinkParent], func(t *testing.T) {
			io := newIO(t.TempDir())
			outside := t.TempDir()
			path := io.path(characterAgentMemoryPath("ca_one"))
			if symlinkParent {
				if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "missing.json"), path); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := readCharacterMemoryPublicationFile(io, characterAgentMemoryPath("ca_one")); err == nil {
				t.Fatal("symlink must not masquerade as absent before image")
			}
		})
	}
}

func TestCharacterMemoryPublicationMissingReadDoesNotCreatePlanningState(t *testing.T) {
	st := NewStore(t.TempDir())
	digest := characterMemoryPublicationSHA([]byte("missing outcome"))
	manifest, err := st.ProjectedV2().LoadCharacterMemoryPublication(digest)
	if err != nil || manifest != nil {
		t.Fatalf("missing read: manifest=%+v err=%v", manifest, err)
	}
	entries, err := os.ReadDir(st.Dir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only missing publication created files: %v %v", entries, err)
	}
	if err := st.ApplyPreparedCharacterMemoryPublication(nil); err == nil {
		t.Fatal("nil prepare must not authorize canon writes")
	}
}
