package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineDeliveryOverrunExplicitReasonFlag(t *testing.T) {
	flags, rest, err := parsePipelineFlags([]string{"--stages", "project-all,seal,promote,render", "--delivery-overrun-reason", "User approved continuation while preserving the original deadline"})
	if err != nil {
		t.Fatalf("explicit audited continuation cannot be requested: %v", err)
	}
	if len(rest) != 0 || !strings.Contains(fmt.Sprintf("%+v", flags), "OverrunReason:User approved continuation") {
		t.Fatalf("continuation reason was lost: %+v rest=%v", flags, rest)
	}
}

func TestPipelineDeliveryOverrunFlagRejectsAmbiguousScope(t *testing.T) {
	for _, args := range [][]string{
		{"--stages", "project-all", "--delivery-overrun-reason", ""},
		{"--stages", "project-all", "--delivery-overrun-reason", "  "},
		{"--stages", "render", "--delivery-overrun-reason", "approved"},
		{"--stages", "architect", "--delivery-overrun-reason", "approved"},
		{"--stages", "project-all", "--restart", "--delivery-overrun-reason", "approved"},
		{"--rebase-all-chapters", "--delivery-overrun-reason", "approved"},
		{"--init-only", "--delivery-overrun-reason", "approved"},
	} {
		if _, _, err := parsePipelineFlags(args); err == nil {
			t.Fatalf("ambiguous continuation accepted: %v", args)
		}
	}
}

func TestPipelineDeliveryOverrunDoesNotReadPermissionFromPrompt(t *testing.T) {
	flags, _, err := parsePipelineFlags([]string{"--stages", "project-all", "--prompt", "--delivery-overrun-reason approved"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", flags), "OverrunReason:approved") {
		t.Fatal("story prompt granted host continuation authority")
	}
}

func TestPipelineDeliveryOverrunResumesBeforePlannerWithoutResettingClock(t *testing.T) {
	opts, st, _ := continuationProducerCLIFixture(t)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	cfg.Budget.ChapterDeliverySeconds = 1200
	_, err = writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{Version: "project-all-attempt.v2", Nonce: "audited-overrun-test"})
	publicationArtifactMust(t, err)
	identity, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	publicationArtifactMust(t, err)
	g := identity.Generation
	publicationArtifactMust(t, st.ProjectedV2().CreateBuildingGeneration(g, identity.Source, identity.Registry))
	start := time.Now().UTC().Add(-time.Hour)
	publicationArtifactMust(t, st.ArmChapterDeliveryBudget(g, start))
	original, err := st.BeginChapterDelivery(g, g.FirstProjectedChapter, start)
	publicationArtifactMust(t, err)
	configJSON, err := json.Marshal(cfg)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, os.WriteFile(opts.ConfigPath, configJSON, 0600))
	stop := errors.New("planner boundary reached without calling a provider")
	oldPlanner := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = oldPlanner })
	calls := 0
	pipelineProjectedChapterPlanner = func(_ context.Context, _ bootstrap.Config, _ assets.Bundle, _ string, chapter int, _, _ string, _ agents.ProjectedArcBoundary, hooks ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		calls++
		timing, err := st.LoadChapterDeliveryTiming(g.GenerationID, chapter)
		publicationArtifactMust(t, err)
		if timing == nil || timing.StartedAt != original.StartedAt || timing.DeadlineAt != original.DeadlineAt || timing.LimitSeconds != 1200 || !timing.ClosedAt.IsZero() {
			t.Fatalf("continuation reset or completed timing: %+v", timing)
		}
		if len(hooks) != 1 || hooks[0].BeforeAgent == nil {
			t.Fatal("provider accounting guard is missing")
		}
		publicationArtifactMust(t, hooks[0].BeforeAgent())
		return nil, stop
	}
	if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3}); !errors.Is(err, store.ErrChapterDeliveryDeadline) || calls != 0 {
		t.Fatalf("ordinary expired generation dispatched: calls=%d err=%v", calls, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3, OverrunReason: "User explicitly approved continuation; preserve the failed SLO"}); !errors.Is(err, stop) {
			t.Fatalf("authorized resume never reached its existing planner: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected planner invocations: %d", calls)
	}
	stored, err := st.ProjectedV2().LoadBuildingGeneration(g.GenerationID)
	publicationArtifactMust(t, err)
	if stored.GenerationDigest != g.GenerationDigest || stored.ProjectedChapterCount != 0 {
		t.Fatal("runtime authorization changed the frozen generation or invented a chapter")
	}
}

func TestPipelineDeliveryOverrunCanAuthorizeSealedGenerationWithoutClosingIt(t *testing.T) {
	st, generation := chapterDeliverySealedDispatchFixture(t, true)
	before, err := st.LoadChapterDeliveryTiming(generation.GenerationID, 1)
	publicationArtifactMust(t, err)
	owner := "sealed-overrun-authority"
	publicationArtifactMust(t, st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: generation.FirstProjectedChapter, Owner: owner, ExpiresAt: time.Now().Add(time.Hour)}))
	t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(owner) })
	publicationArtifactMust(t, authorizePipelineDeliveryOverrun(st, generation, "Explicit approval to continue sealed rendering", owner))
	publicationArtifactMust(t, st.CheckChapterDeliveryBudget(generation.GenerationID, before.DeadlineAt.Add(time.Minute)))
	after, err := st.LoadChapterDeliveryTiming(generation.GenerationID, 1)
	publicationArtifactMust(t, err)
	if after.StartedAt != before.StartedAt || after.DeadlineAt != before.DeadlineAt || !after.ClosedAt.IsZero() || after.AcceptanceReceiptDigest != "" {
		t.Fatal("sealed continuation invented acceptance or reset timing")
	}
}
