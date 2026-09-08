package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newPhysicalArbitratedStoreForTest(t *testing.T, characters ...string) (*store.Store, domain.CharacterDecisionProposal, *domain.ChapterWorldSimulation) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("physical", 1); err != nil {
		t.Fatal(err)
	}
	character := "角色"
	if len(characters) > 0 {
		character = characters[0]
	}
	registry, actor, err := (domain.CharacterAgentRegistry{}).UpsertCharacter(character, nil, "core", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	quantity := 12.0
	docID, quantityID := "res_0000000000000001", "res_0000000000000002"
	physical, err := domain.FinalizeWorldPhysicalStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{
			{ResourceID: docID, Name: "SECRET作者目录名", Unit: "", ReadableFacts: []domain.ResourceReadableFactV2{{ID: "original", Text: "原始经营用油记录"}}},
			{ResourceID: quantityID, Name: "SECRET世界余量", Unit: "L", ActualAmount: &quantity},
		}, Actors: []domain.CharacterPhysicalStateV2{{AgentID: actor.AgentID, Character: actor.Character, Location: "A", Resources: []domain.CharacterResourceHoldingV2{
			{ResourceID: docID, PerceivedName: "手边原件", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
			{ResourceID: quantityID, PerceivedName: "油罐", PerceivedUnit: "L", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_tool_physical", Chapter: 1, TimeWindow: "本轮", PhysicalState: &physical})
	if err != nil {
		t.Fatal(err)
	}
	views, err := domain.BuildCharacterResourceViewsV2(physical, actor.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character, Location: "A", CurrentGoal: "核验原件", Pressure: "时间有限", StimulusDigest: stimulus.Digest, ResourceViews: views, KnownFacts: []domain.CharacterAgentFact{{ID: "known-doc", Kind: "known", Text: "手边原件可以阅读"}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{GenerationID: stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character, ObservationDigest: observation.Digest, Location: "A", CurrentGoal: "核验原件", Pressure: "时间有限", AvailableOptions: []string{"阅读原件后移动", "等候"}, Decision: "阅读后去B", IntendedAction: "读完手边原件再去B", ActionDuration: "数分钟", DecisionReason: "手边已有原件", KnowledgeRefs: []string{"known-doc"}, ResourceReads: []domain.ResourceReadRequestV2{{ResourceID: docID}}}, observation)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{GenerationID: stimulus.GenerationID, Chapter: 1, RegistryRoot: registry.RegistryRoot, Entries: []domain.CharacterAgentActivationEntry{{AgentID: actor.AgentID, Character: actor.Character, State: "active", Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func() error{
		func() error { return st.CharacterAgents.SaveRegistrySnapshot(stimulus.GenerationID, 1, registry) }, func() error { return st.CharacterAgents.SaveStimulus(stimulus) }, func() error { return st.CharacterAgents.SaveObservation(observation) }, func() error { return st.CharacterAgents.SaveActivation(activation) }, func() error { return st.CharacterAgents.SaveProposal(proposal, observation) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	args := map[string]any{"time_window": "本轮", "hard_contract_status": "feasible", "finalized": true,
		"resource_settlements":   []map[string]any{{"resource_id": quantityID, "before": 12, "delta": -.2, "after": 11.8, "evidence_refs": []string{proposal.Digest}}},
		"resolutions":            []map[string]any{{"agent_id": actor.AgentID, "character": actor.Character, "proposal_digest": proposal.Digest, "decision": proposal.Decision, "intended_action": proposal.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "世界实际余量11.8，未测量", "state_after": "SECRET世界实际11.8", "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "原件被核对", TransmissionPath: "后续核验", ArrivalChapter: 1, Visibility: "delayed", ProtagonistImpact: "改变下一步选择"}}, "post_state": map[string]any{"location": "B", "resource_updates": []any{}, "received_facts": []map[string]any{{"source_type": "resource_read", "resource_id": docID, "source_id": "original"}}}}},
		"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: actor.Character, AvailableOptions: proposal.AvailableOptions, ChosenDecision: proposal.Decision, DecisionReason: proposal.DecisionReason, PlanConstraints: []string{"先核验再移动"}, CausalChain: []string{"已阅读原件所以移动"}},
	}
	raw, _ := json.Marshal(args)
	tool := NewResolveChapterWorldTool(st, stimulus, activation, []domain.CharacterDecisionProposal{proposal}, "sha256:"+strings.Repeat("a", 64), nil, 1)
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	simulation, err := st.LoadChapterWorldSimulation(1)
	if err != nil || simulation == nil {
		t.Fatalf("simulation missing: %v", err)
	}
	return st, proposal, simulation
}

func TestResolvePhysicalV2SparseToolPreservesIntentAndStoresSourcedKnowledge(t *testing.T) {
	st, proposal, simulation := newPhysicalArbitratedStoreForTest(t)
	if simulation.PhysicalState == nil || simulation.CharacterAgentProtocol.Version != domain.CharacterAgentDecisionProtocolV2Version || simulation.CharacterDecisions[0].Location != "B" {
		t.Fatalf("v2 post state lost: %+v", simulation)
	}
	if proposal.Location != "A" || simulation.CharacterDecisions[0].Decision != proposal.Decision {
		t.Fatal("rewrote original origin/choice")
	}
	receipt, err := st.CharacterAgents.LoadArbitration(proposal.GenerationID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := domain.CharacterPrivateOutcomeV2(proposal, receipt.Resolutions[0], *simulation.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "原始经营用油记录") || strings.Contains(text, "11.8") || strings.Contains(text, "SECRET") {
		t.Fatalf("received document forgotten or author truth leaked: %s", text)
	}
}
