package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func characterObservationForStorageTest(t *testing.T) domain.CharacterObservationPacket {
	t.Helper()
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		GenerationID: "pg2_integrity", Chapter: 3, Round: 1, AgentID: "ca_lin", Character: "林澄",
		StimulusDigest: "sha256:stimulus", KnownFacts: []domain.CharacterAgentFact{{ID: "lamp", Kind: "known", Text: "灯已熄灭"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestCharacterObservationRejectsRelocatedValidPacket(t *testing.T) {
	for _, field := range []string{"generation", "chapter", "round", "agent"} {
		t.Run(field, func(t *testing.T) {
			st := NewStore(t.TempDir())
			original := characterObservationForStorageTest(t)
			misplaced := original
			switch field {
			case "generation":
				misplaced.GenerationID = "pg2_other"
			case "chapter":
				misplaced.Chapter++
			case "round":
				misplaced.Round++
				misplaced.ConflictFeedback = []string{"路线受阻"}
			case "agent":
				misplaced.AgentID = "ca_other"
				misplaced.Character = "周砚"
			}
			misplaced, err := domain.FinalizeCharacterObservationPacket(misplaced)
			if err != nil {
				t.Fatal(err)
			}
			path := characterAgentObservationPath(original.GenerationID, original.Chapter, original.Round, original.AgentID)
			if err := st.CharacterAgents.io.WriteJSON(path, misplaced); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CharacterAgents.LoadObservation(original.GenerationID, original.Chapter, original.Round, original.AgentID); err == nil {
				t.Fatal("accepted a self-consistent observation belonging to another identity")
			}
		})
	}
}

func TestCharacterWorldPacketsRejectRelocatedValidEvidence(t *testing.T) {
	for _, field := range []string{"generation", "chapter"} {
		t.Run(field, func(t *testing.T) {
			st := NewStore(t.TempDir())
			generation, chapter := "pg2_integrity", 3
			otherGeneration, otherChapter := generation, chapter
			if field == "generation" {
				otherGeneration = "pg2_other"
			} else {
				otherChapter++
			}
			stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{GenerationID: otherGeneration, Chapter: otherChapter, TimeWindow: "雨夜"})
			if err != nil {
				t.Fatal(err)
			}
			activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: otherGeneration, Chapter: otherChapter})
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]any{"stimulus": stimulus, "activation": activation} {
				if err := st.CharacterAgents.io.WriteJSON(filepath.Join(characterAgentChapterDir(generation, chapter), name+".json"), value); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.CharacterAgents.LoadStimulus(generation, chapter); err == nil {
				t.Fatal("accepted relocated stimulus")
			}
			if _, err := st.CharacterAgents.LoadActivation(generation, chapter); err == nil {
				t.Fatal("accepted relocated activation")
			}
		})
	}
}

func TestCharacterImmutableRetryVerifiesPayloadNotClaimedDigest(t *testing.T) {
	st := NewStore(t.TempDir())
	original := characterObservationForStorageTest(t)
	if err := st.CharacterAgents.SaveObservation(original); err != nil {
		t.Fatal(err)
	}
	corrupted := original
	corrupted.CurrentGoal = "篡改后的目标"
	path := characterAgentObservationPath(original.GenerationID, original.Chapter, original.Round, original.AgentID)
	if err := st.CharacterAgents.io.WriteJSON(path, corrupted); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveObservation(original); err == nil {
		t.Fatal("retry accepted changed content carrying the original digest")
	}
	registry, err := domain.FinalizeCharacterAgentRegistry(domain.CharacterAgentRegistry{Project: "原作品"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveRegistrySnapshot(original.GenerationID, original.Chapter, registry); err != nil {
		t.Fatal(err)
	}
	corruptedRegistry := registry
	corruptedRegistry.Project = "另一作品"
	if err := st.CharacterAgents.io.WriteJSON(characterAgentRegistrySnapshotPath(original.GenerationID, original.Chapter), corruptedRegistry); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveRegistrySnapshot(original.GenerationID, original.Chapter, registry); err == nil {
		t.Fatal("retry accepted changed registry carrying the original root")
	}
}

func TestImmutableJSONComparisonPreservesIntegerPrecision(t *testing.T) {
	if sameJSON([]byte(`{"memory_version":9007199254740992}`), []byte(`{"memory_version":9007199254740993}`)) {
		t.Fatal("different integer evidence rounded to the same float64")
	}
	if !sameJSON([]byte(`{"chapter":100,"round":1}`), []byte("{\n \"round\": 1, \"chapter\": 100\n}")) {
		t.Fatal("equivalent JSON formatting was treated as different evidence")
	}
}

func TestCharacterImmutablePublicationAcrossIndependentStores(t *testing.T) {
	root := t.TempDir()
	const count = 16
	start := make(chan struct{})
	type result struct {
		digest string
		err    error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		st := NewCharacterAgentStore(newIO(root))
		observation := characterObservationForStorageTest(t)
		observation.CurrentGoal = fmt.Sprintf("互斥选择-%d", i)
		observation, err := domain.FinalizeCharacterObservationPacket(observation)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- result{observation.Digest, st.SaveObservation(observation)}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winner := ""
	for result := range results {
		if result.err != nil {
			continue
		}
		if winner != "" {
			t.Fatal("two independent stores replaced the same immutable path")
		}
		winner = result.digest
	}
	if winner == "" {
		t.Fatal("no proposal could publish")
	}
	st := NewCharacterAgentStore(newIO(root))
	observation := characterObservationForStorageTest(t)
	stored, err := st.LoadObservation(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err != nil || stored == nil || stored.Digest != winner {
		t.Fatalf("stored observation does not match successful publisher: %v, %v", stored, err)
	}
	if err := st.SaveObservation(*stored); err != nil {
		t.Fatalf("identical retry failed: %v", err)
	}
}

func TestCharacterArbitrationRejectsWrongRoundBeforeLoadingDependencies(t *testing.T) {
	st := NewStore(t.TempDir())
	receipt := domain.WorldArbitrationReceipt{GenerationID: "pg2_integrity", Chapter: 3, Round: 2}
	if err := st.CharacterAgents.io.WriteJSON(characterAgentArbitrationPath(receipt.GenerationID, receipt.Chapter, 1), receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CharacterAgents.LoadArbitration(receipt.GenerationID, receipt.Chapter, 1); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("misplaced arbitration was not rejected by identity: %v", err)
	}
}
