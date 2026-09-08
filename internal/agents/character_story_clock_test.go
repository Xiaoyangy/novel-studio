package agents

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func storyClockStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	contract, err := domain.DeriveStoryTimeContract("3章；主线90分钟", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveStoryTimeContract(contract); err != nil {
		t.Fatal(err)
	}
	return st
}

func projectedStoryClock(t *testing.T, next int, day float64) domain.ProjectedPlanningContextV2 {
	t.Helper()
	context := domain.ProjectedPlanningContextV2{
		Version: domain.ProjectedPlanningContextV2Version, GenerationID: "pg2_clock",
		NextChapter: next, ThroughChapter: next - 1, StateRoot: "sha256:" + strings.Repeat("a", 64),
		CumulativeState: []domain.ProjectedPlanningStateFactV2{{
			Category: "timeline", StableID: "clock", Subject: "world", Field: "story_day", ThroughChapter: next - 1,
		}},
	}
	raw, _ := json.Marshal(day)
	context.CumulativeState[0].Value = string(raw)
	digest, err := domain.ComputeProjectedPlanningContextV2Digest(context)
	if err != nil {
		t.Fatal(err)
	}
	context.ContextDigest = digest
	if err := domain.ValidateProjectedPlanningContextV2(context); err != nil {
		t.Fatal(err)
	}
	return context
}

func TestCharacterStoryClockUsesActualPriorEndNotNominalDensity(t *testing.T) {
	st := storyClockStore(t)
	for _, tc := range []struct {
		chapter      int
		startMinutes float64
	}{{1, 0}, {2, 18}, {3, 53.5}} {
		var projected domain.ProjectedPlanningContextV2
		if tc.chapter > 1 {
			projected = projectedStoryClock(t, tc.chapter, tc.startMinutes/1440)
		}
		stimulus, err := buildWorldStimulus(st, "pg2_clock", tc.chapter, ProjectedArcBoundary{}, projected, nil, "now")
		if err != nil {
			t.Fatal(err)
		}
		if stimulus.StoryClock == nil || math.Abs(stimulus.StoryClock.CurrentDay*1440-tc.startMinutes) > 1e-9 {
			t.Fatalf("chapter %d did not inherit actual predecessor end: %+v", tc.chapter, stimulus.StoryClock)
		}
		observation, err := buildCharacterObservation(st, "pg2_clock", tc.chapter, characterAgentProfile{
			Character: domain.Character{Name: "林澄", InitialState: &domain.CharacterInitialState{Time: "T+0", CurrentGoal: "保护凭据", Pressure: "移交截止"}},
			Record:    domain.CharacterAgentRecord{AgentID: "ca_lin", Character: "林澄"},
		}, stimulus, projected, "now")
		if err != nil {
			t.Fatal(err)
		}
		if observation.TimeWindow != stimulus.TimeWindow && tc.chapter > 1 {
			t.Fatalf("stale opening time: %s", observation.TimeWindow)
		}
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "duration_days_max") || strings.Contains(string(raw), "duration_days_min") {
			t.Fatal("arbiter's future time budget leaked into private observation")
		}
	}
}

func TestCharacterStoryClockRejectsUnknownStaleTamperedOrNonfiniteTime(t *testing.T) {
	st := storyClockStore(t)
	if _, _, err := buildCharacterStoryClock(st, 2, domain.ProjectedPlanningContextV2{}); err == nil {
		t.Fatal("chapter mean was treated as actual elapsed time")
	}
	for _, value := range []string{"NaN", "+Inf", "-1", "0.07"} {
		p := projectedStoryClock(t, 2, 18.0/1440)
		p.CumulativeState[0].Value = value
		p.ContextDigest, _ = domain.ComputeProjectedPlanningContextV2Digest(p)
		if _, _, err := buildCharacterStoryClock(st, 2, p); err == nil {
			t.Fatalf("invalid actual time %q accepted", value)
		}
	}
	p := projectedStoryClock(t, 2, 18.0/1440)
	p.CumulativeState[0].Value = "0.01" // leave digest unchanged
	if _, _, err := buildCharacterStoryClock(st, 2, p); err == nil {
		t.Fatal("tampered projected clock accepted")
	}
	if _, _, err := buildCharacterStoryClock(st, 3, projectedStoryClock(t, 2, 18.0/1440)); err == nil {
		t.Fatal("stale predecessor accepted")
	}
}

func TestCharacterStoryClockPreservesExplicitScheduledGapAndLegacyAbsence(t *testing.T) {
	st := storyClockStore(t)
	contract, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil {
		t.Fatal(err)
	}
	contract.ChapterSchedule = []domain.StoryTimeChapterSchedule{{Chapter: 2, StartDay: 20.0 / 1440, EndDay: 50.0 / 1440}}
	if err := st.WorldSim.SaveStoryTimeContract(*contract); err != nil {
		t.Fatal(err)
	}
	clock, _, err := buildCharacterStoryClock(st, 2, projectedStoryClock(t, 2, 18.0/1440))
	if err != nil || math.Abs(clock.CurrentDay*1440-20) > 1e-9 {
		t.Fatalf("authored jump lost: %+v %v", clock, err)
	}
	if _, _, err := buildCharacterStoryClock(st, 2, projectedStoryClock(t, 2, 22.0/1440)); err == nil {
		t.Fatal("schedule rewound actual clock")
	}
	legacy := store.NewStore(t.TempDir())
	clock, _, err = buildCharacterStoryClock(legacy, 2, domain.ProjectedPlanningContextV2{})
	if err != nil || clock != nil {
		t.Fatalf("clockless history changed: %+v %v", clock, err)
	}
}

func TestCharacterStoryClockMarksUnspecifiedDurationAsNominal(t *testing.T) {
	st := store.NewStore(t.TempDir())
	contract, err := domain.DeriveStoryTimeContract("3章", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Older outline-all code relabeled a default without adding an author
	// duration. Its original scale still proves this is only a density estimate.
	contract.Source = domain.StoryTimeSourceOutlineAll
	if err := st.WorldSim.SaveStoryTimeContract(contract); err != nil {
		t.Fatal(err)
	}
	clock, _, err := buildCharacterStoryClock(st, 2, projectedStoryClock(t, 2, 10))
	if err != nil || clock == nil || !clock.NominalBudget || clock.CurrentDay != 10 {
		t.Fatalf("default duration became an author deadline: %+v %v", clock, err)
	}
}
