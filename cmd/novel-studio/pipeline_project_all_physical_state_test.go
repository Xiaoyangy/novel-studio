package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func projectAllPhysicalNumber(value float64) *float64 { return &value }

const (
	physicalBatteryID = "res_0000000000000001"
	physicalSharedID  = "res_0000000000000002"
	physicalPumpID    = "res_0000000000000003"
	physicalUnknownID = "res_0000000000000004"
)

// Construct post-state only by finalizing and applying a real arbitration.
// C2 assertions below must never seed expected post-balances into its store.
func projectAllPhysicalArtifacts(t *testing.T, generation domain.PlanningGenerationV2, withKnowledge ...bool) (*agents.ProjectedChapterArtifacts, domain.OutlineEntry, domain.CharacterAgentRegistry) {
	t.Helper()
	knowledge := len(withKnowledge) > 0 && withKnowledge[0]
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	artifacts.Plan.CausalSimulation.StructuralResources = []domain.StructuralResourcePressure{{Resource: "开阀资格", Controller: "守门人", ScarcityReason: "单一阀门需要错峰使用", AccessRule: "申请", BlackMarketOrInformalPath: "只能请求其他排队者交换顺序", PriceOrCost: "必须排队", PowerEffect: "守门人掌握排队次序", ChapterPressure: "赶不上截止"}}
	registry := domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	for _, name := range []string{"主角", "同伴"} {
		var err error
		registry, _, err = registry.UpsertCharacter(name, nil, "core", 1, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	holding := func(id, access string, observed *float64) domain.CharacterResourceHoldingV2 {
		perception := domain.ResourcePerceptionV2{Kind: "unknown"}
		if observed != nil {
			perception = domain.ResourcePerceptionV2{Kind: "last_observed", Amount: observed, EvidenceRefs: []string{"initial-observation"}}
		}
		unit := "L"
		if id == physicalBatteryID || id == physicalUnknownID {
			unit = "Wh"
		}
		return domain.CharacterResourceHoldingV2{ResourceID: id, PerceivedName: "可用设备", PerceivedUnit: unit, Access: access, Perception: perception}
	}
	before := domain.WorldPhysicalStateV2{
		Version: domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{
			{ResourceID: physicalBatteryID, Name: "电量", Unit: "Wh", ActualAmount: projectAllPhysicalNumber(110)},
			{ResourceID: physicalSharedID, Name: "共享水箱", Unit: "L", ActualAmount: projectAllPhysicalNumber(100)},
			{ResourceID: physicalPumpID, Name: "作者秘密泵量", Unit: "L", ActualAmount: projectAllPhysicalNumber(12)},
			{ResourceID: physicalUnknownID, Name: "未测电池", Unit: "Wh"},
		},
	}
	if knowledge {
		before.Resources[2].ReadableFacts = []domain.ResourceReadableFactV2{{ID: "maintenance-note", Text: "铭牌记载：检修期曾换过阀门。"}, {ID: "unread-note", Text: "未读到的作者私有记录。"}}
	}
	for _, name := range []string{"主角", "同伴"} {
		record, _ := registry.Resolve(name)
		resources := []domain.CharacterResourceHoldingV2{holding(physicalSharedID, "shared", projectAllPhysicalNumber(100))}
		if name == "主角" {
			resources = append(resources, holding(physicalBatteryID, "exclusive", projectAllPhysicalNumber(110)), holding(physicalPumpID, "exclusive", projectAllPhysicalNumber(12)), holding(physicalUnknownID, "exclusive", nil))
		}
		before.Actors = append(before.Actors, domain.CharacterPhysicalStateV2{AgentID: record.AgentID, Character: name, Location: "A", Resources: resources})
	}
	before, err = domain.FinalizeWorldPhysicalStateV2(before)
	if err != nil {
		t.Fatal(err)
	}
	protocolDigest := projectAllCmdTestDigest("physical-v2-protocol")
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{
		Version: domain.WorldStimulusPacketV2Version, GenerationID: generation.GenerationID, Chapter: 1,
		TimeWindow: "上午", PhysicalState: &before, Sources: []string{"initial-observation", "character-agent-protocol:" + protocolDigest},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation := domain.CharacterAgentActivation{GenerationID: generation.GenerationID, Chapter: 1, RegistryRoot: registry.RegistryRoot}
	var observations []domain.CharacterObservationPacket
	var proposals []domain.CharacterDecisionProposal
	var resolutions []domain.CharacterDecisionResolution
	for index, actor := range before.Actors {
		views, err := domain.BuildCharacterResourceViewsV2(before, actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
			Version: domain.CharacterObservationV2Version, GenerationID: generation.GenerationID, Chapter: 1, Round: 1,
			AgentID: actor.AgentID, Character: actor.Character, Tier: "core", Location: actor.Location,
			CurrentGoal: "完成小额验证", Pressure: "截止逼近", StimulusDigest: stimulus.Digest,
			MemoryRoot: projectAllCmdTestDigest("memory-" + actor.AgentID), ResourceViews: views,
			Resources:  domain.FormatCharacterResourceViewsV2(views),
			KnownFacts: []domain.CharacterAgentFact{{ID: "known-route", Kind: "known", Text: "A通往B", Source: "initial-observation", Visibility: "private"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
		activation.Entries = append(activation.Entries, domain.CharacterAgentActivationEntry{AgentID: actor.AgentID, Character: actor.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"appearance"}, ObservationDigest: observation.Digest})
		proposalInput := domain.CharacterDecisionProposal{
			GenerationID: generation.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character,
			ObservationDigest: observation.Digest, Location: "A", Time: "上午", CurrentGoal: observation.CurrentGoal, Pressure: observation.Pressure,
			AvailableOptions: []string{"小额验证", "暂时放弃"}, Decision: "小额验证", DecisionReason: "控制损失并留下证据",
			IntendedAction: "前往B完成验证", ActionDuration: "两分钟", KnowledgeRefs: []string{"known-route"},
		}
		if knowledge {
			if actor.Character == "同伴" {
				proposalInput.Communications = []domain.CharacterCommunicationV2{{ID: "route-warning", ToCharacter: "主角", Kind: "statement", Text: "我提醒你：A通往B。", KnowledgeRefs: []string{"known-route"}}}
			}
			if actor.Character == "主角" {
				proposalInput.ResourceReads = []domain.ResourceReadRequestV2{{ResourceID: physicalPumpID}}
			}
		}
		proposal, err := domain.FinalizeCharacterDecisionProposal(proposalInput, observation)
		if err != nil {
			t.Fatal(err)
		}
		proposals = append(proposals, proposal)
		post := actor
		post.Location = "B"
		resolutions = append(resolutions, domain.CharacterDecisionResolution{
			AgentID: actor.AgentID, Character: actor.Character, ProposalDigest: proposal.Digest, Decision: proposal.Decision, IntendedAction: proposal.IntendedAction,
			ActionOrder: index + 1, Outcome: "success", CompletionState: "completed", ImmediateResult: "作者私有实量11.8，未被角色测量",
			StateAfter: "作者私有实量11.8，不能冒充角色知识", PostState: &post, VisibleToPOV: actor.Character == "主角",
			ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: actor.Character + "下一步需要核对凭据", TransmissionPath: "本人携带凭据", ArrivalChapter: 2, Visibility: "delayed", ProtagonistImpact: "增加核对压力"}},
		})
	}
	activation, err = domain.FinalizeCharacterAgentActivation(activation)
	if err != nil {
		t.Fatal(err)
	}
	if knowledge {
		for i := range resolutions {
			if resolutions[i].Character != "主角" {
				continue
			}
			for _, proposal := range proposals {
				var fact domain.CharacterReceivedFactV2
				if proposal.Character == "同伴" {
					fact = domain.CharacterReceivedFactV2{Kind: "statement", Text: "我提醒你：A通往B。", SourceType: "communication", SourceID: "route-warning", SourceProposalDigest: proposal.Digest, FromAgentID: proposal.AgentID, Chapter: 1}
				} else {
					fact = domain.CharacterReceivedFactV2{Kind: "document_statement", Text: "铭牌记载：检修期曾换过阀门。", SourceType: "resource_read", SourceID: "maintenance-note", SourceProposalDigest: proposal.Digest, ResourceID: physicalPumpID, Chapter: 1}
				}
				fact.ID = domain.CharacterReceivedFactIDV2(resolutions[i].AgentID, fact)
				resolutions[i].PostState.ReceivedFacts = append(resolutions[i].PostState.ReceivedFacts, fact)
			}
		}
	}
	arbitration := domain.WorldArbitrationReceipt{
		Version: domain.WorldArbitrationReceiptV2Version, GenerationID: generation.GenerationID, Chapter: 1, Round: 1,
		StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, Resolutions: resolutions,
		HardContractStatus: "feasible", Finalized: true, ProtagonistProjection: artifacts.WorldSimulation.ProtagonistProjection,
	}
	for _, proposal := range proposals {
		arbitration.ProposalDigests = append(arbitration.ProposalDigests, proposal.Digest)
	}
	for _, change := range []struct {
		id                   string
		before, delta, after float64
	}{{physicalBatteryID, 110, -6, 104}, {physicalSharedID, 100, -5, 95}, {physicalPumpID, 12, -0.2, 11.8}} {
		arbitration.ResourceSettlements = append(arbitration.ResourceSettlements, domain.ResourceSettlementV2{ResourceID: change.id, Before: projectAllPhysicalNumber(change.before), Delta: projectAllPhysicalNumber(change.delta), After: projectAllPhysicalNumber(change.after), EvidenceRefs: []string{stimulus.Digest}})
	}
	arbitration, err = domain.FinalizeWorldArbitrationReceipt(arbitration, stimulus, activation, proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(arbitration, stimulus, proposals...)
	if err != nil {
		t.Fatal(err)
	}
	evidence := domain.CharacterAgentEvidenceBundle{GenerationID: generation.GenerationID, Chapter: 1, Registry: registry, Stimulus: stimulus, Activation: activation, Observations: observations, Proposals: proposals, Arbitrations: []domain.WorldArbitrationReceipt{arbitration}, ProtocolDigest: protocolDigest}
	protocol := domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolV2Version, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, ProtocolDigest: protocolDigest}
	for _, observation := range observations {
		protocol.ObservationDigests = append(protocol.ObservationDigests, observation.Digest)
		evidence.MemoryRoots = append(evidence.MemoryRoots, observation.MemoryRoot)
	}
	for _, proposal := range proposals {
		protocol.ProposalDigests = append(protocol.ProposalDigests, proposal.Digest)
	}
	evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(evidence)
	if err != nil {
		t.Fatal(err)
	}
	protocol.MemoryRoots = evidence.MemoryRoots
	artifacts.CharacterAgentEvidence = &evidence
	sim := artifacts.WorldSimulation
	sim.Version, sim.PhysicalState, sim.CharacterAgentProtocol = 2, &after, &protocol
	sim.CharacterDecisions, err = arbitration.CharacterDecisions(proposals, after)
	if err != nil {
		t.Fatal(err)
	}
	var render map[string]any
	if err := json.Unmarshal(artifacts.RenderContext, &render); err != nil {
		t.Fatal(err)
	}
	render["chapter_world_simulation"] = sim
	// A producer may accidentally mirror typed world data inside an encoded
	// string. Exercise the actual sealed boundary, not an already-clean input.
	encoded, err := domain.EncodeWorldPhysicalStateV2(after)
	if err != nil {
		t.Fatal(err)
	}
	render["accidental_physical_mirror"] = encoded
	artifacts.RenderContext, err = json.Marshal(render)
	if err != nil {
		t.Fatal(err)
	}
	return artifacts, outline, registry
}

func TestProjectAllPhysicalStateArbitrationFlowsThroughBundleShadowAndNextObservation(t *testing.T) {
	generation, obligations := projectAllCmdTestGenerationAndRegistry(t, 2)
	generation.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	var err error
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline, registry := projectAllPhysicalArtifacts(t, generation)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, obligations, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, obligations, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, obligations)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bundle.RenderContext), "actual_amount") || strings.Contains(string(bundle.RenderContext), "作者私有实量") || strings.Contains(string(bundle.RenderContext), "作者秘密泵量") {
		t.Fatal("sealed render context retained author-only world balance")
	}
	projected, err := domain.DeriveProjectedPlanningContextV2(generation, []domain.ProjectedChapterBundle{bundle}, obligations, 2)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := domain.ProjectedPhysicalStateV2(projected)
	if err != nil || physical == nil {
		t.Fatalf("missing projected physical state: %v", err)
	}
	want := map[string]string{physicalBatteryID: "104", physicalSharedID: "95", physicalPumpID: "11.8", physicalUnknownID: "null"}
	seen := map[string]int{}
	for _, mutation := range bundle.ProjectedDelta.Resources {
		if mutation.Field != domain.WorldResourceActualAmountV2Field {
			t.Fatalf("narrative resource pressure became a balance: %+v", mutation)
		}
		seen[mutation.Object]++
		if mutation.Subject != "world" || mutation.After != want[mutation.Object] {
			t.Fatalf("bad global balance: %+v", mutation)
		}
	}
	for id := range want {
		if seen[id] != 1 {
			t.Fatalf("resource %s settled %d times in delta", id, seen[id])
		}
	}
	for _, resource := range physical.Resources {
		raw, _ := json.Marshal(resource.ActualAmount)
		if string(raw) != want[resource.ResourceID] {
			t.Fatalf("bad projected resource: %+v", resource)
		}
	}
	for _, proposal := range artifacts.CharacterAgentEvidence.Proposals {
		if proposal.Location != "A" {
			t.Fatal("arbitration changed original intention origin")
		}
	}
	for _, initial := range bundle.FormalWorldSimulation.InitialConditions {
		if initial.Field == "location" && initial.Value != "A" {
			t.Fatalf("post location relabeled as initial: %+v", initial)
		}
	}
	shadow := store.NewStore(t.TempDir())
	if err := shadow.Init(); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Progress.Init("physical-projection-test", 2); err != nil {
		t.Fatal(err)
	}
	if err := shadow.CharacterAgents.SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Characters.Save([]domain.Character{{Name: "主角", Role: "protagonist", Tier: "core"}, {Name: "同伴", Role: "配角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Arcs: []domain.ArcOutline{{Index: 1, Chapters: []domain.OutlineEntry{outline, {Chapter: 2, Title: "下一章", CoreEvent: "主角与同伴回应后果"}}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := advancePipelineProjectAllWorkspace(shadow, generation.GenerationID, 1, artifacts.WorldSimulation, artifacts.Plan, bundle.ProjectedDelta); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(shadow.Dir(), pipelineProjectedPhysicalStatePath))
	if err != nil {
		t.Fatal(err)
	}
	var receipt pipelineProjectedPhysicalStateReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.ComputeWorldPhysicalStateV2Digest(*physical)
	if receipt.StateDigest != digest || receipt.GenerationID != generation.GenerationID || receipt.Chapter != 1 {
		t.Fatalf("bad shadow receipt identity: %+v", receipt)
	}
	changes, err := shadow.World.LoadStateChanges()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if change.Field == "location" && change.NewValue != "B" {
			t.Fatalf("shadow location reverted to origin: %+v", change)
		}
		if change.Field == "status" || change.Field == "knowledge" {
			t.Fatalf("free-form author truth entered actor ledger: %+v", change)
		}
	}
	worldDelta, err := shadow.LoadChapterWorldDelta(1)
	if err != nil {
		t.Fatal(err)
	}
	worldRaw, _ := json.Marshal(worldDelta)
	if strings.Contains(string(worldRaw), "作者私有实量") {
		t.Fatal("free-form result became public/actor world delta")
	}
	for _, change := range worldDelta.WorldDeltas {
		if change.Kind == "resource_pending" {
			t.Fatalf("physical actual amount relabeled as pending pressure: %+v", change)
		}
		if (change.Kind == "resource_actual" || change.Kind == "physical_state") && change.VisibleToProtagonist {
			t.Fatalf("server physical state was published as visible: %+v", change)
		}
	}
	ledger, err := shadow.ResourceLedger.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ledger != nil && len(ledger.Claims) != 0 {
		t.Fatalf("typed actual balance contaminated legacy pending ledger: %+v", ledger)
	}
	beforeRoot, _ := store.DirectoryContentRoot(shadow.Dir())
	observations, err := agents.BuildCharacterObservationsForProjectedState(shadow, generation.GenerationID, 2, projected)
	if err != nil {
		t.Fatal(err)
	}
	afterRoot, _ := store.DirectoryContentRoot(shadow.Dir())
	if beforeRoot != afterRoot {
		t.Fatal("read-only next observation builder mutated shadow evidence")
	}
	if len(observations) != 2 {
		t.Fatalf("missing actor observations: %+v", observations)
	}
	for _, observation := range observations {
		if observation.Location != "B" {
			t.Fatalf("C2 actor did not move A→B: %+v", observation)
		}
		raw, _ := json.Marshal(observation)
		raw = testutil.CharacterObservationPrivacyJSON(t, raw)
		if strings.Contains(string(raw), "11.8") || strings.Contains(string(raw), "作者秘密泵量") || strings.Contains(string(raw), "actual_amount") {
			t.Fatalf("actual truth leaked into C2 observation: %s", raw)
		}
		for _, view := range observation.ResourceViews {
			if view.ResourceID == physicalPumpID && (view.Perception.Amount == nil || *view.Perception.Amount != 12 || view.Perception.AsOfChapter != 0) {
				t.Fatalf("unmeasured pump memory changed: %+v", view)
			}
		}
	}
}

func TestProjectAllPhysicalStateRejectsTamperedShadowBeforeWrites(t *testing.T) {
	generation, _ := projectAllCmdTestGenerationAndRegistry(t, 2)
	artifacts, _, _ := projectAllPhysicalArtifacts(t, generation)
	delta, err := pipelineProjectAllDelta(1, *artifacts.WorldSimulation, *artifacts.Plan, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"amount", "duplicate", "missing"} {
		t.Run(mode, func(t *testing.T) {
			corrupt := delta
			corrupt.Resources = append([]domain.StateMutationV2(nil), delta.Resources...)
			switch mode {
			case "amount":
				corrupt.Resources[0].After = "999"
			case "duplicate":
				corrupt.Resources = append(corrupt.Resources, corrupt.Resources[0])
			case "missing":
				corrupt.CharacterState = nil
			}
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			before, _ := store.DirectoryContentRoot(st.Dir())
			if err := advancePipelineProjectAllWorkspace(st, generation.GenerationID, 1, artifacts.WorldSimulation, artifacts.Plan, corrupt); err == nil {
				t.Fatal("tampered physical delta accepted")
			}
			after, _ := store.DirectoryContentRoot(st.Dir())
			if before != after {
				t.Fatal("rejected physical delta changed shadow files")
			}
		})
	}
}

func TestProjectAllPhysicalStateIsNeverAnObservableSubstringOrNewGenerationSeed(t *testing.T) {
	simulation := &domain.ChapterWorldSimulation{ProtagonistProjection: domain.ProtagonistDecisionProjection{Protagonist: "主角", ObservableEffects: []string{"仪表编号11.8"}}}
	for _, field := range []string{domain.WorldPhysicalStateV2Field, domain.WorldResourceActualAmountV2Field} {
		if pipelineProjectAllWorldMutationVisibleToProtagonist("resource", domain.StateMutationV2{Subject: "主角", Field: field, After: "11.8"}, simulation) {
			t.Fatalf("typed server state marked visible: %s", field)
		}
	}
	for _, kind := range []string{"physical_state", "resource_actual"} {
		if pipelineProjectAllWorldMutationVisibleToProtagonist(kind, domain.StateMutationV2{Subject: "主角", After: "11.8"}, simulation) {
			t.Fatalf("resume visibility lost typed boundary: %s", kind)
		}
	}
	if !projectAllWorkspaceExcluded(pipelineProjectedPhysicalStatePath, false) {
		t.Fatal("old generation physical receipt copied into a new shadow")
	}
}

func TestProjectAllPhysicalLegacyDeltaRetainsHistoricalStateAndPendingPressure(t *testing.T) {
	artifacts, _ := projectAllCmdTestArtifacts(t, "pg2_legacy", 1)
	artifacts.Plan.CausalSimulation.StructuralResources = []domain.StructuralResourcePressure{{Resource: "准入资格", Controller: "守门人", ChapterPressure: "错过时限"}}
	delta, err := pipelineProjectAllDelta(1, *artifacts.WorldSimulation, *artifacts.Plan, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.CharacterState) != 1 || delta.CharacterState[0].After != artifacts.WorldSimulation.CharacterDecisions[0].StateAfter || len(delta.Knowledge) != 1 {
		t.Fatalf("legacy free-form evidence was rewritten: %+v", delta)
	}
	if len(delta.Resources) != 1 || delta.Resources[0].After != "错过时限" || delta.Resources[0].Field == domain.WorldResourceActualAmountV2Field {
		t.Fatalf("legacy resource interpretation changed: %+v", delta.Resources)
	}
	if len(delta.Locations) != 1 || delta.Locations[0].After != artifacts.WorldSimulation.CharacterDecisions[0].Location {
		t.Fatal("legacy location interpretation changed")
	}
}
