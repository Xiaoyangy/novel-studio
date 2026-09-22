package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLocationMetadataSourceExplicitOptIn(t *testing.T) {
	const source = `{"name":"伤者","role":"配角","initial_state":{"location":"作者独有的医院正式名称","location_name_known":false,"current_goal":"弄清处境","pressure":"行动受限","known_facts":["床边有监测声"],"resources":[],"relationships":[],"commitments":[]}}`
	var character Character
	if err := json.Unmarshal([]byte(source), &character); err != nil {
		t.Fatal(err)
	}
	registry, _, err := (CharacterAgentRegistry{Version: CharacterAgentRegistryVersion}).UpsertCharacter("伤者", nil, "important", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	state, err := BuildWorldPhysicalStateFromInitialV2([]Character{character}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if state.Actors[0].Location != "作者独有的医院正式名称" {
		t.Fatal("physical location changed")
	}
	raw, _ := json.Marshal(state.Actors[0])
	if !strings.Contains(string(raw), `"host_location_metadata_policy":"character-host-location-metadata:opaque.v1"`) {
		t.Fatalf("explicit unknown opening name has no replayable host projection policy: %s", raw)
	}
}

func TestLocationMetadataAcceptedOutcomeDoesNotInventPlaceNames(t *testing.T) {
	f := newSelfExperienceFixture(t)
	f.stimulus.PhysicalState.Actors[0].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterHostLocationMetadataPolicyV1)
	f.receipt.Resolutions[0].PostState.HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	rebindPhysicalTestStimulus(t, &f)
	settlePhysicalFuel(&f)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(receipt)
	text, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], state, receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"停泊的渡船", "仓库"} {
		if strings.Contains(text, location) {
			t.Errorf("host location name %q leaked into owner memory: %s", location, text)
		}
	}
	for _, keep := range []string{"累计有效工时1分钟", "本人记录留在当前安全记录夹", "携带常规手工具前往渡船"} {
		if !strings.Contains(text, keep) {
			t.Errorf("lost own intention or exact progress %q", keep)
		}
	}
	after, _ := json.Marshal(receipt)
	if string(before) != string(after) || state.Actors[0].Location != "停泊的渡船" {
		t.Fatal("safe formatter changed canonical receipt/location")
	}
}

func TestLocationMetadataPolicyCannotBeGrantedOrRewrittenByArbiter(t *testing.T) {
	for _, mode := range []string{"grant", "rewrite", "omit", "legacy-stimulus"} {
		t.Run(mode, func(t *testing.T) {
			f := newPhysicalProtocolFixture(t)
			f.stimulus.Sources = append(f.stimulus.Sources, CharacterHostLocationMetadataPolicyV1)
			if mode != "grant" {
				f.stimulus.PhysicalState.Actors[0].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
			}
			if mode == "legacy-stimulus" {
				f.stimulus.Sources = nil
				if _, err := FinalizeWorldStimulusPacket(f.stimulus); err == nil {
					t.Fatal("legacy stimulus accepted a new source-only location policy")
				}
				return
			}
			rebindPhysicalTestStimulus(t, &f)
			if mode == "grant" {
				f.receipt.Resolutions[0].PostState.HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
			}
			if mode == "rewrite" {
				f.receipt.Resolutions[0].PostState.HostLocationMetadataPolicy = "model-selected"
			}
			r, err := finalizePhysicalFixture(f)
			if mode != "omit" {
				if err == nil {
					t.Fatal("arbiter changed source-selected location policy")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Resolutions[0].PostState.HostLocationMetadataPolicy != CharacterHostLocationMetadataPolicyV1 {
				t.Fatal("omitting host field erased the source-selected policy")
			}
			state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
			if err != nil {
				t.Fatal(err)
			}
			text, err := CharacterPrivateOutcomeV2(f.proposals[0], r.Resolutions[0], state, r)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, "位置：仓库") {
				t.Fatal("normalized receipt with omitted host policy leaked into private memory")
			}
		})
	}
}

func TestLocationMetadataArtifactMemoryKeepsActorClaims(t *testing.T) {
	f := artifactFlowFixtureV1(t)
	f.stimulus.PhysicalState.Actors[0].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterHostLocationMetadataPolicyV1)
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterHostLocationMetadataPolicyV1)
	}
	f.observations[0].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	f.receipt.Resolutions[0].PostState.HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	context := *f.stimulus.SelfEvaluationContext
	context.BeforePhysicalRoot, _ = CharacterPhysicalRootForCycle(*f.stimulus.PhysicalState)
	context.Digest, _ = selfEvaluationContextDigestV1(context)
	f.stimulus.SelfEvaluationContext = &context
	const claim = "本人记录：此前听到的名称是仓库；这不是独立证实。"
	f.proposals[0].SelfTasks[0].OutputRequests[0].Claims[0].Text = claim
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{f.proposals[0].Digest}
	receipt, state := artifactFlowApplyV1(t, f, map[string]string{})
	before, _ := json.Marshal(state)
	text, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], state, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "当时位置仓库") || strings.Contains(text, "位置：仓库") {
		t.Fatal("artifact formatter injected canonical location")
	}
	if !strings.Contains(text, claim) {
		t.Fatal("legitimate actor-authored name-bearing claim was rewritten")
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("formatting rewrote canonical artifact knowledge")
	}
}

func TestLocationMetadataAcceptedCommunicationKeepsNameAndSource(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.stimulus.PhysicalState.Actors[1].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterHostLocationMetadataPolicyV1)
	f.observations[1].HostLocationMetadataPolicy = CharacterHostLocationMetadataPolicyV1
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterHostLocationMetadataPolicyV1)
	}
	const text = "我告诉你：这里称作门口；这是我的说法。"
	f.proposals[0].Communications = []CharacterCommunicationV2{{ID: "real-name-report", ToCharacter: "乙", Kind: "information", Text: text, KnowledgeRefs: f.proposals[0].KnowledgeRefs}}
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{{Kind: "information", Text: text, SourceType: "communication", SourceID: "real-name-report", SourceProposalDigest: f.proposals[0].Digest, FromAgentID: "ca_a", Chapter: 1}}
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	private, err := CharacterPrivateOutcomeV2(f.proposals[1], receipt.Resolutions[1], state, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(private, "收到的information："+text) || strings.Contains(private, "位置：门口") {
		t.Fatal("safe metadata formatting lost a legitimate received name or injected a separate host label")
	}
	fact := state.Actors[1].ReceivedFacts[0]
	if fact.Text != text || fact.SourceProposalDigest != f.proposals[0].Digest || fact.SourceID != "real-name-report" {
		t.Fatal("legitimate communication provenance changed")
	}
}
