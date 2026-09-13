package domain

import (
	"fmt"
	"strings"
)

const CharacterIncomingMaterialReadPolicyV1 = "character-incoming-material-read:receipt.v1"

func HasCharacterIncomingMaterialReadPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterIncomingMaterialReadPolicyV1)
}

func validateIncomingMaterialReadPolicyV1(stimulus WorldStimulusPacket) error {
	if HasCharacterIncomingMaterialReadPolicyV1(stimulus.Sources) && (stimulus.Version != WorldStimulusPacketV2Version ||
		!physicalContainsRefV2(stimulus.Sources, CharacterActivationCyclePolicyV3) ||
		!HasCharacterSelfExperiencePolicyV2(stimulus.Sources) || !HasCharacterWorkArtifactPolicyV1(stimulus.Sources)) {
		return fmt.Errorf("incoming material reading requires its explicit v3 artifact/self-experience policy")
	}
	return nil
}

func validateIncomingMaterialReadIntentV1(proposal CharacterDecisionProposal, observation CharacterObservationPacket) error {
	for _, request := range proposal.ResourceReads {
		if request.TaskID == "" {
			continue // Keep the original foundation/document read path unchanged.
		}
		if !HasCharacterIncomingMaterialReadPolicyV1(observation.Sources) || !HasCharacterWorkArtifactPolicyV1(observation.Sources) ||
			!HasCharacterSelfExperiencePolicyV2(observation.Sources) || request.ResourceID != "" ||
			!operationalBoundedV1(request.IncomingDeliveryFrom, 160) || strings.TrimSpace(request.IncomingDeliveryFrom) != request.IncomingDeliveryFrom || request.IncomingDeliveryFrom == proposal.Character ||
			!physicalIdentityV2(request.TaskID) {
			return fmt.Errorf("incoming artifact reading requires the new policy, an explicit other sender and original owner work task")
		}
		count := 0
		for _, task := range proposal.SelfTasks {
			if task.TaskID == request.TaskID && task.Kind == "work" {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("incoming read task_id must name exactly one original owner work task")
		}
	}
	return nil
}

func validateIncomingMaterialReadReceiptFieldsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket) error {
	for _, delivery := range receipt.ResourceDeliveries {
		if delivery.DeliveredAtDay == nil {
			continue
		}
		if !HasCharacterIncomingMaterialReadPolicyV1(stimulus.Sources) || !receipt.Finalized || receipt.StoryTime == nil ||
			!physicalAmountV2(delivery.DeliveredAtDay) || *delivery.DeliveredAtDay < receipt.StoryTime.StartDay || *delivery.DeliveredAtDay > receipt.StoryTime.EndDay {
			return fmt.Errorf("timed incoming delivery requires the explicit policy and actual final cycle interval")
		}
	}
	return nil
}

func validateIncomingMaterialReadTransitionsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal) error {
	for _, proposal := range proposals {
		for _, request := range proposal.ResourceReads {
			if request.TaskID != "" && !HasCharacterIncomingMaterialReadPolicyV1(stimulus.Sources) {
				return fmt.Errorf("incoming reading task_id lacks its frozen policy")
			}
		}
	}
	for _, delivery := range receipt.ResourceDeliveries {
		if delivery.DeliveredAtDay != nil {
			if err := validateIncomingMaterialDeliveryV1(receipt, stimulus, before, after, proposals, delivery); err != nil {
				return err
			}
		}
	}
	return nil
}

// Reserved for exact runtime source comparison; names are never inferred from
// an artifact label, free text or a role title.
func incomingMaterialSenderV1(state WorldPhysicalStateV2, name string) (CharacterPhysicalStateV2, bool) {
	var actor CharacterPhysicalStateV2
	count := 0
	for _, candidate := range state.Actors {
		if candidate.Character == name {
			actor, count = candidate, count+1
		}
	}
	return actor, count == 1
}

func incomingMaterialResolutionV1(receipt WorldArbitrationReceipt, owner string) (CharacterDecisionResolution, bool) {
	var result CharacterDecisionResolution
	count := 0
	for _, resolution := range receipt.Resolutions {
		if resolution.AgentID == owner {
			result, count = resolution, count+1
		}
	}
	return result, count == 1 && result.Outcome != "blocked" && result.CompletionState != "blocked" && result.PostState != nil
}

// A location is proven only while stationary or at the known endpoints of the
// existing explicit carry interval. No intermediate route/location is inferred.
func incomingMaterialLocationAtV1(before, after WorldPhysicalStateV2, proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, at float64, carried string) (string, bool) {
	old, oldOK := artifactStateActorV1(before, proposal.AgentID)
	next, nextOK := artifactStateActorV1(after, proposal.AgentID)
	if !oldOK || !nextOK || proposal.Location != old.Location || resolution.PostState == nil || resolution.PostState.Location != next.Location {
		return "", false
	}
	tasks := map[string]CharacterSelfTaskV2{}
	for _, task := range proposal.SelfTasks {
		tasks[task.TaskID] = task
	}
	var route *CharacterSelfExecutionV2
	carriedTarget := carried == ""
	for i := range resolution.SelfExecutions {
		execution := &resolution.SelfExecutions[i]
		task := tasks[execution.TaskID]
		if task.Kind != "carry" || !selfExecutionActiveV2(execution.Status) {
			continue
		}
		if execution.Status != "completed" || !physicalAmountV2(execution.StartDay) || !physicalAmountV2(execution.EndDay) || *execution.StartDay >= *execution.EndDay {
			return "", false
		}
		if route != nil && (*route.StartDay != *execution.StartDay || *route.EndDay != *execution.EndDay) {
			return "", false
		}
		route = execution
		carriedTarget = carriedTarget || physicalContainsRefV2(task.ResourceIDs, carried)
	}
	if old.Location == next.Location {
		return old.Location, route == nil // Do not invent an unrepresented round trip.
	}
	if route == nil {
		return "", false
	}
	if at <= *route.StartDay {
		return old.Location, true
	}
	if at >= *route.EndDay && carriedTarget {
		return next.Location, true
	}
	return "", false
}

func incomingMaterialWorkAtV1(proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, taskID, resource string, at float64) (CharacterSelfExecutionV2, bool) {
	tasks := map[string]CharacterSelfTaskV2{}
	for _, task := range proposal.SelfTasks {
		tasks[task.TaskID] = task
	}
	matched := map[string]bool{}
	var result CharacterSelfExecutionV2
	for _, execution := range resolution.SelfExecutions {
		task, exists := tasks[execution.TaskID]
		if !exists || task.Kind != "work" || (taskID != "" && task.TaskID != taskID) || (resource != "" && !physicalContainsRefV2(task.ResourceIDs, resource)) || !selfExecutionActiveV2(execution.Status) || !physicalAmountV2(execution.StartDay) || !physicalAmountV2(execution.EndDay) || *execution.StartDay >= *execution.EndDay || at < *execution.StartDay || at > *execution.EndDay {
			continue
		}
		matched[task.TaskID] = true
		result = execution
	}
	return result, len(matched) == 1
}

func incomingMaterialUniqueDeliveryV1(receipt WorldArbitrationReceipt, before WorldPhysicalStateV2, receiver, sender string) (ResourceDeliveryV2, error) {
	var result ResourceDeliveryV2
	count := 0
	for _, delivery := range receipt.ResourceDeliveries {
		if delivery.ToAgentID != receiver || delivery.FromAgentID != sender || delivery.Access == "none" {
			continue
		}
		resource, exists := artifactStateResourceV1(before, delivery.ResourceID)
		if !exists || (resource.Artifact == nil && len(resource.ReadableFacts) == 0) {
			continue
		}
		result, count = delivery, count+1
	}
	if count != 1 {
		return result, fmt.Errorf("incoming material read requires exactly one actually delivered readable material from its declared sender")
	}
	return result, nil
}

func validateIncomingMaterialDeliveryV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, delivery ResourceDeliveryV2) error {
	if !HasCharacterIncomingMaterialReadPolicyV1(stimulus.Sources) || delivery.DeliveredAtDay == nil || validateIncomingMaterialReadReceiptFieldsV1(receipt, stimulus) != nil || delivery.Access == "none" || delivery.FromAgentID == delivery.ToAgentID {
		return fmt.Errorf("incoming material delivery lacks its explicit policy, actual time or access grant")
	}
	resource, exists := artifactStateResourceV1(before, delivery.ResourceID)
	current, currentOK := artifactStateResourceV1(after, delivery.ResourceID)
	sender, sent := proposals[delivery.FromAgentID]
	receiver, receiving := proposals[delivery.ToAgentID]
	sentResolution, senderActed := incomingMaterialResolutionV1(receipt, delivery.FromAgentID)
	receiverResolution, receiverActed := incomingMaterialResolutionV1(receipt, delivery.ToAgentID)
	if !exists || !currentOK || !sent || !receiving || !senderActed || !receiverActed || sender.Digest != delivery.SourceProposalDigest || (resource.Artifact == nil && len(resource.ReadableFacts) == 0) {
		return fmt.Errorf("timed incoming material must be a pre-existing readable resource with actual source and recipient proposals")
	}
	requested := false
	for _, request := range receiver.ResourceReads {
		requested = requested || request.IncomingDeliveryFrom == sender.Character && request.TaskID != "" && request.ResourceID == ""
	}
	if !requested {
		return fmt.Errorf("timed incoming material lacks its recipient's original conditional read request")
	}
	grant := false
	if resource.Artifact != nil {
		if current.Artifact == nil || resource.Artifact.VersionDigest != current.Artifact.VersionDigest || delivery.ArtifactVersionDigest != resource.Artifact.VersionDigest || resource.Artifact.Placement.CustodianAgentID != sender.AgentID {
			return fmt.Errorf("incoming artifact must retain its exact pre-existing version and original custodian; no same-cycle creation or update")
		}
		for _, access := range sender.ArtifactAccess {
			grant = grant || access.ResourceID == delivery.ResourceID && access.VersionDigest == delivery.ArtifactVersionDigest && access.ToCharacter == receiver.Character && access.Access == delivery.Access
		}
	} else {
		if delivery.ArtifactVersionDigest != "" || !samePhysicalValueV2(resource.ReadableFacts, current.ReadableFacts) {
			return fmt.Errorf("incoming foundation material changed its original readable source")
		}
		for _, report := range sender.ResourceReports {
			grant = grant || report.ResourceID == delivery.ResourceID && report.ToCharacter == receiver.Character
		}
	}
	if !grant {
		return fmt.Errorf("incoming material delivery lacks the original sender's exact grant/report")
	}
	at := *delivery.DeliveredAtDay
	work, worked := incomingMaterialWorkAtV1(sender, sentResolution, "", delivery.ResourceID, at)
	if !worked {
		return fmt.Errorf("timed material grant requires one original sender work task on that resource at delivery")
	}
	location, located := incomingMaterialLocationAtV1(before, after, sender, sentResolution, at, delivery.ResourceID)
	startLocation, started := incomingMaterialLocationAtV1(before, after, sender, sentResolution, *work.StartDay, delivery.ResourceID)
	endLocation, ended := incomingMaterialLocationAtV1(before, after, sender, sentResolution, *work.EndDay, delivery.ResourceID)
	recipientLocation, arrived := incomingMaterialLocationAtV1(before, after, receiver, receiverResolution, at, "")
	if !located || !started || !ended || !arrived || location != recipientLocation || location != startLocation || location != endLocation {
		return fmt.Errorf("incoming delivery lacks proven co-location during the actual sender grant work")
	}
	if resource.Artifact != nil {
		oldSender, _ := artifactStateActorV1(before, sender.AgentID)
		if resource.Artifact.Placement.Location != oldSender.Location {
			return fmt.Errorf("incoming artifact was not at its custodian's original location")
		}
	} else {
		oldSender, _ := artifactStateActorV1(before, sender.AgentID)
		if !actorHasReadableAccessV2(oldSender, delivery.ResourceID) {
			return fmt.Errorf("incoming foundation source lacks prior sender access")
		}
	}
	return nil
}

func validateIncomingMaterialReadTimeV1(receipt WorldArbitrationReceipt, before, after WorldPhysicalStateV2, proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, request ResourceReadRequestV2, delivery ResourceDeliveryV2, at float64) error {
	if delivery.DeliveredAtDay == nil || !finiteStoryDay(at) {
		return fmt.Errorf("incoming read lacks its actual delivery/read time")
	}
	work, worked := incomingMaterialWorkAtV1(proposal, resolution, request.TaskID, "", at)
	if !worked || *work.StartDay < *delivery.DeliveredAtDay {
		return fmt.Errorf("incoming read must use its original actual work interval starting after delivery")
	}
	for _, execution := range resolution.SelfExecutions {
		if execution.TaskID == request.TaskID && selfExecutionActiveV2(execution.Status) && (execution.StartDay == nil || *execution.StartDay < *delivery.DeliveredAtDay) {
			return fmt.Errorf("incoming reading work cannot begin before the material arrived")
		}
	}
	location, here := incomingMaterialLocationAtV1(before, after, proposal, resolution, at, "")
	startLocation, startHere := incomingMaterialLocationAtV1(before, after, proposal, resolution, *work.StartDay, "")
	deliveryLocation, receivedHere := incomingMaterialLocationAtV1(before, after, proposal, resolution, *delivery.DeliveredAtDay, "")
	if !here || !startHere || !receivedHere || location != startLocation || location != deliveryLocation {
		return fmt.Errorf("incoming read lacks proven stationary access after actual arrival")
	}
	return nil
}

func bindIncomingMaterialArtifactReadV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, result CharacterArtifactReadResultV1) (bool, error) {
	for _, request := range proposal.ResourceReads {
		if request.TaskID == "" || request.IncomingDeliveryFrom == "" {
			continue
		}
		sender, exists := incomingMaterialSenderV1(before, request.IncomingDeliveryFrom)
		if !exists {
			continue // An untriggered condition grants nothing; another may match.
		}
		if !hasResourceDeliveryV2(receipt, result.ResourceID, sender.AgentID, proposal.AgentID, "", "", "shared") && !hasResourceDeliveryV2(receipt, result.ResourceID, sender.AgentID, proposal.AgentID, "", "", "exclusive") {
			continue
		}
		delivery, err := incomingMaterialUniqueDeliveryV1(receipt, before, proposal.AgentID, sender.AgentID)
		if err != nil {
			return false, err
		}
		if delivery.ResourceID != result.ResourceID {
			continue
		}
		resource, known := artifactStateResourceV1(before, result.ResourceID)
		if !known || resource.Artifact == nil || delivery.ArtifactVersionDigest != result.VersionDigest || resource.Artifact.VersionDigest != result.VersionDigest {
			return false, fmt.Errorf("incoming artifact read result differs from the actual delivered original version")
		}
		if err := validateIncomingMaterialDeliveryV1(receipt, stimulus, before, after, proposals, delivery); err != nil {
			return false, err
		}
		if err := validateIncomingMaterialReadTimeV1(receipt, before, after, proposal, resolution, request, delivery, result.AtDay); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func bindIncomingMaterialFoundationReadV1(receipt WorldArbitrationReceipt, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, request ResourceReadRequestV2, resourceID string) (bool, error) {
	sender, exists := incomingMaterialSenderV1(before, request.IncomingDeliveryFrom)
	if !exists {
		return false, nil
	}
	if !hasResourceDeliveryV2(receipt, resourceID, sender.AgentID, proposal.AgentID, "", "", "shared") && !hasResourceDeliveryV2(receipt, resourceID, sender.AgentID, proposal.AgentID, "", "", "exclusive") {
		return false, nil
	}
	delivery, err := incomingMaterialUniqueDeliveryV1(receipt, before, proposal.AgentID, sender.AgentID)
	if err != nil {
		return false, err
	}
	if delivery.ResourceID != resourceID {
		return false, nil
	}
	resource, exists := artifactStateResourceV1(before, resourceID)
	if !exists || resource.Artifact != nil || len(resource.ReadableFacts) == 0 {
		return false, fmt.Errorf("incoming foundation route cannot read artifact contents")
	}
	// The plain document result has no separate timestamp. Its declared work's
	// actual endpoint is the reading time, never an invented same-cycle order.
	var end float64
	worked := false
	for _, execution := range resolution.SelfExecutions {
		if execution.TaskID == request.TaskID && selfExecutionActiveV2(execution.Status) && execution.EndDay != nil && (!worked || *execution.EndDay > end) {
			end, worked = *execution.EndDay, true
		}
	}
	if !worked {
		return false, fmt.Errorf("incoming foundation read lacks its declared actual work")
	}
	if err := validateIncomingMaterialReadTimeV1(receipt, before, after, proposal, resolution, request, delivery, end); err != nil {
		return false, err
	}
	return true, nil
}
