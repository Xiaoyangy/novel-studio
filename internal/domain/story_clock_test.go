package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func storyClockForTest(t *testing.T, currentDay float64) StoryClockContext {
	t.Helper()
	contract, err := DeriveStoryTimeContract("3章，90分钟", 3)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := FinalizeStoryClockContext(StoryClockContext{
		CurrentDay: currentDay, TimeContractCoreDigest: contract.CoreDigest,
		DurationDaysMin: contract.DurationDaysMin, DurationDaysMax: contract.DurationDaysMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	return clock
}

func TestStoryClockContextRejectsTamperedAndNonfiniteCoordinates(t *testing.T) {
	clock := storyClockForTest(t, 61.0/86400)
	if err := ValidateStoryClockContext(clock); err != nil {
		t.Fatal(err)
	}
	tampered := clock
	tampered.CurrentDay += 1.0 / 86400
	if err := ValidateStoryClockContext(tampered); err == nil {
		t.Fatal("changed actual current time retained a valid digest")
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), 1} {
		changed := clock
		changed.CurrentDay = invalid
		if _, err := FinalizeStoryClockContext(changed); err == nil {
			t.Fatalf("invalid current day accepted: %v", invalid)
		}
	}
}

func TestNominalStoryBudgetCannotBecomeHardDeadline(t *testing.T) {
	clock := storyClockForTest(t, 0)
	clock.NominalBudget = true
	clock.CurrentDay = 3 // Beyond the illustrative estimate, not a hard deadline.
	clock, err := FinalizeStoryClockContext(clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStoryTimeForClock(2, &StoryTimeChapterSchedule{Chapter: 2, StartDay: 3, EndDay: 4}, &clock); err != nil {
		t.Fatalf("fallback density blocked a valid actual elapsed time: %v", err)
	}
}

func TestWorldArbitrationClockRequiresBoundActualTime(t *testing.T) {
	stimulus, activation, _, proposal := validCharacterAgentProtocolForTest(t)
	clock := storyClockForTest(t, 61.0/86400)
	stimulus.StoryClock = &clock
	var err error
	stimulus, err = FinalizeWorldStimulusPacket(stimulus)
	if err != nil {
		t.Fatal(err)
	}
	base := validCharacterArbitrationForTest(stimulus, activation, proposal)
	valid := StoryTimeChapterSchedule{Chapter: stimulus.Chapter, StartDay: clock.CurrentDay, EndDay: 88.0 / 1440}
	for _, tc := range []struct {
		name string
		time *StoryTimeChapterSchedule
		want string
	}{
		{"missing", nil, "required"},
		{"wrong chapter", &StoryTimeChapterSchedule{Chapter: 2, StartDay: valid.StartDay, EndDay: valid.EndDay}, "chapter"},
		{"nominal pretending actual", &StoryTimeChapterSchedule{Chapter: 3, StartDay: 60.0 / 1440, EndDay: valid.EndDay}, "host current_day"},
		{"backwards", &StoryTimeChapterSchedule{Chapter: 3, StartDay: valid.StartDay, EndDay: 0}, "start_day <= end_day"},
		{"over hard max", &StoryTimeChapterSchedule{Chapter: 3, StartDay: valid.StartDay, EndDay: 90.01 / 1440}, "hard duration maximum"},
		{"nan", &StoryTimeChapterSchedule{Chapter: 3, StartDay: math.NaN(), EndDay: valid.EndDay}, "finite"},
		{"infinity", &StoryTimeChapterSchedule{Chapter: 3, StartDay: valid.StartDay, EndDay: math.Inf(1)}, "finite"},
		{"negative", &StoryTimeChapterSchedule{Chapter: 3, StartDay: -1, EndDay: 0}, "nonnegative"},
		{"sub tolerance rollback", &StoryTimeChapterSchedule{Chapter: 3, StartDay: valid.StartDay - 5e-10, EndDay: valid.StartDay - 5e-10}, "precedes host current_day"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := base
			receipt.StoryTime = tc.time
			if _, err := FinalizeWorldArbitrationReceipt(receipt, stimulus, activation, []CharacterDecisionProposal{proposal}, 1); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	base.StoryTime = &valid
	finalized, err := FinalizeWorldArbitrationReceipt(base, stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil || !SameStoryTime(finalized.StoryTime, &valid) {
		t.Fatalf("valid second-level actual time / T88 ending rejected: %+v, %v", finalized.StoryTime, err)
	}
	instant := valid
	instant.EndDay = instant.StartDay
	if err := ValidateStoryTimeForClock(3, &instant, &clock); err != nil {
		t.Fatalf("zero-duration instantaneous result rejected: %v", err)
	}
	rounded := base
	roundedTime := valid
	roundedTime.StartDay += 5e-10
	rounded.StoryTime = &roundedTime
	normalized, err := FinalizeWorldArbitrationReceipt(rounded, stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil || normalized.StoryTime.StartDay != clock.CurrentDay || normalized.Digest != finalized.Digest {
		t.Fatalf("input rounding changed the exact host start or receipt: %+v, %v", normalized.StoryTime, err)
	}
	rounded.Digest = finalized.Digest
	if _, err := FinalizeWorldArbitrationReceipt(rounded, stimulus, activation, []CharacterDecisionProposal{proposal}, 1); err == nil {
		t.Fatal("persisted receipt tampering was hidden by input-rounding normalization")
	}
	changed := finalized
	changedTime := valid
	changedTime.EndDay += 1.0 / 86400
	changed.StoryTime = &changedTime
	digest, err := ComputeWorldArbitrationReceiptDigest(changed)
	if err != nil || digest == finalized.Digest {
		t.Fatalf("actual story-time change was not committed to receipt digest: %v", err)
	}
}

func TestClocklessLegacyProtocolJSONAndDigestsRemainCompatible(t *testing.T) {
	stimulus, activation, _, proposal := validCharacterAgentProtocolForTest(t)
	receipt, err := FinalizeWorldArbitrationReceipt(validCharacterArbitrationForTest(stimulus, activation, proposal), stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{stimulus, receipt, ChapterWorldSimulation{Version: 1, Chapter: 1, TimeWindow: "当晚"}, ChapterWorldSimulation{Version: 2, Chapter: 1, TimeWindow: "当晚"}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "story_time") || strings.Contains(string(raw), "story_clock") {
			t.Fatalf("optional clock changed historical serialized payload: %s", raw)
		}
	}
	// Freeze the original pre-clock stimulus shape independently of the new
	// type. Its content hash must be identical when the optional clock is nil.
	legacyStimulus := struct {
		Version      string `json:"version"`
		GenerationID string `json:"generation_id"`
		Chapter      int    `json:"chapter"`
		TimeWindow   string `json:"time_window"`
		Digest       string `json:"digest"`
	}{stimulus.Version, stimulus.GenerationID, stimulus.Chapter, stimulus.TimeWindow, ""}
	want, err := characterAgentDigest(legacyStimulus)
	if err != nil || stimulus.Digest != want {
		t.Fatalf("clockless stimulus digest changed: got %s want %s err=%v", stimulus.Digest, want, err)
	}
	if err := ValidateStoryTimeForClock(stimulus.Chapter, nil, nil); err != nil {
		t.Fatalf("clockless legacy receipt was made invalid: %v", err)
	}
}
