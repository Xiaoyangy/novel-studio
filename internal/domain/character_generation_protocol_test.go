package domain

import (
	"strings"
	"testing"
)

func TestGenerationCharacterProtocolRejectsMixedBundlesAndKeepsHistoricalMetadata(t *testing.T) {
	for _, tc := range []struct {
		name          string
		protocol      string
		independentV1 bool
		allowed       bool
	}{
		{"v2_rejects_legacy", CharacterAgentDecisionProtocolV2Version, false, false},
		{"v2_rejects_complete_v1", CharacterAgentDecisionProtocolV2Version, true, false},
		{"v1_rejects_legacy", CharacterAgentDecisionProtocolVersion, false, false},
		{"v1_accepts_complete_v1", CharacterAgentDecisionProtocolVersion, true, true},
		{"explicit_legacy_accepts_monolithic", "legacy", false, true},
		{"explicit_legacy_rejects_independent", "legacy", true, false},
		{"missing_metadata_keeps_legacy", "", false, true},
		{"missing_metadata_keeps_complete_v1", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generation, registry, bundles := planningV2TestChain(t, 1)
			if tc.independentV1 {
				attachGenerationProtocolV1BundleForTest(t, &bundles[0])
			}
			generation.CharacterAgentProtocol = tc.protocol
			generation.ChainHeadRoot, generation.ChainTailRoot = bundles[0].BundleDigest, bundles[0].BundleDigest
			var err error
			generation.GenerationDigest, err = ComputePlanningGenerationV2Digest(generation)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidatePlanningGenerationV2(generation); err != nil {
				t.Fatal(err)
			}
			if err := ValidateProjectedChapterBundle(bundles[0]); err != nil {
				t.Fatal(err)
			}
			err = ValidateProjectedChapterBundleChain(generation, bundles, registry)
			if (err == nil) != tc.allowed || (err != nil && !strings.Contains(err.Error(), "character protocol")) {
				t.Fatalf("protocol binding allowed=%t: %v", tc.allowed, err)
			}
		})
	}
}

func attachGenerationProtocolV1BundleForTest(t *testing.T, bundle *ProjectedChapterBundle) {
	t.Helper()
	decision := bundle.ChapterWorldSimulation.CharacterDecisions[0]
	protocol := "sha256:" + strings.Repeat("a", 64)
	registry, err := FinalizeCharacterAgentRegistry(CharacterAgentRegistry{Entries: []CharacterAgentRecord{{AgentID: "ca_probe", Character: decision.Character, Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := FinalizeWorldStimulusPacket(WorldStimulusPacket{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, TimeWindow: "上午", Sources: []string{"character-agent-protocol:" + protocol}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: "ca_probe", Character: decision.Character, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:memory", KnownFacts: []CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "此前已收到的事实"}}})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := FinalizeCharacterAgentActivation(CharacterAgentActivation{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, RegistryRoot: registry.RegistryRoot, Entries: []CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := FinalizeCharacterDecisionProposal(CharacterDecisionProposal{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character, ObservationDigest: observation.Digest, Location: decision.Location, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, AvailableOptions: decision.AvailableOptions, Decision: decision.Decision, DecisionReason: decision.DecisionReason, IntendedAction: decision.Action, ActionDuration: decision.ActionDuration, KnowledgeRefs: []string{"known-1"}}, observation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration, err := FinalizeWorldArbitrationReceipt(validCharacterArbitrationForTest(stimulus, activation, proposal), stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Registry: registry, Stimulus: stimulus, Activation: activation, Observations: []CharacterObservationPacket{observation}, Proposals: []CharacterDecisionProposal{proposal}, Arbitrations: []WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: protocol})
	if err != nil {
		t.Fatal(err)
	}
	bundle.CharacterAgentEvidence = &evidence
	bundle.ChapterWorldSimulation.Version = 2
	bundle.ChapterWorldSimulation.CharacterDecisions, err = arbitration.CharacterDecisions([]CharacterDecisionProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	bundle.ChapterWorldSimulation.ProtagonistProjection = arbitration.ProtagonistProjection
	bundle.ChapterWorldSimulation.CharacterAgentProtocol = &CharacterAgentProtocolReceipt{Version: CharacterAgentDecisionProtocolVersion, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ObservationDigests: []string{observation.Digest}, ProposalDigests: []string{proposal.Digest}, ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, MemoryRoots: evidence.MemoryRoots, ProtocolDigest: protocol}
	planningV2RebindRenderContext(t, bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, *bundle)
}
