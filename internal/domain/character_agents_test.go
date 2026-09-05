package domain

import (
	"strings"
	"testing"
)

func TestCharacterAgentRegistryKeepsIdentityAcrossRenameAndAlias(t *testing.T) {
	registry, first, err := (CharacterAgentRegistry{}).UpsertCharacter("林默", []string{"小林"}, "core", 1, "chapter:000001")
	if err != nil {
		t.Fatal(err)
	}
	registry, renamed, err := registry.UpsertCharacter("林川", []string{"林默", "小林"}, "core", 8, "chapter:000008")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.AgentID != first.AgentID {
		t.Fatalf("rename minted a new agent id: %s != %s", renamed.AgentID, first.AgentID)
	}
	for _, alias := range []string{"林默", "小林", "林川"} {
		resolved, ok := registry.Resolve(alias)
		if !ok || resolved.AgentID != first.AgentID {
			t.Fatalf("alias %q did not resolve to stable agent", alias)
		}
	}
}

func TestCharacterProposalRejectsKnowledgeOutsideObservation(t *testing.T) {
	stimulus, err := FinalizeWorldStimulusPacket(WorldStimulusPacket{Version: WorldStimulusPacketVersion, GenerationID: "pg2_test", Chapter: 3, TimeWindow: "上午"})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{
		Version: CharacterObservationVersion, GenerationID: "pg2_test", Chapter: 3, Round: 1,
		AgentID: "ca_test", Character: "林默", CurrentGoal: "守住证据", Pressure: "有人逼近",
		StimulusDigest: stimulus.Digest, KnownFacts: []CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "门已锁"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := validCharacterAgentProposalForTest(observation)
	proposal.KnowledgeRefs = []string{"other-character-secret"}
	if _, err := FinalizeCharacterDecisionProposal(proposal, observation); err == nil || !strings.Contains(err.Error(), "unavailable knowledge") {
		t.Fatalf("private/future knowledge was accepted: %v", err)
	}
}

func TestWorldArbiterCannotRewriteCharacterIntent(t *testing.T) {
	stimulus, activation, observation, proposal := validCharacterAgentProtocolForTest(t)
	receipt := validCharacterArbitrationForTest(stimulus, activation, proposal)
	receipt.Resolutions[0].Decision = "替角色改掉的决定"
	if _, err := FinalizeWorldArbitrationReceipt(receipt, stimulus, activation, []CharacterDecisionProposal{proposal}, 1); err == nil || !strings.Contains(err.Error(), "rewrote intent") {
		t.Fatalf("arbiter intent rewrite was accepted: %v", err)
	}
	_ = observation
}

func TestCharacterAgentEvidenceDetectsTamperedObservation(t *testing.T) {
	stimulus, activation, observation, proposal := validCharacterAgentProtocolForTest(t)
	arbitration := validCharacterArbitrationForTest(stimulus, activation, proposal)
	finalArbitration, err := FinalizeWorldArbitrationReceipt(arbitration, stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{
		Version: CharacterAgentEvidenceVersion, GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter,
		Registry: CharacterAgentRegistry{Version: CharacterAgentRegistryVersion, Entries: []CharacterAgentRecord{{AgentID: proposal.AgentID, Character: proposal.Character, Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1}}},
		Stimulus: stimulus, Activation: activation, Observations: []CharacterObservationPacket{observation},
		Proposals: []CharacterDecisionProposal{proposal}, Arbitrations: []WorldArbitrationReceipt{finalArbitration},
		MemoryRoots: []string{"sha256:memory"}, ProtocolDigest: "sha256:protocol",
	})
	if err == nil {
		// Registry root is intentionally filled below; reaching this branch means
		// the fixture accidentally became permissive.
		t.Fatal("evidence unexpectedly accepted an unsigned registry fixture")
	}
	registry, err := FinalizeCharacterAgentRegistry(CharacterAgentRegistry{Version: CharacterAgentRegistryVersion, Entries: []CharacterAgentRecord{{AgentID: proposal.AgentID, Character: proposal.Character, Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	activation.RegistryRoot = registry.RegistryRoot
	activation.Digest = ""
	activation, err = FinalizeCharacterAgentActivation(activation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration.ActivationDigest = activation.Digest
	arbitration.Digest = ""
	finalArbitration, err = FinalizeWorldArbitrationReceipt(arbitration, stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err = FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{
		Version: CharacterAgentEvidenceVersion, GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter,
		Registry: registry, Stimulus: stimulus, Activation: activation,
		Observations: []CharacterObservationPacket{observation}, Proposals: []CharacterDecisionProposal{proposal},
		Arbitrations: []WorldArbitrationReceipt{finalArbitration}, MemoryRoots: []string{"sha256:memory"}, ProtocolDigest: "sha256:protocol",
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence.Observations[0].KnownFacts[0].Text = "被篡改"
	if err := ValidateCharacterAgentEvidenceBundle(evidence); err == nil {
		t.Fatal("tampered observation passed sealed evidence validation")
	}
}

func validCharacterAgentProtocolForTest(t *testing.T) (WorldStimulusPacket, CharacterAgentActivation, CharacterObservationPacket, CharacterDecisionProposal) {
	t.Helper()
	stimulus, err := FinalizeWorldStimulusPacket(WorldStimulusPacket{Version: WorldStimulusPacketVersion, GenerationID: "pg2_test", Chapter: 3, TimeWindow: "上午"})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{
		Version: CharacterObservationVersion, GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter, Round: 1,
		AgentID: "ca_test", Character: "林默", CurrentGoal: "守住证据", Pressure: "有人逼近",
		StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:memory",
		KnownFacts: []CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "门已锁"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := FinalizeCharacterAgentActivation(CharacterAgentActivation{
		Version: CharacterAgentActivationVersion, GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter,
		RegistryRoot: "sha256:registry", Entries: []CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := validCharacterAgentProposalForTest(observation)
	proposal, err = FinalizeCharacterDecisionProposal(proposal, observation)
	if err != nil {
		t.Fatal(err)
	}
	return stimulus, activation, observation, proposal
}

func validCharacterAgentProposalForTest(observation CharacterObservationPacket) CharacterDecisionProposal {
	return CharacterDecisionProposal{
		Version: CharacterDecisionProposalVersion, GenerationID: observation.GenerationID, Chapter: observation.Chapter,
		Round: observation.Round, AgentID: observation.AgentID, Character: observation.Character, ObservationDigest: observation.Digest,
		Location: "档案室", CurrentGoal: "守住证据", Pressure: "有人逼近", AvailableOptions: []string{"带走证据", "原地等待"},
		Decision: "带走证据", DecisionReason: "门已锁但风险正在增加", IntendedAction: "把证据装入背包并离开", ActionDuration: "五分钟", KnowledgeRefs: []string{"known-1"},
	}
}

func validCharacterArbitrationForTest(stimulus WorldStimulusPacket, activation CharacterAgentActivation, proposal CharacterDecisionProposal) WorldArbitrationReceipt {
	return WorldArbitrationReceipt{
		Version: WorldArbitrationReceiptVersion, GenerationID: stimulus.GenerationID, Chapter: stimulus.Chapter, Round: 1,
		StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ProposalDigests: []string{proposal.Digest}, Finalized: true,
		HardContractStatus: "feasible",
		Resolutions: []CharacterDecisionResolution{{
			AgentID: proposal.AgentID, Character: proposal.Character, ProposalDigest: proposal.Digest,
			Decision: proposal.Decision, IntendedAction: proposal.IntendedAction, ActionOrder: 1, Outcome: "success",
			CompletionState: "completed", ImmediateResult: "证据被安全带走", StateAfter: "离开档案室",
			ButterflyEffects: []DecisionButterflyEffect{{Effect: "追查转向", TransmissionPath: "失窃被发现", ArrivalChapter: stimulus.Chapter, Visibility: "delayed", ProtagonistImpact: "增加追兵"}},
		}},
		ProtagonistProjection: ProtagonistDecisionProjection{Protagonist: proposal.Character, AvailableOptions: proposal.AvailableOptions, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, PlanConstraints: []string{"保留选择"}, CausalChain: []string{"带走证据→追查转向"}},
	}
}

func TestCharacterAgentSuccessorPlanPreservesRangeAndHardBoundary(t *testing.T) {
	plan := CharacterAgentSuccessorPlan{
		Version: CharacterAgentSuccessorPlanVersion, ParentGenerationID: "pg2_parent", BaseCanonChapter: 4,
		TriggerChapter: 6, ArcFirstChapter: 5, ArcLastChapter: 7, BookLastChapter: 20,
		ArbitrationDigest: "sha256:arbiter", AcceptedCanonRoot: "sha256:canon", EndingDirection: "真相公开",
		NonNegotiables: []string{"主角存活"}, HardContractConflicts: []string{"第7章前必须保住证据"},
		RevisedChapters: []OutlineEntry{
			{Chapter: 6, Title: "转向", CoreEvent: "改走水路", Hook: "船已离岸", Scenes: []string{"码头交涉"}},
			{Chapter: 7, Title: "闭合", CoreEvent: "证据送达", Hook: "新的追踪出现", Scenes: []string{"交付证据"}},
		},
		ArchitectSummary: "不改变角色拒绝原路线的选择，以替代路径兑现硬合同。",
	}
	finalized, err := FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil || finalized.Digest == "" {
		t.Fatalf("valid successor plan rejected: plan=%+v err=%v", finalized, err)
	}
	plan.RevisedChapters = plan.RevisedChapters[:1]
	if _, err := FinalizeCharacterAgentSuccessorPlan(plan); err == nil {
		t.Fatal("successor plan accepted an incomplete remaining arc")
	}
}

func TestWorldArbitrationHardContractConflictFailsClosedAfterRevisionBudget(t *testing.T) {
	stimulus, activation, _, proposal := validCharacterAgentProtocolForTest(t)
	receipt := validCharacterArbitrationForTest(stimulus, activation, proposal)
	receipt.Round = 2
	receipt.Finalized = false
	receipt.HardContractStatus = "infeasible"
	receipt.HardContractConflicts = []string{"结局要求证据存在，但角色已销毁唯一证据"}
	receipt.Digest = ""
	if _, err := FinalizeWorldArbitrationReceipt(receipt, stimulus, activation, []CharacterDecisionProposal{proposal}, 1); err != nil {
		t.Fatalf("hard-contract failure should persist for Architect successor routing: %v", err)
	}
}
