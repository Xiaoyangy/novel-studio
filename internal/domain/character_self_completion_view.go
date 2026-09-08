package domain

import (
	"fmt"
	"math"
	"strings"
)

// Opt-in observation selection, not a new execution result or world fact.
const CharacterSelfCompletionViewPolicyV1 = "character-self-completion-view:receipt.v1"

func HasCharacterSelfCompletionViewPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterSelfCompletionViewPolicyV1)
}

// The absent branch must remain byte-identical for frozen old observations.
func BuildCharacterSelfObservationForSourcesV2(state WorldPhysicalStateV2, agentID string, sources []string) ([]CharacterSelfExperienceV2, []CharacterTaskProgressV2, error) {
	if !HasCharacterSelfCompletionViewPolicyV1(sources) {
		return BuildCharacterSelfObservationV2(state, agentID)
	}
	state, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return nil, nil, err
	}
	for _, actor := range state.Actors {
		if actor.AgentID != agentID {
			continue
		}
		// Every current task summary is mandatory, including completed work.
		// Completed rows retain their original source ID without repeating the
		// full source event; the binding validator authenticates it in full state.
		progress := actor.TaskProgress
		required := map[string]bool{}
		for _, task := range progress {
			if task.State != "completed" {
				required[task.SourceExperienceID] = true
			}
		}
		operations, err := selectCharacterOperationalObservationsV1(actor.OperationalObservations)
		if err != nil {
			return nil, nil, err
		}
		for _, operation := range operations {
			required[operation.SourceExperienceID] = true
		}
		for _, holding := range actor.Resources {
			if holding.Perception.Kind != "unaware" && holding.KnownPlacement != nil {
				required[holding.KnownPlacement.SourceExperienceID] = true
			}
		}
		selected := map[string]bool{}
		for _, experience := range actor.SelfExperiences {
			if required[experience.ID] {
				selected[experience.ID] = true
			}
		}
		if len(selected) != len(required) {
			return nil, nil, fmt.Errorf("completed self-task view lacks an exact owner source experience")
		}
		materialize := func() []CharacterSelfExperienceV2 {
			var out []CharacterSelfExperienceV2
			for _, experience := range actor.SelfExperiences {
				if selected[experience.ID] {
					out = append(out, experience)
				}
			}
			return out
		}
		experiences := materialize()
		if err := validateSelfObservationBudgetV2(experiences, progress); err != nil {
			return nil, nil, fmt.Errorf("mandatory owner task completion/progress and source anchors exceed self-observation budget; no completed or unfinished task was dropped: %w", err)
		}
		// Recent non-anchor history is optional. Reduce this tail before failing
		// a budget; never evict a mandatory completion in order to fit the tail.
		for i, recent := len(actor.SelfExperiences)-1, 0; i >= 0 && recent < CharacterSelfObservationRecentLimitV2; i-- {
			experience := actor.SelfExperiences[i]
			if selected[experience.ID] || (actor.SelfChronologyBaseline != nil && experience.Evaluation == nil) {
				continue
			}
			recent++
			selected[experience.ID] = true
			candidate := materialize()
			if err := validateSelfObservationBudgetV2(candidate, progress); err != nil {
				delete(selected, experience.ID)
				continue
			}
			experiences = candidate
		}
		return experiences, progress, nil
	}
	return nil, nil, fmt.Errorf("completed self-task view has no matching owner")
}

// Shape only, never source authority. Dispatch/admission additionally binds
// every complete row and its original source event against the full owner state.
func validCompactSelfCompletionV1(task CharacterTaskProgressV2, observation CharacterObservationPacket) bool {
	return HasCharacterSelfCompletionViewPolicyV1(observation.Sources) && task.State == "completed" &&
		physicalIdentityV2(task.TaskID) && strings.TrimSpace(task.Action) != "" && task.Unit == "minute" &&
		task.AsOfChapter > 0 && task.AsOfChapter <= observation.Chapter && task.Completed >= 0 && !math.IsNaN(task.Completed) && !math.IsInf(task.Completed, 0) &&
		(task.Target == nil || (physicalAmountV2(task.Target) && *task.Target > 0)) &&
		len(task.SourceExperienceID) == 69 && strings.HasPrefix(task.SourceExperienceID, "self_") && strings.Trim(task.SourceExperienceID[5:], "0123456789abcdef") == "" &&
		(task.LatestAttemptStatus == "" || task.LatestAttemptStatus == "completed" || task.LatestAttemptStatus == "in_progress" || task.LatestAttemptStatus == "blocked" || task.LatestAttemptStatus == "not_started")
}

func validateCompactSelfCompletionSourcesV1(state WorldPhysicalStateV2, observation CharacterObservationPacket) error {
	if !HasCharacterSelfCompletionViewPolicyV1(observation.Sources) {
		return nil
	}
	for _, actor := range state.Actors {
		if actor.AgentID != observation.AgentID || actor.Character != observation.Character {
			continue
		}
		byTask := map[string]CharacterTaskProgressV2{}
		bySource := map[string]CharacterSelfExperienceV2{}
		for _, task := range actor.TaskProgress {
			byTask[task.TaskID] = task
		}
		for _, source := range actor.SelfExperiences {
			bySource[source.ID] = source
		}
		for _, task := range observation.TaskProgress {
			if task.State != "completed" {
				continue
			}
			actual, exists := byTask[task.TaskID]
			source, found := bySource[task.SourceExperienceID]
			if !exists || !found || !samePhysicalValueV2(actual, task) || !validCompactSelfCompletionV1(task, observation) {
				return fmt.Errorf("completed self-task summary differs from its exact owner state/source")
			}
			// Reuse the original source-event identity, timing and summary rules,
			// including previous-cycle/future-knowledge checks. Supplying the real
			// source here means the compact shape exception is never used here.
			bound := observation
			bound.SelfExperiences = []CharacterSelfExperienceV2{source}
			bound.TaskProgress = []CharacterTaskProgressV2{task}
			bound.ResourceViews = nil
			if err := validateCharacterSelfObservationV2(bound); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("completed self-task summary has no matching full-state owner")
}
