package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCharacterInitialStateIsIndependentAndDoesNotResetLaterResources(t *testing.T) {
	st := store.NewStore(t.TempDir())
	profile := characterAgentProfile{
		Character: domain.Character{Name: "许岚", Role: "仓库经营者", InitialState: &domain.CharacterInitialState{
			Time: "T+0", Location: "油料仓库", CurrentGoal: "维持排水", CurrentAction: "检查排水机", Pressure: "水即将漫过存货",
			KnownFacts: []string{"我曾调出三十升储备油用于排水"}, Resources: []string{"备用油六十升"},
			Relationships: []string{"与林澄有账目争议"}, Commitments: []string{"按约记录经营用油"},
		}},
		Record: domain.CharacterAgentRecord{AgentID: "ca_xu", Character: "许岚"},
		Dossier: &domain.CharacterDossier{CurrentAtStoryStart: domain.CharacterStartState{
			Location: "错误统一起点", Pressure: "FirstChapter.CoreEvent 作者情节", NextIndependentMove: "提前完成取证",
		}},
		Continuity: &domain.CharacterContinuityEntry{Dynamics: domain.CharacterDynamicsProfile{
			CurrentGoal: "旧零章把大纲原文当共同目标", Resources: []string{"旧零章抽象资源"},
		}},
		Agenda: &domain.CharacterAgenda{CurrentGoal: "旧零章按第一章大纲行动"},
	}
	first, err := buildCharacterObservation(st, "pg2_initial_state", 1, profile, domain.WorldStimulusPacket{}, domain.ProjectedPlanningContextV2{}, "now")
	if err != nil {
		t.Fatal(err)
	}
	if first.Location != "油料仓库" || first.CurrentGoal != "维持排水" || first.Pressure != "水即将漫过存货" || !reflect.DeepEqual(first.Resources, []string{"备用油六十升"}) {
		t.Fatalf("explicit actor state was replaced by shared outline/dossier defaults: %+v", first)
	}
	raw, _ := json.Marshal(first)
	if !strings.Contains(string(raw), "我曾调出三十升储备油用于排水") || strings.Contains(string(raw), "旧零章") || strings.Contains(string(raw), "FirstChapter") {
		t.Fatalf("own initial secret was dropped or derived author context leaked: %s", raw)
	}
	profile.Continuity.LastSeenChapter = 1
	profile.Continuity.Dynamics.CurrentGoal = "保全剩余经营物资"
	profile.Continuity.Dynamics.PrimaryPressure = "备用油减少"
	profile.Continuity.Dynamics.Resources = []string{"备用油四十升"}
	profile.Agenda = nil
	projected := domain.ProjectedPlanningContextV2{CumulativeState: []domain.ProjectedPlanningStateFactV2{
		{Category: "character_state", StableID: "location-xu", Subject: "许岚", Field: "location", Value: "值班室", ThroughChapter: 1},
		{Category: "character_state", StableID: "goal-xu", Subject: "许岚", Field: "current_goal", Value: "核对移交记录", ThroughChapter: 1},
	}}
	second, err := buildCharacterObservation(st, "pg2_initial_state", 2, profile, domain.WorldStimulusPacket{}, projected, "later")
	if err != nil {
		t.Fatal(err)
	}
	if second.Location != "值班室" || second.CurrentGoal != "核对移交记录" || second.Pressure != "备用油减少" || !reflect.DeepEqual(second.Resources, []string{"备用油四十升"}) {
		t.Fatalf("later observation reset to initial balances/targets instead of current state: %+v", second)
	}
}

func TestCharacterKnowledgeBoundaryIsNotPositiveKnowledgeOrLegacySeedRecall(t *testing.T) {
	st := store.NewStore(t.TempDir())
	const boundary = "林澄不知道许岚篡改六十升为九十升，更不知道排水用油去向。"
	const ownSecret = "我把自己的备用钥匙留在衣袋里"
	profile := characterAgentProfile{
		Character: domain.Character{Name: "林澄", Role: "值班员"}, Record: domain.CharacterAgentRecord{AgentID: "ca_lin", Character: "林澄"},
		Dossier: &domain.CharacterDossier{KnowledgeBoundary: boundary},
		Continuity: &domain.CharacterContinuityEntry{CurrentFacts: []string{"我在值班室"}, Dynamics: domain.CharacterDynamicsProfile{
			KnowledgeLedger: domain.CharacterKnowledgeLedger{KnownFacts: []string{ownSecret}, ForbiddenKnowledge: []string{boundary}},
		}},
	}
	digest := sha256.Sum256([]byte("character-agent-memory-migration.v1\x00ca_lin\x00" + boundary))
	legacy := domain.CharacterAgentMemoryFact{ID: "mem_" + hex.EncodeToString(digest[:8]), Chapter: 1, Kind: "accepted_continuity", Text: boundary,
		SourceDigest: "sha256:" + hex.EncodeToString(digest[:]), Accepted: true}
	memory := domain.CharacterAgentMemory{AgentID: "ca_lin", Character: "林澄", State: "projected", GenerationID: "pg2_boundary", Facts: []domain.CharacterAgentMemoryFact{
		legacy, {ID: "positive-memory", Chapter: 1, Kind: "accepted_state", Text: ownSecret, SourceDigest: "sha256:accepted", Accepted: true},
	}}
	if err := st.CharacterAgents.SaveProjectedMemory(memory); err != nil {
		t.Fatal(err)
	}
	before, err := st.CharacterAgents.LoadProjectedMemory(memory.GenerationID, memory.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := buildCharacterObservation(st, memory.GenerationID, 1, profile, domain.WorldStimulusPacket{}, domain.ProjectedPlanningContextV2{}, "now")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(observation)
	if strings.Contains(string(raw), "许岚篡改") || strings.Contains(string(raw), "九十升") || !strings.Contains(string(raw), ownSecret) || len(observation.Memory) != 1 {
		t.Fatalf("author boundary leaked or a true private fact was removed: %s", raw)
	}
	after, err := st.CharacterAgents.LoadProjectedMemory(memory.GenerationID, memory.AgentID)
	if err != nil || !reflect.DeepEqual(before, after) || observation.MemoryRoot != before.MemoryRoot {
		t.Fatal("new observation rewrote historical memory instead of taking a safe view")
	}
	legacy.SourceDigest = "sha256:independent-evidence"
	if legacyKnowledgeBoundarySeed(legacy, "ca_lin", boundary) {
		t.Fatal("legacy filter discarded a different evidence source by text alone")
	}
}
