package domain

import (
	"fmt"
	"sort"
	"strings"
)

func validateSelfEvaluationFactV1(fact CharacterSelfExperienceV2) error {
	evaluation := fact.Evaluation
	if evaluation == nil {
		return nil
	}
	if evaluation.Version != CharacterSelfChronologyPolicyV1 || !strings.HasPrefix(evaluation.GenerationID, PlanningGenerationIDPrefix) || len(evaluation.GenerationID) <= len(PlanningGenerationIDPrefix) || evaluation.Cycle <= 0 || evaluation.Cycle > 64 || evaluation.Ordinal <= 0 || !finiteStoryDay(evaluation.EvaluatedAtDay) || evaluation.EvaluatedAtDay < 0 {
		return fmt.Errorf("self experience has an invalid host evaluation")
	}
	if !characterSourceDigestPatternV2.MatchString(evaluation.ContextDigest) || !characterSourceDigestPatternV2.MatchString(evaluation.StimulusDigest) {
		return fmt.Errorf("self experience evaluation lacks its frozen input binding")
	}
	if fact.EndDay != nil && *fact.EndDay > evaluation.EvaluatedAtDay+1e-12 {
		return fmt.Errorf("self experience was evaluated before its actual execution ended")
	}
	return nil
}

// Actual intervals form the primary order; unexecuted tasks have an explicit
// separate bucket at this evaluation boundary, never an invented interval.
func selfExecutionChronologyLessV1(a, b CharacterSelfExecutionV2) bool {
	aActive, bActive := selfExecutionActiveV2(a.Status), selfExecutionActiveV2(b.Status)
	if aActive != bActive {
		return aActive
	}
	if aActive {
		if *a.EndDay != *b.EndDay {
			return *a.EndDay < *b.EndDay
		}
		if *a.StartDay != *b.StartDay {
			return *a.StartDay < *b.StartDay
		}
	}
	if a.TaskID != b.TaskID {
		return a.TaskID < b.TaskID
	}
	return a.Status < b.Status
}

func canonicalSelfExecutionsV1(executions []CharacterSelfExecutionV2) ([]CharacterSelfExecutionV2, error) {
	out := append([]CharacterSelfExecutionV2(nil), executions...)
	for _, execution := range out {
		if err := validateCharacterSelfExecutionShapeV2(execution); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return selfExecutionChronologyLessV1(out[i], out[j]) })
	byTask := map[string][]CharacterSelfExecutionV2{}
	for _, execution := range out {
		byTask[execution.TaskID] = append(byTask[execution.TaskID], execution)
	}
	for _, segments := range byTask {
		if len(segments) < 2 {
			continue
		}
		for i, segment := range segments {
			if !selfExecutionActiveV2(segment.Status) {
				return nil, fmt.Errorf("one self-task evaluation cannot mix executed segments with unexecuted status or duplicate an unexecuted task")
			}
			if i < len(segments)-1 && segment.Status == "completed" {
				return nil, fmt.Errorf("a multi-segment self task can complete only in its last actual segment")
			}
			if i > 0 && *segment.StartDay < *segments[i-1].EndDay-1e-12 {
				return nil, fmt.Errorf("self task segments overlap within one evaluation")
			}
		}
	}
	return out, nil
}

func legacySelfExperiencesV1(actor CharacterPhysicalStateV2) []CharacterSelfExperienceV2 {
	var legacy []CharacterSelfExperienceV2
	for _, fact := range actor.SelfExperiences {
		if fact.Evaluation == nil {
			legacy = append(legacy, fact)
		}
	}
	// This is the existing signed legacy storage order, not a new time claim.
	sort.Slice(legacy, func(i, j int) bool {
		if legacy[i].Chapter != legacy[j].Chapter {
			return legacy[i].Chapter < legacy[j].Chapter
		}
		return legacy[i].ID < legacy[j].ID
	})
	return legacy
}

func validateSelfChronologyBaselineV1(actor CharacterPhysicalStateV2) error {
	baseline := actor.SelfChronologyBaseline
	if baseline == nil || baseline.Version != CharacterSelfChronologyPolicyV1 || baseline.AgentID != actor.AgentID || baseline.ExperienceCount < 0 || !characterSourceDigestPatternV2.MatchString(baseline.SourcePhysicalRoot) {
		return fmt.Errorf("owner lacks a valid explicit self chronology baseline")
	}
	wanted, err := baselineDigestV1(*baseline)
	if err != nil || wanted != baseline.Digest {
		return fmt.Errorf("self chronology baseline digest mismatch")
	}
	legacy := legacySelfExperiencesV1(actor)
	digest, err := characterAgentDigest(legacy)
	if err != nil || digest != baseline.ExperienceDigest || len(legacy) != baseline.ExperienceCount {
		return fmt.Errorf("legacy self history changed after its explicit chronology baseline")
	}
	old, err := deriveCharacterTaskProgressV2(legacy)
	if err != nil || (!samePhysicalValueV2(old, baseline.TaskProgress) && !(len(old) == 0 && len(baseline.TaskProgress) == 0)) {
		return fmt.Errorf("self chronology baseline does not preserve its verified legacy progress")
	}
	return nil
}

func validateSelfEvaluationGroupsV1(experiences []CharacterSelfExperienceV2) error {
	type evaluationKey struct{ chapter, cycle int }
	groups := map[evaluationKey][]CharacterSelfExperienceV2{}
	for _, fact := range experiences {
		if fact.Evaluation != nil {
			key := evaluationKey{fact.Chapter, fact.Evaluation.Cycle}
			groups[key] = append(groups[key], fact)
		}
	}
	keys := make([]evaluationKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].chapter != keys[j].chapter {
			return keys[i].chapter < keys[j].chapter
		}
		return keys[i].cycle < keys[j].cycle
	})
	previousDay := -1.0
	chapterGenerations := map[int]string{}
	for _, key := range keys {
		facts := groups[key]
		reference := facts[0].Evaluation
		if previous := chapterGenerations[key.chapter]; previous != "" && previous != reference.GenerationID {
			return fmt.Errorf("owner chronology mixes generations within one chapter")
		}
		chapterGenerations[key.chapter] = reference.GenerationID
		if reference.EvaluatedAtDay < previousDay-1e-12 {
			return fmt.Errorf("owner evaluation time moved backwards")
		}
		previousDay = reference.EvaluatedAtDay
		var executions []CharacterSelfExecutionV2
		for _, fact := range facts {
			value := fact.Evaluation
			if value.GenerationID != reference.GenerationID || value.ContextDigest != reference.ContextDigest || value.StimulusDigest != reference.StimulusDigest || value.EvaluatedAtDay != reference.EvaluatedAtDay {
				return fmt.Errorf("owner evaluation contains mixed host contexts")
			}
			executions = append(executions, CharacterSelfExecutionV2{TaskID: fact.TaskID, Status: fact.Status, StartDay: fact.StartDay, EndDay: fact.EndDay})
		}
		ordered, err := canonicalSelfExecutionsV1(executions)
		if err != nil {
			return err
		}
		sort.Slice(facts, func(i, j int) bool { return facts[i].Evaluation.Ordinal < facts[j].Evaluation.Ordinal })
		for i, fact := range facts {
			actual := CharacterSelfExecutionV2{TaskID: fact.TaskID, Status: fact.Status, StartDay: fact.StartDay, EndDay: fact.EndDay}
			if fact.Evaluation.Ordinal != i+1 || !samePhysicalValueV2(actual, ordered[i]) {
				return fmt.Errorf("self evaluation ordinal is not derived from canonical actual execution segments")
			}
		}
	}
	return nil
}

func deriveSelfChronologyProgressV1(actor CharacterPhysicalStateV2) ([]CharacterTaskProgressV2, error) {
	if err := validateSelfChronologyBaselineV1(actor); err != nil {
		return nil, err
	}
	if err := validateSelfEvaluationGroupsV1(actor.SelfExperiences); err != nil {
		return nil, err
	}
	progress := map[string]CharacterTaskProgressV2{}
	for _, task := range cloneSelfProgressV1(actor.SelfChronologyBaseline.TaskProgress) {
		progress[task.TaskID] = task
	}
	ordered := append([]CharacterSelfExperienceV2(nil), actor.SelfExperiences...)
	sortSelfChronologyV1(ordered)
	var intervals [][2]float64
	for _, fact := range ordered {
		if fact.Kind != "work" {
			continue
		}
		active := selfExecutionActiveV2(fact.Status)
		if active {
			intervals = append(intervals, [2]float64{*fact.StartDay, *fact.EndDay})
		}
		if fact.Evaluation == nil {
			continue // Only the explicitly verified baseline supplies old totals.
		}
		task, exists := progress[fact.TaskID]
		if exists && (task.Action != fact.Action || task.Unit != fact.ProgressUnit || !samePhysicalNumberV2(task.Target, fact.ProgressTarget)) {
			return nil, fmt.Errorf("self work experience changed a continuing task's definition")
		}
		if !exists {
			task = CharacterTaskProgressV2{TaskID: fact.TaskID, Action: fact.Action, Unit: fact.ProgressUnit, Target: physicalNumberCopyV2(fact.ProgressTarget), State: "not_started"}
		}
		if active {
			task.Completed += (*fact.EndDay - *fact.StartDay) * 1440
		}
		if fact.Status == "completed" && task.Target != nil && task.Completed < *task.Target && !physicalAmountsCloseV2(task.Completed, *task.Target) {
			return nil, fmt.Errorf("self task cannot declare its duration target completed without actual effective work")
		}
		if fact.Status == "completed" {
			task.State = "completed"
		} else if task.State != "completed" {
			if task.Completed > 0 || active || task.State == "in_progress" {
				task.State = "in_progress"
			} else {
				task.State = "not_started"
			}
		}
		task.LatestAttemptStatus, task.AsOfChapter, task.SourceExperienceID = fact.Status, fact.Chapter, fact.ID
		progress[fact.TaskID] = task
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	for i := 1; i < len(intervals); i++ {
		if intervals[i][0] < intervals[i-1][1]-1e-12 {
			return nil, fmt.Errorf("one actor cannot double-count overlapping effective work intervals")
		}
	}
	var out []CharacterTaskProgressV2
	for _, task := range progress {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}
