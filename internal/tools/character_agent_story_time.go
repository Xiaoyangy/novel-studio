package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Pointers distinguish an explicitly submitted zero (the story's opening)
// from an omitted coordinate. Tool schemas alone do not validate Execute calls.
type arbitrationStoryTimeInput struct {
	Chapter  *int     `json:"chapter"`
	StartDay *float64 `json:"start_day"`
	EndDay   *float64 `json:"end_day"`
}

func (input *arbitrationStoryTimeInput) schedule() (*domain.StoryTimeChapterSchedule, error) {
	if input == nil {
		return nil, nil
	}
	if input.Chapter == nil || input.StartDay == nil || input.EndDay == nil {
		return nil, fmt.Errorf("story_time must explicitly include chapter, start_day and end_day")
	}
	return &domain.StoryTimeChapterSchedule{
		Chapter: *input.Chapter, StartDay: *input.StartDay, EndDay: *input.EndDay,
	}, nil
}
