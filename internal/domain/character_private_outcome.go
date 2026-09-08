package domain

import (
	"fmt"
	"strings"
)

// CharacterPrivateOutcomeV2 is deliberately composed from the actor's intent,
// observable completion/location and perception projection. World-side free
// text may contain unmeasured balances and never becomes owner memory here.
func CharacterPrivateOutcomeV2(proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, state WorldPhysicalStateV2, receipts ...WorldArbitrationReceipt) (string, error) {
	if resolution.PostState == nil || resolution.AgentID != proposal.AgentID {
		return "", fmt.Errorf("private physical outcome is not bound to actor post-state")
	}
	if len(receipts) > 1 {
		return "", fmt.Errorf("private physical outcome requires at most one exact receipt")
	}
	views, err := BuildCharacterResourceViewsV2(state, resolution.AgentID)
	if err != nil {
		return "", err
	}
	var currentExperiences map[string]bool
	for _, actor := range state.Actors {
		if actor.AgentID != proposal.AgentID {
			continue
		}
		currentExperiences, err = privateOutcomeChronologyScopeV1(proposal, resolution, actor, receipts)
		if err != nil {
			return "", err
		}
	}
	parts := []string{"决定：" + proposal.Decision, "行动：" + proposal.IntendedAction, "完成度：" + resolution.CompletionState, "位置：" + resolution.PostState.Location}
	if len(resolution.SelfExecutions) > 0 {
		// Keep the old formatter byte-identical for already sealed receipts.
		// New policy explicitly separates the proposed whole action from the
		// owner-visible segments that actually happened in this chapter.
		parts = []string{"本轮决定：" + proposal.Decision, "本轮意图（不代表全部完成）：" + proposal.IntendedAction, "完成度：" + resolution.CompletionState, "位置：" + resolution.PostState.Location}
		for _, actor := range state.Actors {
			if actor.AgentID != proposal.AgentID {
				continue
			}
			var experiences []CharacterSelfExperienceV2
			taskIDs := map[string]bool{}
			for _, experience := range actor.SelfExperiences {
				if experience.Chapter == proposal.Chapter && experience.SourceProposalDigest == proposal.Digest && (currentExperiences == nil || currentExperiences[experience.ID]) {
					experiences = append(experiences, experience)
					taskIDs[experience.TaskID] = true
				}
			}
			var progress []CharacterTaskProgressV2
			for _, task := range actor.TaskProgress {
				if taskIDs[task.TaskID] && (currentExperiences == nil || currentExperiences[task.SourceExperienceID]) {
					progress = append(progress, task)
				}
			}
			parts = append(parts, FormatCharacterSelfExperiencesV2(experiences, progress)...)
		}
	}
	if resources := FormatCharacterResourceViewsV2(views); len(resources) > 0 {
		parts = append(parts, "资源感知："+strings.Join(resources, "；"))
	}
	for _, actor := range state.Actors {
		if actor.AgentID != resolution.AgentID {
			continue
		}
		var operations []CharacterOperationalObservationV1
		for _, operation := range actor.OperationalObservations {
			if operation.Chapter == proposal.Chapter && operation.SourceProposalDigest == proposal.Digest && (currentExperiences == nil || currentExperiences[operation.SourceExperienceID]) {
				operations = append(operations, operation)
			}
		}
		parts = append(parts, FormatCharacterOperationalObservationsV1(operations)...)
		for _, knowledge := range actor.ArtifactKnowledge {
			if knowledge.SourceProposalDigest != proposal.Digest || knowledge.Chapter != proposal.Chapter {
				continue
			}
			if len(receipts) != 1 || knowledge.GenerationID != receipts[0].GenerationID || !artifactReceiptTimeV1(receipts[0], knowledge.AtDay) || knowledge.AtDay <= receipts[0].StoryTime.StartDay {
				continue
			}
			parts = append(parts, FormatCharacterArtifactKnowledgeV1(knowledge)...)
		}
		for _, fact := range actor.ReceivedFacts {
			if fact.Chapter != proposal.Chapter {
				continue
			}
			if fact.SourceType == "communication" {
				parts = append(parts, "收到的"+fact.Kind+"："+fact.Text)
			} else {
				parts = append(parts, "读到的文档陈述（未自动确认为当前真相）："+fact.Text)
			}
		}
	}
	return strings.Join(parts, "；"), nil
}

// The old proposal digest intentionally survives continuation. Only the exact
// current receipt can identify which of that proposal's executions happened
// now; neither a maximum cycle number nor the latest progress row is authority.
// Callers have already validated the complete arbitration/stimulus chain. This
// formatter checks membership and returns IDs, without pruning canonical state.
// A nil set retains the byte-identical pre-chronology formatter behavior.
func privateOutcomeChronologyScopeV1(proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, actor CharacterPhysicalStateV2, receipts []WorldArbitrationReceipt) (map[string]bool, error) {
	chronology := resolution.PostState.SelfChronologyBaseline != nil
	for _, experience := range actor.SelfExperiences {
		if experience.Chapter == proposal.Chapter && experience.SourceProposalDigest == proposal.Digest && experience.Evaluation != nil {
			chronology = true
		}
	}
	if !chronology {
		return nil, nil
	}
	if len(receipts) != 1 {
		return nil, fmt.Errorf("chronological private outcome requires its exact arbitration receipt")
	}
	receipt := receipts[0]
	proposalDigest, err := ComputeCharacterDecisionProposalDigest(proposal)
	if err != nil || proposalDigest != proposal.Digest {
		return nil, fmt.Errorf("chronological private outcome has an invalid original proposal")
	}
	digest, err := ComputeWorldArbitrationReceiptDigest(receipt)
	if err != nil || digest != receipt.Digest || receipt.Version != WorldArbitrationReceiptV2Version || receipt.GenerationID != proposal.GenerationID || receipt.Chapter != proposal.Chapter || receipt.StoryTime == nil {
		return nil, fmt.Errorf("chronological private outcome has an invalid receipt binding")
	}
	member := false
	for _, candidate := range receipt.Resolutions {
		if samePhysicalValueV2(candidate, resolution) {
			member = true
		}
	}
	if !member || resolution.ProposalDigest != proposal.Digest || resolution.Character != proposal.Character || !samePhysicalValueV2(actor, *resolution.PostState) {
		return nil, fmt.Errorf("chronological private outcome differs from its receipt's owner post-state")
	}
	executions, err := canonicalSelfExecutionsV1(resolution.SelfExecutions)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(executions))
	ordinals := make(map[int]bool, len(executions))
	cycle, contextDigest := 0, ""
	for _, experience := range actor.SelfExperiences {
		evaluation := experience.Evaluation
		if experience.Chapter != proposal.Chapter || experience.SourceProposalDigest != proposal.Digest || evaluation == nil || evaluation.StimulusDigest != receipt.StimulusDigest {
			continue
		}
		if evaluation.Version != CharacterSelfChronologyPolicyV1 || evaluation.GenerationID != receipt.GenerationID || evaluation.Cycle <= 0 || evaluation.EvaluatedAtDay != receipt.StoryTime.EndDay || evaluation.Ordinal <= 0 || evaluation.Ordinal > len(executions) || ordinals[evaluation.Ordinal] {
			return nil, fmt.Errorf("private self experience has a foreign or duplicate current evaluation")
		}
		if cycle == 0 {
			cycle, contextDigest = evaluation.Cycle, evaluation.ContextDigest
		}
		if cycle != evaluation.Cycle || contextDigest != evaluation.ContextDigest {
			return nil, fmt.Errorf("private self experiences mix different evaluation cycles")
		}
		execution := executions[evaluation.Ordinal-1]
		if experience.TaskID != execution.TaskID || experience.Status != execution.Status || !samePhysicalNumberV2(experience.StartDay, execution.StartDay) || !samePhysicalNumberV2(experience.EndDay, execution.EndDay) || experience.ID != CharacterSelfExperienceIDV2(actor.AgentID, experience) {
			return nil, fmt.Errorf("private self experience differs from the current actual execution")
		}
		ordinals[evaluation.Ordinal], selected[experience.ID] = true, true
	}
	if len(selected) != len(executions) {
		return nil, fmt.Errorf("private self outcome lacks an exact current evaluation for every execution")
	}
	return selected, nil
}
