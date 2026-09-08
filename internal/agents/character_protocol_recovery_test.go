package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func seedCharacterProtocolRecovery(t *testing.T, chapter int, protocol string) (*store.Store, domain.CharacterObservationPacket) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{GenerationID: "pg2_bound", CurrentChapter: chapter, TotalChapters: 3}); err != nil {
		t.Fatal(err)
	}
	var sources []string
	if protocol != "" {
		sources = []string{"character-agent-protocol:" + protocol}
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{
		GenerationID: "pg2_bound", Chapter: chapter, TimeWindow: "夜间", Sources: sources,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveStimulus(stimulus); err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		GenerationID: stimulus.GenerationID, Chapter: chapter, Round: 1, AgentID: "ca_lin", Character: "林澄",
		StimulusDigest: stimulus.Digest, CurrentGoal: "保全票据", Pressure: "雨势加大",
		KnownFacts:  []domain.CharacterAgentFact{{ID: "fact-1", Kind: "known", Text: "我已经看到原件"}},
		PublicRules: []domain.CharacterAgentFact{{ID: "old-rule", Kind: "world_rule", Text: "旧协议原样保留的公开规则"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveObservation(observation); err != nil {
		t.Fatal(err)
	}
	activation := domain.CharacterAgentActivation{
		GenerationID: stimulus.GenerationID, Chapter: chapter, RegistryRoot: "sha256:registry",
		Entries: []domain.CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character,
			State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	}
	if err := st.CharacterAgents.SaveActivation(activation); err != nil {
		t.Fatal(err)
	}
	proposal := domain.CharacterDecisionProposal{
		GenerationID: observation.GenerationID, Chapter: chapter, Round: 1, AgentID: observation.AgentID,
		Character: observation.Character, ObservationDigest: observation.Digest, Location: "值班室",
		CurrentGoal: "保全票据", Pressure: "雨势加大", AvailableOptions: []string{"封存", "留在桌上"},
		Decision: "封存", DecisionReason: "避免被雨水打湿", IntendedAction: "把票据放入防水袋", ActionDuration: "一分钟",
		KnowledgeRefs: []string{"fact-1"},
	}
	if err := st.CharacterAgents.SaveProposal(proposal, observation); err != nil {
		t.Fatal(err)
	}
	return st, observation
}

func characterProtocolSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCharacterProtocolMismatchStopsBeforeContextReceiptsOrOtherWrites(t *testing.T) {
	for _, protocol := range []string{"sha256:legacy-character-prompt", ""} {
		st, _ := seedCharacterProtocolRecovery(t, 1, protocol)
		before := characterProtocolSnapshot(t, st.Dir())
		_, _, err := runCharacterAgentWorldSimulation(context.Background(), bootstrap.Config{}, st, &bootstrap.ModelSet{},
			tools.NewContextTool(st, tools.References{}, ""), 1, ProjectedArcBoundary{FirstChapter: 1})
		if err == nil || !strings.Contains(err.Error(), "character-agent protocol mismatch") || !strings.Contains(err.Error(), "--pipeline") {
			t.Fatalf("missing actionable protocol conflict: %v", err)
		}
		if !reflect.DeepEqual(before, characterProtocolSnapshot(t, st.Dir())) {
			t.Fatal("protocol conflict wrote context receipts, registration, usage or changed existing proposals")
		}
	}
}

func TestCharacterProtocolRecoveryChecksLatestEvidenceWhenArcFirstIsMissing(t *testing.T) {
	st, _ := seedCharacterProtocolRecovery(t, 2, "sha256:legacy-character-prompt")
	before := characterProtocolSnapshot(t, st.Dir())
	_, _, err := runCharacterAgentWorldSimulation(context.Background(), bootstrap.Config{}, st, &bootstrap.ModelSet{},
		tools.NewContextTool(st, tools.References{}, ""), 3, ProjectedArcBoundary{FirstChapter: 1})
	if err == nil || !strings.Contains(err.Error(), "protocol mismatch") || !strings.Contains(err.Error(), "chapter=2") {
		t.Fatalf("missing first chapter incorrectly authorized new observations in the old generation: %v", err)
	}
	if !reflect.DeepEqual(before, characterProtocolSnapshot(t, st.Dir())) {
		t.Fatal("next-chapter protocol rejection changed existing evidence")
	}
}

func TestCharacterProtocolCurrentRecoveryReusesExactObservation(t *testing.T) {
	st, observation := seedCharacterProtocolRecovery(t, 1, CharacterAgentProtocolDigest())
	before := characterProtocolSnapshot(t, st.Dir())
	inputs, err := loadOrPrepareCharacterAgentInputs(st, observation.GenerationID, 1, ProjectedArcBoundary{}, domain.ProjectedPlanningContextV2{},
		[]string{"fresh-context-access", "character-agent-protocol:" + CharacterAgentProtocolDigest()})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(inputs.Observations[observation.AgentID])
	want, _ := json.Marshal(observation)
	if string(got) != string(want) {
		t.Fatalf("same-protocol resume regenerated or filtered stored observation: %s", got)
	}
	if !reflect.DeepEqual(before, characterProtocolSnapshot(t, st.Dir())) {
		t.Fatal("same-protocol read/resume rewrote immutable inputs")
	}
}

func TestCharacterProtocolNewGenerationBuildsViewsWithoutRewritingOldEvidence(t *testing.T) {
	st, oldObservation := seedCharacterProtocolRecovery(t, 1, "sha256:legacy-character-prompt")
	oldRoot := filepath.Join(st.Dir(), "meta", "character_agents", "projected", oldObservation.GenerationID)
	before := characterProtocolSnapshot(t, oldRoot)
	if err := st.Characters.Save([]domain.Character{{Name: "林澄", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "林澄保全票据", CoreEvent: "林澄看见封条"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{Category: "记录", Rule: "作者预定终局", Visibility: "formal", CharacterView: "原件接触需要留痕"}}); err != nil {
		t.Fatal(err)
	}
	inputs, err := loadOrPrepareCharacterAgentInputs(st, "pg2_new_protocol", 1, ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1}, domain.ProjectedPlanningContextV2{},
		[]string{"character-agent-protocol:" + CharacterAgentProtocolDigest()})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Observations) != 1 {
		t.Fatalf("new generation did not build one active observation: %+v", inputs.Activation)
	}
	for _, observation := range inputs.Observations {
		if observation.GenerationID != "pg2_new_protocol" || observation.Digest == oldObservation.Digest || len(observation.PublicRules) != 1 || observation.PublicRules[0].Text != "原件接触需要留痕" {
			t.Fatalf("new generation did not use its explicit view: %+v", observation)
		}
	}
	if !reflect.DeepEqual(before, characterProtocolSnapshot(t, oldRoot)) {
		t.Fatal("new generation changed old protocol observations or proposals")
	}
}
