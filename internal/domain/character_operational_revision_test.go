package domain

import (
	"encoding/json"
	"testing"
)

func TestOperationalRevisionPreservesLegacyDeviceReceiptAndExistingHistory(t *testing.T) {
	f := newOperationalObservationFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	// Independently computed using HEAD's pre-fix implementation in an archive.
	const legacy = "sha256:27c8d96bbd7a92976d3c750fe5f324a56d3a31c0f537720e7cc85bbb5b37e3a8"
	if r.Digest != legacy {
		t.Fatalf("valid equipment receipt bytes changed: %s", r.Digest)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Actors[0].OperationalObservations) != 1 {
		t.Fatal("fixture lacks a real source-derived prior operational observation")
	}
	before, _ := json.Marshal(state)
	current := r.StoryTime.EndDay
	stimulus := WorldStimulusPacket{Sources: []string{CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1}, StoryClock: &StoryClockContext{CurrentDay: current}}
	revision := WorldArbitrationReceipt{Round: 1, HardContractStatus: "feasible", StoryTime: &StoryTimeChapterSchedule{StartDay: current, EndDay: current},
		Conflicts: []WorldArbitrationConflict{{Kind: "rule", AffectedAgentIDs: []string{state.Actors[0].AgentID}, Feedback: "接口请求不适用"}}}
	if !operationalRequestCanReviseV1(revision, stimulus, state, state, state.Actors[0].AgentID) {
		t.Fatal("strict no-op revision required erasing already verified history")
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("no-op eligibility changed existing operational history")
	}
	changed := continuationCloneV1(state)
	changed.Actors[0].OperationalObservations = nil
	if operationalRequestCanReviseV1(revision, stimulus, state, changed, state.Actors[0].AgentID) {
		t.Fatal("revision erased a previous actual operational observation")
	}
}
