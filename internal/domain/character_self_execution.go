package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const CharacterSelfTaskLimitV2 = 32
const CharacterSelfTaskActionMaxRunesV2 = 256

func ValidateCharacterSelfTaskIntentV2(proposal CharacterDecisionProposal, observation CharacterObservationPacket) error {
	if err := ValidateCharacterOperationalObservationIntentV1(proposal, observation); err != nil {
		return err
	}
	if !HasCharacterSelfExperiencePolicyV2(observation.Sources) {
		if len(proposal.SelfTasks) != 0 {
			return fmt.Errorf("self tasks require the explicit self-experience policy")
		}
		return nil
	}
	if observation.Version != CharacterObservationV2Version || len(proposal.SelfTasks) == 0 || len(proposal.SelfTasks) > CharacterSelfTaskLimitV2 {
		return fmt.Errorf("self-experience policy requires the owner's explicit self_tasks")
	}
	allowed := observation.AllowedFactIDs()
	views := map[string]CharacterResourceViewV2{}
	for _, view := range observation.ResourceViews {
		views[view.ResourceID] = view
	}
	previous := map[string]CharacterTaskProgressV2{}
	for _, progress := range observation.TaskProgress {
		previous[progress.TaskID] = progress
	}
	seen := map[string]bool{}
	for _, task := range proposal.SelfTasks {
		if !physicalIdentityV2(task.TaskID) || len(task.TaskID) > 128 || seen[task.TaskID] || strings.TrimSpace(task.Action) == "" || len(task.KnowledgeRefs) == 0 {
			return fmt.Errorf("self task requires a unique stable ID, owner action and visible knowledge refs")
		}
		seen[task.TaskID] = true
		if err := validateCharacterSelfTaskShapeV2(task); err != nil {
			return err
		}
		for _, ref := range task.KnowledgeRefs {
			if _, ok := allowed[ref]; !ok {
				return fmt.Errorf("self task references unavailable owner knowledge")
			}
		}
		resources := map[string]bool{}
		for _, id := range task.ResourceIDs {
			if _, visible := views[id]; !visible || resources[id] {
				return fmt.Errorf("self task references an unseen or duplicate resource")
			}
			resources[id] = true
		}
		if progress, exists := previous[task.TaskID]; exists && (task.Kind != "work" || progress.Action != task.Action || progress.Unit != selfTaskUnitV2(task) || !samePhysicalNumberV2(progress.Target, task.ProgressTarget)) {
			return fmt.Errorf("continued self task changed its existing action, target or unit")
		}
	}
	return nil
}

func validateCharacterSelfTaskShapeV2(task CharacterSelfTaskV2) error {
	if err := validateCharacterOperationalRequestsV1(task); err != nil {
		return err
	}
	if utf8.RuneCountInString(task.Action) > CharacterSelfTaskActionMaxRunesV2 || strings.IndexFunc(task.Action, unicode.IsControl) >= 0 {
		return fmt.Errorf("self task action must be a bounded single-line owner description")
	}
	switch task.Kind {
	case "work":
		if task.ProgressUnit != "" && task.ProgressUnit != "minute" {
			return fmt.Errorf("self work supports explicit effective minutes, not arbitrary world quantities")
		}
		if task.ProgressTarget != nil && (!physicalAmountV2(task.ProgressTarget) || *task.ProgressTarget <= 0) {
			return fmt.Errorf("self work target must be a finite positive owner-declared duration")
		}
	case "carry", "place":
		if len(task.ResourceIDs) == 0 || task.ProgressUnit != "" || task.ProgressTarget != nil {
			return fmt.Errorf("carry/place requires explicit resources and cannot declare work or balance progress")
		}
	default:
		return fmt.Errorf("unsupported self task kind")
	}
	return nil
}

func selfTaskUnitV2(task CharacterSelfTaskV2) string {
	if task.Kind == "work" {
		return "minute"
	}
	return ""
}

func selfExecutionActiveV2(status string) bool {
	return status == "completed" || status == "in_progress"
}

func validateCharacterSelfExecutionShapeV2(execution CharacterSelfExecutionV2) error {
	if err := validateCharacterOperationalResultsV1(execution); err != nil {
		return err
	}
	switch execution.Status {
	case "completed", "in_progress":
		if !physicalAmountV2(execution.StartDay) || !physicalAmountV2(execution.EndDay) || *execution.EndDay <= *execution.StartDay {
			return fmt.Errorf("executed self task requires a finite positive actual interval")
		}
	case "not_started", "blocked":
		if execution.StartDay != nil || execution.EndDay != nil {
			return fmt.Errorf("unexecuted self task cannot claim an execution interval")
		}
	default:
		return fmt.Errorf("invalid self execution status")
	}
	return nil
}

func CharacterSelfExperienceIDV2(agentID string, experience CharacterSelfExperienceV2) string {
	experience.ID = ""
	policy := CharacterSelfExperiencePolicyV2
	if experience.Evaluation != nil {
		policy = CharacterSelfChronologyPolicyV1
	}
	digest, _ := characterAgentDigest(struct {
		Policy string                    `json:"policy"`
		Owner  string                    `json:"owner"`
		Fact   CharacterSelfExperienceV2 `json:"fact"`
	}{policy, agentID, experience})
	return "self_" + strings.TrimPrefix(digest, "sha256:")
}

// Reconstruct owner-visible changes independently of any free post-state
// claims. Model-provided derived fields may be absent, exactly before, or
// exactly the deterministic after state (for immutable receipt replay).
func applyCharacterSelfExecutionsV2(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
	if err := validateSelfChronologyStimulusV1(stimulus); err != nil {
		return err
	}
	chronology := HasCharacterSelfChronologyPolicyV1(stimulus.Sources)
	policy := HasCharacterSelfExperiencePolicyV2(stimulus.Sources)
	sharedIntervalCarry := HasCharacterSharedIntervalCarryPolicyV1(stimulus.Sources)
	if sharedIntervalCarry && !policy {
		return fmt.Errorf("shared-interval carry requires the explicit self-experience policy")
	}
	if !policy {
		for _, resolution := range receipt.Resolutions {
			if len(resolution.SelfExecutions) != 0 {
				return fmt.Errorf("self executions require the explicit self-experience policy")
			}
		}
		for _, actor := range after.Actors {
			for _, previous := range before.Actors {
				if actor.AgentID != previous.AgentID {
					continue
				}
				if !samePhysicalValueV2(actor.SelfExperiences, previous.SelfExperiences) || !samePhysicalValueV2(actor.TaskProgress, previous.TaskProgress) {
					return fmt.Errorf("owner experiences/progress cannot change without their explicit execution policy")
				}
				old := map[string]CharacterResourceHoldingV2{}
				for _, h := range previous.Resources {
					old[h.ResourceID] = h
				}
				for _, h := range actor.Resources {
					if !samePhysicalValueV2(h.KnownPlacement, old[h.ResourceID].KnownPlacement) || h.PerceivedLabel != old[h.ResourceID].PerceivedLabel {
						return fmt.Errorf("self resource state cannot change without its explicit execution policy")
					}
				}
			}
		}
		return nil
	}
	if receipt.StoryTime == nil || math.IsNaN(receipt.StoryTime.StartDay) || math.IsInf(receipt.StoryTime.StartDay, 0) || math.IsNaN(receipt.StoryTime.EndDay) || math.IsInf(receipt.StoryTime.EndDay, 0) || receipt.StoryTime.StartDay < 0 || receipt.StoryTime.EndDay < receipt.StoryTime.StartDay {
		return fmt.Errorf("self executions require the explicit finite actual story-time interval")
	}
	if err := ValidateStoryTimeForClock(receipt.Chapter, receipt.StoryTime, stimulus.StoryClock); err != nil {
		return err
	}
	oldActors := map[string]CharacterPhysicalStateV2{}
	for _, actor := range before.Actors {
		oldActors[actor.AgentID] = actor
	}
	for i := range after.Actors {
		actor := &after.Actors[i]
		old := oldActors[actor.AgentID]
		resolution, resolved := resolutions[actor.AgentID]
		if !resolved {
			continue
		}
		proposal, exists := proposals[actor.AgentID]
		if !exists {
			return fmt.Errorf("self execution lacks an owner proposal")
		}
		if chronology {
			var err error
			resolution.SelfExecutions, err = canonicalSelfExecutionsV1(resolution.SelfExecutions)
			if err != nil {
				return err
			}
		}
		if digest, err := ComputeCharacterDecisionProposalDigest(proposal); err != nil || digest != proposal.Digest {
			return fmt.Errorf("self execution owner proposal digest mismatch")
		}
		tasks := map[string]CharacterSelfTaskV2{}
		for _, task := range proposal.SelfTasks {
			if !physicalIdentityV2(task.TaskID) || strings.TrimSpace(task.Action) == "" || tasks[task.TaskID].TaskID != "" {
				return fmt.Errorf("self execution owner tasks are missing or duplicate")
			}
			if err := validateCharacterSelfTaskShapeV2(task); err != nil {
				return err
			}
			tasks[task.TaskID] = task
		}
		if len(tasks) == 0 {
			return fmt.Errorf("self-experience policy requires an explicit task for every resolved owner")
		}
		if !receipt.Finalized && len(resolution.SelfExecutions) != 0 {
			return fmt.Errorf("provisional arbitration cannot publish self execution or progress")
		}
		derived := old
		derived.Location = actor.Location
		derived.Resources = append([]CharacterResourceHoldingV2{}, actor.Resources...)
		derived.SelfExperiences = append([]CharacterSelfExperienceV2(nil), old.SelfExperiences...)
		oldHoldings := map[string]CharacterResourceHoldingV2{}
		for _, h := range old.Resources {
			oldHoldings[h.ResourceID] = h
		}
		for j := range derived.Resources {
			h := &derived.Resources[j]
			previous := oldHoldings[h.ResourceID]
			h.KnownPlacement = previous.KnownPlacement
			if h.PerceivedName != previous.PerceivedName && (h.PerceivedLabel == "" || h.PerceivedLabel == previous.PerceivedLabel) && authorizeResourceNameV2(receipt, *h, resolution, proposals, resolutions) {
				// Preserve the existing explicit delivered-name channel. Its
				// sender saw a static safe view label under this same policy.
				h.PerceivedLabel = h.PerceivedName
			}
			if h.PerceivedLabel == "" {
				h.PerceivedLabel = previous.PerceivedLabel
			}
			if h.Perception.Kind != "unaware" && h.PerceivedLabel == "" {
				h.PerceivedLabel = UnidentifiedResourceNameV2
			}
			if h.PerceivedLabel != previous.PerceivedLabel && h.PerceivedLabel != UnidentifiedResourceNameV2 && !artifactCreatedByProposalV1(before, *after, h.ResourceID, proposal) {
				label := *h
				label.PerceivedName = h.PerceivedLabel
				if !authorizeResourceNameV2(receipt, label, resolution, proposals, resolutions) {
					return fmt.Errorf("self-experience resource label lacks an independently received safe name")
				}
			}
		}
		covered := map[string]bool{}
		carryStart := math.Inf(1)
		completedCarries := 0
		for _, execution := range resolution.SelfExecutions {
			if err := validateCharacterSelfExecutionShapeV2(execution); err != nil {
				return err
			}
			task, exists := tasks[execution.TaskID]
			if !exists {
				return fmt.Errorf("self execution references another owner or an unsubmitted task")
			}
			covered[task.TaskID] = true
			if selfExecutionActiveV2(execution.Status) {
				if resolution.Outcome == "blocked" || resolution.CompletionState == "blocked" || *execution.StartDay < receipt.StoryTime.StartDay-1e-12 || *execution.EndDay > receipt.StoryTime.EndDay+1e-12 {
					return fmt.Errorf("self execution exceeds its final actual interval or belongs to a blocked actor")
				}
				for _, resourceID := range task.ResourceIDs {
					previous, knew := oldHoldings[resourceID]
					if !knew || previous.Perception.Kind == "unaware" || (previous.Access == "none" && !hasResourceDeliveryV2(receipt, resourceID, "", actor.AgentID, "", "", "shared") && !hasResourceDeliveryV2(receipt, resourceID, "", actor.AgentID, "", "", "exclusive")) {
						return fmt.Errorf("active self task cannot use an unseen or inaccessible resource")
					}
				}
			}
			if task.Kind == "carry" && selfExecutionActiveV2(execution.Status) {
				if execution.Status == "completed" {
					completedCarries++
				}
				carryStart = math.Min(carryStart, *execution.StartDay)
			}
		}
		if completedCarries > 1 {
			if !sharedIntervalCarry {
				return fmt.Errorf("self placement policy supports one explicit endpoint carry, not inferred intermediate transports")
			}
			if err := validateSharedIntervalCarryExecutionsV1(resolution.SelfExecutions, tasks); err != nil {
				return err
			}
		}
		for ordinal, execution := range resolution.SelfExecutions {
			task := tasks[execution.TaskID]
			fact := CharacterSelfExperienceV2{Chapter: receipt.Chapter, TaskID: task.TaskID, Kind: task.Kind, Action: task.Action, Status: execution.Status, ResourceIDs: normalizeV2Strings(task.ResourceIDs), StartDay: physicalNumberCopyV2(execution.StartDay), EndDay: physicalNumberCopyV2(execution.EndDay), ProgressTarget: physicalNumberCopyV2(task.ProgressTarget), ProgressUnit: selfTaskUnitV2(task), SourceProposalDigest: proposal.Digest}
			if chronology {
				context := stimulus.SelfEvaluationContext
				fact.Evaluation = &CharacterSelfEvaluationV1{Version: CharacterSelfChronologyPolicyV1, GenerationID: receipt.GenerationID,
					Cycle: context.Cycle, ContextDigest: context.Digest, StimulusDigest: stimulus.Digest, EvaluatedAtDay: receipt.StoryTime.EndDay, Ordinal: ordinal + 1}
			}
			placementKind := ""
			if (task.Kind == "carry" || task.Kind == "place") && execution.Status == "completed" {
				if task.Kind == "carry" {
					fact.Location, placementKind = actor.Location, "with_actor"
				} else {
					if old.Location != actor.Location && (math.IsInf(carryStart, 1) || *execution.EndDay > carryStart+1e-12) {
						return fmt.Errorf("place must be explicitly completed at the confirmed origin before endpoint transport")
					}
					fact.Location, placementKind = old.Location, "stored"
				}
				for _, resourceID := range task.ResourceIDs {
					previous, knew := oldHoldings[resourceID]
					if !knew || previous.Perception.Kind == "unaware" {
						return fmt.Errorf("self carry/place cannot discover an unseen resource")
					}
					if previous.Access != "exclusive" && !artifactCustodianAtOriginV1(before, resourceID, actor.AgentID) && !hasResourceDeliveryV2(receipt, resourceID, "", actor.AgentID, "", "", "exclusive") {
						return fmt.Errorf("self carry/place lacks explicit possession authority; shared reading is not transport")
					}
				}
			}
			fact.ID = CharacterSelfExperienceIDV2(actor.AgentID, fact)
			for _, previous := range derived.SelfExperiences {
				if previous.ID == fact.ID {
					return fmt.Errorf("self execution repeats a previously recorded experience")
				}
			}
			derived.SelfExperiences = append(derived.SelfExperiences, fact)
			if placementKind != "" {
				for j := range derived.Resources {
					if physicalContainsRefV2(task.ResourceIDs, derived.Resources[j].ResourceID) {
						derived.Resources[j].KnownPlacement = &CharacterResourcePlacementV2{Kind: placementKind, Location: fact.Location, AsOfChapter: receipt.Chapter, SourceExperienceID: fact.ID}
					}
				}
			}
		}
		if receipt.Finalized && len(covered) != len(tasks) {
			return fmt.Errorf("final self execution must explicitly distinguish every task's actual or unstarted status")
		}
		// Legacy derivation mixes optional execution times with opaque IDs.
		// Feed it the same canonical input used by persisted-state validation;
		// otherwise append order can produce progress that changes on reload.
		// This preserves the historical derivation, not a new chronology policy.
		sortCharacterSelfStateV2(&derived)
		var err error
		if chronology {
			derived.TaskProgress, err = deriveSelfChronologyProgressV1(derived)
		} else {
			derived.TaskProgress, err = deriveCharacterTaskProgressV2(derived.SelfExperiences)
		}
		if err != nil {
			return err
		}
		if len(actor.SelfExperiences) > 0 && !samePhysicalValueV2(actor.SelfExperiences, old.SelfExperiences) && !samePhysicalValueV2(actor.SelfExperiences, derived.SelfExperiences) {
			return fmt.Errorf("arbiter cannot invent owner self-experience text or execution facts")
		}
		if len(actor.TaskProgress) > 0 && !samePhysicalValueV2(actor.TaskProgress, old.TaskProgress) && !samePhysicalValueV2(actor.TaskProgress, derived.TaskProgress) {
			return fmt.Errorf("arbiter cannot invent owner cumulative work progress")
		}
		for j, supplied := range actor.Resources {
			if supplied.KnownPlacement != nil && !samePhysicalValueV2(supplied.KnownPlacement, oldHoldings[supplied.ResourceID].KnownPlacement) && !samePhysicalValueV2(supplied.KnownPlacement, derived.Resources[j].KnownPlacement) {
				return fmt.Errorf("resource placement lacks an explicit completed owner task")
			}
		}
		actor.SelfExperiences, actor.TaskProgress, actor.Resources = derived.SelfExperiences, derived.TaskProgress, derived.Resources
	}
	return nil
}

func deriveCharacterTaskProgressV2(experiences []CharacterSelfExperienceV2) ([]CharacterTaskProgressV2, error) {
	ordered := append([]CharacterSelfExperienceV2(nil), experiences...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Chapter != ordered[j].Chapter {
			return ordered[i].Chapter < ordered[j].Chapter
		}
		if ordered[i].EndDay != nil && ordered[j].EndDay != nil && *ordered[i].EndDay != *ordered[j].EndDay {
			return *ordered[i].EndDay < *ordered[j].EndDay
		}
		return ordered[i].ID < ordered[j].ID
	})
	progress := map[string]CharacterTaskProgressV2{}
	var intervals [][2]float64
	for _, experience := range ordered {
		if experience.Kind != "work" {
			continue
		}
		task, exists := progress[experience.TaskID]
		if exists && (task.Action != experience.Action || task.Unit != experience.ProgressUnit || !samePhysicalNumberV2(task.Target, experience.ProgressTarget)) {
			return nil, fmt.Errorf("self work experience changed a continuing task's definition")
		}
		if !exists {
			task = CharacterTaskProgressV2{TaskID: experience.TaskID, Action: experience.Action, Unit: experience.ProgressUnit, Target: physicalNumberCopyV2(experience.ProgressTarget)}
		}
		if selfExecutionActiveV2(experience.Status) {
			intervals = append(intervals, [2]float64{*experience.StartDay, *experience.EndDay})
			task.Completed += (*experience.EndDay - *experience.StartDay) * 1440
		}
		if experience.Status == "completed" && task.Target != nil && task.Completed < *task.Target && !physicalAmountsCloseV2(task.Completed, *task.Target) {
			return nil, fmt.Errorf("self task cannot declare its duration target completed without actual effective work")
		}
		task.State, task.AsOfChapter, task.SourceExperienceID = experience.Status, experience.Chapter, experience.ID
		progress[task.TaskID] = task
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
