package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func v3RestartEntryFixture(t *testing.T) (cliOptions, *store.Store, pipelineProjectAllIdentity) {
	t.Helper()
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	if err := st.Progress.Init("V3 restart source fixture", 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Progress.EnsureGenerationIfEmpty("restart-source-fixture", domain.GenerationModeSimulationRestartFromSeed); err != nil {
		t.Fatal(err)
	}
	var outlines []domain.OutlineEntry
	for chapter := 1; chapter <= 3; chapter++ {
		outlines = append(outlines, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("核验%d", chapter), CoreEvent: "江烬核对已收到的欠费条款并作出本人选择", Hook: "保留尚未核实的线索", Scenes: []string{"核对票据", "本人回应"}})
	}
	if err := st.Outline.SaveOutline(outlines); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Title: "核验卷", Arcs: []domain.ArcOutline{{Index: 1, Title: "核验弧", Goal: "在规则内核实本人线索", Chapters: outlines}}}}); err != nil {
		t.Fatal(err)
	}
	ready := assessArchitectReadiness(dir)
	if !ready.Ready {
		t.Fatalf("fixture foundation: %v %v", ready.Missing, ready.Issues)
	}
	if err := writeArchitectReadiness(dir, ready); err != nil {
		t.Fatal(err)
	}
	// Production mechanical zero-init, not an invented readiness boolean.
	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatal(err)
	}
	tick, _ := json.Marshal(map[string]any{"volume": 1, "arc": 1, "through_chapter": 0, "events": []domain.WorldEvent{{Chapter: 0, Actors: []string{"账单规则"}, Location: "老小区楼道", Summary: "账单规则在开场前封存了待查的旧账收据。", Consequence: "收据留待住户后续核对，不替主角预先选择。", VisibilityChapter: 1, VisibilityPath: "住户后来领取收据时亲见"}}})
	if _, err := tools.NewSaveWorldTickTool(st).Execute(context.Background(), tick); err != nil {
		t.Fatal(err)
	}
	if err := pipelineRequirePrewritingReady(dir); err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{Chunks: []domain.RAGChunk{{ID: "local-fixture", SourcePath: "deconstruction-library/local.md", SourceKind: "craft_method", Text: "按实际选择组织顺序"}}}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(dir))
	configPath := filepath.Join(root, "config.json")
	write := func(policy string) {
		raw, _ := json.Marshal(map[string]any{"provider": "ollama", "model": "no-model-is-invoked", "character_agents": map[string]any{"protocol": "v2", "execution_policy": policy, "max_activation_cycles": 4}, "providers": map[string]any{"ollama": map[string]any{"type": "openai", "base_url": "http://127.0.0.1:1/v1"}}})
		if err := os.WriteFile(configPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("v2")
	opts := cliOptions{Dir: root, ConfigPath: configPath}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("progress: %v", err)
	}
	base, err := pipelineCanonRoot(dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, sourceArtifacts, err := pipelinePlanningDependencies(dir)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.NewDependencyFingerprint(progress.GenerationID, base, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	projectAllCmdTestInstallPreplanManifests(t, st, outlines, progress.GenerationID, base, fingerprint)
	preplan := pipelinePreplanReceipt{Version: pipelinePlanningSchema, GenerationID: progress.GenerationID, BaseCanonChapter: 0, BaseCanonRoot: base, CurrentCanonRoot: base, DependencyRoot: fingerprint.RootSHA256, TotalChapters: 3, VolumeIndices: []int{1}, StagedChapters: []int{1, 2, 3}, DetailedChapters: []int{1, 2, 3}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), SourceArtifacts: sourceArtifacts}
	if _, err := writePipelinePlanningJSON(filepath.Join(dir, pipelinePlanningReceiptPath), preplan); err != nil {
		t.Fatal(err)
	}
	identity, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Generation.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV2 {
		t.Fatal("fixture is not an actual v2 attempt")
	}
	if err := st.ProjectedV2().CreateBuildingGeneration(identity.Generation, identity.Source, identity.Registry); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProjectedV2().InitializeProjectionCursor(identity.Generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	write("v3")
	return opts, st, identity
}

func TestPipelineV3RestartRealStageRotatesOnlyAfterLockAndDispatchesNewAttempt(t *testing.T) {
	opts, st, old := v3RestartEntryFixture(t)
	oldRoot := filepath.Join(st.Dir(), "meta/planning/v2/.building", old.Generation.GenerationID)
	oldDigest, err := store.DirectoryContentRoot(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	flags, extra, err := parsePipelineFlags([]string{"--stages", "project-all,seal", "--from", "1", "--to", "3", "--restart"})
	if err != nil || len(extra) != 0 {
		t.Fatalf("parse restart: %v", err)
	}
	stop := errors.New("test stops at actual planner boundary without creating a model")
	previous := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = previous })
	dispatches := 0
	var nextGeneration string
	pipelineProjectedChapterPlanner = func(ctx context.Context, cfg bootstrap.Config, bundle assets.Bundle, workspace string, chapter int, contextDigest, protocol string, boundary agents.ProjectedArcBoundary, accounting ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		dispatches++
		if cfg.CharacterAgents.ExecutionPolicy != "v3" || protocol != domain.CharacterAgentDecisionProtocolV2Version || boundary.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV3 || chapter != 1 {
			t.Error("stage reached planner with wrong protocol/bounds")
		}
		cursor, err := st.ProjectedV2().LoadProjectionCursor()
		if err != nil || cursor == nil {
			t.Fatalf("new cursor: %v", err)
		}
		g, err := st.ProjectedV2().LoadBuildingGeneration(cursor.GenerationID)
		if err != nil || g == nil {
			t.Fatalf("new generation: %v", err)
		}
		nonce, err := loadPipelineProjectAllAttemptNonce(st.Dir())
		if err != nil || nonce == "" || nonce != g.AttemptID || g.GenerationID == old.Generation.GenerationID {
			t.Error("restart did not bind its actual new nonce")
		}
		lease, err := st.Runtime.LoadPipelineExecution()
		if err != nil || lease == nil || lease.Mode != domain.PipelineExecutionProjectAll {
			t.Error("planner dispatch occurred outside live execution lease")
		}
		if filepath.Base(filepath.Dir(filepath.Dir(workspace))) != g.GenerationID {
			t.Errorf("wrong isolated generation directory: %s", workspace)
		}
		nextGeneration = g.GenerationID
		return nil, stop
	}
	if err := pipelineProjectAllOnce(opts, flags); !errors.Is(err, stop) {
		t.Fatalf("restart failed before actual planner boundary: %v", err)
	}
	if dispatches != 1 || nextGeneration == "" {
		t.Fatal("new V3 stage was not dispatched exactly once")
	}
	got, err := store.DirectoryContentRoot(oldRoot)
	if err != nil || got != oldDigest {
		t.Fatal("restart changed old generation bytes")
	}
	usage, err := st.Usage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if usage != nil && (usage.Overall != (domain.AgentUsageTotals{}) || len(usage.PendingUsageCalls) != 0) {
		t.Fatal("fake stage created model usage")
	}
}

func TestPipelineV3RestartPreflightDoesNotRotateWithoutPermissionOrLease(t *testing.T) {
	opts, st, old := v3RestartEntryFixture(t)
	previous := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = previous })
	dispatches := 0
	pipelineProjectedChapterPlanner = func(context.Context, bootstrap.Config, assets.Bundle, string, int, string, string, agents.ProjectedArcBoundary, ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		dispatches++
		return nil, errors.New("must not dispatch")
	}
	if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3}); err == nil {
		t.Fatal("no-restart silently upgraded")
	}
	if nonce, err := loadPipelineProjectAllAttemptNonce(st.Dir()); err != nil || nonce != "" {
		t.Fatal("preflight wrote restart nonce")
	}
	owner := "fixture-foreign-live-owner"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 1, Owner: owner, ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(owner) })
	if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3, Restart: true}); err == nil {
		t.Fatal("restart ignored execution lock")
	}
	if nonce, err := loadPipelineProjectAllAttemptNonce(st.Dir()); err != nil || nonce != "" {
		t.Fatal("failed lock acquisition rotated nonce")
	}
	cursor, err := st.ProjectedV2().LoadProjectionCursor()
	if err != nil || cursor == nil || cursor.GenerationID != old.Generation.GenerationID {
		t.Fatal("failed preflight changed projection cursor")
	}
	if dispatches != 0 {
		t.Fatal("failed preflight reached model boundary")
	}
}
