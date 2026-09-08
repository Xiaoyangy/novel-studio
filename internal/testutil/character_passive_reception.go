package testutil

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

type PassiveReceptionFixture struct {
	Inputs   domain.CharacterActivationInputSet
	Proposal domain.CharacterDecisionProposal
	Receipt  domain.WorldArbitrationReceipt
}

func CharacterPassiveReception(t *testing.T, remote, policy bool, contextDigests ...string) PassiveReceptionFixture {
	t.Helper()
	input := CharacterActivationInputs(t, contextDigests...)
	base := CharacterCycle(t, 1, "", nil, 0, contextDigests...)
	registry, sleeper, err := input.Registry.UpsertCharacter("乙", nil, "important", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	input.Registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	location := "船上"
	if remote {
		location = "岸边"
	}
	input.Stimulus.PhysicalState.Actors = append(input.Stimulus.PhysicalState.Actors, domain.CharacterPhysicalStateV2{AgentID: sleeper.AgentID, Character: "乙", Location: location, Resources: []domain.CharacterResourceHoldingV2{}})
	physical, err := domain.FinalizeWorldPhysicalStateV2(*input.Stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	input.Stimulus.PhysicalState = &physical
	if policy {
		input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterPassiveReceptionPolicyV2)
	}
	mechanism := domain.CodexMechanism{ID: "radio", Name: "无线电传话", Visibility: "public", Trigger: "主动发送", ActorScope: []string{"角色"}, Preconditions: []string{"无线电可用"}, Inputs: []string{"话音"}, Costs: []string{"电量"}, Effects: []string{"传递消息"}, FailureModes: []string{"信号中断"}, Observability: []string{"接收声音"}, Timing: "即时"}
	if remote {
		input.Stimulus.Mechanisms = []domain.CodexMechanism{mechanism}
	}
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	observation := input.Observations[0]
	observation.StimulusDigest = input.Stimulus.Digest
	if remote {
		observation.PublicMechanisms = []domain.CodexMechanism{mechanism}
	}
	observation, err = domain.FinalizeCharacterObservationPacket(observation)
	if err != nil {
		t.Fatal(err)
	}
	input.Observations[0] = observation
	memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: sleeper.AgentID, Character: "乙", State: "projected", GenerationID: input.Stimulus.GenerationID})
	if err != nil {
		t.Fatal(err)
	}
	sleeping, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: input.Stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: sleeper.AgentID, Character: "乙", Tier: "important", Location: location, CurrentGoal: "等候", Pressure: "风雨", StimulusDigest: input.Stimulus.Digest, MemoryRoot: memory.MemoryRoot, Sources: []string{domain.CharacterSourceRefPolicyV2}})
	if err != nil {
		t.Fatal(err)
	}
	input.Observations = append(input.Observations, sleeping)
	input.Memories = append(input.Memories, memory)
	input.Activation.RegistryRoot = input.Registry.RegistryRoot
	input.Activation.Entries[0].ObservationDigest = observation.Digest
	input.Activation.Entries = append(input.Activation.Entries, domain.CharacterAgentActivationEntry{AgentID: sleeper.AgentID, Character: "乙", Tier: "important", State: domain.CharacterAgentSleeping})
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	if err != nil {
		t.Fatal(err)
	}
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	if err != nil {
		t.Fatal(err)
	}
	proposal := base.Evidence.Proposals[0]
	proposal.ObservationDigest = observation.Digest
	proposal.Communications = []domain.CharacterCommunicationV2{{ID: "report_status", ToCharacter: "乙", Kind: "information", Text: "燃油上次观测为12升", KnowledgeRefs: []string{"known-fuel"}}}
	if remote {
		proposal.MechanismRefs = []string{mechanism.ID}
	}
	proposal, err = domain.FinalizeCharacterDecisionProposal(proposal, observation)
	if err != nil {
		t.Fatal(err)
	}
	receipt := base.Evidence.Arbitrations[0]
	receipt.Digest, receipt.StimulusDigest, receipt.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	receipt.ProposalDigests = []string{proposal.Digest}
	receipt.Resolutions[0].ProposalDigest = proposal.Digest
	receipt.Resolutions[0].StateAfter = "SECRET世界真值与未来计划不得进入收到消息"
	receipt.ResourceSettlements[0].EvidenceRefs = []string{proposal.Digest}
	day := receipt.StoryTime.EndDay
	reception := domain.CharacterPassiveReceptionV2{ToAgentID: sleeper.AgentID, FromAgentID: proposal.AgentID, SourceProposalDigest: proposal.Digest, CommunicationID: "report_status", DeliveredAtDay: &day, Channel: "in_person"}
	if remote {
		reception.Channel, reception.MechanismRef = "mechanism", mechanism.ID
		receipt.Resolutions[0].MechanismRefs = []string{mechanism.ID}
	}
	receipt.PassiveReceptions = []domain.CharacterPassiveReceptionV2{reception}
	return PassiveReceptionFixture{Inputs: input, Proposal: proposal, Receipt: receipt}
}
