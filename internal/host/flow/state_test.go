package flow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
)

func checkpointFlowChapterPlan(t *testing.T, s *store.Store, chapter int) {
	t.Helper()
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(chapter), "plan", fmt.Sprintf("drafts/%02d.plan.json", chapter),
	); err != nil {
		t.Fatal(err)
	}
}

func TestLoadStateRecoversDraftIntentIntoCallerCheckpointCache(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	candidate := "第一章 恢复测试\n\n林澈把湿伞靠在门边，先摸了摸口袋里的旧票据，才抬头看柜台后的那个人。"
	intent := map[string]any{
		"chapter":               1,
		"mode":                  "write",
		"artifact":              "drafts/01.draft.md",
		"candidate_body_sha256": reviewreport.BodySHA256(candidate),
		"causal_epoch_key":      "initial",
		"created_at":            "2026-07-15T00:00:00Z",
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "drafts", "01.draft_write_intent.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, candidate); err != nil {
		t.Fatal(err)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft"); cp != nil {
		t.Fatalf("fixture unexpectedly has draft checkpoint before recovery: %+v", cp)
	}

	_ = LoadState(st)
	recovered := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "draft")
	if recovered == nil || recovered.Digest != "sha256:"+reviewreport.BodySHA256(candidate) {
		t.Fatalf("LoadState recovery did not update caller checkpoint cache: %+v", recovered)
	}

	replacement := "第一章 恢复测试\n\n" + strings.Repeat("林澈换了张新票据，又沿着柜台逐项核对。", 20)
	args, err := json.Marshal(map[string]any{"chapter": 1, "content": replacement, "mode": "write"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toolspkg.NewDraftChapterTool(st).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "check_consistency") {
		t.Fatalf("recovered draft was overwritten as if its checkpoint were unseen: %v", err)
	}
	if got, err := st.Drafts.LoadDraft(1); err != nil || got != candidate {
		t.Fatalf("failed overwrite guard changed recovered draft: got=%q err=%v", got, err)
	}
}

func TestLoadStateMarksOnlyExactPipelineRenderTarget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          domain.PipelineExecutionMode
		targetChapter int
		want          bool
	}{
		{name: "exact render target", mode: domain.PipelineExecutionRender, targetChapter: 1, want: true},
		{name: "other render chapter", mode: domain.PipelineExecutionRender, targetChapter: 2, want: false},
		{name: "non-render pipeline mode", mode: domain.PipelineExecutionPreplan, targetChapter: 1, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			if err := st.Progress.Init("render-state", 3); err != nil {
				t.Fatal(err)
			}
			lock := domain.PipelineExecutionLock{
				Mode: tc.mode, TargetChapter: tc.targetChapter, Owner: "render-state-test",
			}
			if tc.mode == domain.PipelineExecutionRender {
				lock.PlanDigest = "sha256:frozen-plan"
			}
			if err := st.Runtime.AcquirePipelineExecution(lock); err != nil {
				t.Fatal(err)
			}
			state := LoadState(st)
			if state.NextActionPipelineRender != tc.want {
				t.Fatalf("NextActionPipelineRender=%v want=%v state=%+v", state.NextActionPipelineRender, tc.want, state)
			}
		})
	}
}

func TestLoadStateClosesDrafterRouteOnCombinedRenderConvergenceLedger(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "candidate")
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	sourceOutputDir := filepath.Join(root, "live", "novel")
	candidateID := "render-ch0001-flow-total"
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		if writeErr := os.WriteFile(path, raw, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	identity := map[string]any{
		"version": "pipeline-render-candidate.v2", "candidate_id": candidateID,
		"generation_id": "generation", "chapter": 1,
		"plan_digest": plan.Digest, "plan_checkpoint_seq": plan.Seq,
		"projected_bundle_digest":  "sha256:bundle",
		"promotion_receipt_digest": "sha256:promotion",
	}
	manifest := make(map[string]any, len(identity)+1)
	for key, value := range identity {
		manifest[key] = value
	}
	manifest["source_output_dir"] = sourceOutputDir
	ledger := make(map[string]any, len(identity)+3)
	for key, value := range identity {
		ledger[key] = value
	}
	ledger["version"] = "pipeline-render-convergence.v1"
	ledger["failure_limit"] = 3
	ledger["records"] = []map[string]any{
		{"body_sha256": strings.Repeat("1", 64), "semantic_reject": true},
		{"body_sha256": strings.Repeat("2", 64), "structural_block": true},
		{"body_sha256": strings.Repeat("3", 64), "structural_block": true},
	}
	writeJSON(filepath.Join(dir, "meta", "planning", "render_candidate.json"), manifest)
	writeJSON(filepath.Join(
		filepath.Dir(sourceOutputDir), ".render-candidates", "convergence", candidateID, "ledger.json",
	), ledger)

	state := LoadState(st)
	if !state.NextActionStructuralReplanRequired ||
		state.NextActionStructuralReplanAttempts != 3 ||
		state.NextActionStructuralReplanLimit != 3 || state.NextActionPlanReady {
		t.Fatalf("combined ledger left drafter route open: %+v", state)
	}
}

func TestChapterPlanReadyForDraftRejectsStaleAttractionPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 128); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{
		Structured:  rules.Structured{Genre: "都市脑洞轻松搞笑爽文"},
		Preferences: "长篇连载，热梗可少量点缀",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "失业饭桌"}); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 1)
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("stale plan without attraction contract must route back to planner")
	}
}

func TestCurrentExternalAIGCRepairWithEmptyAntiAIPlanRoutesBackToWriter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("anti-ai-route", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1000, "scene", "main"); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, "朱雀整篇单段 86%", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	body := "第一章\n\n主角把账本合上，没急着向任何人证明什么。"
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	brief := []byte("# ch01 rewrite brief\n\n- 当前 zhuque 整篇单段结果 86%，必须整章返工。\n")
	briefPath := filepath.Join(st.Dir(), "reviews", "01_rewrite_brief.md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, brief, 0o644); err != nil {
		t.Fatal(err)
	}
	bodySum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(brief)
	plan := domain.ChapterPlan{Chapter: 1, Title: "第一章"}
	plan.CausalSimulation.ContextSources = []string{
		fmt.Sprintf("rewrite_source:chapters/01.md#sha256=%x", bodySum),
		fmt.Sprintf("rewrite_brief:reviews/01_rewrite_brief.md#sha256=%x", briefSum),
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, st, 1)
	percent := 86.0
	row := reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 86, ScoreScale: "percent", ScorePercent: &percent, Verdict: "ai_like",
		BodySHA256: reviewreport.BodySHA256(body), CheckedAt: "2026-07-15T20:00:00+08:00",
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "external_detection_log.jsonl"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	state := LoadState(st)
	if state.NextActionPlanReady {
		t.Fatal("current external AIGC repair accepted a formal plan with empty anti_ai_execution_plan")
	}
	instruction := Route(state)
	if instruction == nil || instruction.Agent != "writer" {
		t.Fatalf("invalid formal plan did not route back to Writer: %+v", instruction)
	}
}

func TestChapterPlanReadyForDraftUsesWebBriefAttractionRequirements(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	brief := "# 项目文风\n\n轻松搞笑爽文；系统会接话并始终支持主角。\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	plan.CausalSimulation.EntertainmentPlan.CompanionVoiceBeat = "系统用短促吐槽接话并支持主角。"
	plan.CausalSimulation.EntertainmentPlan.ForbiddenComedy = []string{"不连续抛梗"}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 2)
	if chapterPlanReadyForDraft(s, 2, false) {
		t.Fatal("Flow must not dispatch a plan that commit will reject under web-reference attraction rules")
	}
}

func TestChapterPlanReadyForDraftRejectsLegacyQuantityConflict(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter: 3, CoreEvent: "已有摊主主动加入，试点由五家扩到十家。",
	}}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 3, Title: "五十单到了", Contract: domain.ChapterContract{
		RequiredBeats: []string{"试点扩到十家"},
	}}
	plan.CausalSimulation.ProtagonistDecision = "维持五摊上限，拒绝第六摊。"
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 3)
	if chapterPlanReadyForDraft(s, 3, false) {
		t.Fatal("legacy ten-target/five-cap plan must route back through world simulation and planning")
	}
}

func TestChapterPlanReadyForDraftRejectsInvalidCraftReceipt(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	plan.CausalSimulation.ContextSources = []string{"craft_recall_receipt:0123456789abcdef01234567"}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 2)
	if chapterPlanReadyForDraft(s, 2, true) {
		t.Fatal("router must send a rewrite plan with a missing/stale craft receipt back to Writer")
	}
}

func TestChapterPlanReadyForDraftRejectsStagedPlanOrWorldPartial(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧正式计划"}); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 1)
	if err := s.Drafts.SaveChapterPlanPartial(1, map[string]any{"structure": map[string]any{"chapter": 1}}); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("Flow accepted a formal plan while a staged plan partial was active")
	}
	if err := s.Drafts.DeleteChapterPlanPartial(1); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{Chapter: 1, TimeWindow: "本章"}); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("Flow accepted a formal plan while a staged world partial was active")
	}
}

func TestChapterPlanReadyIgnoresStaleFormalSimulationWhenNoLongerRequired(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: "old-cast-simulation", TimeWindow: "旧 cast 时段",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "当前无需全角色推演"}); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 1)
	if !chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("stale optional formal simulation should not make an otherwise valid plan unroutable")
	}
}

func TestChapterPlanReadyRejectsNewerSimulationCheckpointWithSameID(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	const simulationID = "same-semantic-simulation"
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧 POV 计划"}
	plan.CausalSimulation.WorldSimulationID = simulationID
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 1)
	if !chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("checkpoint-bound plan should be ready before a newer simulation epoch")
	}
	if err := s.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: simulationID, TimeWindow: "重新推演后的同一事实窗口",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("a matching SimulationID must not let an older POV plan cross a newer simulation checkpoint")
	}
	if _, err := s.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		t.Fatal(err)
	}
	if !chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("an identical plan finalized after the newer simulation checkpoint should recover routing readiness")
	}
}

func TestRenderOnlyReviewReusesCurrentCausalPlan(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("render-only-review-route", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 1000, "scene", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewritesAndFlow([]int{1}, "正式复审仅要求表达层重写", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
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
	body := "第一章正文。\n\n【额度和限制与任务全挤在这里。】"
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
	checkpointFlowChapterPlan(t, s, 1)
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
	if err := s.Drafts.SaveRewriteBrief(1, "# rewrite brief\n\n- AI味与主角内在判断偏薄，只重渲染表达。\n"); err != nil {
		t.Fatal(err)
	}
	writeGate := func(rule string) {
		t.Helper()
		payload := reviewreport.MechanicalGatePayload{
			Chapter: 1, BodySHA256: bodyHash,
			RuleViolations: []rules.Violation{{Rule: rule, Severity: rules.SeverityError}},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(s.Dir(), "reviews", "01_ai_gate.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeGate("system_message_overpacked")
	if !toolspkg.RenderOnlyReviewAllowsPlanReuse(s, 1) || !chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("render-only system dialogue fix should reuse the current causal plan")
	}
	writeGate("semicolon_overuse")
	if !toolspkg.RenderOnlyReviewAllowsPlanReuse(s, 1) || !chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("render-only punctuation fix should reuse the current causal plan")
	}
	writeGate("pov_interiority_thin")
	if !toolspkg.RenderOnlyReviewAllowsPlanReuse(s, 1) || !chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("render-only interiority/presentation fix should reuse the current causal plan")
	}
	worldRequired, worldReady, worldGaps := toolspkg.ChapterWorldSimulationStatus(s, 1)
	if !worldRequired || worldReady || !slices.ContainsFunc(worldGaps, func(gap string) bool {
		return strings.Contains(gap, "rewrite_source does not match")
	}) {
		t.Fatalf("fixture must reproduce the post-review rewrite_source version gap: required=%v ready=%v gaps=%v", worldRequired, worldReady, worldGaps)
	}
	state := LoadState(s)
	if !state.NextActionPlanReady || !state.NextActionReviewRerenderRequired {
		t.Fatalf("expression-only review must retain the sealed plan and require a fresh draft: %+v", state)
	}
	instruction := Route(state)
	if instruction == nil || instruction.Agent != "drafter" || instruction.Chapter != 1 {
		t.Fatalf("expression-only review with a stale rewrite_source version must route directly to Drafter: %+v", instruction)
	}
	review.Dimensions[1].Verdict = "warning"
	if err := s.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if toolspkg.RenderOnlyReviewAllowsPlanReuse(s, 1) || chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("character-dimension failures must not use the expression-only plan reuse path")
	}
	review.Dimensions[1].Verdict = "pass"
	if err := s.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	writeGate("pending_resource_as_fact")
	if toolspkg.RenderOnlyReviewAllowsPlanReuse(s, 1) || chapterPlanReadyForDraft(s, 1, true) {
		t.Fatal("resource/fact failures must return to causal replanning")
	}
}

func TestChapterPlanReadyForDraftRejectsStaleRewriteBrief(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第二章正文。"
	if err := s.Drafts.SaveFinalChapter(2, body); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(s.Dir(), "reviews", "02_rewrite_brief.md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := []byte("# rewrite\n\n旧要求")
	if err := os.WriteFile(briefPath, brief, 0o644); err != nil {
		t.Fatal(err)
	}
	bodySum := sha256.Sum256([]byte(body))
	briefSum := sha256.Sum256(brief)
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章", CausalSimulation: domain.ChapterCausalSimulation{
		ContextSources: []string{
			fmt.Sprintf("rewrite_source:chapters/02.md#sha256=%x", bodySum),
			fmt.Sprintf("rewrite_brief:reviews/02_rewrite_brief.md#sha256=%x", briefSum),
		},
	}}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 2)
	if !chapterPlanReadyForDraft(s, 2, true) {
		t.Fatal("plan bound to current body and brief should be ready")
	}
	if err := os.WriteFile(briefPath, []byte("# rewrite\n\n用户新增要求"), 0o644); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 2, true) {
		t.Fatal("changed rewrite brief must invalidate the old plan")
	}
}

func TestChapterPlanReadinessRejectsPlanWrittenAfterLatestCheckpoint(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧计划"}); err != nil {
		t.Fatal(err)
	}
	checkpointFlowChapterPlan(t, s, 1)
	if !chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("checkpoint-bound old plan should be ready")
	}
	if err := s.Drafts.SaveDraft(1, "第一章\n\n旧计划生成的正文。"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if !chapterDraftReadyForFinalize(s, 1) {
		t.Fatal("draft after the checkpoint-bound plan should be ready")
	}

	// Simulate SaveChapterPlan succeeding while checkpoint append fails. The
	// old checkpoint still exists but must not certify these new bytes.
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "新计划尚未 checkpoint"}); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("new plan bytes inherited an old plan checkpoint")
	}
	if chapterDraftReadyForFinalize(s, 1) {
		t.Fatal("body epoch used an old checkpoint after the formal plan changed")
	}
}

func TestChapterPlanReadinessRejectsMissingOrWrongArtifactCheckpoint(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	// Checkpoint journaling is mandatory for pipeline-managed projects. Legacy
	// imports without meta/pipeline.json keep the compatibility path exercised
	// by the write-side guard tests.
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "pipeline.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "当前计划"}); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("formal plan without a checkpoint was accepted")
	}
	current, err := os.ReadFile(filepath.Join(s.Dir(), "drafts", "01.plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	wrongArtifact := filepath.Join(s.Dir(), "drafts", "01.plan.backup.json")
	if err := os.WriteFile(wrongArtifact, current, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.backup.json"); err != nil {
		t.Fatal(err)
	}
	if chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("same digest under the wrong artifact path was accepted")
	}

	// Plan's latest-only checkpoint API can repair a wrong-path checkpoint even
	// when both files happen to have the same digest.
	checkpointFlowChapterPlan(t, s, 1)
	if !chapterPlanReadyForDraft(s, 1, false) {
		t.Fatal("canonical artifact+digest checkpoint should make the plan ready")
	}
}

func TestChapterDraftReadyForFinalizeHonorsExplicitRerenderRequest(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 2, Title: "第二章"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "plan", "drafts/02.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n旧草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if !chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("draft newer than plan should be ready")
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2,"reason":"human readability"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	if chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("explicit rerender request must make the old draft stale")
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n新草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(2), "draft", "drafts/02.draft.md", "plan", "rerender-request", "draft", "edit"); err != nil {
		t.Fatal(err)
	}
	if !chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("new draft after rerender request should be ready")
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n文件已改写但 checkpoint 模拟失败"); err != nil {
		t.Fatal(err)
	}
	if chapterDraftReadyForFinalize(s, 2) {
		t.Fatal("Host finalized draft bytes that were not bound to the latest body checkpoint")
	}
}
