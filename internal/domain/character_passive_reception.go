package domain

import (
	"fmt"
	"strings"
)

const CharacterPassiveReceptionPolicyV2 = "character-passive-reception:receipt.v1"

// Passive reception changes only delivered owner knowledge, never an intent,
// location, resource, reply or acknowledgment by the sleeping recipient. Text
// and kind are not model fields: the host retrieves the exact source message.
type CharacterPassiveReceptionV2 struct {
	ToAgentID            string   `json:"to_agent_id"`
	FromAgentID          string   `json:"from_agent_id"`
	SourceProposalDigest string   `json:"source_proposal_digest"`
	CommunicationID      string   `json:"communication_id"`
	DeliveredAtDay       *float64 `json:"delivered_at_day"`
	Channel              string   `json:"channel"` // in_person / mechanism
	MechanismRef         string   `json:"mechanism_ref,omitempty"`
}

func HasCharacterPassiveReceptionPolicyV2(sources []string) bool {
	for _, source := range sources {
		if source == CharacterPassiveReceptionPolicyV2 {
			return true
		}
	}
	return false
}

func applyCharacterPassiveReceptionsV2(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
	if len(receipt.PassiveReceptions) == 0 {
		return nil
	}
	if !HasCharacterPassiveReceptionPolicyV2(stimulus.Sources) || receipt.Version != WorldArbitrationReceiptV2Version || receipt.StoryTime == nil || stimulus.StoryClock == nil || receipt.StoryTime.StartDay != stimulus.StoryClock.CurrentDay {
		return fmt.Errorf("passive reception requires its explicit v2 policy and actual story clock")
	}
	oldActors := map[string]CharacterPhysicalStateV2{}
	newActors := map[string]CharacterPhysicalStateV2{}
	actorIndex := map[string]int{}
	for _, actor := range before.Actors {
		oldActors[actor.AgentID] = actor
	}
	for i, actor := range after.Actors {
		newActors[actor.AgentID], actorIndex[actor.AgentID] = actor, i
	}
	mechanisms := map[string]bool{}
	for _, mechanism := range stimulus.Mechanisms {
		mechanisms[mechanism.ID] = true
	}
	seen := map[string]bool{}
	for _, reception := range receipt.PassiveReceptions {
		sender, proposed := proposals[reception.FromAgentID]
		sent, acted := resolutions[reception.FromAgentID]
		receiver, exists := oldActors[reception.ToAgentID]
		_, receiverProposed := proposals[reception.ToAgentID]
		_, receiverActed := resolutions[reception.ToAgentID]
		if !proposed || !acted || !exists || receiverProposed || receiverActed || reception.ToAgentID == reception.FromAgentID || sender.Digest != reception.SourceProposalDigest || sent.Outcome == "blocked" || sent.CompletionState == "blocked" {
			return fmt.Errorf("passive reception has an unexecuted/foreign source or a non-passive recipient")
		}
		if !physicalAmountV2(reception.DeliveredAtDay) || *reception.DeliveredAtDay < receipt.StoryTime.StartDay || *reception.DeliveredAtDay > receipt.StoryTime.EndDay {
			return fmt.Errorf("passive reception time is outside this actual cycle")
		}
		var message *CharacterCommunicationV2
		for i := range sender.Communications {
			if sender.Communications[i].ID == reception.CommunicationID {
				message = &sender.Communications[i]
				break
			}
		}
		if message == nil || message.ToCharacter != receiver.Character || strings.TrimSpace(message.Text) == "" {
			return fmt.Errorf("passive reception does not address the exact proposed communication recipient")
		}
		key := reception.ToAgentID + "/" + sender.Digest + "/" + message.ID
		if seen[key] {
			return fmt.Errorf("duplicate passive delivery for the same communication")
		}
		seen[key] = true
		switch reception.Channel {
		case "in_person":
			if reception.MechanismRef != "" {
				return fmt.Errorf("in-person delivery cannot invent a transport mechanism")
			}
			start, end := oldActors[sender.AgentID].Location, newActors[sender.AgentID].Location
			location := receiver.Location
			time := *reception.DeliveredAtDay
			atStart, atEnd := time == receipt.StoryTime.StartDay, time == receipt.StoryTime.EndDay
			if (atStart && start != location) || (atEnd && end != location) || (!atStart && !atEnd && (start != location || end != location)) {
				return fmt.Errorf("passive in-person delivery lacks a compatible actual sender/recipient location")
			}
		case "mechanism":
			if !mechanisms[reception.MechanismRef] || !physicalContainsRefV2(sender.MechanismRefs, reception.MechanismRef) || !physicalContainsRefV2(sent.MechanismRefs, reception.MechanismRef) {
				return fmt.Errorf("remote passive delivery requires an existing, proposed and actually applied world mechanism")
			}
		default:
			return fmt.Errorf("unsupported passive delivery channel")
		}
		if message.Kind == "conditional_response" && !passiveCommunicationConditionV2(*message, sender, sent, oldActors, newActors, proposals, resolutions, *reception.DeliveredAtDay) {
			return fmt.Errorf("passive conditional communication lacks an already received prerequisite")
		}
		fact := CharacterReceivedFactV2{Kind: message.Kind, Text: message.Text, SourceType: "communication", SourceID: message.ID, SourceProposalDigest: sender.Digest, FromAgentID: sender.AgentID, Chapter: receipt.Chapter, ReceivedAtDay: physicalNumberCopyV2(reception.DeliveredAtDay)}
		fact.ID = CharacterReceivedFactIDV2(receiver.AgentID, fact)
		for _, prior := range receiver.ReceivedFacts {
			if prior.SourceType == "communication" && prior.FromAgentID == sender.AgentID && prior.SourceProposalDigest == sender.Digest && prior.SourceID == message.ID {
				return fmt.Errorf("passive reception replays an already delivered source communication")
			}
		}
		index := actorIndex[receiver.AgentID]
		after.Actors[index].ReceivedFacts = append(append([]CharacterReceivedFactV2(nil), after.Actors[index].ReceivedFacts...), fact)
	}
	return nil
}

func passiveCommunicationConditionV2(message CharacterCommunicationV2, sender CharacterDecisionProposal, sent CharacterDecisionResolution, before, after map[string]CharacterPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution, deliveredAt float64) bool {
	match := func(fact CharacterReceivedFactV2) bool {
		return fact.SourceType == "communication" && fact.Kind == message.ConditionKind && before[fact.FromAgentID].Character == message.ConditionFromCharacter
	}
	// Same-chapter facts from a previous cycle are already source-authenticated.
	for _, fact := range before[sender.AgentID].ReceivedFacts {
		if match(fact) {
			return true
		}
	}
	for _, fact := range after[sender.AgentID].ReceivedFacts {
		if !match(fact) || (fact.ReceivedAtDay != nil && *fact.ReceivedAtDay > deliveredAt) {
			continue
		}
		cause, ok := proposals[fact.FromAgentID]
		causeResult, acted := resolutions[fact.FromAgentID]
		if !ok || !acted || cause.Digest != fact.SourceProposalDigest || causeResult.ActionOrder >= sent.ActionOrder {
			continue
		}
		for _, communication := range cause.Communications {
			if communication.ID == fact.SourceID && communication.ToCharacter == sender.Character && communication.Text == fact.Text && communication.Kind == fact.Kind {
				return true
			}
		}
	}
	return false
}
