package store

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCharacterCanonSeedsPositiveInitialAndCurrentKnowledgeNotAuthorBoundaries(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const privateFact = "我曾调走三十升油用于排水"
	const forbidden = "她不知道周砚把原始接收联藏在暗格"
	if err := st.Characters.Save([]domain.Character{{Name: "许岚", Role: "重要配角", InitialState: &domain.CharacterInitialState{
		KnownFacts: []string{privateFact},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{Character: "许岚", Role: "重要配角", KnowledgeBoundary: forbidden}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.io.WriteJSON(characterContinuityJSON, domain.CharacterContinuityLedger{Entries: []domain.CharacterContinuityEntry{{
		Name: "许岚", CurrentFacts: []string{"我持有仓库钥匙"}, Dynamics: domain.CharacterDynamicsProfile{
			KnowledgeLedger: domain.CharacterKnowledgeLedger{KnownFacts: []string{"我刚看见水位上升"}, ForbiddenKnowledge: []string{forbidden}},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(0); err != nil {
		t.Fatal(err)
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil || registry == nil {
		t.Fatal(err)
	}
	actor, ok := registry.Resolve("许岚")
	if !ok {
		t.Fatal("character identity not seeded")
	}
	memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
	if err != nil || memory == nil {
		t.Fatal(err)
	}
	var facts []string
	for _, fact := range memory.Facts {
		facts = append(facts, fact.Text)
	}
	joined := strings.Join(facts, "\n")
	for _, want := range []string{privateFact, "我持有仓库钥匙", "我刚看见水位上升"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("positive personal knowledge was not retained: %v", facts)
		}
	}
	if strings.Contains(joined, "周砚") || len(facts) != 3 {
		t.Fatalf("author negative boundary was converted to accepted memory: %v", facts)
	}
}
