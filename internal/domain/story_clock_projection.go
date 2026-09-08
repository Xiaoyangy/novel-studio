package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// The world/story_day mutation is the one actual clock coordinate consumed by
// the next chapter. It must be an exact projection of this chapter's receipt,
// not a separately authored estimate or a convenience-ledger override.
func validateProjectedStoryTimeV2(simulation ChapterWorldSimulation, delta ProjectedDelta) error {
	var clocks []StateMutationV2
	for _, mutation := range delta.Timeline {
		if strings.TrimSpace(mutation.Subject) == "world" && strings.TrimSpace(mutation.Field) == "story_day" {
			clocks = append(clocks, mutation)
		}
	}
	if simulation.StoryTime == nil {
		if len(clocks) != 0 {
			return fmt.Errorf("world/story_day timeline requires simulation story_time")
		}
		return nil
	}
	if err := ValidateStoryTimeForClock(simulation.Chapter, simulation.StoryTime, nil); err != nil {
		return err
	}
	if len(clocks) != 1 {
		return fmt.Errorf("story_time requires exactly one world/story_day timeline mutation, got %d", len(clocks))
	}
	clock := clocks[0]
	before, beforeErr := strconv.ParseFloat(strings.TrimSpace(clock.Before), 64)
	after, afterErr := strconv.ParseFloat(strings.TrimSpace(clock.After), 64)
	if strings.TrimSpace(clock.Object) != "" || strings.TrimSpace(clock.Operation) != "advance" ||
		beforeErr != nil || afterErr != nil || !finiteStoryDay(before) || !finiteStoryDay(after) ||
		before != simulation.StoryTime.StartDay || after != simulation.StoryTime.EndDay {
		return fmt.Errorf("world/story_day timeline differs from simulation story_time")
	}
	return nil
}
