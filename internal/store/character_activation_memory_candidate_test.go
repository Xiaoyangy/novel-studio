package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestActivationMemoryDerivationIncludesPassiveRecipientsAndIsIdempotent(t *testing.T) {
	evidence := testutil.CharacterActivationChapterWithPassive(t)
	simulation, err := domain.BuildCharacterActivationSimulation(evidence, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.ProjectedChapterBundle{GenerationID: simulation.GenerationID, Chapter: 1, ChapterWorldSimulation: simulation, CharacterActivationEvidence: &evidence}
	outcome := domain.ActualOutcomeReceiptV2{ReceiptDigest: "sha256:" + strings.Repeat("a", 64), AcceptedAt: "2026-09-06T00:00:00Z"}
	paths, err := characterMemoryCandidatePaths(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected registry plus sender and passive receiver, got %v", paths)
	}
	before := map[string]CharacterMemoryPublicationFile{}
	for _, path := range paths {
		before[path] = CharacterMemoryPublicationFile{Path: path}
	}
	files, err := deriveActivationMemoryPublicationFiles(bundle, outcome, paths, before)
	if err != nil {
		t.Fatal(err)
	}
	var receiverID string
	for _, entry := range evidence.Inputs[0].Registry.Entries {
		if entry.Character == "乙" {
			receiverID = entry.AgentID
		}
	}
	for _, file := range files {
		if strings.Contains(string(file.After), "SECRET") {
			t.Fatal("world-side private prose entered canonical memory candidate")
		}
		if file.Path != characterAgentMemoryPath(receiverID) {
			continue
		}
		var memory domain.CharacterAgentMemory
		if err := json.Unmarshal(file.After, &memory); err != nil {
			t.Fatal(err)
		}
		if len(memory.Facts) != 1 || memory.Facts[0].Kind != "accepted_received_communication" || !memory.Facts[0].Accepted || !strings.Contains(memory.Facts[0].Text, "燃油上次观测为12升") || memory.State != "canonical" {
			t.Fatalf("passive recipient got a fabricated choice or lost received knowledge: %+v", memory)
		}
	}
	for _, file := range files {
		before[file.Path] = CharacterMemoryPublicationFile{Path: file.Path, BeforeExists: true, Before: file.After, BeforeSHA256: file.AfterSHA256}
	}
	replayed, err := deriveActivationMemoryPublicationFiles(bundle, outcome, paths, before)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if !reflect.DeepEqual(replayed[i].After, files[i].After) {
			t.Fatal("repeated memory publication changed accepted facts or versions")
		}
	}
}
