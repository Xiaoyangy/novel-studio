package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectedStoreRejectsCompleteV1EvidenceForV2BeforeAnyIntentWrite(t *testing.T) {
	root := t.TempDir()
	projected := NewStore(root).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, true)
	attachStoreIndependentV1EvidenceForProtocolTest(t, &bundles[0])
	if err := domain.ValidateProjectedChapterBundle(bundles[0]); err != nil {
		t.Fatalf("test requires independently valid full v1 evidence: %v", err)
	}
	generation.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	var err error
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	before := generationProtocolFilesForTest(t, root)
	if _, err := projected.ProjectChapterAndAdvance(generation.GenerationDigest, generation.ChainTailRoot, registry.RegistryRoot, *cursor, bundles[0], registry); err == nil || !strings.Contains(err.Error(), "character protocol") {
		t.Fatalf("complete v1 evidence entered explicit v2 transaction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, projectedChapterIntentsDir, generation.GenerationID)); !os.IsNotExist(err) {
		t.Fatalf("rejected complete v1 evidence persisted an intent: %v", err)
	}
	if after := generationProtocolFilesForTest(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("protocol rejection changed registry, cursor, generation, intent, or other durable files")
	}
}

func TestProjectedStoreSealedReadersRejectResignedGenerationProtocolMixture(t *testing.T) {
	root := t.TempDir()
	projected := NewStore(root).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, true)
	if generation.CharacterAgentProtocol != "" || bundles[0].ChapterWorldSimulation.Version != 1 {
		t.Fatal("test requires metadata-free historical monolithic simulation")
	}
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	if err := projected.SaveProjectedChapterBundle(bundles[0]); err != nil {
		t.Fatal(err)
	}
	seal, err := projected.SealGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := projected.LoadSealedGeneration(generation.GenerationID)
	if err != nil || sealed == nil {
		t.Fatalf("historical generation no longer seals/loads: %v", err)
	}
	manifest, err := projected.LoadProjectedChainManifest(generation.GenerationID)
	if err != nil || manifest == nil {
		t.Fatalf("historical manifest no longer verifies: %v", err)
	}
	loaded, err := projected.LoadProjectedChapterBundles(generation.GenerationID)
	if err != nil || len(loaded) != 1 || loaded[0].BundleDigest != bundles[0].BundleDigest {
		t.Fatalf("historical bundles no longer load unchanged: %v", err)
	}
	// Re-sign both the generation and its seal receipt, so a generic stale
	// checksum rejection cannot accidentally satisfy this regression.
	sealed.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	sealed.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(*sealed)
	if err != nil {
		t.Fatal(err)
	}
	seal.GenerationDigest = sealed.GenerationDigest
	seal.ReceiptDigest, err = domain.ComputeSealReceiptV2Digest(*seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateSealReceiptV2(*seal, *sealed, *manifest); err != nil {
		t.Fatalf("test-only re-signing left an unrelated seal integrity failure: %v", err)
	}
	base := projectedSealedGenerationPath(generation.GenerationID)
	if err := projected.io.WriteJSON(filepath.Join(base, projectedGenerationManifestFile), *sealed); err != nil {
		t.Fatal(err)
	}
	if err := projected.io.WriteJSON(filepath.Join(base, projectedSealReceiptFile), *seal); err != nil {
		t.Fatal(err)
	}
	before := generationProtocolFilesForTest(t, root)
	for _, tc := range []struct {
		name string
		read func() error
	}{
		{"bundles", func() error { _, err := projected.LoadProjectedChapterBundles(generation.GenerationID); return err }},
		{"chain_manifest", func() error { _, err := projected.LoadProjectedChainManifest(generation.GenerationID); return err }},
		{"sealed_generation", func() error { _, err := projected.LoadSealedGeneration(generation.GenerationID); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(); err == nil || !strings.Contains(err.Error(), "character protocol") {
				t.Fatalf("re-signed sealed mixture was not rejected by its protocol binding: %v", err)
			}
		})
	}
	if after := generationProtocolFilesForTest(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("read-time protocol rejection rewrote historical sealed files")
	}
}

// Construct a real v1 actor evidence chain, not merely a Version=2 label on
// a legacy simulation. All proposal/arbitration/observation hashes validate
// before the enclosing generation's incompatible v2 contract is tested.
func attachStoreIndependentV1EvidenceForProtocolTest(t *testing.T, bundle *domain.ProjectedChapterBundle) {
	t.Helper()
	decision := bundle.ChapterWorldSimulation.CharacterDecisions[0]
	protocol := projectedStoreV2Digest("independent-v1-protocol")
	registry, err := domain.FinalizeCharacterAgentRegistry(domain.CharacterAgentRegistry{Entries: []domain.CharacterAgentRecord{{AgentID: "ca_store_v1", Character: decision.Character, Tier: "core", Status: domain.CharacterAgentActive, MemoryVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, TimeWindow: "上午", Sources: []string{"character-agent-protocol:" + protocol}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: "ca_store_v1", Character: decision.Character, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, StimulusDigest: stimulus.Digest, MemoryRoot: projectedStoreV2Digest("v1-memory"), KnownFacts: []domain.CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "此前已收到的事实"}}})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, RegistryRoot: registry.RegistryRoot, Entries: []domain.CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character, ObservationDigest: observation.Digest, Location: decision.Location, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, AvailableOptions: decision.AvailableOptions, Decision: decision.Decision, DecisionReason: decision.DecisionReason, IntendedAction: decision.Action, ActionDuration: decision.ActionDuration, KnowledgeRefs: []string{"known-1"}}, observation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration, err := domain.FinalizeWorldArbitrationReceipt(domain.WorldArbitrationReceipt{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ProposalDigests: []string{proposal.Digest}, Finalized: true, HardContractStatus: "feasible",
		Resolutions:           []domain.CharacterDecisionResolution{{AgentID: proposal.AgentID, Character: proposal.Character, ProposalDigest: proposal.Digest, Decision: proposal.Decision, IntendedAction: proposal.IntendedAction, ActionOrder: 1, Outcome: "success", CompletionState: "completed", ImmediateResult: decision.ImmediateResult, StateAfter: decision.StateAfter, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "结果可供后续复核", TransmissionPath: "现场反馈", ArrivalChapter: bundle.Chapter, Visibility: "delayed", ProtagonistImpact: "后续选择有据"}}}},
		ProtagonistProjection: bundle.ChapterWorldSimulation.ProtagonistProjection,
	}, stimulus, activation, []domain.CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Registry: registry, Stimulus: stimulus, Activation: activation, Observations: []domain.CharacterObservationPacket{observation}, Proposals: []domain.CharacterDecisionProposal{proposal}, Arbitrations: []domain.WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: protocol})
	if err != nil {
		t.Fatal(err)
	}
	bundle.CharacterAgentEvidence = &evidence
	bundle.ChapterWorldSimulation.Version = 2
	bundle.ChapterWorldSimulation.CharacterDecisions, err = arbitration.CharacterDecisions([]domain.CharacterDecisionProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	bundle.ChapterWorldSimulation.ProtagonistProjection = arbitration.ProtagonistProjection
	bundle.ChapterWorldSimulation.CharacterAgentProtocol = &domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolVersion, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ObservationDigests: []string{observation.Digest}, ProposalDigests: []string{proposal.Digest}, ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, MemoryRoots: evidence.MemoryRoots, ProtocolDigest: protocol}
	projectedStoreV2RebindRenderContext(t, bundle)
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(*bundle)
	if err != nil {
		t.Fatal(err)
	}
}
