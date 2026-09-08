package domain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Lock down why the legacy apply path must canonicalize BEFORE deriving.
// The historical mixed-time algorithm remains for signed v1 facts; this is
// not an assertion that opaque IDs provide a correct chronology.
func TestSelfProgressMixedTimeLegacyAudit(t *testing.T) {
	makeFact := func(id, status string, start, end *float64) CharacterSelfExperienceV2 {
		return CharacterSelfExperienceV2{ID: id, Chapter: 1, TaskID: "work", Kind: "work", Action: "inspect", Status: status, StartDay: start, EndDay: end, ProgressUnit: "minute"}
	}
	a := makeFact("a", "in_progress", selfTestDay(1), selfTestDay(2))
	b := makeFact("b", "not_started", nil, nil)
	c := makeFact("c", "in_progress", selfTestDay(0), selfTestDay(1))
	appended := []CharacterSelfExperienceV2{c, a, b}
	before, err := deriveCharacterTaskProgressV2(appended)
	if err != nil {
		t.Fatal(err)
	}
	actor := CharacterPhysicalStateV2{SelfExperiences: appended, TaskProgress: before}
	sortCharacterSelfStateV2(&actor)
	after, err := deriveCharacterTaskProgressV2(actor.SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	if samePhysicalValueV2(before, after) {
		t.Fatal("legacy derivation changed; verify signed historical compatibility before updating this fixture")
	}
	t.Logf("same 2 minutes: append-order state=%s source=%s; stored canonical-order state=%s source=%s", before[0].State, before[0].SourceExperienceID, after[0].State, after[0].SourceExperienceID)
	if before[0].Completed != 2 || after[0].Completed != 2 {
		t.Fatal("fixture must isolate ordering, not invented work or overlapping time")
	}
	for _, order := range [][]CharacterSelfExperienceV2{{a, b, c}, {a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}} {
		candidate := CharacterPhysicalStateV2{SelfExperiences: append([]CharacterSelfExperienceV2(nil), order...)}
		sortCharacterSelfStateV2(&candidate)
		derived, err := deriveCharacterTaskProgressV2(candidate.SelfExperiences)
		if err != nil || !samePhysicalValueV2(derived, after) {
			t.Fatalf("canonical input candidate is not permutation invariant: %v", err)
		}
	}
}

func TestSelfProgressReadOnlyCycleAudit(t *testing.T) {
	dir := os.Getenv("NOVEL_SELF_ORDER_AUDIT_CYCLES")
	if dir == "" {
		t.Skip("no explicit read-only cycle audit requested")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no explicit cycle inputs: %v", err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cycle CharacterActivationCycle
		if err := json.Unmarshal(raw, &cycle); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCharacterActivationCycle(cycle); err != nil {
			t.Fatalf("existing cycle %d failed its original digest verification: %v", cycle.Index, err)
		}
		for _, resolution := range cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1].Resolutions {
			actor := *resolution.PostState
			sortCharacterSelfStateV2(&actor)
			wanted, err := deriveCharacterTaskProgressV2(actor.SelfExperiences)
			if err != nil || !samePhysicalValueV2(wanted, actor.TaskProgress) {
				t.Fatalf("canonical-input repair changes stored cycle %d actor %s: %v", cycle.Index, actor.AgentID, err)
			}
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, current) {
			t.Fatal("read-only audit changed source or source was concurrently modified")
		}
		t.Logf("cycle=%d original_digest=%s verified and canonical-input progress identical", cycle.Index, cycle.Digest)
	}
}

// Optional, numeric/identity-only read-only audit. No real sample is part of
// the test suite and no source file is ever rewritten.
func TestSelfProgressReadOnlyStimulusAudit(t *testing.T) {
	path := os.Getenv("NOVEL_SELF_ORDER_AUDIT_STIMULUS")
	if path == "" {
		t.Skip("no explicit read-only stimulus audit requested")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stimulus WorldStimulusPacket
	if err := json.Unmarshal(raw, &stimulus); err != nil {
		t.Fatal(err)
	}
	if stimulus.PhysicalState == nil {
		t.Fatal("stimulus has no physical state")
	}
	state, err := FinalizeWorldPhysicalStateV2(*stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range state.Actors {
		before, err := deriveCharacterTaskProgressV2(actor.SelfExperiences)
		if err != nil || !samePhysicalValueV2(before, actor.TaskProgress) {
			t.Fatalf("stored actor %s cannot validate: %v", actor.AgentID, err)
		}
		sortCharacterSelfStateV2(&actor)
		after, err := deriveCharacterTaskProgressV2(actor.SelfExperiences)
		if err != nil || !samePhysicalValueV2(before, after) {
			t.Fatalf("canonical-input repair would change existing actor %s: %v", actor.AgentID, err)
		}
		t.Logf("actor=%s experiences=%d tasks=%d canonical-before-derive keeps existing exact progress", actor.AgentID, len(actor.SelfExperiences), len(actor.TaskProgress))
	}
}
