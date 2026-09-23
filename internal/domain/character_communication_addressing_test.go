package domain_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestCommunicationAddressingIntentRequiresOneSourceBoundMode(t *testing.T) {
	observation := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: []string{domain.CharacterCommunicationAddressingPolicyV1}, KnownFacts: []domain.CharacterAgentFact{{ID: "perceived-person", Kind: "known", Text: "面前有人"}, {ID: "received-message", Kind: "received_information", Text: "刚收到的一条通信"}, {ID: "document", Kind: "received_document_statement", Text: "文书内容"}}}
	for _, mode := range []string{"named", "hint", "reply"} {
		t.Run(mode, func(t *testing.T) {
			message := domain.CharacterCommunicationV2{ID: "send", Kind: "information", Text: "本人原话", KnowledgeRefs: []string{"perceived-person"}}
			switch mode {
			case "named":
				message.ToCharacter = "乙"
			case "hint":
				message.RecipientHint = "面前刚向我说话的人"
			case "reply":
				message.ReplyToReceivedFactID, message.KnowledgeRefs = "received-message", []string{"received-message"}
			}
			proposal := domain.CharacterDecisionProposal{Communications: []domain.CharacterCommunicationV2{message}}
			if err := domain.ValidateCharacterKnowledgeIntentV2(proposal, observation); err != nil {
				t.Fatal(err)
			}
			old := observation
			old.Sources = nil
			if err := domain.ValidateCharacterKnowledgeIntentV2(proposal, old); (err == nil) != (mode == "named") {
				t.Fatalf("historical mode changed: %v", err)
			}
		})
	}
	for _, message := range []domain.CharacterCommunicationV2{
		{ToCharacter: "乙", RecipientHint: "面前的人"},
		{RecipientHint: "面前的人", ReplyToReceivedFactID: "received-message"},
		{ReplyToReceivedFactID: "perceived-person"},
		{ReplyToReceivedFactID: "document"},
		{ReplyToReceivedFactID: "other-owner-message"},
		{},
	} {
		message.ID, message.Kind, message.Text, message.KnowledgeRefs = "bad", "information", "原话", []string{"perceived-person", "received-message", "document"}
		if err := domain.ValidateCharacterKnowledgeIntentV2(domain.CharacterDecisionProposal{Communications: []domain.CharacterCommunicationV2{message}}, observation); err == nil {
			t.Fatalf("accepted ambiguous or unreceived address: %+v", message)
		}
	}
}

func communicationAddressFixture(t *testing.T, remote bool) testutil.PassiveReceptionFixture {
	t.Helper()
	f := testutil.CharacterPassiveReception(t, remote, true)
	f.Inputs.Stimulus.Sources = append(f.Inputs.Stimulus.Sources, domain.CharacterCommunicationAddressingPolicyV1)
	var err error
	f.Inputs.Stimulus, err = domain.FinalizeWorldStimulusPacket(f.Inputs.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	observationIndex := -1
	for i, observation := range f.Inputs.Observations {
		if observation.AgentID == f.Proposal.AgentID {
			observationIndex = i
		}
	}
	if observationIndex < 0 {
		t.Fatal("fixture lacks sender observation")
	}
	o := f.Inputs.Observations[observationIndex]
	o.StimulusDigest = f.Inputs.Stimulus.Digest
	o.Sources = append(o.Sources, domain.CharacterCommunicationAddressingPolicyV1)
	o, err = domain.FinalizeCharacterObservationPacket(o)
	if err != nil {
		t.Fatal(err)
	}
	f.Inputs.Observations[observationIndex] = o
	for i := range f.Inputs.Activation.Entries {
		if f.Inputs.Activation.Entries[i].AgentID == f.Proposal.AgentID {
			f.Inputs.Activation.Entries[i].ObservationDigest = o.Digest
		}
	}
	f.Inputs.Activation, err = domain.FinalizeCharacterAgentActivation(f.Inputs.Activation)
	if err != nil {
		t.Fatal(err)
	}
	f.Proposal.ObservationDigest = o.Digest
	f.Proposal.Communications[0].ToCharacter, f.Proposal.Communications[0].RecipientHint = "", "面前唯一正在听我说话的人"
	f.Proposal, err = domain.FinalizeCharacterDecisionProposal(f.Proposal, o)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Receipt.PassiveReceptions[0]
	f.Receipt.PassiveReceptions = nil
	f.Receipt.CommunicationReceptions = []domain.CharacterCommunicationReceptionV1{{ToAgentID: r.ToAgentID, FromAgentID: r.FromAgentID, SourceProposalDigest: f.Proposal.Digest, CommunicationID: r.CommunicationID, DeliveredAtDay: r.DeliveredAtDay, Channel: r.Channel, MechanismRef: r.MechanismRef, RecipientResolution: "unique", EvidenceRefs: append([]string(nil), f.Proposal.Communications[0].KnowledgeRefs...)}}
	f.Receipt.StimulusDigest, f.Receipt.ActivationDigest = f.Inputs.Stimulus.Digest, f.Inputs.Activation.Digest
	f.Receipt.ProposalDigests = []string{f.Proposal.Digest}
	f.Receipt.Resolutions[0].ProposalDigest = f.Proposal.Digest
	f.Receipt.ResourceSettlements[0].EvidenceRefs = []string{f.Proposal.Digest}
	return f
}

func TestCommunicationAddressingDerivesSleepingReceiptAndPrivateSentText(t *testing.T) {
	for _, remote := range []bool{false, true} {
		f := communicationAddressFixture(t, remote)
		before, _ := json.Marshal(f)
		r, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
		if err != nil {
			t.Fatal(err)
		}
		state, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
		if err != nil {
			t.Fatal(err)
		}
		for _, actor := range state.Actors {
			if actor.AgentID != r.CommunicationReceptions[0].ToAgentID {
				continue
			}
			if len(actor.ReceivedFacts) != 1 {
				t.Fatalf("expected one host-derived reception, got %+v", actor.ReceivedFacts)
			}
			fact := actor.ReceivedFacts[0]
			if fact.Text != f.Proposal.Communications[0].Text || fact.Kind != f.Proposal.Communications[0].Kind || fact.FromAgentID != f.Proposal.AgentID || fact.SourceProposalDigest != f.Proposal.Digest || fact.ReceivedAtDay == nil || *fact.ReceivedAtDay != r.StoryTime.EndDay {
				t.Fatal("host-derived fact changed original content/source/time")
			}
		}
		text, err := domain.CharacterActivationPrivateOutcome(f.Proposal, r.Resolutions[0], state, r)
		if err != nil || !strings.Contains(text, "向面前唯一正在听我说话的人发出information："+f.Proposal.Communications[0].Text) || strings.Contains(text, "向乙") {
			t.Fatalf("sender memory gained a canonical recipient name or lost original text: %s %v", text, err)
		}
		replay, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
		if err != nil || !reflect.DeepEqual(replay, state) {
			t.Fatal("receipt replay changed or duplicated delivery")
		}
		after, _ := json.Marshal(f)
		if string(before) != string(after) {
			t.Fatal("receipt application mutated input")
		}
	}
}

func communicationActiveReceiver(t *testing.T, f *testutil.PassiveReceptionFixture) domain.CharacterDecisionProposal {
	t.Helper()
	receiver := f.Receipt.CommunicationReceptions[0].ToAgentID
	var o domain.CharacterObservationPacket
	var post domain.CharacterPhysicalStateV2
	for _, candidate := range f.Inputs.Observations {
		if candidate.AgentID == receiver {
			o = candidate
		}
	}
	for _, actor := range f.Inputs.Stimulus.PhysicalState.Actors {
		if actor.AgentID == receiver {
			post = actor
		}
	}
	o.Sources = append(o.Sources, domain.CharacterCommunicationAddressingPolicyV1)
	o.StimulusDigest = f.Inputs.Stimulus.Digest
	o.KnownFacts = []domain.CharacterAgentFact{{ID: "receiver-known", Kind: "known", Text: "本人正在现场等待"}}
	var err error
	o, err = domain.FinalizeCharacterObservationPacket(o)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Proposal
	p.AgentID, p.Character, p.ObservationDigest, p.Location = receiver, post.Character, o.Digest, post.Location
	p.CurrentGoal, p.Pressure = o.CurrentGoal, o.Pressure
	p.Decision, p.IntendedAction, p.DecisionReason = "现场等待", "留在现场等待", "等待实际消息"
	p.Communications, p.MechanismRefs = nil, nil
	p.KnowledgeRefs = []string{"receiver-known"}
	p, err = domain.FinalizeCharacterDecisionProposal(p, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Inputs.Activation.Entries {
		entry := &f.Inputs.Activation.Entries[i]
		if entry.AgentID == receiver {
			entry.State, entry.Reasons, entry.ObservationDigest = domain.CharacterAgentActive, []string{"task_progress"}, o.Digest
		}
	}
	f.Inputs.Activation, err = domain.FinalizeCharacterAgentActivation(f.Inputs.Activation)
	if err != nil {
		t.Fatal(err)
	}
	f.Receipt.ActivationDigest = f.Inputs.Activation.Digest
	f.Receipt.ProposalDigests = append(f.Receipt.ProposalDigests, p.Digest)
	r := f.Receipt.Resolutions[0]
	r.AgentID, r.Character, r.ProposalDigest = receiver, post.Character, p.Digest
	r.Decision, r.IntendedAction, r.ActionOrder, r.PostState = p.Decision, p.IntendedAction, 2, &post
	r.MechanismRefs = nil
	f.Receipt.Resolutions = append(f.Receipt.Resolutions, r)
	return p
}

func TestCommunicationAddressingActiveReceiverNormalizedReplay(t *testing.T) {
	f := communicationAddressFixture(t, false)
	peer := communicationActiveReceiver(t, &f)
	proposals := []domain.CharacterDecisionProposal{f.Proposal, peer}
	r, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, resolution := range r.Resolutions {
		if resolution.AgentID == peer.AgentID && len(resolution.PostState.ReceivedFacts) != 1 {
			t.Fatal("active receiver normalized post-state lacks exact received fact")
		}
	}
	replayed, err := domain.FinalizeWorldArbitrationReceipt(r, f.Inputs.Stimulus, f.Inputs.Activation, proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(r)
	after, _ := json.Marshal(replayed)
	if string(before) != string(after) {
		t.Fatal("active recipient replay changed normalized bytes or duplicate facts")
	}
	for i := range r.Resolutions {
		if r.Resolutions[i].AgentID == peer.AgentID {
			r.Resolutions[i].PostState.ReceivedFacts[0].Text = "Arbiter伪造的话"
		}
	}
	if _, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, proposals...); err == nil {
		t.Fatal("rewritten active receipt fact accepted")
	}
}

func TestCommunicationAddressingRejectsUnprovenOrAmbiguousDelivery(t *testing.T) {
	cases := map[string]func(*testutil.PassiveReceptionFixture){
		"unknown receiver": func(f *testutil.PassiveReceptionFixture) { f.Receipt.CommunicationReceptions[0].ToAgentID = "unknown" },
		"self receiver": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].ToAgentID = f.Proposal.AgentID
		},
		"unknown sender": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].FromAgentID = "unknown"
		},
		"foreign digest": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].SourceProposalDigest = "sha256:other"
		},
		"unknown message": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].CommunicationID = "other"
		},
		"ambiguous recipient": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].RecipientResolution = "ambiguous"
		},
		"missing binding evidence": func(f *testutil.PassiveReceptionFixture) { f.Receipt.CommunicationReceptions[0].EvidenceRefs = nil },
		"invented evidence": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].EvidenceRefs = []string{"world-secret"}
		},
		"missing time": func(f *testutil.PassiveReceptionFixture) { f.Receipt.CommunicationReceptions[0].DeliveredAtDay = nil },
		"outside time": func(f *testutil.PassiveReceptionFixture) {
			day := f.Receipt.StoryTime.EndDay + 1
			f.Receipt.CommunicationReceptions[0].DeliveredAtDay = &day
		},
		"blocked sender": func(f *testutil.PassiveReceptionFixture) { f.Receipt.Resolutions[0].CompletionState = "blocked" },
		"duplicate tuple": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions = append(f.Receipt.CommunicationReceptions, f.Receipt.CommunicationReceptions[0])
		},
		"invented channel": func(f *testutil.PassiveReceptionFixture) { f.Receipt.CommunicationReceptions[0].Channel = "broadcast" },
		"unapplied mechanism": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].Channel, f.Receipt.CommunicationReceptions[0].MechanismRef = "mechanism", "unknown-radio"
		},
		"mechanism on speech": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].MechanismRef = "invented"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := communicationAddressFixture(t, false)
			mutate(&f)
			if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil {
				t.Fatal("unproven communication reception accepted")
			}
		})
	}
	f := communicationAddressFixture(t, true)
	f.Receipt.CommunicationReceptions[0].Channel, f.Receipt.CommunicationReceptions[0].MechanismRef = "in_person", ""
	if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil {
		t.Fatal("remote recipient received in-person message")
	}
	f = communicationAddressFixture(t, false)
	peer := communicationActiveReceiver(t, &f)
	for i := range f.Receipt.Resolutions {
		if f.Receipt.Resolutions[i].AgentID == peer.AgentID {
			f.Receipt.Resolutions[i].PostState.Location = "岸边"
		}
	}
	if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal, peer}, 1); err == nil {
		t.Fatal("delivery ignored the active receiver's actual endpoint")
	}
}

func communicationReplyFixture(t *testing.T) testutil.PassiveReceptionFixture {
	t.Helper()
	f := communicationAddressFixture(t, false)
	prior := domain.CharacterReceivedFactV2{Kind: "request", Text: "此前本人实际收到的请求", SourceType: "communication", SourceID: "prior-request", SourceProposalDigest: "sha256:" + strings.Repeat("a", 64), FromAgentID: f.Receipt.CommunicationReceptions[0].ToAgentID, Chapter: 1}
	prior.ID = domain.CharacterReceivedFactIDV2(f.Proposal.AgentID, prior)
	for i := range f.Inputs.Stimulus.PhysicalState.Actors {
		actor := &f.Inputs.Stimulus.PhysicalState.Actors[i]
		if actor.AgentID == f.Proposal.AgentID {
			actor.ReceivedFacts = []domain.CharacterReceivedFactV2{prior}
		}
	}
	f.Receipt.Resolutions[0].PostState.ReceivedFacts = []domain.CharacterReceivedFactV2{prior}
	var err error
	f.Inputs.Stimulus, err = domain.FinalizeWorldStimulusPacket(f.Inputs.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	var o domain.CharacterObservationPacket
	for _, candidate := range f.Inputs.Observations {
		if candidate.AgentID == f.Proposal.AgentID {
			o = candidate
		}
	}
	o.StimulusDigest = f.Inputs.Stimulus.Digest
	o.KnownFacts = append(o.KnownFacts, domain.CharacterAgentFact{ID: prior.ID, Kind: "received_request", Text: prior.Text, Visibility: "private"})
	o, err = domain.FinalizeCharacterObservationPacket(o)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Inputs.Activation.Entries {
		if f.Inputs.Activation.Entries[i].AgentID == o.AgentID {
			f.Inputs.Activation.Entries[i].ObservationDigest = o.Digest
		}
	}
	f.Inputs.Activation, err = domain.FinalizeCharacterAgentActivation(f.Inputs.Activation)
	if err != nil {
		t.Fatal(err)
	}
	f.Proposal.ObservationDigest = o.Digest
	f.Proposal.Communications[0].RecipientHint, f.Proposal.Communications[0].ReplyToReceivedFactID = "", prior.ID
	f.Proposal.Communications[0].KnowledgeRefs = []string{prior.ID}
	f.Proposal, err = domain.FinalizeCharacterDecisionProposal(f.Proposal, o)
	if err != nil {
		t.Fatal(err)
	}
	f.Receipt.StimulusDigest, f.Receipt.ActivationDigest = f.Inputs.Stimulus.Digest, f.Inputs.Activation.Digest
	f.Receipt.ProposalDigests = []string{f.Proposal.Digest}
	f.Receipt.Resolutions[0].ProposalDigest = f.Proposal.Digest
	f.Receipt.ResourceSettlements[0].EvidenceRefs = []string{f.Proposal.Digest}
	f.Receipt.CommunicationReceptions[0].SourceProposalDigest, f.Receipt.CommunicationReceptions[0].EvidenceRefs = f.Proposal.Digest, []string{prior.ID}
	return f
}

func TestCommunicationAddressingReplyUsesOwnersActualReceivedSender(t *testing.T) {
	f := communicationReplyFixture(t)
	r, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	text, err := domain.CharacterActivationPrivateOutcome(f.Proposal, r.Resolutions[0], state, r)
	if err != nil || !strings.Contains(text, "向该已收通信的发送者发出information：") || strings.Contains(text, "向乙") {
		t.Fatalf("reply leaked canonical recipient name: %s %v", text, err)
	}
	for _, mutate := range []func(*testutil.PassiveReceptionFixture){
		func(f *testutil.PassiveReceptionFixture) {
			f.Proposal.Communications[0].ReplyToReceivedFactID = "not-owner-received"
		},
		func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.CommunicationReceptions[0].EvidenceRefs = []string{"known-fuel"}
		},
		func(f *testutil.PassiveReceptionFixture) {
			// Remove the fact from the sender's exact before-state while keeping
			// the proposal's observation claim. Host delivery must distrust it.
			for i := range f.Inputs.Stimulus.PhysicalState.Actors {
				if f.Inputs.Stimulus.PhysicalState.Actors[i].AgentID == f.Proposal.AgentID {
					f.Inputs.Stimulus.PhysicalState.Actors[i].ReceivedFacts = nil
				}
			}
			f.Receipt.Resolutions[0].PostState.ReceivedFacts = nil
			var err error
			f.Inputs.Stimulus, err = domain.FinalizeWorldStimulusPacket(f.Inputs.Stimulus)
			if err != nil {
				t.Fatal(err)
			}
			f.Receipt.StimulusDigest = f.Inputs.Stimulus.Digest
		},
	} {
		bad := communicationReplyFixture(t)
		mutate(&bad)
		if _, err := domain.FinalizeWorldArbitrationReceipt(bad.Receipt, bad.Inputs.Stimulus, bad.Inputs.Activation, []domain.CharacterDecisionProposal{bad.Proposal}, 1); err == nil {
			t.Fatal("reply accepted without exact owner source/evidence")
		}
	}
}

func TestCommunicationAddressingConditionalReplyDoesNotMatchHiddenName(t *testing.T) {
	f := communicationReplyFixture(t)
	message := &f.Proposal.Communications[0]
	message.Kind, message.ConditionKind = "conditional_response", "request"
	// The exact received fact is the condition. No canonical identity is
	// granted by merely receiving it, nor required to answer it.
	var observation domain.CharacterObservationPacket
	for _, candidate := range f.Inputs.Observations {
		if candidate.AgentID == f.Proposal.AgentID {
			observation = candidate
		}
	}
	var prior domain.CharacterReceivedFactV2
	for _, actor := range f.Inputs.Stimulus.PhysicalState.Actors {
		if actor.AgentID == f.Proposal.AgentID {
			prior = actor.ReceivedFacts[0]
		}
	}
	observation.KnownFacts = append(observation.KnownFacts, domain.CharacterAgentFact{ID: prior.ID, Kind: "received_request", Text: prior.Text})
	if err := domain.ValidateCharacterKnowledgeIntentV2(f.Proposal, observation); err != nil {
		t.Fatalf("exact received reply cannot execute without a hidden canonical name: %v", err)
	}
	message.ConditionFromCharacter = "乙"
	if err := domain.ValidateCharacterKnowledgeIntentV2(f.Proposal, observation); err == nil {
		t.Fatal("non-named conditional reply can recover canonical source identity")
	}
	message.ConditionFromCharacter = ""
	message.ReplyToReceivedFactID, message.RecipientHint = "", "面前的人"
	if err := domain.ValidateCharacterKnowledgeIntentV2(f.Proposal, observation); err == nil {
		t.Fatal("anonymous future event used as an already-received reply condition")
	}
}
