package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const CharacterSelfExperiencePolicyV2 = "character-self-experience:receipt.v1"
const CharacterPerceivedLabelMaxRunesV2 = 160
const CharacterSelfObservationRecentLimitV2 = 8
const CharacterSelfObservationExperienceLimitV2 = 32
const CharacterSelfObservationByteLimitV2 = 16 * 1024

func validateCharacterPerceivedLabelV2(label string) error {
	if utf8.RuneCountInString(strings.TrimSpace(label)) > CharacterPerceivedLabelMaxRunesV2 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return fmt.Errorf("perceived_label must be a single-line static identity/use/access label of at most 160 characters")
	}
	return nil
}

// SelfTasks are an actor's own structured intentions, not world outcomes.
// TaskID remains stable across chapters when continuing the same work.
type CharacterSelfTaskV2 struct {
	OutputRequests      []CharacterWorkOutputRequestV1             `json:"output_requests,omitempty"`
	ObservationRequests []CharacterOperationalObservationRequestV1 `json:"observation_requests,omitempty"`
	TaskID              string                                     `json:"task_id"`
	Kind                string                                     `json:"kind"`
	Action              string                                     `json:"action"`
	ResourceIDs         []string                                   `json:"resource_ids,omitempty"`
	ProgressTarget      *float64                                   `json:"progress_target,omitempty"`
	ProgressUnit        string                                     `json:"progress_unit,omitempty"`
	KnowledgeRefs       []string                                   `json:"knowledge_refs"`
}

type CharacterSelfExecutionV2 struct {
	OutputResults      []CharacterWorkOutputResultV1             `json:"output_results,omitempty"`
	ObservationResults []CharacterOperationalObservationResultV1 `json:"observation_results,omitempty"`
	TaskID             string                                    `json:"task_id"`
	Status             string                                    `json:"status"`
	StartDay           *float64                                  `json:"start_day,omitempty"`
	EndDay             *float64                                  `json:"end_day,omitempty"`
}

// The host materializes these records exclusively from one owner's submitted
// task and the final receipt's explicit execution. No arbiter prose or world
// resource balance is a source for their text or progress.
type CharacterSelfExperienceV2 struct {
	Evaluation           *CharacterSelfEvaluationV1 `json:"evaluation,omitempty"`
	ID                   string                     `json:"id"`
	Chapter              int                        `json:"chapter"`
	TaskID               string                     `json:"task_id"`
	Kind                 string                     `json:"kind"`
	Action               string                     `json:"action"`
	Status               string                     `json:"status"`
	Location             string                     `json:"location,omitempty"`
	ResourceIDs          []string                   `json:"resource_ids,omitempty"`
	StartDay             *float64                   `json:"start_day,omitempty"`
	EndDay               *float64                   `json:"end_day,omitempty"`
	ProgressTarget       *float64                   `json:"progress_target,omitempty"`
	ProgressUnit         string                     `json:"progress_unit,omitempty"`
	SourceProposalDigest string                     `json:"source_proposal_digest"`
}

type CharacterTaskProgressV2 struct {
	LatestAttemptStatus string   `json:"latest_attempt_status,omitempty"`
	TaskID              string   `json:"task_id"`
	Action              string   `json:"action"`
	Unit                string   `json:"unit"`
	Target              *float64 `json:"target,omitempty"`
	Completed           float64  `json:"completed"`
	State               string   `json:"state"`
	AsOfChapter         int      `json:"as_of_chapter"`
	SourceExperienceID  string   `json:"source_experience_id"`
}

type CharacterResourcePlacementV2 struct {
	Kind               string `json:"kind"`
	Location           string `json:"location"`
	AsOfChapter        int    `json:"as_of_chapter"`
	SourceExperienceID string `json:"source_experience_id"`
}

func HasCharacterSelfExperiencePolicyV2(sources []string) bool {
	for _, source := range sources {
		if strings.TrimSpace(source) == CharacterSelfExperiencePolicyV2 {
			return true
		}
	}
	return false
}

func planningV2RenderContextProhibitedKeysForBundle(bundle ProjectedChapterBundle) map[string]struct{} {
	keys := planningV2RenderContextProhibitedKeys()
	if bundle.HasCharacterSelfExperienceEvidence() {
		// Owner-private structured history is not a public prose channel.
		// The planner must select lawful POV facts, never mirror all actors.
		for _, key := range []string{"self_tasks", "self_executions", "self_experiences", "task_progress", "known_placement", "operational_observations", "observation_requests", "observation_results"} {
			keys[key] = struct{}{}
		}
	}
	return keys
}

// Prepare is used only when the host explicitly selects the new policy. It
// never edits author source files or extracts a label from historical prose.
func PrepareCharacterSelfExperienceStateV2(state WorldPhysicalStateV2) (WorldPhysicalStateV2, error) {
	out, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return out, err
	}
	for i := range out.Actors {
		for j := range out.Actors[i].Resources {
			holding := &out.Actors[i].Resources[j]
			if holding.Perception.Kind != "unaware" && strings.TrimSpace(holding.PerceivedLabel) == "" {
				holding.PerceivedLabel = UnidentifiedResourceNameV2
			}
		}
	}
	return out, nil
}

func BuildCharacterSelfObservationV2(state WorldPhysicalStateV2, agentID string) ([]CharacterSelfExperienceV2, []CharacterTaskProgressV2, error) {
	state, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return nil, nil, err
	}
	for _, actor := range state.Actors {
		if actor.AgentID == agentID {
			// Never silently evict older unfinished work: its complete compact
			// baseline is mandatory, regardless of recent activity ordering.
			var progress []CharacterTaskProgressV2
			required := map[string]bool{}
			operations, err := selectCharacterOperationalObservationsV1(actor.OperationalObservations)
			if err != nil {
				return nil, nil, err
			}
			for _, operation := range operations {
				required[operation.SourceExperienceID] = true
			}
			for _, task := range actor.TaskProgress {
				if task.State != "completed" {
					progress = append(progress, task)
					required[task.SourceExperienceID] = true
				}
			}
			for _, holding := range actor.Resources {
				if holding.Perception.Kind != "unaware" && holding.KnownPlacement != nil {
					required[holding.KnownPlacement.SourceExperienceID] = true
				}
			}
			if len(required) > CharacterSelfObservationExperienceLimitV2 {
				return nil, nil, fmt.Errorf("active self-task/placement proofs exceed observation budget; focused context is required, no unfinished task was dropped")
			}
			selected := map[string]bool{}
			for id := range required {
				selected[id] = true
			}
			for i, recent := len(actor.SelfExperiences)-1, 0; i >= 0 && recent < CharacterSelfObservationRecentLimitV2 && len(selected) < CharacterSelfObservationExperienceLimitV2; i-- {
				if actor.SelfChronologyBaseline != nil && actor.SelfExperiences[i].Evaluation == nil {
					continue // Unknown historical order is not claimed as recent chronology.
				}
				id := actor.SelfExperiences[i].ID
				if !selected[id] {
					selected[id] = true
					recent++
				}
			}
			var experiences []CharacterSelfExperienceV2
			for _, experience := range actor.SelfExperiences {
				if selected[experience.ID] {
					experiences = append(experiences, experience)
				}
			}
			if err := validateSelfObservationBudgetV2(experiences, progress); err != nil {
				return nil, nil, err
			}
			return experiences, progress, nil
		}
	}
	return nil, nil, fmt.Errorf("self experience observation has no matching owner")
}

func validateSelfObservationBudgetV2(experiences []CharacterSelfExperienceV2, progress []CharacterTaskProgressV2) error {
	if len(experiences) > CharacterSelfObservationExperienceLimitV2 {
		return fmt.Errorf("self experience observation exceeds its bounded history budget")
	}
	raw, err := json.Marshal(struct {
		Experiences []CharacterSelfExperienceV2 `json:"experiences"`
		Progress    []CharacterTaskProgressV2   `json:"progress"`
	}{experiences, progress})
	if err != nil {
		return err
	}
	if len(raw) > CharacterSelfObservationByteLimitV2 {
		return fmt.Errorf("self observation exceeds its byte budget; focused context is required, no unfinished task was silently dropped")
	}
	return nil
}

func FormatCharacterSelfExperiencesV2(experiences []CharacterSelfExperienceV2, progress []CharacterTaskProgressV2) []string {
	var out []string
	for _, experience := range experiences {
		verb := map[string]string{"not_started": "尚未开始", "blocked": "本轮未能执行", "in_progress": "实际执行了部分", "completed": "已完成此步骤"}[experience.Status]
		text := fmt.Sprintf("本人经历（第%d章，%s）：%s；%s", experience.Chapter, experience.TaskID, experience.Action, verb)
		if experience.StartDay != nil && experience.EndDay != nil {
			text += fmt.Sprintf("；实际区间T+%.6g至T+%.6g分钟", *experience.StartDay*1440, *experience.EndDay*1440)
		}
		if experience.Evaluation != nil {
			text += fmt.Sprintf("；第%d次评估于T+%.6g分钟确认本次状态（评估时刻不等于执行工时）", experience.Evaluation.Cycle, experience.Evaluation.EvaluatedAtDay*1440)
		}
		if experience.Location != "" {
			text += "；已确认位置：" + experience.Location
		}
		out = append(out, text)
	}
	for _, task := range progress {
		text := fmt.Sprintf("本人任务进度（%s）：%s；累计有效工时%.6g分钟；状态%s", task.TaskID, task.Action, task.Completed, task.State)
		if task.LatestAttemptStatus != "" {
			text += "；最近一次尝试状态" + task.LatestAttemptStatus + "（不清零既有工时）"
		}
		if task.Target != nil {
			text += fmt.Sprintf("；本人申报目标%.6g分钟，尚余%.6g分钟；达到工时不等于作业结果合格", *task.Target, math.Max(0, *task.Target-task.Completed))
		}
		out = append(out, text)
	}
	return out
}

func sortCharacterSelfStateV2(actor *CharacterPhysicalStateV2) {
	if actor.SelfChronologyBaseline != nil {
		sortSelfChronologyV1(actor.SelfExperiences)
	} else {
		sort.Slice(actor.SelfExperiences, func(i, j int) bool {
			if actor.SelfExperiences[i].Chapter != actor.SelfExperiences[j].Chapter {
				return actor.SelfExperiences[i].Chapter < actor.SelfExperiences[j].Chapter
			}
			return actor.SelfExperiences[i].ID < actor.SelfExperiences[j].ID
		})
	}
	sort.Slice(actor.TaskProgress, func(i, j int) bool { return actor.TaskProgress[i].TaskID < actor.TaskProgress[j].TaskID })
}

func validateCharacterSelfStateV2(actor CharacterPhysicalStateV2, catalog map[string]WorldResourceBalanceV2) error {
	byID := map[string]CharacterSelfExperienceV2{}
	for _, experience := range actor.SelfExperiences {
		if experience.Evaluation != nil && actor.SelfChronologyBaseline == nil {
			return fmt.Errorf("evaluated self experience requires an explicit host chronology baseline")
		}
		if err := validateCharacterSelfExperienceShapeV2(actor.AgentID, experience, catalog); err != nil {
			return err
		}
		if _, duplicate := byID[experience.ID]; duplicate {
			return fmt.Errorf("self experience is duplicated")
		}
		byID[experience.ID] = experience
	}
	var wanted []CharacterTaskProgressV2
	var err error
	if actor.SelfChronologyBaseline != nil {
		wanted, err = deriveSelfChronologyProgressV1(actor)
	} else {
		wanted, err = deriveCharacterTaskProgressV2(actor.SelfExperiences)
	}
	if err != nil {
		return err
	}
	if !samePhysicalValueV2(wanted, actor.TaskProgress) && !(len(wanted) == 0 && len(actor.TaskProgress) == 0) {
		return fmt.Errorf("owner work progress is not derived from its actual self experiences")
	}
	return validateCharacterKnownPlacementsV2(actor.Resources, byID)
}

func validateCharacterSelfExperienceShapeV2(agentID string, experience CharacterSelfExperienceV2, catalog map[string]WorldResourceBalanceV2) error {
	if err := validateSelfEvaluationFactV1(experience); err != nil {
		return err
	}
	if experience.Chapter <= 0 || !physicalIdentityV2(experience.TaskID) || strings.TrimSpace(experience.Action) == "" || !characterSourceDigestPatternV2.MatchString(experience.SourceProposalDigest) || experience.ID != CharacterSelfExperienceIDV2(agentID, experience) {
		return fmt.Errorf("self experience has invalid owner identity, source or derived ID")
	}
	if err := validateCharacterSelfTaskShapeV2(CharacterSelfTaskV2{Kind: experience.Kind, Action: experience.Action, ResourceIDs: experience.ResourceIDs, ProgressUnit: experience.ProgressUnit, ProgressTarget: experience.ProgressTarget}); err != nil {
		return err
	}
	if experience.Kind == "work" && experience.ProgressUnit != "minute" {
		return fmt.Errorf("self work experience must preserve its explicit minute unit")
	}
	if err := validateCharacterSelfExecutionShapeV2(CharacterSelfExecutionV2{Status: experience.Status, StartDay: experience.StartDay, EndDay: experience.EndDay}); err != nil {
		return err
	}
	if experience.Location != "" && (experience.Status != "completed" || (experience.Kind != "carry" && experience.Kind != "place")) {
		return fmt.Errorf("self location result requires an explicitly completed carry/place")
	}
	for _, id := range experience.ResourceIDs {
		if !physicalResourceIDV2(id) {
			return fmt.Errorf("self experience resource identity is invalid")
		}
		if catalog != nil {
			if _, exists := catalog[id]; !exists {
				return fmt.Errorf("self experience references an unknown world resource")
			}
		}
	}
	return nil
}

func validateCharacterKnownPlacementsV2(holdings []CharacterResourceHoldingV2, byID map[string]CharacterSelfExperienceV2) error {
	for _, holding := range holdings {
		if placement := holding.KnownPlacement; placement != nil {
			experience, exists := byID[placement.SourceExperienceID]
			if !exists || placement.AsOfChapter != experience.Chapter || placement.Location != experience.Location || strings.TrimSpace(placement.Location) == "" || experience.Status != "completed" || !physicalContainsRefV2(experience.ResourceIDs, holding.ResourceID) || (placement.Kind != "with_actor" && placement.Kind != "stored") || (placement.Kind == "with_actor" && experience.Kind != "carry") || (placement.Kind == "stored" && experience.Kind != "place") {
				return fmt.Errorf("known resource placement lacks the owner's exact completed experience")
			}
		}
	}
	return nil
}

func validateCharacterSelfObservationV2(observation CharacterObservationPacket) error {
	chronology := HasCharacterSelfChronologyPolicyV1(observation.Sources)
	if chronology && !HasCharacterSelfExperiencePolicyV2(observation.Sources) {
		return fmt.Errorf("self chronology observation requires the base self-experience policy")
	}
	if !HasCharacterSelfExperiencePolicyV2(observation.Sources) {
		if len(observation.SelfExperiences)+len(observation.TaskProgress) > 0 {
			return fmt.Errorf("self observation fields require the explicit self-experience policy")
		}
		return nil
	}
	actor := CharacterPhysicalStateV2{AgentID: observation.AgentID, SelfExperiences: observation.SelfExperiences, TaskProgress: observation.TaskProgress}
	for _, view := range observation.ResourceViews {
		if err := validateCharacterPerceivedLabelV2(view.Name); err != nil {
			return err
		}
		actor.Resources = append(actor.Resources, CharacterResourceHoldingV2{ResourceID: view.ResourceID, KnownPlacement: view.KnownPlacement})
	}
	if err := validateSelfObservationBudgetV2(observation.SelfExperiences, observation.TaskProgress); err != nil {
		return err
	}
	byID := map[string]CharacterSelfExperienceV2{}
	for _, experience := range observation.SelfExperiences {
		if experience.Evaluation != nil {
			value := experience.Evaluation
			if !chronology || observation.CycleContext == nil || value.EvaluatedAtDay > observation.CycleContext.CurrentDay+1e-12 ||
				(experience.Chapter == observation.Chapter && (value.GenerationID != observation.GenerationID || value.Cycle >= observation.CycleContext.Index)) {
				return fmt.Errorf("self evaluation is not a prior authorized owner observation")
			}
		}
		if err := validateCharacterSelfExperienceShapeV2(observation.AgentID, experience, nil); err != nil {
			return err
		}
		if _, duplicate := byID[experience.ID]; duplicate {
			return fmt.Errorf("self observation repeats an experience")
		}
		byID[experience.ID] = experience
		if experience.Chapter > observation.Chapter || (experience.Chapter == observation.Chapter && (observation.CycleContext == nil || observation.CycleContext.Index < 2 || (experience.EndDay != nil && *experience.EndDay > observation.CycleContext.CurrentDay))) {
			return fmt.Errorf("self observation cannot include a current/future chapter's unexecuted outcome")
		}
	}
	seen := map[string]bool{}
	for _, task := range observation.TaskProgress {
		source, exists := byID[task.SourceExperienceID]
		statusBound := task.State == source.Status && task.LatestAttemptStatus == ""
		if chronology && source.Evaluation != nil {
			statusBound = task.LatestAttemptStatus == source.Status && (task.State == "not_started" || task.State == "in_progress" || task.State == "completed") && !(task.Completed > 0 && task.State == "not_started")
		}
		if !exists || seen[task.TaskID] || task.TaskID != source.TaskID || task.Action != source.Action || task.Unit != "minute" || task.Unit != source.ProgressUnit || !samePhysicalNumberV2(task.Target, source.ProgressTarget) || !statusBound || task.AsOfChapter != source.Chapter || math.IsNaN(task.Completed) || math.IsInf(task.Completed, 0) || task.Completed < 0 {
			return fmt.Errorf("self task summary lacks its owner-bound latest execution source")
		}
		seen[task.TaskID] = true
	}
	// A bounded observation cannot re-sum omitted history. Full cumulative
	// amounts are authenticated against the complete host stimulus immediately
	// before a model call and when sealing the evidence bundle.
	return validateCharacterKnownPlacementsV2(actor.Resources, byID)
}
