package store

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestDossierOpeningKnowledgeRendersApartFromHistoricalTimeline(t *testing.T) {
	st := NewStore(t.TempDir())
	dossier := domain.CharacterDossier{Version: 1, Character: "林澄",
		CurrentAtStoryStart:    domain.CharacterStartState{Time: "T0"},
		PreStoryTimeline:       []domain.CharacterPastEvent{{Time: "T-600", Event: "真实发生的签领"}},
		KnownFactsAtStoryStart: []string{"我知道T-600有一份签领记录", "柜台现在正在核账"},
	}
	if err := st.SaveCharacterDossier(dossier); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadCharacterDossier(dossier.Character)
	if err != nil || !reflect.DeepEqual(loaded.KnownFactsAtStoryStart, dossier.KnownFactsAtStoryStart) || !reflect.DeepEqual(loaded.PreStoryTimeline, dossier.PreStoryTimeline) {
		t.Fatalf("dossier knowledge/timeline roundtrip changed meaning: %+v %v", loaded, err)
	}
	raw, err := os.ReadFile(st.Progress.io.path(characterDossierMD(dossier.Character)))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	historyStart, factsStart := strings.Index(text, "## 故事开始前经历"), strings.Index(text, "## 故事开始时已知事实")
	if historyStart < 0 || factsStart <= historyStart {
		t.Fatal("Markdown did not separate event history from known-at-opening facts")
	}
	history := text[historyStart:factsStart]
	if !strings.Contains(history, "T-600：真实发生的签领") || strings.Contains(history, "柜台现在") || strings.Contains(text, "T0：我知道") || strings.Contains(text, "T0：柜台") {
		t.Fatalf("opening knowledge was assigned a fabricated event time: %s", text)
	}
}

func TestDossierOnlyCanonSeedDeduplicatesKnownFactsAndPreservesExistingMemory(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	dossier := domain.CharacterDossier{Character: "周砚", Role: "主角", KnownFactsAtStoryStart: []string{"我在T-600收到六十升油", "我正在检查渡船", " 我正在检查渡船 "}}
	if err := st.SaveCharacterDossier(dossier); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(0); err != nil {
		t.Fatal(err)
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil || registry == nil {
		t.Fatal(err)
	}
	actor, ok := registry.Resolve(dossier.Character)
	if !ok {
		t.Fatal("dossier-only actor was not registered")
	}
	memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
	if err != nil || memory == nil || len(memory.Facts) != 2 {
		t.Fatalf("primary canon migration ignored dossier knowledge or duplicated it: %+v %v", memory, err)
	}
	for _, fact := range memory.Facts {
		if fact.Kind == "pre_story" || strings.HasPrefix(fact.Text, "T0") {
			t.Fatalf("current knowledge was converted to an event: %+v", fact)
		}
	}
	originalRoot := memory.MemoryRoot
	dossier.KnownFactsAtStoryStart = append(dossier.KnownFactsAtStoryStart, "后来新写入但尚未接受的事实")
	if err := st.SaveCharacterDossier(dossier); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCharacterAgentCanon(0); err != nil {
		t.Fatal(err)
	}
	after, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
	if err != nil || after.MemoryRoot != originalRoot {
		t.Fatal("dossier refresh silently changed an existing canonical memory")
	}
}
