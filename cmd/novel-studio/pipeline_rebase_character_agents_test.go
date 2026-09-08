package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestResetPipelineAllChapterCandidateClearsCharacterAgentStateWithArchive(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	archive := filepath.Join(root, "archives", "old", "output", "novel")
	candidate := filepath.Join(root, ".canon-rebase", "rebase-test", "output")
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	registry, actor, err := (domain.CharacterAgentRegistry{}).UpsertCharacter("林澄", []string{"小林"}, "core", 5, "old")
	if err != nil {
		t.Fatal(err)
	}
	registry.Entries[0].LastActivatedChapter, registry.Entries[0].MemoryVersion = 5, 8
	if err := st.CharacterAgents.SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.SaveCanonicalMemory(domain.CharacterAgentMemory{AgentID: actor.AgentID, Character: actor.Character, LastAcceptedChapter: 5,
		Facts: []domain.CharacterAgentMemoryFact{{ID: "discarded", Chapter: 5, Kind: "accepted_state", Text: "废弃正文事实", SourceDigest: "sha256:old", Accepted: true}},
	}); err != nil {
		t.Fatal(err)
	}
	rebaseAllTestWriteFile(t, live, "chapters/05.md", "待否定的正文")
	rebaseAllTestWriteFile(t, live, "meta/character_agents/projected/old/chapters/000005/stimulus.json", `{"old":"stimulus"}`)
	rebaseAllTestWriteFile(t, live, "meta/character_agents/successors/current.json", `{"old":"successor"}`)
	if err := copyPipelineRenderCandidateTree(live, archive); err != nil {
		t.Fatal(err)
	}
	if err := copyPipelineRenderCandidateTree(live, candidate); err != nil {
		t.Fatal(err)
	}
	before, _ := store.DirectoryContentRoot(live)
	archiveBefore, _ := store.DirectoryContentRoot(archive)
	if err := resetPipelineAllChapterCandidate(candidate, &zeroInitProject{}, archive); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"chapters", "meta/character_agents/memory", "meta/character_agents/projected", "meta/character_agents/successors"} {
		if _, err := os.Stat(filepath.Join(candidate, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("old generation survived candidate reset: %s %v", rel, err)
		}
	}
	reset, err := store.NewStore(candidate).CharacterAgents.LoadRegistry()
	if err != nil || reset == nil {
		t.Fatal(err)
	}
	preserved, ok := reset.Resolve("小林")
	if !ok || preserved.AgentID != actor.AgentID || preserved.LastActivatedChapter != 0 || preserved.MemoryVersion != 0 {
		t.Fatalf("candidate reset did not preserve stable identity and clear runtime state: %+v", preserved)
	}
	if dirty, err := pipelineChapterZeroHasRestartState(candidate); err != nil || dirty {
		t.Fatalf("retained identity/usage caused a completed chapter-zero reset to repeat: dirty=%v err=%v", dirty, err)
	}
	if after, _ := store.DirectoryContentRoot(live); after != before {
		t.Fatal("pre-publication reset changed live data")
	}
	if after, _ := store.DirectoryContentRoot(archive); after != archiveBefore {
		t.Fatal("reset changed archived bytes")
	}
}

func TestChapterZeroRestartDetectsCharacterMemoryWithoutOldPlanningReceipts(t *testing.T) {
	for _, rel := range []string{
		"meta/character_agents/memory/ca_old.json",
		"meta/character_agents/projected/old/memory/ca_old.json",
		"meta/character_agents/successors/current.json",
	} {
		output := filepath.Join(t.TempDir(), "output", "novel")
		rebaseAllTestWriteFile(t, output, rel, `{"old":"rejected story state"}`)
		if dirty, err := pipelineChapterZeroHasRestartState(output); err != nil || !dirty {
			t.Fatalf("chapter-zero rebase skipped residual character state %s: dirty=%v err=%v", rel, dirty, err)
		}
	}
}
