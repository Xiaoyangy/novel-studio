package domain

import (
	"fmt"
	"math"
)

// StoryClockContext binds an actual chapter-start coordinate to the frozen
// story-time contract. CurrentDay must come from accepted history or the prior
// final arbitration, never from a nominal chapters-to-days estimate.
type StoryClockContext struct {
	CurrentDay             float64 `json:"current_day"`
	TimeContractCoreDigest string  `json:"time_contract_core_digest"`
	DurationDaysMin        float64 `json:"duration_days_min"`
	DurationDaysMax        float64 `json:"duration_days_max"`
	// A fallback density is a planning estimate, never an author deadline.
	NominalBudget bool   `json:"nominal_budget,omitempty"`
	Digest        string `json:"digest"`
}

func ComputeStoryClockContextDigest(clock StoryClockContext) (string, error) {
	clock.Digest = ""
	return characterAgentDigest(clock)
}

func FinalizeStoryClockContext(clock StoryClockContext) (StoryClockContext, error) {
	if err := validateStoryClockContextShape(clock); err != nil {
		return clock, err
	}
	digest, err := ComputeStoryClockContextDigest(clock)
	if err != nil {
		return clock, err
	}
	clock.Digest = digest
	return clock, nil
}

func ValidateStoryClockContext(clock StoryClockContext) error {
	finalized, err := FinalizeStoryClockContext(clock)
	if err != nil {
		return err
	}
	if clock.Digest != finalized.Digest {
		return fmt.Errorf("story clock digest mismatch")
	}
	return nil
}

func validateStoryClockContextShape(clock StoryClockContext) error {
	if err := validatePlanningV2Digest("story clock time_contract_core_digest", clock.TimeContractCoreDigest); err != nil {
		return err
	}
	if !finiteStoryDay(clock.CurrentDay) || !finiteStoryDay(clock.DurationDaysMin) || !finiteStoryDay(clock.DurationDaysMax) ||
		clock.CurrentDay < 0 || clock.DurationDaysMin <= 0 || clock.DurationDaysMax < clock.DurationDaysMin || (!clock.NominalBudget && clock.CurrentDay > clock.DurationDaysMax) {
		return fmt.Errorf("story clock requires finite nonnegative current_day within its positive duration bounds")
	}
	return nil
}

// ValidateStoryTimeForClock validates actual elapsed time supplied by the
// arbiter. A clock makes this coordinate mandatory. The duration minimum is a
// planning promise, not a demand that every ending consume the full budget.
// Clockless historical receipts may omit the new field without changing hashes.
func ValidateStoryTimeForClock(chapter int, storyTime *StoryTimeChapterSchedule, clock *StoryClockContext) error {
	if clock != nil {
		if err := ValidateStoryClockContext(*clock); err != nil {
			return err
		}
		if storyTime == nil {
			return fmt.Errorf("story_time is required when the world stimulus has a story clock")
		}
	}
	if storyTime == nil {
		return nil
	}
	if chapter <= 0 || storyTime.Chapter != chapter {
		return fmt.Errorf("story_time chapter does not match arbitration chapter %d", chapter)
	}
	if !finiteStoryDay(storyTime.StartDay) || !finiteStoryDay(storyTime.EndDay) || storyTime.StartDay < 0 || storyTime.EndDay < storyTime.StartDay {
		return fmt.Errorf("story_time requires finite nonnegative start_day <= end_day")
	}
	if clock != nil {
		if math.Abs(storyTime.StartDay-clock.CurrentDay) > 1e-9 {
			return fmt.Errorf("story_time start_day %.12g does not match host current_day %.12g", storyTime.StartDay, clock.CurrentDay)
		}
		if storyTime.EndDay < clock.CurrentDay {
			return fmt.Errorf("story_time end_day precedes host current_day")
		}
		if !clock.NominalBudget && storyTime.EndDay > clock.DurationDaysMax {
			return fmt.Errorf("story_time end_day %.12g exceeds the hard duration maximum %.12g", storyTime.EndDay, clock.DurationDaysMax)
		}
	}
	return nil
}

// SameStoryTime compares the exact receipt payload, preserving nil for legacy
// evidence. Numeric tolerances belong only at the host-input validation boundary.
func SameStoryTime(left, right *StoryTimeChapterSchedule) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func finiteStoryDay(day float64) bool {
	return !math.IsNaN(day) && !math.IsInf(day, 0)
}
