package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestActivationChapterOpeningBindsClockBeforeValidatingPriorSelfExperience(t *testing.T) {
	_, prior := verifiedInputOrigin(t)
	state := prior.AfterState()
	input := prior.Input()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// The formerly successful first-chapter path keeps its exact packet bytes.
	openingSession, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, prior.Cycle().ChapterContextDigest, *input.Stimulus.PhysicalState, prior.Cycle().StartDay, 8)
	selectionMust(t, err)
	for _, actor := range input.Stimulus.PhysicalState.Actors {
		record, _ := input.Registry.Resolve(actor.Character)
		profile := characterAgentProfile{Record: record, Character: domain.Character{Name: actor.Character, Tier: record.Tier}}
		legacy, err := buildCharacterObservation(st, openingSession.GenerationID, 1, profile, input.Stimulus, domain.ProjectedPlanningContextV2{}, "fixed")
		selectionMust(t, err)
		legacy.CycleContext, err = domain.NewCharacterObservationCycleContext(openingSession)
		selectionMust(t, err)
		legacy, err = domain.FinalizeCharacterObservationPacket(legacy)
		selectionMust(t, err)
		current, err := buildCharacterActivationObservation(st, openingSession, profile, input.Stimulus, domain.ProjectedPlanningContextV2{}, "fixed")
		selectionMust(t, err)
		if !sameCharacterCycleValue(legacy, current) {
			t.Fatal("clock binding order changed an already valid opening packet")
		}
	}
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 2, "sha256:"+strings.Repeat("e", 64), state, prior.Cycle().EndDay, 8)
	selectionMust(t, err)
	stimulus := input.Stimulus
	stimulus.Chapter, stimulus.PhysicalState = 2, &state
	clock := *stimulus.StoryClock
	clock.CurrentDay = session.CurrentDay
	clock, err = domain.FinalizeStoryClockContext(clock)
	selectionMust(t, err)
	stimulus.StoryClock = &clock
	var sources []string
	for _, source := range stimulus.Sources {
		if !strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
			sources = append(sources, source)
		}
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, 2, 1, session.ChapterContextDigest, "")
	selectionMust(t, err)
	stimulus.Sources = append(sources, token)
	stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	selectionMust(t, err)
	stimulus, err = domain.FinalizeWorldStimulusPacket(stimulus)
	selectionMust(t, err)
	before, _ := json.Marshal(state)
	for _, actor := range state.Actors {
		record, ok := input.Registry.Resolve(actor.Character)
		if !ok {
			t.Fatal("fixture lacks stable identity")
		}
		profile := characterAgentProfile{Record: record, Character: domain.Character{Name: actor.Character, Tier: record.Tier}}
		if _, err := buildCharacterObservation(st, session.GenerationID, 2, profile, stimulus, domain.ProjectedPlanningContextV2{}, "fixed"); err == nil || !strings.Contains(err.Error(), "prior authorized owner observation") {
			t.Fatalf("fixture did not reproduce premature finalization: %v", err)
		}
		observation, err := buildCharacterActivationObservation(st, session, profile, stimulus, domain.ProjectedPlanningContextV2{}, "fixed")
		selectionMust(t, err)
		if observation.CycleContext == nil || observation.CycleContext.Index != 1 || observation.CycleContext.CurrentDay != session.CurrentDay || len(observation.SelfExperiences) == 0 {
			t.Fatal("new chapter reset or dropped actual prior self experience")
		}
		for _, fact := range observation.SelfExperiences {
			if fact.Chapter != 1 || fact.Evaluation == nil || fact.Evaluation.EvaluatedAtDay > session.CurrentDay {
				t.Fatal("changed old chapter/evaluation or included a future result")
			}
		}
		if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(stimulus, observation); err != nil {
			t.Fatalf("new chapter observation is not bound to actual owner state: %v", err)
		}
		foreign := session
		foreign.GenerationID = "pg2_foreign"
		if _, err := buildCharacterActivationObservation(st, foreign, profile, stimulus, domain.ProjectedPlanningContextV2{}, "fixed"); err == nil {
			t.Fatal("foreign host session authorized prior experiences")
		}
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("observation construction rewrote predecessor history")
	}
}
