package domain_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestPassiveReceptionUpdatesOnlyDeliveredOwnerKnowledge(t *testing.T) {
	for _, remote := range []bool{false, true} {
		f := testutil.CharacterPassiveReception(t, remote, true)
		before, _ := json.Marshal(f.Inputs)
		r, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
		if err != nil {
			t.Fatal(err)
		}
		state, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
		if err != nil {
			t.Fatal(err)
		}
		for _, actor := range state.Actors {
			if actor.Character != "乙" {
				continue
			}
			if len(actor.ReceivedFacts) != 1 {
				t.Fatalf("passive receipt missing: %+v", actor)
			}
			fact := actor.ReceivedFacts[0]
			if fact.Text != f.Proposal.Communications[0].Text || fact.SourceProposalDigest != f.Proposal.Digest || fact.ReceivedAtDay == nil || *fact.ReceivedAtDay != r.StoryTime.EndDay {
				t.Fatalf("delivered content/source/time changed: %+v", fact)
			}
			for _, old := range f.Inputs.Stimulus.PhysicalState.Actors {
				if old.AgentID == actor.AgentID {
					actor.ReceivedFacts = nil
					if !reflect.DeepEqual(actor, old) {
						t.Fatal("passive message changed recipient location/resources/intent")
					}
				}
			}
		}
		if len(r.Resolutions) != 1 || r.Resolutions[0].Decision != f.Proposal.Decision {
			t.Fatal("passive recipient received a fabricated decision")
		}
		replayed, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
		if err != nil || !reflect.DeepEqual(replayed, state) {
			t.Fatal("receipt replay duplicated or changed delivery")
		}
		after, _ := json.Marshal(f.Inputs)
		if string(before) != string(after) {
			t.Fatal("passive projection changed its frozen input")
		}
	}
}

func TestPassiveReceptionRejectsUnprovenDelivery(t *testing.T) {
	cases := map[string]func(*testutil.PassiveReceptionFixture){
		"unknown recipient": func(f *testutil.PassiveReceptionFixture) { f.Receipt.PassiveReceptions[0].ToAgentID = "ca_unknown" },
		"active recipient": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.PassiveReceptions[0].ToAgentID = f.Proposal.AgentID
		},
		"unknown sender": func(f *testutil.PassiveReceptionFixture) { f.Receipt.PassiveReceptions[0].FromAgentID = "ca_unknown" },
		"foreign proposal": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.PassiveReceptions[0].SourceProposalDigest = "sha256:wrong"
		},
		"unproposed message": func(f *testutil.PassiveReceptionFixture) { f.Receipt.PassiveReceptions[0].CommunicationID = "made-up" },
		"missing time":       func(f *testutil.PassiveReceptionFixture) { f.Receipt.PassiveReceptions[0].DeliveredAtDay = nil },
		"future time": func(f *testutil.PassiveReceptionFixture) {
			day := f.Receipt.StoryTime.EndDay + 1
			f.Receipt.PassiveReceptions[0].DeliveredAtDay = &day
		},
		"blocked sender": func(f *testutil.PassiveReceptionFixture) { f.Receipt.Resolutions[0].Outcome = "blocked" },
		"unapplied channel": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.PassiveReceptions[0].Channel = "mechanism"
			f.Receipt.PassiveReceptions[0].MechanismRef = "invented-radio"
		},
		"duplicate delivery": func(f *testutil.PassiveReceptionFixture) {
			f.Receipt.PassiveReceptions = append(f.Receipt.PassiveReceptions, f.Receipt.PassiveReceptions[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := testutil.CharacterPassiveReception(t, false, true)
			mutate(&f)
			if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil {
				t.Fatal("unproven passive delivery accepted")
			}
		})
	}
	f := testutil.CharacterPassiveReception(t, false, false)
	if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil {
		t.Fatal("historical unmarked protocol accepted passive effects")
	}
	f = testutil.CharacterPassiveReception(t, true, true)
	f.Receipt.PassiveReceptions[0].Channel, f.Receipt.PassiveReceptions[0].MechanismRef = "in_person", ""
	if _, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil {
		t.Fatal("cross-location speech teleported to sleeping recipient")
	}
}

func TestPassiveIntentAloneDoesNotDeliverAnything(t *testing.T) {
	f := testutil.CharacterPassiveReception(t, false, true)
	f.Receipt.PassiveReceptions = nil
	r, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.ApplyArbitrationPhysicalStateV2(r, f.Inputs.Stimulus, f.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range state.Actors {
		if actor.Character == "乙" && len(actor.ReceivedFacts) != 0 {
			t.Fatal("sending intent was treated as actual delivery")
		}
	}
}
