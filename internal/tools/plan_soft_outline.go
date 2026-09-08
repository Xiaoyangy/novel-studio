package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func independentSimulationForOutlineAnchors(s *store.Store, chapter int) (*domain.ChapterWorldSimulation, error) {
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return nil, err
	}
	if sim == nil || sim.Version < 2 {
		return nil, nil
	}
	if err := validateStoredCharacterAgentProtocol(s, *sim); err != nil {
		return nil, fmt.Errorf("soft outline replan requires verified character arbitration: %w", err)
	}
	return sim, nil
}

// Remove only the exact clause the old host injected from this same outline.
// Other beats, hard obligations, author requirements and older generations
// are not weakened. An empty result must be completed by the Planner.
func withoutInjectedSoftOutlineBeat(beats []string, coreEvent string) []string {
	legacy := "完整兑现本章大纲核心事件（允许压缩、并场和换序，但终态不可改变）：" + strings.TrimSpace(coreEvent)
	out := make([]string, 0, len(beats))
	for _, beat := range beats {
		if strings.TrimSpace(beat) != legacy {
			out = append(out, beat)
		}
	}
	return out
}
