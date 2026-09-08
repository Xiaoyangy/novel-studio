package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The old attempt exists before any cursor/input is published. Its producer
// choice is the actual pre-history formula, not an arbitrary fixture digest.
func continuationProducerCLIFixture(t *testing.T, producers ...string) (cliOptions, *store.Store, pipelineProjectAllIdentity) {
	t.Helper()
	opts, st, _ := v3RestartEntryFixture(t)
	_, err := writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{Version: "project-all-attempt.v2", Nonce: "legacy-v3-producer-attempt"})
	publicationArtifactMust(t, err)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	candidates := agents.CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3)
	cfg.CharacterAgents.FrozenActivationProducer = candidates[len(candidates)-1]
	if len(producers) == 1 {
		cfg.CharacterAgents.FrozenActivationProducer = producers[0]
	}
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	old, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.ProjectedV2().CreateBuildingGeneration(old.Generation, old.Source, old.Registry))
	return opts, st, old
}

func TestContinuationProducerCLIRecoversBeforeFirstInputWithoutRestart(t *testing.T) {
	opts, st, old := continuationProducerCLIFixture(t)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	resumed, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	publicationArtifactMust(t, err)
	if resumed.Generation.GenerationID != old.Generation.GenerationID || resumed.FrozenActivationProducer != old.FrozenActivationProducer {
		t.Fatal("unstarted old generation was silently replaced by the new producer")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("read-only identity selection changed generation/cursor/evidence")
	}
	fresh, err := buildPipelineProjectAllIdentityForPreflight(cfg, bundle, st, progress, true)
	publicationArtifactMust(t, err)
	if fresh.Generation.GenerationID == old.Generation.GenerationID || fresh.FrozenActivationProducer == old.FrozenActivationProducer {
		t.Fatal("explicit fresh generation reused the old producer")
	}
	stop := errors.New("fake planner boundary reached; no model is created")
	previous := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = previous })
	calls := 0
	pipelineProjectedChapterPlanner = func(_ context.Context, got bootstrap.Config, _ assets.Bundle, _ string, chapter int, _, _ string, _ agents.ProjectedArcBoundary, _ ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		calls++
		cursor, err := st.ProjectedV2().LoadProjectionCursor()
		publicationArtifactMust(t, err)
		if cursor == nil || cursor.GenerationID != old.Generation.GenerationID || chapter != 1 || got.CharacterAgents.FrozenActivationProducer != old.FrozenActivationProducer {
			t.Fatal("real CLI dispatch lost frozen generation/producer")
		}
		return nil, stop
	}
	if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3}); !errors.Is(err, stop) {
		t.Fatalf("old V3 CLI resume did not reach actual dispatch: %v", err)
	}
	if calls != 1 {
		t.Fatal("old generation dispatched more than once")
	}
}

func TestContinuationProducerCLIDriftNeverFallsBackOrMovesCursor(t *testing.T) {
	opts, st, old := continuationProducerCLIFixture(t)
	publicationArtifactMust(t, st.ProjectedV2().ResetProjectionCursorForRestart(old.Generation.GenerationID))
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	for _, kind := range []string{"model", "planner prompt", "references"} {
		t.Run(kind, func(t *testing.T) {
			changed, prompts := cfg, bundle
			switch kind {
			case "model":
				changed.ModelName += "-different"
			case "planner prompt":
				prompts.Prompts.Planner += " changed"
			case "references":
				prompts.References.LongformPlanning += " changed"
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if _, err := buildPipelineProjectAllIdentity(changed, prompts, st, progress); err == nil || !strings.Contains(err.Error(), "dependency drift") {
				t.Fatalf("producer compatibility swallowed %s drift: %v", kind, err)
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("failed resume moved cursor or wrote evidence")
			}
		})
	}
}

func TestContinuationProducerCLIStagedPlanDriftKeepsOriginalIdentity(t *testing.T) {
	opts, st, old := continuationProducerCLIFixture(t)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	manifests, err := st.Planning.LoadStagedChapterPlanManifests()
	publicationArtifactMust(t, err)
	outlines, err := st.Outline.LoadOutline()
	publicationArtifactMust(t, err)
	outlines[0].Hook += "；新的合法细推安排"
	projectAllCmdTestInstallPreplanManifests(t, st, outlines, progress.GenerationID, old.Preplan.BaseCanonRoot, manifests[0].DependencyFingerprint)
	publicationArtifactMust(t, validatePipelinePreplanFresh(st, old.Preplan))
	legacy := cfg
	legacy.CharacterAgents.FrozenActivationProducer = old.FrozenActivationProducer
	dependency, err := pipelineProjectAllDependencyRoot(legacy, bundle, old.Preplan)
	publicationArtifactMust(t, err)
	if dependency != old.Generation.PlanningDependencyRoot {
		t.Fatal("fixture must change the separately bound staged plan, not producer/model/source dependency")
	}
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if _, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress); err == nil {
		t.Fatal("same producer dependency bypassed full stable-outline/generation identity")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("staged-plan drift created a new generation or moved the cursor")
	}
}
