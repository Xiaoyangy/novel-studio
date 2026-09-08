package domain

import "testing"

func TestPassiveConditionalResponseRequiresReceivedCause(t *testing.T) {
	message := CharacterCommunicationV2{Kind: "conditional_response", ConditionFromCharacter: "丙", ConditionKind: "request"}
	sender := CharacterDecisionProposal{AgentID: "ca_a", Character: "甲"}
	sent := CharacterDecisionResolution{ActionOrder: 2}
	actors := map[string]CharacterPhysicalStateV2{"ca_a": {AgentID: "ca_a", Character: "甲"}, "ca_c": {AgentID: "ca_c", Character: "丙"}}
	if passiveCommunicationConditionV2(message, sender, sent, actors, actors, nil, nil, 1) {
		t.Fatal("unreceived prerequisite allowed a conditional response")
	}
	// An authenticated fact in the previous cycle is usable even though its
	// chapter equals this chapter and its old proposal is not this round's set.
	old := actors["ca_a"]
	old.ReceivedFacts = []CharacterReceivedFactV2{{SourceType: "communication", Kind: "request", FromAgentID: "ca_c", Chapter: 1}}
	prior := map[string]CharacterPhysicalStateV2{"ca_a": old, "ca_c": actors["ca_c"]}
	if !passiveCommunicationConditionV2(message, sender, sent, prior, prior, nil, nil, 1) {
		t.Fatal("prior same-chapter received condition was forgotten")
	}
	clock := 2.0
	current := actors["ca_a"]
	current.ReceivedFacts = []CharacterReceivedFactV2{{SourceType: "communication", Kind: "request", FromAgentID: "ca_c", SourceProposalDigest: "source", SourceID: "ask", Text: "请告知", ReceivedAtDay: &clock}}
	after := map[string]CharacterPhysicalStateV2{"ca_a": current, "ca_c": actors["ca_c"]}
	proposals := map[string]CharacterDecisionProposal{"ca_c": {Digest: "source", Communications: []CharacterCommunicationV2{{ID: "ask", ToCharacter: "甲", Kind: "request", Text: "请告知"}}}}
	resolutions := map[string]CharacterDecisionResolution{"ca_c": {ActionOrder: 1}}
	if passiveCommunicationConditionV2(message, sender, sent, actors, after, proposals, resolutions, 1) {
		t.Fatal("future received condition caused an earlier reply")
	}
	clock = .5
	if !passiveCommunicationConditionV2(message, sender, sent, actors, after, proposals, resolutions, 1) {
		t.Fatal("actually prior current-cycle condition was dropped")
	}
	resolutions["ca_c"] = CharacterDecisionResolution{ActionOrder: 3}
	if passiveCommunicationConditionV2(message, sender, sent, actors, after, proposals, resolutions, 1) {
		t.Fatal("later action was used as an earlier response's cause")
	}
}
