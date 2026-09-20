package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestChapterDeliveryBudgetCLIRejectsLostStartAfterShadowExecution(t *testing.T) {
	opts, live, _ := continuationProducerCLIFixture(t)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	cfg.Budget.ChapterDeliverySeconds = 1200
	progress, err := live.Progress.Load()
	publicationArtifactMust(t, err)
	_, err = writePipelinePlanningJSON(filepath.Join(live.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{
		Version: "project-all-attempt.v2", Nonce: "fresh-shadow-original-start",
	})
	publicationArtifactMust(t, err)
	fresh, err := buildPipelineProjectAllIdentity(cfg, bundle, live, progress)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, live.ProjectedV2().CreateBuildingGeneration(fresh.Generation, fresh.Source, fresh.Registry))
	publicationArtifactMust(t, live.ArmChapterDeliveryBudgetNow(fresh.Generation))
	ledgerPath := filepath.Join(live.Dir(), "meta", "runtime", "chapter_delivery", "ledger.json")
	armed, err := os.ReadFile(ledgerPath)
	publicationArtifactMust(t, err)
	raw, err := json.Marshal(cfg)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, os.WriteFile(opts.ConfigPath, raw, 0600))

	stop := errors.New("shadow execution checkpoint persisted; no model invoked")
	previous := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = previous })
	calls := 0
	var shadowOutput string
	const pendingID = "shadow-character-call-with-original-start"
	pipelineProjectedChapterPlanner = func(_ context.Context, _ bootstrap.Config, _ assets.Bundle, output string, chapter int, contextDigest, _ string, _ agents.ProjectedArcBoundary, _ ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		calls++
		if calls > 1 {
			return nil, stop
		}
		shadowOutput = output
		if filepath.Clean(output) == filepath.Clean(live.Dir()) {
			t.Fatal("planner is not using its actual isolated workspace")
		}
		timing, err := live.LoadChapterDeliveryTiming(fresh.Generation.GenerationID, chapter)
		publicationArtifactMust(t, err)
		if timing == nil || timing.StartedAt.IsZero() || timing.DeadlineAt.Sub(timing.StartedAt).Seconds() != 1200 {
			t.Fatal("planner was dispatched before the original wall-clock budget was persisted")
		}
		// Real baseline/session publication leaves chapter-specific execution
		// evidence only in shadow, before any projected bundle exists in live.
		cycle := testutil.CharacterCycle(t, 1, "", nil, 0, contextDigest)
		session, err := domain.NewCharacterActivationSession(fresh.Generation.GenerationID, chapter, contextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, fresh.Generation.MaxCharacterActivationCycles)
		publicationArtifactMust(t, err)
		publicationArtifactMust(t, store.NewStore(output).CreateCharacterActivationSession(session))
		// Call-start accounting lacks Chapter, so it must not replace the exact
		// session evidence when deciding whether this chapter already started.
		meter, err := host.NewDurableUsageMeter(live)
		publicationArtifactMust(t, err)
		birth, alive, err := host.UsageProcessIdentity(os.Getpid())
		publicationArtifactMust(t, err)
		if !alive {
			t.Fatal("test process has no accounting identity")
		}
		publicationArtifactMust(t, meter.StartCall(pendingID, "character", fresh.Generation.GenerationID, os.Getpid(), birth))
		publicationArtifactMust(t, meter.Flush())
		return nil, stop
	}
	flags := pipelineFlags{Start: 1, End: 3}
	if err := pipelineProjectAllOnce(opts, flags); !errors.Is(err, stop) {
		t.Fatalf("first dispatch did not reach the controlled planning boundary: %v", err)
	}
	if calls != 1 || shadowOutput == "" {
		t.Fatalf("first planner dispatch count=%d workspace=%q", calls, shadowOutput)
	}
	if session, err := store.NewStore(shadowOutput).LoadCharacterActivationSession(fresh.Generation.GenerationID, 1); err != nil || session == nil {
		t.Fatalf("real shadow session was not preserved: %+v %v", session, err)
	}
	if session, err := live.LoadCharacterActivationSession(fresh.Generation.GenerationID, 1); err != nil || session != nil {
		t.Fatalf("fixture leaked shadow evidence into live: %+v %v", session, err)
	}
	// Simulate a partial restoration of the authentic earlier armed snapshot.
	// This is not a manually forged ledger or a new generation/reset request.
	publicationArtifactMust(t, os.WriteFile(ledgerPath, armed, 0600))
	err = pipelineProjectAllOnce(opts, flags)
	if err == nil || errors.Is(err, stop) || !strings.Contains(err.Error(), "original start") {
		t.Fatalf("resume failed to reject lost original start before planner dispatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("resume redispatched paid planning after original start was lost: calls=%d", calls)
	}
	after, readErr := os.ReadFile(ledgerPath)
	publicationArtifactMust(t, readErr)
	if !bytes.Equal(armed, after) {
		t.Fatal("rejected resume changed the authentic armed ledger or granted a fresh clock")
	}
	meter, err := host.NewDurableUsageMeter(live)
	publicationArtifactMust(t, err)
	if pending, found := meter.PendingCalls()[pendingID]; !found || pending.GenerationID != fresh.Generation.GenerationID {
		t.Fatal("rejected resume lost the original pending accounting evidence")
	}
}
