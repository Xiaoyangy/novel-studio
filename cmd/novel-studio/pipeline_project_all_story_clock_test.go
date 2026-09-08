package main

import (
	"strconv"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPipelineProjectAllDeltaProjectsActualStoryClockWithoutRounding(t *testing.T) {
	plan := domain.ChapterPlan{Chapter: 2, Goal: "把账本带出档案室", Hook: "门外响起脚步"}
	sim := domain.ChapterWorldSimulation{Chapter: 2, StoryTime: &domain.StoryTimeChapterSchedule{
		Chapter: 2, StartDay: 61.0 / 86400, EndDay: 122.0 / 86400,
	}}
	delta, err := pipelineProjectAllDelta(2, sim, plan, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedDeltaV2(delta); err != nil {
		t.Fatal(err)
	}
	var clocks []domain.StateMutationV2
	for _, mutation := range delta.Timeline {
		if mutation.Subject == "world" && mutation.Field == "story_day" {
			clocks = append(clocks, mutation)
		}
	}
	if len(clocks) != 1 || clocks[0].Operation != "advance" || clocks[0].Cause != "world arbitration" ||
		clocks[0].Before != strconv.FormatFloat(sim.StoryTime.StartDay, 'g', -1, 64) ||
		clocks[0].After != strconv.FormatFloat(sim.StoryTime.EndDay, 'g', -1, 64) {
		t.Fatalf("actual second-level time was not projected exactly: %+v", clocks)
	}
	if len(delta.Timeline) != 2 {
		t.Fatalf("adding clock replaced the story outcome transition: %+v", delta.Timeline)
	}
	sim.StoryTime = nil
	legacy, err := pipelineProjectAllDelta(2, sim, plan, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Timeline) != 1 || legacy.Timeline[0].Subject != "chapter" || legacy.Timeline[0].Field != "outcome" {
		t.Fatalf("clockless legacy delta changed: %+v", legacy.Timeline)
	}
}
