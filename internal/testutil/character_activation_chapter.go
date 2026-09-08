package testutil

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// A complete single-cycle chapter for consumer/seal tests. Execution and
// multi-cycle behavior are separately tested through the real AgentLoop.
func CharacterActivationChapter(t *testing.T) domain.CharacterActivationChapterEvidence {
	t.Helper()
	context, _, cycle, _ := CharacterReadiness(t, false)
	input := CharacterActivationInputs(t, context.Digest)
	return characterActivationChapterFromInputs(t, context, cycle, input, cycle.Evidence.Proposals[0], cycle.Evidence.Arbitrations[0])
}

func CharacterActivationChapterWithPassive(t *testing.T) domain.CharacterActivationChapterEvidence {
	t.Helper()
	context, _, cycle, _ := CharacterReadiness(t, false)
	f := CharacterPassiveReception(t, false, true, context.Digest)
	return characterActivationChapterFromInputs(t, context, cycle, f.Inputs, f.Proposal, f.Receipt)
}

func characterActivationChapterFromInputs(t *testing.T, context domain.CharacterReadinessContext, cycle domain.CharacterActivationCycle, input domain.CharacterActivationInputSet, proposal domain.CharacterDecisionProposal, receipt domain.WorldArbitrationReceipt) domain.CharacterActivationChapterEvidence {
	t.Helper()
	var observation domain.CharacterObservationPacket
	for _, candidate := range input.Observations {
		if candidate.AgentID == proposal.AgentID {
			observation = candidate
			break
		}
	}
	proposal.ObservationDigest = observation.Digest
	var err error
	proposal, err = domain.FinalizeCharacterDecisionProposal(proposal, observation)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Digest, receipt.StimulusDigest, receipt.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	receipt.ProposalDigests = []string{proposal.Digest}
	receipt.Resolutions[0].ProposalDigest = proposal.Digest
	receipt.ResourceSettlements[0].EvidenceRefs = []string{proposal.Digest}
	for i := range receipt.PassiveReceptions {
		receipt.PassiveReceptions[i].SourceProposalDigest = proposal.Digest
	}
	receipt, err = domain.FinalizeWorldArbitrationReceipt(receipt, input.Stimulus, input.Activation, []domain.CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	cycle.Evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{GenerationID: context.GenerationID, Chapter: 1, Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation, Observations: []domain.CharacterObservationPacket{observation}, Proposals: []domain.CharacterDecisionProposal{proposal}, Arbitrations: []domain.WorldArbitrationReceipt{receipt}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: cycle.Evidence.ProtocolDigest})
	if err != nil {
		t.Fatal(err)
	}
	cycle.InputSetDigest = input.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewCharacterActivationSession(context.GenerationID, 1, context.Digest, *input.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	reviewInput, err := domain.NewCharacterReadinessReviewInput(context, session, []domain.CharacterActivationCycle{cycle}, cycle.Evidence.ProtocolDigest)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := domain.FinalizeCharacterReadinessReview(reviewInput, ReadyVerdict(reviewInput))
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, readiness)
	if err != nil {
		t.Fatal(err)
	}
	chapter, err := domain.FinalizeCharacterActivationChapterEvidence(domain.CharacterActivationChapterEvidence{Context: context, Session: session, Cycles: []domain.CharacterActivationCycle{cycle}, Inputs: []domain.CharacterActivationInputSet{input}, Reviews: []domain.CharacterReadinessReviewAudit{{Input: reviewInput, Receipt: readiness}}, ProtocolDigest: cycle.Evidence.ProtocolDigest})
	if err != nil {
		t.Fatal(err)
	}
	return chapter
}
