package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func copyCharacterRebaseTree(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, raw, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func seedCharacterRebaseState(t *testing.T, output string) domain.CharacterAgentRecord {
	t.Helper()
	st := NewStore(output)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	registry, original, err := (domain.CharacterAgentRegistry{}).UpsertCharacter("林默", []string{"小林"}, "core", 1, "original-identity")
	if err != nil {
		t.Fatal(err)
	}
	registry, renamed, err := registry.UpsertCharacter("林澄", []string{"林默"}, "core", 8, "renamed-identity")
	if err != nil || renamed.AgentID != original.AgentID {
		t.Fatalf("fixture identity rename failed: %+v %v", renamed, err)
	}
	registry.Entries[0].Status = domain.CharacterAgentRetired
	registry.Entries[0].LastActivatedChapter = 8
	registry.Entries[0].MemoryVersion = 9
	if err := st.CharacterAgents.SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	memory := domain.CharacterAgentMemory{AgentID: renamed.AgentID, Character: renamed.Character, LastAcceptedChapter: 8,
		Facts: []domain.CharacterAgentMemoryFact{{ID: "old-fact", Chapter: 8, Kind: "accepted_state", Text: "被否定正文里的私人事实", SourceDigest: "sha256:old-body", Accepted: true}}}
	if err := st.CharacterAgents.SaveCanonicalMemory(memory); err != nil {
		t.Fatal(err)
	}
	memory.GenerationID = "pg2_rejected"
	if err := st.CharacterAgents.SaveProjectedMemory(memory); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.io.WriteJSON(characterAgentCurrentSuccessorPath(), map[string]string{"parent_generation_id": "pg2_rejected"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CharacterAgents.AppendUsage(domain.CharacterAgentUsage{UsageID: "old-spend", GenerationID: "pg2_rejected", Role: "character",
		AgentID: renamed.AgentID, Character: renamed.Character, Chapter: 8, Round: 1, Input: 123, CostUSD: 0.25, CostSource: "estimated"}); err != nil {
		t.Fatal(err)
	}
	return renamed
}

func TestCharacterRebaseCandidateDropsStoryStatePreservesIdentityAndArchive(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	archive := filepath.Join(root, "archives", "old", "output", "novel")
	candidate := filepath.Join(root, ".canon-rebase", "rebase-test", "output")
	identity := seedCharacterRebaseState(t, live)
	copyCharacterRebaseTree(t, live, archive)
	copyCharacterRebaseTree(t, live, candidate)
	liveRoot, _ := DirectoryContentRoot(live)
	archiveRoot, _ := DirectoryContentRoot(archive)
	st := NewStore(candidate)
	if err := st.CharacterAgents.ResetForChapterZeroRebase(archive); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"memory", "projected", "successors"} {
		if _, err := os.Stat(filepath.Join(candidate, characterAgentRoot, rel)); !os.IsNotExist(err) {
			t.Fatalf("old character story state survived chapter-zero reset: %s %v", rel, err)
		}
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil || registry == nil {
		t.Fatalf("stable registry disappeared: %v", err)
	}
	for _, alias := range []string{"林澄", "林默", "小林"} {
		record, ok := registry.Resolve(alias)
		if !ok || record.AgentID != identity.AgentID || record.AgentName != identity.AgentName || record.Status != domain.CharacterAgentSleeping || record.LastActivatedChapter != 0 || record.MemoryVersion != 0 {
			t.Fatalf("identity alias %q or zero state lost: %+v", alias, record)
		}
	}
	if memory, err := st.CharacterAgents.LoadCanonicalMemory(identity.AgentID); err != nil || memory != nil {
		t.Fatalf("rejected canon memory remained readable: %+v %v", memory, err)
	}
	if successor, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil || successor != nil {
		t.Fatalf("old successor survived: %+v %v", successor, err)
	}
	usage, err := st.CharacterAgents.LoadUsage()
	if err != nil || len(usage) != 1 || usage[0].UsageID != "old-spend" || usage[0].CostUSD != 0.25 {
		t.Fatalf("historical spend was discarded: %+v %v", usage, err)
	}
	firstRoot, _ := DirectoryContentRoot(candidate)
	if err := st.CharacterAgents.ResetForChapterZeroRebase(archive); err != nil {
		t.Fatalf("candidate reset retry failed: %v", err)
	}
	if after, _ := DirectoryContentRoot(candidate); after != firstRoot {
		t.Fatal("candidate reset is not deterministic/idempotent")
	}
	if after, _ := DirectoryContentRoot(live); after != liveRoot {
		t.Fatal("candidate reset mutated original live canon")
	}
	if after, _ := DirectoryContentRoot(archive); after != archiveRoot {
		t.Fatal("candidate reset mutated original archive")
	}
}

func TestCharacterRebaseResetRejectsLiveOrUnarchivedChangesBeforeClearing(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "output", "novel")
	archive := filepath.Join(root, "archives", "old", "output", "novel")
	candidate := filepath.Join(root, ".canon-rebase", "rebase-test", "output")
	identity := seedCharacterRebaseState(t, live)
	copyCharacterRebaseTree(t, live, archive)
	copyCharacterRebaseTree(t, live, candidate)
	before, _ := DirectoryContentRoot(live)
	if err := NewStore(live).CharacterAgents.ResetForChapterZeroRebase(archive); err == nil {
		t.Fatal("ordinary live Store was allowed to clear memories")
	}
	if after, _ := DirectoryContentRoot(live); after != before {
		t.Fatal("rejected live reset changed canon")
	}
	st := NewStore(candidate)
	if err := st.CharacterAgents.ResetForChapterZeroRebase(""); err == nil {
		t.Fatal("candidate memory was reset without archive proof")
	}
	path := st.CharacterAgents.io.path(characterAgentMemoryPath(identity.AgentID))
	if err := os.WriteFile(path, []byte(`{"unarchived":"new private data"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ = DirectoryContentRoot(candidate)
	if err := st.CharacterAgents.ResetForChapterZeroRebase(archive); err == nil {
		t.Fatal("unarchived candidate changes were silently destroyed")
	}
	if after, _ := DirectoryContentRoot(candidate); after != before {
		t.Fatal("failed archive validation partially reset the candidate")
	}
}
