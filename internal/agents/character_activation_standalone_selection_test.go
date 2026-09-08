package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func standaloneActivationSessionFixture(t *testing.T, st *store.Store, generation string, chapter, limit int) {
	t.Helper()
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(generation, chapter, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, cycle.StartDay, limit)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(session); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneActivationSelectionPinsAllExistingSessionsWithoutWrites(t *testing.T) {
	st := store.NewStore(t.TempDir())
	const generation = "pg2_standalone_frozen"
	standaloneActivationSessionFixture(t, st, generation, 3, 4)
	standaloneActivationSessionFixture(t, st, generation, 4, 4)
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []int{0, 1, 2, 4, 8, 64} {
		cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", MaxActivationCycles: configured}}
		limit, err := characterActivationExecutionLimit(st, cfg, generation, ProjectedArcBoundary{})
		if err != nil || limit != 4 {
			t.Fatalf("configured=%d switched frozen session limit: %d %v", configured, limit, err)
		}
	}
	for _, protocol := range []string{"v1", "legacy"} {
		cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: protocol, MaxActivationCycles: 1}}
		if _, err := characterActivationExecutionLimit(st, cfg, generation, ProjectedArcBoundary{}); err == nil {
			t.Fatalf("existing multi-cycle generation downgraded to %s", protocol)
		}
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatal("standalone selection repaired or rewrote existing evidence")
	}
}

func TestStandaloneActivationSelectionPreservesSingleAndRejectsMixedOrConflictingEvidence(t *testing.T) {
	for _, scenario := range []string{"single", "mixed", "limits", "one-cycle-session", "missing-session", "corrupt-session", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			const generation = "pg2_selection_boundary"
			cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", MaxActivationCycles: 8}}
			if scenario == "single" || scenario == "mixed" {
				if err := st.CharacterAgents.SaveStimulus(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketVersion, GenerationID: generation, Chapter: 1, TimeWindow: "legacy"}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "single" {
				limit := 4
				if scenario == "one-cycle-session" {
					limit = 1
				}
				standaloneActivationSessionFixture(t, st, generation, 1, limit)
			}
			root := filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", generation)
			switch scenario {
			case "limits":
				standaloneActivationSessionFixture(t, st, generation, 2, 8)
			case "missing-session":
				if err := os.Rename(filepath.Join(root, "000001/session.json"), filepath.Join(root, "000001/session.saved")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-session":
				if err := os.WriteFile(filepath.Join(root, "000001/session.json"), []byte(`{"max_cycles":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "000002")); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil && scenario != "symlink" {
				t.Fatal(err)
			}
			limit, gotErr := characterActivationExecutionLimit(st, cfg, generation, ProjectedArcBoundary{})
			if scenario == "single" {
				if gotErr != nil || limit != 1 {
					t.Fatalf("existing single chapter proof was upgraded: %d %v", limit, gotErr)
				}
			} else if gotErr == nil {
				t.Fatalf("invalid evidence selected a protocol: %d", limit)
			}
			if scenario != "symlink" {
				after, err := store.DirectoryContentRoot(st.Dir())
				if err != nil || before != after {
					t.Fatal("rejection changed persisted evidence")
				}
			}
		})
	}
}

func TestStandaloneActivationSelectionKeepsFreshGenerationConfig(t *testing.T) {
	st := store.NewStore(t.TempDir())
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", MaxActivationCycles: 8}}
	if got, err := characterActivationExecutionLimit(st, cfg, "pg2_new", ProjectedArcBoundary{}); err != nil || got != 8 {
		t.Fatalf("fresh generation lost its explicit config: %d %v", got, err)
	}
	if _, err := characterActivationExecutionLimit(st, cfg, "pg2_bad/../other", ProjectedArcBoundary{}); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("generation path escaped its exact namespace: %v", err)
	}
}
