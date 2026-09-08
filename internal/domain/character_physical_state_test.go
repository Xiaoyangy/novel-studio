package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

const physicalFuelTestID = "res_0000000000000001"
const physicalPaperTestID = "res_0000000000000002"
const physicalInnerTestID = "res_0000000000000003"

func physicalTestNumber(value float64) *float64 { return &value }

type physicalProtocolFixture struct {
	stimulus     WorldStimulusPacket
	activation   CharacterAgentActivation
	observations []CharacterObservationPacket
	proposals    []CharacterDecisionProposal
	receipt      WorldArbitrationReceipt
}

func newPhysicalProtocolFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	state := WorldPhysicalStateV2{Version: WorldPhysicalStateV2Version, Resources: []WorldResourceBalanceV2{
		{ResourceID: physicalFuelTestID, Name: "作者账本真实燃油", Unit: "L", ActualAmount: physicalTestNumber(12)},
		{ResourceID: physicalPaperTestID, Name: "作者知道已改写的登记纸", ReadableFacts: []ResourceReadableFactV2{{ID: "printed", Text: "登记纸上标示九十升。"}}},
		{ResourceID: physicalInnerTestID, Name: "封袋内私人账本", ReadableFacts: []ResourceReadableFactV2{{ID: "inner", Text: "内页写着另一笔支出。"}}, AccessRequiresAny: []string{"open_seal"}},
	}, Actors: []CharacterPhysicalStateV2{
		{AgentID: "ca_a", Character: "甲", Location: "仓库", Resources: []CharacterResourceHoldingV2{
			{ResourceID: physicalFuelTestID, PerceivedName: "燃油", PerceivedUnit: "L", Access: "shared", Perception: ResourcePerceptionV2{Kind: "last_observed", Amount: physicalTestNumber(12), EvidenceRefs: []string{"seed-a"}}, EvidenceRefs: []string{"seed-a"}},
			{ResourceID: physicalPaperTestID, PerceivedName: "登记纸", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"seed-a"}},
			{ResourceID: physicalInnerTestID, Access: "none", Perception: ResourcePerceptionV2{Kind: "unaware"}, EvidenceRefs: []string{"seed-a"}},
		}},
		{AgentID: "ca_b", Character: "乙", Location: "门口", Resources: []CharacterResourceHoldingV2{
			{ResourceID: physicalFuelTestID, PerceivedName: "燃油", PerceivedUnit: "L", Access: "shared", Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"seed-b"}},
			{ResourceID: physicalPaperTestID, PerceivedName: "登记纸", Access: "none", Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"seed-b"}},
			{ResourceID: physicalInnerTestID, Access: "none", Perception: ResourcePerceptionV2{Kind: "unaware"}, EvidenceRefs: []string{"seed-b"}},
		}},
	}}
	var err error
	state, err = FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	f := physicalProtocolFixture{}
	f.stimulus, err = FinalizeWorldStimulusPacket(WorldStimulusPacket{Version: WorldStimulusPacketV2Version, GenerationID: "pg2_physical", Chapter: 1, TimeWindow: "同一分钟", PhysicalState: &state, Mechanisms: []CodexMechanism{{ID: "gauge", Name: "读表", Visibility: "formal"}, {ID: "open_seal", Name: "拆封", Visibility: "formal"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.activation = CharacterAgentActivation{Version: CharacterAgentActivationVersion, GenerationID: f.stimulus.GenerationID, Chapter: 1, RegistryRoot: "sha256:registry"}
	for _, actor := range state.Actors {
		views, err := BuildCharacterResourceViewsV2(state, actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{Version: CharacterObservationV2Version, GenerationID: f.stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character, Location: actor.Location, CurrentGoal: "保住物资", Pressure: "时间紧迫", StimulusDigest: f.stimulus.Digest, ResourceViews: views, KnownFacts: []CharacterAgentFact{{ID: "known-" + actor.AgentID, Kind: "known", Text: "我知道自己的处境"}}, PublicMechanisms: f.stimulus.Mechanisms})
		if err != nil {
			t.Fatal(err)
		}
		f.observations = append(f.observations, observation)
		f.activation.Entries = append(f.activation.Entries, CharacterAgentActivationEntry{AgentID: actor.AgentID, Character: actor.Character, State: CharacterAgentActive, Reasons: []string{"active"}, ObservationDigest: observation.Digest})
		f.proposals = append(f.proposals, CharacterDecisionProposal{Version: CharacterDecisionProposalVersion, GenerationID: f.stimulus.GenerationID, Chapter: 1, Round: 1, AgentID: actor.AgentID, Character: actor.Character, ObservationDigest: observation.Digest, Location: actor.Location, CurrentGoal: "保住物资", Pressure: "时间紧迫", Resources: []string{"旧提案余额110"}, AvailableOptions: []string{"行动", "等待"}, Decision: "行动", DecisionReason: "先保住物资", IntendedAction: "回应眼前压力", ActionDuration: "一分钟", KnowledgeRefs: []string{"known-" + actor.AgentID}})
		copy := actor
		f.receipt.Resolutions = append(f.receipt.Resolutions, CharacterDecisionResolution{AgentID: actor.AgentID, Character: actor.Character, Decision: "行动", IntendedAction: "回应眼前压力", ActionOrder: len(f.proposals), Outcome: "success", CompletionState: "completed", ImmediateResult: "执行了选择", StateAfter: "秘密真值只属于裁判", PostState: &copy, ButterflyEffects: []DecisionButterflyEffect{{Effect: "物资状态变化", TransmissionPath: "现场", ArrivalChapter: 1, ProtagonistImpact: "继续权衡"}}})
	}
	f.activation, err = FinalizeCharacterAgentActivation(f.activation)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.Version = WorldArbitrationReceiptV2Version
	f.receipt.GenerationID = f.stimulus.GenerationID
	f.receipt.Chapter = 1
	f.receipt.Round = 1
	f.receipt.StimulusDigest = f.stimulus.Digest
	f.receipt.ActivationDigest = f.activation.Digest
	f.receipt.HardContractStatus = "feasible"
	f.receipt.Finalized = true
	f.receipt.ProtagonistProjection = ProtagonistDecisionProjection{Protagonist: "甲", ChosenDecision: "行动", DecisionReason: "先保住物资", AvailableOptions: []string{"行动", "等待"}, PlanConstraints: []string{"保持角色选择"}, CausalChain: []string{"压力导致选择"}}
	rebindPhysicalTestProposals(t, &f)
	return f
}

func rebindPhysicalTestProposals(t *testing.T, f *physicalProtocolFixture) {
	t.Helper()
	f.receipt.ProposalDigests = nil
	f.receipt.Digest = ""
	for i := range f.proposals {
		p, err := FinalizeCharacterDecisionProposal(f.proposals[i], f.observations[i])
		if err != nil {
			t.Fatal(err)
		}
		f.proposals[i] = p
		f.receipt.ProposalDigests = append(f.receipt.ProposalDigests, p.Digest)
		f.receipt.Resolutions[i].ProposalDigest = p.Digest
	}
}

func rebindPhysicalTestStimulus(t *testing.T, f *physicalProtocolFixture) {
	t.Helper()
	var err error
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.StimulusDigest = f.stimulus.Digest
	for i := range f.observations {
		f.observations[i].StimulusDigest = f.stimulus.Digest
		f.observations[i].ResourceViews, err = BuildCharacterResourceViewsV2(*f.stimulus.PhysicalState, f.observations[i].AgentID)
		if err != nil {
			t.Fatal(err)
		}
		f.observations[i], err = FinalizeCharacterObservationPacket(f.observations[i])
		if err != nil {
			t.Fatal(err)
		}
		f.proposals[i].ObservationDigest = f.observations[i].Digest
		f.activation.Entries[i].ObservationDigest = f.observations[i].Digest
	}
	f.activation, err = FinalizeCharacterAgentActivation(f.activation)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.ActivationDigest = f.activation.Digest
	rebindPhysicalTestProposals(t, f)
}
func settlePhysicalFuel(f *physicalProtocolFixture) {
	f.receipt.ResourceSettlements = []ResourceSettlementV2{{ResourceID: physicalFuelTestID, Before: physicalTestNumber(12), Delta: physicalTestNumber(-.2), After: physicalTestNumber(11.8), EvidenceRefs: []string{f.proposals[0].Digest}}}
}
func finalizePhysicalFixture(f physicalProtocolFixture) (WorldArbitrationReceipt, error) {
	return FinalizeWorldArbitrationReceipt(f.receipt, f.stimulus, f.activation, f.proposals, 1)
}

func TestPhysicalStateV2SettlesSharedResourceOnceWithoutInventingKnowledge(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	settlePhysicalFuel(&f)
	f.receipt.Resolutions[0].PostState.Location = "门口"
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if *after.Resources[0].ActualAmount != 11.8 || *f.stimulus.PhysicalState.Resources[0].ActualAmount != 12 {
		t.Fatal("world balance doubled or mutated stimulus")
	}
	if *after.Actors[0].Resources[0].Perception.Amount != 12 || after.Actors[1].Resources[0].Perception.Amount != nil {
		t.Fatal("settlement automatically upgraded private perception")
	}
	decisions, err := r.CharacterDecisions(f.proposals, after)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Location != "门口" || f.proposals[0].Location != "仓库" || strings.Contains(strings.Join(decisions[0].Resources, " "), "110") {
		t.Fatalf("pre-action location/resources survived as post-state: %+v", decisions[0])
	}
}

func TestPhysicalStateV2RejectsNonconservingAndUnknownSettlements(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*physicalProtocolFixture)
	}{
		{"twice", func(f *physicalProtocolFixture) {
			f.receipt.ResourceSettlements = append(f.receipt.ResourceSettlements, f.receipt.ResourceSettlements[0])
		}},
		{"wrong before", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].Before = physicalTestNumber(11) }},
		{"wrong after", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].After = physicalTestNumber(11) }},
		{"negative after", func(f *physicalProtocolFixture) { f.receipt.ResourceSettlements[0].After = physicalTestNumber(-1) }},
		{"nan delta", func(f *physicalProtocolFixture) {
			f.receipt.ResourceSettlements[0].Delta = physicalTestNumber(math.NaN())
		}},
		{"unknown true amount", func(f *physicalProtocolFixture) { f.stimulus.PhysicalState.Resources[0].ActualAmount = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPhysicalProtocolFixture(t)
			settlePhysicalFuel(&f)
			tc.edit(&f)
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("invalid settlement accepted")
			}
		})
	}
	f := newPhysicalProtocolFixture(t)
	f.stimulus.PhysicalState.Resources[0].ActualAmount = nil
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.ResourceSettlements = []ResourceSettlementV2{{ResourceID: physicalFuelTestID, Delta: physicalTestNumber(-.2), EvidenceRefs: []string{f.proposals[0].Digest}}}
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil || after.Resources[0].ActualAmount != nil {
		t.Fatal("unknown remaining quantity became fabricated")
	}
}

func TestPhysicalStateV2EstimatesAndMeasurementsRequireIndependentActorIntent(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	settlePhysicalFuel(&f)
	f.receipt.Resolutions[1].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "estimated", EstimateMin: physicalTestNumber(11.8), EstimateMax: physicalTestNumber(11.8), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[1].Digest}}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("arbiter disguised true balance as its own estimate")
	}
	f = newPhysicalProtocolFixture(t)
	f.proposals[1].ResourceEstimates = []ResourceEstimateV2{{ResourceID: physicalFuelTestID, EstimateMin: physicalTestNumber(8), EstimateMax: physicalTestNumber(9), EvidenceRefs: CharacterSourceRefsV2("ca_b", []string{"seed-b"})}}
	rebindPhysicalTestProposals(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.Resolutions[1].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "estimated", EstimateMin: physicalTestNumber(8), EstimateMax: physicalTestNumber(9), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[1].Digest, "seed-b"}}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatalf("an evidenced but wrong estimate was rejected: %v", err)
	}
	f = newPhysicalProtocolFixture(t)
	f.proposals[1].ResourceMeasurements = []ResourceMeasurementV2{{ResourceID: physicalFuelTestID, MechanismRef: "gauge"}}
	f.proposals[1].MechanismRefs = []string{"gauge"}
	rebindPhysicalTestProposals(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.Resolutions[1].MechanismRefs = []string{"gauge"}
	f.receipt.Resolutions[1].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "last_observed", Amount: physicalTestNumber(11.8), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[1].Digest}}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatalf("requested/applied measurement rejected: %v", err)
	}
	f.receipt.Resolutions[1].MechanismRefs = nil
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("unapplied measurement revealed true balance")
	}
}

func TestPhysicalStateV2ReportedAmountRequiresActualDeliveryAndNeverBecomesTruth(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.proposals[0].ResourceReports = []ResourceReportV2{{ResourceID: physicalFuelTestID, ToCharacter: "乙", PerceivedUnit: "L", Amount: physicalTestNumber(90), EvidenceRefs: CharacterSourceRefsV2("ca_a", []string{"seed-a"})}}
	rebindPhysicalTestProposals(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.Resolutions[1].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "reported", Amount: physicalTestNumber(90), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[0].Digest}}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("report proposal was mistaken for delivered information")
	}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalFuelTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, ReceivedFields: []string{"amount"}, Access: "none", EvidenceRefs: []string{f.proposals[0].Digest, CharacterSourceRefV2("ca_a", "seed-a")}}}
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if *after.Resources[0].ActualAmount != 11.8 || *after.Actors[1].Resources[0].Perception.Amount != 90 {
		t.Fatal("unverified report altered truth or disappeared")
	}
	f.receipt.Resolutions[1].PostState.Resources[0].Perception.Kind = "last_observed"
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("received report became personal observation")
	}
}

func TestPhysicalStateV2TransfersRightsWithoutCloningOrPretendingPromiseIsDelivery(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.receipt.Resolutions[0].PostState.Resources = append(f.receipt.Resolutions[0].PostState.Resources[:1], f.receipt.Resolutions[0].PostState.Resources[2:]...)
	f.receipt.Resolutions[1].PostState.Resources[1].Access = "exclusive"
	f.receipt.Resolutions[1].PostState.Resources[1].EvidenceRefs = []string{f.proposals[0].Digest}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("new access without actual delivery accepted")
	}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalPaperTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, Access: "exclusive", ReceivedFields: []string{}, EvidenceRefs: []string{f.proposals[0].Digest}}}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatal(err)
	}
	f.receipt.Resolutions[0].PostState.Resources = append(f.receipt.Resolutions[0].PostState.Resources, CharacterResourceHoldingV2{ResourceID: physicalPaperTestID, PerceivedName: "登记纸", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown"}})
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("exclusive holding was cloned")
	}
}

func TestPhysicalStateV2ConditionalReadingNeedsTheActuallyDeliveredExactResource(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.proposals[0].Communications = []CharacterCommunicationV2{{ID: "promise", ToCharacter: "乙", Kind: "commitment", Text: "我会去取登记纸。", KnowledgeRefs: []string{"known-ca_a"}}}
	f.proposals[1].ResourceReads = []ResourceReadRequestV2{{IncomingDeliveryFrom: "甲"}}
	rebindPhysicalTestProposals(t, &f)
	promise := CharacterReceivedFactV2{Kind: "commitment", Text: "我会去取登记纸。", SourceType: "communication", SourceID: "promise", SourceProposalDigest: f.proposals[0].Digest, FromAgentID: "ca_a", Chapter: 1}
	read := CharacterReceivedFactV2{Kind: "document_statement", Text: "登记纸上标示九十升。", SourceType: "resource_read", SourceID: "printed", ResourceID: physicalPaperTestID, SourceProposalDigest: f.proposals[1].Digest, Chapter: 1}
	f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{promise}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatal(err)
	}
	f.receipt.Resolutions[1].PostState.ReceivedFacts = append(f.receipt.Resolutions[1].PostState.ReceivedFacts, read)
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("promise granted access to document contents")
	}
	f.receipt.Resolutions[0].PostState.Resources = append(f.receipt.Resolutions[0].PostState.Resources[:1], f.receipt.Resolutions[0].PostState.Resources[2:]...)
	f.receipt.Resolutions[1].PostState.Resources[1].Access = "exclusive"
	f.receipt.Resolutions[1].PostState.Resources[1].EvidenceRefs = []string{f.proposals[0].Digest}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalPaperTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, Access: "exclusive", ReceivedFields: []string{}, EvidenceRefs: []string{f.proposals[0].Digest}}}
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil || len(after.Actors[1].ReceivedFacts) != 2 {
		t.Fatalf("actual delivered document/promise were not retained: %v", err)
	}
	read.ResourceID = physicalInnerTestID
	read.SourceID = "inner"
	read.Text = "内页写着另一笔支出。"
	f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{promise, read}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("reading outer object scanned sender's private/inner documents")
	}
}

func TestPhysicalStateV2AccessGateAndDescriptorPrivacy(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.receipt.Resolutions[1].PostState.Resources[2].Access = "exclusive"
	f.receipt.Resolutions[1].PostState.Resources[2].Perception.Kind = "unknown"
	f.receipt.Resolutions[1].PostState.Resources[2].EvidenceRefs = []string{f.proposals[0].Digest}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalInnerTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, Access: "exclusive", ReceivedFields: []string{}, EvidenceRefs: []string{f.proposals[0].Digest}}}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("sealed inner resource granted without access mechanism")
	}
	f.receipt.Resolutions[0].MechanismRefs = []string{"open_seal"}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatalf("explicit applied access mechanism rejected: %v", err)
	}
	state := *f.stimulus.PhysicalState
	state.Resources[0].Name = "秘密目录名11.8"
	state.Resources[0].Unit = "秘密量纲"
	state.Actors[1].Resources[0].PerceivedName = ""
	state.Actors[1].Resources[0].PerceivedUnit = ""
	views, err := BuildCharacterResourceViewsV2(state, "ca_b")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(views)
	if strings.Contains(string(raw), "秘密") || strings.Contains(string(raw), "11.8") || strings.Contains(string(raw), "actual_amount") || strings.Contains(string(raw), "readable_facts") {
		t.Fatalf("private catalog leaked into observations: %s", raw)
	}
	if views[0].Name != UnidentifiedResourceNameV2 || views[0].Unit != "" {
		t.Fatal("unknown perceived descriptors were backfilled from truth")
	}
}

func TestPhysicalInitialStateV2MergesOnlyConsistentGlobalDefinitions(t *testing.T) {
	registry, _, err := (CharacterAgentRegistry{}).UpsertCharacter("甲", nil, "core", 1, "now")
	if err != nil {
		t.Fatal(err)
	}
	registry, _, err = registry.UpsertCharacter("乙", nil, "core", 1, "now")
	if err != nil {
		t.Fatal(err)
	}
	entry := InitialCharacterResourceV2{ResourceID: physicalFuelTestID, Name: "燃油", Unit: "L", ActualAmount: physicalTestNumber(12), Access: "shared", Perception: ResourcePerceptionV2{Kind: "unknown"}}
	characters := []Character{{Name: "甲", InitialState: &CharacterInitialState{Location: "仓库", Resources: []string{"旧文本有60升但不解析"}, ResourceBalances: []InitialCharacterResourceV2{entry}}}, {Name: "乙", InitialState: &CharacterInitialState{Location: "门口", ResourceBalances: []InitialCharacterResourceV2{entry}}}}
	state, err := BuildWorldPhysicalStateFromInitialV2(characters, registry)
	if err != nil || len(state.Resources) != 1 {
		t.Fatalf("shared resource was copied per actor: %+v %v", state, err)
	}
	characters[1].InitialState.ResourceBalances[0].Unit = "ml"
	if _, err := BuildWorldPhysicalStateFromInitialV2(characters, registry); err == nil {
		t.Fatal("conflicting shared unit was first-wins")
	}
	characters[0].InitialState.ResourceBalances = nil
	characters[1].InitialState.ResourceBalances = nil
	state, err = BuildWorldPhysicalStateFromInitialV2(characters, registry)
	if err != nil || len(state.Resources) != 0 {
		t.Fatal("legacy descriptive resources were parsed into quantities")
	}
	entry.ResourceID = "fuel-secret-11.8"
	characters[0].InitialState.ResourceBalances = []InitialCharacterResourceV2{entry}
	if _, err := BuildWorldPhysicalStateFromInitialV2(characters, registry); err == nil {
		t.Fatal("nonopaque resource id accepted")
	}
}

func TestPhysicalStateV2NameOnlyDeliveryDoesNotGrantContentsOrAmount(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.stimulus.PhysicalState.Actors[1].Resources[1].PerceivedName = ""
	f.proposals[0].ResourceReports = []ResourceReportV2{{ResourceID: physicalPaperTestID, ToCharacter: "乙", PerceivedName: "登记纸", EvidenceRefs: CharacterSourceRefsV2("ca_a", []string{"seed-a"})}}
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.Resolutions[1].PostState.Resources[1].PerceivedName = "登记纸"
	f.receipt.Resolutions[1].PostState.Resources[1].EvidenceRefs = []string{f.proposals[0].Digest}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("sender naming proposal was not a delivered name")
	}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalPaperTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, ReceivedFields: []string{"name"}, Access: "none", EvidenceRefs: []string{f.proposals[0].Digest}}}
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	got := after.Actors[1].Resources[1]
	if got.PerceivedName != "登记纸" || got.Access != "none" || got.Perception.Amount != nil || len(after.Actors[1].ReceivedFacts) != 0 {
		t.Fatalf("name delivery became access/read/quantity: %+v", got)
	}
}

func TestPhysicalStateV2ConditionalCommunicationNeedsReceivedTriggerAndPreservesFacts(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.proposals[1].Communications = []CharacterCommunicationV2{{ID: "request", ToCharacter: "甲", Kind: "request", Text: "请求查阅登记纸。", KnowledgeRefs: []string{"known-ca_b"}}}
	f.proposals[0].Communications = []CharacterCommunicationV2{{ID: "answer", ToCharacter: "乙", Kind: "conditional_response", Text: "回应对方：愿意去取登记纸。", KnowledgeRefs: []string{"known-ca_a"}, ConditionFromCharacter: "乙", ConditionKind: "request"}}
	rebindPhysicalTestProposals(t, &f)
	f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{{Kind: "conditional_response", Text: f.proposals[0].Communications[0].Text, SourceType: "communication", SourceID: "answer", SourceProposalDigest: f.proposals[0].Digest, FromAgentID: "ca_a", Chapter: 1}}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("conditional response fired without an actually received request")
	}
	f.receipt.Resolutions[0].PostState.ReceivedFacts = []CharacterReceivedFactV2{{Kind: "request", Text: f.proposals[1].Communications[0].Text, SourceType: "communication", SourceID: "request", SourceProposalDigest: f.proposals[1].Digest, FromAgentID: "ca_b", Chapter: 1}}
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Actors[0].ReceivedFacts) != 1 || len(after.Actors[1].ReceivedFacts) != 1 {
		t.Fatal("actual conversational turn was not recorded")
	}
	changed, err := FinalizeWorldPhysicalStateV2(after)
	if err != nil {
		t.Fatal(err)
	}
	changed.Actors[1].ReceivedFacts = nil
	if err := validateReceivedFactsTransitionV2(WorldArbitrationReceipt{Chapter: 2}, after, changed, nil, nil); err == nil {
		t.Fatal("later post_state erased an established conversation")
	}
}

func TestPhysicalStateV2RenderBoundaryRejectsStructuredTruthButNotSameNumberBeliefs(t *testing.T) {
	for _, value := range []any{
		map[string]any{"kind": "estimated", "amount": 11.8, "name": "燃油", "unit": "L"},
		map[string]any{"kind": "reported", "amount": 11.8, "source": "别人的陈述"},
	} {
		if planningPhysicalTruthV2(value, 0) {
			t.Fatal("legitimate perceived number was treated as secret by value")
		}
	}
	f := newPhysicalProtocolFixture(t)
	encoded, err := EncodeWorldPhysicalStateV2(*f.stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{encoded, map[string]any{"field": WorldPhysicalStateV2Field, "subject": "world", "after": encoded}, `{"readable_facts":[{"id":"private","text":"内页秘密"}]}`, `{"resource_settlements":[{"resource_id":"res_0000000000000001","after":11.8}]}`, `{"passive_receptions":[{"to_agent_id":"private-recipient","communication_id":"private-message"}]}`} {
		if !planningPhysicalTruthV2(value, 0) {
			t.Fatalf("encoded/structured private state escaped: %v", value)
		}
		payload := map[string]any{"mirror": value, "own_estimate": map[string]any{"kind": "estimated", "amount": 11.8}}
		_, _ = stripPlanningPhysicalTruthV2(payload)
		if planningPhysicalTruthV2(payload, 0) || payload["own_estimate"] == nil {
			t.Fatal("stripping leaked state or erased lawful estimate")
		}
	}
}
