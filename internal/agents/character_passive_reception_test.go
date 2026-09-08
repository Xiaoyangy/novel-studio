package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestPassiveMessageWakesRecipientOnlyForNextIndependentDecision(t *testing.T) {
	f := testutil.CharacterPassiveReception(t, false, true)
	st := store.NewStore(t.TempDir())
	base := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(f.Inputs.Stimulus.GenerationID, 1, base.ChapterContextDigest, *f.Inputs.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := proofs.PublishActivationInputs(f.Inputs); err != nil {
		t.Fatal(err)
	}
	base.Evidence.Proposals = []domain.CharacterDecisionProposal{f.Proposal}
	base.Evidence.Arbitrations = []domain.WorldArbitrationReceipt{f.Receipt}
	model := &activationExecutionModel{cycle: base}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "passive-reception", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	cycle, err := runCharacterActivationCycle(context.Background(), cfg, st, models, session, activationInputsForExecution(f.Inputs, session))
	if err != nil {
		t.Fatal(err)
	}
	if model.characterCalls.Load() != 1 || model.arbiterCalls.Load() != 1 || len(cycle.Evidence.Proposals) != 1 || len(cycle.Evidence.Arbitrations[0].Resolutions) != 1 {
		t.Fatal("sleeping receiver was made to decide during delivery")
	}
	if cycle.Evidence.Proposals[0].AgentID != f.Proposal.AgentID {
		t.Fatal("sender proposal replaced by recipient")
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, cycle, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := buildNextCharacterActivationInputs(f.Inputs, cycle, session)
	if err != nil {
		t.Fatal(err)
	}
	receiverID := f.Receipt.PassiveReceptions[0].ToAgentID
	var receiver domain.CharacterObservationPacket
	for _, observation := range next.Observations {
		if observation.AgentID == receiverID {
			receiver = observation
		}
	}
	if len(receiver.KnownFacts) != 1 || !strings.Contains(receiver.KnownFacts[0].Text, f.Proposal.Communications[0].Text) || !strings.Contains(receiver.KnownFacts[0].Text, "实际送达") {
		t.Fatalf("next owner view lacks delivered message/time: %+v", receiver.KnownFacts)
	}
	for _, entry := range next.Activation.Entries {
		if entry.AgentID == receiverID && (entry.State != domain.CharacterAgentActive || !containsAgentString(entry.Reasons, "received_information")) {
			t.Fatalf("delivered message did not wake receiver: %+v", entry)
		}
	}
	for _, observation := range next.Observations {
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatal("world-side prose leaked into a private observation")
		}
		if observation.AgentID == f.Proposal.AgentID {
			if !strings.Contains(string(raw), "本人已发出的通信") || strings.Contains(string(raw), "乙已经收到") {
				t.Fatal("sender forgot its utterance or received an invented acknowledgment")
			}
		}
	}
	secondProof, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondProof.PublishActivationInputs(next); err != nil {
		t.Fatal(err)
	}
	// The recipient is now called normally with only its own observed message.
	// It independently chooses to ask for provenance; delivery did not create
	// consent, a reply, a location change or an action on its behalf.
	receiverBase := testutil.CharacterCycle(t, 1, "", nil, 0)
	receiverBase.Evidence.Proposals = []domain.CharacterDecisionProposal{{Location: receiver.Location, CurrentGoal: "核对刚收到的消息", Pressure: "信息尚未核验", AvailableOptions: []string{"追问", "继续等候"}, Decision: "追问", DecisionReason: "对方报告还需确认", IntendedAction: "要求对方说明来源", ActionDuration: "一分钟", KnowledgeRefs: []string{receiver.KnownFacts[0].ID}}}
	receiverModel := &activationExecutionModel{cycle: receiverBase}
	proposals, err := runCharacterProposalRoundWithModel(context.Background(), cfg, st, receiverModel, map[string]domain.CharacterObservationPacket{receiverID: receiver}, []string{receiverID}, 1, &session)
	if err != nil {
		t.Fatal(err)
	}
	if receiverModel.characterCalls.Load() != 1 || len(proposals) != 1 || proposals[0].AgentID != receiverID || proposals[0].Decision != "追问" || proposals[0].ObservationDigest != receiver.Digest {
		t.Fatal("recipient did not independently submit its next-cycle choice")
	}
	if canon, err := st.CharacterAgents.LoadCanonicalMemory(receiverID); err != nil || canon != nil {
		t.Fatal("passive delivery published unaccepted canonical memory")
	}
}
