package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
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

func TestExpressionOnlyReviewClassifierDoesNotAuthorizeUnsealedPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("expression-only-render-guard", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 1000, "scene", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewritesAndFlow([]int{1}, "正式复审仅要求表达层重写", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	markPipelineManaged(t, s)
	if err := s.WorldSim.SaveSimulationCast(domain.SimulationCast{Assignments: []domain.TierAssignment{
		{Name: "林澈", Tier: domain.TierProtagonistCircle},
	}}); err != nil {
		t.Fatal(err)
	}
	const simulationID = "sealed-before-formal-review"
	if err := s.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: simulationID, TimeWindow: "正式复审前已经冻结的时段",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	body := "第一章正文。\n\n【额度、限制与任务全挤在同一句里。】"
	if err := s.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "第一章"}
	plan.CausalSimulation.WorldSimulationID = simulationID
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	bodyHash := reviewreport.BodySHA256(body)
	review := domain.ReviewEntry{
		Chapter: 1, BodySHA256: bodyHash, Scope: "chapter", ContractStatus: "met", Verdict: "rewrite",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Verdict: "pass"},
			{Dimension: "character", Verdict: "pass"},
			{Dimension: "pacing", Verdict: "pass"},
			{Dimension: "continuity", Verdict: "pass"},
			{Dimension: "foreshadow", Verdict: "pass"},
			{Dimension: "hook", Verdict: "pass"},
		},
	}
	if err := s.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveRewriteBrief(1, "# rewrite brief\n\n- 降低 AI 味并增加主角内在判断，只重渲染表达。\n"); err != nil {
		t.Fatal(err)
	}
	gate := reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: bodyHash,
		RuleViolations: []rules.Violation{
			{Rule: "pov_interiority_thin", Severity: rules.SeverityError},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning},
		},
	}
	raw, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "reviews", "01_ai_gate.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	worldRequired, worldReady, gaps := ChapterWorldSimulationStatus(s, 1)
	if !worldRequired || worldReady || !strings.Contains(strings.Join(gaps, "；"), "rewrite_source does not match") {
		t.Fatalf("fixture must have only a post-freeze rewrite-source version blocker: required=%v ready=%v gaps=%v", worldRequired, worldReady, gaps)
	}
	if !RenderOnlyReviewAllowsPlanReuse(s, 1) {
		t.Fatal("strict expression-only semantic classifier rejected its positive fixture")
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil {
		t.Fatal("an unsealed plan must not turn semantic classification into a prose-write capability")
	}

	review.Dimensions[1].Verdict = "warning"
	if err := s.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCurrentChapterRenderPlan(s, 1); err == nil {
		t.Fatal("character-dimension failure must not bypass simulation and rewrite-craft validation")
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
