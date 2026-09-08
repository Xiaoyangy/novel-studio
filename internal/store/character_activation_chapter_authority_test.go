package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestActivationChapterAuthorityStoreRereadsExactFiles(t *testing.T) {
	st := NewStore(t.TempDir())
	chapter := testutil.CharacterActivationChapter(t)
	gen, ch := chapter.Session.GenerationID, chapter.Session.Chapter
	root, err := characterActivationSessionDir(gen, ch)
	verifiedStoreMust(t, err)
	path := filepath.Join(root, "chapter_evidence.json")
	verifiedStoreMust(t, st.writeCharacterActivationJSON(path, chapter, true))
	before, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := NewStore(st.dir).LoadVerifiedCharacterActivationChapter(gen, ch)
			if err != nil || got == nil {
				t.Errorf("verified read: %v", err)
				return
			}
			if got.Evidence().Digest != chapter.Digest {
				t.Error("read changed digest")
			}
			if _, err := got.BuildSimulation("", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	after, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("verified read mutated source tree")
	}
	// A subsequent operation cannot use the prior read's authority or trust an
	// unchanged (or freshly re-signed) outer digest after the file changes.
	chapter.Inputs[0].Observations[0].Location = "tampered on disk"
	chapter.Digest = ""
	hash, err := domain.DeterministicPlanningHash(chapter)
	verifiedStoreMust(t, err)
	chapter.Digest = "sha256:" + hash
	raw, err := json.Marshal(chapter)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, os.WriteFile(filepath.Join(st.dir, path), raw, 0600))
	for _, reader := range []*Store{st, NewStore(st.dir)} {
		if _, err := reader.LoadVerifiedCharacterActivationChapter(gen, ch); err == nil {
			t.Fatal("cached authority concealed source change")
		}
		if _, err := reader.LoadCharacterActivationChapterEvidence(gen, ch); err == nil {
			t.Fatal("legacy loader skipped source change")
		}
	}
}

func TestActivationChapterAuthorityStoreRejectsForeignAndLinkedSources(t *testing.T) {
	st := NewStore(t.TempDir())
	chapter := testutil.CharacterActivationChapter(t)
	gen, ch := chapter.Session.GenerationID, chapter.Session.Chapter
	root, err := characterActivationSessionDir(gen, ch)
	verifiedStoreMust(t, err)
	path := filepath.Join(root, "chapter_evidence.json")
	if got, err := st.LoadVerifiedCharacterActivationChapter(gen, ch); err != nil || got != nil {
		t.Fatalf("missing source: %v", err)
	}
	chapter.Session.Chapter++
	verifiedStoreMust(t, st.writeCharacterActivationJSON(path, chapter, true))
	if _, err := st.LoadVerifiedCharacterActivationChapter(gen, ch); err == nil {
		t.Fatal("foreign path identity accepted")
	}
	full := filepath.Join(st.dir, path)
	backup := full + ".backup"
	verifiedStoreMust(t, os.Rename(full, backup))
	verifiedStoreMust(t, os.Symlink(backup, full))
	if _, err := st.LoadVerifiedCharacterActivationChapter(gen, ch); err == nil {
		t.Fatal("symlink accepted as immutable source")
	}
}
