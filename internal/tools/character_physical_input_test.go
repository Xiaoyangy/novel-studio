package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func physicalInputFixture() (domain.WorldStimulusPacket, []domain.CharacterDecisionProposal) {
	quantity := 12.0
	state := domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version,
		Resources: []domain.WorldResourceBalanceV2{{ResourceID: "res_0000000000000001", Name: "author only", Unit: "L", ActualAmount: &quantity}},
		Actors: []domain.CharacterPhysicalStateV2{{AgentID: "actor", Character: "角色", Location: "A", Resources: []domain.CharacterResourceHoldingV2{{
			ResourceID: "res_0000000000000001", PerceivedName: "安全称呼", PerceivedUnit: "L", Access: "shared",
			Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: &quantity, AsOfChapter: 0, EvidenceRefs: []string{"opening:reading"}}, EvidenceRefs: []string{"opening:access"},
		}}}}}
	return domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, PhysicalState: &state}, []domain.CharacterDecisionProposal{{AgentID: "actor", Character: "角色"}}
}

func TestPhysicalPostStateSparseUpdatesPreserveUnchangedAuthority(t *testing.T) {
	stimulus, proposals := physicalInputFixture()
	before, _ := json.Marshal(stimulus)
	for _, raw := range []string{`{"location":"B"}`, `{"location":"B","resource_updates":[]}`} {
		result, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{{CharacterDecisionResolution: domain.CharacterDecisionResolution{AgentID: "actor"}, PostState: json.RawMessage(raw)}}, stimulus, proposals)
		if err != nil {
			t.Fatal(err)
		}
		post := result[0].PostState
		if post.AgentID != "actor" || post.Character != "角色" || post.Location != "B" || !reflect.DeepEqual(post.Resources, stimulus.PhysicalState.Actors[0].Resources) {
			t.Fatalf("sparse post did not preserve exact baseline: %+v", post)
		}
	}
	after, _ := json.Marshal(stimulus)
	if string(before) != string(after) {
		t.Fatal("merge changed its signed input")
	}
	result, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{{CharacterDecisionResolution: domain.CharacterDecisionResolution{AgentID: "actor"}, PostState: json.RawMessage(`{"location":"B","resource_updates":[{"resource_id":"res_0000000000000001","access":"none"}]}`)}}, stimulus, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].PostState.Resources[0].Access != "none" || !reflect.DeepEqual(result[0].PostState.Resources[0].Perception, stimulus.PhysicalState.Actors[0].Resources[0].Perception) {
		t.Fatal("access update silently changed perception")
	}
}

func TestPhysicalPostStateCompleteEmptyIsDifferentFromEmptyUpdates(t *testing.T) {
	stimulus, proposals := physicalInputFixture()
	result, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{{CharacterDecisionResolution: domain.CharacterDecisionResolution{AgentID: "actor"}, PostState: json.RawMessage(`{"location":"B","resources":[]}`)}}, stimulus, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].PostState.Resources == nil || len(result[0].PostState.Resources) != 0 {
		t.Fatal("explicit complete empty list did not clear holdings")
	}
	for _, raw := range []string{
		`{"location":"B","resources":[],"resource_updates":[]}`,
		`{"location":"B","resources":null}`,
		`{"location":"B","resource_updates":null}`,
		`{"location":"B","agent_id":"forged"}`,
		`{"location":"B","resource_updates":[{"resource_id":"res_9999999999999999","access":"exclusive"}]}`,
		`{"location":"B","resource_updates":[{"resource_id":"res_0000000000000001"},{"resource_id":"res_0000000000000001"}]}`,
	} {
		if _, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{{CharacterDecisionResolution: domain.CharacterDecisionResolution{AgentID: "actor"}, PostState: json.RawMessage(raw)}}, stimulus, proposals); err == nil {
			t.Fatalf("unsafe merge accepted: %s", raw)
		}
	}
}

func TestReceivedFactsMaterializeOnlyExactCommunicationOrDocumentSource(t *testing.T) {
	stimulus, proposals := physicalInputFixture()
	stimulus.GenerationID = "pg2_knowledge"
	stimulus.Chapter = 1
	proposals[0].Digest = "sha256:" + strings.Repeat("a", 64)
	proposals[0].Chapter = 1
	proposals[0].GenerationID = stimulus.GenerationID
	proposals = append(proposals, domain.CharacterDecisionProposal{AgentID: "sender", Character: "发送者", Digest: "sha256:" + strings.Repeat("b", 64), Chapter: 1, GenerationID: stimulus.GenerationID,
		Communications: []domain.CharacterCommunicationV2{{ID: "promise", ToCharacter: "角色", Kind: "commitment", Text: "不再追加签认，并将取出资料"}},
	})
	stimulus.PhysicalState.Resources[0].ReadableFacts = []domain.ResourceReadableFactV2{{ID: "original", Text: "形成时的经营用油记录"}}
	input := arbitrationResolutionInput{CharacterDecisionResolution: domain.CharacterDecisionResolution{AgentID: "actor"}, PostState: json.RawMessage(`{"location":"A","received_facts":[{"source_type":"communication","from_agent_id":"sender","source_id":"promise"},{"source_type":"resource_read","resource_id":"res_0000000000000001","source_id":"original"}]}`)}
	result, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{input}, stimulus, proposals)
	if err != nil {
		t.Fatal(err)
	}
	facts := result[0].PostState.ReceivedFacts
	if len(facts) != 2 || facts[0].Text != "不再追加签认，并将取出资料" || facts[1].Text != "形成时的经营用油记录" || facts[1].Kind != "document_statement" {
		t.Fatalf("source materialization failed: %+v", facts)
	}
	for _, fact := range facts {
		if fact.ID == "" || fact.Chapter != 1 {
			t.Fatalf("unbound received fact: %+v", fact)
		}
	}
	input.PostState = json.RawMessage(`{"location":"A","received_facts":[{"source_type":"resource_read","resource_id":"res_0000000000000001","source_id":"original","text":"作者后来补出的精确11.8"}]}`)
	if _, err := normalizeArbitrationResolutions([]arbitrationResolutionInput{input}, stimulus, proposals); err == nil {
		t.Fatal("world free text replaced original document content")
	}
}
