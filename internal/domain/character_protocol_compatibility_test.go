package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSealedCharacterBundleKeepsItsStoredProtocolAndLegacyObservation(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	oldProtocol := "sha256:" + strings.Repeat("a", 64)
	decision := bundle.ChapterWorldSimulation.CharacterDecisions[0]
	registry, err := FinalizeCharacterAgentRegistry(CharacterAgentRegistry{Entries: []CharacterAgentRecord{{
		AgentID: "ca_legacy", Character: decision.Character, Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	legacyMechanism := CodexMechanism{ID: "M_LEGACY", Name: "历史已公开机制", Visibility: "formal", Effects: []string{"旧协议已知状态"}}
	stimulus, err := FinalizeWorldStimulusPacket(WorldStimulusPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, TimeWindow: "上午",
		Sources: []string{"character-agent-protocol:" + oldProtocol}, Mechanisms: []CodexMechanism{legacyMechanism},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: "ca_legacy", Character: decision.Character,
		CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:memory",
		KnownFacts:       []CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "已知历史事实"}},
		PublicRules:      []CharacterAgentFact{{ID: "legacy-rule", Kind: "world_rule", Text: "旧格式公开规则"}},
		PublicMechanisms: []CodexMechanism{legacyMechanism},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := FinalizeCharacterAgentActivation(CharacterAgentActivation{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, RegistryRoot: registry.RegistryRoot,
		Entries: []CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: CharacterAgentActive,
			Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := FinalizeCharacterDecisionProposal(CharacterDecisionProposal{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character,
		ObservationDigest: observation.Digest, Location: decision.Location, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure,
		AvailableOptions: decision.AvailableOptions, Decision: decision.Decision, DecisionReason: decision.DecisionReason,
		IntendedAction: decision.Action, ActionDuration: decision.ActionDuration, KnowledgeRefs: []string{"known-1"},
	}, observation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration, err := FinalizeWorldArbitrationReceipt(validCharacterArbitrationForTest(stimulus, activation, proposal), stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Registry: registry, Stimulus: stimulus, Activation: activation,
		Observations: []CharacterObservationPacket{observation}, Proposals: []CharacterDecisionProposal{proposal},
		Arbitrations: []WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: oldProtocol,
	})
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
	bundle.ChapterWorldSimulation.CharacterAgentProtocol = &CharacterAgentProtocolReceipt{
		Version: CharacterAgentDecisionProtocolVersion, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest,
		ActivationDigest: activation.Digest, ObservationDigests: []string{observation.Digest}, ProposalDigests: []string{proposal.Digest},
		ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, MemoryRoots: evidence.MemoryRoots, ProtocolDigest: oldProtocol,
	}
	planningV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("legacy character protocol bundle no longer verifies: %v", err)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var restored ProjectedChapterBundle
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProjectedChapterBundle(restored); err != nil {
		t.Fatalf("legacy bundle no longer verifies after disk round trip: %v", err)
	}
	roundTrip, err := json.Marshal(restored)
	if err != nil || !bytes.Equal(raw, roundTrip) || restored.CharacterAgentEvidence.ProtocolDigest != oldProtocol {
		t.Fatal("verification rewrote the stored protocol or legacy observation")
	}
	// Relabeling only the simulation with the current/new protocol cannot
	// upgrade a historical evidence chain, even if the outer hash is refreshed.
	restored.ChapterWorldSimulation.CharacterAgentProtocol.ProtocolDigest = "sha256:" + strings.Repeat("b", 64)
	planningV2RebindRenderContext(t, &restored)
	restored.BundleDigest = planningV2MustBundleDigest(t, restored)
	if err := ValidateProjectedChapterBundle(restored); err == nil || !strings.Contains(err.Error(), "evidence identity") {
		t.Fatalf("new protocol was allowed to silently relabel old evidence: %v", err)
	}
}
