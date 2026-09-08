package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationSessionStoreFixture(t *testing.T) (*Store, domain.CharacterActivationSession, domain.CharacterActivationCycle) {
	t.Helper()
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(session); err != nil {
		t.Fatal(err)
	}
	return st, session, cycle
}

func TestActivationSessionConcurrentCommitAndResumeAreIdempotent(t *testing.T) {
	st, session, cycle := activationSessionStoreFixture(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := NewStore(st.Dir()).AppendCharacterActivationCycle(session.Digest, cycle)
			if err != nil {
				t.Error(err)
			} else if next.Phase != "assessing" || len(next.CycleDigests) != 1 {
				t.Error("unexpected committed cursor")
			}
		}()
	}
	wg.Wait()
	current, err := st.LoadCharacterActivationSession(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	readiness := testutil.CycleReadiness(t, cycle, "ready_for_plan")
	expected := current.Digest
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := NewStore(st.Dir()).ApplyCharacterChapterReadiness(expected, readiness)
			if err != nil {
				t.Error(err)
			} else if next.Phase != "ready" {
				t.Error("lost ready assessment")
			}
		}()
	}
	wg.Wait()
	restored, err := NewStore(st.Dir()).RecoverCharacterActivationSession(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Phase != "ready" || len(restored.CycleDigests) != 1 || len(restored.ReadinessDigests) != 1 {
		t.Fatal("recovery repeated work or lost readiness")
	}
	if _, err := st.LoadCharacterActivationSession("../../outside", 1); err == nil {
		t.Fatal("unsafe session path accepted")
	}
}

func TestActivationSessionRecoversResultBeforeCursorWithoutModel(t *testing.T) {
	st, session, cycle := activationSessionStoreFixture(t)
	root, _ := characterActivationSessionDir(session.GenerationID, 1)
	if err := st.CharacterAgents.writeImmutable(activationCyclePath(root, 1), cycle); err != nil {
		t.Fatal(err)
	}
	current, err := st.LoadCharacterActivationSession(session.GenerationID, 1)
	if err != nil || len(current.CycleDigests) != 0 {
		t.Fatal("read-only load advanced orphan result")
	}
	current, err = st.RecoverCharacterActivationSession(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != "assessing" || len(current.CycleDigests) != 1 {
		t.Fatal("cycle-before-cursor gap not recovered")
	}
	readiness := testutil.CycleReadiness(t, cycle, "continue")
	if err := st.CharacterAgents.writeImmutable(activationReadinessPath(root, 1), readiness); err != nil {
		t.Fatal(err)
	}
	current, err = NewStore(st.Dir()).RecoverCharacterActivationSession(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != "collecting" || len(current.ReadinessDigests) != 1 {
		t.Fatal("assessment-before-cursor gap not recovered")
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(cycle.Evidence.Arbitrations[0], cycle.Evidence.Stimulus, cycle.Evidence.Proposals...)
	if err != nil {
		t.Fatal(err)
	}
	second := testutil.CharacterCycle(t, 2, cycle.Digest, &after, cycle.EndDay)
	if _, err := st.AppendCharacterActivationCycle(session.Digest, second); err == nil {
		t.Fatal("stale CAS accepted a new cycle")
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), activationCyclePath(root, 2))); !os.IsNotExist(err) {
		t.Fatal("stale CAS wrote a cycle")
	}
	if _, err := st.AppendCharacterActivationCycle(current.Digest, second); err != nil {
		t.Fatal(err)
	}
}

func TestActivationSessionDoesNotTrustResignedCursor(t *testing.T) {
	st, session, cycle := activationSessionStoreFixture(t)
	current, err := st.AppendCharacterActivationCycle(session.Digest, cycle)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := characterActivationSessionDir(session.GenerationID, 1)
	path := filepath.Join(st.Dir(), root, "session.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"time", "state", "ready", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			changed := *current
			switch kind {
			case "time":
				changed.CurrentDay += .01
			case "state":
				changed.CurrentPhysicalRoot = session.InitialPhysicalRoot
			case "ready":
				changed.Phase = "ready"
				changed.ReadinessDigests = []string{cycle.Digest}
			case "foreign":
				changed.GenerationID = "pg2_foreign"
			}
			changed.Digest = ""
			raw, _ := json.Marshal(changed)
			changed.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
			raw, _ = json.Marshal(changed)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := st.LoadCharacterActivationSession(session.GenerationID, 1); err == nil {
				t.Fatal("re-signed false cursor accepted")
			}
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActivationSessionRejectsSymlinkedEvidenceDirectory(t *testing.T) {
	st, session, cycle := activationSessionStoreFixture(t)
	root, _ := characterActivationSessionDir(session.GenerationID, 1)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(st.Dir(), root, "cycles")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendCharacterActivationCycle(session.Digest, cycle); err == nil {
		t.Fatal("cycle escaped through a directory symlink")
	}
	if _, err := st.LoadCharacterActivationCycle(session.GenerationID, 1, 1); err == nil {
		t.Fatal("cycle read followed a directory symlink")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("outside directory was modified")
	}
}
