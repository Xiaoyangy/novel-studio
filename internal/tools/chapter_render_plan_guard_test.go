package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func markPipelineManaged(t *testing.T, s *store.Store) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "pipeline.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentChapterRenderPlanRejectsStagedPartials(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧正式计划"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlanPartial(1, map[string]any{"structure": map[string]any{"chapter": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil || !strings.Contains(err.Error(), "plan.partial") {
		t.Fatalf("formal plan bypassed staged plan partial: %v", err)
	}
	if err := s.Drafts.DeleteChapterPlanPartial(1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{Chapter: 1, TimeWindow: "本章"}); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil || !strings.Contains(err.Error(), "simulation partial") {
		t.Fatalf("formal simulation/plan bypassed staged world partial: %v", err)
	}
}

func TestCurrentPlanBodyEpochRejectsOldDraftAfterNewPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	scope := domain.ChapterScope(1)
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第一版"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章\n\n旧计划产生的正文。"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第二版"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentPlanBodyEpoch(s, 1); err == nil || !strings.Contains(err.Error(), "旧 plan") {
		t.Fatalf("old draft became current by changing only the plan: %v", err)
	}
	if err := validateAppendBaseCurrentPlanEpoch(s, 1); err == nil || !strings.Contains(err.Error(), "mode=write") {
		t.Fatalf("tiny append was allowed to refresh an old-plan draft epoch: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章\n\n新计划之后重新渲染的正文。"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentPlanBodyEpoch(s, 1); err != nil {
		t.Fatalf("fresh post-plan draft should pass epoch gate: %v", err)
	}
}

func TestDraftPartsCannotCrossPlanEpoch(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	scope := domain.ChapterScope(1)
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第一版"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	_, part1, err := s.Drafts.SaveDraftPart(1, 1, 2, "旧片", "旧计划", "第一片旧正文。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "draft_part", part1.ContentPath); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第二版"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	index, part2, err := s.Drafts.SaveDraftPart(1, 2, 2, "新片", "新计划", "第二片新正文。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "draft_part", part2.ContentPath); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err == nil || !strings.Contains(err.Error(), "分片 1") {
		t.Fatalf("old part crossed into new plan epoch: %v", err)
	}
	index, part1, err = s.Drafts.SaveDraftPart(1, 1, 2, "新片", "新计划", "第一片也按新计划重写。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(scope, "draft_part", part1.ContentPath); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err != nil {
		t.Fatalf("all post-plan parts should pass epoch gate: %v", err)
	}
	index, part1, err = s.Drafts.SaveDraftPart(1, 1, 2, "新片", "新计划", "分片文件已写入，但模拟 checkpoint 追加失败。")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err == nil || !strings.Contains(err.Error(), "checkpoint 不匹配") {
		t.Fatalf("part mutation after checkpoint failure borrowed old evidence: %v", err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatestAcross(scope, "draft_part", part1.ContentPath, "plan", "rerender-request", "draft_part"); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err != nil {
		t.Fatalf("fresh exact part checkpoint should recover merge readiness: %v", err)
	}
}

func TestDraftPartCheckpointCannotCrossArtifactWithSameDigest(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, s)
	plan, err := decodeChapterPlanArgs(planArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	scope := domain.ChapterScope(1)
	if _, err := s.Checkpoints.AppendArtifactLatest(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	index, part1, err := s.Drafts.SaveDraftPart(1, 1, 2, "同文", "第一片", "两片故意使用完全相同的正文。")
	if err != nil {
		t.Fatal(err)
	}
	index, part2, err := s.Drafts.SaveDraftPart(1, 2, 2, "同文", "第二片", "两片故意使用完全相同的正文。")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatestAcross(scope, "draft_part", part1.ContentPath, "plan", "rerender-request", "draft_part"); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err == nil || !strings.Contains(err.Error(), "分片 2") {
		t.Fatalf("same digest under part 1 artifact incorrectly certified part 2: %v", err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatestAcross(scope, "draft_part", part2.ContentPath, "plan", "rerender-request", "draft_part"); err != nil {
		t.Fatal(err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err != nil {
		t.Fatalf("both exact same-digest part artifacts should pass: %v", err)
	}
}

func TestCurrentPlanBindingRejectsNewPlanAfterCheckpointAppendFailure(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, s)
	scope := domain.ChapterScope(1)
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "已 checkpoint 的旧计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(scope, "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "已写入但 checkpoint 失败的新计划"}); err != nil {
		t.Fatal(err)
	}
	index := &domain.ChapterDraftPartIndex{Chapter: 1, Parts: []domain.ChapterDraftPart{{Part: 1, ContentPath: "drafts/01.parts/part-01.md"}}}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil || !strings.Contains(err.Error(), "摘要不匹配") {
		t.Fatalf("render guard trusted an old plan checkpoint: %v", err)
	}
	if err := validateCurrentPlanBodyEpoch(s, 1); err == nil || !strings.Contains(err.Error(), "摘要不匹配") {
		t.Fatalf("body epoch trusted an old plan checkpoint: %v", err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err == nil || !strings.Contains(err.Error(), "摘要不匹配") {
		t.Fatalf("parts epoch trusted an old plan checkpoint: %v", err)
	}
}

func TestManagedCurrentPlanBindingRejectsMissingCheckpoint(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, s)
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "没有 checkpoint 的正式计划"}); err != nil {
		t.Fatal(err)
	}
	index := &domain.ChapterDraftPartIndex{Chapter: 1, Parts: []domain.ChapterDraftPart{{Part: 1, ContentPath: "drafts/01.parts/part-01.md"}}}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil || !strings.Contains(err.Error(), "没有 plan checkpoint") {
		t.Fatalf("managed render guard accepted a plan without checkpoint: %v", err)
	}
	if err := validateCurrentPlanBodyEpoch(s, 1); err == nil || !strings.Contains(err.Error(), "没有 plan checkpoint") {
		t.Fatalf("managed body epoch accepted a plan without checkpoint: %v", err)
	}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err == nil || !strings.Contains(err.Error(), "没有 plan checkpoint") {
		t.Fatalf("managed parts epoch accepted a plan without checkpoint: %v", err)
	}
}

func TestCurrentRenderPlanRejectsNewerSimulationCheckpointWithSameID(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, s)
	const simulationID = "same-semantic-simulation"
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧 POV 计划"}
	plan.CausalSimulation.WorldSimulationID = simulationID
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: simulationID, TimeWindow: "更新后的同一事实窗口",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil || !strings.Contains(err.Error(), "晚于当前正式 plan") {
		t.Fatalf("write-side guard accepted a plan older than the current simulation checkpoint: %v", err)
	}
	if err := ValidateReusableCausalPlanForRerender(s, 1); err == nil || !strings.Contains(err.Error(), "尚未消费当前 world simulation epoch") {
		t.Fatalf("render-only reuse bypassed the newer simulation checkpoint: %v", err)
	}
}

func TestCurrentRenderPlanRejectsWorldSimulationBytesChangedAfterCheckpoint(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	sim := domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: "frozen-sim", TimeWindow: "本章",
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ChosenDecision: "先停扩",
		},
	}
	if err := s.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}

	sim.ProtagonistProjection.ObservableEffects = []string{"checkpoint 后新增的可见结果"}
	if err := s.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentChapterPlanCausalCheckpoint(s, 1); err == nil ||
		!strings.Contains(err.Error(), "world simulation 与 checkpoint 不匹配") {
		t.Fatalf("plan freeze accepted mutated live simulation overlay: %v", err)
	}
}

func TestLegacyPlanWithoutCheckpointKeepsImportCompatibility(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("legacy", 2); err != nil {
		t.Fatal(err)
	}
	legacyPlan, err := decodeChapterPlanArgs(planArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(legacyPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err != nil {
		t.Fatalf("legacy plan with no checkpoint should retain write compatibility: %v", err)
	}
	if err := validateCurrentPlanBodyEpoch(s, 1); err != nil {
		t.Fatalf("legacy body epoch with no plan checkpoint should remain compatible: %v", err)
	}
	index := &domain.ChapterDraftPartIndex{Chapter: 1, Parts: []domain.ChapterDraftPart{{Part: 1, ContentPath: "drafts/01.parts/part-01.md"}}}
	if err := validateDraftPartsPlanEpoch(s, 1, index); err != nil {
		t.Fatalf("legacy parts epoch with no plan checkpoint should remain compatible: %v", err)
	}
}
