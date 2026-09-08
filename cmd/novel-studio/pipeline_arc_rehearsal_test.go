package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestArcRehearsalStageOrderAndRecovery(t *testing.T) {
	for _, raw := range []string{
		"architect,outline-all,zero-init,preplan,rehearse-arc,project-all,seal,promote,render",
		"preplan,rehearse-arc,project-all",
		"rehearse-arc",
		"project-all,seal",
	} {
		if _, err := resolveStages(raw); err != nil {
			t.Errorf("valid/resumed stage list %q: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"rehearse-arc,rehearse-arc",
		"project-all,rehearse-arc", "render,rehearse-arc",
		"rehearse-arc,architect", "rehearse-arc,outline-all",
		"rehearse-arc,zero-init", "rehearse-arc,preplan",
	} {
		if _, err := resolveStages(raw); err == nil {
			t.Errorf("out-of-order stage list %q was accepted", raw)
		}
	}
}

func TestArcRehearsalDoesNotStartLiveRetrieval(t *testing.T) {
	if pipelineStagesNeedQdrant([]string{"preplan", "rehearse-arc", "project-all"}) {
		t.Fatal("frozen arc rehearsal must not start live retrieval")
	}
}

func TestArchitectRefreshInvalidatesArcRehearsal(t *testing.T) {
	state := &domain.PipelineState{Stages: []string{"architect", "rehearse-arc", "project-all"}}
	for _, stage := range state.Stages {
		state.MarkDone(stage, domain.PipelineStageEvidence{Stage: stage, Status: "verified", CheckedAt: time.Now()})
	}
	if !invalidateExplicitArchitectRefresh(state, true) || slices.Contains(state.Completed, "rehearse-arc") {
		t.Fatal("foundation refresh retained a stale whole-arc rehearsal")
	}
}

func TestDetailWindowPreservesLegacyAbsenceButDoesNotIgnoreBrokenRehearsal(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	arc := pipelineArcScope{Volume: 1, Arc: 1, Title: "一弧", Goal: "真实选择", FirstChapter: 1, LastChapter: 9, BookLastChapter: 9}
	window, err := loadPipelineDetailWindow(st, arc, 0)
	if err != nil || window != nil {
		t.Fatalf("legacy absence should retain the original whole-arc contract: window=%+v err=%v", window, err)
	}
	if err := os.MkdirAll(filepath.Join(st.Dir(), store.ArcRehearsalRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPipelineDetailWindow(st, arc, 0); err == nil {
		t.Fatal("an incomplete rehearsal must not silently revert to legacy planning")
	}
}

func TestDetailWindowFirstBundleConsumesAcceptedPredecessor(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 9)
	generation.ProjectionScope = domain.PlanningProjectionScopeArcV2
	generation.ScopeID = domain.DeriveArcCycleID(1, 1, 1, 9)
	generation.BookHorizonChapter = 9
	generation.BaseCanonChapter, generation.FirstProjectedChapter, generation.LastProjectedChapter, generation.ExpectedChapterCount = 3, 4, 6, 3
	generation.ParentGenerationID = "pg2_previous_window"
	generation.DetailWindow = &domain.PlanningDetailWindowV1{
		Version: domain.PlanningDetailWindowVersionV1, ArcID: generation.ScopeID, ArcFirstChapter: 1, ArcLastChapter: 9,
		RehearsalDigest: projectAllCmdTestDigest("rehearsal"), RehearsalInputDigest: projectAllCmdTestDigest("input"), AcceptedOutcomeDigest: projectAllCmdTestDigest("accepted"),
		AcceptedPredecessor: &domain.ProjectedPlanningPredecessorContractV2{Chapter: 3, BundleDigest: projectAllCmdTestDigest("chapter3"), ProjectedPostStateRoot: generation.BaseStateRoot, OutgoingConsequenceID: "accepted-consequence-3", OutgoingConsequenceText: "已验收第三章留下的实际后果"},
	}
	registry.ProjectionScope, registry.ScopeID, registry.BookHorizonChapter, registry.FirstChapter, registry.LastChapter = generation.ProjectionScope, generation.ScopeID, 9, 4, 6
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 4)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 4)
	if _, _, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, registry); err != nil {
		t.Fatal(err)
	}
	artifacts.Plan.CausalSimulation.ArcTransition.IncomingConsequenceText = "模型自填的另一个后果"
	if _, _, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, registry); err == nil {
		t.Fatal("model-authored predecessor replaced the accepted chapter-three consequence")
	}
}
