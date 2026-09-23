package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationValidationBenchmarkStore(b *testing.B) (*store.Store, domain.ChapterWorldSimulation) {
	b.Helper()
	source := os.Getenv("NOVEL_ACTIVATION_VALIDATION_READONLY")
	if source == "" {
		b.Skip("requires an explicit immutable shadow snapshot")
	}
	dir := b.TempDir()
	if err := os.CopyFS(dir, os.DirFS(source)); err != nil {
		b.Fatal(err)
	}
	st := store.NewStore(dir)
	sim, err := st.LoadChapterWorldSimulation(2)
	if err != nil || sim == nil {
		b.Fatalf("load simulation: %v", err)
	}
	path := filepath.Join(dir, "meta/character_agents/activation_sessions", sim.GenerationID, "000002/chapter_evidence.json")
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("source evidence bytes=%d cycles=%d", info.Size(), len(sim.CharacterActivation.CycleDigests))
	return st, *sim
}

func BenchmarkStoredActivationValidationActual(b *testing.B) {
	st, sim := activationValidationBenchmarkStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateStoredCharacterAgentProtocol(st, sim); err != nil {
			b.Fatal(err)
		}
	}
}

// Attribute only the removed operation, separately from full validation.
func BenchmarkActivationDiscardedProofCopyActual(b *testing.B) {
	st, sim := activationValidationBenchmarkStore(b)
	verified, err := loadCurrentVerifiedCharacterActivationEvidence(st, sim)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if copy := verified.Evidence(); copy.Digest == "" {
			b.Fatal("missing proof copy")
		}
	}
}

// Regular regression: every validation rereads its current actual source and
// rejects changed simulation intent, missing evidence, and changed source bytes.
func TestStoredActivationValidationRereadsAndRejectsTampering(t *testing.T) {
	evidence := testutil.CharacterActivationChapter(t)
	sim, err := domain.BuildCharacterActivationSimulation(evidence, "tick_fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(t.TempDir())
	path := filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", sim.GenerationID, "000001/chapter_evidence.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(evidence)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateStoredCharacterAgentProtocol(st, sim); err != nil {
		t.Fatal(err)
	}
	wrong := sim
	wrong.ProtagonistProjection.ChosenDecision = "unaccepted replacement intent"
	if err := validateStoredCharacterAgentProtocol(st, wrong); err == nil {
		t.Fatal("changed simulation accepted")
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStoredCharacterAgentProtocol(st, sim); err != nil {
		t.Fatal(err)
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatal("validator changed stored bytes")
	}
	evidence.Reviews[0].Receipt.Reason = "tampered after successful validation"
	changed, _ := json.Marshal(evidence)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateStoredCharacterAgentProtocol(st, sim); err == nil {
		t.Fatal("reused authority after source tampering")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := validateStoredCharacterAgentProtocol(st, sim); err == nil {
		t.Fatal("missing evidence accepted")
	}
}
