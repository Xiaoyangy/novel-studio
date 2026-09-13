package domain

import "fmt"

func artifactIntentTaskV1(p CharacterDecisionProposal, taskID, resourceID string) bool {
	for _, task := range p.SelfTasks {
		if task.TaskID == taskID && task.Kind == "work" && physicalContainsRefV2(task.ResourceIDs, resourceID) {
			return true
		}
	}
	return false
}

func artifactBoundTaskTimeV1(receipt WorldArbitrationReceipt, resolution CharacterDecisionResolution, proposal CharacterDecisionProposal, old CharacterPhysicalStateV2, taskID, resourceID string, at float64) bool {
	if !artifactIntentTaskV1(proposal, taskID, resourceID) || !artifactActualWorkTimeV1(receipt, resolution, proposal, at) {
		return false
	}
	for _, execution := range resolution.SelfExecutions {
		if execution.TaskID != taskID || !selfExecutionActiveV2(execution.Status) || execution.StartDay == nil || execution.EndDay == nil || at < *execution.StartDay-1e-12 || at > *execution.EndDay+1e-12 {
			continue
		}
		if resolution.PostState != nil && old.Location == resolution.PostState.Location {
			return true
		}
		for _, move := range resolution.SelfExecutions {
			if move.Status != "completed" || move.EndDay == nil || *move.EndDay > *execution.StartDay+1e-12 {
				continue
			}
			for _, task := range proposal.SelfTasks {
				if task.TaskID == move.TaskID && task.Kind == "carry" {
					return true
				}
			}
		}
	}
	return false
}

func validateArtifactDeliveriesV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal) error {
	for _, delivery := range receipt.ResourceDeliveries {
		resource, exists := artifactStateResourceV1(before, delivery.ResourceID)
		if !exists || resource.Artifact == nil {
			if delivery.ArtifactVersionDigest != "" {
				return fmt.Errorf("artifact version cannot be attached to a non-artifact delivery")
			}
			continue
		}
		artifact := resource.Artifact
		if delivery.ArtifactVersionDigest != artifact.VersionDigest {
			return fmt.Errorf("artifact delivery lacks its exact content version")
		}
		if delivery.DeliveredAtDay != nil {
			if err := validateIncomingMaterialDeliveryV1(receipt, stimulus, before, after, proposals, delivery); err != nil {
				return err
			}
			continue
		}
		sender, ok := proposals[delivery.FromAgentID]
		if !ok {
			return fmt.Errorf("artifact delivery source is unavailable")
		}
		target, ok := artifactStateActorV1(after, delivery.ToAgentID)
		if !ok {
			return fmt.Errorf("artifact delivery recipient is unavailable")
		}
		actor, ok := artifactStateActorV1(after, delivery.FromAgentID)
		if !ok {
			return fmt.Errorf("artifact custodian is unavailable")
		}
		if artifact.Placement.CustodianAgentID != sender.AgentID || artifact.Placement.Location != actor.Location || actor.Location != target.Location {
			return fmt.Errorf("physical artifact access must be actually granted by its same-place custodian")
		}
		bound := false
		for _, intent := range sender.ArtifactAccess {
			if intent.ResourceID == delivery.ResourceID && intent.VersionDigest == delivery.ArtifactVersionDigest && intent.ToCharacter == target.Character && intent.Access == delivery.Access {
				bound = true
			}
		}
		if !bound {
			return fmt.Errorf("artifact delivery was not independently authorized in its custodian's proposal")
		}
	}
	return nil
}

func artifactSelectedClaimsV1(artifact CharacterWorkArtifactV1, ids []string) ([]CharacterWorkArtifactClaimV1, error) {
	if len(ids) == 0 || len(normalizeV2Strings(ids)) != len(ids) {
		return nil, fmt.Errorf("actual artifact read/sign requires nonempty unique claim ids")
	}
	var result []CharacterWorkArtifactClaimV1
	for _, id := range ids {
		found := false
		for _, claim := range artifact.Claims {
			if claim.ClaimID == id {
				result = append(result, continuationCloneV1(claim))
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("artifact result references a claim absent from the actual version")
		}
	}
	return result, nil
}

func artifactClaimsKnownV1(owner CharacterPhysicalStateV2, id, version string, claims []string) bool {
	known := map[string]bool{}
	for _, knowledge := range owner.ArtifactKnowledge {
		if knowledge.ResourceID == id && knowledge.VersionDigest == version {
			for _, claim := range knowledge.Claims {
				known[claim.ClaimID] = true
			}
		}
	}
	for _, claim := range claims {
		if !known[claim] {
			return false
		}
	}
	return len(claims) > 0
}

func appendArtifactKnowledgeV1(actor *CharacterPhysicalStateV2, knowledge CharacterArtifactKnowledgeV1) error {
	for _, previous := range actor.ArtifactKnowledge {
		if previous.KnowledgeDigest == knowledge.KnowledgeDigest {
			if !samePhysicalValueV2(previous, knowledge) {
				return fmt.Errorf("artifact knowledge digest conflicts")
			}
			return nil
		}
	}
	if len(actor.ArtifactKnowledge) >= CharacterWorkArtifactLimitV1*CharacterWorkArtifactRevisionLimitV1 {
		return fmt.Errorf("artifact knowledge budget exceeded")
	}
	actor.ArtifactKnowledge = append(actor.ArtifactKnowledge, knowledge)
	return nil
}

func (transition *workArtifactTransitionV1) finish(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
	if transition == nil {
		return nil
	}
	// Only explicit completed carry/place may change an existing artifact's
	// physical location. Mere actor movement never drops or teleports it.
	for index := range after.Resources {
		resource := &after.Resources[index]
		if resource.Artifact == nil {
			continue
		}
		artifact := resource.Artifact
		if transition.created[resource.ResourceID] != "" {
			continue
		}
		oldResource, ok := artifactStateResourceV1(before, resource.ResourceID)
		if !ok || oldResource.Artifact == nil {
			return fmt.Errorf("artifact lost its original physical source")
		}
		old := oldResource.Artifact
		for _, delivery := range receipt.ResourceDeliveries {
			if delivery.ResourceID == resource.ResourceID && delivery.Access == "exclusive" {
				recipient, _ := artifactStateActorV1(*after, delivery.ToAgentID)
				artifact.Placement = CharacterArtifactPlacementV1{Kind: "with_actor", Location: recipient.Location, CustodianAgentID: recipient.AgentID}
			}
		}
		if artifact.Placement.CustodianAgentID != old.Placement.CustodianAgentID {
			continue
		}
		owner, ok := artifactStateActorV1(*after, old.Placement.CustodianAgentID)
		if !ok {
			return fmt.Errorf("artifact custodian vanished")
		}
		resolution, active := resolutions[owner.AgentID]
		if active {
			for _, fact := range owner.SelfExperiences {
				if fact.Evaluation == nil || fact.Evaluation.GenerationID != receipt.GenerationID || fact.Chapter != receipt.Chapter || fact.Evaluation.Cycle != stimulus.SelfEvaluationContext.Cycle || fact.SourceProposalDigest != resolution.ProposalDigest || fact.Status != "completed" || !physicalContainsRefV2(fact.ResourceIDs, resource.ResourceID) {
					continue
				}
				if fact.Kind == "carry" {
					artifact.Placement = CharacterArtifactPlacementV1{Kind: "with_actor", Location: fact.Location, CustodianAgentID: owner.AgentID}
				}
				if fact.Kind == "place" {
					artifact.Placement = CharacterArtifactPlacementV1{Kind: "stored", Location: fact.Location, CustodianAgentID: owner.AgentID}
				}
			}
		}
		if artifact.Placement.Kind == "with_actor" && artifact.Placement.Location != owner.Location {
			return fmt.Errorf("moving with an artifact requires its explicit completed carry; no implicit loss/teleport")
		}
	}
	for actorIndex := range after.Actors {
		actor := &after.Actors[actorIndex]
		resolution, active := resolutions[actor.AgentID]
		if !active {
			continue
		}
		proposal := proposals[actor.AgentID]
		old, _ := artifactStateActorV1(before, actor.AgentID)
		oldFacts := map[string]bool{}
		for _, fact := range old.ReceivedFacts {
			oldFacts[fact.ID] = true
		}
		for _, fact := range actor.ReceivedFacts {
			if !oldFacts[fact.ID] && fact.SourceType == "resource_read" {
				resource, _ := artifactStateResourceV1(*after, fact.ResourceID)
				if resource.Artifact != nil {
					return fmt.Errorf("artifact contents require exact versioned read results, not unversioned received_facts")
				}
			}
		}
		readKeys := map[string]bool{}
		for _, result := range resolution.ArtifactReadResults {
			key := result.ResourceID + "/" + result.VersionDigest
			resource, known := artifactStateResourceV1(*after, result.ResourceID)
			holding, accessible := artifactHoldingV1(*actor, result.ResourceID)
			if !known || resource.Artifact == nil || resource.Artifact.VersionDigest != result.VersionDigest || !accessible || holding.Access == "none" || holding.Perception.Kind == "unaware" || actor.Location != resource.Artifact.Placement.Location || readKeys[key] || !artifactActualWorkTimeV1(receipt, resolution, proposal, result.AtDay) {
				return fmt.Errorf("artifact read result lacks actual same-place access, version or work interval")
			}
			readKeys[key] = true
			bound := false
			for _, intent := range proposal.ArtifactReads {
				if intent.ResourceID == result.ResourceID && intent.VersionDigest == result.VersionDigest && (len(intent.ClaimIDs) == 0 || physicalContainsAllRefsV2(intent.ClaimIDs, result.ClaimIDs)) && artifactBoundTaskTimeV1(receipt, resolution, proposal, old, intent.TaskID, result.ResourceID, result.AtDay) {
					bound = true
				}
			}
			if !bound && HasCharacterIncomingMaterialReadPolicyV1(stimulus.Sources) {
				var err error
				bound, err = bindIncomingMaterialArtifactReadV1(receipt, stimulus, before, *after, proposals, proposal, resolution, result)
				if err != nil {
					return err
				}
			}
			if !bound {
				return fmt.Errorf("artifact reading was not requested for this exact version/scope")
			}
			claims, err := artifactSelectedClaimsV1(*resource.Artifact, result.ClaimIDs)
			if err != nil {
				return err
			}
			var signatures []CharacterArtifactSignatureReceiptV1
			for _, signature := range resource.Artifact.Signatures {
				if signature.VersionDigest == result.VersionDigest && signature.AtDay <= result.AtDay && physicalContainsAllRefsV2(result.ClaimIDs, signature.ClaimIDs) {
					signatures = append(signatures, signature)
				}
			}
			knowledge, err := newArtifactKnowledgeV1(receipt, stimulus, proposal, *resource.Artifact, "read", claims, result.AtDay, signatures)
			if err != nil {
				return err
			}
			if err := appendArtifactKnowledgeV1(actor, knowledge); err != nil {
				return err
			}
		}
		signed := map[string]bool{}
		for _, result := range resolution.ArtifactSignatures {
			key := result.ResourceID + "/" + result.VersionDigest
			resource, known := artifactStateResourceV1(*after, result.ResourceID)
			holding, accessible := artifactHoldingV1(*actor, result.ResourceID)
			if !known || resource.Artifact == nil || resource.Artifact.Status != "complete" || resource.Artifact.VersionDigest != result.VersionDigest || !accessible || holding.Access == "none" || actor.Location != resource.Artifact.Placement.Location || signed[key] || !artifactActualWorkTimeV1(receipt, resolution, proposal, result.AtDay) || !artifactClaimsKnownV1(old, result.ResourceID, result.VersionDigest, result.ClaimIDs) {
				return fmt.Errorf("artifact signature requires its signer's previously authored/read exact complete version and actual same-place work")
			}
			signed[key] = true
			bound := false
			for _, intent := range proposal.ArtifactSigns {
				if intent.ResourceID == result.ResourceID && intent.VersionDigest == result.VersionDigest && intent.Scope == result.Scope && samePhysicalValueV2(intent.ClaimIDs, result.ClaimIDs) && artifactBoundTaskTimeV1(receipt, resolution, proposal, old, intent.TaskID, result.ResourceID, result.AtDay) {
					bound = true
				}
			}
			if !bound {
				return fmt.Errorf("artifact signature changed the signer's original version/claims/scope")
			}
			for _, previous := range resource.Artifact.Signatures {
				if previous.SignerAgentID == actor.AgentID && previous.VersionDigest == result.VersionDigest {
					return fmt.Errorf("artifact version is already signed by this actor")
				}
			}
			signature := CharacterArtifactSignatureReceiptV1{GenerationID: receipt.GenerationID, ResourceID: result.ResourceID, VersionDigest: result.VersionDigest, ClaimIDs: append([]string(nil), result.ClaimIDs...), Scope: result.Scope, AtDay: result.AtDay, SignerAgentID: actor.AgentID, Signer: actor.Character, SourceProposalDigest: proposal.Digest, Chapter: receipt.Chapter, Cycle: stimulus.SelfEvaluationContext.Cycle}
			var err error
			signature.SignatureDigest, err = ComputeCharacterArtifactSignatureDigestV1(signature)
			if err != nil {
				return err
			}
			for i := range after.Resources {
				if after.Resources[i].ResourceID == resource.ResourceID {
					after.Resources[i].Artifact.Signatures = append(after.Resources[i].Artifact.Signatures, signature)
					resource = after.Resources[i]
					break
				}
			}
			claims, err := artifactSelectedClaimsV1(*resource.Artifact, result.ClaimIDs)
			if err != nil {
				return err
			}
			knowledge, err := newArtifactKnowledgeV1(receipt, stimulus, proposal, *resource.Artifact, "signed", claims, result.AtDay, []CharacterArtifactSignatureReceiptV1{signature})
			if err != nil {
				return err
			}
			if err := appendArtifactKnowledgeV1(actor, knowledge); err != nil {
				return err
			}
		}
		claimed := transition.claimedKnowledge[actor.AgentID]
		if len(claimed) > 0 && !samePhysicalValueV2(claimed, old.ArtifactKnowledge) && !samePhysicalValueV2(claimed, actor.ArtifactKnowledge) {
			return fmt.Errorf("arbiter changed the owner's Host-derived artifact knowledge")
		}
	}
	return nil
}
