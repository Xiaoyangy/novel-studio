package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestFoundationSnapshotExtractionPreservesOriginalHashInventory(t *testing.T) {
	root := t.TempDir()
	io := newIO(root)
	files := map[string][]byte{"premise.md": []byte("premise\n"), "characters.json": []byte("[]"), "meta/character_agents/memory/ca_fixture.json": []byte(`{"version":1}`), "reviews/final.md": []byte("review")}
	for path, raw := range files {
		verifiedStoreMust(t, io.WriteFileUnlocked(path, raw))
	}
	verifiedStoreMust(t, io.WriteFileUnlocked("reviews/drafts/ignored.md", []byte("draft")))
	verifiedStoreMust(t, io.WriteFileUnlocked("meta/character_agents/memory/ignored.txt", []byte("not source")))
	verifiedStoreMust(t, io.WriteFileUnlocked("world_rules.json", nil))
	want := map[string]string{}
	for path, raw := range files {
		want[path] = characterMemoryPublicationSHA(raw)
	}
	inventory, got, err := CaptureProjectAllFoundationSnapshot(root)
	verifiedStoreMust(t, err)
	if !reflect.DeepEqual(inventory.Artifacts, want) {
		t.Fatalf("foundation inventory changed: %#v", inventory.Artifacts)
	}
	hash, err := domain.DeterministicPlanningHash(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{"project-all-foundation-snapshot.v1", want})
	verifiedStoreMust(t, err)
	if got != "sha256:"+hash {
		t.Fatal("extraction changed legacy foundation root bytes")
	}
	before, err := DirectoryContentRoot(root)
	verifiedStoreMust(t, err)
	_, _, err = CaptureProjectAllFoundationSnapshot(root)
	verifiedStoreMust(t, err)
	after, err := DirectoryContentRoot(root)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("foundation hash wrote files")
	}
}

func TestFoundationSnapshotRejectsTopLevelSourceSymlink(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "characters.json")
	verifiedStoreMust(t, os.WriteFile(external, []byte("[]"), 0600))
	verifiedStoreMust(t, os.Symlink(external, filepath.Join(root, "characters.json")))
	if _, _, err := CaptureProjectAllFoundationSnapshot(root); err == nil {
		t.Fatal("source fingerprint followed a foreign symlink")
	}
}
