package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfProgressMixedTimeApplySaveReloadPreservesDerivedIdentity(t *testing.T) {
	f := newSelfExperienceFixture(t)
	task := f.proposals[0].SelfTasks[2]
	owner := f.proposals[0].AgentID
	makeFact := func(status string, start, end *float64) CharacterSelfExperienceV2 {
		return CharacterSelfExperienceV2{Chapter: 1, TaskID: task.TaskID, Kind: task.Kind, Action: task.Action,
			Status: status, ResourceIDs: normalizeV2Strings(task.ResourceIDs), StartDay: start, EndDay: end,
			ProgressTarget: physicalNumberCopyV2(task.ProgressTarget), ProgressUnit: "minute"}
	}
	// Valid content-addressed facts with ordered IDs a < b < c, but actual
	// times c < a and no asserted time for b. Opaque IDs are not timestamps.
	findPrior := func(fact CharacterSelfExperienceV2, prefix string) CharacterSelfExperienceV2 {
		for i := 0; i < 4096; i++ {
			fact.SourceProposalDigest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fmt.Sprintf("prior-%s-%d", prefix, i))))
			fact.ID = CharacterSelfExperienceIDV2(owner, fact)
			if strings.HasPrefix(fact.ID, prefix) {
				return fact
			}
		}
		t.Fatal("could not construct deterministic mixed-time fixture")
		return fact
	}
	b := findPrior(makeFact("not_started", nil, nil), "self_8")
	c := findPrior(makeFact("in_progress", selfTestDay(0), selfTestDay(1)), "self_c")
	state := f.stimulus.PhysicalState
	state.Actors[0].SelfExperiences = []CharacterSelfExperienceV2{b, c}
	var err error
	state.Actors[0].TaskProgress, err = deriveCharacterTaskProgressV2(state.Actors[0].SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	f.observations[0].SelfExperiences, f.observations[0].TaskProgress, err = BuildCharacterSelfObservationV2(*state, owner)
	if err != nil {
		t.Fatal(err)
	}
	cycleToken, err := CharacterActivationCycleSourceToken(f.stimulus.GenerationID, 1, 2, "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.Sources = append(f.stimulus.Sources, cycleToken)
	clock, err := FinalizeStoryClockContext(StoryClockContext{CurrentDay: *selfTestDay(1), DurationDaysMin: 1, DurationDaysMax: 1, TimeContractCoreDigest: "sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.StoryClock = &clock
	for i := range f.observations {
		f.observations[i].CycleContext = &CharacterObservationCycleContext{Version: CharacterObservationCyclePolicy, Index: 2,
			ChapterContextDigest: "sha256:" + strings.Repeat("a", 64), PreviousCycleDigest: "sha256:" + strings.Repeat("b", 64), CurrentDay: *selfTestDay(1)}
	}
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{task}
	f.receipt.StoryTime = &StoryTimeChapterSchedule{Chapter: 1, StartDay: *selfTestDay(1), EndDay: *selfTestDay(2)}
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: task.TaskID, Status: "in_progress", StartDay: selfTestDay(1), EndDay: selfTestDay(2)}}
	priorActor := state.Actors[0]
	f.receipt.Resolutions[0].PostState = &priorActor
	f.receipt.ResourceSettlements = nil
	rebindPhysicalTestStimulus(t, &f)
	a := makeFact("in_progress", selfTestDay(1), selfTestDay(2))
	for i := 0; i < 4096; i++ {
		f.proposals[0].SubmittedAt = fmt.Sprintf("2026-01-01T00:00:00.%09dZ", i)
		rebindPhysicalTestProposals(t, &f)
		a.SourceProposalDigest = f.proposals[0].Digest
		a.ID = CharacterSelfExperienceIDV2(owner, a)
		if strings.HasPrefix(a.ID, "self_0") {
			break
		}
	}
	if !strings.HasPrefix(a.ID, "self_0") {
		t.Fatal("could not bind actual new execution to a valid proposal digest")
	}
	appended, err := deriveCharacterTaskProgressV2([]CharacterSelfExperienceV2{b, c, a})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := deriveCharacterTaskProgressV2([]CharacterSelfExperienceV2{a, b, c})
	if err != nil || samePhysicalValueV2(appended, canonical) {
		t.Fatalf("fixture no longer exercises append/canonical drift: %v", err)
	}
	before, _ := json.Marshal(f.stimulus)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if !samePhysicalValueV2(after.Actors[0].TaskProgress, canonical) || !physicalAmountsCloseV2(after.Actors[0].TaskProgress[0].Completed, 2) {
		t.Fatal("actual application did not use the same canonical derivation as replay")
	}
	if len(after.Actors[0].SelfExperiences) != 3 || after.Actors[0].SelfExperiences[1].ID != b.ID || after.Actors[0].SelfExperiences[1].EndDay != nil {
		t.Fatal("compatibility repair changed an old fact or invented an unknown time")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorldArbitrationReceipt
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	verified, err := FinalizeWorldArbitrationReceipt(restored, f.stimulus, f.activation, f.proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, _ := json.Marshal(verified)
	if !bytes.Equal(raw, reencoded) || receipt.Digest != verified.Digest {
		t.Fatal("receipt changed after save/reload verification")
	}
	replayed, err := ApplyArbitrationPhysicalStateV2(verified, f.stimulus, f.proposals...)
	if err != nil || !samePhysicalValueV2(after, replayed) {
		t.Fatalf("post-state changed during replay: %v", err)
	}
	unchanged, _ := json.Marshal(f.stimulus)
	if !bytes.Equal(before, unchanged) {
		t.Fatal("application mutated the frozen before state")
	}
}
