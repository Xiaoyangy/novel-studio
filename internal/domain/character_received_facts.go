package domain

import (
	"fmt"
	"strings"
)

type CharacterCommunicationV2 struct {
	ID                     string   `json:"id"`
	ToCharacter            string   `json:"to_character"`
	Kind                   string   `json:"kind"`
	Text                   string   `json:"text"`
	KnowledgeRefs          []string `json:"knowledge_refs"`
	ConditionFromCharacter string   `json:"condition_from_character,omitempty"`
	ConditionKind          string   `json:"condition_kind,omitempty"`
}

type ResourceReadRequestV2 struct {
	ResourceID           string `json:"resource_id,omitempty"`
	IncomingDeliveryFrom string `json:"incoming_delivery_from,omitempty"`
}

type ResourceReadableFactV2 struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type CharacterReceivedFactV2 struct {
	ID                   string   `json:"id,omitempty"`
	Kind                 string   `json:"kind"`
	Text                 string   `json:"text"`
	SourceType           string   `json:"source_type"`
	SourceID             string   `json:"source_id"`
	SourceProposalDigest string   `json:"source_proposal_digest"`
	FromAgentID          string   `json:"from_agent_id,omitempty"`
	ResourceID           string   `json:"resource_id,omitempty"`
	Chapter              int      `json:"chapter"`
	ReceivedAtDay        *float64 `json:"received_at_day,omitempty"`
}

func CharacterReceivedFactIDV2(agentID string, fact CharacterReceivedFactV2) string {
	fact.ID = ""
	digest, _ := characterAgentDigest(struct {
		AgentID string                  `json:"agent_id"`
		Fact    CharacterReceivedFactV2 `json:"fact"`
	}{agentID, fact})
	return "recv_" + strings.TrimPrefix(digest, "sha256:")
}

func assignCharacterReceivedFactIDsV2(state WorldPhysicalStateV2) WorldPhysicalStateV2 {
	state.Actors = append([]CharacterPhysicalStateV2(nil), state.Actors...)
	for i := range state.Actors {
		actor := &state.Actors[i]
		actor.ReceivedFacts = append([]CharacterReceivedFactV2(nil), actor.ReceivedFacts...)
		for j := range actor.ReceivedFacts {
			if actor.ReceivedFacts[j].ID == "" {
				actor.ReceivedFacts[j].ID = CharacterReceivedFactIDV2(actor.AgentID, actor.ReceivedFacts[j])
			}
		}
	}
	return state
}

func validateCharacterReceivedFactsShapeV2(actor CharacterPhysicalStateV2) error {
	seen := map[string]bool{}
	for _, fact := range actor.ReceivedFacts {
		if fact.ReceivedAtDay != nil && (fact.SourceType != "communication" || !physicalAmountV2(fact.ReceivedAtDay)) {
			return fmt.Errorf("received fact has an invalid actual delivery time")
		}
		if fact.ID == "" || seen[fact.ID] || fact.ID != CharacterReceivedFactIDV2(actor.AgentID, fact) || strings.TrimSpace(fact.Text) == "" || strings.TrimSpace(fact.SourceID) == "" || fact.Chapter <= 0 {
			return fmt.Errorf("actor received fact identity/text/source is invalid")
		}
		seen[fact.ID] = true
		if err := validatePlanningV2Digest("received fact source proposal", fact.SourceProposalDigest); err != nil {
			return err
		}
		switch fact.SourceType {
		case "communication":
			if !communicationKindV2(fact.Kind) || fact.FromAgentID == "" || fact.ResourceID != "" {
				return fmt.Errorf("received communication has invalid source/kind")
			}
		case "resource_read":
			if fact.Kind != "document_statement" || !physicalResourceIDV2(fact.ResourceID) {
				return fmt.Errorf("received document fact requires exact resource source and document_statement kind")
			}
		default:
			return fmt.Errorf("received fact has unsupported source_type")
		}
	}
	return nil
}

func ValidateCharacterKnowledgeIntentV2(proposal CharacterDecisionProposal, observation CharacterObservationPacket) error {
	if observation.Version != CharacterObservationV2Version {
		if len(proposal.Communications)+len(proposal.ResourceReads) > 0 {
			return fmt.Errorf("communication/read intents require v2 observation")
		}
		return nil
	}
	allowed := observation.AllowedFactIDs()
	seen := map[string]bool{}
	for _, communication := range proposal.Communications {
		if !physicalIdentityV2(communication.ID) || seen[communication.ID] || strings.TrimSpace(communication.ToCharacter) == "" || strings.TrimSpace(communication.Text) == "" || !communicationKindV2(communication.Kind) || len(communication.KnowledgeRefs) == 0 {
			return fmt.Errorf("invalid or duplicate communication intent")
		}
		seen[communication.ID] = true
		for _, ref := range communication.KnowledgeRefs {
			if _, ok := allowed[ref]; !ok {
				return fmt.Errorf("communication uses unavailable knowledge ref %q", ref)
			}
		}
		if communication.Kind == "conditional_response" {
			if communication.ConditionFromCharacter == "" || !communicationKindV2(communication.ConditionKind) || communication.ConditionKind == "conditional_response" {
				return fmt.Errorf("conditional response requires a grounded source character and nonconditional communication kind")
			}
		} else if communication.ConditionFromCharacter != "" || communication.ConditionKind != "" {
			return fmt.Errorf("only conditional_response may declare a condition")
		}
	}
	views := map[string]bool{}
	for _, view := range observation.ResourceViews {
		views[view.ResourceID] = true
	}
	seen = map[string]bool{}
	for _, request := range proposal.ResourceReads {
		if (request.ResourceID == "") == (request.IncomingDeliveryFrom == "") {
			return fmt.Errorf("resource read must choose resource_id or incoming_delivery_from")
		}
		key := request.ResourceID + "/" + request.IncomingDeliveryFrom
		if seen[key] || (request.ResourceID != "" && !views[request.ResourceID]) {
			return fmt.Errorf("resource read references an unseen resource or repeats a condition")
		}
		seen[key] = true
	}
	return nil
}

func validateReceivedFactsTransitionV2(receipt WorldArbitrationReceipt, before, after WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
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
	issues := &receivedFactTransitionErrorsV2{}
	resolutionIndex := map[string]int{}
	for index, resolution := range receipt.Resolutions {
		resolutionIndex[resolution.AgentID] = index
	}
	for actorIndex, actor := range after.Actors {
		old := map[string]CharacterReceivedFactV2{}
		for _, fact := range oldActors[actor.AgentID].ReceivedFacts {
			old[fact.ID] = fact
		}
		seen := map[string]bool{}
		for factIndex, fact := range actor.ReceivedFacts {
			path := receivedFactPathV2(resolutionIndex, actor.AgentID, actorIndex, factIndex)
			seen[fact.ID] = true
			if previous, exists := old[fact.ID]; exists {
				if !samePhysicalValueV2(previous, fact) {
					issues.add("received fact was rewritten", path, "previous_fact_rewritten", fact.SourceID, "")
				}
				continue
			}
			if fact.Chapter != receipt.Chapter {
				issues.add("new received fact must bind current chapter", path, "chapter_mismatch", fact.SourceID, "")
				continue
			}
			if fact.ReceivedAtDay != nil {
				issues.add("new received_at_day is derived only from a host-validated passive reception", path, "active_received_at_day_forbidden", fact.SourceID, "")
				continue
			}
			ownerResolution, ownerActed := resolutions[actor.AgentID]
			if !ownerActed {
				issues.add("new received fact requires an explicit actor post_state", path, "owner_resolution_missing", fact.SourceID, "")
				continue
			}
			valid := false
			code, relatedPath := "source_tuple_mismatch", ""
			switch fact.SourceType {
			case "communication":
				sender, exists := proposals[fact.FromAgentID]
				delivery, acted := resolutions[fact.FromAgentID]
				if !exists || !acted || sender.Digest != fact.SourceProposalDigest || delivery.Outcome == "blocked" || delivery.CompletionState == "blocked" {
					switch {
					case !exists:
						code = "sender_proposal_missing"
					case !acted:
						code = "sender_resolution_missing"
					case sender.Digest != fact.SourceProposalDigest:
						code = "sender_proposal_mismatch"
					case delivery.Outcome == "blocked":
						code = "sender_outcome_blocked"
						relatedPath = receivedFactResolutionPathV2(resolutionIndex, fact.FromAgentID, "outcome")
					case delivery.CompletionState == "blocked":
						code = "sender_completion_blocked"
						relatedPath = receivedFactResolutionPathV2(resolutionIndex, fact.FromAgentID, "completion_state")
					}
					break
				}
				for _, communication := range sender.Communications {
					if communication.ID != fact.SourceID || communication.ToCharacter != actor.Character || communication.Kind != fact.Kind || communication.Text != fact.Text {
						continue
					}
					if communication.Kind == "conditional_response" && !receivedCommunicationConditionV2(communication, newActors[sender.AgentID], newActors, proposals, receipt.Chapter) {
						code = "condition_not_received"
						continue
					}
					valid = true
				}
			case "resource_read":
				owner, exists := proposals[actor.AgentID]
				if !exists || owner.Digest != fact.SourceProposalDigest || ownerResolution.Outcome == "blocked" || ownerResolution.CompletionState == "blocked" || (fact.FromAgentID != "" && fact.FromAgentID != actor.AgentID) {
					switch {
					case !exists:
						code = "reader_proposal_missing"
					case owner.Digest != fact.SourceProposalDigest:
						code = "reader_proposal_mismatch"
					case ownerResolution.Outcome == "blocked":
						code = "reader_outcome_blocked"
						relatedPath = receivedFactResolutionPathV2(resolutionIndex, actor.AgentID, "outcome")
					case ownerResolution.CompletionState == "blocked":
						code = "reader_completion_blocked"
						relatedPath = receivedFactResolutionPathV2(resolutionIndex, actor.AgentID, "completion_state")
					default:
						code = "reader_identity_mismatch"
					}
					break
				}
				requested := false
				for _, request := range owner.ResourceReads {
					if request.ResourceID == fact.ResourceID {
						requested = true
					}
					if request.IncomingDeliveryFrom != "" {
						for _, delivery := range receipt.ResourceDeliveries {
							if delivery.ResourceID == fact.ResourceID && delivery.ToAgentID == actor.AgentID && delivery.Access != "none" && oldActors[delivery.FromAgentID].Character == request.IncomingDeliveryFrom {
								requested = true
							}
						}
					}
				}
				readable := actorHasReadableAccessV2(oldActors[actor.AgentID], fact.ResourceID) || actorHasReadableAccessV2(actor, fact.ResourceID)
				for _, delivery := range receipt.ResourceDeliveries {
					if delivery.ResourceID == fact.ResourceID && delivery.ToAgentID == actor.AgentID && delivery.Access != "none" {
						readable = true
					}
				}
				if !requested || !readable {
					code = "read_not_requested"
					if requested {
						code = "no_read_access"
					}
					break
				}
				for _, source := range catalog[fact.ResourceID].ReadableFacts {
					if source.ID == fact.SourceID && source.Text == fact.Text && fact.Kind == "document_statement" {
						valid = true
					}
				}
			}
			if !valid {
				prefix := fmt.Sprintf("actor %s received fact %s lacks an exact delivered communication or authorized document read", receivedFactDiagnosticIDV2(actor.AgentID), receivedFactDiagnosticIDV2(fact.SourceID))
				issues.add(prefix, path, code, fact.SourceID, relatedPath)
			}
		}
		// Source order gives deterministic diagnostics; never iterate the map
		// when several previous facts were dropped in the same attempted patch.
		for _, fact := range oldActors[actor.AgentID].ReceivedFacts {
			if !seen[fact.ID] {
				prefix := fmt.Sprintf("actor %s dropped a previously received fact", receivedFactDiagnosticIDV2(actor.AgentID))
				issues.add(prefix, receivedFactPathV2(resolutionIndex, actor.AgentID, actorIndex, -1), "previous_fact_dropped", fact.SourceID, "")
			}
		}
	}
	if issues.prefix != "" {
		return issues
	}
	return nil
}

func receivedCommunicationConditionV2(communication CharacterCommunicationV2, sender CharacterPhysicalStateV2, actors map[string]CharacterPhysicalStateV2, proposals map[string]CharacterDecisionProposal, chapter int) bool {
	for _, fact := range sender.ReceivedFacts {
		if fact.SourceType != "communication" || fact.Kind != communication.ConditionKind || actors[fact.FromAgentID].Character != communication.ConditionFromCharacter {
			continue
		}
		if fact.Chapter < chapter {
			return true
		}
		// Prior facts are already authenticated by the predecessor physical
		// state. Current facts must also correspond to a concrete proposal.
		if source, ok := proposals[fact.FromAgentID]; ok && source.Digest == fact.SourceProposalDigest {
			for _, sent := range source.Communications {
				if sent.ID == fact.SourceID && sent.ToCharacter == sender.Character && sent.Text == fact.Text && sent.Kind == fact.Kind {
					return true
				}
			}
		}
	}
	return false
}

func actorHasReadableAccessV2(actor CharacterPhysicalStateV2, resourceID string) bool {
	for _, holding := range actor.Resources {
		if holding.ResourceID == resourceID && holding.Access != "none" {
			return true
		}
	}
	return false
}
func communicationKindV2(kind string) bool {
	switch kind {
	case "statement", "commitment", "information", "request", "conditional_response":
		return true
	}
	return false
}
