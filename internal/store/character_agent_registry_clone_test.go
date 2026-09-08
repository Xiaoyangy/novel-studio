package store

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCloneCharacterAgentRecordsDetachesSealedEvidence(t *testing.T) {
	before := []domain.CharacterAgentRecord{{Character: "甲", Status: "active", Aliases: []string{"旧名"}}}
	after := cloneCharacterAgentRecords(before)
	after[0].Status = "sleeping"
	after[0].Aliases[0] = "改名"
	if before[0].Status != "active" || before[0].Aliases[0] != "旧名" {
		t.Fatalf("canonical memory promotion mutated sealed registry: %+v", before)
	}
	if cloneCharacterAgentRecords(nil) != nil {
		t.Fatal("nil registry changed shape")
	}
}
