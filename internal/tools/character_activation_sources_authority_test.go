package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestActivationGroundingSourceAuthorityMatchesPublicInputAndRereads(t *testing.T) {
	chapter := testutil.CharacterActivationChapter(t)
	sim, err := domain.BuildCharacterActivationSimulation(chapter, "tick_fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Goal: "保持真实结果", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
	st := store.NewStore(t.TempDir())
	path := filepath.Join(st.Dir(), "meta", "character_agents", "activation_sessions", sim.GenerationID, "000001", "chapter_evidence.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chapter)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.NewActivationPlanGroundingInput(plan, sim, chapter, chapter.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := currentPlanGroundingInput(st, plan, sim, chapter.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatal("source-aware input changed public JSON/hash")
	}
	loaded, err := loadCurrentCharacterActivationEvidence(st, sim)
	if err != nil {
		t.Fatal(err)
	}
	loadedJSON, _ := json.Marshal(loaded)
	if !bytes.Equal(raw, loadedJSON) {
		t.Fatal("compatibility evidence loader changed source")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatalf("read changed source: %v", err)
	}
	sim.CharacterDecisionTrace[0].Decision.Decision = "invented intent"
	if _, err := currentPlanGroundingInput(st, plan, sim, chapter.ProtocolDigest); err == nil {
		t.Fatal("altered simulation accepted")
	}
	chapter.Reviews[0].Receipt.Reason = "invented result"
	raw, _ = json.Marshal(chapter)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := currentPlanGroundingInput(st, plan, sim, chapter.ProtocolDigest); err == nil {
		t.Fatal("next operation reused authority after source tampering")
	}
}
