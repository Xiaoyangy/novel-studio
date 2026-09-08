package agents

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// A chapter count is not an elapsed-time measurement. The first chapter starts
// at the book origin; every later chapter needs the preceding actual endpoint.
// An explicitly authored chapter schedule can introduce a time jump, but nominal
// days-per-chapter and arc averages never advance this clock.
func buildCharacterStoryClock(st *store.Store, chapter int, projected domain.ProjectedPlanningContextV2) (*domain.StoryClockContext, []string, error) {
	contract, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil || contract == nil {
		return nil, nil, err // Clockless historical projects retain their protocol.
	}
	if err := contract.Validate(); err != nil {
		return nil, nil, fmt.Errorf("character story clock contract: %w", err)
	}
	if chapter < 1 || chapter > contract.TargetChapters {
		return nil, nil, fmt.Errorf("character story clock chapter %d is outside the time contract", chapter)
	}
	current := 0.0
	sources := []string{"story-time-core:" + contract.CoreDigest}
	if len(projected.CumulativeState) > 0 && projected.Version == "" {
		return nil, nil, fmt.Errorf("character story clock requires versioned projected evidence")
	}
	if projected.Version != "" {
		if projected.NextChapter != chapter {
			return nil, nil, fmt.Errorf("character story clock projected chapter mismatch")
		}
		if err := domain.ValidateProjectedPlanningContextV2(projected); err != nil {
			return nil, nil, fmt.Errorf("character story clock projected evidence: %w", err)
		}
	}
	if chapter > 1 {
		found := false
		for _, fact := range projected.CumulativeState {
			if fact.Category != "timeline" || fact.Subject != "world" || fact.Field != "story_day" {
				continue
			}
			if found || fact.ThroughChapter != chapter-1 {
				return nil, nil, fmt.Errorf("character story clock requires one immediate predecessor time fact")
			}
			current, err = strconv.ParseFloat(fact.Value, 64)
			if err != nil || math.IsNaN(current) || math.IsInf(current, 0) || current < 0 {
				return nil, nil, fmt.Errorf("character story clock predecessor time is invalid")
			}
			found = true
			sources = append(sources, "projected-story-clock:"+fact.StableID)
		}
		if !found && projected.Version != "" {
			baseline, err := st.LoadProjectAllAcceptedCharacterBaseline(projected.GenerationID, chapter, projected.StateRoot)
			if err != nil {
				return nil, nil, err
			}
			if baseline != nil {
				if baseline.StoryClock == nil || baseline.StoryTime == nil || baseline.StoryClock.TimeContractCoreDigest != contract.CoreDigest {
					return nil, nil, fmt.Errorf("accepted story-clock snapshot is missing or belongs to another time contract")
				}
				current = baseline.StoryTime.EndDay
				found = true
				sources = append(sources, "accepted-story-clock:"+baseline.SourceOutcomeDigest)
			}
		}
		if !found {
			progress, err := st.Progress.Load()
			if err != nil {
				return nil, nil, err
			}
			if progress == nil || !slices.Contains(progress.CompletedChapters, chapter-1) {
				return nil, nil, fmt.Errorf("chapter %d has no accepted or projected actual story clock; nominal chapter density is not elapsed time", chapter)
			}
			sim, err := st.LoadChapterWorldSimulation(chapter - 1)
			if err != nil {
				return nil, nil, fmt.Errorf("load accepted predecessor story clock: %w", err)
			}
			if sim == nil || sim.StoryTime == nil {
				return nil, nil, fmt.Errorf("chapter %d requires an accepted predecessor with an explicit story-time receipt (legacy migration needs a clock anchor)", chapter)
			}
			var evidence *domain.CharacterAgentEvidenceBundle
			var activationEvidence *domain.CharacterActivationChapterEvidence
			if sim.CharacterAgentProtocol != nil && sim.CharacterAgentProtocol.Version == domain.CharacterAgentDecisionProtocolV2Version {
				cursor, cursorErr := st.ProjectedV2().LoadRealizationCursor()
				if cursorErr != nil {
					return nil, nil, cursorErr
				}
				if cursor != nil && cursor.LastOutcomeReceiptDigest != "" {
					bundle, loadErr := st.LoadAcceptedCharacterAgentBundle(chapter - 1)
					if loadErr != nil {
						return nil, nil, loadErr
					}
					evidence = bundle.CharacterAgentEvidence
					activationEvidence = bundle.CharacterActivationEvidence
				} else {
					if !st.World.HasAcceptedChapterReview(chapter - 1) {
						return nil, nil, fmt.Errorf("interactive v2 story clock lacks a body-bound accepted review")
					}
					if sim.CharacterActivation != nil {
						activationEvidence, err = st.LoadCharacterActivationChapterEvidence(sim.GenerationID, sim.Chapter)
					} else {
						evidence, err = loadCharacterAgentEvidence(st, *sim)
					}
				}
			} else {
				evidence, err = loadCharacterAgentEvidence(st, *sim)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("character story clock predecessor evidence: %w", err)
			}
			priorClock, sourceDigest, err := domain.CharacterSimulationStoryTimeSource(*sim, evidence, activationEvidence)
			if err != nil {
				return nil, nil, err
			}
			if priorClock == nil || priorClock.TimeContractCoreDigest != contract.CoreDigest {
				return nil, nil, fmt.Errorf("character story clock predecessor belongs to a different time contract")
			}
			current = sim.StoryTime.EndDay
			sources = append(sources, "accepted-story-clock:"+sourceDigest)
		}
	}
	for _, scheduled := range contract.ChapterSchedule {
		if scheduled.Chapter != chapter {
			continue
		}
		if scheduled.StartDay+1e-9 < current {
			return nil, nil, fmt.Errorf("chapter %d schedule starts before the preceding actual outcome; refine the schedule through the authorized planning path", chapter)
		}
		current = math.Max(current, scheduled.StartDay)
		sources = append(sources, "story-time-schedule:"+contract.ScheduleDigest)
		break
	}
	clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{
		CurrentDay: current, TimeContractCoreDigest: contract.CoreDigest,
		DurationDaysMin: contract.DurationDaysMin, DurationDaysMax: contract.DurationDaysMax,
		NominalBudget: characterStoryTimeBudgetIsNominal(*contract),
	})
	if err != nil {
		return nil, nil, err
	}
	return &clock, sources, nil
}

func characterStoryTimeBudgetIsNominal(contract domain.StoryTimeContract) bool {
	if contract.Source == domain.StoryTimeSourceExplicit {
		return false
	}
	parsed := domain.ParseStoryScale(contract.SourceEstimatedScale)
	return contract.Source == domain.StoryTimeSourceFallbackNominal ||
		(parsed.DurationDaysMax == 0 && parsed.DurationYearsMax == 0)
}

func characterStoryClockText(day float64) string {
	return "全书开局后 " + strconv.FormatFloat(day*domain.StoryMinutesPerDay, 'f', 6, 64) + " 分钟（已确认的当前时间；不是章均估算）"
}
