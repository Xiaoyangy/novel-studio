package domain

import "fmt"

func validateResourceDeliveriesV2(receipt WorldArbitrationReceipt, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution, requireRefs func([]string) error) error {
	oldActors, newActors := map[string]CharacterPhysicalStateV2{}, map[string]CharacterPhysicalStateV2{}
	for _, actor := range before.Actors {
		oldActors[actor.AgentID] = actor
	}
	for _, actor := range after.Actors {
		newActors[actor.AgentID] = actor
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range before.Resources {
		catalog[resource.ResourceID] = resource
	}
	seen := map[string]bool{}
	for index, delivery := range receipt.ResourceDeliveries {
		resource, known := catalog[delivery.ResourceID]
		sender, sent := proposals[delivery.FromAgentID]
		source, acted := resolutions[delivery.FromAgentID]
		target, targetExists := newActors[delivery.ToAgentID]
		key := delivery.ResourceID + "/" + delivery.FromAgentID + "/" + delivery.ToAgentID
		if !known || !sent || !acted || !targetExists || sender.Digest != delivery.SourceProposalDigest || source.Outcome == "blocked" || source.CompletionState == "blocked" || seen[key] {
			return fmt.Errorf("resource delivery has an unknown/unexecuted/duplicate source, recipient or resource")
		}
		seen[key] = true
		if err := requireRefs(delivery.EvidenceRefs); err != nil {
			return fmt.Errorf("resource_deliveries[%s].evidence_refs: %w", delivery.ResourceID, err)
		}
		switch delivery.Access {
		case "none", "shared", "exclusive":
		default:
			return fmt.Errorf("resource delivery has invalid access grant")
		}
		if delivery.Access != "none" {
			granted := false
			for _, holding := range target.Resources {
				if holding.ResourceID == delivery.ResourceID && holding.Access == delivery.Access {
					granted = true
				}
			}
			if !granted {
				for _, later := range receipt.ResourceDeliveries[index+1:] {
					if later.ResourceID == delivery.ResourceID && later.FromAgentID == delivery.ToAgentID && later.ToAgentID != delivery.ToAgentID && later.Access != "none" {
						granted = true
					}
				}
			}
			if !granted {
				return fmt.Errorf("resource delivery access does not match recipient post_state")
			}
			priorAccess := actorHasReadableAccessV2(oldActors[delivery.FromAgentID], delivery.ResourceID)
			for _, earlier := range receipt.ResourceDeliveries[:index] {
				if earlier.ResourceID == delivery.ResourceID && earlier.ToAgentID == delivery.FromAgentID && earlier.Access != "none" {
					priorAccess = true
				}
			}
			if !priorAccess && len(resource.AccessRequiresAny) > 0 {
				opened := false
				targetResolution := resolutions[delivery.ToAgentID]
				for _, mechanism := range resource.AccessRequiresAny {
					if physicalContainsRefV2(source.MechanismRefs, mechanism) || (targetResolution.Outcome != "blocked" && targetResolution.CompletionState != "blocked" && physicalContainsRefV2(targetResolution.MechanismRefs, mechanism)) {
						opened = true
					}
				}
				if !opened {
					return fmt.Errorf("resource delivery did not apply a required access mechanism")
				}
			}
		}
		fields := map[string]bool{}
		for _, field := range delivery.ReceivedFields {
			if fields[field] {
				return fmt.Errorf("duplicate resource delivery received field")
			}
			fields[field] = true
			if field != "name" && field != "unit" && field != "amount" {
				return fmt.Errorf("unsupported resource delivery received field")
			}
			if !resourceReportContainsFieldV2(sender, delivery.ResourceID, target.Character, field) {
				return fmt.Errorf("resource delivery claims information not sent in the source report")
			}
		}
	}
	return nil
}

func hasResourceDeliveryV2(receipt WorldArbitrationReceipt, resourceID, from, to, proposalDigest, field, access string) bool {
	for _, delivery := range receipt.ResourceDeliveries {
		if delivery.ResourceID != resourceID || delivery.ToAgentID != to || (from != "" && delivery.FromAgentID != from) || (proposalDigest != "" && delivery.SourceProposalDigest != proposalDigest) {
			continue
		}
		if field != "" && !physicalContainsRefV2(delivery.ReceivedFields, field) {
			continue
		}
		if access != "" && delivery.Access != access {
			continue
		}
		return true
	}
	return false
}
