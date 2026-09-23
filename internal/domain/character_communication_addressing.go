package domain

import (
	"fmt"
	"strings"
)

const CharacterCommunicationAddressingPolicyV1 = "character-communication-addressing:bound-recipient.v1"

// This binds an existing non-named message to one actually reached recipient.
// Natural-language hint uniqueness is the arbiter's semantic judgment. The
// Host verifies the unique binding, original evidence and physical delivery;
// this record never supplies replacement message text or recipient knowledge.
type CharacterCommunicationReceptionV1 struct {
	ToAgentID            string   `json:"to_agent_id"`
	FromAgentID          string   `json:"from_agent_id"`
	SourceProposalDigest string   `json:"source_proposal_digest"`
	CommunicationID      string   `json:"communication_id"`
	DeliveredAtDay       *float64 `json:"delivered_at_day"`
	Channel              string   `json:"channel"`
	MechanismRef         string   `json:"mechanism_ref,omitempty"`
	RecipientResolution  string   `json:"recipient_resolution"`
	EvidenceRefs         []string `json:"evidence_refs"`
}

func HasCharacterCommunicationAddressingPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterCommunicationAddressingPolicyV1)
}

func validateCharacterCommunicationAddressV1(message CharacterCommunicationV2, observation CharacterObservationPacket) error {
	if message.RecipientHint == "" && message.ReplyToReceivedFactID == "" {
		if strings.TrimSpace(message.ToCharacter) == "" {
			return fmt.Errorf("communication requires its named recipient")
		}
		return nil
	}
	if !HasCharacterCommunicationAddressingPolicyV1(observation.Sources) || message.ToCharacter != "" || (message.RecipientHint != "" && message.ReplyToReceivedFactID != "") {
		return fmt.Errorf("non-named communication requires one exclusive address mode and its new policy")
	}
	if message.RecipientHint != "" {
		if strings.TrimSpace(message.RecipientHint) == "" {
			return fmt.Errorf("communication recipient hint must not be blank")
		}
		return nil
	}
	if !physicalContainsRefV2(message.KnowledgeRefs, message.ReplyToReceivedFactID) {
		return fmt.Errorf("reply must cite its received communication fact")
	}
	for _, fact := range observation.KnownFacts {
		if fact.ID == message.ReplyToReceivedFactID && strings.HasPrefix(fact.Kind, "received_") && communicationKindV2(strings.TrimPrefix(fact.Kind, "received_")) {
			return nil
		}
	}
	return fmt.Errorf("reply requires an actual communication fact in this owner's observation")
}

func communicationReceptionMessageV1(reception CharacterCommunicationReceptionV1, proposals map[string]CharacterDecisionProposal) (CharacterDecisionProposal, CharacterCommunicationV2, bool) {
	sender, ok := proposals[reception.FromAgentID]
	if !ok || sender.Digest != reception.SourceProposalDigest {
		return sender, CharacterCommunicationV2{}, false
	}
	for _, message := range sender.Communications {
		if message.ID == reception.CommunicationID {
			return sender, message, message.ToCharacter == "" && ((strings.TrimSpace(message.RecipientHint) != "") != (message.ReplyToReceivedFactID != "")) && strings.TrimSpace(message.Text) != "" && communicationKindV2(message.Kind)
		}
	}
	return sender, CharacterCommunicationV2{}, false
}

func communicationReceptionFactV1(receipt WorldArbitrationReceipt, reception CharacterCommunicationReceptionV1, message CharacterCommunicationV2) CharacterReceivedFactV2 {
	fact := CharacterReceivedFactV2{Kind: message.Kind, Text: message.Text, SourceType: "communication", SourceID: message.ID, SourceProposalDigest: reception.SourceProposalDigest, FromAgentID: reception.FromAgentID, Chapter: receipt.Chapter, ReceivedAtDay: physicalNumberCopyV2(reception.DeliveredAtDay)}
	fact.ID = CharacterReceivedFactIDV2(reception.ToAgentID, fact)
	return fact
}

// Only the exact Host-derived value is admitted during normalized receipt
// replay. All reception rows are independently validated before Apply returns.
func isCommunicationReceptionFactV1(receipt WorldArbitrationReceipt, owner string, fact CharacterReceivedFactV2, proposals map[string]CharacterDecisionProposal) bool {
	for _, reception := range receipt.CommunicationReceptions {
		if reception.ToAgentID != owner {
			continue
		}
		_, message, ok := communicationReceptionMessageV1(reception, proposals)
		if ok && samePhysicalValueV2(fact, communicationReceptionFactV1(receipt, reception, message)) {
			return true
		}
	}
	return false
}

func communicationSourceTupleV1(from, digest, id string) string {
	return from + "\x00" + digest + "\x00" + id
}

func communicationLocationAtV1(old, next CharacterPhysicalStateV2, receipt WorldArbitrationReceipt, at float64) (string, bool) {
	if old.AgentID == "" || old.AgentID != next.AgentID || old.Location == "" || next.Location == "" {
		return "", false
	}
	start, end := receipt.StoryTime.StartDay, receipt.StoryTime.EndDay
	if at == start && at == end {
		return old.Location, old.Location == next.Location
	}
	if at == start {
		return old.Location, true
	}
	if at == end {
		return next.Location, true
	}
	return old.Location, old.Location == next.Location
}

func applyCharacterCommunicationReceptionsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
	if len(receipt.CommunicationReceptions) == 0 {
		return nil
	}
	if !HasCharacterCommunicationAddressingPolicyV1(stimulus.Sources) || receipt.Version != WorldArbitrationReceiptV2Version || receipt.StoryTime == nil || stimulus.StoryClock == nil || receipt.StoryTime.StartDay != stimulus.StoryClock.CurrentDay {
		return fmt.Errorf("communication reception requires its new policy and actual story clock")
	}
	oldActors, newActors, indexes := map[string]CharacterPhysicalStateV2{}, map[string]CharacterPhysicalStateV2{}, map[string]int{}
	for _, actor := range before.Actors {
		oldActors[actor.AgentID] = actor
	}
	for i, actor := range after.Actors {
		newActors[actor.AgentID], indexes[actor.AgentID] = actor, i
	}
	mechanisms := map[string]bool{}
	for _, mechanism := range stimulus.Mechanisms {
		mechanisms[mechanism.ID] = true
	}
	type boundReception struct {
		row     CharacterCommunicationReceptionV1
		sender  CharacterDecisionProposal
		sent    CharacterDecisionResolution
		message CharacterCommunicationV2
		fact    CharacterReceivedFactV2
	}
	var bound []boundReception
	seen := map[string]bool{}
	for _, reception := range receipt.CommunicationReceptions {
		sender, message, proposed := communicationReceptionMessageV1(reception, proposals)
		sent, acted := resolutions[reception.FromAgentID]
		receiver, exists := oldActors[reception.ToAgentID]
		if !proposed || !acted || !exists || oldActors[reception.FromAgentID].AgentID == "" || reception.ToAgentID == reception.FromAgentID || sent.Outcome == "blocked" || sent.CompletionState == "blocked" {
			return fmt.Errorf("communication reception has an unexecuted, foreign or named source/recipient")
		}
		if reception.RecipientResolution != "unique" || len(reception.EvidenceRefs) == 0 {
			return fmt.Errorf("communication reception requires the arbiter's unique recipient resolution and original evidence")
		}
		for _, ref := range reception.EvidenceRefs {
			if ref == "" || !physicalContainsRefV2(message.KnowledgeRefs, ref) {
				return fmt.Errorf("recipient resolution uses evidence absent from the original communication")
			}
		}
		if message.ReplyToReceivedFactID != "" {
			matched := false
			for _, prior := range oldActors[sender.AgentID].ReceivedFacts {
				if prior.ID == message.ReplyToReceivedFactID && prior.SourceType == "communication" && prior.FromAgentID == receiver.AgentID {
					if message.Kind != "conditional_response" || (message.ConditionFromCharacter == "" && message.ConditionKind == prior.Kind) {
						matched = true
					}
				}
			}
			if !matched || !physicalContainsRefV2(message.KnowledgeRefs, message.ReplyToReceivedFactID) || !physicalContainsRefV2(reception.EvidenceRefs, message.ReplyToReceivedFactID) {
				return fmt.Errorf("reply recipient must be the exact sender of this owner's cited received communication")
			}
		}
		if !physicalAmountV2(reception.DeliveredAtDay) || *reception.DeliveredAtDay < receipt.StoryTime.StartDay || *reception.DeliveredAtDay > receipt.StoryTime.EndDay {
			return fmt.Errorf("communication reception time is outside this actual cycle")
		}
		key := communicationSourceTupleV1(sender.AgentID, sender.Digest, message.ID)
		if seen[key] {
			return fmt.Errorf("one communication cannot bind multiple or duplicate recipients")
		}
		seen[key] = true
		for _, actor := range before.Actors {
			for _, prior := range actor.ReceivedFacts {
				if prior.SourceType == "communication" && communicationSourceTupleV1(prior.FromAgentID, prior.SourceProposalDigest, prior.SourceID) == key {
					return fmt.Errorf("communication source was already delivered")
				}
			}
		}
		switch reception.Channel {
		case "in_person":
			if reception.MechanismRef != "" {
				return fmt.Errorf("in-person communication cannot invent a mechanism")
			}
			from, fromKnown := communicationLocationAtV1(oldActors[sender.AgentID], newActors[sender.AgentID], receipt, *reception.DeliveredAtDay)
			to, toKnown := communicationLocationAtV1(receiver, newActors[receiver.AgentID], receipt, *reception.DeliveredAtDay)
			if !fromKnown || !toKnown || from != to {
				return fmt.Errorf("in-person communication requires both actors at the same proven location at delivery")
			}
		case "mechanism":
			if !mechanisms[reception.MechanismRef] || !physicalContainsRefV2(sender.MechanismRefs, reception.MechanismRef) || !physicalContainsRefV2(sent.MechanismRefs, reception.MechanismRef) {
				return fmt.Errorf("remote communication requires an existing, proposed and actually applied mechanism")
			}
		default:
			return fmt.Errorf("unsupported communication reception channel")
		}
		bound = append(bound, boundReception{reception, sender, sent, message, communicationReceptionFactV1(receipt, reception, message)})
	}
	for _, delivery := range bound {
		if delivery.message.Kind == "conditional_response" && (delivery.message.ReplyToReceivedFactID == "" || delivery.message.ConditionFromCharacter != "") {
			return fmt.Errorf("non-named conditional reception requires its exact before-state reply fact, not canonical identity matching")
		}
		index := indexes[delivery.row.ToAgentID]
		present := false
		for _, existing := range after.Actors[index].ReceivedFacts {
			if existing.SourceType != "communication" || communicationSourceTupleV1(existing.FromAgentID, existing.SourceProposalDigest, existing.SourceID) != communicationSourceTupleV1(delivery.row.FromAgentID, delivery.row.SourceProposalDigest, delivery.row.CommunicationID) {
				continue
			}
			if present || !samePhysicalValueV2(existing, delivery.fact) {
				return fmt.Errorf("received communication differs from its exact host-derived receipt")
			}
			present = true
		}
		if !present {
			after.Actors[index].ReceivedFacts = append(append([]CharacterReceivedFactV2(nil), after.Actors[index].ReceivedFacts...), delivery.fact)
		}
	}
	return nil
}
