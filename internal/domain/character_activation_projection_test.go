package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestActivationCycleProjectionOptionalButNonemptyStillBindsCurrentProposal(t *testing.T) {
	f := testutil.CharacterPassiveReception(t, false, true)
	r := f.Receipt
	r.ProtagonistProjection = domain.ProtagonistDecisionProjection{}
	final, err := domain.FinalizeWorldArbitrationReceipt(r, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Resolutions) != 1 || final.ProtagonistProjection.Protagonist != "" || final.Resolutions[0].Decision != f.Proposal.Decision {
		t.Fatal("omitting cycle projection invented a sleeping actor choice")
	}
	before, _ := json.Marshal(final)
	retry, err := domain.FinalizeWorldArbitrationReceipt(final, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
	after, _ := json.Marshal(retry)
	if err != nil || string(before) != string(after) {
		t.Fatalf("cycle receipt is not byte/digest stable: %v", err)
	}
	for name, projection := range map[string]domain.ProtagonistDecisionProjection{
		"sleeping actor": {Protagonist: "乙", ChosenDecision: "伪造回应"},
		"old decision":   {Protagonist: f.Proposal.Character, ChosenDecision: "前轮选择"},
		"partial":        {ObservableEffects: []string{"只有影响不能伪装省略投影"}},
		"whitespace":     {Protagonist: " "},
	} {
		t.Run(name, func(t *testing.T) {
			r.ProtagonistProjection = projection
			if _, err := domain.FinalizeWorldArbitrationReceipt(r, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1); err == nil || !strings.Contains(err.Error(), "protagonist projection") {
				t.Fatalf("unbound nonempty projection accepted: %v", err)
			}
		})
	}
	// Already finalized historical cycle projections retain their exact bytes.
	legacy := testutil.CharacterCycle(t, 1, "", nil, 0).Evidence
	before, _ = json.Marshal(legacy.Arbitrations[0])
	retry, err = domain.FinalizeWorldArbitrationReceipt(legacy.Arbitrations[0], legacy.Stimulus, legacy.Activation, legacy.Proposals, 1)
	after, _ = json.Marshal(retry)
	if err != nil || string(before) != string(after) {
		t.Fatalf("historical nonempty projection changed: %v", err)
	}
}

func TestOneShotArbitrationStillRequiresPOVProjection(t *testing.T) {
	for _, version := range []string{domain.WorldStimulusPacketVersion, domain.WorldStimulusPacketV2Version} {
		t.Run(version, func(t *testing.T) {
			e := testutil.CharacterCycle(t, 1, "", nil, 0).Evidence
			stimulus, receipt := e.Stimulus, e.Arbitrations[0]
			stimulus.Version, stimulus.Sources = version, nil
			if version == domain.WorldStimulusPacketVersion {
				stimulus.PhysicalState, stimulus.StoryClock = nil, nil
				receipt.Version = domain.WorldArbitrationReceiptVersion
				receipt.ResourceSettlements, receipt.StoryTime = nil, nil
				for i := range receipt.Resolutions {
					receipt.Resolutions[i].PostState = nil
				}
			}
			var err error
			stimulus, err = domain.FinalizeWorldStimulusPacket(stimulus)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Digest, receipt.StimulusDigest = "", stimulus.Digest
			valid, err := domain.FinalizeWorldArbitrationReceipt(receipt, stimulus, e.Activation, e.Proposals, 1)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(valid)
			retry, err := domain.FinalizeWorldArbitrationReceipt(valid, stimulus, e.Activation, e.Proposals, 1)
			after, _ := json.Marshal(retry)
			if err != nil || string(before) != string(after) {
				t.Fatalf("one-shot receipt changed on verification: %v", err)
			}
			receipt.ProtagonistProjection = domain.ProtagonistDecisionProjection{}
			if _, err := domain.FinalizeWorldArbitrationReceipt(receipt, stimulus, e.Activation, e.Proposals, 1); err == nil || !strings.Contains(err.Error(), "protagonist projection") {
				t.Fatalf("one-shot accepted missing POV: %v", err)
			}
		})
	}
}

func TestActivationProjectionRequiresExactCycleContractShape(t *testing.T) {
	base := testutil.CharacterCycle(t, 1, "", nil, 0).Evidence.Stimulus
	if !domain.HasCharacterActivationCycleContract(base) {
		t.Fatal("real cycle contract not recognized")
	}
	for name, edit := range map[string]func(*domain.WorldStimulusPacket){
		"v1": func(s *domain.WorldStimulusPacket) { s.Version = domain.WorldStimulusPacketVersion },
		"policy only": func(s *domain.WorldStimulusPacket) {
			s.Sources = []string{domain.CharacterActivationCyclePolicy, domain.CharacterPassiveReceptionPolicyV2}
		},
		"bad token": func(s *domain.WorldStimulusPacket) {
			s.Sources = []string{domain.CharacterActivationCycleSourcePrefix + "not-a-digest"}
		},
		"token whitespace": func(s *domain.WorldStimulusPacket) {
			s.Sources = []string{domain.CharacterActivationCycleSourcePrefix + " sha256:" + strings.Repeat("f", 64)}
		},
		"ambiguous": func(s *domain.WorldStimulusPacket) {
			s.Sources = append(append([]string(nil), s.Sources...), domain.CharacterActivationCycleSourcePrefix+"sha256:"+strings.Repeat("f", 64))
		},
		"no physical": func(s *domain.WorldStimulusPacket) { s.PhysicalState = nil },
		"no clock":    func(s *domain.WorldStimulusPacket) { s.StoryClock = nil },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			edit(&changed)
			if domain.HasCharacterActivationCycleContract(changed) {
				t.Fatal("non-cycle input gained an omitted-POV exemption")
			}
		})
	}
}

func TestActivationChapterStillRequiresAnActualPOVChoiceSomewhereInChain(t *testing.T) {
	context, _, _, _ := testutil.CharacterReadiness(t, false)
	context.POVCharacter = "乙" // The fixture's only active actor is 甲.
	context, err := domain.FinalizeCharacterReadinessContext(context)
	if err != nil {
		t.Fatal(err)
	}
	f := testutil.CharacterPassiveReception(t, false, true, context.Digest)
	f.Receipt.ProtagonistProjection = domain.ProtagonistDecisionProjection{}
	receipt, err := domain.FinalizeWorldArbitrationReceipt(f.Receipt, f.Inputs.Stimulus, f.Inputs.Activation, []domain.CharacterDecisionProposal{f.Proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0, context.Digest)
	var activeObservation domain.CharacterObservationPacket
	for _, observation := range f.Inputs.Observations {
		if observation.AgentID == f.Proposal.AgentID {
			activeObservation = observation
		}
	}
	cycle.Evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{
		GenerationID: context.GenerationID, Chapter: 1, Registry: f.Inputs.Registry, Stimulus: f.Inputs.Stimulus, Activation: f.Inputs.Activation,
		Observations: []domain.CharacterObservationPacket{activeObservation}, Proposals: []domain.CharacterDecisionProposal{f.Proposal}, Arbitrations: []domain.WorldArbitrationReceipt{receipt},
		MemoryRoots: []string{activeObservation.MemoryRoot}, ProtocolDigest: cycle.Evidence.ProtocolDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	cycle.InputSetDigest = f.Inputs.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewCharacterActivationSession(context.GenerationID, 1, context.Digest, *f.Inputs.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.NewCharacterReadinessReviewInput(context, session, []domain.CharacterActivationCycle{cycle}, cycle.Evidence.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := domain.FinalizeCharacterReadinessReview(input, testutil.ReadyVerdict(input))
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, ready)
	if err != nil {
		t.Fatal(err)
	}
	chapter, err := domain.FinalizeCharacterActivationChapterEvidence(domain.CharacterActivationChapterEvidence{Context: context, Session: session,
		Cycles: []domain.CharacterActivationCycle{cycle}, Inputs: []domain.CharacterActivationInputSet{f.Inputs}, Reviews: []domain.CharacterReadinessReviewAudit{{Input: input, Receipt: ready}}, ProtocolDigest: cycle.Evidence.ProtocolDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.BuildCharacterActivationSimulation(chapter, "", nil); err == nil || !strings.Contains(err.Error(), "no actual POV character choice") {
		t.Fatalf("passive reception was promoted into an invented whole-chapter POV choice: %v", err)
	}
}
