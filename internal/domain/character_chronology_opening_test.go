package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChronologyOpeningRequiresExactExternalPredecessor(t *testing.T) {
	f := newSelfExperienceFixture(t)
	source := *f.stimulus.PhysicalState
	before, _ := json.Marshal(source)
	prepared, err := PrepareCharacterSelfChronologyStateV1(source)
	if err != nil {
		t.Fatal(err)
	}
	opening := WorldStimulusPacket{Sources: []string{CharacterSelfChronologyPolicyV1}, PhysicalState: &prepared}
	if err := ValidateCharacterChronologyOpeningFromSourceV1(source, opening); err != nil {
		t.Fatal(err)
	}
	a, _ := CharacterPhysicalRootForCycle(source)
	b, _ := CharacterPhysicalRootForCycle(prepared)
	if a == b {
		t.Fatal("fixture did not introduce a real metadata root change")
	}
	if err := ValidateCharacterChronologyOpeningFromSourceV1(prepared, opening); err != nil {
		t.Fatal("anchored predecessor was not idempotent", err)
	}
	for name, mutate := range map[string]func(*WorldPhysicalStateV2){
		"resigned source root": func(s *WorldPhysicalStateV2) {
			x := s.Actors[0].SelfChronologyBaseline
			x.SourcePhysicalRoot = "sha256:" + strings.Repeat("f", 64)
			x.Digest, _ = baselineDigestV1(*x)
		},
		"location": func(s *WorldPhysicalStateV2) { s.Actors[0].Location = "invented destination" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(prepared)
			var bad WorldPhysicalStateV2
			_ = json.Unmarshal(raw, &bad)
			mutate(&bad)
			if err := ValidateWorldPhysicalStateV2(bad); err != nil {
				t.Fatal("negative must be internally valid to prove external-source guard", err)
			}
			if ValidateCharacterChronologyOpeningFromSourceV1(source, WorldStimulusPacket{Sources: opening.Sources, PhysicalState: &bad}) == nil {
				t.Fatal("self-consistent alternate opening bypassed actual source")
			}
			previous := ProjectedChapterBundle{Chapter: 1, ChapterWorldSimulation: ChapterWorldSimulation{PhysicalState: &source}}
			current := ProjectedChapterBundle{Chapter: 2, CharacterAgentEvidence: &CharacterAgentEvidenceBundle{Stimulus: WorldStimulusPacket{Sources: opening.Sources, PhysicalState: &bad}}}
			if validateCharacterChronologyProjectedPredecessorV1(previous, current) == nil {
				t.Fatal("projected predecessor guard did not consume actual previous state")
			}
		})
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("validation mutated its original source")
	}
	if err := ValidateCharacterChronologyOpeningFromSourceV1(source, WorldStimulusPacket{}); err != nil {
		t.Fatal("legacy policy semantics changed", err)
	}
}
