package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestCommunicationAddressingAcceptedSleepingMemoryAndReplay(t *testing.T) {
	evidence := testutil.CharacterActivationChapterWithAddressedCommunication(t)
	simulation, err := domain.BuildCharacterActivationSimulation(evidence, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.ProjectedChapterBundle{GenerationID: simulation.GenerationID, Chapter: 1, ChapterWorldSimulation: simulation, CharacterActivationEvidence: &evidence}
	outcome := domain.ActualOutcomeReceiptV2{ReceiptDigest: "sha256:" + strings.Repeat("a", 64), AcceptedAt: "2026-09-23T00:00:00Z"}
	paths, err := characterMemoryCandidatePaths(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("accepted addressed communication lost sleeping recipient memory path: %v", paths)
	}
	before := map[string]CharacterMemoryPublicationFile{}
	for _, path := range paths {
		before[path] = CharacterMemoryPublicationFile{Path: path}
	}
	files, err := deriveActivationMemoryPublicationFiles(bundle, outcome, paths, before)
	if err != nil {
		t.Fatal(err)
	}
	receiver := evidence.Cycles[0].Evidence.Arbitrations[0].CommunicationReceptions[0].ToAgentID
	found := false
	for _, file := range files {
		if file.Path != characterAgentMemoryPath(receiver) {
			continue
		}
		found = true
		var memory domain.CharacterAgentMemory
		if err := json.Unmarshal(file.After, &memory); err != nil {
			t.Fatal(err)
		}
		if memory.State != "canonical" || len(memory.Facts) != 1 || memory.Facts[0].Kind != "accepted_received_communication" || !memory.Facts[0].Accepted {
			t.Fatalf("actual receipt not promoted without fabricating a decision: %+v", memory)
		}
		if strings.Contains(memory.Facts[0].Text, "甲") || !strings.Contains(memory.Facts[0].Text, "燃油上次观测为12升") {
			t.Fatal("accepted receipt leaked a name or lost original text")
		}
	}
	if !found {
		t.Fatal("no sleeping recipient canonical memory candidate")
	}
	for _, file := range files {
		before[file.Path] = CharacterMemoryPublicationFile{Path: file.Path, BeforeExists: true, Before: file.After, BeforeSHA256: file.AfterSHA256}
	}
	replayed, err := deriveActivationMemoryPublicationFiles(bundle, outcome, paths, before)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if string(replayed[i].After) != string(files[i].After) {
			t.Fatal("repeated accepted receipt changed canonical memory/version")
		}
	}
}
