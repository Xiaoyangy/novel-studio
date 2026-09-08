package agents

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestEnsureCharacterAgentMemorySeedsOpeningKnowledgeWithoutInventedEvents(t *testing.T) {
	st := store.NewStore(t.TempDir())
	profile := characterAgentProfile{
		Character: domain.Character{Name: "周砚", Role: "机修员"}, Record: domain.CharacterAgentRecord{AgentID: "ca_zhou", Character: "周砚"},
		Dossier: &domain.CharacterDossier{CurrentAtStoryStart: domain.CharacterStartState{Time: "T0"},
			KnownFactsAtStoryStart: []string{"我在T-600收到六十升油", "我正在检查渡船", "我正在检查渡船"}},
		Continuity: &domain.CharacterContinuityEntry{CurrentFacts: []string{"我正在检查渡船"}},
	}
	if err := ensureCharacterAgentMemory(st, "pg2_opening_knowledge", profile, 1, "now"); err != nil {
		t.Fatal(err)
	}
	canonical, err := st.CharacterAgents.LoadCanonicalMemory(profile.Record.AgentID)
	if err != nil || canonical == nil || len(canonical.Facts) != 2 {
		t.Fatalf("opening facts missing or duplicated across sources: %+v %v", canonical, err)
	}
	for _, fact := range canonical.Facts {
		if fact.Kind != "known_at_story_start" || strings.HasPrefix(fact.Text, "T0") {
			t.Fatalf("opening knowledge received fabricated history semantics: %+v", fact)
		}
	}
	root := canonical.MemoryRoot
	profile.Dossier.KnownFactsAtStoryStart = []string{"未被验收的新事实"}
	if err := ensureCharacterAgentMemory(st, "pg2_another", profile, 1, "later"); err != nil {
		t.Fatal(err)
	}
	after, err := st.CharacterAgents.LoadCanonicalMemory(profile.Record.AgentID)
	if err != nil || after.MemoryRoot != root {
		t.Fatal("existing canonical memory was overwritten from a changed opening dossier")
	}
	projected, err := st.CharacterAgents.LoadProjectedMemory("pg2_another", profile.Record.AgentID)
	if err != nil || projected == nil || len(projected.Facts) != 2 {
		t.Fatalf("new projection did not inherit the unchanged accepted memory: %+v %v", projected, err)
	}
}
