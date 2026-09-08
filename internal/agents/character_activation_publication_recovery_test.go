package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func activationPublicationRecoveryFixture(t *testing.T) (*store.Store, *domain.ChapterWorldSimulation, *domain.Checkpoint) {
	t.Helper()
	st, _ := formalActivationStore(t)
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "publication-recovery", &activationChapterProbeModel{})}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicy, MaxCharacterActivationCycles: 4}
	sim, cp, err := runCharacterAgentWorldSimulation(context.Background(), cfg, st, models, tools.NewContextTool(st, tools.References{}, ""), 1, boundary)
	if err != nil {
		t.Fatal(err)
	}
	return st, sim, cp
}
func activationPublicationTestBytes(t *testing.T, st *store.Store, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(st.Dir(), path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return raw
}
func activationPublicationTestPartial(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{"keep": "later planner work"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{Chapter: 1, TimeWindow: "later independent partial"}); err != nil {
		t.Fatal(err)
	}
}

func TestActivationPublicationFirstPublishImmediatelyValidatesOnSameStore(t *testing.T) {
	st, simulation, published := activationPublicationRecoveryFixture(t)
	current, err := tools.CurrentChapterWorldSimulationCheckpoint(st, simulation.Chapter)
	if err != nil || current == nil || current.Seq != published.Seq || current.Digest != published.Digest {
		t.Fatalf("same Store cannot consume its just-published simulation: checkpoint=%+v err=%v", current, err)
	}
	before := activationPublicationTestBytes(t, st, "meta/checkpoints.jsonl")
	_, retry, err := tools.PublishCharacterActivationSimulation(context.Background(), st, simulation.GenerationID, simulation.Chapter, nil)
	if err != nil || retry.Seq != published.Seq || string(activationPublicationTestBytes(t, st, "meta/checkpoints.jsonl")) != string(before) {
		t.Fatalf("same-Store read/retry appended a new causal epoch: %v", err)
	}
}

func TestActivationPublicationCrossStoreRetryRefreshesCallerWithoutNewEpoch(t *testing.T) {
	reader, state := formalActivationStore(t) // A exists before B publishes.
	writer := store.NewStore(reader.Dir())
	probe := &activationChapterProbeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "cross-store-publication", probe)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3, CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicy, MaxCharacterActivationCycles: 4}
	sim, cp, err := runCharacterAgentWorldSimulation(context.Background(), cfg, writer, models, tools.NewContextTool(writer, tools.References{}, ""), 1, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if reader.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation") != nil {
		t.Fatal("fixture failed to keep A's pre-publication cache")
	}
	activationPublicationTestPartial(t, writer)
	if _, err := writer.Checkpoints.Append(domain.ChapterScope(1), "plan", "", ""); err != nil {
		t.Fatal(err)
	}
	paths := []string{"meta/checkpoints.jsonl", "drafts/01.plan.partial.json", "meta/chapter_simulations/001.partial.json", "meta/chapter_simulations/001.json"}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path] = activationPublicationTestBytes(t, writer, path)
	}
	recovered, retryCP, err := tools.PublishCharacterActivationSimulation(context.Background(), reader, state.GenerationID, 1, nil)
	if err != nil || recovered.SimulationID != sim.SimulationID || retryCP.Seq != cp.Seq {
		t.Fatalf("A retry changed original publication: %v", err)
	}
	current, err := tools.CurrentChapterWorldSimulationCheckpoint(reader, 1)
	if err != nil || current == nil || current.Seq != cp.Seq || current.Digest != cp.Digest {
		t.Fatalf("A retry returned success but left caller cache unusable: %+v %v", current, err)
	}
	for path, want := range before {
		if string(activationPublicationTestBytes(t, reader, path)) != string(want) {
			t.Fatalf("cache refresh modified journal or later work: %s", path)
		}
	}
}

func TestActivationPublicationMissingSimulationPreservesLaterPartialsAndOriginalCheckpoint(t *testing.T) {
	for _, intentOnly := range []bool{false, true} {
		t.Run(fmt.Sprint("intent_only=", intentOnly), func(t *testing.T) {
			st, sim, cp := activationPublicationRecoveryFixture(t)
			if cached := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation"); cached == nil || cached.Seq != cp.Seq {
				t.Fatal("fixture must retain the original published checkpoint in this Store's cache")
			}
			original := activationPublicationTestBytes(t, st, "meta/chapter_simulations/001.json")
			activationPublicationTestPartial(t, st)
			if !intentOnly {
				if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "plan", "", ""); err != nil {
					t.Fatal(err)
				}
			}
			partials := map[string][]byte{}
			for _, path := range []string{"drafts/01.plan.partial.json", "meta/chapter_simulations/001.partial.json"} {
				partials[path] = activationPublicationTestBytes(t, st, path)
			}
			if err := os.Remove(filepath.Join(st.Dir(), "meta/chapter_simulations/001.json")); err != nil {
				t.Fatal(err)
			}
			if intentOnly {
				if err := os.Remove(filepath.Join(st.Dir(), "meta/checkpoints.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			journal := activationPublicationTestBytes(t, st, "meta/checkpoints.jsonl")
			if _, err := tools.NewContextTool(st, tools.References{}, "").PrepareCharacterAgentExecutionContext(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			// Use the original, populated Store cache in the intent-only case too.
			recovered, gotCP, err := tools.PublishCharacterActivationSimulation(context.Background(), st, sim.GenerationID, 1, nil)
			if err != nil || recovered.SimulationID != sim.SimulationID || gotCP.Seq != cp.Seq {
				t.Fatalf("recovery changed identity or failed: %v", err)
			}
			if string(activationPublicationTestBytes(t, st, "meta/chapter_simulations/001.json")) != string(original) {
				t.Fatal("recovery changed original simulation bytes")
			}
			for path, want := range partials {
				if string(activationPublicationTestBytes(t, st, path)) != string(want) {
					t.Fatalf("recovery erased later work: %s", path)
				}
			}
			after := activationPublicationTestBytes(t, st, "meta/checkpoints.jsonl")
			if !intentOnly && string(after) != string(journal) {
				t.Fatal("recovery re-signed the original checkpoint after plan")
			}
			if intentOnly && len(after) == 0 {
				t.Fatal("stale checkpoint cache pretended to repair a missing journal")
			}
		})
	}
}

func TestActivationPublicationCheckpointConflictsFailBeforeAnyDataMutation(t *testing.T) {
	for _, mode := range []string{"wrong-simulation-digest", "plan-without-simulation-checkpoint", "malformed-journal", "missing-authorization"} {
		t.Run(mode, func(t *testing.T) {
			st, sim, _ := activationPublicationRecoveryFixture(t)
			activationPublicationTestPartial(t, st)
			if err := os.Remove(filepath.Join(st.Dir(), "meta/chapter_simulations/001.json")); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "wrong-simulation-digest":
				if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json", "sha256:"+strings.Repeat("e", 64)); err != nil {
					t.Fatal(err)
				}
			case "plan-without-simulation-checkpoint":
				if err := os.Remove(filepath.Join(st.Dir(), "meta/checkpoints.jsonl")); err != nil {
					t.Fatal(err)
				}
				if _, err := store.NewStore(st.Dir()).Checkpoints.Append(domain.ChapterScope(1), "plan", "", ""); err != nil {
					t.Fatal(err)
				}
			case "malformed-journal":
				if err := os.WriteFile(filepath.Join(st.Dir(), "meta/checkpoints.jsonl"), []byte("not-json\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-authorization":
				if err := os.Remove(filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", sim.GenerationID, "000001/publication.json")); err != nil {
					t.Fatal(err)
				}
			}
			// A new unconsumed access receipt cannot authorize changing existing CPs.
			if _, err := tools.NewContextTool(st, tools.References{}, "").PrepareCharacterAgentExecutionContext(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			paths := []string{"meta/checkpoints.jsonl", "drafts/01.plan.partial.json", "meta/chapter_simulations/001.partial.json", "meta/runtime/planning_context_access/simulate.json", "meta/character_agents/activation_sessions/" + sim.GenerationID + "/000001/publication.json"}
			before := map[string][]byte{}
			for _, path := range paths {
				before[path] = activationPublicationTestBytes(t, st, path)
			}
			if _, _, err := tools.PublishCharacterActivationSimulation(context.Background(), store.NewStore(st.Dir()), sim.GenerationID, 1, nil); err == nil {
				t.Fatal("conflicting publication unexpectedly succeeded")
			}
			if len(activationPublicationTestBytes(t, st, "meta/chapter_simulations/001.json")) != 0 {
				t.Fatal("publication saved simulation before discovering CP conflict")
			}
			for path, want := range before {
				if string(activationPublicationTestBytes(t, st, path)) != string(want) {
					t.Fatalf("rejected publication changed %s", path)
				}
			}
		})
	}
}

func TestActivationPublicationRejectsPhasePIDAndInactiveLeaseBeforeMutation(t *testing.T) {
	for _, mode := range []string{"render", "foundation", "world_tick", "outline_all", "preplan", "promote", "foreign-pid", "expired", "wrong-chapter", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			st := chapterActivationStore(t)
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "standalone-publication", &activationChapterProbeModel{})}
			cfg := bootstrap.Config{}
			cfg.CharacterAgents.MaxRevisionRounds = 1
			proof, err := runCharacterActivationChapter(context.Background(), cfg, st, models, "pg2_publication_phase", 1, ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}, domain.ProjectedPlanningContextV2{}, nil, 4)
			if err != nil {
				t.Fatal(err)
			}
			activationPublicationTestPartial(t, st)
			lock := domain.PipelineExecutionLock{Mode: domain.PipelineExecutionMode(mode), TargetChapter: 1, Owner: "publication-test", ExpiresAt: time.Now().Add(time.Hour)}
			if mode == "foreign-pid" || mode == "expired" || mode == "wrong-chapter" || mode == "malformed" {
				lock.Mode = domain.PipelineExecutionProjectAll
			}
			if mode == "wrong-chapter" {
				lock.TargetChapter = 2
			}
			if mode == "render" {
				lock.PlanDigest = "sha256:" + strings.Repeat("a", 64)
			}
			if err := st.Runtime.AcquirePipelineExecution(lock); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json")
			if mode == "foreign-pid" || mode == "expired" || mode == "malformed" {
				raw := activationPublicationTestBytes(t, st, "meta/runtime/pipeline_execution.json")
				if err := json.Unmarshal(raw, &lock); err != nil {
					t.Fatal(err)
				}
				if mode == "foreign-pid" {
					lock.ProcessID = os.Getppid()
				}
				if mode == "expired" {
					lock.ExpiresAt = time.Now().Add(-time.Minute)
				}
				raw, _ = json.Marshal(lock)
				if mode == "malformed" {
					raw = []byte("bad-json")
				}
				if err := os.WriteFile(lockPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string][]byte{}
			for _, path := range []string{"drafts/01.plan.partial.json", "meta/chapter_simulations/001.partial.json", "meta/checkpoints.jsonl", "meta/runtime/pipeline_execution.json"} {
				before[path] = activationPublicationTestBytes(t, st, path)
			}
			if _, _, err := tools.PublishCharacterActivationSimulation(context.Background(), st, proof.Session.GenerationID, 1, nil); err == nil {
				t.Fatal("wrong phase/owner lease authorized publication")
			}
			if len(activationPublicationTestBytes(t, st, "meta/chapter_simulations/001.json")) != 0 {
				t.Fatal("wrong-phase publication wrote simulation")
			}
			for path, want := range before {
				if string(activationPublicationTestBytes(t, st, path)) != string(want) {
					t.Fatalf("rejected phase changed %s", path)
				}
			}
		})
	}
}
